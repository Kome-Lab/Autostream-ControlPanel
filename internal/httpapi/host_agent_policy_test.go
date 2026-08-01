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

func TestHostAgentPolicyUsesServerOwnedBindingAndRegisteredServiceEndpoints(t *testing.T) {
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "worker.example.com")
	t.Setenv("AUTOSTREAM_BIND_ADDR", "0.0.0.0:80")
	t.Setenv("AUTOSTREAM_CONFIG_REVISION", "0")
	auth := store.NewMemoryAuthStore()
	agentToken, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), agentToken, store.ServiceRegistration{
		ServiceID:       "host-agent-01",
		ServiceType:     "update_agent",
		ServiceName:     "Host Agent 01",
		TransportMode:   "pull_v2",
		ExecutionHostID: "host-01",
		OwnershipEpoch:  3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), agentToken, store.ServiceRegistration{
		ServiceID:   "host-agent-01",
		ServiceType: "update_agent",
		ServiceName: "Host Agent 01",
	}); err != nil {
		t.Fatal(err)
	}

	workerToken, err := auth.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	workerRegistration := store.ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker 01",
		Host:        "worker.example.com",
		Port:        18084,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:18084",
	}
	if _, err := auth.PrecreateService(t.Context(), workerToken, workerRegistration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), workerToken, workerRegistration); err != nil {
		t.Fatal(err)
	}

	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policies := store.NewMemoryUpdaterPolicyStore()
	policy := updaterPolicyForHTTPTest(hostKey)
	policy.TransportMode = store.SystemUpdateTransportPullV2
	policy.ExecutionHostID = "host-01"
	policy.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("a", 64)
	policy.API = store.UpdaterPolicyAPI{}
	policy.Hosts = nil
	policy.Targets[0].ServiceID = "worker-01"
	policy.Targets[0].HostID = "host-01"
	policy.Targets[0].LocalListenPort = 18084
	updates := store.NewMemorySystemUpdateStore()
	for expectedEpoch := int64(0); expectedEpoch < 3; expectedEpoch++ {
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			t.Context(),
			"host-01",
			expectedEpoch,
			store.SystemUpdateTransportPullV2,
			"host-agent-01",
			0,
		); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := policies.SavePullUpdaterPolicy(t.Context(), updates, "host-agent-01", 0, 3, policy)
	if err != nil {
		t.Fatal(err)
	}

	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/policy",
		bytes.NewBufferString(`{"service_id":"host-agent-01","current_revision":0}`),
	)
	request.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("host agent policy status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", response.Header().Get("Cache-Control"))
	}
	var body struct {
		ServiceID                   string `json:"service_id"`
		TransportMode               string `json:"transport_mode"`
		ExecutionHostID             string `json:"execution_host_id"`
		OwnershipEpoch              int64  `json:"ownership_epoch"`
		Revision                    int64  `json:"revision"`
		SourcePolicyRevision        int64  `json:"source_policy_revision"`
		LocalExecutorPolicyRevision int64  `json:"local_executor_policy_revision"`
		ObserveOnly                 bool   `json:"observe_only"`
		LocalExecutorPolicySHA256   string `json:"local_executor_policy_sha256"`
		Targets                     []struct {
			ServiceID             string                 `json:"service_id"`
			ServiceType           string                 `json:"service_type"`
			DeploymentMode        string                 `json:"deployment_mode"`
			AppliedConfigRevision int64                  `json:"applied_config_revision"`
			AppliedConfigSHA256   string                 `json:"applied_config_sha256"`
			DesiredEndpoint       *store.ServiceEndpoint `json:"desired_endpoint"`
			AppliedEndpoint       *store.ServiceEndpoint `json:"applied_endpoint"`
			LocalListenEndpoint   *store.ServiceEndpoint `json:"local_listen_endpoint"`
			LocalHealthEndpoint   *store.ServiceEndpoint `json:"local_health_endpoint"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ServiceID != "host-agent-01" ||
		body.TransportMode != "pull_v2" ||
		body.ExecutionHostID != "host-01" ||
		body.OwnershipEpoch != 3 ||
		body.Revision != saved.ProjectionRevision ||
		body.SourcePolicyRevision != saved.Revision ||
		body.LocalExecutorPolicyRevision != saved.LocalExecutorPolicyRevision ||
		body.LocalExecutorPolicySHA256 != policy.LocalExecutorPolicySHA256 ||
		body.ObserveOnly {
		t.Fatalf("unexpected host agent policy binding: %#v", body)
	}
	if len(body.Targets) != 1 ||
		body.Targets[0].ServiceID != "worker-01" ||
		body.Targets[0].ServiceType != "worker" ||
		body.Targets[0].DeploymentMode != "systemd" ||
		body.Targets[0].AppliedConfigRevision != 1 ||
		body.Targets[0].DesiredEndpoint == nil ||
		body.Targets[0].AppliedEndpoint == nil ||
		body.Targets[0].LocalListenEndpoint == nil ||
		body.Targets[0].LocalHealthEndpoint == nil ||
		body.Targets[0].DesiredEndpoint.Port != 18084 ||
		body.Targets[0].AppliedEndpoint.Port != 18084 ||
		body.Targets[0].LocalListenEndpoint.Host != "127.0.0.1" ||
		body.Targets[0].LocalListenEndpoint.Port != 18084 ||
		body.Targets[0].LocalListenEndpoint.SSLEnabled ||
		body.Targets[0].LocalListenEndpoint.PublicURL != "http://127.0.0.1:18084" ||
		*body.Targets[0].LocalHealthEndpoint != *body.Targets[0].LocalListenEndpoint {
		t.Fatalf("unexpected host agent policy targets: %#v", body.Targets)
	}

	if _, err := auth.UpdateServiceMetadata(t.Context(), "worker-01", store.ServiceMetadataUpdate{
		ServiceName: "Worker 01",
		Host:        "worker.example.com",
		Port:        28084,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com:28084",
	}); err != nil {
		t.Fatal(err)
	}
	samePolicyRevision := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/policy",
		bytes.NewBufferString(`{"service_id":"host-agent-01","current_revision":1}`),
	)
	samePolicyRevision.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	samePolicyRevisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(samePolicyRevisionResponse, samePolicyRevision)
	if samePolicyRevisionResponse.Code != http.StatusOK {
		t.Fatalf("same-revision policy status=%d body=%s", samePolicyRevisionResponse.Code, samePolicyRevisionResponse.Body.String())
	}
	var refreshed hostAgentPolicyResponse
	if err := json.NewDecoder(samePolicyRevisionResponse.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Targets) != 1 ||
		refreshed.Targets[0].AppliedConfigRevision != 1 ||
		refreshed.Targets[0].AppliedEndpoint == nil ||
		refreshed.Targets[0].AppliedEndpoint.Port != 28084 ||
		refreshed.Targets[0].LocalListenEndpoint == nil ||
		refreshed.Targets[0].LocalListenEndpoint.Port != 18084 {
		t.Fatalf("same policy revision did not refresh target config: %#v", refreshed.Targets)
	}

	if _, err := auth.UpdateServiceMetadata(t.Context(), "worker-01", store.ServiceMetadataUpdate{
		ServiceName: "Worker 01",
		Host:        "worker.example.com",
		Port:        443,
		SSLEnabled:  true,
		PublicURL:   "https://worker.example.com",
	}); err != nil {
		t.Fatal(err)
	}
	publicHTTPSRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/policy",
		bytes.NewBufferString(`{"service_id":"host-agent-01","current_revision":1}`),
	)
	publicHTTPSRequest.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	publicHTTPSResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicHTTPSResponse, publicHTTPSRequest)
	if publicHTTPSResponse.Code != http.StatusOK {
		t.Fatalf("public HTTPS policy status=%d body=%s", publicHTTPSResponse.Code, publicHTTPSResponse.Body.String())
	}
	var publicHTTPSPolicy hostAgentPolicyResponse
	if err := json.NewDecoder(publicHTTPSResponse.Body).Decode(&publicHTTPSPolicy); err != nil {
		t.Fatal(err)
	}
	if len(publicHTTPSPolicy.Targets) != 1 ||
		publicHTTPSPolicy.Targets[0].AppliedEndpoint == nil ||
		publicHTTPSPolicy.Targets[0].AppliedEndpoint.Port != 443 ||
		publicHTTPSPolicy.Targets[0].LocalListenEndpoint == nil ||
		publicHTTPSPolicy.Targets[0].LocalListenEndpoint.Port != 18084 {
		t.Fatalf("public and local endpoints were conflated: %#v", publicHTTPSPolicy.Targets)
	}
	dockerPolicy := saved
	dockerPolicy.Targets = append(
		[]store.UpdaterPolicyTarget(nil), saved.Targets...,
	)
	dockerPolicy.Targets[0].DeploymentMode = "docker"
	dockerPolicy.Targets[0].LocalListenPort = 0
	savedDocker, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-01",
		saved.Revision,
		3,
		dockerPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	dockerRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/policy",
		bytes.NewBufferString(`{"service_id":"host-agent-01","current_revision":0}`),
	)
	dockerRequest.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	dockerResponse := httptest.NewRecorder()
	handler.ServeHTTP(dockerResponse, dockerRequest)
	if dockerResponse.Code != http.StatusOK {
		t.Fatalf(
			"Docker host agent policy status=%d body=%s",
			dockerResponse.Code,
			dockerResponse.Body.String(),
		)
	}
	var dockerBody hostAgentPolicyResponse
	if err := json.NewDecoder(dockerResponse.Body).Decode(&dockerBody); err != nil {
		t.Fatal(err)
	}
	if dockerBody.Revision != savedDocker.ProjectionRevision ||
		len(dockerBody.Targets) != 1 ||
		dockerBody.Targets[0].DeploymentMode != "docker" ||
		dockerBody.Targets[0].AppliedEndpoint == nil ||
		dockerBody.Targets[0].AppliedEndpoint.Port != 443 ||
		dockerBody.Targets[0].LocalListenEndpoint != nil ||
		dockerBody.Targets[0].LocalHealthEndpoint != nil {
		t.Fatalf(
			"Docker policy fabricated an untrusted local endpoint: %#v",
			dockerBody,
		)
	}
}

func TestHostAgentPolicyAllowsEpochZeroObserverWithoutClaimingSSHOwnership(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	agentToken, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		[]string{"service.register", "service.heartbeat", "service.config.read"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), agentToken, store.ServiceRegistration{
		ServiceID:       "host-agent-observer",
		ServiceType:     "update_agent",
		ServiceName:     "Host Agent Observer",
		TransportMode:   store.SystemUpdateTransportPullV2,
		ExecutionHostID: "host-observed",
		OwnershipEpoch:  0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), agentToken, store.ServiceRegistration{
		ServiceID:   "host-agent-observer",
		ServiceType: "update_agent",
		ServiceName: "Host Agent Observer",
		Capabilities: map[string]any{
			"host_agent":   true,
			"observe_only": true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	hostKey, _ := ed25519AuthorizedKeyForTest(t, "")
	policies := store.NewMemoryUpdaterPolicyStore()
	policy := updaterPolicyForHTTPTest(hostKey)
	policy.TransportMode = store.SystemUpdateTransportPullV2
	policy.ExecutionHostID = "host-observed"
	policy.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("b", 64)
	policy.API = store.UpdaterPolicyAPI{}
	policy.Hosts = nil
	policy.Targets[0].ServiceID = policy.Targets[0].TargetID
	policy.Targets[0].HostID = "host-observed"
	updates := store.NewMemorySystemUpdateStore()
	before, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-observed")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-observer",
		0,
		0,
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := updates.GetSystemUpdateExecutionHost(t.Context(), "host-observed")
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("observer policy save mutated execution ownership:\nbefore=%#v\nafter=%#v", before, after)
	}

	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/policy",
		bytes.NewBufferString(`{"service_id":"host-agent-observer","current_revision":0}`),
	)
	request.Header.Set("Authorization", "Bearer "+agentToken.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("observer policy status=%d body=%s", response.Code, response.Body.String())
	}
	var body hostAgentPolicyResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ServiceID != "host-agent-observer" ||
		body.ExecutionHostID != "host-observed" ||
		body.OwnershipEpoch != 0 ||
		body.Revision != saved.Revision ||
		!body.ObserveOnly {
		t.Fatalf("unexpected observer policy binding: %#v", body)
	}
}

func TestHostAgentPolicyRejectsClientSelectedIdentity(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{"service.register", "service.heartbeat", "service.config.read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PrecreateService(t.Context(), token, store.ServiceRegistration{
		ServiceID:       "host-agent-a",
		ServiceType:     "update_agent",
		ServiceName:     "Host Agent A",
		TransportMode:   "pull_v2",
		ExecutionHostID: "host-a",
		OwnershipEpoch:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, store.ServiceRegistration{
		ServiceID:   "host-agent-a",
		ServiceType: "update_agent",
		ServiceName: "Host Agent A",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth))
	request := httptest.NewRequest(
		http.MethodPost,
		"/services/host-agent/policy",
		bytes.NewBufferString(`{"service_id":"host-agent-b","current_revision":0}`),
	)
	request.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-agent policy status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHostAgentPolicyStrictBoundedRequest(t *testing.T) {
	fixture := newRuntimeTokenRotationHTTPFixture(t)
	valid := `{"service_id":"host-agent-a","current_revision":0}`
	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown field",
			body: `{"service_id":"host-agent-a","current_revision":0,"host_id":"attacker"}`,
		},
		{
			name: "trailing json",
			body: valid + `{}`,
		},
		{
			name: "valid prefix with oversized trailing bytes",
			body: valid + strings.Repeat(" ", maxHostAgentControlRequestBytes),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := fixture.serviceRequest(
				t,
				fixture.oldToken.RawToken,
				"/services/host-agent/policy",
				tt.body,
			)
			if response.Code != http.StatusBadRequest ||
				!strings.Contains(response.Body.String(), `"code":"bad_request"`) {
				t.Fatalf(
					"strict policy status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf(
					"strict policy cache control=%q",
					response.Header().Get("Cache-Control"),
				)
			}
		})
	}
}
