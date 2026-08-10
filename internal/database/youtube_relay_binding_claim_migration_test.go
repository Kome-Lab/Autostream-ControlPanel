package database

import (
	"strings"
	"testing"
)

func TestStreamYouTubeRelayBindingClaimsMigrationKeepsBindingExclusiveAndSecretFree(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/066_stream_youtube_relay_binding_claims.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS youtube_relay_binding_revision BIGINT UNSIGNED NOT NULL DEFAULT 0",
		"UNIQUE KEY IF NOT EXISTS uq_profiles_youtube_relay_binding_revision (id, youtube_relay_binding_revision)",
		"relay_binding_id VARCHAR(128)",
		"reservation_token CHAR(36)",
		"youtube_output_id CHAR(36)",
		"youtube_output_revision BIGINT UNSIGNED NOT NULL",
		"reusable_live_stream_id VARCHAR(255)",
		"state ENUM('reserved','prepared','recovery_required')",
		"dispatch_state ENUM('not_dispatched','possibly_dispatched') NOT NULL DEFAULT 'not_dispatched'",
		"UNIQUE KEY uq_yt_relay_claim_stream (stream_id)",
		"UNIQUE KEY uq_yt_relay_claim_reservation_token (reservation_token)",
		"UNIQUE KEY uq_yt_relay_claim_live_stream (oauth_account_id, reusable_live_stream_id)",
		"FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE RESTRICT",
		"FOREIGN KEY (youtube_output_id) REFERENCES profiles(id) ON DELETE RESTRICT",
		"FOREIGN KEY (youtube_output_id, youtube_output_revision)",
		"REFERENCES profiles(id, youtube_relay_binding_revision)",
		"ON UPDATE RESTRICT ON DELETE RESTRICT",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("relay binding claim migration is missing %q:\n%s", required, text)
		}
	}
	if !strings.Contains(text, "COLLATE ascii_bin") {
		t.Fatalf("relay binding and YouTube IDs must preserve ASCII case:\n%s", text)
	}
	assertMigrationContainsNoRawSecrets(t, text)
}

// TestStreamYouTubeRelayBindingClaimPrepareFenceMigrationUpgradesApplied066
// protects the forward-only upgrade path: 066 may already be recorded on a
// WIP/CI database, so the new columns must be introduced by an idempotent 067
// rather than by rewriting the original migration.
func TestStreamYouTubeRelayBindingClaimPrepareFenceMigrationUpgradesApplied066(t *testing.T) {
	base, err := embeddedMigrations.ReadFile("migrations/066_stream_youtube_relay_binding_claims.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(base), "prepare_state") || strings.Contains(string(base), "encoder_stop_confirmed_at") {
		t.Fatalf("already-applied 066 must remain unchanged; forward fields belong in 067:\n%s", base)
	}

	body, err := embeddedMigrations.ReadFile("migrations/067_stream_youtube_relay_binding_claim_prepare_state.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"ALTER TABLE stream_youtube_relay_binding_claims",
		"ADD COLUMN IF NOT EXISTS prepare_state ENUM('not_attempted','possibly_prepared') NOT NULL DEFAULT 'not_attempted'",
		"ADD COLUMN IF NOT EXISTS encoder_stop_confirmed_at DATETIME(6) NULL",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("relay claim forward migration is missing %q:\n%s", required, text)
		}
	}
	for _, stmt := range splitSQLStatements(text) {
		if strings.Contains(strings.ToUpper(stmt), "ADD COLUMN") && !strings.Contains(strings.ToUpper(stmt), "ADD COLUMN IF NOT EXISTS") {
			t.Fatalf("067 must tolerate an interrupted/reapplied upgrade:\n%s", stmt)
		}
	}
	assertMigrationContainsNoRawSecrets(t, text)
}
