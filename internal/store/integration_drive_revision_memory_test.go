package store

import (
	"errors"
	"testing"
	"time"
)

func newMemoryDriveRevisionFixture(t *testing.T) (*MemoryIntegrationStore, DriveDestination) {
	t.Helper()
	store := NewMemoryIntegrationStore()
	created, err := store.CreateDriveDestination(t.Context(), DriveDestination{Name: "initial destination", AuthMode: "oauth2", OAuthAccountID: "synthetic-account", FolderID: "synthetic-initial-folder"})
	if err != nil {
		t.Fatal(err)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, created.UpdatedAt)
	if err != nil || timestamp.Nanosecond()%1000 != 0 || created.ExpectedUpdatedAt != nil {
		t.Fatal("created destination did not expose canonical microsecond authority")
	}
	return store, created
}

func TestMemoryDriveDestinationRevisionRejectsStaleFolderUpdate(t *testing.T) {
	store, created := newMemoryDriveRevisionFixture(t)
	expected := created.UpdatedAt
	update := created
	update.Name, update.FolderID, update.ExpectedUpdatedAt = "accepted destination", "synthetic-accepted-folder", &expected
	saved, err := store.UpdateDriveDestination(t.Context(), update)
	if err != nil || saved.FolderID != "" || saved.ExpectedUpdatedAt != nil || saved.UpdatedAt == expected {
		t.Fatal("current destination update was not accepted safely")
	}
	update.Name, update.FolderID = "stale destination", "synthetic-stale-folder"
	if _, err = store.UpdateDriveDestination(t.Context(), update); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("stale destination update was not rejected")
	}
	current, err := store.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil || current.Name != saved.Name || current.FolderID != "synthetic-accepted-folder" || current.UpdatedAt != saved.UpdatedAt || current.ExpectedUpdatedAt != nil {
		t.Fatal("stale destination request changed its accepted metadata or folder")
	}
	nextExpected := saved.UpdatedAt
	update = saved
	update.Name, update.ExpectedUpdatedAt = "retained folder", &nextExpected
	retained, err := store.UpdateDriveDestination(t.Context(), update)
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil || current.FolderID != "synthetic-accepted-folder" || current.FolderIDFingerprint != retained.FolderIDFingerprint {
		t.Fatal("omitted folder did not preserve the accepted secret and metadata")
	}
}

func TestMemoryDriveDestinationRevisionAdvancesWhenClockIsBehind(t *testing.T) {
	store, created := newMemoryDriveRevisionFixture(t)
	previous := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	expected := previous.Format(time.RFC3339Nano)
	store.mu.Lock()
	seeded := store.destinations[created.ID]
	seeded.UpdatedAt = expected
	store.destinations[created.ID] = seeded
	store.mu.Unlock()
	created.Name, created.ExpectedUpdatedAt = "future authority", &expected
	saved, err := store.UpdateDriveDestination(t.Context(), created)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := time.Parse(time.RFC3339Nano, saved.UpdatedAt)
	if err != nil || !actual.Equal(previous.Add(time.Microsecond)) {
		t.Fatal("destination revision did not monotonically advance from a future authority")
	}
}

func TestMemoryDriveDestinationRevisionConcurrentWritersUseOneAtomicCAS(t *testing.T) {
	store, created := newMemoryDriveRevisionFixture(t)
	expected := created.UpdatedAt
	type result struct {
		saved  DriveDestination
		err    error
		folder string
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, name := range []string{"first", "second"} {
		go func(name string) {
			<-start
			update := created
			update.Name, update.FolderID, update.ExpectedUpdatedAt = name, "synthetic-"+name+"-folder", &expected
			saved, err := store.UpdateDriveDestination(t.Context(), update)
			results <- result{saved: saved, err: err, folder: update.FolderID}
		}(name)
	}
	close(start)
	successes, conflicts := 0, 0
	var winner result
	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				successes++
				winner = result
			} else if errors.Is(result.err, ErrRevisionConflict) {
				conflicts++
			} else {
				t.Fatal("concurrent destination update returned an unexpected error")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent destination update did not finish")
		}
	}
	actual, err := store.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if successes != 1 || conflicts != 1 || err != nil || actual.Name != winner.saved.Name || actual.FolderID != winner.folder {
		t.Fatal("destination CAS admitted more than one writer or published the losing folder")
	}
}

func TestMemoryDriveDestinationRevisionRejectsMalformedAuthorityBeforeFolderWrite(t *testing.T) {
	store, created := newMemoryDriveRevisionFixture(t)
	invalid := "not-a-timestamp"
	created.Name, created.FolderID, created.ExpectedUpdatedAt = "attempted", "synthetic-attempted-folder", &invalid
	if _, err := store.UpdateDriveDestination(t.Context(), created); !errors.Is(err, ErrInvalidResourceRevision) {
		t.Fatal("malformed destination authority was not rejected")
	}
	actual, err := store.GetDriveDestinationForDispatch(t.Context(), created.ID)
	if err != nil || actual.Name != "initial destination" || actual.FolderID != "synthetic-initial-folder" {
		t.Fatal("invalid destination authority changed metadata or folder")
	}
}
