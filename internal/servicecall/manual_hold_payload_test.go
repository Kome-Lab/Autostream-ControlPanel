package servicecall

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	"github.com/example/autostream-control-panel/internal/manualhold"
	"github.com/example/autostream-control-panel/internal/store"
)

func managedRunPayloadRecord(owner, streamID, run string) lifecycle.Record {
	resource := lifecycle.ResourceRef{Kind: lifecycle.Stream, OwnerID: lifecycle.ID(owner), ResourceID: lifecycle.ID(streamID)}
	contract := lifecycle.ContractRef{ContractID: lifecycle.CC02, LogicalRevision: 1, SpecRef: lifecycle.SpecRef, ModuleCommitRef: manualhold.ModuleCommit}
	correlation := lifecycle.Correlation{RequestID: "synthetic-managed-start", AttemptID: "synthetic-managed-start", OperationID: "auto-start", StepID: "start", ExecutionKey: "synthetic-managed-start"}
	header := lifecycle.Header{ContractRef: contract, RecordKey: lifecycle.RecordKey{ContractRef: contract, OwnerID: "synthetic-control-panel", RecordID: lifecycle.ID("record-" + owner)}, Correlation: correlation, DomainRef: lifecycle.DomainRef{DeploymentID: "synthetic-deployment", Realm: "stream-operations", DomainID: lifecycle.ID(streamID), MembershipRevision: 1, SnapshotRef: "synthetic-membership", Resources: []lifecycle.ResourceRef{resource}}, ProducerRef: lifecycle.ProducerRef{ComponentID: "C01", ServiceID: "synthetic-control-panel", HostID: "synthetic-host", BootID: "synthetic-boot", ProcessIncarnation: "synthetic-process", WriterID: "synthetic-writer"}, RecordRevision: 3, ProfileRef: lifecycle.ProfileRef{ProfileID: lifecycle.ExplicitResume, Revision: 1}}
	vector := lifecycle.RevisionVector{}
	membership := uint64(1)
	vector = append(vector, lifecycle.RevisionEntry{Key: lifecycle.AxisKey{Axis: lifecycle.Membership, OwnerID: resource.OwnerID, Resource: resource}, Incarnation: lifecycle.ID(streamID), Value: lifecycle.VersionedValue{Kind: lifecycle.PresentUint64, Uint64: &membership}, SourceFrameRef: "synthetic-source"})
	for _, axis := range []lifecycle.Axis{lifecycle.Admission, lifecycle.Ownership, lifecycle.Fence, lifecycle.Boot, lifecycle.Process, lifecycle.RunAxis, lifecycle.ConfigAxis} {
		value := lifecycle.ID("synthetic-" + string(axis))
		if axis == lifecycle.RunAxis {
			value = lifecycle.ID(run)
		}
		vector = append(vector, lifecycle.RevisionEntry{Key: lifecycle.AxisKey{Axis: axis, OwnerID: resource.OwnerID, Resource: resource}, Incarnation: lifecycle.ID(streamID), Value: lifecycle.VersionedValue{Kind: lifecycle.PresentLiteral, Literal: &value}, SourceFrameRef: "synthetic-source"})
	}
	return lifecycle.Record{Header: header, Vector: &lifecycle.VectorRecord{Vector: vector}}
}

func TestManualAutoStartWithoutArchiveProfileRunTransport(t *testing.T) {
	const streamID = "synthetic-stream-transport"
	const run = "synthetic-managed-run-01"
	var mu sync.Mutex
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("actual Start body was invalid JSON")
			w.WriteHeader(400)
			return
		}
		record, err := manualhold.Decode(r.Header.Get(manualhold.Header))
		if err != nil {
			t.Error("actual Start wire omitted valid pinned G0 record")
			w.WriteHeader(400)
			return
		}
		resource, _ := manualhold.Resource(record)
		owner := string(resource.OwnerID)
		if body["stream_id"] != streamID || body["archive_run_id"] != run {
			t.Error("actual Start body lost claim Run identity")
		}
		for _, entry := range record.Vector.Vector {
			if entry.Key.Axis == lifecycle.RunAxis && (entry.Value.Literal == nil || string(*entry.Value.Literal) != run) {
				t.Error("header Run differs from actual Start body")
			}
		}
		if archive, exists := body["archive_profile_id"]; exists && archive != "" {
			t.Error("managed Run changed the selected archive profile")
		}
		if owner == "encoder-msr" {
			var at time.Time
			raw, _ := json.Marshal(body["started_at"])
			if json.Unmarshal(raw, &at) != nil || at.IsZero() {
				t.Error("Encoder wire lost actual Run start time")
			}
		}
		mu.Lock()
		seen[owner]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"job_generation":1}`))
	}))
	defer server.Close()
	services := []store.RegisteredService{{ServiceID: "encoder-msr", ServiceType: "encoder_recorder", PublicURL: server.URL, ReportedCapabilities: map[string]any{ManualAutoStartHoldCapability: true}}, {ServiceID: "worker-msr", ServiceType: "worker", PublicURL: server.URL, ReportedCapabilities: map[string]any{ManualAutoStartHoldCapability: true}}}
	originals := map[string]lifecycle.Record{}
	for _, s := range services {
		originals[s.ServiceID] = managedRunPayloadRecord(s.ServiceID, streamID, run)
	}
	client := testClient()
	ctx := WithStartAdmissions(t.Context(), originals)
	started := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	results := client.Start(ctx, store.Stream{ID: streamID, Name: "Synthetic Run without archive profile", ArchiveRunID: run, ArchiveStartedAt: &started}, services, StartRequest{EncoderInputURL: "srt://input.example.invalid:9000", ArchiveRunID: run, ArchiveStartedAt: started})
	if len(results) != 2 {
		t.Fatalf("actual transport result boundaries=%d want2: %#v", len(results), results)
	}
	for _, result := range results {
		if !result.Success {
			t.Fatalf("actual managed transport failed: %#v", result)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["encoder-msr"] != 1 || seen["worker-msr"] != 1 {
		t.Fatalf("actual transport dispatch counts=%v", seen)
	}
}
