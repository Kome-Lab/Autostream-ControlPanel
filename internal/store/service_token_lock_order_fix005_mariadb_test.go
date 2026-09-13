package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/database"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
