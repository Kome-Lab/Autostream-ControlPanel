package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

func TestMemoryRuntimeTokenRotationLifecycleAndResponseLossReplay(t *testing.T) {
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	stageParams, seal, unseal, oldRaw := readyMemoryRuntimeTokenRotationFixture(
		t, policies, registry, updates, "rotation-lifecycle",
	)

	staged, err := updates.StageSystemUpdateRuntimeTokenRotation(
		t.Context(), registry, policies, stageParams, seal,
	)
	if err != nil || !staged.Created {
		t.Fatalf("stage = %#v err=%v", staged, err)
	}
	if staged.Rotation.Status != SystemUpdateRuntimeTokenRotationStaged ||
		staged.Rotation.Revision != 1 ||
		staged.Rotation.PreviousTokenID == staged.Rotation.StagedTokenID {
		t.Fatalf("unexpected staged rotation: %#v", staged.Rotation)
	}
	persisted := updates.runtimeTokenRotations[staged.Rotation.ID]
	if persisted.stagedTokenCiphertext == "" ||
		persisted.stagedTokenNonce == "" ||
		persisted.stagedTokenHash == "" ||
		registry.serviceTokens[staged.Rotation.StagedTokenID].RawToken != "" {
		t.Fatal("raw staged token survived in durable memory state")
	}

	replayedStage, err := updates.StageSystemUpdateRuntimeTokenRotation(
		t.Context(), registry, policies, stageParams, seal,
	)
	if err != nil || replayedStage.Created ||
		replayedStage.Rotation.ID != staged.Rotation.ID {
		t.Fatalf("stage replay = %#v err=%v", replayedStage, err)
	}

	preClaimToken, err := runtimeTokenRotationReplayToken(persisted, unseal)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
		t.Context(), registry, policies,
		MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
			ExpectedRevision: 1, RawStagedToken: preClaimToken.RawToken,
			Now: stageParams.Now.Add(time.Second),
		},
	); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationTransition) {
		t.Fatalf("local stage was accepted before claim: %v", err)
	}

	claimParams := ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
		RotationID: staged.Rotation.ID, ServiceID: stageParams.ServiceID,
		ExecutionHostID:              stageParams.ExecutionHostID,
		AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
		ClaimID:                      "00000000-0000-4000-8000-000000000001",
		ExpectedRevision:             1,
		Now:                          stageParams.Now.Add(time.Second),
	}
	for name, invalid := range map[string]ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
		"wrong host": func() ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.ExecutionHostID = "host-b"
			return value
		}(),
		"wrong service": func() ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.ServiceID = "host-agent-b"
			return value
		}(),
		"wrong previous token": func() ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.AuthenticatedPreviousTokenID = staged.Rotation.StagedTokenID
			return value
		}(),
		"wrong revision": func() ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams {
			value := claimParams
			value.ExpectedRevision = 99
			return value
		}(),
	} {
		t.Run("claim "+name, func(t *testing.T) {
			if _, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
				t.Context(), registry, policies, invalid, unseal,
			); err == nil {
				t.Fatalf("%s claim unexpectedly succeeded", name)
			}
		})
	}
	if updates.runtimeTokenRotations[staged.Rotation.ID].CredentialClaimedAt != nil {
		t.Fatal("failed claim left a durable claim marker")
	}
	if _, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		t.Context(), registry, policies, claimParams,
		func(_, _ string) (string, error) {
			return "", errors.New("test unseal failure")
		},
	); err == nil {
		t.Fatal("unseal failure unexpectedly claimed credential")
	}
	if updates.runtimeTokenRotations[staged.Rotation.ID].CredentialClaimedAt != nil {
		t.Fatal("unseal failure left a durable claim marker")
	}
	claimed, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		t.Context(), registry, policies, claimParams, unseal,
	)
	if err != nil || !claimed.Claimed || claimed.Rotation.Revision != 2 ||
		claimed.Token.RawToken == "" ||
		claimed.Token.ID != staged.Rotation.StagedTokenID {
		t.Fatalf("claim = %#v err=%v", claimed, err)
	}
	rawStagedToken := claimed.Token.RawToken
	if _, err := registry.AuthenticateServiceToken(
		t.Context(), rawStagedToken, "updates.claim",
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("staged token authenticated before activation: %v", err)
	}
	claimReplay, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		t.Context(), registry, policies, claimParams, unseal,
	)
	if err != nil || claimReplay.Claimed ||
		claimReplay.Token.RawToken != rawStagedToken ||
		claimReplay.Rotation.Revision != claimed.Rotation.Revision {
		t.Fatalf("claim replay = %#v err=%v", claimReplay, err)
	}
	differentClaim := claimParams
	differentClaim.ClaimID = "00000000-0000-4000-8000-000000000002"
	if _, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		t.Context(), registry, policies, differentClaim, unseal,
	); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationCredentialClaimed) {
		t.Fatalf("different claim ID error = %v", err)
	}
	currentRevisionClaim := claimParams
	currentRevisionClaim.ExpectedRevision = claimed.Rotation.Revision
	if _, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
		t.Context(), registry, policies, currentRevisionClaim, unseal,
	); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationStale) {
		t.Fatalf("current revision reclaimed credential: %v", err)
	}
	active, err := updates.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		t.Context(), stageParams.ExecutionHostID,
	)
	if err != nil || active.ID != staged.Rotation.ID ||
		active.CredentialClaimedAt == nil ||
		active.stagedTokenCiphertext != "" ||
		active.credentialClaimIDHash != "" {
		t.Fatalf("active public metadata = %#v err=%v", active, err)
	}

	local, applied, err := updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
		t.Context(), registry, policies,
		MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
			ExpectedRevision: 2, RawStagedToken: rawStagedToken,
			Now: stageParams.Now.Add(2 * time.Second),
		},
	)
	if err != nil || !applied || local.Revision != 3 ||
		local.LocalStageReceiptID != "staged-token:"+staged.Rotation.StagedTokenID {
		t.Fatalf("local stage = %#v applied=%v err=%v", local, applied, err)
	}
	localReplay, applied, err := updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
		t.Context(), registry, policies,
		MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
			ExpectedRevision: 2, RawStagedToken: rawStagedToken,
			Now: stageParams.Now.Add(3 * time.Second),
		},
	)
	if err != nil || applied || localReplay.Revision != local.Revision {
		t.Fatalf("local stage replay = %#v applied=%v err=%v", localReplay, applied, err)
	}
	proofParams := readyMemoryRuntimeTokenRotationHeartbeatProof(
		t, policies, registry, stageParams, local, rawStagedToken,
		stageParams.Now.Add(4*time.Second),
	)
	registry.mu.Lock()
	equalHeartbeatService := registry.services[stageParams.ServiceID]
	equalHeartbeatService.LastHeartbeatAt = cloneTimePtr(local.LocalStageAcknowledgedAt)
	registry.services[stageParams.ServiceID] = equalHeartbeatService
	registry.mu.Unlock()
	if _, _, err := updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		t.Context(), registry, policies, proofParams,
	); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationHeartbeatProof) {
		t.Fatalf("equal local-stage heartbeat proof error = %v", err)
	}
	registry.mu.Lock()
	freshHeartbeatService := registry.services[stageParams.ServiceID]
	freshHeartbeatService.LastHeartbeatAt = cloneTimePtr(&proofParams.Now)
	registry.services[stageParams.ServiceID] = freshHeartbeatService
	registry.mu.Unlock()
	for name, proof := range map[string]ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
		"wrong token": func() ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
			value := proofParams
			value.RawStagedToken = oldRaw
			return value
		}(),
		"wrong host": func() ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
			value := proofParams
			value.ExecutionHostID = "host-b"
			return value
		}(),
		"wrong revision": func() ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
			value := proofParams
			value.ExpectedRevision = 99
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
				t.Context(), registry, policies, proof,
			); err == nil {
				t.Fatalf("%s proof unexpectedly succeeded", name)
			}
		})
	}
	proved, applied, err := updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		t.Context(), registry, policies, proofParams,
	)
	if err != nil || !applied || proved.Revision != 4 {
		t.Fatalf("prove = %#v applied=%v err=%v", proved, applied, err)
	}
	provedReplay, applied, err := updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
		t.Context(), registry, policies, proofParams,
	)
	if err != nil || applied || provedReplay.Revision != proved.Revision {
		t.Fatalf("proof replay = %#v applied=%v err=%v", provedReplay, applied, err)
	}
	activated, applied, err := updates.ActivateSystemUpdateRuntimeTokenRotation(
		t.Context(), registry, ActivateSystemUpdateRuntimeTokenRotationParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
			ExpectedRevision: 4, RawStagedToken: rawStagedToken,
			Now: stageParams.Now.Add(6 * time.Second),
		},
	)
	if err != nil || !applied ||
		activated.Status != SystemUpdateRuntimeTokenRotationActivated ||
		activated.Revision != 5 {
		t.Fatalf("activate = %#v applied=%v err=%v", activated, applied, err)
	}
	activatedReplay, applied, err := updates.ActivateSystemUpdateRuntimeTokenRotation(
		t.Context(), registry, ActivateSystemUpdateRuntimeTokenRotationParams{
			RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
			ExpectedRevision: 4, RawStagedToken: rawStagedToken,
			Now: stageParams.Now.Add(7 * time.Second),
		},
	)
	if err != nil || applied || activatedReplay.Revision != activated.Revision {
		t.Fatalf("activation replay = %#v applied=%v err=%v", activatedReplay, applied, err)
	}
	terminal := updates.runtimeTokenRotations[staged.Rotation.ID]
	if terminal.stagedTokenCiphertext != "" ||
		terminal.stagedTokenNonce != "" ||
		terminal.credentialClaimIDHash != "" ||
		terminal.credentialClaimRevision != 0 {
		t.Fatalf("activated replay secrets survived: %#v", terminal)
	}
	if _, err := registry.AuthenticateServiceToken(
		t.Context(), oldRaw, "updates.claim",
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token remained active: %v", err)
	}
	if token, err := registry.AuthenticateServiceToken(
		t.Context(), rawStagedToken, "updates.claim",
	); err != nil || token.ID != staged.Rotation.StagedTokenID {
		t.Fatalf("new token not active: token=%#v err=%v", token, err)
	}
	service, err := registry.GetService(t.Context(), stageParams.ServiceID)
	if err != nil || service.TokenID != staged.Rotation.StagedTokenID ||
		service.NodeTokenCiphertext == "" || service.NodeTokenNonce == "" {
		t.Fatalf("service token activation = %#v err=%v", service, err)
	}
	if _, err := updates.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
		t.Context(), stageParams.ExecutionHostID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("activated rotation remained active: %v", err)
	}
}

func TestMemoryRuntimeTokenRotationNormalCancellationScrubsReplaySecrets(t *testing.T) {
	for _, claimedBeforeCancel := range []bool{false, true} {
		name := "unclaimed immediate"
		if claimedBeforeCancel {
			name = "claimed acknowledged"
		}
		t.Run(name, func(t *testing.T) {
			policies, registry, updates := readyMemorySystemdPortCoordinator(t)
			params, seal, unseal, _ := readyMemoryRuntimeTokenRotationFixture(
				t, policies, registry, updates,
				"rotation-normal-cancel-"+strings.ReplaceAll(name, " ", "-"),
			)
			staged, err := updates.StageSystemUpdateRuntimeTokenRotation(
				t.Context(), registry, policies, params, seal,
			)
			if err != nil {
				t.Fatal(err)
			}
			current := staged.Rotation
			if claimedBeforeCancel {
				claimed, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
					t.Context(), registry, policies,
					ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
						RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
						ExecutionHostID:              params.ExecutionHostID,
						AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
						ClaimID:                      "00000000-0000-4000-8000-000000000005",
						ExpectedRevision:             current.Revision,
						Now:                          params.Now.Add(time.Second),
					},
					unseal,
				)
				if err != nil {
					t.Fatal(err)
				}
				current = claimed.Rotation
			}
			cancelParams := CancelSystemUpdateRuntimeTokenRotationParams{
				RotationID:       staged.Rotation.ID,
				ExecutionHostID:  params.ExecutionHostID,
				ExpectedRevision: current.Revision,
				Now:              params.Now.Add(2 * time.Second),
			}
			canceled, applied, err := updates.CancelSystemUpdateRuntimeTokenRotation(
				t.Context(), registry, cancelParams,
			)
			if err != nil || !applied {
				t.Fatalf("cancel = %#v applied=%v err=%v", canceled, applied, err)
			}
			if claimedBeforeCancel {
				if canceled.Status != SystemUpdateRuntimeTokenRotationCancelRequested {
					t.Fatalf("cancel request = %#v", canceled)
				}
				ackParams := AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams{
					RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
					ExecutionHostID:              params.ExecutionHostID,
					AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
					ExpectedRevision:             canceled.Revision,
					Now:                          params.Now.Add(3 * time.Second),
				}
				canceled, applied, err = updates.AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
					t.Context(), registry, policies, ackParams,
				)
				if err != nil || !applied {
					t.Fatalf(
						"cancel acknowledgement = %#v applied=%v err=%v",
						canceled,
						applied,
						err,
					)
				}
				replayed, replayApplied, replayErr :=
					updates.AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
						t.Context(), registry, policies, ackParams,
					)
				if replayErr != nil || replayApplied ||
					replayed.Revision != canceled.Revision {
					t.Fatalf(
						"cancel acknowledgement replay = %#v applied=%v err=%v",
						replayed,
						replayApplied,
						replayErr,
					)
				}
			} else {
				replayed, replayApplied, replayErr :=
					updates.CancelSystemUpdateRuntimeTokenRotation(
						t.Context(), registry, cancelParams,
					)
				if replayErr != nil || replayApplied ||
					replayed.Revision != canceled.Revision {
					t.Fatalf(
						"cancel replay = %#v applied=%v err=%v",
						replayed,
						replayApplied,
						replayErr,
					)
				}
			}
			if canceled.Status != SystemUpdateRuntimeTokenRotationCanceled {
				t.Fatalf("terminal cancel = %#v", canceled)
			}
			durable := updates.runtimeTokenRotations[staged.Rotation.ID]
			if durable.stagedTokenCiphertext != "" ||
				durable.stagedTokenNonce != "" ||
				durable.credentialClaimIDHash != "" ||
				durable.credentialClaimRevision != 0 {
				t.Fatalf("normal cancel replay secrets survived: %#v", durable)
			}
		})
	}
}

func TestMemoryRuntimeTokenRotationBlocksJobsAndRejectsUnsafeStage(t *testing.T) {
	t.Run("active rotation blocks both job types", func(t *testing.T) {
		policies, registry, updates := readyMemorySystemdPortCoordinator(t)
		params, seal, _, _ := readyMemoryRuntimeTokenRotationFixture(
			t, policies, registry, updates, "rotation-blocks-jobs",
		)
		staged, err := updates.StageSystemUpdateRuntimeTokenRotation(
			t.Context(), registry, policies, params, seal,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: "worker-a", TargetServiceType: "worker",
			Operation:      SystemUpdateOperationSoftwareUpdate,
			AgentServiceID: params.ServiceID, ExecutionHostID: params.ExecutionHostID,
			DeploymentMode: "systemd", CurrentVersion: "v1.0.0",
			TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle,
			IdempotencyKey: "job-during-rotation", RequestedByUserID: "admin",
		}); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationBusy) {
			t.Fatalf("software job error = %v", err)
		}
		target, err := registry.GetService(t.Context(), "worker-a")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.CreateSystemdPortReconfigurationJob(
			t.Context(), registry, policies, CreateSystemdPortReconfigurationJobParams{
				TargetID: "worker-a", NewPort: 18084,
				ExpectedEndpointRevision: target.EndpointRevision,
				IdempotencyKey:           "port-during-rotation",
				RequestedByUserID:        "admin",
			},
		); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationBusy) {
			t.Fatalf("port job error = %v", err)
		}
		policy, err := policies.GetUpdaterPolicy(t.Context(), params.ServiceID)
		if err != nil {
			t.Fatal(err)
		}
		policy.PollIntervalSeconds++
		if _, err := policies.SavePullUpdaterPolicy(
			t.Context(), updates, params.ServiceID, policy.Revision,
			params.ExpectedOwnershipEpoch, policy,
		); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationBusy) {
			t.Fatalf("policy mutation error = %v", err)
		}
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			t.Context(), params.ExecutionHostID, params.ExpectedOwnershipEpoch,
			SystemUpdateTransportPullV2, params.ServiceID,
			params.ExpectedProjectionRevision,
		); !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationBusy) {
			t.Fatalf("ownership mutation error = %v", err)
		}
		if _, _, err := updates.CancelSystemUpdateRuntimeTokenRotation(
			t.Context(), registry, CancelSystemUpdateRuntimeTokenRotationParams{
				RotationID: staged.Rotation.ID, ExecutionHostID: params.ExecutionHostID,
				ExpectedRevision: 1, Now: params.Now.Add(time.Second),
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updates.CreateSystemUpdateJob(t.Context(), CreateSystemUpdateJobParams{
			TargetID: "worker-a", TargetServiceType: "worker",
			Operation:      SystemUpdateOperationSoftwareUpdate,
			AgentServiceID: params.ServiceID, ExecutionHostID: params.ExecutionHostID,
			DeploymentMode: "systemd", CurrentVersion: "v1.0.0",
			TargetVersion: "v1.1.0", Strategy: SystemUpdateStrategyWhenIdle,
			IdempotencyKey: "job-after-cancel", RequestedByUserID: "admin",
		}); err != nil {
			t.Fatalf("job remained blocked after cancel: %v", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*MemoryUpdaterPolicyStore, *MemoryAuthStore, *MemorySystemUpdateStore, *StageSystemUpdateRuntimeTokenRotationParams)
		want   error
	}{
		{
			name: "configure stage",
			mutate: func(_ *MemoryUpdaterPolicyStore, registry *MemoryAuthStore, _ *MemorySystemUpdateStore, params *StageSystemUpdateRuntimeTokenRotationParams) {
				service := registry.services[params.ServiceID]
				service.StagedNodeTokenID = "configure-stage-token"
				registry.services[params.ServiceID] = service
			},
			want: ErrConflict,
		},
		{
			name: "shared old token",
			mutate: func(_ *MemoryUpdaterPolicyStore, registry *MemoryAuthStore, _ *MemorySystemUpdateStore, params *StageSystemUpdateRuntimeTokenRotationParams) {
				service := registry.services[params.ServiceID]
				other := registry.services["worker-a"]
				other.TokenID = service.TokenID
				registry.services[other.ServiceID] = other
			},
			want: ErrSystemUpdateRuntimeTokenRotationSharedToken,
		},
		{
			name: "ownership policy mismatch",
			mutate: func(_ *MemoryUpdaterPolicyStore, _ *MemoryAuthStore, _ *MemorySystemUpdateStore, params *StageSystemUpdateRuntimeTokenRotationParams) {
				params.ExpectedProjectionRevision++
			},
			want: ErrSystemUpdateOwnershipConflict,
		},
		{
			name: "active job",
			mutate: func(_ *MemoryUpdaterPolicyStore, _ *MemoryAuthStore, updates *MemorySystemUpdateStore, params *StageSystemUpdateRuntimeTokenRotationParams) {
				updates.jobs["active"] = SystemUpdateJob{
					ID: "active", ExecutionHostID: params.ExecutionHostID,
					Status: SystemUpdateStatusQueued,
				}
			},
			want: ErrSystemUpdateExecutionHostBusy,
		},
		{
			name: "active host self update",
			mutate: func(_ *MemoryUpdaterPolicyStore, _ *MemoryAuthStore, updates *MemorySystemUpdateStore, params *StageSystemUpdateRuntimeTokenRotationParams) {
				updates.hostSelfUpdates["self-update-active"] = SystemUpdateHostSelfUpdate{
					ID: "self-update-active", ExecutionHostID: params.ExecutionHostID,
					Status: SystemUpdateHostSelfUpdateStaging,
				}
			},
			want: ErrSystemUpdateHostSelfUpdateBusy,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates := readyMemorySystemdPortCoordinator(t)
			params, seal, _, _ := readyMemoryRuntimeTokenRotationFixture(
				t, policies, registry, updates, "unsafe-"+test.name,
			)
			test.mutate(policies, registry, updates, &params)
			_, err := updates.StageSystemUpdateRuntimeTokenRotation(
				t.Context(), registry, policies, params, seal,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("stage error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMemoryRuntimeTokenRotationEmergencyRevokeTerminatesEveryPhase(t *testing.T) {
	for _, phase := range []string{
		"staged",
		"claimed",
		"local_staged",
		"heartbeat_proved",
		"cancel_requested",
	} {
		t.Run(phase, func(t *testing.T) {
			policies, registry, updates := readyMemorySystemdPortCoordinator(t)
			params, seal, unseal, _ := readyMemoryRuntimeTokenRotationFixture(
				t, policies, registry, updates, "rotation-emergency-"+phase,
			)
			staged, err := updates.StageSystemUpdateRuntimeTokenRotation(
				t.Context(), registry, policies, params, seal,
			)
			if err != nil {
				t.Fatal(err)
			}
			persisted := updates.runtimeTokenRotations[staged.Rotation.ID]
			replayToken, err := runtimeTokenRotationReplayToken(persisted, unseal)
			if err != nil {
				t.Fatal(err)
			}
			rawStagedToken := replayToken.RawToken
			current := staged.Rotation
			if phase != "staged" {
				claimed, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
					t.Context(), registry, policies,
					ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
						RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
						ExecutionHostID:              params.ExecutionHostID,
						AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
						ClaimID:                      "00000000-0000-4000-8000-000000000003",
						ExpectedRevision:             current.Revision,
						Now:                          params.Now.Add(time.Second),
					},
					unseal,
				)
				if err != nil {
					t.Fatal(err)
				}
				current = claimed.Rotation
				rawStagedToken = claimed.Token.RawToken
			}
			if phase == "local_staged" || phase == "heartbeat_proved" {
				local, _, err := updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
					t.Context(), registry, policies,
					MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
						RotationID:       staged.Rotation.ID,
						ExecutionHostID:  params.ExecutionHostID,
						ExpectedRevision: current.Revision,
						RawStagedToken:   rawStagedToken,
						Now:              params.Now.Add(2 * time.Second),
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				current = local
				if phase == "heartbeat_proved" {
					proof := readyMemoryRuntimeTokenRotationHeartbeatProof(
						t, policies, registry, params, current,
						rawStagedToken, params.Now.Add(3*time.Second),
					)
					proved, _, err := updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
						t.Context(), registry, policies, proof,
					)
					if err != nil {
						t.Fatal(err)
					}
					current = proved
				}
			}
			if phase == "cancel_requested" {
				cancelRequested, _, err := updates.CancelSystemUpdateRuntimeTokenRotation(
					t.Context(), registry,
					CancelSystemUpdateRuntimeTokenRotationParams{
						RotationID:       staged.Rotation.ID,
						ExecutionHostID:  params.ExecutionHostID,
						ExpectedRevision: current.Revision,
						Now:              params.Now.Add(2 * time.Second),
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				current = cancelRequested
			}

			request := EmergencyRevokeSystemUpdateRuntimeTokenParams{
				RotationID:       staged.Rotation.ID,
				ExecutionHostID:  params.ExecutionHostID,
				ExpectedRevision: current.Revision,
				TokenID:          staged.Rotation.PreviousTokenID,
				Now:              params.Now.Add(4 * time.Second),
			}
			emergency, applied, err := updates.EmergencyRevokeSystemUpdateRuntimeToken(
				t.Context(), registry, request,
			)
			if err != nil || !applied ||
				emergency.Status != SystemUpdateRuntimeTokenRotationCanceled ||
				emergency.CanceledAt == nil ||
				emergency.EmergencyRevokedTokenID != staged.Rotation.PreviousTokenID {
				t.Fatalf(
					"emergency revoke = %#v applied=%v err=%v",
					emergency, applied, err,
				)
			}
			replayed, applied, err := updates.EmergencyRevokeSystemUpdateRuntimeToken(
				t.Context(), registry, request,
			)
			if err != nil || applied || replayed.Revision != emergency.Revision {
				t.Fatalf("emergency replay = %#v applied=%v err=%v", replayed, applied, err)
			}
			for _, tokenID := range []string{
				staged.Rotation.PreviousTokenID,
				staged.Rotation.StagedTokenID,
			} {
				if token := registry.serviceTokens[tokenID]; token.RevokedAt == nil {
					t.Fatalf("token %s remained active", tokenID)
				}
			}
			durable := updates.runtimeTokenRotations[staged.Rotation.ID]
			if durable.stagedTokenCiphertext != "" ||
				durable.stagedTokenNonce != "" ||
				durable.credentialClaimIDHash != "" ||
				durable.credentialClaimRevision != 0 {
				t.Fatalf("terminal replay secrets survived: %#v", durable)
			}
			service := registry.services[params.ServiceID]
			if service.Status != "offline" ||
				service.LastHeartbeatAt != nil ||
				len(service.ReportedCapabilities) != 0 ||
				service.NodeTokenCiphertext != "" ||
				service.NodeTokenNonce != "" {
				t.Fatalf("emergency service state survived: %#v", service)
			}
			if _, err := updates.GetActiveSystemUpdateRuntimeTokenRotationByExecutionHost(
				t.Context(), params.ExecutionHostID,
			); !errors.Is(err, ErrNotFound) {
				t.Fatalf("emergency rotation kept active lane: %v", err)
			}

			if _, err := updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
				t.Context(), registry, policies,
				ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
					RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
					ExecutionHostID:              params.ExecutionHostID,
					AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
					ClaimID:                      "00000000-0000-4000-8000-000000000004",
					ExpectedRevision:             emergency.Revision,
					Now:                          params.Now.Add(5 * time.Second),
				},
				unseal,
			); err == nil {
				t.Fatal("claim succeeded after emergency termination")
			}
			if _, _, err := updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
				t.Context(), registry, policies,
				MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
					RotationID:       staged.Rotation.ID,
					ExecutionHostID:  params.ExecutionHostID,
					ExpectedRevision: emergency.Revision,
					RawStagedToken:   rawStagedToken,
					Now:              params.Now.Add(5 * time.Second),
				},
			); err == nil {
				t.Fatal("local stage succeeded after emergency termination")
			}
			policy, err := policies.GetUpdaterPolicy(t.Context(), params.ServiceID)
			if err != nil {
				t.Fatal(err)
			}
			receiptID := current.LocalStageReceiptID
			if receiptID == "" {
				receiptID = "staged-token:" + staged.Rotation.StagedTokenID
			}
			if _, _, err := updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
				t.Context(), registry, policies,
				ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
					RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
					ExecutionHostID:  params.ExecutionHostID,
					ExpectedRevision: emergency.Revision,
					RawStagedToken:   rawStagedToken,
					Phase:            SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
					AgentVersion:     "v1.7.8", ExecutorVersion: "v1.7.8",
					AgentProtocolVersion: 2, ExecutorProtocolVersion: 1,
					MutationProtocolVersion:             1,
					ExpectedOwnershipEpoch:              params.ExpectedOwnershipEpoch,
					ExpectedSourcePolicyRevision:        params.ExpectedSourcePolicyRevision,
					ExpectedProjectionRevision:          params.ExpectedProjectionRevision,
					ExpectedLocalExecutorPolicyRevision: params.ExpectedLocalExecutorPolicyRevision,
					ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
					LocalStageReceiptID:                 receiptID,
					Now:                                 params.Now.Add(5 * time.Second),
				},
			); err == nil {
				t.Fatal("heartbeat proof succeeded after emergency termination")
			}
			if _, _, err := updates.ActivateSystemUpdateRuntimeTokenRotation(
				t.Context(), registry,
				ActivateSystemUpdateRuntimeTokenRotationParams{
					RotationID:       staged.Rotation.ID,
					ExecutionHostID:  params.ExecutionHostID,
					ExpectedRevision: emergency.Revision,
					RawStagedToken:   rawStagedToken,
					Now:              params.Now.Add(5 * time.Second),
				},
			); err == nil {
				t.Fatal("activation succeeded after emergency termination")
			}
			if _, _, err := updates.AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
				t.Context(), registry, policies,
				AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams{
					RotationID: staged.Rotation.ID, ServiceID: params.ServiceID,
					ExecutionHostID:              params.ExecutionHostID,
					AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
					ExpectedRevision:             emergency.Revision,
					Now:                          params.Now.Add(5 * time.Second),
				},
			); err == nil {
				t.Fatal("cancel acknowledgement succeeded after emergency termination")
			}
		})
	}
}

func TestMemoryRuntimeTokenRotationSerializesConcurrentHostStage(t *testing.T) {
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)
	first, seal, _, _ := readyMemoryRuntimeTokenRotationFixture(
		t, policies, registry, updates, "rotation-race-a",
	)
	second := first
	second.IdempotencyKey = "rotation-race-b"
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, params := range []StageSystemUpdateRuntimeTokenRotationParams{first, second} {
		params := params
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := updates.StageSystemUpdateRuntimeTokenRotation(
				t.Context(), registry, policies, params, seal,
			)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes, busy := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSystemUpdateRuntimeTokenRotationBusy):
			busy++
		default:
			t.Fatalf("unexpected race error: %v", err)
		}
	}
	if successes != 1 || busy != 1 {
		t.Fatalf("race results: success=%d busy=%d", successes, busy)
	}
}

func readyMemoryRuntimeTokenRotationFixture(
	t *testing.T,
	policies *MemoryUpdaterPolicyStore,
	registry *MemoryAuthStore,
	updates *MemorySystemUpdateStore,
	idempotencyKey string,
) (
	StageSystemUpdateRuntimeTokenRotationParams,
	NodeTokenSealer,
	NodeTokenUnsealer,
	string,
) {
	t.Helper()
	const key = "runtime-token-rotation-memory-test-key"
	const oldRaw = "ast_svc_runtime_rotation_old"
	registry.mu.Lock()
	service := registry.services["host-agent-a"]
	token := registry.serviceTokens[service.TokenID]
	token.TokenHash = security.HashToken(oldRaw)
	token.Scopes = []string{
		"service.register", "service.heartbeat", "updates.claim",
		"updates.report", "updates.grant",
	}
	token.RawToken = ""
	registry.serviceTokens[token.ID] = token
	registry.mu.Unlock()
	policy, err := policies.GetUpdaterPolicy(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), service.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	seal := NodeTokenSealer(func(raw string) (string, string, error) {
		return security.EncryptSecret(raw, key)
	})
	unseal := NodeTokenUnsealer(func(ciphertext, nonce string) (string, error) {
		return security.DecryptSecret(ciphertext, nonce, key)
	})
	return StageSystemUpdateRuntimeTokenRotationParams{
		ServiceID: service.ServiceID, ExecutionHostID: service.ExecutionHostID,
		IdempotencyKey:                      idempotencyKey,
		ExpectedOwnershipEpoch:              ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        policy.Revision,
		ExpectedProjectionRevision:          policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		Now:                                 time.Now().UTC(),
	}, seal, unseal, oldRaw
}

func readyMemoryRuntimeTokenRotationHeartbeatProof(
	t *testing.T,
	policies *MemoryUpdaterPolicyStore,
	registry *MemoryAuthStore,
	stageParams StageSystemUpdateRuntimeTokenRotationParams,
	local SystemUpdateRuntimeTokenRotation,
	rawStagedToken string,
	now time.Time,
) ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
	t.Helper()
	policy, err := policies.GetUpdaterPolicy(t.Context(), stageParams.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	const (
		agentVersion    = "v1.7.8"
		executorVersion = "v1.7.8"
	)
	registry.mu.Lock()
	service := registry.services[stageParams.ServiceID]
	service.Status = "online"
	service.ReportedVersion = agentVersion
	service.LastHeartbeatAt = cloneTimePtr(&now)
	if service.ReportedCapabilities == nil {
		service.ReportedCapabilities = map[string]any{}
	}
	for key, value := range map[string]any{
		"host_agent":                     true,
		"update_executor":                true,
		"mutation_enabled":               true,
		"recovery_pending":               false,
		"agent_version":                  agentVersion,
		"agent_protocol_version":         2,
		"executor_version":               executorVersion,
		"executor_protocol_version":      1,
		"mutation_protocol_version":      1,
		"execution_host_id":              stageParams.ExecutionHostID,
		"ownership_epoch":                stageParams.ExpectedOwnershipEpoch,
		"source_policy_revision":         stageParams.ExpectedSourcePolicyRevision,
		"projection_revision":            stageParams.ExpectedProjectionRevision,
		"local_executor_policy_revision": stageParams.ExpectedLocalExecutorPolicyRevision,
		"local_executor_policy_sha256":   policy.LocalExecutorPolicySHA256,
		"local_stage_receipt_id":         local.LocalStageReceiptID,
		"local_phase":                    SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
	} {
		service.ReportedCapabilities[key] = value
	}
	registry.services[service.ServiceID] = service
	registry.mu.Unlock()
	return ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
		RotationID: local.ID, ServiceID: stageParams.ServiceID,
		ExecutionHostID:  stageParams.ExecutionHostID,
		ExpectedRevision: local.Revision, RawStagedToken: rawStagedToken,
		Phase:        SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
		AgentVersion: agentVersion, ExecutorVersion: executorVersion,
		AgentProtocolVersion: 2, ExecutorProtocolVersion: 1,
		MutationProtocolVersion:             1,
		ExpectedOwnershipEpoch:              stageParams.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        stageParams.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          stageParams.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: stageParams.ExpectedLocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		LocalStageReceiptID:                 local.LocalStageReceiptID,
		Now:                                 now,
	}
}
