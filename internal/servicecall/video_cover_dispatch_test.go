package servicecall

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestDispatchVideoCoverGETsFreshGenerationThenAppliesExactlyOnce(t *testing.T) {
	descriptor := testCoverDescriptor()
	initial := testVideoCoverRuntime(false, 1, "", nil)
	applied := testVideoCoverRuntime(true, 2, descriptor.VariantID, &descriptor)
	var getCalls, putCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cover-token" {
			t.Fatalf("authorization header missing")
		}
		switch r.Method {
		case http.MethodGet:
			getCalls++
			_ = json.NewEncoder(w).Encode(initial)
		case http.MethodPut:
			putCalls++
			var request EncoderVideoCoverApplyRequest
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.StreamID != "stream-1" || request.JobGeneration != 9 || request.ExpectedGeneration != 4 || request.Revision != 2 || !request.Active || request.CoverAsset == nil || request.CoverAsset.VariantID != descriptor.VariantID {
				t.Fatalf("apply request was not bound to fresh state: %#v", request)
			}
			_ = json.NewEncoder(w).Encode(EncoderVideoCoverApplyResponse{
				StreamID: "stream-1", JobGeneration: 9, RequestedRevision: 2, ActualGeneration: 4,
				Accepted: true, Applied: true, Outcome: "applied", Actual: applied,
			})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	client := testVideoCoverClient(server.Client(), server.URL)
	service := store.RegisteredService{ServiceID: "encoder-1", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
	result := client.DispatchVideoCover(t.Context(), service, VideoCoverDispatchRequest{
		StreamID: "stream-1", JobGeneration: 9, Revision: 2, Active: true,
		AssetVariantID: descriptor.VariantID, IdempotencyKey: "show-2", CoverAsset: &descriptor,
	})
	if !result.Applied || result.Ambiguous || result.SafeErrorCode != "" || getCalls != 1 || putCalls != 1 {
		t.Fatalf("result=%#v GET=%d PUT=%d", result, getCalls, putCalls)
	}
}

func TestDispatchVideoCoverNeverRetriesAmbiguousPUT(t *testing.T) {
	initialBody, err := json.Marshal(testVideoCoverRuntime(false, 1, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	var getCalls, putCalls int
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			getCalls++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(initialBody)), Header: make(http.Header)}, nil
		case http.MethodPut:
			putCalls++
			return nil, errors.New("injected response loss")
		default:
			return nil, errors.New("unexpected method")
		}
	})}
	client := testVideoCoverClient(httpClient, "http://encoder.example.test")
	service := store.RegisteredService{ServiceID: "encoder-1", ServiceType: "encoder_recorder", PublicURL: "http://encoder.example.test", ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
	result := client.DispatchVideoCover(t.Context(), service, VideoCoverDispatchRequest{
		StreamID: "stream-1", JobGeneration: 9, Revision: 2, Active: false,
		AssetVariantID: "variant-cover-1", IdempotencyKey: "hide-2", HideConfirmed: true,
	})
	if !result.Ambiguous || result.Applied || result.SafeErrorCode != "" || getCalls != 1 || putCalls != 1 {
		t.Fatalf("result=%#v GET=%d PUT=%d", result, getCalls, putCalls)
	}
}

func TestDispatchVideoCoverMalformedSemanticResponseIsAmbiguous(t *testing.T) {
	initial := testVideoCoverRuntime(false, 1, "", nil)
	rejected := EncoderVideoCoverApplyResponse{
		StreamID: "stream-1", JobGeneration: 9, RequestedRevision: 2, ActualGeneration: 4,
		Rejected: true, Outcome: "rejected", Actual: initial, Error: &VisualSafeError{Code: "stale_cover_revision"},
	}
	var malformed map[string]any
	encoded, _ := json.Marshal(rejected)
	_ = json.Unmarshal(encoded, &malformed)
	delete(malformed, "accepted")
	var putCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(initial)
			return
		}
		putCalls++
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(malformed)
	}))
	defer server.Close()
	client := testVideoCoverClient(server.Client(), server.URL)
	service := store.RegisteredService{ServiceID: "encoder-1", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
	result := client.DispatchVideoCover(t.Context(), service, VideoCoverDispatchRequest{
		StreamID: "stream-1", JobGeneration: 9, Revision: 2, Active: false,
		AssetVariantID: "variant-cover-1", IdempotencyKey: "hide-2", HideConfirmed: true,
	})
	if !result.Ambiguous || result.Applied || result.SafeErrorCode != "" || putCalls != 1 {
		t.Fatalf("malformed response was trusted: result=%#v PUT=%d", result, putCalls)
	}
}

func TestVideoCoverResponseRejectsForbiddenNullPresence(t *testing.T) {
	request := EncoderVideoCoverApplyRequest{
		StreamID: "stream-1", JobGeneration: 9, ExpectedGeneration: 4, Revision: 2,
		Active: false, IdempotencyKey: "hide-2", HideConfirmed: true,
	}
	response := EncoderVideoCoverApplyResponse{
		StreamID: "stream-1", JobGeneration: 9, RequestedRevision: 2, ActualGeneration: 4,
		Accepted: true, Applied: true, Outcome: "applied", Actual: testVideoCoverRuntime(false, 2, "", nil),
	}
	canonical, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !validateVideoCoverApplyResponse(canonical, http.StatusOK, request, response) {
		t.Fatal("canonical response was rejected")
	}

	mutations := map[string]func(map[string]any){
		"applied_error": func(root map[string]any) { root["error"] = nil },
		"ready_error": func(root map[string]any) {
			root["actual"].(map[string]any)["error"] = nil
		},
		"inactive_cover_asset": func(root map[string]any) {
			root["actual"].(map[string]any)["cover_asset"] = nil
		},
		"inactive_desired_variant": func(root map[string]any) {
			root["actual"].(map[string]any)["desired"].(map[string]any)["variant_id"] = nil
		},
		"known_inactive_applied_variant": func(root map[string]any) {
			root["actual"].(map[string]any)["applied"].(map[string]any)["variant_id"] = nil
		},
		"disabled_cover_variant": func(root map[string]any) {
			root["actual"].(map[string]any)["cover"].(map[string]any)["variant_id"] = nil
		},
		"disabled_witness_watermark_variant": func(root map[string]any) {
			root["actual"].(map[string]any)["applied_witness"].(map[string]any)["watermark"].(map[string]any)["variant_id"] = nil
		},
		"optional_last_good": func(root map[string]any) {
			root["actual"].(map[string]any)["last_good_applied"] = nil
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(canonical, &document); err != nil {
				t.Fatal(err)
			}
			mutate(document)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var decoded EncoderVideoCoverApplyResponse
			if !decodeStrictSingleJSON(raw, &decoded) {
				t.Fatal("presence mutant did not reach semantic validation")
			}
			if validateVideoCoverApplyResponse(raw, http.StatusOK, request, decoded) {
				t.Fatal("forbidden explicit null was accepted")
			}
		})
	}

	if validateVisualSafeErrorShape(
		[]byte(`{"code":"stale_cover_revision","request_id":null}`),
		VisualSafeError{Code: "stale_cover_revision"},
	) {
		t.Fatal("explicit null safe-error request_id was accepted")
	}

	unknown := testVideoCoverRuntime(false, 2, "", nil)
	lastGood := unknown.Applied
	unknown.Readiness = VisualReadinessUnknown
	unknown.Applied = VideoCoverAppliedState{State: "unknown"}
	unknown.AppliedWitness = nil
	unknown.LastGoodApplied = &lastGood
	unknown.Error = &VisualSafeError{Code: "cover_apply_ambiguous"}
	unknownRaw, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if !validateVideoCoverRuntimeState(unknownRaw, unknown) {
		t.Fatal("canonical unknown runtime was rejected")
	}
	var unknownDocument map[string]any
	if err := json.Unmarshal(unknownRaw, &unknownDocument); err != nil {
		t.Fatal(err)
	}
	unknownDocument["applied_witness"] = nil
	unknownRaw, err = json.Marshal(unknownDocument)
	if err != nil {
		t.Fatal(err)
	}
	var decodedUnknown VideoCoverRuntimeState
	if !decodeStrictSingleJSON(unknownRaw, &decodedUnknown) {
		t.Fatal("unknown witness null mutant did not reach semantic validation")
	}
	if validateVideoCoverRuntimeState(unknownRaw, decodedUnknown) {
		t.Fatal("unknown runtime accepted explicitly present applied_witness:null")
	}
}

func TestVideoCoverAmbiguousResponseRequiresActualErrorLinkage(t *testing.T) {
	request := EncoderVideoCoverApplyRequest{
		StreamID: "stream-1", JobGeneration: 9, ExpectedGeneration: 4, Revision: 2,
		Active: false, IdempotencyKey: "hide-2", HideConfirmed: true,
	}
	actual := testVideoCoverRuntime(false, 2, "", nil)
	lastGood := actual.Applied
	actual.Readiness = VisualReadinessUnknown
	actual.Applied = VideoCoverAppliedState{State: "unknown"}
	actual.AppliedWitness = nil
	actual.LastGoodApplied = &lastGood
	actual.Error = &VisualSafeError{Code: "cover_apply_ambiguous"}
	response := EncoderVideoCoverApplyResponse{
		StreamID: "stream-1", JobGeneration: 9, RequestedRevision: 2, ActualGeneration: 4,
		Accepted: true, Outcome: "ambiguous", Actual: actual,
		Error: &VisualSafeError{Code: "cover_apply_ambiguous"},
	}
	canonical, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !validateVideoCoverApplyResponse(canonical, http.StatusAccepted, request, response) {
		t.Fatal("canonical ambiguous response was rejected")
	}
	response.Actual.Error = &VisualSafeError{Code: "capability_required"}
	mismatched, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if validateVideoCoverApplyResponse(mismatched, http.StatusAccepted, request, response) {
		t.Fatal("ambiguous response with mismatched actual error was accepted")
	}
}

func TestDispatchVideoCoverRejectsAppliedStateNotBoundToFreshRequest(t *testing.T) {
	requestedDescriptor := testCoverDescriptor()
	tests := []struct {
		name     string
		response func() EncoderVideoCoverApplyResponse
	}{
		{
			name: "different_generation",
			response: func() EncoderVideoCoverApplyResponse {
				actual := testVideoCoverRuntime(true, 2, requestedDescriptor.VariantID, &requestedDescriptor)
				actual.Generation = 5
				actual.AppliedWitness.Generation = 5
				return EncoderVideoCoverApplyResponse{
					StreamID: "stream-1", JobGeneration: 9, RequestedRevision: 2, ActualGeneration: 5,
					Accepted: true, Applied: true, Outcome: "applied", Actual: actual,
				}
			},
		},
		{
			name: "different_asset_identity",
			response: func() EncoderVideoCoverApplyResponse {
				differentDescriptor := testCoverDescriptor()
				differentDescriptor.AssetID = "asset-cover-other"
				differentDescriptor.VariantID = "variant-cover-other"
				differentDescriptor.SHA256 = strings.Repeat("b", 64)
				actual := testVideoCoverRuntime(true, 2, differentDescriptor.VariantID, &differentDescriptor)
				return EncoderVideoCoverApplyResponse{
					StreamID: "stream-1", JobGeneration: 9, RequestedRevision: 2, ActualGeneration: 4,
					Accepted: true, Applied: true, Outcome: "applied", Actual: actual,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := testVideoCoverRuntime(false, 1, "", nil)
			response := test.response()
			var getCalls, putCalls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					getCalls++
					_ = json.NewEncoder(w).Encode(initial)
					return
				}
				putCalls++
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			client := testVideoCoverClient(server.Client(), server.URL)
			service := store.RegisteredService{ServiceID: "encoder-1", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
			result := client.DispatchVideoCover(t.Context(), service, VideoCoverDispatchRequest{
				StreamID: "stream-1", JobGeneration: 9, Revision: 2, Active: true,
				AssetVariantID: requestedDescriptor.VariantID, IdempotencyKey: "show-2", CoverAsset: &requestedDescriptor,
			})
			if !result.Ambiguous || result.Applied || result.SafeErrorCode != "" || getCalls != 1 || putCalls != 1 {
				t.Fatalf("unbound applied response was trusted: result=%#v GET=%d PUT=%d", result, getCalls, putCalls)
			}
		})
	}
}

func TestDispatchVideoCoverTreatsCanonicalUnavailablePUTAsCapabilityFailure(t *testing.T) {
	initial := testVideoCoverRuntime(false, 1, "", nil)
	var putCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(initial)
			return
		}
		putCalls++
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"capability_required"}`)
	}))
	defer server.Close()
	client := testVideoCoverClient(server.Client(), server.URL)
	service := store.RegisteredService{ServiceID: "encoder-1", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
	result := client.DispatchVideoCover(t.Context(), service, VideoCoverDispatchRequest{
		StreamID: "stream-1", JobGeneration: 9, Revision: 2, Active: false,
		AssetVariantID: "variant-cover-1", IdempotencyKey: "hide-2", HideConfirmed: true,
	})
	if result.Applied || result.Ambiguous || result.SafeErrorCode != "capability_required" || putCalls != 1 {
		t.Fatalf("unavailable result=%#v PUT=%d", result, putCalls)
	}
}

func TestVideoCoverUnavailableResponseRejectsNonCanonicalJSON(t *testing.T) {
	fixtures := map[string][]byte{
		"wrong_code":    []byte(`{"code":"cover_graph_unavailable"}`),
		"unknown_field": []byte(`{"code":"capability_required","job_generation":9}`),
		"duplicate":     []byte(`{"code":"capability_required","code":"capability_required"}`),
		"null":          []byte(`{"code":null}`),
		"trailing":      []byte(`{"code":"capability_required"}{}`),
		"invalid_utf8":  append([]byte(`{"code":"capability_required","x":"`), 0xff, '"', '}'),
	}
	if !decodeVideoCoverUnavailableResponse([]byte(`{"code":"capability_required"}`)) {
		t.Fatal("canonical unavailable response was rejected")
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			if decodeVideoCoverUnavailableResponse(fixture) {
				t.Fatalf("non-canonical unavailable response accepted: %q", fixture)
			}
		})
	}
}

func TestDispatchVideoCoverAcceptsAuthoritativeStaleJobResponse(t *testing.T) {
	initial := testVideoCoverRuntime(false, 1, "", nil)
	actual := testVideoCoverRuntime(false, 1, "", nil)
	actual.JobGeneration = 10
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(initial)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(EncoderVideoCoverApplyResponse{
			StreamID: "stream-1", JobGeneration: 10, RequestedRevision: 2, ActualGeneration: 4,
			Rejected: true, Outcome: "rejected", Actual: actual, Error: &VisualSafeError{Code: "stale_job_generation"},
		})
	}))
	defer server.Close()
	client := testVideoCoverClient(server.Client(), server.URL)
	service := store.RegisteredService{ServiceID: "encoder-1", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
	result := client.DispatchVideoCover(t.Context(), service, VideoCoverDispatchRequest{
		StreamID: "stream-1", JobGeneration: 9, Revision: 2, Active: false,
		AssetVariantID: "variant-cover-1", IdempotencyKey: "hide-2", HideConfirmed: true,
	})
	if result.Applied || result.Ambiguous || result.SafeErrorCode != "stale_job_generation" {
		t.Fatalf("stale job result=%#v", result)
	}
}

func TestReconcileVideoCoverReadsActualStateWithoutPUT(t *testing.T) {
	actual := testVideoCoverRuntime(false, 2, "", nil)
	var getCalls, putCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			_ = json.NewEncoder(w).Encode(actual)
		case http.MethodPut:
			putCalls++
			t.Fatalf("reconciliation must not issue PUT")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	client := testVideoCoverClient(server.Client(), server.URL)
	service := store.RegisteredService{ServiceID: "encoder-1", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
	result := client.ReconcileVideoCover(t.Context(), service, VideoCoverReconcileRequest{
		StreamID: "stream-1", JobGeneration: 9, Revision: 2, Active: false, AssetVariantID: "variant-cover-1",
	})
	if !result.Applied || result.Ambiguous || result.SafeErrorCode != "" || getCalls != 1 || putCalls != 0 {
		t.Fatalf("result=%#v GET=%d PUT=%d", result, getCalls, putCalls)
	}
}

func testVideoCoverClient(httpClient *http.Client, rawURL string) Client {
	host := strings.TrimPrefix(rawURL, "http://")
	host = strings.TrimPrefix(host, "https://")
	if index := strings.IndexByte(host, ':'); index >= 0 {
		host = host[:index]
	}
	return Client{Config: Config{
		Timeout:   0,
		URLPolicy: netpolicy.ServiceURLPolicy{AllowedHosts: map[string]struct{}{host: {}}},
	}, HTTP: httpClient, RuntimeTokenResolver: func(store.RegisteredService) (string, error) { return "cover-token", nil }}
}

func testCoverDescriptor() MediaAssetDescriptor {
	opaque := true
	aspect := 0
	return MediaAssetDescriptor{
		AssetID: "asset-cover-1", VariantID: "variant-cover-1", Usage: "video_cover", MediaType: "image/png",
		Width: 1920, Height: 1080, ByteSize: 1024, PixelCount: 1920 * 1080, Animated: false,
		AspectRatioErrorPPM: &aspect, Opaque: &opaque, SHA256: strings.Repeat("a", 64), Revision: 1, Readiness: VisualReadinessReady,
	}
}

func testVideoCoverRuntime(active bool, revision uint64, variantID string, descriptor *MediaAssetDescriptor) VideoCoverRuntimeState {
	knownActive := active
	layer := VideoVisualLayerState{Enabled: active, Revision: revision, VariantID: variantID}
	watermark := VideoVisualLayerState{Revision: 1}
	pipeline := VisualPipelineInvariant{
		Layers:           []string{"base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"},
		WatermarkTopmost: true, CoverWatermarkIndependent: true, OutputParity: []string{"live", "archive", "preview"},
	}
	source := "none"
	if active {
		source = "upload"
	}
	return VideoCoverRuntimeState{
		StreamID: "stream-1", JobGeneration: 9, Generation: 4, Capability: CapabilityLiveVideoCoverV1, Readiness: VisualReadinessReady,
		Desired: VideoCoverDesiredState{Active: active, Revision: revision, Source: source, VariantID: variantID},
		Applied: VideoCoverAppliedState{State: "known", Active: &knownActive, Revision: revision, VariantID: variantID},
		Cover:   layer, CoverAsset: descriptor, Watermark: watermark, Pipeline: pipeline,
		AppliedWitness:    &VideoCoverAppliedWitness{GraphApplied: true, Generation: 4, Revision: revision, Active: active, Cover: layer, Watermark: watermark, Pipeline: pipeline},
		NoAutomaticResend: true,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
