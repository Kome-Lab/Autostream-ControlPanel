package updateagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelClientClaimsAcceptsAndReportsOpaqueBootstrapJob(t *testing.T) {
	var calls []string
	recipientFingerprint := bootstrapPanelTestRecipientFingerprint()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer runtime-token" {
			t.Fatalf("Authorization=%q", got)
		}
		calls = append(calls, r.URL.Path)
		w.Header().Set("Cache-Control", "no-store")
		switch r.URL.Path {
		case "/services/update-agent/bootstrap-jobs/claim":
			var body struct {
				ServiceID               string `json:"service_id"`
				CurrentRevision         int64  `json:"current_revision"`
				RecipientKeyFingerprint string `json:"recipient_key_fingerprint"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ServiceID != "updater-01" || body.CurrentRevision != 7 ||
				body.RecipientKeyFingerprint != recipientFingerprint {
				t.Fatalf("claim body=%#v", body)
			}
			writeBootstrapPanelTestJSON(w, http.StatusOK, map[string]any{
				"id":                "6ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
				"updater_id":        "updater-01",
				"expected_revision": 7,
				"host_ids":          []string{"host-01"},
				"lease_token":       "bootstrap-lease-token",
				"release_token":     "github-pat-release",
				"envelope": map[string]any{
					"version": 1, "ephemeral_public_key": "ephemeral", "nonce": "nonce", "ciphertext": "ciphertext",
				},
			})
		case "/services/update-agent/bootstrap-jobs/6ba7b810-9dad-4f0e-9a58-4aee7cb5560f/accept":
			w.WriteHeader(http.StatusNoContent)
		case "/services/update-agent/bootstrap-jobs/6ba7b810-9dad-4f0e-9a58-4aee7cb5560f/report":
			var report BootstrapJobReport
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
				t.Fatal(err)
			}
			if report.HostID != "host-01" || report.Status != BootstrapHostStatusInstalling || report.Progress != 70 {
				t.Fatalf("report=%#v", report)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := PanelClient{
		BaseURL: server.URL,
		Token:   "runtime-token",
		HTTP:    server.Client(),
	}
	claim, ok, err := client.ClaimBootstrap(context.Background(), "updater-01", 7, recipientFingerprint)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if claim.ID != "6ba7b810-9dad-4f0e-9a58-4aee7cb5560f" ||
		claim.LeaseToken != "bootstrap-lease-token" ||
		claim.ReleaseToken.Reveal() != "github-pat-release" ||
		claim.Envelope.Ciphertext != "ciphertext" {
		t.Fatalf("claim=%#v", claim)
	}
	if err := client.AcceptBootstrap(context.Background(), claim.ID, "updater-01", claim.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := client.ReportBootstrap(context.Background(), claim.ID, BootstrapJobReport{
		ServiceID:  "updater-01",
		LeaseToken: claim.LeaseToken,
		HostID:     "host-01",
		Status:     BootstrapHostStatusInstalling,
		Progress:   70,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "/services/update-agent/bootstrap-jobs/claim,/services/update-agent/bootstrap-jobs/6ba7b810-9dad-4f0e-9a58-4aee7cb5560f/accept,/services/update-agent/bootstrap-jobs/6ba7b810-9dad-4f0e-9a58-4aee7cb5560f/report" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestPanelClientBootstrapClaimRequiresNoStoreAndSecureTransport(t *testing.T) {
	insecure := PanelClient{BaseURL: "http://panel.example.com", Token: "runtime-token"}
	recipientFingerprint := bootstrapPanelTestRecipientFingerprint()
	if _, _, err := insecure.ClaimBootstrap(context.Background(), "updater-01", 1, recipientFingerprint); err == nil ||
		!strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("insecure claim err=%v", err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBootstrapPanelTestJSON(w, http.StatusOK, map[string]any{
			"id": "job", "updater_id": "updater-01", "expected_revision": 1,
			"host_ids": []string{"host-01"}, "lease_token": "lease", "release_token": "release",
			"envelope": map[string]any{"version": 1, "ephemeral_public_key": "public", "nonce": "nonce", "ciphertext": "cipher"},
		})
	}))
	defer server.Close()
	client := PanelClient{BaseURL: server.URL, Token: "runtime-token", HTTP: server.Client()}
	if _, _, err := client.ClaimBootstrap(context.Background(), "updater-01", 1, recipientFingerprint); err == nil ||
		!strings.Contains(err.Error(), "no-store") {
		t.Fatalf("cacheable claim err=%v", err)
	}
}

func TestPanelClientRejectsUnsafeBootstrapReportMessages(t *testing.T) {
	client := PanelClient{BaseURL: "https://panel.example.com", Token: "runtime-token"}
	for name, message := range map[string]string{
		"C0":            "failed\nnext",
		"DEL":           "failed\u007fhidden",
		"bidi override": "failed\u202esecret",
		"bidi isolate":  "failed\u2066secret\u2069",
	} {
		t.Run(name, func(t *testing.T) {
			err := client.ReportBootstrap(context.Background(), "6ba7b810-9dad-4f0e-9a58-4aee7cb5560f", BootstrapJobReport{
				ServiceID: "updater-01", LeaseToken: "lease-token", HostID: "host-01",
				Status: BootstrapHostStatusFailed, Progress: 100, Code: "ssh_failed", Message: message,
			})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("ReportBootstrap() error = %v", err)
			}
		})
	}
}

func TestPanelClientBootstrapSecretPostsNeverFollowRedirects(t *testing.T) {
	redirectReached := false
	redirectTarget := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectReached = true
	}))
	defer redirectTarget.Close()

	for _, operation := range []struct {
		name string
		run  func(PanelClient) error
	}{
		{
			name: "accept",
			run: func(client PanelClient) error {
				return client.AcceptBootstrap(
					context.Background(),
					"6ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
					"updater-01",
					"lease-token",
				)
			},
		},
		{
			name: "report",
			run: func(client PanelClient) error {
				return client.ReportBootstrap(
					context.Background(),
					"6ba7b810-9dad-4f0e-9a58-4aee7cb5560f",
					BootstrapJobReport{
						ServiceID:  "updater-01",
						LeaseToken: "lease-token",
						HostID:     "host-01",
						Status:     BootstrapHostStatusInstalling,
						Progress:   70,
					},
				)
			},
		},
	} {
		t.Run(operation.name, func(t *testing.T) {
			redirectReached = false
			redirectSource := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", redirectTarget.URL+"/credential-capture")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer redirectSource.Close()
			client := PanelClient{
				BaseURL: redirectSource.URL,
				Token:   "runtime-token",
				HTTP:    redirectSource.Client(),
			}
			err := operation.run(client)
			var panelError *PanelHTTPError
			if !errors.As(err, &panelError) || panelError.Status != http.StatusTemporaryRedirect {
				t.Fatalf("redirect response error = %v", err)
			}
			if redirectReached {
				t.Fatal("bootstrap credential-bearing request followed a redirect")
			}
		})
	}
}

func writeBootstrapPanelTestJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func bootstrapPanelTestRecipientFingerprint() string {
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
}
