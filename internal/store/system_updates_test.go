package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemorySystemUpdateStoreLifecycleAndIdempotency(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	params := CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", DeploymentMode: "systemd",
		AgentServiceID: "updater-01",
		CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle,
		IdempotencyKey: "request-01", RequestedByUserID: "user-01", RequestedByUsername: "operator",
	}
	job, created, err := updates.CreateSystemUpdateJob(t.Context(), params)
	if err != nil || !created || job.Status != SystemUpdateStatusQueued || job.ExecutionHostID != params.AgentServiceID {
		t.Fatalf("create job = %#v, created=%v, err=%v", job, created, err)
	}
	replayed, created, err := updates.CreateSystemUpdateJob(t.Context(), params)
	if err != nil || created || replayed.ID != job.ID {
		t.Fatalf("idempotent replay = %#v, created=%v, err=%v", replayed, created, err)
	}
	resolvedDrift := params
	resolvedDrift.AgentServiceID = "updater-02"
	resolvedDrift.ExecutionHostID = "host-drift"
	resolvedDrift.CurrentVersion = "v1.0.1"
	resolvedDrift.TargetVersion = "v1.2.0"
	resolvedReplay, created, err := updates.CreateSystemUpdateJob(t.Context(), resolvedDrift)
	if err != nil || created || resolvedReplay.ID != job.ID || resolvedReplay.AgentServiceID != "updater-01" || resolvedReplay.ExecutionHostID != "updater-01" || resolvedReplay.TargetVersion != "v1.1.0" {
		t.Fatalf("resolved-state idempotent replay = %#v, created=%v, err=%v", resolvedReplay, created, err)
	}
	conflicting := params
	conflicting.TargetID = "worker-02"
	if _, _, err := updates.CreateSystemUpdateJob(t.Context(), conflicting); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("client-field idempotency conflict err = %v", err)
	}
	active := params
	active.IdempotencyKey = "request-02"
	if _, _, err := updates.CreateSystemUpdateJob(t.Context(), active); !errors.Is(err, ErrSystemUpdateTargetActive) {
		t.Fatalf("duplicate active target err = %v", err)
	}
	canceled, err := updates.CancelSystemUpdateJob(t.Context(), job.ID, "user-01")
	if SystemUpdateStatusCancelled != "canceled" {
		t.Fatalf("canceled status constant = %q", SystemUpdateStatusCancelled)
	}
	if err != nil || canceled.Status != SystemUpdateStatusCancelled || canceled.CompletedAt == nil {
		t.Fatalf("cancel job = %#v, err=%v", canceled, err)
	}
	if _, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled job remained active: %v", err)
	}
}

func TestMemorySystemUpdateStoreRejectsUnknownCurrentVersion(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	base := CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-01", DeploymentMode: "systemd",
		TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: "unknown-current", RequestedByUserID: "user-01",
	}
	for _, current := range []string{"", "dev", "not-a-version", "1.2.3", "v1.2.3+build.1"} {
		params := base
		params.CurrentVersion = current
		if _, _, err := updates.CreateSystemUpdateJob(t.Context(), params); !errors.Is(err, ErrInvalidSystemUpdate) {
			t.Fatalf("current version %q create err = %v", current, err)
		}
	}
	for _, target := range []string{"", "dev", "1.2.3", "v1.2.3+build.1"} {
		params := base
		params.CurrentVersion = "v1.0.0"
		params.TargetVersion = target
		if _, _, err := updates.CreateSystemUpdateJob(t.Context(), params); !errors.Is(err, ErrInvalidSystemUpdate) {
			t.Fatalf("target version %q create err = %v", target, err)
		}
	}
}

func TestMemorySystemUpdateStoreClaimReportLeaseTransitionAndRedaction(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", DeploymentMode: "systemd",
		AgentServiceID: "updater-01",
		CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyMaintenance,
		IdempotencyKey: "request-claim", RequestedByUserID: "user-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, clearActive, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", map[string]string{"worker-01": "systemd"}, now, 2*time.Minute)
	if err != nil || clearActive || claim.Job.ID != job.ID || claim.Job.Status != SystemUpdateStatusClaimed || claim.LeaseToken == "" || claim.LeaseGeneration != 1 || claim.ReportSequence != 1 || claim.RecoveryRequired || !claim.LeaseExpiresAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("claim = %#v, err=%v", claim, err)
	}
	if strings.Contains(claim.Job.Message, claim.LeaseToken) {
		t.Fatal("lease token leaked into job")
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: "wrong", LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: SystemUpdateStatusDownloading, Progress: 5}, now.Add(time.Second), 15*time.Minute); !errors.Is(err, ErrSystemUpdateLeaseInvalid) {
		t.Fatalf("wrong lease err = %v", err)
	}
	reported, applied, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{
		AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1,
		Status: SystemUpdateStatusHealthChecking, Progress: 90,
		Message: "Bearer raw-secret ast_svc_another-secret token=third-secret health ok",
	}, now.Add(time.Second), 15*time.Minute)
	if err != nil || !applied || reported.Status != SystemUpdateStatusHealthChecking || reported.LeaseExpiresAt == nil || !reported.LeaseExpiresAt.Equal(now.Add(time.Second).Add(15*time.Minute)) {
		t.Fatalf("forward-skip report = %#v, applied=%v, err=%v", reported, applied, err)
	}
	if strings.Contains(reported.Message, "raw-secret") || strings.Contains(reported.Message, "ast_svc_") || strings.Contains(reported.Message, "third-secret") {
		t.Fatalf("report message was not redacted: %q", reported.Message)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 2, Status: SystemUpdateStatusSucceeded, Progress: 100, ArtifactDigest: "not-a-digest"}, now.Add(2*time.Second), 15*time.Minute); !errors.Is(err, ErrInvalidSystemUpdate) {
		t.Fatalf("non-canonical digest err = %v", err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 2, Status: SystemUpdateStatusSucceeded, Progress: 100, Code: "Bad Code!"}, now.Add(2*time.Second), 15*time.Minute); !errors.Is(err, ErrInvalidSystemUpdate) {
		t.Fatalf("invalid report code err = %v", err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 2, Status: SystemUpdateStatusInstalling, Progress: 91}, now.Add(2*time.Second), 15*time.Minute); !errors.Is(err, ErrSystemUpdateTransition) {
		t.Fatalf("backward transition err = %v", err)
	}
	terminalReport := SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 2, Status: SystemUpdateStatusSucceeded, Progress: 100}
	completed, applied, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, terminalReport, now.Add(2*time.Second), 15*time.Minute)
	if err != nil || !applied || completed.Status != SystemUpdateStatusSucceeded || completed.LeaseExpiresAt != nil || completed.CompletedAt == nil {
		t.Fatalf("complete report = %#v, applied=%v, err=%v", completed, applied, err)
	}
	if _, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("completed job remained active: %v", err)
	}
	replayedTerminal, replayApplied, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, terminalReport, now.Add(24*time.Hour), 15*time.Minute)
	if err != nil || replayApplied || replayedTerminal.ID != completed.ID || !replayedTerminal.UpdatedAt.Equal(completed.UpdatedAt) {
		t.Fatalf("terminal response-loss replay = %#v, applied=%v, err=%v", replayedTerminal, replayApplied, err)
	}
	modifiedTerminal := terminalReport
	modifiedTerminal.Code = "different_result"
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, modifiedTerminal, now.Add(24*time.Hour), 15*time.Minute); !errors.Is(err, ErrSystemUpdateSequenceStale) {
		t.Fatalf("modified terminal replay err = %v", err)
	}
	wrongTokenTerminal := terminalReport
	wrongTokenTerminal.LeaseToken = "wrong"
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, wrongTokenTerminal, now.Add(24*time.Hour), 15*time.Minute); !errors.Is(err, ErrSystemUpdateLeaseInvalid) {
		t.Fatalf("wrong-token terminal replay err = %v", err)
	}
}

func TestMemorySystemUpdateMutationAuthorizationIsExactAndNonReplayable(t *testing.T) {
	newClaim := func(key string, now time.Time) (*MemorySystemUpdateStore, SystemUpdateJob, SystemUpdateClaim) {
		updates := NewMemorySystemUpdateStore()
		job, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-01", DeploymentMode: "systemd",
			CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: key, RequestedByUserID: "user-01",
		})
		if err != nil {
			t.Fatal(err)
		}
		claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", map[string]string{"worker-01": "systemd"}, now, 2*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return updates, job, claim
	}
	now := time.Now().UTC()
	updates, job, claim := newClaim("authorize-exact", now)
	authorization := SystemUpdateAuthorization{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, TargetID: "worker-01", TargetVersion: "v1.1.0", DeploymentMode: "systemd"}
	if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, authorization, now.Add(time.Second)); !errors.Is(err, ErrSystemUpdateAuthorizationState) {
		t.Fatalf("claimed mutation authorization err = %v", err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: SystemUpdateStatusInstalling, Progress: 70}, now.Add(time.Second), 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, authorization, now.Add(2*time.Second)); err != nil {
		t.Fatalf("installing mutation authorization: %v", err)
	}
	for _, serviceID := range []string{"worker-01", "updater-01"} {
		if active, err := updates.HasActiveSystemUpdateReference(t.Context(), serviceID); err != nil || !active {
			t.Fatalf("active reference for %s = %v, %v", serviceID, active, err)
		}
	}
	for name, mutate := range map[string]func(*SystemUpdateAuthorization){
		"wrong token":      func(a *SystemUpdateAuthorization) { a.LeaseToken = "wrong" },
		"wrong generation": func(a *SystemUpdateAuthorization) { a.LeaseGeneration++ },
		"wrong agent":      func(a *SystemUpdateAuthorization) { a.AgentServiceID = "updater-02" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := authorization
			mutate(&candidate)
			if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, candidate, now.Add(2*time.Second)); !errors.Is(err, ErrSystemUpdateLeaseInvalid) {
				t.Fatalf("authorization err = %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*SystemUpdateAuthorization){
		"wrong host":    func(a *SystemUpdateAuthorization) { a.ExecutionHostID = "host-02" },
		"wrong target":  func(a *SystemUpdateAuthorization) { a.TargetID = "worker-02" },
		"wrong version": func(a *SystemUpdateAuthorization) { a.TargetVersion = "v9.9.9" },
		"wrong mode":    func(a *SystemUpdateAuthorization) { a.DeploymentMode = "docker" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := authorization
			mutate(&candidate)
			if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, candidate, now.Add(2*time.Second)); !errors.Is(err, ErrSystemUpdateAuthorizationMismatch) {
				t.Fatalf("authorization err = %v", err)
			}
		})
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 2, Status: SystemUpdateStatusReconciling, Progress: 80}, now.Add(3*time.Second), 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, authorization, now.Add(4*time.Second)); err != nil {
		t.Fatalf("reconciling mutation authorization: %v", err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 3, Status: SystemUpdateStatusSucceeded, Progress: 100}, now.Add(5*time.Second), 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, authorization, now.Add(6*time.Second)); !errors.Is(err, ErrSystemUpdateAuthorizationState) {
		t.Fatalf("terminal mutation authorization err = %v", err)
	}
	for _, serviceID := range []string{"worker-01", "updater-01"} {
		if active, err := updates.HasActiveSystemUpdateReference(t.Context(), serviceID); err != nil || active {
			t.Fatalf("terminal reference for %s = %v, %v", serviceID, active, err)
		}
	}

	expiringUpdates, expiringJob, expiringClaim := newClaim("authorize-expired", now)
	if _, _, err := expiringUpdates.ReportSystemUpdateJob(t.Context(), expiringJob.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: expiringClaim.LeaseToken, LeaseGeneration: expiringClaim.LeaseGeneration, Sequence: 1, Status: SystemUpdateStatusInstalling, Progress: 70}, now.Add(time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	expired := SystemUpdateAuthorization{AgentServiceID: "updater-01", LeaseToken: expiringClaim.LeaseToken, LeaseGeneration: expiringClaim.LeaseGeneration, TargetID: "worker-01", TargetVersion: "v1.1.0", DeploymentMode: "systemd"}
	if err := expiringUpdates.AuthorizeSystemUpdateMutation(t.Context(), expiringJob.ID, expired, now.Add(2*time.Minute)); !errors.Is(err, ErrSystemUpdateLeaseInvalid) {
		t.Fatalf("expired mutation authorization err = %v", err)
	}
}

func TestMemorySystemUpdateMutationAuthorizationBindsExplicitExecutionHost(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-01", ExecutionHostID: "host-01",
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle,
		IdempotencyKey: "authorize-host", RequestedByUserID: "user-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "host-01", "", map[string]string{"worker-01": "systemd"}, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: SystemUpdateStatusInstalling, Progress: 70}, now.Add(time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	authorization := SystemUpdateAuthorization{
		AgentServiceID: "updater-01", ExecutionHostID: "host-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
		TargetID: "worker-01", TargetVersion: "v1.1.0", DeploymentMode: "systemd",
	}
	if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, authorization, now.Add(2*time.Second)); err != nil {
		t.Fatalf("explicit host authorization: %v", err)
	}
	authorization.ExecutionHostID = ""
	if err := updates.AuthorizeSystemUpdateMutation(t.Context(), job.ID, authorization, now.Add(2*time.Second)); !errors.Is(err, ErrSystemUpdateAuthorizationMismatch) {
		t.Fatalf("host-less authorization for explicit host err = %v", err)
	}
}

func TestSystemUpdateTransitionAllowsOnlyLateStageReconciliation(t *testing.T) {
	for _, status := range []string{SystemUpdateStatusInstalling, SystemUpdateStatusStarting, SystemUpdateStatusHealthChecking, SystemUpdateStatusRollingBack} {
		if !allowedSystemUpdateTransition(status, SystemUpdateStatusReconciling) {
			t.Fatalf("%s -> reconciling was rejected", status)
		}
	}
	for _, status := range []string{SystemUpdateStatusClaimed, SystemUpdateStatusDownloading, SystemUpdateStatusVerifying, SystemUpdateStatusStaging, SystemUpdateStatusStopping} {
		if allowedSystemUpdateTransition(status, SystemUpdateStatusReconciling) {
			t.Fatalf("unsafe %s -> reconciling was accepted", status)
		}
	}
	if allowedSystemUpdateTransition(SystemUpdateStatusReconciling, SystemUpdateStatusInstalling) || !allowedSystemUpdateTransition(SystemUpdateStatusReconciling, SystemUpdateStatusRolledBack) {
		t.Fatal("reconciling transition fence is invalid")
	}
}

func TestControlPanelServiceIDIsReservedForSyntheticUpdateTarget(t *testing.T) {
	err := validateServiceRegistration(ServiceRegistration{ServiceID: "control-panel", ServiceType: "worker", ServiceName: "collision", PublicURL: "https://worker.example.com"})
	if !errors.Is(err, ErrInvalidServiceRegistration) {
		t.Fatalf("control-panel service ID collision was accepted: %v", err)
	}
}

func TestServiceRegistrationRejectsShellUnsafeServiceID(t *testing.T) {
	for _, serviceID := range []string{"updater $(touch /tmp/pwn)", "updater`id`", "updater'quoted", strings.Repeat("a", 129)} {
		err := validateServiceRegistration(ServiceRegistration{ServiceID: serviceID, ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com"})
		if !errors.Is(err, ErrInvalidServiceRegistration) {
			t.Fatalf("unsafe service ID %q was accepted: %v", serviceID, err)
		}
	}
}

func TestMemorySystemUpdateStoreDoesNotClaimIneligibleOrUnexpiredWork(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	_, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		AgentServiceID: "updater-01",
		Strategy:       SystemUpdateStrategyWhenIdle, IdempotencyKey: "request-wait", RequestedByUserID: "user-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", map[string]string{"other": "systemd"}, now, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ineligible claim err = %v", err)
	}
	claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", map[string]string{"worker-01": "systemd"}, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-02", "", "", map[string]string{"worker-01": "systemd"}, now.Add(30*time.Second), time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpired second claim err = %v", err)
	}
	if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-02", "", "", map[string]string{"worker-01": "systemd"}, now.Add(2*time.Minute), time.Minute); !errors.Is(err, ErrSystemUpdateTakeoverForbidden) {
		t.Fatalf("cross-agent reclaim err = %v", err)
	}
	reclaimed, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", map[string]string{"worker-01": "systemd"}, now.Add(2*time.Minute), time.Minute)
	if err != nil || reclaimed.Job.ID != claim.Job.ID || reclaimed.LeaseToken == claim.LeaseToken || !reclaimed.RecoveryRequired || reclaimed.LastStatus != SystemUpdateStatusClaimed || reclaimed.Job.Status != SystemUpdateStatusReconciling || reclaimed.LeaseGeneration != 2 || reclaimed.ReportSequence != 1 {
		t.Fatalf("expired reclaim = %#v, err=%v", reclaimed, err)
	}
}

func TestMemorySystemUpdateStoreSerializesAgentExecutionAndReclaimsBeforeQueued(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	create := func(targetID, serviceType, key string) SystemUpdateJob {
		job, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: targetID, TargetServiceType: serviceType, DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", AgentServiceID: "updater-01",
			Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: key, RequestedByUserID: "user-01",
		})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	first := create("worker-01", "worker", "serialized-01")
	second := create("encoder-01", "encoder_recorder", "serialized-02")
	eligible := map[string]string{"worker-01": "systemd", "encoder-01": "systemd"}
	now := time.Now().UTC()
	claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", eligible, now, 2*time.Minute)
	if err != nil || (claim.Job.ID != first.ID && claim.Job.ID != second.ID) {
		t.Fatalf("first serialized claim = %#v err=%v", claim, err)
	}
	activeJob := first
	queuedJob := second
	if claim.Job.ID == second.ID {
		activeJob, queuedJob = second, first
	}
	_, applied, err := updates.ReportSystemUpdateJob(t.Context(), activeJob.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration, Sequence: 1, Status: SystemUpdateStatusDownloading, Progress: 10}, now.Add(time.Minute), 45*time.Minute)
	if err != nil || !applied {
		t.Fatalf("extend execution lease: applied=%v err=%v", applied, err)
	}
	if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", eligible, now.Add(3*time.Minute), 2*time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("queued job replaced nonexpired active journal: %v", err)
	}
	reclaimed, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", eligible, now.Add(47*time.Minute), 2*time.Minute)
	if err != nil || reclaimed.Job.ID != activeJob.ID || !reclaimed.RecoveryRequired || reclaimed.Job.Status != SystemUpdateStatusReconciling || reclaimed.ReportSequence != 2 {
		t.Fatalf("expired active was not reclaimed before queued: %#v err=%v", reclaimed, err)
	}
	if _, applied, err := updates.ReportSystemUpdateJob(t.Context(), activeJob.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: reclaimed.LeaseToken, LeaseGeneration: reclaimed.LeaseGeneration, Sequence: reclaimed.ReportSequence, Status: SystemUpdateStatusSucceeded, Progress: 100}, now.Add(48*time.Minute), 45*time.Minute); err != nil || !applied {
		t.Fatalf("finish reconciled job: applied=%v err=%v", applied, err)
	}
	next, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", eligible, now.Add(49*time.Minute), 2*time.Minute)
	if err != nil || next.Job.ID != queuedJob.ID {
		t.Fatalf("queued job was not released after terminal report: %#v err=%v", next, err)
	}
}

func TestMemorySystemUpdateStoreParallelClaimsYieldOneExecutingJob(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	for index, target := range []string{"worker-01", "encoder-01"} {
		if _, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: target, TargetServiceType: target, DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", AgentServiceID: "updater-01",
			Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: "parallel-0" + string(rune('1'+index)), RequestedByUserID: "user-01",
		}); err != nil {
			t.Fatal(err)
		}
	}
	eligible := map[string]string{"worker-01": "systemd", "encoder-01": "systemd"}
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", eligible, time.Now().UTC(), 2*time.Minute)
			errorsOut <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsOut)
	successes, busy := 0, 0
	for err := range errorsOut {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrNotFound):
			busy++
		default:
			t.Fatalf("parallel claim err = %v", err)
		}
	}
	if successes != 1 || busy != 1 {
		t.Fatalf("parallel claim outcomes success=%d busy=%d", successes, busy)
	}
}

func TestMemorySystemUpdateStoreAllowsParallelClaimsAcrossExecutionHosts(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	targetsByHost := map[string]string{"host-a": "worker-a", "host-b": "worker-b"}
	eligible := map[string]string{}
	for hostID, targetID := range targetsByHost {
		eligible[targetID] = "systemd"
		if _, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: targetID, TargetServiceType: "worker", AgentServiceID: "updater-01", ExecutionHostID: hostID,
			DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle,
			IdempotencyKey: "parallel-" + hostID, RequestedByUserID: "user-01",
		}); err != nil {
			t.Fatal(err)
		}
	}

	type claimResult struct {
		hostID string
		claim  SystemUpdateClaim
		err    error
	}
	start := make(chan struct{})
	results := make(chan claimResult, len(targetsByHost))
	var wait sync.WaitGroup
	for hostID := range targetsByHost {
		hostID := hostID
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", hostID, "", eligible, time.Now().UTC(), 2*time.Minute)
			results <- claimResult{hostID: hostID, claim: claim, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	for result := range results {
		if result.err != nil || result.claim.Job.ExecutionHostID != result.hostID || result.claim.Job.TargetID != targetsByHost[result.hostID] {
			t.Fatalf("host claim %s = %#v, err=%v", result.hostID, result.claim, result.err)
		}
	}
}

func TestMemorySystemUpdateStoreRecoversPerHostWithoutBlockingOtherHosts(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	create := func(targetID, hostID string) SystemUpdateJob {
		job, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: targetID, TargetServiceType: "worker", AgentServiceID: "updater-01", ExecutionHostID: hostID,
			DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle,
			IdempotencyKey: "recover-" + targetID, RequestedByUserID: "user-01",
		})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	create("worker-a-1", "host-a")
	create("worker-a-2", "host-a")
	hostBJob := create("worker-b-1", "host-b")
	eligible := map[string]string{"worker-a-1": "systemd", "worker-a-2": "systemd", "worker-b-1": "systemd"}
	now := time.Now().UTC()
	hostAClaim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "host-a", "", eligible, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	hostBClaim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "host-b", "", eligible, now.Add(30*time.Second), time.Minute)
	if err != nil || hostBClaim.Job.ID != hostBJob.ID {
		t.Fatalf("host-b claim = %#v, err=%v", hostBClaim, err)
	}
	recovered, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "host-a", "", eligible, now.Add(2*time.Minute), time.Minute)
	if err != nil || recovered.Job.ID != hostAClaim.Job.ID || !recovered.RecoveryRequired || recovered.Job.Status != SystemUpdateStatusReconciling {
		t.Fatalf("host-a recovery = %#v, err=%v", recovered, err)
	}
	if _, clear, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "host-b", recovered.Job.ID, eligible, now.Add(3*time.Minute), time.Minute); !errors.Is(err, ErrSystemUpdateActiveUnavailable) || clear {
		t.Fatalf("cross-host active job was not rejected: clear=%v err=%v", clear, err)
	}
}

func TestMemorySystemUpdateStoreActiveJobClaimNeverPoisonsAnotherQueuedJob(t *testing.T) {
	updates := NewMemorySystemUpdateStore()
	create := func(targetID, key string) SystemUpdateJob {
		job, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{TargetID: targetID, TargetServiceType: "worker", AgentServiceID: "updater-01", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle, IdempotencyKey: key, RequestedByUserID: "user-01"})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	first := create("worker-01", "active-local-01")
	second := create("worker-02", "active-local-02")
	eligible := map[string]string{"worker-01": "systemd", "worker-02": "systemd"}
	now := time.Now().UTC()
	initial, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", eligible, now, 2*time.Minute)
	if err != nil || (initial.Job.ID != first.ID && initial.Job.ID != second.ID) {
		t.Fatalf("initial active claim = %#v err=%v", initial, err)
	}
	activeJob := first
	queuedJob := second
	if initial.Job.ID == second.ID {
		activeJob, queuedJob = second, first
	}
	recovered, clearActive, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", activeJob.ID, eligible, now.Add(time.Minute), 2*time.Minute)
	if err != nil || clearActive || recovered.Job.ID != activeJob.ID || recovered.Job.Status != SystemUpdateStatusReconciling || recovered.LeaseGeneration != initial.LeaseGeneration+1 || !recovered.RecoveryRequired {
		t.Fatalf("active_job_id did not fence/recover same job: %#v clear=%v err=%v", recovered, clearActive, err)
	}
	if _, applied, err := updates.ReportSystemUpdateJob(t.Context(), activeJob.ID, SystemUpdateReport{AgentServiceID: "updater-01", LeaseToken: recovered.LeaseToken, LeaseGeneration: recovered.LeaseGeneration, Sequence: recovered.ReportSequence, Status: SystemUpdateStatusSucceeded, Progress: 100}, now.Add(2*time.Minute), 45*time.Minute); err != nil || !applied {
		t.Fatalf("finish active recovery: applied=%v err=%v", applied, err)
	}
	clearedClaim, clearActive, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", activeJob.ID, eligible, now.Add(3*time.Minute), 2*time.Minute)
	if err != nil || !clearActive || clearedClaim.Job.ID != "" {
		t.Fatalf("terminal active_job_id did not request durable clear: %#v clear=%v err=%v", clearedClaim, clearActive, err)
	}
	queued, err := updates.GetActiveSystemUpdateJob(t.Context(), queuedJob.TargetID)
	if err != nil || queued.Status != SystemUpdateStatusQueued {
		t.Fatalf("terminal active clear claimed another job: %#v err=%v", queued, err)
	}
	if _, clearActive, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "missing-job", nil, now.Add(4*time.Minute), 2*time.Minute); err != nil || !clearActive {
		t.Fatalf("missing active_job_id clear = %v err=%v", clearActive, err)
	}
	next, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "", "", eligible, now.Add(5*time.Minute), 2*time.Minute)
	if err != nil || next.Job.ID != queuedJob.ID {
		t.Fatalf("normal poll after durable clear did not claim queued job: %#v err=%v", next, err)
	}
}
