package mediaassets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type DiskStorage struct {
	root string
}

func DefaultStorageRoot() string {
	if configured := strings.TrimSpace(os.Getenv("AUTOSTREAM_MEDIA_ASSET_DIR")); configured != "" {
		return configured
	}
	if runtime.GOOS == "windows" {
		if base := strings.TrimSpace(os.Getenv("ProgramData")); base != "" {
			return filepath.Join(base, "AutoStream", "media-assets")
		}
	}
	return "/var/lib/autostream/control-panel/media-assets"
}

func NewDiskStorage(root string) (*DiskStorage, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return nil, errors.New("media asset storage root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create media asset storage: %w", err)
	}
	return &DiskStorage{root: root}, nil
}

func (s *DiskStorage) Root() string { return s.root }

func (s *DiskStorage) WriteContentAddressed(r io.Reader, suffix string) (storageKey, digest string, size int64, err error) {
	if s == nil {
		return "", "", 0, errors.New("media asset storage is unavailable")
	}
	tmpDir := filepath.Join(s.root, ".incoming")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return "", "", 0, err
	}
	tmp, err := os.CreateTemp(tmpDir, "asset-*")
	if err != nil {
		return "", "", 0, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return "", "", 0, err
	}
	if err := tmp.Sync(); err != nil {
		return "", "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", "", 0, err
	}
	digest = hex.EncodeToString(h.Sum(nil))
	suffix = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
	if suffix == "" {
		suffix = "bin"
	}
	storageKey = filepath.ToSlash(filepath.Join(digest[:2], digest[2:4], digest+"."+suffix))
	target, err := s.resolve(storageKey)
	if err != nil {
		return "", "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return "", "", 0, err
	}
	if _, err := os.Stat(target); err == nil {
		return storageKey, digest, size, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", 0, err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			return storageKey, digest, size, nil
		}
		return "", "", 0, err
	}
	return storageKey, digest, size, nil
}

func (s *DiskStorage) Open(storageKey string) (*os.File, error) {
	path, err := s.resolve(storageKey)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *DiskStorage) Remove(storageKey string) error {
	path, err := s.resolve(storageKey)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *DiskStorage) resolve(storageKey string) (string, error) {
	storageKey = filepath.FromSlash(strings.TrimSpace(storageKey))
	if storageKey == "" || filepath.IsAbs(storageKey) {
		return "", ErrIntegrity
	}
	target := filepath.Clean(filepath.Join(s.root, storageKey))
	rel, err := filepath.Rel(s.root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrIntegrity
	}
	return target, nil
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(raw[0:4]),
		hex.EncodeToString(raw[4:6]),
		hex.EncodeToString(raw[6:8]),
		hex.EncodeToString(raw[8:10]),
		hex.EncodeToString(raw[10:16])), nil
}
