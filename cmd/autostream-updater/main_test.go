package main

import (
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/updateagent"
)

func TestUsageExposesCentralConfigureRunAndValidation(t *testing.T) {
	err := run([]string{"unknown"})
	if err == nil || !strings.Contains(err.Error(), "configure") || !strings.Contains(err.Error(), "validate-config") || strings.Contains(err.Error(), "--token") || strings.Contains(err.Error(), "bootstrap-docker-target") || strings.Contains(err.Error(), "helper") {
		t.Fatalf("usage error = %v", err)
	}
}

func TestConfigureRequiresPanelURLBeforeLocalMutation(t *testing.T) {
	err := run([]string{"configure", "--node", "central-updater", "--config", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--panel-url is required") {
		t.Fatalf("configure missing panel URL = %v", err)
	}
}

func TestLegacyUpdaterCommandsAreNotDeployable(t *testing.T) {
	for _, command := range [][]string{{"bootstrap-docker-target"}, {"compose-config-digest"}, {"helper", "apply"}} {
		err := run(command)
		if err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Fatalf("legacy command %v was not rejected: %v", command, err)
		}
	}
}

func TestValidateConfigRejectsPositionalInputsBeforeLoading(t *testing.T) {
	err := run([]string{"validate-config", "unexpected"})
	if err == nil || !strings.Contains(err.Error(), "only --config PATH") {
		t.Fatalf("unexpected validate-config result: %v", err)
	}
}

func TestCentralEntrypointRejectsConfigurationWithoutHosts(t *testing.T) {
	if err := requireCentralConfig(updateagent.Config{}); err == nil || !strings.Contains(err.Error(), "hosts") {
		t.Fatalf("hosts-omitted result = %v", err)
	}
	if err := requireCentralConfig(updateagent.Config{Hosts: []updateagent.SSHHost{{HostID: "host-a"}}}); err != nil {
		t.Fatalf("central hosts result = %v", err)
	}
}
