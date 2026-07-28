package updateagent

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitialSystemdPortSidecarPlansUseFourFixedRootPathsAndExactTwoLines(
	t *testing.T,
) {
	policy := configureTransactionPolicyFixture(t)
	plans, err := initialSystemdPortSidecarPlans(
		policy,
		defaultSystemdPortSidecarDirectory,
	)
	if err != nil {
		t.Fatalf("plan initial sidecars: %v", err)
	}
	if len(plans) != 4 {
		t.Fatalf("plans = %#v", plans)
	}
	want := map[string]string{
		"/opt/autostream/local-executor/ports/worker.env":           "AUTOSTREAM_BIND_ADDR=127.0.0.1:18081\nAUTOSTREAM_CONFIG_REVISION=11\n",
		"/opt/autostream/local-executor/ports/encoder-recorder.env": "AUTOSTREAM_BIND_ADDR=127.0.0.1:18082\nAUTOSTREAM_CONFIG_REVISION=12\n",
		"/opt/autostream/local-executor/ports/discord-bot.env":      "AUTOSTREAM_BIND_ADDR=127.0.0.1:18083\nAUTOSTREAM_CONFIG_REVISION=13\n",
		"/opt/autostream/local-executor/ports/observability.env":    "OBSERVABILITY_BIND_ADDR=127.0.0.1:18084\nAUTOSTREAM_CONFIG_REVISION=14\n",
	}
	for _, plan := range plans {
		wantBody, ok := want[filepath.ToSlash(plan.Path)]
		if !ok {
			t.Fatalf("unexpected sidecar path %q", plan.Path)
		}
		if string(plan.Body) != wantBody ||
			bytes.Count(plan.Body, []byte{'\n'}) != 2 ||
			plan.SHA256 != systemdPortSidecarSHA256(plan.Body) {
			t.Fatalf("non-canonical sidecar plan = %#v", plan)
		}
	}
}

func TestInitialSystemdPortSidecarPlansRejectTargetDigestDrift(t *testing.T) {
	policy := configureTransactionPolicyFixture(t)
	policy.Targets[0].ConfigSHA256 = "sha256:" + strings.Repeat("f", 64)
	if _, err := initialSystemdPortSidecarPlans(
		policy,
		defaultSystemdPortSidecarDirectory,
	); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("digest drift error = %v", err)
	}
}

func TestInitialSystemdPortSidecarsNeverOverwriteDifferingExistingFile(
	t *testing.T,
) {
	policy := configureTransactionPolicyFixture(t)
	plans, err := initialSystemdPortSidecarPlans(
		policy,
		defaultSystemdPortSidecarDirectory,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := make(map[string]initialSystemdPortSidecarSnapshot, len(plans))
	for _, plan := range plans {
		snapshots[plan.Path] = initialSystemdPortSidecarSnapshot{
			Existed: true,
			Body:    append([]byte(nil), plan.Body...),
		}
	}
	snapshots[plans[0].Path] = initialSystemdPortSidecarSnapshot{
		Existed: true,
		Body:    []byte("AUTOSTREAM_BIND_ADDR=127.0.0.1:19999\nAUTOSTREAM_CONFIG_REVISION=1\n"),
	}
	if err := validateInitialSystemdPortSidecarSnapshots(
		plans,
		snapshots,
	); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("different existing sidecar error = %v", err)
	}
}

func TestInitialSystemdPortSidecarsPermitExactExistingAndMissingFiles(
	t *testing.T,
) {
	policy := configureTransactionPolicyFixture(t)
	plans, err := initialSystemdPortSidecarPlans(
		policy,
		defaultSystemdPortSidecarDirectory,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := make(map[string]initialSystemdPortSidecarSnapshot, len(plans))
	for index, plan := range plans {
		snapshots[plan.Path] = initialSystemdPortSidecarSnapshot{
			Existed: index%2 == 0,
			Body:    append([]byte(nil), plan.Body...),
		}
	}
	if err := validateInitialSystemdPortSidecarSnapshots(
		plans,
		snapshots,
	); err != nil {
		t.Fatalf("exact/missing snapshots: %v", err)
	}
}

func configureTransactionPolicyFixture(t *testing.T) LocalExecutorPolicy {
	t.Helper()
	definitions := []struct {
		id          string
		serviceType string
		port        int
		revision    int64
		database    string
	}{
		{id: "worker-a", serviceType: "worker", port: 18081, revision: 11},
		{id: "encoder-a", serviceType: "encoder_recorder", port: 18082, revision: 12},
		{id: "discord-a", serviceType: "discord_bot", port: 18083, revision: 13},
		{id: "observability-a", serviceType: "observability", port: 18084, revision: 14, database: "autostream_observability"},
	}
	targets := make([]LocalExecutorTarget, 0, len(definitions))
	for index, definition := range definitions {
		profile, ok := standardSystemdProfileFor(definition.serviceType)
		if !ok {
			t.Fatalf("missing profile for %s", definition.serviceType)
		}
		adapter, err := systemdPortAdapterFor(
			definition.serviceType,
			profile.unit,
		)
		if err != nil {
			t.Fatal(err)
		}
		body := systemdPortSidecarBytes(
			adapter.BindVariable,
			"127.0.0.1",
			definition.port,
			definition.revision,
		)
		targets = append(targets, LocalExecutorTarget{
			ServiceID:        definition.id,
			ServiceType:      definition.serviceType,
			DeploymentMode:   ModeSystemd,
			DatabaseName:     definition.database,
			EndpointRevision: int64(index + 1),
			ConfigRevision:   definition.revision,
			ConfigSHA256:     systemdPortSidecarSHA256(body),
			LocalListen: LocalExecutorEndpoint{
				Host: "127.0.0.1",
				Port: definition.port,
			},
			Systemd: &SystemdTarget{
				SystemctlPath: "/usr/bin/systemctl",
				RunuserPath:   "/usr/sbin/runuser",
				SmokeUser:     "autostream",
				Unit:          profile.unit,
				ReleaseRoot:   profile.releaseRoot,
				CurrentLink:   profile.currentLink,
				BinaryPath:    profile.binaryPath,
				RequiredPaths: append([]string(nil), profile.requiredPaths...),
			},
		})
	}
	return LocalExecutorPolicy{
		SchemaVersion:        LocalExecutorMutationPolicySchemaVersion,
		ProtocolVersion:      LocalExecutorMutationProtocolVersion,
		HostID:               "host-a",
		AgentUID:             1001,
		AgentGID:             1002,
		SocketPath:           LocalExecutorSocketPath,
		SourcePolicyRevision: 3,
		ProjectionRevision:   4,
		PolicyRevision:       5,
		Mutation: &LocalExecutorMutationPolicy{
			PanelURL: "https://panel.example.com",
		},
		Targets: targets,
	}
}
