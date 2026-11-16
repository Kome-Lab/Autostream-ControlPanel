package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// Faults and gates wrap the real driver/connection/transaction. SQL still runs
// against the disposable MariaDB server; no authorization or count is stubbed.
type operationalLastAdminDriver struct {
	base                               driver.Connector
	mode                               string
	beforeEffect                       func()
	afterGuard                         func()
	beforeGuard                        func()
	faulted                            atomic.Bool
	commits                            atomic.Int64
	effects                            atomic.Int64
	effectOnce, guardOnce, attemptOnce sync.Once
}

func (d *operationalLastAdminDriver) Driver() driver.Driver { return d.base.Driver() }
func (d *operationalLastAdminDriver) Connect(ctx context.Context) (driver.Conn, error) {
	c, err := d.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &operationalLastAdminConn{Conn: c, owner: d}, nil
}

type operationalLastAdminConn struct {
	driver.Conn
	owner  *operationalLastAdminDriver
	mu     sync.Mutex
	active *operationalLastAdminTx
}

func (c *operationalLastAdminConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	wrapped := &operationalLastAdminTx{Tx: tx, conn: c}
	c.mu.Lock()
	c.active = wrapped
	c.mu.Unlock()
	return wrapped, nil
}
func (c *operationalLastAdminConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *operationalLastAdminConn) Ping(ctx context.Context) error {
	return c.Conn.(driver.Pinger).Ping(ctx)
}
func (c *operationalLastAdminConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *operationalLastAdminConn) IsValid() bool { return c.Conn.(driver.Validator).IsValid() }
func (c *operationalLastAdminConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	guard := strings.Contains(q, "FROM user_admin_guard")
	if guard && c.owner.beforeGuard != nil {
		c.owner.attemptOnce.Do(c.owner.beforeGuard)
	}
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err == nil && guard && c.owner.afterGuard != nil {
		return &operationalLastAdminRows{Rows: rows, owner: c.owner}, nil
	}
	return rows, err
}

type operationalLastAdminRows struct {
	driver.Rows
	owner *operationalLastAdminDriver
}

func (r *operationalLastAdminRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err == nil {
		r.owner.guardOnce.Do(r.owner.afterGuard)
	}
	return err
}
func operationalLastAdminEffect(q string) bool {
	return strings.HasPrefix(q, "UPDATE users ") || strings.HasPrefix(q, "DELETE FROM users ") || strings.HasPrefix(q, "DELETE FROM user_roles ") || strings.HasPrefix(q, "INSERT INTO user_roles ") || strings.HasPrefix(q, "DELETE FROM sessions ")
}
func (c *operationalLastAdminConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	effect := operationalLastAdminEffect(q)
	c.mu.Lock()
	tx := c.active
	c.mu.Unlock()
	if effect && tx != nil {
		if c.owner.beforeEffect != nil {
			c.owner.effectOnce.Do(c.owner.beforeEffect)
		}
	}
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
	if err == nil && effect && tx != nil {
		tx.wrote.Store(true)
		c.owner.effects.Add(1)
		if c.owner.mode == "rollback-after-write" && c.owner.faulted.CompareAndSwap(false, true) {
			return nil, errors.New("synthetic write response failure")
		}
	}
	return result, err
}

type operationalLastAdminTx struct {
	driver.Tx
	conn  *operationalLastAdminConn
	wrote atomic.Bool
}

func (tx *operationalLastAdminTx) finish() {
	tx.conn.mu.Lock()
	if tx.conn.active == tx {
		tx.conn.active = nil
	}
	tx.conn.mu.Unlock()
}
func (tx *operationalLastAdminTx) Rollback() error { defer tx.finish(); return tx.Tx.Rollback() }
func (tx *operationalLastAdminTx) Commit() error {
	defer tx.finish()
	d := tx.conn.owner
	if !tx.wrote.Load() {
		return tx.Tx.Commit()
	}
	d.commits.Add(1)
	if !d.faulted.CompareAndSwap(false, true) {
		return tx.Tx.Commit()
	}
	switch d.mode {
	case "disconnect-before-commit":
		_ = tx.Tx.Rollback()
		_ = tx.conn.Conn.Close()
		return io.ErrUnexpectedEOF
	case "commit-ack-lost":
		if err := tx.Tx.Commit(); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	default:
		return tx.Tx.Commit()
	}
}
func operationalLastAdminWrappedDB(t *testing.T, raw string, owner *operationalLastAdminDriver) *sql.DB {
	t.Helper()
	cfg, err := mysql.ParseDSN(raw)
	if err != nil {
		t.Fatal("invalid disposable driver DSN")
	}
	cfg.InterpolateParams = true
	owner.base, err = mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(owner)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}
