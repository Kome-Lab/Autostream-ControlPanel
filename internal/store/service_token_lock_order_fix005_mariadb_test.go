package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/go-sql-driver/mysql"
)

const mariaDBFIX006FixturePrefix = "fix006-"

type mariaDBFIX005Cleanup struct {
	db     *sql.DB
	prefix string

	mu                     sync.Mutex
	serviceIDs             map[string]struct{}
	tokenIDs               map[string]struct{}
	streamIDs              map[string]struct{}
	assignmentIDs          map[string]struct{}
	artifactIDs            map[string]struct{}
	eventIDs               map[string]struct{}
	hostIDs                map[string]struct{}
	policyIDs              map[string]struct{}
	rotationIDs            map[string]struct{}
	jobIDs                 map[string]struct{}
	selfUpdateIDs          map[string]struct{}
	archiveMarkerStreamIDs map[string]struct{}
	retryMarkerStreamIDs   map[string]struct{}
	auxiliaryServiceIDs    map[string]struct{}
	auxiliaryStreamIDs     map[string]struct{}
	done                   bool
}

func newMariaDBFIX005Cleanup(t *testing.T, ctx context.Context, db *sql.DB) *mariaDBFIX005Cleanup {
	t.Helper()
	fixture := &mariaDBFIX005Cleanup{
		db:                     db,
		prefix:                 mariaDBFIX006FixturePrefix + strconv.FormatInt(time.Now().UnixNano(), 36) + "-",
		serviceIDs:             make(map[string]struct{}),
		tokenIDs:               make(map[string]struct{}),
		streamIDs:              make(map[string]struct{}),
		assignmentIDs:          make(map[string]struct{}),
		artifactIDs:            make(map[string]struct{}),
		eventIDs:               make(map[string]struct{}),
		hostIDs:                make(map[string]struct{}),
		policyIDs:              make(map[string]struct{}),
		rotationIDs:            make(map[string]struct{}),
		jobIDs:                 make(map[string]struct{}),
		selfUpdateIDs:          make(map[string]struct{}),
		archiveMarkerStreamIDs: make(map[string]struct{}),
		retryMarkerStreamIDs:   make(map[string]struct{}),
		auxiliaryServiceIDs:    make(map[string]struct{}),
		auxiliaryStreamIDs:     make(map[string]struct{}),
	}
	if count, err := fixture.namespaceResidueCount(ctx); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Fatalf("FIX-006 fixture namespace %q was not empty before setup: %d rows", mariaDBFIX006FixturePrefix, count)
	}
	t.Cleanup(func() { fixture.cleanup(t) })
	return fixture
}

func (fixture *mariaDBFIX005Cleanup) trackID(registry map[string]struct{}, id string) {
	if fixture == nil || strings.TrimSpace(id) == "" {
		return
	}
	fixture.mu.Lock()
	registry[strings.TrimSpace(id)] = struct{}{}
	fixture.mu.Unlock()
}

func (fixture *mariaDBFIX005Cleanup) trackServiceID(serviceID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.serviceIDs, serviceID)
	fixture.trackID(fixture.auxiliaryServiceIDs, serviceID)
}

func (fixture *mariaDBFIX005Cleanup) trackToken(token ServiceToken) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.tokenIDs, token.ID)
}

func (fixture *mariaDBFIX005Cleanup) trackStreamID(streamID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.streamIDs, streamID)
	fixture.trackID(fixture.archiveMarkerStreamIDs, streamID)
	fixture.trackID(fixture.retryMarkerStreamIDs, streamID)
	fixture.trackID(fixture.auxiliaryStreamIDs, streamID)
}

func (fixture *mariaDBFIX005Cleanup) trackHostID(hostID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.hostIDs, hostID)
}

func (fixture *mariaDBFIX005Cleanup) trackPolicyID(policyID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.policyIDs, policyID)
}

func (fixture *mariaDBFIX005Cleanup) trackRotationID(rotationID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.rotationIDs, rotationID)
}

func (fixture *mariaDBFIX005Cleanup) trackJobID(jobID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.jobIDs, jobID)
}

func (fixture *mariaDBFIX005Cleanup) trackAssignmentID(assignmentID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.assignmentIDs, assignmentID)
}

func (fixture *mariaDBFIX005Cleanup) trackArtifactID(artifactID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.artifactIDs, artifactID)
}

func (fixture *mariaDBFIX005Cleanup) trackEventID(eventID string) {
	if fixture == nil {
		return
	}
	fixture.trackID(fixture.eventIDs, eventID)
}

func (fixture *mariaDBFIX005Cleanup) cleanup(t *testing.T) {
	t.Helper()
	fixture.mu.Lock()
	if fixture.done {
		fixture.mu.Unlock()
		return
	}
	fixture.done = true
	fixture.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := fixture.cleanupInTransaction(ctx); err != nil {
		t.Errorf("FIX-006 fixture cleanup %q: %v", fixture.prefix, err)
		return
	}
	count, err := fixture.residueCount(ctx)
	if err != nil {
		t.Errorf("FIX-006 fixture residue query %q: %v", fixture.prefix, err)
		return
	}
	if count != 0 {
		t.Errorf("FIX-006 fixture namespace %q retained %d rows after cleanup", fixture.prefix, count)
	}
}

func (fixture *mariaDBFIX005Cleanup) cleanupInTransaction(ctx context.Context) error {
	if fixture == nil || fixture.db == nil || !strings.HasPrefix(fixture.prefix, mariaDBFIX006FixturePrefix) {
		return errors.New("unsafe FIX-006 fixture cleanup namespace")
	}
	tx, err := fixture.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	fixture.mu.Lock()
	serviceIDs := mariaDBFIX005SortedKeys(fixture.serviceIDs)
	trackedStreamIDs := mariaDBFIX005SortedKeys(fixture.streamIDs)
	archiveMarkerStreamIDs := mariaDBFIX005SortedKeys(fixture.archiveMarkerStreamIDs)
	retryMarkerStreamIDs := mariaDBFIX005SortedKeys(fixture.retryMarkerStreamIDs)
	auxiliaryServiceIDs := mariaDBFIX005SortedKeys(fixture.auxiliaryServiceIDs)
	auxiliaryStreamIDs := mariaDBFIX005SortedKeys(fixture.auxiliaryStreamIDs)
	trackedTokenIDs := mariaDBFIX005SortedKeys(fixture.tokenIDs)
	hostIDs := mariaDBFIX005SortedKeys(fixture.hostIDs)
	policyIDs := mariaDBFIX005SortedKeys(fixture.policyIDs)
	rotationIDs := mariaDBFIX005SortedKeys(fixture.rotationIDs)
	jobIDs := mariaDBFIX005SortedKeys(fixture.jobIDs)
	selfUpdateIDs := mariaDBFIX005SortedKeys(fixture.selfUpdateIDs)
	assignmentIDs := mariaDBFIX005SortedKeys(fixture.assignmentIDs)
	artifactIDs := mariaDBFIX005SortedKeys(fixture.artifactIDs)
	eventIDs := mariaDBFIX005SortedKeys(fixture.eventIDs)
	fixture.mu.Unlock()

	for _, column := range []string{
		"token_id",
		"staged_node_previous_token_id",
		"staged_node_token_id",
	} {
		ids, queryErr := queryMariaDBFIX005StringsByIDs(
			ctx, tx, "services", column, "service_id", serviceIDs,
		)
		if queryErr != nil {
			return queryErr
		}
		trackedTokenIDs = append(trackedTokenIDs, ids...)
	}
	for _, filter := range []struct {
		table, selected, column string
		ids                     []string
	}{
		{"system_update_runtime_token_rotations", "id", "service_id", serviceIDs},
		{"system_update_runtime_token_rotations", "id", "execution_host_id", hostIDs},
		{"system_update_jobs", "id", "agent_service_id", serviceIDs},
		{"system_update_jobs", "id", "execution_host_id", hostIDs},
		{"system_update_host_self_updates", "id", "agent_service_id", serviceIDs},
		{"system_update_host_self_updates", "id", "execution_host_id", hostIDs},
		{"stream_service_assignments", "id", "service_id", serviceIDs},
		{"stream_service_assignments", "id", "stream_id", trackedStreamIDs},
		{"stream_artifacts", "id", "stream_id", trackedStreamIDs},
		{"service_stream_events", "id", "service_id", serviceIDs},
		{"service_stream_events", "id", "stream_id", trackedStreamIDs},
	} {
		ids, queryErr := queryMariaDBFIX005StringsByIDs(
			ctx, tx, filter.table, filter.selected, filter.column, filter.ids,
		)
		if queryErr != nil {
			return queryErr
		}
		switch filter.table {
		case "system_update_runtime_token_rotations":
			rotationIDs = append(rotationIDs, ids...)
		case "system_update_jobs":
			jobIDs = append(jobIDs, ids...)
		case "system_update_host_self_updates":
			selfUpdateIDs = append(selfUpdateIDs, ids...)
		case "stream_service_assignments":
			assignmentIDs = append(assignmentIDs, ids...)
		case "stream_artifacts":
			artifactIDs = append(artifactIDs, ids...)
		case "service_stream_events":
			eventIDs = append(eventIDs, ids...)
		}
	}
	rotationIDs = sortedUniqueStrings(rotationIDs)
	for _, column := range []string{"previous_token_id", "staged_token_id", "emergency_revoked_token_id"} {
		ids, queryErr := queryMariaDBFIX005StringsByIDs(
			ctx, tx, "system_update_runtime_token_rotations", column, "id", rotationIDs,
		)
		if queryErr != nil {
			return queryErr
		}
		trackedTokenIDs = append(trackedTokenIDs, ids...)
	}
	trackedTokenIDs = sortedUniqueStrings(trackedTokenIDs)
	trackedStreamIDs = sortedUniqueStrings(trackedStreamIDs)
	hostIDs = sortedUniqueStrings(hostIDs)
	jobIDs = sortedUniqueStrings(jobIDs)
	selfUpdateIDs = sortedUniqueStrings(selfUpdateIDs)
	assignmentIDs = sortedUniqueStrings(assignmentIDs)
	artifactIDs = sortedUniqueStrings(artifactIDs)
	eventIDs = sortedUniqueStrings(eventIDs)

	fixture.mu.Lock()
	for _, item := range []struct {
		registry map[string]struct{}
		ids      []string
	}{
		{fixture.tokenIDs, trackedTokenIDs},
		{fixture.assignmentIDs, assignmentIDs},
		{fixture.artifactIDs, artifactIDs},
		{fixture.eventIDs, eventIDs},
		{fixture.rotationIDs, rotationIDs},
		{fixture.jobIDs, jobIDs},
		{fixture.selfUpdateIDs, selfUpdateIDs},
	} {
		for _, id := range item.ids {
			item.registry[id] = struct{}{}
		}
	}
	fixture.mu.Unlock()

	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "system_update_mutation_grants", "job_id", jobIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "system_update_jobs", "id", jobIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "system_update_host_self_update_grants", "self_update_id", selfUpdateIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "system_update_host_self_updates", "id", selfUpdateIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "system_update_runtime_token_rotations", "id", rotationIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "service_port_reservations", "service_id", serviceIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "service_port_reservations", "execution_host_id", hostIDs); err != nil {
		return err
	}
	for _, table := range []string{"update_agent_target_local_listeners", "update_agent_target_databases"} {
		if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, table, "updater_service_id", policyIDs); err != nil {
			return err
		}
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "update_agent_policies", "service_id", policyIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "runtime_secret_leases", "service_id", auxiliaryServiceIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "runtime_secret_leases", "stream_id", auxiliaryStreamIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "service_remediation_executions", "stream_id", trackedStreamIDs); err != nil {
		return err
	}
	for _, table := range []string{
		"stream_discord_youtube_live_notifications",
		"stream_youtube_relay_binding_claims",
		"stream_media_runtimes",
		"stream_artifact_shares",
		"stream_artifacts",
		"stream_logs",
	} {
		if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, table, "stream_id", auxiliaryStreamIDs); err != nil {
			return err
		}
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "stream_settings", "stream_id", archiveMarkerStreamIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "stream_youtube_runtimes", "stream_id", retryMarkerStreamIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "service_stream_events", "id", eventIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "stream_service_assignments", "id", assignmentIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "service_metric_snapshots", "service_id", serviceIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "system_update_execution_hosts", "execution_host_id", hostIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "services", "service_id", serviceIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "service_tokens", "id", trackedTokenIDs); err != nil {
		return err
	}
	if err := deleteMariaDBFIX005RowsByIDs(ctx, tx, "streams", "id", trackedStreamIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (fixture *mariaDBFIX005Cleanup) residueCount(ctx context.Context) (int64, error) {
	if fixture == nil || fixture.db == nil || !strings.HasPrefix(fixture.prefix, mariaDBFIX006FixturePrefix) {
		return 0, errors.New("unsafe FIX-006 fixture residue namespace")
	}
	total, err := fixture.namespaceResidueCount(ctx)
	if err != nil {
		return 0, err
	}
	fixture.mu.Lock()
	registries := []struct {
		table, column string
		ids           []string
	}{
		{"services", "service_id", mariaDBFIX005SortedKeys(fixture.serviceIDs)},
		{"service_tokens", "id", mariaDBFIX005SortedKeys(fixture.tokenIDs)},
		{"streams", "id", mariaDBFIX005SortedKeys(fixture.streamIDs)},
		{"stream_service_assignments", "id", mariaDBFIX005SortedKeys(fixture.assignmentIDs)},
		{"stream_artifacts", "id", mariaDBFIX005SortedKeys(fixture.artifactIDs)},
		{"service_stream_events", "id", mariaDBFIX005SortedKeys(fixture.eventIDs)},
		{"system_update_execution_hosts", "execution_host_id", mariaDBFIX005SortedKeys(fixture.hostIDs)},
		{"update_agent_policies", "service_id", mariaDBFIX005SortedKeys(fixture.policyIDs)},
		{"system_update_runtime_token_rotations", "id", mariaDBFIX005SortedKeys(fixture.rotationIDs)},
		{"system_update_jobs", "id", mariaDBFIX005SortedKeys(fixture.jobIDs)},
		{"system_update_host_self_updates", "id", mariaDBFIX005SortedKeys(fixture.selfUpdateIDs)},
	}
	streamIDs := mariaDBFIX005SortedKeys(fixture.streamIDs)
	serviceIDs := mariaDBFIX005SortedKeys(fixture.serviceIDs)
	fixture.mu.Unlock()
	for _, registry := range registries {
		count, countErr := countMariaDBFIX005RowsByIDs(
			ctx, fixture.db, registry.table, registry.column, registry.ids,
		)
		if countErr != nil {
			return 0, countErr
		}
		total += count
	}
	for _, table := range []string{
		"stream_discord_youtube_live_notifications",
		"stream_youtube_relay_binding_claims",
		"stream_media_runtimes",
		"stream_artifact_shares",
		"stream_logs",
		"stream_settings",
		"stream_youtube_runtimes",
		"service_remediation_executions",
		"runtime_secret_leases",
	} {
		count, countErr := countMariaDBFIX005RowsByIDs(ctx, fixture.db, table, "stream_id", streamIDs)
		if countErr != nil {
			return 0, countErr
		}
		total += count
	}
	for _, table := range []string{
		"runtime_secret_leases",
		"service_metric_snapshots",
		"service_port_reservations",
	} {
		count, countErr := countMariaDBFIX005RowsByIDs(ctx, fixture.db, table, "service_id", serviceIDs)
		if countErr != nil {
			return 0, countErr
		}
		total += count
	}
	return total, nil
}

func (fixture *mariaDBFIX005Cleanup) namespaceResidueCount(ctx context.Context) (int64, error) {
	if fixture == nil || fixture.db == nil {
		return 0, errors.New("unsafe FIX-006 fixture residue namespace")
	}
	like := mariaDBFIX006FixturePrefix + "%"
	queries := []struct {
		query string
		args  []any
	}{
		{`SELECT COUNT(*) FROM services WHERE service_id LIKE ?`, []any{like}},
		{`SELECT COUNT(*) FROM streams WHERE name LIKE ?`, []any{like}},
		{`SELECT COUNT(*) FROM update_agent_policies WHERE service_id LIKE ?`, []any{like}},
		{`SELECT COUNT(*) FROM system_update_execution_hosts WHERE execution_host_id LIKE ? OR agent_service_id LIKE ?`, []any{like, like}},
		{`SELECT COUNT(*) FROM system_update_runtime_token_rotations WHERE service_id LIKE ? OR execution_host_id LIKE ? OR idempotency_key LIKE ?`, []any{like, like, like}},
		{`SELECT COUNT(*) FROM system_update_jobs WHERE target_id LIKE ? OR agent_service_id LIKE ? OR execution_host_id LIKE ? OR idempotency_key LIKE ?`, []any{like, like, like, like}},
		{`SELECT COUNT(*) FROM system_update_host_self_updates WHERE execution_host_id LIKE ? OR agent_service_id LIKE ? OR idempotency_key LIKE ?`, []any{like, like, like}},
		{`SELECT COUNT(*) FROM runtime_secret_leases WHERE service_id LIKE ?`, []any{like}},
		{`SELECT COUNT(*) FROM service_stream_events WHERE service_id LIKE ?`, []any{like}},
		{`SELECT COUNT(*) FROM stream_service_assignments WHERE service_id LIKE ?`, []any{like}},
		{`SELECT COUNT(*) FROM service_metric_snapshots WHERE service_id LIKE ?`, []any{like}},
	}
	var total int64
	for _, item := range queries {
		var count int64
		if err := fixture.db.QueryRowContext(ctx, item.query, item.args...).Scan(&count); err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func queryMariaDBFIX005Strings(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	query string,
	args ...any,
) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sortedUniqueStrings(values), nil
}

func queryMariaDBFIX005StringsByIDs(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	table, selectedColumn, filterColumn string,
	ids []string,
) ([]string, error) {
	ids = sortedUniqueStrings(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return queryMariaDBFIX005Strings(
		ctx,
		queryer,
		"SELECT COALESCE("+selectedColumn+", '') FROM "+table+
			" WHERE "+filterColumn+" IN ("+placeholders+") ORDER BY "+selectedColumn,
		args...,
	)
}

func mariaDBFIX005SortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		if value = strings.TrimSpace(value); value != "" {
			keys = append(keys, value)
		}
	}
	sort.Strings(keys)
	return keys
}

func deleteMariaDBFIX005RowsByIDs(
	ctx context.Context,
	tx *sql.Tx,
	table, column string,
	ids []string,
) error {
	ids = sortedUniqueStrings(ids)
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+column+" IN ("+placeholders+")", args...)
	return err
}

func countMariaDBFIX005RowsByIDs(
	ctx context.Context,
	db *sql.DB,
	table, column string,
	ids []string,
) (int64, error) {
	ids = sortedUniqueStrings(ids)
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	var count int64
	err := db.QueryRowContext(
		ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+column+" IN ("+placeholders+")", args...,
	).Scan(&count)
	return count, err
}

func openMariaDBFIX005Test(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"))
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured; FIX-006 MariaDB proof remains a shipping gate")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

type mariaDBFIX005PullFixture struct {
	cleanup     *mariaDBFIX005Cleanup
	auth        MariaDBAuthStore
	policies    MariaDBUpdaterPolicyStore
	updates     *MariaDBSystemUpdateStore
	params      ActivatePullUpdaterOwnershipParams
	agentToken  ServiceToken
	targetToken ServiceToken
	targetID    string
	peerID      string
}

func newMariaDBFIX005PullFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	shareAgentToken bool,
) mariaDBFIX005PullFixture {
	t.Helper()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	return newMariaDBFIX005PullFixtureWithCleanup(
		t, ctx, db, cleanup, "", shareAgentToken, nil,
	)
}

func newMariaDBFIX005PullFixtureWithCleanup(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	cleanup *mariaDBFIX005Cleanup,
	fixtureSuffix string,
	shareAgentToken bool,
	sharedTargetToken *ServiceToken,
) mariaDBFIX005PullFixture {
	t.Helper()
	auth := NewMariaDBAuthStore(db)
	updates := NewMariaDBSystemUpdateStore(db)
	policies := NewMariaDBUpdaterPolicyAdminStore(db, "unused-for-pull")
	fixturePrefix := cleanup.prefix + strings.TrimSpace(fixtureSuffix)
	hostID := fixturePrefix + "host"
	peerID := fixturePrefix + "a-observer"
	peerHostID := fixturePrefix + "peer-host"
	targetID := fixturePrefix + "m-target"
	agentID := fixturePrefix + "z-pull"

	targetToken := registerMariaDBFIX005Service(t, ctx, auth, cleanup, ServiceRegistration{
		ServiceID: targetID, ServiceType: "worker", ServiceName: targetID,
		PublicURL: "https://worker.example.com:18081",
	}, sharedTargetToken)
	cleanup.trackHostID(hostID)
	peerToken := registerMariaDBFIX005Service(t, ctx, auth, cleanup, ServiceRegistration{
		ServiceID: peerID, ServiceType: "update_agent", ServiceName: peerID,
		TransportMode:   SystemUpdateTransportPullV2,
		ExecutionHostID: peerHostID, OwnershipEpoch: 0,
		Capabilities: map[string]any{"observe_only": true},
	}, nil)
	cleanup.trackPolicyID(agentID)

	var existing *ServiceToken
	if shareAgentToken {
		existing = &peerToken
	}
	agentToken := registerMariaDBFIX005Service(t, ctx, auth, cleanup, ServiceRegistration{
		ServiceID: agentID, ServiceType: "update_agent", ServiceName: agentID,
		TransportMode:   SystemUpdateTransportPullV2,
		ExecutionHostID: hostID, OwnershipEpoch: 0,
		Capabilities: map[string]any{"observe_only": true},
	}, existing)
	policy, err := policies.SavePullUpdaterPolicy(
		ctx,
		updates,
		agentID,
		0,
		0,
		UpdaterPolicy{
			TransportMode:             SystemUpdateTransportPullV2,
			ExecutionHostID:           hostID,
			LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []UpdaterPolicyTarget{{
				TargetID: targetID, ServiceID: targetID,
				ServiceType: "worker", DeploymentMode: "systemd",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]any{
		"host_agent":             true,
		"observe_only":           true,
		"update_executor":        true,
		"mutation_enabled":       false,
		"recovery_pending":       false,
		"transport_mode":         SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      hostID,
		"ownership_epoch":        int64(0),
		"policy_revision":        policy.ProjectionRevision,
		"policy_status":          "applied",
		"target_availability": map[string]any{
			targetID: "available",
		},
		"target_availability_codes": map[string]any{
			targetID: "executor_verified",
		},
		"reported_ports": map[string]any{
			targetID: int64(18081),
		},
		"port_drift": map[string]any{
			targetID: false,
		},
		"reported_service_types": map[string]any{
			targetID: "worker",
		},
		"reported_deployment_modes": map[string]any{
			targetID: "systemd",
		},
		"reported_executor_policy_revisions": map[string]any{
			targetID: policy.LocalExecutorPolicyRevision,
		},
		"reported_executor_policy_sha256": map[string]any{
			targetID: policy.LocalExecutorPolicySHA256,
		},
		"reported_config_revisions": map[string]any{
			targetID: int64(1),
		},
		"reported_config_sha256": map[string]any{
			targetID: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
	}
	if _, err := auth.Heartbeat(ctx, agentToken, ServiceHeartbeat{
		ServiceID: agentID, Status: "online", Version: "v1.0.0", Capabilities: capabilities,
	}); err != nil {
		t.Fatal(err)
	}
	return mariaDBFIX005PullFixture{
		cleanup: cleanup, auth: auth, policies: policies, updates: updates,
		agentToken: agentToken, targetToken: targetToken, targetID: targetID, peerID: peerID,
		params: ActivatePullUpdaterOwnershipParams{
			ServiceID:                           agentID,
			ExecutionHostID:                     hostID,
			ExpectedExecutionHostOwnershipEpoch: 0,
			ExpectedSourcePolicyRevision:        policy.Revision,
			ExpectedProjectionRevision:          policy.ProjectionRevision,
			ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
			ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		},
	}
}

func installMariaDBFIX009SystemUpdateJobLaneAnchors(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	cleanup *mariaDBFIX005Cleanup,
	executionHostIDs ...string,
) {
	t.Helper()
	terminalStatuses := []string{"canceled", "failed", "rolled_back", "succeeded"}
	for _, executionHostID := range sortedUniqueStrings(executionHostIDs) {
		anchorRows := make([]struct {
			executionHostID string
			status          string
		}, 0, len(terminalStatuses)+1)
		for _, status := range terminalStatuses {
			anchorRows = append(anchorRows, struct {
				executionHostID string
				status          string
			}{executionHostID: executionHostID, status: status})
		}
		anchorRows = append(anchorRows, struct {
			executionHostID string
			status          string
		}{executionHostID: executionHostID + "~fix009-upper", status: "canceled"})

		for index, anchor := range anchorRows {
			jobID := newUUID()
			cleanup.trackJobID(jobID)
			observedAt := time.Now().UTC().Add(time.Duration(index) * time.Microsecond)
			if _, err := db.ExecContext(ctx, `INSERT INTO system_update_jobs
(id, target_id, target_service_type, execution_host_id, deployment_mode, target_version, strategy, status, idempotency_key, requested_by_user_id, created_at, updated_at)
VALUES (?, ?, 'worker', ?, 'systemd', 'v0.0.0', 'immediate', ?, ?, ?, ?, ?)`,
				jobID,
				cleanup.prefix+"lane-anchor-"+jobID,
				anchor.executionHostID,
				anchor.status,
				"lane-anchor-"+jobID,
				cleanup.prefix,
				observedAt,
				observedAt,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func registerMariaDBFIX005Service(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	cleanup *mariaDBFIX005Cleanup,
	registration ServiceRegistration,
	existing *ServiceToken,
) ServiceToken {
	t.Helper()
	cleanup.trackServiceID(registration.ServiceID)
	var token ServiceToken
	if existing == nil {
		scopes := []string{"service.register", "service.heartbeat"}
		if registration.ServiceType == "update_agent" {
			scopes = append(
				scopes,
				"service.config.read",
				"updates.claim",
				"updates.report",
				"updates.authorize",
			)
		}
		var err error
		token, err = auth.CreateServiceToken(ctx, registration.ServiceType, scopes)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		token = *existing
	}
	cleanup.trackToken(token)
	registration.Version = "v1.0.0"
	if registration.Capabilities == nil {
		registration.Capabilities = map[string]any{}
	}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	return token
}

func deactivateMariaDBFIX005Params(
	activated ActivatePullUpdaterOwnershipResult,
) DeactivatePullUpdaterOwnershipParams {
	return DeactivatePullUpdaterOwnershipParams{
		ServiceID:                           activated.Service.ServiceID,
		ExecutionHostID:                     activated.Ownership.ExecutionHostID,
		ExpectedExecutionHostOwnershipEpoch: activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activated.Policy.Revision,
		ExpectedProjectionRevision:          activated.Policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activated.Policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   activated.Policy.LocalExecutorPolicySHA256,
	}
}

func mariaDBFIX005RuntimeStageParams(
	fixture mariaDBFIX005PullFixture,
	activated ActivatePullUpdaterOwnershipResult,
	idempotencyKey string,
) (StageSystemUpdateRuntimeTokenRotationParams, NodeTokenSealer, NodeTokenUnsealer) {
	key := "fix006-runtime-key-" + fixture.cleanup.prefix
	seal := NodeTokenSealer(func(raw string) (string, string, error) {
		return security.EncryptSecret(raw, key)
	})
	unseal := NodeTokenUnsealer(func(ciphertext, nonce string) (string, error) {
		return security.DecryptSecret(ciphertext, nonce, key)
	})
	return StageSystemUpdateRuntimeTokenRotationParams{
		ServiceID: fixture.params.ServiceID, ExecutionHostID: fixture.params.ExecutionHostID,
		IdempotencyKey:                      idempotencyKey,
		ExpectedOwnershipEpoch:              activated.Ownership.OwnershipEpoch,
		ExpectedSourcePolicyRevision:        activated.Policy.Revision,
		ExpectedProjectionRevision:          activated.Policy.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: activated.Policy.LocalExecutorPolicyRevision,
		Now:                                 time.Now().UTC().Truncate(time.Microsecond),
	}, seal, unseal
}

func readyMariaDBFIX005RuntimeHeartbeatProof(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBFIX005PullFixture,
	stageParams StageSystemUpdateRuntimeTokenRotationParams,
	local SystemUpdateRuntimeTokenRotation,
	rawStagedToken string,
	now time.Time,
) ProveSystemUpdateRuntimeTokenRotationHeartbeatParams {
	t.Helper()
	policy, err := fixture.policies.GetUpdaterPolicy(ctx, stageParams.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	const (
		agentVersion    = "v1.7.8"
		executorVersion = "v1.7.8"
	)
	capabilities, err := json.Marshal(map[string]any{
		"host_agent": true, "update_executor": true,
		"mutation_enabled": true, "recovery_pending": false,
		"agent_version": agentVersion, "agent_protocol_version": 2,
		"executor_version": executorVersion, "executor_protocol_version": 1,
		"mutation_protocol_version":      1,
		"execution_host_id":              stageParams.ExecutionHostID,
		"ownership_epoch":                stageParams.ExpectedOwnershipEpoch,
		"source_policy_revision":         stageParams.ExpectedSourcePolicyRevision,
		"projection_revision":            stageParams.ExpectedProjectionRevision,
		"local_executor_policy_revision": stageParams.ExpectedLocalExecutorPolicyRevision,
		"local_executor_policy_sha256":   policy.LocalExecutorPolicySHA256,
		"local_stage_receipt_id":         local.LocalStageReceiptID,
		"local_phase":                    SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE services
SET status = 'online', last_heartbeat_at = ?, reported_version = ?,
    reported_capabilities = ?, updated_at = ?
WHERE service_id = ?`,
		now, agentVersion, capabilities, now, stageParams.ServiceID,
	); err != nil {
		t.Fatal(err)
	}
	return ProveSystemUpdateRuntimeTokenRotationHeartbeatParams{
		RotationID: local.ID, ServiceID: stageParams.ServiceID,
		ExecutionHostID:  stageParams.ExecutionHostID,
		ExpectedRevision: local.Revision, RawStagedToken: rawStagedToken,
		Phase:        SystemUpdateRuntimeTokenRotationHeartbeatProofPhase,
		AgentVersion: agentVersion, ExecutorVersion: executorVersion,
		AgentProtocolVersion: 2, ExecutorProtocolVersion: 1,
		MutationProtocolVersion:             1,
		ExpectedOwnershipEpoch:              stageParams.ExpectedOwnershipEpoch,
		ExpectedSourcePolicyRevision:        stageParams.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          stageParams.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: stageParams.ExpectedLocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		LocalStageReceiptID:                 local.LocalStageReceiptID,
		Now:                                 now,
	}
}

func TestMariaDBFIX005PrecreateVsTokenMutationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, mutation := range []string{"rotate", "revoke"} {
		mutation := mutation
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("precreate_vs_%s/%d", mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				token, err := auth.CreateServiceToken(
					ctx, "worker", []string{"service.register", "service.heartbeat"},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(token)

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				mutationHeld := make(chan struct{})
				releaseMutation := make(chan struct{})
				var releaseMutationOnce sync.Once
				defer releaseMutationOnce.Do(func() { close(releaseMutation) })
				var heldOnce sync.Once
				mutationOperation := mutation + "_service_token"
				mutationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation != mutationOperation {
						return
					}
					mutationPhases <- phase
					if phase == mariaDBServiceTokenBindingsValidated {
						heldOnce.Do(func() { close(mutationHeld) })
						<-releaseMutation
					}
				})
				mutationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, mutationObserver,
				)
				mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					if mutation == "rotate" {
						rotated, mutationErr := auth.RotateServiceToken(mutationCtx, token.ID)
						mutationResult <- mariaDBServiceTokenMutationResult{token: rotated, err: mutationErr}
						return
					}
					mutationResult <- mariaDBServiceTokenMutationResult{
						err: auth.RevokeServiceToken(mutationCtx, token.ID),
					}
				}()
				select {
				case <-mutationHeld:
				case <-time.After(5 * time.Second):
					t.Fatal("token mutation did not hold the unbound token revalidation phase")
				}
				assertMariaDBFIX005PhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
					mariaDBServiceTokenTokenLocksHeld,
					mariaDBServiceTokenBindingsValidated,
				})

				precreatePhases := make(chan mariaDBServiceTokenLockPhase, 8)
				precreateObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == "precreate_service" {
						precreatePhases <- phase
					}
				})
				precreateCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, precreateObserver,
				)
				serviceID := cleanup.prefix + "late-binding"
				cleanup.trackServiceID(serviceID)
				precreateResult := make(chan error, 1)
				go func() {
					_, precreateErr := auth.PrecreateService(
						precreateCtx,
						token,
						ServiceRegistration{
							ServiceID: serviceID, ServiceType: "worker", ServiceName: serviceID,
							PublicURL: "https://worker.example.com:18081",
						},
					)
					precreateResult <- precreateErr
				}()
				assertMariaDBFIX005PhaseSequence(t, precreatePhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
				})
				select {
				case err := <-precreateResult:
					t.Fatalf("PrecreateService completed before the token lock was released: %v", err)
				case <-time.After(75 * time.Millisecond):
				}
				releaseMutationOnce.Do(func() { close(releaseMutation) })

				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				assertMariaDBServiceTokenOperationError(t, mutation, mutationOutcome.err)
				cleanup.trackToken(mutationOutcome.token)
				precreateErr := receiveMariaDBServiceTokenError(t, precreateResult, "PrecreateService")
				if !errors.Is(precreateErr, ErrForbidden) {
					t.Fatalf("PrecreateService error = %v, want ErrForbidden", precreateErr)
				}
				if _, err := auth.GetService(ctx, serviceID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("late-bound service lookup error = %v, want ErrNotFound", err)
				}
				if !mariaDBServiceTokenRevoked(t, ctx, db, token.ID) {
					t.Fatal("old token was not revoked")
				}
				if mutation == "rotate" {
					if mutationOutcome.token.ID == "" || mariaDBServiceTokenRevoked(t, ctx, db, mutationOutcome.token.ID) {
						t.Fatalf("rotated token final state = %s", formatSafeServiceTokenDiagnostic("rotate", mutationOutcome.token, 0, "unexpected_result"))
					}
				}
			})
		}
	}
}

func TestMariaDBFIX009PrecreateWinsVsTokenMutationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, mutation := range []string{"rotate", "revoke"} {
		mutation := mutation
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("precreate_wins_vs_%s/%d", mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				token, err := auth.CreateServiceToken(
					ctx, "worker", []string{"service.register", "service.heartbeat"},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(token)

				mutationOperation := mutation + "_service_token"
				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 16)
				beforeServiceLocks := make(chan struct{})
				releaseMutation := make(chan struct{})
				var releaseMutationOnce sync.Once
				defer releaseMutationOnce.Do(func() { close(releaseMutation) })
				var holdOnce sync.Once
				mutationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation != mutationOperation {
						return
					}
					mutationPhases <- phase
					if phase == mariaDBServiceTokenBeforeServiceLocks {
						holdOnce.Do(func() {
							close(beforeServiceLocks)
							<-releaseMutation
						})
					}
				})
				mutationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, mutationObserver,
				)
				mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					if mutation == "rotate" {
						rotated, mutationErr := auth.RotateServiceToken(mutationCtx, token.ID)
						mutationResult <- mariaDBServiceTokenMutationResult{token: rotated, err: mutationErr}
						return
					}
					mutationResult <- mariaDBServiceTokenMutationResult{
						err: auth.RevokeServiceToken(mutationCtx, token.ID),
					}
				}()
				select {
				case <-beforeServiceLocks:
				case <-time.After(5 * time.Second):
					t.Fatal("token mutation did not pause after its committed reference discovery")
				}

				serviceID := cleanup.prefix + "precreate-winner"
				cleanup.trackServiceID(serviceID)
				if _, err := auth.PrecreateService(ctx, token, ServiceRegistration{
					ServiceID: serviceID, ServiceType: "worker", ServiceName: serviceID,
					PublicURL: "https://worker.example.com:18081",
				}); err != nil {
					t.Fatalf("PrecreateService did not commit before %s: %v", mutation, err)
				}
				releaseMutationOnce.Do(func() { close(releaseMutation) })

				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				assertMariaDBServiceTokenOperationError(t, mutation, mutationOutcome.err)
				cleanup.trackToken(mutationOutcome.token)
				close(mutationPhases)
				discoveryAttempts := 0
				for phase := range mutationPhases {
					if phase == mariaDBServiceTokenBeforeServiceLocks {
						discoveryAttempts++
					}
				}
				if discoveryAttempts != 2 {
					t.Fatalf("committed binding discovery attempts = %d, want one retry", discoveryAttempts)
				}

				service, err := auth.GetService(ctx, serviceID)
				if err != nil {
					t.Fatal(err)
				}
				if !mariaDBServiceTokenRevoked(t, ctx, db, token.ID) {
					t.Fatal("old token was not revoked")
				}
				var oldReferences int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM services
WHERE token_id = ? OR staged_node_previous_token_id = ? OR staged_node_token_id = ?`,
					token.ID, token.ID, token.ID,
				).Scan(&oldReferences); err != nil {
					t.Fatal(err)
				}
				if mutation == "rotate" {
					if mutationOutcome.token.ID == "" ||
						service.TokenID != mutationOutcome.token.ID ||
						oldReferences != 0 ||
						mariaDBServiceTokenRevoked(t, ctx, db, mutationOutcome.token.ID) {
						t.Fatalf("precreate/rotate final state service=%s token=%s old_refs=%d", formatSafeRegisteredServiceDiagnostic(service), formatSafeServiceTokenDiagnostic("rotate", mutationOutcome.token, oldReferences, "unexpected_result"), oldReferences)
					}
					return
				}
				if service.TokenID != token.ID || oldReferences != 1 {
					t.Fatalf("precreate/revoke final state service=%s old_refs=%d", formatSafeRegisteredServiceDiagnostic(service), oldReferences)
				}
			})
		}
	}
}

func TestMariaDBFIX009ActivationInsertFailureRollsBack(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for iteration := 1; iteration <= 3; iteration++ {
		t.Run(strconv.Itoa(iteration), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(parent, 20*time.Second)
			defer cancel()
			cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
			serviceID := cleanup.prefix + "activation-rollback"
			oldToken := createMariaDBServiceTokenPairService(
				t, ctx, auth, serviceID, "update_agent", nil, cleanup,
			)
			now := time.Now().UTC()
			configureToken := cleanup.prefix + "configure"
			if _, err := auth.SetServiceConfigureToken(
				ctx, serviceID, security.HashToken(configureToken), now.Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}
			staged, err := auth.StageServiceNodeConfiguration(
				ctx,
				serviceID,
				configureToken,
				now,
				func(string) (string, string, error) {
					return "rollback-ciphertext", "rollback-nonce", nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			cleanup.trackToken(staged.Token)
			triggerName := "fix009_activation_" + strings.ReplaceAll(newUUID(), "-", "")
			if _, err := db.ExecContext(ctx, fmt.Sprintf(
				"CREATE TRIGGER `%s` BEFORE UPDATE ON services FOR EACH ROW "+
					"SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'fix009 activation rollback'",
				triggerName,
			)); err != nil {
				t.Fatal(err)
			}
			triggerPresent := true
			t.Cleanup(func() {
				if triggerPresent {
					_, _ = db.ExecContext(context.Background(), "DROP TRIGGER IF EXISTS `"+triggerName+"`")
				}
			})

			_, _, _, activationErr := auth.ActivateServiceNodeConfiguration(
				ctx,
				serviceID,
				staged.Token.ID,
				staged.ActivationToken,
				now.Add(time.Minute),
				ServiceRuntimeReport{},
			)
			if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS `"+triggerName+"`"); err != nil {
				t.Fatal(err)
			}
			triggerPresent = false
			if activationErr == nil {
				t.Fatal("activation unexpectedly committed through the injected service-row update failure")
			}
			service, err := auth.GetService(ctx, serviceID)
			if err != nil {
				t.Fatal(err)
			}
			if service.TokenID != oldToken.ID ||
				service.StagedNodePreviousTokenID != oldToken.ID ||
				service.StagedNodeTokenID != staged.Token.ID ||
				service.StagedNodeTokenHash != staged.Token.TokenHash ||
				service.NodeTokenCiphertext != "" ||
				service.NodeTokenNonce != "" {
				t.Fatalf("failed activation partially mutated service: %s", formatSafeRegisteredServiceDiagnostic(service))
			}
			var oldRevoked bool
			if err := db.QueryRowContext(
				ctx,
				`SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?`,
				oldToken.ID,
			).Scan(&oldRevoked); err != nil {
				t.Fatal(err)
			}
			var stagedTokenRows int
			if err := db.QueryRowContext(
				ctx,
				`SELECT COUNT(*) FROM service_tokens WHERE id = ?`,
				staged.Token.ID,
			).Scan(&stagedTokenRows); err != nil {
				t.Fatal(err)
			}
			if oldRevoked || stagedTokenRows != 0 {
				t.Fatalf("failed activation token state old_revoked=%v staged_rows=%d", oldRevoked, stagedTokenRows)
			}
			if _, err := auth.AuthenticateServiceToken(ctx, oldToken.RawToken, "updates.claim"); err != nil {
				t.Fatalf("failed activation invalidated the old token: %v", err)
			}
			if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, "updates.claim"); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("failed activation enabled the staged token: %v", err)
			}
		})
	}
}

func TestMariaDBFIX009ActivationWinsVsGenericTokenMutation(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, mutation := range []string{"rotate", "revoke"} {
		mutation := mutation
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("activation_vs_%s/%d", mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				serviceID := cleanup.prefix + "activation-race"
				oldToken := createMariaDBServiceTokenPairService(
					t, ctx, auth, serviceID, "update_agent", nil, cleanup,
				)
				now := time.Now().UTC()
				configureToken := cleanup.prefix + "configure"
				if _, err := auth.SetServiceConfigureToken(
					ctx, serviceID, security.HashToken(configureToken), now.Add(time.Hour),
				); err != nil {
					t.Fatal(err)
				}
				staged, err := auth.StageServiceNodeConfiguration(
					ctx,
					serviceID,
					configureToken,
					now,
					func(string) (string, string, error) {
						return "activation-race-ciphertext", "activation-race-nonce", nil
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(staged.Token)

				activationHeld := make(chan struct{})
				releaseActivation := make(chan struct{})
				var releaseActivationOnce sync.Once
				defer releaseActivationOnce.Do(func() { close(releaseActivation) })
				var activationHoldOnce sync.Once
				activationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == "activate_service_node_configuration" &&
						phase == mariaDBServiceTokenBindingsValidated {
						activationHoldOnce.Do(func() {
							close(activationHeld)
							<-releaseActivation
						})
					}
				})
				activationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, activationObserver,
				)
				activationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					activated, _, _, activationErr := auth.ActivateServiceNodeConfiguration(
						activationCtx,
						serviceID,
						staged.Token.ID,
						staged.ActivationToken,
						now.Add(time.Minute),
						ServiceRuntimeReport{},
					)
					activationResult <- mariaDBServiceTokenMutationResult{token: activated, err: activationErr}
				}()
				select {
				case <-activationHeld:
				case <-time.After(5 * time.Second):
					t.Fatal("activation did not hold the validated service/token closure")
				}

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 16)
				mutationObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == mutation+"_service_token" {
						mutationPhases <- phase
					}
				})
				mutationCtx := context.WithValue(
					ctx, mariaDBServiceTokenLockObserverContextKey{}, mutationObserver,
				)
				mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
				go func() {
					if mutation == "rotate" {
						rotated, mutationErr := auth.RotateServiceToken(mutationCtx, oldToken.ID)
						mutationResult <- mariaDBServiceTokenMutationResult{token: rotated, err: mutationErr}
						return
					}
					mutationResult <- mariaDBServiceTokenMutationResult{
						err: auth.RevokeServiceToken(mutationCtx, oldToken.ID),
					}
				}()
				if phase := receiveMariaDBServiceTokenPhase(t, mutationPhases, mutation+" service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
					t.Fatalf("first %s phase = %q", mutation, phase)
				}
				select {
				case phase := <-mutationPhases:
					t.Fatalf("%s advanced to %q while activation held the service row", mutation, phase)
				case <-time.After(75 * time.Millisecond):
				}
				releaseActivationOnce.Do(func() { close(releaseActivation) })

				activated := receiveMariaDBServiceTokenMutation(t, activationResult)
				assertMariaDBServiceTokenOperationError(t, "activation", activated.err)
				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				if !errors.Is(mutationOutcome.err, ErrNotFound) {
					assertMariaDBServiceTokenOperationError(t, mutation, mutationOutcome.err)
					t.Fatalf("%s error = %v, want ErrNotFound after activation", mutation, mutationOutcome.err)
				}
				service, err := auth.GetService(ctx, serviceID)
				if err != nil {
					t.Fatal(err)
				}
				if activated.token.ID != staged.Token.ID ||
					service.TokenID != staged.Token.ID ||
					service.StagedNodePreviousTokenID != "" ||
					service.StagedNodeTokenID != "" ||
					!mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
					mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID) {
					t.Fatalf("activation/%s final state service=%s token=%s", mutation, formatSafeRegisteredServiceDiagnostic(service), formatSafeServiceTokenDiagnostic("activate", activated.token, 0, "unexpected_result"))
				}
			})
		}
	}
}

func assertMariaDBFIX005PhaseSequence(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	expected []mariaDBServiceTokenLockPhase,
) {
	t.Helper()
	for index, want := range expected {
		got := receiveMariaDBServiceTokenPhase(t, phases, fmt.Sprintf("phase %d", index+1))
		if got != want {
			t.Fatalf("lock phase %d = %q, want %q", index+1, got, want)
		}
	}
}

func receiveMariaDBFIX006UpdaterPolicyPhase(
	t *testing.T,
	phases <-chan mariaDBUpdaterPolicyLockPhase,
	label string,
) mariaDBUpdaterPolicyLockPhase {
	t.Helper()
	select {
	case phase := <-phases:
		return phase
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not reach its bounded barrier", label)
		return ""
	}
}

type mariaDBFIX005OwnershipResult struct {
	ownership SystemUpdateExecutionHost
	err       error
}

func TestFIX009ReusedSchemaPairSuitesFenceJobLanesAndRepeatInternally(t *testing.T) {
	sourceBytes, err := os.ReadFile("service_token_lock_order_fix005_mariadb_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	anchorCall := "installMariaDBFIX009SystemUpdate" + "JobLaneAnchors("
	if count := strings.Count(source, anchorCall); count != 3 {
		t.Fatalf("FIX-009 job-lane anchor definition/call count = %d, want 3", count)
	}
	repeatLoop := "for suiteRepetition := 1; suiteRepetition <= 3;" + " suiteRepetition++"
	if count := strings.Count(source, repeatLoop); count != 2 {
		t.Fatalf("FIX-009 internal suite repeat count = %d, want 2", count)
	}
	for _, anchor := range []string{
		`terminalStatuses := []string{"canceled", "failed", "rolled_back", "succeeded"}`,
		`executionHostID + "~fix009-upper"`,
		"cleanup.trackJobID(jobID)",
	} {
		if !strings.Contains(source, anchor) {
			t.Fatalf("FIX-009 reused-schema lane anchor is missing %q", anchor)
		}
	}
}

func TestMariaDBFIX006PolicyCyclePairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	for suiteRepetition := 1; suiteRepetition <= 3; suiteRepetition++ {
		for _, pairCase := range mariaDBFIX007PolicyPairInventory() {
			if pairCase.kind != "cycle" {
				continue
			}
			pairCase := pairCase
			pair := struct {
				first  string
				second string
			}{first: pairCase.firstOperation, second: pairCase.secondOperation}
			for iteration := 1; iteration <= pairCase.iterations; iteration++ {
				t.Run(fmt.Sprintf("repeat_%d/%s_vs_%s/%d", suiteRepetition, pair.first, pair.second, iteration), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(parent, 30*time.Second)
					defer cancel()
					cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
					firstFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t, ctx, db, cleanup, "cycle-a-", false, nil,
					)
					secondFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t,
						ctx,
						db,
						cleanup,
						"cycle-b-",
						false,
						&firstFixture.targetToken,
					)
					if firstFixture.targetToken.ID == "" ||
						firstFixture.targetToken.ID != secondFixture.targetToken.ID {
						t.Fatalf(
							"policy cycle fixture does not share its target token: first=%q second=%q",
							firstFixture.targetToken.ID,
							secondFixture.targetToken.ID,
						)
					}
					firstPlan, err := firstFixture.policies.discoverMariaDBPullUpdaterOwnershipLockPlan(
						ctx,
						firstFixture.auth,
						firstFixture.params.ServiceID,
						firstFixture.params.ExecutionHostID,
					)
					if err != nil {
						t.Fatal(err)
					}
					closure := make(map[string]struct{}, len(firstPlan.ServiceIDs))
					for _, serviceID := range firstPlan.ServiceIDs {
						closure[serviceID] = struct{}{}
					}
					for _, serviceID := range []string{firstFixture.targetID, secondFixture.targetID} {
						if _, exists := closure[serviceID]; !exists {
							t.Fatalf("shared-token closure omitted service %q: %#v", serviceID, firstPlan.ServiceIDs)
						}
					}
					var firstActivated, secondActivated ActivatePullUpdaterOwnershipResult
					if pair.first == "deactivate" {
						firstActivated, err = firstFixture.policies.ActivatePullUpdaterOwnership(
							ctx, firstFixture.auth, firstFixture.updates, firstFixture.params,
						)
						if err != nil {
							t.Fatal(err)
						}
					}
					if pair.second == "deactivate" {
						secondActivated, err = secondFixture.policies.ActivatePullUpdaterOwnership(
							ctx, secondFixture.auth, secondFixture.updates, secondFixture.params,
						)
						if err != nil {
							t.Fatal(err)
						}
					}
					installMariaDBFIX009SystemUpdateJobLaneAnchors(
						t,
						ctx,
						db,
						cleanup,
						firstFixture.params.ExecutionHostID,
						secondFixture.params.ExecutionHostID,
					)
					ownershipPreState := snapshotMariaDBFIX007OwnershipSemanticState(
						t, ctx, firstFixture, secondFixture,
					)

					firstOperation := pair.first + "_pull_updater_ownership"
					firstPolicyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
					firstServicePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					releaseFirst := make(chan struct{})
					var releaseOnce sync.Once
					defer releaseOnce.Do(func() { close(releaseFirst) })
					firstPolicyObserver := mariaDBUpdaterPolicyLockObserver(func(
						operation string,
						phase mariaDBUpdaterPolicyLockPhase,
					) {
						if operation != firstOperation {
							return
						}
						firstPolicyPhases <- phase
						if phase == mariaDBUpdaterPolicyPolicyLocksHeld {
							select {
							case <-releaseFirst:
							case <-ctx.Done():
							}
						}
					})
					firstServiceObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == firstOperation {
							firstServicePhases <- phase
						}
					})
					firstCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBUpdaterPolicyLockObserverContextKey{},
							firstPolicyObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						firstServiceObserver,
					)
					firstResult := make(chan mariaDBFIX005OwnershipResult, 1)
					go func() {
						firstResult <- runMariaDBFIX006OwnershipOperation(
							firstCtx, firstFixture, pair.first, firstActivated,
						)
					}()
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						firstPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{
							mariaDBUpdaterPolicyBeforeHostLock,
							mariaDBUpdaterPolicyHostLockHeld,
							mariaDBUpdaterPolicyLaneLocksHeld,
							mariaDBUpdaterPolicyBeforePolicyLocks,
							mariaDBUpdaterPolicyPolicyLocksHeld,
						},
					)
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, firstFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, secondFixture.params.ServiceID,
					)

					secondOperation := pair.second + "_pull_updater_ownership"
					secondPolicyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
					secondServicePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					secondPolicyObserver := mariaDBUpdaterPolicyLockObserver(func(
						operation string,
						phase mariaDBUpdaterPolicyLockPhase,
					) {
						if operation == secondOperation {
							secondPolicyPhases <- phase
						}
					})
					secondServiceObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == secondOperation {
							secondServicePhases <- phase
						}
					})
					secondCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBUpdaterPolicyLockObserverContextKey{},
							secondPolicyObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						secondServiceObserver,
					)
					secondResult := make(chan mariaDBFIX005OwnershipResult, 1)
					go func() {
						secondResult <- runMariaDBFIX006OwnershipOperation(
							secondCtx, secondFixture, pair.second, secondActivated,
						)
					}()
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						secondPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{
							mariaDBUpdaterPolicyBeforeHostLock,
							mariaDBUpdaterPolicyHostLockHeld,
							mariaDBUpdaterPolicyLaneLocksHeld,
							mariaDBUpdaterPolicyBeforePolicyLocks,
						},
					)
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, secondFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, firstFixture.params.ServiceID,
					)

					releaseOnce.Do(func() { close(releaseFirst) })
					firstOutcome := receiveMariaDBFIX006OwnershipResult(t, firstResult, "first ownership")
					secondOutcome := receiveMariaDBFIX006OwnershipResult(t, secondResult, "second ownership")
					assertMariaDBServiceTokenOperationError(t, pair.first, firstOutcome.err)
					assertMariaDBServiceTokenOperationError(t, pair.second, secondOutcome.err)
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						firstPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{mariaDBUpdaterPolicyPolicySetRevalidated},
					)
					assertMariaDBFIX006UpdaterPolicyPhaseSequence(
						t,
						secondPolicyPhases,
						[]mariaDBUpdaterPolicyLockPhase{
							mariaDBUpdaterPolicyPolicyLocksHeld,
							mariaDBUpdaterPolicyPolicySetRevalidated,
						},
					)
					for _, phases := range []<-chan mariaDBServiceTokenLockPhase{
						firstServicePhases,
						secondServicePhases,
					} {
						assertMariaDBFIX005PhaseSequence(t, phases, []mariaDBServiceTokenLockPhase{
							mariaDBServiceTokenBeforeServiceLocks,
							mariaDBServiceTokenServiceLocksHeld,
							mariaDBServiceTokenBeforeTokenLocks,
							mariaDBServiceTokenTokenLocksHeld,
							mariaDBServiceTokenBindingsValidated,
						})
					}
					assertMariaDBFIX007StrongOwnershipFinalState(
						t,
						ctx,
						ownershipPreState,
						[]mariaDBFIX007OwnershipAction{
							{fixture: firstFixture, operation: pair.first, result: firstOutcome.ownership},
							{fixture: secondFixture, operation: pair.second, result: secondOutcome.ownership},
						},
						"",
						mariaDBServiceTokenMutationResult{},
					)
				})
			}
		}
	}
}

func runMariaDBFIX006OwnershipOperation(
	ctx context.Context,
	fixture mariaDBFIX005PullFixture,
	operation string,
	activated ActivatePullUpdaterOwnershipResult,
) mariaDBFIX005OwnershipResult {
	if operation == "activate" {
		result, err := fixture.policies.ActivatePullUpdaterOwnership(
			ctx, fixture.auth, fixture.updates, fixture.params,
		)
		return mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
	}
	result, err := fixture.policies.DeactivatePullUpdaterOwnership(
		ctx,
		fixture.auth,
		fixture.updates,
		deactivateMariaDBFIX005Params(activated),
	)
	return mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
}

func assertMariaDBFIX006UpdaterPolicyPhaseSequence(
	t *testing.T,
	phases <-chan mariaDBUpdaterPolicyLockPhase,
	expected []mariaDBUpdaterPolicyLockPhase,
) {
	t.Helper()
	for index, want := range expected {
		got := receiveMariaDBFIX006UpdaterPolicyPhase(
			t, phases, fmt.Sprintf("updater policy phase %d", index+1),
		)
		if got != want {
			t.Fatalf("updater policy phase %d = %q, want %q", index+1, got, want)
		}
	}
}

func receiveMariaDBFIX006OwnershipResult(
	t *testing.T,
	result <-chan mariaDBFIX005OwnershipResult,
	label string,
) mariaDBFIX005OwnershipResult {
	t.Helper()
	select {
	case outcome := <-result:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not complete before timeout", label)
		return mariaDBFIX005OwnershipResult{}
	}
}

func TestMariaDBFIX006UpdaterOwnershipVsTokenMutationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	for _, pairCase := range mariaDBFIX007PolicyPairInventory() {
		if pairCase.kind != "token_mutation" {
			continue
		}
		ownershipOperation := pairCase.firstOperation
		tokenMutation := pairCase.secondOperation
		for iteration := 1; iteration <= pairCase.iterations; iteration++ {
			t.Run(fmt.Sprintf("%s_vs_%s/%d", ownershipOperation, tokenMutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 25*time.Second)
				defer cancel()
				fixture := newMariaDBFIX005PullFixture(t, ctx, db, true)
				var activated ActivatePullUpdaterOwnershipResult
				if ownershipOperation == "deactivate" {
					var err error
					activated, err = fixture.policies.ActivatePullUpdaterOwnership(
						ctx, fixture.auth, fixture.updates, fixture.params,
					)
					if err != nil {
						t.Fatal(err)
					}
				}
				ownershipPreState := snapshotMariaDBFIX007OwnershipSemanticState(
					t, ctx, fixture,
				)

				blocker := lockMariaDBServiceTokenForTest(t, ctx, db, fixture.agentToken.ID)
				defer blocker.Rollback()
				ownershipPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				observedOwnershipOperation := ownershipOperation + "_pull_updater_ownership"
				ownershipObserver := mariaDBServiceTokenLockObserver(func(
					operation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if operation == observedOwnershipOperation {
						ownershipPhases <- phase
					}
				})
				policyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
				policyObserver := mariaDBUpdaterPolicyLockObserver(func(
					operation string,
					phase mariaDBUpdaterPolicyLockPhase,
				) {
					if operation == observedOwnershipOperation {
						policyPhases <- phase
					}
				})
				ownershipCtx := context.WithValue(
					context.WithValue(
						ctx,
						mariaDBUpdaterPolicyLockObserverContextKey{},
						policyObserver,
					),
					mariaDBServiceTokenLockObserverContextKey{},
					ownershipObserver,
				)
				ownershipResult := make(chan mariaDBFIX005OwnershipResult, 1)
				go func() {
					if ownershipOperation == "activate" {
						result, err := fixture.policies.ActivatePullUpdaterOwnership(
							ownershipCtx, fixture.auth, fixture.updates, fixture.params,
						)
						ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
						return
					}
					result, err := fixture.policies.DeactivatePullUpdaterOwnership(
						ownershipCtx,
						fixture.auth,
						fixture.updates,
						deactivateMariaDBFIX005Params(activated),
					)
					ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
				}()
				for index, want := range []mariaDBUpdaterPolicyLockPhase{
					mariaDBUpdaterPolicyBeforeHostLock,
					mariaDBUpdaterPolicyHostLockHeld,
					mariaDBUpdaterPolicyLaneLocksHeld,
					mariaDBUpdaterPolicyBeforePolicyLocks,
					mariaDBUpdaterPolicyPolicyLocksHeld,
					mariaDBUpdaterPolicyPolicySetRevalidated,
				} {
					got := receiveMariaDBFIX006UpdaterPolicyPhase(
						t, policyPhases, fmt.Sprintf("ownership policy phase %d", index+1),
					)
					if got != want {
						t.Fatalf("ownership policy phase %d = %q, want %q", index+1, got, want)
					}
				}
				assertMariaDBFIX005PhaseSequence(t, ownershipPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
				})
				for _, serviceID := range []string{
					fixture.peerID,
					fixture.targetID,
					fixture.params.ServiceID,
				} {
					assertMariaDBFIX006ServiceRowLockHeld(t, ctx, db, serviceID)
				}

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				mutationResult := startMariaDBFIX005TokenMutation(
					ctx,
					fixture.auth,
					fixture.agentToken.ID,
					tokenMutation,
					mutationPhases,
				)
				assertMariaDBFIX005PhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
				})
				assertMariaDBFIX006ServiceRowLockHeld(t, ctx, db, fixture.peerID)
				if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
					t.Fatal(err)
				}

				var ownershipOutcome mariaDBFIX005OwnershipResult
				select {
				case ownershipOutcome = <-ownershipResult:
				case <-time.After(10 * time.Second):
					t.Fatal("updater ownership operation did not complete")
				}
				assertMariaDBServiceTokenOperationError(t, ownershipOperation, ownershipOutcome.err)
				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				assertMariaDBServiceTokenOperationError(t, tokenMutation, mutationOutcome.err)
				fixture.cleanup.trackToken(mutationOutcome.token)
				assertMariaDBFIX005PhaseSequence(t, ownershipPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenTokenLocksHeld,
					mariaDBServiceTokenBindingsValidated,
				})
				assertMariaDBFIX005PhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
					mariaDBServiceTokenTokenLocksHeld,
					mariaDBServiceTokenBindingsValidated,
				})

				assertMariaDBFIX007StrongOwnershipFinalState(
					t,
					ctx,
					ownershipPreState,
					[]mariaDBFIX007OwnershipAction{{
						fixture: fixture, operation: ownershipOperation, result: ownershipOutcome.ownership,
					}},
					tokenMutation,
					mutationOutcome,
				)
			})
		}
	}
}

func startMariaDBFIX005TokenMutation(
	ctx context.Context,
	auth MariaDBAuthStore,
	tokenID, mutation string,
	phases chan<- mariaDBServiceTokenLockPhase,
) <-chan mariaDBServiceTokenMutationResult {
	operation := mutation + "_service_token"
	observer := mariaDBServiceTokenLockObserver(func(
		observedOperation string,
		phase mariaDBServiceTokenLockPhase,
	) {
		if observedOperation == operation {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	result := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		if mutation == "rotate" {
			rotated, err := auth.RotateServiceToken(mutationCtx, tokenID)
			result <- mariaDBServiceTokenMutationResult{token: rotated, err: err}
			return
		}
		result <- mariaDBServiceTokenMutationResult{err: auth.RevokeServiceToken(mutationCtx, tokenID)}
	}()
	return result
}

type mariaDBFIX005RuntimeOperation struct {
	name       string
	path       string
	tokenID    string
	rotationID string
	claimID    string
	stageTime  time.Time
	run        func(context.Context) mariaDBFIX007RuntimeOperationResult
}

type mariaDBFIX007RuntimeOperationResult struct {
	rotation SystemUpdateRuntimeTokenRotation
	result   string
	err      error
}

func TestMariaDBFIX006RuntimeTokenRotationMatrixInventory(t *testing.T) {
	seen := make(map[string]map[string]int)
	for _, testCase := range mariaDBFIX007RuntimeMatrixInventory() {
		if seen[testCase.path] == nil {
			seen[testCase.path] = make(map[string]int)
		}
		seen[testCase.path][testCase.tokenMutation]++
	}
	if len(seen) != 8 {
		t.Fatalf("runtime path count = %d, want 8", len(seen))
	}
	for path, mutations := range seen {
		for _, mutation := range []string{"rotate", "revoke"} {
			if mutations[mutation] != 1 {
				t.Fatalf("runtime matrix %s/%s count = %d, want 1", path, mutation, mutations[mutation])
			}
		}
	}
	if got := len(mariaDBFIX007RuntimeMatrixInventory()); got != 16 {
		t.Fatalf("runtime matrix pair count = %d, want 16", got)
	}
}

func TestMariaDBFIX006UpdaterRuntimeBarrierUsesObservedLockPhase(t *testing.T) {
	sourceBytes, err := os.ReadFile("service_token_lock_order_fix005_mariadb_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func " + "TestMariaDBFIX006UpdaterOwnershipVsRuntimeRotationPairs("
	nextMarker := "func " + "assertMariaDBFIX006ExecutionHostRowLockHeld("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("updater-vs-runtime test not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following updater-vs-runtime test not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	for _, forbidden := range []string{"ownershipStarted", "75 * time.Millisecond", "time.Sleep("} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("updater-vs-runtime barrier still relies on %q", forbidden)
		}
	}
	for _, required := range []string{
		"mariaDBUpdaterPolicyLockObserver",
		"mariaDBUpdaterPolicyLaneLocksHeld",
		"mariaDBRuntimeTokenRotationLockObserver",
		"assertMariaDBFIX007RuntimeRotationRowLockHeld",
		"assertMariaDBFIX007ServiceTokenRowLockHeld",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("updater-vs-runtime barrier does not require %q", required)
		}
	}
}

func TestMariaDBFIX006CleanupUsesExactTrackedIDs(t *testing.T) {
	sourceBytes, err := os.ReadFile("service_token_lock_order_fix005_mariadb_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func " + "(fixture *mariaDBFIX005Cleanup) cleanupInTransaction("
	nextMarker := "func " + "(fixture *mariaDBFIX005Cleanup) residueCount("
	start := strings.Index(source, startMarker)
	end := strings.Index(source, nextMarker)
	if start < 0 || end <= start {
		t.Fatal("FIX-006 cleanup implementation source was not found")
	}
	body := source[start:end]
	if strings.Contains(body, " LIKE ") || strings.Contains(body, "fixture.prefix + \"%\"") {
		t.Fatal("FIX-006 cleanup still deletes rows by a prefix pattern")
	}
	for _, required := range []string{
		"serviceIDs",
		"trackedTokenIDs",
		"trackedStreamIDs",
		"assignmentIDs",
		"artifactIDs",
		"hostIDs",
		"policyIDs",
		"rotationIDs",
		"archiveMarkerStreamIDs",
		"retryMarkerStreamIDs",
		"auxiliaryServiceIDs",
		"auxiliaryStreamIDs",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("FIX-006 cleanup does not consume tracked registry %q", required)
		}
	}
}

func TestMariaDBFIX006RuntimeTokenRotationLockOrderMatrix(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	for _, testCase := range mariaDBFIX007RuntimeMatrixInventory() {
		testCase := testCase
		path := testCase.path
		mutation := testCase.tokenMutation
		for iteration := 1; iteration <= testCase.iterations; iteration++ {
			t.Run(fmt.Sprintf("%s_vs_%s/%d", path, mutation, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 30*time.Second)
				defer cancel()
				fixture := newMariaDBFIX005PullFixture(t, ctx, db, false)
				activated, err := fixture.policies.ActivatePullUpdaterOwnership(
					ctx, fixture.auth, fixture.updates, fixture.params,
				)
				if err != nil {
					t.Fatal(err)
				}
				operation := prepareMariaDBFIX005RuntimeOperation(
					t, ctx, db, fixture, activated, path,
				)
				preState := snapshotMariaDBFIX007RuntimeSemanticState(
					t, ctx, db, fixture, operation.rotationID,
				)
				assertMariaDBFIX007RuntimePreState(t, testCase, operation, preState)
				expectedState, err := deriveMariaDBFIX008ExpectedRuntimeState(
					testCase, operation, preState,
				)
				if err != nil {
					t.Fatal(err)
				}
				blocker := lockMariaDBServiceTokenForTest(t, ctx, db, operation.tokenID)
				defer blocker.Rollback()

				runtimePhases := make(chan mariaDBServiceTokenLockPhase, 8)
				runtimeObserver := mariaDBServiceTokenLockObserver(func(
					observedOperation string,
					phase mariaDBServiceTokenLockPhase,
				) {
					if observedOperation == operation.name {
						runtimePhases <- phase
					}
				})
				runtimeLockPhases := make(chan mariaDBRuntimeTokenRotationLockPhase, 8)
				runtimeLockObserver := mariaDBRuntimeTokenRotationLockObserver(func(
					observedOperation string,
					phase mariaDBRuntimeTokenRotationLockPhase,
				) {
					if observedOperation == operation.name {
						runtimeLockPhases <- phase
					}
				})
				runtimeCtx := context.WithValue(
					context.WithValue(
						ctx,
						mariaDBRuntimeTokenRotationLockObserverContextKey{},
						runtimeLockObserver,
					),
					mariaDBServiceTokenLockObserverContextKey{},
					runtimeObserver,
				)
				runtimeResult := make(chan mariaDBFIX007RuntimeOperationResult, 1)
				go func() { runtimeResult <- operation.run(runtimeCtx) }()
				assertMariaDBFIX007RuntimeLockPhaseSequence(
					t, runtimeLockPhases, testCase.requiredLockPhases,
				)
				assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
					mariaDBServiceTokenServiceLocksHeld,
					mariaDBServiceTokenBeforeTokenLocks,
				})
				assertMariaDBFIX006ServiceRowLockHeld(t, ctx, db, fixture.params.ServiceID)

				mutationPhases := make(chan mariaDBServiceTokenLockPhase, 8)
				mutationResult := startMariaDBFIX005TokenMutation(
					ctx, fixture.auth, operation.tokenID, mutation, mutationPhases,
				)
				assertMariaDBFIX005PhaseSequence(t, mutationPhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenBeforeServiceLocks,
				})
				assertMariaDBFIX006ServiceRowLockHeld(t, ctx, db, fixture.params.ServiceID)
				if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
					t.Fatal(err)
				}

				runtimeOutcome := receiveMariaDBFIX007RuntimeOperationResult(t, runtimeResult, path)
				assertMariaDBServiceTokenOperationError(t, path, runtimeOutcome.err)
				mutationOutcome := receiveMariaDBServiceTokenMutation(t, mutationResult)
				fixture.cleanup.trackToken(mutationOutcome.token)
				assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
					mariaDBServiceTokenTokenLocksHeld,
					mariaDBServiceTokenBindingsValidated,
				})
				assertMariaDBFIX005MutationTail(t, mutationPhases, path == "activate")
				assertMariaDBFIX007RuntimeExactFinalState(
					t,
					ctx,
					db,
					fixture,
					expectedState,
					runtimeOutcome,
					mutationOutcome,
				)
			})
		}
	}
}

func prepareMariaDBFIX005RuntimeOperation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBFIX005PullFixture,
	activated ActivatePullUpdaterOwnershipResult,
	path string,
) mariaDBFIX005RuntimeOperation {
	t.Helper()
	stageParams, seal, unseal := mariaDBFIX005RuntimeStageParams(
		fixture, activated, fixture.cleanup.prefix+path,
	)
	stage := func() StageSystemUpdateRuntimeTokenRotationResult {
		result, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
			ctx, fixture.auth, fixture.policies, stageParams, seal,
		)
		if err != nil {
			t.Fatal(err)
		}
		fixture.cleanup.trackRotationID(result.Rotation.ID)
		fixture.cleanup.trackToken(ServiceToken{ID: result.Rotation.StagedTokenID})
		return result
	}
	var claimedID string
	claim := func(staged StageSystemUpdateRuntimeTokenRotationResult) ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult {
		claimedID = newUUID()
		result, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
			ctx,
			fixture.auth,
			fixture.policies,
			ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
				RotationID: staged.Rotation.ID, ServiceID: stageParams.ServiceID,
				ExecutionHostID:              stageParams.ExecutionHostID,
				AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
				ClaimID:                      claimedID,
				ExpectedRevision:             staged.Rotation.Revision,
				Now:                          stageParams.Now.Add(time.Second),
			},
			unseal,
		)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	markLocal := func(
		staged StageSystemUpdateRuntimeTokenRotationResult,
		claimed ClaimSystemUpdateRuntimeTokenRotationStagedCredentialResult,
	) SystemUpdateRuntimeTokenRotation {
		result, _, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
			ctx,
			fixture.auth,
			fixture.policies,
			MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
				RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
				ExpectedRevision: claimed.Rotation.Revision,
				RawStagedToken:   claimed.Token.RawToken,
				Now:              stageParams.Now.Add(2 * time.Second),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	switch path {
	case "stage":
		return mariaDBFIX005RuntimeOperation{
			name: "stage_system_update_runtime_token_rotation", path: path,
			tokenID: activated.Service.TokenID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
				result, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
					callCtx, fixture.auth, fixture.policies, stageParams, seal,
				)
				fixture.cleanup.trackRotationID(result.Rotation.ID)
				fixture.cleanup.trackToken(ServiceToken{ID: result.Rotation.StagedTokenID})
				operationResult := "existing"
				if result.Created {
					operationResult = "created"
				}
				return mariaDBFIX007RuntimeOperationResult{
					rotation: result.Rotation, result: operationResult, err: err,
				}
			},
		}
	case "claim_staged_credential":
		staged := stage()
		claimID := newUUID()
		return mariaDBFIX005RuntimeOperation{
			name: "claim_system_update_runtime_token_rotation_credential", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
			claimID: claimID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
				result, err := fixture.updates.ClaimSystemUpdateRuntimeTokenRotationStagedCredential(
					callCtx,
					fixture.auth,
					fixture.policies,
					ClaimSystemUpdateRuntimeTokenRotationStagedCredentialParams{
						RotationID: staged.Rotation.ID, ServiceID: stageParams.ServiceID,
						ExecutionHostID:              stageParams.ExecutionHostID,
						AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
						ClaimID:                      claimID,
						ExpectedRevision:             staged.Rotation.Revision,
						Now:                          stageParams.Now.Add(time.Second),
					},
					unseal,
				)
				operationResult := "duplicate"
				if result.Claimed {
					operationResult = "claimed"
				}
				return mariaDBFIX007RuntimeOperationResult{
					rotation: result.Rotation, result: operationResult, err: err,
				}
			},
		}
	case "local_staged", "heartbeat_proof", "activate":
		staged := stage()
		claimed := claim(staged)
		if path == "local_staged" {
			return mariaDBFIX005RuntimeOperation{
				name: "mark_system_update_runtime_token_rotation_local_staged", path: path,
				tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
				claimID: claimedID, stageTime: stageParams.Now,
				run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
					rotation, applied, err := fixture.updates.MarkSystemUpdateRuntimeTokenRotationLocalStaged(
						callCtx,
						fixture.auth,
						fixture.policies,
						MarkSystemUpdateRuntimeTokenRotationLocalStagedParams{
							RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
							ExpectedRevision: claimed.Rotation.Revision,
							RawStagedToken:   claimed.Token.RawToken,
							Now:              stageParams.Now.Add(2 * time.Second),
						},
					)
					return mariaDBFIX007RuntimeTransitionResult(rotation, applied, err)
				},
			}
		}
		local := markLocal(staged, claimed)
		proof := readyMariaDBFIX005RuntimeHeartbeatProof(
			t, ctx, db, fixture, stageParams, local, claimed.Token.RawToken,
			stageParams.Now.Add(3*time.Second),
		)
		if path == "heartbeat_proof" {
			return mariaDBFIX005RuntimeOperation{
				name: "prove_system_update_runtime_token_rotation_heartbeat", path: path,
				tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
				claimID: claimedID, stageTime: stageParams.Now,
				run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
					rotation, applied, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
						callCtx, fixture.auth, fixture.policies, proof,
					)
					return mariaDBFIX007RuntimeTransitionResult(rotation, applied, err)
				},
			}
		}
		proved, _, err := fixture.updates.ProveSystemUpdateRuntimeTokenRotationHeartbeat(
			ctx, fixture.auth, fixture.policies, proof,
		)
		if err != nil {
			t.Fatal(err)
		}
		return mariaDBFIX005RuntimeOperation{
			name: "activate_system_update_runtime_token_rotation", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
			claimID: claimedID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
				rotation, applied, err := fixture.updates.ActivateSystemUpdateRuntimeTokenRotation(
					callCtx,
					fixture.auth,
					ActivateSystemUpdateRuntimeTokenRotationParams{
						RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
						ExpectedRevision: proved.Revision,
						RawStagedToken:   claimed.Token.RawToken,
						Now:              stageParams.Now.Add(4 * time.Second),
					},
				)
				return mariaDBFIX007RuntimeTransitionResult(rotation, applied, err)
			},
		}
	case "cancel":
		staged := stage()
		return mariaDBFIX005RuntimeOperation{
			name: "cancel_system_update_runtime_token_rotation", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
				rotation, applied, err := fixture.updates.CancelSystemUpdateRuntimeTokenRotation(
					callCtx,
					fixture.auth,
					CancelSystemUpdateRuntimeTokenRotationParams{
						RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
						ExpectedRevision: staged.Rotation.Revision,
						Now:              stageParams.Now.Add(time.Second),
					},
				)
				return mariaDBFIX007RuntimeTransitionResult(rotation, applied, err)
			},
		}
	case "acknowledge_cancel":
		staged := stage()
		claimed := claim(staged)
		canceled, _, err := fixture.updates.CancelSystemUpdateRuntimeTokenRotation(
			ctx,
			fixture.auth,
			CancelSystemUpdateRuntimeTokenRotationParams{
				RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
				ExpectedRevision: claimed.Rotation.Revision,
				Now:              stageParams.Now.Add(time.Second),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return mariaDBFIX005RuntimeOperation{
			name: "acknowledge_system_update_runtime_token_rotation_cancel", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID,
			claimID: claimedID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
				rotation, applied, err := fixture.updates.AcknowledgeSystemUpdateRuntimeTokenRotationCancel(
					callCtx,
					fixture.auth,
					fixture.policies,
					AcknowledgeSystemUpdateRuntimeTokenRotationCancelParams{
						RotationID: staged.Rotation.ID, ServiceID: stageParams.ServiceID,
						ExecutionHostID:              stageParams.ExecutionHostID,
						AuthenticatedPreviousTokenID: staged.Rotation.PreviousTokenID,
						ExpectedRevision:             canceled.Revision,
						Now:                          stageParams.Now.Add(2 * time.Second),
					},
				)
				return mariaDBFIX007RuntimeTransitionResult(rotation, applied, err)
			},
		}
	case "emergency_revoke":
		staged := stage()
		return mariaDBFIX005RuntimeOperation{
			name: "emergency_revoke_system_update_runtime_token", path: path,
			tokenID: staged.Rotation.PreviousTokenID, rotationID: staged.Rotation.ID, stageTime: stageParams.Now,
			run: func(callCtx context.Context) mariaDBFIX007RuntimeOperationResult {
				rotation, applied, err := fixture.updates.EmergencyRevokeSystemUpdateRuntimeToken(
					callCtx,
					fixture.auth,
					EmergencyRevokeSystemUpdateRuntimeTokenParams{
						RotationID: staged.Rotation.ID, ExecutionHostID: stageParams.ExecutionHostID,
						ExpectedRevision: staged.Rotation.Revision,
						TokenID:          staged.Rotation.PreviousTokenID,
						Now:              stageParams.Now.Add(time.Second),
					},
				)
				return mariaDBFIX007RuntimeTransitionResult(rotation, applied, err)
			},
		}
	default:
		t.Fatalf("unknown runtime path %q", path)
		return mariaDBFIX005RuntimeOperation{}
	}
}

func mariaDBFIX007RuntimeTransitionResult(
	rotation SystemUpdateRuntimeTokenRotation,
	applied bool,
	err error,
) mariaDBFIX007RuntimeOperationResult {
	result := "no_op"
	if applied {
		result = "applied"
	}
	return mariaDBFIX007RuntimeOperationResult{rotation: rotation, result: result, err: err}
}

func receiveMariaDBFIX007RuntimeOperationResult(
	t *testing.T,
	result <-chan mariaDBFIX007RuntimeOperationResult,
	label string,
) mariaDBFIX007RuntimeOperationResult {
	t.Helper()
	select {
	case outcome := <-result:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not complete before timeout", label)
		return mariaDBFIX007RuntimeOperationResult{}
	}
}

func assertMariaDBFIX005MutationTail(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	expectReferenceSetRetry bool,
) {
	t.Helper()
	assertMariaDBFIX005PhaseSequence(t, phases, []mariaDBServiceTokenLockPhase{
		mariaDBServiceTokenServiceLocksHeld,
		mariaDBServiceTokenBeforeTokenLocks,
		mariaDBServiceTokenTokenLocksHeld,
	})
	if expectReferenceSetRetry {
		assertMariaDBFIX005PhaseSequence(t, phases, []mariaDBServiceTokenLockPhase{
			mariaDBServiceTokenBeforeServiceLocks,
			mariaDBServiceTokenServiceLocksHeld,
			mariaDBServiceTokenBeforeTokenLocks,
			mariaDBServiceTokenTokenLocksHeld,
			mariaDBServiceTokenBindingsValidated,
		})
		select {
		case phase := <-phases:
			t.Fatalf("unexpected token mutation phase after reference-set retry %q", phase)
		default:
		}
		return
	}
	select {
	case phase := <-phases:
		if phase != mariaDBServiceTokenBindingsValidated {
			t.Fatalf("unexpected token mutation tail phase %q", phase)
		}
	default:
	}
}

func TestMariaDBFIX006UpdaterOwnershipVsRuntimeRotationPairs(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	for suiteRepetition := 1; suiteRepetition <= 3; suiteRepetition++ {
		for _, pairCase := range mariaDBFIX007PolicyPairInventory() {
			if pairCase.kind != "runtime_rotation" {
				continue
			}
			ownershipOperation := pairCase.secondOperation
			for iteration := 1; iteration <= pairCase.iterations; iteration++ {
				t.Run(fmt.Sprintf("repeat_%d/%s_vs_runtime_claim_staged_credential/%d", suiteRepetition, ownershipOperation, iteration), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(parent, 30*time.Second)
					defer cancel()
					cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
					runtimeFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t, ctx, db, cleanup, "runtime-", false, nil,
					)
					runtimeActivated, err := runtimeFixture.policies.ActivatePullUpdaterOwnership(
						ctx, runtimeFixture.auth, runtimeFixture.updates, runtimeFixture.params,
					)
					if err != nil {
						t.Fatal(err)
					}
					ownershipFixture := newMariaDBFIX005PullFixtureWithCleanup(
						t, ctx, db, cleanup, "ownership-", false, nil,
					)
					var ownershipActivated ActivatePullUpdaterOwnershipResult
					if ownershipOperation == "deactivate" {
						ownershipActivated, err = ownershipFixture.policies.ActivatePullUpdaterOwnership(
							ctx, ownershipFixture.auth, ownershipFixture.updates, ownershipFixture.params,
						)
						if err != nil {
							t.Fatal(err)
						}
					}
					installMariaDBFIX009SystemUpdateJobLaneAnchors(
						t,
						ctx,
						db,
						cleanup,
						runtimeFixture.params.ExecutionHostID,
						ownershipFixture.params.ExecutionHostID,
					)
					runtimeOperation := prepareMariaDBFIX005RuntimeOperation(
						t,
						ctx,
						db,
						runtimeFixture,
						runtimeActivated,
						"claim_staged_credential",
					)
					runtimeExpected := mariaDBFIX007UpdaterRuntimeClaimExpectedCase()
					runtimePreState := snapshotMariaDBFIX007RuntimeSemanticState(
						t, ctx, db, runtimeFixture, runtimeOperation.rotationID,
					)
					assertMariaDBFIX007RuntimePreState(
						t, runtimeExpected, runtimeOperation, runtimePreState,
					)
					runtimeExpectedState, err := deriveMariaDBFIX008ExpectedRuntimeState(
						runtimeExpected, runtimeOperation, runtimePreState,
					)
					if err != nil {
						t.Fatal(err)
					}
					ownershipPreState := snapshotMariaDBFIX007OwnershipSemanticState(
						t, ctx, ownershipFixture,
					)
					blocker := lockMariaDBServiceTokenForTest(t, ctx, db, runtimeOperation.tokenID)
					defer blocker.Rollback()
					runtimePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					releaseRuntimeTokenLock := make(chan struct{})
					var releaseRuntimeTokenLockOnce sync.Once
					defer releaseRuntimeTokenLockOnce.Do(func() { close(releaseRuntimeTokenLock) })
					runtimeObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == runtimeOperation.name {
							runtimePhases <- phase
							if phase == mariaDBServiceTokenTokenLocksHeld {
								select {
								case <-releaseRuntimeTokenLock:
								case <-ctx.Done():
								}
							}
						}
					})
					runtimeLockPhases := make(chan mariaDBRuntimeTokenRotationLockPhase, 8)
					runtimeLockObserver := mariaDBRuntimeTokenRotationLockObserver(func(
						observedOperation string,
						phase mariaDBRuntimeTokenRotationLockPhase,
					) {
						if observedOperation == runtimeOperation.name {
							runtimeLockPhases <- phase
						}
					})
					runtimeCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBRuntimeTokenRotationLockObserverContextKey{},
							runtimeLockObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						runtimeObserver,
					)
					runtimeResult := make(chan mariaDBFIX007RuntimeOperationResult, 1)
					go func() {
						runtimeResult <- runtimeOperation.run(runtimeCtx)
					}()
					assertMariaDBFIX007RuntimeLockPhaseSequence(
						t,
						runtimeLockPhases,
						[]mariaDBRuntimeTokenRotationLockPhase{
							mariaDBRuntimeTokenRotationHostLocksHeld,
							mariaDBRuntimeTokenRotationRotationLocksHeld,
							mariaDBRuntimeTokenRotationLaneLocksHeld,
							mariaDBRuntimeTokenRotationPolicyLocksHeld,
						},
					)
					assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBeforeServiceLocks,
						mariaDBServiceTokenServiceLocksHeld,
						mariaDBServiceTokenBeforeTokenLocks,
					})
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, runtimeFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX007RuntimeRotationRowLockHeld(
						t, ctx, db, runtimeOperation.rotationID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, runtimeFixture.params.ServiceID,
					)
					assertMariaDBFIX006ServiceRowLockHeld(
						t, ctx, db, runtimeFixture.params.ServiceID,
					)

					observedOwnershipOperation := ownershipOperation + "_pull_updater_ownership"
					ownershipPolicyPhases := make(chan mariaDBUpdaterPolicyLockPhase, 8)
					ownershipPolicyObserver := mariaDBUpdaterPolicyLockObserver(func(
						operation string,
						phase mariaDBUpdaterPolicyLockPhase,
					) {
						if operation == observedOwnershipOperation {
							ownershipPolicyPhases <- phase
						}
					})
					ownershipServicePhases := make(chan mariaDBServiceTokenLockPhase, 8)
					ownershipServiceObserver := mariaDBServiceTokenLockObserver(func(
						operation string,
						phase mariaDBServiceTokenLockPhase,
					) {
						if operation == observedOwnershipOperation {
							ownershipServicePhases <- phase
						}
					})
					ownershipCtx := context.WithValue(
						context.WithValue(
							ctx,
							mariaDBUpdaterPolicyLockObserverContextKey{},
							ownershipPolicyObserver,
						),
						mariaDBServiceTokenLockObserverContextKey{},
						ownershipServiceObserver,
					)
					ownershipResult := make(chan mariaDBFIX005OwnershipResult, 1)
					go func() {
						if ownershipOperation == "activate" {
							result, err := ownershipFixture.policies.ActivatePullUpdaterOwnership(
								ownershipCtx,
								ownershipFixture.auth,
								ownershipFixture.updates,
								ownershipFixture.params,
							)
							ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
							return
						}
						result, err := ownershipFixture.policies.DeactivatePullUpdaterOwnership(
							ownershipCtx,
							ownershipFixture.auth,
							ownershipFixture.updates,
							deactivateMariaDBFIX005Params(ownershipActivated),
						)
						ownershipResult <- mariaDBFIX005OwnershipResult{ownership: result.Ownership, err: err}
					}()
					for index, want := range []mariaDBUpdaterPolicyLockPhase{
						mariaDBUpdaterPolicyBeforeHostLock,
						mariaDBUpdaterPolicyHostLockHeld,
						mariaDBUpdaterPolicyLaneLocksHeld,
						mariaDBUpdaterPolicyBeforePolicyLocks,
					} {
						got := receiveMariaDBFIX006UpdaterPolicyPhase(
							t, ownershipPolicyPhases, fmt.Sprintf("updater policy phase %d", index+1),
						)
						if got != want {
							t.Fatalf("updater policy phase %d = %q, want %q", index+1, got, want)
						}
					}
					assertMariaDBFIX006ExecutionHostRowLockHeld(
						t, ctx, db, ownershipFixture.params.ExecutionHostID,
					)
					assertMariaDBFIX006UpdaterPolicyRowLockHeld(
						t, ctx, db, runtimeFixture.params.ServiceID,
					)
					select {
					case phase := <-ownershipPolicyPhases:
						t.Fatalf("updater advanced past the contended policy phase before release: %q", phase)
					case outcome := <-ownershipResult:
						t.Fatalf("updater completed before the contended policy lock was released: %#v", outcome)
					default:
					}
					if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
						t.Fatal(err)
					}

					assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenTokenLocksHeld,
					})
					assertMariaDBFIX007ServiceTokenRowLockHeld(
						t, ctx, db, runtimeOperation.tokenID,
					)
					releaseRuntimeTokenLockOnce.Do(func() { close(releaseRuntimeTokenLock) })
					assertMariaDBFIX005PhaseSequence(t, runtimePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBindingsValidated,
					})
					runtimeOutcome := receiveMariaDBFIX007RuntimeOperationResult(
						t, runtimeResult, "runtime credential claim",
					)
					assertMariaDBServiceTokenOperationError(t, "runtime credential claim", runtimeOutcome.err)
					var ownershipOutcome mariaDBFIX005OwnershipResult
					select {
					case ownershipOutcome = <-ownershipResult:
					case <-time.After(10 * time.Second):
						t.Fatalf("%s did not complete", ownershipOperation)
					}
					assertMariaDBServiceTokenOperationError(t, ownershipOperation, ownershipOutcome.err)
					for index, want := range []mariaDBUpdaterPolicyLockPhase{
						mariaDBUpdaterPolicyPolicyLocksHeld,
						mariaDBUpdaterPolicyPolicySetRevalidated,
					} {
						got := receiveMariaDBFIX006UpdaterPolicyPhase(
							t, ownershipPolicyPhases, fmt.Sprintf("updater released policy phase %d", index+1),
						)
						if got != want {
							t.Fatalf("updater released policy phase %d = %q, want %q", index+1, got, want)
						}
					}
					assertMariaDBFIX005PhaseSequence(t, ownershipServicePhases, []mariaDBServiceTokenLockPhase{
						mariaDBServiceTokenBeforeServiceLocks,
						mariaDBServiceTokenServiceLocksHeld,
						mariaDBServiceTokenBeforeTokenLocks,
						mariaDBServiceTokenTokenLocksHeld,
						mariaDBServiceTokenBindingsValidated,
					})
					assertMariaDBFIX007StrongOwnershipFinalState(
						t,
						ctx,
						ownershipPreState,
						[]mariaDBFIX007OwnershipAction{{
							fixture:   ownershipFixture,
							operation: ownershipOperation,
							result:    ownershipOutcome.ownership,
						}},
						"",
						mariaDBServiceTokenMutationResult{},
					)
					assertMariaDBFIX007RuntimeExactFinalState(
						t,
						ctx,
						db,
						runtimeFixture,
						runtimeExpectedState,
						runtimeOutcome,
						mariaDBServiceTokenMutationResult{},
					)
				})
			}
		}
	}
}

func assertMariaDBFIX006ExecutionHostRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	executionHostID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT execution_host_id
FROM system_update_execution_hosts
WHERE execution_host_id = ?
FOR UPDATE NOWAIT`,
		executionHostID,
		"execution host",
	)
}

func assertMariaDBFIX006UpdaterPolicyRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT service_id FROM update_agent_policies
WHERE service_id = ?
FOR UPDATE NOWAIT`,
		serviceID,
		"updater policy",
	)
}

func assertMariaDBFIX006ServiceRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT service_id FROM services
WHERE service_id = ?
FOR UPDATE NOWAIT`,
		serviceID,
		"service",
	)
}

func assertMariaDBFIX007RuntimeRotationRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	rotationID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT id FROM system_update_runtime_token_rotations
WHERE id = ?
FOR UPDATE NOWAIT`,
		rotationID,
		"runtime rotation",
	)
}

func assertMariaDBFIX007ServiceTokenRowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	tokenID string,
) {
	t.Helper()
	assertMariaDBFIX006RowLockHeld(
		t,
		ctx,
		db,
		`SELECT id FROM service_tokens
WHERE id = ?
FOR UPDATE NOWAIT`,
		tokenID,
		"service token",
	)
}

func assertMariaDBFIX006RowLockHeld(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	query, id, label string,
) {
	t.Helper()
	probe, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	err = probe.QueryRowContext(ctx, query, id).Scan(&lockedID)
	_ = probe.Rollback()
	if err == nil {
		t.Fatalf("%s %s was not locked at the observed phase", label, id)
	}
	var driverErr *mysql.MySQLError
	if errors.As(err, &driverErr) && (driverErr.Number == 1205 || driverErr.Number == 3572) {
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("%s %s disappeared before the lock barrier", label, id)
	}
	t.Fatalf("probe %s row lock: %v", label, err)
}

func TestMariaDBFIX006FixtureCleanupLeavesNoResidue(t *testing.T) {
	db, ctx := openMariaDBFIX005Test(t)
	var fixture *mariaDBFIX005Cleanup
	t.Run("fixture", func(t *testing.T) {
		fixture = newMariaDBFIX005Cleanup(t, ctx, db)
		auth := NewMariaDBAuthStore(db)
		streams := NewMariaDBStreamStore(db)
		serviceID := fixture.prefix + "encoder"
		token := createMariaDBServiceTokenPairService(t, ctx, auth, serviceID, "encoder_recorder", nil, fixture)
		stream, err := streams.CreateStream(ctx, fixture.prefix+"cleanup")
		if err != nil {
			t.Fatal(err)
		}
		fixture.trackStreamID(stream.ID)
		if _, err := auth.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{
			ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary",
		}); err != nil {
			t.Fatal(err)
		}
		var assignmentID string
		if err := db.QueryRowContext(ctx, `SELECT id FROM stream_service_assignments
WHERE service_id = ? AND stream_id = ?`, serviceID, stream.ID).Scan(&assignmentID); err != nil {
			t.Fatal(err)
		}
		fixture.trackAssignmentID(assignmentID)
		if err := streams.WriteStreamArtifactReport(
			ctx,
			token,
			ServiceStreamEvent{
				ServiceID: serviceID,
				StreamID:  stream.ID,
				EventType: "archive.artifacts.reported",
			},
			[]StreamArtifact{{
				Kind: "archive", Name: "final.mp4",
				RelativePath: "final/" + stream.ID + "/final.mp4",
				SizeBytes:    1,
			}},
		); err != nil {
			t.Fatal(err)
		}
		artifacts, err := streams.ListStreamArtifacts(ctx, stream.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, artifact := range artifacts {
			fixture.trackArtifactID(artifact.ID)
		}
		eventIDs, err := queryMariaDBFIX005StringsByIDs(
			ctx, db, "service_stream_events", "id", "stream_id", []string{stream.ID},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, eventID := range eventIDs {
			fixture.trackEventID(eventID)
		}
	})
	if fixture == nil {
		t.Fatal("FIX-006 cleanup fixture was not created")
	}
	if count, err := fixture.residueCount(ctx); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Fatalf("FIX-006 cleanup left %d fixture rows", count)
	}
}
