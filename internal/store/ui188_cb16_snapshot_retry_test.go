package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Pass through actual SQL unchanged. Gate only the first real lock call after
// discovery, then let the normal guarded assignment commit before that lock.
type cb16GateKey struct{}
type cb16SnapshotGate struct {
	reached, release chan struct{}
	once             sync.Once
	fired            atomic.Bool
	attempts         atomic.Int32
	firstStream      string
}
type cb16Connector struct {
	base driver.Connector
	gate *cb16SnapshotGate
}
type cb16Connection struct {
	driver.Conn
	gate *cb16SnapshotGate
}

func (c cb16Connector) Driver() driver.Driver { return c.base.Driver() }
func (c cb16Connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &cb16Connection{conn, c.gate}, nil
}
func (c *cb16Connection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if ctx.Value(cb16GateKey{}) == c.gate {
		c.gate.attempts.Add(1)
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *cb16Connection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if ctx.Value(cb16GateKey{}) == c.gate && strings.Contains(q, "FROM streams WHERE id = ? FOR UPDATE") && c.gate.fired.CompareAndSwap(false, true) {
		if len(args) != 1 {
			return nil, errors.New("fixture stream lock argument count")
		}
		s, ok := args[0].Value.(string)
		if !ok {
			return nil, errors.New("fixture lock argument type")
		}
		c.gate.firstStream = s
		close(c.gate.reached)
		select {
		case <-c.gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c *cb16Connection) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *cb16Connection) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}
func (c *cb16Connection) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }
func (c *cb16Connection) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *cb16Connection) IsValid() bool { return c.Conn.(driver.Validator).IsValid() }
func (c *cb16Connection) CheckNamedValue(v *driver.NamedValue) error {
	if check, ok := c.Conn.(driver.NamedValueChecker); ok {
		return check.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

func cb16CrossSnapshot(t *testing.T, public bool) (CurrentDiscordCredentialSnapshot, error) {
	t.Helper()
	base := cb15TestDB(t)
	f := cb15StoreFixture(t, base)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	gate := &cb16SnapshotGate{reached: make(chan struct{}), release: make(chan struct{})}
	defer gate.once.Do(func() { close(gate.release) })
	cfg, err := mysql.ParseDSN(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"))
	if err != nil {
		t.Fatal("fixture DSN")
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal("fixture connector")
	}
	db := sql.OpenDB(cb16Connector{connector, gate})
	db.SetMaxOpenConns(8)
	defer db.Close()
	stores := CurrentDiscordCredentialStores{Services: NewMariaDBAuthStore(db), Streams: NewMariaDBStreamStore(db), Profiles: NewMariaDBProfileStore(db), Secrets: NewMariaDBSecretStore(db, "cb15-test-only-encryption-key"), Leases: NewMariaDBRuntimeSecretLeaseStore(db)}
	type result struct {
		snapshot CurrentDiscordCredentialSnapshot
		err      error
	}
	read := make(chan result, 1)
	requestCtx := context.WithValue(ctx, cb16GateKey{}, gate)
	go func() {
		if public {
			s, e := stores.Snapshot(requestCtx, f.serviceID, f.token.ID)
			read <- result{s, e}
		} else {
			s, _, _, e := stores.currentDiscordOperation(requestCtx, f.serviceID, f.token.ID, nil, 0)
			read <- result{s, e}
		}
	}()
	select {
	case <-gate.reached:
	case <-ctx.Done():
		t.Fatal("actual first lock not reached")
	}
	if gate.firstStream != f.current.ID {
		t.Fatal("first actual lock must use discovered A")
	}
	probe, err := base.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("NOWAIT begin")
	}
	var id string
	if probe.QueryRowContext(ctx, "SELECT id FROM streams WHERE id=? FOR UPDATE NOWAIT", f.current.ID).Scan(&id) != nil || probe.QueryRowContext(ctx, "SELECT service_id FROM services WHERE service_id=? FOR UPDATE NOWAIT", f.serviceID).Scan(&id) != nil {
		probe.Rollback()
		t.Fatal("discovery held locks before first actual lock")
	}
	if probe.Rollback() != nil {
		t.Fatal("NOWAIT release")
	}
	if _, err = f.stores.Services.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: f.serviceID, StreamID: f.other.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal("normal guarded assignment B")
	}
	gate.once.Do(func() { close(gate.release) })
	var answer result
	select {
	case answer = <-read:
	case <-ctx.Done():
		t.Fatal("snapshot completion")
	}
	probe, err = base.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("rollback proof")
	}
	if probe.QueryRowContext(ctx, "SELECT id FROM streams WHERE id=? FOR UPDATE NOWAIT", f.current.ID).Scan(&id) != nil || probe.QueryRowContext(ctx, "SELECT service_id FROM services WHERE service_id=? FOR UPDATE NOWAIT", f.serviceID).Scan(&id) != nil {
		probe.Rollback()
		t.Fatal("old lock survived snapshot return")
	}
	if probe.Rollback() != nil {
		t.Fatal("rollback proof release")
	}
	var leases int
	if base.QueryRowContext(ctx, "SELECT COUNT(*) FROM runtime_secret_leases WHERE service_id=?", f.serviceID).Scan(&leases) != nil || leases != 0 {
		t.Fatal("snapshot acquired secret lease")
	}
	current, err := f.stores.Services.GetService(ctx, f.serviceID)
	if err != nil || current.CurrentStreamID != f.other.ID {
		t.Fatal("canonical B lost")
	}
	if public && answer.err == nil {
		if gate.attempts.Load() != 2 || len(answer.snapshot.Bindings) != 1 || len(answer.snapshot.Assignments) != 1 || answer.snapshot.Bindings[0].StreamID != f.other.ID || !answer.snapshot.Bindings[0].Ready || !answer.snapshot.Bindings[0].AssignmentEpoch.Equal(answer.snapshot.Assignments[0].AssignedAt) {
			t.Fatal("fresh B/epoch/profile not returned by attempt2")
		}
	}
	t.Logf("actual_first_lock_gate=true typed_assignment_conflict=%t attempts=%d locks_released=true secret_leases=0 current_B=true", errors.Is(answer.err, ErrServiceAssignmentConflict), gate.attempts.Load())
	return answer.snapshot, answer.err
}
func TestUI188CB16MariaDBTypedDiscoveryConflict(t *testing.T) {
	_, err := cb16CrossSnapshot(t, false)
	if !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatal("typed real discovery conflict not reproduced")
	}
}
func TestUI188CB16MariaDBSnapshotFreshAfterDiscoveryConflict(t *testing.T) {
	_, err := cb16CrossSnapshot(t, true)
	if err != nil {
		t.Fatal("snapshot must converge fresh after one typed conflict")
	}
}
