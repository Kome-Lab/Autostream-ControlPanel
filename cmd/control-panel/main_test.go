package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

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
	if res.Header().Get("Content-Security-Policy") != "default-src 'self'" || res.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("static security headers are missing: %#v", res.Header())
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
