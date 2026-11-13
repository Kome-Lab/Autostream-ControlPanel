package v3authorization

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
)

type authorityView struct {
	ctx        context.Context
	tx         *sql.Tx
	generation uint64
	observedAt string
	locking    bool
}

func (v *authorityView) get(kind AuthorityRecordKind, realm, first, second string) (ownedRecord, error) {
	key := authorityRecordKey{kind, realm, first, second}
	query := "SELECT record_bytes,generation,DATE_FORMAT(updated_at,'%Y-%m-%dT%H:%i:%s.%fZ') FROM v3_cp_authority_records WHERE kind=? AND realm=? AND first_id=? AND second_id=?"
	if v.locking {
		query += " FOR UPDATE"
	}
	var body []byte
	var out ownedRecord
	if v.tx.QueryRowContext(v.ctx, query, kind, realm, first, second).Scan(&body, &out.Generation, &out.UpdatedAt) != nil || out.Generation == 0 || out.Generation > v.generation || out.UpdatedAt == "" {
		return ownedRecord{}, ErrUnavailable
	}
	record, err := decodeAuthorityRecord(body, key)
	if err != nil {
		return ownedRecord{}, ErrUnavailable
	}
	out.Record = record
	return out, nil
}
func (v *authorityView) principal(p Principal) error {
	if !validID(p.ID) {
		return ErrDenied
	}
	query := "SELECT id,status FROM users WHERE id=?"
	if v.locking {
		query += " FOR UPDATE"
	}
	var id, status string
	err := v.tx.QueryRowContext(v.ctx, query, p.ID).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	if err != nil {
		return ErrUnavailable
	}
	if id != p.ID || status != "active" {
		return ErrDenied
	}
	return nil
}
func (v *authorityView) capability(p Principal, s evidence.Scope) (V3Capability, error) {
	record, err := v.get(CapabilityRecord, s.Realm, p.ID, s.Resource)
	if err != nil {
		return V3Capability{}, err
	}
	c := *record.Record.Capability
	if c.Principal != p.ID || c.Scope != s || c.Action != Action {
		return V3Capability{}, ErrConflict
	}
	if err = requireOwnerActive(c.State); err != nil {
		return V3Capability{}, err
	}
	return c, nil
}
func (v *authorityView) membership(p Principal, realm, domain, required string) error {
	record, err := v.get(MembershipRecord, realm, p.ID, domain)
	if err != nil {
		return err
	}
	m := record.Record.Membership
	if m.Principal != p.ID || m.Realm != realm || m.Domain != domain {
		return ErrConflict
	}
	if m.State == "UNKNOWN" {
		return ErrUnavailable
	}
	if m.State != "MEMBER" {
		return ErrDenied
	}
	if required != "" && m.Revision != required {
		return ErrConflict
	}
	return nil
}
func (v *authorityView) target(i Intent) (ResolvedTarget, error) {
	if !validIntent(i) {
		return ResolvedTarget{}, ErrInvalid
	}
	s := i.Scope
	realm, err := v.get(RealmRecord, s.Realm, s.Realm, "")
	if err != nil {
		return ResolvedTarget{}, err
	}
	host, err := v.get(HostRecord, s.Realm, s.Host, "")
	if err != nil {
		return ResolvedTarget{}, err
	}
	resource, err := v.get(ResourceRecord, s.Realm, s.Resource, "")
	if err != nil {
		return ResolvedTarget{}, err
	}
	backend, err := v.get(BackendRecord, s.Realm, s.Backend, "")
	if err != nil {
		return ResolvedTarget{}, err
	}
	candidate, err := v.get(CandidateRecord, s.Realm, s.Resource, i.CandidateRevision)
	if err != nil {
		return ResolvedTarget{}, err
	}
	r, h, res, b, c := *realm.Record.Realm, *host.Record.Host, *resource.Record.Resource, *backend.Record.Backend, *candidate.Record.Candidate
	for _, state := range []string{r.State, h.State, res.State, b.State, c.State} {
		if err = requireOwnerActive(state); err != nil {
			return ResolvedTarget{}, err
		}
	}
	if r.Realm != s.Realm || h.Realm != s.Realm || h.Host != s.Host || res.Scope != s || b.Realm != s.Realm || b.Host != s.Host || b.Backend != s.Backend || c.Scope != s {
		return ResolvedTarget{}, ErrConflict
	}
	if c.Approval == "UNKNOWN" {
		return ResolvedTarget{}, ErrUnavailable
	}
	if c.Approval != "APPROVED" {
		return ResolvedTarget{}, ErrDenied
	}
	if c.Revision != i.CandidateRevision || c.ContentPin != i.CandidateContentPin || res.ReferenceRevision != i.ExpectedRevision || res.StateVersion != i.ExpectedStateVersion {
		return ResolvedTarget{}, ErrConflict
	}
	generation := uint64(0)
	for _, r := range []ownedRecord{realm, host, resource, backend, candidate} {
		if r.Generation > generation {
			generation = r.Generation
		}
	}
	return ResolvedTarget{r, h, res, b, c, ownerRevision("target", generation), v.observedAt}, nil
}
func (v *authorityView) rules(scope evidence.Scope, action string) (ResolvedPolicyTrust, error) {
	if !validScope(scope) || action != Action {
		return ResolvedPolicyTrust{}, ErrInvalid
	}
	result := ResolvedPolicyTrust{ObservedAt: v.observedAt}
	for _, kind := range []AuthorityRecordKind{PolicyRecord, TrustRecord} {
		record, err := v.get(kind, scope.Realm, scope.Resource, action)
		if err != nil {
			return ResolvedPolicyTrust{}, err
		}
		rule := *record.Record.Rule
		if rule.Scope != scope || rule.Action != action {
			return ResolvedPolicyTrust{}, ErrConflict
		}
		if err = requireOwnerActive(rule.State); err != nil {
			return ResolvedPolicyTrust{}, err
		}
		if rule.Revocation == "UNKNOWN" {
			return ResolvedPolicyTrust{}, ErrUnavailable
		}
		if rule.Revocation != "ACTIVE" {
			return ResolvedPolicyTrust{}, ErrDenied
		}
		resolved := ResolvedRule{rule, record.Generation, record.UpdatedAt}
		if kind == PolicyRecord {
			result.Policy = resolved
		} else {
			result.Trust = resolved
		}
	}
	return result, nil
}
func (v *authorityView) decision(p Principal, i Intent) (Decision, error) {
	if p.ID != i.RequestingPrincipal {
		return Decision{}, ErrDenied
	}
	if !validIntent(i) {
		return Decision{}, ErrInvalid
	}
	if err := v.principal(p); err != nil {
		return Decision{}, err
	}
	capability, err := v.capability(p, i.Scope)
	if err != nil {
		return Decision{}, err
	}
	if !capability.Issue {
		return Decision{}, ErrDenied
	}
	target, err := v.target(i)
	if err != nil {
		return Decision{}, err
	}
	if err = v.membership(p, i.Scope.Realm, target.Host.OwnerDomain, i.Required.Membership); err != nil {
		return Decision{}, err
	}
	rules, err := v.rules(i.Scope, i.Action)
	if err != nil {
		return Decision{}, err
	}
	if rules.Policy.Rule.Revision != i.Required.Policy || rules.Trust.Rule.Revision != i.Required.Trust {
		return Decision{}, ErrConflict
	}
	return Decision{Principal: p.ID, Scope: i.Scope, Action: i.Action, PolicyRevision: rules.Policy.Rule.Revision, TrustRevision: rules.Trust.Rule.Revision, AuthorityRevision: ownerRevision("authority", v.generation), TargetRevision: target.Revision, State: "ALLOWED"}, nil
}
func (a *SQLAuthority) view(ctx context.Context, locking bool) (*authorityView, error) {
	if a.schema(ctx) != nil {
		return nil, ErrUnavailable
	}
	options := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if locking {
		options = &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	}
	tx, err := a.db.BeginTx(ctx, options)
	if err != nil {
		return nil, ErrUnavailable
	}
	v := &authorityView{ctx: ctx, tx: tx, locking: locking}
	query := "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1"
	if locking {
		query += " FOR UPDATE"
	}
	if tx.QueryRowContext(ctx, query).Scan(&v.generation) != nil || tx.QueryRowContext(ctx, "SELECT CAST(UTC_TIMESTAMP(6) AS CHAR)").Scan(&v.observedAt) != nil {
		tx.Rollback()
		return nil, ErrUnavailable
	}
	return v, nil
}
func (a *SQLAuthority) snapshot(ctx context.Context, fn func(*authorityView) error) error {
	view, err := a.view(ctx, false)
	if err != nil {
		return err
	}
	defer view.tx.Rollback()
	if err = fn(view); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	if view.tx.Commit() != nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	return nil
}
func (a *SQLAuthority) Read(ctx context.Context, p Principal, i Intent) (Decision, error) {
	var decision Decision
	err := a.snapshot(ctx, func(v *authorityView) error { var err error; decision, err = v.decision(p, i); return err })
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}
func (a *SQLAuthority) ResolveTarget(ctx context.Context, i Intent) (ResolvedTarget, error) {
	var result ResolvedTarget
	err := a.snapshot(ctx, func(v *authorityView) error { var err error; result, err = v.target(i); return err })
	if err != nil {
		return ResolvedTarget{}, err
	}
	return result, nil
}
func (a *SQLAuthority) ResolvePolicyTrust(ctx context.Context, scope evidence.Scope, action string) (ResolvedPolicyTrust, error) {
	var result ResolvedPolicyTrust
	err := a.snapshot(ctx, func(v *authorityView) error { var err error; result, err = v.rules(scope, action); return err })
	if err != nil {
		return ResolvedPolicyTrust{}, err
	}
	return result, nil
}

// AuthorizeRead checks CURRENT v3 history permission, current user/membership,
// and exact persisted principal/Scope/Execution. It does not require current
// Issue permission, Candidate approval or Policy/Trust eligibility for a historic
// record. Knowing an Evidence ID never broadens a lookup.
func (a *SQLAuthority) AuthorizeRead(ctx context.Context, p Principal, q Lookup) error {
	if !sqlValidLookup(q) {
		return ErrInvalid
	}
	return a.snapshot(ctx, func(v *authorityView) error {
		if NewSQLStore(a.db).schemaUsing(ctx, v.tx) != nil {
			return ErrUnavailable
		}
		if err := v.principal(p); err != nil {
			return err
		}
		cap, err := v.capability(p, q.Scope)
		if err != nil {
			return err
		}
		if !cap.ReadHistory {
			return ErrDenied
		}
		host, err := v.get(HostRecord, q.Scope.Realm, q.Scope.Host, "")
		if err != nil {
			return err
		}
		if host.Record.Host.State == "UNKNOWN" {
			return ErrUnavailable
		}
		if err = v.membership(p, q.Scope.Realm, host.Record.Host.OwnerDomain, ""); err != nil {
			return err
		}
		record, err := lookupSQLRecord(ctx, v.tx, q)
		if err != nil {
			return ErrUnavailable
		}
		if record.Principal != p.ID || record.Intent.Scope != q.Scope || record.Intent.Execution != q.Execution {
			return ErrDenied
		}
		return nil
	})
}

type authorityTxKey struct{}
type authorityTransaction struct {
	mu               sync.Mutex
	db               *sql.DB
	tx               *sql.Tx
	principal        Principal
	intent           Intent
	decision         Decision
	active, finished bool
}

// WithCurrent is a one-shot, synchronous transaction boundary. Global epoch row
// -> existing user identity -> canonical owner records -> issuance row is the
// lock order. Save takes the same epoch row before ANY v3 mutation. Existing v2
// user disable/delete/lockout/credential writers conflict on the locked users row.
// SQLStore.Finish receives this SAME transaction via saveCtx; no global mutable
// transaction, separate autocommit save, timeout success or automatic retry exists.
func (a *SQLAuthority) WithCurrent(ctx context.Context, p Principal, i Intent, expected Decision, fn func(context.Context, Decision) error) error {
	if fn == nil {
		return ErrInvalid
	}
	view, err := a.view(ctx, true)
	if err != nil {
		return err
	}
	defer view.tx.Rollback()
	current, err := view.decision(p, i)
	if err != nil {
		return err
	}
	if validateDecision(p, i, expected) != nil || current != expected {
		return ErrConflict
	}
	binding := &authorityTransaction{db: a.db, tx: view.tx, principal: p, intent: i, decision: current, active: true}
	saveCtx := context.WithValue(ctx, authorityTxKey{}, binding)
	defer func() { binding.mu.Lock(); binding.active = false; binding.mu.Unlock() }()
	err = fn(saveCtx, current)
	// Retained callback contexts cannot write after this synchronous boundary.
	binding.mu.Lock()
	binding.active = false
	binding.mu.Unlock()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	if view.tx.Commit() != nil || ctx.Err() != nil {
		return ErrStoreUnknown
	}
	return nil
}
func (b *authorityTransaction) finish(ctx context.Context, s *SQLStore, r Record) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active || b.finished || s == nil || s.db != b.db || r.Principal != b.principal.ID || r.Intent != b.intent || r.Decision != b.decision {
		return ErrConflict
	}
	b.finished = true
	if s.schemaUsing(ctx, b.tx) != nil {
		return ErrUnavailable
	}
	return finishSQLRecord(ctx, b.tx, r)
}
