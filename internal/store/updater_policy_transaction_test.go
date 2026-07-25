package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMariaDBUpdaterPolicyAdminStoreUsesOneTransaction(t *testing.T) {
	t.Run("commits policy and encrypted token together", func(t *testing.T) {
		state := &updaterPolicyAtomicDBState{}
		db := openUpdaterPolicyAtomicTestDB(t, state)
		policies := NewMariaDBUpdaterPolicyAdminStore(db, "atomic-test-encryption-key")
		releaseToken := "github_pat_atomic_success"

		saved, status, err := policies.SaveUpdaterPolicyAndReleaseToken(
			t.Context(),
			"updater-01",
			0,
			validUpdaterPolicy(),
			&releaseToken,
		)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Revision != 1 || !status.Configured {
			t.Fatalf("saved policy/status = %#v / %#v", saved, status)
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.begins != 1 || state.commits != 1 || state.rollbacks != 0 ||
			!state.policyCommitted || state.tokenCiphertextCommitted == "" {
			t.Fatalf("transaction state = %#v", state)
		}
		if state.tokenCiphertextCommitted == releaseToken ||
			strings.Contains(state.tokenCiphertextCommitted, releaseToken) {
			t.Fatal("transaction stored the release token as plaintext")
		}
		if !state.policyExecInTransaction || !state.tokenExecInTransaction {
			t.Fatalf("writes did not share one transaction: %#v", state)
		}
	})

	t.Run("rolls policy back when token write fails", func(t *testing.T) {
		state := &updaterPolicyAtomicDBState{failTokenWrite: true}
		db := openUpdaterPolicyAtomicTestDB(t, state)
		policies := NewMariaDBUpdaterPolicyAdminStore(db, "atomic-test-encryption-key")
		releaseToken := "github_pat_must_not_partially_commit"

		_, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
			t.Context(),
			"updater-01",
			0,
			validUpdaterPolicy(),
			&releaseToken,
		)
		if err == nil || strings.Contains(err.Error(), releaseToken) {
			t.Fatalf("token write error = %v", err)
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.begins != 1 || state.commits != 0 || state.rollbacks != 1 ||
			state.policyCommitted || state.tokenCiphertextCommitted != "" {
			t.Fatalf("failed transaction state = %#v", state)
		}
		if !state.policyExecInTransaction || !state.tokenExecInTransaction {
			t.Fatalf("failed writes did not share one transaction: %#v", state)
		}
	})
}

var updaterPolicyAtomicDriverSequence atomic.Uint64

func openUpdaterPolicyAtomicTestDB(t *testing.T, state *updaterPolicyAtomicDBState) *sql.DB {
	t.Helper()
	driverName := fmt.Sprintf("updater-policy-atomic-test-%d", updaterPolicyAtomicDriverSequence.Add(1))
	sql.Register(driverName, &updaterPolicyAtomicDriver{state: state})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type updaterPolicyAtomicDBState struct {
	mu                       sync.Mutex
	failTokenWrite           bool
	begins                   int
	commits                  int
	rollbacks                int
	policyExecInTransaction  bool
	tokenExecInTransaction   bool
	policyCommitted          bool
	tokenCiphertextCommitted string
}

type updaterPolicyAtomicDriver struct {
	state *updaterPolicyAtomicDBState
}

func (d *updaterPolicyAtomicDriver) Open(string) (driver.Conn, error) {
	return &updaterPolicyAtomicConn{state: d.state}, nil
}

type updaterPolicyAtomicConn struct {
	state            *updaterPolicyAtomicDBState
	inTransaction    bool
	stagedPolicy     bool
	stagedCiphertext string
}

func (c *updaterPolicyAtomicConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (c *updaterPolicyAtomicConn) Close() error {
	return nil
}

func (c *updaterPolicyAtomicConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *updaterPolicyAtomicConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.begins++
	c.inTransaction = true
	c.stagedPolicy = false
	c.stagedCiphertext = ""
	return &updaterPolicyAtomicTx{conn: c}, nil
}

func (c *updaterPolicyAtomicConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	switch {
	case strings.Contains(query, "INSERT INTO update_agent_policies"):
		c.state.policyExecInTransaction = c.inTransaction
		c.stagedPolicy = true
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "INSERT INTO secrets"):
		c.state.tokenExecInTransaction = c.inTransaction
		if c.state.failTokenWrite {
			return nil, errors.New("forced updater token write failure")
		}
		if len(args) < 2 {
			return nil, errors.New("missing encrypted updater token")
		}
		ciphertext, ok := args[1].Value.(string)
		if !ok {
			return nil, errors.New("encrypted updater token is not a string")
		}
		c.stagedCiphertext = ciphertext
		return driver.RowsAffected(1), nil
	default:
		return nil, fmt.Errorf("unexpected SQL: %s", query)
	}
}

type updaterPolicyAtomicTx struct {
	conn *updaterPolicyAtomicConn
}

func (tx *updaterPolicyAtomicTx) Commit() error {
	tx.conn.state.mu.Lock()
	defer tx.conn.state.mu.Unlock()
	tx.conn.state.commits++
	tx.conn.state.policyCommitted = tx.conn.stagedPolicy
	tx.conn.state.tokenCiphertextCommitted = tx.conn.stagedCiphertext
	tx.conn.inTransaction = false
	return nil
}

func (tx *updaterPolicyAtomicTx) Rollback() error {
	tx.conn.state.mu.Lock()
	defer tx.conn.state.mu.Unlock()
	tx.conn.state.rollbacks++
	tx.conn.stagedPolicy = false
	tx.conn.stagedCiphertext = ""
	tx.conn.inTransaction = false
	return nil
}
