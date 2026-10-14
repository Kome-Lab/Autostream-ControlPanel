package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
)

var sessionRefreshSequence atomic.Uint64

type sessionRefreshDriver struct {
	ctx                                               context.Context
	execErr, affectedErr, firstReadErr, secondReadErr error
	secondMissing                                     bool
	reads, execs                                      int
	now                                               time.Time
}

func (d *sessionRefreshDriver) Open(string) (driver.Conn, error) { return &sessionRefreshConn{d}, nil }

type sessionRefreshConn struct{ d *sessionRefreshDriver }

func (c *sessionRefreshConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *sessionRefreshConn) Close() error { return nil }
func (c *sessionRefreshConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *sessionRefreshConn) QueryContext(ctx context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.reads++
	if ctx != c.d.ctx || len(args) != 1 || args[0].Value != security.HashToken("same-token") {
		return nil, errors.New("changed context or token")
	}
	if c.d.reads == 1 && c.d.firstReadErr != nil {
		return nil, c.d.firstReadErr
	}
	if c.d.reads == 2 && c.d.secondReadErr != nil {
		return nil, c.d.secondReadErr
	}
	return &sessionRefreshRows{done: c.d.reads == 2 && c.d.secondMissing, now: c.d.now}, nil
}
func (c *sessionRefreshConn) ExecContext(ctx context.Context, _ string, args []driver.NamedValue) (driver.Result, error) {
	c.d.execs++
	if ctx != c.d.ctx || len(args) != 4 || args[1].Value != security.HashToken("same-token") || args[2].Value != c.d.now || args[3].Value != c.d.now {
		return nil, errors.New("changed UPDATE authority")
	}
	return sessionRefreshResult{c.d}, c.d.execErr
}

type sessionRefreshResult struct{ d *sessionRefreshDriver }

func (r sessionRefreshResult) LastInsertId() (int64, error) {
	return 0, errors.New("unexpected insert")
}
func (r sessionRefreshResult) RowsAffected() (int64, error) { return 0, r.d.affectedErr }

type sessionRefreshRows struct {
	done bool
	now  time.Time
}

func (r *sessionRefreshRows) Columns() []string {
	return []string{"user_id", "csrf_token_hash", "idle_expires_at", "absolute_expires_at"}
}
func (r *sessionRefreshRows) Close() error { return nil }
func (r *sessionRefreshRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, []driver.Value{"DB-user", "DB-csrf-hash", r.now.Add(10 * time.Minute), r.now.Add(time.Hour)})
	return nil
}
func TestSessionRefreshZeroChangedAuthorityAndErrors(t *testing.T) {
	sentinel := errors.New("DB failure")
	for _, tc := range []struct {
		name                                string
		first, exec, affected, second, want error
		missing                             bool
		reads, execs                        int
	}{
		{name: "DB authority", reads: 2, execs: 1},
		{name: "first SELECT failure", first: sentinel, want: sentinel, reads: 1},
		{name: "UPDATE failure", exec: sentinel, want: sentinel, reads: 1, execs: 1},
		{name: "RowsAffected failure", affected: sentinel, want: sentinel, reads: 1, execs: 1},
		{name: "read-after-update failure", second: sentinel, want: sentinel, reads: 2, execs: 1},
		{name: "revoked during UPDATE", missing: true, want: ErrNotFound, reads: 2, execs: 1},
		{name: "cancellation during read", second: context.Canceled, want: context.Canceled, reads: 2, execs: 1},
		{name: "deadline during read", second: context.DeadlineExceeded, want: context.DeadlineExceeded, reads: 2, execs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), struct{}{}, tc.name)
			now := time.Now().UTC().Truncate(time.Second)
			d := &sessionRefreshDriver{ctx: ctx, now: now, firstReadErr: tc.first, execErr: tc.exec, affectedErr: tc.affected, secondReadErr: tc.second, secondMissing: tc.missing}
			name := fmt.Sprintf("r3-refresh-%d", sessionRefreshSequence.Add(1))
			sql.Register(name, d)
			db, err := sql.Open(name, "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			got, err := NewMariaDBAuthStore(db).refreshSessionAt(ctx, "same-token", 10*time.Minute, now)
			if !errors.Is(err, tc.want) || d.reads != tc.reads || d.execs != tc.execs {
				t.Fatalf("err=%v reads=%d execs=%d", err, d.reads, d.execs)
			}
			if err == nil && (got.UserID != "DB-user" || got.CSRFTokenHash != "DB-csrf-hash" || got.TokenHash != security.HashToken("same-token") || !got.IdleExpiresAt.Equal(now.Add(10*time.Minute))) {
				t.Fatal("zero-changed result must be the DB session")
			}
		})
	}
}
