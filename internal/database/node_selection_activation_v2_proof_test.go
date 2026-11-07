package database

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestNodeSelectionActivationV2OutcomeProof(t *testing.T) {
	complete := sql.NullTime{Time: time.Unix(1794040000, 0), Valid: true}
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, status, artifact, previous string
		want                             bool
	}{
		{"success uses artifact evidence", "succeeded", digest, "", true},
		{"rollback uses restored artifact evidence", "rolled_back", "", digest, true},
		{"success does not use rollback evidence", "succeeded", "", digest, false},
		{"rollback does not use candidate evidence", "rolled_back", digest, "", false},
		{"success rejects malformed artifact", "succeeded", "sha256:" + strings.Repeat("z", 64), digest, false},
		{"rollback rejects malformed restored digest", "rolled_back", digest, "short", false},
		{"failed is not proof", "failed", digest, digest, false},
		{"unknown is not proof", "unknown", digest, digest, false},
		{"pending is not proof", "installing", digest, digest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := nodeSelectionActivationJob{Status: tc.status, CompletedAt: complete, ArtifactDigest: tc.artifact, PreviousDigest: tc.previous}
			if got := nodeSelectionActivationTerminalProof(job); got != tc.want {
				t.Fatalf("canonical v2 %s evidence accepted=%v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
