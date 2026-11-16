package bootstrapapproval

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

// Verifier is trusted composition, never selected or constructed from HTTP
// fields. VerifyAndConsume is invoked before any CP transaction/SQL lock. It
// must permanently preserve an uncertain/consumed attempt across CP rollback.
// Inspect is read-only and must never reissue, release or consume approval.
type Verifier interface {
	Qualification() Qualification
	VerifyAndConsume(context.Context, Request) (Verification, error)
	Inspect(context.Context, Request) (Verification, error)
}

type Core struct {
	db       *sql.DB
	verifier Verifier
	limits   lifecycle.Limits
}
type approvalKey struct{}
type approvalView struct {
	core         *Core
	verification Verification
	binding      string
	mu           sync.Mutex
	used         bool
}

// NewFixtureCore is an explicitly opt-in integration seam, never a production
// qualifier. The exact isolated CI database is enforced, including on restart.
// There is intentionally no production/default/environment-enabled constructor.
func NewFixtureCore(ctx context.Context, db *sql.DB, verifier Verifier, limits lifecycle.Limits) (*Core, error) {
	if ctx == nil || db == nil || verifier == nil || !qualified(verifier) || !limitsValid(limits) {
		return nil, ErrHeld
	}
	c := &Core{db: db, verifier: verifier, limits: limits}
	if c.schema(ctx, db) != nil {
		return nil, ErrHeld
	}
	return c, nil
}
func qualified(v Verifier) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		if r.IsNil() {
			return false
		}
	}
	q := v.Qualification()
	return q.ProfileID == FixtureProfile && q.FixtureOnly && !q.ProductionQualified
}
func limitsValid(l lifecycle.Limits) bool {
	return lifecycle.ValidateLimits(l).Kind == lifecycle.StructurallyCoherent && l.MaxIDBytes <= 128
}
func (c *Core) ProductionQualified() bool { return false }
func (c *Core) UsesDB(db *sql.DB) bool    { return c != nil && c.db == db }

// Prepare clones before and after the verifier call. Only this function can
// install the private ApprovalView. A plain Verification/manifest cannot do so.
func (c *Core) Prepare(ctx context.Context, r Request) (context.Context, error) {
	if c == nil || ctx == nil || ctx.Err() != nil || !qualified(c.verifier) || ctx.Value(approvalKey{}) != nil || validate(r, c.limits) != nil {
		return nil, ErrDenied
	}
	if c.schema(ctx, c.db) != nil {
		return nil, ErrHeld
	}
	frozen := cloneRequest(r)
	// A restarted caller cannot turn absence of a CP commit into fresh external
	// consumption. Require an explicit same-attempt known-unused observation;
	// unknown/consumed state proceeds only to Inspect, never automatic reissue.
	prior, inspectErr := c.verifier.Inspect(ctx, cloneRequest(frozen))
	prior = cloneVerification(prior)
	if inspectErr != nil || !originObservationValid(frozen, prior, c.limits) || prior.Consumed {
		return nil, ErrUnknown
	}
	if ctx.Err() != nil {
		return nil, ErrUnknown
	}
	v, err := c.verifier.VerifyAndConsume(ctx, cloneRequest(frozen))
	if err != nil {
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrHeld) {
			return nil, err
		}
		return nil, ErrUnknown
	}
	v = cloneVerification(v)
	if ctx.Err() != nil {
		return nil, ErrUnknown
	}
	if !qualified(c.verifier) || !verified(frozen, v, c.limits) {
		return nil, ErrDenied
	}
	return context.WithValue(ctx, approvalKey{}, &approvalView{core: c, verification: v, binding: Binding(frozen)}), nil
}
func verified(r Request, v Verification, l lifecycle.Limits) bool {
	return v.Consumed && originObservationValid(r, v, l)
}

func originObservationValid(r Request, v Verification, l lifecycle.Limits) bool {
	if len(v.Ceiling.Operations) > l.MaxRefs || len(v.Ceiling.Subjects) > l.MaxRefs || len(v.OriginID) > l.MaxIDBytes || len(v.ReceiptID) > l.MaxIDBytes {
		return false
	}
	for _, values := range [][]string{v.Ceiling.Operations, v.Ceiling.Subjects} {
		for _, value := range values {
			if len(value) > l.MaxIDBytes {
				return false
			}
		}
	}
	return validate(v.Request, l) == nil && Binding(r) == Binding(v.Request) && v.Knowledge == lifecycle.Known &&
		id(v.OriginID) && id(v.ReceiptID) && v.OriginRevision > 0 && ceilingAllows(r, v.Ceiling)
}

// Consume has no verifier/network/clock call. Every local write is performed
// through the original private DB/Tx/request/context-bound participant.
func (c *Core) Consume(ctx context.Context, db *sql.DB, tx lf.SQLTransaction, r Request, generation uint64) error {
	if c == nil || ctx == nil || db != c.db || validate(r, c.limits) != nil || generation != r.ExpectedRevision+1 {
		return ErrDenied
	}
	view, ok := ctx.Value(approvalKey{}).(*approvalView)
	if !ok || view == nil || view.core != c || view.binding != Binding(r) || !verified(r, view.verification, c.limits) {
		return ErrDenied
	}
	view.mu.Lock()
	defer view.mu.Unlock()
	if view.used {
		return ErrConflict
	}
	q, err := lf.ApprovalSQL(ctx, db, tx, view.binding)
	if err != nil || c.schema(ctx, q) != nil {
		return ErrHeld
	}
	view.used = true // Rollback cannot make this independently consumed view reusable.
	var revision uint64
	if q.QueryRowContext(ctx, "SELECT revision FROM v3_cp_approval_checkpoint WHERE singleton=1 FOR UPDATE").Scan(&revision) != nil || revision == ^uint64(0) {
		return ErrHeld
	}
	var count int
	if q.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_approval_consumptions WHERE manifest_id=? OR attempt_id=?", r.Reference.ManifestID, r.Reference.AttemptID).Scan(&count) != nil {
		return ErrHeld
	}
	if count != 0 {
		return ErrConflict
	}
	ceiling, _ := json.Marshal(view.verification.Ceiling)
	_, err = q.ExecContext(ctx, "INSERT INTO v3_cp_approval_consumptions(manifest_id,attempt_id,event_id,actor,purpose,request_binding,origin_id,origin_receipt,origin_revision,ceiling_bytes,generation,checkpoint_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", r.Reference.ManifestID, r.Reference.AttemptID, r.EventID, r.Principal, string(r.Reference.Identity.Purpose), view.binding, view.verification.OriginID, view.verification.ReceiptID, view.verification.OriginRevision, ceiling, generation, revision+1)
	if err != nil {
		return ErrConflict
	}
	if _, err = q.ExecContext(ctx, "INSERT INTO v3_cp_approval_history(revision,manifest_id,attempt_id,request_binding,generation) VALUES(?,?,?,?,?)", revision+1, r.Reference.ManifestID, r.Reference.AttemptID, view.binding, generation); err != nil {
		return ErrHeld
	}
	result, err := q.ExecContext(ctx, "UPDATE v3_cp_approval_checkpoint SET revision=?,manifest_id=?,attempt_id=?,request_binding=?,generation=? WHERE singleton=1 AND revision=?", revision+1, r.Reference.ManifestID, r.Reference.AttemptID, view.binding, generation, revision)
	if err != nil {
		return ErrHeld
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrConflict
	}
	return nil
}

// Inspect performs only same-attempt reads. Origin and CP knowledge remain
// separate even if a known external consumption has no local committed row.
// No call here can clear uncertainty, start a mutation, or release a hold.
func (c *Core) Inspect(ctx context.Context, r Request) (Inspection, error) {
	out := Inspection{OriginKnowledge: lifecycle.KnowledgeUnknown, CPKnowledge: lifecycle.KnowledgeUnknown, Gate: lifecycle.Held}
	if c == nil || ctx == nil || ctx.Err() != nil || !qualified(c.verifier) || validate(r, c.limits) != nil {
		return out, ErrDenied
	}
	v, err := c.verifier.Inspect(ctx, cloneRequest(r)) // strictly before opening the CP snapshot
	v = cloneVerification(v)
	if err == nil && originObservationValid(r, v, c.limits) {
		out.OriginKnowledge = lifecycle.Known
		out.OriginConsumed = v.Consumed
	}
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, ErrUnknown
	}
	defer tx.Rollback()
	if c.schema(ctx, tx) != nil {
		return out, ErrHeld
	}
	var binding, manifest, attempt, actor string
	var generation, revision uint64
	err = tx.QueryRowContext(ctx, "SELECT request_binding,manifest_id,attempt_id,actor,generation,checkpoint_revision FROM v3_cp_approval_consumptions WHERE attempt_id=?", r.Reference.AttemptID).Scan(&binding, &manifest, &attempt, &actor, &generation, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		// Row absence is not a no-effect proof if another member of the same
		// atomic result survived a partial restore. Preserve UNKNOWN/HELD.
		var traces int
		if tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM v3_cp_approval_history WHERE attempt_id=? OR manifest_id=?)+(SELECT COUNT(*) FROM v3_cp_approval_checkpoint WHERE attempt_id=? OR manifest_id=?)+(SELECT COUNT(*) FROM v3_cp_management_bootstrap WHERE event_id=?)+(SELECT COUNT(*) FROM v3_cp_management_audit WHERE actor=? AND event_id=? AND result='APPLIED')", r.Reference.AttemptID, r.Reference.ManifestID, r.Reference.AttemptID, r.Reference.ManifestID, r.EventID, r.Principal, r.EventID).Scan(&traces) != nil || traces != 0 {
			return out, ErrUnknown
		}
		out.CPKnowledge = lifecycle.Known
		return out, nil
	}
	if err != nil || binding != Binding(r) || manifest != r.Reference.ManifestID || attempt != r.Reference.AttemptID || actor != r.Principal || generation != r.ExpectedRevision+1 {
		return out, ErrUnknown
	}
	var n int
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_approval_history WHERE revision=? AND manifest_id=? AND attempt_id=? AND request_binding=? AND generation=?", revision, manifest, attempt, binding, generation).Scan(&n) != nil || n != 1 {
		return out, ErrUnknown
	}
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_approval_checkpoint WHERE singleton=1 AND revision=? AND manifest_id=? AND attempt_id=? AND request_binding=? AND generation=?", revision, manifest, attempt, binding, generation).Scan(&n) != nil || n != 1 {
		return out, ErrUnknown
	}
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_management_bootstrap WHERE singleton=1 AND state='INITIALIZED' AND event_id=? AND principal=? AND generation=?", r.EventID, r.Principal, generation).Scan(&n) != nil || n != 1 {
		return out, ErrUnknown
	}
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_management_grants WHERE principal=? AND realm=? AND host=? AND resource=? AND backend=? AND action=?", r.Principal, r.Scope.Realm, r.Scope.Host, r.Scope.Resource, r.Scope.Backend, r.Action).Scan(&n) != nil || n != 1 {
		return out, ErrUnknown
	}
	var grantBytes []byte
	if tx.QueryRowContext(ctx, "SELECT grant_bytes FROM v3_cp_management_grant_history WHERE generation=? AND ordinal=0", generation).Scan(&grantBytes) != nil {
		return out, ErrUnknown
	}
	digest := sha256.Sum256(grantBytes)
	if hex.EncodeToString(digest[:]) != r.GrantBinding {
		return out, ErrUnknown
	}
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_management_audit WHERE actor=? AND event_id=? AND operation='bootstrap' AND result='APPLIED' AND after_revision=?", r.Principal, r.EventID, generation).Scan(&n) != nil || n != 1 {
		return out, ErrUnknown
	}
	out.CPKnowledge = lifecycle.Known
	out.CPPresent = true
	out.Generation = generation
	return out, nil
}
