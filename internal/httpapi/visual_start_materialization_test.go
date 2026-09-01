package httpapi

import (
	"bytes"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

func TestMaterializeVisualStartSnapshotUsesOneGenerationAndSafeDescriptors(t *testing.T) {
	media, err := mediaassets.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	streamID := "stream-visual-1"
	sceneAsset, sceneVariant := createVisualStartAsset(t, media, "owner-1", streamID, "scene_background", 854, 480, false)
	coverAsset, coverVariant := createVisualStartAsset(t, media, "owner-1", streamID, "video_cover", 1920, 1080, true)
	settings := streamvisual.Settings{
		StreamID: streamID, Revision: 7,
		BackgroundMode: "image", BackgroundAssetID: sceneAsset.ID, BackgroundVariantID: sceneVariant.ID,
		HeaderTitleMode: "custom", HeaderTitleValue: "Bounded title",
		DiscordTargetMode: "manual", DiscordSnapshotRevision: 11,
		DiscordGuildID: "1001", DiscordTextChannelID: "1002", DiscordVoiceChannelID: "1003",
		CoverSource: "upload", CoverAssetID: coverAsset.ID, CoverVariantID: coverVariant.ID, CoverStartActive: true,
	}
	server := &Server{streamVisual: fixedVisualRepository{settings: settings}, mediaAssets: media}
	request := servicecall.StartRequest{DiscordGuildID: "legacy-guild", DiscordTextChannelID: "legacy-text", DiscordVoiceChannelID: "legacy-voice"}
	assignments := []store.RegisteredService{
		{ServiceType: "worker", ReportedCapabilities: map[string]any{servicecall.CapabilitySceneAppearanceV1: true}},
		{ServiceType: "encoder_recorder", ReportedCapabilities: map[string]any{servicecall.CapabilityLiveVideoCoverV1: true}},
	}
	generation := videocover.State{StreamID: streamID, JobGeneration: 4, DesiredRevision: 1, DesiredActive: true, AssetVariantID: coverVariant.ID}
	if err := server.materializeVisualStartSnapshot(t.Context(), streamID, assignments, generation, &request); err != nil {
		t.Fatal(err)
	}
	if request.DiscordTargetRevision != 11 || request.DiscordGuildID != "1001" || request.DiscordTextChannelID != "1002" || request.DiscordVoiceChannelID != "1003" {
		t.Fatalf("resolved Discord target was not materialized: %#v", request)
	}
	if request.SceneAppearance == nil || request.SceneAppearance.Generation != 4 || request.SceneAppearance.Revision != 7 || request.SceneAppearance.Background == nil ||
		request.SceneAppearance.Background.AssetID != sceneAsset.ID || request.SceneAppearance.Background.VariantID != sceneVariant.ID || request.SceneAppearance.Background.Opaque != nil || request.SceneAppearance.Background.AspectRatioErrorPPM != nil {
		t.Fatalf("scene appearance snapshot mismatch: %#v", request.SceneAppearance)
	}
	if request.VideoCoverStart == nil || request.VideoCoverStart.JobGeneration != request.SceneAppearance.Generation || request.VideoCoverStart.Revision != 1 || !request.VideoCoverStart.Active || request.VideoCoverStart.CoverAsset == nil ||
		request.VideoCoverStart.CoverAsset.AssetID != coverAsset.ID || request.VideoCoverStart.CoverAsset.VariantID != coverVariant.ID || request.VideoCoverStart.CoverAsset.Opaque == nil || !*request.VideoCoverStart.CoverAsset.Opaque ||
		request.VideoCoverStart.CoverAsset.AspectRatioErrorPPM == nil || *request.VideoCoverStart.CoverAsset.AspectRatioErrorPPM != 0 {
		t.Fatalf("video cover start snapshot mismatch: %#v", request.VideoCoverStart)
	}
	if request.SceneAppearance.Background.SHA256 == "" || request.VideoCoverStart.CoverAsset.SHA256 == "" {
		t.Fatal("immutable variant hashes were not materialized")
	}
}

func TestMaterializeVisualStartSnapshotMixedFleetFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		settings    streamvisual.Settings
		assignments []store.RegisteredService
		wantError   bool
	}{
		{
			name:        "old runtimes default remains omitted",
			settings:    streamvisual.Settings{StreamID: "stream-mixed", Revision: 1, BackgroundMode: "default", HeaderTitleMode: "default", DiscordSnapshotRevision: 1, CoverSource: "none"},
			assignments: []store.RegisteredService{{ServiceType: "worker"}, {ServiceType: "encoder_recorder"}},
		},
		{
			name:        "old worker custom scene blocked",
			settings:    streamvisual.Settings{StreamID: "stream-mixed", Revision: 2, BackgroundMode: "default", HeaderTitleMode: "custom", HeaderTitleValue: "required", DiscordSnapshotRevision: 1, CoverSource: "none"},
			assignments: []store.RegisteredService{{ServiceType: "worker"}, {ServiceType: "encoder_recorder"}}, wantError: true,
		},
		{
			name:        "old encoder configured cover blocked",
			settings:    streamvisual.Settings{StreamID: "stream-mixed", Revision: 2, BackgroundMode: "default", HeaderTitleMode: "default", DiscordSnapshotRevision: 1, CoverSource: "upload", CoverAssetID: "asset-1", CoverVariantID: "variant-1"},
			assignments: []store.RegisteredService{{ServiceType: "worker"}, {ServiceType: "encoder_recorder"}}, wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{streamVisual: fixedVisualRepository{settings: test.settings}}
			var request servicecall.StartRequest
			err := server.materializeVisualStartSnapshot(t.Context(), "stream-mixed", test.assignments, videocover.State{JobGeneration: 3, DesiredRevision: 1}, &request)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v wantError=%t", err, test.wantError)
			}
			if !test.wantError && (request.SceneAppearance != nil || request.VideoCoverStart != nil) {
				t.Fatalf("old runtimes received v2 payload: %#v", request)
			}
		})
	}
}

func createVisualStartAsset(t *testing.T, media *mediaassets.MemoryRepository, userID, streamID, usage string, width, height int, opaque bool) (mediaassets.Asset, mediaassets.Variant) {
	t.Helper()
	session, err := media.CreateUploadSession(t.Context(), userID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := media.Upload(t.Context(), mediaassets.UploadInput{SessionID: session.ID, UserID: userID, UsageType: usage, Filename: usage + ".png", ContentType: "image/png", Body: bytes.NewReader(testVideoCoverPNG(t))})
	if err != nil {
		t.Fatal(err)
	}
	variant, err := media.EnsureVariant(t.Context(), userID, asset.ID, width, height, opaque)
	if err != nil {
		t.Fatal(err)
	}
	if err := media.ClaimDraft(t.Context(), userID, session.ID, streamID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	media.ReferenceVariant(streamID, variant.ID)
	return asset, variant
}
