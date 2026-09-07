package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
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
		if operation == "st_port_create" || operation == "st_port_host_lane" {
			observe(string(phase))
		}
	}))
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
// The CI database account may lack PROCESS; that remains unavailable evidence.
func LogSystemUpdatePortHostLaneWaitForTest(t *testing.T, parent context.Context, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT l.lock_table,l.lock_index,l.lock_type,l.lock_mode FROM information_schema.INNODB_LOCK_WAITS w JOIN information_schema.INNODB_LOCKS l ON l.lock_id=w.requested_lock_id WHERE l.lock_table IN (CONCAT(CHAR(96),DATABASE(),CHAR(96),'.',CHAR(96),'system_update_jobs',CHAR(96)),CONCAT(CHAR(96),DATABASE(),CHAR(96),'.',CHAR(96),'system_update_runtime_token_rotations',CHAR(96)),CONCAT(CHAR(96),DATABASE(),CHAR(96),'.',CHAR(96),'system_update_host_self_updates',CHAR(96)),CONCAT(CHAR(96),DATABASE(),CHAR(96),'.',CHAR(96),'system_update_port_transactions',CHAR(96))) LIMIT 8`)
	if err != nil {
		t.Log("ST-PORT host lane wait: available=false")
		return
	}
	defer rows.Close()
	count := 0
	for count < 8 && rows.Next() {
		var table, index, kind, mode sql.NullString
		if rows.Scan(&table, &index, &kind, &mode) != nil {
			break
		}
		name := "other"
		for _, candidate := range []string{"system_update_jobs", "system_update_runtime_token_rotations", "system_update_host_self_updates", "system_update_port_transactions"} {
			if strings.HasSuffix(table.String, ".`"+candidate+"`") {
				name = candidate
			}
		}
		t.Logf("ST-PORT host lane wait: table=%s index=%s type=%s mode=%s", name,
			stPortDiagnosticClass(index.String, "PRIMARY", "idx_system_update_jobs_execution_host_status_created", "uq_system_update_jobs_idempotency", "uq_system_update_runtime_token_rotations_active_host", "uq_system_update_host_self_updates_active_host", "idx_port_transaction_host_hold"),
			stPortDiagnosticClass(kind.String, "RECORD", "TABLE"),
			stPortDiagnosticClass(mode.String, "S", "X", "S,GAP", "X,GAP", "IS", "IX", "AUTO_INC"))
		count++
	}
	t.Logf("ST-PORT host lane wait: available=true rows=%d complete=%t", count, rows.Err() == nil)
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
