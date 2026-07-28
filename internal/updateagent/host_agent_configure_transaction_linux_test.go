//go:build linux

package updateagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestHostAgentConfigurationTransactionRollsBackPolicyAndNewSidecars(
	t *testing.T,
) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned Host Agent configuration transaction")
	}
	root, err := os.MkdirTemp(
		"/root",
		".autostream-host-configure-test-*",
	)
	if err != nil {
		t.Skipf("root-controlled test directory is unavailable: %v", err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	identityDir := filepath.Join(root, "identity")
	policyDir := filepath.Join(root, "policy")
	sidecarDir := filepath.Join(root, "ports")
	for _, directory := range []string{identityDir, policyDir, sidecarDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	identityPath := filepath.Join(identityDir, "host-agent.json")
	policyPath := filepath.Join(policyDir, "policy.json")

	identity, err := prepareUpdaterConfig(identityPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := prepareLocalExecutorPolicy(policyPath)
	if err != nil {
		identity.Abort()
		t.Fatal(err)
	}
	sidecars, err := prepareSystemdPortSidecars(sidecarDir)
	if err != nil {
		policy.Abort()
		identity.Abort()
		t.Fatal(err)
	}
	transaction := &PreparedHostAgentConfiguration{
		identity: identity,
		policy:   policy,
		sidecars: sidecars,
	}
	defer transaction.Abort()

	projection, err := BuildHostAgentConfigurePolicy(
		HostAgentConfigurePolicySource{
			PanelURL:                    "https://panel.example.com",
			ExecutionHostID:             "host-a",
			AgentUID:                    1001,
			AgentGID:                    1002,
			SourcePolicyRevision:        3,
			ProjectionRevision:          4,
			LocalExecutorPolicyRevision: 5,
			Targets: []HostAgentConfigurePolicyTarget{{
				ServiceID:             "worker-a",
				ServiceType:           "worker",
				DeploymentMode:        ModeSystemd,
				EndpointRevision:      2,
				AppliedConfigRevision: 7,
				AppliedEndpointPort:   18081,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	competitor := []byte("operator-created-before-identity-commit\n")
	if err := os.WriteFile(identityPath, competitor, 0o640); err != nil {
		t.Fatal(err)
	}
	err = transaction.Commit(
		UpdaterConfigureIdentity{
			PanelURL:      "https://panel.example.com",
			NodeID:        "host-agent-a",
			RuntimeToken:  "runtime-token",
			ServiceName:   "Host Agent A",
			ServiceType:   "update_agent",
			TransportMode: "pull_v2",
		},
		projection,
	)
	if err == nil || !strings.Contains(err.Error(), "appeared after preflight") {
		t.Fatalf("identity race commit error = %v", err)
	}
	gotCompetitor, err := os.ReadFile(identityPath)
	if err != nil || string(gotCompetitor) != string(competitor) {
		t.Fatalf("identity competitor was overwritten: %q, %v", gotCompetitor, err)
	}
	if _, err := os.Lstat(policyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("policy survived failed transaction: %v", err)
	}
	workerSidecar := filepath.Join(sidecarDir, "worker.env")
	if _, err := os.Lstat(workerSidecar); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new sidecar survived failed transaction: %v", err)
	}

	transaction.Abort()
	entries, err := os.ReadDir(sidecarDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("sidecar transaction left temporary files: %#v", entries)
	}
}

func TestHostAgentConfigurationTransactionPreservesPairWhenIdentityRenameReportsFailure(
	t *testing.T,
) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned Host Agent configuration transaction")
	}
	root, err := os.MkdirTemp(
		"/root",
		".autostream-host-configure-uncertain-test-*",
	)
	if err != nil {
		t.Skipf("root-controlled test directory is unavailable: %v", err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	identityDir := filepath.Join(root, "identity")
	policyDir := filepath.Join(root, "policy")
	sidecarDir := filepath.Join(root, "ports")
	for _, directory := range []string{identityDir, policyDir, sidecarDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	identityPath := filepath.Join(identityDir, "host-agent.json")
	policyPath := filepath.Join(policyDir, "policy.json")
	identity, err := prepareUpdaterConfig(identityPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := prepareLocalExecutorPolicy(policyPath)
	if err != nil {
		identity.Abort()
		t.Fatal(err)
	}
	sidecars, err := prepareSystemdPortSidecars(sidecarDir)
	if err != nil {
		policy.Abort()
		identity.Abort()
		t.Fatal(err)
	}
	transaction := &PreparedHostAgentConfiguration{
		identity: identity,
		policy:   policy,
		sidecars: sidecars,
	}
	defer transaction.Abort()

	projection, err := BuildHostAgentConfigurePolicy(
		HostAgentConfigurePolicySource{
			PanelURL:                    "https://panel.example.com",
			ExecutionHostID:             "host-a",
			AgentUID:                    1001,
			AgentGID:                    1002,
			SourcePolicyRevision:        3,
			ProjectionRevision:          4,
			LocalExecutorPolicyRevision: 5,
			Targets: []HostAgentConfigurePolicyTarget{{
				ServiceID:             "worker-a",
				ServiceType:           "worker",
				DeploymentMode:        ModeSystemd,
				EndpointRevision:      2,
				AppliedConfigRevision: 7,
				AppliedEndpointPort:   18081,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	identity.renamePath = func(tempPath, destinationPath string) error {
		if err := os.Rename(tempPath, destinationPath); err != nil {
			return err
		}
		return syscall.EIO
	}
	identityValue := UpdaterConfigureIdentity{
		PanelURL:      "https://panel.example.com",
		NodeID:        "host-agent-a",
		RuntimeToken:  "runtime-token",
		ServiceName:   "Host Agent A",
		ServiceType:   "update_agent",
		TransportMode: "pull_v2",
	}
	err = transaction.Commit(identityValue, projection)
	if err == nil || !strings.Contains(err.Error(), "was installed") {
		t.Fatalf("identity rename uncertainty = %v", err)
	}
	if !identity.committed {
		t.Fatal("identity rename result was not marked committed")
	}
	if err := ValidateInstalledUpdaterIdentity(identityPath, identityValue); err != nil {
		t.Fatalf("installed identity was not preserved: %v", err)
	}
	if _, err := os.Lstat(policyPath); err != nil {
		t.Fatalf("policy was rolled back after identity rename uncertainty: %v", err)
	}
	workerSidecar := filepath.Join(sidecarDir, "worker.env")
	if _, err := os.Lstat(workerSidecar); err != nil {
		t.Fatalf("sidecar was rolled back after identity rename uncertainty: %v", err)
	}
}
