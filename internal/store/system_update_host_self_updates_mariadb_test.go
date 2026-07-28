package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBHostSelfUpdateLifecycleGrantAndStrictProof(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := fixture.auth.GetService(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := fixture.auth.Heartbeat(
		ctx,
		store.ServiceToken{
			ID:          agent.TokenID,
			ServiceType: "update_agent",
		},
		store.ServiceHeartbeat{
			ServiceID: agent.ServiceID,
			Status:    "online",
			Version:   "v1.7.8",
			OS:        "linux",
			Arch:      "amd64",
			Capabilities: map[string]any{
				"host_agent":                          true,
				"observe_only":                        false,
				"update_executor":                     true,
				"mutation_enabled":                    true,
				"recovery_pending":                    false,
				"self_update_ready":                   true,
				"self_update_phase":                   "stable",
				"self_update_active_agent_version":    "v1.7.8",
				"self_update_active_executor_version": "v1.7.8",
				"executor_version":                    "v1.7.8",
				"agent_protocol_version":              "2",
				"executor_protocol_version":           2,
				"mutation_protocol_version":           2,
				"recovery_protocol_version": store.
					SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion,
				"execution_host_id":              activated.Ownership.ExecutionHostID,
				"ownership_epoch":                activated.Ownership.OwnershipEpoch,
				"source_policy_revision":         activated.Policy.Revision,
				"policy_revision":                activated.Policy.ProjectionRevision,
				"local_executor_policy_revision": activated.Policy.LocalExecutorPolicyRevision,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	userID := "self-update-user-" + fixture.suffix
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO users
(id, username, password_hash, status, created_at, updated_at)
VALUES (?, ?, ?, 'active', ?, ?)`,
		userID, userID, "not-used-by-this-test", now, now,
	); err != nil {
		t.Fatal(err)
	}
	params := store.CreateSystemUpdateHostSelfUpdateParams{
		ExecutionHostID:     activated.Ownership.ExecutionHostID,
		TargetVersion:       "v1.8.0",
		IdempotencyKey:      "mariadb-self-update-" + fixture.suffix,
		RequestedByUserID:   userID,
		RequestedByUsername: userID,
		Release:             mariaDBHostSelfUpdateRelease(now),
		Now:                 now,
	}
	update, created, err := fixture.updates.CreateSystemUpdateHostSelfUpdate(
		ctx, fixture.auth, fixture.policies, params,
	)
	if err != nil || !created {
		t.Fatalf("create = %#v created=%v err=%v", update, created, err)
	}
	if blocked, err := fixture.updates.HasSystemUpdateIdentityMutationFence(
		ctx, fixture.auth, update.AgentServiceID,
	); err != nil || !blocked {
		t.Fatalf("active self-update identity fence = %v, %v", blocked, err)
	}
	replay, created, err := fixture.updates.CreateSystemUpdateHostSelfUpdate(
		ctx, fixture.auth, fixture.policies, params,
	)
	if err != nil || created || replay.ID != update.ID {
		t.Fatalf("create replay = %#v created=%v err=%v", replay, created, err)
	}
	refreshedAttestation := params
	refreshedAttestation.Release.AttestationVerifiedAt =
		refreshedAttestation.Release.AttestationVerifiedAt.Add(time.Second)
	replay, created, err = fixture.updates.CreateSystemUpdateHostSelfUpdate(
		ctx,
		fixture.auth,
		fixture.policies,
		refreshedAttestation,
	)
	if err != nil || created || replay.ID != update.ID {
		t.Fatalf(
			"attestation timestamp changed immutable replay = %#v created=%v err=%v",
			replay,
			created,
			err,
		)
	}
	conflict := params
	conflict.IdempotencyKey += "-conflict"
	if _, _, err := fixture.updates.CreateSystemUpdateHostSelfUpdate(
		ctx, fixture.auth, fixture.policies, conflict,
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateBusy) {
		t.Fatalf("active host exclusion error = %v", err)
	}

	issued, err := fixture.updates.IssueSystemUpdateHostSelfUpdateGrant(
		ctx, fixture.auth, fixture.policies,
		store.IssueSystemUpdateHostSelfUpdateGrantParams{
			SelfUpdateID:     update.ID,
			ExecutionHostID:  update.ExecutionHostID,
			AgentServiceID:   update.AgentServiceID,
			ExpectedRevision: update.Revision,
			Operation:        store.SystemUpdateHostSelfUpdateGrantStage,
			PlanSHA256:       strings.Repeat("d", 64),
			SessionID:        "mariadb-stage-" + fixture.suffix,
			Now:              now.Add(time.Second),
			TTL:              time.Minute,
		},
	)
	if err != nil || issued.RawToken == "" {
		t.Fatalf("issue grant = %#v err=%v", issued, err)
	}
	if _, err := fixture.updates.IssueSystemUpdateHostSelfUpdateGrant(
		ctx,
		fixture.auth,
		fixture.policies,
		store.IssueSystemUpdateHostSelfUpdateGrantParams{
			SelfUpdateID:     update.ID,
			ExecutionHostID:  update.ExecutionHostID,
			AgentServiceID:   update.AgentServiceID,
			ExpectedRevision: update.Revision,
			Operation:        store.SystemUpdateHostSelfUpdateGrantStage,
			PlanSHA256:       strings.Repeat("e", 64),
			SessionID:        "mariadb-stage-" + fixture.suffix,
			Now:              now.Add(1500 * time.Millisecond),
			TTL:              time.Minute,
		},
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateGrant) {
		t.Fatalf("same Maria session accepted changed binding: %v", err)
	}
	wrong := issued.Grant
	wrong.PlanSHA256 = strings.Repeat("e", 64)
	if _, err := fixture.updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		ctx, fixture.auth, fixture.policies,
		store.ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: issued.RawToken,
			Binding:  wrong,
			Now:      now.Add(2 * time.Second),
		},
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateGrant) {
		t.Fatalf("wrong grant binding error = %v", err)
	}
	expiring, err := fixture.updates.IssueSystemUpdateHostSelfUpdateGrant(
		ctx, fixture.auth, fixture.policies,
		store.IssueSystemUpdateHostSelfUpdateGrantParams{
			SelfUpdateID:     update.ID,
			ExecutionHostID:  update.ExecutionHostID,
			AgentServiceID:   update.AgentServiceID,
			ExpectedRevision: update.Revision,
			Operation:        store.SystemUpdateHostSelfUpdateGrantStage,
			PlanSHA256:       strings.Repeat("f", 64),
			SessionID:        "mariadb-expiry-" + fixture.suffix,
			Now:              now.Add(3 * time.Second),
			TTL:              15 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		ctx, fixture.auth, fixture.policies,
		store.ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: expiring.RawToken,
			Binding:  expiring.Grant,
			Now:      now.Add(time.Minute),
		},
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateExpired) {
		t.Fatalf("expired grant error = %v", err)
	}
	consumed, err := fixture.updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		ctx, fixture.auth, fixture.policies,
		store.ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: issued.RawToken,
			Binding:  issued.Grant,
			Now:      now.Add(2 * time.Second),
		},
	)
	if err != nil || !consumed.Consumed {
		t.Fatalf("consume = %#v err=%v", consumed, err)
	}
	reserved, err := fixture.updates.GetSystemUpdateHostSelfUpdate(
		ctx,
		update.ID,
	)
	if err != nil ||
		reserved.Status != store.SystemUpdateHostSelfUpdateStaging ||
		reserved.Revision != update.Revision+1 ||
		reserved.StartedAt == nil ||
		consumed.Grant.StageClaimRevision != reserved.Revision ||
		consumed.Grant.StageClaimedAt == nil ||
		consumed.Grant.ConsumedAt == nil ||
		!consumed.Grant.StageClaimedAt.Equal(*consumed.Grant.ConsumedAt) {
		t.Fatalf(
			"Maria stage grant did not reserve the job: update=%#v consume=%#v err=%v",
			reserved,
			consumed,
			err,
		)
	}
	if _, err := fixture.updates.CancelSystemUpdateHostSelfUpdate(
		ctx,
		update.ID,
		userID,
		update.Revision,
		false,
		now.Add(3*time.Second),
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateStale) {
		t.Fatalf("Maria pre-claim cancel revision survived: %v", err)
	}
	if _, err := fixture.updates.CancelSystemUpdateHostSelfUpdate(
		ctx,
		update.ID,
		userID,
		reserved.Revision,
		true,
		now.Add(3*time.Second),
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateCancel) {
		t.Fatalf("Maria claimed stage was terminal-cancelable: %v", err)
	}
	replayedConsume, err :=
		fixture.updates.ConsumeSystemUpdateHostSelfUpdateGrant(
			ctx, fixture.auth, fixture.policies,
			store.ConsumeSystemUpdateHostSelfUpdateGrantParams{
				RawToken: issued.RawToken,
				Binding:  issued.Grant,
				Now:      now.Add(time.Hour),
			},
		)
	if err != nil || replayedConsume.Consumed ||
		replayedConsume.Grant.StageClaimRevision !=
			consumed.Grant.StageClaimRevision ||
		replayedConsume.Grant.StageClaimedAt == nil ||
		!replayedConsume.Grant.StageClaimedAt.Equal(
			*consumed.Grant.StageClaimedAt,
		) {
		t.Fatalf("consume replay = %#v err=%v", replayedConsume, err)
	}

	staging, _, err := fixture.updates.ObserveSystemUpdateHostSelfUpdate(
		ctx, store.SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:   update.ExecutionHostID,
			AgentServiceID:    update.AgentServiceID,
			ExpectedRevision:  reserved.Revision,
			Now:               now.Add(4 * time.Second),
			HeartbeatAt:       now.Add(4 * time.Second),
			Phase:             "staged",
			PendingGeneration: update.AttemptGeneration,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wrongGeneration := mariaDBHostSelfUpdateSuccessObservation(
		staging, now.Add(5*time.Second),
	)
	wrongGeneration.HeartbeatGeneration = "wrong-generation"
	if _, _, err := fixture.updates.ObserveSystemUpdateHostSelfUpdate(
		ctx, wrongGeneration,
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateStale) {
		t.Fatalf("stale generation error = %v", err)
	}
	rolledBack, _, err := fixture.updates.ObserveSystemUpdateHostSelfUpdate(
		ctx, store.SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:         staging.ExecutionHostID,
			AgentServiceID:          staging.AgentServiceID,
			ExpectedRevision:        staging.Revision,
			Now:                     now.Add(6 * time.Second),
			HeartbeatAt:             now.Add(6 * time.Second),
			AgentVersion:            staging.PreviousAgentVersion,
			AgentProtocolVersion:    staging.PreviousAgentProtocolVersion,
			ExecutorVersion:         staging.PreviousExecutorVersion,
			ExecutorProtocolVersion: staging.PreviousExecutorProtocolVersion,
			MutationProtocolVersion: staging.PreviousMutationProtocolVersion,
			RecoveryProtocolVersion: staging.PreviousRecoveryProtocolVersion,
			Phase:                   "stable",
			FailedGeneration:        staging.AttemptGeneration,
			ActiveAgentVersion:      staging.PreviousAgentVersion,
			ActiveExecutorVersion:   staging.PreviousExecutorVersion,
		},
	)
	if err != nil ||
		rolledBack.Status != store.SystemUpdateHostSelfUpdateRolledBack {
		t.Fatalf("rollback proof = %#v err=%v", rolledBack, err)
	}
	if _, err := fixture.updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		ctx,
		fixture.auth,
		fixture.policies,
		store.ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: issued.RawToken,
			Binding:  issued.Grant,
			Now:      now.Add(6500 * time.Millisecond),
		},
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateStale) &&
		!errors.Is(err, store.ErrSystemUpdateHostSelfUpdateState) {
		t.Fatalf("Maria terminal consumed grant replay error = %v", err)
	}
	if blocked, err := fixture.updates.HasSystemUpdateIdentityMutationFence(
		ctx, fixture.auth, rolledBack.AgentServiceID,
	); err != nil || blocked {
		t.Fatalf("terminal self-update identity fence = %v, %v", blocked, err)
	}

	retry, retryCreated, err :=
		fixture.updates.RetrySystemUpdateHostSelfUpdate(
			ctx, fixture.auth, fixture.policies,
			store.RetrySystemUpdateHostSelfUpdateParams{
				ID:                  rolledBack.ID,
				IdempotencyKey:      params.IdempotencyKey + "-retry",
				RequestedByUserID:   userID,
				RequestedByUsername: userID,
				Now:                 now.Add(7 * time.Second),
			},
		)
	if err != nil || !retryCreated {
		t.Fatalf("retry = %#v created=%v err=%v", retry, retryCreated, err)
	}
	retryStaging, _, err := fixture.updates.ObserveSystemUpdateHostSelfUpdate(
		ctx, store.SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:   retry.ExecutionHostID,
			AgentServiceID:    retry.AgentServiceID,
			ExpectedRevision:  retry.Revision,
			Now:               now.Add(8 * time.Second),
			HeartbeatAt:       now.Add(8 * time.Second),
			Phase:             "staged",
			PendingGeneration: retry.AttemptGeneration,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	succeeded, _, err := fixture.updates.ObserveSystemUpdateHostSelfUpdate(
		ctx, mariaDBHostSelfUpdateSuccessObservation(
			retryStaging, now.Add(9*time.Second),
		),
	)
	if err != nil ||
		succeeded.Status != store.SystemUpdateHostSelfUpdateSucceeded {
		t.Fatalf("success proof = %#v err=%v", succeeded, err)
	}

	cancelParams := params
	cancelParams.IdempotencyKey += "-cancel"
	cancelParams.Now = now.Add(10 * time.Second)
	cancelUpdate, _, err := fixture.updates.CreateSystemUpdateHostSelfUpdate(
		ctx, fixture.auth, fixture.policies, cancelParams,
	)
	if err != nil {
		t.Fatal(err)
	}
	cancelFirstGrant, err := fixture.updates.IssueSystemUpdateHostSelfUpdateGrant(
		ctx,
		fixture.auth,
		fixture.policies,
		store.IssueSystemUpdateHostSelfUpdateGrantParams{
			SelfUpdateID:     cancelUpdate.ID,
			ExecutionHostID:  cancelUpdate.ExecutionHostID,
			AgentServiceID:   cancelUpdate.AgentServiceID,
			ExpectedRevision: cancelUpdate.Revision,
			Operation:        store.SystemUpdateHostSelfUpdateGrantStage,
			PlanSHA256:       strings.Repeat("9", 64),
			SessionID:        "mariadb-cancel-first-" + fixture.suffix,
			Now:              now.Add(10500 * time.Millisecond),
			TTL:              time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("issue Maria cancel-first grant: %v", err)
	}
	canceled, err := fixture.updates.CancelSystemUpdateHostSelfUpdate(
		ctx, cancelUpdate.ID, userID, cancelUpdate.Revision,
		false, now.Add(11*time.Second),
	)
	if err != nil ||
		canceled.Status != store.SystemUpdateHostSelfUpdateCanceled {
		t.Fatalf("cancel = %#v err=%v", canceled, err)
	}
	if _, err := fixture.updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		ctx,
		fixture.auth,
		fixture.policies,
		store.ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: cancelFirstGrant.RawToken,
			Binding:  cancelFirstGrant.Grant,
			Now:      now.Add(12 * time.Second),
		},
	); !errors.Is(err, store.ErrSystemUpdateHostSelfUpdateStale) {
		t.Fatalf("Maria cancel-first consume was not rejected: %v", err)
	}
}

func mariaDBHostSelfUpdateRelease(
	now time.Time,
) store.SystemUpdateHostReleaseMetadata {
	return store.SystemUpdateHostReleaseMetadata{
		Tag:                     "v1.8.0",
		Commit:                  strings.Repeat("a", 40),
		PublishedAt:             now.Add(-24 * time.Hour),
		ManifestAssetID:         1,
		ManifestAssetName:       "host-agent-manifest.json",
		ManifestSHA256:          strings.Repeat("1", 64),
		ManifestChecksumAssetID: 2,
		ManifestChecksumSHA256:  strings.Repeat("2", 64),
		ArchiveAssetID:          3,
		ArchiveAssetName:        "autostream-host-agent_v1.8.0_linux_amd64.tar.gz",
		ArchiveSize:             4096,
		ArchiveSHA256:           strings.Repeat("3", 64),
		ArchiveChecksumAssetID:  4,
		ArchiveChecksumSHA256:   strings.Repeat("4", 64),
		Arch:                    "amd64",
		AgentProtocolVersion:    2,
		ExecutorProtocolVersion: 2,
		MutationProtocolVersion: 2,
		RecoveryProtocolVersion: store.
			SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion,
		MinimumPanelVersion:   "v1.8.0",
		AttestationVerifiedAt: now.Add(-time.Minute),
	}
}

func mariaDBHostSelfUpdateSuccessObservation(
	update store.SystemUpdateHostSelfUpdate,
	now time.Time,
) store.SystemUpdateHostSelfUpdateObservation {
	return store.SystemUpdateHostSelfUpdateObservation{
		ExecutionHostID:         update.ExecutionHostID,
		AgentServiceID:          update.AgentServiceID,
		ExpectedRevision:        update.Revision,
		Now:                     now,
		HeartbeatAt:             now,
		AgentVersion:            update.TargetVersion,
		AgentProtocolVersion:    update.Release.AgentProtocolVersion,
		ExecutorVersion:         update.TargetVersion,
		ExecutorProtocolVersion: update.Release.ExecutorProtocolVersion,
		MutationProtocolVersion: update.Release.MutationProtocolVersion,
		RecoveryProtocolVersion: update.Release.RecoveryProtocolVersion,
		Phase:                   "stable",
		HeartbeatGeneration:     update.AttemptGeneration,
		ActiveAgentVersion:      update.TargetVersion,
		ActiveExecutorVersion:   update.TargetVersion,
	}
}
