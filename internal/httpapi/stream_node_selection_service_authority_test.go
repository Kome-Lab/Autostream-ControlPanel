package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

type nodeSelectionServiceAuthorityFixture struct {
	handler          *Server
	streams          *store.MemoryStreamStore
	auth             *store.MemoryAuthStore
	dispatcher       *fakeServiceDispatcher
	streamID         string
	encoderID        string
	workerID         string
	encoderProfileID string
	oldBotID         string
	newBotID         string
	oldToken         store.ServiceToken
	newToken         store.ServiceToken
	newConfig        store.Profile
}

func newNodeSelectionServiceAuthorityFixture(t *testing.T) nodeSelectionServiceAuthorityFixture {
	t.Helper()
	auth, streams := store.NewMemoryAuthStore(), store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "service caller selection authority")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	oldBotID, newBotID := "authority-old-bot", "authority-new-bot"
	scopes := []string{"service.register", "service.heartbeat", "streams.start"}
	oldToken := registerStreamIsolationService(t, auth, oldBotID, "discord_bot", scopes)
	newToken := registerStreamIsolationService(t, auth, newBotID, "discord_bot", scopes)
	if _, err := auth.Heartbeat(t.Context(), newToken, store.ServiceHeartbeat{
		ServiceID: newBotID, Status: "registered",
		Capabilities: map[string]any{"runtime_config": true, "discord_waiting_monitor_v1": true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), oldBotID, stream.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	encoderProfile, err := profiles.CreateProfile(t.Context(), store.ProfileEncoder, "authority-h264", map[string]any{"video_codec": "h264"})
	if err != nil {
		t.Fatal(err)
	}
	oldConfig := createDiscordConfigForTest(t, profiles, "old caller config", oldBotID, "1001", "1003", "1002")
	newConfig := createDiscordConfigForTest(t, profiles, "new caller config", newBotID, "1001", "1003", "1002")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth),
		WithServiceRegistryStore(auth), WithProfileStore(profiles),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher))
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := nodeSelectionServiceAuthorityFixture{
		handler: handler, streams: streams, auth: auth, dispatcher: dispatcher,
		streamID:  stream.ID,
		encoderID: primaryServiceID(primaryStreamAssignments(assignments), "encoder_recorder"),
		workerID:  primaryServiceID(primaryStreamAssignments(assignments), "worker"),
		oldBotID:  oldBotID, newBotID: newBotID, oldToken: oldToken, newToken: newToken,
		newConfig: newConfig, encoderProfileID: encoderProfile.ID,
	}
	fixture.selectBot(t, oldBotID, oldConfig.ID)
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f nodeSelectionServiceAuthorityFixture) selectBot(t *testing.T, botID, profileID string) {
	t.Helper()
	if _, err := f.streams.UpdateStreamSettings(t.Context(), f.streamID, store.StreamSettings{
		DiscordConfigID: profileID, AutoStartTrigger: autoStartTriggerDiscordVoiceJoin,
		EncoderProfileID:         f.encoderProfileID,
		ExpectedDiscordServiceID: &botID,
		NodeSelections: &store.StreamNodeSelectionUpdate{
			EncoderServiceID: &f.encoderID, WorkerServiceID: &f.workerID, DiscordServiceID: &botID,
		},
	}); err != nil {
		t.Fatal(err)
	}
}

type nodeSelectionServiceAuthoritySnapshot struct {
	stream      store.Stream
	assignments []store.RegisteredService
	services    map[string]store.RegisteredService
}

func (f nodeSelectionServiceAuthorityFixture) snapshot(t *testing.T) nodeSelectionServiceAuthoritySnapshot {
	t.Helper()
	stream, err := f.streams.GetStream(t.Context(), f.streamID)
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := f.auth.ListStreamAssignments(t.Context(), f.streamID)
	if err != nil {
		t.Fatal(err)
	}
	services := make(map[string]store.RegisteredService)
	for _, id := range []string{f.encoderID, f.workerID, f.oldBotID, f.newBotID} {
		service, err := f.auth.GetService(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		services[id] = service
	}
	return nodeSelectionServiceAuthoritySnapshot{stream: stream, assignments: assignments, services: services}
}

func (f nodeSelectionServiceAuthorityFixture) assertRejectedWithoutMutation(t *testing.T, before nodeSelectionServiceAuthoritySnapshot, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusForbidden || body.Code != code {
		t.Fatalf("service start status=%d code=%s, want 403/%s", response.Code, body.Code, code)
	}
	if f.dispatcher.startCalls != 0 || f.dispatcher.stopCalls != 0 || f.dispatcher.retryCalls != 0 {
		t.Fatal("rejected service caller dispatched a lifecycle request")
	}
	if !reflect.DeepEqual(before, f.snapshot(t)) {
		t.Fatal("rejected service caller changed the frame or execution ownership")
	}
}

func TestServiceStartRejectsOldPrimaryAfterDiscordSelectionChanges(t *testing.T) {
	fixture := newNodeSelectionServiceAuthorityFixture(t)
	fixture.selectBot(t, fixture.newBotID, fixture.newConfig.ID)
	before := fixture.snapshot(t)
	if !before.stream.NodeSelectionsConfigured || before.stream.SelectedDiscordID != fixture.newBotID ||
		primaryServiceID(primaryStreamAssignments(before.assignments), "discord_bot") != fixture.oldBotID {
		t.Fatal("fixture must retain old runtime primary while selecting the new Bot")
	}
	request := httptest.NewRequest(http.MethodPost, "/services/streams/"+fixture.streamID+"/start", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.oldToken.RawToken)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	fixture.assertRejectedWithoutMutation(t, before, response, "service_not_primary_assignment")

	// The same complete frame remains startable by its newly selected Bot.
	request = httptest.NewRequest(http.MethodPost, "/services/streams/"+fixture.streamID+"/start", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.newToken.RawToken)
	response = httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fixture.dispatcher.startCalls != 1 {
		t.Fatalf("selected Bot start status=%d dispatches=%d body=%s", response.Code, fixture.dispatcher.startCalls, response.Body.String())
	}
	if primaryServiceID(primaryStreamAssignments(fixture.dispatcher.startedServices), "discord_bot") != fixture.newBotID {
		t.Fatal("selected Bot start dispatched a different Discord Node")
	}
}

func TestServiceStartRechecksTrustedCallerAfterSelectionChanges(t *testing.T) {
	fixture := newNodeSelectionServiceAuthorityFixture(t)
	caller, assigned, err := fixture.handler.serviceTokenPrimaryAssignedToStream(t.Context(), fixture.oldToken, fixture.streamID, "discord_bot")
	if err != nil || !assigned {
		t.Fatal("fixture caller was not authorized before selection changed", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/streams/"+fixture.streamID+"/start", nil)
	request.SetPathValue("id", fixture.streamID)
	trusted := context.WithValue(request.Context(), currentUserKey{}, serviceCurrentUser(caller))
	trusted = context.WithValue(trusted, streamStartCallerServiceKey{}, caller.ServiceID)
	request = request.WithContext(trusted)

	// The outer service authorization has completed. Settings change before
	// the inner lifecycle handler reloads the frame under its lifecycle lock.
	fixture.selectBot(t, fixture.newBotID, fixture.newConfig.ID)
	before := fixture.snapshot(t)
	response := httptest.NewRecorder()
	fixture.handler.startStreamWithMaterialization(response, request, nil)
	fixture.assertRejectedWithoutMutation(t, before, response, "service_not_selected_for_stream")
}
