package servicecall

import (
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStartNegotiatesWorkerVideoIngestWithoutLeakingCredential(t *testing.T) {
	// Managed start now uses the canonical handshake fixture.
	TestStartPreparationNormalOrderAndCredentialBoundary(t)
}

func TestStartWorkerVideoCapabilityMismatchFailsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workerCaps  map[string]any
		encoderCaps map[string]any
	}{
		{name: "Worker only", workerCaps: map[string]any{"scene_frames_mjpeg_srt": true}},
		{name: "Encoder only", encoderCaps: map[string]any{"worker_frame_ingest_mjpeg_srt": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			services := []store.RegisteredService{
				{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: tc.workerCaps},
				{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: tc.encoderCaps},
			}
			client := testClient()
			client.Config.IngestTokenSigningKey = "test-ingest-signing-key"
			results := client.Start(t.Context(), store.Stream{ID: "stream-01"}, services, StartRequest{})
			if requests != 0 || len(results) != 1 || results[0].Success || results[0].Code != "worker_video_capability_mismatch" || results[0].FailurePhase != "pre_dispatch" {
				t.Fatalf("capability mismatch did not fail before dispatch: requests=%d results=%#v", requests, results)
			}
			issues := client.StartReadinessIssues(services, StartRequest{}, time.Now().UTC())
			if !hasIssueCode(issues, "worker_video_capability_mismatch") {
				t.Fatalf("readiness did not report capability mismatch: %#v", issues)
			}
		})
	}
}

func TestWorkerVideoCapabilitiesRequireExactReportedBooleans(t *testing.T) {
	services := []store.RegisteredService{
		{ServiceID: "worker-01", ServiceType: "worker", ReportedCapabilities: map[string]any{"scene_frames_mjpeg_srt": "true"}},
		{ServiceID: "enc-01", ServiceType: "encoder_recorder", ReportedCapabilities: map[string]any{"worker_frame_ingest_mjpeg_srt": "true"}},
	}
	if WorkerVideoCapabilitiesEnabled(services) {
		t.Fatalf("string capability values must not negotiate Worker video: %#v", services)
	}
	services[0].ReportedCapabilities["scene_frames_mjpeg_srt"] = true
	if _, mismatch := workerVideoCapabilityMismatch(services); !mismatch {
		t.Fatalf("one exact capability and one non-boolean capability must fail closed: %#v", services)
	}
}

func TestStartNegotiatedWorkerVideoStopsBeforeBotWhenWorkerRejectsRoute(t *testing.T) {
	h := newPreparationHarness(t, "worker_reject")
	results := h.start()
	h.assertSecretSafe(results)
	if strings.Join(h.events, ",") != "prepare,worker,abort" || !hasFailedDispatchResult(results) || !h.control.CleanupConfirmed {
		t.Fatal(h.events, results)
	}
	if h.control.BotStarted || h.control.EncoderCommitted {
		t.Fatal("prepare treated as final success")
	}
}

func TestStartRejectsSecretBearingWorkerVideoURLWithoutLeakingCredential(t *testing.T) {
	h := newPreparationHarness(t, "route_secret")
	results := h.start()
	h.assertSecretSafe(results)
	if !hasFailedDispatchResult(results) || strings.Contains(strings.Join(h.events, ","), "worker") {
		t.Fatal(h.events, results)
	}
}

func hasFailedDispatchResult(results []DispatchResult) bool {
	for _, result := range results {
		if !result.Success {
			return true
		}
	}
	return false
}
