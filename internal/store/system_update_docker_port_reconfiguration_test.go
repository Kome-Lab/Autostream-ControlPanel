package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateSystemUpdateDockerPortPlanBindsRuntimeBaseline(t *testing.T) {
	plan := &SystemUpdatePortReconfiguration{
		NetworkNamespace:               systemUpdatePortNetworkNamespace,
		Protocol:                       SystemUpdatePortProtocolTCP,
		OldPort:                        8084,
		NewPort:                        18084,
		ExpectedEndpointRevision:       7,
		TargetEndpointRevision:         8,
		ExpectedConfigRevision:         11,
		TargetConfigRevision:           12,
		ExpectedConfigSHA256:           "sha256:" + strings.Repeat("1", 64),
		TargetConfigSHA256:             "sha256:" + strings.Repeat("2", 64),
		ExpectedSourcePolicyRevision:   13,
		ExpectedUpdaterPolicyRevision:  17,
		ExpectedExecutorPolicyRevision: 19,
		ExpectedExecutorPolicySHA256:   "sha256:" + strings.Repeat("3", 64),
		PortPlanSHA256:                 strings.Repeat("4", 64),
		Docker: &SystemUpdateDockerPortReconfiguration{
			PublishedHostIP:             "127.0.0.1",
			OldPublishedPort:            8084,
			NewPublishedPort:            18084,
			OldContainerPort:            8080,
			NewContainerPort:            18080,
			OldHealthPort:               8084,
			NewHealthPort:               18084,
			ApprovedComposeConfigSHA256: strings.Repeat("5", 64),
			ApprovedComposeRevision:     19,
			ExpectedVersionEnvSHA256:    "sha256:" + strings.Repeat("6", 64),
			ExpectedContainerID:         strings.Repeat("a", 64),
			ExpectedImageID:             "sha256:" + strings.Repeat("7", 64),
			ExpectedRepositoryDigest:    "sha256:" + strings.Repeat("8", 64),
		},
	}

	if err := validateSystemUpdatePortReconfigurationPlanForDeployment(plan, "docker"); err != nil {
		t.Fatalf("valid Docker port plan rejected: %v", err)
	}

	for name, mutate := range map[string]func(*SystemUpdatePortReconfiguration){
		"missing Docker baseline": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker = nil
		},
		"privileged container port": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker.NewContainerPort = 443
		},
		"unapproved Compose": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker.ApprovedComposeConfigSHA256 = ""
		},
		"stale Compose revision": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker.ApprovedComposeRevision++
		},
		"missing env baseline": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker.ExpectedVersionEnvSHA256 = ""
		},
		"missing container identity": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker.ExpectedContainerID = ""
		},
		"missing image identity": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker.ExpectedImageID = ""
		},
		"missing repository identity": func(candidate *SystemUpdatePortReconfiguration) {
			candidate.Docker.ExpectedRepositoryDigest = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneSystemUpdatePortReconfiguration(plan)
			docker := *candidate.Docker
			candidate.Docker = &docker
			mutate(candidate)
			if err := validateSystemUpdatePortReconfigurationPlanForDeployment(candidate, "docker"); err == nil {
				t.Fatal("unsafe Docker port plan accepted")
			}
		})
	}
}

func TestValidateSystemUpdateSystemdPortPlanRejectsDockerFields(t *testing.T) {
	plan := &SystemUpdatePortReconfiguration{
		NetworkNamespace:               systemUpdatePortNetworkNamespace,
		Protocol:                       SystemUpdatePortProtocolTCP,
		OldPort:                        8084,
		NewPort:                        18084,
		ExpectedEndpointRevision:       7,
		TargetEndpointRevision:         8,
		ExpectedConfigRevision:         11,
		TargetConfigRevision:           12,
		ExpectedConfigSHA256:           "sha256:" + strings.Repeat("1", 64),
		TargetConfigSHA256:             "sha256:" + strings.Repeat("2", 64),
		ExpectedSourcePolicyRevision:   13,
		ExpectedUpdaterPolicyRevision:  17,
		ExpectedExecutorPolicyRevision: 19,
		ExpectedExecutorPolicySHA256:   "sha256:" + strings.Repeat("3", 64),
		PortPlanSHA256:                 strings.Repeat("4", 64),
		Docker: &SystemUpdateDockerPortReconfiguration{
			PublishedHostIP: "127.0.0.1",
		},
	}
	if err := validateSystemUpdatePortReconfigurationPlanForDeployment(plan, "systemd"); err == nil {
		t.Fatal("systemd port plan accepted Docker-only fields")
	}
}

func TestValidateSystemUpdateDockerPortPlanAllowsIndependentChanges(t *testing.T) {
	base := validDockerPortReconfigurationPlanForTest()
	for name, mutate := range map[string]func(*SystemUpdatePortReconfiguration){
		"advertised only": func(plan *SystemUpdatePortReconfiguration) {
			plan.NewPort = plan.OldPort + 1
		},
		"published only": func(plan *SystemUpdatePortReconfiguration) {
			plan.Docker.NewPublishedPort = plan.Docker.OldPublishedPort + 1
			plan.Docker.NewHealthPort = plan.Docker.NewPublishedPort
		},
		"container only": func(plan *SystemUpdatePortReconfiguration) {
			plan.Docker.NewContainerPort = plan.Docker.OldContainerPort + 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan := cloneSystemUpdatePortReconfiguration(base)
			mutate(plan)
			if err := validateSystemUpdatePortReconfigurationPlanForDeployment(plan, "docker"); err != nil {
				t.Fatalf("independent Docker port change rejected: %v", err)
			}
		})
	}
	if err := validateSystemUpdatePortReconfigurationPlanForDeployment(base, "docker"); err == nil {
		t.Fatal("Docker port no-op accepted")
	}
}

func TestMemoryCreateDockerPortReconfigurationBindsPublishedAndContainerPorts(t *testing.T) {
	policies, registry, updates := readyMemoryDockerPortCoordinator(t)
	job, created, err := updates.CreateDockerPortReconfigurationJob(
		t.Context(),
		registry,
		policies,
		CreateDockerPortReconfigurationJobParams{
			TargetID:                 "worker-a",
			NewAdvertisedPort:        443,
			NewPublishedPort:         18084,
			NewContainerPort:         18080,
			ExpectedEndpointRevision: 3,
			IdempotencyKey:           "docker-port-worker-a-3",
			RequestedByUserID:        "admin-a",
			RequestedByUsername:      "Admin A",
		},
	)
	if err != nil || !created {
		t.Fatalf("create = %#v created=%v err=%v", job, created, err)
	}
	if job.DeploymentMode != "docker" ||
		job.PortReconfigure == nil ||
		job.PortReconfigure.Docker == nil ||
		job.PortReconfigure.OldPort != 18081 ||
		job.PortReconfigure.NewPort != 443 ||
		job.PortReconfigure.Docker.OldPublishedPort != 18081 ||
		job.PortReconfigure.Docker.NewPublishedPort != 18084 ||
		job.PortReconfigure.Docker.OldContainerPort != 8080 ||
		job.PortReconfigure.Docker.NewContainerPort != 18080 ||
		job.PortReconfigure.Docker.PublishedHostIP != "127.0.0.1" ||
		job.PortReconfigure.Docker.ExpectedContainerID != strings.Repeat("a", 64) ||
		job.PortReconfigure.Docker.ExpectedImageID != "sha256:"+strings.Repeat("b", 64) ||
		job.PortReconfigure.Docker.ExpectedRepositoryDigest != "sha256:"+strings.Repeat("c", 64) {
		t.Fatalf("derived Docker plan = %#v", job.PortReconfigure)
	}
	wantConfigSHA256, err := systemUpdateDockerPortEnvSHA256(
		"worker",
		18084,
		18080,
		4,
	)
	if err != nil || job.PortReconfigure.TargetConfigSHA256 != wantConfigSHA256 {
		t.Fatalf(
			"target config digest=%q want=%q err=%v",
			job.PortReconfigure.TargetConfigSHA256,
			wantConfigSHA256,
			err,
		)
	}
	intentSHA256, err := ComputeSystemUpdatePortIntentSHA256(job)
	if err != nil || intentSHA256 != job.PortReconfigure.PortPlanSHA256 {
		t.Fatalf(
			"intent digest=%q stored=%q err=%v",
			intentSHA256,
			job.PortReconfigure.PortPlanSHA256,
			err,
		)
	}
	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.AppliedEndpoint == nil ||
		service.AppliedEndpoint.Host != "worker-a.example.com" ||
		service.AppliedEndpoint.Port != 18081 ||
		service.DesiredEndpoint == nil ||
		service.DesiredEndpoint.Host != "worker-a.example.com" ||
		service.DesiredEndpoint.Port != 443 ||
		service.EndpointStatus != "pending" ||
		service.EndpointRevision != 4 {
		t.Fatalf("advertised endpoint state = %#v", service)
	}
	reservations, err := updates.ListServicePortReservations(t.Context(), "host-a")
	if err != nil || len(reservations) != 2 ||
		reservations[0].Port != 18081 ||
		reservations[1].Port != 18084 {
		t.Fatalf("published reservations=%#v err=%v", reservations, err)
	}

	replayed, replayCreated, err := updates.CreateDockerPortReconfigurationJob(
		t.Context(),
		registry,
		policies,
		CreateDockerPortReconfigurationJobParams{
			TargetID:                 "worker-a",
			NewAdvertisedPort:        443,
			NewPublishedPort:         18084,
			NewContainerPort:         18080,
			ExpectedEndpointRevision: 3,
			IdempotencyKey:           "docker-port-worker-a-3",
			RequestedByUserID:        "admin-a",
			RequestedByUsername:      "Admin A",
		},
	)
	if err != nil || replayCreated || replayed.ID != job.ID {
		t.Fatalf("replay=%#v created=%v err=%v", replayed, replayCreated, err)
	}
}

func TestMemoryDockerPortReconfigurationRejectsSyntheticControlPanelPublishedPortWithoutPartialState(t *testing.T) {
	policies, registry, updates := readyMemoryDockerPortCoordinator(t)
	policies.mu.Lock()
	policy := policies.policies["host-agent-a"]
	policy.Targets = append(policy.Targets, UpdaterPolicyTarget{
		TargetID:       "control-panel",
		ServiceID:      "control-panel",
		HostID:         policy.ExecutionHostID,
		ServiceType:    "control_panel",
		DeploymentMode: "systemd",
		DatabaseName:   "autostream_panel",
	})
	policies.policies[policy.UpdaterID] = policy
	policies.mu.Unlock()
	controlPanelTarget := &PullUpdaterControlPanelTarget{
		ServiceID:             "control-panel",
		ServiceType:           "control_panel",
		EndpointRevision:      1,
		AppliedConfigRevision: 1,
		AppliedConfigSHA256:   "sha256:" + strings.Repeat("d", 64),
		AppliedEndpoint: ServiceEndpoint{
			Host: "127.0.0.1", Port: 18080, PublicURL: "http://127.0.0.1:18080",
		},
	}

	if _, _, err := updates.CreateDockerPortReconfigurationJob(
		t.Context(),
		registry,
		policies,
		CreateDockerPortReconfigurationJobParams{
			TargetID:                 "worker-a",
			NewAdvertisedPort:        443,
			NewPublishedPort:         controlPanelTarget.AppliedEndpoint.Port,
			NewContainerPort:         18082,
			ExpectedEndpointRevision: 3,
			IdempotencyKey:           "docker-synthetic-control-panel-port-collision",
			RequestedByUserID:        "admin-a",
			ControlPanelTarget:       controlPanelTarget,
		},
	); !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("synthetic Control Panel published-port collision = %v, want ErrServicePortReserved", err)
	}
	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.EndpointRevision != 3 || service.EndpointStatus != "applied" ||
		!sameServiceEndpoint(service.DesiredEndpoint, service.AppliedEndpoint) {
		t.Fatalf("collision partially mutated service = %#v", service)
	}
	jobs, err := updates.ListSystemUpdateJobs(t.Context(), 100)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("collision partially created jobs = %#v err=%v", jobs, err)
	}
	reservations, err := updates.ListServicePortReservations(t.Context(), "host-a")
	if err != nil || len(reservations) != 1 ||
		reservations[0].ServiceID != "worker-a" ||
		reservations[0].Port != 18081 {
		t.Fatalf("collision partially mutated reservations = %#v err=%v", reservations, err)
	}
}

func TestMemoryCreateDockerPortReconfigurationAcceptsDistinctAdvertisedAndPublishedBaseline(t *testing.T) {
	policies, registry, updates := readyMemoryDockerPortCoordinator(t)
	registry.mu.Lock()
	target := registry.services["worker-a"]
	target.Host = "worker.example.com"
	target.Port = 443
	target.SSLEnabled = true
	target.PublicURL = "https://worker.example.com"
	target.AppliedEndpoint = &ServiceEndpoint{
		Host:       "worker.example.com",
		Port:       443,
		SSLEnabled: true,
		PublicURL:  "https://worker.example.com",
	}
	target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
	registry.services[target.ServiceID] = target
	agent := registry.services["host-agent-a"]
	agent.ReportedCapabilities["reported_ports"] =
		map[string]any{"worker-a": float64(443)}
	registry.services[agent.ServiceID] = agent
	registry.mu.Unlock()

	job, created, err := updates.CreateDockerPortReconfigurationJob(
		t.Context(),
		registry,
		policies,
		CreateDockerPortReconfigurationJobParams{
			TargetID:                 "worker-a",
			NewAdvertisedPort:        8443,
			NewPublishedPort:         18084,
			NewContainerPort:         18080,
			ExpectedEndpointRevision: target.EndpointRevision,
			IdempotencyKey:           "docker-port-distinct-baseline",
			RequestedByUserID:        "admin-a",
			RequestedByUsername:      "Admin A",
		},
	)
	if err != nil || !created {
		t.Fatalf("create=%+v created=%v err=%v", job, created, err)
	}
	if job.PortReconfigure == nil ||
		job.PortReconfigure.Docker == nil ||
		job.PortReconfigure.OldPort != 443 ||
		job.PortReconfigure.Docker.OldPublishedPort != 18081 ||
		job.PortReconfigure.Docker.OldHealthPort != 18081 {
		t.Fatalf("job=%+v", job)
	}
}

func TestMemoryDockerPortReconfigurationAllowsNextChangeAfterPrivilegedAdvertisedPort(t *testing.T) {
	policies, registry, updates := readyMemoryDockerPortCoordinator(t)
	registry.mu.Lock()
	target := registry.services["worker-a"]
	target.PublicURL = "https://worker-a.example.com:443"
	target.AppliedEndpoint.Port = 443
	target.AppliedEndpoint.PublicURL = target.PublicURL
	target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
	registry.services[target.ServiceID] = target
	agent := registry.services["host-agent-a"]
	agent.ReportedCapabilities["reported_ports"] =
		map[string]any{"worker-a": float64(443)}
	registry.services[agent.ServiceID] = agent
	registry.mu.Unlock()

	job, created, err := updates.CreateDockerPortReconfigurationJob(
		t.Context(),
		registry,
		policies,
		CreateDockerPortReconfigurationJobParams{
			TargetID: "worker-a", NewAdvertisedPort: 8443,
			NewPublishedPort: 18081, NewContainerPort: 8080,
			ExpectedEndpointRevision: 3,
			IdempotencyKey:           "docker-after-advertised-443",
			RequestedByUserID:        "admin-a",
		},
	)
	if err != nil || !created || job.PortReconfigure == nil ||
		job.PortReconfigure.OldPort != 443 ||
		job.PortReconfigure.NewPort != 8443 {
		t.Fatalf("job=%#v created=%v err=%v", job, created, err)
	}
}

func TestMemoryDockerPortReconfigurationRequiresCompleteExecutorBaseline(t *testing.T) {
	policies, registry, updates := readyMemoryDockerPortCoordinator(t)
	registry.mu.Lock()
	agent := registry.services["host-agent-a"]
	delete(agent.ReportedCapabilities, "reported_docker_image_ids")
	registry.services[agent.ServiceID] = agent
	registry.mu.Unlock()

	_, _, err := updates.CreateDockerPortReconfigurationJob(
		t.Context(),
		registry,
		policies,
		CreateDockerPortReconfigurationJobParams{
			TargetID:                 "worker-a",
			NewAdvertisedPort:        443,
			NewPublishedPort:         18084,
			NewContainerPort:         18080,
			ExpectedEndpointRevision: 3,
			IdempotencyKey:           "docker-port-missing-baseline",
			RequestedByUserID:        "admin-a",
		},
	)
	if !errors.Is(err, ErrSystemUpdateAgentNotReady) {
		t.Fatalf("missing root-observed baseline result=%v", err)
	}
	jobs, listErr := updates.ListSystemUpdateJobs(t.Context(), 100)
	if listErr != nil || len(jobs) != 0 {
		t.Fatalf("missing baseline partially created jobs=%#v err=%v", jobs, listErr)
	}
}

func TestDockerPullActivationReservesPublishedPortNotAdvertisedPort(t *testing.T) {
	policy := UpdaterPolicy{
		LocalExecutorPolicyRevision: 19,
		ExecutionHostID:             "host-a",
		Targets: []UpdaterPolicyTarget{{
			TargetID: "worker-a", ServiceID: "worker-a",
			ServiceType: "worker", DeploymentMode: "docker",
		}},
	}
	target := RegisteredService{
		ServiceID: "worker-a", ServiceType: "worker",
		AppliedEndpoint: &ServiceEndpoint{Host: "worker.example.com", Port: 443},
	}
	observer := RegisteredService{ReportedCapabilities: map[string]any{
		"reported_docker_port_capabilities":  map[string]any{"worker-a": "v1"},
		"reported_docker_published_ports":    map[string]any{"worker-a": float64(18081)},
		"reported_docker_container_ports":    map[string]any{"worker-a": float64(8080)},
		"reported_docker_health_ports":       map[string]any{"worker-a": float64(18081)},
		"reported_docker_compose_sha256":     map[string]any{"worker-a": strings.Repeat("d", 64)},
		"reported_docker_compose_revisions":  map[string]any{"worker-a": float64(19)},
		"reported_docker_version_env_sha256": map[string]any{"worker-a": "sha256:" + strings.Repeat("e", 64)},
		"reported_docker_container_ids":      map[string]any{"worker-a": strings.Repeat("a", 64)},
		"reported_docker_image_ids":          map[string]any{"worker-a": "sha256:" + strings.Repeat("b", 64)},
		"reported_docker_repository_digests": map[string]any{"worker-a": "sha256:" + strings.Repeat("c", 64)},
	}}
	reservations, err := pullActivationBaselineReservations(
		policy,
		map[string]RegisteredService{"worker-a": target},
		observer,
	)
	if err != nil || len(reservations) != 1 ||
		reservations[0].Port != 18081 ||
		reservations[0].ServiceRole != systemUpdatePortCurrentRole {
		t.Fatalf("Docker activation reservations=%#v err=%v", reservations, err)
	}
}

func TestMemoryDockerPortReconfigurationIndependentChangesDoNotSelfReserve(t *testing.T) {
	tests := []struct {
		name              string
		newAdvertisedPort int
		newPublishedPort  int
		newContainerPort  int
		wantReservations  int
		wantPendingPort   int
	}{
		{
			name: "advertised only", newAdvertisedPort: 443,
			newPublishedPort: 18081, newContainerPort: 8080,
			wantReservations: 1,
		},
		{
			name: "published only", newAdvertisedPort: 18081,
			newPublishedPort: 18084, newContainerPort: 8080,
			wantReservations: 2, wantPendingPort: 18084,
		},
		{
			name: "container only", newAdvertisedPort: 18081,
			newPublishedPort: 18081, newContainerPort: 18080,
			wantReservations: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates := readyMemoryDockerPortCoordinator(t)
			job, created, err := updates.CreateDockerPortReconfigurationJob(
				t.Context(),
				registry,
				policies,
				CreateDockerPortReconfigurationJobParams{
					TargetID:                 "worker-a",
					NewAdvertisedPort:        test.newAdvertisedPort,
					NewPublishedPort:         test.newPublishedPort,
					NewContainerPort:         test.newContainerPort,
					ExpectedEndpointRevision: 3,
					IdempotencyKey:           "docker-independent-" + test.name,
					RequestedByUserID:        "admin-a",
				},
			)
			if err != nil || !created || job.PortReconfigure == nil {
				t.Fatalf("create=%#v created=%v err=%v", job, created, err)
			}
			reservations, err := updates.ListServicePortReservations(t.Context(), "host-a")
			if err != nil || len(reservations) != test.wantReservations {
				t.Fatalf("reservations=%#v err=%v", reservations, err)
			}
			if reservations[0].Port != 18081 ||
				reservations[0].ServiceRole != systemUpdatePortCurrentRole {
				t.Fatalf("current reservation=%#v", reservations)
			}
			if test.wantPendingPort != 0 &&
				(reservations[1].Port != test.wantPendingPort ||
					reservations[1].ServiceRole != systemUpdatePortPendingRole) {
				t.Fatalf("pending reservation=%#v", reservations)
			}
			canceled, err := updates.CancelSystemUpdateJob(t.Context(), job.ID, "admin-a")
			if err != nil || canceled.Status != SystemUpdateStatusCancelled {
				t.Fatalf("cancel=%#v err=%v", canceled, err)
			}
			reservations, err = updates.ListServicePortReservations(t.Context(), "host-a")
			if err != nil || len(reservations) != 1 ||
				reservations[0].Port != 18081 ||
				reservations[0].ServiceRole != systemUpdatePortCurrentRole {
				t.Fatalf("reservations after cancel=%#v err=%v", reservations, err)
			}
		})
	}
}

func TestMemoryDockerPortReconfigurationRejectsPublishedPortCollisionAtomically(t *testing.T) {
	policies, registry, updates := readyMemoryDockerPortCoordinator(t)
	if _, created, err := updates.ReserveServicePort(t.Context(), ServicePortReservation{
		ExecutionHostID: "host-a", NetworkNamespace: "host", Protocol: "tcp",
		Port: 19000, ServiceID: "worker-a", ServiceRole: "test-collision",
	}); err != nil || !created {
		t.Fatalf("collision fixture created=%v err=%v", created, err)
	}
	_, _, err := updates.CreateDockerPortReconfigurationJob(
		t.Context(), registry, policies,
		CreateDockerPortReconfigurationJobParams{
			TargetID: "worker-a", NewAdvertisedPort: 18081,
			NewPublishedPort: 19000, NewContainerPort: 8080,
			ExpectedEndpointRevision: 3,
			IdempotencyKey:           "docker-published-collision",
			RequestedByUserID:        "admin-a",
		},
	)
	if !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("published-port collision result=%v", err)
	}
	service, err := registry.GetService(t.Context(), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if service.EndpointRevision != 3 ||
		service.EndpointStatus != "applied" ||
		!sameServiceEndpoint(service.AppliedEndpoint, service.DesiredEndpoint) {
		t.Fatalf("collision partially mutated service=%#v", service)
	}
	jobs, err := updates.ListSystemUpdateJobs(t.Context(), 100)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("collision partially created jobs=%#v err=%v", jobs, err)
	}
}

func TestMemoryDockerPortReconfigurationSamePublishedTerminalState(t *testing.T) {
	for _, test := range []struct {
		name               string
		result             SystemUpdatePortReconfigurationResult
		status             string
		wantConfigRevision int64
		wantEndpointStatus string
	}{
		{
			name: "applied", result: SystemUpdatePortReconfigurationApplied,
			status: SystemUpdateStatusSucceeded, wantConfigRevision: 4,
			wantEndpointStatus: "applied",
		},
		{
			name: "rolled back", result: SystemUpdatePortReconfigurationRolledBack,
			status: SystemUpdateStatusRolledBack, wantConfigRevision: 3,
			wantEndpointStatus: "rolled_back",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			policies, registry, updates := readyMemoryDockerPortCoordinator(t)
			job, _, err := updates.CreateDockerPortReconfigurationJob(
				t.Context(), registry, policies,
				CreateDockerPortReconfigurationJobParams{
					TargetID: "worker-a", NewAdvertisedPort: 18081,
					NewPublishedPort: 18081, NewContainerPort: 18080,
					ExpectedEndpointRevision: 3,
					IdempotencyKey:           "docker-terminal-" + test.name,
					RequestedByUserID:        "admin-a",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			base := time.Now().UTC()
			claim, _, err := updates.ClaimSystemUpdateJob(
				t.Context(), "host-agent-a", "host-a", "",
				map[string]string{"worker-a": "docker"}, base, time.Minute,
			)
			if err != nil {
				t.Fatal(err)
			}
			sequence := claim.ReportSequence
			if test.status == SystemUpdateStatusRolledBack {
				for _, status := range []string{
					SystemUpdateStatusInstalling,
					SystemUpdateStatusRollingBack,
				} {
					if _, applied, reportErr := updates.ReportSystemUpdateJob(
						t.Context(), job.ID, SystemUpdateReport{
							AgentServiceID: "host-agent-a", ExecutionHostID: "host-a",
							LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
							Sequence: sequence, Status: status, Progress: 70,
						}, base.Add(time.Second), time.Minute,
					); reportErr != nil || !applied {
						t.Fatalf("%s report applied=%v err=%v", status, applied, reportErr)
					}
					sequence++
				}
			}
			terminal, applied, err := updates.ReportSystemUpdateJob(
				t.Context(), job.ID, SystemUpdateReport{
					AgentServiceID: "host-agent-a", ExecutionHostID: "host-a",
					LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
					Sequence: sequence, Status: test.status, Progress: 100,
					PortReconfigure: &SystemUpdatePortReconfiguration{Result: test.result},
				}, base.Add(2*time.Second), time.Minute,
			)
			if err != nil || !applied || terminal.PortReconfigure == nil ||
				terminal.PortReconfigure.Result != test.result {
				t.Fatalf("terminal=%#v applied=%v err=%v", terminal, applied, err)
			}
			service, err := registry.GetService(t.Context(), "worker-a")
			if err != nil {
				t.Fatal(err)
			}
			if service.AppliedEndpoint.Port != 18081 ||
				service.AppliedConfigRevision != test.wantConfigRevision ||
				service.EndpointStatus != test.wantEndpointStatus {
				t.Fatalf("terminal service=%#v", service)
			}
			reservations, err := updates.ListServicePortReservations(t.Context(), "host-a")
			if err != nil || len(reservations) != 1 ||
				reservations[0].Port != 18081 ||
				reservations[0].ServiceRole != systemUpdatePortCurrentRole {
				t.Fatalf("terminal reservations=%#v err=%v", reservations, err)
			}
		})
	}
}

func validDockerPortReconfigurationPlanForTest() *SystemUpdatePortReconfiguration {
	return &SystemUpdatePortReconfiguration{
		NetworkNamespace:               systemUpdatePortNetworkNamespace,
		Protocol:                       SystemUpdatePortProtocolTCP,
		OldPort:                        18081,
		NewPort:                        18081,
		ExpectedEndpointRevision:       7,
		TargetEndpointRevision:         8,
		ExpectedConfigRevision:         11,
		TargetConfigRevision:           12,
		ExpectedConfigSHA256:           "sha256:" + strings.Repeat("1", 64),
		TargetConfigSHA256:             "sha256:" + strings.Repeat("2", 64),
		ExpectedSourcePolicyRevision:   13,
		ExpectedUpdaterPolicyRevision:  17,
		ExpectedExecutorPolicyRevision: 19,
		ExpectedExecutorPolicySHA256:   "sha256:" + strings.Repeat("3", 64),
		PortPlanSHA256:                 strings.Repeat("4", 64),
		Docker: &SystemUpdateDockerPortReconfiguration{
			PublishedHostIP:             "127.0.0.1",
			OldPublishedPort:            18081,
			NewPublishedPort:            18081,
			OldContainerPort:            8080,
			NewContainerPort:            8080,
			OldHealthPort:               18081,
			NewHealthPort:               18081,
			ApprovedComposeConfigSHA256: strings.Repeat("5", 64),
			ApprovedComposeRevision:     19,
			ExpectedVersionEnvSHA256:    "sha256:" + strings.Repeat("6", 64),
			ExpectedContainerID:         strings.Repeat("a", 64),
			ExpectedImageID:             "sha256:" + strings.Repeat("7", 64),
			ExpectedRepositoryDigest:    "sha256:" + strings.Repeat("8", 64),
		},
	}
}

func readyMemoryDockerPortCoordinator(
	t *testing.T,
) (*MemoryUpdaterPolicyStore, *MemoryAuthStore, *MemorySystemUpdateStore) {
	t.Helper()
	policies, registry, updates := readyMemorySystemdPortCoordinator(t)

	policies.mu.Lock()
	policy := policies.policies["host-agent-a"]
	for index := range policy.Targets {
		if policy.Targets[index].ServiceID == "worker-a" {
			policy.Targets[index].DeploymentMode = "docker"
		}
	}
	policies.policies[policy.UpdaterID] = policy
	policies.mu.Unlock()

	oldConfigSHA256, err := systemUpdateDockerPortEnvSHA256("worker", 18081, 8080, 3)
	if err != nil {
		t.Fatal(err)
	}
	registry.mu.Lock()
	target := registry.services["worker-a"]
	target.Host = "worker-a.example.com"
	target.PublicURL = "https://worker-a.example.com:18081"
	target.AppliedEndpoint = &ServiceEndpoint{
		Host:       "worker-a.example.com",
		Port:       18081,
		SSLEnabled: true,
		PublicURL:  "https://worker-a.example.com:18081",
	}
	target.DesiredEndpoint = copyServiceEndpoint(target.AppliedEndpoint)
	target.AppliedConfigRevision = 3
	target.AppliedConfigSHA256 = oldConfigSHA256
	registry.services[target.ServiceID] = target

	agent := registry.services["host-agent-a"]
	agent.ReportedCapabilities["target_availability"] = map[string]any{"worker-a": "available"}
	agent.ReportedCapabilities["target_availability_codes"] = map[string]any{"worker-a": "executor_verified"}
	agent.ReportedCapabilities["reported_ports"] = map[string]any{"worker-a": float64(18081)}
	agent.ReportedCapabilities["reported_service_types"] = map[string]any{"worker-a": "worker"}
	agent.ReportedCapabilities["reported_deployment_modes"] = map[string]any{"worker-a": "docker"}
	agent.ReportedCapabilities["reported_executor_policy_revisions"] = map[string]any{
		"worker-a": float64(policy.LocalExecutorPolicyRevision),
	}
	agent.ReportedCapabilities["reported_executor_policy_sha256"] = map[string]any{
		"worker-a": policy.LocalExecutorPolicySHA256,
	}
	agent.ReportedCapabilities["reported_config_revisions"] = map[string]any{"worker-a": float64(3)}
	agent.ReportedCapabilities["reported_config_sha256"] = map[string]any{"worker-a": oldConfigSHA256}
	agent.ReportedCapabilities["port_drift"] = map[string]any{"worker-a": false}
	agent.ReportedCapabilities["reported_docker_port_capabilities"] = map[string]any{"worker-a": "v1"}
	agent.ReportedCapabilities["reported_docker_published_ports"] = map[string]any{"worker-a": float64(18081)}
	agent.ReportedCapabilities["reported_docker_container_ports"] = map[string]any{"worker-a": float64(8080)}
	agent.ReportedCapabilities["reported_docker_health_ports"] = map[string]any{"worker-a": float64(18081)}
	agent.ReportedCapabilities["reported_docker_compose_sha256"] = map[string]any{
		"worker-a": strings.Repeat("d", 64),
	}
	agent.ReportedCapabilities["reported_docker_compose_revisions"] = map[string]any{
		"worker-a": float64(policy.LocalExecutorPolicyRevision),
	}
	agent.ReportedCapabilities["reported_docker_version_env_sha256"] = map[string]any{
		"worker-a": "sha256:" + strings.Repeat("e", 64),
	}
	agent.ReportedCapabilities["reported_docker_container_ids"] = map[string]any{
		"worker-a": strings.Repeat("a", 64),
	}
	agent.ReportedCapabilities["reported_docker_image_ids"] = map[string]any{
		"worker-a": "sha256:" + strings.Repeat("b", 64),
	}
	agent.ReportedCapabilities["reported_docker_repository_digests"] = map[string]any{
		"worker-a": "sha256:" + strings.Repeat("c", 64),
	}
	registry.services[agent.ServiceID] = agent
	registry.mu.Unlock()
	return policies, registry, updates
}
