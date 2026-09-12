package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/observability"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

func formatSafeHTTPSensitiveDiagnostic(value any) string {
	return fmt.Sprintf("type=%T details=redacted", value)
}

type failingAuditStore struct{}

func (failingAuditStore) WriteAudit(context.Context, store.AuditEvent) error {
	return errors.New("audit store unavailable")
}

func (failingAuditStore) ListAudit(context.Context, store.AuditFilter) ([]store.AuditEvent, error) {
	return nil, errors.New("audit store unavailable")
}

func artifactListContains(artifacts []store.StreamArtifact, name, relativePath string) bool {
	for _, artifact := range artifacts {
		if artifact.Name == name && (relativePath == "" || artifact.RelativePath == relativePath) {
			return true
		}
	}
	return false
}

type failOnStreamAssignmentRegistry struct {
	store.ServiceRegistryStore
	failServiceID string
}

func (s failOnStreamAssignmentRegistry) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (store.RegisteredService, error) {
	if serviceID == s.failServiceID {
		return store.RegisteredService{}, errors.New("injected stream assignment failure")
	}
	return s.ServiceRegistryStore.AssignServiceToStreamWithRole(ctx, serviceID, streamID, actorUserID, assignmentRole)
}

func (s failOnStreamAssignmentRegistry) AssignServiceToStreamGuarded(ctx context.Context, mutation store.ServiceAssignmentMutation) (store.RegisteredService, error) {
	if mutation.ServiceID == s.failServiceID {
		return store.RegisteredService{}, errors.New("injected stream assignment failure")
	}
	return s.ServiceRegistryStore.AssignServiceToStreamGuarded(ctx, mutation)
}

type relayStaticStartFixtureForTest struct {
	server         *Server
	streams        *store.MemoryStreamStore
	profiles       *store.MemoryProfileStore
	stream         store.Stream
	relayBindingID string
	discord        store.Profile
	youtube        store.Profile
	dispatcher     *fakeServiceDispatcher
	youtubeLive    *fakeYouTubeLiveClient
	cookie         *http.Cookie
	csrf           string
}

func newRelayStaticStartFixtureForTest(t *testing.T, dispatcher *fakeServiceDispatcher, youtubeLive *fakeYouTubeLiveClient) relayStaticStartFixtureForTest {
	t.Helper()
	if dispatcher == nil {
		dispatcher = &fakeServiceDispatcher{}
	}
	const relayBindingID = "relay-00000000-0000-4000-8000-000000000003"
	const reusableLiveStreamID = "youtube-live-stream-dispatch"
	if youtubeLive == nil {
		youtubeLive = &fakeYouTubeLiveClient{relayStaticPrepared: ytlive.PreparedOutput{BroadcastID: "broadcast-static-dispatch", LiveStreamID: reusableLiveStreamID}}
	}

	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "streams.start", "streams.stop"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "static relay dispatch")
	if err != nil {
		t.Fatal(err)
	}
	registerServiceInstanceWithCapabilities(t, auth, "encoder_recorder-01", "encoder_recorder", map[string]any{
		"output_relay_mode":       "live_api_relay_static",
		"output_relay_binding_id": relayBindingID,
	})
	registerServiceInstance(t, auth, "worker-01", "worker")
	registerServiceInstance(t, auth, "discord_bot-01", "discord_bot")
	for _, serviceID := range []string{"encoder_recorder-01", "worker-01", "discord_bot-01"} {
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, stream.ID, "test-user"); err != nil {
			t.Fatal(err)
		}
	}

	integrations := store.NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), store.OAuthProvider{
		ProviderType: "google",
		Name:         "YouTube Google",
		Enabled:      true,
		ClientID:     "youtube-client-id",
		ClientSecret: "raw-youtube-client-secret",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RedirectURI:  "https://control.example.com/auth/oauth/google/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: "google",
		AccountLabel: "youtube account",
		RefreshToken: "raw-youtube-refresh-token",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "static relay dispatch discord", "discord_bot-01", "guild-static", "voice-static", "")
	youtube, err := profiles.CreateProfile(t.Context(), store.ProfileYouTubeOutput, "static relay dispatch output", map[string]any{
		"mode":                    "live_api_relay_static",
		"oauth_account_id":        account.ID,
		"relay_binding_id":        relayBindingID,
		"reusable_live_stream_id": reusableLiveStreamID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(streams,
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithProfileStore(profiles),
		WithIntegrationStore(integrations),
		WithYouTubeLiveClient(&relayStaticCompletingYouTubeLiveClient{fakeYouTubeLiveClient: youtubeLive}),
		withManualDiscordTargetForTest(t, streams, stream.ID, "1001", "1002", "1003"),
		WithServiceDispatcher(dispatcher),
	)
	cookie, csrf := loginForTest(t, server, "operator", "correct horse battery")
	return relayStaticStartFixtureForTest{
		server:         server,
		streams:        streams,
		profiles:       profiles,
		stream:         stream,
		relayBindingID: relayBindingID,
		discord:        discord,
		youtube:        youtube,
		dispatcher:     dispatcher,
		youtubeLive:    youtubeLive,
		cookie:         cookie,
		csrf:           csrf,
	}
}

// relayStaticReservationBarrierStreamStore lets an HTTP regression mutate the
// durable output configuration exactly after the server has read it but before
// the static binding reservation is attempted. The actual MemoryStreamStore
// performs the reservation, so this exercises the same revision and settings
// fences as the regular in-memory server.
type relayStaticReservationBarrierStreamStore struct {
	*store.MemoryStreamStore
	beforeReserve func()
}

func (s *relayStaticReservationBarrierStreamStore) ReserveStreamYouTubeRelayBindingClaim(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	if before := s.beforeReserve; before != nil {
		s.beforeReserve = nil
		before()
	}
	return s.MemoryStreamStore.ReserveStreamYouTubeRelayBindingClaim(ctx, claim)
}

// relayStaticSelectionBarrierStreamStore changes the selected output after the
// static mode was read but before the post-claim start-wide fence runs. This
// is the dangerous static-to-dynamic window: an implementation that rereads
// the output only in applyYouTubeOutput would otherwise run a different
// provider/encoder path under a static lifecycle claim.
type relayStaticSelectionBarrierStreamStore struct {
	*store.MemoryStreamStore
	afterStaticStartClaim func()
}

func (s *relayStaticSelectionBarrierStreamStore) ClaimStreamStart(ctx context.Context, request store.StreamStartClaimRequest) (store.ClaimedStreamStart, error) {
	claimed, err := s.MemoryStreamStore.ClaimStreamStart(ctx, request)
	if err == nil && s.afterStaticStartClaim != nil {
		after := s.afterStaticStartClaim
		s.afterStaticStartClaim = nil
		after()
	}
	return claimed, err
}

func (s *relayStaticSelectionBarrierStreamStore) TransitionStreamStatus(ctx context.Context, id, from, to string) (store.Stream, bool, error) {
	stream, transitioned, err := s.MemoryStreamStore.TransitionStreamStatus(ctx, id, from, to)
	if transitioned && to == "starting" && s.afterStaticStartClaim != nil {
		after := s.afterStaticStartClaim
		s.afterStaticStartClaim = nil
		after()
	}
	return stream, transitioned, err
}

// relayStaticFinalizeResponseLostStreamStore models a response loss after the
// atomic finalizer committed. The server must verify the exact prepared claim
// and runtime before it sends the Encoder start request.
type relayStaticFinalizeResponseLostStreamStore struct {
	*store.MemoryStreamStore
	responseLost bool
}

func (s *relayStaticFinalizeResponseLostStreamStore) FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx context.Context, claim store.YouTubeRelayBindingClaim, runtime store.StreamYouTubeRuntime) error {
	if err := s.MemoryStreamStore.FinalizeStreamYouTubeRuntimeAndMarkRelayBindingPrepared(ctx, claim, runtime); err != nil {
		return err
	}
	if s.responseLost {
		s.responseLost = false
		return errors.New("relay static finalizer response lost")
	}
	return nil
}

// relayStaticPrepareMarkerResponseLostStreamStore models the worst safe
// provider handoff boundary: the marker commits but its response is lost before
// the Panel can call PrepareRelayStatic. The server must not issue a second
// Prepare; it must turn the visible possible-prepare reservation into recovery.
type relayStaticPrepareMarkerResponseLostStreamStore struct {
	*store.MemoryStreamStore
	responseLost bool
}

func (s *relayStaticPrepareMarkerResponseLostStreamStore) MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	marked, err := s.MemoryStreamStore.MarkReservedStreamYouTubeRelayBindingClaimPossiblyPrepared(ctx, claim)
	if err != nil {
		return store.YouTubeRelayBindingClaim{}, err
	}
	if s.responseLost {
		s.responseLost = false
		return store.YouTubeRelayBindingClaim{}, errors.New("relay static prepare marker response lost")
	}
	return marked, nil
}

// relayStaticDispatchMarkerRepairStreamStore models the nastiest downstream
// handoff outage: the dispatch marker commits, its response and the immediate
// re-read are lost, and the first attempt to mark recovery also fails. The
// explicit recovery endpoint must later normalize the inactive prepared claim
// atomically instead of leaving the reusable binding permanently occupied.
type relayStaticDispatchMarkerRepairStreamStore struct {
	*store.MemoryStreamStore
	markerCommitted         bool
	failPostMarkerRead      bool
	failInitialMarkRecovery bool
}

func (s *relayStaticDispatchMarkerRepairStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	_, err := s.MemoryStreamStore.MarkPreparedStreamYouTubeRelayBindingClaimPossiblyDispatched(ctx, claim)
	if err != nil {
		return store.YouTubeRelayBindingClaim{}, err
	}
	s.markerCommitted = true
	s.failPostMarkerRead = true
	return store.YouTubeRelayBindingClaim{}, errors.New("relay static dispatch marker response lost")
}

func (s *relayStaticDispatchMarkerRepairStreamStore) GetStreamYouTubeRelayBindingClaimForStream(ctx context.Context, streamID string) (store.YouTubeRelayBindingClaim, error) {
	if s.markerCommitted && s.failPostMarkerRead {
		s.failPostMarkerRead = false
		return store.YouTubeRelayBindingClaim{}, errors.New("relay static dispatch marker read unavailable")
	}
	return s.MemoryStreamStore.GetStreamYouTubeRelayBindingClaimForStream(ctx, streamID)
}

func (s *relayStaticDispatchMarkerRepairStreamStore) MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	if s.failInitialMarkRecovery {
		s.failInitialMarkRecovery = false
		return store.YouTubeRelayBindingClaim{}, errors.New("relay static initial recovery write unavailable")
	}
	return s.MemoryStreamStore.MarkStreamYouTubeRelayBindingClaimRecoveryRequired(ctx, claim)
}

// relayStaticEncoderStopReceiptRecordingStreamStore proves the panel persists
// the Encoder acknowledgement before it releases the static claim. Once the
// provider completion succeeds the claim is intentionally gone, so observing
// this fenced Store call is the only direct regression proof of ordering.
type relayStaticEncoderStopReceiptRecordingStreamStore struct {
	*store.MemoryStreamStore
	encoderStopReceiptCalls int
	lastEncoderStopClaim    store.YouTubeRelayBindingClaim
}

func (s *relayStaticEncoderStopReceiptRecordingStreamStore) MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx context.Context, claim store.YouTubeRelayBindingClaim) (store.YouTubeRelayBindingClaim, error) {
	s.encoderStopReceiptCalls++
	s.lastEncoderStopClaim = claim
	return s.MemoryStreamStore.MarkPreparedStreamYouTubeRelayBindingClaimEncoderStopConfirmed(ctx, claim)
}

func (f relayStaticStartFixtureForTest) start(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/streams/"+f.stream.ID+"/start", bytes.NewBufferString(`{"discord_config_id":"`+f.discord.ID+`","youtube_output_id":"`+f.youtube.ID+`"}`))
	req.AddCookie(f.cookie)
	req.Header.Set("X-CSRF-Token", f.csrf)
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	return res
}

func setRelayStaticReservationPreconditionsForTest(t *testing.T, streams *store.MemoryStreamStore, profiles *store.MemoryProfileStore, stream store.Stream, youtube store.Profile) store.Stream {
	t.Helper()
	profiles.BindStreamYouTubeRelayBindingClaims(streams)
	configured, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{YouTubeOutputID: youtube.ID})
	if err != nil {
		t.Fatalf("set relay-static stream output: %v", err)
	}
	starting, transitioned, err := streams.TransitionStreamStatus(t.Context(), configured.ID, configured.Status, "starting")
	if err != nil || !transitioned {
		t.Fatalf("claim relay-static stream lifecycle: transitioned=%t err=%v", transitioned, err)
	}
	return starting
}

func assertAuditFailureReason(t *testing.T, events []store.AuditEvent, action, resourceID, reason string) {
	t.Helper()
	for _, event := range events {
		if event.Action == action && event.ResourceID == resourceID && event.Result == "failure" && event.Metadata["reason"] == reason {
			return
		}
	}
	t.Fatalf("missing audit failure action=%q resource=%q reason=%q: %#v", action, resourceID, reason, events)
}

func toJSONForTest(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

type captureMailer struct {
	messages  []MailMessage
	settings  []store.AppSettings
	passwords []string
	err       error
}

func (m *captureMailer) Send(_ context.Context, settings store.AppSettings, password string, message MailMessage) error {
	m.settings = append(m.settings, settings)
	m.passwords = append(m.passwords, password)
	m.messages = append(m.messages, message)
	return m.err
}

type fakeTurnstileVerifier struct {
	requests []TurnstileVerifyRequest
	result   TurnstileVerifyResult
	err      error
}

func (f *fakeTurnstileVerifier) Verify(_ context.Context, req TurnstileVerifyRequest) (TurnstileVerifyResult, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return TurnstileVerifyResult{}, f.err
	}
	return f.result, nil
}

func remediationValidationClient(t *testing.T, expectedToken string, contexts map[string]observability.RemediationDispatchContext) (observability.Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+expectedToken {
			t.Fatalf("unexpected observability auth header: %q", r.Header.Get("Authorization"))
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/remediation-actions/") || !strings.HasSuffix(r.URL.Path, "/dispatch-context") {
			http.NotFound(w, r)
			return
		}
		actionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/remediation-actions/"), "/dispatch-context")
		context, ok := contexts[actionID]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, context)
	}))
	return observability.Client{BaseURL: server.URL, Token: expectedToken, Timeout: time.Second, HTTP: server.Client()}, server.Close
}

type failingArtifactReportStreamStore struct {
	*store.MemoryStreamStore
	err error
}

func (s failingArtifactReportStreamStore) WriteStreamArtifactReport(context.Context, store.ServiceToken, store.ServiceStreamEvent, []store.StreamArtifact) error {
	return s.err
}

func disableNodeVersionUpdateChecks(t *testing.T) {
	t.Helper()
	for _, target := range nodeVersionUpdateTargets {
		t.Setenv(target.latestVersionEnv, "")
		t.Setenv(target.updateCheckURLEnv, "off")
	}
	t.Setenv(dockerVersionUpdateTarget.latestVersionEnv, "")
	t.Setenv(dockerVersionUpdateTarget.updateCheckURLEnv, "off")
}

func createServiceTokenForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceType string, scopes []string) store.ServiceToken {
	return createBoundServiceTokenForTest(t, handler, cookie, csrf, serviceType, defaultServiceIDForTest(serviceType), scopes)
}

func createBoundServiceTokenForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceType, serviceID string, scopes []string) store.ServiceToken {
	t.Helper()
	payload := map[string]any{"service_type": serviceType, "scopes": scopes}
	if stringSliceContains(scopes, "service.register") {
		payload["service_id"] = serviceID
		payload["service_name"] = serviceID
		payload["public_url"] = "https://" + serviceID + ".example.com"
		payload["version"] = "0.1.0"
		payload["capabilities"] = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api-tokens", bytes.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create token status = %d body = %s", res.Code, res.Body.String())
	}
	var token store.ServiceToken
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

func defaultServiceIDForTest(serviceType string) string {
	switch serviceType {
	case "worker":
		return "worker-01"
	case "encoder_recorder":
		return "encoder-01"
	case "discord_bot":
		return "discord-01"
	case "observability":
		return "observability-01"
	default:
		return serviceType + "-01"
	}
}

func registerServiceWithTokenForTest(t *testing.T, auth *store.MemoryAuthStore, token store.ServiceToken, registration store.ServiceRegistration) store.RegisteredService {
	t.Helper()
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	service, err := auth.RegisterService(t.Context(), token, registration)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func registerObservabilityNodeForTest(t *testing.T, auth *store.MemoryAuthStore, _ string, publicURL string) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(
		t.Context(),
		"observability",
		[]string{"service.register", "service.heartbeat", "observability.ingest", "notifications.email.send", "remediation.execute"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeWithTokenForTest(t, auth, token, publicURL)
	return token
}

func registerObservabilityNodeWithTokenForTest(t *testing.T, auth *store.MemoryAuthStore, token store.ServiceToken, publicURL string) store.RegisteredService {
	t.Helper()
	t.Setenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY", "test-secret-encryption-key-32-bytes")
	t.Setenv("AUTOSTREAM_SERVICE_ALLOWED_HOSTS", "127.0.0.1")
	service := registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID:   "observability-01",
		ServiceType: "observability",
		ServiceName: "Observability",
		PublicURL:   publicURL,
	})
	ciphertext, nonce, err := security.EncryptSecret(token.RawToken, "test-secret-encryption-key-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	service, err = auth.SetServiceNodeTokenSecret(t.Context(), service.ServiceID, ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func registerServiceForTest(t *testing.T, handler http.Handler, rawToken, serviceID, serviceType string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"service_id": serviceID, "service_type": serviceType, "service_name": serviceID,
		"public_url": "https://" + serviceID + ".example.com", "version": "0.1.0", "capabilities": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("register service status = %d body = %s", res.Code, res.Body.String())
	}
}

type activeRuntimeSecretLeaseStore struct{}

func (activeRuntimeSecretLeaseStore) ClaimRuntimeSecretLease(ctx context.Context, lease store.RuntimeSecretLease, ttl time.Duration) (store.RuntimeSecretLease, error) {
	return store.RuntimeSecretLease{}, store.ErrRuntimeSecretLeaseActive
}

func (activeRuntimeSecretLeaseStore) ReleaseRuntimeSecretLease(ctx context.Context, lease store.RuntimeSecretLease) error {
	return nil
}

type trackingSecretStore struct {
	getCalls int
	statuses []store.SecretStatus
}

func (s *trackingSecretStore) ListSecretStatus(ctx context.Context) ([]store.SecretStatus, error) {
	return append([]store.SecretStatus(nil), s.statuses...), nil
}

func (s *trackingSecretStore) UpdateSecret(ctx context.Context, name, value string) (store.SecretStatus, error) {
	return store.SecretStatus{Name: name, Configured: value != ""}, nil
}

func (s *trackingSecretStore) GetSecretValue(ctx context.Context, name string) (string, error) {
	s.getCalls++
	return "Bot <RAW_DISCORD_TOKEN>", nil
}

type fakeServiceDispatcher struct {
	startCalls               int
	stopCalls                int
	retryCalls               int
	audioStatusCalls         int
	workerEventsCalls        int
	encoderPreflightCalls    int
	workerEventSendCalls     int
	archiveDownloadCalls     int
	archiveDeleteCalls       int
	archiveRenameCalls       int
	startedStream            store.Stream
	stoppedStream            store.Stream
	retriedStream            store.Stream
	audioStatusStream        store.Stream
	workerEventsStream       store.Stream
	encoderPreflightStream   store.Stream
	workerEventStream        store.Stream
	archiveStream            store.Stream
	archiveArtifact          store.StreamArtifact
	archiveByteRange         string
	archiveRenameName        string
	startRequest             servicecall.StartRequest
	retriedArchiveConfig     map[string]any
	startedServices          []store.RegisteredService
	stoppedServices          []store.RegisteredService
	retriedServices          []store.RegisteredService
	audioStatusServices      []store.RegisteredService
	workerEventsServices     []store.RegisteredService
	encoderPreflightServices []store.RegisteredService
	workerEventServices      []store.RegisteredService
	audioStatus              servicecall.AudioStatusResult
	workerEvents             servicecall.WorkerEventsResult
	encoderPreflight         servicecall.ServicePreflightResult
	workerEventRequest       servicecall.WorkerEventRequest
	startResultsOverride     []servicecall.DispatchResult
	stopResultsOverride      []servicecall.DispatchResult
	failStart                bool
	failStop                 bool
	failRetry                bool
	failAudioStatus          bool
	failWorkerEvents         bool
	failEncoderPreflight     bool
	failWorkerEventSend      bool
	failArchiveAction        bool
	dispatchFailureError     string
}

type captionRuntimeServiceDispatcher struct {
	fakeServiceDispatcher
	captionRuntimeCalls     int
	captionRuntimeStream    store.Stream
	captionRuntimeServices  []store.RegisteredService
	captionRuntimeProfileID string
	captionRuntimeResult    servicecall.DispatchResult
}

func (f *captionRuntimeServiceDispatcher) UpdateWorkerCaptionRuntimeSettings(_ context.Context, stream store.Stream, services []store.RegisteredService, captionProfileID string) servicecall.DispatchResult {
	f.captionRuntimeCalls++
	f.captionRuntimeStream = stream
	f.captionRuntimeServices = append([]store.RegisteredService(nil), services...)
	f.captionRuntimeProfileID = captionProfileID
	if f.captionRuntimeResult.ServiceType != "" || f.captionRuntimeResult.Code != "" || f.captionRuntimeResult.Success {
		return f.captionRuntimeResult
	}
	return servicecall.DispatchResult{ServiceID: "worker-caption-01", ServiceType: "worker", Endpoint: "/jobs/" + stream.ID + "/caption-runtime-settings", StatusCode: http.StatusOK, Success: true}
}

type blockingStartDispatcher struct {
	fakeServiceDispatcher
	startEntered chan struct{}
	releaseStart chan struct{}
	stopEntered  chan struct{}
}

// gatedLifecycleTransitionStore lets separate Server instances reach the same
// conditional transition together. It verifies the persisted CAS claim rather
// than relying on one Server's in-process stream lock.
type gatedLifecycleTransitionStore struct {
	store.StreamStore
	startClaims store.StreamStartClaimStore
	expected    string
	status      string
	entered     chan struct{}
	release     chan struct{}
}

func (s *gatedLifecycleTransitionStore) ClaimStreamStart(ctx context.Context, request store.StreamStartClaimRequest) (store.ClaimedStreamStart, error) {
	if s.startClaims == nil {
		return store.ClaimedStreamStart{}, store.ErrServiceAssignmentGuardUnavailable
	}
	if strings.EqualFold(strings.TrimSpace(request.ExpectedStatus), s.expected) && strings.EqualFold(s.status, "starting") {
		select {
		case s.entered <- struct{}{}:
		case <-ctx.Done():
			return store.ClaimedStreamStart{}, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return store.ClaimedStreamStart{}, ctx.Err()
		}
	}
	return s.startClaims.ClaimStreamStart(ctx, request)
}

func (s *gatedLifecycleTransitionStore) TransitionClaimedStreamStart(ctx context.Context, claim store.StreamStartOwnershipClaim, status string) (store.Stream, bool, error) {
	if s.startClaims == nil {
		return store.Stream{}, false, store.ErrServiceAssignmentGuardUnavailable
	}
	return s.startClaims.TransitionClaimedStreamStart(ctx, claim, status)
}

func (s *gatedLifecycleTransitionStore) TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (store.Stream, bool, error) {
	if strings.EqualFold(strings.TrimSpace(expectedStatus), s.expected) && strings.EqualFold(strings.TrimSpace(status), s.status) {
		select {
		case s.entered <- struct{}{}:
		case <-ctx.Done():
			return store.Stream{}, false, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return store.Stream{}, false, ctx.Err()
		}
	}
	return s.StreamStore.TransitionStreamStatus(ctx, id, expectedStatus, status)
}

type synchronizedServiceDispatcher struct {
	fakeServiceDispatcher
	mu sync.Mutex
}

// cancelingRequestStopDispatcher simulates the Discord Bot's self-stop: the
// Bot cancels the outbound auto-stop request while processing the Panel's
// downstream stop dispatch. The Panel must continue the durable lifecycle on
// a detached, bounded context.
type cancelingRequestStopDispatcher struct {
	fakeServiceDispatcher
	cancelRequest        context.CancelFunc
	stopContextCancelled bool
}

func (f *cancelingRequestStopDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	if f.cancelRequest != nil {
		f.cancelRequest()
	}
	f.stopContextCancelled = ctx.Err() != nil
	return f.fakeServiceDispatcher.Stop(ctx, stream, services)
}

func (f *synchronizedServiceDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fakeServiceDispatcher.Start(ctx, stream, services, req)
}

func (f *synchronizedServiceDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fakeServiceDispatcher.Stop(ctx, stream, services)
}

func (f *synchronizedServiceDispatcher) StartCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startCalls
}

func (f *synchronizedServiceDispatcher) StopCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

func waitForLifecycleTransitionAttempts(t *testing.T, entered <-chan struct{}, want int) {
	t.Helper()
	for range want {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("only part of the concurrent lifecycle transition reached the CAS gate")
		}
	}
}

func receiveLifecycleResponseCodes(t *testing.T, responses <-chan *httptest.ResponseRecorder, want int) map[int]int {
	t.Helper()
	codes := make(map[int]int, want)
	for range want {
		select {
		case response := <-responses:
			codes[response.Code]++
		case <-time.After(time.Second):
			t.Fatalf("concurrent lifecycle request did not complete")
		}
	}
	return codes
}

type failingYouTubeRuntimeStreamStore struct {
	*store.MemoryStreamStore
	err error
}

func (s *failingYouTubeRuntimeStreamStore) SaveStreamYouTubeRuntime(context.Context, store.StreamYouTubeRuntime) error {
	return s.err
}

type failingRelayBindingClaimLookupStreamStore struct {
	*store.MemoryStreamStore
	err error
}

func (s *failingRelayBindingClaimLookupStreamStore) GetStreamYouTubeRelayBindingClaimForStream(context.Context, string) (store.YouTubeRelayBindingClaim, error) {
	return store.YouTubeRelayBindingClaim{}, s.err
}

type failingRearmStreamStore struct {
	*store.MemoryStreamStore
	failSettingsUpdate bool
}

func (s *failingRearmStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings store.StreamSettings) (store.Stream, error) {
	if s.failSettingsUpdate {
		return store.Stream{}, errors.New("waiting stream settings unavailable")
	}
	return s.MemoryStreamStore.UpdateStreamSettings(ctx, id, settings)
}

func (s *failingRearmStreamStore) TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (store.Stream, bool, error) {
	if s.failSettingsUpdate && expectedStatus == "completed" && status == "ready" {
		return store.Stream{}, false, errors.New("waiting stream status unavailable")
	}
	return s.MemoryStreamStore.TransitionStreamStatus(ctx, id, expectedStatus, status)
}

type previewFakeDispatcher struct {
	fakeServiceDispatcher
	result    servicecall.PreviewAssetResult
	calls     int
	name      string
	byteRange string
}

type notificationFakeDispatcher struct {
	fakeServiceDispatcher
	result          servicecall.DispatchResult
	notifyCalls     int
	notifiedStream  store.Stream
	notifiedEventID string
	notifiedURL     string
}

func (f *previewFakeDispatcher) PreviewAsset(ctx context.Context, stream store.Stream, services []store.RegisteredService, name, byteRange string) servicecall.PreviewAssetResult {
	f.calls++
	f.name = name
	f.byteRange = byteRange
	result := f.result
	if result.ServiceID == "" && len(services) > 0 {
		result.ServiceID = services[0].ServiceID
		result.ServiceType = services[0].ServiceType
	}
	return result
}

func (f *notificationFakeDispatcher) NotifyDiscordYouTubeLive(ctx context.Context, stream store.Stream, services []store.RegisteredService, eventID, watchURL string) servicecall.DispatchResult {
	f.notifyCalls++
	f.notifiedStream = stream
	f.notifiedEventID = eventID
	f.notifiedURL = watchURL
	result := f.result
	if result.ServiceID == "" {
		for _, service := range services {
			if service.ServiceType == "discord_bot" {
				result.ServiceID = service.ServiceID
				result.ServiceType = service.ServiceType
				break
			}
		}
	}
	if result.Endpoint == "" {
		result.Endpoint = "/streams/" + stream.ID + "/notifications/youtube-live"
	}
	if result.StatusCode == 0 && result.Error == "" && result.Code == "" {
		result.StatusCode = http.StatusOK
		result.Success = true
	}
	if result.Success && result.MessageID == "" {
		result.MessageID = "discord-message-01"
	}
	return result
}

type fakeYouTubeLiveClient struct {
	prepareCalls              int
	relayStaticPrepareCalls   int
	relayStaticCleanupCalls   int
	completeCalls             int
	prepareRequest            ytlive.PrepareRequest
	relayStaticRequest        ytlive.RelayStaticPrepareRequest
	relayStaticCleanupRequest ytlive.RelayStaticBroadcastCleanupRequest
	completeRequest           ytlive.CompleteRequest
	prepared                  ytlive.PreparedOutput
	relayStaticPrepared       ytlive.PreparedOutput
	prepareErr                error
	relayStaticPrepareErr     error
	relayStaticCleanupErr     error
	completeErr               error
	onRelayStaticPrepare      func(context.Context, ytlive.RelayStaticPrepareRequest)
	onComplete                func(context.Context, ytlive.CompleteRequest)
}

// relayStaticCompletingYouTubeLiveClient is intentionally opt-in. Generic
// LiveClient fakes must not accidentally gain the response-loss reconciliation
// contract required before a reusable static relay claim can be released.
type relayStaticCompletingYouTubeLiveClient struct {
	*fakeYouTubeLiveClient
}

type transitioningYouTubeLiveClient struct {
	*fakeYouTubeLiveClient
	transitionCalls   int
	transitionRequest ytlive.BroadcastTransitionRequest
	transitionErr     error
}

func (f *transitioningYouTubeLiveClient) TransitionBroadcastLive(_ context.Context, req ytlive.BroadcastTransitionRequest) error {
	f.transitionCalls++
	f.transitionRequest = req
	return f.transitionErr
}

func (f *relayStaticCompletingYouTubeLiveClient) CompleteRelayStaticBroadcast(ctx context.Context, req ytlive.CompleteRequest) error {
	return f.fakeYouTubeLiveClient.Complete(ctx, req)
}

type fakeOAuthVerifier struct {
	identity oauthlogin.Identity
	err      error
}

type fakeOAuthConnector struct {
	account   oauthlogin.ConnectedAccount
	err       error
	onConnect func(oauthlogin.ConnectRequest)
}

func (f fakeOAuthVerifier) Verify(ctx context.Context, req oauthlogin.VerifyRequest) (oauthlogin.Identity, error) {
	if f.err != nil {
		return oauthlogin.Identity{}, f.err
	}
	return f.identity, nil
}

func (f fakeOAuthConnector) Connect(ctx context.Context, req oauthlogin.ConnectRequest) (oauthlogin.ConnectedAccount, error) {
	if f.onConnect != nil {
		f.onConnect(req)
	}
	if f.err != nil {
		return oauthlogin.ConnectedAccount{}, f.err
	}
	return f.account, nil
}

func startOAuthForTestWithRedirect(t *testing.T, handler http.Handler, providerID, redirectAfter string) (string, *http.Cookie) {
	t.Helper()
	requestBody, err := json.Marshal(map[string]string{"redirect_after": redirectAfter})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+providerID+"/start", bytes.NewReader(requestBody))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth start status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth start response did not include state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func startOAuthForTest(t *testing.T, handler http.Handler, providerID string) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/oauth/"+providerID+"/start", bytes.NewBufferString(`{}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth start status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth start did not return state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func assertOAuthCallbackNoStoreHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if header.Get("Cache-Control") != "no-store" {
		t.Fatalf("oauth callback Cache-Control = %q, want no-store", header.Get("Cache-Control"))
	}
	if header.Get("Pragma") != "no-cache" {
		t.Fatalf("oauth callback Pragma = %q, want no-cache", header.Get("Pragma"))
	}
	if header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("oauth callback Referrer-Policy = %q, want no-referrer", header.Get("Referrer-Policy"))
	}
}

func startOAuthAccountForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, providerID string) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-accounts/start", bytes.NewBufferString(fmt.Sprintf(`{"provider_id":%q}`, providerID)))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("oauth account start status = %d body = %s", res.Code, res.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State == "" {
		t.Fatalf("oauth account start did not return state")
	}
	return body.State, findCookieForTest(t, res.Result().Cookies(), oauthStateCookieName)
}

func findCookieForTest(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %s in %#v", name, cookies)
	return nil
}

func (f *fakeYouTubeLiveClient) Prepare(ctx context.Context, req ytlive.PrepareRequest) (ytlive.PreparedOutput, error) {
	f.prepareCalls++
	f.prepareRequest = req
	if f.prepareErr != nil {
		return ytlive.PreparedOutput{}, f.prepareErr
	}
	return f.prepared, nil
}

func (f *fakeYouTubeLiveClient) PrepareRelayStatic(ctx context.Context, req ytlive.RelayStaticPrepareRequest) (ytlive.PreparedOutput, error) {
	f.relayStaticPrepareCalls++
	f.relayStaticRequest = req
	if f.onRelayStaticPrepare != nil {
		f.onRelayStaticPrepare(ctx, req)
	}
	if f.relayStaticPrepareErr != nil {
		return ytlive.PreparedOutput{}, f.relayStaticPrepareErr
	}
	return f.relayStaticPrepared, nil
}

func (f *fakeYouTubeLiveClient) DeleteRelayStaticBroadcast(ctx context.Context, req ytlive.RelayStaticBroadcastCleanupRequest) error {
	f.relayStaticCleanupCalls++
	f.relayStaticCleanupRequest = req
	return f.relayStaticCleanupErr
}

func (f *fakeYouTubeLiveClient) Complete(ctx context.Context, req ytlive.CompleteRequest) error {
	f.completeCalls++
	f.completeRequest = req
	if f.onComplete != nil {
		f.onComplete(ctx, req)
	}
	return f.completeErr
}

type readinessBlockDispatcher struct {
	fakeServiceDispatcher
	issues []servicecall.ReadinessIssue
}

type relayStaticPreFenceMutationDispatcher struct {
	*fakeServiceDispatcher
	mutate func()
}

func (f *relayStaticPreFenceMutationDispatcher) StartReadinessIssues(_ []store.RegisteredService, _ servicecall.StartRequest, _ time.Time) []servicecall.ReadinessIssue {
	if mutate := f.mutate; mutate != nil {
		f.mutate = nil
		mutate()
	}
	return nil
}

type relayStaticNotificationDispatcher struct {
	*fakeServiceDispatcher
	notifyCalls     int
	notifiedStream  store.Stream
	notifiedEventID string
	notifiedURL     string
}

func (f *relayStaticNotificationDispatcher) NotifyDiscordYouTubeLive(_ context.Context, stream store.Stream, _ []store.RegisteredService, eventID, watchURL string) servicecall.DispatchResult {
	f.notifyCalls++
	f.notifiedStream = stream
	f.notifiedEventID = eventID
	f.notifiedURL = watchURL
	return servicecall.DispatchResult{ServiceID: "discord_bot-01", ServiceType: "discord_bot", Endpoint: "/streams/" + stream.ID + "/notifications/youtube-live", StatusCode: http.StatusOK, Success: true, MessageID: "discord-static-message-01"}
}

func (f *readinessBlockDispatcher) StartReadinessIssues(services []store.RegisteredService, req servicecall.StartRequest, now time.Time) []servicecall.ReadinessIssue {
	return f.issues
}

func (f *fakeServiceDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	f.startCalls++
	f.startedStream = stream
	f.startRequest = req
	f.startedServices = append([]store.RegisteredService(nil), services...)
	if f.startResultsOverride != nil {
		return append([]servicecall.DispatchResult(nil), f.startResultsOverride...)
	}
	if f.failStart {
		return []servicecall.DispatchResult{{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/jobs/start", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(services))
	workerVideoNegotiated := servicecall.WorkerVideoCapabilitiesEnabled(services)
	for _, service := range services {
		result := servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/start", StatusCode: http.StatusAccepted, Success: true}
		if workerVideoNegotiated && service.ServiceType == "worker" {
			result.VideoOverlayBurnInNegotiated = true
		}
		results = append(results, result)
	}
	return results
}

func (f *blockingStartDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	select {
	case f.startEntered <- struct{}{}:
	default:
	}
	<-f.releaseStart
	return f.fakeServiceDispatcher.Start(ctx, stream, services, req)
}

func (f *blockingStartDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	if f.stopEntered != nil {
		select {
		case f.stopEntered <- struct{}{}:
		default:
		}
	}
	return f.fakeServiceDispatcher.Stop(ctx, stream, services)
}

func (f *fakeServiceDispatcher) Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	f.stopCalls++
	f.stoppedStream = stream
	f.stoppedServices = append([]store.RegisteredService(nil), services...)
	if f.stopResultsOverride != nil {
		return append([]servicecall.DispatchResult(nil), f.stopResultsOverride...)
	}
	if f.failStop {
		return []servicecall.DispatchResult{{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/stop", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(services))
	for _, service := range services {
		results = append(results, servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/stop", StatusCode: http.StatusAccepted, Success: true})
	}
	return results
}

func (f *fakeServiceDispatcher) RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []servicecall.DispatchResult {
	f.retryCalls++
	f.retriedStream = stream
	f.retriedArchiveConfig = archiveConfig
	for _, service := range services {
		if service.ServiceType == "encoder_recorder" {
			f.retriedServices = append(f.retriedServices, service)
		}
	}
	if f.failRetry && len(f.retriedServices) > 0 {
		return []servicecall.DispatchResult{{ServiceID: f.retriedServices[0].ServiceID, ServiceType: f.retriedServices[0].ServiceType, Endpoint: "/streams/package", Success: false, Error: f.failureError()}}
	}
	results := make([]servicecall.DispatchResult, 0, len(f.retriedServices))
	for _, service := range f.retriedServices {
		results = append(results, servicecall.DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: "/streams/package", StatusCode: http.StatusAccepted, Success: true})
	}
	return results
}

func (f *fakeServiceDispatcher) AudioStatus(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.AudioStatusResult {
	f.audioStatusCalls++
	f.audioStatusStream = stream
	f.audioStatusServices = append([]store.RegisteredService(nil), services...)
	if f.failAudioStatus {
		return servicecall.AudioStatusResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/audio-status", Success: false, Error: "failed"}
	}
	if f.audioStatus.Success || f.audioStatus.Error != "" {
		return f.audioStatus
	}
	return servicecall.AudioStatusResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/audio-status",
		StatusCode:  http.StatusOK,
		Success:     true,
		AudioBridgeState: servicecall.AudioBridgeStatus{
			StreamID:     stream.ID,
			BridgeActive: true,
		},
	}
}

func (f *fakeServiceDispatcher) WorkerEvents(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.WorkerEventsResult {
	f.workerEventsCalls++
	f.workerEventsStream = stream
	f.workerEventsServices = append([]store.RegisteredService(nil), services...)
	if f.failWorkerEvents {
		return servicecall.WorkerEventsResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/worker-events", Success: false, Error: "failed"}
	}
	if f.workerEvents.Success || f.workerEvents.Error != "" {
		return f.workerEvents
	}
	return servicecall.WorkerEventsResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/worker-events",
		StatusCode:  http.StatusOK,
		Success:     true,
		Events:      []servicecall.WorkerEvent{},
	}
}

func (f *fakeServiceDispatcher) EncoderPreflight(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.ServicePreflightResult {
	f.encoderPreflightCalls++
	f.encoderPreflightStream = stream
	f.encoderPreflightServices = append([]store.RegisteredService(nil), services...)
	if f.failEncoderPreflight {
		return servicecall.ServicePreflightResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/preflight", Success: false, Error: "failed"}
	}
	if f.encoderPreflight.Success || f.encoderPreflight.Error != "" {
		return f.encoderPreflight
	}
	return servicecall.ServicePreflightResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/preflight",
		StatusCode:  http.StatusOK,
		Success:     true,
		Ready:       true,
		CheckedAt:   time.Now().UTC(),
		Checks:      []servicecall.ServicePreflightCheck{},
	}
}

func (f *fakeServiceDispatcher) SendWorkerEvent(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.WorkerEventRequest) servicecall.DispatchResult {
	f.workerEventSendCalls++
	f.workerEventStream = stream
	f.workerEventServices = append([]store.RegisteredService(nil), services...)
	f.workerEventRequest = req
	if f.failWorkerEventSend {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/events/" + req.EventType, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{
		ServiceID:   services[0].ServiceID,
		ServiceType: services[0].ServiceType,
		Endpoint:    "/streams/" + stream.ID + "/events/" + req.EventType,
		StatusCode:  http.StatusAccepted,
		Success:     true,
	}
}

func (f *fakeServiceDispatcher) DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, byteRange string) servicecall.ArchiveArtifactDownloadResult {
	f.archiveDownloadCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	f.archiveByteRange = byteRange
	if f.failArchiveAction {
		return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	if byteRange == "bytes=0-3" {
		return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusPartialContent, Success: true, FileName: artifact.Name, ContentType: "video/mp4", ContentRange: "bytes 0-3/13", AcceptRanges: "bytes", SizeBytes: 4, Body: io.NopCloser(strings.NewReader("arch"))}
	}
	return servicecall.ArchiveArtifactDownloadResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true, FileName: artifact.Name, ContentType: "video/mp4", SizeBytes: 13, Body: io.NopCloser(strings.NewReader("archive-bytes"))}
}

func (f *fakeServiceDispatcher) DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) servicecall.DispatchResult {
	f.archiveDeleteCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	if f.failArchiveAction {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true}
}

func (f *fakeServiceDispatcher) RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) servicecall.DispatchResult {
	f.archiveRenameCalls++
	f.archiveStream = stream
	f.archiveArtifact = artifact
	f.archiveRenameName = name
	if f.failArchiveAction {
		return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, Success: false, Error: f.failureError()}
	}
	return servicecall.DispatchResult{ServiceID: services[0].ServiceID, ServiceType: services[0].ServiceType, Endpoint: "/streams/" + stream.ID + "/artifacts/" + artifact.Name, StatusCode: http.StatusOK, Success: true}
}

func (f *fakeServiceDispatcher) failureError() string {
	if f.dispatchFailureError != "" {
		return f.dispatchFailureError
	}
	return "failed"
}

func registerAssignedServices(t *testing.T, auth *store.MemoryAuthStore, streamID string, serviceTypes ...string) {
	t.Helper()
	for _, serviceType := range serviceTypes {
		serviceID := serviceType + "-01"
		registerServiceInstance(t, auth, serviceID, serviceType)
		if _, err := auth.AssignServiceToStream(t.Context(), serviceID, streamID, "test-user"); err != nil {
			t.Fatal(err)
		}
	}
}

func createDiscordConfigForTest(t *testing.T, profiles *store.MemoryProfileStore, name, serviceID, _, _, _ string) store.Profile {
	t.Helper()
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, name, map[string]any{
		"service_id":           serviceID,
		"bot_token_configured": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func withManualDiscordTargetForTest(t *testing.T, streams store.StreamStore, streamID, guildID, textChannelID, voiceChannelID string) ServerOption {
	t.Helper()
	repository := streamvisual.NewMemoryRepository(streams)
	if _, err := repository.Update(t.Context(), streamID, "test", streamvisual.Update{
		ExpectedRevision: 1,
		DiscordTarget: streamvisual.OptionalDiscordTarget{
			Set: true,
			Value: streamvisual.DiscordTarget{
				Mode:           "manual",
				GuildID:        guildID,
				TextChannelID:  textChannelID,
				VoiceChannelID: voiceChannelID,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return WithStreamVisualRepository(repository)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func registerServiceInstance(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType string) {
	t.Helper()
	capabilities := map[string]any{}
	if serviceType == "encoder_recorder" {
		capabilities["output_relay_mode"] = "direct"
	}
	registerServiceInstanceWithCapabilities(t, auth, serviceID, serviceType, capabilities)
}

func registerServiceInstanceWithCapabilities(t *testing.T, auth *store.MemoryAuthStore, serviceID, serviceType string, capabilities map[string]any) {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), serviceType, []string{"service.register", "service.heartbeat", "service.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID, PublicURL: "https://" + serviceID + ".example.com", Version: "0.1.0", Capabilities: capabilities})
}

func assignServiceForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceID, streamID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/"+serviceID+"/assign", bytes.NewBufferString(`{"stream_id":"`+streamID+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign %s to %s status = %d body = %s", serviceID, streamID, res.Code, res.Body.String())
	}
}

func assignServiceWithRoleForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, serviceID, streamID, role string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/"+serviceID+"/assign", bytes.NewBufferString(`{"stream_id":"`+streamID+`","assignment_role":"`+role+`"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("assign %s to %s as %s status = %d body = %s", serviceID, streamID, role, res.Code, res.Body.String())
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasAuditAction(events []store.AuditEvent, action string) bool {
	for _, event := range events {
		if event.Action == action {
			return true
		}
	}
	return false
}

func testAvatarPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(24 + x%80), G: uint8(100 + y%100), B: 180, A: 255})
		}
	}
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func loginForTest(t *testing.T, handler http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	body := bytes.NewBufferString(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", res.Code, res.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("missing session cookie")
	}
	var response struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.CSRFToken == "" {
		t.Fatal("missing csrf token")
	}
	return cookie, response.CSRFToken
}

func loginMFAChallengeForTest(t *testing.T, handler http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("MFA login challenge status = %d body = %s", res.Code, res.Body.String())
	}
	var response struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ChallengeToken == "" {
		t.Fatal("missing MFA challenge token")
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("MFA challenge must not issue cookies")
	}
	return response.ChallengeToken
}
