package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestMemorySelectedStartClaims(t *testing.T) { runSelectedStartClaimCases(t, false) }
func TestMariaDBSelectedStartClaims(t *testing.T) { runSelectedStartClaimCases(t, true) }

func runSelectedStartClaimCases(t *testing.T, maria bool) {
	for _, name := range []string{"busy_owner", "partial_availability", "concurrent_claims", "changed_selection"} {
		t.Run(name, func(t *testing.T) {
			streams, services, _ := newClockClaimStores(t, maria)
			ctx := t.Context()
			var serviceIDs, tokenIDs, streamIDs []string
			defer func() {
				for _, id := range streamIDs {
					if db, ok := streams.(MariaDBStreamStore); ok {
						if _, err := db.db.Exec("DELETE FROM stream_node_selections WHERE stream_id=?", id); err != nil {
							t.Error(err)
						}
					}
					cleanupClockClaimFixture(t, streams, id, nil, nil)
				}
				cleanupSelectedClaimServices(t, streams, serviceIDs, tokenIDs)
			}()
			selected := StreamNodeSelections{}
			for _, kind := range []string{"encoder_recorder", "worker", "discord_bot"} {
				id := newUUID()
				token, err := services.CreateServiceToken(ctx, kind, []string{"service.register", "service.heartbeat"})
				if err != nil {
					t.Fatal(err)
				}
				serviceIDs, tokenIDs = append(serviceIDs, id), append(tokenIDs, token.ID)
				registration := ServiceRegistration{ServiceID: id, ServiceType: kind, ServiceName: kind, PublicURL: "https://" + id + ".example.com", Capabilities: map[string]any{}}
				if _, err := services.PrecreateService(ctx, token, registration); err != nil {
					t.Fatal(err)
				}
				if _, err := services.RegisterService(ctx, token, registration); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "encoder_recorder":
					selected.EncoderServiceID = id
				case "worker":
					selected.WorkerServiceID = id
				case "discord_bot":
					selected.DiscordServiceID = id
				}
			}
			create := func(label string) Stream {
				t.Helper()
				stream, err := streams.CreateStream(ctx, label)
				if err != nil {
					t.Fatal(err)
				}
				streamIDs = append(streamIDs, stream.ID)
				setSelectedClaimFixture(t, streams, stream.ID, selected)
				stream, err = streams.GetStream(ctx, stream.ID)
				if err != nil {
					t.Fatal(err)
				}
				return stream
			}
			a, b := create("claim A"), create("claim B")
			request := func(stream Stream) StreamStartClaimRequest {
				t.Helper()
				var candidates []RegisteredService
				for _, id := range serviceIDs {
					service, err := services.GetService(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					service.AssignmentRole = "primary"
					candidates = append(candidates, service)
				}
				selection := selected
				return StreamStartClaimRequest{StreamID: stream.ID, ExpectedStatus: stream.Status,
					ExpectedStreamUpdatedAt: stream.UpdatedAt, ExpectedPrimaryAssignments: candidates,
					ExpectedNodeSelections: &selection}
			}
			if name == "concurrent_claims" {
				requests := []StreamStartClaimRequest{request(a), request(b)}
				results := make(chan error, 2)
				gate := make(chan struct{})
				var wg sync.WaitGroup
				for _, req := range requests {
					wg.Add(1)
					go func(req StreamStartClaimRequest) {
						defer wg.Done()
						<-gate
						_, err := streams.ClaimStreamStart(ctx, req)
						results <- err
					}(req)
				}
				close(gate)
				wg.Wait()
				close(results)
				successes := 0
				for err := range results {
					if err == nil {
						successes++
					} else if !errors.Is(err, ErrServiceAssignmentConflict) {
						t.Fatal(err)
					}
				}
				if successes != 1 {
					t.Fatalf("concurrent starts acquired %d owners, want one", successes)
				}
				owner := ""
				for _, id := range serviceIDs {
					service, err := services.GetService(ctx, id)
					if err != nil || service.CurrentStreamID == "" {
						t.Fatal("missing committed owner", err)
					}
					if owner == "" {
						owner = service.CurrentStreamID
					} else if owner != service.CurrentStreamID {
						t.Fatal("start left a partial ownership split")
					}
				}
				return
			}
			if name == "changed_selection" {
				req := request(b)
				changed := selected
				changed.WorkerServiceID = ""
				setSelectedClaimFixture(t, streams, b.ID, changed)
				before, _ := streams.GetStream(ctx, b.ID)
				if _, err := streams.ClaimStreamStart(ctx, req); !errors.Is(err, ErrServiceAssignmentConflict) {
					t.Fatal("stale selection was accepted", err)
				}
				after, _ := streams.GetStream(ctx, b.ID)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("stale start changed the frame")
				}
				for _, id := range serviceIDs {
					service, _ := services.GetService(ctx, id)
					if service.CurrentStreamID != "" {
						t.Fatal("stale start acquired an execution owner")
					}
				}
				return
			}
			if name == "partial_availability" {
				if _, err := services.(ServiceAssignmentGuardStore).AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{ServiceID: selected.WorkerServiceID, StreamID: a.ID, AssignmentRole: "primary"}); err != nil {
					t.Fatal(err)
				}
				if _, err := streams.UpdateStreamStatus(ctx, a.ID, "live"); err != nil {
					t.Fatal(err)
				}
				before, _ := streams.GetStream(ctx, b.ID)
				if _, err := streams.ClaimStreamStart(ctx, request(b)); !errors.Is(err, ErrServiceAssignmentConflict) {
					t.Fatal("partially available nodes were claimed", err)
				}
				after, _ := streams.GetStream(ctx, b.ID)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("rejected start changed the frame")
				}
				for _, id := range []string{selected.EncoderServiceID, selected.DiscordServiceID} {
					service, _ := services.GetService(ctx, id)
					if service.CurrentStreamID != "" {
						t.Fatal("rejected start acquired a partial owner")
					}
				}
				return
			}
			first, err := streams.ClaimStreamStart(ctx, request(a))
			if err != nil {
				t.Fatal(err)
			}
			if _, changed, err := streams.TransitionClaimedStreamStart(ctx, first.OwnershipClaim, "live"); err != nil || !changed {
				t.Fatal("first start did not finalize", err)
			}
			before, _ := streams.GetStream(ctx, b.ID)
			beforeAssignments, _ := services.ListStreamAssignments(ctx, a.ID)
			if _, err := streams.ClaimStreamStart(ctx, request(b)); !errors.Is(err, ErrServiceAssignmentConflict) {
				t.Fatal("second frame stole a running Node", err)
			}
			after, _ := streams.GetStream(ctx, b.ID)
			afterAssignments, _ := services.ListStreamAssignments(ctx, a.ID)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeAssignments, afterAssignments) {
				t.Fatal("rejected start changed a frame or running assignments")
			}
			if _, err := streams.UpdateStreamStatus(ctx, a.ID, "completed"); err != nil {
				t.Fatal(err)
			}
			second, err := streams.ClaimStreamStart(ctx, request(b))
			if err != nil {
				t.Fatal("available shared Nodes could not be acquired", err)
			}
			if len(second.PrimaryAssignments) != 3 {
				t.Fatal("start did not acquire all three runtime owners")
			}
			previous, _ := streams.GetStream(ctx, a.ID)
			if streamNodeSelections(previous) != selected {
				t.Fatal("reuse erased the previous frame's saved choices")
			}
			if _, changed, _ := streams.TransitionClaimedStreamStart(ctx, first.OwnershipClaim, "failed"); changed {
				t.Fatal("late completion changed an old start")
			}
			for _, id := range serviceIDs {
				service, _ := services.GetService(ctx, id)
				if service.CurrentStreamID != b.ID {
					t.Fatal("late completion changed the new Node owner")
				}
			}
		})
	}
}

// The fixture establishes persisted selection input; the production start claim
// itself performs all owner discovery, locks, validation and mutations.
func setSelectedClaimFixture(t *testing.T, streams clockClaimStore, id string, selected StreamNodeSelections) {
	t.Helper()
	switch s := streams.(type) {
	case *MemoryStreamStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		stream := s.streams[id]
		applyStreamNodeSelections(&stream, selected)
		s.streams[id] = stream
	case MariaDBStreamStore:
		tx, err := s.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := saveStreamNodeSelectionsTx(t.Context(), tx, id, selected, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unsupported real store")
	}
}

func cleanupSelectedClaimServices(t *testing.T, streams clockClaimStore, serviceIDs, tokenIDs []string) {
	t.Helper()
	db, ok := streams.(MariaDBStreamStore)
	if !ok {
		return
	}
	for _, item := range []struct { query string; ids []string }{
		{"DELETE FROM services WHERE service_id=?", serviceIDs},
		{"DELETE FROM service_tokens WHERE id=?", tokenIDs},
	} {
		for _, id := range item.ids {
			if _, err := db.db.Exec(item.query, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
		}
	}
}
