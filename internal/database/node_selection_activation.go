package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// NodeSelectionActivation protects the one-time change from legacy execution
// assignments to saved preferences. It is not a general downgrade mechanism.
type NodeSelectionActivation struct {
	db      *sql.DB
	binding nodeSelectionActivationBinding
	ready   chan struct{}
	once    sync.Once
	healthy atomic.Bool
}

const nodeSelectionActivationJobFields = "id,target_id,target_service_type,operation,target_version,status,COALESCE(agent_service_id,''),execution_host_id,transport_mode,current_version,artifact_digest,previous_digest,ownership_epoch,completed_at"

func scanNodeSelectionActivationJob(row interface{ Scan(...any) error }) (nodeSelectionActivationJob, error) {
	var j nodeSelectionActivationJob
	err := row.Scan(&j.ID, &j.TargetID, &j.TargetType, &j.Operation, &j.TargetVersion, &j.Status, &j.AgentID, &j.HostID, &j.Transport, &j.CurrentVersion, &j.ArtifactDigest, &j.PreviousDigest, &j.Epoch, &j.CompletedAt)
	return j, err
}

func (a *NodeSelectionActivation) Ready() bool {
	select {
	case <-a.ready:
		return true
	default:
		return false
	}
}
func (a *NodeSelectionActivation) ReadySignal() <-chan struct{} { return a.ready }
func (a *NodeSelectionActivation) ProbeReady() bool             { return a.Ready() || a.healthy.Load() }
func (a *NodeSelectionActivation) UpdateJobID() string          { return a.binding.JobID }
func (a *NodeSelectionActivation) UpdateAgentID() string        { return a.binding.AgentID }
func (a *NodeSelectionActivation) markReady()                   { a.once.Do(func() { close(a.ready) }) }

// PrepareNodeSelectionActivation completes all fallible selection preparation
// before the application identity probe becomes available. Until activation,
// main exposes only the bounded update/recovery control plane and no workers.
func PrepareNodeSelectionActivation(ctx context.Context, db *sql.DB, runningVersion, buildCommit string) (*NodeSelectionActivation, error) {
	a := &NodeSelectionActivation{db: db, ready: make(chan struct{})}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var activated sql.NullTime
	var saved nodeSelectionActivationBinding
	err = tx.QueryRowContext(ctx, "SELECT activated_at,COALESCE(update_job_id,''),target_version,build_commit,agent_service_id,execution_host_id,ownership_epoch,current_version FROM stream_node_selection_activation WHERE singleton_id=1 FOR UPDATE").Scan(&activated, &saved.JobID, &saved.TargetVersion, &saved.BuildCommit, &saved.AgentID, &saved.HostID, &saved.Epoch, &saved.CurrentVersion)
	if err != nil {
		return nil, err
	}
	if activated.Valid {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		a.markReady()
		return a, nil
	}
	active, activeErr := scanNodeSelectionActivationJob(tx.QueryRowContext(ctx, "SELECT "+nodeSelectionActivationJobFields+" FROM system_update_jobs WHERE active_target_id='control-panel' AND status<>'queued'"))
	if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
		return nil, activeErr
	}
	hasActive := activeErr == nil
	var previous nodeSelectionActivationJob
	if saved.JobID != "" {
		previous, err = scanNodeSelectionActivationJob(tx.QueryRowContext(ctx, "SELECT "+nodeSelectionActivationJobFields+" FROM system_update_jobs WHERE id=?", saved.JobID))
		if err != nil || !nodeSelectionActivationJobMatches(previous, saved) {
			return nil, errors.New("saved Node activation update authority is unavailable")
		}
	}
	if hasActive {
		if !nodeSelectionActivationInstallingJob(active, runningVersion) {
			return nil, errors.New("Node activation requires the exact installing Control Panel update")
		}
		if saved.JobID != "" && saved.JobID != active.ID {
			// A replacement is allowed only after the original updater proved that it
			// restored the previous release. An absent/failed job is not that proof.
			if previous.Status != "rolled_back" || !nodeSelectionActivationTerminalProof(previous) || previous.CurrentVersion != active.CurrentVersion {
				return nil, errors.New("previous Node activation update has no verified rollback")
			}
		} else if saved.JobID != "" && (saved.TargetVersion != runningVersion || saved.BuildCommit != buildCommit) {
			return nil, errors.New("Node activation candidate identity changed")
		}
		a.binding = nodeSelectionActivationBinding{JobID: active.ID, TargetVersion: runningVersion, BuildCommit: buildCommit, AgentID: active.AgentID, HostID: active.HostID, Epoch: active.Epoch, CurrentVersion: active.CurrentVersion}
	} else if saved.JobID != "" {
		if saved.TargetVersion != runningVersion || saved.BuildCommit != buildCommit {
			return nil, errors.New("Node activation candidate identity changed")
		}
		a.binding = saved
		if previous.Status != "succeeded" {
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			return a, nil // keep failed/unknown/rolled-back candidates closed
		}
		if !nodeSelectionActivationTerminalProof(previous) {
			return nil, errors.New("Node activation terminal proof is incomplete")
		}
	}
	// The unpublished transition has not accepted any application writes. Rebuild
	// from the current legacy authority, including changes made after rollback.
	// All changes here are transactional and confined to the new preference table.
	if err = rebuildUnactivatedNodeSelections(ctx, tx); err != nil {
		return nil, err
	}
	if hasActive {
		_, err = tx.ExecContext(ctx, "UPDATE stream_node_selection_activation SET update_job_id=?,target_version=?,build_commit=?,agent_service_id=?,execution_host_id=?,ownership_epoch=?,current_version=? WHERE singleton_id=1", a.binding.JobID, a.binding.TargetVersion, a.binding.BuildCommit, a.binding.AgentID, a.binding.HostID, a.binding.Epoch, a.binding.CurrentVersion)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE stream_node_selection_activation SET activated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1")
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if hasActive {
		a.healthy.Store(true)
	} else {
		a.markReady()
	}
	return a, nil
}

func rebuildUnactivatedNodeSelections(ctx context.Context, tx *sql.Tx) error {
	body, err := embeddedMigrations.ReadFile("migrations/090_stream_node_selections.sql")
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM stream_node_selections"); err != nil {
		return err
	}
	for _, stmt := range splitSQLStatements(string(body)) {
		// CREATE TABLE is intentionally excluded: DDL would implicitly commit.
		upper := strings.ToUpper(stmt)
		insert := strings.Index(upper, "INSERT INTO STREAM_NODE_SELECTIONS")
		if insert < 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, stmt[insert:]); err != nil {
			return fmt.Errorf("prepare saved Node choices: %w", err)
		}
	}
	return nil
}

// Run watches the same durable job, never a latest-job projection or a client
// report body. Transient uncertainty also makes probes fail closed.
func (a *NodeSelectionActivation) Run(ctx context.Context) {
	if a.Ready() || a.binding.JobID == "" {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ready, healthy, err := a.checkCompletion(checkCtx)
		cancel()
		a.healthy.Store(err == nil && healthy)
		if err == nil && ready {
			a.markReady()
			return
		}
	}
}

func (a *NodeSelectionActivation) checkCompletion(ctx context.Context) (bool, bool, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	var jobID, commit string
	var activated sql.NullTime
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(update_job_id,''),build_commit,activated_at FROM stream_node_selection_activation WHERE singleton_id=1 FOR UPDATE").Scan(&jobID, &commit, &activated); err != nil {
		return false, false, err
	}
	if jobID != a.binding.JobID || commit != a.binding.BuildCommit {
		return false, false, errors.New("Node activation binding changed")
	}
	job, err := scanNodeSelectionActivationJob(tx.QueryRowContext(ctx, "SELECT "+nodeSelectionActivationJobFields+" FROM system_update_jobs WHERE id=?", a.binding.JobID))
	if err != nil {
		return false, false, err
	}
	if !nodeSelectionActivationJobMatches(job, a.binding) {
		return false, false, errors.New("Node activation update authority changed")
	}
	if job.Status != "succeeded" {
		return false, nodeSelectionActivationInstallingJob(job, a.binding.TargetVersion), nil
	}
	if !nodeSelectionActivationTerminalProof(job) {
		return false, false, errors.New("Node activation terminal proof is incomplete")
	}
	if !activated.Valid {
		if _, err = tx.ExecContext(ctx, "UPDATE stream_node_selection_activation SET activated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1"); err != nil {
			return false, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, false, err
	}
	return true, true, nil
}
