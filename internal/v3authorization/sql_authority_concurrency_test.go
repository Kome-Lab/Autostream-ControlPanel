package v3authorization

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func authorityPending(t *testing.T, f *authorityFixture) Record {
	t.Helper()
	ctx := context.Background()
	decision, err := f.authority.Read(ctx, f.principal, f.intent)
	if err != nil {
		t.Fatal(err)
	}
	r := Record{Principal: f.principal.ID, Intent: f.intent, Frame: f.request.Frame, Issuer: f.service.config.Issuer, Audience: f.service.config.Audience, State: "PENDING", Decision: decision, ClaimID: "synthetic-authority-claim"}
	r.Immutable, err = immutable(r)
	if err != nil {
		t.Fatal(err)
	}
	result, claim, err := NewSQLStore(f.db).Claim(ctx, r)
	if err != nil || claim != Claimed {
		t.Fatal("persist pending liability", err)
	}
	return result
}
func authorityAssertRowLocked(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value any
	err = tx.QueryRowContext(ctx, query+" FOR UPDATE NOWAIT", args...).Scan(&value)
	var native *mysql.MySQLError
	if !errors.As(err, &native) || (native.Number != 1205 && native.Number != 3572) {
		t.Fatal("observer did not prove the actual InnoDB row lock", err)
	}
}
func TestSQLAuthorityCallbackAtomicSaveAndContextLifetime(t *testing.T) {
	f := newAuthorityFixture(t)
	pending := authorityPending(t, f)
	issued := sqlTestIssued(t, pending)
	store := NewSQLStore(f.db)
	calls := 0
	var retained context.Context
	err := f.authority.WithCurrent(context.Background(), f.principal, f.intent, pending.Decision, func(ctx context.Context, d Decision) error {
		calls++
		retained = ctx
		if d != pending.Decision {
			t.Fatal("changed callback decision")
		}
		if err := store.Finish(ctx, issued); err != nil {
			return err
		}
		// A separate connection must still observe the committed PENDING row.
		outside, err := store.Lookup(context.Background(), f.lookup())
		if err != nil || outside.State != "PENDING" || len(outside.Envelope) != 0 {
			t.Fatal("save escaped authority transaction", err)
		}
		if err := store.Finish(ctx, issued); !errors.Is(err, ErrConflict) {
			t.Fatal("transaction accepted repeated save", err)
		}
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatal("one-shot callback", calls, err)
	}
	read, err := store.Lookup(context.Background(), f.lookup())
	if err != nil || read.State != "ISSUED" || !bytes.Equal(read.Envelope, issued.Envelope) {
		t.Fatal("atomic commit lost issued bytes", err)
	}
	if err := store.Finish(retained, issued); !errors.Is(err, ErrConflict) {
		t.Fatal("retained callback context remained writable", err)
	}
}
func TestSQLAuthorityCallbackRollbackAndStoreBinding(t *testing.T) {
	for _, name := range []string{"callback-error", "other-pool", "other-intent"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			pending := authorityPending(t, f)
			issued := sqlTestIssued(t, pending)
			store := NewSQLStore(f.db)
			if name == "other-pool" {
				other, err := sql.Open("mysql", os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN"))
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				store = NewSQLStore(other)
			}
			if name == "other-intent" {
				issued.Intent.OriginalBudget = "different-budget"
			}
			err := f.authority.WithCurrent(context.Background(), f.principal, f.intent, pending.Decision, func(ctx context.Context, _ Decision) error {
				err := store.Finish(ctx, issued)
				if name == "callback-error" {
					if err != nil {
						t.Fatal(err)
					}
					return ErrDenied
				}
				return err
			})
			if err == nil {
				t.Fatal("invalid boundary saved evidence")
			}
			read, err := NewSQLStore(f.db).Lookup(context.Background(), f.lookup())
			if err != nil || read.State != "PENDING" || len(read.Envelope) != 0 {
				t.Fatal("rollback changed liability", err)
			}
		})
	}
}
func TestSQLAuthorityCallbackSerializesMutation(t *testing.T) {
	f := newAuthorityFixture(t)
	pending := authorityPending(t, f)
	issued := sqlTestIssued(t, pending)
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		result <- f.authority.WithCurrent(ctx, f.principal, f.intent, pending.Decision, func(txctx context.Context, _ Decision) error {
			if err := NewSQLStore(f.db).Finish(txctx, issued); err != nil {
				return err
			}
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case err := <-result:
		t.Fatal("callback not reached", err)
	case <-ctx.Done():
		t.Fatal("callback timeout")
	}
	// Deterministic barrier is INSIDE the real transaction. NOWAIT probes prove
	// actual locks, rather than inferring serialization from goroutine start.
	authorityAssertRowLocked(t, f.db, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1")
	authorityAssertRowLocked(t, f.db, "SELECT id FROM users WHERE id=?", f.principal.ID)
	authorityAssertRowLocked(t, f.db, "SELECT generation FROM v3_cp_authority_records WHERE kind='host' AND realm=? AND first_id=? AND second_id=''", f.intent.Scope.Realm, f.intent.Scope.Host)
	capRecord := f.records[5]
	copyCap := *capRecord.Capability
	copyCap.Issue = false
	capRecord.Capability = &copyCap
	changed := make(chan error, 1)
	go func() { _, err := f.authority.Save(ctx, 1, []AuthorityRecord{capRecord}); changed <- err }()
	select {
	case err := <-changed:
		t.Fatal("owner mutation crossed callback lock", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal("conditional save failed", err)
	}
	if err := <-changed; err != nil {
		t.Fatal("serialized mutation failed", err)
	}
	read, err := NewSQLStore(f.db).Lookup(ctx, f.lookup())
	if err != nil || read.State != "ISSUED" || !bytes.Equal(read.Envelope, issued.Envelope) {
		t.Fatal("committed bytes changed", err)
	}
	d, err := f.authority.Read(ctx, f.principal, f.intent)
	requireRefusal(t, d, err)
}
func TestSQLAuthorityExistingUserDisableSerializes(t *testing.T) {
	f := newAuthorityFixture(t)
	d, err := f.authority.Read(context.Background(), f.principal, f.intent)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		done <- f.authority.WithCurrent(ctx, f.principal, f.intent, d, func(context.Context, Decision) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("callback timeout")
	}
	authorityAssertRowLocked(t, f.db, "SELECT id FROM users WHERE id=?", f.principal.ID)
	disabled := make(chan error, 1)
	go func() {
		_, err := f.db.ExecContext(ctx, "UPDATE users SET status='disabled' WHERE id=?", f.principal.ID)
		disabled <- err
	}()
	select {
	case err := <-disabled:
		t.Fatal("existing user writer crossed active-principal lock", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	decision, err := f.authority.Read(ctx, f.principal, f.intent)
	requireRefusal(t, decision, err)
}
func TestSQLAuthorityCancellationPreservesPending(t *testing.T) {
	for _, name := range []string{"before-callback", "inside-callback", "after-save"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			pending := authorityPending(t, f)
			issued := sqlTestIssued(t, pending)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			if name == "before-callback" {
				cancel()
			}
			err := f.authority.WithCurrent(ctx, f.principal, f.intent, pending.Decision, func(txctx context.Context, _ Decision) error {
				calls++
				if name == "after-save" {
					if err := NewSQLStore(f.db).Finish(txctx, issued); err != nil {
						return err
					}
				}
				cancel()
				return nil
			})
			if err == nil || (name == "before-callback" && calls != 0) {
				t.Fatal("cancellation became success")
			}
			current, err := NewSQLStore(f.db).Lookup(context.Background(), f.lookup())
			if err != nil || current.State != "PENDING" || len(current.Envelope) != 0 {
				t.Fatal("cancellation lost pending", err)
			}
		})
	}
}

// A test-only driver wraps the REAL MariaDB connector. It can lose only the next
// COMMIT acknowledgement, either after a real commit or after a real rollback.
// It is not used by product constructors, and cannot manufacture successful keys,
// qualified clocks or a source snapshot.
type authorityFaultConnector struct {
	base  driver.Connector
	fault *atomic.Int32
}

func (c authorityFaultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &authorityFaultConn{Conn: conn, fault: c.fault}, nil
}
func (c authorityFaultConnector) Driver() driver.Driver { return c.base.Driver() }

type authorityFaultConn struct {
	driver.Conn
	fault *atomic.Int32
}

func (c *authorityFaultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return authorityFaultTx{tx, c.fault}, nil
}
func (c *authorityFaultConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *authorityFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c *authorityFaultConn) CheckNamedValue(v *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

type authorityFaultTx struct {
	driver.Tx
	fault *atomic.Int32
}

func (t authorityFaultTx) Commit() error {
	switch t.fault.Swap(0) {
	case 1:
		if err := t.Tx.Commit(); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	case 2:
		if err := t.Tx.Rollback(); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	default:
		return t.Tx.Commit()
	}
}
func authorityConnector(t *testing.T) driver.Connector {
	t.Helper()
	config, err := mysql.ParseDSN(os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN"))
	if err != nil || config.DBName != "autostream_v3_auth_ci" {
		t.Fatal("disposable authority connector only")
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		t.Fatal("synthetic connector unavailable")
	}
	return connector
}
func TestSQLAuthorityCommitUnknownKeepsUnknown(t *testing.T) {
	for _, mode := range []int32{1, 2} {
		name := "committed-ack-lost"
		if mode == 2 {
			name = "rolled-back-ack-lost"
		}
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			fault := &atomic.Int32{}
			db := sql.OpenDB(authorityFaultConnector{authorityConnector(t), fault})
			defer db.Close()
			f.deps.Authority = NewSQLAuthority(db)
			f.deps.Store = NewSQLStore(db)
			f.service = NewService(f.service.config, f.deps)
			f.security.onSeal = func() { fault.Store(mode) }
			result := f.issue()
			if result.Status != Unknown || len(result.Envelope) != 0 || result.AuthorizesExecution {
				t.Fatal("uncertain commit became success", result)
			}
			stored, err := NewSQLStore(f.db).Lookup(context.Background(), f.lookup())
			if err != nil {
				t.Fatal(err)
			}
			want := "ISSUED"
			if mode == 2 {
				want = "PENDING"
			}
			if stored.State != want {
				t.Fatal("test failed to exercise expected physical outcome", stored.State)
			}
			replay := f.issue()
			if replay.Status == Issued || replay.AuthorizesExecution || f.security.seals != 1 {
				t.Fatal("uncertain issuance reused old authority", replay)
			}
			if mode == 2 && (replay.Status != Pending || len(replay.Envelope) != 0) {
				t.Fatal("uncertain pending repaired or replaced", replay)
			}
		})
	}
}
func TestSQLAuthorityLockTimeoutAndCancelledWait(t *testing.T) {
	for _, name := range []string{"lock-timeout", "context-timeout"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			d, err := f.authority.Read(context.Background(), f.principal, f.intent)
			if err != nil {
				t.Fatal(err)
			}
			blocker, err := f.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			var generation uint64
			if blocker.QueryRow("SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE").Scan(&generation) != nil {
				t.Fatal("lock fixture")
			}
			config, err := mysql.ParseDSN(os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			if config.Params == nil {
				config.Params = map[string]string{}
			}
			config.Params["innodb_lock_wait_timeout"] = "1"
			connector, err := mysql.NewConnector(config)
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			a := NewSQLAuthority(db)
			if observed, err := a.Read(context.Background(), f.principal, f.intent); err != nil || observed != d {
				t.Fatal("timeout fixture cannot read actual authority", err)
			}
			duration := 5 * time.Second
			if name == "context-timeout" {
				duration = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			calls := 0
			started := time.Now()
			err = a.WithCurrent(ctx, f.principal, f.intent, d, func(context.Context, Decision) error { calls++; return nil })
			if name == "lock-timeout" && (time.Since(started) < 900*time.Millisecond || ctx.Err() != nil) {
				t.Fatal("native lock timeout was not exercised")
			}
			if name == "context-timeout" && ctx.Err() == nil {
				t.Fatal("context wait was not cancelled")
			}
			if err == nil || calls != 0 {
				t.Fatal("lock/cancel failure reached callback", err, calls)
			}
		})
	}
}
func TestSQLAuthorityNativeDeadlockFailsClosed(t *testing.T) {
	f := newAuthorityFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := f.authority.Read(ctx, f.principal, f.intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(ctx, "CREATE TABLE v3_authority_deadlock_weight(id INT PRIMARY KEY,n INT) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 32; n++ {
		if _, err = f.db.ExecContext(ctx, "INSERT INTO v3_authority_deadlock_weight VALUES(?,0)", n); err != nil {
			t.Fatal(err)
		}
	}
	blocker, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.ExecContext(ctx, "UPDATE v3_authority_deadlock_weight SET n=n+1"); err != nil {
		t.Fatal(err)
	}
	var id string
	if blocker.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", f.principal.ID).Scan(&id) != nil {
		t.Fatal("lock synthetic user")
	}
	calls := &atomic.Int32{}
	result := make(chan error, 1)
	go func() {
		result <- f.authority.WithCurrent(ctx, f.principal, f.intent, d, func(context.Context, Decision) error { calls.Add(1); return nil })
	}()
	// Wait for the ACTUAL epoch row lock, before making the deliberate inverse
	// edge. The heavy fixture transaction makes the read-only Authority victim.
	locked := false
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		observer, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var generation uint64
		err = observer.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE NOWAIT").Scan(&generation)
		observer.Rollback()
		var native *mysql.MySQLError
		if errors.As(err, &native) && native.Number == 1205 {
			locked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !locked {
		t.Fatal("authority did not acquire real first lock")
	}
	var generation uint64
	if err = blocker.QueryRowContext(ctx, "SELECT generation FROM v3_cp_authority_epoch WHERE singleton=1 FOR UPDATE").Scan(&generation); err != nil {
		t.Fatal("wrong deadlock victim", err)
	}
	select {
	case err = <-result:
		if err == nil || calls.Load() != 0 {
			t.Fatal("native deadlock became successful callback", err)
		}
	case <-ctx.Done():
		t.Fatal("native deadlock was not resolved")
	}
}
func TestSQLAuthoritySchemaAndMutationPreservation(t *testing.T) {
	f := newAuthorityFixture(t)
	ctx := context.Background()
	var before int
	if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authority_history").Scan(&before) != nil || before != 9 {
		t.Fatal("initial history")
	}
	if _, err := f.authority.Save(ctx, 0, f.records); !errors.Is(err, ErrConflict) {
		t.Fatal("stale owner CAS accepted", err)
	}
	if _, err := f.authority.Save(ctx, 1, []AuthorityRecord{{Kind: PolicyRecord}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing revision accepted", err)
	}
	f.mutate(t, PolicyRecord, func(r *AuthorityRecord) { r.Rule.Revocation = "REVOKED" })
	var after int
	if f.db.QueryRow("SELECT COUNT(*) FROM v3_cp_authority_history").Scan(&after) != nil || after != before+1 {
		t.Fatal("owner history was lost")
	}
	if err := ApplyAuthorityMigrations(ctx, f.db); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	for _, name := range []string{"checksum", "dirty", "engine"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			statement := "UPDATE v3_cp_authority_schema SET checksum='wrong'"
			if name == "dirty" {
				statement = "UPDATE v3_cp_authority_schema SET dirty=1"
			}
			if name == "engine" {
				statement = "ALTER TABLE v3_cp_authority_records ENGINE=MyISAM"
			}
			if _, err := f.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			d, err := f.authority.Read(ctx, f.principal, f.intent)
			requireRefusal(t, d, err)
			calls := 0
			if err = f.authority.WithCurrent(ctx, f.principal, f.intent, Decision{}, func(context.Context, Decision) error { calls++; return nil }); err == nil || calls != 0 {
				t.Fatal("unsafe physical schema ran callback")
			}
		})
	}
}
