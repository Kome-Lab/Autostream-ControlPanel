package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

type RuntimeSecretLease struct {
	ID               string
	ServiceID        string
	TokenID          string
	StreamID         string
	ArchiveProfileID string
	SecretName       string
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

type RuntimeSecretLeaseStore interface {
	ClaimRuntimeSecretLease(ctx context.Context, lease RuntimeSecretLease, ttl time.Duration) (RuntimeSecretLease, error)
	ReleaseRuntimeSecretLease(ctx context.Context, lease RuntimeSecretLease) error
}

var (
	ErrRuntimeSecretLeaseActive  = errors.New("runtime secret lease active")
	ErrInvalidRuntimeSecretLease = errors.New("invalid runtime secret lease")
)

type MemoryRuntimeSecretLeaseStore struct {
	mu     sync.Mutex
	leases map[string]RuntimeSecretLease
}

func NewMemoryRuntimeSecretLeaseStore() *MemoryRuntimeSecretLeaseStore {
	return &MemoryRuntimeSecretLeaseStore{leases: map[string]RuntimeSecretLease{}}
}

func (s *MemoryRuntimeSecretLeaseStore) ClaimRuntimeSecretLease(ctx context.Context, lease RuntimeSecretLease, ttl time.Duration) (RuntimeSecretLease, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeSecretLease{}, err
	}
	lease = normalizeRuntimeSecretLease(lease)
	if lease.ServiceID == "" || lease.TokenID == "" || lease.SecretName == "" {
		return RuntimeSecretLease{}, ErrInvalidRuntimeSecretLease
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	now := time.Now().UTC()
	lease.ID = newUUID()
	lease.CreatedAt = now
	lease.ExpiresAt = now.Add(ttl)
	key := runtimeSecretLeaseKey(lease)
	s.mu.Lock()
	defer s.mu.Unlock()
	for existingKey, existing := range s.leases {
		if !now.Before(existing.ExpiresAt) {
			delete(s.leases, existingKey)
		}
	}
	if existing, ok := s.leases[key]; ok && now.Before(existing.ExpiresAt) {
		return RuntimeSecretLease{}, ErrRuntimeSecretLeaseActive
	}
	s.leases[key] = lease
	return lease, nil
}

func (s *MemoryRuntimeSecretLeaseStore) ReleaseRuntimeSecretLease(ctx context.Context, lease RuntimeSecretLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lease = normalizeRuntimeSecretLease(lease)
	if lease.ID == "" || lease.ServiceID == "" || lease.TokenID == "" || lease.SecretName == "" {
		return ErrInvalidRuntimeSecretLease
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := runtimeSecretLeaseKey(lease)
	if existing, ok := s.leases[key]; ok && existing.ID == lease.ID && existing.TokenID == lease.TokenID {
		delete(s.leases, key)
	}
	return nil
}

type MariaDBRuntimeSecretLeaseStore struct {
	db *sql.DB
}

func NewMariaDBRuntimeSecretLeaseStore(db *sql.DB) MariaDBRuntimeSecretLeaseStore {
	return MariaDBRuntimeSecretLeaseStore{db: db}
}

func (s MariaDBRuntimeSecretLeaseStore) ClaimRuntimeSecretLease(ctx context.Context, lease RuntimeSecretLease, ttl time.Duration) (RuntimeSecretLease, error) {
	lease = normalizeRuntimeSecretLease(lease)
	if lease.ServiceID == "" || lease.TokenID == "" || lease.SecretName == "" {
		return RuntimeSecretLease{}, ErrInvalidRuntimeSecretLease
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	now := time.Now().UTC()
	lease.ID = newUUID()
	lease.CreatedAt = now
	lease.ExpiresAt = now.Add(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuntimeSecretLease{}, err
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(ctx, `DELETE FROM runtime_secret_leases WHERE expires_at <= ?`, now)
	var existingExpiresAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT expires_at FROM runtime_secret_leases WHERE service_id = ? AND stream_id = ? AND archive_profile_id = ? AND secret_name = ? FOR UPDATE`, lease.ServiceID, lease.StreamID, lease.ArchiveProfileID, lease.SecretName).Scan(&existingExpiresAt)
	if err == nil && now.Before(existingExpiresAt) {
		return RuntimeSecretLease{}, ErrRuntimeSecretLeaseActive
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RuntimeSecretLease{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_secret_leases (id, service_id, token_id, stream_id, archive_profile_id, secret_name, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, lease.ID, lease.ServiceID, lease.TokenID, lease.StreamID, lease.ArchiveProfileID, lease.SecretName, lease.CreatedAt, lease.ExpiresAt)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE runtime_secret_leases SET id = ?, token_id = ?, created_at = ?, expires_at = ? WHERE service_id = ? AND stream_id = ? AND archive_profile_id = ? AND secret_name = ?`, lease.ID, lease.TokenID, lease.CreatedAt, lease.ExpiresAt, lease.ServiceID, lease.StreamID, lease.ArchiveProfileID, lease.SecretName)
	}
	if err != nil {
		return RuntimeSecretLease{}, err
	}
	if err := tx.Commit(); err != nil {
		return RuntimeSecretLease{}, err
	}
	return lease, nil
}

func (s MariaDBRuntimeSecretLeaseStore) ReleaseRuntimeSecretLease(ctx context.Context, lease RuntimeSecretLease) error {
	lease = normalizeRuntimeSecretLease(lease)
	if lease.ID == "" || lease.ServiceID == "" || lease.TokenID == "" || lease.SecretName == "" {
		return ErrInvalidRuntimeSecretLease
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM runtime_secret_leases WHERE id = ? AND token_id = ? AND service_id = ? AND stream_id = ? AND archive_profile_id = ? AND secret_name = ?`, lease.ID, lease.TokenID, lease.ServiceID, lease.StreamID, lease.ArchiveProfileID, lease.SecretName)
	return err
}

func normalizeRuntimeSecretLease(lease RuntimeSecretLease) RuntimeSecretLease {
	lease.ServiceID = strings.TrimSpace(lease.ServiceID)
	lease.TokenID = strings.TrimSpace(lease.TokenID)
	lease.StreamID = strings.TrimSpace(lease.StreamID)
	lease.ArchiveProfileID = strings.TrimSpace(lease.ArchiveProfileID)
	lease.SecretName = strings.TrimSpace(lease.SecretName)
	return lease
}

func runtimeSecretLeaseKey(lease RuntimeSecretLease) string {
	return lease.ServiceID + "\x00" + lease.StreamID + "\x00" + lease.ArchiveProfileID + "\x00" + lease.SecretName
}
