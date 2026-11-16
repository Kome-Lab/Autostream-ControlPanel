package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func operationalWorkloadFixture(t *testing.T) (*store.MemoryAuthStore, *Server, *http.Cookie, string) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	const userID = "human-workload-s13"
	if err := auth.AddUser(store.User{ID: userID, Username: userID, Status: "active", Roles: []string{"super_admin"}}, "synthetic S13 workload password", security.DefaultPermissions); err != nil {
		t.Fatal(err)
	}
	session, err := auth.CreateSession(context.Background(), userID, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithOperationalAuthorization(auth))
	return auth, server, &http.Cookie{Name: sessionCookieName, Value: session.Token}, session.CSRFToken
}

func operationalWorkloadRequest(t *testing.T, server http.Handler, cookie *http.Cookie, csrf, target, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	return res
}

func operationalWorkloadStatus(t *testing.T, res *httptest.ResponseRecorder, want int, code string) {
	t.Helper()
	if res.Code != want {
		t.Fatalf("status=%d want=%d", res.Code, want)
	}
	if code != "" {
		var payload struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
			t.Fatal("expected JSON denial")
		}
		if payload.Code != code {
			t.Fatalf("denial code=%q want=%q", payload.Code, code)
		}
	}
}

func TestOperationalWorkloadHumanSessionSeparation(t *testing.T) {
	_, server, cookie, csrf := operationalWorkloadFixture(t)
	cases := []struct {
		name, path, code string
		status           int
	}{
		{"mutation_issue", "/services/update-jobs/job-s13/mutation-grants", "missing_service_token", http.StatusUnauthorized},
		{"mutation_consume", "/services/update-jobs/job-s13/mutation-grants/consume", "system_update_mutation_grant_required", http.StatusUnauthorized},
		{"host_self_update_issue", "/services/host-agent/self-updates/update-s13/grants", "missing_service_token", http.StatusUnauthorized},
		{"host_self_update_consume", "/services/host-agent/self-update-grants/consume", "invalid_host_self_update_request", http.StatusBadRequest},
		{"bootstrap_claim", "/services/update-agent/bootstrap-jobs/claim", "missing_service_token", http.StatusUnauthorized},
		{"node_registration", "/services/register", "missing_service_token", http.StatusUnauthorized},
		{"node_heartbeat", "/api/node-agent/heartbeat", "missing_service_token", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := operationalWorkloadRequest(t, server, cookie, csrf, tc.path, `{}`, "")
			operationalWorkloadStatus(t, res, tc.status, tc.code)
		})
	}
}

func TestOperationalWorkloadServiceScopeAndRevocation(t *testing.T) {
	cases := []struct{ name, path, scope string }{
		{"mutation_issue", "/services/update-jobs/job-s13/mutation-grants", "updates.authorize"},
		{"host_self_update_issue", "/services/host-agent/self-updates/update-s13/grants", "service.config.read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, server, cookie, csrf := operationalWorkloadFixture(t)
			ctx := context.Background()
			wrong, err := auth.CreateServiceToken(ctx, "worker", []string{"service.heartbeat"})
			if err != nil {
				t.Fatal(err)
			}
			res := operationalWorkloadRequest(t, server, cookie, csrf, tc.path, `{}`, wrong.RawToken)
			operationalWorkloadStatus(t, res, http.StatusForbidden, "missing_service_scope")
			qualified, err := auth.CreateServiceToken(ctx, "worker", []string{tc.scope})
			if err != nil {
				t.Fatal(err)
			}
			res = operationalWorkloadRequest(t, server, cookie, csrf, tc.path, `{}`, qualified.RawToken)
			if tc.name == "host_self_update_issue" {
				operationalWorkloadStatus(t, res, http.StatusNotFound, "host_self_update_not_found")
			} else {
				operationalWorkloadStatus(t, res, http.StatusBadRequest, "invalid_updater_v2_mutation_grant")
			}
			if err := auth.RevokeServiceToken(ctx, qualified.ID); err != nil {
				t.Fatal(err)
			}
			res = operationalWorkloadRequest(t, server, cookie, csrf, tc.path, `{}`, qualified.RawToken)
			operationalWorkloadStatus(t, res, http.StatusUnauthorized, "invalid_service_token")
		})
	}
}

func TestOperationalWorkloadServiceTokenCannotReplaceExecutionGrant(t *testing.T) {
	auth, server, cookie, csrf := operationalWorkloadFixture(t)
	token, err := auth.CreateServiceToken(context.Background(), "worker", []string{"updates.authorize", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	res := operationalWorkloadRequest(t, server, cookie, csrf, "/services/update-jobs/job-s13/mutation-grants/consume", `{}`, token.RawToken)
	operationalWorkloadStatus(t, res, http.StatusUnauthorized, "system_update_mutation_grant_required")
	body, err := json.Marshal(map[string]any{"token": token.RawToken, "binding": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	res = operationalWorkloadRequest(t, server, cookie, csrf, "/services/host-agent/self-update-grants/consume", string(body), token.RawToken)
	operationalWorkloadStatus(t, res, http.StatusBadRequest, "invalid_host_self_update_request")
}

func TestOperationalWorkloadTokenCannotReplaceHumanSession(t *testing.T) {
	auth, server, _, _ := operationalWorkloadFixture(t)
	token, err := auth.CreateServiceToken(context.Background(), "worker", []string{"service.heartbeat", "updates.authorize", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	operationalWorkloadStatus(t, res, http.StatusUnauthorized, "unauthorized")
}
