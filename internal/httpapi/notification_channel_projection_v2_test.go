package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestNotificationChannelProjectionV2Routes(t *testing.T) {
	canonical := map[string]any{"id": "channel-v2", "name": "email", "type": "email", "enabled": true, "uses_global_smtp": true, "masked_email_target": "o***s@example.com", "masked_webhook_url": "https://example.com/<WEBHOOK_PATH>", "severity_filter": []any{"critical"}, "event_type_filter": []any{"incident.created"}, "created_at": "2026-09-11T00:00:00Z", "updated_at": "2026-09-11T01:00:00Z"}
	var oldValue any
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		row := map[string]any{}
		for key, value := range canonical {
			row[key] = value
		}
		row["smtp_password_configured"] = oldValue
		row["smtp_password"] = "synthetic-hidden-password"
		row["webhook_url"] = "https://example.com/private-webhook"
		row["unrecognized_field"] = "must-not-pass"
		if r.Method == http.MethodGet && r.URL.Path == "/notification-channels" {
			_ = json.NewEncoder(w).Encode([]any{row})
		} else {
			_ = json.NewEncoder(w).Encode(row)
		}
	}))
	defer obs.Close()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"notification_channels.read", "notification_channels.create", "notification_channels.update"}); err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeForTest(t, auth, "synthetic-observability-token", obs.URL)
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, server, "admin", "correct horse battery")
	for _, tc := range []struct {
		name  string
		value any
	}{{"true", true}, {"false", false}, {"null", nil}, {"string", "configured"}} {
		t.Run(tc.name, func(t *testing.T) {
			oldValue = tc.value
			for _, route := range []struct {
				method, path string
				status       int
			}{{http.MethodGet, "/observability/notification-channels", 200}, {http.MethodGet, "/observability/notification-channels/channel-v2", 200}, {http.MethodPost, "/observability/notification-channels", 201}, {http.MethodPut, "/observability/notification-channels/channel-v2", 200}} {
				req := httptest.NewRequest(route.method, route.path, bytes.NewBufferString(`{"name":"email","type":"email","uses_global_smtp":true,"enabled":true}`))
				req.AddCookie(cookie)
				req.Header.Set("X-CSRF-Token", csrf)
				res := httptest.NewRecorder()
				server.ServeHTTP(res, req)
				if res.Code != route.status {
					t.Fatalf("%s %s status=%d", route.method, route.path, res.Code)
				}
				var got map[string]any
				if route.method == http.MethodGet && route.path == "/observability/notification-channels" {
					var rows []map[string]any
					if err := json.Unmarshal(res.Body.Bytes(), &rows); err != nil {
						t.Fatal(err)
					}
					if len(rows) != 1 {
						t.Fatal("missing channel")
					}
					got = rows[0]
				} else if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, canonical) {
					t.Fatal("projection leaked a legacy/secret field or lost canonical fields")
				}
			}
		})
	}
}

func TestNotificationChannelProjectionV2KeepsGlobalSMTPStatus(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "synthetic-hidden-password"); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAppSettingsStore(store.NewMemoryAppSettingsStore()), WithSecretStore(secrets))
	cookie, _ := loginForTest(t, server, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/settings/app/manage", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusOK || got["smtp_password_configured"] != true {
		t.Fatal("global SMTP configured status lost")
	}
	if _, exists := got["smtp_password"]; exists || bytes.Contains(res.Body.Bytes(), []byte("synthetic-hidden-password")) {
		t.Fatal("global SMTP secret disclosed")
	}
}
