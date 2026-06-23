package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryAuditStoreRedactsMetadataOnWriteAndList(t *testing.T) {
	st := NewMemoryAuthStore()
	if err := st.WriteAudit(t.Context(), AuditEvent{
		Action:       "notification_channels.create",
		ResourceType: "notification_channel",
		Result:       "success",
		Metadata: map[string]any{
			"webhook_url": "https://discord.com/api/webhooks/123456789012345678/raw-secret-token",
			"nested": map[string]any{
				"endpoint":               "rtsp://user:password@camera.example.com/live",
				"value":                  "super-raw-discord-token",
				"google_drive_folder_id": "drive-folder-secret-id",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	for _, events := range [][]AuditEvent{st.AuditEvents(), mustListAudit(t, st)} {
		body, err := json.Marshal(events)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, raw := range []string{"raw-secret-token", "password@camera", "super-raw-discord-token", "drive-folder-secret-id", "discord.com/api/webhooks"} {
			if strings.Contains(text, raw) {
				t.Fatalf("audit metadata leaked raw secret %q in %s", raw, text)
			}
		}
		if !strings.Contains(text, "redacted") {
			t.Fatalf("expected redacted audit metadata, got %s", text)
		}
	}
}

func TestMemoryAuditStoreRedactsSecretLikeFreeTextFields(t *testing.T) {
	st := NewMemoryAuthStore()
	if err := st.WriteAudit(t.Context(), AuditEvent{
		ActorUsername: "rtsp://user:password@camera.example.com/live",
		UserAgent:     "Bearer ast_svc_this_is_a_long_service_token",
		Action:        "auth.login",
		ResourceType:  "user",
		ResourceID:    "https://discord.com/api/webhooks/id/raw-secret-token",
		Result:        "failure",
	}); err != nil {
		t.Fatal(err)
	}

	events := st.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("unexpected audit events: %#v", events)
	}
	body, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"password@camera", "ast_svc_this_is_a_long_service_token", "discord.com/api/webhooks", "raw-secret-token"} {
		if strings.Contains(string(body), raw) {
			t.Fatalf("audit free-text field leaked raw secret %q in %s", raw, body)
		}
	}
	if events[0].ActorUsername != "<redacted>" || events[0].UserAgent != "<redacted>" || events[0].ResourceID != "<redacted>" {
		t.Fatalf("expected free-text audit fields to be redacted, got %#v", events[0])
	}
}

func TestMemoryAuditStoreRedactsQuerySecretValues(t *testing.T) {
	st := NewMemoryAuthStore()
	callbackURL := "https://control.example.com/callback?api_key=raw-query-secret&state=public"
	if err := st.WriteAudit(t.Context(), AuditEvent{
		Action:       "auth.login",
		ResourceType: "user",
		ResourceID:   callbackURL,
		Result:       "failure",
		Metadata: map[string]any{
			"input": callbackURL,
		},
	}); err != nil {
		t.Fatal(err)
	}

	events := st.AuditEvents()
	body, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "raw-query-secret") || strings.Contains(string(body), "api_key=") {
		t.Fatalf("audit event leaked query secret in %s", body)
	}
	if events[0].ResourceID != "<redacted>" {
		t.Fatalf("expected resource id to be redacted, got %#v", events[0].ResourceID)
	}
}

func TestMemoryAuditStoreRedactsIntegrationSecretsInFreeText(t *testing.T) {
	st := NewMemoryAuthStore()
	if err := st.WriteAudit(t.Context(), AuditEvent{
		Action:       "integrations.runtime_secret.resolve",
		ResourceType: "integration",
		ResourceID:   "drive-destination-01",
		Result:       "failure",
		Metadata: map[string]any{
			"message": "youtube stream_key=raw-youtube-stream-key refresh_token=raw-google-refresh-token smtp_password=raw-smtp-password folder_id=raw-shared-drive-folder-id",
			"nested": []any{
				map[string]any{"detail": "client_secret=raw-google-client-secret"},
				"access_token=raw-google-access-token",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	events := st.AuditEvents()
	body, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, raw := range []string{"raw-youtube-stream-key", "raw-google-refresh-token", "raw-smtp-password", "raw-shared-drive-folder-id", "raw-google-client-secret", "raw-google-access-token", "stream_key=", "refresh_token=", "smtp_password=", "folder_id=", "client_secret=", "access_token="} {
		if strings.Contains(text, raw) {
			t.Fatalf("audit integration free text leaked raw secret %q in %s", raw, text)
		}
	}
	metadata := events[0].Metadata
	if metadata["message"] != "<redacted>" {
		t.Fatalf("expected message to be redacted, got %#v", metadata["message"])
	}
	nested, ok := metadata["nested"].([]any)
	if !ok || len(nested) != 2 || nested[1] != "<redacted>" {
		t.Fatalf("expected nested free-text secret to be redacted, got %#v", metadata["nested"])
	}
}

func mustListAudit(t *testing.T, st *MemoryAuthStore) []AuditEvent {
	t.Helper()
	events, err := st.ListAudit(t.Context(), AuditFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return events
}
