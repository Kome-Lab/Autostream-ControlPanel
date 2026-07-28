package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestUpdateNodeRejectsEndpointChangeForActivePullSystemdTarget(t *testing.T) {
	auth, _, _, handler, cookie, csrf := newActivePullManagedNodeEndpointFixture(t)
	before, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}

	response := putNodeForEndpointTest(
		t,
		handler,
		cookie,
		csrf,
		"worker-a",
		map[string]any{"port": 19090},
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"node_endpoint_managed_by_updater"`) {
		t.Fatalf("managed endpoint update = %d %s", response.Code, response.Body.String())
	}

	after, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if after.Host != before.Host ||
		after.Port != before.Port ||
		after.SSLEnabled != before.SSLEnabled ||
		after.PublicURL != before.PublicURL ||
		after.EndpointRevision != before.EndpointRevision {
		t.Fatalf("rejected endpoint update mutated service: before=%#v after=%#v", before, after)
	}
}

func TestUpdateNodeAllowsMetadataAndExactEndpointNoOpWithoutAdvancingRevision(t *testing.T) {
	auth, _, _, handler, cookie, csrf := newActivePullManagedNodeEndpointFixture(t)
	seed, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	before, err := auth.UpdateServiceMetadata(t.Context(), "worker-a", store.ServiceMetadataUpdate{
		ServiceName: seed.ServiceName,
		Description: seed.Description,
		Host:        seed.Host,
		Port:        seed.Port,
		SSLEnabled:  seed.SSLEnabled,
		PublicURL:   seed.PublicURL + "/api/",
	})
	if err != nil {
		t.Fatal(err)
	}

	metadata := putNodeForEndpointTest(
		t,
		handler,
		cookie,
		csrf,
		"worker-a",
		map[string]any{
			"name":        "Worker Renamed",
			"description": "metadata only",
		},
	)
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata-only update = %d %s", metadata.Code, metadata.Body.String())
	}
	afterMetadata, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if afterMetadata.ServiceName != "Worker Renamed" ||
		afterMetadata.Description != "metadata only" {
		t.Fatalf("metadata update was not persisted: %#v", afterMetadata)
	}
	assertEndpointStateUnchangedForTest(t, before, afterMetadata)

	noOp := putNodeForEndpointTest(
		t,
		handler,
		cookie,
		csrf,
		"worker-a",
		map[string]any{
			"host":        afterMetadata.Host,
			"port":        afterMetadata.Port,
			"ssl_enabled": afterMetadata.SSLEnabled,
			"public_url":  afterMetadata.PublicURL,
		},
	)
	if noOp.Code != http.StatusOK {
		t.Fatalf("exact endpoint no-op = %d %s", noOp.Code, noOp.Body.String())
	}
	afterNoOp, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	assertEndpointStateUnchangedForTest(t, afterMetadata, afterNoOp)
}

func TestUpdateNodeAllowsEndpointChangeBeforePullOwnershipActivation(t *testing.T) {
	auth, _, _, handler, _, _, _ := newPullActivationHTTPFixture(t, true)
	cookie, csrf := addNodeEndpointAdminForTest(t, auth, handler)

	response := putNodeForEndpointTest(
		t,
		handler,
		cookie,
		csrf,
		"worker-a",
		map[string]any{"port": 19090},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("observe-only endpoint update = %d %s", response.Code, response.Body.String())
	}
	updated, err := auth.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Port != 19090 {
		t.Fatalf("observe-only endpoint port = %d, want 19090", updated.Port)
	}
}

func TestUpdateNodeFailsClosedWhenPullOwnershipCannotBeVerified(t *testing.T) {
	auth, policies, updates, _, _, _ := newActivePullManagedNodeEndpointFixture(t)
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(failingNodeEndpointOwnershipStore{
			SystemUpdateStore: updates,
			err:               errors.New("ownership unavailable"),
		}),
	)
	cookie, csrf := loginForTest(t, handler, "node-endpoint-admin", "correct horse battery")

	response := putNodeForEndpointTest(
		t,
		handler,
		cookie,
		csrf,
		"worker-a",
		map[string]any{"port": 19090},
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"node_endpoint_managed_by_updater"`) {
		t.Fatalf("unverified ownership update = %d %s", response.Code, response.Body.String())
	}
}

type failingNodeEndpointOwnershipStore struct {
	store.SystemUpdateStore
	err error
}

func (s failingNodeEndpointOwnershipStore) GetSystemUpdateExecutionHost(
	context.Context,
	string,
) (store.SystemUpdateExecutionHost, error) {
	return store.SystemUpdateExecutionHost{}, s.err
}

func (s failingNodeEndpointOwnershipStore) SwitchSystemUpdateExecutionHost(
	context.Context,
	string,
	int64,
	string,
	string,
	int64,
) (store.SystemUpdateExecutionHost, error) {
	return store.SystemUpdateExecutionHost{}, s.err
}

func newActivePullManagedNodeEndpointFixture(
	t *testing.T,
) (
	*store.MemoryAuthStore,
	*store.MemoryUpdaterPolicyStore,
	*store.MemorySystemUpdateStore,
	http.Handler,
	*http.Cookie,
	string,
) {
	t.Helper()
	auth, policies, updates, handler, _, _, saved := newPullActivationHTTPFixture(t, true)
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), saved.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policies.ActivatePullUpdaterOwnership(
		t.Context(),
		auth,
		updates,
		store.ActivatePullUpdaterOwnershipParams{
			ServiceID:                           saved.UpdaterID,
			ExecutionHostID:                     saved.ExecutionHostID,
			ExpectedExecutionHostOwnershipEpoch: ownership.OwnershipEpoch,
			ExpectedSourcePolicyRevision:        saved.Revision,
			ExpectedProjectionRevision:          saved.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   saved.LocalExecutorPolicySHA256,
		},
	); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := addNodeEndpointAdminForTest(t, auth, handler)
	return auth, policies, updates, handler, cookie, csrf
}

func addNodeEndpointAdminForTest(
	t *testing.T,
	auth *store.MemoryAuthStore,
	handler http.Handler,
) (*http.Cookie, string) {
	t.Helper()
	if err := auth.AddUser(
		store.User{
			ID:       "node-endpoint-admin",
			Username: "node-endpoint-admin",
		},
		"correct horse battery",
		[]string{"api_tokens.create"},
	); err != nil {
		t.Fatal(err)
	}
	return loginForTest(t, handler, "node-endpoint-admin", "correct horse battery")
}

func putNodeForEndpointTest(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	csrf string,
	serviceID string,
	body map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPut,
		"/nodes/"+serviceID,
		bytes.NewReader(encoded),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertEndpointStateUnchangedForTest(
	t *testing.T,
	before store.RegisteredService,
	after store.RegisteredService,
) {
	t.Helper()
	if after.Host != before.Host ||
		after.Port != before.Port ||
		after.SSLEnabled != before.SSLEnabled ||
		after.PublicURL != before.PublicURL ||
		after.EndpointRevision != before.EndpointRevision ||
		after.EndpointStatus != before.EndpointStatus {
		t.Fatalf("endpoint state changed: before=%#v after=%#v", before, after)
	}
}
