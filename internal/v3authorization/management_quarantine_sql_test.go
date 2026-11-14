package v3authorization

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The public API queue is observed at Context.Done, inside enter's select.
// This is scheduling evidence, not a qualified clock or production dependency.
type managementObservedContext struct {
	context.Context
	observed chan struct{}
	once     *sync.Once
}

func (c managementObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type managementLostACKConnector struct {
	base    driver.Connector
	armed   *atomic.Bool
	entered chan struct{}
	release <-chan struct{}
}

func (c managementLostACKConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &managementLostACKConn{Conn: conn, boundary: c}, nil
}
func (c managementLostACKConnector) Driver() driver.Driver { return c.base.Driver() }

type managementLostACKConn struct {
	driver.Conn
	boundary managementLostACKConnector
}

func (c *managementLostACKConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &managementLostACKTx{Tx: tx, boundary: c.boundary}, nil
}
func (c *managementLostACKConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
func (c *managementLostACKConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}
func (c *managementLostACKConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

type managementLostACKTx struct {
	driver.Tx
	boundary managementLostACKConnector
}

func (tx *managementLostACKTx) Commit() error {
	if !tx.boundary.armed.Swap(false) {
		return tx.Tx.Commit()
	}
	// Physically release all database locks before the driver reports failure.
	// A second service entry must still wait until uncertainty is published.
	if err := tx.Tx.Rollback(); err != nil {
		return err
	}
	close(tx.boundary.entered)
	<-tx.boundary.release
	return errors.New("synthetic commit acknowledgement lost after rollback")
}

func TestSQLAuthorityManagementQueuedCommitUnknown(t *testing.T) {
	for _, stage := range []string{"bootstrap", "apply", "inspect"} {
		t.Run(stage, func(t *testing.T) {
			f := newManagementFixture(t)
			if stage != "bootstrap" {
				f.bootstrap(t)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			armed := &atomic.Bool{}
			db := sql.OpenDB(managementLostACKConnector{base: authorityConnector(t), armed: armed, entered: entered, release: release})
			defer db.Close()
			defer releaseOnce.Do(func() { close(release) })
			manager := NewManagementService(NewSQLAuthority(db), f.anchor)
			meta, queuedMeta, before := f.metadata(t), f.metadata(t), f.generation(t)
			policy := f.record(t, PolicyRecord)
			first, second := make(chan ManagementResult, 1), make(chan ManagementResult, 1)
			armed.Store(true)
			go func() {
				switch stage {
				case "bootstrap":
					first <- manager.Bootstrap(ctx, f.actor, meta, f.grant)
				case "apply":
					first <- manager.Apply(ctx, f.actor, meta, []AuthorityRecord{policy}, nil)
				default:
					first <- manager.Inspect(ctx, f.actor, meta)
				}
			}()
			select {
			case <-entered:
			case result := <-first:
				t.Fatal("commit barrier was not exercised", result)
			case <-ctx.Done():
				t.Fatal("commit barrier timed out")
			}
			waiting := make(chan struct{})
			queued := managementObservedContext{Context: ctx, observed: waiting, once: &sync.Once{}}
			go func() {
				if stage == "bootstrap" {
					second <- manager.Bootstrap(queued, f.actor, queuedMeta, f.grant)
				} else {
					second <- manager.Apply(queued, f.actor, queuedMeta, []AuthorityRecord{policy}, nil)
				}
			}()
			select {
			case <-waiting:
			case result := <-second:
				t.Fatal("queued mutation escaped the entry boundary", result)
			case <-ctx.Done():
				t.Fatal("queued mutation did not reach boundary")
			}
			select {
			case result := <-second:
				t.Fatal("queued mutation ran before commit outcome", result)
			default:
			}
			releaseOnce.Do(func() { close(release) })
			if result := <-first; result.Status != Unknown {
				t.Fatal("first unknown commit became definite", result)
			}
			if result := <-second; result.Status != Unknown {
				t.Fatal("queued mutation bypassed poison publication", result)
			}
			if f.generation(t) != before {
				t.Fatal("uncertain queue wrote new authority")
			}
		})
	}
}

func TestSQLAuthorityManagementExplicitTargetProvisioning(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			f := newManagementFixture(t)
			f.grant.ProvisionTargets = enabled
			f.bootstrap(t)
			scope := f.intent.Scope
			scope.Resource = "explicit-new-resource"
			meta := f.metadata(t)
			meta.Scope = scope
			resource := f.record(t, ResourceRecord)
			resource.Resource.Scope = scope
			before := f.generation(t)
			managementRefused(t, f.manager.Apply(context.Background(), f.actor, meta, []AuthorityRecord{resource}, nil))
			managementRefused(t, f.manager.Inspect(context.Background(), f.actor, meta))
			if f.generation(t) != before {
				t.Fatal("provisioning implicitly granted record/read access")
			}
			grant := ManagementGrant{Principal: f.actor.Principal.ID, Scope: scope, Action: Action, State: "ACTIVE",
				Operations: []ManagementOperation{ManagementRead, ManagementResource}, Subjects: []string{f.actor.Principal.ID}}
			meta = f.metadata(t)
			meta.Scope = scope
			result := f.manager.Apply(context.Background(), f.actor, meta, nil, []ManagementGrant{grant})
			if !enabled {
				managementRefused(t, result)
				if f.generation(t) != before {
					t.Fatal("ordinary scoped administrator enrolled another target")
				}
				return
			}
			managementApplied(t, result, before)
			before = f.generation(t)
			meta = f.metadata(t)
			meta.Scope = scope
			managementApplied(t, f.manager.Apply(context.Background(), f.actor, meta, []AuthorityRecord{resource}, nil), before)
			before = f.generation(t)
			meta = f.metadata(t)
			meta.Scope.Realm = "another-realm"
			grant.Scope = meta.Scope
			managementRefused(t, f.manager.Apply(context.Background(), f.actor, meta, nil, []ManagementGrant{grant}))
			if f.generation(t) != before {
				t.Fatal("target provisioner crossed Realm")
			}
		})
	}
}

func TestSQLAuthorityManagementProvisioningSourceFailures(t *testing.T) {
	for _, failure := range []string{"revoked", "unknown", "malformed", "future-generation"} {
		t.Run(failure, func(t *testing.T) {
			f := newManagementFixture(t)
			f.grant.ProvisionTargets = true
			f.bootstrap(t)
			grant := f.grant
			switch failure {
			case "revoked":
				grant.State = "REVOKED"
			case "unknown":
				grant.State = "UNKNOWN"
			}
			body, err := encodeManagementGrant(grant)
			if err != nil {
				t.Fatal(err)
			}
			generation := f.generation(t)
			if failure == "malformed" {
				body = []byte("invalid synthetic stored grant")
			}
			if failure == "future-generation" {
				generation++
			}
			if _, err := f.db.Exec("UPDATE v3_cp_management_grants SET grant_bytes=?,generation=? WHERE principal=?", body, generation, f.actor.Principal.ID); err != nil {
				t.Fatal(err)
			}
			meta := f.metadata(t)
			meta.Scope.Resource = "new-provisioning-target"
			next := ManagementGrant{Principal: f.actor.Principal.ID, Scope: meta.Scope, Action: Action, State: "ACTIVE",
				Operations: []ManagementOperation{ManagementRead}, Subjects: []string{f.actor.Principal.ID}}
			before := f.generation(t)
			managementRefused(t, f.manager.Apply(context.Background(), f.actor, meta, nil, []ManagementGrant{next}))
			if f.generation(t) != before {
				t.Fatal("unqualified provisioning source changed authority")
			}
		})
	}
}
