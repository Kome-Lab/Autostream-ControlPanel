package updateagent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	managedSSHPrivateKeyName = "id_ed25519"
	managedSSHPrivateMaxSize = 1 << 20
)

var managedSSHHostIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ManagedSSHIdentityPrivatePath returns the private-key pathname for a managed
// host without reading or exposing any key material.
func ManagedSSHIdentityPrivatePath(stateDir, hostID string) (string, error) {
	if stateDir == "" || stateDir != strings.TrimSpace(stateDir) {
		return "", errors.New("managed SSH state directory is invalid")
	}
	cleanStateDir := filepath.Clean(stateDir)
	if !filepath.IsAbs(cleanStateDir) || filepath.Dir(cleanStateDir) == cleanStateDir {
		return "", errors.New("managed SSH state directory must be a non-root absolute path")
	}
	if hostID != strings.TrimSpace(hostID) || !managedSSHHostIDPattern.MatchString(hostID) {
		return "", errors.New("managed SSH host_id is invalid")
	}
	privatePath := filepath.Join(cleanStateDir, "ssh", hostID, managedSSHPrivateKeyName)
	relative, err := filepath.Rel(cleanStateDir, privatePath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("managed SSH identity path escapes state directory")
	}
	return privatePath, nil
}

// EnsureManagedSSHIdentity creates one daemon-owned Ed25519 client identity per
// managed host. Existing valid key material is reused byte-for-byte.
func EnsureManagedSSHIdentity(stateDir, hostID string) (privatePath, authorizedPublicKey, fingerprint string, err error) {
	privatePath, err = ManagedSSHIdentityPrivatePath(stateDir, hostID)
	if err != nil {
		return "", "", "", err
	}
	if !managedSSHIdentityCallerAllowed() {
		return "", "", "", errors.New("managed SSH identities must be initialized by the non-root updater service user")
	}
	cleanStateDir := filepath.Clean(stateDir)
	if err := validateManagedDirectoryChain(cleanStateDir); err != nil {
		return "", "", "", err
	}
	sshDir := filepath.Join(cleanStateDir, "ssh")
	if err := ensureManagedSSHDirectory(sshDir); err != nil {
		return "", "", "", err
	}
	hostDir := filepath.Dir(privatePath)
	if err := ensureManagedSSHDirectory(hostDir); err != nil {
		return "", "", "", err
	}

	publicPath := privatePath + ".pub"
	privateExists, err := managedSSHPathExists(privatePath)
	if err != nil {
		return "", "", "", fmt.Errorf("inspect managed SSH private key: %w", err)
	}
	publicExists, err := managedSSHPathExists(publicPath)
	if err != nil {
		return "", "", "", fmt.Errorf("inspect managed SSH public key: %w", err)
	}
	if !privateExists && publicExists {
		return "", "", "", errors.New("managed SSH public key exists without its private key")
	}
	if !privateExists {
		privateBytes, publicBytes, generationErr := generateManagedSSHIdentity()
		if generationErr != nil {
			return "", "", "", generationErr
		}
		installed, installErr := publishManagedSSHFile(privatePath, privateBytes, 0o600)
		if installErr != nil {
			return "", "", "", fmt.Errorf("install managed SSH private key: %w", installErr)
		}
		if installed {
			if _, installErr := publishManagedSSHFile(publicPath, publicBytes, 0o644); installErr != nil {
				return "", "", "", fmt.Errorf("install managed SSH public key: %w", installErr)
			}
		}
	}

	signer, err := loadManagedSSHSigner(privatePath)
	if err != nil {
		return "", "", "", fmt.Errorf("managed SSH private key is invalid: %w", err)
	}
	expectedPublicLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	publicExists, err = managedSSHPathExists(publicPath)
	if err != nil {
		return "", "", "", fmt.Errorf("inspect managed SSH public key: %w", err)
	}
	if !publicExists {
		if _, err := publishManagedSSHFile(publicPath, []byte(expectedPublicLine+"\n"), 0o644); err != nil {
			return "", "", "", fmt.Errorf("repair managed SSH public key: %w", err)
		}
	}
	storedPublicKey, err := loadManagedSSHPublicKey(publicPath)
	if err != nil {
		return "", "", "", fmt.Errorf("managed SSH public key is invalid: %w", err)
	}
	if !bytes.Equal(storedPublicKey.Marshal(), signer.PublicKey().Marshal()) {
		return "", "", "", errors.New("managed SSH private and public keys do not match")
	}
	if err := validateManagedDirectoryChain(cleanStateDir); err != nil {
		return "", "", "", err
	}
	return privatePath, expectedPublicLine, ssh.FingerprintSHA256(signer.PublicKey()), nil
}

type managedSSHPruneFile struct {
	path string
	info os.FileInfo
}

type managedSSHPruneDirectory struct {
	path  string
	info  os.FileInfo
	files []managedSSHPruneFile
}

// PruneManagedSSHIdentities removes daemon-owned identities that are no longer
// referenced by the durably committed managed policy. Callers must not invoke
// this before the policy snapshot is committed.
func PruneManagedSSHIdentities(stateDir string, keepHostIDs []string) error {
	if _, err := ManagedSSHIdentityPrivatePath(stateDir, "path-validation"); err != nil {
		return err
	}
	if !managedSSHIdentityCallerAllowed() {
		return errors.New("managed SSH identities must be pruned by the non-root updater service user")
	}
	keep := make(map[string]struct{}, len(keepHostIDs))
	for _, hostID := range keepHostIDs {
		if hostID != strings.TrimSpace(hostID) || !managedSSHHostIDPattern.MatchString(hostID) {
			return errors.New("managed SSH keep host_id is invalid")
		}
		if _, exists := keep[hostID]; exists {
			return errors.New("managed SSH keep host_id is duplicated")
		}
		keep[hostID] = struct{}{}
	}

	cleanStateDir := filepath.Clean(stateDir)
	if err := validateManagedDirectoryChain(cleanStateDir); err != nil {
		return err
	}
	sshDir := filepath.Join(cleanStateDir, "ssh")
	sshInfo, err := os.Lstat(sshDir)
	if errors.Is(err, fs.ErrNotExist) {
		if len(keep) == 0 {
			return nil
		}
		return errors.New("managed SSH identity directory is unavailable")
	}
	if err != nil {
		return errors.New("inspect managed SSH identity directory")
	}
	if err := validateManagedSSHDirectoryInfo(sshInfo); err != nil {
		return err
	}
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return errors.New("read managed SSH identity directory")
	}

	// Preflight every entry before deleting any key. A malformed or symlinked
	// sibling must fail the whole pass without causing partial cleanup.
	prune := make([]managedSSHPruneDirectory, 0, len(entries))
	for _, entry := range entries {
		hostID := entry.Name()
		if !managedSSHHostIDPattern.MatchString(hostID) {
			return errors.New("managed SSH identity directory contains an invalid host entry")
		}
		hostDir := filepath.Join(sshDir, hostID)
		hostInfo, err := os.Lstat(hostDir)
		if err != nil {
			return errors.New("inspect managed SSH host directory")
		}
		if err := validateManagedSSHDirectoryInfo(hostInfo); err != nil {
			return err
		}
		if _, retained := keep[hostID]; retained {
			continue
		}
		files, err := inspectManagedSSHPruneFiles(hostDir)
		if err != nil {
			return err
		}
		prune = append(prune, managedSSHPruneDirectory{path: hostDir, info: hostInfo, files: files})
	}

	for _, directory := range prune {
		if err := validateUnchangedManagedSSHDirectory(sshDir, sshInfo); err != nil {
			return err
		}
		if err := validateUnchangedManagedSSHDirectory(directory.path, directory.info); err != nil {
			return err
		}
		for _, file := range directory.files {
			if err := validateUnchangedManagedSSHDirectory(sshDir, sshInfo); err != nil {
				return err
			}
			if err := validateUnchangedManagedSSHDirectory(directory.path, directory.info); err != nil {
				return err
			}
			current, err := os.Lstat(file.path)
			if err != nil || !os.SameFile(file.info, current) || file.info.Mode() != current.Mode() ||
				file.info.Size() != current.Size() || !file.info.ModTime().Equal(current.ModTime()) {
				return errors.New("managed SSH identity changed during prune")
			}
			if err := os.Remove(file.path); err != nil {
				return errors.New("remove managed SSH identity file")
			}
		}
		if err := syncDirectory(directory.path); err != nil {
			return errors.New("sync pruned managed SSH host directory")
		}
		if err := validateUnchangedManagedSSHDirectory(sshDir, sshInfo); err != nil {
			return err
		}
		if err := validateUnchangedManagedSSHDirectory(directory.path, directory.info); err != nil {
			return err
		}
		if err := os.Remove(directory.path); err != nil {
			return errors.New("remove managed SSH host directory")
		}
		if err := syncDirectory(sshDir); err != nil {
			return errors.New("sync managed SSH identity directory")
		}
	}
	// A prior pass may have removed an orphan and failed only while syncing the
	// parent. Sync even when this retry finds no remaining orphan directories.
	if err := validateUnchangedManagedSSHDirectory(sshDir, sshInfo); err != nil {
		return err
	}
	if err := syncDirectory(sshDir); err != nil {
		return errors.New("sync managed SSH identity directory")
	}
	return nil
}

func pruneManagedSSHIdentitiesForConfig(stateDir string, cfg Config) error {
	keepHostIDs := make([]string, 0, len(cfg.Hosts))
	for _, host := range cfg.Hosts {
		keepHostIDs = append(keepHostIDs, host.HostID)
	}
	return PruneManagedSSHIdentities(stateDir, keepHostIDs)
}

func inspectManagedSSHPruneFiles(hostDir string) ([]managedSSHPruneFile, error) {
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		return nil, errors.New("read managed SSH host directory")
	}
	files := make([]managedSSHPruneFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		private := name == managedSSHPrivateKeyName || strings.HasPrefix(name, "."+managedSSHPrivateKeyName+".tmp-")
		public := name == managedSSHPrivateKeyName+".pub" || strings.HasPrefix(name, "."+managedSSHPrivateKeyName+".pub.tmp-")
		if !private && !public {
			return nil, errors.New("managed SSH host directory contains an unexpected entry")
		}
		path := filepath.Join(hostDir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !managedSSHOwnedByCurrentUser(info) {
			return nil, errors.New("managed SSH identity entry must be a daemon-owned regular non-symlink file")
		}
		limit := int64(managedSSHPrivateMaxSize)
		if public {
			limit = maxHostPublicKeyBytes
		}
		if info.Size() < 0 || info.Size() > limit {
			return nil, errors.New("managed SSH identity entry is too large")
		}
		if runtime.GOOS != "windows" {
			if private && info.Mode().Perm()&0o077 != 0 {
				return nil, errors.New("managed SSH private identity must not be accessible by group or other users")
			}
			if public && info.Mode().Perm()&0o022 != 0 {
				return nil, errors.New("managed SSH public identity must not be writable by group or other users")
			}
		}
		files = append(files, managedSSHPruneFile{path: path, info: info})
	}
	return files, nil
}

func validateManagedSSHDirectoryInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed SSH identity path must contain only real directories")
	}
	if !managedSSHOwnedByCurrentUser(info) {
		return errors.New("managed SSH identity directory must be owned by the updater service user")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return errors.New("managed SSH identity directory must not be writable by group or other users")
	}
	return nil
}

func validateUnchangedManagedSSHDirectory(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(expected, current) || expected.Mode() != current.Mode() {
		return errors.New("managed SSH identity directory changed during prune")
	}
	return validateManagedSSHDirectoryInfo(current)
}

func generateManagedSSHIdentity() (privateBytes, publicBytes []byte, err error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, errors.New("generate managed SSH Ed25519 key")
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, nil, errors.New("prepare managed SSH Ed25519 key")
	}
	privateBlock, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return nil, nil, errors.New("marshal managed SSH private key")
	}
	privateBytes = pem.EncodeToMemory(privateBlock)
	if len(privateBytes) == 0 {
		return nil, nil, errors.New("encode managed SSH private key")
	}
	return privateBytes, ssh.MarshalAuthorizedKey(signer.PublicKey()), nil
}

func validateManagedDirectoryChain(stateDir string) error {
	for directory, first := stateDir, true; ; directory, first = filepath.Dir(directory), false {
		info, err := os.Lstat(directory)
		if err != nil {
			return errors.New("managed SSH state directory is unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("managed SSH directory path must contain only real directories")
		}
		if first && !managedSSHOwnedByCurrentUser(info) {
			return errors.New("managed SSH state directory must be owned by the updater service user")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
			if first || info.Mode()&os.ModeSticky == 0 {
				return errors.New("managed SSH directory path is writable by group or other users")
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
	}
}

func ensureManagedSSHDirectory(path string) error {
	parent := filepath.Dir(path)
	if err := validateManagedSSHDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return errors.New("create managed SSH directory")
	}
	if err := validateManagedSSHDirectory(path); err != nil {
		return err
	}
	if err := syncDirectory(parent); err != nil {
		return errors.New("sync managed SSH parent directory")
	}
	return nil
}

func validateManagedSSHDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("managed SSH parent directory is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed SSH parent must be a real directory")
	}
	if !managedSSHOwnedByCurrentUser(info) {
		return errors.New("managed SSH parent must be owned by the updater service user")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return errors.New("managed SSH parent must not be writable by group or other users")
	}
	return nil
}

func managedSSHPathExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("managed SSH key path must be a regular non-symlink file")
	}
	return true, nil
}

func publishManagedSSHFile(path string, data []byte, mode os.FileMode) (bool, error) {
	parent := filepath.Dir(path)
	if err := validateManagedSSHDirectory(parent); err != nil {
		return false, err
	}
	temp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return false, err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return false, err
	}
	if _, err := temp.Write(data); err != nil {
		return false, err
	}
	if err := temp.Sync(); err != nil {
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	if err := validateManagedSSHDirectory(parent); err != nil {
		return false, err
	}
	if err := os.Link(tempPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(tempPath); err != nil {
		return false, err
	}
	removeTemp = false
	if err := syncDirectory(parent); err != nil {
		return false, err
	}
	return true, nil
}

func loadManagedSSHSigner(path string) (ssh.Signer, error) {
	data, err := readManagedSSHKeyFile(path, true, managedSSHPrivateMaxSize)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil || signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("must be an Ed25519 private key")
	}
	return signer, nil
}

func loadManagedSSHPublicKey(path string) (ssh.PublicKey, error) {
	data, err := readManagedSSHKeyFile(path, false, maxHostPublicKeyBytes)
	if err != nil {
		return nil, err
	}
	if !bytes.HasSuffix(data, []byte("\n")) || bytes.Count(data, []byte("\n")) != 1 {
		return nil, errors.New("must contain one canonical authorized-key line")
	}
	line := strings.TrimSuffix(string(data), "\n")
	return parsePinnedHostPublicKey(line)
}

func readManagedSSHKeyFile(path string, private bool, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Dir(filepath.Clean(path)) == filepath.Clean(path) {
		return nil, errors.New("managed SSH key path must be a non-root absolute path")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("managed SSH key file is unavailable")
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Size() <= 0 || pathInfo.Size() > limit {
		return nil, errors.New("managed SSH key must be a bounded regular non-symlink file")
	}
	if !managedSSHOwnedByCurrentUser(pathInfo) {
		return nil, errors.New("managed SSH key must be owned by the updater service user")
	}
	if runtime.GOOS != "windows" {
		if private && pathInfo.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("managed SSH private key must not be accessible by group or other users")
		}
		if !private && pathInfo.Mode().Perm()&0o022 != 0 {
			return nil, errors.New("managed SSH public key must not be writable by group or other users")
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open managed SSH key")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(pathInfo, openedInfo) || pathInfo.Mode() != openedInfo.Mode() || pathInfo.Size() != openedInfo.Size() || !pathInfo.ModTime().Equal(openedInfo.ModTime()) {
		return nil, errors.New("managed SSH key changed during secure open")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, errors.New("read managed SSH key")
	}
	return data, nil
}
