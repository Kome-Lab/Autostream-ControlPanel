package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestStreamSettingsCannotStealLiveServiceAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign", "services.unassign", "workers.assign", "workers.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	owner, err := streams.CreateStream(t.Context(), "live owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "idle target")
	if err != nil {
		t.Fatal(err)
	}
	encoderToken := registerStreamIsolationService(t, auth, "encoder-live-owner", "encoder_recorder", []string{"service.register", "service.heartbeat"})
	workerToken := registerStreamIsolationService(t, auth, "worker-live-owner", "worker", []string{"service.register", "service.heartbeat"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-live-owner", owner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-live-owner", owner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), encoderToken, store.ServiceHeartbeat{ServiceID: "encoder-live-owner", Status: "online", CurrentStreamID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), workerToken, store.ServiceHeartbeat{ServiceID: "worker-live-owner", Status: "online", CurrentStreamID: owner.ID}); err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(t.Context(), owner.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	beforeEncoder, err := auth.GetService(t.Context(), "encoder-live-owner")
	if err != nil {
		t.Fatal(err)
	}
	beforeWorker, err := auth.GetService(t.Context(), "worker-live-owner")
	if err != nil {
		t.Fatal(err)
	}
	beforeAssignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeAssignmentIDs := streamSettingsGuardAssignmentIDsForTest(t, auth)
	beforeOwner, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/streams/"+target.ID+"/settings", bytes.NewBufferString(`{"name":"must-not-steal","encoder_service_id":"encoder-live-owner","worker_service_id":"worker-live-owner"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("save shared live Node choices status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	saved, err := streams.GetStream(t.Context(), target.ID)
	if err != nil || saved.Name != "must-not-steal" || !saved.NodeSelectionsConfigured ||
		saved.SelectedEncoderID != "encoder-live-owner" || saved.SelectedWorkerID != "worker-live-owner" {
		t.Fatalf("shared live Node preferences were not saved: stream=%#v err=%v", saved, err)
	}

	unassignReq := httptest.NewRequest(http.MethodDelete, "/services/encoder-live-owner/assignment", nil)
	unassignReq.AddCookie(cookie)
	unassignReq.Header.Set("X-CSRF-Token", csrf)
	unassignRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignRes, unassignReq)
	if unassignRes.Code != http.StatusConflict || !strings.Contains(unassignRes.Body.String(), `"code":"service_unassign_protected_stream"`) {
		t.Fatalf("protected unassign status=%d body=%s", unassignRes.Code, unassignRes.Body.String())
	}

	sameReq := httptest.NewRequest(http.MethodPost, "/services/encoder-live-owner/assign", bytes.NewBufferString(`{"stream_id":"`+owner.ID+`","assignment_role":"primary"}`))
	sameReq.AddCookie(cookie)
	sameReq.Header.Set("X-CSRF-Token", csrf)
	sameRes := httptest.NewRecorder()
	handler.ServeHTTP(sameRes, sameReq)
	if sameRes.Code != http.StatusOK {
		t.Fatalf("same-target protected assignment status=%d body=%s", sameRes.Code, sameRes.Body.String())
	}

	afterEncoder, _ := auth.GetService(t.Context(), "encoder-live-owner")
	afterWorker, _ := auth.GetService(t.Context(), "worker-live-owner")
	if !reflect.DeepEqual(afterEncoder, beforeEncoder) || !reflect.DeepEqual(afterWorker, beforeWorker) {
		t.Fatalf("saving shared preferences changed live services: encoder_before=%s encoder_after=%s worker_before=%s worker_after=%s", formatSafeHTTPSensitiveDiagnostic(beforeEncoder), formatSafeHTTPSensitiveDiagnostic(afterEncoder), formatSafeHTTPSensitiveDiagnostic(beforeWorker), formatSafeHTTPSensitiveDiagnostic(afterWorker))
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(assignments, beforeAssignments) || assignedServiceID(assignments, "encoder_recorder") != "encoder-live-owner" || assignedServiceID(assignments, "worker") != "worker-live-owner" {
		t.Fatalf("live assignments changed: %s", formatSafeHTTPSensitiveDiagnostic(assignments))
	}
	targetAssignments, err := auth.ListStreamAssignments(t.Context(), target.ID)
	if err != nil || len(targetAssignments) != 0 {
		t.Fatalf("target assignments=%s err=%v", formatSafeHTTPSensitiveDiagnostic(targetAssignments), err)
	}
	if !reflect.DeepEqual(streamSettingsGuardAssignmentIDsForTest(t, auth), beforeAssignmentIDs) {
		t.Fatal("saving shared preferences or same-target assignment changed live assignment IDs")
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 || dispatcher.encoderPreflightCalls != 0 {
		t.Fatalf("saving shared preferences dispatched downstream: %s", formatSafeHTTPSensitiveDiagnostic(dispatcher))
	}
	currentOwner, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || !reflect.DeepEqual(currentOwner, beforeOwner) {
		t.Fatalf("owner lifecycle changed: stream=%#v err=%v", currentOwner, err)
	}
}

func TestStreamSettingsConflictDoesNotRollbackSameTargetNoOp(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	target, err := streams.CreateStream(t.Context(), "same target")
	if err != nil {
		t.Fatal(err)
	}
	protectedOwner, err := streams.CreateStream(t.Context(), "protected worker owner")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder-same-target", "encoder_recorder")
	registerServiceInstance(t, auth, "worker-protected-owner", "worker")
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-same-target", target.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(t.Context(), "worker-protected-owner", protectedOwner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), protectedOwner.ID, "live"); err != nil {
		t.Fatal(err)
	}
	beforeEncoder, err := auth.GetService(t.Context(), "encoder-same-target")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	beforeWorker, err := auth.GetService(t.Context(), "worker-protected-owner")
	if err != nil {
		t.Fatal(err)
	}
	beforeOwner, err := streams.GetStream(t.Context(), protectedOwner.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeAssignmentIDs := streamSettingsGuardAssignmentIDsForTest(t, auth)
	beforeTargetAssignments, err := auth.ListStreamAssignments(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeOwnerAssignments, err := auth.ListStreamAssignments(t.Context(), protectedOwner.ID)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/streams/"+target.ID+"/settings", bytes.NewBufferString(`{"name":"same target","encoder_service_id":"encoder-same-target","worker_service_id":"worker-protected-owner"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("save existing/local and shared/protected choices status=%d body=%s", res.Code, res.Body.String())
	}
	saved, err := streams.GetStream(t.Context(), target.ID)
	if err != nil || saved.Name != "same target" || !saved.NodeSelectionsConfigured ||
		saved.SelectedEncoderID != "encoder-same-target" || saved.SelectedWorkerID != "worker-protected-owner" {
		t.Fatalf("mixed Node choices were not saved: stream=%#v err=%v", saved, err)
	}
	afterEncoder, err := auth.GetService(t.Context(), "encoder-same-target")
	if err != nil || !reflect.DeepEqual(afterEncoder, beforeEncoder) {
		t.Fatalf("same-target no-op was rolled back or churned: before=%s after=%s err=%v", formatSafeHTTPSensitiveDiagnostic(beforeEncoder), formatSafeHTTPSensitiveDiagnostic(afterEncoder), err)
	}
	worker, err := auth.GetService(t.Context(), "worker-protected-owner")
	if err != nil || !reflect.DeepEqual(worker, beforeWorker) || worker.CurrentStreamID != protectedOwner.ID {
		t.Fatalf("protected worker owner changed: service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(worker), err)
	}
	targetAssignments, err := auth.ListStreamAssignments(t.Context(), target.ID)
	if err != nil || !reflect.DeepEqual(targetAssignments, beforeTargetAssignments) {
		t.Fatalf("saving preferences changed the same-target execution assignments: %v", err)
	}
	ownerAssignments, err := auth.ListStreamAssignments(t.Context(), protectedOwner.ID)
	if err != nil || !reflect.DeepEqual(ownerAssignments, beforeOwnerAssignments) {
		t.Fatalf("saving preferences changed the protected owner's execution assignments: %v", err)
	}
	if !reflect.DeepEqual(streamSettingsGuardAssignmentIDsForTest(t, auth), beforeAssignmentIDs) {
		t.Fatal("saving mixed Node preferences recreated or rolled back an execution assignment")
	}
	afterOwner, err := streams.GetStream(t.Context(), protectedOwner.ID)
	if err != nil || !reflect.DeepEqual(afterOwner, beforeOwner) {
		t.Fatalf("saving mixed preferences changed the live owner: %v", err)
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 || dispatcher.encoderPreflightCalls != 0 {
		t.Fatal("saving mixed Node preferences dispatched downstream")
	}
}

func TestStreamSettingsExpectedOwnerFencePreservesConcurrentIdleReassign(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	originalOwner, err := streams.CreateStream(t.Context(), "original owner")
	if err != nil {
		t.Fatal(err)
	}
	concurrentOwner, err := streams.CreateStream(t.Context(), "concurrent owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "stale settings target")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder-cas-owner", "encoder_recorder")
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-cas-owner", originalOwner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	registry := &settingsAssignmentCASRaceRegistry{
		MemoryAuthStore: auth,
		serviceID:       "encoder-cas-owner",
		concurrentOwner: concurrentOwner.ID,
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(registry), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	beforeAssignmentIDs := streamSettingsGuardAssignmentIDsForTest(t, auth)
	var reassignedService store.RegisteredService
	var reassignedAssignments []store.StreamServiceAssignment
	var reassignedIDs map[string]string
	registry.afterReassign = func() {
		reassignedService, err = auth.GetService(t.Context(), registry.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		reassignedAssignments, err = auth.ListServiceAssignmentsForService(t.Context(), registry.serviceID)
		if err != nil {
			t.Fatal(err)
		}
		reassignedIDs = streamSettingsGuardAssignmentIDsForTest(t, auth)
	}
	// Mutate after validation reads the original service, before it returns that
	// observation. Saving preferences must accept either idle runtime owner.
	registry.armed = true
	req := httptest.NewRequest(http.MethodPut, "/streams/"+target.ID+"/settings", bytes.NewBufferString(`{"name":"saved shared target","encoder_service_id":"encoder-cas-owner"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if registry.mutationErr != nil || !registry.reassigned || reflect.DeepEqual(reassignedIDs, beforeAssignmentIDs) {
		t.Fatalf("independent idle reassignment was not exercised: err=%v", registry.mutationErr)
	}
	if res.Code != http.StatusOK {
		t.Fatalf("save shared choice after idle reassignment status=%d body=%s", res.Code, res.Body.String())
	}
	if registry.assignCalls != 0 || registry.unassignCalls != 0 {
		t.Fatalf("preference save attempted runtime assignment/rollback: assign=%d unassign=%d", registry.assignCalls, registry.unassignCalls)
	}
	service, err := auth.GetService(t.Context(), "encoder-cas-owner")
	if err != nil || service.CurrentStreamID != concurrentOwner.ID || !reflect.DeepEqual(service, reassignedService) {
		t.Fatalf("settings stole or rolled back the concurrent owner: service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(service), err)
	}
	assignments, err := auth.ListServiceAssignmentsForService(t.Context(), registry.serviceID)
	if err != nil || !reflect.DeepEqual(assignments, reassignedAssignments) ||
		!reflect.DeepEqual(streamSettingsGuardAssignmentIDsForTest(t, auth), reassignedIDs) {
		t.Fatalf("settings recreated or rolled back the independently committed assignment: %v", err)
	}
	saved, err := streams.GetStream(t.Context(), target.ID)
	if err != nil || saved.Name != "saved shared target" || !saved.NodeSelectionsConfigured || saved.SelectedEncoderID != registry.serviceID {
		t.Fatalf("shared preference was not saved after idle reassignment: stream=%#v err=%v", saved, err)
	}
	targetAssignments, err := auth.ListStreamAssignments(t.Context(), target.ID)
	if err != nil || len(targetAssignments) != 0 {
		t.Fatalf("preference save acquired target execution ownership: %v", err)
	}
	oldAssignments, err := auth.ListStreamAssignments(t.Context(), originalOwner.ID)
	if err != nil || len(oldAssignments) != 0 {
		t.Fatalf("preference save restored the superseded original owner: %v", err)
	}
	for _, before := range []store.Stream{originalOwner, concurrentOwner} {
		after, err := streams.GetStream(t.Context(), before.ID)
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatalf("preference save changed an unrelated owner's stream: %v", err)
		}
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 || dispatcher.encoderPreflightCalls != 0 {
		t.Fatal("saving a shared choice after reassignment dispatched downstream")
	}
}

type settingsAssignmentCASRaceRegistry struct {
	*store.MemoryAuthStore
	serviceID       string
	concurrentOwner string
	once            sync.Once
	armed           bool
	reassigned      bool
	mutationErr     error
	afterReassign   func()
	assignCalls     int
	unassignCalls   int
}

func (s *settingsAssignmentCASRaceRegistry) GetService(ctx context.Context, serviceID string) (store.RegisteredService, error) {
	observed, err := s.MemoryAuthStore.GetService(ctx, serviceID)
	if err != nil || !s.armed || serviceID != s.serviceID {
		return observed, err
	}
	s.once.Do(func() {
		_, s.mutationErr = s.MemoryAuthStore.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{
			ServiceID: s.serviceID, StreamID: s.concurrentOwner, AssignmentRole: "primary",
		})
		if s.mutationErr == nil {
			s.reassigned = true
			if s.afterReassign != nil {
				s.afterReassign()
			}
		}
	})
	if s.mutationErr != nil {
		return store.RegisteredService{}, s.mutationErr
	}
	return observed, nil
}

func (s *settingsAssignmentCASRaceRegistry) AssignServiceToStreamGuarded(ctx context.Context, mutation store.ServiceAssignmentMutation) (store.RegisteredService, error) {
	s.assignCalls++
	return s.MemoryAuthStore.AssignServiceToStreamGuarded(ctx, mutation)
}

func (s *settingsAssignmentCASRaceRegistry) UnassignServiceFromStreamGuarded(ctx context.Context, mutation store.ServiceUnassignmentMutation) (store.RegisteredService, error) {
	s.unassignCalls++
	return s.MemoryAuthStore.UnassignServiceFromStreamGuarded(ctx, mutation)
}

// These fixtures perform every mutation synchronously. Snapshot the private
// execution IDs without adding an application API or copying token storage.
func streamSettingsGuardAssignmentIDsForTest(t *testing.T, auth *store.MemoryAuthStore) map[string]string {
	t.Helper()
	field := reflect.ValueOf(auth).Elem().FieldByName("assignmentIDs")
	if !field.IsValid() || field.Kind() != reflect.Map {
		t.Fatal("MemoryAuthStore has no execution assignment identity map")
	}
	snapshot := make(map[string]string, field.Len())
	entries := field.MapRange()
	for entries.Next() {
		if entries.Key().Kind() != reflect.String || entries.Value().Kind() != reflect.String {
			t.Fatal("unexpected execution assignment identity representation")
		}
		snapshot[entries.Key().String()] = entries.Value().String()
	}
	return snapshot
}

func TestStartStreamMapsProtectedDiscordMaterializationToConflict(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	owner, err := streams.CreateStream(t.Context(), "live discord owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "start target")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, target.ID, "encoder_recorder", "worker")
	registerServiceInstance(t, auth, "discord-busy-owner", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "busy discord config", "discord-busy-owner", "guild-01", "voice-01", "text-01")
	baseDispatcher := &fakeServiceDispatcher{}
	var mutationErr error
	dispatcher := &relayStaticPreFenceMutationDispatcher{
		fakeServiceDispatcher: baseDispatcher,
		mutate: func() {
			if _, mutationErr = auth.AssignServiceToStreamGuarded(t.Context(), store.ServiceAssignmentMutation{ServiceID: "discord-busy-owner", StreamID: owner.ID, AssignmentRole: "primary"}); mutationErr != nil {
				return
			}
			_, mutationErr = streams.UpdateStreamStatus(t.Context(), owner.ID, "live")
		},
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, target.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+target.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if mutationErr != nil {
		t.Fatalf("concurrent Discord ownership mutation: %v", mutationErr)
	}
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"code":"service_assignment_conflict"`) {
		t.Fatalf("protected Discord materialization status=%d body=%s", res.Code, res.Body.String())
	}
	for _, privateID := range []string{owner.ID, target.ID, "discord-busy-owner"} {
		if strings.Contains(res.Body.String(), privateID) {
			t.Fatalf("protected materialization leaked an ID: %s", res.Body.String())
		}
	}
	if baseDispatcher.startCalls != 0 {
		t.Fatalf("protected materialization dispatched start: %s", formatSafeHTTPSensitiveDiagnostic(baseDispatcher))
	}
	service, err := auth.GetService(t.Context(), "discord-busy-owner")
	if err != nil || service.CurrentStreamID != owner.ID {
		t.Fatalf("protected Discord owner changed: service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(service), err)
	}
}

func TestStartStreamClaimRejectsAssignmentMutationAfterSnapshot(t *testing.T) {
	for _, mutation := range []string{"reassign", "unassign", "delete_service", "delete_stream"} {
		t.Run(mutation, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.start"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "claim target")
			if err != nil {
				t.Fatal(err)
			}
			other, err := streams.CreateStream(t.Context(), "concurrent owner")
			if err != nil {
				t.Fatal(err)
			}
			registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker", "discord_bot")
			profiles := store.NewMemoryProfileStore()
			config := createDiscordConfigForTest(t, profiles, "claim config", "discord_bot-01", "guild-claim", "voice-claim", "text-claim")
			if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID}); err != nil {
				t.Fatal(err)
			}

			baseDispatcher := &fakeServiceDispatcher{}
			var mutationErr error
			dispatcher := &relayStaticPreFenceMutationDispatcher{
				fakeServiceDispatcher: baseDispatcher,
				mutate: func() {
					switch mutation {
					case "reassign":
						_, mutationErr = auth.AssignServiceToStreamGuarded(t.Context(), store.ServiceAssignmentMutation{ServiceID: "encoder_recorder-01", StreamID: other.ID, AssignmentRole: "primary"})
					case "unassign":
						_, mutationErr = auth.UnassignServiceFromStreamGuarded(t.Context(), store.ServiceUnassignmentMutation{ServiceID: "encoder_recorder-01"})
					case "delete_service":
						mutationErr = auth.DeleteService(t.Context(), "encoder_recorder-01")
					case "delete_stream":
						mutationErr = streams.DeleteStream(t.Context(), stream.ID)
					}
				},
			}
			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
			cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

			req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", nil)
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if mutationErr != nil {
				t.Fatalf("concurrent mutation: %v", mutationErr)
			}
			if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"code":"service_assignment_conflict"`) {
				t.Fatalf("stale start status=%d body=%s", res.Code, res.Body.String())
			}
			if baseDispatcher.startCalls != 0 {
				t.Fatalf("claim conflict dispatched stale assignments: %#v", baseDispatcher.startedServices)
			}
			if mutation != "delete_stream" {
				current, getErr := streams.GetStream(t.Context(), stream.ID)
				if getErr != nil || current.Status != "created" || current.ArchiveRunID != "" || current.ArchiveStartedAt != nil {
					t.Fatalf("claim conflict left partial stream authority: stream=%#v err=%v", current, getErr)
				}
			}
		})
	}
}

func TestStartStreamClaimDoesNotLeaveConfiguredDiscordAssignmentOnConflict(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "configured Discord claim")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	registerServiceInstance(t, auth, "discord-claim", "discord_bot")
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "configured claim", "discord-claim", "guild-claim", "voice-claim", "text-claim")
	baseDispatcher := &fakeServiceDispatcher{}
	var mutationErr error
	dispatcher := &relayStaticPreFenceMutationDispatcher{
		fakeServiceDispatcher: baseDispatcher,
		mutate: func() {
			_, mutationErr = auth.UnassignServiceFromStreamGuarded(t.Context(), store.ServiceUnassignmentMutation{ServiceID: "worker-01"})
		},
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+config.ID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if mutationErr != nil {
		t.Fatalf("concurrent mutation: %v", mutationErr)
	}
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"code":"service_assignment_conflict"`) {
		t.Fatalf("configured claim status=%d body=%s", res.Code, res.Body.String())
	}
	if baseDispatcher.startCalls != 0 {
		t.Fatalf("configured claim conflict dispatched: %#v", baseDispatcher.startedServices)
	}
	discord, err := auth.GetService(t.Context(), "discord-claim")
	if err != nil || discord.CurrentStreamID != "" {
		t.Fatalf("configured Discord assignment committed before failed claim: service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(discord), err)
	}
}

func TestServiceAutoStartClaimRejectsMutationWithoutMaterializingDiscord(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "service auto-start claim")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	discordToken, err := auth.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, discordToken, store.ServiceRegistration{ServiceID: "discord-auto-claim", ServiceType: "discord_bot", ServiceName: "Discord Auto Claim", PublicURL: "https://discord-auto-claim.example.com", Version: "0.1.0", Capabilities: map[string]any{}})
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "auto claim config", "discord-auto-claim", "guild-auto", "voice-auto", "text-auto")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: config.ID, AutoStartTrigger: "discord_voice_join"}); err != nil {
		t.Fatal(err)
	}
	baseDispatcher := &fakeServiceDispatcher{}
	var mutationErr error
	dispatcher := &relayStaticPreFenceMutationDispatcher{
		fakeServiceDispatcher: baseDispatcher,
		mutate: func() {
			_, mutationErr = auth.UnassignServiceFromStreamGuarded(t.Context(), store.ServiceUnassignmentMutation{ServiceID: "encoder_recorder-01"})
		},
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher))

	req := httptest.NewRequest(http.MethodPost, "/services/streams/"+stream.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+discordToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if mutationErr != nil {
		t.Fatalf("concurrent mutation: %v", mutationErr)
	}
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"code":"service_assignment_conflict"`) {
		t.Fatalf("service claim status=%d body=%s", res.Code, res.Body.String())
	}
	if baseDispatcher.startCalls != 0 {
		t.Fatalf("service claim conflict dispatched: %#v", baseDispatcher.startedServices)
	}
	discord, err := auth.GetService(t.Context(), "discord-auto-claim")
	if err != nil || discord.CurrentStreamID != "" {
		t.Fatalf("service auto-start materialized Discord before failed claim: service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(discord), err)
	}
}

func TestV2ArchiveReportWrongStreamDoesNotReleaseAuthority(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	streams := store.NewMemoryStreamStore()
	owner, err := streams.CreateStream(t.Context(), "v2 report owner")
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamSettings(t.Context(), owner.ID, store.StreamSettings{Name: owner.Name, ArchiveProfileID: "archive-profile"})
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := streams.CreateStream(t.Context(), "wrong v2 report target")
	if err != nil {
		t.Fatal(err)
	}
	token := registerStreamIsolationService(t, auth, "encoder-v2-wrong-stream", "encoder_recorder", []string{"service.register", "encoder.status.write"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-v2-wrong-stream", owner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if _, err := streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "archive-run-owner", startedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), owner.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	reportBody, err := json.Marshal(map[string]any{
		"service_id": "encoder-v2-wrong-stream", "stream_id": wrong.ID,
		"archive_run_id": "archive-run-owner", "archive_started_at": startedAt,
		"artifacts": []map[string]any{{"kind": "archive", "name": "final.mp4", "relative_path": "final/" + wrong.ID + "/archive-run-owner/final.mp4", "size_bytes": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewReader(reportBody))
	req.Header.Set("Authorization", "Bearer "+token.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("wrong-stream v2 report status=%d body=%s", res.Code, res.Body.String())
	}
	current, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || current.ArchiveReportedAt != nil {
		t.Fatalf("wrong-stream report closed owner authority: stream=%#v err=%v", current, err)
	}
	if _, err := auth.UnassignServiceFromStreamGuarded(t.Context(), store.ServiceUnassignmentMutation{ServiceID: "encoder-v2-wrong-stream"}); !errors.Is(err, store.ErrServiceUnassignProtectedStream) {
		t.Fatalf("wrong-stream report released owner: %v", err)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), wrong.ID)
	if err != nil || len(artifacts) != 0 {
		t.Fatalf("wrong-stream report persisted artifacts: artifacts=%#v err=%v", artifacts, err)
	}
}

func TestStreamSettingsCannotStealPendingArchiveAndReportStillSucceeds(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.update", "services.assign", "services.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	owner, err := streams.CreateStream(t.Context(), "archive owner")
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamSettings(t.Context(), owner.ID, store.StreamSettings{Name: owner.Name, ArchiveProfileID: "archive-profile", ArchiveFileName: "recording.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "idle target")
	if err != nil {
		t.Fatal(err)
	}
	token := registerStreamIsolationService(t, auth, "encoder-archive-owner", "encoder_recorder", []string{"service.register", "encoder.status.write"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-archive-owner", owner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 8, 23, 1, 30, 0, 123456789, time.UTC)
	owner, err = streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "archive-run-owner", startedAt)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(t.Context(), owner.ID, "completed")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	beforeService, err := auth.GetService(t.Context(), "encoder-archive-owner")
	if err != nil {
		t.Fatal(err)
	}
	beforeAssignmentIDs := streamSettingsGuardAssignmentIDsForTest(t, auth)
	beforeAssignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimeOwner := func() {
		t.Helper()
		service, err := auth.GetService(t.Context(), "encoder-archive-owner")
		if err != nil || !reflect.DeepEqual(service, beforeService) {
			t.Fatalf("saving archive preferences changed the reporting Encoder: %v", err)
		}
		assignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
		if err != nil || !reflect.DeepEqual(assignments, beforeAssignments) ||
			!reflect.DeepEqual(streamSettingsGuardAssignmentIDsForTest(t, auth), beforeAssignmentIDs) {
			t.Fatalf("saving archive preferences changed execution assignment identity: %v", err)
		}
		targetAssignments, err := auth.ListStreamAssignments(t.Context(), target.ID)
		if err != nil || len(targetAssignments) != 0 {
			t.Fatalf("saving archive preferences acquired target execution ownership: %v", err)
		}
	}

	reassignReq := httptest.NewRequest(http.MethodPut, "/streams/"+target.ID+"/settings", bytes.NewBufferString(`{"name":"target","encoder_service_id":"encoder-archive-owner"}`))
	reassignReq.AddCookie(cookie)
	reassignReq.Header.Set("X-CSRF-Token", csrf)
	reassignRes := httptest.NewRecorder()
	handler.ServeHTTP(reassignRes, reassignReq)
	if reassignRes.Code != http.StatusOK {
		t.Fatalf("save shared archive Encoder choice status=%d body=%s", reassignRes.Code, reassignRes.Body.String())
	}
	savedTarget, err := streams.GetStream(t.Context(), target.ID)
	if err != nil || savedTarget.Name != "target" || !savedTarget.NodeSelectionsConfigured ||
		savedTarget.SelectedEncoderID != "encoder-archive-owner" {
		t.Fatalf("archive Encoder preference was not saved: stream=%#v err=%v", savedTarget, err)
	}
	assertRuntimeOwner()

	clearReq := httptest.NewRequest(http.MethodPut, "/streams/"+owner.ID+"/settings", bytes.NewBufferString(`{"name":"archive owner","encoder_service_id":""}`))
	clearReq.AddCookie(cookie)
	clearReq.Header.Set("X-CSRF-Token", csrf)
	clearRes := httptest.NewRecorder()
	handler.ServeHTTP(clearRes, clearReq)
	if clearRes.Code != http.StatusOK {
		t.Fatalf("clear saved archive Encoder choice status=%d body=%s", clearRes.Code, clearRes.Body.String())
	}
	clearedOwner, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || !clearedOwner.NodeSelectionsConfigured || clearedOwner.SelectedEncoderID != "" ||
		clearedOwner.Status != owner.Status || clearedOwner.ArchiveRunID != owner.ArchiveRunID ||
		clearedOwner.ArchiveStartedAt == nil || !clearedOwner.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) ||
		clearedOwner.ArchiveReportedAt != nil || clearedOwner.ArchiveFileName != owner.ArchiveFileName {
		t.Fatalf("clearing preferences changed pending archive authority: stream=%#v err=%v", clearedOwner, err)
	}
	processing, err := streams.ListArchiveProcessingStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foundProcessing := false
	for _, stream := range processing {
		if stream.ID == owner.ID {
			foundProcessing = true
		}
	}
	if !foundProcessing {
		t.Fatal("clearing a saved Encoder preference removed its pending archive")
	}
	unassignReq := httptest.NewRequest(http.MethodDelete, "/services/encoder-archive-owner/assignment", nil)
	unassignReq.AddCookie(cookie)
	unassignReq.Header.Set("X-CSRF-Token", csrf)
	unassignRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignRes, unassignReq)
	if unassignRes.Code != http.StatusConflict || !strings.Contains(unassignRes.Body.String(), `"code":"service_unassign_protected_stream"`) {
		t.Fatalf("actual archive unassign status=%d body=%s", unassignRes.Code, unassignRes.Body.String())
	}
	assertRuntimeOwner()

	relativePath := "final/" + owner.ID + "/" + owner.ArchiveRunID + "/final.mp4"
	reportBody, err := json.Marshal(map[string]any{
		"service_id":         "encoder-archive-owner",
		"stream_id":          owner.ID,
		"archive_run_id":     owner.ArchiveRunID,
		"archive_started_at": owner.ArchiveStartedAt,
		"artifacts":          []map[string]any{{"kind": "archive", "name": "final.mp4", "relative_path": relativePath, "size_bytes": 321}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reportReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewReader(reportBody))
	reportReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	reportRes := httptest.NewRecorder()
	handler.ServeHTTP(reportRes, reportReq)
	if reportRes.Code != http.StatusAccepted {
		t.Fatalf("artifact report after shared/cleared preferences status=%d body=%s", reportRes.Code, reportRes.Body.String())
	}
	afterReport, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReport.ArchiveRunID != owner.ArchiveRunID || afterReport.ArchiveStartedAt == nil || !afterReport.ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) || afterReport.ArchiveReportedAt == nil || afterReport.ArchiveFileName != owner.ArchiveFileName {
		t.Fatalf("archive identity changed after preference edits/report: before=%#v after=%#v", owner, afterReport)
	}
	artifacts, err := streams.ListStreamArtifacts(t.Context(), owner.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].RelativePath != relativePath ||
		artifacts[0].ArchiveRunID != owner.ArchiveRunID || artifacts[0].ArchiveStartedAt == nil ||
		!artifacts[0].ArchiveStartedAt.Equal(*owner.ArchiveStartedAt) || artifacts[0].SourceServiceID != "encoder-archive-owner" {
		t.Fatalf("artifact report result=%#v err=%v", artifacts, err)
	}

	assertRuntimeOwner()
	processing, err = streams.ListArchiveProcessingStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range processing {
		if stream.ID == owner.ID {
			t.Fatal("authoritative artifact report did not close archive processing")
		}
	}
	// Saving the same preference after reporting still does not transfer a
	// runtime owner. Explicit runtime reassignment remains a separate operation.
	saveAfterReport := httptest.NewRequest(http.MethodPut, "/streams/"+target.ID+"/settings", bytes.NewBufferString(`{"name":"target","encoder_service_id":"encoder-archive-owner"}`))
	saveAfterReport.AddCookie(cookie)
	saveAfterReport.Header.Set("X-CSRF-Token", csrf)
	saveAfterReportRes := httptest.NewRecorder()
	handler.ServeHTTP(saveAfterReportRes, saveAfterReport)
	if saveAfterReportRes.Code != http.StatusOK {
		t.Fatalf("save shared choice after report status=%d body=%s", saveAfterReportRes.Code, saveAfterReportRes.Body.String())
	}
	assertRuntimeOwner()
	reassignAfterReport := httptest.NewRequest(http.MethodPost, "/services/encoder-archive-owner/assign", bytes.NewBufferString(`{"stream_id":"`+target.ID+`","assignment_role":"primary"}`))
	reassignAfterReport.AddCookie(cookie)
	reassignAfterReport.Header.Set("X-CSRF-Token", csrf)
	reassignAfterReportRes := httptest.NewRecorder()
	handler.ServeHTTP(reassignAfterReportRes, reassignAfterReport)
	if reassignAfterReportRes.Code != http.StatusOK {
		t.Fatalf("idle reassign after report status=%d body=%s", reassignAfterReportRes.Code, reassignAfterReportRes.Body.String())
	}
	service, err := auth.GetService(t.Context(), "encoder-archive-owner")
	if err != nil || service.CurrentStreamID != target.ID {
		t.Fatalf("idle reassign service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(service), err)
	}
	oldAssignments, err := auth.ListStreamAssignments(t.Context(), owner.ID)
	if err != nil || len(oldAssignments) != 0 {
		t.Fatalf("old owner assignments=%s err=%v", formatSafeHTTPSensitiveDiagnostic(oldAssignments), err)
	}
	if dispatcher.startCalls != 0 || dispatcher.stopCalls != 0 || dispatcher.retryCalls != 0 || dispatcher.encoderPreflightCalls != 0 {
		t.Fatal("archive preference edits dispatched downstream")
	}
}

func TestArchiveRetryKeepsEncoderOwnershipUntilArtifactReport(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.retry_upload", "streams.delete", "services.assign", "services.unassign"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	owner, err := streams.CreateStream(t.Context(), "retry owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := streams.CreateStream(t.Context(), "idle target")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	archiveProfile, err := profiles.CreateProfile(t.Context(), store.ProfileArchive, "retry archive", map[string]any{"retention_days": 30})
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamSettings(t.Context(), owner.ID, store.StreamSettings{
		Name:             owner.Name,
		ArchiveProfileID: archiveProfile.ID,
		ArchiveFileName:  "recording.mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := registerStreamIsolationService(t, auth, "encoder-retry-owner", "encoder_recorder", []string{"service.register", "encoder.status.write"})
	if _, err := auth.AssignServiceToStream(t.Context(), "encoder-retry-owner", owner.ID, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 8, 23, 2, 15, 0, 987654321, time.UTC)
	owner, err = streams.PrepareStreamArchiveRun(t.Context(), owner.ID, "archive-run-retry", startedAt)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamStatus(t.Context(), owner.ID, "completed")
	if err != nil {
		t.Fatal(err)
	}
	relativePath := "final/" + owner.ID + "/" + owner.ArchiveRunID + "/final.mp4"
	if err := streams.UpsertStreamArtifacts(t.Context(), owner.ID, []store.StreamArtifact{{
		StreamID: owner.ID, ArchiveRunID: owner.ArchiveRunID, ArchiveStartedAt: owner.ArchiveStartedAt,
		Kind: "archive", Name: "final.mp4", RelativePath: relativePath, SizeBytes: 321,
	}}); err != nil {
		t.Fatal(err)
	}
	beforeRetry, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || beforeRetry.ArchiveReportedAt == nil {
		t.Fatalf("reported run setup stream=%#v err=%v", beforeRetry, err)
	}

	dispatcher := &blockingRetryDispatcher{retryEntered: make(chan struct{}, 1), releaseRetry: make(chan struct{})}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	retryReq := httptest.NewRequest(http.MethodPost, "/streams/"+owner.ID+"/retry-upload", nil)
	retryReq.AddCookie(cookie)
	retryReq.Header.Set("X-CSRF-Token", csrf)
	retryRes := httptest.NewRecorder()
	retryDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(retryRes, retryReq)
		close(retryDone)
	}()
	select {
	case <-dispatcher.retryEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("retry dispatcher was not reached")
	}

	assignReq := httptest.NewRequest(http.MethodPost, "/services/encoder-retry-owner/assign", bytes.NewBufferString(`{"stream_id":"`+target.ID+`","assignment_role":"primary"}`))
	assignReq.AddCookie(cookie)
	assignReq.Header.Set("X-CSRF-Token", csrf)
	assignRes := httptest.NewRecorder()
	handler.ServeHTTP(assignRes, assignReq)
	if assignRes.Code != http.StatusConflict || !strings.Contains(assignRes.Body.String(), `"code":"service_assignment_protected_stream"`) {
		t.Fatalf("retry reassign status=%d body=%s", assignRes.Code, assignRes.Body.String())
	}

	unassignReq := httptest.NewRequest(http.MethodDelete, "/services/encoder-retry-owner/assignment", nil)
	unassignReq.AddCookie(cookie)
	unassignReq.Header.Set("X-CSRF-Token", csrf)
	unassignRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignRes, unassignReq)
	if unassignRes.Code != http.StatusConflict || !strings.Contains(unassignRes.Body.String(), `"code":"service_unassign_protected_stream"`) {
		t.Fatalf("retry unassign status=%d body=%s", unassignRes.Code, unassignRes.Body.String())
	}
	deleteReq := httptest.NewRequest(http.MethodDelete, "/streams/"+owner.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusConflict || !strings.Contains(deleteRes.Body.String(), `"code":"service_unassign_protected_stream"`) {
		t.Fatalf("retry stream delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	for _, response := range []string{assignRes.Body.String(), unassignRes.Body.String(), deleteRes.Body.String()} {
		for _, privateID := range []string{owner.ID, target.ID, "encoder-retry-owner"} {
			if strings.Contains(response, privateID) {
				t.Fatalf("assignment conflict leaked an ID: %s", response)
			}
		}
	}
	duringRetry, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || duringRetry.DeletedAt != nil || duringRetry.ArchiveRunID != beforeRetry.ArchiveRunID || duringRetry.ArchiveStartedAt == nil || !duringRetry.ArchiveStartedAt.Equal(*beforeRetry.ArchiveStartedAt) || duringRetry.ArchiveFileName != beforeRetry.ArchiveFileName || duringRetry.ArchiveReportedAt != nil {
		t.Fatalf("retry changed archive identity: before=%#v during=%#v err=%v", beforeRetry, duringRetry, err)
	}
	service, err := auth.GetService(t.Context(), "encoder-retry-owner")
	if err != nil || service.CurrentStreamID != owner.ID {
		t.Fatalf("retry ownership changed: service=%s err=%v", formatSafeHTTPSensitiveDiagnostic(service), err)
	}

	close(dispatcher.releaseRetry)
	select {
	case <-retryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("retry request did not complete")
	}
	if retryRes.Code != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", retryRes.Code, retryRes.Body.String())
	}

	reportBody, err := json.Marshal(map[string]any{
		"service_id":         "encoder-retry-owner",
		"stream_id":          owner.ID,
		"archive_run_id":     owner.ArchiveRunID,
		"archive_started_at": owner.ArchiveStartedAt,
		"artifacts":          []map[string]any{{"kind": "archive", "name": "final.mp4", "relative_path": relativePath, "size_bytes": 654}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reportReq := httptest.NewRequest(http.MethodPost, "/services/stream-artifacts", bytes.NewReader(reportBody))
	reportReq.Header.Set("Authorization", "Bearer "+token.RawToken)
	reportRes := httptest.NewRecorder()
	handler.ServeHTTP(reportRes, reportReq)
	if reportRes.Code != http.StatusAccepted {
		t.Fatalf("retry artifact report status=%d body=%s", reportRes.Code, reportRes.Body.String())
	}
	afterReport, err := streams.GetStream(t.Context(), owner.ID)
	if err != nil || afterReport.ArchiveRunID != beforeRetry.ArchiveRunID || afterReport.ArchiveStartedAt == nil || !afterReport.ArchiveStartedAt.Equal(*beforeRetry.ArchiveStartedAt) || afterReport.ArchiveFileName != beforeRetry.ArchiveFileName || afterReport.ArchiveReportedAt == nil {
		t.Fatalf("retry report changed archive identity: before=%#v after=%#v err=%v", beforeRetry, afterReport, err)
	}

	unassignAfterReportReq := httptest.NewRequest(http.MethodDelete, "/services/encoder-retry-owner/assignment", nil)
	unassignAfterReportReq.AddCookie(cookie)
	unassignAfterReportReq.Header.Set("X-CSRF-Token", csrf)
	unassignAfterReportRes := httptest.NewRecorder()
	handler.ServeHTTP(unassignAfterReportRes, unassignAfterReportReq)
	if unassignAfterReportRes.Code != http.StatusOK {
		t.Fatalf("unassign after retry report status=%d body=%s", unassignAfterReportRes.Code, unassignAfterReportRes.Body.String())
	}
}

type blockingRetryDispatcher struct {
	fakeServiceDispatcher
	retryEntered chan struct{}
	releaseRetry chan struct{}
}

func (f *blockingRetryDispatcher) RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []servicecall.DispatchResult {
	select {
	case f.retryEntered <- struct{}{}:
	case <-ctx.Done():
		return []servicecall.DispatchResult{{Success: false, Error: ctx.Err().Error()}}
	}
	select {
	case <-f.releaseRetry:
	case <-ctx.Done():
		return []servicecall.DispatchResult{{Success: false, Error: ctx.Err().Error()}}
	}
	return f.fakeServiceDispatcher.RetryArchiveUpload(ctx, stream, services, archiveConfig)
}
