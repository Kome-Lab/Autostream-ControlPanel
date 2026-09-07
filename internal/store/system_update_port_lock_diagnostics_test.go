package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

type mariaDBPortDiagnosticTransactionsContextKeyForTest struct{}

type mariaDBPortDiagnosticIdentityForTest struct {
	connectionID uint64
	schema       string
	serverHost   string
	serverPort   uint64
	available    bool
}

type mariaDBPortDiagnosticTransactionsForTest struct {
	mu          sync.Mutex
	held        mariaDBPortDiagnosticIdentityForTest
	independent mariaDBPortDiagnosticIdentityForTest
}

// CONNECTION_ID is read on the existing *sql.Tx, never a borrowed pool
// connection. The later INNODB_TRX join resolves that still-active transaction.
// Connection IDs, schema and server names remain in memory and SQL parameters.
func (s *mariaDBPortDiagnosticTransactionsForTest) capture(t *testing.T, ctx context.Context, tx *sql.Tx, side string) {
	t.Helper()
	var identity mariaDBPortDiagnosticIdentityForTest
	err := tx.QueryRowContext(ctx, `SELECT CONNECTION_ID(), DATABASE(), @@hostname, @@port`).Scan(
		&identity.connectionID, &identity.schema, &identity.serverHost, &identity.serverPort)
	logMariaDBPortDiagnosticResultForTest(t, ctx, "transaction_identity", side, err)
	identity.available = err == nil && identity.connectionID != 0 && identity.schema != "" && identity.serverHost != ""
	s.mu.Lock()
	defer s.mu.Unlock()
	if side == "held" {
		s.held = identity
	} else if side == "independent" {
		s.independent = identity
	}
}

func logMariaDBPortDiagnosticResultForTest(t *testing.T, ctx context.Context, phase, side string, err error) {
	t.Helper()
	errno, state, class := uint16(0), "00000", "none"
	timedOut, canceled := errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled)
	if err != nil {
		timedOut = timedOut || errors.Is(ctx.Err(), context.DeadlineExceeded)
		canceled = canceled || errors.Is(ctx.Err(), context.Canceled)
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		timedOut = timedOut || networkError.Timeout()
	}
	if err != nil {
		state, class = "none", "other"
	}
	var serverError *mysql.MySQLError
	if errors.As(err, &serverError) {
		errno, class = serverError.Number, "server"
		validState := true
		for _, ch := range serverError.SQLState {
			if !(ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z') {
				validState = false
			}
		}
		if validState {
			state = string(serverError.SQLState[:])
		}
		switch serverError.Number {
		case 1044, 1045, 1142, 1227:
			class = "permission"
		case 1049, 1054, 1064, 1109, 1146:
			class = "schema_query"
		case 1205:
			class = "lock_timeout"
		case 1213:
			class = "deadlock"
		case 2002, 2003, 2006, 2013, 2055:
			class = "connection"
		}
	} else if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) || networkError != nil {
		class = "connection"
	} else if errors.Is(err, sql.ErrNoRows) {
		class = "no_rows"
	}
	if canceled {
		class = "canceled"
	} else if timedOut {
		class = "timeout"
	}
	t.Logf("ST-PORT lock diagnostic result: phase=%s side=%s ok=%t errno=%d sqlstate=%s class=%s timeout=%t canceled=%t",
		phase, side, err == nil, errno, state, class, timedOut, canceled)
}

func mariaDBPortDiagnosticPoolForTest(t *testing.T, ctx context.Context, application *sql.DB, schema string) (*sql.DB, func()) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("AUTOSTREAM_MARIADB_LOCK_DIAGNOSTIC_DSN"))
	if dsn == "" {
		t.Log("ST-PORT lock diagnostic input: management_configured=false parsed=false endpoint_match=false schema_match=false")
		return application, func() {}
	}
	management, managementErr := mysql.ParseDSN(dsn)
	mutation, mutationErr := mysql.ParseDSN(strings.TrimPrefix(strings.TrimSpace(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")), "mysql://"))
	parsed := managementErr == nil && mutationErr == nil
	endpointMatch, schemaMatch := false, false
	if parsed {
		endpointMatch = management.Net == mutation.Net && management.Addr == mutation.Addr
		schemaMatch = management.DBName == schema && mutation.DBName == schema
	}
	t.Logf("ST-PORT lock diagnostic input: management_configured=true parsed=%t endpoint_match=%t schema_match=%t", parsed, endpointMatch, schemaMatch)
	if !parsed || !endpointMatch || !schemaMatch {
		return nil, func() {}
	}
	// Suppress only this diagnostic connection's free-form driver messages.
	// The application's connection, logger and permissions are unchanged.
	management.Logger = &mysql.NopLogger{}
	for _, duration := range []*time.Duration{&management.Timeout, &management.ReadTimeout, &management.WriteTimeout} {
		if *duration <= 0 || *duration > 2*time.Second {
			*duration = 2 * time.Second
		}
	}
	connector, err := mysql.NewConnector(management)
	logMariaDBPortDiagnosticResultForTest(t, ctx, "management_connector", "none", err)
	if err != nil {
		return nil, func() {}
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	return db, func() { _ = db.Close() }
}

// The source lock barrier still holds A while B is pending at the existing
// two-second timer. At most eight A/B edges from the current schema are emitted.
// This does not acquire application locks or grant the application PROCESS.
func logMariaDBPortWaitEdgeForTest(t *testing.T, parent context.Context, application *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	state, ok := parent.Value(mariaDBPortDiagnosticTransactionsContextKeyForTest{}).(*mariaDBPortDiagnosticTransactionsForTest)
	var held, independent mariaDBPortDiagnosticIdentityForTest
	if ok && state != nil {
		state.mu.Lock()
		held, independent = state.held, state.independent
		state.mu.Unlock()
	}
	captured := held.available && independent.available
	distinct := captured && held.connectionID != independent.connectionID
	sameSchema := captured && held.schema == independent.schema
	sameServer := captured && held.serverHost == independent.serverHost && held.serverPort == independent.serverPort
	t.Logf("ST-PORT lock diagnostic scope: captured=%t distinct_connections=%t same_schema=%t same_server=%t", captured, distinct, sameSchema, sameServer)
	if !distinct || !sameSchema || !sameServer {
		return
	}
	db, cleanup := mariaDBPortDiagnosticPoolForTest(t, ctx, application, held.schema)
	defer cleanup()
	if db == nil {
		return
	}
	before := db.Stats()
	conn, err := db.Conn(ctx)
	after := db.Stats()
	t.Logf("ST-PORT lock diagnostic connection: acquired=%t pool_waited=%t pool_saturated=%t", err == nil,
		after.WaitCount > before.WaitCount, before.MaxOpenConnections > 0 && before.InUse >= before.MaxOpenConnections)
	logMariaDBPortDiagnosticResultForTest(t, ctx, "connection", "none", err)
	if err != nil {
		return
	}
	defer conn.Close()
	var schema, serverHost string
	var serverPort uint64
	err = conn.QueryRowContext(ctx, `SELECT DATABASE(), @@hostname, @@port`).Scan(&schema, &serverHost, &serverPort)
	logMariaDBPortDiagnosticResultForTest(t, ctx, "connection_identity", "none", err)
	identityMatch := err == nil && schema == held.schema && serverHost == held.serverHost && serverPort == held.serverPort
	t.Logf("ST-PORT lock diagnostic connection identity: matches_transactions=%t", identityMatch)
	if !identityMatch {
		return
	}
	const query = `SELECT requesting.trx_mysql_thread_id, blocking.trx_mysql_thread_id,
requested.lock_table, requested.lock_index, requested.lock_type, requested.lock_mode,
held.lock_table, held.lock_index, held.lock_type, held.lock_mode
FROM information_schema.INNODB_LOCK_WAITS waits
JOIN information_schema.INNODB_TRX requesting ON requesting.trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_TRX blocking ON blocking.trx_id = waits.blocking_trx_id
JOIN information_schema.INNODB_LOCKS requested ON requested.lock_id = waits.requested_lock_id AND requested.lock_trx_id = waits.requesting_trx_id
JOIN information_schema.INNODB_LOCKS held ON held.lock_id = waits.blocking_lock_id AND held.lock_trx_id = waits.blocking_trx_id
WHERE ((requesting.trx_mysql_thread_id = ? AND blocking.trx_mysql_thread_id = ?) OR
       (requesting.trx_mysql_thread_id = ? AND blocking.trx_mysql_thread_id = ?))
AND requested.lock_table IN (?, ?, ?, ?)
AND held.lock_table IN (?, ?, ?, ?)
LIMIT 9`
	tables := []string{"system_update_jobs", "system_update_runtime_token_rotations", "system_update_host_self_updates", "system_update_port_transactions"}
	args := []any{independent.connectionID, held.connectionID, held.connectionID, independent.connectionID}
	for side := 0; side < 2; side++ {
		for _, table := range tables {
			args = append(args, "`"+schema+"`.`"+table+"`")
		}
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	logMariaDBPortDiagnosticResultForTest(t, ctx, "wait_query", "none", err)
	if err != nil {
		t.Log("ST-PORT lock diagnostic summary: query_ok=false rows=0 complete=false limit_reached=false")
		return
	}
	defer rows.Close()
	count, complete, limitReached := 0, true, false
	for rows.Next() {
		if count == 8 {
			limitReached, complete = true, false
			break
		}
		var requester, blocker uint64
		var requestFields, heldFields [4]sql.NullString
		if err := rows.Scan(&requester, &blocker, &requestFields[0], &requestFields[1], &requestFields[2], &requestFields[3],
			&heldFields[0], &heldFields[1], &heldFields[2], &heldFields[3]); err != nil {
			logMariaDBPortDiagnosticResultForTest(t, ctx, "wait_scan", "none", err)
			complete = false
			break
		}
		role := func(connectionID uint64) string {
			if connectionID == held.connectionID {
				return "held"
			}
			if connectionID == independent.connectionID {
				return "independent"
			}
			return "other"
		}
		project := func(fields [4]sql.NullString) [4]string {
			out := [4]string{"other", "", "", ""}
			for _, table := range tables {
				if fields[0].String == "`"+schema+"`.`"+table+"`" {
					out[0] = table
				}
			}
			out[1] = stPortDiagnosticClass(fields[1].String, "PRIMARY", "idx_system_update_jobs_execution_host_status_created", "uq_system_update_jobs_idempotency", "uq_system_update_runtime_token_rotations_active_host", "uq_system_update_host_self_updates_active_host", "idx_port_transaction_host_hold")
			out[2] = stPortDiagnosticClass(fields[2].String, "RECORD", "TABLE")
			out[3] = stPortDiagnosticClass(fields[3].String, "S", "X", "S,GAP", "X,GAP", "IS", "IX", "AUTO_INC")
			return out
		}
		requestRole, blockingRole := role(requester), role(blocker)
		request, blocking := project(requestFields), project(heldFields)
		if requestRole == "other" || blockingRole == "other" || requestRole == blockingRole || request[0] == "other" || blocking[0] == "other" {
			t.Log("ST-PORT lock diagnostic edge: scope_valid=false")
			complete = false
			break
		}
		t.Logf("ST-PORT lock diagnostic edge: requester=%s blocker=%s requested_table=%s requested_index=%s requested_type=%s requested_mode=%s blocking_table=%s blocking_index=%s blocking_type=%s blocking_mode=%s",
			requestRole, blockingRole, request[0], request[1], request[2], request[3], blocking[0], blocking[1], blocking[2], blocking[3])
		count++
	}
	err = rows.Err()
	logMariaDBPortDiagnosticResultForTest(t, ctx, "wait_rows", "none", err)
	t.Logf("ST-PORT lock diagnostic summary: query_ok=true rows=%d complete=%t limit_reached=%t", count, complete && err == nil, limitReached)
}
