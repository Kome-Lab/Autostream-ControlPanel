package lifecyclefoundation

import (
	"context"
	"crypto/sha256"
	"crypto/rand"
	"encoding/hex"
	"database/sql"
	"fmt"
)

// Gate has no configured production constructor in B1. A zero/required Gate
// rejects; only _test.go can attach the persistent synthetic Service and source.
type Gate struct {
	service *Service
	source CheckpointSource
}

type Executor interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type SQLTransaction interface {
	Executor
	Commit() error
	Rollback() error
}

type CheckpointSource func(context.Context, Executor) (Checkpoint, error)

type Call struct {
	Operation string
	Purpose string
	Actor string
	Target any
	Content any
}

type gateKey struct{}
type gateAdmission struct {
	gate *Gate
	call Call
}

func NewRequiredGate() *Gate { return &Gate{} }

func (g *Gate) Ready() error {
	if g == nil || g.service == nil || g.source == nil { return ErrHeld }
	return nil
}

// ProductStartup rejects a request for L2 before database open/migrations.
// B1 intentionally has no environment setting that enables a real/synthetic Gate.
func ProductStartup(setting string) error {
	switch setting {
	case "", "off", "false", "0": return nil
	default: return ErrHeld
	}
}

// SyntheticReference binds test-profile content equality only. It is not an
// adopted identity commitment, signature profile or proof against an attacker.
// Product adapters call it only after a Gate passes Ready.
func SyntheticReference(value any) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(encode(value))))
}

func (g *Gate) Require(ctx context.Context, operation string) (bool, error) {
	if g.Ready() != nil || ctx == nil || ctx.Err() != nil { return false, ErrHeld }
	a, exists := ctx.Value(gateKey{}).(*gateAdmission)
	if !exists { return false, nil }
	if a == nil || a.gate != g { return true, ErrHeld }
	if a.call.Operation != operation &&
		!(a.call.Operation == "management.apply" && operation == "authority.save") &&
		!(a.call.Operation == "evidence.finish" && operation == "evidence.store.finish") {
		return true, ErrHeld
	}
	p, _ := ctx.Value(participationKey{}).(*participation)
	if p == nil { return true, ErrHeld }
	if err := RequireParticipation(ctx, g.service.store, p.request); err != nil { return true, err }
	return true, nil
}

// Do owns no second state machine: admission, quarantine and completion are B0
// Service.Run. Each durable issuance stage has a distinct admission bound to its
// unchanged issuance identity. No SQL lock spans clock/signing provider work.
func (g *Gate) Do(ctx context.Context, call Call, fn func(context.Context) error) error {
	if g.Ready() != nil || ctx == nil || fn == nil { return ErrHeld }
	if ctx.Value(gateKey{}) != nil || ctx.Value(participationKey{}) != nil { return ErrHeld }
	mode := Write
	if call.Purpose == "CURRENT_READ" { mode = Read }
	id, err := newAdmissionID()
	if err != nil { return ErrHeld }
	req := Request{ID: "product-" + id, Intent: Intent{
		Mode: mode, Purpose: call.Purpose, Operation: call.Operation,
		OperationContent: SyntheticReference(struct{ Actor string; Content any }{call.Actor, call.Content}),
		Scope: Scope{Type: "RESOURCE", ID: SyntheticReference(call.Target)}, Subject: "product",
	}}
	if req.Validate() != nil { return ErrHeld }
	result, err := g.service.Run(ctx, req, func(partCtx context.Context, transaction *Transaction) (Checkpoint, error) {
		p := transaction.proof
		if call.Actor != "" && p.authorization.Actor != call.Actor { return Checkpoint{}, ErrHeld }
		query := &participantExecutor{tx: transaction.tx, p: p, ctx: partCtx}
		before, err := g.source(partCtx, query)
		if err != nil || before != p.before { return Checkpoint{}, ErrHeld }
		p.gate = g
		admitted := context.WithValue(partCtx, gateKey{}, &gateAdmission{gate: g, call: call})
		if err = fn(admitted); err != nil { return Checkpoint{}, err }
		after, err := g.source(admitted, query)
		if err != nil { return Checkpoint{}, err }
		return after, nil
	})
	if err != nil { return err }
	if result.Outcome != "COMPLETE" { return ErrUnknown }
	return nil
}

func newAdmissionID() (string, error) {
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil { return "", err }
	return hex.EncodeToString(id[:]), nil
}

// SQL returns a private-context-bound executor, never the underlying *sql.Tx.
// Joining another DB, an expired callback or a context with no Gate is rejected.
func SQL(ctx context.Context, db *sql.DB) (Executor, bool, error) {
	if ctx == nil { return nil, false, ErrHeld }
	p, present := ctx.Value(participationKey{}).(*participation)
	if !present { return nil, false, nil }
	if p == nil || p.store.db != db || p.gate == nil { return nil, true, ErrHeld }
	if err := RequireParticipation(ctx, p.store, p.request); err != nil { return nil, true, err }
	return &participantExecutor{tx: p.tx, p: p, ctx: ctx}, true, nil
}

// BeginSQL shares the owning B0 transaction. Commit acknowledges only this local
// participant's completion; the real Commit remains exclusively in SQLStore.apply.
// A participant Rollback poisons the owner, even if its caller swallows the error.
func BeginSQL(ctx context.Context, db *sql.DB, options *sql.TxOptions) (SQLTransaction, error) {
	q, bound, err := SQL(ctx, db)
	if err != nil { return nil, err }
	if !bound { return db.BeginTx(ctx, options) }
	e := q.(*participantExecutor)
	h := &joinedTransaction{participantExecutor: e}
	e.p.mu.Lock()
	e.p.joins = append(e.p.joins, h)
	e.p.mu.Unlock()
	return h, nil
}

type participantExecutor struct {
	tx *sql.Tx
	p *participation
	ctx context.Context
}

func (e *participantExecutor) valid(ctx context.Context) error {
	if e == nil || ctx == nil || e.p == nil || e.tx == nil { return ErrHeld }
	return RequireParticipation(ctx, e.p.store, e.p.request)
}
func (e *participantExecutor) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if err := e.valid(ctx); err != nil { return nil, err }
	return e.tx.QueryContext(ctx, q, args...)
}
func (e *participantExecutor) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if e.valid(ctx) != nil {
		cancelled, cancel := context.WithCancel(context.Background()); cancel()
		return e.tx.QueryRowContext(cancelled, q, args...)
	}
	return e.tx.QueryRowContext(ctx, q, args...)
}
func (e *participantExecutor) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if err := e.valid(ctx); err != nil { return nil, err }
	if e.p.request.Intent.Mode == Read {
		a, _ := ctx.Value(gateKey{}).(*gateAdmission)
		if a == nil || a.call.Operation != "management.inspect" ||
			q != managementAuditInsert { return nil, ErrHeld }
	}
	return e.tx.ExecContext(ctx, q, args...)
}

// Only the existing fixed critical-audit INSERT is permitted in Management's R
// participant; arbitrary read-mode business DML remains forbidden.
const managementAuditInsert = "INSERT INTO v3_cp_management_audit(audit_id,event_id,observed_at,time_qualification,actor,operation,target_type,target_id,realm,host,resource,backend,action,before_revision,after_revision,result,reason_code,correlation_id) VALUES(?,?,UTC_TIMESTAMP(6),'UNQUALIFIED_DB_OBSERVATION',?,?,?,?,?,?,?,?,?,?,?,?,?,?)"

type joinedTransaction struct {
	*participantExecutor
	done bool
}
func (j *joinedTransaction) Commit() error {
	j.p.mu.Lock(); defer j.p.mu.Unlock()
	if !j.p.active || j.done || j.p.failed { return ErrHeld }
	j.done = true
	return nil
}
func (j *joinedTransaction) Rollback() error {
	j.p.mu.Lock(); defer j.p.mu.Unlock()
	if j.done { return nil }
	j.done = true
	j.p.failed = true
	return nil
}

func (g *Gate) Inspect(ctx context.Context, target any, id string) (Inspection, error) {
	if g.Ready() != nil { return Inspection{}, ErrHeld }
	return g.service.Inspect(ctx, Intent{Mode: Read, Purpose: "CURRENT_READ", Operation: "lifecycle.inspect",
		OperationContent: SyntheticReference(id), Scope: Scope{Type: "RESOURCE", ID: SyntheticReference(target)}, Subject: "product"}, id)
}

func (j *joinedTransaction) available() bool {
	j.p.mu.Lock(); defer j.p.mu.Unlock()
	return j.p.active && !j.done && !j.p.failed
}
func (j *joinedTransaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if !j.available() { return nil, ErrHeld }
	return j.participantExecutor.QueryContext(ctx, query, args...)
}
func (j *joinedTransaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if !j.available() { cancelled, cancel := context.WithCancel(context.Background()); cancel(); return j.tx.QueryRowContext(cancelled, query, args...) }
	return j.participantExecutor.QueryRowContext(ctx, query, args...)
}
func (j *joinedTransaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if !j.available() { return nil, ErrHeld }
	return j.participantExecutor.ExecContext(ctx, query, args...)
}
