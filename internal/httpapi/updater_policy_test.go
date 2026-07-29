package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateagent"
	"golang.org/x/crypto/ssh"
)

func TestUpdaterPolicyAdminNormalizesHostKeyCommentAndKeepsReleaseTokenWriteOnly(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "updater-admin", Username: "updater-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(
		store.User{ID: "updater-reader", Username: "updater-reader"},
		"correct horse battery",
		[]string{"system_updates.read"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "updater-admin", "correct horse battery")
	hostKey, hostFingerprint := ed25519AuthorizedKeyForTest(t, "central host")
	const releaseToken = "github_pat_release_token_must_not_be_returned"
	body := map[string]any{
		"expected_revision":          0,
		"api":                        map[string]any{"bind_host": "127.0.0.1", "host": "127.0.0.1", "port": 8090, "ssl_enabled": false},
		"poll_interval_seconds":      15,
		"heartbeat_interval_seconds": 30,
		"hosts": []map[string]any{{
			"host_id": "host-01", "name": "Host 01", "address": "10.0.0.10", "port": 55850,
			"user": "autostream-update-host", "arch": "amd64", "host_public_key": hostKey,
		}},
		"targets": []map[string]any{{
			"target_id": "worker-01", "service_id": "worker-service-01", "host_id": "host-01", "service_type": "worker", "deployment_mode": "systemd",
		}},
		"github_token": releaseToken,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/system-updates/updaters/updater-01/settings", bytes.NewReader(payload))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("save updater policy status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), releaseToken) {
		t.Fatalf("save updater policy echoed release token: %s", res.Body.String())
	}
	var saved struct {
		Revision               int64  `json:"revision"`
		GitHubTokenConfigured  bool   `json:"github_token_configured"`
		GitHubTokenFingerprint string `json:"github_token_fingerprint"`
		Hosts                  []struct {
			HostPublicKey            string `json:"host_public_key"`
			HostPublicKeyFingerprint string `json:"host_public_key_fingerprint"`
		} `json:"hosts"`
		Targets []struct {
			ServiceID string `json:"service_id"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(res.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || !saved.GitHubTokenConfigured || len(saved.Hosts) != 1 {
		t.Fatalf("saved updater policy response = %#v", saved)
	}
	if saved.GitHubTokenFingerprint == "" {
		t.Fatal("saved updater policy omitted release token fingerprint")
	}
	if saved.Hosts[0].HostPublicKeyFingerprint != hostFingerprint {
		t.Fatalf("host fingerprint = %q; want %q", saved.Hosts[0].HostPublicKeyFingerprint, hostFingerprint)
	}
	if len(saved.Targets) != 1 || saved.Targets[0].ServiceID != "worker-service-01" {
		t.Fatalf("saved target service binding = %#v", saved.Targets)
	}
	if strings.Contains(saved.Hosts[0].HostPublicKey, "central host") || !strings.HasPrefix(saved.Hosts[0].HostPublicKey, ssh.KeyAlgoED25519+" ") {
		t.Fatalf("host public key was not normalized to commentless canonical form: %q", saved.Hosts[0].HostPublicKey)
	}
	storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || storedToken != releaseToken {
		t.Fatalf("stored release token = %q, %v", storedToken, err)
	}

	get := httptest.NewRequest(http.MethodGet, "/system-updates/updaters/updater-01/settings", nil)
	get.AddCookie(cookie)
	getResult := httptest.NewRecorder()
	handler.ServeHTTP(getResult, get)
	if getResult.Code != http.StatusOK || strings.Contains(getResult.Body.String(), releaseToken) ||
		!strings.Contains(getResult.Body.String(), `"github_token_configured":true`) {
		t.Fatalf("get updater policy status = %d body = %s", getResult.Code, getResult.Body.String())
	}
	readerCookie, _ := loginForTest(t, handler, "updater-reader", "correct horse battery")
	readerGet := httptest.NewRequest(http.MethodGet, "/system-updates/updaters/updater-01/settings", nil)
	readerGet.AddCookie(readerCookie)
	readerGetResult := httptest.NewRecorder()
	handler.ServeHTTP(readerGetResult, readerGet)
	if readerGetResult.Code != http.StatusOK ||
		!strings.Contains(readerGetResult.Body.String(), `"github_token_configured":true`) ||
		strings.Contains(readerGetResult.Body.String(), "github_token_fingerprint") {
		t.Fatalf("reader token metadata status = %d body = %s", readerGetResult.Code, readerGetResult.Body.String())
	}

	body["expected_revision"] = 1
	body["github_token"] = ""
	payload, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	deleteRequest := httptest.NewRequest(http.MethodPut, "/system-updates/updaters/updater-01/settings", bytes.NewReader(payload))
	deleteRequest.AddCookie(cookie)
	deleteRequest.Header.Set("X-CSRF-Token", csrf)
	deleteResult := httptest.NewRecorder()
	handler.ServeHTTP(deleteResult, deleteRequest)
	if deleteResult.Code != http.StatusOK || !strings.Contains(deleteResult.Body.String(), `"github_token_configured":false`) {
		t.Fatalf("delete updater release token status = %d body = %s", deleteResult.Code, deleteResult.Body.String())
	}
	if _, err := policies.GetUpdaterReleaseTokenValue(t.Context()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("explicit empty release token was not deleted: %v", err)
	}
}

func TestSSHUpdaterPolicyRejectsDatabaseNameWithoutMutatingReleaseToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "ssh-policy-admin", Username: "ssh-policy-admin"},
		"correct horse battery",
		[]string{"system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-ssh")
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	const originalToken = "github_pat_original"
	originalPolicy, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-ssh",
		0,
		updaterPolicyForHTTPTest(hostKey),
		func() *string {
			value := originalToken
			return &value
		}(),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "ssh-policy-admin", "correct horse battery")
	for name, databaseName := range map[string]json.RawMessage{
		"value": json.RawMessage(`"autostream_panel"`),
		"null":  json.RawMessage(`null`),
	} {
		t.Run(name, func(t *testing.T) {
			requestBody := validUpdaterPolicyRequest(t)
			requestBody.ExpectedRevision = originalPolicy.Revision
			requestBody.Targets[0].DatabaseName = databaseName
			replacementToken := "github_pat_replacement"
			requestBody.GitHubToken = &replacementToken
			payload, err := json.Marshal(requestBody)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(
				http.MethodPut,
				"/system-updates/updaters/updater-ssh/settings",
				bytes.NewReader(payload),
			)
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", csrf)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest ||
				!strings.Contains(response.Body.String(), `"code":"invalid_updater_database_name"`) {
				t.Fatalf("SSH database name = %d %s", response.Code, response.Body.String())
			}
			storedPolicy, err := policies.GetUpdaterPolicy(t.Context(), "updater-ssh")
			if err != nil || storedPolicy.Revision != originalPolicy.Revision {
				t.Fatalf("SSH database name mutated policy: %#v, %v", storedPolicy, err)
			}
			storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
			if err != nil || storedToken != originalToken {
				t.Fatalf("SSH database name mutated token: %q, %v", storedToken, err)
			}
		})
	}
}

func TestUpdaterPolicySaveRejectsEveryActiveBootstrapStateWithoutMutation(t *testing.T) {
	for _, bootstrapState := range []UpdateHostBootstrapStatus{
		UpdateHostBootstrapStatusQueued,
		UpdateHostBootstrapStatusClaimed,
		UpdateHostBootstrapStatusRunning,
	} {
		t.Run(string(bootstrapState), func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(
				store.User{ID: "policy-admin", Username: "policy-admin"},
				"correct horse battery",
				[]string{"system_updates.read", "system_updates.execute", "secrets.update"},
			); err != nil {
				t.Fatal(err)
			}
			registerUpdateAgentForPolicyTest(t, auth, "updater-01")
			policies := store.NewMemoryUpdaterPolicyStore()
			hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
			originalPolicy, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
				t.Context(),
				"updater-01",
				0,
				updaterPolicyForHTTPTest(hostKey),
				stringPointerForBootstrapTest("github_pat_original"),
			)
			if err != nil {
				t.Fatal(err)
			}
			broker := NewUpdateHostBootstrapBroker()
			job, replayed, err := broker.Create(UpdateHostBootstrapCreateParams{
				UpdaterID: "updater-01", ExpectedRevision: originalPolicy.Revision,
				ClientJobID: "bootstrap-job", IdempotencyKey: "bootstrap-once",
				RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
				HostIDs:                 []string{"host-01"}, Envelope: []byte("opaque-credential"),
			})
			if err != nil || replayed {
				t.Fatalf("create bootstrap job = (%#v, %v, %v)", job, replayed, err)
			}
			var claim UpdateHostBootstrapClaim
			if bootstrapState == UpdateHostBootstrapStatusClaimed || bootstrapState == UpdateHostBootstrapStatusRunning {
				recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
				claim, err = broker.Claim(
					"updater-01",
					originalPolicy.Revision,
					recipientFingerprint,
					recipientFingerprint,
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			if bootstrapState == UpdateHostBootstrapStatusRunning {
				if _, err := broker.Accept(job.ID, "updater-01", originalPolicy.Revision, claim.LeaseToken); err != nil {
					t.Fatal(err)
				}
			}
			handler := NewServer(
				store.NewMemoryStreamStore(),
				WithAuthStore(auth),
				WithAuditStore(auth),
				WithServiceRegistryStore(auth),
				WithUpdaterPolicyStore(policies),
				WithUpdateHostBootstrapBroker(broker),
			)
			cookie, csrf := loginForTest(t, handler, "policy-admin", "correct horse battery")
			replacement := updaterPolicyForHTTPTest(hostKey)
			payload, err := json.Marshal(map[string]any{
				"expected_revision":          originalPolicy.Revision,
				"api":                        replacement.API,
				"poll_interval_seconds":      replacement.PollIntervalSeconds,
				"heartbeat_interval_seconds": replacement.HeartbeatIntervalSeconds,
				"hosts":                      replacement.Hosts,
				"targets":                    replacement.Targets,
				"github_token":               "github_pat_changed",
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(
				http.MethodPut,
				"/system-updates/updaters/updater-01/settings",
				bytes.NewReader(payload),
			)
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", csrf)
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, request)
			if result.Code != http.StatusConflict ||
				!strings.Contains(result.Body.String(), `"code":"updater_host_bootstrap_in_progress"`) {
				t.Fatalf("active bootstrap policy save status=%d body=%s", result.Code, result.Body.String())
			}
			storedPolicy, err := policies.GetUpdaterPolicy(t.Context(), "updater-01")
			if err != nil || storedPolicy.Revision != originalPolicy.Revision {
				t.Fatalf("policy mutated during active bootstrap: policy=%#v err=%v", storedPolicy, err)
			}
			storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
			if err != nil || storedToken != "github_pat_original" {
				t.Fatalf("release token mutated during active bootstrap: token=%q err=%v", storedToken, err)
			}
		})
	}
}

func TestParseUpdaterED25519PublicKeyAcceptsCommentButRejectsUnsafeForms(t *testing.T) {
	t.Parallel()

	canonical, _ := ed25519AuthorizedKeyForTest(t, "")
	key, err := parseUpdaterED25519PublicKey(canonical + " central-host")
	if err != nil {
		t.Fatalf("commented public key was rejected: %v", err)
	}
	if got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))); got != canonical {
		t.Fatalf("comment was not removed canonically: %q", got)
	}

	for name, raw := range map[string]string{
		"options":        `from="10.0.0.1" ` + canonical,
		"multiple lines": canonical + "\n" + canonical,
		"RSA":            rsaAuthorizedKeyForTest(t),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseUpdaterED25519PublicKey(raw); !errors.Is(err, errInvalidUpdaterHostPublicKey) {
				t.Fatalf("unsafe public key error = %v, want errInvalidUpdaterHostPublicKey", err)
			}
		})
	}
}

func TestGenericSecretEndpointRejectsUpdaterReleaseToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "secret-admin", Username: "secret-admin"},
		"correct horse battery",
		[]string{"secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	policies := store.NewMemoryUpdaterPolicyStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "secret-admin", "correct horse battery")
	const releaseToken = "must-not-enter-generic-secret-store"
	req := httptest.NewRequest(
		http.MethodPut,
		"/secrets/"+store.UpdaterGitHubReleaseTokenSecretName,
		strings.NewReader(`{"value":"`+releaseToken+`"}`),
	)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"unknown_secret"`) {
		t.Fatalf("generic updater token update status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := policies.GetUpdaterReleaseTokenValue(t.Context()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("generic endpoint mutated updater release token: %v", err)
	}
}

func TestUpdaterPolicyRejectsNonASCIIReleaseTokenWithoutMutation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "updater-admin", Username: "updater-admin"},
		"correct horse battery",
		[]string{"system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "updater-admin", "correct horse battery")
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	unsafeToken := "github_pat_\U0001f4a5"
	body := updaterPolicyUpdateRequest{
		ExpectedRevision:    0,
		API:                 store.UpdaterPolicyAPI{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090},
		PollIntervalSeconds: 15, HeartbeatIntervalSeconds: 30,
		Hosts: updaterPolicyForHTTPTest(hostKey).Hosts, Targets: updaterPolicyTargetRequestsForTest(updaterPolicyForHTTPTest(hostKey).Targets),
		GitHubToken: &unsafeToken,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/system-updates/updaters/updater-01/settings", bytes.NewReader(payload))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_updater_policy"`) {
		t.Fatalf("non-ASCII token status = %d body = %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), unsafeToken) {
		t.Fatalf("unsafe token escaped in response: %s", res.Body.String())
	}
	if _, err := policies.GetUpdaterPolicy(t.Context(), "updater-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unsafe token mutated policy: %v", err)
	}
	if _, err := policies.GetUpdaterReleaseTokenValue(t.Context()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unsafe token was stored: %v", err)
	}
}

func TestUpdaterPolicyRejectsHeartbeatAboveAvailabilityWindow(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "updater-admin", Username: "updater-admin"},
		"correct horse battery",
		[]string{"system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "updater-admin", "correct horse battery")
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	body := updaterPolicyUpdateRequest{
		ExpectedRevision:    0,
		API:                 store.UpdaterPolicyAPI{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090},
		PollIntervalSeconds: 15, HeartbeatIntervalSeconds: 61,
		Hosts: updaterPolicyForHTTPTest(hostKey).Hosts, Targets: updaterPolicyTargetRequestsForTest(updaterPolicyForHTTPTest(hostKey).Targets),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/system-updates/updaters/updater-01/settings", bytes.NewReader(payload))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_updater_policy"`) {
		t.Fatalf("heartbeat above availability window status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := policies.GetUpdaterPolicy(t.Context(), "updater-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid heartbeat interval mutated policy: %v", err)
	}
}

func TestUpdaterPolicyValidationFailureDoesNotMutatePolicyOrReleaseToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "updater-admin", Username: "updater-admin"},
		"correct horse battery",
		[]string{"system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	seedHostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	previousToken := "previous-release-token"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"token-seed",
		0,
		updaterPolicyForHTTPTest(seedHostKey),
		&previousToken,
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "updater-admin", "correct horse battery")
	body := `{
		"expected_revision":0,
		"api":{"bind_host":"127.0.0.1","host":"127.0.0.1","port":8090,"ssl_enabled":false},
		"poll_interval_seconds":15,
		"heartbeat_interval_seconds":30,
		"hosts":[{"host_id":"host-01","name":"Host 01","address":"10.0.0.10","port":55850,"user":"autostream-update-host","arch":"amd64","host_public_key":"ssh-rsa invalid"}],
		"targets":[{"target_id":"worker-01","host_id":"host-01","service_type":"worker","deployment_mode":"systemd"}],
		"github_token":"replacement-release-token"
	}`
	body = strings.Replace(body, "ssh-rsa invalid", rsaAuthorizedKeyForTest(t), 1)
	req := httptest.NewRequest(http.MethodPut, "/system-updates/updaters/updater-01/settings", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_updater_host_public_key") {
		t.Fatalf("invalid host key status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := policies.GetUpdaterPolicy(t.Context(), "updater-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid policy was persisted: %v", err)
	}
	storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || storedToken != "previous-release-token" {
		t.Fatalf("validation failure mutated release token = %q, %v", storedToken, err)
	}
}

func TestUpdaterPolicySaveRequiresSecretPermissionOnlyWhenTokenChanges(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "update-operator", Username: "update-operator"},
		"correct horse battery",
		[]string{"system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(
		store.User{ID: "secret-operator", Username: "secret-operator"},
		"correct horse battery",
		[]string{"secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	originalHostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	originalToken := "existing-release-token"
	originalPolicy, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		updaterPolicyForHTTPTest(originalHostKey),
		&originalToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "update-operator", "correct horse battery")
	attackerHostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	currentRevision := originalPolicy.Revision
	for _, test := range []struct {
		name         string
		includeToken bool
		wantStatus   int
	}{
		{name: "retains existing release token", wantStatus: http.StatusOK},
		{name: "replaces release token", includeToken: true, wantStatus: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestBody := map[string]any{
				"expected_revision":          currentRevision,
				"api":                        map[string]any{"bind_host": "127.0.0.1", "host": "127.0.0.1", "port": 8090, "ssl_enabled": false},
				"poll_interval_seconds":      15,
				"heartbeat_interval_seconds": 30,
				"hosts": []map[string]any{{
					"host_id": "host-01", "name": "Attacker Host", "address": "attacker.example.com", "port": 55850,
					"user": "autostream-update-host", "arch": "amd64", "host_public_key": attackerHostKey,
				}},
				"targets": []map[string]any{{
					"target_id": "worker-01", "host_id": "host-01", "service_type": "worker", "deployment_mode": "systemd",
				}},
			}
			if test.includeToken {
				requestBody["github_token"] = "replacement-release-token"
			}
			body, err := json.Marshal(requestBody)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPut, "/system-updates/updaters/updater-01/settings", bytes.NewReader(body))
			req.AddCookie(cookie)
			req.Header.Set("X-CSRF-Token", csrf)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.wantStatus {
				t.Fatalf("updater policy permission status = %d body = %s", res.Code, res.Body.String())
			}
			if test.wantStatus == http.StatusForbidden && !strings.Contains(res.Body.String(), "permission_denied") {
				t.Fatalf("updater policy permission denial body = %s", res.Body.String())
			}
			storedPolicy, err := policies.GetUpdaterPolicy(t.Context(), "updater-01")
			if err != nil {
				t.Fatal(err)
			}
			if test.wantStatus == http.StatusOK {
				if storedPolicy.Revision != currentRevision+1 ||
					len(storedPolicy.Hosts) != 1 ||
					storedPolicy.Hosts[0].Address != "attacker.example.com" {
					t.Fatalf("token-preserving policy update failed: before=%#v after=%#v", originalPolicy, storedPolicy)
				}
				currentRevision = storedPolicy.Revision
			} else if storedPolicy.Revision != currentRevision ||
				len(storedPolicy.Hosts) != 1 ||
				storedPolicy.Hosts[0].Address != "attacker.example.com" {
				t.Fatalf("permission denial mutated policy: revision=%d after=%#v", currentRevision, storedPolicy)
			}
			storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
			if err != nil || storedToken != originalToken {
				t.Fatalf("permission denial mutated release token: %q, %v", storedToken, err)
			}
		})
	}

	secretCookie, secretCSRF := loginForTest(t, handler, "secret-operator", "correct horse battery")
	validBody, err := json.Marshal(updaterPolicyUpdateRequest{
		ExpectedRevision:         originalPolicy.Revision,
		API:                      originalPolicy.API,
		PollIntervalSeconds:      originalPolicy.PollIntervalSeconds,
		HeartbeatIntervalSeconds: originalPolicy.HeartbeatIntervalSeconds,
		Hosts:                    originalPolicy.Hosts,
		Targets:                  updaterPolicyTargetRequestsForTest(originalPolicy.Targets),
	})
	if err != nil {
		t.Fatal(err)
	}
	secretOnlyRequest := httptest.NewRequest(
		http.MethodPut,
		"/system-updates/updaters/updater-01/settings",
		bytes.NewReader(validBody),
	)
	secretOnlyRequest.AddCookie(secretCookie)
	secretOnlyRequest.Header.Set("X-CSRF-Token", secretCSRF)
	secretOnlyResult := httptest.NewRecorder()
	handler.ServeHTTP(secretOnlyResult, secretOnlyRequest)
	if secretOnlyResult.Code != http.StatusForbidden || !strings.Contains(secretOnlyResult.Body.String(), "permission_denied") {
		t.Fatalf("updater policy without system_updates.execute status = %d body = %s", secretOnlyResult.Code, secretOnlyResult.Body.String())
	}
	storedPolicy, err := policies.GetUpdaterPolicy(t.Context(), "updater-01")
	if err != nil ||
		storedPolicy.Revision != currentRevision ||
		len(storedPolicy.Hosts) != 1 ||
		storedPolicy.Hosts[0].Address != "attacker.example.com" {
		t.Fatalf("system_updates.execute denial mutated policy: %#v, %v", storedPolicy, err)
	}
	storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || storedToken != originalToken {
		t.Fatalf("system_updates.execute denial mutated release token: %q, %v", storedToken, err)
	}
}

func TestUpdaterPolicyRevisionConflictDoesNotMutateReleaseToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "updater-admin", Username: "updater-admin"},
		"correct horse battery",
		[]string{"system_updates.execute", "secrets.update"},
	); err != nil {
		t.Fatal(err)
	}
	registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	previousToken := "previous-token"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&previousToken,
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	cookie, csrf := loginForTest(t, handler, "updater-admin", "correct horse battery")
	requestBody := updaterPolicyUpdateRequest{
		ExpectedRevision: 0, API: store.UpdaterPolicyAPI{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090},
		PollIntervalSeconds: 15, HeartbeatIntervalSeconds: 30,
		Hosts: updaterPolicyForHTTPTest(hostKey).Hosts, Targets: updaterPolicyTargetRequestsForTest(updaterPolicyForHTTPTest(hostKey).Targets),
	}
	replacement := "replacement-token"
	requestBody.GitHubToken = &replacement
	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/system-updates/updaters/updater-01/settings", bytes.NewReader(payload))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "updater_policy_revision_conflict") {
		t.Fatalf("stale updater policy status = %d body = %s", res.Code, res.Body.String())
	}
	storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || storedToken != "previous-token" {
		t.Fatalf("revision conflict mutated release token = %q, %v", storedToken, err)
	}
}

func TestUpdateAgentPullsPolicyByAssignedRuntimeTokenWithoutReleaseToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	agentToken := registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, hostFingerprint := ed25519AuthorizedKeyForTest(t, "")
	releaseToken := "do-not-return-this-token"
	policy, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&releaseToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
	)
	req := httptest.NewRequest(http.MethodPost, "/services/update-agent/policy", strings.NewReader(`{"service_id":"updater-01","current_revision":0}`))
	req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("pull updater policy status = %d body = %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pull updater policy cache control = %q", res.Header().Get("Cache-Control"))
	}
	if strings.Contains(res.Body.String(), "do-not-return-this-token") || strings.Contains(res.Body.String(), "github_token") {
		t.Fatalf("pull updater policy leaked release token material: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), `"service_id"`) {
		t.Fatalf("legacy managed policy response added target service_id: %s", res.Body.String())
	}
	decoder := json.NewDecoder(res.Body)
	decoder.DisallowUnknownFields()
	var pulled updateagent.ManagedPolicy
	if err := decoder.Decode(&pulled); err != nil {
		t.Fatal(err)
	}
	if pulled.UpdaterID != "updater-01" || pulled.Revision != policy.Revision || len(pulled.Hosts) != 1 || pulled.Hosts[0].HostPublicKeyFingerprint != hostFingerprint {
		t.Fatalf("pulled updater policy = %#v", pulled)
	}
	unchanged := httptest.NewRequest(http.MethodPost, "/services/update-agent/policy", strings.NewReader(`{"service_id":"updater-01","current_revision":1}`))
	unchanged.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	unchangedResult := httptest.NewRecorder()
	handler.ServeHTTP(unchangedResult, unchanged)
	if unchangedResult.Code != http.StatusNoContent || unchangedResult.Body.Len() != 0 {
		t.Fatalf("unchanged updater policy status = %d body = %s", unchangedResult.Code, unchangedResult.Body.String())
	}

	secondAgentToken := registerUpdateAgentForPolicyTest(t, auth, "updater-02")
	missing := httptest.NewRequest(http.MethodPost, "/services/update-agent/policy", strings.NewReader(`{"service_id":"updater-02","current_revision":0}`))
	missing.Header.Set("Authorization", "Bearer "+secondAgentToken.RawToken)
	missingResult := httptest.NewRecorder()
	handler.ServeHTTP(missingResult, missing)
	if missingResult.Code != http.StatusConflict || !strings.Contains(missingResult.Body.String(), "updater_policy_not_configured") {
		t.Fatalf("missing updater policy status = %d body = %s", missingResult.Code, missingResult.Body.String())
	}
	wrongAssignment := httptest.NewRequest(http.MethodPost, "/services/update-agent/policy", strings.NewReader(`{"service_id":"updater-02","current_revision":0}`))
	wrongAssignment.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	wrongAssignmentResult := httptest.NewRecorder()
	handler.ServeHTTP(wrongAssignmentResult, wrongAssignment)
	if wrongAssignmentResult.Code != http.StatusForbidden || !strings.Contains(wrongAssignmentResult.Body.String(), "update_agent_not_assigned_to_token") {
		t.Fatalf("cross-agent policy pull status = %d body = %s", wrongAssignmentResult.Code, wrongAssignmentResult.Body.String())
	}
}

func TestSystemUpdateClaimRequiresReleaseTokenBeforeClaimMutation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	agentToken := registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01", PublicURL: "https://worker.example.com", Version: "v1.0.0",
	})
	now := time.Now().UTC()
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{
		ServiceID: "updater-01", Status: "online", Version: "v1.0.0",
		Capabilities: map[string]any{
			"policy_revision":   1,
			"policy_status":     "applied",
			"managed_targets":   []any{"worker-01"},
			"deployment_modes":  map[string]any{"worker-01": "systemd"},
			"target_hosts":      map[string]any{"worker-01": "host-01"},
			"deployed_versions": map[string]any{"worker-01": "v1.0.0"},
			"host_statuses":     map[string]any{"host-01": "reachable"},
			"host_checked_at":   map[string]any{"host-01": now.Format(time.RFC3339Nano)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	if _, err := policies.SaveUpdaterPolicy(t.Context(), "updater-01", 0, updaterPolicyForHTTPTest(hostKey)); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-01", ExecutionHostID: "host-01",
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "release-token-required",
		RequestedByUserID: "admin", RequestedByUsername: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	claim := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(`{"service_id":"updater-01","host_id":"host-01"}`))
		req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	missing := claim()
	if missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "updater_release_token_not_configured") {
		t.Fatalf("claim without release token status = %d body = %s", missing.Code, missing.Body.String())
	}
	jobs, err := updates.ListSystemUpdateJobs(t.Context(), 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Status != store.SystemUpdateStatusQueued || jobs[0].AgentServiceID != "updater-01" {
		t.Fatalf("claim without token mutated jobs = %#v, %v", jobs, err)
	}
	const releaseToken = "github_pat_one_time_claim_value"
	policyWithToken, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		1,
		updaterPolicyForHTTPTest(hostKey),
		func() *string {
			value := releaseToken
			return &value
		}(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{
		ServiceID: "updater-01", Status: "online", Version: "v1.0.0",
		Capabilities: map[string]any{
			"policy_revision":   policyWithToken.Revision,
			"policy_status":     "applied",
			"managed_targets":   []any{"worker-01"},
			"deployment_modes":  map[string]any{"worker-01": "systemd"},
			"target_hosts":      map[string]any{"worker-01": "host-01"},
			"deployed_versions": map[string]any{"worker-01": "v1.0.0"},
			"host_statuses":     map[string]any{"host-01": "reachable"},
			"host_checked_at":   map[string]any{"host-01": now.Format(time.RFC3339Nano)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	claimed := claim()
	if claimed.Code != http.StatusOK || claimed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("claim with release token status = %d cache=%q body=%s", claimed.Code, claimed.Header().Get("Cache-Control"), claimed.Body.String())
	}
	var response struct {
		ReleaseToken string                `json:"release_token"`
		Job          store.SystemUpdateJob `json:"job"`
	}
	if err := json.NewDecoder(claimed.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ReleaseToken != releaseToken || response.Job.ID != job.ID {
		t.Fatalf("claim response = %#v", response)
	}

	replacementHostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	replacementPolicy := updaterPolicyForHTTPTest(replacementHostKey)
	replacementPolicy.Hosts[0].HostID = "host-02"
	replacementPolicy.Hosts[0].Name = "Host 02"
	replacementPolicy.Hosts[0].Address = "10.0.0.20"
	replacementPolicy.Targets[0] = store.UpdaterPolicyTarget{
		TargetID: "observability-02", HostID: "host-02", ServiceType: "observability", DeploymentMode: "systemd",
	}
	if _, err := policies.SaveUpdaterPolicy(t.Context(), "updater-01", policyWithToken.Revision, replacementPolicy); err != nil {
		t.Fatal(err)
	}
	agent, err := auth.GetService(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	newClaims, err := handler.systemUpdateTargetsForAgentHostClaim(t.Context(), agent, "host-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(newClaims) != 0 {
		t.Fatalf("pending replacement policy enabled new claims from old applied mapping: %#v", newClaims)
	}
	recoveryRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/claim",
		strings.NewReader(`{"service_id":"updater-01","host_id":"host-01","active_job_id":"`+job.ID+`"}`),
	)
	recoveryRequest.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	recoveryResult := httptest.NewRecorder()
	handler.ServeHTTP(recoveryResult, recoveryRequest)
	if recoveryResult.Code != http.StatusOK {
		t.Fatalf("active rev1 recovery under pending rev2 status = %d body = %s", recoveryResult.Code, recoveryResult.Body.String())
	}
	var recovery struct {
		ReleaseToken string                `json:"release_token"`
		Job          store.SystemUpdateJob `json:"job"`
	}
	if err := json.NewDecoder(recoveryResult.Body).Decode(&recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.ReleaseToken != releaseToken || recovery.Job.ID != job.ID || recovery.Job.Status != store.SystemUpdateStatusReconciling {
		t.Fatalf("active rev1 recovery under pending rev2 = %#v", recovery)
	}
}

func TestSystemUpdateClaimClearsStaleCursorWithoutReleaseToken(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	agentToken := registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01",
		PublicURL: "https://worker.example.com", Version: "v1.0.0",
	})

	now := time.Now().UTC()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policies := store.NewMemoryUpdaterPolicyStore()
	releaseToken := "github_pat_cursor_clear"
	policy, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&releaseToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := func(revision int64) {
		t.Helper()
		if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{
			ServiceID: "updater-01", Status: "online", Version: "v1.0.0",
			Capabilities: map[string]any{
				"policy_revision":   revision,
				"policy_status":     "applied",
				"managed_targets":   []any{"worker-01"},
				"deployment_modes":  map[string]any{"worker-01": "systemd"},
				"target_hosts":      map[string]any{"worker-01": "host-01"},
				"deployed_versions": map[string]any{"worker-01": "v1.0.0"},
				"host_statuses":     map[string]any{"host-01": "reachable"},
				"host_checked_at":   map[string]any{"host-01": now.Format(time.RFC3339Nano)},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat(policy.Revision)

	updates := store.NewMemorySystemUpdateStore()
	createJob := func(idempotencyKey string) store.SystemUpdateJob {
		t.Helper()
		job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
			TargetID: "worker-01", TargetServiceType: "worker",
			AgentServiceID: "updater-01", ExecutionHostID: "host-01", DeploymentMode: "systemd",
			CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
			Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: idempotencyKey,
			RequestedByUserID: "admin", RequestedByUsername: "admin",
		})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	job := createJob("cursor-clear-active")
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	claim := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	initial := claim(`{"service_id":"updater-01","host_id":"host-01"}`)
	if initial.Code != http.StatusOK {
		t.Fatalf("initial claim status = %d body = %s", initial.Code, initial.Body.String())
	}
	var initialClaim systemUpdateClaimResponse
	if err := json.NewDecoder(initial.Body).Decode(&initialClaim); err != nil {
		t.Fatal(err)
	}
	if initialClaim.Job.ID != job.ID || initialClaim.ReleaseToken != releaseToken {
		t.Fatalf("initial claim = %#v", initialClaim)
	}

	emptyToken := ""
	policyWithoutToken, tokenStatus, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		policy.Revision,
		updaterPolicyForHTTPTest(hostKey),
		&emptyToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if tokenStatus.Configured {
		t.Fatalf("release token remained configured: %#v", tokenStatus)
	}
	heartbeat(policyWithoutToken.Revision)

	beforeActiveRetry, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	activeRetry := claim(
		`{"service_id":"updater-01","host_id":"host-01","active_job_id":"` + job.ID + `"}`,
	)
	if activeRetry.Code != http.StatusConflict ||
		!strings.Contains(activeRetry.Body.String(), `"code":"updater_release_token_not_configured"`) {
		t.Fatalf("active claim without token status = %d body = %s", activeRetry.Code, activeRetry.Body.String())
	}
	afterActiveRetry, err := updates.GetActiveSystemUpdateJob(t.Context(), job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if afterActiveRetry.Status != beforeActiveRetry.Status ||
		afterActiveRetry.LeaseGeneration != beforeActiveRetry.LeaseGeneration ||
		!afterActiveRetry.UpdatedAt.Equal(beforeActiveRetry.UpdatedAt) {
		t.Fatalf("active claim without token mutated job: before=%#v after=%#v", beforeActiveRetry, afterActiveRetry)
	}

	completed, applied, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{
		AgentServiceID: "updater-01",
		LeaseToken:     initialClaim.LeaseToken, LeaseGeneration: initialClaim.LeaseGeneration,
		Sequence: initialClaim.ReportSequence, Status: store.SystemUpdateStatusSucceeded, Progress: 100,
	}, now.Add(time.Minute), systemUpdateExecutionLeaseTTL)
	if err != nil || !applied || completed.Status != store.SystemUpdateStatusSucceeded {
		t.Fatalf("terminal report = %#v applied=%v err=%v", completed, applied, err)
	}

	terminalCursor := claim(
		`{"service_id":"updater-01","host_id":"host-01","active_job_id":"` + job.ID + `"}`,
	)
	if terminalCursor.Code != http.StatusOK ||
		strings.TrimSpace(terminalCursor.Body.String()) != `{"clear_active_job_id":true}` {
		t.Fatalf("terminal cursor without token status = %d body = %s", terminalCursor.Code, terminalCursor.Body.String())
	}
	missingCursor := claim(
		`{"service_id":"updater-01","host_id":"host-01","active_job_id":"missing-job"}`,
	)
	if missingCursor.Code != http.StatusOK ||
		strings.TrimSpace(missingCursor.Body.String()) != `{"clear_active_job_id":true}` {
		t.Fatalf("missing cursor without token status = %d body = %s", missingCursor.Code, missingCursor.Body.String())
	}

	queued := createJob("cursor-clear-next")
	newClaim := claim(`{"service_id":"updater-01","host_id":"host-01"}`)
	if newClaim.Code != http.StatusConflict ||
		!strings.Contains(newClaim.Body.String(), `"code":"updater_release_token_not_configured"`) {
		t.Fatalf("new claim without token status = %d body = %s", newClaim.Code, newClaim.Body.String())
	}
	jobs, err := updates.ListSystemUpdateJobs(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var queuedAfter store.SystemUpdateJob
	for _, candidate := range jobs {
		if candidate.ID == queued.ID {
			queuedAfter = candidate
			break
		}
	}
	if queuedAfter.ID != queued.ID || queuedAfter.Status != store.SystemUpdateStatusQueued ||
		queuedAfter.LeaseGeneration != 0 || !queuedAfter.UpdatedAt.Equal(queued.UpdatedAt) {
		t.Fatalf("new claim without token mutated queued job: before=%#v after=%#v", queued, queuedAfter)
	}
}

func TestManagedRecoveryRequiresFreshAuthenticatedHeartbeat(t *testing.T) {
	t.Setenv("AUTOSTREAM_NODE_HEARTBEAT_OFFLINE_AFTER", "3m")
	auth := store.NewMemoryAuthStore()
	agentToken := registerUpdateAgentForPolicyTest(t, auth, "updater-01")
	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker 01",
		PublicURL: "https://worker.example.com", Version: "v1.0.0",
	})

	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policies := store.NewMemoryUpdaterPolicyStore()
	releaseToken := "github_pat_recovery_heartbeat"
	policy, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&releaseToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	capabilities := map[string]any{
		"policy_revision":   policy.Revision,
		"policy_status":     "applied",
		"managed_targets":   []any{"worker-01"},
		"deployment_modes":  map[string]any{"worker-01": "systemd"},
		"target_hosts":      map[string]any{"worker-01": "host-01"},
		"deployed_versions": map[string]any{"worker-01": "v1.0.0"},
		"host_statuses":     map[string]any{"host-01": "reachable"},
		"host_checked_at":   map[string]any{"host-01": now.Format(time.RFC3339Nano)},
	}
	if _, err := auth.Heartbeat(t.Context(), agentToken, store.ServiceHeartbeat{
		ServiceID: "updater-01", Status: "online", Version: "v1.0.0", Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}

	updates := store.NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker",
		AgentServiceID: "updater-01", ExecutionHostID: "host-01", DeploymentMode: "systemd",
		CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "heartbeat-recovery",
		RequestedByUserID: "admin", RequestedByUsername: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	services := &staleUntilAuthenticatedHeartbeatStore{
		MemoryAuthStore: auth,
		serviceID:       "updater-01",
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(services),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	requestClaim := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/claim", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	initial := requestClaim(`{"service_id":"updater-01","host_id":"host-01"}`)
	if initial.Code != http.StatusOK {
		t.Fatalf("initial claim status = %d body = %s", initial.Code, initial.Body.String())
	}
	var initialClaim systemUpdateClaimResponse
	if err := json.NewDecoder(initial.Body).Decode(&initialClaim); err != nil {
		t.Fatal(err)
	}
	if initialClaim.Job.ID != job.ID || initialClaim.ReleaseToken != releaseToken ||
		initialClaim.RecoveryRequired {
		t.Fatalf("initial claim = %#v", initialClaim)
	}

	services.markStale()
	staleFreshClaim := requestClaim(`{"service_id":"updater-01","host_id":"host-01"}`)
	if staleFreshClaim.Code != http.StatusConflict ||
		!strings.Contains(staleFreshClaim.Body.String(), `"code":"updater_offline"`) {
		t.Fatalf("stale fresh claim status = %d body = %s", staleFreshClaim.Code, staleFreshClaim.Body.String())
	}
	active, err := updates.GetActiveSystemUpdateJob(t.Context(), "worker-01")
	if err != nil || active.ID != job.ID || active.Status != store.SystemUpdateStatusClaimed {
		t.Fatalf("stale fresh claim mutated active job = %#v, %v", active, err)
	}

	heartbeatBody, err := json.Marshal(store.ServiceHeartbeat{
		ServiceID: "updater-01", Status: "online", Version: "v1.0.0", Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := httptest.NewRequest(http.MethodPost, "/services/heartbeat", bytes.NewReader(heartbeatBody))
	heartbeat.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	heartbeatResult := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatResult, heartbeat)
	if heartbeatResult.Code != http.StatusAccepted {
		t.Fatalf("authenticated heartbeat status = %d body = %s", heartbeatResult.Code, heartbeatResult.Body.String())
	}

	recovery := requestClaim(
		`{"service_id":"updater-01","host_id":"host-01","active_job_id":"` + job.ID + `"}`,
	)
	if recovery.Code != http.StatusOK {
		t.Fatalf("recovery claim after heartbeat status = %d body = %s", recovery.Code, recovery.Body.String())
	}
	var recovered systemUpdateClaimResponse
	if err := json.NewDecoder(recovery.Body).Decode(&recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Job.ID != job.ID || recovered.Job.Status != store.SystemUpdateStatusReconciling ||
		!recovered.RecoveryRequired || recovered.ReleaseToken != releaseToken ||
		recovered.LeaseGeneration <= initialClaim.LeaseGeneration {
		t.Fatalf("recovery claim after heartbeat = %#v", recovered)
	}
}

func TestManagedUpdaterTopologyRequiresAppliedRevisionAndReleaseToken(t *testing.T) {
	now := time.Now().UTC()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	clientKey, clientFingerprint := ed25519AuthorizedKeyForTest(t, "")
	policy := updaterPolicyForHTTPTest(hostKey)
	policy.UpdaterID = "updater-01"
	policy.Revision = 2
	policy.ProjectionRevision = 2
	agent := store.RegisteredService{
		ServiceID: "updater-01", ServiceType: "update_agent", ServiceName: "Updater", Status: "online",
		Version: "v1.0.0", ReportedVersion: "v1.0.0", LastHeartbeatAt: &now,
		ReportedCapabilities: map[string]any{
			"policy_revision":             2,
			"policy_status":               "applied",
			"managed_targets":             []string{"worker-01"},
			"deployment_modes":            map[string]string{"worker-01": "systemd"},
			"target_hosts":                map[string]string{"worker-01": "host-01"},
			"deployed_versions":           map[string]string{"worker-01": "v1.0.0"},
			"host_statuses":               map[string]string{"host-01": "reachable"},
			"host_checked_at":             map[string]string{"host-01": now.Format(time.RFC3339Nano)},
			"ssh_client_public_keys":      map[string]string{"host-01": clientKey, "unmanaged-host": clientKey},
			"ssh_client_key_fingerprints": map[string]string{"host-01": "untrusted-reported-value"},
		},
	}
	assignments, updaters, hosts := systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{"updater-01": policy},
		false,
	)
	assignment := assignments["worker-01"]
	if !assignment.PolicyReady || !assignment.ReleaseTokenRequired || assignment.ReleaseTokenConfigured {
		t.Fatalf("managed assignment without release token = %#v", assignment)
	}
	if len(updaters) != 1 || updaters[0].DesiredRevision != 2 || updaters[0].AppliedRevision != 2 || updaters[0].PolicyStatus != "applied" ||
		updaters[0].SSHClientKeyFingerprints["host-01"] != clientFingerprint {
		t.Fatalf("managed updater status = %#v", updaters)
	}
	if _, exposed := updaters[0].SSHClientPublicKeys["unmanaged-host"]; exposed {
		t.Fatalf("updater response exposed an unmanaged SSH client key: %#v", updaters[0].SSHClientPublicKeys)
	}
	if len(hosts) != 1 || hosts[0].SSHClientKeyFingerprint != clientFingerprint {
		t.Fatalf("managed host status = %#v", hosts)
	}
	checks := map[string]serviceUpdateInfoResponse{"worker": {LatestVersion: "v1.1.0", ManifestVerified: true}}
	target := buildSystemUpdateTarget("worker-01", "worker", "Worker 01", "v1.0.0", "", false, assignment, checks)
	if target.Eligible || target.BlockedReason != "updater_release_token_not_configured" {
		t.Fatalf("managed target without release token = %#v", target)
	}

	assignments, _, _ = systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{"updater-01": policy},
		true,
	)
	target = buildSystemUpdateTarget("worker-01", "worker", "Worker 01", "v1.0.0", "", false, assignments["worker-01"], checks)
	if !target.Eligible || target.BlockedReason != "" {
		t.Fatalf("managed target with applied policy and release token = %#v", target)
	}

	agent.ReportedCapabilities["policy_revision"] = 1
	assignments, updaters, _ = systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{"updater-01": policy},
		true,
	)
	target = buildSystemUpdateTarget("worker-01", "worker", "Worker 01", "v1.0.0", "", false, assignments["worker-01"], checks)
	if target.Eligible || target.BlockedReason != "updater_policy_pending" || updaters[0].AppliedRevision != 1 {
		t.Fatalf("managed target with stale applied revision = %#v updater=%#v", target, updaters[0])
	}
	agent.ReportedCapabilities["policy_status"] = "pending"
	agent.ReportedCapabilities["policy_error_code"] = "active_job_pending"
	_, updaters, _ = systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{"updater-01": policy},
		true,
	)
	if updaters[0].PolicyStatus != "pending" || updaters[0].PolicyErrorCode != "active_job_pending" {
		t.Fatalf("active-job policy deferral was not exposed safely: %#v", updaters[0])
	}

	agent.ReportedCapabilities["policy_revision"] = 2
	agent.ReportedCapabilities["policy_status"] = "failed"
	agent.ReportedCapabilities["policy_error_code"] = "raw internal failure detail"
	_, updaters, _ = systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{"updater-01": policy},
		true,
	)
	if updaters[0].PolicyStatus != "failed" || updaters[0].PolicyErrorCode != "" {
		t.Fatalf("unbounded policy error escaped updater response: %#v", updaters[0])
	}
	agent.ReportedCapabilities["policy_error_code"] = "ssh_identity_failed"
	_, updaters, _ = systemUpdateAgentTopologyWithPolicies(
		[]store.RegisteredService{agent},
		now,
		map[string]store.UpdaterPolicy{"updater-01": policy},
		true,
	)
	if updaters[0].PolicyErrorCode != "ssh_identity_failed" {
		t.Fatalf("safe policy error code was dropped: %#v", updaters[0])
	}
}

func registerUpdateAgentForPolicyTest(t *testing.T, auth *store.MemoryAuthStore, serviceID string) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: serviceID, ServiceType: "update_agent", ServiceName: "Updater", PublicURL: "https://updater.example.com", Version: "v1.0.0",
	})
	return token
}

func updaterPolicyForHTTPTest(hostPublicKey string) store.UpdaterPolicy {
	return store.UpdaterPolicy{
		API:                 store.UpdaterPolicyAPI{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090},
		PollIntervalSeconds: 15, HeartbeatIntervalSeconds: 30,
		Hosts: []store.UpdaterPolicyHost{{
			HostID: "host-01", Name: "Host 01", Address: "10.0.0.10", Port: 55850,
			User: "autostream-update-host", Arch: "amd64", HostPublicKey: hostPublicKey,
		}},
		Targets: []store.UpdaterPolicyTarget{{
			TargetID: "worker-01", HostID: "host-01", ServiceType: "worker", DeploymentMode: "systemd",
		}},
	}
}

func updaterPolicyTargetRequestsForTest(targets []store.UpdaterPolicyTarget) []updaterPolicyTargetRequest {
	requests := make([]updaterPolicyTargetRequest, 0, len(targets))
	for _, target := range targets {
		requests = append(requests, updaterPolicyTargetRequest{
			TargetID:       target.TargetID,
			ServiceID:      target.ServiceID,
			HostID:         target.HostID,
			ServiceType:    target.ServiceType,
			DeploymentMode: target.DeploymentMode,
		})
	}
	return requests
}

func ed25519AuthorizedKeyForTest(t *testing.T, comment string) (string, string) {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey)))
	if strings.TrimSpace(comment) != "" {
		authorized += " " + strings.TrimSpace(comment)
	}
	return authorized, ssh.FingerprintSHA256(sshPublicKey)
}

func rsaAuthorizedKeyForTest(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
}

type staleUntilAuthenticatedHeartbeatStore struct {
	*store.MemoryAuthStore
	mu        sync.Mutex
	serviceID string
	stale     bool
}

func (s *staleUntilAuthenticatedHeartbeatStore) markStale() {
	s.mu.Lock()
	s.stale = true
	s.mu.Unlock()
}

func (s *staleUntilAuthenticatedHeartbeatStore) GetService(ctx context.Context, serviceID string) (store.RegisteredService, error) {
	service, err := s.MemoryAuthStore.GetService(ctx, serviceID)
	if err != nil {
		return store.RegisteredService{}, err
	}
	s.mu.Lock()
	stale := s.stale && serviceID == s.serviceID
	s.mu.Unlock()
	if stale {
		staleAt := time.Unix(1, 0).UTC()
		service.LastHeartbeatAt = &staleAt
	}
	return service, nil
}

func (s *staleUntilAuthenticatedHeartbeatStore) Heartbeat(ctx context.Context, token store.ServiceToken, heartbeat store.ServiceHeartbeat) (store.RegisteredService, error) {
	service, err := s.MemoryAuthStore.Heartbeat(ctx, token, heartbeat)
	if err != nil {
		return store.RegisteredService{}, err
	}
	if heartbeat.ServiceID == s.serviceID {
		s.mu.Lock()
		s.stale = false
		s.mu.Unlock()
	}
	return service, nil
}
