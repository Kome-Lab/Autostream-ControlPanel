package servicecall

import (
	"encoding/json"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStartRequiresSingleWorkerResponseJSONValue(t *testing.T) {
	const (
		validResponse = `{"job_generation":17}`
	)
	padResponse := func(totalLength int, suffix string) string {
		t.Helper()
		paddingLength := totalLength - len(validResponse) - len(suffix)
		if paddingLength < 0 {
			t.Fatalf("cannot build %d-byte response with %d-byte suffix", totalLength, len(suffix))
		}
		body := validResponse + strings.Repeat(" ", paddingLength) + suffix
		if len(body) != totalLength {
			t.Fatalf("response length=%d, want %d", len(body), totalLength)
		}
		return body
	}
	secondObject := `{"job_generation":18}`

	for _, test := range []struct {
		name         string
		workerResult string
		wantBodyLen  int
		wantSuccess  bool
	}{
		{
			name:         "single JSON object",
			workerResult: validResponse,
			wantBodyLen:  len(validResponse),
			wantSuccess:  true,
		},
		{
			name:         "second JSON object",
			workerResult: validResponse + secondObject,
			wantBodyLen:  len(validResponse) + len(secondObject),
		},
		{
			name:         "trailing garbage",
			workerResult: validResponse + "garbage",
			wantBodyLen:  len(validResponse) + len("garbage"),
		},
		{
			name:         "below limit with trailing whitespace",
			workerResult: padResponse(expectedWorkerStartResponseLimitBytes-1, ""),
			wantBodyLen:  expectedWorkerStartResponseLimitBytes - 1,
			wantSuccess:  true,
		},
		{
			name:         "exact limit with trailing whitespace",
			workerResult: padResponse(expectedWorkerStartResponseLimitBytes, ""),
			wantBodyLen:  expectedWorkerStartResponseLimitBytes,
			wantSuccess:  true,
		},
		{
			name:         "one byte over limit with trailing whitespace",
			workerResult: padResponse(expectedWorkerStartResponseLimitPlusOneBytes, ""),
			wantBodyLen:  expectedWorkerStartResponseLimitPlusOneBytes,
		},
		{
			name:         "second JSON object hidden beyond limit",
			workerResult: padResponse(expectedWorkerStartResponseLimitBytes+len(secondObject), secondObject),
			wantBodyLen:  expectedWorkerStartResponseLimitBytes + len(secondObject),
		},
		{
			name:         "garbage hidden beyond limit",
			workerResult: padResponse(expectedWorkerStartResponseLimitPlusOneBytes, "g"),
			wantBodyLen:  expectedWorkerStartResponseLimitPlusOneBytes,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if len(test.workerResult) != test.wantBodyLen {
				t.Fatalf("test response length=%d, want %d", len(test.workerResult), test.wantBodyLen)
			}
			requests := map[string]int{}
			var botGeneration any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				switch {
				case payload["overlay_profile_id"] != nil:
					requests["worker"]++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					_, _ = io.WriteString(w, test.workerResult)
				case payload["discord_target"] != nil:
					requests["discord_bot"]++
					botGeneration = payload["job_generation"]
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Fatalf("unexpected start payload: %#v", payload)
				}
			}))
			defer server.Close()

			results := testClient().Start(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{
				{ServiceID: "worker-01", ServiceType: "worker", PublicURL: server.URL},
				{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: server.URL, ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
			}, StartRequest{DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003", DiscordTextChannelID: "1002"})

			if test.wantSuccess {
				if requests["worker"] != 1 || requests["discord_bot"] != 1 || len(results) != 2 || !results[0].Success || !results[1].Success {
					t.Fatalf("valid Worker response dispatch failed: requests=%#v results=%#v", requests, results)
				}
				if botGeneration != float64(17) {
					t.Fatalf("Discord Bot generation=%#v, want 17", botGeneration)
				}
				return
			}

			if requests["worker"] != 1 || requests["discord_bot"] != 0 {
				t.Fatalf("invalid Worker response dispatch count=%#v, want worker=1 discord_bot=0", requests)
			}
			if len(results) != 1 || results[0].Success || results[0].Code != "worker_job_generation_response_invalid" || results[0].FailurePhase != "protocol" {
				t.Fatalf("invalid Worker response did not fail as a protocol error: %#v", results)
			}
			if strings.Contains(results[0].Error, test.workerResult) {
				t.Fatalf("protocol error exposed raw Worker response: %q", results[0].Error)
			}
		})
	}
}

func TestStartDispatchesArchiveRunV2(t *testing.T) {
	startedAt := time.Date(2026, 8, 18, 5, 6, 29, 123456789, time.UTC)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	service := store.RegisteredService{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}
	results := testClient().Start(t.Context(), store.Stream{ID: "stream-01", Name: "History"}, []store.RegisteredService{service}, StartRequest{
		ArchiveProfileID: "archive-01", ArchiveRunID: "run-01", ArchiveStartedAt: startedAt,
	})
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("start result = %#v", results)
	}
	if payload["archive_run_id"] != "run-01" || payload["started_at"] != startedAt.Format(time.RFC3339Nano) {
		t.Fatalf("Encoder v2 archive payload = %#v", payload)
	}
}

func TestStartRejectsMissingArchiveRunAuthority(t *testing.T) {
	results := testClient().Start(t.Context(), store.Stream{ID: "stream-01", Name: "History"}, []store.RegisteredService{{
		ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com",
	}}, StartRequest{ArchiveProfileID: "archive-01"})
	if len(results) != 1 || results[0].Success || results[0].Code != "archive_run_authority_unavailable" || results[0].FailurePhase != "pre_dispatch" {
		t.Fatalf("missing archive authority result = %#v", results)
	}
}
