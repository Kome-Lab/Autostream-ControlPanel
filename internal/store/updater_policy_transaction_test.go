package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestMariaDBSavePullUpdaterPolicyRollsBackPolicyWhenOwnershipWriteFails(t *testing.T) {
	state := &updaterPolicyAtomicDBState{failOwnershipWrite: true}
	db := openUpdaterPolicyAtomicTestDB(t, state)
	policies := NewMariaDBUpdaterPolicyAdminStore(db, "unused-for-pull")
	updates := NewMariaDBSystemUpdateStore(db)
	input := validPullUpdaterPolicyForOwnership()
	input.Targets[0].LocalListenPort = 18084

	_, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-a",
		0,
		1,
		input,
	)
	if err == nil {
		t.Fatal("forced ownership write failure was accepted")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.begins != 1 || state.commits != 0 || state.rollbacks != 1 ||
		state.policyCommitted || state.ownershipCommitted {
		t.Fatalf("failed pull transaction state = %#v", state)
	}
	if !state.policyExecInTransaction ||
		!state.databaseBindingExecInTransaction ||
		!state.localListenerDeleteExecInTransaction ||
		!state.localListenerInsertExecInTransaction ||
		!state.ownershipExecInTransaction {
		t.Fatalf("pull writes did not share one transaction: %#v", state)
	}
	if state.tokenExecInTransaction || state.tokenCiphertextCommitted != "" {
		t.Fatalf("pull save accessed release-token persistence: %#v", state)
	}
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
	mu                                   sync.Mutex
	failTokenWrite                       bool
	failOwnershipWrite                   bool
	begins                               int
	commits                              int
	rollbacks                            int
	policyExecInTransaction              bool
	databaseBindingExecInTransaction     bool
	localListenerDeleteExecInTransaction bool
	localListenerInsertExecInTransaction bool
	tokenExecInTransaction               bool
	ownershipExecInTransaction           bool
	policyCommitted                      bool
	ownershipCommitted                   bool
	tokenCiphertextCommitted             string
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
	stagedOwnership  bool
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
	c.stagedOwnership = false
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
	case strings.Contains(query, "update_agent_target_databases"):
		c.state.databaseBindingExecInTransaction = c.inTransaction
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "DELETE FROM update_agent_target_local_listeners"):
		c.state.localListenerDeleteExecInTransaction = c.inTransaction
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "INSERT INTO update_agent_target_local_listeners"):
		c.state.localListenerInsertExecInTransaction = c.inTransaction
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
	case strings.Contains(query, "UPDATE system_update_execution_hosts"):
		c.state.ownershipExecInTransaction = c.inTransaction
		if c.state.failOwnershipWrite {
			return nil, errors.New("forced execution-host ownership write failure")
		}
		c.stagedOwnership = true
		return driver.RowsAffected(1), nil
	default:
		return nil, fmt.Errorf("unexpected SQL: %s", query)
	}
}

func (c *updaterPolicyAtomicConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch {
	case strings.Contains(query, "FROM system_update_execution_hosts"):
		now := time.Now().UTC()
		return &updaterPolicyAtomicRows{
			columns: []string{
				"execution_host_id",
				"transport_mode",
				"agent_service_id",
				"legacy_agent_service_id",
				"ownership_epoch",
				"policy_revision",
				"created_at",
				"updated_at",
			},
			values: [][]driver.Value{{
				"host-a",
				SystemUpdateTransportPullV2,
				"host-agent-a",
				"central-updater",
				int64(1),
				int64(0),
				now,
				now,
			}},
		}, nil
	case strings.Contains(query, "FROM system_update_jobs"):
		return &updaterPolicyAtomicRows{columns: []string{"id"}}, nil
	case strings.Contains(query, "FROM system_update_runtime_token_rotations"):
		return &updaterPolicyAtomicRows{columns: []string{"id"}}, nil
	case strings.Contains(query, "FROM update_agent_policies"):
		return &updaterPolicyAtomicRows{columns: []string{
			"revision",
			"projection_revision",
			"local_executor_policy_revision",
			"policy_json",
			"updated_at",
		}}, nil
	default:
		return nil, fmt.Errorf("unexpected SQL query: %s", query)
	}
}

type updaterPolicyAtomicRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *updaterPolicyAtomicRows) Columns() []string {
	return r.columns
}

func (r *updaterPolicyAtomicRows) Close() error {
	return nil
}

func (r *updaterPolicyAtomicRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

type updaterPolicyAtomicTx struct {
	conn *updaterPolicyAtomicConn
}

func (tx *updaterPolicyAtomicTx) Commit() error {
	tx.conn.state.mu.Lock()
	defer tx.conn.state.mu.Unlock()
	tx.conn.state.commits++
	tx.conn.state.policyCommitted = tx.conn.stagedPolicy
	tx.conn.state.ownershipCommitted = tx.conn.stagedOwnership
	tx.conn.state.tokenCiphertextCommitted = tx.conn.stagedCiphertext
	tx.conn.inTransaction = false
	return nil
}

func (tx *updaterPolicyAtomicTx) Rollback() error {
	tx.conn.state.mu.Lock()
	defer tx.conn.state.mu.Unlock()
	tx.conn.state.rollbacks++
	tx.conn.stagedPolicy = false
	tx.conn.stagedOwnership = false
	tx.conn.stagedCiphertext = ""
	tx.conn.inTransaction = false
	return nil
}
