package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		!strings.Contains(csp, "img-src 'self' data: blob:") ||
		strings.Count(csp, "blob:") != 1 ||
		!strings.Contains(csp, "script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com") ||
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
