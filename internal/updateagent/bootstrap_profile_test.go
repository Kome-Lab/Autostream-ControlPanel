package updateagent

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestBuildStandardSystemdHelperConfigBuildsAllowlistedProfiles(t *testing.T) {
	requireStandardSystemdProfilePlatform(t)

	const (
		panelURL = "https://panel.example.com"
		hostID   = "host-a"
	)
	targets := []Target{
		{TargetID: "panel", HostID: hostID, ServiceType: "control_panel", DeploymentMode: ModeSystemd},
		{TargetID: "encoder", HostID: hostID, ServiceType: "encoder_recorder", DeploymentMode: ModeSystemd},
		{TargetID: "metrics", HostID: hostID, ServiceType: "observability", DeploymentMode: ModeSystemd},
		{TargetID: "discord", HostID: hostID, ServiceType: "discord_bot", DeploymentMode: ModeSystemd},
		{TargetID: "worker", HostID: hostID, ServiceType: "worker", DeploymentMode: ModeSystemd},
	}

	got, err := BuildStandardSystemdHelperConfig(panelURL, hostID, runtime.GOARCH, targets, map[string]string{
		"control_panel": "panel_db",
		"observability": "observability-db",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := HelperConfig{
		SchemaVersion: HelperConfigSchemaVersion,
		HostID:        hostID,
		PanelURL:      panelURL,
		Arch:          runtime.GOARCH,
		StateDir:      "/var/lib/autostream-update-host",
		Targets: []Target{
			{
				TargetID:       "panel",
				HostID:         hostID,
				ServiceType:    "control_panel",
				DeploymentMode: ModeSystemd,
				HealthURL:      "http://127.0.0.1:8080/health",
				VersionURL:     "http://127.0.0.1:8080/updater/version",
				BackupArgv:     []string{"/usr/local/sbin/autostream-backup-control-panel", "panel_db"},
				Systemd: &SystemdTarget{
					SystemctlPath: "/usr/bin/systemctl",
					RunuserPath:   "/usr/sbin/runuser",
					SmokeUser:     "autostream",
					Unit:          "autostream-control-panel.service",
					ReleaseRoot:   "/opt/autostream/control-panel/releases",
					CurrentLink:   "/opt/autostream/control-panel/current",
					BinaryPath:    "bin/control-panel",
					RequiredPaths: []string{"share/autostream-control-panel"},
				},
			},
			{
				TargetID:       "encoder",
				HostID:         hostID,
				ServiceType:    "encoder_recorder",
				DeploymentMode: ModeSystemd,
				HealthURL:      "http://127.0.0.1:8081/health",
				VersionURL:     "http://127.0.0.1:8081/updater/version",
				Systemd: &SystemdTarget{
					SystemctlPath: "/usr/bin/systemctl",
					RunuserPath:   "/usr/sbin/runuser",
					SmokeUser:     "autostream",
					Unit:          "autostream-encoder-recorder.service",
					ReleaseRoot:   "/opt/autostream/encoder-recorder/releases",
					CurrentLink:   "/opt/autostream/encoder-recorder/current",
					BinaryPath:    "bin/autostream-encoder-recorder",
				},
			},
			{
				TargetID:       "metrics",
				HostID:         hostID,
				ServiceType:    "observability",
				DeploymentMode: ModeSystemd,
				HealthURL:      "http://127.0.0.1:8082/health",
				VersionURL:     "http://127.0.0.1:8082/updater/version",
				BackupArgv:     []string{"/usr/local/sbin/autostream-backup-observability", "observability-db"},
				Systemd: &SystemdTarget{
					SystemctlPath: "/usr/bin/systemctl",
					RunuserPath:   "/usr/sbin/runuser",
					SmokeUser:     "autostream",
					Unit:          "autostream-observability.service",
					ReleaseRoot:   "/opt/autostream/observability/releases",
					CurrentLink:   "/opt/autostream/observability/current",
					BinaryPath:    "bin/autostream-observability",
				},
			},
			{
				TargetID:       "discord",
				HostID:         hostID,
				ServiceType:    "discord_bot",
				DeploymentMode: ModeSystemd,
				HealthURL:      "http://127.0.0.1:8083/health",
				VersionURL:     "http://127.0.0.1:8083/updater/version",
				Systemd: &SystemdTarget{
					SystemctlPath: "/usr/bin/systemctl",
					RunuserPath:   "/usr/sbin/runuser",
					SmokeUser:     "autostream",
					Unit:          "autostream-discord-bot.service",
					ReleaseRoot:   "/opt/autostream/discord-bot/releases",
					CurrentLink:   "/opt/autostream/discord-bot/current",
					BinaryPath:    "bin/autostream-discord-bot",
				},
			},
			{
				TargetID:       "worker",
				HostID:         hostID,
				ServiceType:    "worker",
				DeploymentMode: ModeSystemd,
				HealthURL:      "http://127.0.0.1:8084/health",
				VersionURL:     "http://127.0.0.1:8084/updater/version",
				Systemd: &SystemdTarget{
					SystemctlPath: "/usr/bin/systemctl",
					RunuserPath:   "/usr/sbin/runuser",
					SmokeUser:     "autostream",
					Unit:          "autostream-worker.service",
					ReleaseRoot:   "/opt/autostream/worker/releases",
					CurrentLink:   "/opt/autostream/worker/current",
					BinaryPath:    "bin/autostream-worker",
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("helper config mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("generated helper config is invalid: %v", err)
	}
}

func TestBuildStandardSystemdHelperConfigRejectsNonIdentityTargets(t *testing.T) {
	requireStandardSystemdProfilePlatform(t)

	base := Target{
		TargetID:       "worker",
		HostID:         "host-a",
		ServiceType:    "worker",
		DeploymentMode: ModeSystemd,
	}
	tests := map[string]func(*Target){
		"docker mode": func(target *Target) {
			target.DeploymentMode = ModeDocker
		},
		"health URL": func(target *Target) {
			target.HealthURL = "http://127.0.0.1:8084/custom"
		},
		"version URL": func(target *Target) {
			target.VersionURL = "http://127.0.0.1:8084/custom"
		},
		"backup command": func(target *Target) {
			target.BackupArgv = []string{"/tmp/custom"}
		},
		"systemd fields": func(target *Target) {
			target.Systemd = &SystemdTarget{Unit: "custom.service"}
		},
		"docker fields": func(target *Target) {
			target.Docker = &DockerTarget{DockerPath: "/usr/bin/docker"}
		},
		"explicit empty custom field": func(target *Target) {
			target.presentFields = map[string]bool{
				"target_id":       true,
				"host_id":         true,
				"service_type":    true,
				"deployment_mode": true,
				"health_url":      true,
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			target := base
			mutate(&target)
			if _, err := BuildStandardSystemdHelperConfig(
				"https://panel.example.com",
				"host-a",
				runtime.GOARCH,
				[]Target{target},
				nil,
			); err == nil {
				t.Fatal("expected identity-only systemd target rejection")
			}
		})
	}
}

func TestBuildStandardSystemdHelperConfigRejectsInvalidIdentityAndDatabaseInputs(t *testing.T) {
	requireStandardSystemdProfilePlatform(t)

	baseTarget := Target{
		TargetID:       "worker",
		HostID:         "host-a",
		ServiceType:    "worker",
		DeploymentMode: ModeSystemd,
	}
	tests := []struct {
		name          string
		panelURL      string
		hostID        string
		arch          string
		targets       []Target
		databaseNames map[string]string
		wantError     string
	}{
		{
			name:      "target host differs",
			panelURL:  "https://panel.example.com",
			hostID:    "host-b",
			arch:      runtime.GOARCH,
			targets:   []Target{baseTarget},
			wantError: "host_id",
		},
		{
			name:      "unsupported service",
			panelURL:  "https://panel.example.com",
			hostID:    "host-a",
			arch:      runtime.GOARCH,
			targets:   []Target{{TargetID: "other", HostID: "host-a", ServiceType: "other", DeploymentMode: ModeSystemd}},
			wantError: "service_type",
		},
		{
			name:      "missing database name",
			panelURL:  "https://panel.example.com",
			hostID:    "host-a",
			arch:      runtime.GOARCH,
			targets:   []Target{{TargetID: "panel", HostID: "host-a", ServiceType: "control_panel", DeploymentMode: ModeSystemd}},
			wantError: "database name",
		},
		{
			name:     "invalid database name",
			panelURL: "https://panel.example.com",
			hostID:   "host-a",
			arch:     runtime.GOARCH,
			targets:  []Target{{TargetID: "metrics", HostID: "host-a", ServiceType: "observability", DeploymentMode: ModeSystemd}},
			databaseNames: map[string]string{
				"observability": "bad.name",
			},
			wantError: "database name",
		},
		{
			name:     "database name too long",
			panelURL: "https://panel.example.com",
			hostID:   "host-a",
			arch:     runtime.GOARCH,
			targets:  []Target{{TargetID: "panel", HostID: "host-a", ServiceType: "control_panel", DeploymentMode: ModeSystemd}},
			databaseNames: map[string]string{
				"control_panel": strings.Repeat("a", 65),
			},
			wantError: "database name",
		},
		{
			name:     "database name for non owner",
			panelURL: "https://panel.example.com",
			hostID:   "host-a",
			arch:     runtime.GOARCH,
			targets:  []Target{baseTarget},
			databaseNames: map[string]string{
				"worker": "worker_db",
			},
			wantError: "databaseNames",
		},
		{
			name:      "invalid panel URL",
			panelURL:  "http://panel.example.com",
			hostID:    "host-a",
			arch:      runtime.GOARCH,
			targets:   []Target{baseTarget},
			wantError: "panel_url",
		},
		{
			name:      "invalid host",
			panelURL:  "https://panel.example.com",
			hostID:    "bad host",
			arch:      runtime.GOARCH,
			targets:   []Target{baseTarget},
			wantError: "host_id",
		},
		{
			name:      "invalid architecture",
			panelURL:  "https://panel.example.com",
			hostID:    "host-a",
			arch:      "386",
			targets:   []Target{baseTarget},
			wantError: "arch",
		},
		{
			name:      "no targets",
			panelURL:  "https://panel.example.com",
			hostID:    "host-a",
			arch:      runtime.GOARCH,
			wantError: "target",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildStandardSystemdHelperConfig(
				test.panelURL,
				test.hostID,
				test.arch,
				test.targets,
				test.databaseNames,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestBuildStandardSystemdHelperConfigSupportsRemoteArchitecture(t *testing.T) {
	requireStandardSystemdProfilePlatform(t)

	remoteArch := "arm64"
	if runtime.GOARCH == "arm64" {
		remoteArch = "amd64"
	}
	cfg, err := BuildStandardSystemdHelperConfig(
		"https://panel.example.com",
		"host-a",
		remoteArch,
		[]Target{{
			TargetID:       "worker",
			HostID:         "host-a",
			ServiceType:    "worker",
			DeploymentMode: ModeSystemd,
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("build remote %s helper config: %v", remoteArch, err)
	}
	if cfg.Arch != remoteArch {
		t.Fatalf("helper arch=%q want=%q", cfg.Arch, remoteArch)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("runtime validation unexpectedly accepted a foreign-architecture helper config")
	}
}

func TestValidateLegacyStandardSystemdHelperConfigRejectsManualProfiles(t *testing.T) {
	requireStandardSystemdProfilePlatform(t)

	build := func(t *testing.T) HelperConfig {
		t.Helper()
		cfg, err := BuildStandardSystemdHelperConfig(
			"https://panel.example.com",
			"host-a",
			runtime.GOARCH,
			[]Target{{
				TargetID:       "worker",
				HostID:         "host-a",
				ServiceType:    "worker",
				DeploymentMode: ModeSystemd,
			}},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	if err := validateLegacyStandardSystemdHelperConfig(build(t)); err != nil {
		t.Fatalf("generated standard profile rejected: %v", err)
	}

	tests := map[string]func(*HelperConfig){
		"docker": func(cfg *HelperConfig) {
			cfg.Targets[0].DeploymentMode = ModeDocker
			cfg.Targets[0].Systemd = nil
			cfg.Targets[0].Docker = &DockerTarget{}
		},
		"custom endpoint": func(cfg *HelperConfig) {
			cfg.Targets[0].HealthURL = "http://127.0.0.1:9090/health"
		},
		"custom unit": func(cfg *HelperConfig) {
			cfg.Targets[0].Systemd.Unit = "custom-worker.service"
		},
		"custom state directory": func(cfg *HelperConfig) {
			cfg.StateDir = "/var/lib/custom-update-host"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := build(t)
			mutate(&cfg)
			if err := validateLegacyStandardSystemdHelperConfig(cfg); err == nil {
				t.Fatal("manual legacy profile was accepted for automatic adoption")
			}
		})
	}
}

func requireStandardSystemdProfilePlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("standard systemd helper profiles require a supported Linux runtime")
	}
}
