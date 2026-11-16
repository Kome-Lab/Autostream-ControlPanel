package lifecyclefoundation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

type explicitFixtureAuthorizer struct {
	base     *syntheticAuthorizer
	original Intent
	id       string
}

func (a *explicitFixtureAuthorizer) Resolve(_ context.Context, intent Intent) (Authorization, error) {
	if intent != a.original && !(intent.Mode == Read && intent.Purpose == "CURRENT_READ" && intent.Operation == "lifecycle.inspect" && intent.Scope == a.original.Scope && intent.Subject == a.original.Subject) {
		return Authorization{}, ErrHeld
	}
	return Authorization{Actor: "synthetic-human", PrincipalKind: "HUMAN", Intent: intent}, nil
}
func (a *explicitFixtureAuthorizer) ValidateTx(ctx context.Context, tx *sql.Tx, authorization Authorization, cp Checkpoint) error {
	return a.base.ValidateTx(ctx, tx, authorization, cp)
}

// The existing real SQL Service/ledger fixture exercises DoExplicit and its
// private transaction participant. This synthetic equality source qualifies no
// production secret commitment, Provider, independent host, or authority.
func TestManualAutoStartExplicitAttemptLifecycle(t *testing.T) {
	for _, ackLoss := range []bool{false, true} {
		name := "complete"
		if ackLoss {
			name = "finalize_ack_lost"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			id := "manual-resume-" + unique() + ":begin"
			call := Call{Operation: "manual.auto_start_hold.begin", Purpose: "AUTHORITY_CHANGE", Actor: "synthetic-human", Target: struct{ StreamID string }{"synthetic-stream"}, Content: struct {
				RequestID            string
				ExpectedHoldRevision uint64
			}{id, 3}}
			original := Intent{Mode: Write, Purpose: call.Purpose, Operation: call.Operation, OperationContent: SyntheticReference(struct {
				Actor   string
				Content any
			}{call.Actor, call.Content}), Scope: Scope{Type: "RESOURCE", ID: SyntheticReference(call.Target)}, Subject: "product"}
			authorizer := &explicitFixtureAuthorizer{base: f.auth, original: original, id: id}
			service, err := NewService(f.cp, f.ledger, authorizer)
			must(t, err)
			source := func(ctx context.Context, q Executor) (Checkpoint, error) {
				var value uint64
				if err := q.QueryRowContext(ctx, "SELECT value FROM b0_test_effects WHERE deployment=?", f.binding.Deployment).Scan(&value); err != nil {
					return Checkpoint{}, err
				}
				cp := initialCheckpoint()
				cp.AuthorityEpoch += value
				cp.Count = value
				cp.Content = SyntheticReference(struct{ Value uint64 }{value})
				cp.HistoryHead = fmt.Sprintf("synthetic-history-%d", value)
				return cp, nil
			}
			before, err := source(context.Background(), f.db)
			must(t, err)
			f.before = before
			frame := Frame{Binding: f.binding, Checkpoint: before, Phase: Completed}
			_, err = f.db.Exec("UPDATE v3_cp_lifecycle_heads SET body=? WHERE deployment=?", encode(frame), f.binding.Deployment)
			must(t, err)
			_, err = f.db.Exec("UPDATE b0_test_ledger_heads SET body=? WHERE deployment=?", encode(Head{Frame: frame, Phase: Completed}), f.binding.Deployment)
			must(t, err)
			gate := &Gate{service: service, source: source}
			if ackLoss {
				f.ledger.fault, f.ledger.after = Finalized, true
			}
			callbacks := 0
			result, runErr := gate.DoExplicit(context.Background(), id, call, func(ctx context.Context) error {
				callbacks++
				transaction, err := BeginSQL(ctx, f.db, nil)
				if err != nil {
					return err
				}
				defer transaction.Rollback()
				if _, err = transaction.ExecContext(ctx, "UPDATE b0_test_effects SET value=value+1 WHERE deployment=?", f.binding.Deployment); err != nil {
					return err
				}
				return transaction.Commit()
			})
			if ackLoss {
				expect(t, errors.Is(runErr, ErrUnknown) && result.Outcome != "COMPLETE", "lost ACK became known success")
			} else {
				must(t, runErr)
				expect(t, result.Outcome == "COMPLETE", "explicit same-ID stage did not complete")
			}
			expect(t, callbacks == 1 && f.value(t) == 1, "explicit stage failed to join exactly one product transaction")
			var attempts, receipts, ledgerAttempts int
			must(t, f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_lifecycle_attempts WHERE deployment=? AND attempt=?", f.binding.Deployment, id).Scan(&attempts))
			must(t, f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_lifecycle_receipts WHERE deployment=? AND attempt=?", f.binding.Deployment, id).Scan(&receipts))
			must(t, f.db.QueryRow("SELECT COUNT(*) FROM b0_test_ledger_attempts WHERE deployment=? AND attempt=?", f.binding.Deployment, id).Scan(&ledgerAttempts))
			expect(t, attempts == 1 && ledgerAttempts == 1, "original Attempt/Commit correlation lost")
			if ackLoss {
				expect(t, receipts == 0, "unknown Finalize invented saved CP receipt")
			} else {
				expect(t, receipts == 1, "completed stage lacks durable receipt")
			}
			var beforeHead, beforeLedger string
			must(t, f.db.QueryRow("SELECT body FROM v3_cp_lifecycle_heads WHERE deployment=?", f.binding.Deployment).Scan(&beforeHead))
			must(t, f.db.QueryRow("SELECT body FROM b0_test_ledger_heads WHERE deployment=?", f.binding.Deployment).Scan(&beforeLedger))
			inspected, err := gate.InspectExplicitCall(context.Background(), id, call)
			must(t, err)
			expect(t, inspected.Ledger.Reservation.Request.ID == id, "Inspect changed original Attempt")
			if ackLoss {
				expect(t, inspected.Ledger.Phase == Finalized && inspected.Quarantined, "UNKNOWN did not preserve finalization/quarantine")
			} else {
				expect(t, inspected.Ledger.Phase == Completed && inspected.Receipt != nil && !inspected.Quarantined, "pure Inspect lost complete receipt")
			}
			changed := call
			changed.Content = struct {
				RequestID            string
				ExpectedHoldRevision uint64
			}{id, 4}
			_, err = gate.InspectExplicitCall(context.Background(), id, changed)
			expect(t, err != nil, "same ID different immutable request inspected as original")
			_, err = gate.DoExplicit(context.Background(), id, call, func(context.Context) error { callbacks++; return nil })
			expect(t, err != nil && callbacks == 1 && f.value(t) == 1, "original Attempt replay executed callback")
			var afterHead, afterLedger string
			must(t, f.db.QueryRow("SELECT body FROM v3_cp_lifecycle_heads WHERE deployment=?", f.binding.Deployment).Scan(&afterHead))
			must(t, f.db.QueryRow("SELECT body FROM b0_test_ledger_heads WHERE deployment=?", f.binding.Deployment).Scan(&afterLedger))
			expect(t, beforeHead == afterHead && beforeLedger == afterLedger, "Inspect/replay changed durable head or released quarantine")
		})
	}
}
