package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

type vcProfileRaceStreamStore struct {
	*store.MemoryStreamStore
	beforeSettings func()
}

func (s *vcProfileRaceStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings store.StreamSettings) (store.Stream, error) {
	if s.beforeSettings != nil {
		s.beforeSettings()
		s.beforeSettings = nil
	}
	return s.MemoryStreamStore.UpdateStreamSettings(ctx, id, settings)
}

// Exercise the operator create route and authenticated runtime projection. A
// saved target alone cannot open a managed Gateway: its selected Bot must own
// the waiting stream before any Discord event can arrive.
func TestVCWaitingCreateReservesSelectedBotBeforeStart(t *testing.T) {
	auth, streams := store.NewMemoryAuthStore(), store.NewMemoryStreamStore()
	profiles, secrets := store.NewMemoryProfileStore(), store.NewMemorySecretStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "selected-bot", ServiceType: "discord_bot", ServiceName: "selected", PublicURL: "https://selected.example.com", Version: "test"})
	unselectedToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, unselectedToken, store.ServiceRegistration{ServiceID: "unselected-bot", ServiceType: "discord_bot", ServiceName: "unselected", PublicURL: "https://unselected.example.com", Version: "test"})
	secret, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_waiting", "synthetic-waiting-credential")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "selected auth", map[string]any{"service_id": "selected-bot", "bot_token_secret_name": secret.Name, "bot_token_fingerprint": secret.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &readinessBlockDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithSecretStore(secrets), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	payload := ui188CreatePayload(t, handler, `{"name":"waiting VC","discord_config_id":"`+profile.ID+`","auto_start_trigger":"discord_voice_join","visual_settings":{"discord_target":{"mode":"manual","guild_id":"1001","voice_channel_id":"1002","text_channel_id":"1003"}}}`)
	created := serveUserJSON(t, handler, http.MethodPost, "/streams", payload, cookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var stream store.Stream
	if err := json.Unmarshal(created.Body.Bytes(), &stream); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id=selected-bot", nil)
	request.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("runtime status=%d", response.Code)
	}
	var runtime serviceRuntimeConfigResponse
	if err := json.Unmarshal(response.Body.Bytes(), &runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Assignments) != 1 || runtime.Assignments[0].StreamID != stream.ID || runtime.Assignments[0].AssignmentRole != "primary" || runtime.Assignments[0].AssignedAt.IsZero() {
		t.Fatal("saved waiting stream has no explicit primary Bot authority before start")
	}
	if runtime.CurrentDiscordBindings == nil || len(runtime.CurrentDiscordBindings.Bindings) != 1 || !runtime.CurrentDiscordBindings.Bindings[0].Ready || runtime.CurrentDiscordBindings.Bindings[0].ProfileID != profile.ID || !runtime.CurrentDiscordBindings.Bindings[0].AssignmentEpoch.Equal(runtime.Assignments[0].AssignedAt) {
		t.Fatal("selected primary is missing its current credential binding")
	}
	if len(runtime.StreamDiscordConfigs) != 1 || runtime.StreamDiscordConfigs[0].GuildID != "1001" || runtime.StreamDiscordConfigs[0].VoiceChannelID != "1002" {
		t.Fatal("saved destination was confused with Bot authentication")
	}
	unselected, err := auth.GetService(t.Context(), "unselected-bot")
	if err != nil || unselected.CurrentStreamID != "" {
		t.Fatal("unselected Bot was assigned")
	}
	// Reusing the selected Bot must reject the whole creation, leaving the
	// established owner and all other new reservations untouched.
	var retryBody map[string]any
	_ = json.Unmarshal([]byte(payload), &retryBody)
	delete(retryBody, "encoder_service_id")
	delete(retryBody, "worker_service_id")
	retryPayload, _ := json.Marshal(retryBody)
	conflict := serveUserJSON(t, handler, http.MethodPost, "/streams", ui188CreatePayload(t, handler, string(retryPayload)), cookie, csrf)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("occupied Bot create status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	rows, _ := streams.ListStreams(t.Context())
	if len(rows) != 1 {
		t.Fatal("occupied Bot rejection persisted a partial stream")
	}
	if strings.Contains(response.Body.String(), "synthetic-waiting-credential") {
		t.Fatal("runtime projection exposed credential value")
	}
	// Saving the same selection must keep its ownership epoch stable.
	noOp := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", `{"name":"renamed","discord_config_id":"`+profile.ID+`","auto_start_trigger":"discord_voice_join"}`, cookie, csrf)
	if noOp.Code != http.StatusOK {
		t.Fatalf("same-selection save status=%d body=%s", noOp.Code, noOp.Body.String())
	}
	after, _ := auth.ListServiceAssignmentsForService(t.Context(), "selected-bot")
	if len(after) != 1 || !after[0].AssignedAt.Equal(runtime.Assignments[0].AssignedAt) {
		t.Fatal("same-selection save advanced the Bot assignment epoch")
	}
	nextProfile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "replacement auth", map[string]any{"service_id": "unselected-bot", "bot_token_secret_name": secret.Name, "bot_token_fingerprint": secret.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "limited"}, "correct horse battery", []string{"streams.update"}); err != nil {
		t.Fatal(err)
	}
	limitedCookie, limitedCSRF := loginForTest(t, handler, "limited", "correct horse battery")
	editBody := `{"discord_config_id":"` + nextProfile.ID + `","auto_start_trigger":"discord_voice_join"}`
	denied := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", editBody, limitedCookie, limitedCSRF)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("Bot assignment permission status=%d", denied.Code)
	}
	other, err := streams.CreateStream(t.Context(), "legitimate other waiting stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "unselected-bot", other.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	occupied := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", editBody, cookie, csrf)
	if occupied.Code != http.StatusConflict {
		t.Fatalf("occupied replacement Bot status=%d", occupied.Code)
	}
	stillOther, _ := auth.GetService(t.Context(), "unselected-bot")
	if stillOther.CurrentStreamID != other.ID {
		t.Fatal("selection stole another legitimate waiting Bot")
	}
	expectedOther := other.ID
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), store.ServiceUnassignmentMutation{ServiceID: "unselected-bot", ActorUserID: "fixture", ExpectedCurrentStreamID: &expectedOther}); err != nil {
		t.Fatal(err)
	}
	replaced := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", editBody, cookie, csrf)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replacement save status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	oldBot, _ := auth.GetService(t.Context(), "selected-bot")
	newBot, _ := auth.GetService(t.Context(), "unselected-bot")
	if oldBot.CurrentStreamID != "" || newBot.CurrentStreamID != stream.ID || newBot.AssignmentRole != "primary" {
		t.Fatal("selection change did not release old Bot and assign the new Bot")
	}
	// This boundary uses actual CP authentication, readiness, lifecycle and
	// ownership-claim stores. External service dispatch is the controlled peer;
	// the Bot's separate SDK fixture exercises its caller and /jobs/start.
	serviceStart := func(rawToken string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
		if rawToken != "" {
			r.Header.Set("Authorization", "Bearer "+rawToken)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if denied := serviceStart(""); denied.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated auto-start status=%d", denied.Code)
	}
	if denied := serviceStart(token.RawToken); denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "service_not_primary_assignment") {
		t.Fatalf("old Bot auto-start status=%d body=%s", denied.Code, denied.Body.String())
	}
	dispatcher.issues = []servicecall.ReadinessIssue{{ServiceID: newBot.ServiceID, ServiceType: "discord_bot", Code: "service_public_url_invalid", Message: "controlled readiness rejection"}}
	if blocked := serviceStart(unselectedToken.RawToken); blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "stream_start_not_ready") {
		t.Fatalf("readiness auto-start status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	beforeStart, _ := streams.GetStream(t.Context(), stream.ID)
	if beforeStart.Status != "created" || dispatcher.startCalls != 0 {
		t.Fatal("readiness rejection claimed or dispatched the waiting stream")
	}
	dispatcher.issues = nil
	started := serviceStart(unselectedToken.RawToken)
	if started.Code != http.StatusOK {
		t.Fatalf("primary auto-start status=%d body=%s", started.Code, started.Body.String())
	}
	startedStream, _ := streams.GetStream(t.Context(), stream.ID)
	if startedStream.Status != "live" || dispatcher.startCalls != 1 || dispatcher.startRequest.DiscordGuildID != "1001" || dispatcher.startRequest.DiscordVoiceChannelID != "1002" {
		t.Fatal("authorized CP start did not claim and dispatch the saved target")
	}
	duplicate := serviceStart(unselectedToken.RawToken)
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), "already_active") || dispatcher.startCalls != 1 {
		t.Fatal("duplicate VC start dispatched twice")
	}
}

func TestVCWaitingSelectionOwnerRaceRollsBackBotAndSettings(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := &vcProfileRaceStreamStore{MemoryStreamStore: store.NewMemoryStreamStore()}
	profiles := store.NewMemoryProfileStore()
	profiles.BindStreamYouTubeRelayBindingClaims(streams.MemoryStreamStore)
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"previous-bot", "next-bot", "changed-owner-bot"} {
		registerServiceInstance(t, auth, id, "discord_bot")
	}
	previous := createDiscordConfigForTest(t, profiles, "previous auth", "previous-bot", "", "", "")
	next := createDiscordConfigForTest(t, profiles, "next auth", "next-bot", "", "", "")
	stream, err := streams.CreateStream(t.Context(), "original settings")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: previous.ID}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles))
	if _, err := auth.AssignServiceToStream(t.Context(), "previous-bot", stream.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	streams.beforeSettings = func() {
		if _, err := profiles.UpdateProfile(t.Context(), store.ProfileDiscordConfig, next.ID, next.Name, map[string]any{"service_id": "changed-owner-bot"}); err != nil {
			t.Fatal(err)
		}
	}
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	result := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", `{"name":"must not commit","discord_config_id":"`+next.ID+`"}`, cookie, csrf)
	if result.Code != http.StatusConflict || !strings.Contains(result.Body.String(), "service_assignment_conflict") {
		t.Fatalf("owner-race status=%d body=%s", result.Code, result.Body.String())
	}
	current, _ := streams.GetStream(t.Context(), stream.ID)
	beforeBot, _ := auth.GetService(t.Context(), "previous-bot")
	afterBot, _ := auth.GetService(t.Context(), "next-bot")
	changedBot, _ := auth.GetService(t.Context(), "changed-owner-bot")
	if current.Name != "original settings" || current.DiscordConfigID != previous.ID || beforeBot.CurrentStreamID != stream.ID || afterBot.CurrentStreamID != "" || changedBot.CurrentStreamID != "" {
		t.Fatal("owner-race left new settings or a mismatched primary Bot")
	}
}

func TestVCWaitingCreateRejectsUnboundBotAuthentication(t *testing.T) {
	auth, streams, profiles := store.NewMemoryAuthStore(), store.NewMemoryStreamStore(), store.NewMemoryProfileStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "wrong-type-worker", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	for _, selectedOwner := range []string{"", "missing-bot", "wrong-type-worker"} {
		t.Run("owner_"+selectedOwner, func(t *testing.T) {
			profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "invalid legacy auth "+selectedOwner, map[string]any{"service_id": selectedOwner})
			if err != nil {
				t.Fatal(err)
			}
			payload := ui188CreatePayload(t, handler, `{"name":"must not persist","discord_config_id":"`+profile.ID+`","auto_start_trigger":"discord_voice_join","visual_settings":{"discord_target":{"mode":"manual","guild_id":"1001","voice_channel_id":"1002","text_channel_id":"1003"}}}`)
			result := serveUserJSON(t, handler, http.MethodPost, "/streams", payload, cookie, csrf)
			if result.Code != http.StatusConflict || !strings.Contains(result.Body.String(), "discord_config_service_mismatch") {
				t.Fatalf("invalid authentication owner status=%d body=%s", result.Code, result.Body.String())
			}
			rows, _ := streams.ListStreams(t.Context())
			if len(rows) != 0 {
				t.Fatal("invalid authentication selection persisted a stream")
			}
			services, _ := auth.ListServices(t.Context())
			for _, service := range services {
				if service.CurrentStreamID != "" {
					t.Fatal("invalid authentication selection partially reserved a service")
				}
			}
		})
	}
}
