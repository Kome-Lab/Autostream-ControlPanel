package servicecall

import (
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStopExtendsOnlyEncoderTimeoutWithoutMutatingSuppliedHTTPClient(t *testing.T) {
	const normalTimeout = time.Second
	transport := &requestDeadlineTransport{}
	suppliedHTTPClient := &http.Client{Transport: transport, Timeout: normalTimeout}
	client := testClient()
	client.Config.Timeout = normalTimeout
	client.HTTP = suppliedHTTPClient
	stream := store.Stream{ID: "stream-01"}

	for _, service := range []store.RegisteredService{
		{ServiceID: "encoder-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com"},
		{ServiceID: "discord-01", ServiceType: "discord_bot", PublicURL: "https://discord.example.com"},
		{ServiceID: "worker-01", ServiceType: "worker", PublicURL: "https://worker.example.com"},
	} {
		results := client.Stop(t.Context(), stream, []store.RegisteredService{service})
		if len(results) != 1 || !results[0].Success {
			t.Fatalf("stop %s failed: %#v", service.ServiceType, results)
		}
	}
	startResults := client.Start(t.Context(), stream, []store.RegisteredService{{
		ServiceID: "encoder-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com",
	}}, StartRequest{})
	if len(startResults) != 1 || !startResults[0].Success {
		t.Fatalf("start failed: %#v", startResults)
	}

	if client.Config.Timeout != normalTimeout {
		t.Fatalf("client config timeout was mutated: got %s want %s", client.Config.Timeout, normalTimeout)
	}
	if suppliedHTTPClient.Timeout != normalTimeout {
		t.Fatalf("supplied HTTP client timeout was mutated: got %s want %s", suppliedHTTPClient.Timeout, normalTimeout)
	}
	if len(transport.requests) != 4 {
		t.Fatalf("request count = %d, want 4", len(transport.requests))
	}
	assertRequestTimeout(t, transport.requests[0], 15*time.Second)
	assertRequestTimeout(t, transport.requests[1], normalTimeout)
	assertRequestTimeout(t, transport.requests[2], normalTimeout)
	assertRequestTimeout(t, transport.requests[3], normalTimeout)
}

func TestStopEncoderUsesLongerConfiguredTimeout(t *testing.T) {
	const configuredTimeout = 20 * time.Second
	transport := &requestDeadlineTransport{}
	suppliedHTTPClient := &http.Client{Transport: transport, Timeout: time.Second}
	client := testClient()
	client.Config.Timeout = configuredTimeout
	client.HTTP = suppliedHTTPClient

	results := client.Stop(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{
		ServiceID: "encoder-01", ServiceType: "encoder_recorder", PublicURL: "https://encoder.example.com",
	}})
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("encoder stop failed: %#v", results)
	}
	if suppliedHTTPClient.Timeout != time.Second {
		t.Fatalf("supplied HTTP client timeout was mutated: got %s want %s", suppliedHTTPClient.Timeout, time.Second)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(transport.requests))
	}
	assertRequestTimeout(t, transport.requests[0], configuredTimeout)
}

func TestPreviewAssetUsesAssignedEncoderTokenAndBoundsResponse(t *testing.T) {
	playlist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:2.0,\nsegment-000001.ts\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/preview/index.m3u8" {
			t.Fatalf("unexpected preview path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Fatalf("unexpected preview authorization: %q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("Accept"), "mpegurl") {
			t.Fatalf("unexpected preview accept header: %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(playlist))
	}))
	defer server.Close()

	client := testClient()
	result := client.PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "index.m3u8", "")
	if !result.Success || string(result.Body) != playlist || result.ContentType != "application/vnd.apple.mpegurl" {
		t.Fatalf("unexpected preview result: %#v body=%q", result, string(result.Body))
	}
}

func TestPreviewAssetRejectsOversizedPlaylist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxPreviewPlaylistBytes+1)))
	}))
	defer server.Close()
	client := testClient()
	result := client.PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "index.m3u8", "")
	if result.Success || result.Code != "preview_asset_too_large" || len(result.Body) != 0 {
		t.Fatalf("oversized preview was accepted: %#v", result)
	}
}

func TestPreviewAssetForwardsSingleRangeAndPreservesPartialResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/preview/segment-000001.ts" {
			t.Fatalf("unexpected preview path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Fatalf("unexpected preview authorization: %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("Range"); got != "bytes=1-3" {
			t.Fatalf("range = %q, want bytes=1-3", got)
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 1-3/6")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("bcd"))
	}))
	defer server.Close()

	client := testClient()
	result := client.PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "segment-000001.ts", "bytes=1-3")
	if !result.Success || result.StatusCode != http.StatusPartialContent || result.ContentRange != "bytes 1-3/6" || result.AcceptRanges != "bytes" || string(result.Body) != "bcd" {
		t.Fatalf("unexpected partial preview result: %#v body=%q", result, string(result.Body))
	}
}

func TestPreviewAssetPreservesUnsatisfiedRangeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/stream-01/preview/segment-000001.ts" {
			t.Fatalf("unexpected preview path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Range"); got != "bytes=99-" {
			t.Fatalf("range = %q, want bytes=99-", got)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes */6")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer server.Close()

	result := testClient().PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "segment-000001.ts", "bytes=99-")
	if !result.Success || result.StatusCode != http.StatusRequestedRangeNotSatisfiable || result.ContentRange != "bytes */6" || result.AcceptRanges != "bytes" || len(result.Body) != 0 {
		t.Fatalf("unexpected unsatisfied range result: %#v body=%q", result, string(result.Body))
	}
}

func TestPreviewAssetDoesNotForwardInvalidOrMultiRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "" {
			t.Fatalf("unexpected range forwarding: %q", got)
		}
		_, _ = w.Write([]byte("segment"))
	}))
	defer server.Close()

	result := testClient().PreviewAsset(t.Context(), store.Stream{ID: "stream-01"}, []store.RegisteredService{{ServiceID: "enc-01", ServiceType: "encoder_recorder", PublicURL: server.URL}}, "segment-000001.ts", "bytes=0-1,4-5")
	if !result.Success {
		t.Fatalf("invalid range request failed unexpectedly: %#v", result)
	}
}
