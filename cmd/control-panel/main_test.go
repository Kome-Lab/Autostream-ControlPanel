package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

type testAppSettingsStore struct {
	settings store.AppSettings
	err      error
}

func (s testAppSettingsStore) GetAppSettings(ctx context.Context) (store.AppSettings, error) {
	if err := ctx.Err(); err != nil {
		return store.AppSettings{}, err
	}
	return s.settings, s.err
}

func (s testAppSettingsStore) UpdateAppSettings(ctx context.Context, settings store.AppSettings) (store.AppSettings, error) {
	if err := ctx.Err(); err != nil {
		return store.AppSettings{}, err
	}
	return settings, s.err
}

func TestStaticWebDirUsesConfiguredEnvDir(t *testing.T) {
	got := staticWebDirFromCandidates(" /custom/web ", []string{t.TempDir()})
	if got != "/custom/web" {
		t.Fatalf("static web dir = %q, want configured env dir", got)
	}
}

func TestStaticWebDirUsesFirstExistingCandidate(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	want := t.TempDir()
	other := t.TempDir()

	got := staticWebDirFromCandidates("", []string{missing, want, other})
	if got != want {
		t.Fatalf("static web dir = %q, want %q", got, want)
	}
}

func TestStaticFilesHandlerServesOnlyFilesUnderRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log('ok')"), 0o640); err != nil {
		t.Fatal(err)
	}

	appCalled := false
	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appCalled = true
			http.Error(w, "api fallback", http.StatusTeapot)
		}),
		dir: root,
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || appCalled || res.Body.String() != "console.log('ok')" {
		t.Fatalf("static response = %d appCalled=%v body=%q", res.Code, appCalled, res.Body.String())
	}
	csp := res.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") ||
		!strings.Contains(csp, "object-src 'none'") ||
		!strings.Contains(csp, "img-src 'self' data: blob: https://www.google-analytics.com https://*.google-analytics.com") ||
		!strings.Contains(csp, "media-src 'self' blob:") ||
		!strings.Contains(csp, "worker-src 'self' blob:") ||
		strings.Count(csp, "blob:") != 3 ||
		!strings.Contains(csp, "script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com https://www.googletagmanager.com https://static.cloudflareinsights.com") ||
		!strings.Contains(csp, "connect-src 'self' https://www.google-analytics.com https://*.google-analytics.com https://analytics.google.com https://*.analytics.google.com https://www.googletagmanager.com https://cloudflareinsights.com") ||
		strings.Contains(csp, "*.cloudflareinsights.com") ||
		strings.Contains(csp, "unsafe-eval") ||
		!strings.Contains(csp, "frame-src 'self' https://challenges.cloudflare.com") ||
		res.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("static security headers are missing: %#v", res.Header())
	}
}

func TestStaticFilesHandlerLetsRootFallThroughForAppRedirect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>app</main>"), 0o640); err != nil {
		t.Fatal(err)
	}

	appCalled := false
	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appCalled = true
			http.Redirect(w, r, "/login", http.StatusFound)
		}),
		dir: root,
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusFound || res.Header().Get("Location") != "/login" || !appCalled {
		t.Fatalf("root response = %d location=%q appCalled=%v", res.Code, res.Header().Get("Location"), appCalled)
	}
}

func TestStaticFilesHandlerServesIndexForHTMLNavigation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>app</main>"), 0o640); err != nil {
		t.Fatal(err)
	}

	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "api fallback", http.StatusTeapot)
		}),
		dir: root,
	}

	for _, path := range []string{
		"/login",
		"/setup",
		"/admin",
		"/admin/streams",
		"/admin/workers",
		"/admin/audit-logs",
		"/admin/nodes",
		"/dashboard",
		"/streams",
		"/encoder",
		"/discord",
		"/youtube",
		"/caption",
		"/overlay",
		"/archive",
		"/integrations",
		"/workers",
		"/logs",
		"/users",
		"/roles",
		"/audit",
		"/security",
		"/settings",
		"/tokens",
		"/service-health",
		"/monitoring",
		"/incidents",
		"/diagnostics",
		"/remediation",
		"/notifications",
		"/metrics",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || res.Body.String() != "<main>app</main>" {
			t.Fatalf("path %q static response = %d body=%q", path, res.Code, res.Body.String())
		}
	}
}

func TestStaticFilesHandlerServesNestedNextIndexForHTMLNavigation(t *testing.T) {
	root := t.TempDir()
	adminStreams := filepath.Join(root, "admin", "streams")
	if err := os.MkdirAll(adminStreams, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminStreams, "index.html"), []byte("<main>streams</main>"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>root</main>"), 0o640); err != nil {
		t.Fatal(err)
	}

	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "api fallback", http.StatusTeapot)
		}),
		dir: root,
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/streams", nil)
	req.Header.Set("Accept", "text/html")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "<main>streams</main>" {
		t.Fatalf("nested Next index response = %d body=%q", res.Code, res.Body.String())
	}
}

func TestStaticFilesHandlerServesAdminIndexForNextPrefetchHead(t *testing.T) {
	root := t.TempDir()
	adminStreams := filepath.Join(root, "admin", "streams")
	if err := os.MkdirAll(adminStreams, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminStreams, "index.html"), []byte("<main>streams</main>"), 0o640); err != nil {
		t.Fatal(err)
	}

	appCalled := false
	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appCalled = true
			http.Error(w, "api fallback", http.StatusTeapot)
		}),
		dir: root,
	}

	for _, accept := range []string{"", "*/*"} {
		appCalled = false
		req := httptest.NewRequest(http.MethodHead, "/admin/streams/", nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || appCalled || res.Body.Len() != 0 {
			t.Fatalf("HEAD Accept %q response = %d appCalled=%v body=%q", accept, res.Code, appCalled, res.Body.String())
		}
		if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("HEAD Accept %q content type = %q", accept, contentType)
		}
	}
}

func TestStaticFilesHandlerServesCanonicalUIForMachineAcceptHeaders(t *testing.T) {
	root := t.TempDir()
	for _, route := range []string{"login", filepath.Join("admin", "streams")} {
		dir := filepath.Join(root, route)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><head></head><body>"+route+"</body></html>"), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	appCalled := false
	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appCalled = true
			http.Error(w, "api fallback", http.StatusTeapot)
		}),
		dir: root,
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, requestPath := range []string{"/login/", "/admin/streams/"} {
			appCalled = false
			req := httptest.NewRequest(method, requestPath, nil)
			req.Header.Set("Accept", "*/*")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK || appCalled {
				t.Fatalf("%s %s response = %d appCalled=%v body=%q", method, requestPath, res.Code, appCalled, res.Body.String())
			}
			if method == http.MethodHead && res.Body.Len() != 0 {
				t.Fatalf("HEAD %s body = %q", requestPath, res.Body.String())
			}
			if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
				t.Fatalf("%s %s content type = %q", method, requestPath, contentType)
			}
		}
	}
}

func TestStaticFilesHandlerInjectsGoogleAnalyticsIntoInitialHTML(t *testing.T) {
	root := t.TempDir()
	for _, route := range []string{"login", "setup"} {
		dir := filepath.Join(root, route)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		body := "<!doctype html><html><head><title>Fixture</title></head><body>" + route + "</body></html>"
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	adminStreams := filepath.Join(root, "admin", "streams")
	if err := os.MkdirAll(adminStreams, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminStreams, "__next._tree.txt"), []byte("next-tree"), 0o640); err != nil {
		t.Fatal(err)
	}

	settings := store.NewMemoryAppSettingsStore()
	if _, err := settings.UpdateAppSettings(context.Background(), store.AppSettings{
		GoogleAnalyticsEnabled:       true,
		GoogleAnalyticsMeasurementID: "G-TEST1234",
		SMTPEnabled:                  true,
		SMTPHost:                     "smtp.example.com",
		SMTPPort:                     587,
		SMTPStartTLS:                 true,
		SMTPFrom:                     "AutoStream <noreply@example.com>",
		SMTPUsername:                 "mailer",
		SMTPPasswordConfigured:       true,
	}); err != nil {
		t.Fatal(err)
	}
	handler := staticFilesHandler{
		app:         http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "api fallback", http.StatusTeapot) }),
		dir:         root,
		appSettings: settings,
	}

	req := httptest.NewRequest(http.MethodGet, "/login/", nil)
	req.Header.Set("Accept", "*/*")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login response = %d body=%q", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{
		"<head>\n<!-- Google tag (gtag.js) -->",
		`https://www.googletagmanager.com/gtag/js?id=G-TEST1234`,
		`data-measurement-id="G-TEST1234"`,
		`function gtag(){dataLayer.push(arguments);}`,
		`gtag('config', 'G-TEST1234'`,
		`window.location.origin + window.location.pathname`,
		`gtag('event', 'page_view'`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("injected login HTML is missing %q: %s", want, body)
		}
	}
	if strings.Count(body, `gtag('config', 'G-TEST1234'`) != 1 {
		t.Fatalf("config command count = %d", strings.Count(body, `gtag('config', 'G-TEST1234'`))
	}
	for _, secretField := range []string{"smtp.example.com", "noreply@example.com", "mailer"} {
		if strings.Contains(body, secretField) {
			t.Fatalf("injected HTML leaked application setting %q", secretField)
		}
	}
	if cacheControl := res.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("login Cache-Control = %q", cacheControl)
	}
	getContentLength := res.Header().Get("Content-Length")
	if getContentLength == "" {
		t.Fatal("login GET response is missing Content-Length")
	}

	req = httptest.NewRequest(http.MethodHead, "/login/", nil)
	req.Header.Set("Accept", "*/*")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.Len() != 0 {
		t.Fatalf("login HEAD response = %d body=%q", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Content-Length") != getContentLength {
		t.Fatalf("login HEAD headers do not match dynamic GET: %#v", res.Header())
	}

	req = httptest.NewRequest(http.MethodGet, "/setup/", nil)
	req.Header.Set("Accept", "text/html")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if strings.Contains(res.Body.String(), "googletagmanager.com") {
		t.Fatalf("setup page must not contain Google Analytics: %s", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/streams/__next._tree.txt", nil)
	req.Header.Set("Accept", "*/*")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "next-tree" {
		t.Fatalf("Next route data response = %d body=%q", res.Code, res.Body.String())
	}
	if strings.HasPrefix(res.Header().Get("Content-Type"), "text/html") || res.Header().Get("Cache-Control") == "no-store" {
		t.Fatalf("Next route data received dynamic HTML headers: %#v", res.Header())
	}

	if _, err := settings.UpdateAppSettings(context.Background(), store.AppSettings{
		GoogleAnalyticsEnabled:       true,
		GoogleAnalyticsMeasurementID: "G-NEW5678",
	}); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/login/", nil)
	req.Header.Set("Accept", "*/*")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if !strings.Contains(res.Body.String(), "G-NEW5678") || strings.Contains(res.Body.String(), "G-TEST1234") {
		t.Fatalf("updated response contains stale measurement ID: %s", res.Body.String())
	}
}

func TestStaticFilesHandlerOmitsGoogleAnalyticsForUnavailableSettings(t *testing.T) {
	root := t.TempDir()
	login := filepath.Join(root, "login")
	if err := os.MkdirAll(login, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(login, "index.html"), []byte("<html><head></head><body>login</body></html>"), 0o640); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name     string
		settings store.AppSettingsStore
	}{
		{name: "disabled", settings: store.NewMemoryAppSettingsStore()},
		{name: "invalid ID", settings: testAppSettingsStore{settings: store.AppSettings{GoogleAnalyticsEnabled: true, GoogleAnalyticsMeasurementID: `G-BAD<script>`}}},
		{name: "store error", settings: testAppSettingsStore{err: errors.New("settings unavailable")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := staticFilesHandler{
				app:         http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "api fallback", http.StatusTeapot) }),
				dir:         root,
				appSettings: tt.settings,
			}
			req := httptest.NewRequest(http.MethodGet, "/login/", nil)
			req.Header.Set("Accept", "*/*")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "googletagmanager.com") {
				t.Fatalf("response = %d body=%q", res.Code, res.Body.String())
			}
			if cacheControl := res.Header().Get("Cache-Control"); cacheControl != "no-store" {
				t.Fatalf("Cache-Control = %q", cacheControl)
			}
		})
	}
}

func TestStaticFilesHandlerKeepsAPIFallbackForJSONAndAssetMisses(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>app</main>"), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"streams", "service-health", "workers", "roles", "users"} {
		dir := filepath.Join(root, route)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>"+route+"</main>"), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "api fallback", http.StatusTeapot)
		}),
		dir: root,
	}

	var req *http.Request
	var res *httptest.ResponseRecorder
	for _, path := range []string{"/streams", "/service-health", "/workers", "/roles", "/users", "/settings/app", "/services/runtime-config", "/observability/metrics"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "application/json")
		res = httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusTeapot {
			t.Fatalf("API request %q should fall through, status = %d body=%q", path, res.Code, res.Body.String())
		}
		if path == "/streams" && !strings.Contains(res.Header().Get("Vary"), "Accept") {
			t.Fatalf("content-negotiated route should vary by Accept, headers = %#v", res.Header())
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/streams", nil)
	req.Header.Set("Accept", "text/html")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "<main>streams</main>" {
		t.Fatalf("HTML navigation should use the exported route, status = %d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodHead, "/streams", nil)
	req.Header.Set("Accept", "*/*")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusTeapot {
		t.Fatalf("legacy API HEAD should fall through, status = %d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	req.Header.Set("Accept", "*/*")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusTeapot {
		t.Fatalf("missing asset request should fall through, status = %d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Accept", "text/html")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusTeapot {
		t.Fatalf("API route should not be treated as SPA navigation, status = %d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/settings/app", nil)
	req.Header.Set("Accept", "text/html")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusTeapot {
		t.Fatalf("nested settings API route should not be treated as SPA navigation, status = %d body=%q", res.Code, res.Body.String())
	}
}

func TestStaticFilesHandlerRejectsTraversalBeforeFallback(t *testing.T) {
	root := t.TempDir()
	handler := staticFilesHandler{
		app: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("unsafe static path must not fall through to API handler")
		}),
		dir: root,
	}

	for _, path := range []string{"/../secret.txt", `/..\secret.txt`, "/sub/../../secret.txt"} {
		req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: path}}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("path %q status = %d, want 404", path, res.Code)
		}
	}
}

func TestSafeStaticPathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../secret.txt", `..\secret.txt`, "/absolute.txt", "sub/../../secret.txt", "sub\x00file"} {
		if full, ok := safeStaticPath(root, rel); ok {
			t.Fatalf("unsafe relative path %q accepted as %q", rel, full)
		}
	}
	if full, ok := safeStaticPath(root, "assets/app.js"); !ok || filepath.Dir(full) != filepath.Join(root, "assets") {
		t.Fatalf("safe path rejected or resolved outside root: full=%q ok=%v", full, ok)
	}
}
