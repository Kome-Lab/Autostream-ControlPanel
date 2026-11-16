package v3authorization

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"strings"
)

//go:embed management_migrations/001_management.sql
var managementMigration string

func managementChecksum() string {
	sum := sha256.Sum256([]byte(managementMigration))
	return hex.EncodeToString(sum[:])
}

// Explicit, opt-in administration only. Never run by startup/constructors.
// A missing/dirty/partially existing state is NOT a fresh installation.
func ApplyManagementMigrations(ctx context.Context, db *sql.DB) error {
	if ctx == nil || db == nil || NewSQLAuthority(db).schema(ctx) != nil {
		return ErrUnavailable
	}
	conn, e := db.Conn(ctx)
	if e != nil {
		return ErrUnavailable
	}
	defer conn.Close()
	var locked int
	if conn.QueryRowContext(ctx, "SELECT GET_LOCK('autostream-v3-cp-management-migration',0)").Scan(&locked) != nil || locked != 1 {
		return ErrUnavailable
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK('autostream-v3-cp-management-migration')")
	if _, e = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS v3_cp_management_schema(singleton TINYINT NOT NULL PRIMARY KEY,version BIGINT UNSIGNED NOT NULL,checksum VARBINARY(64) NOT NULL,dirty BOOLEAN NOT NULL,CHECK(singleton=1)) ENGINE=InnoDB"); e != nil {
		return ErrUnavailable
	}
	var version uint64
	var checksum string
	var dirty bool
	e = conn.QueryRowContext(ctx, "SELECT version,checksum,dirty FROM v3_cp_management_schema WHERE singleton=1").Scan(&version, &checksum, &dirty)
	if errors.Is(e, sql.ErrNoRows) {
		var count int
		if conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('v3_cp_management_bootstrap','v3_cp_management_grants','v3_cp_management_grant_history','v3_cp_management_audit')").Scan(&count) != nil || count != 0 {
			return ErrUnavailable
		}
		if _, e = conn.ExecContext(ctx, "INSERT INTO v3_cp_management_schema VALUES(1,0,'',0)"); e != nil {
			return ErrUnavailable
		}
	} else if e != nil {
		return ErrUnavailable
	}
	if dirty {
		return ErrUnavailable
	}
	if version == 1 {
		if checksum != managementChecksum() {
			return ErrUnavailable
		}
		var state, event, principal string
		var generation, current uint64
		if conn.QueryRowContext(ctx, "SELECT state,event_id,principal,generation FROM v3_cp_management_bootstrap WHERE singleton=1").Scan(&state, &event, &principal, &generation) != nil ||
			conn.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1").Scan(&current) != nil {
			return ErrUnavailable
		}
		if state == "UNINITIALIZED" && event == "" && principal == "" && generation == 0 {
			return nil
		}
		if state != "INITIALIZED" || !validID(event) || !validID(principal) || generation == 0 || generation > current {
			return ErrUnavailable
		}
		return nil
	}
	if version != 0 || checksum != "" {
		return ErrUnavailable
	}
	if _, e = conn.ExecContext(ctx, "UPDATE v3_cp_management_schema SET dirty=1 WHERE singleton=1 AND version=0 AND dirty=0"); e != nil {
		return ErrUnavailable
	}
	for _, statement := range strings.Split(managementMigration, "\n-- statement\n") {
		if _, e = conn.ExecContext(ctx, strings.TrimSpace(statement)); e != nil {
			return ErrUnavailable
		}
	}
	if _, e = conn.ExecContext(ctx, "UPDATE v3_cp_management_schema SET version=1,checksum=?,dirty=0 WHERE singleton=1 AND dirty=1", managementChecksum()); e != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *ManagementService) schema(ctx context.Context) error {
	if s == nil || s.authority == nil || ctx == nil || s.authority.schema(ctx) != nil {
		return ErrUnavailable
	}
	var version uint64
	var checksum string
	var dirty bool
	if s.authority.db.QueryRowContext(ctx, "SELECT version,checksum,dirty FROM v3_cp_management_schema WHERE singleton=1").Scan(&version, &checksum, &dirty) != nil || version != 1 || checksum != managementChecksum() || dirty {
		return ErrUnavailable
	}
	var engines int
	if s.authority.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('v3_cp_management_schema','v3_cp_management_bootstrap','v3_cp_management_grants','v3_cp_management_grant_history','v3_cp_management_audit','sessions') AND ENGINE='InnoDB'").Scan(&engines) != nil || engines != 6 {
		return ErrUnavailable
	}
	var triggers int
	if s.authority.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND TRIGGER_NAME IN ('v3_management_audit_no_update','v3_management_audit_no_delete','v3_management_history_no_update','v3_management_history_no_delete','v3_management_bootstrap_no_delete','v3_management_bootstrap_one_way') AND ACTION_TIMING='BEFORE'").Scan(&triggers) != nil || triggers != 6 {
		return ErrUnavailable
	}
	return nil
}
