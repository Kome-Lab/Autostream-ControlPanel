package store

import (
	"errors"
	"strings"
	"testing"
)

func TestMemorySavePullUpdaterPolicyAdvancesOwnershipRevisionAtomically(t *testing.T) {
	ctx := t.Context()
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()

	legacyToken := "github_pat_shared_release_token"
	legacyPolicy := validUpdaterPolicy()
	legacyPolicy.UpdaterID = ""
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		ctx,
		"legacy-updater",
		0,
		legacyPolicy,
		&legacyToken,
	); err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"host-agent-a",
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	input := validPullUpdaterPolicyForOwnership()
	created, err := policies.SavePullUpdaterPolicy(ctx, updates, "host-agent-a", 0, ownership.OwnershipEpoch, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatalf("created policy revision = %d", created.Revision)
	}
	currentOwnership, err := updates.GetSystemUpdateExecutionHost(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if currentOwnership.PolicyRevision != created.Revision ||
		currentOwnership.OwnershipEpoch != ownership.OwnershipEpoch {
		t.Fatalf("created ownership = %#v, original = %#v", currentOwnership, ownership)
	}

	input.PollIntervalSeconds = 20
	updated, err := policies.SavePullUpdaterPolicy(ctx, updates, "host-agent-a", created.Revision, ownership.OwnershipEpoch, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.PollIntervalSeconds != 20 {
		t.Fatalf("updated policy = %#v", updated)
	}
	currentOwnership, err = updates.GetSystemUpdateExecutionHost(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if currentOwnership.PolicyRevision != updated.Revision ||
		currentOwnership.OwnershipEpoch != ownership.OwnershipEpoch {
		t.Fatalf("updated ownership = %#v, original = %#v", currentOwnership, ownership)
	}
	gotToken, err := policies.GetUpdaterReleaseTokenValue(ctx)
	if err != nil || gotToken != legacyToken {
		t.Fatalf("pull save mutated shared release token: token=%q err=%v", gotToken, err)
	}
}

func TestMemoryPullPolicyRejectsLegacySavePathsBeforeReleaseTokenAccess(t *testing.T) {
	ctx := t.Context()
	policies := NewMemoryUpdaterPolicyStore()
	legacyPolicy := validUpdaterPolicy()
	legacyPolicy.UpdaterID = ""
	token := "github_pat_preserved_across_rejected_pull_save"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(ctx, "legacy-updater", 0, legacyPolicy, &token); err != nil {
		t.Fatal(err)
	}

	input := validPullUpdaterPolicyForOwnership()
	if _, err := policies.SaveUpdaterPolicy(ctx, "host-agent-a", 0, input); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("plain pull save error = %v, want ErrInvalidSettings", err)
	}
	replacement := "github_pat_must_not_replace_shared_token"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		ctx,
		"host-agent-a",
		0,
		input,
		&replacement,
	); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("release-token pull save error = %v, want ErrInvalidSettings", err)
	}
	got, err := policies.GetUpdaterReleaseTokenValue(ctx)
	if err != nil || got != token {
		t.Fatalf("rejected pull save mutated shared release token: token=%q err=%v", got, err)
	}
}

func TestMemorySavePullUpdaterPolicyRejectsStaleBindingAndActiveJobWithoutPartialMutation(t *testing.T) {
	ctx := t.Context()
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"host-agent-a",
		0,
	); err != nil {
		t.Fatal(err)
	}

	input := validPullUpdaterPolicyForOwnership()
	ownership, err := updates.GetSystemUpdateExecutionHost(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	created, err := policies.SavePullUpdaterPolicy(ctx, updates, "host-agent-a", 0, ownership.OwnershipEpoch, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		"host-agent-a",
		created.Revision,
		ownership.OwnershipEpoch+1,
		input,
	); !errors.Is(err, ErrSystemUpdateExecutionHostStale) {
		t.Fatalf("stale ownership epoch error = %v, want ErrSystemUpdateExecutionHostStale", err)
	}
	assertPullPolicyAndOwnershipRevision(t, policies, updates, "host-agent-a", "host-a", created.Revision)
	if _, err := policies.SavePullUpdaterPolicy(ctx, updates, "host-agent-a", 0, ownership.OwnershipEpoch, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save error = %v, want ErrConflict", err)
	}
	assertPullPolicyAndOwnershipRevision(t, policies, updates, "host-agent-a", "host-a", created.Revision)

	job, createdJob, err := updates.CreateSystemUpdateJob(ctx, CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    "host-agent-a",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "pull-policy-active-job",
		RequestedByUserID: "admin",
	})
	if err != nil || !createdJob {
		t.Fatalf("create active job: job=%#v created=%v err=%v", job, createdJob, err)
	}
	input.PollIntervalSeconds = 25
	if _, err := policies.SavePullUpdaterPolicy(ctx, updates, "host-agent-a", created.Revision, ownership.OwnershipEpoch, input); !errors.Is(err, ErrSystemUpdateExecutionHostBusy) {
		t.Fatalf("active-job save error = %v, want ErrSystemUpdateExecutionHostBusy", err)
	}
	assertPullPolicyAndOwnershipRevision(t, policies, updates, "host-agent-a", "host-a", created.Revision)
}

func TestMemorySavePullUpdaterPolicyPreservesSSHObserverOwnershipAndIgnoresActiveJob(t *testing.T) {
	ctx := t.Context()
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportSSHV1,
		"central-updater",
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := updates.CreateSystemUpdateJob(ctx, CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    "central-updater",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "observer-does-not-own-active-job",
		RequestedByUserID: "admin",
	}); err != nil || !created {
		t.Fatalf("create SSH-owned job: created=%v err=%v", created, err)
	}

	observer, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		"host-agent-a",
		0,
		ownership.OwnershipEpoch,
		validPullUpdaterPolicyForOwnership(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if observer.Revision != 1 {
		t.Fatalf("observer policy revision = %d", observer.Revision)
	}
	after, err := updates.GetSystemUpdateExecutionHost(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if after != ownership {
		t.Fatalf("observer policy changed SSH ownership: before=%#v after=%#v", ownership, after)
	}
}

func TestMemorySavePullUpdaterPolicyRejectsOwnershipBindingMismatch(t *testing.T) {
	ctx := t.Context()
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"another-host-agent",
		0,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		"host-agent-a",
		0,
		1,
		validPullUpdaterPolicyForOwnership(),
	); !errors.Is(err, ErrSystemUpdateAgentBindingMismatch) {
		t.Fatalf("binding mismatch error = %v, want ErrSystemUpdateAgentBindingMismatch", err)
	}
	if _, err := policies.GetUpdaterPolicy(ctx, "host-agent-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("binding mismatch persisted policy: %v", err)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.PolicyRevision != 0 || ownership.AgentServiceID != "another-host-agent" {
		t.Fatalf("binding mismatch mutated ownership: %#v", ownership)
	}
}

func validPullUpdaterPolicyForOwnership() UpdaterPolicy {
	return UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
}

func assertPullPolicyAndOwnershipRevision(
	t *testing.T,
	policies *MemoryUpdaterPolicyStore,
	updates *MemorySystemUpdateStore,
	serviceID, hostID string,
	wantRevision int64,
) {
	t.Helper()
	policy, err := policies.GetUpdaterPolicy(t.Context(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != wantRevision || ownership.PolicyRevision != wantRevision {
		t.Fatalf("policy/ownership revision = %d/%d, want %d", policy.Revision, ownership.PolicyRevision, wantRevision)
	}
}
