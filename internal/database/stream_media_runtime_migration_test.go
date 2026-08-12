package database

import (
	"strings"
	"testing"
)

func TestStreamMediaRuntimeMigrationPersistsOnlyNonSecretNegotiationState(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/071_stream_media_runtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(body))
	for _, required := range []string{
		"create table if not exists stream_media_runtimes",
		"video_overlay_burn_in tinyint(1) not null default 0",
		"primary key (stream_id)",
		"foreign key (stream_id) references streams(id) on delete cascade",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{"passphrase ", "credential ", "token ", "secret "} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("media runtime migration must not persist %q: %s", forbidden, body)
		}
	}
}
