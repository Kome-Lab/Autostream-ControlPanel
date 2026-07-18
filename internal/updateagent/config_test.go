package updateagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func validTestConfig(t *testing.T) Config {
	t.Helper()
	abs := func(parts ...string) string {
		path := filepath.Join(append([]string{t.TempDir()}, parts...)...)
		if runtime.GOOS != "windows" {
			return "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")
		}
		return path
	}
	return Config{
		PanelURL: "https://panel.example.com", NodeID: "updater-01", RuntimeToken: "secret", GitHubToken: "github-read-token",
		API:      APIConfig{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090},
		StateDir: abs("state"), HelperArgv: []string{abs("bin", "autostream-updater")},
		Targets: []Target{{
			TargetID: "worker-01", ServiceType: "worker", DeploymentMode: ModeSystemd,
			HealthURL: "http://127.0.0.1:8081/health", VersionURL: "http://127.0.0.1:8081/version",
			Systemd: &SystemdTarget{SystemctlPath: abs("bin", "systemctl"), RunuserPath: abs("bin", "runuser"), SmokeUser: "autostream", Unit: "autostream-worker.service", ReleaseRoot: abs("releases"), CurrentLink: abs("current"), BinaryPath: "bin/worker"},
		}},
	}
}

func validCentralTestConfig(t *testing.T) Config {
	t.Helper()
	cfg := validTestConfig(t)
	identity, publicKey := writeRemoteSSHIdentity(t)
	knownHosts := writeRemoteSSHKnownHosts(t, "192.0.2.10", publicKey)
	cfg.HelperArgv = nil
	cfg.Hosts = []SSHHost{{HostID: "edge-01", Name: "Edge 01", Address: "192.0.2.10", Port: 22, User: "autostream-update-host", IdentityFile: identity, KnownHostsFile: knownHosts, Arch: "amd64"}}
	cfg.Targets = []Target{{TargetID: "worker-01", HostID: "edge-01", ServiceType: "worker", DeploymentMode: ModeSystemd}}
	return cfg
}

func TestBootstrapComposeDigestSentinelIsRejectedByRuntimeLoader(t *testing.T) {
	cfg := validTestConfig(t)
	abs := filepath.Dir(cfg.StateDir)
	cfg.Targets[0] = Target{
		TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker,
		HealthURL: "http://localhost:8081/health", VersionURL: "http://localhost:8081/version",
		Docker: &DockerTarget{DockerPath: filepath.Join(abs, "docker"), ComposeProject: "autostream", ProjectDir: abs, ComposeFiles: []string{filepath.Join(abs, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "AUTOSTREAM_DOCKER_VERSION", VersionEnvFile: filepath.Join(abs, "worker.env"), CurrentVersion: "v1.0.0", ComposeConfigSHA256: strings.Repeat("0", 64)},
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "updater.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path, false); err == nil || !strings.Contains(err.Error(), "bootstrap sentinel") {
		t.Fatalf("runtime loader accepted bootstrap sentinel: %v", err)
	}
	if _, err := LoadBootstrapConfig(path, false); err != nil {
		t.Fatalf("bootstrap loader rejected explicit sentinel: %v", err)
	}
	cfg.Targets[0].Docker.ComposeConfigSHA256 = strings.Repeat("a", 64)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("runtime config rejected real compose digest: %v", err)
	}
}

func TestConfigValidateRejectsRemoteHTTPAndMissingDatabaseBackup(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.PanelURL = "http://panel.example.com"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected remote HTTP rejection, got %v", err)
	}
	cfg = validTestConfig(t)
	cfg.Targets[0].ServiceType = "control_panel"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "backup_argv") {
		t.Fatalf("expected backup requirement, got %v", err)
	}
}

func TestConfigValidateRejectsUnsafeOrAmbiguousTargets(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Targets[0].Systemd.BinaryPath = "../worker"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected traversal path rejection")
	}
	cfg = validTestConfig(t)
	cfg.Targets = append(cfg.Targets, cfg.Targets[0])
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate target rejection, got %v", err)
	}
}

func TestDockerConfigRequiresCanonicalVersionVariable(t *testing.T) {
	cfg := validTestConfig(t)
	abs := filepath.Dir(cfg.StateDir)
	cfg.Targets[0] = Target{
		TargetID: "worker-docker", ServiceType: "worker", DeploymentMode: ModeDocker,
		HealthURL: "http://localhost:8081/health", VersionURL: "http://localhost:8081/version",
		Docker: &DockerTarget{DockerPath: filepath.Join(abs, "docker"), ComposeProject: "autostream", ProjectDir: abs, ComposeFiles: []string{filepath.Join(abs, "compose.yml")}, Service: "worker", ImageRepo: "ghcr.io/kome-lab/autostream-docker/worker", ImageVariable: "WORKER_IMAGE", VersionEnvFile: filepath.Join(abs, "worker.env"), CurrentVersion: "v1.0.0", ComposeConfigSHA256: strings.Repeat("0", 64)},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "AUTOSTREAM_DOCKER_VERSION") {
		t.Fatalf("expected canonical variable rejection, got %v", err)
	}
}

func TestAPIConfigRequiresTLSOutsideLoopback(t *testing.T) {
	for _, api := range []APIConfig{
		{BindHost: "0.0.0.0", Host: "updater.internal", Port: 8090},
		{BindHost: "::", Host: "::1", Port: 8090},
		{BindHost: "127.0.0.1", Host: "updater.internal", Port: 8090},
	} {
		if err := api.Validate(); err == nil || !strings.Contains(err.Error(), "requires TLS") {
			t.Fatalf("expected plaintext remote API rejection for %+v, got %v", api, err)
		}
	}
	for _, api := range []APIConfig{
		{BindHost: "127.0.0.1", Host: "localhost", Port: 8090},
		{BindHost: "::1", Host: "::1", Port: 8090},
	} {
		if err := api.Validate(); err != nil {
			t.Fatalf("loopback API rejected for %+v: %v", api, err)
		}
	}
}

func TestCentralConfigRequiresStrictHostRoutingAndForbidsLocalHelper(t *testing.T) {
	cfg := validCentralTestConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid central config rejected: %v", err)
	}
	if host, ok := cfg.Host("edge-01"); !ok || host.Arch != "amd64" {
		t.Fatalf("host lookup = %#v, %v", host, ok)
	}
	if targets := cfg.TargetsForHost("edge-01"); len(targets) != 1 || targets[0].TargetID != "worker-01" {
		t.Fatalf("targets for host = %#v", targets)
	}

	t.Run("helper argv", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		cfg.HelperArgv = []string{filepath.Join(t.TempDir(), "helper")}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("central helper_argv result = %v", err)
		}
	})
	t.Run("missing host", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		cfg.Targets[0].HostID = ""
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "host_id") {
			t.Fatalf("missing host result = %v", err)
		}
	})
	t.Run("unknown host", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		cfg.Targets[0].HostID = "unknown"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("unknown host result = %v", err)
		}
	})
	t.Run("privileged target fields", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		cfg.Targets[0].HealthURL = "http://127.0.0.1:8080/health"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "identity fields only") {
			t.Fatalf("privileged central target result = %v", err)
		}
	})
	t.Run("explicit empty privileged JSON field", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		payload, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		payload = []byte(strings.Replace(string(payload), `"deployment_mode":"systemd"`, `"deployment_mode":"systemd","health_url":""`, 1))
		path := filepath.Join(t.TempDir(), "central.json")
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, false); err == nil || !strings.Contains(err.Error(), "identity fields only") {
			t.Fatalf("empty privileged field result = %v", err)
		}
	})
	t.Run("explicit empty hosts is central mode", func(t *testing.T) {
		cfg := validTestConfig(t)
		cfg.HelperArgv = nil
		cfg.Hosts = make([]SSHHost, 0)
		cfg.Targets = []Target{{TargetID: "worker-01", HostID: "edge-01", ServiceType: "worker", DeploymentMode: ModeSystemd}}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("empty hosts result = %v", err)
		}
	})
	t.Run("explicit null hosts is central mode", func(t *testing.T) {
		cfg := validTestConfig(t)
		payload, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		payload = []byte(strings.Replace(string(payload), `"targets":`, `"hosts":null,"targets":`, 1))
		path := filepath.Join(t.TempDir(), "null-hosts.json")
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, false); err == nil || !strings.Contains(err.Error(), "helper_argv is forbidden") {
			t.Fatalf("null hosts result = %v", err)
		}
	})
	t.Run("identity target marshals four fields only", func(t *testing.T) {
		payload, err := json.Marshal(validCentralTestConfig(t).Targets[0])
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 4 {
			t.Fatalf("central target fields = %s", payload)
		}
	})
}

func TestCentralConfigRejectsUnsafeOrSharedSSHCredentials(t *testing.T) {
	t.Run("root user", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		cfg.Hosts[0].User = "root"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "non-root") {
			t.Fatalf("root SSH result = %v", err)
		}
	})
	t.Run("unsupported arch", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		cfg.Hosts[0].Arch = "386"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "amd64 or arm64") {
			t.Fatalf("arch result = %v", err)
		}
	})
	t.Run("relative identity", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		cfg.Hosts[0].IdentityFile = "edge.key"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Fatalf("relative identity result = %v", err)
		}
	})
	t.Run("identity reused", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		second := cfg.Hosts[0]
		second.HostID = "edge-02"
		second.Name = "Edge 02"
		second.Address = "192.0.2.11"
		cfg.Hosts = append(cfg.Hosts, second)
		cfg.Targets = append(cfg.Targets, Target{TargetID: "worker-02", HostID: "edge-02", ServiceType: "worker", DeploymentMode: ModeSystemd})
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unique per host") {
			t.Fatalf("shared identity result = %v", err)
		}
	})
	t.Run("identity copied", func(t *testing.T) {
		cfg := validCentralTestConfig(t)
		identityCopy := filepath.Join(t.TempDir(), "copied-identity")
		payload, err := os.ReadFile(cfg.Hosts[0].IdentityFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(identityCopy, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		second := cfg.Hosts[0]
		second.HostID = "edge-02"
		second.Name = "Edge 02"
		second.Address = "192.0.2.11"
		second.IdentityFile = identityCopy
		cfg.Hosts = append(cfg.Hosts, second)
		cfg.Targets = append(cfg.Targets, Target{TargetID: "worker-02", HostID: "edge-02", ServiceType: "worker", DeploymentMode: ModeSystemd})
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "SSH identity must be unique") {
			t.Fatalf("copied identity result = %v", err)
		}
	})
	if runtime.GOOS != "windows" {
		t.Run("world readable identity", func(t *testing.T) {
			cfg := validCentralTestConfig(t)
			if err := os.Chmod(cfg.Hosts[0].IdentityFile, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "group-readable") {
				t.Fatalf("identity permissions result = %v", err)
			}
		})
	}
}

func TestLegacyConfigRejectsHostIDWithoutCentralHosts(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Targets[0].HostID = "edge-01"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires central hosts") {
		t.Fatalf("legacy host_id result = %v", err)
	}
}

func TestConfigLoaderUsesBoundedNonSymlinkStableFile(t *testing.T) {
	cfg := validTestConfig(t)
	payload, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		realPath := filepath.Join(dir, "real.json")
		linkPath := filepath.Join(dir, "link.json")
		if err := os.WriteFile(realPath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPath, linkPath); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := LoadConfig(linkPath, false); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("symlink result = %v", err)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "oversize.json")
		if err := os.WriteFile(path, bytes.Repeat([]byte("x"), configMaxBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, false); err == nil || !strings.Contains(err.Error(), "bounded") {
			t.Fatalf("oversize result = %v", err)
		}
	})
	t.Run("replaced between stat and open", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		replacement := filepath.Join(dir, "replacement.json")
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		expected, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.NodeID = "updater-replacement"
		replacementPayload, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacement, replacementPayload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		file, _, err := openVerifiedConfig(path, expected)
		if file != nil {
			_ = file.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "changed") {
			t.Fatalf("replacement result = %v", err)
		}
	})
	t.Run("trailing JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "trailing.json")
		if err := os.WriteFile(path, append(append([]byte(nil), payload...), []byte(` {}`)...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path, false); err == nil || !strings.Contains(err.Error(), "trailing") {
			t.Fatalf("trailing result = %v", err)
		}
	})
}
