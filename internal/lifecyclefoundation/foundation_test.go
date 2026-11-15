package lifecyclefoundation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

func TestLifecycleContractBoundary(t *testing.T) {
	b := fixtureBinding()
	must(t, b.Validate())
	b.Protocol = "UNKNOWN"
	expect(t, errors.Is(b.Validate(), ErrHeld), "unknown profile accepted")
	b = fixtureBinding()
	b.RootReference = ""
	expect(t, b.Validate() != nil, "missing Root reference accepted")
	for _, bad := range []Intent{
		{Mode: Write, Purpose: "UNKNOWN", Operation: "x", Scope: Scope{Type: "GLOBAL"}, Subject: "s"},
		{Mode: Read, Purpose: "AUTHORITY_CHANGE", Operation: "x", Scope: Scope{Type: "GLOBAL"}, Subject: "s"},
		{Mode: Write, Purpose: "AUTHORITY_CHANGE", Operation: "x", Scope: Scope{Type: "HOST"}, Subject: "s"},
		{Mode: Write, Purpose: "AUTHORITY_CHANGE", Operation: "x", Scope: Scope{Type: "GLOBAL", ID: "widened"}, Subject: "s"},
	} {
		expect(t, bad.Validate() != nil, "bad intent accepted")
	}
	var request Request
	expect(t, decode("{\"ID\":\"a\",\"ID\":\"b\",\"Intent\":{}}", &request) != nil, "duplicate field accepted")
	expect(t, decode("{\"ID\":\"a\",\"Actor\":\"forged\"}", &request) != nil, "body Actor accepted")
	_, hasActor := reflect.TypeOf(Request{}).FieldByName("Actor")
	expect(t, !hasActor, "caller Request contains Actor")
	_, err := NewService(nil, nil, nil)
	expect(t, errors.Is(err, ErrHeld), "unconfigured source allowed")
	expect(t, (SavedReceipt{}).Validate() != nil, "unconfirmed receipt allowed")
	var typedNil *syntheticLedger
	store, err := NewSQLStore(new(sql.DB), fixtureBinding())
	must(t, err)
	_, err = NewService(store, typedNil, &syntheticAuthorizer{})
	expect(t, errors.Is(err, ErrHeld), "typed nil provider allowed")
	expect(t, RequireParticipation(context.Background(), nil, Request{}) != nil, "forged participation allowed")
}

func TestLifecycleWriteRead(t *testing.T) {
	f := newFixture(t)
	r := writeRequest()
	got, err := f.service.Run(context.Background(), r, f.effect)
	must(t, err)
	expect(t, got.Outcome == "COMPLETE" && f.value(t) == 1, "W did not commit exactly once")
	inspection, err := f.service.Inspect(context.Background(), inspectIntent(), r.ID)
	must(t, err)
	expect(t, inspection.Ledger.Phase == Completed && inspection.Receipt != nil && !inspection.Quarantined, "W stage mismatch")
	_, err = f.service.Run(context.Background(), r, f.effect)
	expect(t, isHeld(err) && f.value(t) == 1, "same Attempt replayed")
	current, err := f.cp.current(context.Background())
	must(t, err)
	read := Request{ID: "read-" + unique(), Intent: Intent{Mode: Read, Purpose: "CURRENT_READ", Operation: "fixture.read", OperationContent: "synthetic-operation-content", Scope: Scope{Type: "GLOBAL"}, Subject: "fixture-target"}}
	result, err := f.service.Run(context.Background(), read, func(ctx context.Context, tx *Transaction) (Checkpoint, error) {
		_, denied := tx.Exec(ctx, "UPDATE b0_test_effects SET value=value+100 WHERE deployment=?", f.binding.Deployment)
		expect(t, errors.Is(denied, ErrHeld), "R business write allowed")
		return current.Checkpoint, nil
	})
	must(t, err)
	after, err := f.cp.current(context.Background())
	must(t, err)
	expect(t, result.Outcome == "COMPLETE" && after.Checkpoint == current.Checkpoint && after.Sequence == current.Sequence+1 && f.value(t) == 1, "R advanced authority or replayed effect")
}

func TestLifecycleAuthorizationBoundary(t *testing.T) {
	for _, scenario := range []string{"operation", "content", "scope", "subject", "workload", "actor", "source-revoked"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			r := writeRequest()
			switch scenario {
			case "operation":
				r.Intent.Operation = "roles.escalate"
			case "content":
				r.Intent.OperationContent = "different-operation-content"
			case "scope":
				r.Intent.Scope = Scope{Type: "HOST", ID: "other-host"}
			case "subject":
				r.Intent.Subject = "another-subject"
			case "workload":
				f.auth.intentChange = func(a *Authorization) { a.PrincipalKind = "WORKLOAD" }
			case "actor":
				f.auth.intentChange = func(a *Authorization) { a.Actor = "" }
			case "source-revoked":
				f.ledger.onReserve = func() {
					_, err := f.db.Exec("UPDATE b0_test_authority SET active=0 WHERE deployment=?", f.binding.Deployment)
					must(t, err)
				}
			}
			out, err := f.service.Run(context.Background(), r, f.effect)
			expect(t, err != nil && out.Outcome != "COMPLETE" && f.value(t) == 0, "source boundary widened authority")
		})
	}
}

func reserveFixture(t *testing.T, f *fixture, req Request) Reservation {
	t.Helper()
	frame, err := f.cp.current(context.Background())
	must(t, err)
	auth, err := f.auth.Resolve(context.Background(), req.Intent)
	must(t, err)
	r, err := f.ledger.Reserve(context.Background(), Reservation{Binding: f.binding, Before: frame, Request: req, Authorization: auth, Sequence: frame.Sequence+1})
	must(t, err)
	return r
}

func TestLifecycleStageOrderAndPrivateParticipation(t *testing.T) {
	f := newFixture(t)
	r := reserveFixture(t, f, writeRequest())
	expect(t, f.ledger.Complete(context.Background(), SavedReceipt{}) != nil, "Complete skipped CP receipt")
	var savedContext context.Context
	var savedTransaction *Transaction
	record, err := f.cp.apply(context.Background(), r, f.auth, func(ctx context.Context, tx *Transaction) (Checkpoint, error) {
		must(t, RequireParticipation(ctx, f.cp, r.Request))
		other, openErr := NewSQLStore(f.db, f.binding)
		must(t, openErr)
		expect(t, RequireParticipation(ctx, other, r.Request) != nil, "different owner reused participant")
		wrong := r.Request
		wrong.ID = "wrong-attempt"
		expect(t, RequireParticipation(ctx, f.cp, wrong) != nil, "different request reused participant")
		_, e := tx.Exec(context.Background(), "UPDATE b0_test_effects SET value=50")
		expect(t, e != nil, "unbound context accepted")
		savedContext, savedTransaction = ctx, tx
		return f.effect(ctx, tx)
	})
	must(t, err)
	expect(t, RequireParticipation(savedContext, f.cp, r.Request) != nil, "participant survived callback")
	_, err = savedTransaction.Exec(savedContext, "UPDATE b0_test_effects SET value=100")
	expect(t, err != nil && f.value(t) == 1, "expired transaction participant wrote")
	receipt, err := f.ledger.Finalize(context.Background(), record)
	must(t, err)
	head, err := f.ledger.ObserveLatest(context.Background(), f.binding, "probe")
	must(t, err)
	expect(t, head.Head.Slot != nil && head.Head.Phase == Finalized, "Finalize released slot")
	expect(t, f.ledger.Complete(context.Background(), SavedReceipt{Receipt: receipt}) != nil, "encoded receipt forged CP confirmation")
	saved, err := f.cp.saveReceipt(context.Background(), receipt)
	must(t, err)
	corrupt := saved
	corrupt.Receipt.LedgerSequence++
	expect(t, corrupt.Validate() != nil, "receipt confirmation accepted altered result")
	must(t, f.ledger.Complete(context.Background(), saved))
	_, staleErr := f.cp.apply(context.Background(), r, f.auth, f.effect)
	expect(t, staleErr != nil && f.value(t) == 1, "old Admission wrote after completion")
	expect(t, f.ledger.Complete(context.Background(), saved) != nil && f.value(t) == 1, "Complete replay accepted")
}

func TestLifecycleAtomicRollback(t *testing.T) {
	f := newFixture(t)
	f.auth.approval = &Approval{Origin: "independent-fixture", ID: "approval-one", Manifest: "manifest-one", Purpose: "AUTHORITY_CHANGE"}
	r := writeRequest()
	_, err := f.service.Run(context.Background(), r, func(ctx context.Context, tx *Transaction) (Checkpoint, error) {
		_, e := f.effect(ctx, tx)
		must(t, e)
		return Checkpoint{}, errors.New("synthetic callback failure")
	})
	expect(t, errors.Is(err, ErrUnknown) && f.value(t) == 0, "partial effect committed")
	for _, table := range []string{"v3_cp_lifecycle_attempts", "v3_cp_lifecycle_consumptions", "v3_cp_lifecycle_history"} {
		var count int
		must(t, f.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE deployment=?", f.binding.Deployment).Scan(&count))
		expect(t, count == 0, "CP transaction partially persisted: "+table)
	}
	var consumed int
	must(t, f.db.QueryRow("SELECT COUNT(*) FROM b0_test_ledger_consumptions WHERE deployment=?", f.binding.Deployment).Scan(&consumed))
	expect(t, consumed == 1, "external consumption silently reused after CP rollback")
}

func TestLifecycleApprovalConsumption(t *testing.T) {
	f := newFixture(t)
	f.auth.approval = &Approval{Origin: "independent-fixture", ID: "approval-one", Manifest: "manifest-one", Purpose: "AUTHORITY_CHANGE"}
	r := writeRequest()
	_, err := f.service.Run(context.Background(), r, f.effect)
	must(t, err)
	current, err := f.cp.current(context.Background())
	must(t, err)
	f.before = current.Checkpoint
	next := writeRequest()
	got, err := f.service.Run(context.Background(), next, f.effect)
	expect(t, err != nil && got.Outcome != "COMPLETE" && f.value(t) == 1, "Approval reused with another Attempt")
	f.auth.approval.Manifest = "different-manifest"
	_, err = f.service.Run(context.Background(), writeRequest(), f.effect)
	expect(t, err != nil && f.value(t) == 1, "Approval reused with different manifest")
}

func TestLifecycleLostAcknowledgements(t *testing.T) {
	for _, phase := range []Phase{Reserved, CPCommitted, Finalized, ReceiptSaved, Completed} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-after-%v", phase, after), func(t *testing.T) {
				f := newFixture(t)
				r := writeRequest()
				if phase == CPCommitted || phase == ReceiptSaved {
					calls := 0
					f.cp.commit = func(tx *sql.Tx) error {
						calls++
						target := 1
						if phase == ReceiptSaved {
							target = 2
						}
						if calls != target {
							return tx.Commit()
						}
						if after {
							must(t, tx.Commit())
						} else {
							must(t, tx.Rollback())
						}
						return ErrUnknown
					}
				} else {
					f.ledger.fault, f.ledger.after = phase, after
				}
				got, err := f.service.Run(context.Background(), r, f.effect)
				expect(t, errors.Is(err, ErrUnknown) && got.Outcome == "UNKNOWN", "lost ACK became success")
				expected := 1
				if phase == Reserved || phase == CPCommitted && !after {
					expected = 0
				}
				expect(t, f.value(t) == expected, "incorrect native commit/rollback result")
				cp, openErr := NewSQLStore(f.db, f.binding)
				must(t, openErr)
				service, openErr := NewService(cp, &syntheticLedger{db: f.db}, f.auth)
				must(t, openErr)
				fired := false
				_, err = service.Run(context.Background(), writeRequest(), func(context.Context, *Transaction) (Checkpoint, error) {
					fired = true
					return f.before, nil
				})
				expect(t, err != nil && !fired && f.value(t) == expected, "Restart automatically replayed an uncertain operation")
				var beforeCount, afterCount int
				must(t, f.db.QueryRow("SELECT COUNT(*) FROM b0_test_ledger_history WHERE deployment=?", f.binding.Deployment).Scan(&beforeCount))
				observation, inspectionErr := service.Inspect(context.Background(), inspectIntent(), r.ID)
				if inspectionErr == nil {
					expect(t, observation.Quarantined, "unknown not durably quarantined")
				}
				must(t, f.db.QueryRow("SELECT COUNT(*) FROM b0_test_ledger_history WHERE deployment=?", f.binding.Deployment).Scan(&afterCount))
				expect(t, beforeCount == afterCount && f.value(t) == expected, "Inspect changed outcome or released reservation")
			})
		}
	}
}

func TestLifecycleDisconnectAndCancellation(t *testing.T) {
	t.Run("actual-sql-disconnect", func(t *testing.T) {
		f := newFixture(t)
		commits := 0
		f.cp.commit = func(tx *sql.Tx) error {
			commits++
			var id uint64
			must(t, tx.QueryRow("SELECT CONNECTION_ID()").Scan(&id))
			_, err := f.db.Exec("KILL CONNECTION " + strconv.FormatUint(id, 10))
			must(t, err)
			return tx.Commit()
		}
		result, err := f.service.Run(context.Background(), writeRequest(), f.effect)
		expect(t, errors.Is(err, ErrUnknown) && result.Outcome == "UNKNOWN" && commits == 1 && f.value(t) == 0, "disconnected commit retried or succeeded")
	})
	t.Run("cancelled-reserve", func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.ledger.onReserve = cancel
		result, err := f.service.Run(ctx, writeRequest(), f.effect)
		expect(t, errors.Is(err, ErrUnknown) && result.Outcome == "UNKNOWN" && f.value(t) == 0, "cancelled request became success")
		_, err = f.cp.current(context.Background())
		expect(t, err != nil, "cancelled uncertainty disappeared")
	})
}

type stalePort struct {
	Port
	change func(*Observation)
}

func (p stalePort) ObserveLatest(ctx context.Context, b Binding, challenge string) (Observation, error) {
	o, err := p.Port.ObserveLatest(ctx, b, challenge)
	p.change(&o)
	return o, err
}

func TestLifecycleStaleAndPartialRestore(t *testing.T) {
	for _, scenario := range []string{"challenge", "store", "epoch", "content", "unknown-phase", "cp-only", "ledger-only", "all-comparison-roots"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			var cpOld, ledgerOld string
			must(t, f.db.QueryRow("SELECT body FROM v3_cp_lifecycle_heads WHERE deployment=?", f.binding.Deployment).Scan(&cpOld))
			must(t, f.db.QueryRow("SELECT body FROM b0_test_ledger_heads WHERE deployment=?", f.binding.Deployment).Scan(&ledgerOld))
			if scenario == "cp-only" || scenario == "ledger-only" || scenario == "all-comparison-roots" {
				_, err := f.service.Run(context.Background(), writeRequest(), f.effect)
				must(t, err)
				if scenario != "ledger-only" {
					_, err = f.db.Exec("UPDATE v3_cp_lifecycle_heads SET body=? WHERE deployment=?", cpOld, f.binding.Deployment)
					must(t, err)
				}
				if scenario != "cp-only" {
					_, err = f.db.Exec("UPDATE b0_test_ledger_heads SET body=? WHERE deployment=?", ledgerOld, f.binding.Deployment)
					must(t, err)
				}
			} else {
				f.service.ledger = stalePort{Port: f.ledger, change: func(o *Observation) {
					switch scenario {
					case "challenge":
						o.Challenge = "old-response"
					case "store":
						o.Binding.Store = "different-store"
					case "epoch":
						o.Head.Frame.Checkpoint.AuthorityEpoch++
					case "unknown-phase":
						o.Head.Frame.Phase = "UNKNOWN"
					case "content":
						o.Head.Frame.Checkpoint.Content = "same-generation-different-content"
					}
				}}
			}
			if scenario == "all-comparison-roots" {
				// Restore every mutable B0 fixture root to the captured empty
				// deployment, including dedupe/consumption/history, not just heads.
				// TRUNCATE is a disposable DBA restore fixture; no product table
				// or production recovery capability is involved.
				for _, table := range []string{
					"v3_cp_lifecycle_heads", "v3_cp_lifecycle_attempts",
					"v3_cp_lifecycle_consumptions", "v3_cp_lifecycle_receipts",
					"v3_cp_lifecycle_history", "v3_cp_lifecycle_unknowns",
					"b0_test_ledger_heads", "b0_test_ledger_attempts",
					"b0_test_ledger_consumptions", "b0_test_ledger_history",
				} {
					_, restoreErr := f.db.Exec("TRUNCATE TABLE " + table)
					must(t, restoreErr)
				}
				_, err := f.db.Exec("INSERT INTO v3_cp_lifecycle_heads VALUES(?,?)", f.binding.Deployment, cpOld)
				must(t, err)
				_, err = f.db.Exec("INSERT INTO b0_test_ledger_heads VALUES(?,?)", f.binding.Deployment, ledgerOld)
				must(t, err)
				_, err = f.db.Exec("UPDATE b0_test_effects SET value=0 WHERE deployment=?", f.binding.Deployment)
				must(t, err)
				_, err = f.db.Exec("UPDATE b0_test_authority SET active=1 WHERE deployment=?", f.binding.Deployment)
				must(t, err)
				result, err := f.service.Run(context.Background(), writeRequest(), f.effect)
				must(t, err)
				expect(t, result.Outcome == "COMPLETE" && f.value(t) == 1, "common-restore limitation was hidden")
				return
			}
			invoked := false
			result, err := f.service.Run(context.Background(), writeRequest(), func(context.Context, *Transaction) (Checkpoint, error) {
				invoked = true
				return f.before, nil
			})
			expect(t, err != nil && result.Outcome != "COMPLETE" && !invoked, "stale or partially restored state accepted")
		})
	}
}
