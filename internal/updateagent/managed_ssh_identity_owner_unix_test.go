//go:build !windows

package updateagent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type managedSSHOwnerTestInfo struct {
	uid uint32
}

func (i managedSSHOwnerTestInfo) Name() string       { return "managed-ssh-owner-test" }
func (i managedSSHOwnerTestInfo) Size() int64        { return 1 }
func (i managedSSHOwnerTestInfo) Mode() fs.FileMode  { return 0o600 }
func (i managedSSHOwnerTestInfo) ModTime() time.Time { return time.Time{} }
func (i managedSSHOwnerTestInfo) IsDir() bool        { return false }
func (i managedSSHOwnerTestInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid} }

func TestManagedSSHOwnerValidationUsesEffectiveUser(t *testing.T) {
	current := uint32(os.Geteuid())
	if !managedSSHOwnedByCurrentUser(managedSSHOwnerTestInfo{uid: current}) {
		t.Fatal("current effective user was rejected")
	}
	other := current + 1
	if other == current {
		other = current - 1
	}
	if managedSSHOwnedByCurrentUser(managedSSHOwnerTestInfo{uid: other}) {
		t.Fatal("different owner was accepted")
	}
}

func TestEnsureManagedSSHIdentityRejectsRootBeforeMutation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only regression")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
	if err == nil || !strings.Contains(err.Error(), "non-root") {
		t.Fatalf("root managed identity initialization = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(stateDir, "ssh")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("root initialization mutated managed SSH state: %v", statErr)
	}
}

func TestPruneManagedSSHIdentitiesRejectsRootBeforeMutation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only regression")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	privatePath := filepath.Join(stateDir, "ssh", "edge-remove", managedSSHPrivateKeyName)
	if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privatePath, []byte("must-not-delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := PruneManagedSSHIdentities(stateDir, nil)
	if err == nil || !strings.Contains(err.Error(), "non-root") {
		t.Fatalf("root managed identity prune = %v", err)
	}
	if data, readErr := os.ReadFile(privatePath); readErr != nil || string(data) != "must-not-delete" {
		t.Fatalf("root prune mutated managed SSH state: data=%q err=%v", data, readErr)
	}
}
