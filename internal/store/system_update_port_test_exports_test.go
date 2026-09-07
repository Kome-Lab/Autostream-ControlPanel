package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

// Test-only bridge to the existing production lock observer. The callback is
// reached after real service/token locks and binding validation, never at
// goroutine startup. No new production instrumentation or lock order is added.
func WithSystemUpdatePortLocksHeldForTest(ctx context.Context, held func()) context.Context {
	return context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation == "st_port_snapshot" && phase == mariaDBServiceTokenBindingsValidated {
			held()
		}
	}))
}

func WithSystemUpdatePortCreatePhaseForTest(ctx context.Context, observe func(string)) context.Context {
	return context.WithValue(ctx, mariaDBUpdaterPolicyLockObserverContextKey{}, mariaDBUpdaterPolicyLockObserver(func(operation string, phase mariaDBUpdaterPolicyLockPhase) {
		if operation == "st_port_create" || operation == "st_port_baseline" || operation == "st_port_host_lane" {
			observe(string(phase))
		}
	}))
}

func ResolveSystemUpdatePortV2InsertErrorForTest(ctx context.Context, updates *MariaDBSystemUpdateStore, tx *sql.Tx, params CreateSystemdPortReconfigurationJobParams, insertErr error) (SystemUpdateJob, bool, error) {
	return updates.resolveSystemUpdatePortV2InsertError(ctx, tx, params, insertErr)
}

type mariaDBPortCreateGapKeyForTest struct {
	hostID, requestedByUserID, idempotencyKey string
}

// Observe each actual create transaction after its host lock and before its
// idempotency discovery and existing-row lock. The source-lock barrier orders
// the held create before the independent create. No key values are logged.
func WithSystemUpdatePortCreateGapDiagnosticsForTest(t *testing.T, parent context.Context) (context.Context, context.Context) {
	t.Helper()
	transactions := &mariaDBPortDiagnosticTransactionsForTest{}
	parent = context.WithValue(parent, mariaDBPortDiagnosticTransactionsContextKeyForTest{}, transactions)
	var mu sync.Mutex
	var heldKey mariaDBPortCreateGapKeyForTest
	heldKeyAvailable := false
	observer := func(side string) mariaDBPortCreateTransactionObserver {
		return func(parent context.Context, tx *sql.Tx, hostID, requestedByUserID, idempotencyKey string) {
			ctx, cancel := context.WithTimeout(parent, 2*time.Second)
			defer cancel()
			transactions.capture(t, ctx, tx, side)
			key := mariaDBPortCreateGapKeyForTest{hostID: hostID, requestedByUserID: requestedByUserID, idempotencyKey: idempotencyKey}
			if side == "held" {
				mu.Lock()
				heldKey, heldKeyAvailable = key, true
				mu.Unlock()
			}
			var isolation string
			err := tx.QueryRowContext(ctx, `SELECT @@tx_isolation`).Scan(&isolation)
			t.Logf("ST-PORT create transaction: side=%s isolation_available=%t isolation=%s", side, err == nil,
				stPortDiagnosticClass(isolation, "READ-UNCOMMITTED", "READ-COMMITTED", "REPEATABLE-READ", "SERIALIZABLE"))
			logMariaDBPortCreateIdempotencyPlanForTest(t, ctx, tx, side, key)
			if side != "independent" {
				return
			}
			mu.Lock()
			first, available := heldKey, heldKeyAvailable
			mu.Unlock()
			t.Logf("ST-PORT create gap keys: held_available=%t host_distinct=%t request_key_distinct=%t", available,
				available && first.hostID != key.hostID,
				available && (first.requestedByUserID != key.requestedByUserID || first.idempotencyKey != key.idempotencyKey))
			if available {
				logMariaDBPortCreateGapBoundsForTest(t, ctx, tx, first, key)
			}
		}
	}
	return context.WithValue(parent, mariaDBPortCreateTransactionObserverContextKey{}, observer("held")),
		context.WithValue(parent, mariaDBPortCreateTransactionObserverContextKey{}, observer("independent"))
}

func logMariaDBPortCreateIdempotencyPlanForTest(t *testing.T, ctx context.Context, tx *sql.Tx, side string, key mariaDBPortCreateGapKeyForTest) {
	t.Helper()
	rows, err := tx.QueryContext(ctx, "EXPLAIN "+mariaDBPortCreateIdempotencyQuery, key.requestedByUserID, key.idempotencyKey)
	if err != nil {
		t.Logf("ST-PORT idempotency plan: side=%s available=false", side)
		return
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Logf("ST-PORT idempotency plan: side=%s columns_available=false", side)
		return
	}
	for count := 0; count < 8 && rows.Next(); count++ {
		values, dest := make([]sql.NullString, len(columns)), make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if rows.Scan(dest...) != nil {
			break
		}
		fields := make(map[string]string, len(columns))
		for i, column := range columns {
			fields[column] = values[i].String
		}
		t.Logf("ST-PORT idempotency plan: side=%s table=%s access=%s index=%s filesort=%t", side,
			stPortDiagnosticClass(fields["table"], "system_update_jobs", "p"),
			stPortDiagnosticClass(fields["type"], "ALL", "index", "range", "ref", "eq_ref", "const", "system"),
			stPortDiagnosticClass(fields["key"], "PRIMARY", "uq_system_update_jobs_idempotency", "idx_system_update_jobs_execution_host_status_created"),
			strings.Contains(fields["Extra"], "Using filesort"))
	}
}

// A consistent, nonlocking view of index neighbours establishes whether absent
// logical keys lie in the same physical gap. It does not identify a live lock
// wait, and no row identifier or indexed value is emitted.
func logMariaDBPortCreateGapBoundsForTest(t *testing.T, ctx context.Context, tx *sql.Tx, first, second mariaDBPortCreateGapKeyForTest) {
	t.Helper()
	type probe struct {
		index, exists, predecessor, successor string
		args                                  func(mariaDBPortCreateGapKeyForTest) []any
	}
	probes := []probe{
		{
			index:       "uq_system_update_jobs_idempotency",
			exists:      `SELECT id FROM system_update_jobs FORCE INDEX (uq_system_update_jobs_idempotency) WHERE requested_by_user_id=? AND idempotency_key=? LIMIT 1`,
			predecessor: `SELECT id FROM system_update_jobs FORCE INDEX (uq_system_update_jobs_idempotency) WHERE (requested_by_user_id,idempotency_key)<(?,?) ORDER BY requested_by_user_id DESC,idempotency_key DESC LIMIT 1`,
			successor:   `SELECT id FROM system_update_jobs FORCE INDEX (uq_system_update_jobs_idempotency) WHERE (requested_by_user_id,idempotency_key)>(?,?) ORDER BY requested_by_user_id,idempotency_key LIMIT 1`,
			args: func(key mariaDBPortCreateGapKeyForTest) []any {
				return []any{key.requestedByUserID, key.idempotencyKey}
			},
		},
		{
			index:       "idx_system_update_jobs_execution_host_status_created",
			exists:      `SELECT id FROM system_update_jobs FORCE INDEX (idx_system_update_jobs_execution_host_status_created) WHERE execution_host_id=? LIMIT 1`,
			predecessor: `SELECT id FROM system_update_jobs FORCE INDEX (idx_system_update_jobs_execution_host_status_created) WHERE execution_host_id<? ORDER BY execution_host_id DESC,status DESC,created_at DESC,id DESC LIMIT 1`,
			successor:   `SELECT id FROM system_update_jobs FORCE INDEX (idx_system_update_jobs_execution_host_status_created) WHERE execution_host_id>? ORDER BY execution_host_id,status,created_at,id LIMIT 1`,
			args: func(key mariaDBPortCreateGapKeyForTest) []any {
				return []any{key.hostID}
			},
		},
	}
	read := func(query string, args []any) (sql.NullString, error) {
		var id string
		err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return sql.NullString{}, nil
		}
		return sql.NullString{String: id, Valid: err == nil}, err
	}
	for _, current := range probes {
		var values [2][3]sql.NullString
		available := true
		for i, key := range []mariaDBPortCreateGapKeyForTest{first, second} {
			for j, query := range []string{current.exists, current.predecessor, current.successor} {
				var err error
				values[i][j], err = read(query, current.args(key))
				if err != nil {
					available = false
					break
				}
			}
			if !available {
				break
			}
		}
		if !available {
			t.Logf("ST-PORT create gap bounds: index=%s available=false", current.index)
			continue
		}
		a, b := values[0], values[1]
		predecessorSame, successorSame := a[1] == b[1], a[2] == b[2]
		t.Logf("ST-PORT create gap bounds: index=%s available=true a_key_exists=%t b_key_exists=%t a_predecessor_exists=%t b_predecessor_exists=%t a_successor_exists=%t b_successor_exists=%t predecessor_same=%t successor_same=%t same_missing_gap=%t", current.index,
			a[0].Valid, b[0].Valid, a[1].Valid, b[1].Valid, a[2].Valid, b[2].Valid,
			predecessorSame, successorSame, !a[0].Valid && !b[0].Valid && predecessorSame && successorSame)
	}
}

// Inspect the exact lane query plans without executing their locking reads.
// Values, SQL text, row identifiers and lock_data are never logged.
func LogSystemUpdatePortHostLanePlansForTest(t *testing.T, parent context.Context, db *sql.DB, hostID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	for _, lane := range []struct{ name, query string }{
		{"jobs", mariaDBPortHostLaneJobsQuery},
		{"rotation", mariaDBPortHostLaneRotationQuery},
		{"self_update", mariaDBPortHostLaneSelfUpdateQuery},
	} {
		rows, err := db.QueryContext(ctx, "EXPLAIN "+lane.query, hostID)
		if err != nil {
			t.Logf("ST-PORT host lane plan: lane=%s available=false", lane.name)
			continue
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Logf("ST-PORT host lane plan: lane=%s columns_available=false", lane.name)
			continue
		}
		for count := 0; count < 8 && rows.Next(); count++ {
			values, dest := make([]sql.NullString, len(columns)), make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if rows.Scan(dest...) != nil {
				break
			}
			fields := make(map[string]string, len(columns))
			for i, column := range columns {
				fields[column] = values[i].String
			}
			t.Logf("ST-PORT host lane plan: lane=%s table=%s access=%s index=%s filesort=%t", lane.name,
				stPortDiagnosticClass(fields["table"], "system_update_jobs", "system_update_runtime_token_rotations", "system_update_host_self_updates", "p"),
				stPortDiagnosticClass(fields["type"], "ALL", "index", "range", "ref", "eq_ref", "const", "system"),
				stPortDiagnosticClass(fields["key"], "PRIMARY", "idx_system_update_jobs_execution_host_status_created", "uq_system_update_runtime_token_rotations_active_host", "uq_system_update_host_self_updates_active_host", "idx_port_transaction_host_hold"),
				strings.Contains(fields["Extra"], "Using filesort"))
		}
		rows.Close()
	}
}

// One bounded observation while the existing actual-lock barrier is held.
// The context carries only this fixture's actual A/B transaction identities.
func LogSystemUpdatePortHostLaneWaitForTest(t *testing.T, parent context.Context, db *sql.DB) {
	t.Helper()
	logMariaDBPortWaitEdgeForTest(t, parent, db)
}

func stPortDiagnosticClass(value string, allowed ...string) string {
	if value == "" {
		return "none"
	}
	for _, candidate := range allowed {
		if value == candidate {
			return candidate
		}
	}
	return "other"
}

func WithSystemUpdateLifecycleHostLockForTest(ctx context.Context, held func()) context.Context {
	ctx = context.WithValue(ctx, mariaDBUpdaterPolicyLockObserverContextKey{}, mariaDBUpdaterPolicyLockObserver(func(_ string, phase mariaDBUpdaterPolicyLockPhase) {
		if phase == mariaDBUpdaterPolicyHostLockHeld {
			held()
		}
	}))
	return context.WithValue(ctx, mariaDBRuntimeTokenRotationLockObserverContextKey{}, mariaDBRuntimeTokenRotationLockObserver(func(_ string, phase mariaDBRuntimeTokenRotationLockPhase) {
		if phase == mariaDBRuntimeTokenRotationHostLocksHeld {
			held()
		}
	}))
}

func AssertSystemUpdateHostRowLockForTest(t *testing.T, ctx context.Context, db *sql.DB, hostID string) {
	t.Helper()
	assertMariaDBFIX006ExecutionHostRowLockHeld(t, ctx, db, hostID)
}

func NewMemorySystemUpdatePortFixtureForTest(t *testing.T) (*MemoryAuthStore, *MemoryUpdaterPolicyStore, *MemorySystemUpdateStore) {
	t.Helper()
	f := newSTPortV2Fixture(t)
	addSTPortV2Observability(t, f)
	return f.registry, f.policies, f.updates
}

func MutateMemorySystemUpdatePortBeforeForTest(updates *MemorySystemUpdateStore, jobID string, mutate func(*contracts.SystemUpdatePortPolicySnapshot)) {
	updates.mu.Lock()
	defer updates.mu.Unlock()
	job := updates.jobs[jobID]
	job.portTransaction = cloneSystemUpdatePortTransaction(job.portTransaction)
	mutate(&job.portTransaction.Before.Snapshot)
	updates.jobs[jobID] = job
}

func MemorySystemUpdatePortStateForTest(auth *MemoryAuthStore, policies *MemoryUpdaterPolicyStore, updates *MemorySystemUpdateStore, jobID string) []byte {
	policies.mu.Lock()
	defer policies.mu.Unlock()
	updates.mu.Lock()
	defer updates.mu.Unlock()
	auth.mu.Lock()
	defer auth.mu.Unlock()
	job := updates.jobs[jobID]
	reservations := make([]ServicePortReservation, 0, len(updates.portReservations))
	for _, reservation := range updates.portReservations {
		reservations = append(reservations, reservation)
	}
	sort.Slice(reservations, func(i, j int) bool { return reservations[i].Port < reservations[j].Port })
	body, _ := json.Marshal(struct {
		Job          SystemUpdateJob
		Transaction  *systemUpdatePortTransaction
		Policies     map[string]UpdaterPolicy
		Services     map[string]RegisteredService
		Hosts        map[string]SystemUpdateExecutionHost
		Reservations []ServicePortReservation
	}{job, job.portTransaction, policies.policies, auth.services, updates.executionHosts, reservations})
	return body
}

func AssertSystemUpdatePortSourceLocksHeldForTest(t *testing.T, ctx context.Context, db *sql.DB, hostID, updaterID string, services []RegisteredService) {
	t.Helper()
	assertMariaDBFIX006ExecutionHostRowLockHeld(t, ctx, db, hostID)
	assertMariaDBFIX006UpdaterPolicyRowLockHeld(t, ctx, db, updaterID)
	for _, service := range services {
		assertMariaDBFIX006ServiceRowLockHeld(t, ctx, db, service.ServiceID)
		if service.TokenID != "" {
			assertMariaDBFIX007ServiceTokenRowLockHeld(t, ctx, db, service.TokenID)
		}
	}
}

// Copies the pre-operation database fixture into Memory; expected snapshots
// then come from the independently executed Memory coordinator, not DB output.
func NewMemorySystemUpdatePortSourceForTest(policy UpdaterPolicy, host SystemUpdateExecutionHost, services []RegisteredService, reservations []ServicePortReservation) (*MemoryAuthStore, *MemoryUpdaterPolicyStore, *MemorySystemUpdateStore) {
	auth := NewMemoryAuthStore()
	for _, service := range services {
		auth.services[service.ServiceID] = service
		if service.TokenID != "" {
			auth.serviceTokens[service.TokenID] = ServiceToken{ID: service.TokenID, ServiceType: service.ServiceType}
		}
	}
	policies := NewMemoryUpdaterPolicyStore()
	policies.policies[policy.UpdaterID] = cloneUpdaterPolicy(policy)
	updates := NewMemorySystemUpdateStore()
	updates.portPolicyStore = policies
	updates.executionHosts[host.ExecutionHostID] = host
	for _, reservation := range reservations {
		updates.portReservations[servicePortKey(reservation)] = reservation
	}
	return auth, policies, updates
}
