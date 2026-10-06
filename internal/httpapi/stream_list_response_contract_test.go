package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

// MariaDB returns a nil slice when a stream query succeeds without any rows.
// MemoryStreamStore initializes an empty slice, so exercise both results at
// the HTTP boundary instead of relying solely on the memory store behavior.
type streamListResponseStore struct {
	*store.MemoryStreamStore
	items []store.Stream
	err   error
	calls int
}

func (s *streamListResponseStore) ListStreams(context.Context) ([]store.Stream, error) {
	s.calls++
	return s.items, s.err
}

func (s *streamListResponseStore) ListArchiveStreams(ctx context.Context) ([]store.Stream, error) {
	return s.ListStreams(ctx)
}

func (s *streamListResponseStore) ListArchiveProcessingStreams(ctx context.Context) ([]store.Stream, error) {
	return s.ListStreams(ctx)
}

type streamListAssignmentFailureStore struct {
	*store.MemoryAuthStore
}

func (s *streamListAssignmentFailureStore) ListStreamAssignments(context.Context, string) ([]store.RegisteredService, error) {
	return nil, errors.New("assignment storage unavailable")
}

var streamListResponseRoutes = []struct {
	path      string
	storeCode string
}{
	{path: "/streams", storeCode: "list_streams_failed"},
	{path: "/archive/streams", storeCode: "list_archive_streams_failed"},
	{path: "/archive/processing-streams", storeCode: "list_archive_processing_streams_failed"},
}

func streamListResponseHandler(t *testing.T, streams *streamListResponseStore, permissions []string, assignmentFailure bool) (http.Handler, *http.Cookie) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "list-operator", Roles: []string{"stream_operator"}}, "correct horse battery", permissions); err != nil {
		t.Fatal(err)
	}
	var services store.ServiceRegistryStore = auth
	if assignmentFailure {
		services = &streamListAssignmentFailureStore{MemoryAuthStore: auth}
	}
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(services))
	cookie, _ := loginForTest(t, handler, "list-operator", "correct horse battery")
	return handler, cookie
}

func streamListResponseRequest(handler http.Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestStreamListResponsesEncodeEmptySuccessAsArray(t *testing.T) {
	for _, empty := range []struct {
		name  string
		items []store.Stream
	}{
		{name: "nil store result"},
		{name: "initialized empty store result", items: []store.Stream{}},
	} {
		t.Run(empty.name, func(t *testing.T) {
			streams := &streamListResponseStore{MemoryStreamStore: store.NewMemoryStreamStore(), items: empty.items}
			handler, cookie := streamListResponseHandler(t, streams, []string{"streams.read", "archives.read"}, false)
			for _, route := range streamListResponseRoutes {
				t.Run(route.path, func(t *testing.T) {
					res := streamListResponseRequest(handler, cookie, route.path)
					if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "[]" {
						t.Fatalf("empty list must be HTTP 200 []: status=%d body=%s", res.Code, res.Body.String())
					}
					if contentType := res.Header().Get("Content-Type"); contentType != "application/json" {
						t.Fatalf("content type = %q", contentType)
					}
				})
			}
			if streams.calls != len(streamListResponseRoutes) {
				t.Fatalf("store calls = %d, want %d", streams.calls, len(streamListResponseRoutes))
			}
		})
	}
}

func TestStreamListResponsesPreserveStoreFailure(t *testing.T) {
	streams := &streamListResponseStore{MemoryStreamStore: store.NewMemoryStreamStore(), err: errors.New("stream storage unavailable")}
	handler, cookie := streamListResponseHandler(t, streams, []string{"streams.read", "archives.read"}, false)
	for _, route := range streamListResponseRoutes {
		t.Run(route.path, func(t *testing.T) {
			res := streamListResponseRequest(handler, cookie, route.path)
			assertStreamListResponseError(t, res, http.StatusInternalServerError, route.storeCode)
		})
	}
}

func TestStreamListResponsesPreserveAssignmentFailure(t *testing.T) {
	streams := &streamListResponseStore{
		MemoryStreamStore: store.NewMemoryStreamStore(),
		items:             []store.Stream{{ID: "stream-01", Name: "configured stream", Status: "created"}},
	}
	handler, cookie := streamListResponseHandler(t, streams, []string{"streams.read", "archives.read"}, true)
	for _, route := range streamListResponseRoutes {
		t.Run(route.path, func(t *testing.T) {
			res := streamListResponseRequest(handler, cookie, route.path)
			assertStreamListResponseError(t, res, http.StatusInternalServerError, "list_stream_assignments_failed")
		})
	}
}

func TestStreamListResponsesPreserveReadPermission(t *testing.T) {
	streams := &streamListResponseStore{MemoryStreamStore: store.NewMemoryStreamStore()}
	handler, cookie := streamListResponseHandler(t, streams, []string{"streams.create"}, false)
	for _, route := range streamListResponseRoutes {
		t.Run(route.path, func(t *testing.T) {
			res := streamListResponseRequest(handler, cookie, route.path)
			assertStreamListResponseError(t, res, http.StatusForbidden, "permission_denied")
		})
	}
	if streams.calls != 0 {
		t.Fatalf("unauthorized read reached stream storage %d times", streams.calls)
	}
}

func assertStreamListResponseError(t *testing.T, res *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("status=%d, want %d: body=%s", res.Code, status, res.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid error response: %v: body=%s", err, res.Body.String())
	}
	if body.Code != code {
		t.Fatalf("error code=%q, want %q: body=%s", body.Code, code, res.Body.String())
	}
}
