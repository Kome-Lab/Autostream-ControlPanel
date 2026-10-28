package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// The bridge observes the coordinator's completed discovery or actual locked
// transaction. It is compiled only for tests, not exported by the application.
func WithServiceEndpointMetadataPhaseForTest(ctx context.Context, observe func(string, *sql.Tx)) context.Context {
	return context.WithValue(ctx, serviceEndpointMetadataObserverContextKey{}, serviceEndpointMetadataObserver(observe))
}

func WithPullOwnershipPhaseForEndpointTest(ctx context.Context, observe func(string)) context.Context {
	return context.WithValue(ctx, mariaDBUpdaterPolicyLockObserverContextKey{}, mariaDBUpdaterPolicyLockObserver(func(operation string, phase mariaDBUpdaterPolicyLockPhase) {
		if operation == "activate_pull_updater_ownership" {
			observe(string(phase))
		}
	}))
}

func AssertServiceEndpointSourceLocksForTest(t *testing.T, ctx context.Context, db *sql.DB, hostID, updaterID, targetID string) {
	t.Helper()
	assertMariaDBServiceTokenExecutionHostRowLockHeld(t, ctx, db, hostID)
	assertMariaDBServiceTokenUpdaterPolicyRowLockHeld(t, ctx, db, updaterID)
	assertMariaDBServiceTokenServiceRowLockHeld(t, ctx, db, updaterID)
	assertMariaDBServiceTokenServiceRowLockHeld(t, ctx, db, targetID)
}

// A live INNODB_LOCK_WAITS edge proves that the independent activation has
// attempted its host lock while the endpoint transaction still owns that row.
// Connection and host identities stay in memory and parameterized SQL only.
func AssertServiceEndpointHostWaitForTest(t *testing.T, parent context.Context, application *sql.DB, heldConnectionID uint64, hostID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var schema string
	if err := application.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&schema); err != nil || schema == "" || heldConnectionID == 0 {
		t.Fatal("endpoint lock observation has no verified transaction/database identity")
	}
	diagnostic, cleanup := mariaDBPortDiagnosticPoolForTest(t, ctx, application, schema)
	defer cleanup()
	if diagnostic == nil {
		t.Fatal("endpoint lock observation diagnostic authority is unavailable")
	}
	const query = `SELECT requesting.trx_mysql_thread_id
FROM information_schema.INNODB_LOCK_WAITS waits
JOIN information_schema.INNODB_TRX requesting ON requesting.trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_TRX blocking ON blocking.trx_id = waits.blocking_trx_id
JOIN information_schema.INNODB_LOCKS requested ON requested.lock_id = waits.requested_lock_id AND requested.lock_trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_LOCKS held ON held.lock_id = waits.blocking_lock_id AND held.lock_trx_id = waits.blocking_trx_id
WHERE blocking.trx_mysql_thread_id = ?
  AND requesting.trx_mysql_thread_id <> ?
  AND requested.lock_table = ? AND held.lock_table = ?
  AND requested.lock_index = 'PRIMARY' AND held.lock_index = 'PRIMARY'
  AND requested.lock_data LIKE CONCAT('%', ?, '%')
LIMIT 1`
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waitingConnectionID uint64
		table := "`" + schema + "`.`system_update_execution_hosts`"
		err := diagnostic.QueryRowContext(ctx, query, heldConnectionID, heldConnectionID, table, table, hostID).Scan(&waitingConnectionID)
		if err == nil && waitingConnectionID != 0 && waitingConnectionID != heldConnectionID {
			t.Log("endpoint contender proof: distinct activation connection waits on the held execution-host PRIMARY row")
			return
		}
		if err != nil && err != sql.ErrNoRows {
			logMariaDBPortDiagnosticResultForTest(t, ctx, "node_endpoint_host_wait", "independent", err)
			t.Fatal("actual endpoint/activation database lock wait could not be verified")
		}
		select {
		case <-ctx.Done():
			t.Fatal("activation did not reach the actual held execution-host lock within its bounded observation")
		case <-ticker.C:
		}
	}
}
