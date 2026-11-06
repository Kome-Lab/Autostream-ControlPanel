package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestMemorySharedNodeSelectionsDoNotChangeRunningOwner(t *testing.T) {
	ctx := t.Context()
	streams := NewMemoryStreamStore()
	auth := NewMemoryAuthStore()
	auth.BindStreamAssignmentGuard(streams)
	encoder := registerMemoryAssignmentService(t, auth, "selected-encoder", "encoder_recorder")
	worker := registerMemoryAssignmentService(t, auth, "selected-worker", "worker")
	owner, err := streams.CreateStream(ctx, "running owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []RegisteredService{encoder, worker} {
		if _, err := auth.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{
			ServiceID: service.ServiceID, StreamID: owner.ID, AssignmentRole: "primary",
		}); err != nil {
			t.Fatal(err)
		}
	}
	owner, err = streams.UpdateStreamStatus(ctx, owner.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	beforeServices := make(map[string]RegisteredService)
	for _, id := range []string{encoder.ServiceID, worker.ServiceID} {
		service, err := auth.GetService(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		beforeServices[id] = service
	}
	beforeAssignmentIDs := make(map[string]string)
	for key, id := range auth.assignmentIDs {
		beforeAssignmentIDs[key] = id
	}
	assertOwner := func() {
		t.Helper()
		got, err := streams.GetStream(ctx, owner.ID)
		if err != nil || !reflect.DeepEqual(got, owner) {
			t.Fatalf("saved choices changed the running stream: err=%v", err)
		}
		for id, before := range beforeServices {
			after, err := auth.GetService(ctx, id)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("saved choices changed running service %s: err=%v", id, err)
			}
		}
		if !reflect.DeepEqual(auth.assignmentIDs, beforeAssignmentIDs) {
			t.Fatal("saved choices changed execution assignment identity")
		}
	}

	choices := StreamCreateAssignments{EncoderServiceID: encoder.ServiceID, WorkerServiceID: worker.ServiceID}
	draft, err := streams.CreateAssignedStream(ctx, newUUID(), "draft B", "admin", StreamSettings{}, choices)
	if err != nil {
		t.Fatalf("save shared choices: %v", err)
	}
	if draft.SelectedEncoderID != encoder.ServiceID || draft.SelectedWorkerID != worker.ServiceID ||
		draft.AssignedEncoderID != "" || draft.AssignedWorkerID != "" {
		t.Fatalf("saved and execution identities were mixed: %#v", draft)
	}
	assertOwner()
	second, err := streams.CreateAssignedStream(ctx, newUUID(), "draft C", "admin", StreamSettings{}, choices)
	if err != nil {
		t.Fatalf("save another shared choice: %v", err)
	}
	bindings, err := streams.ListStreamNodeSelectionsForService(ctx, encoder.ServiceID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("shared selection bindings=%#v err=%v", bindings, err)
	}
	assertOwner()

	empty := ""
	draft, err = streams.UpdateStreamSettings(ctx, draft.ID, StreamSettings{
		ExpectedSettingsRevision: &draft.SettingsRevision,
		NodeSelections:           &StreamNodeSelectionUpdate{WorkerServiceID: &empty},
	})
	if err != nil {
		t.Fatalf("clear only the saved worker: %v", err)
	}
	selected, err := streams.GetStreamNodeSelections(ctx, draft.ID)
	if err != nil || selected.EncoderServiceID != encoder.ServiceID || selected.WorkerServiceID != "" {
		t.Fatalf("omitted selection or explicit clear lost: selected=%#v err=%v", selected, err)
	}
	beforeRejected := draft
	_, err = streams.UpdateStreamSettings(ctx, draft.ID, StreamSettings{
		ExpectedSettingsRevision: &draft.SettingsRevision,
		NodeSelections:           &StreamNodeSelectionUpdate{EncoderServiceID: &worker.ServiceID},
	})
	if !errors.Is(err, ErrInvalidServiceAssignment) {
		t.Fatalf("wrong Node type accepted: %v", err)
	}
	afterRejected, err := streams.GetStream(ctx, draft.ID)
	if err != nil || !reflect.DeepEqual(afterRejected, beforeRejected) {
		t.Fatalf("rejected choice changed settings: err=%v", err)
	}
	assertOwner()
	if err := streams.DeleteStream(ctx, draft.ID); err != nil {
		t.Fatalf("delete draft sharing running Nodes: %v", err)
	}
	bindings, err = streams.ListStreamNodeSelectionsForService(ctx, encoder.ServiceID)
	if err != nil || len(bindings) != 1 || bindings[0].StreamID != second.ID {
		t.Fatalf("delete affected other saved choices: bindings=%#v err=%v", bindings, err)
	}
	assertOwner()
}
