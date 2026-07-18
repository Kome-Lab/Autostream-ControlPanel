package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/updateagent"
)

type panicCLIReader struct{}

func (panicCLIReader) Read([]byte) (int, error) { panic("stdin must not be read") }

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &stderr, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "autostream-update-host") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunInstallerSystemdPathsWritesOnlyPlannerOutput(t *testing.T) {
	original := loadRemoteSystemdBootstrapPaths
	defer func() { loadRemoteSystemdBootstrapPaths = original }()
	loadRemoteSystemdBootstrapPaths = func(path string) ([]string, error) {
		if path != "/root/policy.json" {
			t.Fatalf("config path = %q", path)
		}
		return []string{"/opt/autostream/control-panel", "/opt/autostream/control-panel/releases"}, nil
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"installer-systemd-paths", "--config", "/root/policy.json"}, panicCLIReader{}, &stdout, &stderr, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "/opt/autostream/control-panel\n/opt/autostream/control-panel/releases\n"; got != want || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", got, stderr.String())
	}
}

func TestRunInstallerSystemdPathsFailsClosedWithoutOutput(t *testing.T) {
	original := loadRemoteSystemdBootstrapPaths
	defer func() { loadRemoteSystemdBootstrapPaths = original }()
	loadRemoteSystemdBootstrapPaths = func(string) ([]string, error) {
		return nil, errors.New("sensitive detail")
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"installer-systemd-paths", "--config", "/root/policy.json"}, panicCLIReader{}, &stdout, &stderr, func(string) string { return "" })
	if err == nil || err.Error() != "installer systemd path policy rejected" || stdout.Len() != 0 || strings.Contains(stderr.String(), "sensitive detail") {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestRunRPCRejectsUnsetAndAlteredForcedCommandBeforeStdin(t *testing.T) {
	if updateagent.RequireRemoteHelperRoot() != nil {
		t.Skip("root-only CLI")
	}
	config := filepath.Join(t.TempDir(), "missing.json")
	for _, original := range []string{"", updateagent.RemoteFixedCommand + " ", "different-command"} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), []string{"rpc", "--config", config}, panicCLIReader{}, &stdout, &stderr, func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return original
			}
			return ""
		})
		if err == nil || err.Error() != "rpc rejected" || stdout.Len() != 0 {
			t.Fatalf("original=%q err=%v stdout=%q", original, err, stdout.String())
		}
	}
}

func TestRunBootstrapFailureNeverEchoesStdinSecret(t *testing.T) {
	if updateagent.RequireRemoteHelperRoot() != nil {
		t.Skip("root-only CLI")
	}
	secret := "github-cli-secret"
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"bootstrap-docker-target", "--config", filepath.Join(t.TempDir(), "missing.json"), "--target", "worker"}, strings.NewReader(secret), &stdout, &stderr, func(string) string { return "" })
	combined := stdout.String() + stderr.String()
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(combined, secret) {
		t.Fatalf("err=%v output=%q", err, combined)
	}
}
