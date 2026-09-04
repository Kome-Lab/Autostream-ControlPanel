package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
)

type fixedVisualRepository struct {
	settings streamvisual.Settings
	assets   streamvisual.AssetReadiness
}

type recordingVisualRepository struct {
	fixedVisualRepository
	updateCalls int
	lastUpdate  streamvisual.Update
}

type lifecycleBlockingVisualRepository struct {
	fixedVisualRepository
	entered chan struct{}
	release chan struct{}
}

func (r *lifecycleBlockingVisualRepository) Update(ctx context.Context, _ string, _ string, _ streamvisual.Update) (streamvisual.Settings, error) {
	close(r.entered)
	select {
	case <-r.release:
		return r.settings, nil
	case <-ctx.Done():
		return streamvisual.Settings{}, ctx.Err()
	}
}

func (r *recordingVisualRepository) Update(_ context.Context, _ string, _ string, update streamvisual.Update) (streamvisual.Settings, error) {
	r.updateCalls++
	r.lastUpdate = update
	return r.settings, nil
}

func (r fixedVisualRepository) Get(context.Context, string) (streamvisual.Settings, error) {
	return r.settings, nil
}
func (r fixedVisualRepository) Update(context.Context, string, string, streamvisual.Update) (streamvisual.Settings, error) {
	return r.settings, nil
}
func (r fixedVisualRepository) InspectAssets(context.Context, streamvisual.Settings) (streamvisual.AssetReadiness, error) {
	return r.assets, nil
}

func TestStreamStartAndReadinessFailClosedBeforeDispatchForMissingSceneCapability(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "starter", Username: "starter"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Visual stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "visual discord", "discord_bot-visual", "", "", "")
	if _, err := streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{
		DiscordConfigID: discord.ID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, serviceType := range []string{"discord_bot", "worker", "encoder_recorder"} {
		createRegisteredAssignedService(t, auth, serviceType+"-visual", serviceType, stream.ID)
	}
	visual := fixedVisualRepository{settings: streamvisual.Settings{StreamID: stream.ID, BackgroundMode: "image", BackgroundAssetID: "asset-bg", BackgroundVariantID: "variant-bg", HeaderTitleMode: "default", DiscordTargetMode: "manual", DiscordSnapshotRevision: 1, DiscordGuildID: "1234567890", DiscordTextChannelID: "3456789012", DiscordVoiceChannelID: "2345678901", CoverSource: "none", Revision: 1}, assets: streamvisual.AssetReadiness{BackgroundExists: true, BackgroundVariantReady: true, BackgroundHashVerified: true, CoverVariantReady: true, MediaAssetIntegrity: true}}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithStreamVisualRepository(visual), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "starter", "correct horse battery")
	readiness := serveUserJSON(t, handler, http.MethodPost, "/streams/"+stream.ID+"/start-readiness", `{}`, cookie, csrf)
	if readiness.Code != http.StatusOK || !strings.Contains(readiness.Body.String(), `"ready":false`) || !strings.Contains(readiness.Body.String(), "scene_appearance_capability") {
		t.Fatalf("readiness=%d %s", readiness.Code, readiness.Body.String())
	}
	start := serveUserJSON(t, handler, http.MethodPost, "/streams/"+stream.ID+"/start", `{}`, cookie, csrf)
	if start.Code != http.StatusConflict || !strings.Contains(start.Body.String(), "scene_appearance_capability") || dispatcher.startCalls != 0 {
		t.Fatalf("start=%d calls=%d body=%s", start.Code, dispatcher.startCalls, start.Body.String())
	}
	persisted, err := streams.GetStream(t.Context(), stream.ID)
	if err != nil || persisted.Status != "created" {
		t.Fatalf("readiness failure mutated stream=%#v err=%v", persisted, err)
	}
}

func TestStreamVisualSettingsHTTPRequiresRevisionAndPreservesExplicitClear(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "editor", Username: "editor"}, "correct horse battery", []string{"streams.read", "streams.update"}); err != nil {
		t.Fatal(err)
	}
	repository := &recordingVisualRepository{fixedVisualRepository: fixedVisualRepository{settings: streamvisual.Settings{StreamID: "stream-visual", BackgroundMode: "default", HeaderTitleMode: "default", CoverSource: "none", Revision: 5}}}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithStreamVisualRepository(repository))
	cookie, csrf := loginForTest(t, handler, "editor", "correct horse battery")

	missing := serveUserJSON(t, handler, http.MethodPut, "/streams/stream-visual/visual-settings", `{"background_mode":null}`, cookie, csrf)
	if missing.Code != http.StatusBadRequest || repository.updateCalls != 0 {
		t.Fatalf("missing revision status=%d calls=%d body=%s", missing.Code, repository.updateCalls, missing.Body.String())
	}
	explicitClear := serveUserJSON(t, handler, http.MethodPut, "/streams/stream-visual/visual-settings", `{"expected_revision":5,"background_mode":null}`, cookie, csrf)
	if explicitClear.Code != http.StatusOK || repository.updateCalls != 1 || repository.lastUpdate.ExpectedRevision != 5 || !repository.lastUpdate.BackgroundMode.Set || repository.lastUpdate.BackgroundMode.Valid {
		t.Fatalf("explicit clear status=%d calls=%d update=%#v body=%s", explicitClear.Code, repository.updateCalls, repository.lastUpdate, explicitClear.Body.String())
	}
}

func TestStreamVisualSettingsHTTPSerializesWithStreamStartLifecycleFence(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "lifecycle-editor", Username: "lifecycle-editor"}, "correct horse battery", []string{"streams.read", "streams.update", "streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Lifecycle-fenced stream")
	if err != nil {
		t.Fatal(err)
	}
	repository := &lifecycleBlockingVisualRepository{
		fixedVisualRepository: fixedVisualRepository{settings: streamvisual.Settings{StreamID: stream.ID, BackgroundMode: "default", HeaderTitleMode: "default", CoverSource: "none", Revision: 2}},
		entered:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	handler := NewServer(streams, WithAuthStore(auth), WithStreamVisualRepository(repository))
	cookie, csrf := loginForTest(t, handler, "lifecycle-editor", "correct horse battery")

	type response struct {
		status int
		body   string
	}
	updateDone := make(chan response, 1)
	go func() {
		result := serveUserJSON(t, handler, http.MethodPut, "/streams/"+stream.ID+"/visual-settings", `{"expected_revision":2,"header_title_mode":"default"}`, cookie, csrf)
		updateDone <- response{status: result.Code, body: result.Body.String()}
	}()
	select {
	case <-repository.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("visual settings update did not reach the repository")
	}

	startDone := make(chan response, 1)
	go func() {
		result := serveUserJSON(t, handler, http.MethodPost, "/streams/"+stream.ID+"/start", `{}`, cookie, csrf)
		startDone <- response{status: result.Code, body: result.Body.String()}
	}()
	select {
	case result := <-startDone:
		t.Fatalf("stream start bypassed the visual settings lifecycle fence: status=%d body=%s", result.status, result.body)
	case <-time.After(100 * time.Millisecond):
	}

	close(repository.release)
	select {
	case result := <-updateDone:
		if result.status != http.StatusOK {
			t.Fatalf("visual update status=%d body=%s", result.status, result.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("visual update did not resume after lifecycle fence release")
	}
	select {
	case result := <-startDone:
		if result.status != http.StatusConflict || !strings.Contains(result.body, "missing_stream_assignments") {
			t.Fatalf("stream start status=%d body=%s", result.status, result.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream start did not resume after lifecycle fence release")
	}
}

func TestStreamStartAndReadinessRequireCoverDispatcherEvenWhenEncoderAdvertisesCapability(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "cover-starter", Username: "cover-starter"}, "correct horse battery", []string{"streams.start"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Cover stream")
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.NewMemoryProfileStore()
	discord := createDiscordConfigForTest(t, profiles, "cover discord", "discord_bot-cover", "", "", "")
	if _, err = streams.UpdateStreamSettings(t.Context(), stream.ID, store.StreamSettings{DiscordConfigID: discord.ID}); err != nil {
		t.Fatal(err)
	}
	createRegisteredAssignedService(t, auth, "discord_bot-cover", "discord_bot", stream.ID)
	createRegisteredAssignedService(t, auth, "worker-cover", "worker", stream.ID)
	encoderToken, err := auth.CreateServiceToken(t.Context(), "encoder_recorder", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, encoderToken, store.ServiceRegistration{ServiceID: "encoder-cover", ServiceType: "encoder_recorder", ServiceName: "encoder-cover", PublicURL: "https://encoder-cover.example.com", Capabilities: map[string]any{"live_video_cover_v1": true}})
	if _, err = auth.AssignServiceToStreamWithRole(t.Context(), "encoder-cover", stream.ID, "test-user", "primary"); err != nil {
		t.Fatal(err)
	}
	visual := fixedVisualRepository{
		settings: streamvisual.Settings{StreamID: stream.ID, BackgroundMode: "default", HeaderTitleMode: "default", DiscordTargetMode: "manual", DiscordSnapshotRevision: 1, DiscordGuildID: "1234567890", DiscordTextChannelID: "3456789012", DiscordVoiceChannelID: "2345678901", CoverSource: "upload", CoverAssetID: "asset-cover", CoverVariantID: "variant-cover", Revision: 1},
		assets:   streamvisual.AssetReadiness{CoverVariantReady: true, MediaAssetIntegrity: true},
	}
	dispatcher := &fakeServiceDispatcher{}
	handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithProfileStore(profiles), WithStreamVisualRepository(visual), WithServiceDispatcher(dispatcher))
	cookie, csrf := loginForTest(t, handler, "cover-starter", "correct horse battery")
	readiness := serveUserJSON(t, handler, http.MethodPost, "/streams/"+stream.ID+"/start-readiness", `{}`, cookie, csrf)
	if readiness.Code != http.StatusOK || !strings.Contains(readiness.Body.String(), "video_cover_action_ready") || strings.Contains(readiness.Body.String(), "video_cover_capability") {
		t.Fatalf("readiness=%d %s", readiness.Code, readiness.Body.String())
	}
	start := serveUserJSON(t, handler, http.MethodPost, "/streams/"+stream.ID+"/start", `{}`, cookie, csrf)
	if start.Code != http.StatusConflict || !strings.Contains(start.Body.String(), "video_cover_action_ready") || dispatcher.startCalls != 0 {
		t.Fatalf("start=%d calls=%d body=%s", start.Code, dispatcher.startCalls, start.Body.String())
	}
}
