package updateagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	managedPolicySnapshotName     = "applied-policy.json"
	managedPolicySnapshotMaxBytes = 1 << 20
	managedPolicySnapshotVersion  = 1
)

type ManagedPolicySnapshotStore interface {
	Load() (*ManagedPolicy, bool, error)
	Save(ManagedPolicy) error
}

type managedPolicySnapshotSaveError struct {
	err       error
	committed bool
}

func (e *managedPolicySnapshotSaveError) Error() string {
	return e.err.Error()
}

func (e *managedPolicySnapshotSaveError) Unwrap() error {
	return e.err
}

// ManagedPolicySnapshotWasCommitted distinguishes a failed pre-rename save
// from a post-rename durability warning. Callers must never continue running
// the old policy after the destination pathname has become the new revision.
func ManagedPolicySnapshotWasCommitted(err error) bool {
	var saveErr *managedPolicySnapshotSaveError
	return errors.As(err, &saveErr) && saveErr.committed
}

type FileManagedPolicySnapshotStore struct {
	StateDir      string
	syncDirectory func(string) error
}

type managedPolicySnapshot struct {
	SchemaVersion int           `json:"schema_version"`
	Policy        ManagedPolicy `json:"policy"`
}

func (s FileManagedPolicySnapshotStore) Load() (*ManagedPolicy, bool, error) {
	path, err := s.path()
	if err != nil {
		return nil, false, err
	}
	if err := validateManagedDirectoryChain(filepath.Dir(path)); err != nil {
		return nil, false, fmt.Errorf("managed policy snapshot parent: %w", err)
	}
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("stat managed policy snapshot")
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() ||
		pathInfo.Size() <= 0 || pathInfo.Size() > managedPolicySnapshotMaxBytes ||
		(snapshotModeEnforced() && pathInfo.Mode().Perm()&0o077 != 0) || !managedSnapshotOwnedByCurrentUser(pathInfo) {
		return nil, false, errors.New("managed policy snapshot must be a private daemon-owned bounded regular non-symlink file")
	}
	file, openedInfo, err := openVerifiedConfig(path, pathInfo)
	if err != nil {
		return nil, false, errors.New("open managed policy snapshot")
	}
	defer file.Close()
	if (snapshotModeEnforced() && openedInfo.Mode().Perm()&0o077 != 0) || !managedSnapshotOwnedByCurrentUser(openedInfo) {
		return nil, false, errors.New("managed policy snapshot ownership changed during secure open")
	}
	data, err := io.ReadAll(io.LimitReader(file, managedPolicySnapshotMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > managedPolicySnapshotMaxBytes {
		return nil, false, errors.New("read managed policy snapshot")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot managedPolicySnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, false, errors.New("decode managed policy snapshot")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, false, errors.New("managed policy snapshot contains trailing data")
	}
	if snapshot.SchemaVersion != managedPolicySnapshotVersion || snapshot.Policy.Revision <= 0 {
		return nil, false, errors.New("managed policy snapshot version or revision is invalid")
	}
	return &snapshot.Policy, true, nil
}

func (s FileManagedPolicySnapshotStore) Save(policy ManagedPolicy) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := validateManagedDirectoryChain(parent); err != nil {
		return fmt.Errorf("managed policy snapshot parent: %w", err)
	}
	existing, err := os.Lstat(path)
	switch {
	case err == nil:
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() ||
			(snapshotModeEnforced() && existing.Mode().Perm()&0o077 != 0) || !managedSnapshotOwnedByCurrentUser(existing) {
			return errors.New("existing managed policy snapshot is unsafe")
		}
	case !errors.Is(err, os.ErrNotExist):
		return errors.New("stat managed policy snapshot destination")
	}
	if policy.Revision <= 0 {
		return errors.New("managed policy snapshot revision is invalid")
	}
	data, err := json.MarshalIndent(managedPolicySnapshot{SchemaVersion: managedPolicySnapshotVersion, Policy: policy}, "", "  ")
	if err != nil {
		return errors.New("encode managed policy snapshot")
	}
	data = append(data, '\n')
	if len(data) > managedPolicySnapshotMaxBytes {
		return errors.New("managed policy snapshot is too large")
	}
	temp, err := os.CreateTemp(parent, ".applied-policy.json.tmp-*")
	if err != nil {
		return errors.New("create managed policy snapshot temporary file")
	}
	tempPath := temp.Name()
	renamed := false
	defer func() {
		_ = temp.Close()
		if !renamed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return errors.New("set managed policy snapshot mode")
	}
	if _, err := temp.Write(data); err != nil {
		return errors.New("write managed policy snapshot")
	}
	if err := temp.Sync(); err != nil {
		return errors.New("sync managed policy snapshot")
	}
	tempInfo, err := temp.Stat()
	if err != nil || !tempInfo.Mode().IsRegular() || (snapshotModeEnforced() && tempInfo.Mode().Perm()&0o077 != 0) || !managedSnapshotOwnedByCurrentUser(tempInfo) {
		return errors.New("managed policy snapshot temporary file is unsafe")
	}
	if err := temp.Close(); err != nil {
		return errors.New("close managed policy snapshot")
	}
	if err := validateManagedDirectoryChain(parent); err != nil {
		return errors.New("managed policy snapshot parent changed before install")
	}
	current, currentErr := os.Lstat(path)
	if existing == nil {
		if currentErr == nil || !errors.Is(currentErr, os.ErrNotExist) {
			return errors.New("managed policy snapshot destination appeared during save")
		}
	} else if currentErr != nil || !os.SameFile(existing, current) {
		return errors.New("managed policy snapshot changed during save")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return errors.New("install managed policy snapshot")
	}
	renamed = true
	syncDir := s.syncDirectory
	if syncDir == nil {
		syncDir = syncDirectory
	}
	if err := syncDir(parent); err != nil {
		return &managedPolicySnapshotSaveError{err: errors.New("sync managed policy snapshot directory"), committed: true}
	}
	return nil
}

func (s FileManagedPolicySnapshotStore) path() (string, error) {
	stateDir := filepath.Clean(s.StateDir)
	if !filepath.IsAbs(stateDir) || filepath.Dir(stateDir) == stateDir {
		return "", errors.New("managed policy snapshot state directory must be a non-root absolute path")
	}
	return filepath.Join(stateDir, managedPolicySnapshotName), nil
}
