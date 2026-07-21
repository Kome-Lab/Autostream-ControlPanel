//go:build linux

package updateagent

import (
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestPrepareUpdaterConfigRejectsNonRootBeforeFilesystemMutation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root rejection coverage")
	}
	path := filepath.Join(t.TempDir(), "must-not-exist", "updater.json")
	prepared, err := PrepareUpdaterConfig(path)
	if prepared != nil || err == nil || !strings.Contains(err.Error(), "requires root") {
		t.Fatalf("non-root prepare = %#v, %v", prepared, err)
	}
	if _, statErr := os.Lstat(filepath.Dir(path)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("non-root prepare mutated filesystem: %v", statErr)
	}
}

func TestPreparedUpdaterConfigRootOwnedAtomicMergeAndDriftFence(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned updater config policy")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "updater.json")
	cfg := validCentralTestConfig(t)
	cfg.PanelURL = "https://old-panel.example.com"
	cfg.NodeID = "old-updater"
	cfg.RuntimeToken = "old-runtime"
	cfg.ServiceName = "Old Updater"
	cfg.GitHubToken = "local-github-secret"
	cfg.API = APIConfig{BindHost: "127.0.0.1", Host: "updater.internal", Port: 9443, SSLEnabled: true, TLSCertFile: "/etc/autostream/tls/updater.crt", TLSKeyFile: "/etc/autostream/tls/updater.key"}
	cfg.StateDir = "/var/lib/custom-updater"
	cfg.PollIntervalSeconds = 23
	cfg.HeartbeatIntervalSeconds = 47
	existing, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, existing, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 0, 0); err != nil {
		t.Fatal(err)
	}

	prepared, err := prepareUpdaterConfig(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Abort()
	preflight, err := os.ReadFile(prepared.tempPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(preflight), "local-github-secret") || strings.Contains(string(preflight), "old-runtime") || strings.TrimSpace(string(preflight)) != "{}" {
		t.Fatalf("preflight temporary file contains existing secrets: %q", preflight)
	}
	identity := UpdaterConfigureIdentity{PanelURL: "https://new-panel.example.com", NodeID: "central-updater", RuntimeToken: "new-runtime", ServiceName: "Central Updater", ServiceType: "update_agent", API: APIConfig{Host: "must-not-persist.example.com", Port: 8090}}
	if err := prepared.Commit(identity); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"panel_url": identity.PanelURL, "node_id": identity.NodeID, "runtime_token": identity.RuntimeToken, "service_name": identity.ServiceName, "github_token": "local-github-secret", "state_dir": "/var/lib/custom-updater"} {
		var got string
		if err := json.Unmarshal(fields[name], &got); err != nil || got != want {
			t.Fatalf("configured %s = %q, %v; want %q", name, got, err, want)
		}
	}
	if strings.Contains(string(data), "must-not-persist.example.com") || !strings.Contains(string(data), "updater.internal") {
		t.Fatalf("local API policy changed: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Gid != 0 || info.Mode().Perm() != 0o640 {
		t.Fatalf("installed owner/mode = %#v %o", info.Sys(), info.Mode().Perm())
	}

	driftPrepared, err := prepareUpdaterConfig(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer driftPrepared.Abort()
	if err := os.WriteFile(path, append(data, '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := driftPrepared.Commit(identity); err == nil || !strings.Contains(err.Error(), "changed after preflight") {
		t.Fatalf("in-place drift commit = %v", err)
	}
}

func TestPreparedUpdaterConfigRejectsMissingAndInvalidLocalPolicyBeforeTokenInput(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned updater config policy")
	}
	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Chmod(root, 0o700); err != nil {
			t.Fatal(err)
		}
		prepared, err := prepareUpdaterConfig(filepath.Join(root, "updater.json"), 0)
		if prepared != nil || err == nil || !strings.Contains(err.Error(), "existing updater config is required") {
			t.Fatalf("missing local policy prepare = %#v, %v", prepared, err)
		}
		entries, readErr := os.ReadDir(root)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("missing local policy left files: %#v, %v", entries, readErr)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Chmod(root, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "updater.json")
		invalid := []byte(`{"panel_url":"https://panel.example.com","node_id":"central-updater","runtime_token":"placeholder","service_name":"Central Updater","github_token":"","api":{"bind_host":"127.0.0.1","host":"127.0.0.1","port":8090,"ssl_enabled":false},"state_dir":"/var/lib/autostream-updater","hosts":[],"targets":[]}`)
		if err := os.WriteFile(path, invalid, 0o640); err != nil {
			t.Fatal(err)
		}
		prepared, err := prepareUpdaterConfig(path, 0)
		if prepared != nil || err == nil || !strings.Contains(err.Error(), "before Configure Token input") || !strings.Contains(err.Error(), "github_token") {
			t.Fatalf("invalid local policy prepare = %#v, %v", prepared, err)
		}
		entries, readErr := os.ReadDir(root)
		if readErr != nil || len(entries) != 1 || entries[0].Name() != "updater.json" {
			t.Fatalf("invalid local policy left files: %#v, %v", entries, readErr)
		}
	})
}

func TestPreparedUpdaterConfigRejectsMalformedJSONBeforeNetworkPhase(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned updater config policy")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "updater.json")
	if err := os.WriteFile(path, []byte(`{"panel_url":`), 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareUpdaterConfig(path, 0)
	if prepared != nil || err == nil || !strings.Contains(err.Error(), "decode existing updater config") {
		t.Fatalf("malformed prepare = %#v, %v", prepared, err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 || entries[0].Name() != "updater.json" {
		t.Fatalf("malformed preflight left temporary files: %#v", entries)
	}
}

func TestPreparedUpdaterConfigUsesProductionServiceGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned updater config policy")
	}
	group, err := user.LookupGroup(updaterConfigInstallGroup)
	if err != nil {
		t.Skipf("production service group is unavailable: %v", err)
	}
	wantGID, err := strconv.Atoi(group.Gid)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "updater.json")
	cfg := validCentralTestConfig(t)
	existing, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, existing, 0o640); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareUpdaterConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.installGID != wantGID || !updaterConfigHasInstallOwner(prepared.tempInfo, wantGID) || prepared.tempInfo.Mode().Perm() != 0o640 {
		t.Fatalf("prepared production owner/mode gid=%d info=%#v mode=%o", prepared.installGID, prepared.tempInfo.Sys(), prepared.tempInfo.Mode().Perm())
	}
	prepared.Abort()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "updater.json" {
		t.Fatalf("abort left production temporary files: %#v", entries)
	}
}

func TestPreparedUpdaterConfigRejectsDestinationReplacementCreationAndDeletion(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned updater config policy")
	}
	identity := UpdaterConfigureIdentity{PanelURL: "https://panel.example.com", NodeID: "central-updater", RuntimeToken: "new-runtime", ServiceName: "Central Updater", ServiceType: "update_agent"}
	cfg := validCentralTestConfig(t)
	cfg.PanelURL = "https://old.example.com"
	cfg.NodeID = "old-updater"
	cfg.RuntimeToken = "old-runtime"
	cfg.ServiceName = "Old"
	valid, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		existing bool
		mutate   func(string) error
	}{
		{name: "replace", existing: true, mutate: func(path string) error {
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.WriteFile(path, valid, 0o640)
		}},
		{name: "delete", existing: true, mutate: os.Remove},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "updater.json")
			if test.existing {
				if err := os.WriteFile(path, valid, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := prepareUpdaterConfig(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Abort()
			if err := test.mutate(path); err != nil {
				t.Fatal(err)
			}
			if err := prepared.Commit(identity); err == nil || (!strings.Contains(err.Error(), "changed after preflight") && !strings.Contains(err.Error(), "appeared after preflight")) {
				t.Fatalf("destination %s commit = %v", test.name, err)
			}
		})
	}
}

func TestPreparedUpdaterConfigRejectsSymlinkAndUnsafeParent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned updater config policy")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "updater.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if prepared, err := prepareUpdaterConfig(link, 0); prepared != nil || err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink prepare = %#v, %v", prepared, err)
	}
	unsafe := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafe, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o770); err != nil {
		t.Fatal(err)
	}
	if prepared, err := prepareUpdaterConfig(filepath.Join(unsafe, "updater.json"), 0); prepared != nil || err == nil || !strings.Contains(err.Error(), "not writable by group") {
		t.Fatalf("unsafe parent prepare = %#v, %v", prepared, err)
	}
}

func TestPreparedUpdaterConfigInstalledIdentityIsReloadedBeforeActivation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned updater config policy")
	}
	cfg := validCentralTestConfig(t)
	cfg.PanelURL = "https://old-panel.example.com"
	cfg.NodeID = "old-updater"
	cfg.RuntimeToken = "old-runtime"
	cfg.ServiceName = "Old Updater"
	existing, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "updater.json")
	if err := os.WriteFile(path, existing, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 0, 0); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareUpdaterConfig(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Abort()
	identity := UpdaterConfigureIdentity{PanelURL: "https://panel.example.com", NodeID: "central-updater", RuntimeToken: "staged-runtime", ServiceName: "Central Updater", ServiceType: "update_agent"}
	if err := prepared.Commit(identity); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInstalledUpdaterIdentity(path, identity); err != nil {
		t.Fatalf("installed identity validation: %v", err)
	}
	mismatch := identity
	mismatch.RuntimeToken = "different-runtime"
	if err := ValidateInstalledUpdaterIdentity(path, mismatch); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("installed identity mismatch = %v", err)
	}
}
