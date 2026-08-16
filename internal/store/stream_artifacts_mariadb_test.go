package store_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBStreamArtifactRereportPreservesIdentityAndShare(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	streams := store.NewMariaDBStreamStore(db)
	auth := store.NewMariaDBAuthStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	stream, err := streams.CreateStream(ctx, "artifact rereport "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	serviceID := "encoder-artifact-" + suffix
	token, err := auth.CreateServiceToken(ctx, "encoder_recorder", []string{"service.register", "encoder.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{
		ServiceID:   serviceID,
		ServiceType: "encoder_recorder",
		ServiceName: "Artifact encoder " + suffix,
		PublicURL:   "https://" + serviceID + ".example.com",
	}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AssignServiceToStream(ctx, serviceID, stream.ID, ""); err != nil {
		t.Fatal(err)
	}
	artifact := store.StreamArtifact{
		Kind:         "archive",
		Name:         "final.mp4",
		RelativePath: fmt.Sprintf("final/%s/final.mp4", stream.ID),
		SizeBytes:    123,
	}
	event := store.ServiceStreamEvent{
		ServiceID: serviceID,
		StreamID:  stream.ID,
		EventType: "archive.artifacts.reported",
		Payload:   map[string]any{"artifact_count": 1},
	}
	if err := streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	first, err := streams.ListStreamArtifacts(ctx, stream.ID)
	if err != nil || len(first) != 1 {
		t.Fatalf("first artifact report: artifacts=%#v err=%v", first, err)
	}
	share, err := streams.CreateStreamArtifactShare(ctx, store.StreamArtifactShare{
		StreamID:      stream.ID,
		ArtifactID:    first[0].ID,
		TokenHash:     strings.Repeat("b", 64-len(suffix)) + suffix,
		AllowDownload: true,
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	artifact.SizeBytes = 456
	if err := streams.WriteStreamArtifactReport(ctx, token, event, []store.StreamArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	artifact.SizeBytes = 789
	if err := streams.UpsertStreamArtifacts(ctx, stream.ID, []store.StreamArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	second, err := streams.ListStreamArtifacts(ctx, stream.ID)
	if err != nil || len(second) != 1 {
		t.Fatalf("re-reported artifact: artifacts=%#v err=%v", second, err)
	}
	if second[0].ID != first[0].ID || !second[0].CreatedAt.Equal(first[0].CreatedAt) || second[0].SizeBytes != 789 {
		t.Fatalf("artifact identity changed after re-report: before=%#v after=%#v", first[0], second[0])
	}
	resolved, err := streams.GetStreamArtifactShareByTokenHash(ctx, share.TokenHash)
	if err != nil || resolved.ArtifactID != second[0].ID {
		t.Fatalf("artifact share no longer resolves: share=%#v err=%v", resolved, err)
	}
	if err := streams.UpsertStreamArtifacts(ctx, stream.ID, []store.StreamArtifact{{
		Kind:         "metadata",
		Name:         "metadata.json",
		RelativePath: fmt.Sprintf("final/%s/metadata.json", stream.ID),
		SizeBytes:    1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := streams.DeleteStream(ctx, stream.ID); err != nil {
		t.Fatal(err)
	}
	archiveStreams, err := streams.ListArchiveStreams(ctx)
	if err != nil || !streamListContains(archiveStreams, stream.ID) {
		t.Fatalf("deleted stream with recording missing from archive list: streams=%#v err=%v", archiveStreams, err)
	}
	if err := streams.DeleteStreamArtifact(ctx, stream.ID, second[0].ID); err != nil {
		t.Fatal(err)
	}
	archiveStreams, err = streams.ListArchiveStreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if streamListContains(archiveStreams, stream.ID) {
		t.Fatalf("deleted stream with only metadata remains in archive list: %#v", archiveStreams)
	}
	remaining, err := streams.ListStreamArtifacts(ctx, stream.ID)
	if err != nil || len(remaining) != 1 || remaining[0].Kind != "metadata" {
		t.Fatalf("metadata retention after recording deletion: artifacts=%#v err=%v", remaining, err)
	}
}

func streamListContains(streams []store.Stream, streamID string) bool {
	for _, stream := range streams {
		if stream.ID == streamID {
			return true
		}
	}
	return false
}
