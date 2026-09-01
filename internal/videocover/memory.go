package videocover

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type memoryAction struct {
	Fingerprint       string
	RequestedRevision uint64
	Status            string
	SafeErrorCode     string
}
type MemoryRepository struct {
	mu      sync.Mutex
	presets map[string]Preset
	states  map[string]map[uint64]State
	actions map[string]memoryAction
	now     func() time.Time
	nextID  uint64
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{presets: map[string]Preset{}, states: map[string]map[uint64]State{}, actions: map[string]memoryAction{}, now: func() time.Time { return time.Now().UTC() }}
}
func (m *MemoryRepository) newID() string { m.nextID++; return fmtID(m.nextID) }
func fmtID(id uint64) string              { return "00000000-0000-4000-8000-" + fmtUint(id) }
func fmtUint(value uint64) string {
	s := ""
	for value > 0 {
		s = string(rune('0'+value%10)) + s
		value /= 10
	}
	if s == "" {
		s = "0"
	}
	for len(s) < 12 {
		s = "0" + s
	}
	return s
}

func (m *MemoryRepository) ListPresets(_ context.Context) ([]Preset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []Preset{}
	for _, p := range m.presets {
		if p.DeletedAt == nil {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result, nil
}
func (m *MemoryRepository) GetPreset(_ context.Context, id string, includeDeleted bool) (Preset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.presets[id]
	if !ok || (!includeDeleted && p.DeletedAt != nil) {
		return Preset{}, ErrNotFound
	}
	return p, nil
}
func validatePreset(p Preset) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len([]rune(p.Name)) > 128 || strings.TrimSpace(p.AssetID) == "" || strings.TrimSpace(p.AssetVariantID) == "" {
		return ErrInvalidRequest
	}
	return nil
}
func (m *MemoryRepository) CreatePreset(_ context.Context, p Preset) (Preset, error) {
	if err := validatePreset(p); err != nil {
		return Preset{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.presets {
		if x.DeletedAt == nil && strings.EqualFold(x.Name, p.Name) {
			return Preset{}, ErrIdempotencyConflict
		}
	}
	now := m.now()
	p.ID = m.newID()
	p.Revision = 1
	p.UpdatedByUserID = p.CreatedByUserID
	p.CreatedAt = now
	p.UpdatedAt = now
	m.presets[p.ID] = p
	return p, nil
}
func (m *MemoryRepository) UpdatePreset(_ context.Context, id string, p Preset, expected uint64) (Preset, error) {
	if err := validatePreset(p); err != nil {
		return Preset{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.presets[id]
	if !ok || current.DeletedAt != nil {
		return Preset{}, ErrNotFound
	}
	if current.Revision != expected {
		return Preset{}, ErrRevisionConflict
	}
	for otherID, x := range m.presets {
		if otherID != id && x.DeletedAt == nil && strings.EqualFold(x.Name, p.Name) {
			return Preset{}, ErrIdempotencyConflict
		}
	}
	current.Name = strings.TrimSpace(p.Name)
	current.AssetID = p.AssetID
	current.AssetVariantID = p.AssetVariantID
	current.Enabled = p.Enabled
	current.UpdatedByUserID = p.UpdatedByUserID
	current.Revision++
	current.UpdatedAt = m.now()
	m.presets[id] = current
	return current, nil
}
func (m *MemoryRepository) DeletePreset(_ context.Context, id, actor string, expected uint64) (Preset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.presets[id]
	if !ok || p.DeletedAt != nil {
		return Preset{}, ErrNotFound
	}
	if p.Revision != expected {
		return Preset{}, ErrRevisionConflict
	}
	now := m.now()
	p.DeletedAt = &now
	p.Revision++
	p.UpdatedAt = now
	p.UpdatedByUserID = actor
	m.presets[id] = p
	return p, nil
}

func (m *MemoryRepository) EnsureGeneration(_ context.Context, streamID string, generation uint64, variantID string, desired bool) (State, error) {
	if generation < 1 {
		return State{}, ErrInvalidRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.states[streamID] == nil {
		m.states[streamID] = map[uint64]State{}
	}
	if state, ok := m.states[streamID][generation]; ok {
		return NormalizeState(state), nil
	}
	now := m.now()
	state := State{StreamID: streamID, JobGeneration: generation, DesiredActive: desired, DesiredRevision: 1, AssetVariantID: variantID, Status: "idle", CreatedAt: now, UpdatedAt: now}
	m.states[streamID][generation] = state
	return NormalizeState(state), nil
}
func (m *MemoryRepository) RecordStartApplied(_ context.Context, streamID string, generation uint64, active bool, revision uint64) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[streamID][generation]
	if !ok {
		return State{}, ErrNotFound
	}
	if revision == 0 || state.DesiredRevision != revision || state.DesiredActive != active {
		return State{}, ErrRevisionConflict
	}
	state.AppliedActive = &active
	state.AppliedRevision = &revision
	state.Status = "applied"
	state.LastErrorCode = ""
	state.UpdatedAt = m.now()
	m.states[streamID][generation] = state
	return NormalizeState(state), nil
}
func (m *MemoryRepository) GetCurrentState(_ context.Context, streamID string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected State
	found := false
	for generation, state := range m.states[streamID] {
		if !found || generation > selected.JobGeneration {
			selected = state
			found = true
		}
	}
	if !found {
		return State{}, ErrNotFound
	}
	return NormalizeState(selected), nil
}
func actionKey(streamID string, generation uint64, key string) string {
	return streamID + "/" + fmtUint(generation) + "/" + key
}

func (m *MemoryRepository) LookupActionReplay(_ context.Context, streamID string, request ActionRequest) (PreparedAction, bool, error) {
	if err := ValidateRequest(request); err != nil {
		return PreparedAction{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	states := m.states[streamID]
	var latest uint64
	for generation := range states {
		if generation > latest {
			latest = generation
		}
	}
	if latest == 0 {
		return PreparedAction{}, false, ErrNotFound
	}
	state := states[latest]
	if latest != request.ExpectedJobGeneration {
		return PreparedAction{State: NormalizeState(state)}, false, nil
	}
	prior, ok := m.actions[actionKey(streamID, latest, request.IdempotencyKey)]
	if !ok {
		return PreparedAction{State: NormalizeState(state)}, false, nil
	}
	if prior.Fingerprint != RequestFingerprint(request) {
		return PreparedAction{}, false, ErrIdempotencyConflict
	}
	return PreparedAction{
		State: NormalizeState(state), Replay: true, Dispatch: false, RequestedRevision: prior.RequestedRevision,
		Outcome: prior.Status, SafeErrorCode: prior.SafeErrorCode,
	}, true, nil
}

// LookupActionOutcome reads one exact action without applying the
// current-generation fence used by the preflight replay lookup. It is used
// only after Record* has durably resolved that action, so a concurrent
// generation rollover cannot hide its immutable outcome.
func (m *MemoryRepository) LookupActionOutcome(_ context.Context, streamID string, request ActionRequest) (PreparedAction, bool, error) {
	if err := ValidateRequest(request); err != nil {
		return PreparedAction{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[streamID][request.ExpectedJobGeneration]
	if !ok {
		return PreparedAction{}, false, ErrNotFound
	}
	prior, ok := m.actions[actionKey(streamID, request.ExpectedJobGeneration, request.IdempotencyKey)]
	if !ok {
		return PreparedAction{State: NormalizeState(state)}, false, nil
	}
	if prior.Fingerprint != RequestFingerprint(request) {
		return PreparedAction{}, false, ErrIdempotencyConflict
	}
	return PreparedAction{
		State: NormalizeState(state), Replay: true, Dispatch: false, RequestedRevision: prior.RequestedRevision,
		Outcome: prior.Status, SafeErrorCode: prior.SafeErrorCode,
	}, true, nil
}

func (m *MemoryRepository) PrepareAction(_ context.Context, streamID string, request ActionRequest) (PreparedAction, error) {
	if err := ValidateRequest(request); err != nil {
		return PreparedAction{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	states := m.states[streamID]
	var latest uint64
	for generation := range states {
		if generation > latest {
			latest = generation
		}
	}
	if latest != request.ExpectedJobGeneration {
		return PreparedAction{}, ErrStaleGeneration
	}
	state := states[latest]
	key := actionKey(streamID, latest, request.IdempotencyKey)
	fingerprint := RequestFingerprint(request)
	if prior, ok := m.actions[key]; ok {
		if prior.Fingerprint != fingerprint {
			return PreparedAction{}, ErrIdempotencyConflict
		}
		return PreparedAction{
			State: NormalizeState(state), Replay: true, Dispatch: false, RequestedRevision: prior.RequestedRevision,
			Outcome: prior.Status, SafeErrorCode: prior.SafeErrorCode,
		}, nil
	}
	if state.DesiredRevision != request.ExpectedRevision {
		return PreparedAction{}, ErrRevisionConflict
	}
	next := state.DesiredRevision + 1
	for existingKey, action := range m.actions {
		if strings.HasPrefix(existingKey, streamID+"/"+fmtUint(latest)+"/") && action.RequestedRevision == next {
			return PreparedAction{}, ErrRevisionConflict
		}
	}
	now := m.now()
	state.DesiredActive = request.Active
	state.DesiredRevision = next
	state.LastIdempotencyKey = request.IdempotencyKey
	state.Status = "idle"
	state.LastErrorCode = ""
	state.UpdatedAt = now
	states[latest] = state
	m.actions[key] = memoryAction{Fingerprint: fingerprint, RequestedRevision: next, Status: "pending"}
	return PreparedAction{State: NormalizeState(state), Dispatch: true, RequestedRevision: next}, nil
}
func (m *MemoryRepository) RecordApplied(_ context.Context, streamID string, generation uint64, key string, active bool, revision uint64) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[streamID][generation]
	if !ok {
		return State{}, ErrNotFound
	}
	actionKey := actionKey(streamID, generation, key)
	action, ok := m.actions[actionKey]
	if !ok || action.RequestedRevision != revision {
		return State{}, ErrRevisionConflict
	}
	if !actionMayResolve(action.Status) {
		return NormalizeState(state), nil
	}
	updateRuntime := currentActionMayResolve(state, key, action)
	action.Status = "applied"
	action.SafeErrorCode = ""
	m.actions[actionKey] = action
	if !updateRuntime {
		return NormalizeState(state), nil
	}
	state.AppliedActive = &active
	state.AppliedRevision = &revision
	state.Status = "applied"
	state.LastErrorCode = ""
	state.UpdatedAt = m.now()
	m.states[streamID][generation] = state
	return NormalizeState(state), nil
}
func (m *MemoryRepository) RecordAmbiguous(_ context.Context, streamID string, generation uint64, key string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[streamID][generation]
	if !ok {
		return State{}, ErrNotFound
	}
	actionKey := actionKey(streamID, generation, key)
	action, ok := m.actions[actionKey]
	if !ok {
		return State{}, ErrNotFound
	}
	if !actionMayResolve(action.Status) {
		return NormalizeState(state), nil
	}
	updateRuntime := currentActionMayResolve(state, key, action)
	action.Status = "confirming"
	action.SafeErrorCode = "transport_outcome_unknown"
	m.actions[actionKey] = action
	if !updateRuntime {
		return NormalizeState(state), nil
	}
	state.Status = "confirming"
	state.LastErrorCode = "transport_outcome_unknown"
	state.UpdatedAt = m.now()
	m.states[streamID][generation] = state
	return NormalizeState(state), nil
}
func (m *MemoryRepository) RecordFailed(_ context.Context, streamID string, generation uint64, key, code string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[streamID][generation]
	if !ok {
		return State{}, ErrNotFound
	}
	actionKey := actionKey(streamID, generation, key)
	action, ok := m.actions[actionKey]
	if !ok {
		return State{}, ErrNotFound
	}
	if !actionMayResolve(action.Status) {
		return NormalizeState(state), nil
	}
	updateRuntime := currentActionMayResolve(state, key, action)
	action.Status = "failed"
	action.SafeErrorCode = safeCode(code)
	m.actions[actionKey] = action
	if !updateRuntime {
		return NormalizeState(state), nil
	}
	state.Status = "failed"
	state.LastErrorCode = action.SafeErrorCode
	state.UpdatedAt = m.now()
	m.states[streamID][generation] = state
	return NormalizeState(state), nil
}

func currentActionMayResolve(state State, key string, action memoryAction) bool {
	if state.LastIdempotencyKey != key || state.DesiredRevision != action.RequestedRevision {
		return false
	}
	return state.Status == "idle" && action.Status == "pending" ||
		state.Status == "confirming" && action.Status == "confirming"
}

func actionMayResolve(status string) bool {
	return status == "pending" || status == "confirming"
}
func safeCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 80 {
		return "video_cover_action_failed"
	}
	for _, r := range code {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return "video_cover_action_failed"
		}
	}
	return code
}

var _ Repository = (*MemoryRepository)(nil)
