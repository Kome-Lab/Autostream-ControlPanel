package servicecall

import (
	"reflect"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestStartPreparationLiveAPIRuntimeUsesEncoderContract(t *testing.T) {
	for _, selection := range []string{"fresh_stream", "configured_reusable_stream"} {
		for _, scheduled := range []bool{false, true} {
			name := selection
			if scheduled {
				name += "_scheduled"
			}
			t.Run(name, func(t *testing.T) {
				h := newPreparationHarness(t, "")
				wantEncoder := map[string]any{
					"mode":                   "live_api",
					"output_id":              "output-01",
					"oauth_account_id":       "oauth-01",
					"broadcast_id":           "abcdef12345",
					"watch_url":              "https://www.youtube.com/watch?v=abcdef12345",
					"live_stream_id":         "live-stream-01",
					"rtmp_url":               "rtmps://ingest.example.invalid/live2",
					"stream_key_secret_name": "synthetic-youtube-key-ref",
					"dry_run":                false,
					"complete_on_stop":       true,
				}
				runtime := make(map[string]any, len(wantEncoder)+3)
				for key, value := range wantEncoder {
					runtime[key] = value
				}
				runtime["ingest_selection"] = selection
				runtime["enable_auto_start"] = scheduled
				if scheduled {
					runtime["scheduled_start_at"] = "2026-11-06T04:41:00+09:00"
				}
				wantCP := make(map[string]any, len(runtime))
				for key, value := range runtime {
					wantCP[key] = value
				}
				h.req.YouTubeRuntime = runtime
				h.req.EncoderRTMPURL = "rtmps://ingest.example.invalid/live2"
				h.req.EncoderStreamKeySecretName = "synthetic-youtube-key-ref"

				// The harness decodes the actual preparation request through Contracts.
				results := h.start()
				h.assertSecretSafe(results)
				if hasFailedDispatchResult(results) || !h.control.EncoderCommitted || !h.control.BotStarted {
					t.Fatalf("live_api preparation failed: %+v", results)
				}
				if !reflect.DeepEqual(h.events, []string{"prepare", "worker", "commit", "bot"}) {
					t.Fatalf("unexpected start sequence: %v", h.events)
				}

				_, payload, ok := h.client.startPayload(
					store.Stream{ID: "stream-01", Name: "Synthetic"},
					h.services[0], h.req, h.services[0].PublicURL,
					h.services[1], h.req.ArchiveStartedAt,
				)
				if !ok {
					t.Fatal("Encoder payload was not available")
				}
				sent := payload.(map[string]any)["youtube_runtime"]
				if !reflect.DeepEqual(sent, wantEncoder) {
					t.Fatalf("Encoder runtime = %#v, want %#v", sent, wantEncoder)
				}
				if !reflect.DeepEqual(h.req.YouTubeRuntime, wantCP) {
					t.Fatal("Encoder projection changed CP lifecycle metadata")
				}
			})
		}
	}
}
