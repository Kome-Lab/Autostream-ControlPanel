package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

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

// Exercise saved selections, authenticated monitoring authority, and the
// separate execution claim obtained only when the selected Bot starts a frame.
func TestVCWaitingCreateReservesSelectedBotBeforeStart(t *testing.T) {
	auth, streams := store.NewMemoryAuthStore(), store.NewMemoryStreamStore()
	profiles, secrets := store.NewMemoryProfileStore(), store.NewMemorySecretStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.create", "streams.update", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	scopes := []string{"service.register", "service.heartbeat", "service.config.read", "service.secret.resolve", "streams.start"}
	token, err := auth.CreateServiceToken(t.Context(), "discord_bot", scopes)
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "selected-bot", ServiceType: "discord_bot", ServiceName: "selected", PublicURL: "https://selected.example.com", Version: "test"})
	unselectedToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", scopes)
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
	for serviceID, serviceToken := range map[string]store.ServiceToken{"selected-bot": token, "unselected-bot": unselectedToken} {
		r := httptest.NewRequest(http.MethodPost, "/services/heartbeat", strings.NewReader(`{"service_id":"`+serviceID+`","status":"online","capabilities":{"discord_waiting_monitor_v1":true}}`))
		r.Header.Set("Authorization", "Bearer "+serviceToken.RawToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusAccepted {
			t.Fatalf("Bot capability heartbeat status=%d body=%s", w.Code, w.Body.String())
		}
	}
	readRuntime := func(serviceID, rawToken string) serviceRuntimeConfigResponse {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/services/runtime-config?service_id="+serviceID, nil)
		r.Header.Set("Authorization", "Bearer "+rawToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("runtime status=%d body=%s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "synthetic-waiting-credential") {
			t.Fatal("runtime projection exposed credential value")
		}
		var config serviceRuntimeConfigResponse
		if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
			t.Fatal(err)
		}
		return config
	}
	waitingBinding := func(config serviceRuntimeConfigResponse, streamID string) serviceRuntimeDiscordWaitingBinding {
		t.Helper()
		if config.DiscordWaitingMonitor != nil {
			for _, binding := range config.DiscordWaitingMonitor.Bindings {
				if binding.StreamID == streamID {
					return binding
				}
			}
		}
		t.Fatal("saved frame is missing its waiting monitor binding")
		return serviceRuntimeDiscordWaitingBinding{}
	}
	payload := ui188CreatePayload(t, handler, `{"name":"waiting VC","discord_config_id":"`+profile.ID+`","auto_start_trigger":"discord_voice_join","visual_settings":{"discord_target":{"mode":"manual","guild_id":"1001","voice_channel_id":"1002","text_channel_id":"1003"}}}`)
	created := serveUserJSON(t, handler, http.MethodPost, "/streams", payload, cookie, csrf)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var stream store.Stream
	if err := json.Unmarshal(created.Body.Bytes(), &stream); err != nil {
		t.Fatal(err)
	}
	if stream.SelectedEncoderID == "" || stream.SelectedWorkerID == "" || stream.SelectedDiscordID != "selected-bot" || stream.AssignedEncoderID != "" || stream.AssignedWorkerID != "" {
		t.Fatal("waiting save did not persist selections separately from execution")
	}
	runtime := readRuntime("selected-bot", token.RawToken)
	if len(runtime.Assignments) != 0 || runtime.Service.CurrentStreamID != "" || runtime.CurrentDiscordBindings == nil || len(runtime.CurrentDiscordBindings.Bindings) != 0 {
		t.Fatal("saved waiting selections synthesized execution or current credential authority")
	}
	if runtime.DiscordWaitingMonitor == nil || runtime.DiscordWaitingMonitor.Version != 1 || runtime.DiscordWaitingMonitor.ExecutionActive || runtime.DiscordWaitingMonitor.AvailabilityCode != "" || len(runtime.DiscordWaitingMonitor.Bindings) != 1 {
		t.Fatal("saved waiting stream has no available monitoring authority")
	}
	binding := waitingBinding(runtime, stream.ID)
	selectionEpoch, err := time.Parse(time.RFC3339Nano, stream.SettingsRevision)
	if err != nil || !binding.Ready || binding.ProfileID != profile.ID || binding.ProfileOwnerServiceID != "selected-bot" || !binding.SelectionEpoch.Equal(selectionEpoch) || binding.GuildID != "1001" || binding.VoiceChannelID != "1002" || binding.TargetRevision == 0 {
		t.Fatal("waiting monitor does not bind the saved selection, credential profile and VC target")
	}
	for _, serviceID := range []string{stream.SelectedEncoderID, stream.SelectedWorkerID, "selected-bot", "unselected-bot"} {
		service, err := auth.GetService(t.Context(), serviceID)
		if err != nil || service.CurrentStreamID != "" {
			t.Fatal("saving a waiting frame acquired a runtime owner")
		}
	}
	beforeSharedBot, err := auth.GetService(t.Context(), "selected-bot")
	if err != nil {
		t.Fatal(err)
	}
	// Share all three selected Nodes and use a distinct VC so both monitor
	// bindings remain unambiguous. This save must not manufacture an owner.
	var retryBody map[string]any
	if err := json.Unmarshal([]byte(payload), &retryBody); err != nil {
		t.Fatal(err)
	}
	retryBody["name"] = "second waiting VC"
	retryBody["visual_settings"].(map[string]any)["discord_target"].(map[string]any)["voice_channel_id"] = "1004"
	retryPayload, err := json.Marshal(retryBody)
	if err != nil {
		t.Fatal(err)
	}
	sharedResponse := serveUserJSON(t, handler, http.MethodPost, "/streams", string(retryPayload), cookie, csrf)
	if sharedResponse.Code != http.StatusCreated {
		t.Fatalf("shared waiting save status=%d body=%s", sharedResponse.Code, sharedResponse.Body.String())
	}
	var shared store.Stream
	if err := json.Unmarshal(sharedResponse.Body.Bytes(), &shared); err != nil {
		t.Fatal(err)
	}
	rows, err := streams.ListStreams(t.Context())
	if err != nil || len(rows) != 2 || shared.SelectedEncoderID != stream.SelectedEncoderID || shared.SelectedWorkerID != stream.SelectedWorkerID || shared.SelectedDiscordID != stream.SelectedDiscordID || shared.AssignedEncoderID != "" || shared.AssignedWorkerID != "" {
		t.Fatal("shared waiting selections did not persist as an independent frame")
	}
	sharedAssignments, err := auth.ListStreamAssignments(t.Context(), shared.ID)
	if err != nil || len(sharedAssignments) != 0 {
		t.Fatal("shared waiting save acquired execution assignments")
	}
	afterSharedBot, err := auth.GetService(t.Context(), "selected-bot")
	if err != nil || !reflect.DeepEqual(beforeSharedBot, afterSharedBot) {
		t.Fatal("saving a second waiting frame changed the Bot runtime state")
	}
	sharedRuntime := readRuntime("selected-bot", token.RawToken)
	if len(sharedRuntime.Assignments) != 0 || sharedRuntime.DiscordWaitingMonitor == nil || len(sharedRuntime.DiscordWaitingMonitor.Bindings) != 2 {
		t.Fatal("shared waiting save displaced monitoring or manufactured execution authority")
	}
	if original := waitingBinding(sharedRuntime, stream.ID); !original.Ready || !original.SelectionEpoch.Equal(binding.SelectionEpoch) || original.ProfileID != binding.ProfileID || original.VoiceChannelID != binding.VoiceChannelID {
		t.Fatal("saving the second frame changed the first frame's monitor binding")
	}
	if second := waitingBinding(sharedRuntime, shared.ID); !second.Ready || second.VoiceChannelID != "1004" {
		t.Fatal("second waiting frame has no independent VC monitor binding")
	}
	noOp := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", `{"name":"renamed","discord_config_id":"`+profile.ID+`","auto_start_trigger":"discord_voice_join"}`, cookie, csrf)
	if noOp.Code != http.StatusOK {
		t.Fatalf("same-selection save status=%d body=%s", noOp.Code, noOp.Body.String())
	}
	updated, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || updated.SelectedDiscordID != "selected-bot" || updated.SelectedEncoderID != stream.SelectedEncoderID || updated.SelectedWorkerID != stream.SelectedWorkerID {
		t.Fatal("same-selection save changed the selected Nodes")
	}
	selectionEpoch, err = time.Parse(time.RFC3339Nano, updated.SettingsRevision)
	if err != nil {
		t.Fatal(err)
	}
	updatedRuntime := readRuntime("selected-bot", token.RawToken)
	updatedBinding := waitingBinding(updatedRuntime, stream.ID)
	if len(updatedRuntime.Assignments) != 0 || !updatedBinding.Ready || !updatedBinding.SelectionEpoch.Equal(selectionEpoch) {
		t.Fatal("same-selection save changed execution ownership or lost its current monitor authority")
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
		t.Fatalf("Bot selection permission status=%d", denied.Code)
	}
	other, err := streams.CreateStream(t.Context(), "legitimate other waiting stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "unselected-bot", other.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	beforeOtherBot, err := auth.GetService(t.Context(), "unselected-bot")
	if err != nil {
		t.Fatal(err)
	}
	occupied := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", editBody, cookie, csrf)
	if occupied.Code != http.StatusOK {
		t.Fatalf("saving an occupied replacement selection status=%d body=%s", occupied.Code, occupied.Body.String())
	}
	stillOther, err := auth.GetService(t.Context(), "unselected-bot")
	if err != nil || !reflect.DeepEqual(beforeOtherBot, stillOther) {
		t.Fatal("saving a replacement selection changed another Bot runtime owner")
	}
	expectedOther := other.ID
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), store.ServiceUnassignmentMutation{ServiceID: "unselected-bot", ActorUserID: "fixture", ExpectedCurrentStreamID: &expectedOther}); err != nil {
		t.Fatal(err)
	}
	replaced := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/settings", editBody, cookie, csrf)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replacement save status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	oldBot, err := auth.GetService(t.Context(), "selected-bot")
	if err != nil {
		t.Fatal(err)
	}
	newBot, err := auth.GetService(t.Context(), "unselected-bot")
	if err != nil || oldBot.CurrentStreamID != "" || newBot.CurrentStreamID != "" {
		t.Fatal("saving a replacement selection changed Bot execution ownership")
	}
	// CP authenticates the selected service, checks readiness, then atomically
	// claims execution. The external dispatcher remains the controlled peer.
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
	beforeStart, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || beforeStart.Status != "created" || beforeStart.SelectedDiscordID != "unselected-bot" || dispatcher.startCalls != 0 {
		t.Fatal("readiness rejection changed or dispatched the waiting stream")
	}
	beforeStartAssignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || len(beforeStartAssignments) != 0 {
		t.Fatal("readiness rejection claimed execution ownership")
	}
	dispatcher.issues = nil
	started := serviceStart(unselectedToken.RawToken)
	if started.Code != http.StatusOK {
		t.Fatalf("selected auto-start status=%d body=%s", started.Code, started.Body.String())
	}
	startedStream, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || startedStream.Status != "live" || dispatcher.startCalls != 1 || dispatcher.startRequest.DiscordGuildID != "1001" || dispatcher.startRequest.DiscordVoiceChannelID != "1002" {
		t.Fatal("authorized CP start did not claim and dispatch the saved target")
	}
	startedAssignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || len(startedAssignments) != 3 || assignedServiceID(startedAssignments, "encoder_recorder") != stream.SelectedEncoderID || assignedServiceID(startedAssignments, "worker") != stream.SelectedWorkerID || assignedServiceID(startedAssignments, "discord_bot") != "unselected-bot" {
		t.Fatal("actual start did not atomically claim all three selected Nodes")
	}
	for _, serviceID := range []string{stream.SelectedEncoderID, stream.SelectedWorkerID, "unselected-bot"} {
		service, err := auth.GetService(t.Context(), serviceID)
		if err != nil || service.CurrentStreamID != stream.ID || service.AssignmentRole != "primary" {
			t.Fatal("actual start has no selected Node runtime ownership")
		}
	}
	startedRuntime := readRuntime("unselected-bot", unselectedToken.RawToken)
	if len(startedRuntime.Assignments) != 1 || startedRuntime.Assignments[0].StreamID != stream.ID || startedRuntime.Assignments[0].AssignmentRole != "primary" || startedRuntime.CurrentDiscordBindings == nil || len(startedRuntime.CurrentDiscordBindings.Bindings) != 1 || !startedRuntime.CurrentDiscordBindings.Bindings[0].Ready || startedRuntime.CurrentDiscordBindings.Bindings[0].ProfileID != nextProfile.ID || !startedRuntime.CurrentDiscordBindings.Bindings[0].AssignmentEpoch.Equal(startedRuntime.Assignments[0].AssignedAt) {
		t.Fatal("actual start did not promote the selected Bot to current credential authority")
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
	previousBotID := "previous-bot"
	beforeSettings, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID: previous.ID,
		NodeSelections:  &store.StreamNodeSelectionUpdate{DiscordServiceID: &previousBotID},
	})
	if err != nil {
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
	if current.Name != "original settings" || current.DiscordConfigID != previous.ID || current.SelectedDiscordID != previousBotID || current.SettingsRevision != beforeSettings.SettingsRevision || beforeBot.CurrentStreamID != stream.ID || afterBot.CurrentStreamID != "" || changedBot.CurrentStreamID != "" {
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
