package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/security"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMariaDBFIX005PrecreateVsTokenMutationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, mutation := range []string{"rotate", "revoke"} {
		mutation := mutation
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("precreate_vs_%s/%d", mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				token, err := auth.CreateServiceToken(
					ctx, "worker", []string{"service.register", "service.heartbeat"},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(token)

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				mutationHeld := make(chan struct{})
				releaseMutation := make(chan struct{})
				var releaseMutationOnce sync.Once
				defer releaseMutationOnce.Do(func() { close(releaseMutation) })
				var heldOnce sync.Once
				mutationOperation := mutation + "_service_token"
				mutationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation != mutationOperation {
						return
					}
					mutationPhases <- phase
					if phase == mariaDBServiceTokenBindingsValidated {
						heldOnce.Do(func() { close(mutationHeld) })
						<-releaseMutation
					}
				})
				mutationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, mutationObserver,
				)
				mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					if mutation == "rotate" {
						rotated, mutationErr := auth.RotateServiceToken(mutationCtx, token.ID)
						mutationResult <- mariaDBServiceTokenMutationResult{token: rotated, err: mutationErr}
						return
					}
					mutationResult <- mariaDBServiceTokenMutationResult{
						err: auth.RevokeServiceToken(mutationCtx, token.ID),
					}
				}()
				select {
				case <-mutationHeld:
				case <-time.After(5 * time.Second):
					t.Fatal("token mutation did not hold the unbound token revalidation phase")
				}
				assertMariaDBFIX005PhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
					mariaDBServiceTokenTokenLocksHeld,
					mariaDBServiceTokenBindingsValidated,
				})

				precreatePhases := make(chan mariaDBServiceTokenLockPhase, 8)
				precreateObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == "precreate_service" {
						precreatePhases <- phase
					}
				})
				precreateCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, precreateObserver,
				)
				serviceID := cleanup.prefix + "late-binding"
				cleanup.trackServiceID(serviceID)
				precreateResult := make(chan error, 1)
				go func() {
					_, precreateErr := auth.PrecreateService(
						precreateCtx,
						token,
						ServiceRegistration{
							ServiceID: serviceID, ServiceType: "worker", ServiceName: serviceID,
							PublicURL: "https://worker.example.com:18081",
						},
					)
					precreateResult <- precreateErr
				}()
				assertMariaDBFIX005PhaseSequence(t, precreatePhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
				})
				select {
				case err := <-precreateResult:
					t.Fatalf("PrecreateService completed before the token lock was released: %v", err)
				case <-time.After(75 * time.Millisecond):
				}
				releaseMutationOnce.Do(func() { close(releaseMutation) })

				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				assertMariaDBServiceTokenOperationError(t, mutation, mutationOutcome.err)
				cleanup.trackToken(mutationOutcome.token)
				precreateErr := receiveMariaDBServiceTokenError(t, precreateResult, "PrecreateService")
				if !errors.Is(precreateErr, ErrForbidden) {
					t.Fatalf("PrecreateService error = %v, want ErrForbidden", precreateErr)
				}
				if _, err := auth.GetService(ctx, serviceID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("late-bound service lookup error = %v, want ErrNotFound", err)
				}
				if !mariaDBServiceTokenRevoked(t, ctx, db, token.ID) {
					t.Fatal("old token was not revoked")
				}
				if mutation == "rotate" {
					if mutationOutcome.token.ID == "" || mariaDBServiceTokenRevoked(t, ctx, db, mutationOutcome.token.ID) {
						t.Fatalf("rotated token final state = %s", formatSafeServiceTokenDiagnostic("rotate", mutationOutcome.token, 0, "unexpected_result"))
					}
				}
			})
		}
	}
}

func TestMariaDBFIX009PrecreateWinsVsTokenMutationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, mutation := range []string{"rotate", "revoke"} {
		mutation := mutation
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("precreate_wins_vs_%s/%d", mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				token, err := auth.CreateServiceToken(
					ctx, "worker", []string{"service.register", "service.heartbeat"},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(token)

				mutationOperation := mutation + "_service_token"
				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 16)
				beforeServiceLocks := make(chan struct{})
				releaseMutation := make(chan struct{})
				var releaseMutationOnce sync.Once
				defer releaseMutationOnce.Do(func() { close(releaseMutation) })
				var holdOnce sync.Once
				mutationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation != mutationOperation {
						return
					}
					mutationPhases <- phase
					if phase == mariaDBServiceTokenBeforeServiceLocks {
						holdOnce.Do(func() {
							close(beforeServiceLocks)
							<-releaseMutation
						})
					}
				})
				mutationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, mutationObserver,
				)
				mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					if mutation == "rotate" {
						rotated, mutationErr := auth.RotateServiceToken(mutationCtx, token.ID)
						mutationResult <- mariaDBServiceTokenMutationResult{token: rotated, err: mutationErr}
						return
					}
					mutationResult <- mariaDBServiceTokenMutationResult{
						err: auth.RevokeServiceToken(mutationCtx, token.ID),
					}
				}()
				select {
				case <-beforeServiceLocks:
				case <-time.After(5 * time.Second):
					t.Fatal("token mutation did not pause after its committed reference discovery")
				}

				serviceID := cleanup.prefix + "precreate-winner"
				cleanup.trackServiceID(serviceID)
				if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{
					ServiceID: serviceID, ServiceType: "worker", ServiceName: serviceID,
					PublicURL: "https://worker.example.com:18081",
				}); err != nil {
					t.Fatalf("PrecreateService did not commit before %s: %v", mutation, err)
				}
				releaseMutationOnce.Do(func() { close(releaseMutation) })

				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				assertMariaDBServiceTokenOperationError(t, mutation, mutationOutcome.err)
				cleanup.trackToken(mutationOutcome.token)
				close(mutationPhases)
				discoveryAttempts := 0
				for phase := range mutationPhases {
					if phase == mariaDBServiceTokenBeforeServiceLocks {
						discoveryAttempts++
					}
				}
				if discoveryAttempts != 2 {
					t.Fatalf("committed binding discovery attempts = %d, want one retry", discoveryAttempts)
				}

				service, err := auth.GetService(ctx, serviceID)
				if err != nil {
					t.Fatal(err)
				}
				if !mariaDBServiceTokenRevoked(t, ctx, db, token.ID) {
					t.Fatal("old token was not revoked")
				}
				var oldReferences int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM services
WHERE token_id = ? OR staged_node_previous_token_id = ? OR staged_node_token_id = ?`,
					token.ID, token.ID, token.ID,
				).Scan(&oldReferences); err != nil {
					t.Fatal(err)
				}
				if mutation == "rotate" {
					if mutationOutcome.token.ID == "" ||
						service.TokenID != mutationOutcome.token.ID ||
						oldReferences != 0 ||
						mariaDBServiceTokenRevoked(t, ctx, db, mutationOutcome.token.ID) {
						t.Fatalf("precreate/rotate final state service=%s token=%s old_refs=%d", formatSafeRegisteredServiceDiagnostic(service), formatSafeServiceTokenDiagnostic("rotate", mutationOutcome.token, oldReferences, "unexpected_result"), oldReferences)
					}
					return
				}
				if service.TokenID != token.ID || oldReferences != 1 {
					t.Fatalf("precreate/revoke final state service=%s old_refs=%d", formatSafeRegisteredServiceDiagnostic(service), oldReferences)
				}
			})
		}
	}
}

func TestMariaDBFIX009ActivationInsertFailureRollsBack(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for iteration := 1; iteration <= 3; iteration++ {
		t.Run(strconv.Itoa(iteration), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(parent, 20*time.Second)
			defer cancel()
			cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
			serviceID := cleanup.prefix + "activation-rollback"
			oldToken := createMariaDBServiceTokenPairService(
				t, ctx, auth, serviceID, "update_agent", nil, cleanup,
			)
			now := time.Now().UTC()
			configureToken := cleanup.prefix + "configure"
			if _, err := auth.SetServiceConfigureToken(
				ctx, serviceID, security.HashToken(configureToken), now.Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}
			staged, err := auth.StageServiceNodeConfiguration(
				ctx,
				serviceID,
				configureToken,
				now,
				func(string) (string, string, error) {
					return "rollback-ciphertext", "rollback-nonce", nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			cleanup.trackToken(staged.Token)
			triggerName := "fix009_activation_" + strings.ReplaceAll(newUUID(), "-", "")
			if _, err := db.ExecContext(ctx, fmt.Sprintf(
				"CREATE TRIGGER `%s` BEFORE UPDATE ON services FOR EACH ROW "+
					"SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'fix009 activation rollback'",
				triggerName,
			)); err != nil {
				t.Fatal(err)
			}
			triggerPresent := true
			t.Cleanup(func() {
				if triggerPresent {
					_, _ = db.ExecContext(context.Background(), "DROP TRIGGER IF EXISTS `"+triggerName+"`")
				}
			})

			_, _, _, activationErr := auth.ActivateServiceNodeConfiguration(
				ctx,
				serviceID,
				staged.Token.ID,
				staged.ActivationToken,
				now.Add(time.Minute),
				ServiceRuntimeReport{},
			)
			if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS `"+triggerName+"`"); err != nil {
				t.Fatal(err)
			}
			triggerPresent = false
			if activationErr == nil {
				t.Fatal("activation unexpectedly committed through the injected service-row update failure")
			}
			service, err := auth.GetService(ctx, serviceID)
			if err != nil {
				t.Fatal(err)
			}
			if service.TokenID != oldToken.ID ||
				service.StagedNodePreviousTokenID != oldToken.ID ||
				service.StagedNodeTokenID != staged.Token.ID ||
				service.StagedNodeTokenHash != staged.Token.TokenHash ||
				service.NodeTokenCiphertext != "" ||
				service.NodeTokenNonce != "" {
				t.Fatalf("failed activation partially mutated service: %s", formatSafeRegisteredServiceDiagnostic(service))
			}
			var oldRevoked bool
			if err := db.QueryRowContext(
				ctx,
				`SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?`,
				oldToken.ID,
			).Scan(&oldRevoked); err != nil {
				t.Fatal(err)
			}
			var stagedTokenRows int
			if err := db.QueryRowContext(
				ctx,
				`SELECT COUNT(*) FROM service_tokens WHERE id = ?`,
				staged.Token.ID,
			).Scan(&stagedTokenRows); err != nil {
				t.Fatal(err)
			}
			if oldRevoked || stagedTokenRows != 0 {
				t.Fatalf("failed activation token state old_revoked=%v staged_rows=%d", oldRevoked, stagedTokenRows)
			}
			if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "updates.claim"); err != nil {
				t.Fatalf("failed activation invalidated the old token: %v", err)
			}
			if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, "updates.claim"); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("failed activation enabled the staged token: %v", err)
			}
		})
	}
}

func TestMariaDBFIX009ActivationWinsVsGenericTokenMutation(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, mutation := range []string{"rotate", "revoke"} {
		mutation := mutation
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("activation_vs_%s/%d", mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				serviceID := cleanup.prefix + "activation-race"
				oldToken := createMariaDBServiceTokenPairService(
					t, ctx, auth, serviceID, "update_agent", nil, cleanup,
				)
				now := time.Now().UTC()
				configureToken := cleanup.prefix + "configure"
				if _, err := auth.SetServiceConfigureToken(
					ctx, serviceID, security.HashToken(configureToken), now.Add(time.Hour),
				); err != nil {
					t.Fatal(err)
				}
				staged, err := auth.StageServiceNodeConfiguration(
					ctx,
					serviceID,
					configureToken,
					now,
					func(string) (string, string, error) {
						return "activation-race-ciphertext", "activation-race-nonce", nil
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(staged.Token)

				activationHeld := make(chan struct{})
				releaseActivation := make(chan struct{})
				var releaseActivationOnce sync.Once
				defer releaseActivationOnce.Do(func() { close(releaseActivation) })
				var activationHoldOnce sync.Once
				activationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == "activate_service_node_configuration" &&
						phase == mariaDBServiceTokenBindingsValidated {
						activationHoldOnce.Do(func() {
							close(activationHeld)
							<-releaseActivation
						})
					}
				})
				activationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, activationObserver,
				)
				activationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					activated, _, _, activationErr := auth.ActivateServiceNodeConfiguration(
						activationCtx,
						serviceID,
						staged.Token.ID,
						staged.ActivationToken,
						now.Add(time.Minute),
						ServiceRuntimeReport{},
					)
					activationResult <- mariaDBServiceTokenMutationResult{token: activated, err: activationErr}
				}()
				select {
				case <-activationHeld:
				case <-time.After(5 * time.Second):
					t.Fatal("activation did not hold the validated service/token closure")
				}

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 16)
				mutationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == mutation+"_service_token" {
						mutationPhases <- phase
					}
				})
				mutationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, mutationObserver,
				)
				mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					if mutation == "rotate" {
						rotated, mutationErr := auth.RotateServiceToken(mutationCtx, oldToken.ID)
						mutationResult <- mariaDBServiceTokenMutationResult{token: rotated, err: mutationErr}
						return
					}
					mutationResult <- mariaDBServiceTokenMutationResult{
						err: auth.RevokeServiceToken(mutationCtx, oldToken.ID),
					}
				}()
				if phase := receiveMariaDBServiceTokenPhase(t, mutationPhases, mutation+" service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
					t.Fatalf("first %s phase = %q", mutation, phase)
				}
				select {
				case phase := <-mutationPhases:
					t.Fatalf("%s advanced to %q while activation held the service row", mutation, phase)
				case <-time.After(75 * time.Millisecond):
				}
				releaseActivationOnce.Do(func() { close(releaseActivation) })

				activated := receiveMariaDBServiceTokenMutation(t, activationResult)
				assertMariaDBServiceTokenOperationError(t, "activation", activated.err)
				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				if !errors.Is(mutationOutcome.err, ErrNotFound) {
					assertMariaDBServiceTokenOperationError(t, mutation, mutationOutcome.err)
					t.Fatalf("%s error = %v, want ErrNotFound after activation", mutation, mutationOutcome.err)
				}
				service, err := auth.GetService(ctx, serviceID)
				if err != nil {
					t.Fatal(err)
				}
				if activated.token.ID != staged.Token.ID ||
					service.TokenID != staged.Token.ID ||
					service.StagedNodePreviousTokenID != "" ||
					service.StagedNodeTokenID != "" ||
					!mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
					mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID) {
					t.Fatalf("activation/%s final state service=%s token=%s", mutation, formatSafeRegisteredServiceDiagnostic(service), formatSafeServiceTokenDiagnostic("activate", activated.token, 0, "unexpected_result"))
				}
			})
		}
	}
}
