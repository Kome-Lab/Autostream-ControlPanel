package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// One isolated SQL schema exercises the production repositories and authenticated
// handlers. Discord and service dispatch are deliberately not part of this test.
func TestUI188MariaDBAtomicCreateCoverSnapshotsPreviewAndStopReceipt(t *testing.T) {
	st := preparationClaimStores(t, true)
	password := outputSecretRandom(t)
	user, err := st.auth.CreateFirstAdmin(t.Context(), "ui188-admin", password, security.DefaultPermissions)
	if err != nil {
		t.Fatal(err)
	}
	media, err := mediaassets.NewMariaDBRepository(st.db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	visual := streamvisual.NewMariaDBRepository(st.db, media)
	covers := videocover.NewMariaDBRepository(st.db)
	handler := NewServer(st.streams, WithAuthStore(st.auth), WithServiceRegistryStore(st.services), WithAuditStore(st.audit), WithMediaAssetRepository(media), WithStreamVisualRepository(visual), WithVideoCoverRepository(covers))
	cookie, csrf := loginForTest(t, handler, "ui188-admin", password)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return serveUserJSON(t, handler, method, path, string(raw), cookie, csrf)
	}
	presets := []videocover.Preset{}
	for _, name := range []string{"A", "B"} {
		session, err := media.CreateUploadSession(t.Context(), user.ID, time.Now().UTC().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		asset, err := media.Upload(t.Context(), mediaassets.UploadInput{SessionID: session.ID, UserID: user.ID, UsageType: "video_cover", Filename: name + ".png", ContentType: "image/png", Body: bytes.NewReader(testVideoCoverPNG(t))})
		if err != nil {
			t.Fatal(err)
		}
		variant, err := media.EnsureVariant(t.Context(), user.ID, asset.ID, 1920, 1080, true)
		if err != nil {
			t.Fatal(err)
		}
		res := request(http.MethodPost, "/video-cover-presets", map[string]any{"name": name, "asset_id": asset.ID, "asset_variant_id": variant.ID, "enabled": true})
		if res.Code != 201 {
			t.Fatalf("preset SQL status=%d %s", res.Code, res.Body.String())
		}
		var preset videocover.Preset
		if err = json.Unmarshal(res.Body.Bytes(), &preset); err != nil {
			t.Fatal(err)
		}
		presets = append(presets, preset)
		preview := request(http.MethodGet, "/video-cover-presets/"+preset.ID+"/media-assets/"+variant.ID, nil)
		if preview.Code != 200 || preview.Body.Len() == 0 || preview.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("saved preset preview %d", preview.Code)
		}
	}
	candidates := []streamvisual.CoverCandidate{}
	for _, preset := range presets {
		candidates = append(candidates, streamvisual.CoverCandidate{Name: preset.Name, Source: "preset", PresetID: preset.ID, PresetRevision: preset.Revision, AssetID: preset.AssetID, VariantID: preset.AssetVariantID})
	}
	payload := ui188CreatePayload(t, handler, `{"name":"SQL UI188","visual_settings":{}}`)
	var body map[string]any
	_ = json.Unmarshal([]byte(payload), &body)
	body["visual_settings"] = map[string]any{"cover_source": "preset", "cover_preset_id": presets[0].ID, "cover_variant_id": presets[0].AssetVariantID, "cover_asset_id": presets[0].AssetID, "cover_candidates": candidates}
	created := request(http.MethodPost, "/streams", body)
	if created.Code != 201 {
		t.Fatalf("atomic create %d %s", created.Code, created.Body.String())
	}
	var stream store.Stream
	_ = json.Unmarshal(created.Body.Bytes(), &stream)
	assignments, err := st.services.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil || len(assignments) != 2 || stream.AssignedEncoderID == "" || stream.AssignedWorkerID == "" {
		t.Fatal("SQL atomic primary reservation missing")
	}
	saved, err := visual.Get(t.Context(), stream.ID)
	if err != nil || len(saved.CoverCandidates) != 2 {
		t.Fatalf("candidate SQL persistence: %v", err)
	}
	// Reusing a busy pair and a rejected visual mutation cannot persist partial rows.
	before, _ := st.streams.ListStreams(t.Context())
	occupied := request(http.MethodPost, "/streams", body)
	after, _ := st.streams.ListStreams(t.Context())
	if occupied.Code != 409 || len(before) != len(after) {
		t.Fatal("occupied create left partial stream")
	}
	if _, err = covers.DeletePreset(t.Context(), presets[0].ID, user.ID, 1); err != nil {
		t.Fatal(err)
	}
	rename := candidates
	rename[0].Name = "A snapshot retained"
	res := request(http.MethodPut, "/streams/"+stream.ID+"/visual-settings", map[string]any{"expected_revision": saved.Revision, "cover_candidates": rename})
	if res.Code != 200 {
		t.Fatalf("deleted preset snapshot compatibility %d %s", res.Code, res.Body.String())
	}
	saved, err = visual.Get(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview := request(http.MethodGet, "/streams/"+stream.ID+"/media-assets/"+presets[0].AssetVariantID, nil)
	if preview.Code != 200 {
		t.Fatal("deleted preset snapshot lost authorized image")
	}
	if _, err = covers.EnsureGeneration(t.Context(), stream.ID, 1, presets[0].AssetVariantID, false); err != nil {
		t.Fatal(err)
	}
	prepared, err := covers.PrepareAction(t.Context(), stream.ID, videocover.ActionRequest{Active: true, ExpectedJobGeneration: 1, ExpectedRevision: 1, IdempotencyKey: "ui188-switch-B", CandidateVariantID: presets[1].AssetVariantID, ExpectedVisualRevision: saved.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RequestedAssetVariantID != presets[1].AssetVariantID {
		t.Fatal("switch did not snapshot requested image")
	}
	if _, err = covers.RecordAmbiguous(t.Context(), stream.ID, 1, "ui188-switch-B"); err != nil {
		t.Fatal(err)
	}
	res = request(http.MethodPut, "/streams/"+stream.ID+"/visual-settings", map[string]any{"expected_revision": saved.Revision, "cover_candidates": rename[:1]})
	if res.Code != 409 {
		t.Fatalf("pending image deletion status=%d", res.Code)
	}
	latest, _ := visual.Get(t.Context(), stream.ID)
	if latest.Revision != saved.Revision || len(latest.CoverCandidates) != 2 {
		t.Fatal("rejected removal changed visual data")
	}
	// Completed status and exact-source receipt commit together, and survive rearm.
	source := stream.UpdatedAt.Format(time.RFC3339Nano)
	if stream, err = st.streams.UpdateStreamStatus(t.Context(), stream.ID, "stopping"); err != nil {
		t.Fatal(err)
	}
	receipts := st.streams.(store.NormalStopReceiptStore)
	completed, transitioned, err := receipts.CompleteNormalStop(t.Context(), stream.ID, source, stream.UpdatedAt)
	if err != nil || !transitioned || completed.Status != "completed" {
		t.Fatalf("atomic terminal receipt %v", err)
	}
	if found, err := receipts.HasNormalStopReceipt(t.Context(), stream.ID, source); err != nil || !found {
		t.Fatal("durable stop receipt missing")
	}
	if _, err = st.streams.UpdateStreamStatus(t.Context(), stream.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	res = request(http.MethodGet, "/streams/"+stream.ID+"/stop-outcome?expected_updated_at="+source, nil)
	var outcome struct {
		Confirmed bool `json:"stop_confirmed"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &outcome)
	if res.Code != 200 || !outcome.Confirmed {
		t.Fatal("exact stop receipt read failed after rearm")
	}
	// A force stop without a preceding normal stop must hold the same durable
	// ownership fence during dispatch, and old completions cannot clear it.
	live, err := st.streams.UpdateStreamStatus(t.Context(), stream.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	force, claimed, err := receipts.ClaimForceStop(t.Context(), live)
	if err != nil || !claimed {
		t.Fatal("force claim failed")
	}
	if _, claimed, err = receipts.ClaimForceStop(t.Context(), live); err != nil || claimed {
		t.Fatal("same source force claim dispatched twice")
	}
	if res = request(http.MethodDelete, "/streams/"+stream.ID, nil); res.Code != 409 {
		t.Fatalf("unconfirmed force stop permitted deletion: %d", res.Code)
	}
	if _, ok, err := receipts.CompleteNormalStop(t.Context(), stream.ID, source, stream.UpdatedAt); err != nil || ok {
		t.Fatal("old normal completion changed force claim")
	}
	if _, ok, err := receipts.CompleteConfirmedForceStop(t.Context(), stream.ID, force.UpdatedAt); err != nil || !ok {
		t.Fatal("confirmed force stop did not release fence")
	}
	live, err = st.streams.UpdateStreamStatus(t.Context(), stream.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	normal, claimed, err := receipts.ClaimNormalStop(t.Context(), live)
	if err != nil || !claimed {
		t.Fatal("new normal claim failed")
	}
	if _, ok, err := receipts.CompleteConfirmedForceStop(t.Context(), stream.ID, force.UpdatedAt); err != nil || ok {
		t.Fatal("old force completion cleared new normal fence")
	}
	if _, ok, err := receipts.FailNormalStop(t.Context(), stream.ID, stream.UpdatedAt); err != nil || ok {
		t.Fatal("old failure changed new normal claim")
	}
	if res = request(http.MethodDelete, "/streams/"+stream.ID, nil); res.Code != 409 {
		t.Fatal("new normal fence was lost")
	}
	if _, ok, err := receipts.CompleteNormalStop(t.Context(), stream.ID, live.UpdatedAt.Format(time.RFC3339Nano), normal.UpdatedAt); err != nil || !ok {
		t.Fatal("current stop failed")
	}
	latest, _ = visual.Get(t.Context(), stream.ID)
	res = request(http.MethodPut, "/streams/"+stream.ID+"/visual-settings", map[string]any{"expected_revision": latest.Revision, "cover_candidates": rename[:1]})
	if res.Code != 200 {
		t.Fatalf("retired runtime prevented safe candidate edit: %d %s", res.Code, res.Body.String())
	}
	res = request(http.MethodDelete, "/streams/"+stream.ID, nil)
	if res.Code != 200 && res.Code != 204 {
		t.Fatalf("confirmed stopped stream could not be deleted: %d %s", res.Code, res.Body.String())
	}
	for _, assignment := range assignments {
		service, err := st.services.GetService(t.Context(), assignment.ServiceID)
		if err != nil || service.CurrentStreamID != "" {
			t.Fatal("deleted stream retained primary owner")
		}
	}
	if _, err = covers.GetPreset(t.Context(), presets[1].ID, false); err != nil {
		t.Fatal("deleting stream deleted another preset")
	}
}
