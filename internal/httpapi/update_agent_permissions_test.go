package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestUpdateAgentTokenMutationsRejectMissingSystemUpdatePermission(t *testing.T) {
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "limited"}, "correct horse battery", []string{"api_tokens.create", "api_tokens.revoke"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{ServiceID: "updater-limited", ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	originalConfigureHash := security.HashToken("original-configure-token")
	if _, err := auth.SetServiceConfigureToken(t.Context(), service.ServiceID, originalConfigureHash, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "limited", "correct horse battery")
	for _, path := range []string{"/nodes/updater-limited/configure-token", "/nodes/updater-limited/rotate-token"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "permission_escalation") {
			t.Fatalf("%s without system_updates.execute status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	got, err := auth.GetService(t.Context(), service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != token.ID || got.ConfigureTokenHash != originalConfigureHash || got.ConfigureTokenUsedAt != nil {
		t.Fatalf("denied updater token mutation changed credentials: %#v", got)
	}
}
