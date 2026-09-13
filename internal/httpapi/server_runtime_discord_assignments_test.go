package httpapi

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceRuntimeConfigIsScopedToAuthenticatedService(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	stream, err := streams.CreateStream(t.Context(), "runtime config stream")
	if err != nil {
		t.Fatal(err)
	}
	tokenOne, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	tokenTwo, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	limitedToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, tokenOne, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	registerServiceWithTokenForTest(t, auth, tokenTwo, store.ServiceRegistration{ServiceID: "discord-02", ServiceType: "discord_bot", ServiceName: "Discord 02", PublicURL: "https://discord-02.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", stream.ID, "test-user"); err != nil {
		t.Fatal(err)
	}
	discordOne, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord one", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-1",
		"voice_channel_id":      "voice-1",
		"text_channel_id":       "text-1",
		"caption_audio_url":     "https://caption.example.com/audio",
		"bot_token_secret_name": "discord-bot-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord two", map[string]any{
		"service_id":       "discord-02",
		"guild_id":         "guild-2",
		"voice_channel_id": "voice-2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID:  discordOne.ID,
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "100000000000000001", "100000000000000002", "100000000000000003"), WithAuditStore(auth))

	limitedReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	limitedReq.Header.Set("Authorization", "Bearer "+limitedToken.RawToken)
	limitedRes := httptest.NewRecorder()
	handler.ServeHTTP(limitedRes, limitedReq)
	if limitedRes.Code != http.StatusForbidden {
		t.Fatalf("limited runtime config status = %d body = %s", limitedRes.Code, limitedRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-02", nil)
	forbiddenReq.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	forbiddenRes := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("cross-service runtime config status = %d body = %s", forbiddenRes.Code, forbiddenRes.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+tokenOne.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("runtime config response must not be cached, got %q", got)
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Service.ServiceID != "discord-01" {
		t.Fatalf("wrong service in runtime config: %#v", body.Service)
	}
	if len(body.Assignments) != 1 || body.Assignments[0].StreamID != stream.ID || body.Assignments[0].AssignmentRole != "primary" {
		t.Fatalf("unexpected assignments: %#v", body.Assignments)
	}
	discordProfiles := body.Profiles[string(store.ProfileDiscordConfig)]
	if len(discordProfiles) != 1 || discordProfiles[0].Config["service_id"] != "discord-01" {
		t.Fatalf("runtime config leaked or omitted profiles: %#v", body.Profiles)
	}
	if _, ok := discordProfiles[0].Config["bot_token_secret_name"]; !ok {
		t.Fatalf("secret reference should remain visible: %#v", discordProfiles[0].Config)
	}
	if len(body.StreamDiscordConfigs) != 1 {
		t.Fatalf("expected one stream discord config, got %#v", body.StreamDiscordConfigs)
	}
	resolved := body.StreamDiscordConfigs[0]
	if resolved.StreamID != stream.ID || resolved.DiscordConfigID != discordOne.ID || resolved.AssignmentRole != "primary" {
		t.Fatalf("unexpected stream discord config identity: %#v", resolved)
	}
	if resolved.GuildID != "100000000000000001" || resolved.VoiceChannelID != "100000000000000003" {
		t.Fatalf("stream overrides were not applied: %#v", resolved)
	}
	if resolved.TextChannelID != "100000000000000002" {
		t.Fatalf("stream text channel override was not applied: %#v", resolved)
	}
	if resolved.AutoStartTrigger != "discord_voice_join" {
		t.Fatalf("stream auto-start trigger was not included: %#v", resolved)
	}
	if strings.Contains(res.Body.String(), "caption.example.com") {
		t.Fatalf("legacy arbitrary caption URL must not be exposed: %#v", resolved)
	}
	if strings.Contains(res.Body.String(), "guild-2") || strings.Contains(res.Body.String(), "discord-02") || strings.Contains(res.Body.String(), "discord.com/api/webhooks") {
		t.Fatalf("runtime config response leaked another service or raw secret: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), `"token_id"`) || strings.Contains(res.Body.String(), tokenOne.ID) {
		t.Fatalf("runtime config response leaked service token binding: %s", res.Body.String())
	}
	sanitized := sanitizeRuntimeProfileConfig(map[string]any{"service_id": "discord-01", "nested": map[string]any{"webhook_url": "https://discord.com/api/webhooks/example/token"}})
	nested, ok := sanitized["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested config map: %#v", sanitized)
	}
	if _, ok := nested["webhook_url"]; ok {
		t.Fatalf("raw nested secret-like config should be removed: %#v", sanitized)
	}
}

func TestServiceRuntimeConfigIncludesConfiguredDiscordStreamsWithoutAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	streamA, err := streams.CreateStream(t.Context(), "runtime config waiting stream a")
	if err != nil {
		t.Fatal(err)
	}
	streamB, err := streams.CreateStream(t.Context(), "runtime config waiting stream b")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "discord assigned by settings", map[string]any{
		"service_id":            "discord-01",
		"guild_id":              "guild-default",
		"voice_channel_id":      "voice-default",
		"text_channel_id":       "text-default",
		"caption_audio_url":     "https://caption.example.com/audio",
		"bot_token_secret_name": "discord-bot-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "other discord", map[string]any{
		"service_id":       "discord-02",
		"guild_id":         "guild-other",
		"voice_channel_id": "voice-other",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), streamA.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), streamB.ID, store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		AutoStartTrigger: "discord_voice_join",
	}); err != nil {
		t.Fatal(err)
	}
	visuals := streamvisual.NewMemoryRepository(streams)
	for _, target := range []struct {
		streamID, guildID, textChannelID, voiceChannelID string
	}{
		{streamA.ID, "100000000000000001", "100000000000000002", "100000000000000003"},
		{streamB.ID, "200000000000000001", "200000000000000002", "200000000000000003"},
	} {
		if _, err := visuals.Update(t.Context(), target.streamID, "test", streamvisual.Update{
			ExpectedRevision: 1,
			DiscordTarget: streamvisual.OptionalDiscordTarget{Set: true, Value: streamvisual.DiscordTarget{
				Mode: "manual", GuildID: target.guildID, TextChannelID: target.textChannelID, VoiceChannelID: target.voiceChannelID,
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithStreamVisualRepository(visuals), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Assignments) != 0 {
		t.Fatalf("manual assignments should not be required for waiting discord streams: %#v", body.Assignments)
	}
	if len(body.StreamDiscordConfigs) != 2 {
		t.Fatalf("expected two configured stream discord configs, got %#v", body.StreamDiscordConfigs)
	}
	byStream := map[string]serviceRuntimeDiscordStreamConfig{}
	for _, item := range body.StreamDiscordConfigs {
		byStream[item.StreamID] = item
		if item.AssignmentRole != "primary" {
			t.Fatalf("configured discord stream should be presented as primary runtime target: %#v", item)
		}
	}
	if byStream[streamA.ID].GuildID != "100000000000000001" || byStream[streamA.ID].VoiceChannelID != "100000000000000003" || byStream[streamA.ID].TextChannelID != "100000000000000002" {
		t.Fatalf("stream visual target was not applied for unassigned stream: %#v", byStream[streamA.ID])
	}
	if byStream[streamB.ID].GuildID != "200000000000000001" || byStream[streamB.ID].VoiceChannelID != "200000000000000003" || byStream[streamB.ID].TextChannelID != "200000000000000002" {
		t.Fatalf("stream overrides were not applied for unassigned stream: %#v", byStream[streamB.ID])
	}
	if strings.Contains(res.Body.String(), "caption.example.com") {
		t.Fatalf("legacy arbitrary caption URL must not be exposed: %#v", byStream[streamA.ID])
	}
	if strings.Contains(res.Body.String(), "guild-other") || strings.Contains(res.Body.String(), "discord-02") || strings.Contains(res.Body.String(), "discord-bot-01") {
		t.Fatalf("runtime config leaked another service or raw secret: %s", res.Body.String())
	}
}

func TestServiceRuntimeConfigExcludesCompletedDiscordSourceAfterRearm(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	profiles := store.NewMemoryProfileStore()
	source, err := streams.CreateStream(t.Context(), "completed VC source")
	if err != nil {
		t.Fatal(err)
	}
	successor, err := streams.CreateStream(t.Context(), "waiting VC successor")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "discord-01", ServiceType: "discord_bot", ServiceName: "Discord 01", PublicURL: "https://discord-01.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	discordProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "rearm discord", map[string]any{
		"service_id":       "discord-01",
		"guild_id":         "guild-rearm",
		"voice_channel_id": "voice-rearm",
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := store.StreamSettings{
		DiscordConfigID:  discordProfile.ID,
		AutoStartTrigger: autoStartTriggerDiscordVoiceJoin,
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), source.ID, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), successor.ID, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), source.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "discord-01", successor.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithAuditStore(auth))

	req := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("runtime config status = %d body = %s", res.Code, res.Body.String())
	}
	var body serviceRuntimeConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamDiscordConfigs) != 1 {
		t.Fatalf("runtime config retained terminal or ambiguous discord configs: %#v", body.StreamDiscordConfigs)
	}
	if got := body.StreamDiscordConfigs[0]; got.StreamID != successor.ID || got.AssignmentRole != "primary" {
		t.Fatalf("runtime config did not retain only the waiting successor: %#v", got)
	}
	contender, err := streams.CreateStream(t.Context(), "ambiguous waiting contender")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), contender.ID, settings); err != nil {
		t.Fatal(err)
	}
	// An explicit primary successor wins over a separately saved, unassigned
	// candidate for the same VC. This prevents a Bot refresh from replacing the
	// authoritative successor with a map-order-dependent implicit candidate.
	secondReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	secondReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	secondRes := httptest.NewRecorder()
	handler.ServeHTTP(secondRes, secondReq)
	if secondRes.Code != http.StatusOK {
		t.Fatalf("runtime config with explicit successor status = %d body = %s", secondRes.Code, secondRes.Body.String())
	}
	body = serviceRuntimeConfigResponse{}
	if err := json.NewDecoder(secondRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamDiscordConfigs) != 1 || body.StreamDiscordConfigs[0].StreamID != successor.ID {
		t.Fatalf("explicit successor did not win same-VC candidate selection: %#v", body.StreamDiscordConfigs)
	}
	if _, err := auth.UnassignServiceFromStream(t.Context(), "discord-01", "operator"); err != nil {
		t.Fatal(err)
	}
	if assignments, err := auth.ListServiceAssignmentsForService(t.Context(), "discord-01"); err != nil {
		t.Fatal(err)
	} else if len(assignments) != 0 {
		t.Fatalf("discord assignment still present after unassign: %#v", assignments)
	}
	thirdReq := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=discord-01", nil)
	thirdReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	thirdRes := httptest.NewRecorder()
	handler.ServeHTTP(thirdRes, thirdReq)
	if thirdRes.Code != http.StatusOK {
		t.Fatalf("runtime config with ambiguous candidates status = %d body = %s", thirdRes.Code, thirdRes.Body.String())
	}
	body = serviceRuntimeConfigResponse{}
	if err := json.NewDecoder(thirdRes.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.StreamDiscordConfigs) != 0 {
		successorCurrent, _ := streams.GetStream(t.Context(), successor.ID)
		contenderCurrent, _ := streams.GetStream(t.Context(), contender.ID)
		t.Fatalf("ambiguous unassigned same-VC candidates must not choose an implicit primary: source=%s successor=%#v contender=%#v configs=%#v", source.ID, successorCurrent, contenderCurrent, body.StreamDiscordConfigs)
	}
}
