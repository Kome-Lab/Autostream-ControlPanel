package updateagent

import (
	"reflect"
	"strings"
	"testing"
)

func TestRemoteSystemdBootstrapPathsUsesFixedServiceLayout(t *testing.T) {
	targets := make([]Target, 0, len(remoteSystemdServiceDirectories)+1)
	want := make([]string, 0, len(remoteSystemdServiceDirectories)*2)
	for serviceType, serviceDir := range remoteSystemdServiceDirectories {
		base := "/opt/autostream/" + serviceDir
		targets = append(targets, Target{
			TargetID:       serviceDir,
			ServiceType:    serviceType,
			DeploymentMode: ModeSystemd,
			Systemd: &SystemdTarget{
				ReleaseRoot: base + "/releases",
				CurrentLink: base + "/current",
			},
		})
		want = append(want, base, base+"/releases")
	}
	targets = append(targets, Target{TargetID: "docker-only", ServiceType: "worker", DeploymentMode: ModeDocker})

	got, err := remoteSystemdBootstrapPaths(HelperConfig{Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	sortStrings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrap paths = %#v, want %#v", got, want)
	}
}

func TestRemoteSystemdBootstrapPathsDeduplicatesSharedPolicy(t *testing.T) {
	target := Target{
		TargetID:       "worker-a",
		ServiceType:    "worker",
		DeploymentMode: ModeSystemd,
		Systemd: &SystemdTarget{
			ReleaseRoot: "/opt/autostream/worker/releases",
			CurrentLink: "/opt/autostream/worker/current",
		},
	}
	second := target
	second.TargetID = "worker-b"
	got, err := remoteSystemdBootstrapPaths(HelperConfig{Targets: []Target{target, second}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/autostream/worker", "/opt/autostream/worker/releases"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrap paths = %#v, want %#v", got, want)
	}
}

func TestRemoteSystemdBootstrapPathsRejectsArbitraryProductionPaths(t *testing.T) {
	base := Target{
		TargetID:       "control-panel",
		ServiceType:    "control_panel",
		DeploymentMode: ModeSystemd,
		Systemd: &SystemdTarget{
			ReleaseRoot: "/opt/autostream/control-panel/releases",
			CurrentLink: "/opt/autostream/control-panel/current",
		},
	}
	tests := map[string]func(*Target){
		"outside prefix":  func(target *Target) { target.Systemd.ReleaseRoot = "/tmp/control-panel/releases" },
		"wrong service":   func(target *Target) { target.Systemd.ReleaseRoot = "/opt/autostream/worker/releases" },
		"non canonical":   func(target *Target) { target.Systemd.ReleaseRoot = "/opt/autostream/control-panel/./releases" },
		"wrong link name": func(target *Target) { target.Systemd.CurrentLink = "/opt/autostream/control-panel/live" },
		"missing policy":  func(target *Target) { target.Systemd = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			target := base
			copy := *base.Systemd
			target.Systemd = &copy
			mutate(&target)
			if _, err := remoteSystemdBootstrapPaths(HelperConfig{Targets: []Target{target}}); err == nil || !strings.Contains(err.Error(), "targets[0]") {
				t.Fatalf("unsafe path result = %v", err)
			}
		})
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
