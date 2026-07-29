package updateagent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func requireManagedSSHIdentityCaller(t *testing.T) {
	t.Helper()
	if !managedSSHIdentityCallerAllowed() {
		t.Skip("managed SSH identities intentionally require the non-root updater service user")
	}
}

func TestManagedSSHIdentityPrivatePathIsDeterministicAndBounded(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	got, err := ManagedSSHIdentityPrivatePath(stateDir, "edge-01")
	if err != nil {
		t.Fatalf("managed identity path: %v", err)
	}
	want := filepath.Join(stateDir, "ssh", "edge-01", "id_ed25519")
	if got != want {
		t.Fatalf("managed identity path = %q, want %q", got, want)
	}
	for _, hostID := range []string{"", "../edge", "edge/01", "edge\\01", " edge-01", "edge:01"} {
		if _, err := ManagedSSHIdentityPrivatePath(stateDir, hostID); err == nil {
			t.Fatalf("unsafe host id %q was accepted", hostID)
		}
	}
	for _, unsafeStateDir := range []string{"relative", string(filepath.Separator)} {
		if _, err := ManagedSSHIdentityPrivatePath(unsafeStateDir, "edge-01"); err == nil {
			t.Fatalf("unsafe state dir %q was accepted", unsafeStateDir)
		}
	}
}

func TestEnsureManagedSSHIdentityCreatesAndReusesEd25519Identity(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	privatePath, authorizedPublicKey, fingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
	if err != nil {
		t.Fatalf("ensure managed identity: %v", err)
	}
	if strings.ContainsAny(authorizedPublicKey, "\r\n") || !strings.HasPrefix(authorizedPublicKey, ssh.KeyAlgoED25519+" ") {
		t.Fatalf("authorized public key = %q", authorizedPublicKey)
	}
	publicKey, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(authorizedPublicKey))
	if err != nil || len(rest) != 0 {
		t.Fatalf("parse authorized public key: key=%v rest=%q err=%v", publicKey, rest, err)
	}
	if fingerprint != ssh.FingerprintSHA256(publicKey) {
		t.Fatalf("fingerprint = %q, want %q", fingerprint, ssh.FingerprintSHA256(publicKey))
	}
	privateBefore, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(privateBefore)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 || !bytes.Equal(signer.PublicKey().Marshal(), publicKey.Marshal()) {
		t.Fatal("managed private and public keys do not match")
	}
	publicPath := privatePath + ".pub"
	publicFile, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(publicFile) != authorizedPublicKey+"\n" {
		t.Fatalf("public key file = %q", publicFile)
	}
	if runtime.GOOS != "windows" {
		privateInfo, _ := os.Stat(privatePath)
		publicInfo, _ := os.Stat(publicPath)
		if privateInfo.Mode().Perm() != 0o600 || (publicInfo.Mode().Perm() != 0o600 && publicInfo.Mode().Perm() != 0o644) {
			t.Fatalf("managed identity modes = private %o public %o", privateInfo.Mode().Perm(), publicInfo.Mode().Perm())
		}
	}

	reusedPath, reusedPublicKey, reusedFingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
	if err != nil {
		t.Fatalf("reuse managed identity: %v", err)
	}
	privateAfter, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if reusedPath != privatePath || reusedPublicKey != authorizedPublicKey || reusedFingerprint != fingerprint || !bytes.Equal(privateBefore, privateAfter) {
		t.Fatal("valid managed identity was replaced or changed")
	}
}

func TestEnsureManagedSSHIdentityRepairsOnlyMissingPublicHalf(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	privatePath, publicKey, fingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
	if err != nil {
		t.Fatal(err)
	}
	privateBefore, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(privatePath + ".pub"); err != nil {
		t.Fatal(err)
	}
	reusedPath, repairedPublicKey, repairedFingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
	if err != nil {
		t.Fatalf("repair missing public key: %v", err)
	}
	privateAfter, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if reusedPath != privatePath || repairedPublicKey != publicKey || repairedFingerprint != fingerprint || !bytes.Equal(privateBefore, privateAfter) {
		t.Fatal("repairing the public key replaced the private identity")
	}
}

func TestEnsureManagedSSHIdentityConcurrentFirstUseKeepsOneIdentity(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	type result struct {
		privatePath string
		publicKey   string
		fingerprint string
		err         error
	}
	const callers = 8
	start := make(chan struct{})
	results := make(chan result, callers)
	var workers sync.WaitGroup
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			privatePath, publicKey, fingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
			results <- result{privatePath: privatePath, publicKey: publicKey, fingerprint: fingerprint, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var first result
	for current := range results {
		if current.err != nil {
			t.Fatalf("concurrent ensure: %v", current.err)
		}
		if first.privatePath == "" {
			first = current
			continue
		}
		if current.privatePath != first.privatePath || current.publicKey != first.publicKey || current.fingerprint != first.fingerprint {
			t.Fatalf("concurrent callers observed different identities: first=%#v current=%#v", first, current)
		}
	}
}

func TestEnsureManagedSSHIdentityHandlesConcurrentPublisherBetweenExistenceChecks(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	privatePath, err := ManagedSSHIdentityPrivatePath(stateDir, "edge-01")
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		privatePath string
		publicKey   string
		fingerprint string
		err         error
	}
	privateObservedMissing := make(chan struct{})
	publisherFinished := make(chan struct{})
	ensureFinished := make(chan result, 1)
	go func() {
		firstPrivateCheck := true
		privatePath, publicKey, fingerprint, err := ensureManagedSSHIdentity(
			stateDir,
			"edge-01",
			func(path string) (bool, error) {
				exists, err := managedSSHPathExists(path)
				if path == privatePath && firstPrivateCheck {
					firstPrivateCheck = false
					if err == nil && !exists {
						close(privateObservedMissing)
						<-publisherFinished
					}
				}
				return exists, err
			},
		)
		ensureFinished <- result{
			privatePath: privatePath,
			publicKey:   publicKey,
			fingerprint: fingerprint,
			err:         err,
		}
	}()

	select {
	case <-privateObservedMissing:
	case <-time.After(5 * time.Second):
		t.Fatal("first ensure did not observe the missing private key")
	}
	winnerPath, winnerPublicKey, winnerFingerprint, winnerErr := EnsureManagedSSHIdentity(stateDir, "edge-01")
	close(publisherFinished)
	current := <-ensureFinished
	if winnerErr != nil {
		t.Fatalf("concurrent publisher: %v", winnerErr)
	}
	if current.err != nil {
		t.Fatalf("ensure after concurrent publisher: %v", current.err)
	}
	if current.privatePath != winnerPath || current.publicKey != winnerPublicKey || current.fingerprint != winnerFingerprint {
		t.Fatalf("ensure observed a different identity: winner=%q/%q/%q current=%q/%q/%q",
			winnerPath, winnerPublicKey, winnerFingerprint,
			current.privatePath, current.publicKey, current.fingerprint)
	}
}

func TestEnsureManagedSSHIdentityRejectsUnsafeOrMismatchedExistingMaterial(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	tests := []struct {
		name   string
		mutate func(t *testing.T, stateDir, privatePath string)
	}{
		{name: "malformed private", mutate: func(t *testing.T, _ string, privatePath string) {
			t.Helper()
			if err := os.WriteFile(privatePath, []byte("not-a-private-key"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "mismatched public", mutate: func(t *testing.T, _ string, privatePath string) {
			t.Helper()
			_, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			otherSigner, err := ssh.NewSignerFromKey(otherPrivate)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(privatePath+".pub", ssh.MarshalAuthorizedKey(otherSigner.PublicKey()), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "malformed public", mutate: func(t *testing.T, _ string, privatePath string) {
			t.Helper()
			if err := os.WriteFile(privatePath+".pub", []byte("ssh-ed25519 invalid\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			privatePath, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
			if err != nil {
				t.Fatal(err)
			}
			privateBefore, err := os.ReadFile(privatePath)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, stateDir, privatePath)
			if _, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01"); err == nil {
				t.Fatal("unsafe existing identity was accepted")
			}
			if tc.name != "malformed private" {
				privateAfter, err := os.ReadFile(privatePath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(privateBefore, privateAfter) {
					t.Fatal("private identity changed after rejecting unsafe material")
				}
			}
		})
	}
}

func TestEnsureManagedSSHIdentityRejectsPublicKeyWithoutPrivateKey(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	hostDir := filepath.Join(stateDir, "ssh", "edge-01")
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "id_ed25519.pub"), ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01"); err == nil {
		t.Fatal("public-key-only identity was accepted")
	}
}

func TestEnsureManagedSSHIdentityRejectsSymlinkedManagedPath(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available to unprivileged Windows tests")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(stateDir, "ssh")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01"); err == nil {
		t.Fatal("symlinked managed SSH directory was accepted")
	}
}

func TestEnsureManagedSSHIdentityRejectsSymlinkedKeyFile(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available to unprivileged Windows tests")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	privatePath, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-key")
	if err := os.WriteFile(outside, []byte("not-a-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(privatePath + ".pub"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, privatePath+".pub"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01"); err == nil {
		t.Fatal("symlinked managed SSH public key was accepted")
	}
}

func TestEnsureManagedSSHIdentityRejectsUnsafeParentPermissions(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics are unavailable on Windows")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01"); err == nil {
		t.Fatal("world-writable managed state directory was accepted")
	}
}

func TestEnsureManagedSSHIdentityRejectsUnsafeKeyPermissions(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics are unavailable on Windows")
	}
	tests := []struct {
		name   string
		mutate func(t *testing.T, privatePath string)
	}{
		{name: "world readable private key", mutate: func(t *testing.T, privatePath string) {
			t.Helper()
			if err := os.Chmod(privatePath, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "world writable public key", mutate: func(t *testing.T, privatePath string) {
			t.Helper()
			if err := os.Chmod(privatePath+".pub", 0o666); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "world writable host directory", mutate: func(t *testing.T, privatePath string) {
			t.Helper()
			if err := os.Chmod(filepath.Dir(privatePath), 0o777); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			privatePath, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01")
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, privatePath)
			if _, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-01"); err == nil {
				t.Fatal("unsafe managed SSH permissions were accepted")
			}
		})
	}
}

func TestPruneManagedSSHIdentitiesDeletesOnlyRemovedHostsAndRotatesReaddedIdentity(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keepPath, _, keepFingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-keep")
	if err != nil {
		t.Fatal(err)
	}
	removedPath, _, removedFingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-remove")
	if err != nil {
		t.Fatal(err)
	}

	if err := PruneManagedSSHIdentities(stateDir, []string{"edge-keep"}); err != nil {
		t.Fatalf("prune managed identities: %v", err)
	}
	if _, err := os.Stat(removedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed host private key still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(removedPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed host identity directory still exists: %v", err)
	}
	reusedPath, _, reusedFingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-keep")
	if err != nil {
		t.Fatal(err)
	}
	if reusedPath != keepPath || reusedFingerprint != keepFingerprint {
		t.Fatal("kept host identity changed during prune")
	}

	readdedPath, _, readdedFingerprint, err := EnsureManagedSSHIdentity(stateDir, "edge-remove")
	if err != nil {
		t.Fatal(err)
	}
	if readdedPath != removedPath {
		t.Fatalf("readded host identity path = %q, want %q", readdedPath, removedPath)
	}
	if readdedFingerprint == removedFingerprint {
		t.Fatal("readded host reused its deleted SSH identity")
	}
}

func TestPruneManagedSSHIdentitiesRejectsUnsafeDirectoryBeforeDeletingAnything(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available to unprivileged Windows tests")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	removedPath, _, _, err := EnsureManagedSSHIdentity(stateDir, "aaa-remove")
	if err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideKey := filepath.Join(outsideDir, managedSSHPrivateKeyName)
	if err := os.WriteFile(outsideKey, []byte("outside-must-survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(stateDir, "ssh", "zzz-symlink")); err != nil {
		t.Fatal(err)
	}

	if err := PruneManagedSSHIdentities(stateDir, nil); err == nil {
		t.Fatal("symlinked orphan identity directory was accepted")
	}
	if _, err := os.Stat(removedPath); err != nil {
		t.Fatalf("safe identity was partially deleted before unsafe entry rejection: %v", err)
	}
	data, err := os.ReadFile(outsideKey)
	if err != nil {
		t.Fatalf("outside key was removed through symlink: %v", err)
	}
	if string(data) != "outside-must-survive" {
		t.Fatalf("outside key changed through symlink: %q", data)
	}
}

func TestPruneManagedSSHIdentitiesRejectsUnsafePermissions(t *testing.T) {
	requireManagedSSHIdentityCaller(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics are unavailable on Windows")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	privatePath, _, _, err := EnsureManagedSSHIdentity(stateDir, "edge-remove")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(privatePath), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := PruneManagedSSHIdentities(stateDir, nil); err == nil {
		t.Fatal("unsafe orphan identity permissions were accepted")
	}
	if _, err := os.Stat(privatePath); err != nil {
		t.Fatalf("unsafe identity was mutated while being rejected: %v", err)
	}
}
