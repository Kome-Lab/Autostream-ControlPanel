package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

type mariaDBFIX011ReferenceMutation struct {
	serviceID string
	column    string
	tokenID   string
}

type mariaDBFIX011ReferenceRaceRecorder struct {
	t           *testing.T
	ctx         context.Context
	db          *sql.DB
	operation   string
	mutations   []mariaDBFIX011ReferenceMutation
	commitCount int
	events      []string
}

func newMariaDBFIX011ReferenceRaceRecorder(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	operation string,
	mutations []mariaDBFIX011ReferenceMutation,
) *mariaDBFIX011ReferenceRaceRecorder {
	t.Helper()
	return &mariaDBFIX011ReferenceRaceRecorder{
		t:         t,
		ctx:       ctx,
		db:        db,
		operation: operation,
		mutations: append([]mariaDBFIX011ReferenceMutation(nil), mutations...),
	}
}

func (recorder *mariaDBFIX011ReferenceRaceRecorder) observe(
	operation string,
	phase mariaDBServiceTokenLockPhase,
) {
	if operation != recorder.operation {
		return
	}
	recorder.events = append(recorder.events, string(phase))
	if phase != mariaDBServiceTokenTokenLocksHeld ||
		recorder.commitCount >= len(recorder.mutations) {
		return
	}
	mutation := recorder.mutations[recorder.commitCount]
	setMariaDBFIX010ServiceTokenReference(
		recorder.t,
		recorder.ctx,
		recorder.db,
		mutation.serviceID,
		mutation.column,
		mutation.tokenID,
	)
	recorder.commitCount++
	recorder.events = append(recorder.events, "external_reference_committed")
}

func clearMariaDBFIX011ServiceTokenReference(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID, column string,
) {
	t.Helper()
	var query string
	switch column {
	case "staged_node_previous_token_id":
		query = `UPDATE services SET staged_node_previous_token_id = NULL WHERE service_id = ?`
	case "staged_node_token_id":
		query = `UPDATE services SET staged_node_token_id = NULL WHERE service_id = ?`
	default:
		t.Fatalf("unsupported FIX-011 reference column %q", column)
	}
	result, err := db.ExecContext(ctx, query, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("clear FIX-011 reference affected=%d err=%v", affected, err)
	}
}

func assertMariaDBFIX011ServicesUnchanged(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	before ...RegisteredService,
) {
	t.Helper()
	for _, expected := range before {
		actual, err := auth.GetService(ctx, expected.ServiceID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf(
				"FIX-011 operation changed service state: before=%s after=%s",
				formatSafeRegisteredServiceDiagnostic(expected),
				formatSafeRegisteredServiceDiagnostic(actual),
			)
		}
	}
}

func assertMariaDBFIX011RetryPhaseOrder(t *testing.T, events []string) {
	t.Helper()
	assertMariaDBFIX011PhaseSubsequence(t, events, []string{
		"reference_discovery_complete",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		"external_reference_committed",
		"reference_set_mismatch",
		"reference_retry_start",
		"reference_discovery_complete",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		string(mariaDBServiceTokenBindingsValidated),
		"stable_auth_replay_conflict",
	})
	for event, want := range map[string]int{
		"reference_discovery_complete": 2,
		"reference_set_mismatch":       1,
		"reference_retry_start":        1,
		"stable_auth_replay_conflict":  1,
	} {
		if got := countMariaDBFIX011Event(events, event); got != want {
			t.Fatalf("%s phases = %d, want %d; events=%v", event, got, want, events)
		}
	}
}

func assertMariaDBFIX011StableConflictPhases(t *testing.T, events []string) {
	t.Helper()
	for event, want := range map[string]int{
		"reference_discovery_complete": 1,
		"reference_set_mismatch":       0,
		"reference_retry_start":        0,
		"stable_auth_replay_conflict":  1,
	} {
		if got := countMariaDBFIX011Event(events, event); got != want {
			t.Fatalf("%s phases = %d, want %d; events=%v", event, got, want, events)
		}
	}
}

func assertMariaDBFIX011ExhaustionPhases(t *testing.T, events []string) {
	t.Helper()
	if got := countMariaDBFIX011Event(events, "reference_discovery_complete"); got != mariaDBServiceTokenReferenceRetryLimit {
		t.Fatalf("reference discovery phases = %d, want %d; events=%v", got, mariaDBServiceTokenReferenceRetryLimit, events)
	}
	if got := countMariaDBFIX011Event(events, "reference_set_mismatch"); got != mariaDBServiceTokenReferenceRetryLimit {
		t.Fatalf("reference mismatch phases = %d, want %d; events=%v", got, mariaDBServiceTokenReferenceRetryLimit, events)
	}
	if got := countMariaDBFIX011Event(events, "reference_retry_start"); got != mariaDBServiceTokenReferenceRetryLimit-1 {
		t.Fatalf("reference retry phases = %d, want %d; events=%v", got, mariaDBServiceTokenReferenceRetryLimit-1, events)
	}
	if got := countMariaDBFIX011Event(events, "stable_auth_replay_conflict"); got != 0 {
		t.Fatalf("retry exhaustion reached stable classification %d times; events=%v", got, events)
	}
}

func countMariaDBFIX011Event(events []string, expected string) int {
	count := 0
	for _, event := range events {
		if event == expected {
			count++
		}
	}
	return count
}

func assertMariaDBFIX011PhaseSubsequence(t *testing.T, events, expected []string) {
	t.Helper()
	next := 0
	for _, event := range events {
		if next < len(expected) && event == expected[next] {
			next++
		}
	}
	if next != len(expected) {
		t.Fatalf("lock-phase subsequence stopped at %d/%d; events=%v", next, len(expected), events)
	}
}
