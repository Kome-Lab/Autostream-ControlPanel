package main

import (
	"context"
	"database/sql"
	"errors"
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
)

const defaultStaticWebDir = "/usr/share/autostream-control-panel"

func main() {
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

	srv := httpapi.NewServer(
		store.NewMariaDBStreamStore(db),
		httpapi.WithAuthStore(store.NewMariaDBAuthStoreWithSecretKey(db, os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))),
		httpapi.WithAuditStore(store.NewMariaDBAuditStore(db)),
		httpapi.WithProfileStore(store.NewMariaDBProfileStore(db)),
		httpapi.WithIntegrationStore(store.NewMariaDBIntegrationStore(db, os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))),
		httpapi.WithSecuritySettingsStore(store.NewMariaDBSecuritySettingsStore(db)),
		httpapi.WithSecretStore(store.NewMariaDBSecretStore(db, os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))),
		httpapi.WithRuntimeSecretLeaseStore(store.NewMariaDBRuntimeSecretLeaseStore(db)),
		httpapi.WithOAuthLoginStore(store.NewMariaDBOAuthLoginStore(db)),
	)
	handler := withStaticFiles(srv, staticWebDir())
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
	dir := strings.TrimSpace(os.Getenv("AUTOSTREAM_WEB_DIR"))
	if dir != "" {
		return dir
	}
	if info, err := os.Stat(defaultStaticWebDir); err == nil && info.IsDir() {
		return defaultStaticWebDir
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
	app http.Handler
	dir string
}

func withStaticFiles(app http.Handler, dir string) http.Handler {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return app
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		log.Printf("static web dir is not available; serving API only: %s", dir)
		return app
	}
	log.Printf("serving Control Panel web UI from %s", dir)
	return staticFilesHandler{app: app, dir: dir}
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
		http.ServeFile(w, r, filepath.Join(h.dir, "index.html"))
		return true
	}
	rel := strings.TrimPrefix(cleanPath, "/")
	full, ok := safeStaticPath(h.dir, rel)
	if !ok {
		http.NotFound(w, r)
		return true
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		if isHTMLNavigationRequest(r) && isControlPanelUIPath(cleanPath) {
			http.ServeFile(w, r, filepath.Join(h.dir, "index.html"))
			return true
		}
		return false
	}
	http.ServeFile(w, r, full)
	return true
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

func isControlPanelUIPath(cleanPath string) bool {
	switch cleanPath {
	case "/login", "/setup", "/dashboard":
		return true
	default:
		return false
	}
}

func setStaticSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'")
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
