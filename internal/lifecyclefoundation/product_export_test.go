package lifecyclefoundation

import (
	"context"
	"database/sql"
	"testing"
)

// These constructors are compiled exclusively into this package's test binary.
// A dependent product package cannot import them or construct a successful Gate.
type ProductTestEnvironment struct {
	DB          *sql.DB
	Binding     Binding
	Gate        *Gate
	f           *fixture
	BeforePort  func(string)
	AfterPort   func(string)
	Unavailable bool
	UnknownHead bool
}
type productTestAuthorizer struct{ actor string }

func (a productTestAuthorizer) Resolve(ctx context.Context, intent Intent) (Authorization, error) {
	return Authorization{Actor: a.actor, PrincipalKind: "HUMAN", Intent: intent}, ctx.Err()
}
func (a productTestAuthorizer) ValidateTx(ctx context.Context, tx *sql.Tx, auth Authorization, _ Checkpoint) error {
	var state string
	if auth.Actor != a.actor || tx.QueryRowContext(ctx, "SELECT status FROM users WHERE id=?", a.actor).Scan(&state) != nil || state != "active" {
		return ErrHeld
	}
	return nil
}
func NewProductTestEnvironment(t *testing.T) *ProductTestEnvironment {
	f := newFixture(t)
	return &ProductTestEnvironment{DB: f.db, Binding: f.binding, f: f}
}
func ResumeProductTestEnvironment(t *testing.T, binding Binding, actor string, source CheckpointSource) *ProductTestEnvironment {
	db := disposableDB(t)
	cp, err := NewSQLStore(db, binding)
	must(t, err)
	f := &fixture{db: db, cp: cp, ledger: &syntheticLedger{db: db}, binding: binding}
	e := &ProductTestEnvironment{DB: db, Binding: binding, f: f}
	e.attach(t, actor, source)
	return e
}
func (e *ProductTestEnvironment) attach(t *testing.T, actor string, source CheckpointSource) {
	service, err := NewService(e.f.cp, productTestPort{e}, productTestAuthorizer{actor})
	must(t, err)
	e.f.service = service
	e.Gate = &Gate{service: service, source: source}
}
func (e *ProductTestEnvironment) Configure(t *testing.T, actor string, source CheckpointSource) {
	cp, err := source(context.Background(), e.DB)
	must(t, err)
	frame := Frame{Binding: e.Binding, Checkpoint: cp, Phase: Completed}
	_, err = e.DB.Exec("UPDATE v3_cp_lifecycle_heads SET body=? WHERE deployment=?", encode(frame), e.Binding.Deployment)
	must(t, err)
	_, err = e.DB.Exec("UPDATE b0_test_ledger_heads SET body=? WHERE deployment=?", encode(Head{Frame: frame, Phase: Completed}), e.Binding.Deployment)
	must(t, err)
	e.attach(t, actor, source)
}
func (e *ProductTestEnvironment) Fault(stage Phase, after bool) {
	if stage == CPCommitted || stage == ReceiptSaved {
		count := 0
		e.f.cp.commit = func(tx *sql.Tx) error {
			count++
			target := 1
			if stage == ReceiptSaved {
				target = 2
			}
			if count != target {
				return tx.Commit()
			}
			if after {
				if err := tx.Commit(); err != nil {
					return err
				}
			} else {
				if err := tx.Rollback(); err != nil {
					return err
				}
			}
			return ErrUnknown
		}
	} else {
		e.f.ledger.fault, e.f.ledger.after = stage, after
	}
}
func (e *ProductTestEnvironment) AfterCPCommit(fn func(int)) {
	count := 0
	e.f.cp.commit = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		count++
		fn(count)
		return nil
	}
}

type productTestPort struct{ e *ProductTestEnvironment }

func (p productTestPort) before(op string) {
	if p.e.BeforePort != nil {
		p.e.BeforePort(op)
	}
}
func (p productTestPort) after(op string) {
	if p.e.AfterPort != nil {
		p.e.AfterPort(op)
	}
}
func (p productTestPort) ObserveLatest(ctx context.Context, b Binding, c string) (Observation, error) {
	p.before("ObserveLatest")
	if p.e.Unavailable {
		return Observation{}, ErrHeld
	}
	r, e := p.e.f.ledger.ObserveLatest(ctx, b, c)
	if p.e.UnknownHead {
		r.Head.Frame.Phase = Phase("UNKNOWN")
	}
	p.after("ObserveLatest")
	return r, e
}
func (p productTestPort) Reserve(ctx context.Context, r Reservation) (Reservation, error) {
	p.before("Reserve")
	out, e := p.e.f.ledger.Reserve(ctx, r)
	p.after("Reserve")
	return out, e
}
func (p productTestPort) Finalize(ctx context.Context, c CommitRecord) (Receipt, error) {
	p.before("Finalize")
	r, e := p.e.f.ledger.Finalize(ctx, c)
	p.after("Finalize")
	return r, e
}
func (p productTestPort) Complete(ctx context.Context, r SavedReceipt) error {
	p.before("Complete")
	e := p.e.f.ledger.Complete(ctx, r)
	p.after("Complete")
	return e
}
func (p productTestPort) InspectAttempt(ctx context.Context, b Binding, id string) (Attempt, error) {
	p.before("InspectAttempt")
	r, e := p.e.f.ledger.InspectAttempt(ctx, b, id)
	p.after("InspectAttempt")
	return r, e
}
