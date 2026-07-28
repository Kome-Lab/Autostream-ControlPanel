package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/updateagent"
)

func TestRunHostAgentRequiresExplicitCommand(t *testing.T) {
	err := run(nil, hostAgentTestDependencies(t))
	if err == nil || !strings.Contains(err.Error(), "usage: autostream-host-agent run") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunHostAgentUsesOnlyRootOwnedFourFieldIdentity(t *testing.T) {
	dependencies := hostAgentTestDependencies(t)
	var loadedPath string
	var requireRootOwned bool
	var started updateagent.Config
	dependencies.LoadIdentity = func(path string, requireRoot bool) (updateagent.Config, error) {
		loadedPath = path
		requireRootOwned = requireRoot
		return updateagent.Config{
			PanelURL:     "https://panel.example.com",
			NodeID:       "host-agent-a",
			RuntimeToken: "runtime-token",
			ServiceName:  "Host Agent A",
		}, nil
	}
	dependencies.Start = func(_ context.Context, identity updateagent.Config) error {
		started = identity
		return nil
	}

	if err := run([]string{"run", "--config", "/root/identity.json"}, dependencies); err != nil {
		t.Fatal(err)
	}
	if loadedPath != "/root/identity.json" || !requireRootOwned {
		t.Fatalf("load = path %q root-owned %v", loadedPath, requireRootOwned)
	}
	if started.NodeID != "host-agent-a" || started.RuntimeToken != "runtime-token" {
		t.Fatalf("started identity = %#v", started)
	}
}

func TestValidateHostAgentConfigDoesNotStartAgent(t *testing.T) {
	dependencies := hostAgentTestDependencies(t)
	started := false
	dependencies.Start = func(context.Context, updateagent.Config) error {
		started = true
		return errors.New("must not start")
	}
	if err := run([]string{"validate-config"}, dependencies); err != nil {
		t.Fatal(err)
	}
	if started {
		t.Fatal("validate-config started the host agent")
	}
	if got := dependencies.Output.(*bytes.Buffer).String(); got != "host agent identity configuration valid\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestHostAgentCLIRejectsRuntimeTokenArgument(t *testing.T) {
	err := run([]string{"run", "--runtime-token", "must-not-enter-argv"}, hostAgentTestDependencies(t))
	if err == nil {
		t.Fatal("runtime token argument was accepted")
	}
}

func TestHostAgentConfigureCommandDelegatesWithoutTokenArgument(t *testing.T) {
	dependencies := hostAgentTestDependencies(t)
	var got []string
	dependencies.Configure = func(_ context.Context, args []string) error {
		got = append([]string(nil), args...)
		return nil
	}
	args := []string{"--panel-url", "https://panel.example.com", "--node", "host-agent-a"}
	if err := run(append([]string{"configure"}, args...), dependencies); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("configure args = %#v", got)
	}
}

func TestHostAgentVersionDoesNotLoadIdentity(t *testing.T) {
	dependencies := hostAgentTestDependencies(t)
	dependencies.LoadIdentity = func(string, bool) (updateagent.Config, error) {
		t.Fatal("version loaded identity")
		return updateagent.Config{}, nil
	}
	if err := run([]string{"--version"}, dependencies); err != nil {
		t.Fatal(err)
	}
	if got := dependencies.Output.(*bytes.Buffer).String(); !strings.HasPrefix(got, "autostream-host-agent ") {
		t.Fatalf("version output = %q", got)
	}
}

func hostAgentTestDependencies(t *testing.T) hostAgentCLIDependencies {
	t.Helper()
	return hostAgentCLIDependencies{
		LoadIdentity: func(path string, requireRoot bool) (updateagent.Config, error) {
			if path != defaultHostAgentConfigPath || !requireRoot {
				t.Fatalf("load = path %q root-owned %v", path, requireRoot)
			}
			return updateagent.Config{
				PanelURL:     "https://panel.example.com",
				NodeID:       "host-agent-a",
				RuntimeToken: "runtime-token",
				ServiceName:  "Host Agent A",
			}, nil
		},
		Start:     func(context.Context, updateagent.Config) error { return nil },
		Configure: func(context.Context, []string) error { return nil },
		Output:    &bytes.Buffer{},
	}
}
