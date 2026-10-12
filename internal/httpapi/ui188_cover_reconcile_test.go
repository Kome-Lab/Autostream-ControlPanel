package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/videocover"
)

type ui188ConcurrentCoverReconciler struct {
	entered chan struct{}
	release chan struct{}
	result  servicecall.VideoCoverDispatchResult
}

func (d *ui188ConcurrentCoverReconciler) DispatchVideoCover(context.Context, store.RegisteredService, servicecall.VideoCoverDispatchRequest) servicecall.VideoCoverDispatchResult {
	return servicecall.VideoCoverDispatchResult{Ambiguous: true}
}
func (d *ui188ConcurrentCoverReconciler) ReconcileVideoCover(ctx context.Context, _ store.RegisteredService, _ servicecall.VideoCoverReconcileRequest) servicecall.VideoCoverDispatchResult {
	select {
	case d.entered <- struct{}{}:
	case <-ctx.Done():
		return servicecall.VideoCoverDispatchResult{Ambiguous: true}
	}
	select {
	case <-d.release:
		return d.result
	case <-ctx.Done():
		return servicecall.VideoCoverDispatchResult{Ambiguous: true}
	}
}

func TestUI188ConcurrentCoverGETRecordsOneExactSystemOutcome(t *testing.T) {
	for _, outcome := range []string{"success", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			auth := store.NewMemoryAuthStore()
			if err := auth.AddUser(store.User{Username: "reader"}, "correct horse battery", []string{"streams.read"}); err != nil {
				t.Fatal(err)
			}
			streams := store.NewMemoryStreamStore()
			stream, err := streams.CreateStream(t.Context(), "reconcile")
			if err != nil {
				t.Fatal(err)
			}
			token := createRegisteredAssignedService(t, auth, "ui188-encoder", "encoder_recorder", stream.ID)
			if _, err = auth.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: "ui188-encoder", Status: "healthy", CurrentStreamID: stream.ID, Capabilities: map[string]any{"live_video_cover_v1": true}}); err != nil {
				t.Fatal(err)
			}
			covers := videocover.NewMemoryRepository()
			if _, err = covers.EnsureGeneration(t.Context(), stream.ID, 7, "variant-A", false); err != nil {
				t.Fatal(err)
			}
			request := videocover.ActionRequest{Active: true, ExpectedJobGeneration: 7, ExpectedRevision: 1, IdempotencyKey: "ui188-get"}
			prepared, err := covers.PrepareAction(t.Context(), stream.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = covers.RecordAmbiguous(t.Context(), stream.ID, 7, request.IdempotencyKey); err != nil {
				t.Fatal(err)
			}
			dispatcher := &ui188ConcurrentCoverReconciler{entered: make(chan struct{}, 2), release: make(chan struct{}), result: servicecall.VideoCoverDispatchResult{Applied: outcome == "success", SafeErrorCode: "video_cover_action_failed"}}
			handler := NewServer(streams, WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithVideoCoverRepository(covers), WithVideoCoverDispatcher(dispatcher))
			cookie, _ := loginForTest(t, handler, "reader", "correct horse battery")
			responses := make(chan *httptest.ResponseRecorder, 2)
			for range 2 {
				go func() {
					responses <- serveUserJSON(t, handler, http.MethodGet, "/streams/"+stream.ID+"/video-cover-state", "", cookie, "")
				}()
			}
			for range 2 {
				select {
				case <-dispatcher.entered:
				case <-time.After(time.Second):
					t.Fatal("both GETs did not observe pending action")
				}
			}
			close(dispatcher.release)
			for range 2 {
				select {
				case response := <-responses:
					if response.Code != 200 {
						t.Fatal(response.Code)
					}
				case <-time.After(time.Second):
					t.Fatal("GET did not complete")
				}
			}
			events := []store.AuditEvent{}
			for _, event := range auth.AuditEvents() {
				if event.Action == "streams.video_cover.show" {
					events = append(events, event)
				}
			}
			if len(events) != 1 {
				t.Fatalf("durable outcome audits=%d", len(events))
			}
			event := events[0]
			if event.Result != outcome || event.ActorUsername != "system" || event.ActorUserID != "" || event.Metadata["desired_revision"] != prepared.RequestedRevision || event.Metadata["job_generation"] != uint64(7) {
				t.Fatalf("wrong action audit: %#v", event)
			}
			again := serveUserJSON(t, handler, http.MethodGet, "/streams/"+stream.ID+"/video-cover-state", "", cookie, "")
			if again.Code != 200 || len(auth.AuditEvents()) != len(events)+1 { // login audit is the other event
				count := 0
				for _, e := range auth.AuditEvents() {
					if e.Action == "streams.video_cover.show" {
						count++
					}
				}
				if count != 1 {
					t.Fatal("terminal GET emitted duplicate audit")
				}
			}
		})
	}
}
