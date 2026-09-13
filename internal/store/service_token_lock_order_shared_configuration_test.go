package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/security"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMariaDBServiceTokenCanonicalLockOrderPairs(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	auth := NewMariaDBAuthStore(db)
	streams := NewMariaDBStreamStore(db)

	for _, pair := range []struct {
		serviceOperation string
		tokenMutation    string
	}{
		{serviceOperation: "heartbeat", tokenMutation: "rotate"},
		{serviceOperation: "heartbeat", tokenMutation: "revoke"},
		{serviceOperation: "artifact_report", tokenMutation: "rotate"},
		{serviceOperation: "artifact_report", tokenMutation: "revoke"},
		{serviceOperation: "service_delete", tokenMutation: "rotate"},
		{serviceOperation: "service_delete", tokenMutation: "revoke"},
	} {
		pair := pair
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("%s_vs_%s/%d", pair.serviceOperation, pair.tokenMutation, iteration), func(t *testing.T) {
				fixture := newMariaDBServiceTokenPairFixture(t, ctx, auth, streams, pair.serviceOperation)
				runMariaDBServiceTokenPair(t, ctx, db, fixture, pair.serviceOperation, pair.tokenMutation)
			})
		}
	}

	for iteration := 1; iteration <= 3; iteration++ {
		t.Run(fmt.Sprintf("heartbeat_vs_rotate_service_node_token_shared/%d", iteration), func(t *testing.T) {
			runMariaDBSharedServiceTokenNodeRotationPair(t, ctx, db, auth)
		})
		t.Run(fmt.Sprintf("heartbeat_vs_configure_service_node_shared/%d", iteration), func(t *testing.T) {
			runMariaDBSharedServiceTokenNodeConfigurePair(t, ctx, db, auth)
		})
		t.Run(fmt.Sprintf("heartbeat_vs_activate_service_node_configuration_shared/%d", iteration), func(t *testing.T) {
			runMariaDBSharedServiceTokenNodeActivationPair(t, ctx, db, auth)
		})
		t.Run(fmt.Sprintf("service_delete_shared_token_fails_closed/%d", iteration), func(t *testing.T) {
			assertMariaDBSharedTokenDeleteFailsClosed(t, ctx, db, auth)
		})
	}
}

func TestMariaDBFIX010UpdateAgentStageRejectsSharedReferences(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, referenceColumn := range []string{
		"token_id",
		"staged_node_previous_token_id",
		"staged_node_token_id",
	} {
		referenceColumn := referenceColumn
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("%s/%d", referenceColumn, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				targetID := cleanup.prefix + "stage-target"
				otherID := cleanup.prefix + "stage-other"
				oldToken := createMariaDBServiceTokenPairService(
					t, ctx, auth, targetID, "update_agent", nil, cleanup,
				)
				createMariaDBServiceTokenPairService(
					t, ctx, auth, otherID, "update_agent", nil, cleanup,
				)
				setMariaDBFIX010ServiceTokenReference(
					t, ctx, db, otherID, referenceColumn, oldToken.ID,
				)
				now := time.Now().UTC()
				configureToken := cleanup.prefix + "stage-configure"
				if _, err := auth.SetServiceConfigureToken(
					ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
				); err != nil {
					t.Fatal(err)
				}
				beforeTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				beforeOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}

				sealerCalled := false
				_, err = auth.StageServiceNodeConfiguration(
					ctx,
					targetID,
					configureToken,
					now,
					func(string) (string, string, error) {
						sealerCalled = true
						return "fix010-stage-ciphertext", "fix010-stage-nonce", nil
					},
				)
				if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
					t.Fatalf("stage error = %v, want shared-token conflict", err)
				}
				if sealerCalled {
					t.Fatal("stage sealed a token before rejecting the shared reference")
				}
				afterTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				afterOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(afterTarget, beforeTarget) ||
					!reflect.DeepEqual(afterOther, beforeOther) ||
					mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
					t.Fatalf(
						"shared stage changed state: target_current=%q target_previous=%q target_staged=%q other_current=%q other_previous=%q other_staged=%q old_revoked=%t",
						afterTarget.TokenID,
						afterTarget.StagedNodePreviousTokenID,
						afterTarget.StagedNodeTokenID,
						afterOther.TokenID,
						afterOther.StagedNodePreviousTokenID,
						afterOther.StagedNodeTokenID,
						mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
					)
				}
			})
		}
	}
}

func TestMariaDBFIX010UpdateAgentActivationRejectsSharedReferences(t *testing.T) {
	tests := []struct {
		name            string
		referenceColumn string
		useStagedToken  bool
	}{
		{name: "old_current", referenceColumn: "token_id"},
		{name: "old_staged_previous", referenceColumn: "staged_node_previous_token_id"},
		{name: "old_staged_token", referenceColumn: "staged_node_token_id"},
		{name: "new_current", referenceColumn: "token_id", useStagedToken: true},
		{name: "new_staged_previous", referenceColumn: "staged_node_previous_token_id", useStagedToken: true},
		{name: "new_staged_token", referenceColumn: "staged_node_token_id", useStagedToken: true},
	}
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, test := range tests {
		test := test
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("%s/%d", test.name, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				targetID := cleanup.prefix + "activation-target"
				otherID := cleanup.prefix + "activation-other"
				oldToken := createMariaDBServiceTokenPairService(
					t, ctx, auth, targetID, "update_agent", nil, cleanup,
				)
				now := time.Now().UTC()
				configureToken := cleanup.prefix + "activation-configure"
				if _, err := auth.SetServiceConfigureToken(
					ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
				); err != nil {
					t.Fatal(err)
				}
				staged, err := auth.StageServiceNodeConfiguration(
					ctx,
					targetID,
					configureToken,
					now,
					func(string) (string, string, error) {
						return "fix010-activation-ciphertext", "fix010-activation-nonce", nil
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(staged.Token)
				createMariaDBServiceTokenPairService(
					t, ctx, auth, otherID, "update_agent", nil, cleanup,
				)
				referenceTokenID := oldToken.ID
				if test.useStagedToken {
					referenceTokenID = staged.Token.ID
				}
				setMariaDBFIX010ServiceTokenReference(
					t, ctx, db, otherID, test.referenceColumn, referenceTokenID,
				)
				beforeTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				beforeOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}

				activatedToken, activatedService, alreadyActivated, err :=
					auth.ActivateServiceNodeConfiguration(
						ctx,
						targetID,
						staged.Token.ID,
						staged.ActivationToken,
						now.Add(time.Minute),
						ServiceRuntimeReport{},
					)
				if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
					t.Fatalf("activation error = %v, want shared-token conflict", err)
				}
				if activatedToken.ID != "" || activatedService.ServiceID != "" || alreadyActivated {
					t.Fatalf(
						"shared activation returned success values: token_id=%q service_id=%q already=%t",
						activatedToken.ID,
						activatedService.ServiceID,
						alreadyActivated,
					)
				}
				afterTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				afterOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}
				var stagedRows int
				if err := db.QueryRowContext(
					ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID,
				).Scan(&stagedRows); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(afterTarget, beforeTarget) ||
					!reflect.DeepEqual(afterOther, beforeOther) ||
					mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
					stagedRows != 0 {
					t.Fatalf(
						"shared activation changed state: target_current=%q target_previous=%q target_staged=%q other_current=%q other_previous=%q other_staged=%q old_revoked=%t staged_rows=%d",
						afterTarget.TokenID,
						afterTarget.StagedNodePreviousTokenID,
						afterTarget.StagedNodeTokenID,
						afterOther.TokenID,
						afterOther.StagedNodePreviousTokenID,
						afterOther.StagedNodeTokenID,
						mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
						stagedRows,
					)
				}
			})
		}
	}
}

func TestMariaDBFIX010ExternalBindingWinsAgainstStageAndActivation(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)

	type concurrentResult struct {
		tokenID          string
		serviceID        string
		alreadyActivated bool
		err              error
	}
	for _, operation := range []string{"stage", "activate"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			for iteration := 1; iteration <= 3; iteration++ {
				t.Run(strconv.Itoa(iteration), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(parent, 20*time.Second)
					defer cancel()
					cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
					targetServiceID := cleanup.prefix + operation + "-target"
					externalServiceID := cleanup.prefix + operation + "-external"
					oldToken := createMariaDBServiceTokenPairService(
						t, ctx, auth, targetServiceID, "update_agent", nil, cleanup,
					)
					now := time.Now().UTC()
					configureToken := cleanup.prefix + "configure"
					if _, err := auth.SetServiceConfigureToken(
						ctx,
						targetServiceID,
						security.HashToken(configureToken),
						now.Add(time.Hour),
					); err != nil {
						t.Fatal(err)
					}

					var staged StagedServiceNodeConfiguration
					if operation == "activate" {
						var err error
						staged, err = auth.StageServiceNodeConfiguration(
							ctx,
							targetServiceID,
							configureToken,
							now,
							func(string) (string, string, error) {
								return cleanup.prefix + "ciphertext", cleanup.prefix + "nonce", nil
							},
						)
						if err != nil {
							t.Fatal(err)
						}
						cleanup.trackToken(staged.Token)
					}
					beforeTarget, err := auth.GetService(ctx, targetServiceID)
					if err != nil {
						t.Fatal(err)
					}

					observedOperation := "stage_service_node_configuration"
					if operation == "activate" {
						observedOperation = "activate_service_node_configuration"
					}
					phases := make(chan mariaDBServiceTokenLockPhase, 16)
					release := make(chan struct{})
					observer := mariaDBServiceTokenLockObserver(func(name string, phase mariaDBServiceTokenLockPhase) {
						if name != observedOperation {
							return
						}
						if !isMariaDBServiceTokenCanonicalLockPhase(phase) {
							return
						}
						phases <- phase
						if phase == mariaDBServiceTokenBeforeServiceLocks {
							<-release
						}
					})
					operationContext := context.WithValue(
						ctx,
						mariaDBServiceTokenLockObserverContextKey{},
						observer,
					)
					sealerCalled := make(chan struct{}, 1)
					result := make(chan concurrentResult, 1)
					go func() {
						if operation == "stage" {
							configuration, err := auth.StageServiceNodeConfiguration(
								operationContext,
								targetServiceID,
								configureToken,
								now,
								func(string) (string, string, error) {
									sealerCalled <- struct{}{}
									return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
								},
							)
							result <- concurrentResult{
								tokenID:   configuration.Token.ID,
								serviceID: configuration.Service.ServiceID,
								err:       err,
							}
							return
						}
						token, service, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
							operationContext,
							targetServiceID,
							staged.Token.ID,
							staged.ActivationToken,
							now.Add(time.Second),
							ServiceRuntimeReport{Version: "v1.0.1"},
						)
						result <- concurrentResult{
							tokenID:          token.ID,
							serviceID:        service.ServiceID,
							alreadyActivated: alreadyActivated,
							err:              err,
						}
					}()

					select {
					case phase := <-phases:
						if phase != mariaDBServiceTokenBeforeServiceLocks {
							t.Fatalf("first observed lock phase = %q, want %q", phase, mariaDBServiceTokenBeforeServiceLocks)
						}
					case <-ctx.Done():
						t.Fatal("node configuration did not reach the service-lock barrier")
					}

					cleanup.trackServiceID(externalServiceID)
					if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{
						ServiceID:       externalServiceID,
						ServiceType:     "update_agent",
						ServiceName:     externalServiceID,
						TransportMode:   SystemUpdateTransportPullV2,
						ExecutionHostID: externalServiceID + "-host",
					}); err != nil {
						close(release)
						t.Fatal(err)
					}
					close(release)

					var outcome concurrentResult
					select {
					case outcome = <-result:
					case <-ctx.Done():
						t.Fatal("node configuration did not finish after releasing the barrier")
					}
					if !errors.Is(outcome.err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
						t.Fatalf("%s error = %v, want shared-token conflict", operation, outcome.err)
					}
					if outcome.tokenID != "" || outcome.serviceID != "" || outcome.alreadyActivated {
						t.Fatalf("%s returned success state: token_id=%q service_id=%q already=%t", operation, outcome.tokenID, outcome.serviceID, outcome.alreadyActivated)
					}
					select {
					case <-sealerCalled:
						t.Fatal("stage sealer ran after the reference inventory changed")
					default:
					}

					afterTarget, err := auth.GetService(ctx, targetServiceID)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(afterTarget, beforeTarget) {
						t.Fatalf(
							"%s partially mutated target: before=%s after=%s",
							operation,
							formatSafeRegisteredServiceDiagnostic(beforeTarget),
							formatSafeRegisteredServiceDiagnostic(afterTarget),
						)
					}
					external, err := auth.GetService(ctx, externalServiceID)
					if err != nil {
						t.Fatal(err)
					}
					if external.TokenID != oldToken.ID || mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
						t.Fatalf(
							"%s external binding or old-token state changed: external=%s old_revoked=%t",
							operation,
							formatSafeRegisteredServiceDiagnostic(external),
							mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
						)
					}
					if operation == "activate" {
						var stagedTokenRows int
						if err := db.QueryRowContext(
							ctx,
							`SELECT COUNT(*) FROM service_tokens WHERE id = ?`,
							staged.Token.ID,
						).Scan(&stagedTokenRows); err != nil {
							t.Fatal(err)
						}
						if stagedTokenRows != 0 {
							t.Fatalf("activation inserted %d staged token rows after conflict", stagedTokenRows)
						}
					}
				})
			}
		})
	}
}
