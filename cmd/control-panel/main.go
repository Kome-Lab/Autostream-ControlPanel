package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/httpapi"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
)

const defaultStaticWebDir = "/usr/share/autostream-control-panel"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("autostream-control-panel %s\ncommit: %s\nbuild_date: %s\n", version.Current(), version.Commit, version.BuildDate)
		return
	}
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := os.Getenv("AUTOSTREAM_BIND_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	db, err := openDatabaseWithRetry(context.Background(), 60*time.Second, 2*time.Second)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	appSettingsStore := store.NewMariaDBAppSettingsStore(db)
	srv := httpapi.NewServer(
		store.NewMariaDBStreamStore(db),
		httpapi.WithAuthStore(store.NewMariaDBAuthStoreWithSecretKey(db, os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))),
		httpapi.WithAuditStore(store.NewMariaDBAuditStore(db)),
		httpapi.WithProfileStore(store.NewMariaDBProfileStore(db)),
		httpapi.WithIntegrationStore(store.NewMariaDBIntegrationStore(db, os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))),
		httpapi.WithSecuritySettingsStore(store.NewMariaDBSecuritySettingsStore(db)),
		httpapi.WithAppSettingsStore(appSettingsStore),
		httpapi.WithSecretStore(store.NewMariaDBSecretStore(db, os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))),
		httpapi.WithRuntimeSecretLeaseStore(store.NewMariaDBRuntimeSecretLeaseStore(db)),
		httpapi.WithSystemUpdateStore(store.NewMariaDBSystemUpdateStore(db)),
		httpapi.WithOAuthLoginStore(store.NewMariaDBOAuthLoginStore(db)),
	)
	handler := withStaticFiles(srv, staticWebDir(), appSettingsStore)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("autostream-control-panel listening on %s", addr)
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	go srv.RunYouTubeCompletionRetryLoop(runCtx, durationFromEnv("AUTOSTREAM_YOUTUBE_COMPLETE_RETRY_INTERVAL", time.Minute))
	select {
	case err := <-errCh:
		if err != nil {
			log.Fatal(err)
		}
	case <-runCtx.Done():
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("control panel shutdown failed: %v", err)
			if closeErr := server.Close(); closeErr != nil {
				log.Printf("control panel close failed: %v", closeErr)
			}
		}
	}
}

func staticWebDir() string {
	return staticWebDirFromCandidates(os.Getenv("AUTOSTREAM_WEB_DIR"), staticWebDirCandidates())
}

func staticWebDirCandidates() []string {
	candidates := []string{
		defaultStaticWebDir,
		filepath.Clean(filepath.Join("web", "out")),
		filepath.Clean(filepath.Join("web", "dist")),
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Clean(filepath.Join(exeDir, "..", "share", "autostream-control-panel")),
			filepath.Join(exeDir, "share", "autostream-control-panel"),
		)
	}
	return candidates
}

func staticWebDirFromCandidates(envDir string, candidates []string) string {
	if dir := strings.TrimSpace(envDir); dir != "" {
		return dir
	}
	seen := map[string]bool{}
	for _, dir := range candidates {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		log.Printf("invalid %s=%q; using %s", name, raw, fallback)
		return fallback
	}
	return value
}

func openDatabaseWithRetry(parent context.Context, timeout, interval time.Duration) (*sql.DB, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		db, err := database.OpenFromEnv(ctx)
		cancel()
		if err == nil {
			if attempt > 1 {
				log.Printf("database connection succeeded after %d attempt(s)", attempt)
			}
			return db, nil
		}
		lastErr = err
		if time.Now().Add(interval).After(deadline) {
			return nil, lastErr
		}
		log.Printf("database is not ready yet (attempt %d): %v", attempt, err)
		time.Sleep(interval)
	}
}

type staticFilesHandler struct {
	app         http.Handler
	dir         string
	appSettings store.AppSettingsStore
}

func withStaticFiles(app http.Handler, dir string, appSettings store.AppSettingsStore) http.Handler {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return app
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		log.Printf("static web dir is not available; serving API only: %s", dir)
		return app
	}
	log.Printf("serving Control Panel web UI from %s", dir)
	return staticFilesHandler{app: app, dir: dir, appSettings: appSettings}
}

func (h staticFilesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if h.serveStatic(w, r) {
			return
		}
	}
	h.app.ServeHTTP(w, r)
}

func (h staticFilesHandler) serveStatic(w http.ResponseWriter, r *http.Request) bool {
	setStaticSecurityHeaders(w)
	if unsafeStaticURLPath(r.URL.Path) {
		http.NotFound(w, r)
		return true
	}
	cleanPath := path.Clean("/" + r.URL.Path)
	if cleanPath == "/" {
		return false
	}
	isUINavigation := isHTMLNavigationRequest(r) || isCanonicalControlPanelUIRequest(r, cleanPath)
	rel := strings.TrimPrefix(cleanPath, "/")
	full, ok := safeStaticPath(h.dir, rel)
	if !ok {
		http.NotFound(w, r)
		return true
	}
	info, err := os.Stat(full)
	if err == nil && info.IsDir() {
		w.Header().Add("Vary", "Accept")
		if !isUINavigation {
			return false
		}
		if h.serveStaticIndex(w, r, rel, cleanPath) {
			return true
		}
		if isControlPanelUIPath(cleanPath) {
			h.serveStaticDocument(w, r, filepath.Join(h.dir, "index.html"), cleanPath)
			return true
		}
		return false
	}
	if err != nil {
		if isUINavigation && isControlPanelUIPath(cleanPath) {
			if h.serveStaticIndex(w, r, rel, cleanPath) {
				return true
			}
			h.serveStaticDocument(w, r, filepath.Join(h.dir, "index.html"), cleanPath)
			return true
		}
		return false
	}
	h.serveStaticDocument(w, r, full, cleanPath)
	return true
}

func (h staticFilesHandler) serveStaticIndex(w http.ResponseWriter, r *http.Request, rel, cleanPath string) bool {
	indexRel := path.Join(rel, "index.html")
	indexFull, ok := safeStaticPath(h.dir, indexRel)
	if !ok {
		return false
	}
	if indexInfo, err := os.Stat(indexFull); err == nil && !indexInfo.IsDir() {
		h.serveStaticDocument(w, r, indexFull, cleanPath)
		return true
	}
	return false
}

func (h staticFilesHandler) serveStaticDocument(w http.ResponseWriter, r *http.Request, full, cleanPath string) {
	if h.appSettings == nil || !isCanonicalControlPanelUIRequest(r, cleanPath) {
		http.ServeFile(w, r, full)
		return
	}

	// These documents depend on runtime application settings. Do not let a
	// disabled or previous measurement ID remain cached after an operator update.
	w.Header().Set("Cache-Control", "no-store")
	document, err := os.ReadFile(full)
	if err != nil {
		http.ServeFile(w, r, full)
		return
	}
	settings, err := h.appSettings.GetAppSettings(r.Context())
	if err != nil {
		log.Printf("load app settings for Google Analytics bootstrap: %v", err)
	} else if settings.GoogleAnalyticsEnabled {
		if measurementID, ok := normalizeGoogleAnalyticsMeasurementID(settings.GoogleAnalyticsMeasurementID); ok {
			document, _ = injectGoogleAnalyticsSnippet(document, measurementID)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, filepath.Base(full), time.Time{}, bytes.NewReader(document))
}

func isHTMLNavigationRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if path.Ext(r.URL.Path) != "" {
		return false
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// Next.js static-export link prefetch and external tag detectors may request UI
// routes with Accept: */*. Only /login and the canonical /admin namespace are
// unambiguously UI; legacy top-level paths such as /streams overlap API routes.
func isCanonicalControlPanelUIRequest(r *http.Request, cleanPath string) bool {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || path.Ext(strings.TrimSuffix(r.URL.Path, "/")) != "" {
		return false
	}
	return isGoogleAnalyticsHTMLPath(cleanPath)
}

func isGoogleAnalyticsHTMLPath(cleanPath string) bool {
	return cleanPath == "/login" || cleanPath == "/admin" || strings.HasPrefix(cleanPath, "/admin/")
}

func isControlPanelUIPath(cleanPath string) bool {
	if isGoogleAnalyticsHTMLPath(cleanPath) {
		return true
	}
	_, ok := controlPanelUIPaths[cleanPath]
	return ok
}

func normalizeGoogleAnalyticsMeasurementID(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) < 6 || len(value) > 24 || !strings.HasPrefix(value, "G-") {
		return "", false
	}
	for _, char := range strings.TrimPrefix(value, "G-") {
		if char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		return "", false
	}
	return value, true
}

func injectGoogleAnalyticsSnippet(document []byte, measurementID string) ([]byte, bool) {
	measurementID, ok := normalizeGoogleAnalyticsMeasurementID(measurementID)
	if !ok || bytes.Contains(document, []byte(`id="autostream-google-analytics"`)) {
		return document, false
	}
	lowerDocument := bytes.ToLower(document)
	headStart := bytes.Index(lowerDocument, []byte("<head"))
	if headStart < 0 || headStart+5 >= len(document) {
		return document, false
	}
	next := document[headStart+5]
	if next != '>' && next != ' ' && next != '\t' && next != '\r' && next != '\n' {
		return document, false
	}
	headEndOffset := bytes.IndexByte(document[headStart:], '>')
	if headEndOffset < 0 {
		return document, false
	}
	insertAt := headStart + headEndOffset + 1
	snippet := fmt.Sprintf(`
<!-- Google tag (gtag.js) -->
<script async src="https://www.googletagmanager.com/gtag/js?id=%[1]s" id="autostream-google-analytics" data-measurement-id="%[1]s"></script>
<script id="autostream-google-analytics-bootstrap">
window.dataLayer = window.dataLayer || [];
function gtag(){dataLayer.push(arguments);}
gtag('consent', 'default', {
  analytics_storage: 'granted',
  ad_storage: 'denied',
  ad_user_data: 'denied',
  ad_personalization: 'denied'
});
gtag('js', new Date());
gtag('config', '%[1]s', {
  send_page_view: false,
  allow_google_signals: false,
  allow_ad_personalization_signals: false,
  cookie_flags: 'SameSite=Strict;Secure'
});
var autostreamGoogleAnalyticsPageLocation = window.location.origin + window.location.pathname;
window.__AUTOSTREAM_GOOGLE_ANALYTICS_ID__ = '%[1]s';
window.__AUTOSTREAM_GOOGLE_ANALYTICS_PAGE_VIEW__ = '%[1]s:' + autostreamGoogleAnalyticsPageLocation;
gtag('event', 'page_view', {
  page_location: autostreamGoogleAnalyticsPageLocation,
  page_path: window.location.pathname,
  page_title: document.title || 'AutoStream Control Panel'
});
</script>`, measurementID)

	result := make([]byte, 0, len(document)+len(snippet))
	result = append(result, document[:insertAt]...)
	result = append(result, snippet...)
	result = append(result, document[insertAt:]...)
	return result, true
}

var controlPanelUIPaths = map[string]struct{}{
	"/login":          {},
	"/setup":          {},
	"/admin":          {},
	"/dashboard":      {},
	"/streams":        {},
	"/encoder":        {},
	"/discord":        {},
	"/youtube":        {},
	"/caption":        {},
	"/overlay":        {},
	"/archive":        {},
	"/integrations":   {},
	"/workers":        {},
	"/logs":           {},
	"/users":          {},
	"/roles":          {},
	"/audit":          {},
	"/security":       {},
	"/settings":       {},
	"/tokens":         {},
	"/service-health": {},
	"/monitoring":     {},
	"/incidents":      {},
	"/diagnostics":    {},
	"/remediation":    {},
	"/notifications":  {},
	"/metrics":        {},
}

func setStaticSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; img-src 'self' data: blob: https://www.google-analytics.com https://*.google-analytics.com; media-src 'self' blob:; worker-src 'self' blob:; font-src 'self'; connect-src 'self' https://www.google-analytics.com https://*.google-analytics.com https://analytics.google.com https://*.analytics.google.com https://www.googletagmanager.com https://cloudflareinsights.com; script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com https://www.googletagmanager.com https://static.cloudflareinsights.com; frame-src 'self' https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func safeStaticPath(root, rel string) (string, bool) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || strings.Contains(rel, `\`) || strings.Contains(rel, "\x00") {
		return "", false
	}
	cleanRel := filepath.Clean(filepath.FromSlash(rel))
	if cleanRel == "." || filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
		return "", false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	full := filepath.Join(rootAbs, cleanRel)
	relToRoot, err := filepath.Rel(rootAbs, full)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relToRoot) {
		return "", false
	}
	return full, true
}

func unsafeStaticURLPath(raw string) bool {
	if strings.Contains(raw, `\`) || strings.Contains(raw, "\x00") {
		return true
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}
