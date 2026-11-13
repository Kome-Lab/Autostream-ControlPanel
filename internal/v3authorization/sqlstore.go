package v3authorization

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	"github.com/go-sql-driver/mysql"
)

//go:embed migrations/001_cp_authorization.sql
var issuanceMigration string

const issuanceSchemaVersion = 1

var sqlIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)
var errSchemaUnavailable = SafeError{"v3_authorization_schema_unavailable"}

// SQLStore borrows an existing CP-owned pool. It never opens, closes, repairs or
// migrates that pool. These tables have no relationship to v2 mutation Grants.
type SQLStore struct{ db *sql.DB }

func NewSQLStore(db *sql.DB) *SQLStore { return &SQLStore{db: db} }

func issuanceChecksum() string {
	sum := sha256.Sum256([]byte(issuanceMigration))
	return hex.EncodeToString(sum[:])
}

// ApplyMigrations is an EXPLICIT administrative/test operation. It is deliberately
// separate from database.RunEmbeddedMigrations and every Open/Read path. DDL may
// commit independently on MariaDB; dirty therefore remains set on ANY failure.
// Calling it against development, staging or production data is not authorized
// by the synthetic tests which accompany this package.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errSchemaUnavailable
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return errSchemaUnavailable
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK('autostream-v3-cp-authorization-migration', 0)`).Scan(&locked); err != nil || locked != 1 {
		return errSchemaUnavailable
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `SELECT RELEASE_LOCK('autostream-v3-cp-authorization-migration')`)
	if _, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS v3_cp_authorization_schema (singleton TINYINT NOT NULL PRIMARY KEY, version BIGINT UNSIGNED NOT NULL, checksum VARBINARY(64) NOT NULL, dirty BOOLEAN NOT NULL, CHECK (singleton = 1)) ENGINE=InnoDB`); err != nil {
		return errSchemaUnavailable
	}
	var version uint64
	var checksum string
	var dirty bool
	err = conn.QueryRowContext(ctx, `SELECT version, checksum, dirty FROM v3_cp_authorization_schema WHERE singleton=1`).Scan(&version, &checksum, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = conn.ExecContext(ctx, `INSERT INTO v3_cp_authorization_schema(singleton,version,checksum,dirty) VALUES(1,0,'',0)`); err != nil {
			return errSchemaUnavailable
		}
		version = 0
		checksum = ""
		dirty = false
	} else if err != nil {
		return errSchemaUnavailable
	}
	if dirty {
		return errSchemaUnavailable
	}
	if version == issuanceSchemaVersion {
		if checksum != issuanceChecksum() {
			return errSchemaUnavailable
		}
		return nil
	}
	if version != 0 || checksum != "" {
		return errSchemaUnavailable
	}
	if _, err = conn.ExecContext(ctx, `UPDATE v3_cp_authorization_schema SET dirty=1 WHERE singleton=1 AND version=0 AND dirty=0`); err != nil {
		return errSchemaUnavailable
	}
	for _, stmt := range strings.Split(issuanceMigration, ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err = conn.ExecContext(ctx, stmt); err != nil {
				return errSchemaUnavailable
			}
		}
	}
	if _, err = conn.ExecContext(ctx, `UPDATE v3_cp_authorization_schema SET version=?,checksum=?,dirty=0 WHERE singleton=1 AND version=0 AND dirty=1`, issuanceSchemaVersion, issuanceChecksum()); err != nil {
		return errSchemaUnavailable
	}
	return nil
}

func (s *SQLStore) schema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errSchemaUnavailable
	}
	var version uint64
	var checksum string
	var dirty bool
	if s.db.QueryRowContext(ctx, `SELECT version,checksum,dirty FROM v3_cp_authorization_schema WHERE singleton=1`).Scan(&version, &checksum, &dirty) != nil || version != issuanceSchemaVersion || checksum != issuanceChecksum() || dirty {
		return errSchemaUnavailable
	}
	return nil
}

func (s *SQLStore) Claim(ctx context.Context, r Record) (Record, ClaimStatus, error) {
	if err := s.schema(ctx); err != nil {
		return Record{}, "", err
	}
	if !sqlValidRecord(r) || r.State != "PENDING" {
		return Record{}, "", ErrInvalid
	}
	body, err := json.Marshal(r)
	if err != nil {
		return Record{}, "", ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, "", ErrStoreUnknown
	}
	defer tx.Rollback()
	e := r.Intent.Execution
	_, err = tx.ExecContext(ctx, `INSERT INTO v3_cp_authorization_issuances(realm,request_id,execution_key,operation_id,attempt_id,step_id,claim_id,state,evidence_id,immutable_bytes,record_bytes) VALUES(?,?,?,?,?,?,?,?,NULL,?,?)`, r.Intent.Scope.Realm, e.RequestID, e.ExecutionKey, e.OperationID, e.AttemptID, e.StepID, r.ClaimID, r.State, r.Immutable, body)
	if err != nil {
		var me *mysql.MySQLError
		if !errors.As(err, &me) || me.Number != 1062 {
			return Record{}, "", ErrStoreUnknown
		}
		if tx.Rollback() != nil {
			return Record{}, "", ErrStoreUnknown
		}
		found, readErr := s.findIdentity(ctx, r)
		if readErr != nil {
			return Record{}, "", readErr
		}
		if len(found) != 1 || !bytes.Equal(found[0].Immutable, r.Immutable) {
			return Record{}, Collided, nil
		}
		return found[0], Existing, nil
	}
	if tx.Commit() != nil {
		return Record{}, "", ErrStoreUnknown
	}
	return sqlCloneRecord(r), Claimed, nil
}

const issuanceColumns = `realm,request_id,execution_key,operation_id,attempt_id,step_id,claim_id,state,evidence_id,immutable_bytes,record_bytes`

func (s *SQLStore) findIdentity(ctx context.Context, r Record) ([]Record, error) {
	e := r.Intent.Execution
	rows, err := s.db.QueryContext(ctx, `SELECT `+issuanceColumns+` FROM v3_cp_authorization_issuances WHERE realm=? AND (request_id=? OR execution_key=? OR (operation_id=? AND attempt_id=? AND step_id=?))`, r.Intent.Scope.Realm, e.RequestID, e.ExecutionKey, e.OperationID, e.AttemptID, e.StepID)
	if err != nil {
		return nil, ErrStoreUnknown
	}
	defer rows.Close()
	var found []Record
	for rows.Next() {
		record, err := sqlScanRecord(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, record)
		if len(found) > 3 {
			return nil, ErrConflict
		}
	}
	if rows.Err() != nil {
		return nil, ErrStoreUnknown
	}
	return found, nil
}

func (s *SQLStore) Finish(ctx context.Context, r Record) error {
	if err := s.schema(ctx); err != nil {
		return err
	}
	if !sqlValidRecord(r) || (r.State != "ISSUED" && r.State != "CLOSED") {
		return ErrInvalid
	}
	body, err := json.Marshal(r)
	if err != nil {
		return ErrInvalid
	}
	var id any
	if r.EvidenceID != "" {
		id = r.EvidenceID
	}
	// A failed acknowledgement is UNKNOWN even if the database applied this CAS.
	result, err := s.db.ExecContext(ctx, `UPDATE v3_cp_authorization_issuances SET state=?,evidence_id=?,record_bytes=? WHERE realm=? AND request_id=? AND claim_id=? AND immutable_bytes=? AND state='PENDING'`, r.State, id, body, r.Intent.Scope.Realm, r.Intent.Execution.RequestID, r.ClaimID, r.Immutable)
	if err != nil {
		return ErrStoreUnknown
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrStoreUnknown
	}
	if n == 1 {
		return nil
	}
	current, err := s.Lookup(ctx, Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution})
	if err != nil {
		return err
	}
	old, _ := json.Marshal(current)
	if bytes.Equal(old, body) {
		return nil
	}
	return ErrConflict
}

func (s *SQLStore) Lookup(ctx context.Context, q Lookup) (Record, error) {
	if err := s.schema(ctx); err != nil {
		return Record{}, err
	}
	if !sqlValidLookup(q) {
		return Record{}, ErrInvalid
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+issuanceColumns+` FROM v3_cp_authorization_issuances WHERE realm=? AND request_id=?`, q.Scope.Realm, q.Execution.RequestID)
	r, err := sqlScanRecord(row)
	if err != nil {
		return Record{}, err
	}
	if r.Intent.Scope != q.Scope || r.Intent.Execution != q.Execution || (q.EvidenceID != "" && q.EvidenceID != r.EvidenceID) {
		return Record{}, ErrConflict
	}
	return r, nil
}

type issuanceScanner interface{ Scan(...any) error }

func sqlScanRecord(row issuanceScanner) (Record, error) {
	var realm, request, key, operation, attempt, step, claim, state string
	var eid sql.NullString
	var immutable, body []byte
	err := row.Scan(&realm, &request, &key, &operation, &attempt, &step, &claim, &state, &eid, &immutable, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, ErrStoreUnknown
	}
	if len(body) > 2<<20 || !utf8.Valid(body) || sqlDuplicateJSON(body) {
		return Record{}, ErrConflict
	}
	var r Record
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return Record{}, ErrConflict
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Record{}, ErrConflict
	}
	canonical, marshalErr := json.Marshal(r)
	if marshalErr != nil || !bytes.Equal(canonical, body) {
		return Record{}, ErrConflict
	}
	e := r.Intent.Execution
	if !sqlValidRecord(r) || realm != r.Intent.Scope.Realm || request != e.RequestID || key != e.ExecutionKey || operation != e.OperationID || attempt != e.AttemptID || step != e.StepID || claim != r.ClaimID || state != r.State || eid.String != r.EvidenceID || !bytes.Equal(immutable, r.Immutable) {
		return Record{}, ErrConflict
	}
	return r, nil
}

func sqlValidLookup(q Lookup) bool {
	ids := []string{q.Scope.Realm, q.Scope.Host, q.Scope.Resource, q.Scope.Backend, q.Execution.RequestID, q.Execution.ExecutionKey, q.Execution.OperationID, q.Execution.AttemptID, q.Execution.StepID}
	for _, id := range ids {
		if !sqlIDPattern.MatchString(id) {
			return false
		}
	}
	return q.EvidenceID == "" || sqlIDPattern.MatchString(q.EvidenceID)
}
func sqlValidRecord(r Record) bool {
	if !sqlValidLookup(Lookup{Scope: r.Intent.Scope, Execution: r.Intent.Execution, EvidenceID: r.EvidenceID}) || !sqlIDPattern.MatchString(r.Principal) || !sqlIDPattern.MatchString(r.ClaimID) || !sqlIDPattern.MatchString(r.Issuer) || !sqlIDPattern.MatchString(r.Audience) || len(r.Immutable) == 0 || len(r.Immutable) > 1<<20 || len(r.Intent.Binding) == 0 || len(r.Intent.Binding) > 64<<10 {
		return false
	}
	switch r.State {
	case "PENDING", "CLOSED":
		return r.EvidenceID == "" && len(r.Envelope) == 0
	case "ISSUED":
		e, err := evidence.Decode(r.Envelope)
		return err == nil && e.Kind == evidence.CPAuthorizationKind && e.EvidenceID == r.EvidenceID && e.Issuer == r.Issuer && e.Audience == r.Audience && e.Scope == r.Intent.Scope && e.Execution == r.Intent.Execution && e.Binding == r.Intent.Binding && e.Frame == r.Frame && e.CPAuthorization != nil && e.CPAuthorization.Action == Action && e.CPAuthorization.Decision == "AUTHORIZED" && e.CPAuthorization.PolicyRevision == r.Decision.PolicyRevision && e.CPAuthorization.TrustRevision == r.Decision.TrustRevision
	default:
		return false
	}
}
func sqlCloneRecord(r Record) Record {
	r.Immutable = bytes.Clone(r.Immutable)
	r.Envelope = bytes.Clone(r.Envelope)
	return r
}

// Stored records are data too: reject duplicate keys recursively before normal
// typed decoding. No parser ambiguity may choose a different immutable identity.
func sqlDuplicateJSON(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	depth := 0
	var value func() error
	value = func() error {
		depth++
		defer func() { depth-- }()
		if depth > 64 {
			return ErrInvalid
		}
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return ErrInvalid
				}
				seen[s] = true
				if err = value(); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return ErrInvalid
			}
		case '[':
			for dec.More() {
				if err = value(); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		return nil
	}
	if value() != nil {
		return true
	}
	_, err := dec.Token()
	return err != io.EOF
}
