package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestNormalizeCaptionProfileConfigUsesDeepgramRuntimeDefaults(t *testing.T) {
	config := normalizeProfileConfig(store.ProfileCaption, map[string]any{
		"provider":          "manual",
		"model":             "legacy",
		"language":          "ja-JP",
		"endpointing_ms":    0,
		"delay_ms":          1200,
		"caption_audio_url": "https://caption.example.com/audio",
	})

	if config["provider"] != "deepgram" || config["model"] != "nova-3" {
		t.Fatalf("Deepgram runtime was not enforced: %#v", config)
	}
	if config["language"] != "ja" || config["api_key_secret_name"] != "deepgram_api_key" {
		t.Fatalf("Deepgram language or secret reference was not normalized: %#v", config)
	}
	if config["endpointing_ms"] != 300 || config["delay_ms"] != 1200 {
		t.Fatalf("Deepgram timing defaults were not normalized: %#v", config)
	}
	if config["interim_results"] != true || config["smart_format"] != true {
		t.Fatalf("Deepgram result defaults were not enabled: %#v", config)
	}
	if _, ok := config["caption_audio_url"]; ok {
		t.Fatalf("legacy arbitrary caption URL was not removed: %#v", config)
	}
	runtime := sanitizeRuntimeProfileConfigForKind(store.ProfileCaption, map[string]any{
		"provider": "operator", "language": "en-US", "caption_audio_url": "https://caption.example.com/audio",
	})
	if runtime["provider"] != "deepgram" || runtime["language"] != "en" || runtime["api_key_secret_name"] != "deepgram_api_key" {
		t.Fatalf("legacy caption runtime config was not upgraded safely: %#v", runtime)
	}
	if _, ok := runtime["caption_audio_url"]; ok {
		t.Fatalf("legacy caption URL reached runtime config: %#v", runtime)
	}
}

func TestProfileCRUDAPI(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	permissions := []string{
		"encoder_profiles.read", "encoder_profiles.create", "encoder_profiles.update", "encoder_profiles.delete",
		"discord_configs.read", "discord_configs.create",
	}
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/profiles/encoder", bytes.NewBufferString(`{"name":"1080p60","config":{"width":1920,"height":1080,"fps":60,"video_bitrate_kbps":8000}}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create profile status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	var created store.Profile
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Kind != store.ProfileEncoder || created.Name != "1080p60" {
		t.Fatalf("unexpected profile: %#v", created)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/profiles/encoder/"+created.ID, bytes.NewBufferString(`{"name":"1080p60-high","config":{"width":1920,"height":1080,"fps":60,"video_bitrate_kbps":9000}}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK || !strings.Contains(updateRes.Body.String(), "1080p60-high") {
		t.Fatalf("update profile status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/profiles/encoder", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), "1080p60-high") {
		t.Fatalf("list profile status = %d body = %s", listRes.Code, listRes.Body.String())
	}

	discordReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"main-guild","service_id":"discord-01","audio_forward_enabled":true}`))
	discordReq.AddCookie(cookie)
	discordReq.Header.Set("X-CSRF-Token", csrf)
	discordRes := httptest.NewRecorder()
	handler.ServeHTTP(discordRes, discordReq)
	if discordRes.Code != http.StatusCreated {
		t.Fatalf("create discord config status = %d body = %s", discordRes.Code, discordRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/profiles/encoder/"+created.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete profile status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}

	events := auth.AuditEvents()
	if !strings.Contains(toJSONForTest(t, events), "encoder_profiles.create") || !strings.Contains(toJSONForTest(t, events), "encoder_profiles.delete") {
		t.Fatalf("profile audit events missing: %#v", events)
	}
}

func TestUpdateCaptionProfileAppliesImmediatelyToReferencingLiveStream(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"caption_profiles.update"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstanceWithCapabilities(t, auth, "worker-caption-01", "worker", map[string]any{"live_caption_runtime_settings": true})
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileCaption, "Live captions", map[string]any{
		"provider": "deepgram", "model": "nova-3", "language": "ja", "api_key_secret_name": "deepgram_api_key",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "Live stream")
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{CaptionProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamStatus(t.Context(), stream.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-caption-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &captionRuntimeServiceDispatcher{}
	handler := NewServer(streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/profiles/caption/"+profile.ID, bytes.NewBufferString(`{"name":"Live captions updated","config":{"provider":"deepgram","model":"nova-3","language":"en","api_key_secret_name":"deepgram_api_key","endpointing_ms":450}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("caption profile update status = %d body = %s", res.Code, res.Body.String())
	}
	if dispatcher.captionRuntimeCalls != 1 || dispatcher.captionRuntimeStream.ID != stream.ID || dispatcher.captionRuntimeProfileID != profile.ID {
		t.Fatalf("live caption runtime update was not dispatched: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	if len(dispatcher.captionRuntimeServices) != 1 || dispatcher.captionRuntimeServices[0].ServiceID != "worker-caption-01" {
		t.Fatalf("caption runtime dispatch did not use the primary Worker: %#v", dispatcher.captionRuntimeServices)
	}
	updated, err := profiles.GetProfile(t.Context(), store.ProfileCaption, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Config["language"] != "en" {
		t.Fatalf("caption profile was not persisted: %#v", updated.Config)
	}
	if !strings.Contains(toJSONForTest(t, auth.AuditEvents()), `"applied_live":true`) {
		t.Fatalf("live caption update audit evidence missing: %#v", auth.AuditEvents())
	}
}

func TestUpdateCaptionProfileReportsSavedButLiveApplyFailed(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"caption_profiles.update"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstanceWithCapabilities(t, auth, "worker-caption-01", "worker", map[string]any{"live_caption_runtime_settings": true})
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileCaption, "Live captions", map[string]any{
		"provider": "deepgram", "model": "nova-3", "language": "ja", "api_key_secret_name": "deepgram_api_key",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "Live stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{CaptionProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-caption-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	dispatcher := &captionRuntimeServiceDispatcher{captionRuntimeResult: servicecall.DispatchResult{ServiceID: "worker-caption-01", ServiceType: "worker", Code: "caption_runtime_unavailable", FailurePhase: "response", Success: false}}
	handler := NewServer(streams,
		WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPut, "/profiles/caption/"+profile.ID, bytes.NewBufferString(`{"name":"Saved profile","config":{"provider":"deepgram","model":"nova-3","language":"en","api_key_secret_name":"deepgram_api_key"}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "caption_profile_saved_runtime_apply_failed") {
		t.Fatalf("caption live apply failure status = %d body = %s", res.Code, res.Body.String())
	}
	updated, err := profiles.GetProfile(t.Context(), store.ProfileCaption, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Saved profile" || updated.Config["language"] != "en" {
		t.Fatalf("profile save was rolled back or lost after live apply failure: %#v", updated)
	}
}

func TestDeleteProfileRejectsStreamReferences(t *testing.T) {
	tests := []struct {
		name       string
		kind       store.ProfileKind
		path       string
		permission string
		settings   func(string) store.StreamSettings
		config     map[string]any
	}{
		{
			name:       "encoder",
			kind:       store.ProfileEncoder,
			path:       "/profiles/encoder/",
			permission: "encoder_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{EncoderProfileID: id} },
			config:     map[string]any{"width": 1920, "height": 1080, "fps": 60},
		},
		{
			name:       "archive",
			kind:       store.ProfileArchive,
			path:       "/profiles/archive/",
			permission: "archive_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{ArchiveProfileID: id} },
			config:     map[string]any{"format": "mp4", "upload_enabled": true},
		},
		{
			name:       "caption",
			kind:       store.ProfileCaption,
			path:       "/profiles/caption/",
			permission: "caption_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{CaptionProfileID: id} },
			config:     map[string]any{"language": "ja-JP", "provider": "manual"},
		},
		{
			name:       "overlay",
			kind:       store.ProfileOverlay,
			path:       "/profiles/overlay/",
			permission: "overlay_profiles.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{OverlayProfileID: id} },
			config:     map[string]any{"theme": "public", "watermark_enabled": true},
		},
		{
			name:       "discord config",
			kind:       store.ProfileDiscordConfig,
			path:       "/discord/configs/",
			permission: "discord_configs.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{DiscordConfigID: id} },
			config:     map[string]any{"service_id": "discord-01", "audio_forward_enabled": true},
		},
		{
			name:       "youtube output",
			kind:       store.ProfileYouTubeOutput,
			path:       "/youtube/outputs/",
			permission: "youtube_outputs.delete",
			settings:   func(id string) store.StreamSettings { return store.StreamSettings{YouTubeOutputID: id} },
			config:     map[string]any{"mode": "stream_key", "rtmp_url": "rtmps://a.rtmps.youtube.com/live2"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{tc.permission}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			profiles := store.NewMemoryProfileStore()
			profile, err := profiles.CreateProfile(t.Context(), tc.kind, tc.name, tc.config)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := streams.CreateStream(t.Context(), "referencing stream")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, tc.settings(profile.ID)); err != nil {
				t.Fatal(err)
			}

			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(profiles))
			cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
			req := httptest.NewRequest(http.MethodDelete, tc.path+profile.ID, nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "profile_in_use") || !strings.Contains(res.Body.String(), stream.ID) {
				t.Fatalf("delete referenced profile status = %d body = %s", res.Code, res.Body.String())
			}
			if _, err := profiles.GetProfile(t.Context(), tc.kind, profile.ID); err != nil {
				t.Fatalf("referenced profile was deleted: %v", err)
			}
		})
	}
}

func TestProfileAPIRejectsRawSecretConfigWithAllowedReferences(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"archive_profiles.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/profiles/archive", bytes.NewBufferString(`{"name":"bad archive","config":{"refresh_token":"raw-refresh-token","upload_enabled":true}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	body := res.Body.String()
	if res.Code != http.StatusBadRequest || !strings.Contains(body, "profile_secret_reference_required") {
		t.Fatalf("expected raw secret config rejection, status=%d body=%s", res.Code, body)
	}
	var payload struct {
		Allowed []string `json:"allowed_secret_references"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode profile secret rejection response: %v body=%s", err, body)
	}
	for _, expected := range []string{"drive_destination:<id>:folder_id", "oauth_account:<id>:refresh_token"} {
		if !stringSliceContains(payload.Allowed, expected) {
			t.Fatalf("expected allowed reference hint %q in body: %s", expected, body)
		}
	}
	if strings.Contains(body, "raw-refresh-token") {
		t.Fatalf("raw secret value leaked in validation response: %s", body)
	}
}

func TestProfileAPIRejectsDisallowedSecretReferenceForKind(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"encoder_profiles.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/profiles/encoder", bytes.NewBufferString(`{"name":"bad encoder","config":{"stream_key_secret_name":"youtube_stream_key","width":1920}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	body := res.Body.String()
	if res.Code != http.StatusBadRequest || !strings.Contains(body, "profile_secret_reference_not_allowed") {
		t.Fatalf("expected disallowed secret reference rejection, status=%d body=%s", res.Code, body)
	}
	var payload struct {
		Invalid []string `json:"invalid_secret_references"`
		Allowed []string `json:"allowed_secret_references"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode disallowed secret reference response: %v body=%s", err, body)
	}
	if !stringSliceContains(payload.Invalid, "youtube_stream_key") {
		t.Fatalf("expected invalid secret reference in body: %s", body)
	}
	if !stringSliceContains(payload.Allowed, "encoder_runtime_secret_<name>") {
		t.Fatalf("expected allowed encoder secret reference in body: %s", body)
	}
}

func TestProfileCRUDRejectsRawSecretConfig(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	permissions := []string{"youtube_outputs.create", "discord_configs.create"}
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"admin"}}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "discord-01", "discord_bot")
	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "Google YouTube",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/integrations/oauth-accounts/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "YouTube Account",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "raw-youtube-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(store.NewMemoryProfileStore()), WithIntegrationStore(integrations))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	tests := []struct {
		name string
		body string
	}{
		{name: "raw stream key field", body: `{"name":"bad-youtube","config":{"rtmp_url":"rtmps://example.youtube.com/live2","stream_key":"super-secret-stream-key"}}`},
		{name: "camelCase stream key field", body: `{"name":"bad-youtube","config":{"rtmp_url":"rtmps://example.youtube.com/live2","streamKey":"super-secret-stream-key"}}`},
		{name: "mixed separator access token field", body: `{"name":"bad-youtube","config":{"rtmp_url":"rtmps://example.youtube.com/live2","access-Token":"super-secret-stream-key"}}`},
		{name: "raw google drive folder id field", body: `{"name":"bad-archive","config":{"google_drive_folder_id":"drive-folder-secret-id"}}`},
		{name: "raw camelCase drive folder id field", body: `{"name":"bad-archive-camel","config":{"googleDriveFolderId":"drive-folder-secret-id"}}`},
		{name: "credentialed URL value", body: `{"name":"bad-url","config":{"source_url":"rtsp://user:password@camera.example.com/live"}}`},
		{name: "raw webhook URL value", body: `{"name":"bad-webhook","config":{"notification":"https://discord.com/api/webhooks/id/raw-secret-token"}}`},
		{name: "plaintext rtmp youtube output", body: `{"name":"bad-rtmp","mode":"stream_key","rtmp_url":"rtmp://a.rtmp.youtube.com/live2","stream_key":"super-secret-stream-key"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(tc.body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
			}
			if strings.Contains(res.Body.String(), "super-secret") || strings.Contains(res.Body.String(), "drive-folder-secret-id") || strings.Contains(res.Body.String(), "raw-secret-token") || strings.Contains(res.Body.String(), "password@") {
				t.Fatalf("raw secret leaked in validation response: %s", res.Body.String())
			}
		})
	}

	allowedReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"good-youtube","mode":"stream_key","rtmp_url":"rtmps://example.youtube.com/live2","stream_key":"super-secret-stream-key"}`))
	allowedReq.AddCookie(cookie)
	allowedReq.Header.Set("X-CSRF-Token", csrf)
	allowedRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusCreated {
		t.Fatalf("allowed structured stream key status = %d body = %s", allowedRes.Code, allowedRes.Body.String())
	}
	if strings.Contains(allowedRes.Body.String(), "super-secret-stream-key") || strings.Contains(allowedRes.Body.String(), `"stream_key":"`) {
		t.Fatalf("raw stream key leaked in youtube output response: %s", allowedRes.Body.String())
	}

	allowedCamelReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"good-youtube-live-dry","mode":"live_api_dry_run","rtmp_url":"rtmps://example.youtube.com/live2","oauth_account_id":"`+account.ID+`","privacy_status":"private","latency_preference":"low","enable_auto_start":true,"enable_auto_stop":true}`))
	allowedCamelReq.AddCookie(cookie)
	allowedCamelReq.Header.Set("X-CSRF-Token", csrf)
	allowedCamelRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedCamelRes, allowedCamelReq)
	if allowedCamelRes.Code != http.StatusCreated {
		t.Fatalf("allowed live api dry-run status = %d body = %s", allowedCamelRes.Code, allowedCamelRes.Body.String())
	}

	invalidStaticOAuthReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"invalid-static-oauth","mode":"live_api_relay_static","oauth_account_id":"036b8f3e-c853-4b9d-9f74-bdbbd6c0c960","relay_binding_id":"relay-00000000-0000-4000-8000-00000000000d","reusable_live_stream_id":"youtube-live-stream-profile"}`))
	invalidStaticOAuthReq.AddCookie(cookie)
	invalidStaticOAuthReq.Header.Set("X-CSRF-Token", csrf)
	invalidStaticOAuthRes := httptest.NewRecorder()
	handler.ServeHTTP(invalidStaticOAuthRes, invalidStaticOAuthReq)
	if invalidStaticOAuthRes.Code != http.StatusNotFound || !strings.Contains(invalidStaticOAuthRes.Body.String(), "oauth_account_not_found") {
		t.Fatalf("invalid static OAuth status = %d body = %s", invalidStaticOAuthRes.Code, invalidStaticOAuthRes.Body.String())
	}

	allowedStaticReq := httptest.NewRequest(http.MethodPost, "/youtube/outputs", bytes.NewBufferString(`{"name":"good-youtube-static","mode":"live_api_relay_static","oauth_account_id":"`+account.ID+`","relay_binding_id":"relay-00000000-0000-4000-8000-00000000000d","reusable_live_stream_id":"youtube-live-stream-profile","complete_on_stop":false}`))
	allowedStaticReq.AddCookie(cookie)
	allowedStaticReq.Header.Set("X-CSRF-Token", csrf)
	allowedStaticRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedStaticRes, allowedStaticReq)
	if allowedStaticRes.Code != http.StatusCreated {
		t.Fatalf("allowed static relay status = %d body = %s", allowedStaticRes.Code, allowedStaticRes.Body.String())
	}
	var allowedStatic youtubeOutputResponse
	if err := json.NewDecoder(allowedStaticRes.Body).Decode(&allowedStatic); err != nil {
		t.Fatal(err)
	}
	if allowedStatic.Mode != "live_api_relay_static" || allowedStatic.OAuthAccountID != account.ID || allowedStatic.RelayBindingID != "relay-00000000-0000-4000-8000-00000000000d" || allowedStatic.ReusableLiveStreamID != "youtube-live-stream-profile" || !allowedStatic.CompleteOnStop || allowedStatic.RTMPURL != "" || allowedStatic.StreamKeyConfigured {
		t.Fatalf("unexpected static relay output response: %#v", allowedStatic)
	}

	discordLegacyReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"bad-discord","config":{"guild_id":"guild-01","bot_token":"raw-discord-bot-token"}}`))
	discordLegacyReq.AddCookie(cookie)
	discordLegacyReq.Header.Set("X-CSRF-Token", csrf)
	discordLegacyRes := httptest.NewRecorder()
	handler.ServeHTTP(discordLegacyRes, discordLegacyReq)
	if discordLegacyRes.Code != http.StatusBadRequest {
		t.Fatalf("legacy discord config with raw secret status = %d body = %s", discordLegacyRes.Code, discordLegacyRes.Body.String())
	}
	if strings.Contains(discordLegacyRes.Body.String(), "raw-discord-bot-token") {
		t.Fatalf("raw discord token leaked in validation response: %s", discordLegacyRes.Body.String())
	}

	discordAllowedReq := httptest.NewRequest(http.MethodPost, "/discord/configs", bytes.NewBufferString(`{"name":"good-discord","service_id":"discord-01","guild_id":"guild-01","voice_channel_id":"voice-01","text_channel_id":"text-01","bot_token":"raw-discord-bot-token","audio_forward_enabled":true}`))
	discordAllowedReq.AddCookie(cookie)
	discordAllowedReq.Header.Set("X-CSRF-Token", csrf)
	discordAllowedRes := httptest.NewRecorder()
	handler.ServeHTTP(discordAllowedRes, discordAllowedReq)
	if discordAllowedRes.Code != http.StatusCreated {
		t.Fatalf("allowed discord config status = %d body = %s", discordAllowedRes.Code, discordAllowedRes.Body.String())
	}
	if strings.Contains(discordAllowedRes.Body.String(), "raw-discord-bot-token") || strings.Contains(discordAllowedRes.Body.String(), `"bot_token":"`) {
		t.Fatalf("raw discord token leaked in discord config response: %s", discordAllowedRes.Body.String())
	}
	if strings.Contains(discordAllowedRes.Body.String(), "guild-01") || strings.Contains(discordAllowedRes.Body.String(), "voice-01") || strings.Contains(discordAllowedRes.Body.String(), "text-01") {
		t.Fatalf("discord config response should not persist stream channel IDs: %s", discordAllowedRes.Body.String())
	}
}

func TestProfileCRUDRequiresSpecificPermission(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"encoder_profiles.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithProfileStore(store.NewMemoryProfileStore()))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/profiles/encoder", bytes.NewBufferString(`{"name":"blocked","config":{}}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}
