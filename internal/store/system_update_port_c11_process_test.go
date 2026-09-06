package store_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

var stPortC11ProcessChild = flag.Bool("st-port-c11-process-child", false, "run the isolated ST-PORT database commit test child")

type stPortC11ProcessRequest struct {
	JobID  string
	Report store.SystemUpdateReport
	Phase  string
}

// This entry point is selected only by the parent test binary. The existing
// opt-in test DSN is inherited; request stdin contains no runtime credentials.
func TestMariaDBSTPortV2C11ProcessChild(t *testing.T) {
	if !*stPortC11ProcessChild {
		return
	}
	var request stPortC11ProcessRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		t.Fatal("invalid C11 process request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal("C11 child database connection failed")
	}
	defer db.Close()
	ctx = store.WithSystemUpdatePortCommitObserver(ctx, func(observation store.SystemUpdatePortCommitObservation) {
		if observation.Phase != request.Phase {
			return
		}
		body, _ := json.Marshal(observation)
		fmt.Fprintln(os.Stdout, "ST_PORT_C11_PHASE "+string(body))
		<-ctx.Done() // Parent kills this actual process at the observed boundary.
	})
	_, _, err = store.NewMariaDBSystemUpdateStore(db).ReportSystemUpdateJob(ctx, request.JobID, request.Report, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal("terminal operation ended before process stop")
	}
	t.Fatal("C11 child escaped its bounded process-stop barrier")
}

func TestMariaDBSTPortV2C11CommitProcessCrash(t *testing.T) {
	for _, result := range []contracts.SystemUpdatePortReconfigurationResult{
		contracts.SystemUpdatePortReconfigurationApplied,
		contracts.SystemUpdatePortReconfigurationRolledBack,
		contracts.SystemUpdatePortReconfigurationUnchanged,
		contracts.SystemUpdatePortReconfigurationRollbackFailed,
	} {
		for _, phase := range []string{store.SystemUpdatePortBeforeCommit, store.SystemUpdatePortAfterCommit} {
			t.Run(string(result)+"/"+phase, func(t *testing.T) {
				f := newMariaDBSTPortV2Fixture(t)
				local, advertised := 18084, 8443
				if result == contracts.SystemUpdatePortReconfigurationUnchanged {
					local, advertised = 18081, 443
				}
				job := f.create(t, contracts.SystemUpdatePortModeLocalAndAdvertised, local, advertised, "c11")
				job = f.claim(t, job, "").Job
				sequence := int64(1)
				status := store.SystemUpdateStatusSucceeded
				if result != contracts.SystemUpdatePortReconfigurationUnchanged {
					f.report(t, job, sequence, store.SystemUpdateStatusInstalling, nil)
					sequence++
					f.consume(t, job, store.SystemUpdateMutationOperationPortReconfigure, "session-1234567890123456")
				}
				if result == contracts.SystemUpdatePortReconfigurationRolledBack {
					f.report(t, job, sequence, store.SystemUpdateStatusRollingBack, nil)
					sequence++
					status = store.SystemUpdateStatusRolledBack
				} else if result == contracts.SystemUpdatePortReconfigurationRollbackFailed {
					status = store.SystemUpdateStatusFailed
				}
				proof := mariaDBSTPortV2Result(job, result)
				report := mariaDBSTPortV2Report(job, sequence, status, proof)
				before := f.databaseState(t, job.ID)
				request := stPortC11ProcessRequest{JobID: job.ID, Report: report, Phase: phase}
				f.stopC11ProcessAtCommit(t, request)
				// A separate pool and connection observe MariaDB after the worker
				// process has actually exited, not a replacement in-memory store.
				fresh, err := database.OpenFromEnv(f.ctx)
				if err != nil {
					t.Fatal("fresh database connection failed")
				}
				t.Cleanup(func() { _ = fresh.Close() })
				f.db = fresh
				f.auth = store.NewMariaDBAuthStore(fresh)
				f.policies = store.NewMariaDBUpdaterPolicyAdminStore(fresh, "")
				f.updates = store.NewMariaDBSystemUpdateStore(fresh)
				after := f.databaseState(t, job.ID)
				if phase == store.SystemUpdatePortBeforeCommit {
					if !reflect.DeepEqual(before, after) {
						t.Fatal("process stop before commit left partial durable state")
					}
				} else {
					f.assertC11CommittedState(t, job, proof, status)
				}
				stored, applied, err := f.updates.ReportSystemUpdateJob(f.ctx, job.ID, report, time.Now().UTC(), time.Minute)
				if err != nil || applied != (phase == store.SystemUpdatePortBeforeCommit) {
					t.Fatalf("same-job post-crash replay failed: applied=%v err=%v", applied, err)
				}
				if phase == store.SystemUpdatePortAfterCommit && !reflect.DeepEqual(after, f.databaseState(t, job.ID)) {
					t.Fatal("terminal reply loss replay performed another durable update")
				}
				f.assertC11CommittedState(t, job, proof, status)
				if result == contracts.SystemUpdatePortReconfigurationUnchanged {
					if !reflect.DeepEqual(before.configuration(), f.databaseState(t, job.ID).configuration()) {
						t.Fatal("unchanged changed configuration at a commit boundary")
					}
				}
				if result == contracts.SystemUpdatePortReconfigurationRollbackFailed {
					if stored.PortResult != nil || !stored.RecoveryRequired {
						t.Fatal("failed recovery consumed the accepted slot")
					}
					recovery := f.claim(t, stored, stored.ID)
					f.consume(t, recovery.Job, store.SystemUpdateMutationOperationPortReconfigureReconcile, "session-2234567890123456")
					rolledBack := mariaDBSTPortV2Result(recovery.Job, contracts.SystemUpdatePortReconfigurationRolledBack)
					f.report(t, recovery.Job, 1, store.SystemUpdateStatusRolledBack, rolledBack)
					f.assertC11CommittedState(t, job, rolledBack, store.SystemUpdateStatusRolledBack)
				}
			})
		}
	}
}

func (f *mariaDBSTPortV2Fixture) stopC11ProcessAtCommit(t *testing.T, request stPortC11ProcessRequest) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	body, _ := json.Marshal(request)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestMariaDBSTPortV2C11ProcessChild$", "-test.timeout=40s", "-st-port-c11-process-child")
	command.Stdin = bytes.NewReader(body)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	observed := make(chan store.SystemUpdatePortCommitObservation, 1)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "ST_PORT_C11_PHASE ") {
				continue
			}
			var value store.SystemUpdatePortCommitObservation
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "ST_PORT_C11_PHASE ")), &value) == nil {
				observed <- value
			}
		}
		done <- command.Wait()
	}()
	select {
	case value := <-observed:
		if value.JobID != request.JobID || value.Phase != request.Phase || value.Status != request.Report.Status {
			t.Fatal("child reached a different transaction boundary")
		}
	case <-done:
		t.Fatal("child exited before the real commit observer")
	case <-ctx.Done():
		t.Fatal("bounded C11 process barrier timed out")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal("could not stop the C11 worker process")
	}
	select {
	case err := <-done:
		if err == nil || command.ProcessState == nil || command.ProcessState.Success() {
			t.Fatal("C11 child was not terminated as required")
		}
	case <-ctx.Done():
		t.Fatal("C11 child did not exit within the process budget")
	}
}

type stPortServiceState struct {
	ID, TokenID, StagedTokenID, Status string
	Desired, Applied                   *store.ServiceEndpoint
	E, AE, C                           int64
	ConfigSHA                          string
	UpdatedAt                          time.Time
}

type stPortDatabaseState struct {
	Policy       []byte
	Host         store.SystemUpdateExecutionHost
	Services     []stPortServiceState
	Bindings     []string
	Reservations []store.ServicePortReservation
	Job          store.SystemUpdateJob
	Transaction  []byte
}

func (value stPortDatabaseState) configuration() stPortDatabaseState {
	value.Job = store.SystemUpdateJob{}
	value.Transaction = nil
	return value
}

func (f *mariaDBSTPortV2Fixture) databaseState(t *testing.T, jobID string) stPortDatabaseState {
	t.Helper()
	var state stPortDatabaseState
	if err := f.db.QueryRowContext(f.ctx, `SELECT policy_json FROM update_agent_policies WHERE service_id=?`, f.policy.UpdaterID).Scan(&state.Policy); err != nil {
		t.Fatal(err)
	}
	var err error
	state.Host, err = f.updates.GetSystemUpdateExecutionHost(f.ctx, f.policy.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range f.sourceServices(t) {
		state.Services = append(state.Services, stPortServiceState{ID: service.ServiceID, TokenID: service.TokenID, StagedTokenID: service.StagedNodeTokenID, Status: service.EndpointStatus, Desired: service.DesiredEndpoint, Applied: service.AppliedEndpoint, E: service.EndpointRevision, AE: service.AppliedEndpointRevision, C: service.AppliedConfigRevision, ConfigSHA: service.AppliedConfigSHA256, UpdatedAt: service.UpdatedAt})
	}
	for _, query := range []string{
		`SELECT CONCAT(target_id,':',binding_policy_revision,':',database_name) FROM update_agent_target_databases WHERE updater_service_id=? ORDER BY target_id`,
		`SELECT CONCAT(target_id,':',binding_policy_revision,':',local_listen_port) FROM update_agent_target_local_listeners WHERE updater_service_id=? ORDER BY target_id`,
	} {
		rows, err := f.db.QueryContext(f.ctx, query, f.policy.UpdaterID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			state.Bindings = append(state.Bindings, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	state.Reservations, err = f.updates.ListServicePortReservations(f.ctx, f.policy.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	if jobID == "" {
		return state
	}
	state.Job, err = f.updates.GetSystemUpdateJob(f.ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRowContext(f.ctx, `SELECT JSON_OBJECT('before',before_json,'target',target_json,'rollback',rollback_json,'plan',plan_json,'phase',phase,'hold',recovery_required,'accepted',accepted_result_json,'last',last_recovery_observation_json,'updated_at',updated_at) FROM system_update_port_transactions WHERE job_id=?`, jobID).Scan(&state.Transaction); err != nil {
		t.Fatal(err)
	}
	return state
}

func (f *mariaDBSTPortV2Fixture) assertC11CommittedState(t *testing.T, original store.SystemUpdateJob, proof *contracts.SystemUpdatePortResultV2, status string) {
	t.Helper()
	stored, err := f.updates.GetSystemUpdateJob(f.ctx, original.ID)
	if err != nil || stored.Status != status || stored.PolicyRevision != original.PolicyRevision || stored.OwnershipEpoch != original.OwnershipEpoch || !reflect.DeepEqual(stored.PortReconfigure, original.PortReconfigure) {
		t.Fatalf("committed job identity/plan changed: %v", err)
	}
	if proof.Result == contracts.SystemUpdatePortReconfigurationRollbackFailed {
		if stored.PortResult != nil || !stored.RecoveryRequired || stored.LastRecoveryObservation == nil || !contracts.EqualSystemUpdatePortResults(*stored.LastRecoveryObservation, *proof) {
			t.Fatal("failed rollback did not retain the distinct recovery hold")
		}
		reservations, err := f.updates.ListServicePortReservations(f.ctx, f.policy.ExecutionHostID)
		if err != nil || len(reservations) != 3 {
			t.Fatal("recovery hold released reservations")
		}
		policy, err := f.policies.GetUpdaterPolicy(f.ctx, f.policy.UpdaterID)
		if err != nil || policy.Revision != original.PortReconfigure.Target.SourcePolicyRevision || policy.ProjectionRevision != original.PortReconfigure.Target.ProjectionRevision || policy.LocalExecutorPolicyRevision != original.PortReconfigure.Target.ExecutorPolicyRevision {
			t.Fatal("recovery hold changed canonical T")
		}
		return
	}
	if stored.PortResult == nil || stored.RecoveryRequired || !contracts.EqualSystemUpdatePortResults(*stored.PortResult, *proof) {
		t.Fatal("accepted proof was not persisted atomically")
	}
	expected := original.PortReconfigure.Target
	if proof.Result == contracts.SystemUpdatePortReconfigurationRolledBack {
		expected = original.PortReconfigure.Rollback
	}
	if proof.Result == contracts.SystemUpdatePortReconfigurationUnchanged {
		expected = original.PortReconfigure.Before
	}
	if !reflect.DeepEqual(f.snapshot(t).Ref, *expected) {
		t.Fatal("committed whole-host policy/config differs from frozen result")
	}
	reservations, err := f.updates.ListServicePortReservations(f.ctx, f.policy.ExecutionHostID)
	if err != nil || len(reservations) != 2 {
		t.Fatal("accepted result left partial reservations")
	}
	for _, reservation := range reservations {
		if reservation.ServiceID == original.TargetID && (reservation.Port != expected.LocalListenPort || reservation.ServiceRole != "api") {
			t.Fatal("selected current reservation does not match accepted snapshot")
		}
	}
}
