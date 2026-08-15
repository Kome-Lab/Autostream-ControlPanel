package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestYouTubeLiveAPIPrepareFailureMetadataClassifiesOAuthTransportSafely(t *testing.T) {
	cause := &url.Error{
		Op:  "Post",
		URL: "https://oauth2.example.invalid/token?client_secret=must-not-be-recorded",
		Err: &oauth2.RetrieveError{
			Response:  &http.Response{StatusCode: http.StatusBadRequest},
			ErrorCode: "invalid_grant",
		},
	}

	metadata := youtubeLiveAPIPrepareFailureMetadata(newYouTubeLiveAPIPrepareError("provider_prepare", cause))
	if metadata["error_class"] != "oauth_token_exchange" {
		t.Fatalf("error_class = %v", metadata["error_class"])
	}
	if metadata["transport_operation"] != "post" {
		t.Fatalf("transport_operation = %v", metadata["transport_operation"])
	}
	if metadata["provider_status_code"] != http.StatusBadRequest || metadata["provider_reason"] != "invalid_grant" {
		t.Fatalf("provider diagnostics = %#v", metadata)
	}
	if strings.Contains(fmt.Sprint(metadata), "client_secret") {
		t.Fatalf("metadata leaked the transport URL: %#v", metadata)
	}
}

func TestYouTubeLiveAPIPrepareFailureMetadataClassifiesDNSSafely(t *testing.T) {
	cause := &url.Error{
		Op:  "Get",
		URL: "https://youtube.example.invalid/?access_token=must-not-be-recorded",
		Err: &net.DNSError{Err: "no such host", Name: "youtube.example.invalid"},
	}

	metadata := youtubeLiveAPIPrepareFailureMetadata(newYouTubeLiveAPIPrepareError("provider_prepare", cause))
	if metadata["error_class"] != "dns" || metadata["transport_operation"] != "get" {
		t.Fatalf("transport diagnostics = %#v", metadata)
	}
	if _, ok := metadata["provider_status_code"]; ok {
		t.Fatalf("DNS failure must not invent provider status: %#v", metadata)
	}
	if strings.Contains(fmt.Sprint(metadata), "access_token") {
		t.Fatalf("metadata leaked the transport URL: %#v", metadata)
	}
}
