package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

// The payload is produced by the same two production builders used by
// StreamSlotForm. Real authenticated handlers and SQL repositories own every
// create/claim/assignment below; no mocked HTTP success is accepted as proof.
func TestMariaDBP2UIStreamCreatePayloadPersistsAtomically(t *testing.T) {
	st := preparationClaimStores(t, true)
	password := outputSecretRandom(t)
	user, err := st.auth.CreateFirstAdmin(t.Context(), "p2-ui-admin", password, security.DefaultPermissions)
	if err != nil {
		t.Fatal(err)
	}
	media, err := mediaassets.NewMariaDBRepository(st.db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	visual := streamvisual.NewMariaDBRepository(st.db, media)
	targets := store.NewMariaDBDiscordTargetPresetStore(st.db)
	handler := NewServer(st.streams, WithAuthStore(st.auth), WithServiceRegistryStore(st.services), WithAuditStore(st.audit), WithProfileStore(st.profiles), WithSecretStore(st.secrets), WithRuntimeSecretLeaseStore(st.leases), WithMediaAssetRepository(media), WithStreamVisualRepository(visual), WithDiscordTargetPresetStore(targets), WithVideoCoverRepository(videocover.NewMariaDBRepository(st.db)))
	cookie, csrf := loginForTest(t, handler, "p2-ui-admin", password)
	target, err := targets.CreateDiscordTargetPreset(t.Context(), store.DiscordTargetPreset{Name: "P2 target", GuildID: "1001", TextChannelID: "1002", VoiceChannelID: "1003", CreatedByUserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	var maximumName int
	if err = st.db.QueryRowContext(t.Context(), "SELECT CHARACTER_MAXIMUM_LENGTH FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='stream_video_cover_candidates' AND COLUMN_NAME='display_name'").Scan(&maximumName); err != nil || maximumName != 255 {
		t.Fatal("candidate filename migration did not expose the 255-character contract")
	}
	for _, scenario := range []struct {
		name       string
		covers     int
		auto       bool
		target     string
		background bool
		mixed      bool
	}{
		{"zero inherit optional profiles empty", 0, false, "inherit", false, false},
		{"one manual automatic", 1, true, "manual", false, false},
		{"two preset automatic background", 2, true, "preset", true, false},
		{"four manual scheduled", 4, false, "manual", false, false},
		{"upload plus shared preset and selected profile", 1, false, "manual", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			values := map[string]any{"name": "UI " + scenario.name, "autoStartFromDiscord": scenario.auto, "discordConfigID": "", "youtubeOutputID": "", "archiveProfileID": "", "encoderProfileID": "", "captionProfileID": "", "watermarkEnabled": false, "overlayProfileID": "", "encoderAudioGainDB": 3.5}
			for field, kind := range map[string]string{"encoderServiceID": "encoder_recorder", "workerServiceID": "worker"} {
				values[field] = p2RegisterSQLService(t, st.services, kind)
			}
			if scenario.auto {
				botID := p2RegisterSQLService(t, st.services, "discord_bot")
				secret, err := st.secrets.UpdateSecret(t.Context(), "discord_bot_token_"+outputSecretRandom(t)[:12], outputSecretRandom(t))
				if err != nil {
					t.Fatal(err)
				}
				profile, err := st.profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "P2 selected authentication "+scenario.name, map[string]any{"service_id": botID, "bot_token_secret_name": secret.Name, "bot_token_fingerprint": secret.Fingerprint})
				if err != nil {
					t.Fatal(err)
				}
				values["discordConfigID"] = profile.ID
			}
			draft := map[string]any{"discordTargetMode": scenario.target}
			if scenario.target == "manual" {
				draft["discordGuildID"], draft["discordTextChannelID"], draft["discordVoiceChannelID"] = "1001", "1002", "1003"
			} else if scenario.target == "preset" {
				draft["discordTargetPresetID"], draft["discordTargetPresetRevision"] = target.ID, target.Revision
			}
			var session mediaassets.UploadSession
			var assets []mediaassets.Asset
			if scenario.covers > 0 || scenario.background {
				session, err = media.CreateUploadSession(t.Context(), user.ID, time.Now().UTC().Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				draft["uploadSessionID"] = session.ID
			}
			candidates := []streamvisual.CoverCandidate{}
			for index := 0; index < scenario.covers; index++ {
				name := strings.Repeat("長", 140) + ".png"
				asset, variant := p2UploadSQLImage(t, media, user.ID, session.ID, "video_cover", name)
				assets = append(assets, asset)
				candidates = append(candidates, streamvisual.CoverCandidate{Name: name, Source: "upload", AssetID: asset.ID, VariantID: variant.ID})
			}
			if scenario.mixed {
				presetSession, err := media.CreateUploadSession(t.Context(), user.ID, time.Now().UTC().Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				asset, variant := p2UploadSQLImage(t, media, user.ID, presetSession.ID, "video_cover", "shared.png")
				raw, _ := json.Marshal(map[string]any{"name": "Shared cover", "asset_id": asset.ID, "asset_variant_id": variant.ID, "enabled": true})
				response := serveUserJSON(t, handler, http.MethodPost, "/video-cover-presets", string(raw), cookie, csrf)
				var preset videocover.Preset
				if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &preset) != nil {
					t.Fatal("shared preset fixture was not created by its normal handler")
				}
				candidates = append(candidates, streamvisual.CoverCandidate{Name: preset.Name, Source: "preset", PresetID: preset.ID, PresetRevision: preset.Revision, AssetID: preset.AssetID, VariantID: preset.AssetVariantID})
				profile, err := st.profiles.CreateProfile(t.Context(), store.ProfileEncoder, "Selected encoding profile", map[string]any{})
				if err != nil {
					t.Fatal(err)
				}
				values["encoderProfileID"] = profile.ID
			}
			if len(candidates) > 0 {
				selected := candidates[len(candidates)-1]
				draft["coverSource"], draft["coverAssetID"], draft["coverVariantID"], draft["coverCandidates"] = selected.Source, selected.AssetID, selected.VariantID, candidates
				if selected.Source == "preset" {
					draft["coverPresetID"] = selected.PresetID
				}
			}
			if scenario.background {
				asset, variant := p2UploadSQLImage(t, media, user.ID, session.ID, "scene_background", "background.png")
				assets = append(assets, asset)
				draft["backgroundMode"], draft["backgroundAssetID"], draft["backgroundVariantID"] = "image", asset.ID, variant.ID
			}
			if scenario.covers == 4 {
				values["scheduledStartAt"] = "2030-07-10T01:00:00Z"
			}
			payload := p2GenerateUIPayload(t, values, draft)
			before, err := st.streams.ListStreams(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			response := serveUserJSON(t, handler, http.MethodPost, "/streams", string(payload), cookie, csrf)
			if response.Code != http.StatusCreated {
				t.Fatalf("real UI payload rejected: status=%d code=%s", response.Code, response.Body.String())
			}
			var created store.Stream
			if err = json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			persisted, err := st.streams.GetStream(t.Context(), created.ID)
			if err != nil || created.SettingsRevision == "" || created.SettingsRevision != persisted.SettingsRevision {
				t.Fatal("created response settings revision does not match persisted stream")
			}
			after, err := st.streams.ListStreams(t.Context())
			if err != nil || len(after) != len(before)+1 {
				t.Fatal("create did not persist exactly one stream")
			}
			saved, err := visual.Get(t.Context(), created.ID)
			if err != nil || len(saved.CoverCandidates) != len(candidates) {
				t.Fatal("visual candidate persistence mismatch")
			}
			for _, candidate := range saved.CoverCandidates {
				if candidate.Source == "upload" && candidate.Name != strings.Repeat("長", 140)+".png" {
					t.Fatal("full original filename was truncated or lost")
				}
			}
			if len(candidates) > 0 && saved.CoverVariantID != candidates[len(candidates)-1].VariantID {
				t.Fatal("start image did not preserve UI selection")
			}
			if scenario.background && saved.BackgroundMode != "image" {
				t.Fatal("background selection was lost")
			}
			assignments, err := st.services.ListStreamAssignments(t.Context(), created.ID)
			want := 2
			if scenario.auto {
				want = 3
			}
			if err != nil || len(assignments) != want {
				t.Fatal("atomic primary reservation missing")
			}
			for _, assignment := range assignments {
				service, err := st.services.GetService(t.Context(), assignment.ServiceID)
				if err != nil || assignment.AssignmentRole != "primary" || assignment.UpdatedAt.IsZero() || service.CurrentStreamID != created.ID {
					t.Fatal("primary epoch/current owner mismatch")
				}
			}
			for _, asset := range assets {
				savedAsset, err := media.GetAsset(t.Context(), user.ID, asset.ID)
				if err != nil || savedAsset.OwnerType != "stream" || savedAsset.OwnerID != created.ID {
					t.Fatal("upload was not claimed to the created stream")
				}
			}
			if session.ID != "" {
				var owner, claimed string
				if err = st.db.QueryRowContext(t.Context(), "SELECT owner_type,claimed_stream_id FROM media_upload_sessions WHERE id=?", session.ID).Scan(&owner, &claimed); err != nil || owner != "stream" || claimed != created.ID {
					t.Fatal("session claim did not commit with the stream")
				}
			}
			duplicate := serveUserJSON(t, handler, http.MethodPost, "/streams", string(payload), cookie, csrf)
			final, err := st.streams.ListStreams(t.Context())
			if duplicate.Code != http.StatusConflict || err != nil || len(final) != len(after) {
				t.Fatal("occupied duplicate created partial state")
			}
		})
	}
	for _, scenario := range []struct {
		name          string
		expired       bool
		oversizedName bool
		status        int
		code          string
	}{
		{"expired upload", true, false, 409, "asset_claim_failed"},
		{"oversized candidate after claim", false, true, 400, "invalid_visual_settings"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			encoder, worker := p2RegisterSQLService(t, st.services, "encoder_recorder"), p2RegisterSQLService(t, st.services, "worker")
			session, err := media.CreateUploadSession(t.Context(), user.ID, time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			asset, variant := p2UploadSQLImage(t, media, user.ID, session.ID, "video_cover", "rejected.png")
			name := "rejected.png"
			if scenario.oversizedName {
				name = strings.Repeat("a", 256)
			}
			if scenario.expired {
				if _, err = st.db.ExecContext(t.Context(), "UPDATE media_upload_sessions SET expires_at=? WHERE id=?", time.Now().UTC().Add(-time.Minute), session.ID); err != nil {
					t.Fatal(err)
				}
			}
			values := map[string]any{"name": "Rejected UI input", "autoStartFromDiscord": false, "discordConfigID": "", "youtubeOutputID": "", "archiveProfileID": "", "encoderProfileID": "", "captionProfileID": "", "watermarkEnabled": false, "overlayProfileID": "", "encoderAudioGainDB": 0, "encoderServiceID": encoder, "workerServiceID": worker}
			draft := map[string]any{"coverSource": "upload", "coverAssetID": asset.ID, "coverVariantID": variant.ID, "coverCandidates": []streamvisual.CoverCandidate{{Name: name, Source: "upload", AssetID: asset.ID, VariantID: variant.ID}}, "uploadSessionID": session.ID}
			payload := p2GenerateUIPayload(t, values, draft)
			before, err := st.streams.ListStreams(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			response := serveUserJSON(t, handler, http.MethodPost, "/streams", string(payload), cookie, csrf)
			after, err := st.streams.ListStreams(t.Context())
			if response.Code != scenario.status || !strings.Contains(response.Body.String(), `"code":"`+scenario.code+`"`) || err != nil || len(after) != len(before) {
				t.Fatal("known rejection changed stream count or classification")
			}
			for _, id := range []string{encoder, worker} {
				service, err := st.services.GetService(t.Context(), id)
				if err != nil || service.CurrentStreamID != "" {
					t.Fatal("known rejection retained a primary reservation")
				}
			}
			saved, err := media.GetAsset(t.Context(), user.ID, asset.ID)
			if err != nil || saved.OwnerType != "upload_draft" || saved.OwnerID != session.ID {
				t.Fatal("known rejection retained a partial asset claim")
			}
			var owner string
			if err = st.db.QueryRowContext(t.Context(), "SELECT owner_type FROM media_upload_sessions WHERE id=?", session.ID).Scan(&owner); err != nil || owner != "upload_draft" {
				t.Fatal("known rejection retained a partial session claim")
			}
		})
	}
}

func p2RegisterSQLService(t *testing.T, services store.ServiceRegistryStore, kind string) string {
	t.Helper()
	id := "p2-" + kind + "-" + outputSecretRandom(t)[:12]
	token, err := services.CreateServiceToken(t.Context(), kind, []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: id, PublicURL: "https://" + id + ".example.com", Version: "p2", Capabilities: map[string]any{"output_relay_mode": "direct"}}
	if _, err = services.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err = services.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	return id
}

func p2UploadSQLImage(t *testing.T, media *mediaassets.SQLRepository, userID, sessionID, usage, name string) (mediaassets.Asset, mediaassets.Variant) {
	t.Helper()
	asset, err := media.Upload(t.Context(), mediaassets.UploadInput{SessionID: sessionID, UserID: userID, UsageType: usage, Filename: name, ContentType: "image/png", Body: bytes.NewReader(testVideoCoverPNG(t))})
	if err != nil {
		t.Fatal(err)
	}
	variant, err := media.EnsureVariant(t.Context(), userID, asset.ID, 1920, 1080, true)
	if err != nil {
		t.Fatal(err)
	}
	return asset, variant
}

func p2GenerateUIPayload(t *testing.T, values, draft map[string]any) []byte {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("fixture source path unavailable")
	}
	node := os.Getenv("AUTOSTREAM_TEST_NODE")
	if node == "" {
		var err error
		node, err = exec.LookPath("node")
		if err != nil {
			t.Fatal("Node is required for the production UI payload witness")
		}
	}
	input, err := json.Marshal(map[string]any{"values": values, "visual": draft})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--no-warnings", filepath.Join(filepath.Dir(source), "..", "..", "web", "tests", "helpers", "stream-create-ui-payload.mts"))
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	payload, err := command.Output()
	if err != nil {
		t.Fatalf("production UI payload generator failed: %v %s", err, stderr.String())
	}
	if !json.Valid(payload) {
		t.Fatal("UI generator returned invalid JSON")
	}
	if directory := os.Getenv("AUTOSTREAM_CREATE_PAYLOAD_EVIDENCE_DIR"); directory != "" {
		if err = os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, outputSecretRandom(t)[:12]+".json"), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return payload
}
