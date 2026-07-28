package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateagent"
)

type portCoordinatorCaptureStore struct {
	store.SystemUpdateStore
	services     store.ServiceRegistryStore
	policies     store.UpdaterPolicyStore
	params       store.CreateSystemdPortReconfigurationJobParams
	dockerParams store.CreateDockerPortReconfigurationJobParams
	job          *store.SystemUpdateJob
	createErr    error
	createCalls  int
	report       store.SystemUpdateReport
	reportJob    store.SystemUpdateJob
	reportErr    error
	reportCalls  int
}

func (s *portCoordinatorCaptureStore) CreateDockerPortReconfigurationJob(
	ctx context.Context,
	services store.ServiceRegistryStore,
	policies store.UpdaterPolicyStore,
	params store.CreateDockerPortReconfigurationJobParams,
) (store.SystemUpdateJob, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.SystemUpdateJob{}, false, err
	}
	s.createCalls++
	s.services = services
	s.policies = policies
	s.dockerParams = params
	if s.createErr != nil {
		return store.SystemUpdateJob{}, false, s.createErr
	}
	now := time.Now().UTC()
	job := store.SystemUpdateJob{
		ID: "docker-port-job-1", TargetID: params.TargetID,
		TargetServiceType: "worker",
		Operation:         store.SystemUpdateOperationPortReconfigure,
		PortReconfigure: &store.SystemUpdatePortReconfiguration{
			NetworkNamespace:         "host",
			Protocol:                 store.SystemUpdatePortProtocolTCP,
			OldPort:                  8081,
			NewPort:                  params.NewAdvertisedPort,
			ExpectedEndpointRevision: params.ExpectedEndpointRevision,
			TargetEndpointRevision:   params.ExpectedEndpointRevision + 1,
			Docker: &store.SystemUpdateDockerPortReconfiguration{
				PublishedHostIP:  "127.0.0.1",
				OldPublishedPort: 18081, NewPublishedPort: params.NewPublishedPort,
				OldContainerPort: 8080, NewContainerPort: params.NewContainerPort,
				OldHealthPort: 18081, NewHealthPort: params.NewPublishedPort,
			},
		},
		DeploymentMode: "docker", CurrentVersion: "v1.0.0",
		TargetVersion: "v1.0.0", Strategy: store.SystemUpdateStrategyMaintenance,
		Status: store.SystemUpdateStatusQueued, IdempotencyKey: params.IdempotencyKey,
		RequestedByUserID:   params.RequestedByUserID,
		RequestedByUsername: params.RequestedByUsername,
		AgentServiceID:      "host-agent-a", ExecutionHostID: "host-a",
		TransportMode:  store.SystemUpdateTransportPullV2,
		OwnershipEpoch: 2, PolicyRevision: 4,
		CreatedAt: now, UpdatedAt: now,
	}
	s.job = &job
	return job, true, nil
}

func (s *portCoordinatorCaptureStore) GetSystemUpdateJobByIdempotency(
	ctx context.Context,
	requestedByUserID string,
	idempotencyKey string,
) (store.SystemUpdateJob, error) {
	if err := ctx.Err(); err != nil {
		return store.SystemUpdateJob{}, err
	}
	if s.job != nil &&
		s.job.RequestedByUserID == strings.TrimSpace(requestedByUserID) &&
		s.job.IdempotencyKey == strings.TrimSpace(idempotencyKey) {
		return *s.job, nil
	}
	return store.SystemUpdateJob{}, store.ErrNotFound
}

func (s *portCoordinatorCaptureStore) CreateSystemdPortReconfigurationJob(
	ctx context.Context,
	services store.ServiceRegistryStore,
	policies store.UpdaterPolicyStore,
	params store.CreateSystemdPortReconfigurationJobParams,
) (store.SystemUpdateJob, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.SystemUpdateJob{}, false, err
	}
	s.createCalls++
	s.services = services
	s.policies = policies
	s.params = params
	if s.createErr != nil {
		return store.SystemUpdateJob{}, false, s.createErr
	}
	now := time.Now().UTC()
	job := store.SystemUpdateJob{
		ID:                "port-job-1",
		TargetID:          params.TargetID,
		TargetServiceType: "worker",
		Operation:         store.SystemUpdateOperationPortReconfigure,
		PortReconfigure: &store.SystemUpdatePortReconfiguration{
			NetworkNamespace:         "host",
			Protocol:                 store.SystemUpdatePortProtocolTCP,
			OldPort:                  8081,
			NewPort:                  params.NewPort,
			ExpectedEndpointRevision: params.ExpectedEndpointRevision,
			TargetEndpointRevision:   params.ExpectedEndpointRevision + 1,
		},
		DeploymentMode:      "systemd",
		CurrentVersion:      "v1.0.0",
		TargetVersion:       "v1.0.0",
		Strategy:            store.SystemUpdateStrategyMaintenance,
		Status:              store.SystemUpdateStatusQueued,
		IdempotencyKey:      params.IdempotencyKey,
		RequestedByUserID:   params.RequestedByUserID,
		RequestedByUsername: params.RequestedByUsername,
		AgentServiceID:      "host-agent-a",
		ExecutionHostID:     "host-a",
		TransportMode:       store.SystemUpdateTransportPullV2,
		OwnershipEpoch:      2,
		PolicyRevision:      4,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	s.job = &job
	return job, true, nil
}

func (s *portCoordinatorCaptureStore) ReportSystemUpdateJob(
	ctx context.Context,
	id string,
	report store.SystemUpdateReport,
	now time.Time,
	leaseTTL time.Duration,
) (store.SystemUpdateJob, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.SystemUpdateJob{}, false, err
	}
	s.reportCalls++
	s.report = report
	if s.reportErr != nil {
		return store.SystemUpdateJob{}, false, s.reportErr
	}
	job := s.reportJob
	if job.ID == "" {
		job = store.SystemUpdateJob{
			ID: "port-job-1", TargetID: "worker-a", TargetServiceType: "worker",
			Operation: store.SystemUpdateOperationPortReconfigure,
			PortReconfigure: &store.SystemUpdatePortReconfiguration{
				Result: report.PortReconfigure.Result,
			},
			Status: report.Status, Progress: report.Progress, Code: report.Code,
			CreatedAt: now, UpdatedAt: now, CompletedAt: &now,
		}
	}
	return job, true, nil
}

func TestCreateSystemUpdateStrictlySeparatesSoftwareAndPortRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "software rejects port field",
			body: `{"target_id":"worker-a","strategy":"maintenance","new_port":18081,"idempotency_key":"request-a"}`,
		},
		{
			name: "port rejects strategy",
			body: `{"operation":"port_reconfigure","target_id":"worker-a","strategy":"maintenance","new_port":18081,"expected_endpoint_revision":1,"idempotency_key":"request-a"}`,
		},
		{
			name: "port requires endpoint revision",
			body: `{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"idempotency_key":"request-a"}`,
		},
		{
			name: "Docker port requires all three fields",
			body: `{"operation":"port_reconfigure","target_id":"worker-a","new_advertised_port":443,"new_published_port":18081,"expected_endpoint_revision":1,"idempotency_key":"request-a"}`,
		},
		{
			name: "Docker port rejects systemd field",
			body: `{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"new_advertised_port":443,"new_published_port":18081,"new_container_port":8080,"expected_endpoint_revision":1,"idempotency_key":"request-a"}`,
		},
		{
			name: "systemd port rejects Docker field",
			body: `{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"new_container_port":8080,"expected_endpoint_revision":1,"idempotency_key":"request-a"}`,
		},
		{
			name: "unknown operation",
			body: `{"operation":"restart","target_id":"worker-a","idempotency_key":"request-a"}`,
		},
		{
			name: "null operation is not the omitted default",
			body: `{"operation":null,"target_id":"worker-a","strategy":"maintenance","idempotency_key":"request-a"}`,
		},
		{
			name: "unknown field",
			body: `{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"expected_endpoint_revision":1,"idempotency_key":"request-a","host_id":"attacker-selected"}`,
		},
		{
			name: "multiple json documents",
			body: `{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"expected_endpoint_revision":1,"idempotency_key":"request-a"} {}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, cookie, csrf, _ := newPortCoordinatorHTTPFixture(t, nil)
			response := postSystemUpdateForTest(t, handler, cookie, csrf, tt.body)
			if response.Code != http.StatusBadRequest ||
				(!strings.Contains(response.Body.String(), `"code":"bad_request"`) &&
					!strings.Contains(response.Body.String(), `"code":"invalid_system_update_request"`)) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCreateDockerPortReconfigurationUsesIndependentFieldsAndExactIdempotency(t *testing.T) {
	handler, cookie, csrf, updates := newPortCoordinatorHTTPFixture(t, nil)
	body := `{"operation":"port_reconfigure","target_id":"worker-a","new_advertised_port":443,"new_published_port":18084,"new_container_port":18080,"expected_endpoint_revision":7,"idempotency_key":"docker-port-request-1"}`
	response := postSystemUpdateForTest(t, handler, cookie, csrf, body)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create response=%d %s", response.Code, response.Body.String())
	}
	if updates.createCalls != 1 ||
		updates.dockerParams.TargetID != "worker-a" ||
		updates.dockerParams.NewAdvertisedPort != 443 ||
		updates.dockerParams.NewPublishedPort != 18084 ||
		updates.dockerParams.NewContainerPort != 18080 ||
		updates.dockerParams.ExpectedEndpointRevision != 7 ||
		updates.dockerParams.IdempotencyKey != "docker-port-request-1" ||
		updates.dockerParams.RequestedByUserID != "port-admin" {
		t.Fatalf("Docker coordinator call=%#v", updates.dockerParams)
	}
	var created store.SystemUpdateJob
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.DeploymentMode != "docker" ||
		created.PortReconfigure == nil ||
		created.PortReconfigure.NewPort != 443 ||
		created.PortReconfigure.Docker == nil ||
		created.PortReconfigure.Docker.NewPublishedPort != 18084 ||
		created.PortReconfigure.Docker.NewContainerPort != 18080 {
		t.Fatalf("created Docker job=%#v", created)
	}
	replay := postSystemUpdateForTest(t, handler, cookie, csrf, body)
	if replay.Code != http.StatusAccepted || updates.createCalls != 1 {
		t.Fatalf("replay=%d %s calls=%d", replay.Code, replay.Body.String(), updates.createCalls)
	}
	conflict := postSystemUpdateForTest(
		t, handler, cookie, csrf,
		`{"operation":"port_reconfigure","target_id":"worker-a","new_advertised_port":443,"new_published_port":18085,"new_container_port":18080,"expected_endpoint_revision":7,"idempotency_key":"docker-port-request-1"}`,
	)
	if conflict.Code != http.StatusConflict ||
		!strings.Contains(conflict.Body.String(), `"code":"idempotency_key_conflict"`) ||
		updates.createCalls != 1 {
		t.Fatalf("mapping conflict=%d %s calls=%d", conflict.Code, conflict.Body.String(), updates.createCalls)
	}
}

func TestCreateSystemdPortReconfigurationUsesCoordinatorAndExactIdempotency(t *testing.T) {
	handler, cookie, csrf, updates := newPortCoordinatorHTTPFixture(t, nil)
	body := `{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"expected_endpoint_revision":7,"idempotency_key":"port-request-1"}`
	response := postSystemUpdateForTest(t, handler, cookie, csrf, body)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create response = %d %s", response.Code, response.Body.String())
	}
	if updates.createCalls != 1 ||
		updates.services == nil ||
		updates.policies == nil ||
		updates.params.TargetID != "worker-a" ||
		updates.params.NewPort != 18081 ||
		updates.params.ExpectedEndpointRevision != 7 ||
		updates.params.IdempotencyKey != "port-request-1" ||
		updates.params.RequestedByUserID != "port-admin" ||
		updates.params.RequestedByUsername != "port-admin" {
		t.Fatalf("coordinator call = %#v", updates)
	}
	var created store.SystemUpdateJob
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Operation != store.SystemUpdateOperationPortReconfigure ||
		created.PortReconfigure == nil ||
		created.PortReconfigure.NewPort != 18081 ||
		created.PortReconfigure.ExpectedEndpointRevision != 7 {
		t.Fatalf("created job = %#v", created)
	}

	replay := postSystemUpdateForTest(t, handler, cookie, csrf, body)
	if replay.Code != http.StatusAccepted || updates.createCalls != 1 {
		t.Fatalf("idempotent replay = %d %s calls=%d", replay.Code, replay.Body.String(), updates.createCalls)
	}
	var replayed store.SystemUpdateJob
	if err := json.NewDecoder(replay.Body).Decode(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed job = %#v, want id %q", replayed, created.ID)
	}

	portConflict := postSystemUpdateForTest(
		t,
		handler,
		cookie,
		csrf,
		`{"operation":"port_reconfigure","target_id":"worker-a","new_port":18082,"expected_endpoint_revision":7,"idempotency_key":"port-request-1"}`,
	)
	if portConflict.Code != http.StatusConflict ||
		!strings.Contains(portConflict.Body.String(), `"code":"idempotency_key_conflict"`) ||
		updates.createCalls != 1 {
		t.Fatalf("new-port conflict = %d %s calls=%d", portConflict.Code, portConflict.Body.String(), updates.createCalls)
	}
	revisionConflict := postSystemUpdateForTest(
		t,
		handler,
		cookie,
		csrf,
		`{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"expected_endpoint_revision":8,"idempotency_key":"port-request-1"}`,
	)
	if revisionConflict.Code != http.StatusConflict ||
		!strings.Contains(revisionConflict.Body.String(), `"code":"idempotency_key_conflict"`) ||
		updates.createCalls != 1 {
		t.Fatalf("endpoint-revision conflict = %d %s calls=%d", revisionConflict.Code, revisionConflict.Body.String(), updates.createCalls)
	}
	operationConflict := postSystemUpdateForTest(
		t,
		handler,
		cookie,
		csrf,
		`{"operation":"software_update","target_id":"worker-a","strategy":"maintenance","idempotency_key":"port-request-1"}`,
	)
	if operationConflict.Code != http.StatusConflict ||
		!strings.Contains(operationConflict.Body.String(), `"code":"idempotency_key_conflict"`) ||
		updates.createCalls != 1 {
		t.Fatalf("operation conflict = %d %s calls=%d", operationConflict.Code, operationConflict.Body.String(), updates.createCalls)
	}
}

func TestCreateSystemdPortReconfigurationMapsCoordinatorErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid", err: store.ErrInvalidSystemUpdate, status: http.StatusBadRequest, code: "invalid_system_update_request"},
		{name: "missing", err: store.ErrNotFound, status: http.StatusNotFound, code: "system_update_target_not_found"},
		{name: "active", err: store.ErrSystemUpdateTargetActive, status: http.StatusConflict, code: "system_update_target_active"},
		{name: "stale endpoint", err: store.ErrSystemUpdateEndpointStale, status: http.StatusConflict, code: "system_update_endpoint_revision_conflict"},
		{name: "reserved", err: store.ErrServicePortReserved, status: http.StatusConflict, code: "service_port_reserved"},
		{name: "ownership", err: store.ErrSystemUpdateOwnershipConflict, status: http.StatusConflict, code: "system_update_ownership_conflict"},
		{name: "not ready", err: store.ErrSystemUpdateAgentNotReady, status: http.StatusConflict, code: "system_update_port_reconfigure_not_ready"},
		{name: "store mismatch", err: store.ErrSystemUpdatePortStoreMismatch, status: http.StatusConflict, code: "system_update_port_store_mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, cookie, csrf, _ := newPortCoordinatorHTTPFixture(t, tt.err)
			response := postSystemUpdateForTest(
				t,
				handler,
				cookie,
				csrf,
				`{"operation":"port_reconfigure","target_id":"worker-a","new_port":18081,"expected_endpoint_revision":7,"idempotency_key":"port-request-1"}`,
			)
			if response.Code != tt.status || !strings.Contains(response.Body.String(), `"code":"`+tt.code+`"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSystemUpdateTargetPortEligibilityIsIndependentOfSoftwareRelease(t *testing.T) {
	target := systemUpdateTargetResponse{
		TargetID:       "worker-a",
		ServiceType:    "worker",
		DeploymentMode: "systemd",
		Eligible:       false,
		BlockedReason:  "release_manifest_unavailable",
	}
	service := store.RegisteredService{
		ServiceID:             "worker-a",
		ServiceType:           "worker",
		EndpointRevision:      7,
		EndpointStatus:        "applied",
		AppliedConfigRevision: 3,
		AppliedConfigSHA256:   "sha256:" + strings.Repeat("a", 64),
		AppliedEndpoint: &store.ServiceEndpoint{
			Host: "127.0.0.1", Port: 8081, PublicURL: "http://127.0.0.1:8081",
		},
		DesiredEndpoint: &store.ServiceEndpoint{
			Host: "127.0.0.1", Port: 8081, PublicURL: "http://127.0.0.1:8081",
		},
	}
	assignment := systemUpdateAgentAssignment{
		AgentID:            "host-agent-a",
		AgentTransportMode: store.SystemUpdateTransportPullV2,
		DeploymentMode:     "systemd",
		Available:          true,
		HostReachability:   "reachable",
		TargetServiceType:  "worker",
		PolicyManaged:      true,
		PolicyReady:        true,
	}
	decorateSystemUpdateTargetOperations(&target, &service, assignment)
	if len(target.EligibleOperations) != 1 ||
		target.EligibleOperations[0] != store.SystemUpdateOperationPortReconfigure ||
		target.OperationBlockedReasons[store.SystemUpdateOperationSoftwareUpdate] != "release_manifest_unavailable" {
		t.Fatalf("operation eligibility = %#v blocked=%#v", target.EligibleOperations, target.OperationBlockedReasons)
	}
	if _, blocked := target.OperationBlockedReasons[store.SystemUpdateOperationPortReconfigure]; blocked {
		t.Fatalf("port operation unexpectedly blocked: %#v", target.OperationBlockedReasons)
	}
}

func TestSystemUpdateDockerPortMappingSnapshotIsAllowlistedAndFailClosed(t *testing.T) {
	now := time.Now().UTC()
	appliedConfigSHA256, err := store.SystemUpdateDockerPortConfigSHA256(
		"worker",
		18081,
		8080,
		3,
	)
	if err != nil {
		t.Fatal(err)
	}
	service := store.RegisteredService{
		ServiceID: "worker-a", ServiceType: "worker",
		EndpointRevision: 7, EndpointStatus: "applied",
		AppliedConfigRevision: 3,
		AppliedConfigSHA256:   appliedConfigSHA256,
		AppliedEndpoint: &store.ServiceEndpoint{
			Host: "worker.example.com", Port: 443,
			PublicURL: "https://worker.example.com",
		},
		DesiredEndpoint: &store.ServiceEndpoint{
			Host: "worker.example.com", Port: 443,
			PublicURL: "https://worker.example.com",
		},
	}
	policy := store.UpdaterPolicy{
		UpdaterID: "host-agent-a", LocalExecutorPolicyRevision: 19,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("f", 64),
	}
	agent := store.RegisteredService{
		ServiceID: "host-agent-a", ServiceType: "update_agent",
		LastHeartbeatAt: &now,
		ReportedCapabilities: map[string]any{
			"target_availability":                   map[string]any{"worker-a": "available"},
			"target_availability_codes":             map[string]any{"worker-a": "executor_verified"},
			"reported_ports":                        map[string]any{"worker-a": float64(443)},
			"port_drift":                            map[string]any{"worker-a": false},
			"reported_service_types":                map[string]any{"worker-a": "worker"},
			"reported_deployment_modes":             map[string]any{"worker-a": "docker"},
			"reported_executor_policy_revisions":    map[string]any{"worker-a": float64(19)},
			"reported_executor_policy_sha256":       map[string]any{"worker-a": policy.LocalExecutorPolicySHA256},
			"reported_config_revisions":             map[string]any{"worker-a": float64(3)},
			"reported_config_sha256":                map[string]any{"worker-a": service.AppliedConfigSHA256},
			"reported_docker_port_capabilities":     map[string]any{"worker-a": "v1"},
			"reported_docker_published_ports":       map[string]any{"worker-a": float64(18081)},
			"reported_docker_container_ports":       map[string]any{"worker-a": float64(8080)},
			"reported_docker_health_ports":          map[string]any{"worker-a": float64(18081)},
			"reported_docker_compose_sha256":        map[string]any{"worker-a": strings.Repeat("d", 64)},
			"reported_docker_compose_revisions":     map[string]any{"worker-a": float64(19)},
			"reported_docker_version_env_sha256":    map[string]any{"worker-a": "sha256:" + strings.Repeat("e", 64)},
			"reported_docker_container_ids":         map[string]any{"worker-a": strings.Repeat("1", 64)},
			"reported_docker_image_ids":             map[string]any{"worker-a": "sha256:" + strings.Repeat("2", 64)},
			"reported_docker_repository_digests":    map[string]any{"worker-a": "sha256:" + strings.Repeat("3", 64)},
			"attacker_selected_privileged_filepath": "/tmp/never-expose",
		},
	}
	assignment := systemUpdateAgentAssignment{
		AgentID: "host-agent-a", AgentTransportMode: store.SystemUpdateTransportPullV2,
		DeploymentMode: "docker", Available: true, HostReachability: "reachable",
		TargetServiceType: "worker", PolicyManaged: true, PolicyReady: true,
	}
	mapping := systemUpdateDockerPortMappingSnapshot(service, agent, policy, assignment)
	if mapping == nil ||
		mapping.Mode != "docker" ||
		mapping.State != "applied" ||
		mapping.AdvertisedPort != 443 ||
		mapping.PublishedHostIP != "127.0.0.1" ||
		mapping.PublishedPort != 18081 ||
		mapping.ContainerPort != 8080 ||
		mapping.HealthPort != 18081 ||
		mapping.ConfigRevision != 3 ||
		mapping.ReportedAt == nil {
		t.Fatalf("mapping snapshot=%#v", mapping)
	}
	target := systemUpdateTargetResponse{
		TargetID: "worker-a", ServiceType: "worker",
		DeploymentMode: "docker", PortMapping: mapping,
	}
	decorateSystemUpdateTargetOperations(&target, &service, assignment)
	if len(target.EligibleOperations) != 1 ||
		target.EligibleOperations[0] != store.SystemUpdateOperationPortReconfigure {
		t.Fatalf("Docker port eligibility=%#v blocked=%#v", target.EligibleOperations, target.OperationBlockedReasons)
	}

	policyNotReady := assignment
	policyNotReady.PolicyReady = false
	policyNotReady.PolicyBlockedReason = "updater_policy_mismatch"
	exactButBlocked := systemUpdateDockerPortMappingSnapshot(
		service,
		agent,
		policy,
		policyNotReady,
	)
	if exactButBlocked == nil || exactButBlocked.State != "applied" {
		t.Fatalf("exact mapping snapshot=%#v", exactButBlocked)
	}
	target.PortMapping = exactButBlocked
	decorateSystemUpdateTargetOperations(&target, &service, policyNotReady)
	if target.OperationBlockedReasons[store.SystemUpdateOperationPortReconfigure] !=
		"updater_policy_mismatch" {
		t.Fatalf("policy-not-ready mapping was not blocked: %#v", target.OperationBlockedReasons)
	}

	driftedConfigSHA256, err := store.SystemUpdateDockerPortConfigSHA256(
		"worker",
		18082,
		8080,
		3,
	)
	if err != nil {
		t.Fatal(err)
	}
	agent.ReportedCapabilities["reported_docker_published_ports"] =
		map[string]any{"worker-a": float64(18082)}
	agent.ReportedCapabilities["reported_config_sha256"] =
		map[string]any{"worker-a": driftedConfigSHA256}
	drifted := systemUpdateDockerPortMappingSnapshot(service, agent, policy, assignment)
	if drifted == nil ||
		drifted.Mode != "docker" ||
		drifted.State != "drifted" ||
		drifted.AdvertisedPort != 443 ||
		drifted.PublishedHostIP != "127.0.0.1" ||
		drifted.PublishedPort != 18082 ||
		drifted.ContainerPort != 8080 ||
		drifted.HealthPort != 18081 ||
		drifted.ConfigRevision != 3 ||
		drifted.ReportedAt == nil {
		t.Fatalf("drifted mapping snapshot=%#v", drifted)
	}
	target.PortMapping = drifted
	decorateSystemUpdateTargetOperations(&target, &service, assignment)
	if target.OperationBlockedReasons[store.SystemUpdateOperationPortReconfigure] !=
		"system_update_port_reconfigure_not_ready" {
		t.Fatalf("drifted mapping was not blocked: %#v", target.OperationBlockedReasons)
	}

	agent.ReportedCapabilities["reported_docker_published_ports"] =
		map[string]any{"worker-a": float64(18081)}
	agent.ReportedCapabilities["reported_config_sha256"] =
		map[string]any{"worker-a": service.AppliedConfigSHA256}
	agent.ReportedCapabilities["port_drift"] =
		map[string]any{"worker-a": true}
	explicitDrift := systemUpdateDockerPortMappingSnapshot(service, agent, policy, assignment)
	if explicitDrift == nil || explicitDrift.State != "drifted" {
		t.Fatalf("explicit drift mapping snapshot=%#v", explicitDrift)
	}
	agent.ReportedCapabilities["port_drift"] =
		map[string]any{"worker-a": false}
	staleAssignment := assignment
	staleAssignment.Available = false
	stale := systemUpdateDockerPortMappingSnapshot(service, agent, policy, staleAssignment)
	if stale == nil || stale.State != "unavailable" {
		t.Fatalf("stale mapping snapshot=%#v", stale)
	}

	agent.ReportedCapabilities["reported_docker_image_ids"] =
		map[string]any{"worker-a": "invalid"}
	invalid := systemUpdateDockerPortMappingSnapshot(service, agent, policy, assignment)
	if invalid == nil || invalid.State != "unavailable" {
		t.Fatalf("invalid mapping snapshot=%#v", invalid)
	}
	delete(agent.ReportedCapabilities, "reported_docker_image_ids")
	unavailable := systemUpdateDockerPortMappingSnapshot(service, agent, policy, assignment)
	if unavailable == nil ||
		unavailable.Mode != "docker" ||
		unavailable.State != "unavailable" ||
		unavailable.PublishedPort != 0 {
		t.Fatalf("incomplete snapshot=%#v", unavailable)
	}
	target.PortMapping = unavailable
	decorateSystemUpdateTargetOperations(&target, &service, assignment)
	if target.OperationBlockedReasons[store.SystemUpdateOperationPortReconfigure] !=
		"system_update_port_reconfigure_not_ready" {
		t.Fatalf("incomplete mapping was not blocked: %#v", target.OperationBlockedReasons)
	}
}

func TestSystemUpdateReportAcceptsNestedPortResultAndMapsCompletionDrift(t *testing.T) {
	handler, token, updates := newPortReportHTTPFixture(t)
	payload, err := json.Marshal(updateagent.JobReport{
		ServiceID:       "host-agent-a",
		LeaseToken:      "lease-token",
		LeaseGeneration: 2,
		Sequence:        3,
		Status:          "succeeded",
		Progress:        100,
		PortReconfigure: &updateagent.PortReconfigurationJobReport{
			Result: string(store.SystemUpdatePortReconfigurationApplied),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	request := httptest.NewRequest(http.MethodPost, "/services/update-jobs/port-job-1/report", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("report response = %d %s", response.Code, response.Body.String())
	}
	if updates.reportCalls != 1 ||
		updates.report.PortReconfigure == nil ||
		updates.report.PortReconfigure.Result != store.SystemUpdatePortReconfigurationApplied {
		t.Fatalf("forwarded report = %#v calls=%d", updates.report, updates.reportCalls)
	}

	updates.reportErr = store.ErrSystemUpdateEndpointStale
	driftRequest := httptest.NewRequest(http.MethodPost, "/services/update-jobs/port-job-1/report", strings.NewReader(body))
	driftRequest.Header.Set("Authorization", "Bearer "+token.RawToken)
	driftResponse := httptest.NewRecorder()
	handler.ServeHTTP(driftResponse, driftRequest)
	if driftResponse.Code != http.StatusConflict ||
		!strings.Contains(driftResponse.Body.String(), `"code":"system_update_endpoint_revision_conflict"`) {
		t.Fatalf("completion drift = %d %s", driftResponse.Code, driftResponse.Body.String())
	}

	invalidRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/port-job-1/report",
		strings.NewReader(`{
			"service_id":"host-agent-a",
			"lease_token":"lease-token",
			"lease_generation":2,
			"sequence":3,
			"status":"succeeded",
			"progress":100,
			"port_reconfigure":{"result":"applied","attacker_selected":18081}
		}`),
	)
	invalidRequest.Header.Set("Authorization", "Bearer "+token.RawToken)
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest ||
		!strings.Contains(invalidResponse.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("nested plan field response = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	internalStateRequest := httptest.NewRequest(
		http.MethodPost,
		"/services/update-jobs/port-job-1/report",
		strings.NewReader(`{
			"service_id":"host-agent-a",
			"lease_token":"lease-token",
			"lease_generation":2,
			"sequence":3,
			"status":"succeeded",
			"progress":100,
			"port_reconfigure":{"result":"applied","state_known":true,"applied_port":18081}
		}`),
	)
	internalStateRequest.Header.Set("Authorization", "Bearer "+token.RawToken)
	internalStateResponse := httptest.NewRecorder()
	handler.ServeHTTP(internalStateResponse, internalStateRequest)
	if internalStateResponse.Code != http.StatusBadRequest ||
		!strings.Contains(internalStateResponse.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("internal executor state response = %d %s", internalStateResponse.Code, internalStateResponse.Body.String())
	}
}

func newPortCoordinatorHTTPFixture(
	t *testing.T,
	createErr error,
) (*Server, *http.Cookie, string, *portCoordinatorCaptureStore) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(
		store.User{ID: "port-admin", Username: "port-admin"},
		"correct horse battery",
		[]string{"system_updates.read", "system_updates.execute"},
	); err != nil {
		t.Fatal(err)
	}
	policies := store.NewMemoryUpdaterPolicyStore()
	updates := &portCoordinatorCaptureStore{
		SystemUpdateStore: store.NewMemorySystemUpdateStore(),
		createErr:         createErr,
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithAuditStore(auth),
		WithServiceRegistryStore(auth),
		WithUpdaterPolicyStore(policies),
		WithSystemUpdateStore(updates),
	)
	cookie, csrf := loginForTest(t, handler, "port-admin", "correct horse battery")
	return handler, cookie, csrf, updates
}

func postSystemUpdateForTest(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	csrf string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/system-updates", bytes.NewBufferString(body))
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newPortReportHTTPFixture(
	t *testing.T,
) (*Server, store.ServiceToken, *portCoordinatorCaptureStore) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(
		t.Context(),
		"update_agent",
		[]string{"service.register", "service.heartbeat", "updates.report"},
	)
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{
		ServiceID:   "host-agent-a",
		ServiceType: "update_agent",
		ServiceName: "Host Agent A",
		PublicURL:   "https://updater.example.com",
		Version:     "v1.0.0",
	}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	updates := &portCoordinatorCaptureStore{SystemUpdateStore: store.NewMemorySystemUpdateStore()}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithSystemUpdateStore(updates),
	)
	return handler, token, updates
}

var _ store.SystemUpdatePortReconfigurationStore = (*portCoordinatorCaptureStore)(nil)
