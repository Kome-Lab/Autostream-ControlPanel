package v3authorization

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

//go:embed authority_migrations/001_authority.sql
var authorityMigration string

func authorityChecksum() string {
	sum := sha256.Sum256([]byte(authorityMigration))
	return hex.EncodeToString(sum[:])
}

// ApplyAuthorityMigrations is an explicit administrative operation, never called
// by startup, constructors, Resolve, Read or issuance. No v3 registration,
// capability, policy or trust data is seeded. DDL failure preserves dirty=1.
func ApplyAuthorityMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrUnavailable
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer conn.Close()
	var locked int
	if conn.QueryRowContext(ctx, "SELECT GET_LOCK('autostream-v3-cp-authority-migration',0)").Scan(&locked) != nil || locked != 1 {
		return ErrUnavailable
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK('autostream-v3-cp-authority-migration')")
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS v3_cp_authority_schema (singleton TINYINT NOT NULL PRIMARY KEY, version BIGINT UNSIGNED NOT NULL, checksum VARBINARY(64) NOT NULL, dirty BOOLEAN NOT NULL, CHECK(singleton=1)) ENGINE=InnoDB"); err != nil {
		return ErrUnavailable
	}
	var version uint64
	var checksum string
	var dirty bool
	err = conn.QueryRowContext(ctx, "SELECT version,checksum,dirty FROM v3_cp_authority_schema WHERE singleton=1").Scan(&version, &checksum, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = conn.ExecContext(ctx, "INSERT INTO v3_cp_authority_schema VALUES(1,0,'',0)"); err != nil {
			return ErrUnavailable
		}
	} else if err != nil {
		return ErrUnavailable
	}
	if dirty {
		return ErrUnavailable
	}
	if version == 1 {
		if checksum != authorityChecksum() {
			return ErrUnavailable
		}
		return nil
	}
	if version != 0 || checksum != "" {
		return ErrUnavailable
	}
	if _, err = conn.ExecContext(ctx, "UPDATE v3_cp_authority_schema SET dirty=1 WHERE singleton=1 AND version=0 AND dirty=0"); err != nil {
		return ErrUnavailable
	}
	for _, statement := range strings.Split(authorityMigration, ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err = conn.ExecContext(ctx, statement); err != nil {
				return ErrUnavailable
			}
		}
	}
	if _, err = conn.ExecContext(ctx, "UPDATE v3_cp_authority_schema SET version=1,checksum=?,dirty=0 WHERE singleton=1 AND dirty=1", authorityChecksum()); err != nil {
		return ErrUnavailable
	}
	return nil
}

// SQLAuthority borrows the same CP pool as SQLStore. All supported v3 mutations
// use Save, and all conditional saves use WithCurrent. Neither method converts
// a v2 role, policy, Grant or Host enrollment into v3 permission.
type SQLAuthority struct{ db *sql.DB }

var _ Authority = (*SQLAuthority)(nil)

func NewSQLAuthority(db *sql.DB) *SQLAuthority { return &SQLAuthority{db: db} }
func (a *SQLAuthority) schema(ctx context.Context) error {
	if a == nil || a.db == nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	var version uint64
	var checksum string
	var dirty bool
	if a.db.QueryRowContext(ctx, "SELECT version,checksum,dirty FROM v3_cp_authority_schema WHERE singleton=1").Scan(&version, &checksum, &dirty) != nil || version != 1 || checksum != authorityChecksum() || dirty {
		return ErrUnavailable
	}
	// The atomicity argument requires physical InnoDB rows, including the existing
	// user identity and issuance tables. Defaults or a transaction declaration alone
	// are not sufficient. No DDL/repair or isolation downgrade is attempted here.
	var engines int
	if a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('users','v3_cp_authority_epoch','v3_cp_authority_records','v3_cp_authority_history','v3_cp_authorization_issuances') AND ENGINE='InnoDB'").Scan(&engines) != nil || engines != 5 {
		return ErrUnavailable
	}
	return nil
}
func (a *SQLAuthority) Generation(ctx context.Context) (uint64, error) {
	if a.schema(ctx) != nil {
		return 0, ErrUnavailable
	}
	var generation uint64
	if a.db.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1").Scan(&generation) != nil {
		return 0, ErrUnavailable
	}
	return generation, nil
}

// Save is a trusted CP-owner persistence boundary, NOT an unauthenticated
// provisioning API. ManagementService supplies current v3 grant authorization,
// an epoch-locked transaction and mandatory audit before committing a bound Save.
// No v2 importer, request auto-registration or production bootstrap is supplied.
// A batch is atomic, CAS-versioned and append-only in history; revocation uses
// explicit inactive/revoked/UNKNOWN current records instead of destructive delete.
func (a *SQLAuthority) Save(ctx context.Context, expected uint64, records []AuthorityRecord) (uint64, error) {
	if ctx == nil {
		return 0, ErrUnavailable
	}
	if len(records) == 0 || len(records) > 1024 || expected == ^uint64(0) {
		return 0, ErrInvalid
	}
	type entry struct {
		key  authorityRecordKey
		body []byte
	}
	entries := make([]entry, 0, len(records))
	seen := map[authorityRecordKey]bool{}
	for _, record := range records {
		key, body, err := encodeAuthorityRecord(record)
		if err != nil || seen[key] {
			return 0, ErrInvalid
		}
		seen[key] = true
		entries = append(entries, entry{key, body})
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].key, entries[j].key
		return string(a.kind)+"\x00"+a.realm+"\x00"+a.first+"\x00"+a.second < string(b.kind)+"\x00"+b.realm+"\x00"+b.first+"\x00"+b.second
	})
	var tx *sql.Tx
	var err error
	binding, _ := ctx.Value(managementSaveKey{}).(*managementSaveTransaction)
	owned := binding == nil
	if binding != nil {
		binding.mu.Lock()
		defer binding.mu.Unlock()
		if !binding.active || binding.used || binding.db != a.db || binding.expected != expected {
			return 0, ErrConflict
		}
		binding.used = true
		tx = binding.tx
	} else {
		if a.schema(ctx) != nil {
			return 0, ErrUnavailable
		}
		tx, err = a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return 0, ErrUnavailable
		}
		defer tx.Rollback()
	}
	var current uint64
	if tx.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE").Scan(&current) != nil {
		return 0, ErrUnavailable
	}
	if current != expected {
		return 0, ErrConflict
	}
	next := current + 1
	for ordinal, e := range entries {
		k := e.key
		if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_authority_records(kind,realm,first_id,second_id,generation,record_bytes,updated_at) VALUES(?,?,?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE generation=VALUES(generation),record_bytes=VALUES(record_bytes),updated_at=VALUES(updated_at)", k.kind, k.realm, k.first, k.second, next, e.body); err != nil {
			return 0, ErrUnavailable
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_authority_history(generation,ordinal,kind,realm,first_id,second_id,record_bytes,updated_at) VALUES(?,?,?,?,?,?,?,UTC_TIMESTAMP(6))", next, ordinal, k.kind, k.realm, k.first, k.second, e.body); err != nil {
			return 0, ErrUnavailable
		}
	}
	updated, err := tx.ExecContext(ctx, "UPDATE v3_cp_authority_epoch SET generation=? WHERE singleton=1 AND generation=?", next, current)
	if err != nil {
		return 0, ErrUnavailable
	}
	if n, e := updated.RowsAffected(); e != nil || n != 1 {
		return 0, ErrConflict
	}
	if ctx.Err() != nil {
		return 0, ErrUnavailable
	}
	if owned && (tx.Commit() != nil || ctx.Err() != nil) {
		return 0, ErrStoreUnknown
	}
	return next, nil
}
