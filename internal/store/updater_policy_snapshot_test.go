package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMariaDBGetUpdaterPolicyRetriesPolicyBindingSnapshotChange(t *testing.T) {
	state := &updaterPolicySnapshotDBState{}
	db := openUpdaterPolicySnapshotTestDB(t, state)
	policy, err := NewMariaDBUpdaterPolicyStore(db).GetUpdaterPolicy(
		t.Context(),
		"host-agent-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 2 ||
		len(policy.Targets) != 1 ||
		policy.Targets[0].DatabaseName != "autostream_o11y" ||
		policy.Targets[0].LocalListenPort != 18082 {
		t.Fatalf("policy = %#v", policy)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.policyReads != 2 || state.bindingReads != 2 || state.listenerReads != 1 {
		t.Fatalf("read counts = policy:%d binding:%d listener:%d", state.policyReads, state.bindingReads, state.listenerReads)
	}
}

func TestMariaDBListUpdaterPoliciesRetriesPolicyBindingSnapshotChange(t *testing.T) {
	state := &updaterPolicySnapshotDBState{}
	db := openUpdaterPolicySnapshotTestDB(t, state)
	policies, err := NewMariaDBUpdaterPolicyStore(db).ListUpdaterPolicies(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 1 ||
		policies[0].Revision != 2 ||
		len(policies[0].Targets) != 1 ||
		policies[0].Targets[0].DatabaseName != "autostream_o11y" ||
		policies[0].Targets[0].LocalListenPort != 18082 {
		t.Fatalf("policies = %#v", policies)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.policyReads != 2 || state.bindingReads != 2 || state.listenerReads != 1 {
		t.Fatalf("read counts = policy:%d binding:%d listener:%d", state.policyReads, state.bindingReads, state.listenerReads)
	}
}

func TestMariaDBGetUpdaterPolicyRetriesLocalListenerSnapshotChange(t *testing.T) {
	state := &updaterPolicySnapshotDBState{listenerSnapshotChangesOnce: true}
	db := openUpdaterPolicySnapshotTestDB(t, state)
	policy, err := NewMariaDBUpdaterPolicyStore(db).GetUpdaterPolicy(
		t.Context(),
		"host-agent-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 2 ||
		len(policy.Targets) != 1 ||
		policy.Targets[0].DatabaseName != "autostream_o11y" ||
		policy.Targets[0].LocalListenPort != 18082 {
		t.Fatalf("policy = %#v", policy)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.policyReads != 3 || state.bindingReads != 3 || state.listenerReads != 2 {
		t.Fatalf("read counts = policy:%d binding:%d listener:%d", state.policyReads, state.bindingReads, state.listenerReads)
	}
}

func TestMariaDBGetUpdaterPolicyFailsAfterBoundedSnapshotRetries(t *testing.T) {
	state := &updaterPolicySnapshotDBState{alwaysStale: true}
	db := openUpdaterPolicySnapshotTestDB(t, state)
	_, err := NewMariaDBUpdaterPolicyStore(db).GetUpdaterPolicy(
		t.Context(),
		"host-agent-a",
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.policyReads != 3 || state.bindingReads != 3 || state.listenerReads != 0 {
		t.Fatalf("unbounded read counts = policy:%d binding:%d listener:%d", state.policyReads, state.bindingReads, state.listenerReads)
	}
}

func TestMariaDBListUpdaterPoliciesFailsAfterBoundedSnapshotRetries(t *testing.T) {
	state := &updaterPolicySnapshotDBState{alwaysStale: true}
	db := openUpdaterPolicySnapshotTestDB(t, state)
	_, err := NewMariaDBUpdaterPolicyStore(db).ListUpdaterPolicies(t.Context())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.policyReads != 3 || state.bindingReads != 3 || state.listenerReads != 0 {
		t.Fatalf("unbounded read counts = policy:%d binding:%d listener:%d", state.policyReads, state.bindingReads, state.listenerReads)
	}
}

func TestMariaDBGetUpdaterPolicyReturnsStableIncompleteBindingsForRepair(t *testing.T) {
	for _, test := range []struct {
		name  string
		state *updaterPolicySnapshotDBState
	}{
		{
			name:  "missing companion row",
			state: &updaterPolicySnapshotDBState{missingBinding: true},
		},
		{
			name:  "stale companion revision",
			state: &updaterPolicySnapshotDBState{staleBinding: true},
		},
		{
			name:  "blank database name",
			state: &updaterPolicySnapshotDBState{blankBinding: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := test.state
			db := openUpdaterPolicySnapshotTestDB(t, state)
			policy, err := NewMariaDBUpdaterPolicyStore(db).GetUpdaterPolicy(
				t.Context(),
				"host-agent-a",
			)
			if err != nil ||
				len(policy.Targets) != 1 ||
				policy.Targets[0].DatabaseName != "" ||
				policy.Targets[0].LocalListenPort != 18082 ||
				PullUpdaterPolicyDatabaseBindingsReady(policy) {
				t.Fatalf("repairable policy = %#v err=%v", policy, err)
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.policyReads != 1 || state.bindingReads != 1 || state.listenerReads != 1 {
				t.Fatalf(
					"read counts = policy:%d binding:%d listener:%d",
					state.policyReads,
					state.bindingReads,
					state.listenerReads,
				)
			}
		})
	}
}

func TestMariaDBListUpdaterPoliciesReturnsStableIncompleteBindingsForRepair(t *testing.T) {
	for _, test := range []struct {
		name  string
		state *updaterPolicySnapshotDBState
	}{
		{
			name:  "missing companion row",
			state: &updaterPolicySnapshotDBState{missingBinding: true},
		},
		{
			name:  "stale companion revision",
			state: &updaterPolicySnapshotDBState{staleBinding: true},
		},
		{
			name:  "blank database name",
			state: &updaterPolicySnapshotDBState{blankBinding: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := test.state
			db := openUpdaterPolicySnapshotTestDB(t, state)
			policies, err := NewMariaDBUpdaterPolicyStore(db).ListUpdaterPolicies(
				t.Context(),
			)
			if err != nil ||
				len(policies) != 1 ||
				len(policies[0].Targets) != 1 ||
				policies[0].Targets[0].DatabaseName != "" ||
				policies[0].Targets[0].LocalListenPort != 18082 ||
				PullUpdaterPolicyDatabaseBindingsReady(policies[0]) {
				t.Fatalf("repairable policies = %#v err=%v", policies, err)
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.policyReads != 1 || state.bindingReads != 1 || state.listenerReads != 1 {
				t.Fatalf(
					"read counts = policy:%d binding:%d listener:%d",
					state.policyReads,
					state.bindingReads,
					state.listenerReads,
				)
			}
		})
	}
}

func TestMemoryPullUpdaterPolicyRepairsOldWriterDatabaseBindingBySave(t *testing.T) {
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()
	input := validPullUpdaterPolicyForOwnership()
	input.Targets[0] = UpdaterPolicyTarget{
		TargetID:        "observability-a",
		ServiceID:       "observability-a",
		HostID:          "host-a",
		ServiceType:     "observability",
		DeploymentMode:  "systemd",
		DatabaseName:    "autostream_o11y",
		LocalListenPort: 18082,
	}
	created, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		"host-agent-a",
		0,
		0,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}

	policies.mu.Lock()
	oldWriter := policies.policies[created.UpdaterID]
	oldWriter.Revision++
	oldWriter.ProjectionRevision = oldWriter.Revision
	oldWriter.LocalExecutorPolicyRevision = oldWriter.Revision
	oldWriter.Targets[0].DatabaseName = ""
	policies.policies[created.UpdaterID] = oldWriter
	policies.mu.Unlock()

	repairable, err := policies.GetUpdaterPolicy(t.Context(), created.UpdaterID)
	if err != nil ||
		repairable.Targets[0].DatabaseName != "" ||
		PullUpdaterPolicyDatabaseBindingsReady(repairable) {
		t.Fatalf("repairable policy = %#v err=%v", repairable, err)
	}
	repairable.Targets[0].DatabaseName = "autostream_o11y"
	repaired, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		updates,
		created.UpdaterID,
		repairable.Revision,
		0,
		repairable,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Revision != repairable.Revision+1 ||
		!PullUpdaterPolicyDatabaseBindingsReady(repaired) ||
		repaired.Targets[0].DatabaseName != "autostream_o11y" {
		t.Fatalf("repaired policy = %#v", repaired)
	}
}

var updaterPolicySnapshotDriverSequence atomic.Uint64

func openUpdaterPolicySnapshotTestDB(
	t *testing.T,
	state *updaterPolicySnapshotDBState,
) *sql.DB {
	t.Helper()
	policy := validPullUpdaterPolicyForOwnership()
	policy.Targets[0] = UpdaterPolicyTarget{
		TargetID:       "observability-a",
		ServiceID:      "observability-a",
		ServiceType:    "observability",
		DeploymentMode: "systemd",
	}
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	state.body = body
	driverName := fmt.Sprintf(
		"updater-policy-snapshot-test-%d",
		updaterPolicySnapshotDriverSequence.Add(1),
	)
	sql.Register(driverName, &updaterPolicySnapshotDriver{state: state})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type updaterPolicySnapshotDBState struct {
	mu                          sync.Mutex
	body                        []byte
	policyReads                 int
	bindingReads                int
	listenerReads               int
	lastRevision                int64
	alwaysStale                 bool
	listenerSnapshotChangesOnce bool
	missingBinding              bool
	staleBinding                bool
	blankBinding                bool
}

type updaterPolicySnapshotDriver struct {
	state *updaterPolicySnapshotDBState
}

func (d *updaterPolicySnapshotDriver) Open(string) (driver.Conn, error) {
	return &updaterPolicySnapshotConn{state: d.state}, nil
}

type updaterPolicySnapshotConn struct {
	state *updaterPolicySnapshotDBState
}

func (c *updaterPolicySnapshotConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (c *updaterPolicySnapshotConn) Close() error {
	return nil
}

func (c *updaterPolicySnapshotConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *updaterPolicySnapshotConn) QueryContext(
	ctx context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	switch {
	case containsAll(query, "FROM update_agent_policies", "LEFT JOIN update_agent_target_databases"):
		c.state.bindingReads++
		currentRevision := int64(2)
		if c.state.alwaysStale {
			currentRevision = c.state.lastRevision + 1
		}
		if c.state.missingBinding {
			return &updaterPolicyAtomicRows{
				columns: []string{
					"current_policy_revision",
					"target_id",
					"binding_policy_revision",
					"database_name",
				},
				values: [][]driver.Value{{
					c.state.lastRevision,
					nil,
					nil,
					nil,
				}},
			}, nil
		}
		bindingRevision := currentRevision
		if c.state.staleBinding {
			currentRevision = c.state.lastRevision
			bindingRevision = currentRevision - 1
		}
		databaseName := "autostream_o11y"
		if c.state.blankBinding {
			currentRevision = c.state.lastRevision
			bindingRevision = currentRevision
			databaseName = ""
		}
		return &updaterPolicyAtomicRows{
			columns: []string{
				"current_policy_revision",
				"target_id",
				"binding_policy_revision",
				"database_name",
			},
			values: [][]driver.Value{{
				currentRevision,
				"observability-a",
				bindingRevision,
				databaseName,
			}},
		}, nil
	case containsAll(query, "FROM update_agent_target_databases", "ORDER BY target_id"):
		// Legacy three-column shape makes the pre-fix implementation return a
		// revision-1 policy with an empty database name.
		c.state.bindingReads++
		return &updaterPolicyAtomicRows{
			columns: []string{
				"target_id",
				"binding_policy_revision",
				"database_name",
			},
			values: [][]driver.Value{{
				"observability-a",
				int64(2),
				"autostream_o11y",
			}},
		}, nil
	case containsAll(query, "FROM update_agent_policies", "LEFT JOIN update_agent_target_local_listeners"):
		c.state.listenerReads++
		currentRevision := c.state.lastRevision
		if c.state.listenerSnapshotChangesOnce && c.state.listenerReads == 1 {
			currentRevision++
		}
		return &updaterPolicyAtomicRows{
			columns: []string{
				"current_policy_revision",
				"target_id",
				"binding_policy_revision",
				"local_listen_port",
			},
			values: [][]driver.Value{{
				currentRevision,
				"observability-a",
				c.state.lastRevision,
				int64(18082),
			}},
		}, nil
	case containsAll(query, "FROM update_agent_policies", "ORDER BY service_id"):
		return c.policyRows(true), nil
	case containsAll(query, "FROM update_agent_policies", "WHERE service_id = ?"):
		return c.policyRows(false), nil
	default:
		return nil, fmt.Errorf("unexpected SQL query: %s", query)
	}
}

func (c *updaterPolicySnapshotConn) policyRows(list bool) driver.Rows {
	c.state.policyReads++
	revision := int64(2)
	if c.state.alwaysStale {
		revision = int64(c.state.policyReads)
	} else if c.state.policyReads == 1 {
		revision = 1
	}
	c.state.lastRevision = revision
	now := updaterPolicySnapshotTime(revision)
	values := []driver.Value{
		revision,
		revision,
		revision,
		append([]byte(nil), c.state.body...),
		now,
	}
	columns := []string{
		"revision",
		"projection_revision",
		"local_executor_policy_revision",
		"policy_json",
		"updated_at",
	}
	if list {
		columns = append([]string{"service_id"}, columns...)
		values = append([]driver.Value{"host-agent-a"}, values...)
	}
	return &updaterPolicyAtomicRows{
		columns: columns,
		values:  [][]driver.Value{values},
	}
}

func updaterPolicySnapshotTime(generation int64) time.Time {
	return time.Date(2026, 7, 29, 0, 0, int(generation), 0, time.UTC)
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
