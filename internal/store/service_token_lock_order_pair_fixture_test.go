package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"testing"
	"time"
)

func setMariaDBFIX010ServiceTokenReference(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID, column, tokenID string,
) {
	t.Helper()
	var query string
	switch column {
	case "token_id":
		query = `UPDATE services SET token_id = ? WHERE service_id = ?`
	case "staged_node_previous_token_id":
		query = `UPDATE services SET staged_node_previous_token_id = ? WHERE service_id = ?`
	case "staged_node_token_id":
		query = `UPDATE services SET staged_node_token_id = ? WHERE service_id = ?`
	default:
		t.Fatalf("unsupported FIX-010 reference column %q", column)
	}
	result, err := db.ExecContext(ctx, query, tokenID, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("set FIX-010 reference affected=%d err=%v", affected, err)
	}
}

type mariaDBServiceTokenPairFixture struct {
	auth      MariaDBAuthStore
	streams   MariaDBStreamStore
	cleanup   *mariaDBFIX005Cleanup
	serviceID string
	streamID  string
	token     ServiceToken
}

func newMariaDBServiceTokenPairFixture(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	streams MariaDBStreamStore,
	serviceOperation string,
) mariaDBServiceTokenPairFixture {
	t.Helper()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, auth.db)
	suffix := cleanup.prefix + "pair"
	serviceType := "worker"
	if serviceOperation == "artifact_report" {
		serviceType = "encoder_recorder"
	}
	serviceID := suffix + "-" + serviceType
	token := createMariaDBServiceTokenPairService(t, ctx, auth, serviceID, serviceType, nil, cleanup)
	stream, err := streams.CreateStream(ctx, cleanup.prefix+"token lock order")
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackStreamID(stream.ID)
	if _, err := auth.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{
		ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary",
	}); err != nil {
		t.Fatal(err)
	}
	return mariaDBServiceTokenPairFixture{
		auth: auth, streams: streams, cleanup: cleanup,
		serviceID: serviceID, streamID: stream.ID, token: token,
	}
}

type mariaDBServiceTokenMutationResult struct {
	token ServiceToken
	err   error
}

func runMariaDBServiceTokenPair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	fixture mariaDBServiceTokenPairFixture,
	serviceOperation,
	tokenMutation string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, fixture.token.ID)
	defer blocker.Rollback()

	serviceResult := make(chan error, 1)
	go func() {
		serviceResult <- runMariaDBServiceTokenPairOperation(ctx, fixture, serviceOperation)
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, fixture.serviceID)

	operation := tokenMutation + "_service_token"
	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(observedOperation string, phase mariaDBServiceTokenLockPhase) {
		if observedOperation == operation {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		if tokenMutation == "rotate" {
			token, err := fixture.auth.RotateServiceToken(mutationCtx, fixture.token.ID)
			mutationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
			return
		}
		mutationResult <- mariaDBServiceTokenMutationResult{err: fixture.auth.RevokeServiceToken(mutationCtx, fixture.token.ID)}
	}()
	if phase := receiveMariaDBServiceTokenPhase(t, phases, "token mutation service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
		t.Fatalf("first token mutation phase = %q, want %q", phase, mariaDBServiceTokenBeforeServiceLocks)
	}
	select {
	case phase := <-phases:
		t.Fatalf("token mutation advanced to %q while the canonical service row was held", phase)
	case <-time.After(75 * time.Millisecond):
	}

	if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	serviceErr := receiveMariaDBServiceTokenError(t, serviceResult, "service operation")
	mutation := receiveMariaDBServiceTokenMutation(t, mutationResult)
	fixture.cleanup.trackToken(mutation.token)
	assertMariaDBServiceTokenOperationError(t, serviceOperation, serviceErr)
	if serviceOperation == "service_delete" {
		if !errors.Is(mutation.err, ErrNotFound) {
			t.Fatalf("%s after service delete error = %v, want ErrNotFound", tokenMutation, mutation.err)
		}
	} else {
		assertMariaDBServiceTokenOperationError(t, tokenMutation, mutation.err)
	}
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	assertMariaDBServiceTokenPairFinalState(t, ctx, db, fixture, serviceOperation, tokenMutation, mutation)
}

func runMariaDBServiceTokenPairOperation(
	ctx context.Context,
	fixture mariaDBServiceTokenPairFixture,
	operation string,
) error {
	switch operation {
	case "heartbeat":
		_, err := fixture.auth.Heartbeat(ctx, fixture.token, ServiceHeartbeat{
			ServiceID: fixture.serviceID, Status: "online", CurrentStreamID: fixture.streamID,
		})
		return err
	case "artifact_report":
		archiveStartedAt := time.Now().UTC()
		archiveRunID := "lock-pair-" + fixture.streamID
		return fixture.streams.WriteStreamArtifactReport(
			ctx,
			fixture.token,
			ServiceStreamEvent{
				ServiceID: fixture.serviceID,
				StreamID:  fixture.streamID,
				EventType: "archive.artifacts.reported",
			},
			[]StreamArtifact{{
				ArchiveRunID: archiveRunID, ArchiveStartedAt: &archiveStartedAt,
				Kind: "archive", Name: "final.mp4",
				RelativePath: "final/" + fixture.streamID + "/" + archiveRunID + "/final.mp4",
				SizeBytes:    1,
			}},
		)
	case "service_delete":
		return fixture.auth.DeleteService(ctx, fixture.serviceID)
	default:
		return fmt.Errorf("unknown service operation %q", operation)
	}
}

func lockMariaDBServiceTokenForTest(t *testing.T, ctx context.Context, db *sql.DB, tokenID string) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM service_tokens WHERE id = ? FOR UPDATE`, tokenID).Scan(&lockedID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	return tx
}

func waitForMariaDBServiceRowLock(t *testing.T, ctx context.Context, db *sql.DB, serviceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		probe, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var lockedID string
		err = probe.QueryRowContext(ctx, `SELECT service_id FROM services WHERE service_id = ? FOR UPDATE NOWAIT`, serviceID).Scan(&lockedID)
		_ = probe.Rollback()
		if err == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && (mysqlErr.Number == 1205 || mysqlErr.Number == 3572) {
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("service %s disappeared before the lock barrier", serviceID)
		}
		t.Fatalf("probe service row lock: %v", err)
	}
	t.Fatalf("service operation did not acquire the service row before the lock barrier")
}

func receiveMariaDBServiceTokenPhase(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	label string,
) mariaDBServiceTokenLockPhase {
	t.Helper()
	select {
	case phase := <-phases:
		return phase
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not reach its bounded barrier", label)
		return ""
	}
}

func receiveMariaDBServiceTokenError(t *testing.T, result <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not complete before timeout", label)
		return nil
	}
}

func receiveMariaDBServiceTokenMutation(
	t *testing.T,
	result <-chan mariaDBServiceTokenMutationResult,
) mariaDBServiceTokenMutationResult {
	t.Helper()
	select {
	case mutation := <-result:
		return mutation
	case <-time.After(10 * time.Second):
		t.Fatal("token mutation did not complete before timeout")
		return mariaDBServiceTokenMutationResult{}
	}
}

func assertMariaDBServiceTokenOperationError(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205) {
		t.Fatalf("%s hit MariaDB lock failure %d: %v", label, mysqlErr.Number, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("%s timed out: %v", label, err)
	}
	t.Fatalf("%s returned unexpected store error: %v", label, err)
}

func assertMariaDBServiceTokenPhaseOrder(t *testing.T, phases <-chan mariaDBServiceTokenLockPhase) {
	t.Helper()
	seenServiceLocks := false
	seenTokenLocks := false
	for {
		select {
		case phase := <-phases:
			switch phase {
			case mariaDBServiceTokenServiceLocksHeld:
				seenServiceLocks = true
			case mariaDBServiceTokenBeforeTokenLocks, mariaDBServiceTokenTokenLocksHeld:
				if !seenServiceLocks {
					t.Fatalf("token phase %q occurred before service locks", phase)
				}
				if phase == mariaDBServiceTokenTokenLocksHeld {
					seenTokenLocks = true
				}
			case mariaDBServiceTokenBindingsValidated:
				if !seenServiceLocks || !seenTokenLocks {
					t.Fatalf("binding validation preceded canonical locks: service=%v token=%v", seenServiceLocks, seenTokenLocks)
				}
			}
		default:
			return
		}
	}
}

func assertMariaDBServiceTokenPairFinalState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBServiceTokenPairFixture,
	serviceOperation,
	tokenMutation string,
	mutation mariaDBServiceTokenMutationResult,
) {
	t.Helper()
	oldRevoked := mariaDBServiceTokenRevoked(t, ctx, db, fixture.token.ID)
	if serviceOperation == "service_delete" {
		if _, err := fixture.auth.GetService(ctx, fixture.serviceID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted service lookup error = %v, want ErrNotFound", err)
		}
		assignments, err := fixture.auth.ListStreamAssignments(ctx, fixture.streamID)
		if err != nil {
			t.Fatal(err)
		}
		if len(assignments) != 0 || !oldRevoked {
			t.Fatalf("service delete left partial state: assignments=%s old_revoked=%v", formatSafeSensitiveCompositeDiagnostic(assignments), oldRevoked)
		}
		return
	}
	service, err := fixture.auth.GetService(ctx, fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := fixture.auth.ListServiceAssignmentsForService(ctx, fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].StreamID != fixture.streamID || service.CurrentStreamID != fixture.streamID {
		t.Fatalf("assignment/current_stream_id split: service=%s assignments=%#v", formatSafeRegisteredServiceDiagnostic(service), assignments)
	}
	if !oldRevoked || service.LastHeartbeatAt != nil || len(service.ReportedCapabilities) != 0 {
		t.Fatalf("token mutation left runtime readiness active: service=%s old_revoked=%v", formatSafeRegisteredServiceDiagnostic(service), oldRevoked)
	}
	if tokenMutation == "rotate" {
		if mutation.token.ID == "" || service.TokenID != mutation.token.ID || mariaDBServiceTokenRevoked(t, ctx, db, mutation.token.ID) {
			t.Fatalf("rotation binding mismatch: service=%s token=%s", formatSafeRegisteredServiceDiagnostic(service), formatSafeServiceTokenDiagnostic("rotate", mutation.token, 0, "unexpected_result"))
		}
	} else if service.TokenID != fixture.token.ID {
		t.Fatalf("revoke changed service token binding: service=%s old=%s", formatSafeRegisteredServiceDiagnostic(service), fixture.token.ID)
	}
	if serviceOperation == "artifact_report" {
		artifacts, err := fixture.streams.ListStreamArtifacts(ctx, fixture.streamID)
		if err != nil {
			t.Fatal(err)
		}
		if len(artifacts) == 0 {
			t.Fatal("artifact report committed no artifact")
		}
	}
}

func mariaDBServiceTokenRevoked(t *testing.T, ctx context.Context, db *sql.DB, tokenID string) bool {
	t.Helper()
	var revoked bool
	if err := db.QueryRowContext(ctx, `SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?`, tokenID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

func createMariaDBServiceTokenPairService(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	serviceID,
	serviceType string,
	existing *ServiceToken,
	cleanup *mariaDBFIX005Cleanup,
) ServiceToken {
	t.Helper()
	cleanup.trackServiceID(serviceID)
	var token ServiceToken
	if existing == nil {
		scopes := []string{"service.register", "service.heartbeat"}
		if serviceType == "encoder_recorder" {
			scopes = append(scopes, "encoder.status.write")
		}
		if serviceType == "update_agent" {
			scopes = append(scopes, "updates.claim", "updates.report", "updates.authorize")
		}
		var err error
		token, err = auth.CreateServiceToken(ctx, serviceType, scopes)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		token = *existing
	}
	cleanup.trackToken(token)
	registration := ServiceRegistration{
		ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID,
		PublicURL: "https://" + serviceID + ".example.com", Port: 443, SSLEnabled: true,
	}
	if serviceType == "update_agent" {
		registration.PublicURL, registration.Port, registration.SSLEnabled = "", 0, false
		registration.TransportMode = SystemUpdateTransportPullV2
		registration.ExecutionHostID = serviceID + "-host"
	}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	return token
}
