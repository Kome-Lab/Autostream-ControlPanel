package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/videocover"
)

// All original correlations/frames below belong to an explicit synthetic owner.
// They are never a production resolver or generated from the candidate output.
type iuWS2Resolver struct {
	binding LifecycleOutcomeBinding
	source  string
}

func (f iuWS2Resolver) ResolveStop(_ context.Context, s store.NormalStopObservation) (LifecycleOutcomeBinding, error) {
	if s.SourceVersion != f.source {
		return LifecycleOutcomeBinding{}, errors.New("original receipt correlation missing")
	}
	return f.binding, nil
}
func (f iuWS2Resolver) ResolveCover(_ context.Context, s videocover.State) (LifecycleOutcomeBinding, error) {
	return f.binding, nil
}

func iuWS2Binding(t *testing.T, streamID, source, encoderID string, cover bool) LifecycleOutcomeBinding {
	t.Helper()
	owner := lifecycle.ID("test-cp-owner")
	target := lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: owner, ResourceID: lifecycle.ID(streamID)}
	frame := lifecycle.SourceFrame{FrameRef: "test-cp-read-frame", OwnerID: owner, Scope: lifecycle.ID(streamID), SourceRevision: 3}
	p := lifecycle.ComparisonProfile{Ref: lifecycle.ProfileRef{ProfileID: lifecycle.StopLookup, Revision: 1}, ResourceKinds: []lifecycle.ResourceKind{lifecycle.Stream}, Purposes: []lifecycle.Purpose{lifecycle.PurposeLookup}, TerminalPolicyRef: "test-terminal-policy", LimitsRef: "test-explicit-limits", Limits: lifecycle.Limits{MaxIDBytes: 192, MaxMembers: 32, MaxAxes: 32, MaxRefs: 64, MaxObligations: 16, MaxPartitions: 16, MaxRanges: 16, MaxDepth: 16}}
	vector := lifecycle.RevisionVector{}
	addLiteral := func(axis lifecycle.Axis, value string) {
		v := lifecycle.ID(value)
		k := lifecycle.AxisKey{Axis: axis, OwnerID: owner, Resource: target}
		p.AxisRules = append(p.AxisRules, lifecycle.AxisRule{Key: k, OwnerRole: "C01", ValueKind: lifecycle.PresentLiteral, Required: true, Comparison: "EXACT"})
		vector = append(vector, lifecycle.RevisionEntry{Key: k, Incarnation: "test-stream-incarnation", Value: lifecycle.VersionedValue{Kind: lifecycle.PresentLiteral, Literal: &v}, SourceFrameRef: frame.FrameRef})
	}
	addNumber := func(axis lifecycle.Axis, value uint64) {
		k := lifecycle.AxisKey{Axis: axis, OwnerID: owner, Resource: target}
		p.AxisRules = append(p.AxisRules, lifecycle.AxisRule{Key: k, OwnerRole: "C01", ValueKind: lifecycle.PresentUint64, Required: true, Comparison: "EXACT"})
		vector = append(vector, lifecycle.RevisionEntry{Key: k, Incarnation: "test-stream-incarnation", Value: lifecycle.VersionedValue{Kind: lifecycle.PresentUint64, Uint64: &value}, SourceFrameRef: frame.FrameRef})
	}
	providerTarget := lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: "test-provider", ResourceID: lifecycle.ID(streamID)}
	backendTarget := lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: lifecycle.ID(encoderID), ResourceID: lifecycle.ID(streamID)}
	resources := []lifecycle.ResourceRef{target, providerTarget}
	boundaries := []lifecycle.BoundaryAddress{{Kind: lifecycle.CPLifecycle, OwnerID: owner, Resource: target, SubBoundary: lifecycle.ReceiptSave}, {Kind: lifecycle.Provider, OwnerID: "test-provider", Resource: providerTarget, SubBoundary: lifecycle.RemoteResult}}
	frames := []lifecycle.SourceFrame{frame}
	purpose, effectID := lifecycle.PurposeLookup, lifecycle.ID("test-original-stop")
	if cover {
		p.Ref.ProfileID = lifecycle.EffectOutcome
		p.Purposes = []lifecycle.Purpose{lifecycle.PurposeEffect}
		purpose, effectID = lifecycle.PurposeEffect, "test-cover-key"
		addNumber(lifecycle.Membership, 1)
		addNumber(lifecycle.Effect, 1)
		addLiteral(lifecycle.Policy, "test-policy")
		addLiteral(lifecycle.Trust, "test-trust")
		addNumber(lifecycle.JobAxis, 7)
		addNumber(lifecycle.Cover, 2)
		resources = append(resources, backendTarget)
		boundaries = []lifecycle.BoundaryAddress{{Kind: lifecycle.LocalPersistence, OwnerID: owner, Resource: target, SubBoundary: lifecycle.LocalDBCommit}, {Kind: lifecycle.BackendMutation, OwnerID: lifecycle.ID(encoderID), Resource: backendTarget, SubBoundary: lifecycle.ServiceApply}, {Kind: lifecycle.Provider, OwnerID: "test-provider", Resource: providerTarget, SubBoundary: lifecycle.RemoteResult}}
		frames = append(frames, lifecycle.SourceFrame{FrameRef: "test-backend-frame", OwnerID: lifecycle.ID(encoderID), Scope: lifecycle.ID(streamID), SourceRevision: 4})
	} else {
		addLiteral(lifecycle.RunAxis, source)
	}
	p.RequiredBoundaries = boundaries
	cr := lifecycle.ContractRef{ContractID: lifecycle.CC07, LogicalRevision: 1, SpecRef: lifecycle.SpecRef, ModuleCommitRef: "d32ea3514b1c1010391caae0348b0ff9e751f447"}
	corr := lifecycle.Correlation{RequestID: "test-original-request", OperationID: "test-original-operation", AttemptID: "test-original-attempt", StepID: "test-original-step", ExecutionKey: "test-original-execution"}
	header := lifecycle.Header{ContractRef: cr, RecordKey: lifecycle.RecordKey{ContractRef: cr, OwnerID: owner, RecordID: "test-original-intent"}, Correlation: corr, DomainRef: lifecycle.DomainRef{DeploymentID: "test-deployment", Realm: "test-realm", DomainID: "test-domain", MembershipRevision: 1, SnapshotRef: "test-membership", Resources: resources}, ProducerRef: lifecycle.ProducerRef{ComponentID: "C01", ServiceID: owner, HostID: "test-host", BootID: "test-boot", ProcessIncarnation: "test-process", WriterID: "test-writer"}, RecordRevision: 3, ProfileRef: p.Ref}
	original := lifecycle.Record{Header: header, Effect: &lifecycle.EffectRecord{EffectID: effectID, ParentEffect: lifecycle.OptionalRef{State: lifecycle.Absent, AbsenceFactRef: "test-no-parent", Observation: &lifecycle.ObservationContext{OwnerID: owner, FrameRef: frame.FrameRef, Scope: frame.Scope, SourceRevision: frame.SourceRevision}}, Target: target, Purpose: purpose, ContentRef: lifecycle.ContentRef{Kind: "NONSECRET_METADATA_REF", Ref: "test-original-metadata"}, SourceVector: vector, IntentRevision: 1, SinkRefs: []lifecycle.ResourceRef{target}}}
	if result := lifecycle.ValidateRecord(original, p); result.Kind != lifecycle.StructurallyCoherent {
		t.Fatalf("fixed input invalid: %+v", result)
	}
	binding := LifecycleOutcomeBinding{Original: original, Profile: p, Query: lifecycle.QueryTarget{Correlation: corr, BindingRef: "test-original-binding", ExecutionKey: corr.ExecutionKey, TargetRecordRef: header.RecordKey.RecordID, Scope: lifecycle.ID(streamID), Purpose: lifecycle.PurposeLookup}, Frames: frames, Gate: lifecycle.VerifiedForProfile}
	binding.OriginalTarget = lifecycle.QueryTarget{Correlation: corr, BindingRef: "test-original-binding", ExecutionKey: corr.ExecutionKey, TargetRecordRef: "test-original-intent", Scope: lifecycle.ID(streamID), Purpose: lifecycle.PurposeLookup}
	// Independent copies of the fixed input owner facts, never candidate output.
	for _, entry := range vector {
		current := entry
		if entry.Value.Uint64 != nil {
			value := *entry.Value.Uint64
			current.Value.Uint64 = &value
		}
		if entry.Value.Literal != nil {
			value := *entry.Value.Literal
			current.Value.Literal = &value
		}
		binding.CurrentVector = append(binding.CurrentVector, current)
	}
	if cover {
		binding.CoverExpected = &videocover.State{StreamID: streamID, JobGeneration: 7, DesiredRevision: 2, DesiredActive: true, AssetVariantID: "test-variant", LastIdempotencyKey: "test-cover-key"}
	}
	return binding
}

type iuWS2CoverObserver struct {
	result           servicecall.VideoCoverDispatchResult
	entered, release chan struct{}
	dispatches       atomic.Int32
	observations     atomic.Int32
}

func (d *iuWS2CoverObserver) DispatchVideoCover(context.Context, store.RegisteredService, servicecall.VideoCoverDispatchRequest) servicecall.VideoCoverDispatchResult {
	d.dispatches.Add(1)
	return servicecall.VideoCoverDispatchResult{Ambiguous: true}
}
func (d *iuWS2CoverObserver) ReconcileVideoCover(ctx context.Context, _ store.RegisteredService, _ servicecall.VideoCoverReconcileRequest) servicecall.VideoCoverDispatchResult {
	d.observations.Add(1)
	if d.entered != nil {
		select {
		case d.entered <- struct{}{}:
		case <-ctx.Done():
			return servicecall.VideoCoverDispatchResult{Ambiguous: true}
		}
		select {
		case <-d.release:
		case <-ctx.Done():
			return servicecall.VideoCoverDispatchResult{Ambiguous: true}
		}
	}
	return d.result
}

type iuWS2HTTPFixture struct {
	auth     *store.MemoryAuthStore
	streams  *store.MemoryStreamStore
	covers   *videocover.MemoryRepository
	stream   store.Stream
	observer *iuWS2CoverObserver
	server   *httptest.Server
	cookie   *http.Cookie
	user     store.User
}

func iuWS2Fixture(t *testing.T, result servicecall.VideoCoverDispatchResult, block bool) *iuWS2HTTPFixture {
	t.Helper()
	f := &iuWS2HTTPFixture{auth: store.NewMemoryAuthStore(), streams: store.NewMemoryStreamStore(), covers: videocover.NewMemoryRepository(), observer: &iuWS2CoverObserver{result: result}}
	if err := f.auth.AddUser(store.User{Username: "test-reader"}, "synthetic-only-password", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	var err error
	f.user, err = f.auth.FindUserByUsername(t.Context(), "test-reader")
	if err != nil {
		t.Fatal(err)
	}
	f.stream, err = f.streams.CreateStream(t.Context(), "test-cover-stream")
	if err != nil {
		t.Fatal(err)
	}
	token := createRegisteredAssignedService(t, f.auth, "test-ws2-encoder", "encoder_recorder", f.stream.ID)
	if _, err = f.auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "test-ws2-encoder", Status: "healthy", CurrentStreamID: f.stream.ID, Capabilities: map[string]any{"live_video_cover_v1": true}}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.covers.EnsureGeneration(t.Context(), f.stream.ID, 7, "test-variant", false); err != nil {
		t.Fatal(err)
	}
	request := videocover.ActionRequest{Active: true, ExpectedJobGeneration: 7, ExpectedRevision: 1, IdempotencyKey: "test-cover-key"}
	if _, err = f.covers.PrepareAction(t.Context(), f.stream.ID, request); err != nil {
		t.Fatal(err)
	}
	if _, err = f.covers.RecordAmbiguous(t.Context(), f.stream.ID, 7, request.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	if block {
		f.observer.entered = make(chan struct{}, 2)
		f.observer.release = make(chan struct{})
	}
	binding := iuWS2Binding(t, f.stream.ID, "", "test-ws2-encoder", true)
	h := NewServer(f.streams, WithAuthStore(f.auth), WithAuditStore(f.auth), WithServiceRegistryStore(f.auth), WithVideoCoverRepository(f.covers), WithVideoCoverDispatcher(f.observer), WithLifecycleOutcomeBridge(&LifecycleOutcomeBridge{Resolver: iuWS2Resolver{binding: binding}}))
	f.cookie, _ = loginForTest(t, h, "test-reader", "synthetic-only-password")
	f.server = httptest.NewServer(h)
	t.Cleanup(f.server.Close)
	return f
}

func iuWS2Get(t *testing.T, server *httptest.Server, cookie *http.Cookie, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}
func iuWS2ReadInspection(t *testing.T, res *http.Response, p lifecycle.ComparisonProfile) lifecycle.Record {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(res.Header.Get(lifecycleOutcomeHeader))
	if err != nil || len(raw) == 0 {
		t.Fatalf("typed CC08 missing: %s", res.Header.Get(lifecycleResultHeader))
	}
	var record lifecycle.Record
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Header.ContractRef.ContractID != lifecycle.CC08 || record.Inspection == nil {
		t.Fatal("not direct shared CC08")
	}
	validation := lifecycle.ValidateRecord(record, p)
	if validation.Kind != lifecycle.Unknown {
		t.Fatalf("provider unknown lost: %+v", validation)
	}
	if validation.ProductionQualified() || validation.AuthorizesMutation() {
		t.Fatal("inspection became authority")
	}
	return record
}

func TestIUWS2StopLookupHTTPBoundaries(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "test-reader"}, "synthetic-only-password", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "test-stop-stream")
	if err != nil {
		t.Fatal(err)
	}
	source := stream.UpdatedAt.UTC().Format(time.RFC3339Nano)
	binding := iuWS2Binding(t, stream.ID, source, "", false)
	h := NewServer(streams, WithAuthStore(auth), WithLifecycleOutcomeBridge(&LifecycleOutcomeBridge{Resolver: iuWS2Resolver{binding: binding, source: source}}))
	cookie, _ := loginForTest(t, h, "test-reader", "synthetic-only-password")
	server := httptest.NewServer(h)
	defer server.Close()
	if _, err = streams.UpdateStreamStatus(t.Context(), stream.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	path := "/streams/" + stream.ID + "/stop-outcome?lifecycle_outcome=1&expected_updated_at=" + url.QueryEscape(source)
	res, body := iuWS2Get(t, server, cookie, path)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"stop_confirmed":false`) {
		t.Fatalf("completed status substituted for receipt: %d %s", res.StatusCode, body)
	}
	record := iuWS2ReadInspection(t, res, binding.Profile)
	if record.Inspection.Observations.Observations[0].Knowledge != lifecycle.KnowledgeUnknown {
		t.Fatal("missing receipt became no-effect/success")
	}
	if err = streams.RecordNormalStopReceipt(t.Context(), stream.ID, source, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	res, body = iuWS2Get(t, server, cookie, path)
	record = iuWS2ReadInspection(t, res, binding.Profile)
	if !strings.Contains(string(body), `"stop_confirmed":true`) || record.Inspection.Observations.Observations[0].Knowledge != lifecycle.Known || record.Header.Correlation != binding.Original.Header.Correlation {
		t.Fatal("exact receipt/correlation not retained")
	}
	res, body = iuWS2Get(t, server, cookie, "/streams/"+stream.ID+"/stop-outcome?lifecycle_outcome=1&expected_updated_at="+url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)))
	if res.StatusCode != 200 || !strings.Contains(string(body), `"stop_confirmed":false`) || res.Header.Get(lifecycleOutcomeHeader) != "" || res.Header.Get(lifecycleResultHeader) != "NEEDS_EVIDENCE" {
		t.Fatal("wrong version used original receipt")
	}
	res, _ = iuWS2Get(t, server, cookie, "/streams/wrong-stream/stop-outcome?expected_updated_at="+url.QueryEscape(source))
	if res.StatusCode != 404 {
		t.Fatal("wrong stream accepted")
	}
}

func TestIUWS2CoverHTTPConvergenceHumanAudit(t *testing.T) {
	for _, outcome := range []string{"applied", "failed", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			result := servicecall.VideoCoverDispatchResult{Applied: outcome == "applied", Ambiguous: outcome == "unknown"}
			if outcome == "failed" {
				result.SafeErrorCode = "cover_graph_unavailable"
			}
			f := iuWS2Fixture(t, result, false)
			res, _ := iuWS2Get(t, f.server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state?lifecycle_outcome=1")
			if res.StatusCode != 200 {
				t.Fatalf("status %d", res.StatusCode)
			}
			binding := iuWS2Binding(t, f.stream.ID, "", "test-ws2-encoder", true)
			record := iuWS2ReadInspection(t, res, binding.Profile)
			state, err := f.covers.GetCurrentState(t.Context(), f.stream.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := outcome
			if outcome == "unknown" {
				want = "confirming"
			}
			if state.Status != want || f.observer.dispatches.Load() != 0 {
				t.Fatal("bad convergence or redispatch")
			}
			count := 0
			for _, event := range f.auth.AuditEvents() {
				if event.Action == "streams.video_cover.show" {
					count++
					if event.ActorUserID != f.user.ID || event.ActorUsername != f.user.Username || event.Metadata["desired_revision"] != uint64(2) {
						t.Fatalf("Human audit changed: %+v", event)
					}
				}
			}
			if outcome == "unknown" {
				if count != 0 || record.Inspection.Observations.Observations[0].Knowledge != lifecycle.KnowledgeUnknown {
					t.Fatal("unknown fabricated commit")
				}
			} else if count != 1 || record.Inspection.Observations.Observations[0].Knowledge != lifecycle.Known {
				t.Fatal("critical local commit missing")
			}
			_, _ = iuWS2Get(t, f.server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state")
			after := 0
			for _, event := range f.auth.AuditEvents() {
				if event.Action == "streams.video_cover.show" {
					after++
				}
			}
			if after != count || f.observer.dispatches.Load() != 0 {
				t.Fatal("terminal query repeated audit or mutation")
			}
		})
	}
	t.Run("legacy-terminal-races-observation", func(t *testing.T) {
		f := iuWS2Fixture(t, servicecall.VideoCoverDispatchResult{Applied: true}, true)
		done := make(chan int, 1)
		go func() {
			res, _ := iuWS2Get(t, f.server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state")
			done <- res.StatusCode
		}()
		select {
		case <-f.observer.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("observation barrier not reached")
		}
		// Reproduce the old PUT writer resolving before its best-effort audit.
		if _, err := f.covers.RecordApplied(t.Context(), f.stream.ID, 7, "test-cover-key", true, 2); err != nil {
			t.Fatal(err)
		}
		close(f.observer.release)
		select {
		case status := <-done:
			if status != 200 {
				t.Fatalf("terminal race status %d", status)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("terminal race deadlocked")
		}
		count := 0
		for _, event := range f.auth.AuditEvents() {
			if event.Action == "streams.video_cover.show" {
				count++
				if event.ActorUserID != f.user.ID {
					t.Fatal("critical terminal audit lost Human")
				}
			}
		}
		if count != 1 || f.observer.dispatches.Load() != 0 {
			t.Fatal("terminal race skipped critical audit or resent")
		}
	})
	for _, failure := range []string{"audit-refusal", "commit-ack-loss"} {
		t.Run(failure, func(t *testing.T) {
			f := iuWS2Fixture(t, servicecall.VideoCoverDispatchResult{Applied: true}, false)
			binding := iuWS2Binding(t, f.stream.ID, "", "test-ws2-encoder", true)
			owner := iuWS2RecordFailure{AuthStore: f.auth, owner: f.auth, committed: failure == "commit-ack-loss"}
			h := NewServer(f.streams, WithAuthStore(owner), WithAuditStore(f.auth), WithServiceRegistryStore(f.auth), WithVideoCoverRepository(f.covers), WithVideoCoverDispatcher(f.observer), WithLifecycleOutcomeBridge(&LifecycleOutcomeBridge{Resolver: iuWS2Resolver{binding: binding}}))
			server := httptest.NewServer(h)
			defer server.Close()
			res, _ := iuWS2Get(t, server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state?lifecycle_outcome=1")
			want := http.StatusInternalServerError
			if failure == "commit-ack-loss" {
				want = http.StatusServiceUnavailable
			}
			if res.StatusCode != want {
				t.Fatalf("failure status %d", res.StatusCode)
			}
			record := iuWS2ReadInspection(t, res, binding.Profile)
			if record.Inspection.Observations.Observations[0].Knowledge != lifecycle.KnowledgeUnknown || record.Inspection.Observations.Observations[1].Knowledge != lifecycle.Known {
				t.Fatal("local commit knowledge mixed with backend result")
			}
			state, _ := f.covers.GetCurrentState(t.Context(), f.stream.ID)
			if failure == "audit-refusal" && state.Status != "confirming" || failure == "commit-ack-loss" && state.Status != "applied" {
				t.Fatal("refusal/unknown state collapsed")
			}
			if failure == "commit-ack-loss" {
				res, _ = iuWS2Get(t, f.server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state")
				if res.StatusCode != 200 || f.observer.observations.Load() != 1 {
					t.Fatal("unknown reissued original operation")
				}
			}
			if f.observer.dispatches.Load() != 0 {
				t.Fatal("record failure resent cover")
			}
		})
	}
}

// This is an explicit synthetic fault at the real caller/Store seam. The SQL
// root separately proves a real audit INSERT failure rolls back the same Tx.
type iuWS2RecordFailure struct {
	store.AuthStore
	owner     store.CoverOutcomeStore
	committed bool
}

func (f iuWS2RecordFailure) RecordCoverObservation(ctx context.Context, audit store.AuditStore, covers videocover.Repository, actor store.CoverRecordActor, state videocover.State, status, code string, event store.AuditEvent) (videocover.State, error) {
	if f.committed {
		if _, err := f.owner.RecordCoverObservation(ctx, audit, covers, actor, state, status, code, event); err != nil {
			return videocover.State{}, err
		}
		return videocover.State{}, lf.ErrUnknown
	}
	return videocover.State{}, errors.New("synthetic critical audit failure")
}

func TestIUWS2CoverHTTPRefusalRaces(t *testing.T) {
	for _, change := range []string{"principal", "permission", "generation", "session"} {
		t.Run(change, func(t *testing.T) {
			f := iuWS2Fixture(t, servicecall.VideoCoverDispatchResult{Applied: true}, true)
			done := make(chan int, 1)
			var permissionRole store.Role
			if change == "permission" {
				var err error
				permissionRole, err = f.auth.CreateRole(t.Context(), "test-current-reader", []string{"streams.read"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.auth.UpdateUser(t.Context(), f.user.ID, store.UserPatch{RoleIDs: []string{permissionRole.ID}}); err != nil {
					t.Fatal(err)
				}
				h := NewServer(f.streams, WithAuthStore(f.auth), WithAuditStore(f.auth), WithOperationalAuthorization(f.auth), WithServiceRegistryStore(f.auth), WithVideoCoverRepository(f.covers), WithVideoCoverDispatcher(f.observer))
				f.server = httptest.NewServer(h)
				t.Cleanup(f.server.Close)
			}
			go func() {
				res, _ := iuWS2Get(t, f.server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state")
				done <- res.StatusCode
			}()
			select {
			case <-f.observer.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("observation barrier not reached")
			}
			switch change {
			case "principal":
				if _, err := f.auth.SetUserStatus(t.Context(), f.user.ID, "disabled"); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if _, err := f.auth.UpdateRole(t.Context(), permissionRole.ID, permissionRole.Name, nil); err != nil {
					t.Fatal(err)
				}
			case "generation":
				if _, err := f.covers.EnsureGeneration(t.Context(), f.stream.ID, 8, "test-next-variant", false); err != nil {
					t.Fatal(err)
				}
			case "session":
				if err := f.auth.DeleteSession(t.Context(), f.cookie.Value); err != nil {
					t.Fatal(err)
				}
			}
			close(f.observer.release)
			select {
			case status := <-done:
				if status != 403 && status != 409 {
					t.Fatalf("race status %d", status)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("record blocked")
			}
			state, err := f.covers.GetCurrentState(t.Context(), f.stream.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "applied" || f.observer.dispatches.Load() != 0 {
				t.Fatal("refusal mutated target or dispatched")
			}
			for _, event := range f.auth.AuditEvents() {
				if event.Action == "streams.video_cover.show" {
					t.Fatal("refusal emitted committed audit")
				}
			}
		})
	}
}

func TestIUWS2CoverOutcomeBindingRefusals(t *testing.T) {
	f := iuWS2Fixture(t, servicecall.VideoCoverDispatchResult{Applied: true}, false)
	base := iuWS2Binding(t, f.stream.ID, "", "test-ws2-encoder", true)
	for _, change := range []string{"stream", "generation", "revision", "correlation", "binding", "profile", "limits", "effect-key", "same-key-content", "backend-owner", "missing-axis", "held"} {
		t.Run(change, func(t *testing.T) {
			body, _ := json.Marshal(base)
			var b LifecycleOutcomeBinding
			_ = json.Unmarshal(body, &b)
			b.CoverExpected.LastIdempotencyKey = base.CoverExpected.LastIdempotencyKey
			if change == "missing-axis" {
				b.CurrentVector = nil
			}
			if change == "held" {
				b.Gate = lifecycle.Held
			}
			switch change {
			case "stream":
				b.Original.Effect.Target.ResourceID = "other-stream"
			case "generation":
				for i := range b.Original.Effect.SourceVector {
					if b.Original.Effect.SourceVector[i].Key.Axis == lifecycle.JobAxis {
						*b.Original.Effect.SourceVector[i].Value.Uint64 = 8
					}
				}
			case "revision":
				for i := range b.Original.Effect.SourceVector {
					if b.Original.Effect.SourceVector[i].Key.Axis == lifecycle.Cover {
						*b.Original.Effect.SourceVector[i].Value.Uint64 = 9
					}
				}
			case "correlation":
				b.Query.Correlation.AttemptID = "different-attempt"
			case "binding":
				b.Query.BindingRef = "different-binding"
			case "profile":
				b.Profile.Ref.ProfileID = lifecycle.StopLookup
			case "limits":
				b.Profile.Limits = lifecycle.Limits{}
			case "effect-key":
				b.Original.Effect.EffectID = "different-key"
			case "same-key-content":
				b.CoverExpected.AssetVariantID = "test-other-content"
			case "backend-owner":
				for i := range b.Profile.RequiredBoundaries {
					if b.Profile.RequiredBoundaries[i].Kind == lifecycle.BackendMutation {
						b.Profile.RequiredBoundaries[i].OwnerID = "test-other-encoder"
						b.Profile.RequiredBoundaries[i].Resource.OwnerID = "test-other-encoder"
					}
				}
			}
			h := NewServer(f.streams, WithAuthStore(f.auth), WithAuditStore(f.auth), WithServiceRegistryStore(f.auth), WithVideoCoverRepository(f.covers), WithVideoCoverDispatcher(f.observer), WithLifecycleOutcomeBridge(&LifecycleOutcomeBridge{Resolver: iuWS2Resolver{binding: b}}))
			server := httptest.NewServer(h)
			defer server.Close()
			before, _ := f.covers.GetCurrentState(t.Context(), f.stream.ID)
			res, _ := iuWS2Get(t, server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state?lifecycle_outcome=1")
			after, _ := f.covers.GetCurrentState(t.Context(), f.stream.ID)
			if res.StatusCode != 409 || res.Header.Get(lifecycleOutcomeHeader) != "" || res.Header.Get(lifecycleResultHeader) != "NEEDS_EVIDENCE" || f.observer.dispatches.Load() != 0 || f.observer.observations.Load() != 0 || after.Status != before.Status || after.DesiredRevision != before.DesiredRevision || after.AssetVariantID != before.AssetVariantID {
				t.Fatal("unbound query observed/recorded/dispatched or changed target")
			}
		})
	}
}

func TestIUWS2CoverHTTPAckLossNoResend(t *testing.T) {
	f := iuWS2Fixture(t, servicecall.VideoCoverDispatchResult{Applied: true}, false)
	// The first real HTTP client loses the response after the server commits.
	transport := &iuWS2LoseResponse{base: f.server.Client().Transport}
	client := &http.Client{Transport: transport}
	req, _ := http.NewRequest(http.MethodGet, f.server.URL+"/streams/"+f.stream.ID+"/video-cover-state", nil)
	req.AddCookie(f.cookie)
	if _, err := client.Do(req); err == nil {
		t.Fatal("lost ACK not injected")
	}
	res, _ := iuWS2Get(t, f.server, f.cookie, "/streams/"+f.stream.ID+"/video-cover-state")
	if res.StatusCode != 200 || f.observer.dispatches.Load() != 0 || f.observer.observations.Load() != 1 {
		t.Fatal("query resent after response loss")
	}
	count := 0
	for _, event := range f.auth.AuditEvents() {
		if event.Action == "streams.video_cover.show" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("commit after ACK loss not durable exactly once")
	}
}

type iuWS2LoseResponse struct{ base http.RoundTripper }

func (t *iuWS2LoseResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return nil, errors.New("synthetic response ACK loss")
}
