package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Included by the ordinary CI's mandatory TestMariaDB witness discovery. This
// exercises production SQL reservations and current-binding readers; it is not
// substituted by the in-memory HTTP regression when a DSN is unavailable.
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
	// A profile owner changed after the HTTP observation cannot reserve the
	// formerly selected Bot or leave the required media nodes partially owned.
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
	for _, id := range a.serviceIDs() {
		service, err := f.stores.Services.GetService(t.Context(), id)
		if err != nil || service.CurrentStreamID != "" {
			t.Fatal("rejected create partially reserved a service")
		}
	}
	if _, err := f.stores.Profiles.UpdateProfile(t.Context(), ProfileDiscordConfig, f.profile.ID, f.profile.Name, f.profile.Config); err != nil {
		t.Fatal(err)
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
	read := make(chan outcome, 1)
	go func() { s, e := f.stores.Snapshot(ctx, f.serviceID, f.token.ID); read <- outcome{s, e} }()
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
	select {
	case result := <-read:
		if result.err != nil || len(result.snapshot.Bindings) != 1 || !result.snapshot.Bindings[0].Ready || result.snapshot.Bindings[0].StreamID != streamID || !result.snapshot.Bindings[0].AssignmentEpoch.Equal(result.snapshot.Assignments[0].AssignedAt) {
			t.Fatal("concurrent current snapshot did not converge to the committed waiting Bot", result.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The settings commit also rejects the same owner race and preserves its
	// last committed configuration, with the existing primary still in place.
	if _, err := f.stores.Profiles.UpdateProfile(t.Context(), ProfileDiscordConfig, f.profile.ID, f.profile.Name, changed); err != nil {
		t.Fatal(err)
	}
	expected := f.serviceID
	if _, err := f.stores.Streams.UpdateStreamSettings(t.Context(), streamID, StreamSettings{Name: "must not commit", DiscordConfigID: f.profile.ID, ExpectedDiscordServiceID: &expected}); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("stale settings owner error=%v", err)
	}
	current, err := f.stores.Streams.GetStream(t.Context(), streamID)
	if err != nil || current.Name != "waiting selected Bot" || current.AutoStartTrigger != "discord_voice_join" {
		t.Fatal("stale owner settings mutated committed data")
	}
}
