package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"sort"

	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
)

// Fixture generations detect observed changes, not unobserved ABA. Production
// uses SQL triggers participating in every source authorization mutation.
func (s *MemoryAuthStore) operationalRevisionLocked() (uint64, error) {
	type userEntry struct {
		ID, State string
		RoleIDs   []string
	}
	type roleEntry struct {
		ID, Name   string
		Operations []string
	}
	var source struct {
		Users []userEntry
		Roles []roleEntry
	}
	for id, user := range s.users {
		ids := append([]string(nil), user.RoleIDs...)
		sort.Strings(ids)
		source.Users = append(source.Users, userEntry{ID: id, State: user.Status, RoleIDs: ids})
	}
	sort.Slice(source.Users, func(i, j int) bool { return source.Users[i].ID < source.Users[j].ID })
	for id, role := range s.roles {
		operations := append([]string(nil), role.Permissions...)
		sort.Strings(operations)
		source.Roles = append(source.Roles, roleEntry{ID: id, Name: role.Name, Operations: operations})
	}
	sort.Slice(source.Roles, func(i, j int) bool { return source.Roles[i].ID < source.Roles[j].ID })
	encoded, err := json.Marshal(source)
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(encoded)
	if s.operationalRevision == 0 || digest != s.operationalDigest {
		if s.operationalRevision == math.MaxUint64 {
			return 0, errOperationalSource
		}
		s.operationalRevision++
		s.operationalDigest = digest
	}
	return s.operationalRevision, nil
}

func (s *MemoryAuthStore) Revision(ctx context.Context) (uint64, error) {
	if ctx == nil || s == nil {
		return 0, errOperationalSource
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.operationalRevisionLocked()
}

func (s *MemoryAuthStore) Snapshot(ctx context.Context, principalID string) (oa.Snapshot, error) {
	if ctx == nil || s == nil {
		return oa.Snapshot{}, errOperationalSource
	}
	if err := ctx.Err(); err != nil {
		return oa.Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return oa.Snapshot{}, err
	}
	generation, err := s.operationalRevisionLocked()
	if err != nil {
		return oa.Snapshot{}, err
	}
	user, ok := s.users[principalID]
	if !ok || user.ID != principalID {
		return oa.Snapshot{}, ErrNotFound
	}
	principal := oa.Principal{ID: user.ID, Kind: oa.KindHuman, State: user.Status, RoleIDs: append([]string(nil), user.RoleIDs...)}
	sort.Strings(principal.RoleIDs)
	roles := make([]oa.Role, 0, len(principal.RoleIDs))
	for _, id := range principal.RoleIDs {
		role, ok := s.roles[id]
		if !ok || role.ID != id {
			return oa.Snapshot{}, errOperationalSource
		}
		roles = append(roles, oa.Role{ID: role.ID, Name: role.Name, Operations: append([]string(nil), role.Permissions...)})
	}
	return oa.Project(principal, roles, generation)
}
