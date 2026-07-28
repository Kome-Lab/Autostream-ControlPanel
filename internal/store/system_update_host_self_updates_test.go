package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryHostSelfUpdateLifecycleStrictProofAndNoTimeoutTerminal(t *testing.T) {
	policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
	created, fresh, err := updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(), registry, policies, params,
	)
	if err != nil || !fresh {
		t.Fatalf("create = %#v fresh=%v err=%v", created, fresh, err)
	}
	if created.Status != SystemUpdateHostSelfUpdateQueued ||
		created.Revision != 1 ||
		created.AttemptGeneration == "" ||
		created.IssuedAt.Equal(created.Release.PublishedAt) {
		t.Fatalf("unexpected create: %#v", created)
	}
	replay, fresh, err := updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(), registry, policies, params,
	)
	if err != nil || fresh || replay.ID != created.ID {
		t.Fatalf("idempotent replay = %#v fresh=%v err=%v", replay, fresh, err)
	}
	refreshedAttestation := params
	refreshedAttestation.Release.AttestationVerifiedAt =
		refreshedAttestation.Release.AttestationVerifiedAt.Add(time.Second)
	replay, fresh, err = updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(),
		registry,
		policies,
		refreshedAttestation,
	)
	if err != nil || fresh || replay.ID != created.ID {
		t.Fatalf(
			"attestation timestamp changed immutable replay: %#v fresh=%v err=%v",
			replay,
			fresh,
			err,
		)
	}

	stalled, changed, err := updates.ObserveSystemUpdateHostSelfUpdate(
		t.Context(), SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:  created.ExecutionHostID,
			AgentServiceID:   created.AgentServiceID,
			ExpectedRevision: created.Revision,
			Now:              params.Now.Add(10 * time.Minute),
			HeartbeatAt:      params.Now,
		},
	)
	if err != nil || !changed ||
		stalled.ObservationState != SystemUpdateHostSelfUpdateObservationStalled ||
		stalled.Status != SystemUpdateHostSelfUpdateQueued ||
		stalled.CompletedAt != nil {
		t.Fatalf("stalled observation = %#v changed=%v err=%v", stalled, changed, err)
	}

	staging, changed, err := updates.ObserveSystemUpdateHostSelfUpdate(
		t.Context(), SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:   created.ExecutionHostID,
			AgentServiceID:    created.AgentServiceID,
			ExpectedRevision:  stalled.Revision,
			Now:               params.Now.Add(11 * time.Minute),
			HeartbeatAt:       params.Now.Add(11 * time.Minute),
			Phase:             "staged",
			PendingGeneration: created.AttemptGeneration,
		},
	)
	if err != nil || !changed ||
		staging.Status != SystemUpdateHostSelfUpdateStaging {
		t.Fatalf("staging = %#v changed=%v err=%v", staging, changed, err)
	}

	_, _, err = updates.ObserveSystemUpdateHostSelfUpdate(
		t.Context(), strictHostSelfUpdateSuccessObservation(
			staging, params.Now.Add(12*time.Minute),
			"wrong-generation",
		),
	)
	if !errors.Is(err, ErrSystemUpdateHostSelfUpdateStale) {
		t.Fatalf("wrong generation proof error = %v", err)
	}
	succeeded, changed, err := updates.ObserveSystemUpdateHostSelfUpdate(
		t.Context(), strictHostSelfUpdateSuccessObservation(
			staging, params.Now.Add(13*time.Minute),
			created.AttemptGeneration,
		),
	)
	if err != nil || !changed ||
		succeeded.Status != SystemUpdateHostSelfUpdateSucceeded ||
		succeeded.CompletedAt == nil {
		t.Fatalf("success proof = %#v changed=%v err=%v", succeeded, changed, err)
	}
}

func TestMemoryHostSelfUpdateCreateRequiresCompatibleCurrentProtocols(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"agent": func(capabilities map[string]any) {
			capabilities["agent_protocol_version"] = 1
		},
		"executor": func(capabilities map[string]any) {
			capabilities["executor_protocol_version"] = 1
		},
		"mutation": func(capabilities map[string]any) {
			capabilities["mutation_protocol_version"] = 1
		},
		"recovery": func(capabilities map[string]any) {
			capabilities["recovery_protocol_version"] = 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
			registry.mu.Lock()
			agent := registry.services["host-agent-a"]
			mutate(agent.ReportedCapabilities)
			registry.services[agent.ServiceID] = agent
			registry.mu.Unlock()

			if _, _, err := updates.CreateSystemUpdateHostSelfUpdate(
				t.Context(), registry, policies, params,
			); !errors.Is(err, ErrSystemUpdateAgentNotReady) {
				t.Fatalf("protocol below control-plane minimum error = %v", err)
			}
		})
	}
}

func TestMemoryHostSelfUpdateCreateDoesNotRequireFutureTargetProtocolsOnCurrentRuntime(t *testing.T) {
	policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
	params.Release.AgentProtocolVersion = 3
	params.Release.ExecutorProtocolVersion = 3
	params.Release.MutationProtocolVersion = 3
	params.Release.RecoveryProtocolVersion = 2

	update, created, err := updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(), registry, policies, params,
	)
	if err != nil || !created {
		t.Fatalf(
			"future target protocols were required on current runtime: update=%#v created=%v err=%v",
			update,
			created,
			err,
		)
	}
}

func TestMemoryHostSelfUpdateCreateRequiresStrictlyNewerSemanticVersion(t *testing.T) {
	for name, testCase := range map[string]struct {
		targetVersion   string
		agentVersion    string
		executorVersion string
		wantErr         error
	}{
		"same version": {
			targetVersion:   "v1.7.8",
			agentVersion:    "v1.7.8",
			executorVersion: "v1.7.8",
			wantErr:         ErrSystemUpdateAgentNotReady,
		},
		"downgrade": {
			targetVersion:   "v1.7.7",
			agentVersion:    "v1.7.8",
			executorVersion: "v1.7.8",
			wantErr:         ErrSystemUpdateAgentNotReady,
		},
		"executor newer than target": {
			targetVersion:   "v1.8.0",
			agentVersion:    "v1.7.8",
			executorVersion: "v1.8.1",
			wantErr:         ErrSystemUpdateAgentNotReady,
		},
		"invalid semver": {
			targetVersion:   "v01.8.0",
			agentVersion:    "v1.7.8",
			executorVersion: "v1.7.8",
			wantErr:         ErrInvalidSystemUpdateHostSelfUpdate,
		},
	} {
		t.Run(name, func(t *testing.T) {
			policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
			params.TargetVersion = testCase.targetVersion
			params.Release.Tag = testCase.targetVersion
			params.Release.ArchiveAssetName =
				"autostream-host-agent_" + testCase.targetVersion +
					"_linux_amd64.tar.gz"

			registry.mu.Lock()
			agent := registry.services["host-agent-a"]
			agent.ReportedVersion = testCase.agentVersion
			agent.ReportedCapabilities["executor_version"] =
				testCase.executorVersion
			registry.services[agent.ServiceID] = agent
			registry.mu.Unlock()

			if _, _, err := updates.CreateSystemUpdateHostSelfUpdate(
				t.Context(), registry, policies, params,
			); !errors.Is(err, testCase.wantErr) {
				t.Fatalf("create error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestMemoryHostSelfUpdateCreateRejectsInconsistentStableRuntimeVersions(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"agent": func(capabilities map[string]any) {
			capabilities["self_update_active_agent_version"] = "v1.7.7"
		},
		"executor": func(capabilities map[string]any) {
			capabilities["executor_version"] = "v1.7.9"
		},
	} {
		t.Run(name, func(t *testing.T) {
			policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
			params.TargetVersion = "v1.9.0"
			params.Release.Tag = params.TargetVersion
			params.Release.ArchiveAssetName =
				"autostream-host-agent_v1.9.0_linux_amd64.tar.gz"

			registry.mu.Lock()
			agent := registry.services["host-agent-a"]
			mutate(agent.ReportedCapabilities)
			registry.services[agent.ServiceID] = agent
			registry.mu.Unlock()

			if _, _, err := updates.CreateSystemUpdateHostSelfUpdate(
				t.Context(), registry, policies, params,
			); !errors.Is(err, ErrSystemUpdateAgentNotReady) {
				t.Fatalf("inconsistent stable runtime error = %v", err)
			}
		})
	}
}

func TestMemoryHostSelfUpdateRollbackUsesCapturedCurrentRuntimeContract(t *testing.T) {
	policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
	update, created, err := updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(), registry, policies, params,
	)
	if err != nil || !created {
		t.Fatalf("create = %#v created=%v err=%v", update, created, err)
	}
	staging, changed, err := updates.ObserveSystemUpdateHostSelfUpdate(
		t.Context(),
		SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:   update.ExecutionHostID,
			AgentServiceID:    update.AgentServiceID,
			ExpectedRevision:  update.Revision,
			Now:               params.Now.Add(time.Second),
			HeartbeatAt:       params.Now.Add(time.Second),
			Phase:             "staged",
			PendingGeneration: update.AttemptGeneration,
		},
	)
	if err != nil || !changed ||
		staging.Status != SystemUpdateHostSelfUpdateStaging {
		t.Fatalf("staging = %#v changed=%v err=%v", staging, changed, err)
	}
	rolledBack, changed, err := updates.ObserveSystemUpdateHostSelfUpdate(
		t.Context(),
		SystemUpdateHostSelfUpdateObservation{
			ExecutionHostID:         staging.ExecutionHostID,
			AgentServiceID:          staging.AgentServiceID,
			ExpectedRevision:        staging.Revision,
			Now:                     params.Now.Add(2 * time.Second),
			HeartbeatAt:             params.Now.Add(2 * time.Second),
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
	if err != nil || !changed ||
		rolledBack.Status != SystemUpdateHostSelfUpdateRolledBack ||
		rolledBack.CompletedAt == nil {
		t.Fatalf(
			"rollback = %#v changed=%v err=%v",
			rolledBack,
			changed,
			err,
		)
	}
	if _, err := updates.GetActiveSystemUpdateHostSelfUpdateByExecutionHost(
		t.Context(),
		rolledBack.ExecutionHostID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back job remained active: %v", err)
	}
}

func TestMemoryHostSelfUpdateConsumeLossTerminatesFromFreshStableRuntime(
	t *testing.T,
) {
	for _, consumeCommitted := range []bool{false, true} {
		name := "consume_not_committed"
		if consumeCommitted {
			name = "consume_committed"
		}
		t.Run(name, func(t *testing.T) {
			policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
			update, created, err := updates.CreateSystemUpdateHostSelfUpdate(
				t.Context(),
				registry,
				policies,
				params,
			)
			if err != nil || !created {
				t.Fatalf(
					"create = %#v created=%v err=%v",
					update,
					created,
					err,
				)
			}
			issued, err := updates.IssueSystemUpdateHostSelfUpdateGrant(
				t.Context(),
				registry,
				policies,
				IssueSystemUpdateHostSelfUpdateGrantParams{
					SelfUpdateID:     update.ID,
					ExecutionHostID:  update.ExecutionHostID,
					AgentServiceID:   update.AgentServiceID,
					ExpectedRevision: update.Revision,
					Operation:        SystemUpdateHostSelfUpdateGrantStage,
					PlanSHA256:       strings.Repeat("d", 64),
					SessionID:        "consume-loss-" + name,
					Now:              params.Now.Add(time.Second),
					TTL:              time.Minute,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if consumeCommitted {
				result, err := updates.ConsumeSystemUpdateHostSelfUpdateGrant(
					t.Context(),
					registry,
					policies,
					ConsumeSystemUpdateHostSelfUpdateGrantParams{
						RawToken: issued.RawToken,
						Binding:  issued.Grant,
						Now:      params.Now.Add(2 * time.Second),
					},
				)
				if err != nil || !result.Consumed {
					t.Fatalf("consume = %#v err=%v", result, err)
				}
				update, err =
					updates.GetSystemUpdateHostSelfUpdate(
						t.Context(),
						update.ID,
					)
				if err != nil ||
					update.Status != SystemUpdateHostSelfUpdateStaging {
					t.Fatalf("atomic staging = %#v err=%v", update, err)
				}
			}

			// This is the first observation from a fresh Host Agent and Local
			// Executor after root durably fenced the uncertain generation.
			rolledBack, changed, err :=
				updates.ObserveSystemUpdateHostSelfUpdate(
					t.Context(),
					SystemUpdateHostSelfUpdateObservation{
						ExecutionHostID:         update.ExecutionHostID,
						AgentServiceID:          update.AgentServiceID,
						ExpectedRevision:        update.Revision,
						Now:                     params.Now.Add(3 * time.Second),
						HeartbeatAt:             params.Now.Add(3 * time.Second),
						AgentVersion:            update.PreviousAgentVersion,
						AgentProtocolVersion:    update.PreviousAgentProtocolVersion,
						ExecutorVersion:         update.PreviousExecutorVersion,
						ExecutorProtocolVersion: update.PreviousExecutorProtocolVersion,
						MutationProtocolVersion: update.PreviousMutationProtocolVersion,
						RecoveryProtocolVersion: update.PreviousRecoveryProtocolVersion,
						Phase:                   "stable",
						FailedGeneration:        update.AttemptGeneration,
						ActiveAgentVersion:      update.PreviousAgentVersion,
						ActiveExecutorVersion:   update.PreviousExecutorVersion,
					},
				)
			if err != nil || !changed ||
				rolledBack.Status != SystemUpdateHostSelfUpdateRolledBack ||
				rolledBack.CompletedAt == nil {
				t.Fatalf(
					"fresh stable failure proof = %#v changed=%v err=%v",
					rolledBack,
					changed,
					err,
				)
			}
			if _, err := updates.ConsumeSystemUpdateHostSelfUpdateGrant(
				t.Context(),
				registry,
				policies,
				ConsumeSystemUpdateHostSelfUpdateGrantParams{
					RawToken: issued.RawToken,
					Binding:  issued.Grant,
					Now:      params.Now.Add(4 * time.Second),
				},
			); !errors.Is(err, ErrSystemUpdateHostSelfUpdateState) &&
				!errors.Is(err, ErrSystemUpdateHostSelfUpdateStale) {
				t.Fatalf("late terminal consume error = %v", err)
			}
		})
	}
}

func TestConsumedHostSelfUpdateGrantRejectsEveryTerminalParent(t *testing.T) {
	consumedAt := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	for _, status := range []string{
		SystemUpdateHostSelfUpdateSucceeded,
		SystemUpdateHostSelfUpdateRolledBack,
		SystemUpdateHostSelfUpdateFailed,
		SystemUpdateHostSelfUpdateCanceled,
	} {
		for _, operation := range []string{
			SystemUpdateHostSelfUpdateGrantStage,
			SystemUpdateHostSelfUpdateGrantReconcile,
		} {
			t.Run(status+"_"+operation, func(t *testing.T) {
				update := SystemUpdateHostSelfUpdate{
					ID:                "self-update-terminal",
					AttemptGeneration: "generation-terminal",
					Status:            status,
					Revision:          2,
				}
				grant := SystemUpdateHostSelfUpdateGrant{
					SelfUpdateID:               update.ID,
					AttemptGeneration:          update.AttemptGeneration,
					Operation:                  operation,
					ExpectedSelfUpdateRevision: 1,
					ConsumedAt:                 &consumedAt,
				}
				if operation == SystemUpdateHostSelfUpdateGrantStage {
					grant.StageClaimRevision = 2
					grant.StageClaimedAt = &consumedAt
				}
				if err := validateConsumedSystemUpdateHostSelfUpdateGrant(
					grant,
					update,
				); !errors.Is(
					err,
					ErrSystemUpdateHostSelfUpdateStale,
				) {
					t.Fatalf(
						"terminal %s replay error = %v",
						operation,
						err,
					)
				}
			})
		}
	}
}

func TestMemoryHostSelfUpdateGrantExactBindingAndResponseLossReplay(t *testing.T) {
	policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
	update, _, err := updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(), registry, policies, params,
	)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := updates.IssueSystemUpdateHostSelfUpdateGrant(
		t.Context(), registry, policies,
		IssueSystemUpdateHostSelfUpdateGrantParams{
			SelfUpdateID:     update.ID,
			ExecutionHostID:  update.ExecutionHostID,
			AgentServiceID:   update.AgentServiceID,
			ExpectedRevision: update.Revision,
			Operation:        SystemUpdateHostSelfUpdateGrantStage,
			PlanSHA256:       strings.Repeat("d", 64),
			SessionID:        "session-stage-a",
			Now:              params.Now.Add(time.Second),
			TTL:              time.Minute,
		},
	)
	if err != nil || !issued.Issued ||
		!strings.HasPrefix(issued.RawToken, "ast_hsug_") ||
		issued.Grant.tokenHash != "" {
		t.Fatalf("issue = %#v err=%v", issued, err)
	}
	if persisted := updates.hostSelfUpdateGrants[issued.Grant.ID]; persisted.tokenHash == "" {
		t.Fatal("grant hash was not persisted")
	} else if strings.Contains(persisted.tokenHash, issued.RawToken) {
		t.Fatal("raw grant token survived in persistent state")
	}
	changedSessionBinding := IssueSystemUpdateHostSelfUpdateGrantParams{
		SelfUpdateID:     update.ID,
		ExecutionHostID:  update.ExecutionHostID,
		AgentServiceID:   update.AgentServiceID,
		ExpectedRevision: update.Revision,
		Operation:        SystemUpdateHostSelfUpdateGrantStage,
		PlanSHA256:       strings.Repeat("e", 64),
		SessionID:        "session-stage-a",
		Now:              params.Now.Add(1500 * time.Millisecond),
		TTL:              time.Minute,
	}
	if _, err := updates.IssueSystemUpdateHostSelfUpdateGrant(
		t.Context(),
		registry,
		policies,
		changedSessionBinding,
	); !errors.Is(err, ErrSystemUpdateHostSelfUpdateGrant) {
		t.Fatalf("same session accepted changed binding: %v", err)
	}

	wrong := issued.Grant
	wrong.ReleaseCommit = strings.Repeat("b", 40)
	if _, err := updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		t.Context(), registry, policies,
		ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: issued.RawToken,
			Binding:  wrong,
			Now:      params.Now.Add(2 * time.Second),
		},
	); !errors.Is(err, ErrSystemUpdateHostSelfUpdateGrant) {
		t.Fatalf("wrong exact binding error = %v", err)
	}
	consumed, err := updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		t.Context(), registry, policies,
		ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: issued.RawToken,
			Binding:  issued.Grant,
			Now:      params.Now.Add(2 * time.Second),
		},
	)
	if err != nil || !consumed.Consumed || consumed.Grant.ConsumedAt == nil {
		t.Fatalf("consume = %#v err=%v", consumed, err)
	}
	reserved, err := updates.GetSystemUpdateHostSelfUpdate(
		t.Context(),
		update.ID,
	)
	if err != nil ||
		reserved.Status != SystemUpdateHostSelfUpdateStaging ||
		reserved.Revision != update.Revision+1 ||
		reserved.StartedAt == nil ||
		consumed.Grant.StageClaimRevision != reserved.Revision ||
		consumed.Grant.StageClaimedAt == nil ||
		!consumed.Grant.StageClaimedAt.Equal(*consumed.Grant.ConsumedAt) {
		t.Fatalf(
			"stage grant did not durably reserve the job: update=%#v consume=%#v err=%v",
			reserved,
			consumed,
			err,
		)
	}
	if _, err := updates.CancelSystemUpdateHostSelfUpdate(
		t.Context(),
		update.ID,
		"admin-a",
		update.Revision,
		false,
		params.Now.Add(3*time.Second),
	); !errors.Is(err, ErrSystemUpdateHostSelfUpdateStale) {
		t.Fatalf("pre-claim cancel revision survived stage claim: %v", err)
	}
	if _, err := updates.CancelSystemUpdateHostSelfUpdate(
		t.Context(),
		update.ID,
		"admin-a",
		reserved.Revision,
		true,
		params.Now.Add(3*time.Second),
	); !errors.Is(err, ErrSystemUpdateHostSelfUpdateCancel) {
		t.Fatalf("claimed stage was terminal-cancelable: %v", err)
	}
	replayed, err := updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		t.Context(), registry, policies,
		ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: issued.RawToken,
			Binding:  issued.Grant,
			Now:      params.Now.Add(30 * time.Minute),
		},
	)
	if err != nil || replayed.Consumed ||
		replayed.Grant.ConsumedAt == nil ||
		!replayed.Grant.ConsumedAt.Equal(*consumed.Grant.ConsumedAt) ||
		replayed.Grant.StageClaimRevision !=
			consumed.Grant.StageClaimRevision ||
		replayed.Grant.StageClaimedAt == nil ||
		!replayed.Grant.StageClaimedAt.Equal(
			*consumed.Grant.StageClaimedAt,
		) {
		t.Fatalf("consume response-loss replay = %#v err=%v", replayed, err)
	}
}

func TestMemoryHostSelfUpdateQueuedCancelAndActiveHostExclusion(t *testing.T) {
	policies, registry, updates, params := readyMemoryHostSelfUpdate(t)
	update, _, err := updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(), registry, policies, params,
	)
	if err != nil {
		t.Fatal(err)
	}
	conflict := params
	conflict.IdempotencyKey = "self-update-conflict"
	if _, _, err := updates.CreateSystemUpdateHostSelfUpdate(
		t.Context(), registry, policies, conflict,
	); !errors.Is(err, ErrSystemUpdateHostSelfUpdateBusy) {
		t.Fatalf("active host error = %v", err)
	}
	issued, err := updates.IssueSystemUpdateHostSelfUpdateGrant(
		t.Context(),
		registry,
		policies,
		IssueSystemUpdateHostSelfUpdateGrantParams{
			SelfUpdateID:     update.ID,
			ExecutionHostID:  update.ExecutionHostID,
			AgentServiceID:   update.AgentServiceID,
			ExpectedRevision: update.Revision,
			Operation:        SystemUpdateHostSelfUpdateGrantStage,
			PlanSHA256:       strings.Repeat("d", 64),
			SessionID:        "cancel-first-stage",
			Now:              params.Now.Add(500 * time.Millisecond),
			TTL:              time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("issue pre-cancel grant: %v", err)
	}
	canceled, err := updates.CancelSystemUpdateHostSelfUpdate(
		t.Context(), update.ID, "admin-a", update.Revision,
		false, params.Now.Add(time.Second),
	)
	if err != nil || canceled.Status != SystemUpdateHostSelfUpdateCanceled ||
		canceled.CompletedAt == nil {
		t.Fatalf("cancel = %#v err=%v", canceled, err)
	}
	if _, err := updates.ConsumeSystemUpdateHostSelfUpdateGrant(
		t.Context(),
		registry,
		policies,
		ConsumeSystemUpdateHostSelfUpdateGrantParams{
			RawToken: issued.RawToken,
			Binding:  issued.Grant,
			Now:      params.Now.Add(2 * time.Second),
		},
	); !errors.Is(err, ErrSystemUpdateHostSelfUpdateStale) {
		t.Fatalf("cancel-first grant consume was not rejected: %v", err)
	}
}

func readyMemoryHostSelfUpdate(
	t *testing.T,
) (
	*MemoryUpdaterPolicyStore,
	*MemoryAuthStore,
	*MemorySystemUpdateStore,
	CreateSystemUpdateHostSelfUpdateParams,
) {
	t.Helper()
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	registry.mu.Lock()
	agent := registry.services["host-agent-a"]
	agent.ReportedVersion = "v1.7.8"
	agent.ReportedOS = "linux"
	agent.ReportedArch = "amd64"
	agent.ReportedCapabilities["self_update_ready"] = true
	agent.ReportedCapabilities["self_update_phase"] = "stable"
	agent.ReportedCapabilities["self_update_active_agent_version"] = "v1.7.8"
	agent.ReportedCapabilities["self_update_active_executor_version"] = "v1.7.8"
	agent.ReportedCapabilities["executor_version"] = "v1.7.8"
	agent.ReportedCapabilities["agent_protocol_version"] = "2"
	agent.ReportedCapabilities["executor_protocol_version"] = 2
	agent.ReportedCapabilities["mutation_protocol_version"] = 2
	agent.ReportedCapabilities["recovery_protocol_version"] =
		SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion
	agent.ReportedCapabilities["recovery_pending"] = false
	registry.services[agent.ServiceID] = agent
	registry.mu.Unlock()
	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	return policies, registry, updates, CreateSystemUpdateHostSelfUpdateParams{
		ExecutionHostID:     "host-a",
		TargetVersion:       "v1.8.0",
		IdempotencyKey:      "self-update-a",
		RequestedByUserID:   "admin-a",
		RequestedByUsername: "admin",
		Release: SystemUpdateHostReleaseMetadata{
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
			ArchiveSize:             1024,
			ArchiveSHA256:           strings.Repeat("3", 64),
			ArchiveChecksumAssetID:  4,
			ArchiveChecksumSHA256:   strings.Repeat("4", 64),
			Arch:                    "amd64",
			AgentProtocolVersion:    2,
			ExecutorProtocolVersion: 2,
			MutationProtocolVersion: 2,
			RecoveryProtocolVersion: SystemUpdateHostSelfUpdateMinimumRecoveryProtocolVersion,
			MinimumPanelVersion:     "v1.8.0",
			AttestationVerifiedAt:   now.Add(-time.Minute),
		},
		Now: now,
	}
}

func strictHostSelfUpdateSuccessObservation(
	update SystemUpdateHostSelfUpdate,
	now time.Time,
	generation string,
) SystemUpdateHostSelfUpdateObservation {
	return SystemUpdateHostSelfUpdateObservation{
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
		HeartbeatGeneration:     generation,
		ActiveAgentVersion:      update.TargetVersion,
		ActiveExecutorVersion:   update.TargetVersion,
	}
}
