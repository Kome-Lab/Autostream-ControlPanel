package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

// This seam projects the exact current whole-host snapshot. Active transactions
// verify the stored B prestate and pending desire; accepted proofs survive cursor
// removal. Matching accepted and active candidates must identify the same state.
type SystemUpdatePortProjectionStore interface {
	GetSystemUpdatePortPolicyProjection(context.Context, string, UpdaterPolicy) (SystemUpdatePortPolicySnapshot, error)
}

func (s *MemorySystemUpdateStore) GetSystemUpdatePortPolicyProjection(ctx context.Context, targetID string, policy UpdaterPolicy) (SystemUpdatePortPolicySnapshot, error) {
	unlock := s.lockPortPolicyOrder()
	defer unlock()
	if err := ctx.Err(); err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	if s.portPolicyStore == nil {
		return SystemUpdatePortPolicySnapshot{}, ErrNotFound
	}
	jobs := []SystemUpdateJob{}
	for _, job := range s.jobs {
		if job.ExecutionHostID == policy.ExecutionHostID && isSystemUpdatePortV2(job) {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return SystemUpdatePortPolicySnapshot{}, ErrNotFound
	}
	current, ok := s.portPolicyStore.policies[policy.UpdaterID]
	if !ok || !sameSystemUpdatePortSourcePolicy(current, policy) {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	host := s.executionHosts[policy.ExecutionHostID]
	if !systemUpdatePortProjectionHostMatches(current, host) {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	var selected *SystemUpdatePortPolicySnapshot
	active := false
	for _, job := range jobs {
		snapshot, candidate, err := activeSystemUpdatePortProjection(job, current, host)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		if !candidate {
			continue
		}
		if active {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		registry := s.portJobRegistries[job.ID]
		if registry == nil {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		registry.mu.Lock()
		err = validateMemorySystemUpdatePortV2StateLocked(s, registry, job)
		registry.mu.Unlock()
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		if err = mergeSystemUpdatePortProjection(&selected, snapshot, targetID); err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		active = true
	}
	justifiedK, canonicalK := false, false
	for _, job := range jobs {
		snapshot, candidate, err := cancelledSystemUpdatePortProjection(job, current, host)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		if !candidate {
			continue
		}
		canonicalK = true
		if active {
			if selected != nil && selected.Ref.SnapshotID == snapshot.Ref.SnapshotID {
				justifiedK = true
			}
			continue
		}
		registry := s.portJobRegistries[job.ID]
		if registry == nil {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		registry.mu.Lock()
		err = validateSystemUpdatePortProjectionServices(snapshot, registry.services)
		registry.mu.Unlock()
		if err != nil {
			continue
		}
		if err = mergeSystemUpdatePortProjection(&selected, snapshot, targetID); err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		justifiedK = true
	}
	for _, job := range jobs {
		snapshot, candidate, err := acceptedSystemUpdatePortProjection(job, current, host)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		if !candidate {
			continue
		}
		if !active {
			registry := s.portJobRegistries[job.ID]
			if registry == nil {
				return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
			}
			registry.mu.Lock()
			err = validateSystemUpdatePortProjectionServices(snapshot, registry.services)
			registry.mu.Unlock()
			if err != nil {
				if justifiedK && selected != nil && systemUpdatePortKCanFollow(snapshot, *selected) {
					continue
				}
				return SystemUpdatePortPolicySnapshot{}, err
			}
		}
		if active && justifiedK && selected != nil && selected.Ref.SnapshotID != snapshot.Ref.SnapshotID && systemUpdatePortKCanFollow(snapshot, *selected) {
			continue
		}
		if err = mergeSystemUpdatePortProjection(&selected, snapshot, targetID); err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
	}
	if selected == nil && canonicalK {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if selected == nil {
		return SystemUpdatePortPolicySnapshot{}, ErrNotFound
	}
	return *selected, nil
}

func (s *MariaDBSystemUpdateStore) GetSystemUpdatePortPolicyProjection(ctx context.Context, targetID string, policy UpdaterPolicy) (SystemUpdatePortPolicySnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, systemUpdateSelect+` WHERE execution_host_id=? AND port_contract_version=2 ORDER BY id`, policy.ExecutionHostID)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	jobs := []SystemUpdateJob{}
	for rows.Next() {
		job, err := scanSystemUpdateJob(rows)
		if err != nil {
			rows.Close()
			return SystemUpdatePortPolicySnapshot{}, err
		}
		jobs = append(jobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	if len(jobs) == 0 {
		return SystemUpdatePortPolicySnapshot{}, ErrNotFound
	}
	current, err := loadMariaDBSystemUpdatePortPolicy(ctx, tx, policy.UpdaterID, false)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	if !sameSystemUpdatePortSourcePolicy(current, policy) {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	host, err := scanSystemUpdateExecutionHost(tx.QueryRowContext(ctx, systemUpdateExecutionHostSelect+` WHERE execution_host_id=?`, policy.ExecutionHostID))
	if err != nil || !systemUpdatePortProjectionHostMatches(current, host) {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	services, err := loadMariaDBPortServices(ctx, tx, current, false, nil)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	var selected *SystemUpdatePortPolicySnapshot
	active := false
	for _, job := range jobs {
		snapshot, candidate, err := activeSystemUpdatePortProjection(job, current, host)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		if !candidate {
			continue
		}
		if active {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		reservations := map[servicePortReservationKey]ServicePortReservation{}
		rows, err := tx.QueryContext(ctx, servicePortReservationSelect+` WHERE execution_host_id=? ORDER BY port`, host.ExecutionHostID)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		for rows.Next() {
			reservation, err := scanServicePortReservation(rows)
			if err != nil {
				rows.Close()
				return SystemUpdatePortPolicySnapshot{}, err
			}
			reservations[servicePortKey(reservation)] = reservation
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		validator := &MemorySystemUpdateStore{portPolicyStore: &MemoryUpdaterPolicyStore{policies: map[string]UpdaterPolicy{current.UpdaterID: current}}, executionHosts: map[string]SystemUpdateExecutionHost{host.ExecutionHostID: host}, portReservations: reservations}
		if err = validateMemorySystemUpdatePortV2StateLocked(validator, &MemoryAuthStore{services: services}, job); err != nil {
			return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		if err = mergeSystemUpdatePortProjection(&selected, snapshot, targetID); err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		active = true
	}
	justifiedK, canonicalK := false, false
	for _, job := range jobs {
		snapshot, candidate, err := cancelledSystemUpdatePortProjection(job, current, host)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		if !candidate {
			continue
		}
		canonicalK = true
		if active {
			if selected != nil && selected.Ref.SnapshotID == snapshot.Ref.SnapshotID {
				justifiedK = true
			}
			continue
		}
		if err = validateSystemUpdatePortProjectionServices(snapshot, services); err != nil {
			continue
		}
		if err = mergeSystemUpdatePortProjection(&selected, snapshot, targetID); err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		justifiedK = true
	}
	for _, job := range jobs {
		snapshot, candidate, err := acceptedSystemUpdatePortProjection(job, current, host)
		if err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
		if !candidate {
			continue
		}
		if !active {
			if err = validateSystemUpdatePortProjectionServices(snapshot, services); err != nil {
				if justifiedK && selected != nil && systemUpdatePortKCanFollow(snapshot, *selected) {
					continue
				}
				return SystemUpdatePortPolicySnapshot{}, err
			}
		}
		if active && justifiedK && selected != nil && selected.Ref.SnapshotID != snapshot.Ref.SnapshotID && systemUpdatePortKCanFollow(snapshot, *selected) {
			continue
		}
		if err = mergeSystemUpdatePortProjection(&selected, snapshot, targetID); err != nil {
			return SystemUpdatePortPolicySnapshot{}, err
		}
	}
	if selected == nil && canonicalK {
		return SystemUpdatePortPolicySnapshot{}, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if selected == nil {
		return SystemUpdatePortPolicySnapshot{}, ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return SystemUpdatePortPolicySnapshot{}, err
	}
	return *selected, nil
}

func sameSystemUpdatePortSourcePolicy(left, right UpdaterPolicy) bool {
	left.UpdatedAt = time.Time{}
	right.UpdatedAt = time.Time{}
	return reflect.DeepEqual(left, right)
}
func systemUpdatePortProjectionHostMatches(policy UpdaterPolicy, host SystemUpdateExecutionHost) bool {
	return host.TransportMode == SystemUpdateTransportPullV2 && host.ExecutionHostID == policy.ExecutionHostID && host.AgentServiceID == policy.UpdaterID && host.OwnershipEpoch > 0 && host.PolicyRevision == policy.ProjectionRevision
}
func systemUpdatePortProjectionRevisionsMatch(ref contracts.SystemUpdatePortSnapshotRef, policy UpdaterPolicy) bool {
	return ref.SourcePolicyRevision == policy.Revision && ref.ProjectionRevision == policy.ProjectionRevision && ref.ExecutorPolicyRevision == policy.LocalExecutorPolicyRevision && ref.ExecutorPolicySHA256 == policy.LocalExecutorPolicySHA256
}

func activeSystemUpdatePortProjection(job SystemUpdateJob, policy UpdaterPolicy, host SystemUpdateExecutionHost) (SystemUpdatePortPolicySnapshot, bool, error) {
	if !isSystemUpdatePortV2(job) || !systemUpdateJobHoldsHost(job) {
		return SystemUpdatePortPolicySnapshot{}, false, nil
	}
	tx := job.portTransaction
	if tx == nil || tx.AcceptedResult != nil {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	snapshot := tx.Before
	frozen := job.PortReconfigure.Before
	switch tx.Phase {
	case "created":
	case "consumed", "rollback_latched":
		snapshot = tx.Target
		frozen = job.PortReconfigure.Target
	default:
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if !systemUpdatePortProjectionRevisionsMatch(snapshot.Ref, policy) || !validSystemUpdatePortProjectionSnapshot(job, policy, host, snapshot, frozen) {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	return snapshot, true, nil
}

func acceptedSystemUpdatePortProjection(job SystemUpdateJob, policy UpdaterPolicy, host SystemUpdateExecutionHost) (SystemUpdatePortPolicySnapshot, bool, error) {
	tx := job.portTransaction
	if tx == nil || tx.Phase != "accepted" {
		return SystemUpdatePortPolicySnapshot{}, false, nil
	}
	if !isSystemUpdatePortV2(job) || tx.AcceptedResult == nil || tx.RecoveryRequired || !contracts.IsAcceptedSystemUpdatePortResult(*tx.AcceptedResult) {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	snapshot := tx.Target
	frozen := job.PortReconfigure.Target
	switch tx.AcceptedResult.Result {
	case contracts.SystemUpdatePortReconfigurationRolledBack:
		snapshot = tx.Rollback
		frozen = job.PortReconfigure.Rollback
	case contracts.SystemUpdatePortReconfigurationUnchanged:
		snapshot = tx.Before
		frozen = job.PortReconfigure.Before
	}
	if !systemUpdatePortProjectionRevisionsMatch(snapshot.Ref, policy) {
		return SystemUpdatePortPolicySnapshot{}, false, nil
	}
	if !validSystemUpdatePortProjectionSnapshot(job, policy, host, snapshot, frozen) || contracts.ValidateSystemUpdatePortResult(systemUpdatePortContractsPlan(job.PortReconfigure), *tx.AcceptedResult) != nil {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if tx.AcceptedResult.Result == contracts.SystemUpdatePortReconfigurationRolledBack && job.Status != SystemUpdateStatusRolledBack || tx.AcceptedResult.Result != contracts.SystemUpdatePortReconfigurationRolledBack && job.Status != SystemUpdateStatusSucceeded {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	return snapshot, true, nil
}

func cancelledSystemUpdatePortProjection(job SystemUpdateJob, policy UpdaterPolicy, host SystemUpdateExecutionHost) (SystemUpdatePortPolicySnapshot, bool, error) {
	tx := job.portTransaction
	if tx == nil || (tx.Phase != "canceled" && tx.Phase != "premutation_failed") {
		return SystemUpdatePortPolicySnapshot{}, false, nil
	}
	if !systemUpdatePortProjectionRevisionsMatch(tx.Before.Ref, policy) {
		return SystemUpdatePortPolicySnapshot{}, false, nil
	}
	if tx.AcceptedResult != nil || tx.RecoveryRequired || tx.Phase == "canceled" && job.Status != SystemUpdateStatusCancelled || tx.Phase == "premutation_failed" && job.Status != SystemUpdateStatusFailed || !validSystemUpdatePortProjectionSnapshot(job, policy, host, tx.Before, job.PortReconfigure.Before) {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	expected := tx.Before.Ref.EndpointRevision
	if tx.Target.Ref.EndpointRevision != expected {
		expected += 2
	}
	if tx.CancelEndpointRevision != expected {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	body, _ := json.Marshal(tx.Before.Snapshot)
	var snapshot contracts.SystemUpdatePortPolicySnapshot
	if json.Unmarshal(body, &snapshot) != nil {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	for i := range snapshot.Targets {
		if snapshot.Targets[i].ServiceID == job.TargetID {
			snapshot.Targets[i].EndpointRevision = expected
		}
	}
	result, err := systemUpdatePortSnapshotWithRef(snapshot, job.TargetID)
	if err != nil {
		return SystemUpdatePortPolicySnapshot{}, false, ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	return result, true, nil
}

// Only independently verified K explains older accepted desired generations.
// The full identity must match after undoing those monotonic increments alone.
func systemUpdatePortKCanFollow(before, after SystemUpdatePortPolicySnapshot) bool {
	body, _ := json.Marshal(after.Snapshot)
	var restored contracts.SystemUpdatePortPolicySnapshot
	if json.Unmarshal(body, &restored) != nil {
		return false
	}
	previous := map[string]int64{}
	for _, target := range before.Snapshot.Targets {
		previous[target.ServiceID] = target.EndpointRevision
	}
	for i := range restored.Targets {
		target := &restored.Targets[i]
		revision, ok := previous[target.ServiceID]
		if !ok || target.EndpointRevision < revision || (target.EndpointRevision-revision)%2 != 0 {
			return false
		}
		target.EndpointRevision = revision
	}
	id, _, err := contracts.ComputeSystemUpdatePortSnapshotIdentity(restored)
	return err == nil && id == before.Ref.SnapshotID
}

func validSystemUpdatePortProjectionSnapshot(job SystemUpdateJob, policy UpdaterPolicy, host SystemUpdateExecutionHost, snapshot SystemUpdatePortPolicySnapshot, frozen *contracts.SystemUpdatePortSnapshotRef) bool {
	if frozen == nil || !reflect.DeepEqual(snapshot.Ref, *frozen) || !portPolicyMatchesSnapshot(policy, snapshot) || job.OwnershipEpoch != host.OwnershipEpoch || job.AgentServiceID != host.AgentServiceID || !systemUpdatePortV2OwnershipMatches(job, host) || snapshot.Snapshot.UpdaterID != policy.UpdaterID || snapshot.Snapshot.HostID != host.ExecutionHostID || snapshot.Snapshot.OwnershipEpoch != host.OwnershipEpoch || contracts.ValidateSystemUpdatePortPlan(systemUpdatePortContractsPlan(job.PortReconfigure)) != nil {
		return false
	}
	computed, err := systemUpdatePortSnapshotWithRef(snapshot.Snapshot, job.TargetID)
	return err == nil && reflect.DeepEqual(computed.Ref, snapshot.Ref)
}

func validateSystemUpdatePortProjectionServices(snapshot SystemUpdatePortPolicySnapshot, services map[string]RegisteredService) error {
	agent, ok := services[snapshot.Snapshot.UpdaterID]
	if !ok || agent.ExecutionHostID != snapshot.Snapshot.HostID || agent.OwnershipEpoch != snapshot.Snapshot.OwnershipEpoch {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	refs := map[string]bool{}
	if agent.TokenID != "" {
		refs[agent.TokenID] = true
	}
	for _, target := range snapshot.Snapshot.Targets {
		service, ok := services[target.ServiceID]
		if !ok && target.ServiceID == "control-panel" {
			continue
		}
		if !ok || service.ServiceType != string(target.ServiceType) || portSnapshotEndpoint(service.DesiredEndpoint) != target.DesiredEndpoint || portSnapshotEndpoint(service.AppliedEndpoint) != target.AppliedEndpoint || service.EndpointRevision != target.EndpointRevision || service.AppliedEndpointRevision != target.AppliedEndpointRevision || service.AppliedConfigRevision != target.ConfigRevision || service.AppliedConfigSHA256 != target.ConfigSHA256 {
			return ErrSystemUpdatePortPolicySnapshotUnavailable
		}
		if service.TokenID != "" {
			refs[service.TokenID] = true
		}
	}
	current := make([]string, 0, len(refs))
	for ref := range refs {
		current = append(current, ref)
	}
	expected := append([]string(nil), snapshot.Snapshot.CredentialReferences...)
	sort.Strings(current)
	sort.Strings(expected)
	if strings.Join(current, "\x00") != strings.Join(expected, "\x00") {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	return nil
}

func mergeSystemUpdatePortProjection(selected **SystemUpdatePortPolicySnapshot, snapshot SystemUpdatePortPolicySnapshot, targetID string) error {
	body, _ := json.Marshal(snapshot.Snapshot)
	var copy contracts.SystemUpdatePortPolicySnapshot
	if json.Unmarshal(body, &copy) != nil {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	projected, err := systemUpdatePortSnapshotWithRef(copy, targetID)
	if err != nil {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	if *selected != nil && !portSnapshotsEqual(**selected, projected) {
		return ErrSystemUpdatePortPolicySnapshotUnavailable
	}
	*selected = &projected
	return nil
}
