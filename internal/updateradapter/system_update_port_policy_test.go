package updateradapter

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/example/autostream-contracts/pkg/contracts"
)

func TestSTPortPolicyMaterializationPreservesCompleteSystemdAuthority(t *testing.T) {
	config, err := SystemdConfigurePortSidecarSHA256("worker", 18081, 3)
	if err != nil {
		t.Fatal(err)
	}
	otherConfig, err := SystemdConfigurePortSidecarSHA256("observability", 18090, 5)
	if err != nil {
		t.Fatal(err)
	}
	source := HostAgentConfigurePolicySource{PanelURL: "https://panel.example.test", ExecutionHostID: "host-a", AgentUID: 991, AgentGID: 992,
		SourcePolicyRevision: 11, ProjectionRevision: 17, LocalExecutorPolicyRevision: 23,
		Targets: []HostAgentConfigurePolicyTarget{
			{ServiceID: "worker-a", ServiceType: "worker", DeploymentMode: "systemd", EndpointRevision: 7, AppliedConfigRevision: 3, AppliedConfigSHA256: config, AppliedEndpointPort: 443, LocalListenPort: 18081},
			{ServiceID: "observability-a", ServiceType: "observability", DeploymentMode: "systemd", DatabaseName: "observability_db", EndpointRevision: 4, AppliedConfigRevision: 5, AppliedConfigSHA256: otherConfig, AppliedEndpointPort: 443, LocalListenPort: 18090},
		}}
	installed, err := BuildHostAgentConfigurePolicy(source)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := BuildSystemUpdatePortPolicy(source)
	if err != nil || !bytes.Equal(installed.Policy, baseline.Policy) {
		t.Fatal("baseline changed installed root bytes")
	}
	var before LocalExecutorPolicy
	if json.Unmarshal(baseline.Policy, &before) != nil {
		t.Fatal("decode baseline")
	}
	configT, _ := SystemdConfigurePortSidecarSHA256("worker", 18084, 4)
	old := contracts.SystemUpdatePortSnapshotRef{SourcePolicyRevision: 11, ProjectionRevision: 17, ExecutorPolicyRevision: 23, ExecutorPolicySHA256: contracts.ComputeSystemUpdatePortBytesSHA256(baseline.Policy),
		EndpointRevision: 7, AppliedEndpointRevision: 7, ConfigRevision: 3, ConfigSHA256: config, LocalListenPort: 18081, AdvertisedPort: 443}
	next := old
	next.SourcePolicyRevision++
	next.ProjectionRevision++
	next.ExecutorPolicyRevision++
	next.ConfigRevision++
	next.LocalListenPort = 18084
	next.ConfigSHA256 = configT
	next.ExecutorPolicySHA256 = ""
	payload, err := contracts.ApplySystemUpdatePortPolicyDelta(baseline.Policy, "worker-a", old, next)
	if err != nil {
		t.Fatal("shared delta rejected installed policy")
	}
	var after LocalExecutorPolicy
	if json.Unmarshal(payload, &after) != nil {
		t.Fatal("decode target policy")
	}
	if after.AgentUID != before.AgentUID || after.AgentGID != before.AgentGID || after.SocketPath != before.SocketPath || !reflect.DeepEqual(after.Mutation, before.Mutation) ||
		!reflect.DeepEqual(after.Targets[0], before.Targets[0]) || after.Targets[0].DatabaseName != "observability_db" ||
		!reflect.DeepEqual(after.Targets[1].Systemd, before.Targets[1].Systemd) || after.Targets[1].LocalListen.Port != 18084 || after.Targets[1].EndpointRevision != 7 {
		t.Fatal("delta changed unrelated authority or public endpoint revision")
	}
}

func TestSTPortDockerBaselineKeepsExistingFixedProfile(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	source := HostAgentConfigurePolicySource{PanelURL: "https://panel.example.test", ExecutionHostID: "host-a", AgentUID: 991, AgentGID: 992,
		SourcePolicyRevision: 11, ProjectionRevision: 17, LocalExecutorPolicyRevision: 23,
		Targets: []HostAgentConfigurePolicyTarget{{ServiceID: "worker-a", ServiceType: "worker", DeploymentMode: "docker", EndpointRevision: 7,
			AppliedConfigRevision: 3, AppliedConfigSHA256: digest, AppliedEndpointPort: 443, LocalListenPort: 18081,
			DockerSnapshot: &contracts.SystemUpdatePortDockerSnapshot{PublishedHostIP: "127.0.0.1", PublishedPort: 18081, ContainerPort: 8080, HealthPort: 18081,
				ComposePolicySHA256: digest, ComposeRevision: 9, VersionEnvSHA256: digest, ImageID: digest, RepositoryDigest: digest},
			DockerRoot: &contracts.UpdaterPortDockerRootBaseline{ComposeConfigSHA256: strings.Repeat("b", 64), CurrentVersion: "v1.2.3"}}}}
	if _, err := BuildHostAgentConfigurePolicy(source); err == nil {
		t.Fatal("baseline support expanded ordinary configure authority")
	}
	projection, err := BuildSystemUpdatePortPolicy(source)
	if err != nil {
		t.Fatal(err)
	}
	var policy LocalExecutorPolicy
	if json.Unmarshal(projection.Policy, &policy) != nil {
		t.Fatal("decode Docker baseline")
	}
	target := policy.Targets[0]
	if target.Docker.ComposeConfigSHA256 != strings.Repeat("b", 64) || target.Docker.PortComposePolicySHA256 != strings.Repeat("a", 64) || target.Docker.CurrentVersion != "v1.2.3" ||
		target.Docker.PortEnvFile != "/opt/autostream/local-executor/docker/ports/worker.env" || target.LocalListen.Host != "127.0.0.1" || target.LocalListen.Port != 18081 {
		t.Fatal("Docker baseline lost installed profile metadata")
	}
	target.Docker.PortEnvFile = "/tmp/attacker.env"
	if target.validate() == nil {
		t.Fatal("arbitrary root filepath accepted")
	}
	source.Targets[0].DockerRoot = nil
	if _, err := BuildSystemUpdatePortPolicy(source); err == nil {
		t.Fatal("missing installed Docker metadata guessed")
	}
}
