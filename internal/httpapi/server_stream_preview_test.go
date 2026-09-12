package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestStreamPreviewProxiesValidatedPlaylist(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "preview stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &previewFakeDispatcher{result: servicecall.PreviewAssetResult{StatusCode: http.StatusOK, Success: true, Body: []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:2.0,\nsegment-000001.ts\n")}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, _ := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/preview/index.m3u8", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview response status=%d headers=%#v body=%s", res.Code, res.Header(), res.Body.String())
	}
	if dispatcher.calls != 1 || dispatcher.name != "index.m3u8" || !strings.Contains(res.Body.String(), "segment-000001.ts") {
		t.Fatalf("preview dispatcher/body mismatch: %s body=%s", formatSafeHTTPSensitiveDiagnostic(dispatcher), res.Body.String())
	}
}

func TestStreamPreviewRejectsExternalPlaylistResources(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "unsafe preview stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &previewFakeDispatcher{result: servicecall.PreviewAssetResult{StatusCode: http.StatusOK, Success: true, Body: []byte("#EXTM3U\nhttps://attacker.example/segment.ts\n")}}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, _ := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodGet, "/streams/"+stream.ID+"/preview/index.m3u8", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "invalid_stream_preview_playlist") || strings.Contains(res.Body.String(), "attacker.example") {
		t.Fatalf("unsafe preview playlist response status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestStreamPreviewLinkIsSignedExpiresAndStopsWithStream(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.jp")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "external preview stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &previewFakeDispatcher{result: servicecall.PreviewAssetResult{StatusCode: http.StatusOK, Success: true, Body: []byte("#EXTM3U\n#EXTINF:2.0,\nsegment-000001.ts\n")}}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher), WithPreviewSigningKey("preview-signing-key"))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/preview-links", nil)
	req.Host = "attacker.example"
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview link status=%d body=%s", res.Code, res.Body.String())
	}
	var link struct {
		URL                string    `json:"url"`
		PlaybackURL        string    `json:"playback_url"`
		PlayerURL          string    `json:"player_url"`
		ExpiresAt          time.Time `json:"expires_at"`
		VideoOverlayBurnIn bool      `json:"video_overlay_burn_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&link); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link.URL)
	if err != nil || parsed.Host != "panel.example.jp" || !strings.HasSuffix(parsed.Path, "/index.m3u8") || link.ExpiresAt.Before(time.Now().UTC().Add(11*time.Hour)) || link.PlaybackURL != link.URL || !strings.Contains(link.PlayerURL, "/stream-preview/?token=") || len(link.PlayerURL) >= len(link.URL) {
		t.Fatalf("unexpected preview link: %#v err=%v", link, err)
	}
	if link.VideoOverlayBurnIn {
		t.Fatalf("legacy/unknown media runtime must preserve DOM overlay: %#v", link)
	}

	publicReq := httptest.NewRequest(http.MethodGet, parsed.Path, nil)
	publicRes := httptest.NewRecorder()
	handler.ServeHTTP(publicRes, publicReq)
	if publicRes.Code != http.StatusOK || dispatcher.calls != 1 {
		t.Fatalf("public preview status=%d body=%s calls=%d", publicRes.Code, publicRes.Body.String(), dispatcher.calls)
	}
	if got := publicRes.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("public preview CORS header = %q", got)
	}
	segmentPath := strings.TrimSuffix(parsed.Path, "/index.m3u8") + "/segment-000001.ts"
	dispatcher.result = servicecall.PreviewAssetResult{
		StatusCode:   http.StatusPartialContent,
		Success:      true,
		Body:         []byte("bcd"),
		ContentRange: "bytes 1-3/6",
		AcceptRanges: "bytes",
	}
	segmentReq := httptest.NewRequest(http.MethodGet, segmentPath, nil)
	segmentReq.Header.Set("Range", "bytes=1-3")
	segmentRes := httptest.NewRecorder()
	handler.ServeHTTP(segmentRes, segmentReq)
	if segmentRes.Code != http.StatusPartialContent || segmentRes.Header().Get("Content-Range") != "bytes 1-3/6" || segmentRes.Header().Get("Accept-Ranges") != "bytes" || segmentRes.Body.String() != "bcd" {
		t.Fatalf("public preview segment status=%d headers=%#v body=%q", segmentRes.Code, segmentRes.Header(), segmentRes.Body.String())
	}
	if dispatcher.name != "segment-000001.ts" || dispatcher.byteRange != "bytes=1-3" || !strings.Contains(segmentRes.Header().Get("Access-Control-Expose-Headers"), "Content-Range") {
		t.Fatalf("public preview segment dispatch/CORS mismatch: %s headers=%#v", formatSafeHTTPSensitiveDiagnostic(dispatcher), segmentRes.Header())
	}

	dispatcher.result = servicecall.PreviewAssetResult{
		StatusCode:   http.StatusRequestedRangeNotSatisfiable,
		Success:      true,
		ContentRange: "bytes */6",
		AcceptRanges: "bytes",
	}
	unsatisfiedReq := httptest.NewRequest(http.MethodGet, segmentPath, nil)
	unsatisfiedReq.Header.Set("Range", "bytes=99-")
	unsatisfiedRes := httptest.NewRecorder()
	handler.ServeHTTP(unsatisfiedRes, unsatisfiedReq)
	if unsatisfiedRes.Code != http.StatusRequestedRangeNotSatisfiable || unsatisfiedRes.Header().Get("Content-Range") != "bytes */6" || unsatisfiedRes.Header().Get("Accept-Ranges") != "bytes" || unsatisfiedRes.Body.Len() != 0 {
		t.Fatalf("public preview unsatisfied range status=%d headers=%#v body=%q", unsatisfiedRes.Code, unsatisfiedRes.Header(), unsatisfiedRes.Body.String())
	}
	if dispatcher.name != "segment-000001.ts" || dispatcher.byteRange != "bytes=99-" || !strings.Contains(unsatisfiedRes.Header().Get("Access-Control-Expose-Headers"), "Content-Range") {
		t.Fatalf("public preview unsatisfied range dispatch/CORS mismatch: %s headers=%#v", formatSafeHTTPSensitiveDiagnostic(dispatcher), unsatisfiedRes.Header())
	}

	dispatcher.result.ContentRange = "bytes 1-3/6"
	invalidRangeRes := httptest.NewRecorder()
	handler.ServeHTTP(invalidRangeRes, httptest.NewRequest(http.MethodGet, segmentPath, nil))
	if invalidRangeRes.Code != http.StatusBadGateway || !strings.Contains(invalidRangeRes.Body.String(), "invalid_stream_preview_range") {
		t.Fatalf("public preview malformed unsatisfied range status=%d body=%s", invalidRangeRes.Code, invalidRangeRes.Body.String())
	}

	tamperedPath := strings.Replace(parsed.Path, "ast_preview_v1.", "ast_preview_v1.X", 1)
	tamperedRes := httptest.NewRecorder()
	handler.ServeHTTP(tamperedRes, httptest.NewRequest(http.MethodGet, tamperedPath, nil))
	if tamperedRes.Code != http.StatusNotFound {
		t.Fatalf("tampered preview status=%d body=%s", tamperedRes.Code, tamperedRes.Body.String())
	}

	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	stoppedRes := httptest.NewRecorder()
	handler.ServeHTTP(stoppedRes, httptest.NewRequest(http.MethodGet, parsed.Path, nil))
	if stoppedRes.Code != http.StatusGone || dispatcher.calls != 4 {
		t.Fatalf("stopped preview status=%d body=%s calls=%d", stoppedRes.Code, stoppedRes.Body.String(), dispatcher.calls)
	}
	if strings.Contains(toJSONForTest(t, auth.AuditEvents()), "ast_ingest_v1") {
		t.Fatalf("preview token leaked into audit events: %#v", auth.AuditEvents())
	}
}

func TestPublicStreamPreviewParticipantsReturnsNamesAvatarsAndSpeakerState(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "participant preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &previewFakeDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher), WithPreviewSigningKey("preview-signing-key"))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	linkReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/preview-links", nil)
	linkReq.AddCookie(cookie)
	linkReq.Header.Set("X-CSRF-Token", csrf)
	linkRes := httptest.NewRecorder()
	handler.ServeHTTP(linkRes, linkReq)
	if linkRes.Code != http.StatusCreated {
		t.Fatalf("preview link status=%d body=%s", linkRes.Code, linkRes.Body.String())
	}
	var link struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(linkRes.Body).Decode(&link); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	previewPath := strings.TrimSuffix(parsed.Path, "/index.m3u8") + "/participants"
	dispatcher.workerEvents = servicecall.WorkerEventsResult{
		ServiceID:   "encoder_recorder-01",
		ServiceType: "encoder_recorder",
		StatusCode:  http.StatusOK,
		Success:     true,
		Events: []servicecall.WorkerEvent{
			{Type: "overlay.participants", Payload: map[string]any{"participants": []any{
				map[string]any{"user_id": "user-01", "display_name": "Alice", "avatar_url": "https://cdn.discordapp.com/avatars/user-01/a.png", "is_bot": false, "speaking": true},
				map[string]any{"user_id": "bot-01", "display_name": "Helper", "is_bot": true},
			}}},
			{Type: "overlay.active_speaker", Payload: map[string]any{"user_id": "user-01"}},
		},
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, previewPath, nil))
	if res.Code != http.StatusOK || res.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("participants status=%d headers=%#v body=%s", res.Code, res.Header(), res.Body.String())
	}
	body := res.Body.String()
	for _, expected := range []string{"Alice", "https://cdn.discordapp.com/avatars/user-01/a.png", "Helper", `"active_speaker_id":"user-01"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("participants response missing %q: %s", expected, body)
		}
	}
}

func TestPreviewResponsesUsePersistedNegotiatedVideoOverlayBurnIn(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "burned-in preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder")
	dispatcher := &previewFakeDispatcher{}
	dispatcher.workerEvents = servicecall.WorkerEventsResult{StatusCode: http.StatusOK, Success: true}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher), WithPreviewSigningKey("preview-signing-key"))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	unknownLinkReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/preview-links", nil)
	unknownLinkReq.AddCookie(cookie)
	unknownLinkReq.Header.Set("X-CSRF-Token", csrf)
	unknownLinkRes := httptest.NewRecorder()
	handler.ServeHTTP(unknownLinkRes, unknownLinkReq)
	if unknownLinkRes.Code != http.StatusCreated || !strings.Contains(unknownLinkRes.Body.String(), `"video_overlay_burn_in":false`) {
		t.Fatalf("preview link did not use legacy fallback for unknown burn-in state: status=%d body=%s", unknownLinkRes.Code, unknownLinkRes.Body.String())
	}
	if err := streams.SetStreamVideoOverlayBurnIn(t.Context(), stream.ID, true); err != nil {
		t.Fatal(err)
	}
	linkReq := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/preview-links", nil)
	linkReq.AddCookie(cookie)
	linkReq.Header.Set("X-CSRF-Token", csrf)
	linkRes := httptest.NewRecorder()
	handler.ServeHTTP(linkRes, linkReq)
	if linkRes.Code != http.StatusCreated || !strings.Contains(linkRes.Body.String(), `"video_overlay_burn_in":true`) {
		t.Fatalf("preview link lost persisted burn-in state: status=%d body=%s", linkRes.Code, linkRes.Body.String())
	}
	var link struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(linkRes.Body).Decode(&link); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	participantsPath := strings.TrimSuffix(parsed.Path, "/index.m3u8") + "/participants"
	participantsRes := httptest.NewRecorder()
	handler.ServeHTTP(participantsRes, httptest.NewRequest(http.MethodGet, participantsPath, nil))
	if participantsRes.Code != http.StatusOK || !strings.Contains(participantsRes.Body.String(), `"video_overlay_burn_in":true`) {
		t.Fatalf("participants response lost persisted burn-in state: status=%d body=%s", participantsRes.Code, participantsRes.Body.String())
	}
}

func TestPublicPreviewParticipantsClearsSpeakerOnSpeakingStop(t *testing.T) {
	response := publicPreviewParticipantsFromEvents([]servicecall.WorkerEvent{
		{Type: "overlay.participants", Payload: map[string]any{"participants": []any{
			map[string]any{"user_id": "user-01", "display_name": "Alice", "speaking": true},
		}}},
		{Type: "overlay.active_speaker", Payload: map[string]any{"user_id": "user-01", "speaking": true}},
		{Type: "overlay.active_speaker", Payload: map[string]any{"user_id": "user-01", "speaking": false}},
	})
	if response.ActiveSpeakerID != "" || len(response.Participants) != 1 || response.Participants[0].Speaking {
		t.Fatalf("speaking stop did not clear preview state: %#v", response)
	}
}

func TestConfiguredStreamPreviewURLDoesNotTrustRequestHostFallbacks(t *testing.T) {
	path := "/stream-previews/signed-token/index.m3u8"
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "")
	if got := configuredStreamPreviewURL(path); got != path {
		t.Fatalf("unconfigured preview URL must stay relative: %q", got)
	}
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "javascript:alert(1)")
	if got := configuredStreamPreviewURL(path); got != path {
		t.Fatalf("unsafe public URL must not be used: %q", got)
	}
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://panel.example.jp/control")
	if got := configuredStreamPreviewURL(path); got != "https://panel.example.jp/control"+path {
		t.Fatalf("configured preview URL was not used: %q", got)
	}
}

func TestValidatedStreamPreviewPlaylistAllowsLongDVRWindow(t *testing.T) {
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for index := 0; index < 300; index++ {
		fmt.Fprintf(&playlist, "#EXTINF:2.0,\nsegment-%06d.ts\n", index)
	}
	if _, ok := validatedStreamPreviewPlaylist([]byte(playlist.String())); !ok {
		t.Fatal("long start-to-now preview playlist was rejected")
	}
}

func TestAdminServiceRuntimeConfigPreviewIsSecretSafe(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"service_health.read"}); err != nil {
		t.Fatal(err)
	}
	stream, err := streams.CreateStream(t.Context(), "runtime preview stream")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-preview-01", ServiceType: "discord_bot", ServiceName: "Discord Preview 01", PublicURL: "https://discord-preview.example.com", Version: "0.1.0", Capabilities: map[string]any{"runtime_config": true}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-preview-01", stream.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord preview", map[string]any{
		"service_id":            "discord-preview-01",
		"guild_id":              "guild-preview",
		"voice_channel_id":      "voice-preview",
		"bot_token_secret_name": "discord_bot_token_preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: discordProfile.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))
	cookie, _ := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/service-health/discord-preview-01/runtime-config", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin runtime config preview status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("admin runtime config preview response must not be cached, got %q", got)
	}
	responseBody := res.Body.String()
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Service.ServiceID != "discord-preview-01" || len(body.Assignments) != 1 {
		t.Fatalf("unexpected preview payload identity: %#v", body)
	}
	if len(body.StreamDiscordConfigs) != 1 || body.StreamDiscordConfigs[0].DiscordConfigID != discordProfile.ID {
		t.Fatalf("runtime preview omitted stream discord config: %#v", body.StreamDiscordConfigs)
	}
	for _, raw := range []string{"discord.com/api/webhooks", token.RawToken, token.ID, `"token_id"`} {
		if strings.Contains(responseBody, raw) {
			t.Fatalf("admin runtime config preview leaked raw secret or token binding %q: %s", raw, responseBody)
		}
	}
	if !strings.Contains(responseBody, "discord_bot_token_preview") {
		t.Fatalf("admin runtime config preview should keep scoped secret references visible: %s", responseBody)
	}
}
