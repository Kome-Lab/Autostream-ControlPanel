package updateagent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func snapshotTestPolicy() ManagedPolicy {
	return ManagedPolicy{
		UpdaterID: "central-updater", Revision: 3,
		API:                 APIConfig{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090},
		PollIntervalSeconds: 15, HeartbeatIntervalSeconds: 30,
		Hosts: []ManagedPolicyHost{{
			HostID: "edge-01", Name: "Edge 01", Address: "192.0.2.10", Port: 55850,
			User: "autostream-update-host", Arch: "amd64",
			HostPublicKey: "ssh-ed25519 AAAATEST", HostPublicKeyFingerprint: "SHA256:test",
		}},
		Targets:   []Target{{TargetID: "worker-01", HostID: "edge-01", ServiceType: "worker", DeploymentMode: ModeSystemd}},
		UpdatedAt: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
	}
}

func TestFileManagedPolicySnapshotReportsPostRenameCommit(t *testing.T) {
	stateDir := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(stateDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy := snapshotTestPolicy()
	store := FileManagedPolicySnapshotStore{
		StateDir: stateDir,
		syncDirectory: func(string) error {
			return errors.New("simulated directory sync failure")
		},
	}
	err := store.Save(policy)
	if err == nil || !ManagedPolicySnapshotWasCommitted(err) {
		t.Fatalf("post-rename save error commit state = %v committed=%v", err, ManagedPolicySnapshotWasCommitted(err))
	}
	loaded, exists, loadErr := (FileManagedPolicySnapshotStore{StateDir: stateDir}).Load()
	if loadErr != nil || !exists || loaded.Revision != policy.Revision {
		t.Fatalf("renamed snapshot was not authoritative after durability warning: policy=%+v exists=%v err=%v", loaded, exists, loadErr)
	}
}

func TestFileManagedPolicySnapshotIsSecretFreeAndStrict(t *testing.T) {
	stateDir := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(stateDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store := FileManagedPolicySnapshotStore{StateDir: stateDir}
	policy := snapshotTestPolicy()
	if err := store.Save(policy); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, managedPolicySnapshotName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"runtime_token", "github_token", "release_token", "identity_file", "id_ed25519", "private_key"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("snapshot contains forbidden secret field %q: %s", forbidden, data)
		}
	}
	loaded, exists, err := store.Load()
	if err != nil || !exists || loaded.Revision != policy.Revision || loaded.UpdaterID != policy.UpdaterID {
		t.Fatalf("loaded snapshot = %#v exists=%v err=%v", loaded, exists, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("snapshot mode = %v", info.Mode().Perm())
		}
	}

	if err := os.WriteFile(path, []byte(`{"schema_version":1,"policy":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("corrupt snapshot was not rejected: %v", err)
	}
}

func TestFileManagedPolicySnapshotRejectsUnsafeExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix ownership and mode contract")
	}
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, managedPolicySnapshotName)
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := FileManagedPolicySnapshotStore{StateDir: stateDir}
	if _, _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "private daemon-owned") {
		t.Fatalf("unsafe snapshot load = %v", err)
	}
	if err := store.Save(snapshotTestPolicy()); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe snapshot save = %v", err)
	}
}
