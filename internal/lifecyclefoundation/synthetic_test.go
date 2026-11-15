package lifecyclefoundation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// This provider exists only in _test.go, on disposable SQL. It is not a product
// adapter, independent trust origin, physical storage qualification or Root.
type syntheticLedger struct {
	db *sql.DB
	fault Phase
	after bool
	onReserve func()
}

func disposableDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN")
	if raw == "" {
		raw = os.Getenv("AUTOSTREAM_S13_TEST_DSN")
	}
	if raw == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("required B0 SQL inventory has no disposable DSN")
		}
		t.Skip("Actions-only disposable SQL fixture not configured")
	}
	cfg, err := mysql.ParseDSN(raw)
	if err != nil || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Fatal("B0 SQL fixtures are Actions-only")
	}
	expectedPassword := map[string]string{"autostream_v3_auth_ci": "synthetic-ci-only", "autostream_s13_ci": "synthetic-s13-only"}
	if cfg.Net != "tcp" || cfg.Addr != "127.0.0.1:3306" || cfg.User != cfg.DBName ||
		expectedPassword[cfg.DBName] == "" || cfg.Passwd != expectedPassword[cfg.DBName] || !cfg.ParseTime ||
		cfg.MultiStatements || cfg.TLSConfig != "" {
		t.Fatal("B0 refuses non-disposable database configuration")
	}
	db, err := sql.Open("mysql", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(12)
	if err = db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func unique() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func initialCheckpoint() Checkpoint {
	return Checkpoint{OperationalRevision: 1, AuthorityEpoch: 1, IdentityRevision: 1, Content: "content-initial", HistoryHead: "history-initial"}
}

func fixtureBinding() Binding {
	return Binding{Deployment: "deployment-" + unique(), Ledger: "synthetic-ledger", Store: "synthetic-store", Incarnation: "cp-incarnation-1", Protocol: SyntheticProfile, Coverage: "synthetic-complete", RootReference: "synthetic-only"}
}

type fixture struct {
	db *sql.DB
	cp *SQLStore
	ledger *syntheticLedger
	auth *syntheticAuthorizer
	service *Service
	binding Binding
	before Checkpoint
}

type syntheticAuthorizer struct {
	db *sql.DB
	deployment string
	approval *Approval
	intentChange func(*Authorization)
}

func (a *syntheticAuthorizer) Resolve(_ context.Context, intent Intent) (Authorization, error) {
	if intent.Scope != (Scope{Type: "GLOBAL"}) || intent.Subject != "fixture-target" || intent.OperationContent != "synthetic-operation-content" ||
		(intent.Operation != "fixture.write" && intent.Operation != "fixture.read" && intent.Operation != "lifecycle.inspect") {
		return Authorization{}, ErrHeld
	}
	auth := Authorization{Actor: "synthetic-human", PrincipalKind: "HUMAN", Intent: intent, Approval: a.approval}
	if a.intentChange != nil {
		a.intentChange(&auth)
	}
	return auth, nil
}

func (a *syntheticAuthorizer) ValidateTx(ctx context.Context, tx *sql.Tx, _ Authorization, _ Checkpoint) error {
	var active int
	// A local, nonlocking read is sufficient for this immutable fixture policy.
	// No real User, Session, Role, credential or product owner table is accessed.
	if tx.QueryRowContext(ctx, "SELECT active FROM b0_test_authority WHERE deployment=?", a.deployment).Scan(&active) != nil || active != 1 {
		return ErrHeld
	}
	return nil
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := disposableDB(t)
	if err := ApplyMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS b0_test_ledger_heads (deployment VARCHAR(96) COLLATE utf8mb4_bin PRIMARY KEY,body LONGTEXT NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE IF NOT EXISTS b0_test_ledger_attempts (deployment VARCHAR(96) COLLATE utf8mb4_bin NOT NULL,attempt VARCHAR(96) COLLATE utf8mb4_bin NOT NULL,body LONGTEXT NOT NULL,PRIMARY KEY(deployment,attempt)) ENGINE=InnoDB",
		"CREATE TABLE IF NOT EXISTS b0_test_ledger_consumptions (deployment VARCHAR(96) COLLATE utf8mb4_bin NOT NULL,origin VARCHAR(96) COLLATE utf8mb4_bin NOT NULL,approval VARCHAR(96) COLLATE utf8mb4_bin NOT NULL,PRIMARY KEY(deployment,origin,approval)) ENGINE=InnoDB",
		"CREATE TABLE IF NOT EXISTS b0_test_ledger_history (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,deployment VARCHAR(96) COLLATE utf8mb4_bin NOT NULL,attempt VARCHAR(96) COLLATE utf8mb4_bin NOT NULL,phase VARCHAR(24) NOT NULL,body LONGTEXT NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE IF NOT EXISTS b0_test_effects (deployment VARCHAR(96) COLLATE utf8mb4_bin PRIMARY KEY,value BIGINT NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE IF NOT EXISTS b0_test_authority (deployment VARCHAR(96) COLLATE utf8mb4_bin PRIMARY KEY,active BOOLEAN NOT NULL) ENGINE=InnoDB",
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	binding := fixtureBinding()
	before := initialCheckpoint()
	frame := Frame{Binding: binding, Checkpoint: before, Phase: Completed}
	for _, entry := range []struct{ query string; args []any }{
		{"INSERT INTO v3_cp_lifecycle_heads VALUES(?,?)", []any{binding.Deployment, encode(frame)}},
		{"INSERT INTO b0_test_ledger_heads VALUES(?,?)", []any{binding.Deployment, encode(Head{Frame: frame, Phase: Completed})}},
		{"INSERT INTO b0_test_effects VALUES(?,0)", []any{binding.Deployment}},
		{"INSERT INTO b0_test_authority VALUES(?,1)", []any{binding.Deployment}},
	} {
		if _, err := db.Exec(entry.query, entry.args...); err != nil {
			t.Fatal(err)
		}
	}
	cp, err := NewSQLStore(db, binding)
	if err != nil {
		t.Fatal(err)
	}
	ledger := &syntheticLedger{db: db}
	auth := &syntheticAuthorizer{db: db, deployment: binding.Deployment}
	service, err := NewService(cp, ledger, auth)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{db: db, cp: cp, ledger: ledger, auth: auth, service: service, binding: binding, before: before}
}

func writeRequest() Request {
	return Request{ID: "attempt-" + unique(), Intent: Intent{Mode: Write, Purpose: "AUTHORITY_CHANGE", Operation: "fixture.write", OperationContent: "synthetic-operation-content", Scope: Scope{Type: "GLOBAL"}, Subject: "fixture-target"}}
}

func inspectIntent() Intent {
	return Intent{Mode: Read, Purpose: "CURRENT_READ", Operation: "lifecycle.inspect", OperationContent: "synthetic-operation-content", Scope: Scope{Type: "GLOBAL"}, Subject: "fixture-target"}
}

func (f *fixture) effect(ctx context.Context, tx *Transaction) (Checkpoint, error) {
	if _, err := tx.Exec(ctx, "UPDATE b0_test_effects SET value=value+1 WHERE deployment=?", f.binding.Deployment); err != nil {
		return Checkpoint{}, err
	}
	next := f.before
	next.AuthorityEpoch++
	next.Content = "content-" + unique()
	next.HistoryHead = "history-" + unique()
	return next, nil
}

func (f *fixture) value(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow("SELECT value FROM b0_test_effects WHERE deployment=?", f.binding.Deployment).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (l *syntheticLedger) load(ctx context.Context, q sqlQuery, b Binding, lock bool) (Head, error) {
	query := "SELECT body FROM b0_test_ledger_heads WHERE deployment=?"
	if lock {
		query += " FOR UPDATE"
	}
	var body string
	if q.QueryRowContext(ctx, query, b.Deployment).Scan(&body) != nil {
		return Head{}, ErrHeld
	}
	var head Head
	if decode(body, &head) != nil || head.Frame.Binding != b {
		return Head{}, ErrHeld
	}
	return head, nil
}

func (l *syntheticLedger) ObserveLatest(ctx context.Context, b Binding, challenge string) (Observation, error) {
	h, err := l.load(ctx, l.db, b, false)
	return Observation{Binding: b, Challenge: challenge, Head: h}, err
}

func (l *syntheticLedger) transition(ctx context.Context, b Binding, id string, phase Phase, change func(*sql.Tx, *Head) (Attempt, error)) (Attempt, error) {
	tx, err := l.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Attempt{}, err
	}
	defer tx.Rollback()
	head, err := l.load(ctx, tx, b, true)
	if err != nil {
		return Attempt{}, err
	}
	attempt, err := change(tx, &head)
	if err != nil {
		return Attempt{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE b0_test_ledger_heads SET body=? WHERE deployment=?", encode(head), b.Deployment); err != nil {
		return Attempt{}, err
	}
	if phase == Reserved {
		_, err = tx.ExecContext(ctx, "INSERT INTO b0_test_ledger_attempts VALUES(?,?,?)", b.Deployment, id, encode(attempt))
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE b0_test_ledger_attempts SET body=? WHERE deployment=? AND attempt=?", encode(attempt), b.Deployment, id)
	}
	if err != nil {
		return Attempt{}, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO b0_test_ledger_history(deployment,attempt,phase,body) VALUES(?,?,?,?)", b.Deployment, id, phase, encode(attempt)); err != nil {
		return Attempt{}, err
	}
	if l.fault == phase && !l.after {
		if err = tx.Rollback(); err != nil {
			return Attempt{}, err
		}
		return Attempt{}, ErrUnknown
	}
	if err = tx.Commit(); err != nil {
		return Attempt{}, ErrUnknown
	}
	if l.fault == phase {
		return Attempt{}, ErrUnknown
	}
	return attempt, nil
}

func (l *syntheticLedger) Reserve(ctx context.Context, r Reservation) (Reservation, error) {
	if l.onReserve != nil {
		l.onReserve()
	}
	a, err := l.transition(ctx, r.Binding, r.Request.ID, Reserved, func(tx *sql.Tx, h *Head) (Attempt, error) {
		if h.Slot != nil || h.Phase != Completed || !equivalent(h.Frame, r.Before) || r.Sequence != r.Before.Sequence+1 {
			return Attempt{}, ErrConflict
		}
		if approval := r.Authorization.Approval; approval != nil {
			if _, err := tx.ExecContext(ctx, "INSERT INTO b0_test_ledger_consumptions VALUES(?,?,?)", r.Binding.Deployment, approval.Origin, approval.ID); err != nil {
				return Attempt{}, ErrConflict
			}
		}
		h.Slot = &r
		h.Phase = Reserved
		h.Frame.Sequence = r.Sequence
		h.Frame.Attempt = r.Request.ID
		h.Frame.Phase = Reserved
		return Attempt{Reservation: r, Phase: Reserved}, nil
	})
	return a.Reservation, err
}

func (l *syntheticLedger) Finalize(ctx context.Context, c CommitRecord) (Receipt, error) {
	r := c.Reservation
	a, err := l.transition(ctx, r.Binding, r.Request.ID, Finalized, func(_ *sql.Tx, h *Head) (Attempt, error) {
		if h.Phase != Reserved || h.Slot == nil || !same(*h.Slot, r) || !nextCheckpoint(r.Before.Checkpoint, c.After, r.Request.Intent) {
			return Attempt{}, ErrHeld
		}
		receipt := Receipt{Commit: c, LedgerSequence: r.Sequence}
		h.Frame.Checkpoint = c.After
		h.Phase = Finalized
		h.Frame.Phase = Finalized
		return Attempt{Reservation: r, Phase: Finalized, Commit: &c, Receipt: &receipt}, nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return *a.Receipt, nil
}

func (l *syntheticLedger) Complete(ctx context.Context, saved SavedReceipt) error {
	if saved.Validate() != nil {
		return ErrHeld
	}
	receipt := saved.Receipt
	r := receipt.Commit.Reservation
	_, err := l.transition(ctx, r.Binding, r.Request.ID, Completed, func(_ *sql.Tx, h *Head) (Attempt, error) {
		if h.Phase != Finalized || h.Slot == nil || !same(*h.Slot, r) ||
			h.Frame.Checkpoint != receipt.Commit.After || receipt.LedgerSequence != r.Sequence {
			return Attempt{}, ErrHeld
		}
		h.Slot = nil
		h.Phase = Completed
		h.Frame.Phase = Completed
		c := receipt.Commit
		return Attempt{Reservation: r, Phase: Completed, Commit: &c, Receipt: &receipt}, nil
	})
	return err
}

func (l *syntheticLedger) InspectAttempt(ctx context.Context, b Binding, id string) (Attempt, error) {
	var body string
	if err := l.db.QueryRowContext(ctx, "SELECT body FROM b0_test_ledger_attempts WHERE deployment=? AND attempt=?", b.Deployment, id).Scan(&body); err != nil {
		return Attempt{}, ErrHeld
	}
	var a Attempt
	if decode(body, &a) != nil {
		return Attempt{}, ErrHeld
	}
	return a, nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expect(t *testing.T, ok bool, message string) {
	t.Helper()
	if !ok {
		t.Fatal(message)
	}
}

func isHeld(err error) bool {
	return errors.Is(err, ErrHeld) || errors.Is(err, ErrConflict)
}

func safeName(s string) string { return strings.ReplaceAll(s, "/", "-") }
