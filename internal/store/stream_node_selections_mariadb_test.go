package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBSharedNodeSelectionsPreserveRunningOwner(t *testing.T) {
	db, ctx := openSharedNodeSelectionMariaDBTest(t)
	streams := store.NewMariaDBStreamStore(db)
	auth := store.NewMariaDBAuthStore(db)
	profiles := store.NewMariaDBProfileStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	encoderID, workerID, botID := "selected-encoder-"+suffix, "selected-worker-"+suffix, "selected-bot-"+suffix
	for _, service := range []struct{ id, kind string }{
		{encoderID, "encoder_recorder"}, {workerID, "worker"}, {botID, "discord_bot"},
	} {
		registerMariaDBAssignmentService(t, ctx, auth, service.id, service.kind)
	}
	ownerProfile, err := profiles.CreateProfile(ctx, store.ProfileDiscordConfig, "selection owner "+suffix, map[string]any{
		"service_id": botID, "guild_id": "owner-guild", "voice_channel_id": "owner-vc",
	})
	if err != nil {
		t.Fatal(err)
	}
	draftProfile, err := profiles.CreateProfile(ctx, store.ProfileDiscordConfig, "selection draft "+suffix, map[string]any{
		"service_id": botID, "guild_id": "draft-guild", "voice_channel_id": "draft-vc",
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := streams.CreateStream(ctx, "running selection owner "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = streams.UpdateStreamSettings(ctx, owner.ID, store.StreamSettings{
		Name: owner.Name, DiscordConfigID: ownerProfile.ID, ExpectedDiscordServiceID: &botID,
		NodeSelections: &store.StreamNodeSelectionUpdate{
			EncoderServiceID: &encoderID, WorkerServiceID: &workerID, DiscordServiceID: &botID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, serviceID := range []string{encoderID, workerID, botID} {
		if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{
			ServiceID: serviceID, StreamID: owner.ID, AssignmentRole: "primary",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := streams.SetStreamVideoOverlayBurnIn(ctx, owner.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := streams.SaveStreamYouTubeRuntime(ctx, store.StreamYouTubeRuntime{
		StreamID: owner.ID, YouTubeOutput: "selection-output", Mode: "live_api",
		BroadcastID: "synthetic-broadcast", LiveStreamID: "synthetic-live-stream",
		StreamKeySecretName: "synthetic-key-reference", CompleteOnStop: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, owner.ID, "live"); err != nil {
		t.Fatal(err)
	}
	snapshotOwner := func() map[string][][]string {
		t.Helper()
		serviceIDs := []any{encoderID, workerID, botID}
		return map[string][][]string{
			"stream":      sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM streams WHERE id=?`, owner.ID),
			"settings":    sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM stream_settings WHERE stream_id=?`, owner.ID),
			"choices":     sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM stream_node_selections WHERE stream_id=? ORDER BY service_type`, owner.ID),
			"services":    sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM services WHERE service_id IN (?,?,?) ORDER BY service_id`, serviceIDs...),
			"assignments": sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM stream_service_assignments WHERE service_id IN (?,?,?) ORDER BY id`, serviceIDs...),
			"tokens": sharedNodeSelectionSQLRows(t, ctx, db, `SELECT tokens.* FROM service_tokens tokens
JOIN services s ON s.token_id=tokens.id WHERE s.service_id IN (?,?,?) ORDER BY tokens.id`, serviceIDs...),
			"media":   sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM stream_media_runtimes WHERE stream_id=?`, owner.ID),
			"youtube": sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM stream_youtube_runtimes WHERE stream_id=?`, owner.ID),
			"profile": sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM profiles WHERE id=?`, ownerProfile.ID),
		}
	}
	before := snapshotOwner()
	assertOwner := func() {
		t.Helper()
		after := snapshotOwner()
		for table, expected := range before {
			if !reflect.DeepEqual(after[table], expected) {
				// Never print row snapshots: they intentionally include token storage.
				t.Fatalf("saving draft choices changed the running owner's %s rows", table)
			}
		}
	}
	choices := store.StreamCreateAssignments{
		EncoderServiceID: encoderID, WorkerServiceID: workerID,
		DiscordServiceID: botID, DiscordConfigID: draftProfile.ID,
	}
	draft := createSharedNodeSelectionMariaDBStream(t, ctx, db, choices, draftProfile.ID, "draft B "+suffix)
	second := createSharedNodeSelectionMariaDBStream(t, ctx, db, choices, draftProfile.ID, "draft C "+suffix)
	if !draft.NodeSelectionsConfigured || draft.SelectedEncoderID != encoderID || draft.SelectedWorkerID != workerID || draft.SelectedDiscordID != botID {
		t.Fatal("atomic creation did not retain all three shared choices")
	}
	for _, streamID := range []string{draft.ID, second.ID} {
		rows := sharedNodeSelectionSQLRows(t, ctx, db, `SELECT id FROM stream_service_assignments WHERE stream_id=?`, streamID)
		if len(rows) != 0 {
			t.Fatal("saving choices created an execution assignment")
		}
	}
	bindings, err := streams.ListStreamNodeSelectionsForService(ctx, botID)
	if err != nil || len(bindings) != 3 {
		t.Fatalf("shared bot selection binding count=%d err=%v", len(bindings), err)
	}
	assertOwner()

	staleRevision := draft.SettingsRevision
	empty := ""
	draft, err = streams.UpdateStreamSettings(ctx, draft.ID, store.StreamSettings{
		Name: "edited B", DiscordConfigID: draftProfile.ID,
		ExpectedSettingsRevision: &staleRevision, ExpectedDiscordServiceID: &botID,
		NodeSelections: &store.StreamNodeSelectionUpdate{WorkerServiceID: &empty},
	})
	if err != nil {
		t.Fatalf("clear a saved worker owned by another live stream: %v", err)
	}
	selected, err := streams.GetStreamNodeSelections(ctx, draft.ID)
	if err != nil || selected.EncoderServiceID != encoderID || selected.WorkerServiceID != "" || selected.DiscordServiceID != botID {
		t.Fatalf("omitted-vs-clear preference was lost: %v", err)
	}
	body, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if value, exists := wire["selected_worker_id"]; !exists || value != "" {
		t.Fatal("explicit selection clear disappeared from the stream JSON response")
	}
	_, err = streams.UpdateStreamSettings(ctx, draft.ID, store.StreamSettings{
		Name: "stale overwrite", DiscordConfigID: draftProfile.ID,
		ExpectedSettingsRevision: &staleRevision, ExpectedDiscordServiceID: &botID,
		NodeSelections: &store.StreamNodeSelectionUpdate{WorkerServiceID: &workerID},
	})
	if !errors.Is(err, store.ErrRevisionConflict) {
		t.Fatalf("stale selection revision was accepted: %v", err)
	}
	_, err = streams.UpdateStreamSettings(ctx, draft.ID, store.StreamSettings{
		Name: "wrong type", DiscordConfigID: draftProfile.ID,
		ExpectedSettingsRevision: &draft.SettingsRevision, ExpectedDiscordServiceID: &botID,
		NodeSelections: &store.StreamNodeSelectionUpdate{EncoderServiceID: &workerID},
	})
	if !errors.Is(err, store.ErrInvalidServiceAssignment) {
		t.Fatalf("wrong selected Node type was accepted: %v", err)
	}
	retained, err := streams.GetStream(ctx, draft.ID)
	if err != nil || !reflect.DeepEqual(retained, draft) {
		t.Fatalf("rejected selection update changed the draft or its revision: %v", err)
	}
	assertOwner()

	currentRevision := draft.SettingsRevision
	draft, err = streams.UpdateStreamSettings(ctx, draft.ID, store.StreamSettings{
		Name: "all choices explicitly cleared", ExpectedSettingsRevision: &currentRevision,
		ExpectedDiscordServiceID: &empty,
		NodeSelections: &store.StreamNodeSelectionUpdate{
			EncoderServiceID: &empty, WorkerServiceID: &empty, DiscordServiceID: &empty,
		},
	})
	if err != nil {
		t.Fatalf("clear all saved choices: %v", err)
	}
	draft, err = streams.GetStream(ctx, draft.ID)
	if err != nil || !draft.NodeSelectionsConfigured ||
		draft.SelectedEncoderID != "" || draft.SelectedWorkerID != "" || draft.SelectedDiscordID != "" {
		t.Fatalf("all-clear lost its configured marker after reload: %v", err)
	}
	body, err = json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"selected_encoder_id", "selected_worker_id", "selected_discord_id"} {
		if value, exists := wire[field]; !exists || value != "" {
			t.Fatalf("all-clear omitted explicit JSON field %s", field)
		}
	}
	tombstones := sharedNodeSelectionSQLRows(t, ctx, db, `SELECT service_type,service_id FROM stream_node_selections WHERE stream_id=? ORDER BY service_type`, draft.ID)
	if len(tombstones) != 3 {
		t.Fatal("all-clear became indistinguishable from a legacy unconfigured stream")
	}
	for _, row := range tombstones {
		if len(row) != 2 || row[1] != "" {
			t.Fatal("all-clear retained a saved Node")
		}
	}
	assertOwner()

	if err := streams.DeleteStream(ctx, draft.ID); err != nil {
		t.Fatalf("delete draft with shared Node choices: %v", err)
	}
	rows := sharedNodeSelectionSQLRows(t, ctx, db, `SELECT service_id FROM stream_node_selections WHERE stream_id=?`, draft.ID)
	if len(rows) != 0 {
		t.Fatal("deleted draft retained its saved selection bindings")
	}
	bindings, err = streams.ListStreamNodeSelectionsForService(ctx, botID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("deleting B changed another frame's saved choice: count=%d err=%v", len(bindings), err)
	}
	assertOwner()
}

func TestMariaDBExplicitNodeSelectionClearCannotClaimLegacyAssignments(t *testing.T) {
	db, ctx := openSharedNodeSelectionMariaDBTest(t)
	streams := store.NewMariaDBStreamStore(db)
	auth := store.NewMariaDBAuthStore(db)
	profiles := store.NewMariaDBProfileStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	encoderID, workerID, botID := "cleared-encoder-"+suffix, "cleared-worker-"+suffix, "cleared-bot-"+suffix
	for _, service := range []struct{ id, kind string }{
		{encoderID, "encoder_recorder"}, {workerID, "worker"}, {botID, "discord_bot"},
	} {
		registerMariaDBAssignmentService(t, ctx, auth, service.id, service.kind)
	}
	profile, err := profiles.CreateProfile(ctx, store.ProfileDiscordConfig, "cleared choices "+suffix, map[string]any{"service_id": botID})
	if err != nil {
		t.Fatal(err)
	}
	choices := store.StreamCreateAssignments{
		EncoderServiceID: encoderID, WorkerServiceID: workerID,
		DiscordServiceID: botID, DiscordConfigID: profile.ID,
	}
	stream := createSharedNodeSelectionMariaDBStream(t, ctx, db, choices, profile.ID, "old failed runtime "+suffix)
	for _, serviceID := range []string{encoderID, workerID, botID} {
		if _, err := auth.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{
			ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	empty := ""
	stream, err = streams.UpdateStreamSettings(ctx, stream.ID, store.StreamSettings{
		ExpectedSettingsRevision: &stream.SettingsRevision, ExpectedDiscordServiceID: &empty,
		NodeSelections: &store.StreamNodeSelectionUpdate{
			EncoderServiceID: &empty, WorkerServiceID: &empty, DiscordServiceID: &empty,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = streams.GetStream(ctx, stream.ID)
	if err != nil || !stream.NodeSelectionsConfigured {
		t.Fatalf("all-clear lost its configured authority after reload: %v", err)
	}
	legacyAssignments, err := auth.ListStreamAssignments(ctx, stream.ID)
	if err != nil || len(legacyAssignments) != 3 {
		t.Fatalf("clearing preferences changed retained execution assignments: count=%d err=%v", len(legacyAssignments), err)
	}
	serviceIDs := []any{encoderID, workerID, botID}
	beforeStream := sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM streams WHERE id=?`, stream.ID)
	beforeServices := sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM services WHERE service_id IN (?,?,?) ORDER BY service_id`, serviceIDs...)
	beforeAssignments := sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM stream_service_assignments WHERE stream_id=? ORDER BY id`, stream.ID)
	emptyChoices := store.StreamNodeSelections{}
	_, err = streams.ClaimStreamStart(ctx, store.StreamStartClaimRequest{
		StreamID: stream.ID, ExpectedStatus: stream.Status, ExpectedStreamUpdatedAt: stream.UpdatedAt,
		ExpectedPrimaryAssignments: legacyAssignments, ExpectedNodeSelections: &emptyChoices,
		ArchiveEnabled: true, ArchiveStartedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if !errors.Is(err, store.ErrServiceAssignmentConflict) && !errors.Is(err, store.ErrInvalidServiceAssignment) {
		t.Fatalf("all-clear fell back to old runtime assignments: %v", err)
	}
	if !reflect.DeepEqual(sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM streams WHERE id=?`, stream.ID), beforeStream) ||
		!reflect.DeepEqual(sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM services WHERE service_id IN (?,?,?) ORDER BY service_id`, serviceIDs...), beforeServices) ||
		!reflect.DeepEqual(sharedNodeSelectionSQLRows(t, ctx, db, `SELECT * FROM stream_service_assignments WHERE stream_id=? ORDER BY id`, stream.ID), beforeAssignments) {
		t.Fatal("rejected all-clear start changed lifecycle, archive, or runtime authority")
	}
}

func openSharedNodeSelectionMariaDBTest(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
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

func createSharedNodeSelectionMariaDBStream(t *testing.T, ctx context.Context, db *sql.DB, choices store.StreamCreateAssignments, profileID, name string) store.Stream {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var streamID string
	if err := tx.QueryRowContext(ctx, `SELECT UUID()`).Scan(&streamID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := tx.ExecContext(ctx, `INSERT INTO streams(id,name,status,created_at,updated_at) VALUES(?,?,'created',?,?)`, streamID, name, now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignNewStreamServicesTx(ctx, tx, streamID, "", choices, now); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStreamSettingsTx(ctx, tx, streamID, store.StreamSettings{
		Name: name, DiscordConfigID: profileID, ExpectedDiscordServiceID: &choices.DiscordServiceID,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stream, err := store.NewMariaDBStreamStore(db).GetStream(ctx, streamID)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func sharedNodeSelectionSQLRows(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) [][]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot [][]string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		record := make([]string, len(values))
		for index, value := range values {
			switch value := value.(type) {
			case nil:
				record[index] = "<NULL>"
			case []byte:
				record[index] = string(value)
			case time.Time:
				record[index] = value.UTC().Format(time.RFC3339Nano)
			default:
				record[index] = fmt.Sprint(value)
			}
		}
		snapshot = append(snapshot, record)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
