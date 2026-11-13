package v3authorization

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

type managementSaveKey struct{}
type managementSaveTransaction struct {
	mu           sync.Mutex
	db           *sql.DB
	tx           *sql.Tx
	expected     uint64
	active, used bool
}
type ManagementService struct {
	authority *SQLAuthority
	bootstrap BootstrapAuthorizer
	uncertain atomic.Bool
	gate      chan struct{}
}

var _ ManagementAPI = (*ManagementService)(nil)

func NewManagementService(a *SQLAuthority, bootstrap BootstrapAuthorizer) *ManagementService {
	return &ManagementService{authority: a, bootstrap: bootstrap, gate: make(chan struct{}, 1)}
}

func (s *ManagementService) enter(ctx context.Context, mutation bool) (func(), error) {
	if s == nil || ctx == nil || s.gate == nil {
		return nil, ErrUnavailable
	}
	select {
	case <-ctx.Done():
		return nil, ErrUnavailable
	case s.gate <- struct{}{}:
	}
	release := func() { <-s.gate }
	if ctx.Err() != nil {
		release()
		return nil, ErrUnavailable
	}
	// Keep the gate through Commit acknowledgement and uncertainty publication.
	// InnoDB may release its epoch lock before the client knows Commit's result.
	if mutation && s.uncertain.Load() {
		release()
		return nil, ErrStoreUnknown
	}
	return release, nil
}

func (s *ManagementService) identity(v *authorityView, a ManagementActor) error {
	if e := v.principal(a.Principal); e != nil {
		return e
	}
	if len(a.SessionHash) != 64 {
		return ErrDenied
	}
	if _, e := hex.DecodeString(a.SessionHash); e != nil {
		return ErrDenied
	}
	var principal string
	var valid int
	e := v.tx.QueryRowContext(v.ctx, "SELECT user_id,(idle_expires_at>UTC_TIMESTAMP(6) AND absolute_expires_at>UTC_TIMESTAMP(6)) FROM sessions WHERE id=? FOR UPDATE", a.SessionHash).Scan(&principal, &valid)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return ErrUnavailable
	}
	if e != nil || principal != a.Principal.ID || valid != 1 {
		return ErrDenied
	}
	return nil
}
func managementInitialized(v *authorityView) (bool, error) {
	var state, event, principal string
	var generation uint64
	if v.tx.QueryRowContext(v.ctx, "SELECT state,event_id,principal,generation FROM v3_cp_management_bootstrap WHERE singleton=1 FOR UPDATE").Scan(&state, &event, &principal, &generation) != nil {
		return false, ErrUnavailable
	}
	if state == "UNINITIALIZED" && event == "" && principal == "" && generation == 0 {
		return false, nil
	}
	if state != "INITIALIZED" || !validID(event) || !validID(principal) || generation == 0 || generation > v.generation {
		return false, ErrUnavailable
	}
	return true, nil
}
func decodeManagementGrant(body []byte) (ManagementGrant, error) {
	var g ManagementGrant
	if len(body) == 0 || len(body) > 32<<10 || managementDecode(body, &g) != nil {
		return g, ErrUnavailable
	}
	canonical, e := encodeManagementGrant(g)
	if e != nil || !bytes.Equal(canonical, body) {
		return ManagementGrant{}, ErrUnavailable
	}
	return g, nil
}
func currentManagementGrant(v *authorityView, p Principal, scope evidence.Scope, action string) (ManagementGrant, error) {
	var body []byte
	var generation uint64
	e := v.tx.QueryRowContext(v.ctx, "SELECT grant_bytes,generation FROM v3_cp_management_grants WHERE realm=? AND host=? AND resource=? AND backend=? AND action=? AND principal=? FOR UPDATE", scope.Realm, scope.Host, scope.Resource, scope.Backend, action, p.ID).Scan(&body, &generation)
	if errors.Is(e, sql.ErrNoRows) {
		return ManagementGrant{}, ErrDenied
	}
	if e != nil || generation == 0 || generation > v.generation {
		return ManagementGrant{}, ErrUnavailable
	}
	g, e := decodeManagementGrant(body)
	if e != nil {
		return g, e
	}
	if g.Principal != p.ID || g.Scope != scope || g.Action != action {
		return ManagementGrant{}, ErrUnavailable
	}
	if g.State == "UNKNOWN" {
		return ManagementGrant{}, ErrUnavailable
	}
	if g.State != "ACTIVE" {
		return ManagementGrant{}, ErrDenied
	}
	return g, nil
}
func managementAudit(ctx context.Context, tx *sql.Tx, p Principal, m ManagementMetadata, operation, targetType, targetID string, before, after uint64, result, code string) error {
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		return ErrUnavailable
	}
	_, e := tx.ExecContext(ctx, "INSERT INTO v3_cp_management_audit(audit_id,event_id,observed_at,time_qualification,actor,operation,target_type,target_id,realm,host,resource,backend,action,before_revision,after_revision,result,reason_code,correlation_id) VALUES(?,?,UTC_TIMESTAMP(6),'UNQUALIFIED_DB_OBSERVATION',?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		hex.EncodeToString(id[:]), m.EventID, p.ID, operation, targetType, targetID, m.Scope.Realm, m.Scope.Host, m.Scope.Resource, m.Scope.Backend, m.Action, before, after, result, code, m.CorrelationID)
	if e != nil {
		return ErrUnavailable
	}
	return nil
}
func (s *ManagementService) auditView(ctx context.Context) (*authorityView, error) {
	tx, e := s.authority.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return nil, ErrUnavailable
	}
	v := &authorityView{ctx: ctx, tx: tx}
	if tx.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1").Scan(&v.generation) != nil {
		tx.Rollback()
		return nil, ErrUnavailable
	}
	return v, nil
}

func (s *ManagementService) denial(ctx context.Context, p Principal, m ManagementMetadata, code string) error {
	if s.schema(ctx) != nil {
		return ErrUnavailable
	}
	if !validID(p.ID) {
		p.ID = "unauthenticated"
	}
	targets, _ := ctx.Value(managementAuditTargetsKey{}).([]managementAuditTarget)
	if !validManagementMetadata(m) {
		m = ManagementMetadata{EventID: "invalid", CorrelationID: "invalid", Action: Action}
		targets = nil
	}
	if len(targets) == 0 || len(targets) > 32 {
		targets = []managementAuditTarget{{"denied", "management", "management"}}
	}
	v, e := s.auditView(ctx)
	if e != nil {
		return e
	}
	defer v.tx.Rollback()
	for _, target := range targets {
		if managementAudit(ctx, v.tx, p, m, target.operation, target.kind, target.id, v.generation, v.generation, "DENIED", code) != nil {
			return ErrUnavailable
		}
	}
	if v.tx.Commit() != nil || ctx.Err() != nil {
		return ErrStoreUnknown
	}
	return nil
}
func (s *ManagementService) fail(ctx context.Context, a ManagementActor, m ManagementMetadata, e error) ManagementResult {
	if e == ErrStoreUnknown {
		if s != nil {
			s.uncertain.Store(true)
		}
		return managementFailure(e)
	}
	if err := s.denial(ctx, a.Principal, m, managementFailure(e).Code); err != nil {
		return managementFailure(err)
	}
	return managementFailure(e)
}
func (s *ManagementService) RecordTransportDenial(ctx context.Context, p Principal, code string) error {
	allowed := false
	for _, v := range []string{"unauthenticated", "csrf_denied", "method_denied", "body_invalid", "inactive_principal", "route_not_found"} {
		if code == v {
			allowed = true
		}
	}
	if !allowed {
		return ErrInvalid
	}
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		return ErrUnavailable
	}
	event := hex.EncodeToString(id[:])
	m := ManagementMetadata{EventID: event, CorrelationID: event, Action: Action}
	// No request payload, token/hash, unknown key or arbitrary error is stored.
	if s.schema(ctx) != nil {
		return ErrUnavailable
	}
	if !validID(p.ID) {
		p.ID = "unauthenticated"
	}
	v, e := s.auditView(ctx)
	if e != nil {
		return e
	}
	defer v.tx.Rollback()
	if managementAudit(ctx, v.tx, p, m, "transport", "management", "management", v.generation, v.generation, "DENIED", code) != nil {
		return ErrUnavailable
	}
	if v.tx.Commit() != nil || ctx.Err() != nil {
		return ErrStoreUnknown
	}
	return nil
}
func (s *ManagementService) begin(ctx context.Context, a ManagementActor, m ManagementMetadata) (*authorityView, error) {
	if s.schema(ctx) != nil {
		return nil, ErrUnavailable
	}
	if !validManagementMetadata(m) {
		return nil, ErrInvalid
	}
	v, e := s.authority.view(ctx, true)
	if e != nil {
		return nil, e
	}
	if e = s.identity(v, a); e != nil {
		v.tx.Rollback()
		return nil, e
	}
	return v, nil
}
func managementDuplicate(v *authorityView, a ManagementActor, m ManagementMetadata) error {
	var n int
	if v.tx.QueryRowContext(v.ctx, "SELECT COUNT(*) FROM v3_cp_management_audit WHERE actor=? AND event_id=? AND result IN ('APPLIED','INSPECTED')", a.Principal.ID, m.EventID).Scan(&n) != nil {
		return ErrUnavailable
	}
	if n != 0 {
		return ErrConflict
	}
	return nil
}
func (s *ManagementService) commit(ctx context.Context, v *authorityView) error {
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	if v.tx.Commit() != nil || ctx.Err() != nil {
		s.uncertain.Store(true)
		return ErrStoreUnknown
	}
	return nil
}
func managementAdvance(v *authorityView) error {
	if v.generation == ^uint64(0) {
		return ErrConflict
	}
	r, e := v.tx.ExecContext(v.ctx, "UPDATE v3_cp_authority_epoch SET generation=? WHERE singleton=1 AND generation=?", v.generation+1, v.generation)
	if e != nil {
		return ErrUnavailable
	}
	n, e := r.RowsAffected()
	if e != nil || n != 1 {
		return ErrConflict
	}
	return nil
}
func managementSaveGrant(v *authorityView, g ManagementGrant, ordinal int) error {
	body, e := encodeManagementGrant(g)
	if e != nil {
		return e
	}
	_, e = v.tx.ExecContext(v.ctx, "INSERT INTO v3_cp_management_grants(realm,host,resource,backend,action,principal,generation,grant_bytes) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE generation=VALUES(generation),grant_bytes=VALUES(grant_bytes)", g.Scope.Realm, g.Scope.Host, g.Scope.Resource, g.Scope.Backend, g.Action, g.Principal, v.generation+1, body)
	if e != nil {
		return ErrUnavailable
	}
	if _, e = v.tx.ExecContext(v.ctx, "INSERT INTO v3_cp_management_grant_history(generation,ordinal,grant_bytes) VALUES(?,?,?)", v.generation+1, ordinal, body); e != nil {
		return ErrUnavailable
	}
	return nil
}

// Bootstrap is internal composition only; no HTTP/startup/CLI caller or default
// authorizer is supplied. V2 identity never provides its independent approval.
func (s *ManagementService) Bootstrap(ctx context.Context, a ManagementActor, m ManagementMetadata, g ManagementGrant) ManagementResult {
	release, enterErr := s.enter(ctx, true)
	if enterErr != nil {
		return managementFailure(enterErr)
	}
	defer release()
	_, own, cloneErr := freezeManagementChanges(nil, []ManagementGrant{g})
	if cloneErr != nil {
		return s.fail(ctx, a, m, cloneErr)
	}
	g = own[0]
	ctx = context.WithValue(ctx, managementAuditTargetsKey{}, []managementAuditTarget{{"bootstrap", "administrator", g.Principal}})
	v, e := s.begin(ctx, a, m)
	if e != nil {
		return s.fail(ctx, a, m, e)
	}
	reject := func(e error) ManagementResult { v.tx.Rollback(); return s.fail(ctx, a, m, e) }
	defer v.tx.Rollback()
	initialized, e := managementInitialized(v)
	if e != nil {
		return reject(e)
	}
	if initialized {
		return reject(ErrConflict)
	}
	if v.generation != m.ExpectedRevision {
		return reject(ErrConflict)
	}
	if g.Principal != a.Principal.ID || g.Scope != m.Scope || g.Action != m.Action || g.State != "ACTIVE" || !hasManagementOperation(g, ManagementRead) || !hasManagementOperation(g, ManagementAdministrator) {
		return reject(ErrInvalid)
	}
	if _, e = encodeManagementGrant(g); e != nil {
		return reject(e)
	}
	for _, p := range g.Subjects {
		if v.principal(Principal{p}) != nil {
			return reject(ErrDenied)
		}
	}
	if s.bootstrap == nil {
		return reject(ErrUnavailable)
	}
	approvalBytes, _ := encodeManagementGrant(g)
	approvalGrant, _ := decodeManagementGrant(approvalBytes)
	if approvalErr := s.bootstrap.AuthorizeBootstrap(ctx, ManagementActor{Principal: a.Principal}, m, approvalGrant); approvalErr != nil {
		switch approvalErr {
		case ErrStoreUnknown:
			return reject(ErrStoreUnknown)
		case ErrDenied:
			return reject(ErrDenied)
		default:
			return reject(ErrUnavailable)
		}
	}
	if managementDuplicate(v, a, m) != nil {
		return reject(ErrConflict)
	}
	if e = managementSaveGrant(v, g, 0); e != nil {
		return reject(e)
	}
	if e = managementAdvance(v); e != nil {
		return reject(e)
	}
	r, e := v.tx.ExecContext(ctx, "UPDATE v3_cp_management_bootstrap SET state='INITIALIZED',event_id=?,principal=?,generation=? WHERE singleton=1 AND state='UNINITIALIZED'", m.EventID, a.Principal.ID, v.generation+1)
	if e != nil {
		return reject(ErrUnavailable)
	}
	n, e := r.RowsAffected()
	if e != nil || n != 1 {
		return reject(ErrConflict)
	}
	if managementAudit(ctx, v.tx, a.Principal, m, "bootstrap", "administrator", g.Principal, v.generation, v.generation+1, "APPLIED", "ok") != nil {
		return reject(ErrUnavailable)
	}
	if e = s.identity(v, a); e != nil {
		return reject(e)
	}
	if e = s.commit(ctx, v); e != nil {
		return managementFailure(e)
	}
	return ManagementResult{Status: ManagementApplied, Code: "v3_management_applied", Generation: v.generation + 1, Initialized: true}
}
func freezeManagementChanges(records []AuthorityRecord, grants []ManagementGrant) ([]AuthorityRecord, []ManagementGrant, error) {
	out := make([]AuthorityRecord, 0, len(records))
	gs := make([]ManagementGrant, 0, len(grants))
	for _, r := range records {
		k, b, e := encodeAuthorityRecord(r)
		if e != nil {
			return nil, nil, e
		}
		c, e := decodeAuthorityRecord(b, k)
		if e != nil {
			return nil, nil, e
		}
		out = append(out, c)
	}
	for _, g := range grants {
		b, e := encodeManagementGrant(g)
		if e != nil {
			return nil, nil, e
		}
		c, e := decodeManagementGrant(b)
		if e != nil {
			return nil, nil, e
		}
		gs = append(gs, c)
	}
	return out, gs, nil
}

func (s *ManagementService) Apply(ctx context.Context, a ManagementActor, m ManagementMetadata, records []AuthorityRecord, grants []ManagementGrant) ManagementResult {
	release, enterErr := s.enter(ctx, true)
	if enterErr != nil {
		return managementFailure(enterErr)
	}
	defer release()
	if len(records)+len(grants) == 0 || len(records)+len(grants) > 32 {
		return s.fail(ctx, a, m, ErrInvalid)
	}
	var cloneErr error
	records, grants, cloneErr = freezeManagementChanges(records, grants)
	if cloneErr != nil {
		return s.fail(ctx, a, m, cloneErr)
	}
	ctx = context.WithValue(ctx, managementAuditTargetsKey{}, managementTargets(records, grants))
	v, e := s.begin(ctx, a, m)
	if e != nil {
		return s.fail(ctx, a, m, e)
	}
	defer v.tx.Rollback()
	reject := func(e error) ManagementResult { v.tx.Rollback(); return s.fail(ctx, a, m, e) }
	initialized, e := managementInitialized(v)
	if e != nil {
		return reject(e)
	}
	if !initialized {
		return reject(ErrManagementUninitialized)
	}
	g, e := currentManagementGrant(v, a.Principal, m.Scope, m.Action)
	if e == ErrDenied && len(records) == 0 && len(grants) != 0 {
		g, e = managementProvisioner(v, a.Principal, m, grants)
	}
	if e != nil {
		return reject(e)
	}
	if v.generation != m.ExpectedRevision {
		return reject(ErrConflict)
	}
	if e = managementDuplicate(v, a, m); e != nil {
		return reject(e)
	}
	if e = managementValidateRecords(v, a, m, g, records); e != nil {
		return reject(e)
	}
	seen := map[string]bool{}
	for _, next := range grants {
		if _, e = encodeManagementGrant(next); e != nil {
			return reject(e)
		}
		if !managementGrantDelegationAllowed(g, next, m.Scope, m.Action) || seen[next.Principal] {
			return reject(ErrDenied)
		}
		seen[next.Principal] = true
		for _, op := range next.Operations {
			if !hasManagementOperation(g, op) {
				return reject(ErrDenied)
			}
		}
		for _, p := range next.Subjects {
			if !managementSubject(g, p) {
				return reject(ErrDenied)
			}
			if next.State == "ACTIVE" && v.principal(Principal{p}) != nil {
				return reject(ErrDenied)
			}
		}
		if next.State == "ACTIVE" && v.principal(Principal{next.Principal}) != nil {
			return reject(ErrDenied)
		}
	}
	if len(records) != 0 {
		b := &managementSaveTransaction{db: s.authority.db, tx: v.tx, expected: v.generation, active: true}
		saveCtx := context.WithValue(ctx, managementSaveKey{}, b)
		_, e = s.authority.Save(saveCtx, v.generation, records)
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		if e != nil {
			return reject(e)
		}
	} else if e = managementAdvance(v); e != nil {
		return reject(e)
	}
	for n, next := range grants {
		if e = managementSaveGrant(v, next, n); e != nil {
			return reject(e)
		}
	}
	for _, r := range records {
		k, _ := authorityKey(r)
		target := k.first
		if k.second != "" {
			target += ":" + k.second
		}
		if managementAudit(ctx, v.tx, a.Principal, m, string(managementOperation(r.Kind)), string(r.Kind), target, v.generation, v.generation+1, "APPLIED", "ok") != nil {
			return reject(ErrUnavailable)
		}
	}
	for _, g := range grants {
		if managementAudit(ctx, v.tx, a.Principal, m, string(ManagementAdministrator), "administrator", g.Principal, v.generation, v.generation+1, "APPLIED", "ok") != nil {
			return reject(ErrUnavailable)
		}
	}
	if e = s.identity(v, a); e != nil {
		return reject(e)
	}
	if e = s.commit(ctx, v); e != nil {
		return managementFailure(e)
	}
	return ManagementResult{Status: ManagementApplied, Code: "v3_management_applied", Generation: v.generation + 1, Initialized: true}
}
func (s *ManagementService) Inspect(ctx context.Context, a ManagementActor, m ManagementMetadata) ManagementResult {
	release, enterErr := s.enter(ctx, false)
	if enterErr != nil {
		return managementFailure(enterErr)
	}
	defer release()
	ctx = context.WithValue(ctx, managementAuditTargetsKey{}, []managementAuditTarget{{"inspect", "scope", m.Scope.Resource}})
	v, e := s.begin(ctx, a, m)
	if e != nil {
		return s.fail(ctx, a, m, e)
	}
	defer v.tx.Rollback()
	reject := func(e error) ManagementResult { v.tx.Rollback(); return s.fail(ctx, a, m, e) }
	initialized, e := managementInitialized(v)
	if e != nil {
		return reject(e)
	}
	if !initialized {
		return reject(ErrManagementUninitialized)
	}
	g, e := currentManagementGrant(v, a.Principal, m.Scope, m.Action)
	if e != nil {
		return reject(e)
	}
	if !hasManagementOperation(g, ManagementRead) {
		return reject(ErrDenied)
	}
	if e = managementDuplicate(v, a, m); e != nil {
		return reject(e)
	}
	out := ManagementResult{Status: ManagementInspected, Code: "v3_management_inspected", Generation: v.generation, Initialized: true}
	administratorRows, e := v.tx.QueryContext(ctx, "SELECT principal,generation,grant_bytes FROM v3_cp_management_grants WHERE realm=? AND host=? AND resource=? AND backend=? AND action=? ORDER BY principal LIMIT 257", m.Scope.Realm, m.Scope.Host, m.Scope.Resource, m.Scope.Backend, m.Action)
	if e != nil {
		return reject(ErrUnavailable)
	}
	for administratorRows.Next() {
		var principal string
		var generation uint64
		var body []byte
		if administratorRows.Scan(&principal, &generation, &body) != nil || generation == 0 || generation > v.generation {
			administratorRows.Close()
			return reject(ErrUnavailable)
		}
		next, decodeErr := decodeManagementGrant(body)
		if decodeErr != nil || next.Principal != principal || next.Scope != m.Scope || next.Action != m.Action {
			administratorRows.Close()
			return reject(ErrUnavailable)
		}
		if managementSubject(g, principal) {
			out.Administrators = append(out.Administrators, next)
			if len(out.Administrators) > 256 {
				administratorRows.Close()
				return reject(ErrUnavailable)
			}
		}
	}
	e = administratorRows.Err()
	administratorRows.Close()
	if e != nil {
		return reject(ErrUnavailable)
	}
	// Read always returns the current authorized snapshot; no mutation CAS is
	// inferred from ExpectedRevision or the returned generation.
	domain := ""
	if host, err := v.get(HostRecord, m.Scope.Realm, m.Scope.Host, ""); err == nil && host.Record.Host != nil {
		domain = host.Record.Host.OwnerDomain
	}
	rows, e := v.tx.QueryContext(ctx, "SELECT kind,first_id,second_id,record_bytes FROM v3_cp_authority_records WHERE realm=? ORDER BY kind,first_id,second_id LIMIT 4097", m.Scope.Realm)
	if e != nil {
		return reject(ErrUnavailable)
	}
	count := 0
	for rows.Next() {
		count++
		if count > 4096 {
			rows.Close()
			return reject(ErrUnavailable)
		}
		var k authorityRecordKey
		k.realm = m.Scope.Realm
		var body []byte
		if rows.Scan(&k.kind, &k.first, &k.second, &body) != nil {
			rows.Close()
			return reject(ErrUnavailable)
		}
		record, e := decodeAuthorityRecord(body, k)
		if e != nil {
			rows.Close()
			return reject(e)
		}
		if managementRecordInScope(record, m.Scope, g, domain) {
			out.Records = append(out.Records, record)
			if len(out.Records) > 256 {
				rows.Close()
				return reject(ErrUnavailable)
			}
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return reject(ErrUnavailable)
	}
	if managementAudit(ctx, v.tx, a.Principal, m, "inspect", "scope", m.Scope.Resource, v.generation, v.generation, "INSPECTED", "ok") != nil {
		return reject(ErrUnavailable)
	}
	if e = s.identity(v, a); e != nil {
		return reject(e)
	}
	if e = s.commit(ctx, v); e != nil {
		return managementFailure(e)
	}
	return out
}

type managementAuditTargetsKey struct{}
type managementAuditTarget struct{ operation, kind, id string }

func managementTargets(records []AuthorityRecord, grants []ManagementGrant) []managementAuditTarget {
	out := make([]managementAuditTarget, 0, len(records)+len(grants))
	for _, r := range records {
		k, _ := authorityKey(r)
		id := k.first
		if k.second != "" {
			id += ":" + k.second
		}
		out = append(out, managementAuditTarget{string(managementOperation(r.Kind)), string(r.Kind), id})
	}
	for _, g := range grants {
		out = append(out, managementAuditTarget{string(ManagementAdministrator), "administrator", g.Principal})
	}
	return out
}

func managementGrantDelegationAllowed(actor, next ManagementGrant, scope evidence.Scope, action string) bool {
	if !hasManagementOperation(actor, ManagementAdministrator) || next.Scope != scope || next.Action != action ||
		!managementSubject(actor, next.Principal) || (next.ProvisionTargets && !actor.ProvisionTargets) {
		return false
	}
	for _, op := range next.Operations {
		if !hasManagementOperation(actor, op) {
			return false
		}
	}
	for _, subject := range next.Subjects {
		if !managementSubject(actor, subject) {
			return false
		}
	}
	return true
}

// Target enrollment requires explicit authority to delegate a precise new grant
// within this Realm. It never authorizes record writes or reads at the new Scope.
func managementProvisioner(v *authorityView, p Principal, m ManagementMetadata, grants []ManagementGrant) (ManagementGrant, error) {
	rows, err := v.tx.QueryContext(v.ctx, "SELECT host,resource,backend,generation,grant_bytes FROM v3_cp_management_grants WHERE realm=? AND action=? AND principal=? ORDER BY host,resource,backend LIMIT 65 FOR UPDATE", m.Scope.Realm, m.Action, p.ID)
	if err != nil {
		return ManagementGrant{}, ErrUnavailable
	}
	defer rows.Close()
	var selected ManagementGrant
	count := 0
	for rows.Next() {
		count++
		if count > 64 {
			return ManagementGrant{}, ErrUnavailable
		}
		var host, resource, backend string
		var generation uint64
		var body []byte
		if rows.Scan(&host, &resource, &backend, &generation, &body) != nil || generation == 0 || generation > v.generation {
			return ManagementGrant{}, ErrUnavailable
		}
		g, decodeErr := decodeManagementGrant(body)
		if decodeErr != nil || g.Principal != p.ID || g.Scope.Realm != m.Scope.Realm || g.Scope.Host != host ||
			g.Scope.Resource != resource || g.Scope.Backend != backend || g.Action != m.Action || g.State == "UNKNOWN" {
			return ManagementGrant{}, ErrUnavailable
		}
		if g.State != "ACTIVE" || !g.ProvisionTargets {
			continue
		}
		allowed := true
		for _, next := range grants {
			if !managementGrantDelegationAllowed(g, next, m.Scope, m.Action) {
				allowed = false
			}
		}
		if allowed {
			selected = g
		}
	}
	if rows.Err() != nil {
		return ManagementGrant{}, ErrUnavailable
	}
	if selected.Principal == "" {
		return ManagementGrant{}, ErrDenied
	}
	return selected, nil
}
