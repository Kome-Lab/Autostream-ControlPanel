package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/updateagent"
)

func TestRunRequiresExplicitCommand(t *testing.T) {
	err := run(nil, localExecutorCLIDependencies{
		Output: &bytes.Buffer{},
		LoadPolicy: func(string, bool) (updateagent.LocalExecutorPolicy, error) {
			return updateagent.LocalExecutorPolicy{}, nil
		},
		ServeExecutor: func(context.Context, string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "usage: autostream-local-executor") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunStartsExecutorWithDefaultRootOwnedPolicy(t *testing.T) {
	var servedPath string
	err := run([]string{"run"}, localExecutorCLIDependencies{
		Output: &bytes.Buffer{},
		LoadPolicy: func(string, bool) (updateagent.LocalExecutorPolicy, error) {
			t.Fatal("run must let ServeLocalExecutor own secure policy loading")
			return updateagent.LocalExecutorPolicy{}, nil
		},
		ServeExecutor: func(_ context.Context, policyPath string) error {
			servedPath = policyPath
			return context.Canceled
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if servedPath != defaultLocalExecutorPolicyPath {
		t.Fatalf("served policy path=%q", servedPath)
	}
}

func TestRunValidatesRootOwnedPolicyAndPrintsDigest(t *testing.T) {
	output := &bytes.Buffer{}
	policy := updateagent.LocalExecutorPolicy{
		SchemaVersion:   updateagent.LocalExecutorPolicySchemaVersion,
		ProtocolVersion: updateagent.LocalExecutorProtocolVersion,
		HostID:          "host-a",
		AgentUID:        1234,
		AgentGID:        1234,
		SocketPath:      updateagent.LocalExecutorSocketPath,
		PolicyRevision:  1,
		Targets: []updateagent.LocalExecutorTarget{{
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: updateagent.ModeSystemd,
			ConfigRevision: 1,
			LocalListen:    updateagent.LocalExecutorEndpoint{Host: "127.0.0.1", Port: 8084},
			Systemd:        &updateagent.SystemdTarget{Unit: "autostream-worker.service"},
		}},
	}
	var loadedPath string
	var requireRootOwned bool
	err := run([]string{"validate-policy"}, localExecutorCLIDependencies{
		Output: output,
		LoadPolicy: func(path string, requireRoot bool) (updateagent.LocalExecutorPolicy, error) {
			loadedPath = path
			requireRootOwned = requireRoot
			return policy, nil
		},
		ServeExecutor: func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if loadedPath != defaultLocalExecutorPolicyPath || !requireRootOwned {
		t.Fatalf("path=%q require_root=%v", loadedPath, requireRootOwned)
	}
	digest, err := policy.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, digest) || !strings.Contains(got, "policy valid") {
		t.Fatalf("output=%q", got)
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	dependencies := localExecutorCLIDependencies{
		Output: &bytes.Buffer{},
		LoadPolicy: func(string, bool) (updateagent.LocalExecutorPolicy, error) {
			return updateagent.LocalExecutorPolicy{}, nil
		},
		ServeExecutor: func(context.Context, string) error { return nil },
	}
	for _, args := range [][]string{
		{"run", "--listen", "127.0.0.1:8090"},
		{"run", "--policy", "relative.json"},
		{"validate-policy", "extra"},
		{"apply"},
		{"recover-self-update"},
		{"recover-self-update", "--recovery-slot", "c"},
		{"recover-self-update", "--recovery-slot", "a", "--policy", "/tmp/policy.json"},
	} {
		if err := run(args, dependencies); err == nil {
			t.Fatalf("args %q unexpectedly accepted", args)
		}
	}
}

func TestRunDelegatesFixedSlotSelfUpdateRecovery(t *testing.T) {
	var recoveredSlot string
	dependencies := localExecutorCLIDependencies{
		Output: &bytes.Buffer{},
		LoadPolicy: func(string, bool) (updateagent.LocalExecutorPolicy, error) {
			return updateagent.LocalExecutorPolicy{}, nil
		},
		ServeExecutor: func(context.Context, string) error { return nil },
		RecoverSelfUpdate: func(_ context.Context, slot string) error {
			recoveredSlot = slot
			return nil
		},
	}
	if err := run(
		[]string{"recover-self-update", "--recovery-slot", "a"},
		dependencies,
	); err != nil {
		t.Fatalf("recover-self-update: %v", err)
	}
	if recoveredSlot != updateagent.HostSelfUpdateSlotA {
		t.Fatalf("recovered slot=%q", recoveredSlot)
	}
}

func TestRunDelegatesVerifiedBundleManualUpgrade(t *testing.T) {
	output := &bytes.Buffer{}
	var received updateagent.ManualHostUpgradeRequest
	rootChecked := false
	dependencies := localExecutorCLIDependencies{
		Output: output,
		LoadPolicy: func(string, bool) (updateagent.LocalExecutorPolicy, error) {
			return updateagent.LocalExecutorPolicy{}, nil
		},
		ServeExecutor: func(context.Context, string) error { return nil },
		RequireRoot: func() error {
			rootChecked = true
			return nil
		},
		UpgradeHostRuntime: func(
			_ context.Context,
			request updateagent.ManualHostUpgradeRequest,
		) (updateagent.ManualHostUpgradeResult, error) {
			received = request
			return updateagent.ManualHostUpgradeResult{
				PreviousSlot: updateagent.HostSelfUpdateSlotA,
				ActiveSlot:   updateagent.HostSelfUpdateSlotB,
				Version:      "v9.9.9",
			}, nil
		},
	}
	err := run([]string{
		"manual-upgrade-host-runtime",
		"--artifact-root", "/var/tmp/verified-host-agent",
		"--archive-sha256", strings.Repeat("a", 64),
		"--archive-size", "12345",
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if !rootChecked {
		t.Fatal("manual upgrade did not require root")
	}
	if received.ArtifactRoot != "/var/tmp/verified-host-agent" ||
		received.ArchiveSHA256 != strings.Repeat("a", 64) ||
		received.ArchiveSize != 12345 {
		t.Fatalf("manual upgrade request=%#v", received)
	}
	if got := output.String(); !strings.Contains(got, "v9.9.9") ||
		!strings.Contains(got, "slots/a -> slots/b") {
		t.Fatalf("manual upgrade output=%q", got)
	}
}

func TestManualUpgradeCancellationIsNotSuppressed(t *testing.T) {
	if suppressLocalExecutorCancellation(
		[]string{"manual-upgrade-host-runtime"}, context.Canceled,
	) {
		t.Fatal("manual upgrade cancellation would be reported as success")
	}
	if !suppressLocalExecutorCancellation([]string{"run"}, context.Canceled) {
		t.Fatal("normal server shutdown cancellation should remain quiet")
	}
	if !suppressLocalExecutorCancellation(
		[]string{"recover-self-update"}, context.Canceled,
	) {
		t.Fatal("existing recovery cancellation behavior should remain quiet")
	}
}

func TestRunEmergencyRuntimeCredentialRecoveryRequiresRootAndConfirmation(
	t *testing.T,
) {
	base := func() localExecutorCLIDependencies {
		return localExecutorCLIDependencies{
			Output: &bytes.Buffer{},
			LoadPolicy: func(
				string,
				bool,
			) (updateagent.LocalExecutorPolicy, error) {
				return updateagent.LocalExecutorPolicy{}, nil
			},
			ServeExecutor: func(context.Context, string) error {
				return nil
			},
		}
	}
	for _, args := range [][]string{
		{"recover-runtime-credential", "--rotation-id", "rotation-a"},
		{"recover-runtime-credential", "--confirm-emergency-revoked"},
		{"recover-runtime-credential", "--rotation-id", " rotation-a", "--confirm-emergency-revoked"},
		{"recover-runtime-credential", "--rotation-id", "rotation-a", "--confirm-emergency-revoked", "extra"},
		{"recover-runtime-credential", "--rotation-id", "rotation-a", "--confirm-emergency-revoked=sentinel-runtime-token-secret"},
	} {
		dependencies := base()
		called := false
		dependencies.RequireRoot = func() error {
			called = true
			return nil
		}
		dependencies.RecoverRuntimeCredential = func(string) error {
			t.Fatal("invalid request reached recovery")
			return nil
		}
		err := run(args, dependencies)
		if err == nil || called {
			t.Fatalf("args=%q err=%v root_called=%v", args, err, called)
		}
		if strings.Contains(
			err.Error(),
			"sentinel-runtime-token-secret",
		) {
			t.Fatalf("argument parse error leaked input: %v", err)
		}
	}

	dependencies := base()
	recovered := ""
	dependencies.RequireRoot = func() error {
		return errors.New("not root")
	}
	dependencies.RecoverRuntimeCredential = func(string) error {
		t.Fatal("non-root request reached recovery")
		return nil
	}
	args := []string{
		"recover-runtime-credential",
		"--rotation-id",
		"rotation-a",
		"--confirm-emergency-revoked",
	}
	if err := run(args, dependencies); err == nil ||
		err.Error() != "recover-runtime-credential requires root" {
		t.Fatalf("non-root error=%v", err)
	}

	dependencies.RequireRoot = func() error { return nil }
	dependencies.RecoverRuntimeCredential = func(rotationID string) error {
		recovered = rotationID
		return nil
	}
	if err := run(args, dependencies); err != nil {
		t.Fatal(err)
	}
	if recovered != "rotation-a" {
		t.Fatalf("recovered rotation=%q", recovered)
	}
}

func TestRunPrintsVersion(t *testing.T) {
	output := &bytes.Buffer{}
	err := run([]string{"version"}, localExecutorCLIDependencies{
		Output: output,
		LoadPolicy: func(string, bool) (updateagent.LocalExecutorPolicy, error) {
			return updateagent.LocalExecutorPolicy{}, nil
		},
		ServeExecutor: func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.HasPrefix(got, "autostream-local-executor ") {
		t.Fatalf("output=%q", got)
	}
}
