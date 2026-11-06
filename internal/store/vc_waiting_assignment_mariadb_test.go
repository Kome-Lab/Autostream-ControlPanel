package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Included by the ordinary CI's mandatory TestMariaDB witness discovery. This
// exercises production SQL selections, monitoring and current-binding readers.
// It is not substituted by the in-memory HTTP regression without a DSN.
func TestMariaDBVCWaitingCreateRevalidatesProfileAndCurrentSnapshot(t *testing.T) {
	db := cb15TestDB(t)
	f := cb15StoreFixture(t, db)
	if _, err := f.stores.Services.UnassignServiceFromStreamGuarded(t.Context(), ServiceUnassignmentMutation{ServiceID: f.serviceID}); err != nil {
		t.Fatal(err)
	}
	register := func(kind string) string {
		t.Helper()
		id := "vc-waiting-" + kind + "-" + newUUID()
		token, err := f.stores.Services.CreateServiceToken(t.Context(), kind, []string{"service.register"})
		if err != nil {
			t.Fatal(err)
		}
		r := ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: id, PublicURL: "https://vc-waiting.example.com", Version: "test"}
		if _, err := f.stores.Services.PrecreateService(t.Context(), token, r); err != nil {
			t.Fatal(err)
		}
		if _, err := f.stores.Services.RegisterService(t.Context(), token, r); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := StreamCreateAssignments{EncoderServiceID: register("encoder_recorder"), WorkerServiceID: register("worker"), DiscordServiceID: f.serviceID, DiscordConfigID: f.profile.ID}
	create := func(ctx context.Context, id string) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO streams(id,name,status,created_at,updated_at)VALUES(?,?,'created',?,?)`, id, "waiting selected Bot", now, now); err != nil {
			return err
		}
		if err := AssignNewStreamServicesTx(ctx, tx, id, "", a, now); err != nil {
			return err
		}
		if err := UpsertStreamSettingsTx(ctx, tx, id, StreamSettings{DiscordConfigID: f.profile.ID, AutoStartTrigger: "discord_voice_join"}, now); err != nil {
			return err
		}
		return tx.Commit()
	}
	// A profile owner changed after the HTTP observation cannot save the
	// formerly selected Bot or leave partial choices, settings, or execution owners.
	changed := copyConfig(f.profile.Config)
	changed["service_id"] = "different-bot"
	if _, err := f.stores.Profiles.UpdateProfile(t.Context(), ProfileDiscordConfig, f.profile.ID, f.profile.Name, changed); err != nil {
		t.Fatal(err)
	}
	rejectedID := newUUID()
	if err := create(t.Context(), rejectedID); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("stale selected profile error=%v", err)
	}
	if _, err := f.stores.Streams.GetStream(t.Context(), rejectedID); !errors.Is(err, ErrNotFound) {
		t.Fatal("rejected create retained a stream row")
	}
	for _, table := range []string{"stream_node_selections", "stream_settings", "stream_service_assignments"} {
		var rows int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE stream_id=?", rejectedID).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("rejected create retained %s rows: count=%d err=%v", table, rows, err)
		}
	}
	for _, id := range a.serviceIDs() {
		service, err := f.stores.Services.GetService(t.Context(), id)
		if err != nil || service.CurrentStreamID != "" {
			t.Fatal("rejected create partially reserved a service")
		}
	}
	if _, err := f.stores.Profiles.UpdateProfile(t.Context(), ProfileDiscordConfig, f.profile.ID, f.profile.Name, f.profile.Config); err != nil {
		t.Fatal(err)
	}
	beforeServices := make(map[string]RegisteredService)
	for _, id := range a.serviceIDs() {
		service, err := f.stores.Services.GetService(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		beforeServices[id] = service
	}
	assertUnassigned := func() {
		t.Helper()
		for _, id := range a.serviceIDs() {
			service, err := f.stores.Services.GetService(t.Context(), id)
			if err != nil || service.CurrentStreamID != "" || !reflect.DeepEqual(service, beforeServices[id]) {
				t.Fatal("waiting save or monitoring changed a service's execution authority", err)
			}
			assignments, err := f.stores.Services.ListServiceAssignmentsForService(t.Context(), id)
			if err != nil || len(assignments) != 0 {
				t.Fatal("waiting save or monitoring created an execution assignment", err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var locked string
	if err := blocker.QueryRowContext(ctx, `SELECT id FROM profiles WHERE id=? FOR UPDATE`, f.profile.ID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	streamID := newUUID()
	created := make(chan error, 1)
	go func() { created <- create(ctx, streamID) }()
	// A real NOWAIT probe proves creation holds the canonical service rows
	// while blocked at its selected-profile phase, rather than a start barrier.
	waitForMariaDBServiceRowLock(t, ctx, db, f.serviceID)
	type outcome struct {
		snapshot CurrentDiscordCredentialSnapshot
		err      error
	}
	type waitingOutcome struct {
		snapshot DiscordWaitingCredentialSnapshot
		err      error
	}
	read := make(chan outcome, 1)
	waitingRead := make(chan waitingOutcome, 1)
	go func() { s, e := f.stores.Snapshot(ctx, f.serviceID, f.token.ID); read <- outcome{s, e} }()
	go func() {
		s, e := f.stores.WaitingDiscordSnapshot(ctx, f.serviceID, f.token.ID)
		waitingRead <- waitingOutcome{s, e}
	}()
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-created:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	committed, err := f.stores.Streams.GetStream(ctx, streamID)
	if err != nil || !committed.NodeSelectionsConfigured ||
		committed.SelectedEncoderID != a.EncoderServiceID || committed.SelectedWorkerID != a.WorkerServiceID ||
		committed.SelectedDiscordID != f.serviceID || committed.DiscordConfigID != f.profile.ID ||
		committed.AutoStartTrigger != "discord_voice_join" {
		t.Fatal("atomic waiting create did not retain the committed choices and settings", err)
	}
	selectionEpoch, err := time.Parse(time.RFC3339Nano, committed.SettingsRevision)
	if err != nil || selectionEpoch.IsZero() {
		t.Fatal("committed waiting settings have no selection epoch", err)
	}
	select {
	case result := <-read:
		if result.err != nil || result.snapshot.Service.CurrentStreamID != "" ||
			len(result.snapshot.Assignments) != 0 || len(result.snapshot.Bindings) != 0 {
			t.Fatal("concurrent current snapshot synthesized execution authority from a waiting choice", result.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var waitingBinding DiscordWaitingCredentialBinding
	select {
	case result := <-waitingRead:
		if result.err != nil || result.snapshot.ExecutionActive || result.snapshot.Service.CurrentStreamID != "" ||
			len(result.snapshot.Bindings) != 1 || len(result.snapshot.Profiles) != 1 {
			t.Fatal("concurrent waiting snapshot did not converge to the committed monitoring authority", result.err)
		}
		waitingBinding = result.snapshot.Bindings[0]
		if !waitingBinding.Ready || waitingBinding.ReadinessCode != "" ||
			waitingBinding.StreamID != streamID || waitingBinding.ServiceID != f.serviceID || waitingBinding.ServiceType != "discord_bot" ||
			!waitingBinding.SelectionEpoch.Equal(selectionEpoch) || waitingBinding.ProfileID != f.profile.ID ||
			waitingBinding.ProfileOwnerServiceID != f.serviceID || waitingBinding.ProfileRevision == "" ||
			waitingBinding.SecretName != f.secret.Name || waitingBinding.Fingerprint != f.secret.Fingerprint ||
			result.snapshot.Profiles[0].ID != f.profile.ID {
			t.Fatal("waiting monitoring authority did not match the committed selection and profile")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertUnassigned()
	value, lease, err := f.stores.ResolveWaitingDiscordBinding(ctx, f.serviceID, f.token.ID, waitingBinding, time.Minute)
	if err != nil || value != "synthetic-cb15-current-value" || lease.ServiceID != f.serviceID ||
		lease.TokenID != f.token.ID || lease.StreamID != "" || lease.ArchiveProfileID != "" {
		t.Fatal("committed waiting authority did not resolve with service-level lease scope", err)
	}
	if err := f.stores.Leases.ReleaseRuntimeSecretLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	assertUnassigned()
	// The settings commit also rejects the same owner race and preserves the
	// last committed choices and settings, with execution still unassigned.
	if _, err := f.stores.Profiles.UpdateProfile(t.Context(), ProfileDiscordConfig, f.profile.ID, f.profile.Name, changed); err != nil {
		t.Fatal(err)
	}
	expected := f.serviceID
	if _, err := f.stores.Streams.UpdateStreamSettings(t.Context(), streamID, StreamSettings{Name: "must not commit", DiscordConfigID: f.profile.ID, ExpectedDiscordServiceID: &expected}); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("stale settings owner error=%v", err)
	}
	current, err := f.stores.Streams.GetStream(t.Context(), streamID)
	if err != nil || !reflect.DeepEqual(current, committed) {
		t.Fatal("stale owner settings mutated committed choices or settings", err)
	}
	if _, _, err := f.stores.ResolveWaitingDiscordBinding(ctx, f.serviceID, f.token.ID, waitingBinding, time.Minute); !errors.Is(err, ErrServiceAssignmentConflict) && !errors.Is(err, ErrForbidden) {
		t.Fatal("old monitoring authority resolved after its profile owner changed", err)
	}
	invalidated, err := f.stores.WaitingDiscordSnapshot(ctx, f.serviceID, f.token.ID)
	if err != nil || invalidated.ExecutionActive || len(invalidated.Bindings) != 1 ||
		invalidated.Bindings[0].StreamID != streamID || invalidated.Bindings[0].Ready || invalidated.Bindings[0].ReadinessCode == "" {
		t.Fatal("changed profile owner retained ready waiting authority", err)
	}
	assertUnassigned()
}
