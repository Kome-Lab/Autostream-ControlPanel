package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamStartAndReadinessRejectClaimedServiceUpdate(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "update guarded stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-01", "updater-01")
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		AgentServiceID: "updater-01", ExecutionHostID: "host-01",
		Strategy: store.SystemUpdateStrategyMaintenance, IdempotencyKey: "guard-stream-start", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-01", "host-01", "", map[string]string{"worker-01": "systemd"}, time.Now().UTC(), time.Minute); err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "update guard discord", "discord_bot-01", "guild-ready", "voice-ready", "")
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher), WithSystemUpdateStore(updates))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	body := `{"discord_config_id":"` + config.ID + `","encoder_input_url":"srt://source.example.com:9000"}`

	readinessRequest := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start-readiness", strings.NewReader(body))
	readinessRequest.AddCookie(cookie)
	readinessRequest.Header.Set("X-CSRF-Token", csrf)
	readinessResponse := httptest.NewRecorder()
	handler.ServeHTTP(readinessResponse, readinessRequest)
	if readinessResponse.Code != http.StatusOK || !strings.Contains(readinessResponse.Body.String(), "service_update_in_progress") {
		t.Fatalf("readiness response = %d %s", readinessResponse.Code, readinessResponse.Body.String())
	}

	startRequest := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", strings.NewReader(body))
	startRequest.AddCookie(cookie)
	startRequest.Header.Set("X-CSRF-Token", csrf)
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusConflict || !strings.Contains(startResponse.Body.String(), "service_update_in_progress") || !strings.Contains(startResponse.Body.String(), job.ID) {
		t.Fatalf("start response = %d %s", startResponse.Code, startResponse.Body.String())
	}
	if dispatcher.startCalls != 0 {
		t.Fatalf("start dispatched while service update was active: %#v", dispatcher)
	}
}

type updateStartRaceDispatcher struct {
	fakeServiceDispatcher
	readinessEntered chan struct{}
	releaseReadiness chan struct{}
	once             sync.Once
}

type serviceAuthenticationBarrierStore struct {
	store.ServiceRegistryStore
	once          sync.Once
	authenticated chan struct{}
}

func (s *serviceAuthenticationBarrierStore) AuthenticateServiceToken(
	ctx context.Context,
	rawToken string,
	requiredScope string,
) (store.ServiceToken, error) {
	token, err := s.ServiceRegistryStore.AuthenticateServiceToken(
		ctx,
		rawToken,
		requiredScope,
	)
	if err == nil {
		s.once.Do(func() { close(s.authenticated) })
	}
	return token, err
}

func TestSystemUpdateClaimRejectsTokenRotatedAfterInitialAuthentication(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	registerServiceInstance(t, auth, "worker-reauth-claim", "worker")
	capabilities := centralUpdateCapabilitiesForTest(
		"host-reauth-claim",
		map[string]string{"worker-reauth-claim": "systemd"},
	)
	oldToken := registerSystemUpdateAgentForTest(
		t,
		auth,
		"updater-reauth-claim",
		capabilities,
	)
	services := &serviceAuthenticationBarrierStore{
		ServiceRegistryStore: auth,
		authenticated:        make(chan struct{}),
	}
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-reauth-claim", "updater-reauth-claim")
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(services),
		WithSystemUpdateStore(updates),
	)

	server.systemUpdateOperationMu.Lock()
	locked := true
	defer func() {
		if locked {
			server.systemUpdateOperationMu.Unlock()
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		payload, err := json.Marshal(contracts.UpdateAgentClaimRequest{
			UpdaterID: "updater-reauth-claim", HostID: "host-reauth-claim",
			LeaseGeneration: 1, Fence: 1,
		})
		if err != nil {
			done <- httptest.NewRecorder()
			return
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/update-jobs/claim",
			bytes.NewReader(payload),
		)
		request.Header.Set("Authorization", "Bearer "+oldToken.RawToken)
		request.Header.Set(systemUpdateContractMajorHeader, "2")
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		done <- result
	}()
	select {
	case <-services.authenticated:
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not complete its initial service-token authentication")
	}

	newToken, err := auth.RotateServiceToken(t.Context(), oldToken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), newToken, store.ServiceHeartbeat{
		ServiceID:    "updater-reauth-claim",
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	job, _, err := updates.CreateSystemUpdateJob(
		t.Context(),
		store.CreateSystemUpdateJobParams{
			TargetID:          "worker-reauth-claim",
			TargetServiceType: "worker",
			AgentServiceID:    "updater-reauth-claim",
			ExecutionHostID:   "host-reauth-claim",
			DeploymentMode:    "systemd",
			CurrentVersion:    "v1.0.0",
			TargetVersion:     "v1.1.0",
			Strategy:          store.SystemUpdateStrategyWhenIdle,
			IdempotencyKey:    "reauth-claim",
			RequestedByUserID: "admin-01",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server.systemUpdateOperationMu.Unlock()
	locked = false

	select {
	case result := <-done:
		if result.Code != http.StatusUnauthorized ||
			!strings.Contains(result.Body.String(), `"code":"invalid_service_token"`) {
			t.Fatalf("rotated-token claim status=%d body=%s", result.Code, result.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not finish after identity lock release")
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != store.SystemUpdateStatusQueued || active.LeaseGeneration != 0 {
		t.Fatalf("pre-rotation request claimed post-rotation job: %#v", active)
	}
}

func TestSystemUpdateReportRejectsTokenRotatedAfterInitialAuthentication(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	capabilities := centralUpdateCapabilitiesForTest(
		"host-reauth-report",
		map[string]string{"worker-reauth-report": "systemd"},
	)
	oldToken := registerSystemUpdateAgentForTest(
		t,
		auth,
		"updater-reauth-report",
		capabilities,
	)
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-reauth-report", "updater-reauth-report")
	job, _, err := updates.CreateSystemUpdateJob(
		t.Context(),
		store.CreateSystemUpdateJobParams{
			TargetID:          "worker-reauth-report",
			TargetServiceType: "worker",
			AgentServiceID:    "updater-reauth-report",
			ExecutionHostID:   "host-reauth-report",
			DeploymentMode:    "systemd",
			CurrentVersion:    "v1.0.0",
			TargetVersion:     "v1.1.0",
			Strategy:          store.SystemUpdateStrategyWhenIdle,
			IdempotencyKey:    "reauth-report",
			RequestedByUserID: "admin-01",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = updates.ClaimSystemUpdateJob(
		t.Context(),
		"updater-reauth-report",
		"host-reauth-report",
		"",
		map[string]string{"worker-reauth-report": "systemd"},
		time.Now().UTC(),
		systemUpdateExecutionLeaseTTL,
	)
	if err != nil {
		t.Fatal(err)
	}
	services := &serviceAuthenticationBarrierStore{
		ServiceRegistryStore: auth,
		authenticated:        make(chan struct{}),
	}
	server := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(services),
		WithSystemUpdateStore(updates),
	)
	reportBody := []byte(`{}`)

	server.systemUpdateOperationMu.Lock()
	locked := true
	defer func() {
		if locked {
			server.systemUpdateOperationMu.Unlock()
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/services/update-jobs/"+job.ID+"/report",
			bytes.NewReader(reportBody),
		)
		request.Header.Set("Authorization", "Bearer "+oldToken.RawToken)
		request.Header.Set(systemUpdateContractMajorHeader, "2")
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		done <- result
	}()
	select {
	case <-services.authenticated:
	case <-time.After(3 * time.Second):
		t.Fatal("report did not complete its initial service-token authentication")
	}
	newToken, err := auth.RotateServiceToken(t.Context(), oldToken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), newToken, store.ServiceHeartbeat{
		ServiceID:    "updater-reauth-report",
		Status:       "online",
		Version:      "v1.0.1",
		Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	server.systemUpdateOperationMu.Unlock()
	locked = false

	select {
	case result := <-done:
		if result.Code != http.StatusUnauthorized ||
			!strings.Contains(result.Body.String(), `"code":"invalid_service_token"`) {
			t.Fatalf("rotated-token report status=%d body=%s", result.Code, result.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("report did not finish after identity lock release")
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != store.SystemUpdateStatusClaimed {
		t.Fatalf("pre-rotation request reported post-rotation job: %#v", active)
	}
}

func (f *updateStartRaceDispatcher) StartReadinessIssues(_ []store.RegisteredService, _ servicecall.StartRequest, _ time.Time) []servicecall.ReadinessIssue {
	f.once.Do(func() { close(f.readinessEntered) })
	<-f.releaseReadiness
	return nil
}

func TestStreamStartWinsClaimRaceAndKeepsQueuedUpdateUnclaimed(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "claim race stream")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, requiredStartServiceTypes...)
	capabilities := centralUpdateCapabilitiesForTest("host-race", map[string]string{"worker-01": "systemd"})
	agentToken := registerSystemUpdateAgentForTest(t, auth, "updater-race", capabilities)
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-race", "updater-race")
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-race", ExecutionHostID: "host-race", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "race-start-claim", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	config := createDiscordConfigForTest(t, profiles, "race discord", "discord_bot-01", "", "", "")
	dispatcher := &updateStartRaceDispatcher{readinessEntered: make(chan struct{}), releaseReadiness: make(chan struct{})}
	server := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"), WithServiceDispatcher(dispatcher), WithSystemUpdateStore(updates))
	cookie, csrf := loginForTest(t, server, "operator", "correct horse battery")
	body := `{"discord_config_id":"` + config.ID + `","encoder_input_url":"srt://source.example.com:9000"}`

	startDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/streams/"+stream.ID+"/start", strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		startDone <- response
	}()
	select {
	case <-dispatcher.readinessEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("stream start did not reach readiness barrier")
	}
	if server.systemUpdateOperationMu.TryLock() {
		server.systemUpdateOperationMu.Unlock()
		t.Fatal("stream start did not hold the update-operation mutex")
	}
	claimDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"updater_id":"updater-race","host_id":"host-race","lease_generation":1,"fence":1}`))
		req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
		req.Header.Set(systemUpdateContractMajorHeader, "2")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		claimDone <- response
	}()
	close(dispatcher.releaseReadiness)

	var startResponse, claimResponse *httptest.ResponseRecorder
	select {
	case startResponse = <-startDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stream start did not finish")
	}
	select {
	case claimResponse = <-claimDone:
	case <-time.After(3 * time.Second):
		t.Fatal("update claim did not finish")
	}
	if startResponse.Code != http.StatusOK || claimResponse.Code != http.StatusNoContent || dispatcher.startCalls != 1 {
		t.Fatalf("start/claim race start=%d %s claim=%d %s dispatch=%d", startResponse.Code, startResponse.Body.String(), claimResponse.Code, claimResponse.Body.String(), dispatcher.startCalls)
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil || active.Status != store.SystemUpdateStatusQueued || active.LeaseGeneration != 0 {
		t.Fatalf("busy update was claimed after stream start: %#v err=%v", active, err)
	}
}

func TestControlPanelUpdateClaimWaitsWhileAnyStreamIsActive(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "live stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), stream.ID, "live"); err != nil {
		t.Fatal(err)
	}
	auth := store.NewMemoryAuthStore()
	capabilities := centralUpdateCapabilitiesForTest("host-panel", map[string]string{"control-panel": "systemd"})
	agentToken := registerSystemUpdateAgentForTest(t, auth, "updater-panel", capabilities)
	updates := store.NewMemorySystemUpdateStore()
	bindSystemUpdateExecutionHostForTest(t, updates, "host-panel", "updater-panel")
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "control-panel", TargetServiceType: "control_panel", AgentServiceID: "updater-panel", ExecutionHostID: "host-panel", DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "panel-live", RequestedByUserID: "admin-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth), WithSystemUpdateStore(updates))
	req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"updater_id":"updater-panel","host_id":"host-panel","lease_generation":1,"fence":1}`))
	req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	req.Header.Set(systemUpdateContractMajorHeader, "2")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("control-panel claim while live = %d %s", response.Code, response.Body.String())
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil || active.Status != store.SystemUpdateStatusQueued {
		t.Fatalf("control-panel update was claimed while live: %#v err=%v", active, err)
	}
}
