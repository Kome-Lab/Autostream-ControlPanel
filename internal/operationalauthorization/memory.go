package operationalauthorization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

type HistoryEntry struct {
	Generation  uint64
	PrincipalID string
	Checksum    string
}

// MemoryStore is an isolated CAS store, not a user-facing mutation endpoint.
type MemoryStore struct {
	mu         sync.RWMutex
	generation uint64
	snapshots  map[string]Snapshot
	history    []HistoryEntry
}

func NewMemoryStore(initial ...Snapshot) (*MemoryStore, error) {
	out := &MemoryStore{generation: 1, snapshots: make(map[string]Snapshot)}
	for _, snapshot := range initial {
		snapshot = withRevision(snapshot, 1)
		if err := validateSnapshot(snapshot, snapshot.Principal.ID); err != nil {
			return nil, err
		}
		if _, exists := out.snapshots[snapshot.Principal.ID]; exists {
			return nil, ErrInvalidSnapshot
		}
		out.snapshots[snapshot.Principal.ID] = snapshot
		out.history = append(out.history, historyEntry(snapshot, 1))
	}
	return out, nil
}
func withRevision(snapshot Snapshot, revision uint64) Snapshot {
	out := cloneSnapshot(snapshot)
	out.Revision = revision
	for i := range out.Grants {
		out.Grants[i].Revision = revision
	}
	return out
}
func historyEntry(snapshot Snapshot, generation uint64) HistoryEntry {
	body, _ := json.Marshal(snapshot)
	digest := sha256.Sum256(body)
	return HistoryEntry{Generation: generation, PrincipalID: snapshot.Principal.ID, Checksum: hex.EncodeToString(digest[:])}
}
func (s *MemoryStore) Snapshot(ctx context.Context, principalID string) (Snapshot, error) {
	if ctx == nil || s == nil {
		return Snapshot{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, exists := s.snapshots[principalID]
	if !exists {
		return Snapshot{}, ErrDenied
	}
	out := cloneSnapshot(snapshot)
	out.Revision = s.generation
	return out, nil
}
func (s *MemoryStore) Revision(ctx context.Context) (uint64, error) {
	if ctx == nil || s == nil {
		return 0, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation, nil
}
func (s *MemoryStore) Replace(ctx context.Context, expected uint64, snapshot Snapshot) (uint64, error) {
	if ctx == nil || s == nil {
		return 0, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected == 0 || expected != s.generation || s.generation == ^uint64(0) {
		return 0, ErrConflict
	}
	next := s.generation + 1
	candidate := withRevision(snapshot, next)
	if err := validateSnapshot(candidate, candidate.Principal.ID); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.snapshots[candidate.Principal.ID] = candidate
	s.history = append(s.history, historyEntry(candidate, next))
	s.generation = next
	return next, nil
}
func (s *MemoryStore) Revoke(ctx context.Context, expected uint64, principalID, grantID string) (uint64, error) {
	if ctx == nil || s == nil {
		return 0, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected == 0 || expected != s.generation || s.generation == ^uint64(0) {
		return 0, ErrConflict
	}
	current, exists := s.snapshots[principalID]
	if !exists {
		return 0, ErrDenied
	}
	next := s.generation + 1
	candidate := withRevision(current, next)
	found := false
	for i := range candidate.Grants {
		if candidate.Grants[i].ID == grantID {
			if candidate.Grants[i].State != StateActive {
				return 0, ErrConflict
			}
			candidate.Grants[i].State = StateRevoked
			found = true
			break
		}
	}
	if !found {
		return 0, ErrDenied
	}
	if err := validateSnapshot(candidate, principalID); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.snapshots[principalID] = candidate
	s.history = append(s.history, historyEntry(candidate, next))
	s.generation = next
	return next, nil
}
func (s *MemoryStore) History(ctx context.Context) ([]HistoryEntry, error) {
	if ctx == nil || s == nil {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]HistoryEntry(nil), s.history...), nil
}
