package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

var profileUpdateDriverSequence atomic.Uint64

type profileUpdateDriver struct {
	ctx                           context.Context
	execErr, affectedErr, readErr error
	affected                      int64
	missing                       bool
	execs, reads                  int
}

func (d *profileUpdateDriver) Open(string) (driver.Conn, error) { return &profileUpdateConn{d}, nil }

type profileUpdateConn struct{ d *profileUpdateDriver }

func (c *profileUpdateConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *profileUpdateConn) Close() error { return nil }
func (c *profileUpdateConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *profileUpdateConn) ExecContext(ctx context.Context, _ string, args []driver.NamedValue) (driver.Result, error) {
	c.d.execs++
	if ctx != c.d.ctx || len(args) != 5 || args[3].Value != "profile-id" || args[4].Value != "encoder" {
		return nil, errors.New("UPDATE context/id/kind changed")
	}
	return profileUpdateResult{c.d}, c.d.execErr
}
func (c *profileUpdateConn) QueryContext(ctx context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.reads++
	if ctx != c.d.ctx || len(args) != 2 || args[0].Value != "profile-id" || args[1].Value != "encoder" {
		return nil, errors.New("SELECT context/id/kind changed")
	}
	if c.d.readErr != nil {
		return nil, c.d.readErr
	}
	return &profileUpdateRows{done: c.d.missing}, nil
}

type profileUpdateResult struct{ d *profileUpdateDriver }

func (r profileUpdateResult) LastInsertId() (int64, error) { return 0, errors.New("unexpected insert") }
func (r profileUpdateResult) RowsAffected() (int64, error) { return r.d.affected, r.d.affectedErr }

type profileUpdateRows struct{ done bool }

func (r *profileUpdateRows) Columns() []string {
	return []string{"id", "kind", "name", "config", "created_at", "updated_at", "youtube_relay_binding_revision"}
}
func (r *profileUpdateRows) Close() error { return nil }
func (r *profileUpdateRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, []driver.Value{"profile-id", "encoder", "DB saved name", `{"width":854}`, time.Unix(1, 0).UTC(), time.Unix(2, 0).UTC(), int64(1)})
	return nil
}

func TestProfileUpdateReadAuthorityAndErrorPropagation(t *testing.T) {
	sentinel := errors.New("database failure")
	for _, test := range []struct {
		name                          string
		affected                      int64
		execErr, affectedErr, readErr error
		missing                       bool
		wantErr                       error
		wantReads                     int
	}{
		{name: "zero returns DB row", wantReads: 1},
		{name: "positive returns DB row", affected: 1, wantReads: 1},
		{name: "missing remains not found", missing: true, wantErr: ErrNotFound, wantReads: 1},
		{name: "UPDATE failure", execErr: sentinel, wantErr: sentinel},
		{name: "RowsAffected failure", affectedErr: sentinel, wantErr: sentinel},
		{name: "SELECT failure", readErr: sentinel, wantErr: sentinel, wantReads: 1},
		{name: "SELECT cancellation", readErr: context.Canceled, wantErr: context.Canceled, wantReads: 1},
		{name: "SELECT deadline", readErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded, wantReads: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), struct{}{}, test.name)
			d := &profileUpdateDriver{ctx: ctx, affected: test.affected, execErr: test.execErr, affectedErr: test.affectedErr, readErr: test.readErr, missing: test.missing}
			name := fmt.Sprintf("profile-update-%d", profileUpdateDriverSequence.Add(1))
			sql.Register(name, d)
			db, err := sql.Open(name, "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			profile, err := NewMariaDBProfileStore(db).UpdateProfile(ctx, ProfileEncoder, "profile-id", "input name", map[string]any{"width": 1920})
			if !errors.Is(err, test.wantErr) || d.execs != 1 || d.reads != test.wantReads {
				t.Fatalf("err=%v execs=%d reads=%d", err, d.execs, d.reads)
			}
			if err == nil && (profile.ID != "profile-id" || profile.Kind != ProfileEncoder || profile.Name != "DB saved name" || !reflect.DeepEqual(profile.Config, map[string]any{"width": float64(854)})) {
				t.Fatalf("result did not come from DB: %#v", profile)
			}
		})
	}
}

func TestProfileUpdateCanceledContextAndValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d := &profileUpdateDriver{ctx: ctx}
	name := fmt.Sprintf("profile-update-%d", profileUpdateDriverSequence.Add(1))
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	profiles := NewMariaDBProfileStore(db)
	if _, err = profiles.UpdateProfile(ctx, ProfileEncoder, "profile-id", "input", map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err=%v", err)
	}
	for _, input := range []struct {
		kind   ProfileKind
		name   string
		config map[string]any
	}{
		{kind: "wrong", name: "input", config: map[string]any{}},
		{kind: ProfileEncoder, name: " ", config: map[string]any{}},
		{kind: ProfileEncoder, name: "input"},
		{kind: ProfileEncoder, name: "input", config: map[string]any{"password": "raw-secret"}},
	} {
		if _, err = profiles.UpdateProfile(t.Context(), input.kind, "profile-id", input.name, input.config); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	if d.execs != 0 || d.reads != 0 {
		t.Fatalf("invalid/canceled request touched DB: %d/%d", d.execs, d.reads)
	}
}
