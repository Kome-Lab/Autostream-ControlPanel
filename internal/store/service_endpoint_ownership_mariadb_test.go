package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

type nodeEndpointMetadataResult struct {
	service store.RegisteredService
	err     error
}

type nodeEndpointActivationResult struct {
	activation store.ActivatePullUpdaterOwnershipResult
	err        error
}

// The target configuration digest is already seeded, so legal activation only
// changes the separate agent/host ownership authority. Target metadata CAS by
// itself cannot detect the intervening activation demonstrated by this case.
func TestMariaDBNodeEndpointMetadataActivationWinsBeforeHostFence(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	f := newMariaDBPullActivationFixture(t, ctx, db, false)
	before := prepareNodeEndpointOwnershipFixture(t, ctx, db, &f)
	baseline := store.ServiceMetadataRevision(before)
	update := nodeEndpointHostUpdate(before, "node-edited.example.com", baseline)
	ready := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var observerOnce sync.Once
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		waitForNodeEndpointWorkers(t, &workers)
	})
	endpointCtx := store.WithServiceEndpointMetadataPhaseForTest(ctx, func(phase string, tx *sql.Tx) {
		if phase != "discovered" {
			return
		}
		observerOnce.Do(func() {
			if tx != nil {
				return
			}
			ready <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	})
	endpointResult := make(chan nodeEndpointMetadataResult, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		service, err := f.auth.UpdateServiceMetadataWithEndpointOwnership(endpointCtx, f.policies, f.updates, f.targetID, update)
		endpointResult <- nodeEndpointMetadataResult{service, err}
	}()
	receiveNodeEndpointSignal(t, ctx, ready, "completed endpoint authority discovery")
	activated, err := f.policies.ActivatePullUpdaterOwnership(ctx, f.auth, f.updates, f.params)
	if err != nil {
		t.Fatal("actual activation contender did not commit before the endpoint host fence")
	}
	assertNodeEndpointActivatedOwnership(t, ctx, f, activated)
	afterActivation, err := f.auth.GetService(ctx, f.targetID)
	if err != nil || store.ServiceMetadataRevision(afterActivation) != baseline {
		t.Fatal("fixture did not preserve target metadata authority across separate ownership activation")
	}
	releaseOnce.Do(func() { close(release) })
	result := receiveNodeEndpointResult(t, ctx, endpointResult)
	if !errors.Is(result.err, store.ErrServiceEndpointManagedByUpdater) {
		t.Fatal("endpoint write did not reject the activation that won the real ownership fence")
	}
	after, err := f.auth.GetService(ctx, f.targetID)
	if err != nil || !sameNodeEndpointConfiguration(before, after) {
		t.Fatal("rejected endpoint contender changed target metadata or endpoint authority")
	}
	assertNodeEndpointActivatedOwnership(t, ctx, f, activated)
}

// The public FQDN changes, while the explicit policy-owned loopback listener and
// observed target configuration remain identical. Activation must wait for the
// endpoint transaction, then validly activate against the committed endpoint.
func TestMariaDBNodeEndpointMetadataHostFenceSerializesActivation(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	f := newMariaDBPullActivationFixture(t, ctx, db, false)
	before := prepareNodeEndpointOwnershipFixture(t, ctx, db, &f)
	baseline := store.ServiceMetadataRevision(before)
	update := nodeEndpointHostUpdate(before, "node-edited.example.com", baseline)
	type heldTransaction struct {
		connectionID uint64
		err          error
	}
	held := make(chan heldTransaction, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var observerOnce sync.Once
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		waitForNodeEndpointWorkers(t, &workers)
	})
	endpointCtx := store.WithServiceEndpointMetadataPhaseForTest(ctx, func(phase string, tx *sql.Tx) {
		if phase != "service_locks_held" {
			return
		}
		observerOnce.Do(func() {
			capture := heldTransaction{}
			if tx == nil {
				capture.err = errors.New("missing actual endpoint transaction")
			} else {
				capture.err = tx.QueryRowContext(ctx, `SELECT CONNECTION_ID()`).Scan(&capture.connectionID)
			}
			held <- capture
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	})
	endpointResult := make(chan nodeEndpointMetadataResult, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		service, err := f.auth.UpdateServiceMetadataWithEndpointOwnership(endpointCtx, f.policies, f.updates, f.targetID, update)
		endpointResult <- nodeEndpointMetadataResult{service, err}
	}()
	var transaction heldTransaction
	select {
	case transaction = <-held:
	case <-ctx.Done():
		t.Fatal("endpoint operation did not reach its actual service-lock boundary")
	case <-time.After(5 * time.Second):
		t.Fatal("endpoint operation did not reach its bounded service-lock boundary")
	}
	if transaction.err != nil || transaction.connectionID == 0 {
		t.Fatal("endpoint lock boundary did not retain a real transaction identity")
	}
	store.AssertServiceEndpointSourceLocksForTest(t, ctx, db, f.params.ExecutionHostID, f.params.ServiceID, f.targetID)
	activationAtHost := make(chan struct{}, 1)
	var activationOnce sync.Once
	activationCtx := store.WithPullOwnershipPhaseForEndpointTest(ctx, func(phase string) {
		if phase == "before_host_lock" {
			activationOnce.Do(func() { activationAtHost <- struct{}{} })
		}
	})
	activationResult := make(chan nodeEndpointActivationResult, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		activation, err := f.policies.ActivatePullUpdaterOwnership(activationCtx, f.auth, f.updates, f.params)
		activationResult <- nodeEndpointActivationResult{activation, err}
	}()
	receiveNodeEndpointSignal(t, ctx, activationAtHost, "activation host-lock attempt")
	store.AssertServiceEndpointHostWaitForTest(t, ctx, db, transaction.connectionID, f.params.ExecutionHostID)
	releaseOnce.Do(func() { close(release) })
	result := receiveNodeEndpointResult(t, ctx, endpointResult)
	if result.err != nil || result.service.ServiceID != f.targetID || result.service.Host != update.Host || result.service.EndpointRevision != before.EndpointRevision+1 {
		t.Fatal("endpoint owner of the actual host fence did not commit its one metadata write")
	}
	var activation nodeEndpointActivationResult
	select {
	case activation = <-activationResult:
	case <-ctx.Done():
		t.Fatal("activation contender did not finish after the host fence was released")
	case <-time.After(5 * time.Second):
		t.Fatal("activation contender exceeded the bounded post-fence wait")
	}
	if activation.err != nil {
		t.Fatal("activation did not revalidate and commit after the public-endpoint-only change")
	}
	assertNodeEndpointActivatedOwnership(t, ctx, f, activation.activation)
	after, err := f.auth.GetService(ctx, f.targetID)
	if err != nil || after.Host != update.Host || after.PublicURL != update.PublicURL || after.ServiceName != before.ServiceName || after.Description != before.Description || after.Port != before.Port || after.EndpointRevision != before.EndpointRevision+1 || after.AppliedConfigSHA256 != before.AppliedConfigSHA256 || after.AppliedConfigRevision != before.AppliedConfigRevision {
		t.Fatal("serialized endpoint/activation pair did not preserve its exact target configuration")
	}
	if after.DesiredEndpoint == nil || after.AppliedEndpoint == nil || *after.DesiredEndpoint != *after.AppliedEndpoint {
		t.Fatal("serialized endpoint write did not retain a coherent desired/applied endpoint pair")
	}
	current := store.ServiceMetadataRevision(after)
	blocked := nodeEndpointHostUpdate(after, "another-node.example.com", current)
	if _, err := f.auth.UpdateServiceMetadataWithEndpointOwnership(ctx, f.policies, f.updates, f.targetID, blocked); !errors.Is(err, store.ErrServiceEndpointManagedByUpdater) {
		t.Fatal("a later endpoint edit was accepted after ownership activation committed")
	}
	unchanged, err := f.auth.GetService(ctx, f.targetID)
	if err != nil || !sameNodeEndpointConfiguration(after, unchanged) {
		t.Fatal("post-activation rejection changed the serialized endpoint result")
	}
}

func prepareNodeEndpointOwnershipFixture(t *testing.T, ctx context.Context, db *sql.DB, f *mariaDBPullActivationFixture) store.RegisteredService {
	t.Helper()
	before, err := f.auth.GetService(ctx, f.targetID)
	if err != nil {
		t.Fatal("endpoint ownership fixture target is unavailable")
	}
	agent, err := f.auth.GetService(ctx, f.params.ServiceID)
	if err != nil {
		t.Fatal("endpoint ownership fixture agent is unavailable")
	}
	registerNodeEndpointFixtureCleanup(t, db, *f, before.TokenID, agent.TokenID)
	// Public activation/deactivation leaves a real host row at epoch 2 and an
	// observe-only agent at epoch 0. Missing-row gap locks would not demonstrate
	// the required row-lock wait between the two real contenders below.
	activated, err := f.policies.ActivatePullUpdaterOwnership(ctx, f.auth, f.updates, f.params)
	if err != nil {
		t.Fatal("endpoint ownership fixture could not establish its first real host ownership")
	}
	deactivated, err := f.policies.DeactivatePullUpdaterOwnership(ctx, f.auth, f.updates, store.DeactivatePullUpdaterOwnershipParams{
		ServiceID: f.params.ServiceID, ExecutionHostID: f.params.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activated.Policy.Revision, ExpectedProjectionRevision: activated.Policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activated.Policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   activated.Policy.LocalExecutorPolicySHA256,
	})
	if err != nil || deactivated.Ownership.OwnershipEpoch != 2 || deactivated.Service.OwnershipEpoch != 0 {
		t.Fatal("endpoint ownership fixture did not reach its legitimate persisted observe-only host state")
	}
	f.params.ExpectedExecutionHostOwnershipEpoch = deactivated.Ownership.OwnershipEpoch
	before, err = f.auth.GetService(ctx, f.targetID)
	if err != nil || before.AppliedConfigSHA256 != "sha256:"+strings.Repeat("c", 64) || before.AppliedConfigRevision != 1 || before.Port != 18081 {
		t.Fatal("endpoint ownership fixture did not retain the required pre-activation target baseline")
	}
	return before
}

func nodeEndpointHostUpdate(before store.RegisteredService, host, revision string) store.ServiceMetadataUpdate {
	return store.ServiceMetadataUpdate{
		ExpectedMetadataRevision: &revision, ServiceName: before.ServiceName, Description: before.Description,
		Host: host, Port: before.Port, SSLEnabled: before.SSLEnabled,
		PublicURL: "https://" + host + ":18081",
	}
}

func sameNodeEndpointConfiguration(left, right store.RegisteredService) bool {
	return left.ServiceName == right.ServiceName && left.Description == right.Description && left.Host == right.Host && left.Port == right.Port && left.SSLEnabled == right.SSLEnabled && left.PublicURL == right.PublicURL && left.EndpointRevision == right.EndpointRevision && left.EndpointStatus == right.EndpointStatus && reflect.DeepEqual(left.DesiredEndpoint, right.DesiredEndpoint) && reflect.DeepEqual(left.AppliedEndpoint, right.AppliedEndpoint)
}

func assertNodeEndpointActivatedOwnership(t *testing.T, ctx context.Context, f mariaDBPullActivationFixture, activation store.ActivatePullUpdaterOwnershipResult) {
	t.Helper()
	host, err := f.updates.GetSystemUpdateExecutionHost(ctx, f.params.ExecutionHostID)
	if err != nil || host.OwnershipEpoch != f.params.ExpectedExecutionHostOwnershipEpoch+1 || host.AgentServiceID != f.params.ServiceID || host.PolicyRevision != f.params.ExpectedProjectionRevision || host.TransportMode != store.SystemUpdateTransportPullV2 || activation.Ownership.OwnershipEpoch != host.OwnershipEpoch {
		t.Fatal("activation contender did not retain the exact execution-host ownership binding")
	}
	agent, err := f.auth.GetService(ctx, f.params.ServiceID)
	if err != nil || agent.OwnershipEpoch != host.OwnershipEpoch || agent.ExecutionHostID != host.ExecutionHostID {
		t.Fatal("activation contender left a partial agent/host ownership pair")
	}
	reservations, err := f.updates.ListServicePortReservations(ctx, f.params.ExecutionHostID)
	if err != nil || len(reservations) != 1 || reservations[0].ServiceID != f.targetID || reservations[0].Port != 18081 || reservations[0].ServiceRole != "api" || reservations[0].NetworkNamespace != "host" || reservations[0].Protocol != "tcp" {
		t.Fatal("activation contender did not retain the policy-owned local-listener reservation")
	}
}

func receiveNodeEndpointSignal(t *testing.T, ctx context.Context, ready <-chan struct{}, phase string) {
	t.Helper()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatalf("Node endpoint contender did not reach %s", phase)
	case <-time.After(5 * time.Second):
		t.Fatalf("Node endpoint contender exceeded its bounded wait for %s", phase)
	}
}

func receiveNodeEndpointResult(t *testing.T, ctx context.Context, result <-chan nodeEndpointMetadataResult) nodeEndpointMetadataResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-ctx.Done():
		t.Fatal("Node endpoint contender did not finish")
	case <-time.After(5 * time.Second):
		t.Fatal("Node endpoint contender exceeded its bounded completion wait")
	}
	return nodeEndpointMetadataResult{}
}

func waitForNodeEndpointWorkers(t *testing.T, workers *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("Node endpoint fixture retained a contender after release/cancel")
	}
}

func registerNodeEndpointFixtureCleanup(t *testing.T, db *sql.DB, f mariaDBPullActivationFixture, targetTokenID, agentTokenID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Error("Node endpoint fixture cleanup could not begin")
			return
		}
		defer tx.Rollback()
		for _, step := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM service_port_reservations WHERE execution_host_id = ?`, []any{f.params.ExecutionHostID}},
			{`DELETE FROM update_agent_target_local_listeners WHERE updater_service_id = ?`, []any{f.params.ServiceID}},
			{`DELETE FROM update_agent_target_databases WHERE updater_service_id = ?`, []any{f.params.ServiceID}},
			{`DELETE FROM update_agent_policies WHERE service_id = ?`, []any{f.params.ServiceID}},
			{`DELETE FROM service_metric_snapshots WHERE service_id IN (?, ?)`, []any{f.targetID, f.params.ServiceID}},
			{`DELETE FROM system_update_execution_hosts WHERE execution_host_id = ?`, []any{f.params.ExecutionHostID}},
			{`DELETE FROM services WHERE service_id IN (?, ?)`, []any{f.targetID, f.params.ServiceID}},
			{`DELETE FROM service_tokens WHERE id IN (?, ?)`, []any{targetTokenID, agentTokenID}},
		} {
			if _, err := tx.ExecContext(ctx, step.query, step.args...); err != nil {
				t.Error("Node endpoint fixture exact-ID cleanup failed")
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Error("Node endpoint fixture cleanup did not commit")
			return
		}
		var residue int
		if err := db.QueryRowContext(ctx, `SELECT
(SELECT COUNT(*) FROM services WHERE service_id IN (?, ?)) +
(SELECT COUNT(*) FROM service_tokens WHERE id IN (?, ?)) +
(SELECT COUNT(*) FROM update_agent_policies WHERE service_id = ?) +
(SELECT COUNT(*) FROM system_update_execution_hosts WHERE execution_host_id = ?) +
(SELECT COUNT(*) FROM service_port_reservations WHERE execution_host_id = ?)`,
			f.targetID, f.params.ServiceID, targetTokenID, agentTokenID, f.params.ServiceID, f.params.ExecutionHostID, f.params.ExecutionHostID).Scan(&residue); err != nil || residue != 0 {
			t.Error("Node endpoint fixture retained rows after exact-ID cleanup")
		}
	})
}
