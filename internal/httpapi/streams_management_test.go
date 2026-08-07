package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestStreamListIncludesPrimaryAssignedNodes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "assigned nodes")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodGet, "/streams", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("list streams status = %d body = %s", res.Code, res.Body.String())
	}
	var items []store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("list streams length = %d", len(items))
	}
	if items[0].AssignedWorkerID != "worker-01" || items[0].AssignedEncoderID != "encoder_recorder-01" {
		t.Fatalf("assigned nodes = worker=%q encoder=%q", items[0].AssignedWorkerID, items[0].AssignedEncoderID)
	}

	_ = csrf
}

func TestCreateStreamReturnsPrimaryAssignedNodes(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.create", "services.assign", "workers.assign"}); err != nil {
		t.Fatal(err)
	}
	registerServiceInstance(t, auth, "encoder_recorder-01", "encoder_recorder")
	registerServiceInstance(t, auth, "worker-01", "worker")
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/streams", strings.NewReader(`{"name":"created with nodes","encoder_service_id":"encoder_recorder-01","worker_service_id":"worker-01"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create stream status = %d body = %s", res.Code, res.Body.String())
	}
	var stream store.Stream
	if err := json.Unmarshal(res.Body.Bytes(), &stream); err != nil {
		t.Fatal(err)
	}
	if stream.AssignedWorkerID != "worker-01" || stream.AssignedEncoderID != "encoder_recorder-01" {
		t.Fatalf("created assigned nodes = worker=%q encoder=%q", stream.AssignedWorkerID, stream.AssignedEncoderID)
	}
}

func TestDeleteStreamReleasesAssignmentsAndRejectsActiveStream(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"stream_operator"}}, "correct horse battery", []string{"streams.delete"}); err != nil {
		t.Fatal(err)
	}
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "deletable")
	if err != nil {
		t.Fatal(err)
	}
	registerAssignedServices(t, auth, stream.ID, "encoder_recorder", "worker")
	handler := NewServer(streams, WithAuthStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/streams/"+stream.ID, nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete stream status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := streams.GetStream(t.Context(), stream.ID); err != store.ErrNotFound {
		t.Fatalf("deleted stream lookup error = %v", err)
	}
	assignments, err := auth.ListStreamAssignments(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 0 {
		t.Fatalf("assignments after delete = %d", len(assignments))
	}

	active, err := streams.CreateStream(t.Context(), "active")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.UpdateStreamStatus(t.Context(), active.ID, "live"); err != nil {
		t.Fatal(err)
	}
	activeReq := httptest.NewRequest(http.MethodDelete, "/streams/"+active.ID, nil)
	activeReq.AddCookie(cookie)
	activeReq.Header.Set("X-CSRF-Token", csrf)
	activeRes := httptest.NewRecorder()
	handler.ServeHTTP(activeRes, activeReq)
	if activeRes.Code != http.StatusConflict {
		t.Fatalf("active delete status = %d body = %s", activeRes.Code, activeRes.Body.String())
	}
	if _, err := streams.GetStream(t.Context(), active.ID); err != nil {
		t.Fatalf("active stream lookup error = %v", err)
	}
}
