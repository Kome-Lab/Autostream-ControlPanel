package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
)

const streamJSONFramingSentinel = "json-framing-private-value"

func TestCreateStreamJSONFraming(t *testing.T) {
	cases := []struct {
		name   string
		tail   string
		split  bool
		accept bool
	}{
		{name: "single object", accept: true},
		{name: "trailing whitespace", tail: " \t\r\n ", accept: true},
		{name: "split whitespace unknown length", tail: " \t\r\n ", split: true, accept: true},
		{name: "second object", tail: `{}`},
		{name: "legacy encoder object", tail: `{"encoder_service_id":"` + streamJSONFramingSentinel + `"}`},
		{name: "legacy worker object", tail: `{"worker_service_id":null}`},
		{name: "legacy flat discord object", tail: `{"discord_guild_id":"` + streamJSONFramingSentinel + `"}`},
		{name: "null", tail: `null`},
		{name: "array", tail: `[]`},
		{name: "string", tail: `"` + streamJSONFramingSentinel + `"`},
		{name: "number", tail: `123`},
		{name: "true", tail: `true`},
		{name: "false", tail: `false`},
		{name: "invalid data", tail: streamJSONFramingSentinel},
		{name: "unfinished object", tail: `{"name":`},
		{name: "unfinished string", tail: `"unfinished`},
		{name: "multiple extra values", tail: `{} null []`},
		{name: "split second object unknown length", tail: `{}`, split: true},
		{name: "split invalid data unknown length", tail: streamJSONFramingSentinel, split: true},
		{name: "oversized trailing whitespace", tail: strings.Repeat(" ", maxControlRequestBytes), split: true},
	}
	for _, draft := range []bool{false, true} {
		mode := "ordinary"
		if draft {
			mode = "upload draft"
		}
		t.Run(mode, func(t *testing.T) {
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					fixture := newStreamJSONFramingFixture(t, draft, "streams.create")
					before := fixture.snapshot(t)
					var body io.Reader = strings.NewReader(fixture.payload + test.tail)
					if test.split {
						// The complete first object is returned before any tail bytes.
						body = io.MultiReader(strings.NewReader(fixture.payload), strings.NewReader(test.tail))
					}
					req := httptest.NewRequest(http.MethodPost, "/streams", body)
					if test.split {
						req.ContentLength = -1
					}
					req.AddCookie(fixture.cookie)
					req.Header.Set("X-CSRF-Token", fixture.csrf)
					req.Header.Set("Content-Type", "application/json")
					res := httptest.NewRecorder()
					fixture.handler.ServeHTTP(res, req)
					if test.accept {
						fixture.assertCreatedOnce(t, before, res, draft)
					} else {
						fixture.assertRejected(t, before, res, http.StatusBadRequest, "bad_request", 0)
					}
				})
			}
		})
	}
}

func TestCreateStreamJSONFramingPreservesValidationAndMiddleware(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		replace     string
		with        string
		auth        string
		permission  string
		status      int
		code        string
		createCalls int
	}{
		{name: "legacy encoder in first object", replace: `"name":`, with: `"encoder_service_id":null,"name":`},
		{name: "legacy worker in first object", replace: `"name":`, with: `"worker_service_id":"","name":`},
		{name: "legacy discord in first object", replace: `"name":`, with: `"discord_guild_id":"` + streamJSONFramingSentinel + `","name":`},
		{name: "unknown visual field", replace: `"expected_revision":0`, with: `"expected_revision":0,"unknown":true`},
		{name: "unknown nested discord field", replace: `"mode":"inherit"`, with: `"mode":"inherit","unknown":true`},
		{name: "missing visual settings", body: `{"name":"framing stream"}`, code: "visual_settings_required"},
		{name: "null visual settings", body: `{"name":"framing stream","visual_settings":null}`, code: "visual_settings_required"},
		{name: "nonzero create revision", replace: `"expected_revision":0`, with: `"expected_revision":1`, code: "invalid_visual_settings", createCalls: 1},
		{name: "oversized first object", replace: `"name":"framing stream"`, with: `"name":"` + strings.Repeat("x", maxControlRequestBytes) + `"`},
		{name: "unauthenticated before framing", auth: "none", status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "permission denied before framing", permission: "streams.read", status: http.StatusForbidden, code: "permission_denied"},
		{name: "missing csrf before framing", auth: "missing csrf", status: http.StatusForbidden, code: "csrf_failed"},
		{name: "incorrect csrf before framing", auth: "incorrect csrf", status: http.StatusForbidden, code: "csrf_failed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			permission := test.permission
			if permission == "" {
				permission = "streams.create"
			}
			fixture := newStreamJSONFramingFixture(t, true, permission)
			before := fixture.snapshot(t)
			body := fixture.payload + `{}`
			if test.body != "" {
				body = test.body
			}
			if test.replace != "" {
				if !strings.Contains(fixture.payload, test.replace) {
					t.Fatal("validation fixture replacement did not match")
				}
				body = strings.Replace(fixture.payload, test.replace, test.with, 1)
			}
			req := httptest.NewRequest(http.MethodPost, "/streams", strings.NewReader(body))
			if test.auth != "none" {
				req.AddCookie(fixture.cookie)
			}
			if test.auth != "missing csrf" {
				req.Header.Set("X-CSRF-Token", fixture.csrf)
			}
			if test.auth == "incorrect csrf" {
				req.Header.Set("X-CSRF-Token", "incorrect")
			}
			res := httptest.NewRecorder()
			fixture.handler.ServeHTTP(res, req)
			status, code := test.status, test.code
			if status == 0 {
				status = http.StatusBadRequest
			}
			if code == "" {
				code = "bad_request"
			}
			fixture.assertRejected(t, before, res, status, code, test.createCalls)
		})
	}
}

// This observer delegates creation to the existing visual/stream memory stores.
// The memory visual repository does not claim media, so compose the existing
// media fixture's claim/reference operations at the AtomicCreator boundary.
// This exercises handler ordering and real memory state, not SQL atomicity.
type streamJSONFramingVisualRepository struct {
	*streamvisual.MemoryRepository
	media       *mediaassets.MemoryRepository
	createCalls int
}

func (r *streamJSONFramingVisualRepository) CreateStream(ctx context.Context, userID string, input streamvisual.Create) (store.Stream, streamvisual.Settings, error) {
	r.createCalls++
	stream, settings, err := r.MemoryRepository.CreateStream(ctx, userID, input)
	if err != nil || input.UploadSessionID == "" {
		return stream, settings, err
	}
	if err := r.media.ClaimDraft(ctx, userID, input.UploadSessionID, stream.ID, time.Now().UTC()); err != nil {
		return stream, settings, err
	}
	r.media.ReferenceVariant(stream.ID, settings.BackgroundVariantID)
	return stream, settings, nil
}

type streamJSONFramingFixture struct {
	handler    http.Handler
	auth       *store.MemoryAuthStore
	streams    *store.MemoryStreamStore
	visual     *streamJSONFramingVisualRepository
	media      *mediaassets.MemoryRepository
	registry   *streamIsolationServiceRegistry
	dispatcher *fakeServiceDispatcher
	cookie     *http.Cookie
	csrf       string
	payload    string
	existingID string
	session    mediaassets.UploadSession
	asset      mediaassets.Asset
	variant    mediaassets.Variant
}

func newStreamJSONFramingFixture(t *testing.T, draft bool, permission string) *streamJSONFramingFixture {
	t.Helper()
	f := &streamJSONFramingFixture{auth: store.NewMemoryAuthStore(), streams: store.NewMemoryStreamStore(), dispatcher: &fakeServiceDispatcher{}}
	if err := f.auth.AddUser(store.User{ID: "framing-user", Username: "framing-user"}, "correct horse battery", []string{permission}); err != nil {
		t.Fatal(err)
	}
	existing, err := f.streams.CreateStream(t.Context(), "existing stream")
	if err != nil {
		t.Fatal(err)
	}
	f.existingID = existing.ID
	registerAssignedServices(t, f.auth, existing.ID, "encoder_recorder", "worker")
	f.registry = &streamIsolationServiceRegistry{ServiceRegistryStore: f.auth}
	f.media, err = mediaassets.NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.session, err = f.media.CreateUploadSession(t.Context(), "framing-user", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f.asset, err = f.media.Upload(t.Context(), mediaassets.UploadInput{SessionID: f.session.ID, UserID: "framing-user", UsageType: "scene_background", Filename: "framing.png", ContentType: "image/png", Body: bytes.NewReader(testAvatarPNG(t, 32, 32))})
	if err != nil {
		t.Fatal(err)
	}
	f.variant, err = f.media.EnsureVariant(t.Context(), "framing-user", f.asset.ID, 32, 32, true)
	if err != nil {
		t.Fatal(err)
	}
	f.visual = &streamJSONFramingVisualRepository{MemoryRepository: streamvisual.NewMemoryRepository(f.streams), media: f.media}
	f.handler = NewServer(f.streams, WithAuthStore(f.auth), WithAuditStore(f.auth), WithServiceRegistryStore(f.registry), WithServiceDispatcher(f.dispatcher), WithStreamVisualRepository(f.visual), WithMediaAssetRepository(f.media))
	f.cookie, f.csrf = loginForTest(t, f.handler, "framing-user", "correct horse battery")
	visual := `"expected_revision":0,"discord_target":{"mode":"inherit"},"header_title_mode":"custom","header_title_value":"Framing title"`
	f.payload = `{"name":"framing stream","visual_settings":{` + visual + `}}`
	if draft {
		f.payload = `{"name":"framing stream","upload_session_id":"` + f.session.ID + `","visual_settings":{` + visual + `,"background_mode":"image","background_asset_id":"` + f.asset.ID + `","background_variant_id":"` + f.variant.ID + `"}}`
	}
	return f
}

type streamJSONFramingState struct {
	streams      []store.Stream
	visuals      map[string]streamvisual.Settings
	services     []store.RegisteredService
	assignments  map[string][]store.RegisteredService
	asset        mediaassets.Asset
	createAudits int
}

func (f *streamJSONFramingFixture) snapshot(t *testing.T) streamJSONFramingState {
	t.Helper()
	state := streamJSONFramingState{visuals: map[string]streamvisual.Settings{}, assignments: map[string][]store.RegisteredService{}}
	var err error
	state.streams, err = f.streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(state.streams, func(i, j int) bool { return state.streams[i].ID < state.streams[j].ID })
	for _, stream := range state.streams {
		state.visuals[stream.ID], err = f.visual.Get(t.Context(), stream.ID)
		if err != nil {
			t.Fatal(err)
		}
		state.assignments[stream.ID], err = f.auth.ListStreamAssignments(t.Context(), stream.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	state.services, err = f.auth.ListServices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(state.services, func(i, j int) bool { return state.services[i].ServiceID < state.services[j].ServiceID })
	state.asset, err = f.media.GetAsset(t.Context(), "framing-user", f.asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range f.auth.AuditEvents() {
		if event.Action == "streams.create" && event.Result == "success" {
			state.createAudits++
		}
	}
	return state
}

func (f *streamJSONFramingFixture) assertNoRuntimeChanges(t *testing.T, before, after streamJSONFramingState) {
	t.Helper()
	if !reflect.DeepEqual(before.services, after.services) || !reflect.DeepEqual(before.assignments[f.existingID], after.assignments[f.existingID]) {
		t.Error("existing Node assignment state changed")
	}
	assertNoStreamIsolationAssignmentWrites(t, f.registry)
	if !reflect.DeepEqual(f.dispatcher, &fakeServiceDispatcher{}) {
		t.Error("create request emitted an external dispatch")
	}
}

func (f *streamJSONFramingFixture) assertRejected(t *testing.T, before streamJSONFramingState, res *httptest.ResponseRecorder, status int, code string, createCalls int) {
	t.Helper()
	var response struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal("rejection did not return JSON")
	}
	if res.Code != status || response.Code != code {
		t.Errorf("status=%d code=%q, want status=%d code=%q", res.Code, response.Code, status, code)
	}
	after := f.snapshot(t)
	if !reflect.DeepEqual(before, after) {
		t.Error("rejected request changed stream, visual, media, assignment, or success-audit state")
	}
	if f.visual.createCalls != createCalls {
		t.Errorf("AtomicCreator calls=%d, want %d", f.visual.createCalls, createCalls)
	}
	f.assertNoRuntimeChanges(t, before, after)
	auditJSON, err := json.Marshal(f.auth.AuditEvents())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(res.Body.Bytes(), []byte(streamJSONFramingSentinel)) || bytes.Contains(auditJSON, []byte(streamJSONFramingSentinel)) {
		t.Error("rejection disclosed the input sentinel")
	}
	if asset, err := f.media.OpenInternalVariant(t.Context(), f.existingID, f.variant.ID); !errors.Is(err, mediaassets.ErrForbidden) {
		if asset.Reader != nil {
			_ = asset.Reader.Close()
		}
		t.Error("rejected request changed media reference binding")
	}
	// Probe reusability only after comparing the untouched state. A previously
	// consumed session cannot be claimed by this different, existing stream.
	if err := f.media.ClaimDraft(t.Context(), "framing-user", f.session.ID, f.existingID, time.Now().UTC()); err != nil {
		t.Error("rejected request consumed the upload session")
	}
}

func (f *streamJSONFramingFixture) assertCreatedOnce(t *testing.T, before streamJSONFramingState, res *httptest.ResponseRecorder, draft bool) {
	t.Helper()
	if res.Code != http.StatusCreated {
		t.Fatalf("valid create status=%d, want 201", res.Code)
	}
	var created store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	after := f.snapshot(t)
	if created.ID == "" || created.ID == f.existingID || created.Name != "framing stream" || created.Status != "created" || f.visual.createCalls != 1 || len(after.streams) != len(before.streams)+1 || len(after.visuals) != len(before.visuals)+1 || after.createAudits != before.createAudits+1 {
		t.Fatal("valid request did not create exactly one stream, visual row, and success audit")
	}
	visual := after.visuals[created.ID]
	if visual.Revision != 1 || visual.HeaderTitleMode != "custom" || visual.HeaderTitleValue != "Framing title" || visual.DiscordTargetMode != "inherit" || visual.CoverSource != "none" {
		t.Error("created visual settings differ from the submitted v2 configuration")
	}
	if !reflect.DeepEqual(before.visuals[f.existingID], after.visuals[f.existingID]) {
		t.Error("valid create changed existing visual settings")
	}
	for _, stream := range after.streams {
		if stream.ID == f.existingID && !reflect.DeepEqual(stream, before.streams[0]) {
			t.Error("valid create changed the existing stream")
		}
	}
	if created.AssignedEncoderID != "" || created.AssignedWorkerID != "" || len(after.assignments[created.ID]) != 0 {
		t.Error("valid create assigned a Node")
	}
	f.assertNoRuntimeChanges(t, before, after)
	if draft {
		if visual.BackgroundAssetID != f.asset.ID || visual.BackgroundVariantID != f.variant.ID || after.asset.OwnerType != "stream" || after.asset.OwnerID != created.ID {
			t.Error("valid draft create did not preserve its media selection and binding")
		}
		asset, err := f.media.OpenInternalVariant(t.Context(), created.ID, f.variant.ID)
		if err != nil {
			t.Fatal("created stream cannot read its bound media variant")
		}
		_ = asset.Reader.Close()
		if err := f.media.ClaimDraft(t.Context(), "framing-user", f.session.ID, f.existingID, time.Now().UTC()); !errors.Is(err, mediaassets.ErrDraftClaimed) {
			t.Error("successfully consumed upload session remained available to another stream")
		}
	} else if !reflect.DeepEqual(before.asset, after.asset) {
		t.Error("ordinary create changed an unrelated media draft")
	}
}
