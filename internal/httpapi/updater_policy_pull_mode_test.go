package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestNormalizedUpdaterPolicyRequestUsesServerOwnedPullBinding(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	agent := store.RegisteredService{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  1,
	}
	policy, err := normalizedUpdaterPolicyRequest(agent, updaterPolicyUpdateRequest{
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: digest,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	})
	if err != nil {
		t.Fatalf("normalizedUpdaterPolicyRequest: %v", err)
	}
	if policy.UpdaterID != agent.ServiceID ||
		policy.TransportMode != store.SystemUpdateTransportPullV2 ||
		policy.ExecutionHostID != agent.ExecutionHostID ||
		policy.Targets[0].HostID != agent.ExecutionHostID ||
		policy.LocalExecutorPolicySHA256 != digest {
		t.Fatalf("policy = %#v", policy)
	}
}

func TestNormalizedUpdaterPolicyRequestAllowsObserveOnlyEpochZero(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	agent := store.RegisteredService{
		ServiceID:       "host-agent-observer",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  0,
		Capabilities:    map[string]any{"observe_only": true},
	}
	policy, err := normalizedUpdaterPolicyRequest(agent, updaterPolicyUpdateRequest{
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: digest,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	})
	if err != nil {
		t.Fatalf("observe-only pull policy: %v", err)
	}
	if policy.ExecutionHostID != agent.ExecutionHostID ||
		policy.TransportMode != store.SystemUpdateTransportPullV2 {
		t.Fatalf("observe-only pull policy = %#v", policy)
	}
}

func TestNormalizedUpdaterPolicyRequestRejectsPullSSHAndLongLivedReleaseToken(t *testing.T) {
	t.Parallel()

	agent := store.RegisteredService{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  1,
	}
	base := updaterPolicyUpdateRequest{
		PollIntervalSeconds:      15,
		HeartbeatIntervalSeconds: 30,
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	token := "long-lived-token"
	tests := map[string]func(*updaterPolicyUpdateRequest){
		"SSH host": func(body *updaterPolicyUpdateRequest) {
			body.Hosts = validUpdaterPolicyRequest(t).Hosts
		},
		"inbound API": func(body *updaterPolicyUpdateRequest) {
			body.API.Port = 8090
		},
		"release token": func(body *updaterPolicyUpdateRequest) {
			body.GitHubToken = &token
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Targets = append([]store.UpdaterPolicyTarget(nil), base.Targets...)
			mutate(&candidate)
			if _, err := normalizedUpdaterPolicyRequest(agent, candidate); err == nil {
				t.Fatal("unsafe pull settings were accepted")
			}
		})
	}
}

func TestPullUpdaterPolicySaveNeedsNoSecretPermissionAndAdvancesOwnershipRevision(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "pull-policy-admin", Username: "pull-policy-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-a",
		"host-a",
		1,
		map[string]any{},
	)
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	sharedReleaseToken := "github_pat_ssh_only"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"legacy-updater",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&sharedReleaseToken,
	); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
		0,
		store.SystemUpdateTransportPullV2,
		"host-agent-a",
		0,
	); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "pull-policy-admin", "correct horse battery")
	requestBody := updaterPolicyUpdateRequest{
		ExpectedRevision:          0,
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "legacy-slot",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	save := func(body updaterPolicyUpdateRequest) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodPut,
			"/system-updates/updaters/host-agent-a/settings",
			bytes.NewReader(payload),
		)
		request.AddCookie(cookie)
		request.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	created := save(requestBody)
	if created.Code != http.StatusOK {
		t.Fatalf("create pull policy = %d %s", created.Code, created.Body.String())
	}
	var createdBody map[string]any
	if err := json.NewDecoder(created.Body).Decode(&createdBody); err != nil {
		t.Fatal(err)
	}
	for _, forbiddenField := range []string{"api", "hosts", "github_token_configured", "github_token_fingerprint"} {
		if _, exists := createdBody[forbiddenField]; exists {
			t.Fatalf("pull response exposed %q: %#v", forbiddenField, createdBody)
		}
	}
	if createdBody["transport_mode"] != store.SystemUpdateTransportPullV2 ||
		createdBody["execution_host_id"] != "host-a" ||
		createdBody["revision"] != float64(1) {
		t.Fatalf("pull response identity = %#v", createdBody)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.OwnershipEpoch != 1 || ownership.PolicyRevision != 1 {
		t.Fatalf("ownership after create = %#v", ownership)
	}
	storedToken, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || storedToken != sharedReleaseToken {
		t.Fatalf("pull create touched shared release token: %q, %v", storedToken, err)
	}

	requestBody.ExpectedRevision = 1
	requestBody.PollIntervalSeconds = 20
	updated := save(requestBody)
	if updated.Code != http.StatusOK {
		t.Fatalf("update pull policy = %d %s", updated.Code, updated.Body.String())
	}
	ownership, err = updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.OwnershipEpoch != 1 || ownership.PolicyRevision != 2 {
		t.Fatalf("ownership after update = %#v", ownership)
	}

	requestBody.ExpectedRevision = 1
	conflict := save(requestBody)
	if conflict.Code != http.StatusConflict ||
		!strings.Contains(conflict.Body.String(), "updater_policy_revision_conflict") {
		t.Fatalf("stale pull policy save = %d %s", conflict.Code, conflict.Body.String())
	}
	stored, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-a")
	if err != nil || stored.Revision != 2 {
		t.Fatalf("stale save mutated policy = %#v, %v", stored, err)
	}
	ownership, err = updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil || ownership.PolicyRevision != 2 {
		t.Fatalf("stale save mutated ownership = %#v, %v", ownership, err)
	}

	if _, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    "host-agent-a",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "block-policy-while-active",
		RequestedByUserID: "pull-policy-admin",
	}); err != nil {
		t.Fatal(err)
	}
	requestBody.ExpectedRevision = 2
	busy := save(requestBody)
	if busy.Code != http.StatusConflict ||
		!strings.Contains(busy.Body.String(), "system_update_execution_host_busy") {
		t.Fatalf("active-job pull policy save = %d %s", busy.Code, busy.Body.String())
	}
}

func TestPullObserverPolicySavePreservesExistingSSHOwnershipAndActiveJob(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "observer-policy-admin", Username: "observer-policy-admin"},
		"correct horse battery",
		[]string{"system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"host-agent-observer",
		"host-a",
		0,
		map[string]any{"observe_only": true},
	)
	policies := store.NewMemoryUpdaterPolicyStore()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	sharedReleaseToken := "github_pat_ssh_observer"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"legacy-updater",
		0,
		updaterPolicyForHTTPTest(hostKey),
		&sharedReleaseToken,
	); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	sshOwnership, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		"host-a",
		0,
		store.SystemUpdateTransportSSHV1,
		"central-updater",
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    "central-updater",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "ssh-job-survives-observer-policy",
		RequestedByUserID: "observer-policy-admin",
	}); err != nil || !created {
		t.Fatalf("create SSH-owned job: created=%v err=%v", created, err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "observer-policy-admin", "correct horse battery")
	body, err := json.Marshal(updaterPolicyUpdateRequest{
		ExpectedRevision:          0,
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("c", 64),
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "worker-slot",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPut,
		"/system-updates/updaters/host-agent-observer/settings",
		bytes.NewReader(body),
	)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("observer pull policy save = %d %s", response.Code, response.Body.String())
	}
	after, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if after != sshOwnership {
		t.Fatalf("observer policy changed SSH ownership: before=%#v after=%#v", sshOwnership, after)
	}
	policy, err := policies.GetUpdaterPolicy(t.Context(), "host-agent-observer")
	if err != nil || policy.Revision != 1 {
		t.Fatalf("observer policy = %#v, %v", policy, err)
	}
	token, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || token != sharedReleaseToken {
		t.Fatalf("observer policy touched release token: %q, %v", token, err)
	}
	for _, forbiddenField := range []string{`"api"`, `"hosts"`, `"github_token_`} {
		if strings.Contains(response.Body.String(), forbiddenField) {
			t.Fatalf("observer response exposed SSH field %q: %s", forbiddenField, response.Body.String())
		}
	}
}

func TestUpdaterPolicyResponseUsesCurrentPullAgentBindingAndStripsSSHFields(t *testing.T) {
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := updaterPolicyForHTTPTest(hostKey)
	policy.UpdaterID = "host-agent-a"
	policy.Revision = 3
	policy.TransportMode = store.SystemUpdateTransportSSHV1
	policy.Targets[0].ServiceID = "worker-a"
	agent := store.RegisteredService{
		ServiceID:       policy.UpdaterID,
		ServiceType:     "update_agent",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-a",
		OwnershipEpoch:  2,
	}
	tokenConfigured := store.SecretStatus{Configured: true, Fingerprint: "must-not-leak"}
	encoded, err := json.Marshal(makeUpdaterPolicyResponse(policy, &tokenConfigured, &agent, true))
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	if response["transport_mode"] != store.SystemUpdateTransportPullV2 ||
		response["execution_host_id"] != "host-a" {
		t.Fatalf("response did not use current server-owned binding: %s", encoded)
	}
	for _, forbiddenField := range []string{"api", "hosts", "github_token_configured", "github_token_fingerprint"} {
		if _, exists := response[forbiddenField]; exists {
			t.Fatalf("pull response exposed stale SSH field %q: %s", forbiddenField, encoded)
		}
	}
	targets, ok := response["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("pull response targets = %#v", response["targets"])
	}
	target, ok := targets[0].(map[string]any)
	if !ok || target["service_id"] != "worker-a" || target["host_id"] != "host-a" {
		t.Fatalf("pull response target binding = %#v", targets[0])
	}
}

func validUpdaterPolicyRequest(t *testing.T) updaterPolicyUpdateRequest {
	t.Helper()
	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policy := updaterPolicyForHTTPTest(hostKey)
	return updaterPolicyUpdateRequest{
		API:                      policy.API,
		PollIntervalSeconds:      policy.PollIntervalSeconds,
		HeartbeatIntervalSeconds: policy.HeartbeatIntervalSeconds,
		Hosts:                    policy.Hosts,
		Targets:                  policy.Targets,
	}
}
