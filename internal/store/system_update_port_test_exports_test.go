package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"testing"

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
