package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

func TestPollingActualUIHTTPMariaDBCreateAndUploadRecovery(t *testing.T) {
	if os.Getenv("AUTOSTREAM_SQL_UI_BROWSER") != "1" {
		t.Skip("explicit SQL browser witness stage only")
	}
	if strings.TrimSpace(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")) == "" {
		t.Fatal("required SQL browser witness has no isolated database configuration")
	}
	runPollingActualUIBrowser(t, true)
}

func TestPollingActualUIHTTPMemoryCreateAcrossRefresh(t *testing.T) {
	if os.Getenv("AUTOSTREAM_MEMORY_UI_BROWSER") != "1" {
		t.Skip("explicit focused memory browser witness only")
	}
	runPollingActualUIBrowser(t, false)
}

func runPollingActualUIBrowser(t *testing.T, maria bool) {
	t.Helper()
	st := preparationClaimStores(t, maria)
	password := outputSecretRandom(t)
	user, err := st.auth.CreateFirstAdmin(t.Context(), "polling-browser-admin", password, security.DefaultPermissions)
	if err != nil {
		t.Fatal(err)
	}
	var media mediaassets.Repository
	var visual interface {
		Get(context.Context, string) (streamvisual.Settings, error)
	}
	var visualOption, targetOption, coverOption ServerOption
	kind := "MEMORY"
	if maria {
		sqlMedia, createErr := mediaassets.NewMariaDBRepository(st.db, t.TempDir())
		err = createErr
		if err != nil {
			t.Fatal(err)
		}
		media = sqlMedia
		repository := streamvisual.NewMariaDBRepository(st.db, sqlMedia)
		visual, visualOption = repository, WithStreamVisualRepository(repository)
		targetOption = WithDiscordTargetPresetStore(store.NewMariaDBDiscordTargetPresetStore(st.db))
		coverOption = WithVideoCoverRepository(videocover.NewMariaDBRepository(st.db))
		kind = "SQL"
	} else {
		media, err = mediaassets.NewMemoryRepository(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		repository := streamvisual.NewMemoryRepository(st.streams)
		visual, visualOption = repository, WithStreamVisualRepository(repository)
		targetOption = WithDiscordTargetPresetStore(store.NewMemoryDiscordTargetPresetStore())
		coverOption = func(*Server) {}
	}
	handler := NewServer(st.streams, WithAuthStore(st.auth), WithServiceRegistryStore(st.services), WithAuditStore(st.audit), WithProfileStore(st.profiles), WithSecretStore(st.secrets), WithRuntimeSecretLeaseStore(st.leases), WithMediaAssetRepository(media), visualOption, targetOption, coverOption)
	encoder := p2RegisterSQLService(t, st.services, "encoder_recorder")
	worker := p2RegisterSQLService(t, st.services, "worker")
	cookie, csrf := loginForTest(t, handler, "polling-browser-admin", password)
	// Synthetic session injection is confined to this disposable loopback test.
	// Production authentication/permissions/CSRF and every handler/store still run.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__polling-fixture" {
			writeJSON(w, http.StatusOK, map[string]any{"csrf_token": csrf, "encoder": encoder, "worker": worker, "with_images": maria, "store_kind": kind})
			return
		}
		if !maria && r.Method == http.MethodGet && r.URL.Path == "/video-cover-presets" {
			writeJSON(w, http.StatusOK, []any{})
			return
		}
		r.AddCookie(cookie)
		handler.ServeHTTP(w, r)
	}))
	defer api.Close()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("the required browser stage must provision Node")
	}
	webRoot, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--no-warnings", "tests/stream-create-sql-browser.mts")
	command.Dir = webRoot
	command.Env = append(os.Environ(), "AUTOSTREAM_SQL_UI_API_ORIGIN="+api.URL, "NEXT_PUBLIC_AUTOSTREAM_DEMO=false", "NODE_OPTIONS=--max-old-space-size=1536", "UV_THREADPOOL_SIZE=1")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err = command.Run(); err != nil {
		t.Fatalf("actual browser/handler/SQL witness failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), `"status":"PASS_REAL_UI_HTTP_`+kind+`_FIXTURE"`) {
		t.Fatal("browser did not report a positive execution receipt")
	}
	rows, err := st.streams.ListStreams(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Name != "SQL browser actual payload" {
		t.Fatal("real UI did not create exactly one persistent stream")
	}
	settings, err := visual.Get(t.Context(), rows[0].ID)
	wantCandidates := 0
	if maria {
		wantCandidates = 4
	}
	if err != nil || len(settings.CoverCandidates) != wantCandidates {
		t.Fatal("actual browser candidates did not persist")
	}
	assignments, err := st.services.ListStreamAssignments(t.Context(), rows[0].ID)
	if err != nil || len(assignments) != 2 {
		t.Fatal("actual browser primary assignments did not commit atomically")
	}
	for _, assignment := range assignments {
		service, err := st.services.GetService(t.Context(), assignment.ServiceID)
		if err != nil || service.CurrentStreamID != rows[0].ID || assignment.AssignmentRole != "primary" {
			t.Fatal("actual browser service current binding mismatch")
		}
	}
	var originals, claimedSessions int
	if maria {
		if err = st.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM media_assets WHERE owner_user_id=? AND owner_type='stream' AND owner_id=? AND deleted_at IS NULL`, user.ID, rows[0].ID).Scan(&originals); err != nil || originals != 4 {
			t.Fatal("actual browser uploads were duplicated or not claimed")
		}
		if err = st.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM media_upload_sessions WHERE user_id=? AND owner_type='stream' AND claimed_stream_id=?`, user.ID, rows[0].ID).Scan(&claimedSessions); err != nil || claimedSessions != 1 {
			t.Fatal("actual browser upload session did not commit with its stream")
		}
	}
	if evidence := os.Getenv("AUTOSTREAM_SQL_UI_EVIDENCE_DIR"); evidence != "" {
		proof, _ := json.MarshalIndent(map[string]any{"status": "PASS_REAL_UI_HANDLER_" + kind + "_STATE", "streams": 1, "candidates": wantCandidates, "originals": originals, "claimed_sessions": claimedSessions, "primary_assignments": 2}, "", "  ")
		if err = os.WriteFile(filepath.Join(evidence, "handler-state.json"), proof, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
