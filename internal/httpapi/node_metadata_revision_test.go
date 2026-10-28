package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

type nodeEndpointCoordinatorRaceRegistry struct {
	store.ServiceRegistryStore
	before func()
	calls  int
}

func (s *nodeEndpointCoordinatorRaceRegistry) UpdateServiceMetadataWithEndpointOwnership(ctx context.Context, policies store.UpdaterPolicyStore, hosts store.SystemUpdateExecutionHostStore, id string, update store.ServiceMetadataUpdate) (store.RegisteredService, error) {
	s.calls++
	if hook := s.before; hook != nil {
		s.before = nil
		hook()
	}
	return s.ServiceRegistryStore.(store.ServiceMetadataEndpointOwnershipStore).UpdateServiceMetadataWithEndpointOwnership(ctx, policies, hosts, id, update)
}

// This wrapper deliberately lacks the optional endpoint coordinator capability.
type nodeMetadataWithoutEndpointCoordinator struct {
	store.ServiceRegistryStore
	writes int
}

func (s *nodeMetadataWithoutEndpointCoordinator) UpdateServiceMetadata(ctx context.Context, id string, update store.ServiceMetadataUpdate) (store.RegisteredService, error) {
	s.writes++
	return s.ServiceRegistryStore.UpdateServiceMetadata(ctx, id, update)
}

func TestNodeMetadataCASRejectsStaleStampBeforeMetadataWrite(t *testing.T) {
	auth, _, _, handler, cookie, csrf := newActivePullManagedNodeEndpointFixture(t)
	before, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	response := putNodeForEndpointTest(t, handler, cookie, csrf, "worker-a", map[string]any{
		"service_name": "Stale editor", "expected_metadata_revision": strings.Repeat("a", 64),
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"node_metadata_conflict"`) {
		t.Fatalf("stale Node metadata revision was accepted: status=%d", response.Code)
	}
	after, err := auth.GetService(t.Context(), "worker-a")
	if err != nil || after.ServiceName != before.ServiceName || after.Description != before.Description || after.EndpointRevision != before.EndpointRevision {
		t.Fatal("stale Node metadata request changed persisted configuration")
	}
}

func TestNodeMetadataReadStampIgnoresMonitoringAndRejectsRealChanges(t *testing.T) {
	auth, _, _, handler, cookie, csrf := newActivePullManagedNodeEndpointFixture(t)
	read := func() string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/nodes", nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var rows []struct {
			ServiceID string `json:"service_id"`
			Revision  string `json:"metadata_revision"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &rows) != nil {
			t.Fatal("Node metadata authority read failed")
		}
		for _, row := range rows {
			if row.ServiceID == "worker-a" {
				if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(row.Revision) {
					t.Fatal("Node read omitted a valid metadata revision")
				}
				return row.Revision
			}
		}
		t.Fatal("Node authority read omitted target")
		return ""
	}
	baseline := read()
	service, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	// A heartbeat uses the real registered token identity and only changes observation.
	if _, err = auth.Heartbeat(t.Context(), store.ServiceToken{ID: service.TokenID}, store.ServiceHeartbeat{
		ServiceID: service.ServiceID, Status: "online", Metrics: map[string]any{"cpu_percent": 12},
		Version: "synthetic-observed", Hostname: "reported-host", Capabilities: map[string]any{"synthetic_capability": true},
		API: &store.NodeAgentAPI{Host: "127.0.0.1", Port: 19084},
	}); err != nil {
		t.Fatal("synthetic heartbeat failed")
	}
	if read() != baseline {
		t.Fatal("monitoring-only heartbeat changed metadata authority")
	}
	saved := putNodeForEndpointTest(t, handler, cookie, csrf, "worker-a", map[string]any{"service_name": "One saved edit", "expected_metadata_revision": baseline})
	if saved.Code != http.StatusOK {
		t.Fatalf("confirmed metadata save failed: status=%d", saved.Code)
	}
	if read() == baseline {
		t.Fatal("real metadata edit did not advance semantic authority")
	}
	stale := putNodeForEndpointTest(t, handler, cookie, csrf, "worker-a", map[string]any{"description": "Must not overwrite", "expected_metadata_revision": baseline})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale edit after save status=%d", stale.Code)
	}
}

func TestNodeMetadataStampedEndpointHTTPRechecksOwnershipAtCoordinator(t *testing.T) {
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	auth, policies, updates, _, _, _, policy := newPullActivationHTTPFixture(t, true)
	registry := &nodeEndpointCoordinatorRaceRegistry{ServiceRegistryStore: auth}
	registry.before = func() {
		ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), policy.ExecutionHostID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = policies.ActivatePullUpdaterOwnership(t.Context(), auth, updates, store.ActivatePullUpdaterOwnershipParams{
			ServiceID: policy.UpdaterID, ExecutionHostID: policy.ExecutionHostID,
			ExpectedExecutionHostOwnershipEpoch: ownership.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        policy.Revision, ExpectedProjectionRevision: policy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision, ExpectedLocalExecutorPolicySHA256: policy.LocalExecutorPolicySHA256,
		})
		if err != nil {
			t.Fatal("actual ownership activation did not commit at the HTTP/store boundary")
		}
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(registry), WithUpdaterPolicyStore(policies), WithSystemUpdateStore(updates))
	cookie, csrf := addNodeEndpointAdminForTest(t, auth, handler)
	before, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	response := putNodeForEndpointTest(t, handler, cookie, csrf, before.ServiceID, map[string]any{"host": "node-edited.example.com", "expected_metadata_revision": store.ServiceMetadataRevision(before)})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"node_endpoint_managed_by_updater"`) || registry.calls != 1 {
		t.Fatalf("actual stamped endpoint caller did not use the final ownership coordinator: status=%d calls=%d", response.Code, registry.calls)
	}
	after, err := auth.GetService(t.Context(), before.ServiceID)
	if err != nil || store.ServiceMetadataRevision(after) != store.ServiceMetadataRevision(before) {
		t.Fatal("rejected endpoint edit changed semantic metadata after separate authority activation")
	}
}

func TestNodeMetadataEndpointUnavailableHasNoFallbackAndKeepsLegacyCompatibility(t *testing.T) {
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	auth, policies, updates, _, _, _, _ := newPullActivationHTTPFixture(t, true)
	registry := &nodeMetadataWithoutEndpointCoordinator{ServiceRegistryStore: auth}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(registry), WithUpdaterPolicyStore(policies), WithSystemUpdateStore(updates))
	cookie, csrf := addNodeEndpointAdminForTest(t, auth, handler)
	before, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	stamp := store.ServiceMetadataRevision(before)
	blocked := putNodeForEndpointTest(t, handler, cookie, csrf, before.ServiceID, map[string]any{"host": "node-edited.example.com", "expected_metadata_revision": stamp})
	if blocked.Code != http.StatusServiceUnavailable || !strings.Contains(blocked.Body.String(), `"code":"node_endpoint_ownership_unavailable"`) || registry.writes != 0 {
		t.Fatal("unsupported strict endpoint authority fell back to an unfenced write")
	}
	metadata := putNodeForEndpointTest(t, handler, cookie, csrf, before.ServiceID, map[string]any{"service_name": "Metadata-only edit", "expected_metadata_revision": stamp})
	if metadata.Code != http.StatusOK || registry.writes != 1 {
		t.Fatal("metadata-only stamped edit lost its same-row fast path")
	}
	legacy := putNodeForEndpointTest(t, handler, cookie, csrf, before.ServiceID, map[string]any{"host": "node-edited.example.com"})
	if legacy.Code != http.StatusOK || registry.writes != 2 {
		t.Fatal("omitted-stamp legacy caller lost its existing preliminary-guard compatibility")
	}
}

func TestNodeMetadataMalformedExpectedStampNeverWrites(t *testing.T) {
	auth, _, _, handler, cookie, csrf := newActivePullManagedNodeEndpointFixture(t)
	before, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []any{nil, "", strings.Repeat("A", 64), strings.Repeat("a", 63), 7} {
		response := putNodeForEndpointTest(t, handler, cookie, csrf, before.ServiceID, map[string]any{"service_name": "Must not apply", "expected_metadata_revision": invalid})
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_metadata_revision"`) {
			t.Fatalf("malformed Node metadata revision status=%d", response.Code)
		}
	}
	after, err := auth.GetService(t.Context(), before.ServiceID)
	if err != nil || store.ServiceMetadataRevision(after) != store.ServiceMetadataRevision(before) {
		t.Fatal("malformed metadata revision changed persisted configuration")
	}
}
