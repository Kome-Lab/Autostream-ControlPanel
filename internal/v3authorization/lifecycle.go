package v3authorization

import (
	"context"
	"database/sql"
	"errors"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

// Configuration is immutable per composed owner. Passing nil is an explicit
// request for a closed required Gate, never a return to the legacy path.
func (a *SQLAuthority) WithLifecycleGate(g *lf.Gate) *SQLAuthority {
	copy := *a
	if g == nil { g = lf.NewRequiredGate() }
	copy.lifecycle = g
	return &copy
}
func (s *SQLStore) WithLifecycleGate(g *lf.Gate) *SQLStore {
	copy := *s
	if g == nil { g = lf.NewRequiredGate() }
	copy.lifecycle = g
	return &copy
}
func (s *Service) WithLifecycleGate(g *lf.Gate) *Service {
	copy := *s
	if g == nil { g = lf.NewRequiredGate() }
	copy.lifecycle = g
	return &copy
}

func lifecycleError(err error) error {
	if errors.Is(err, lf.ErrUnknown) { return ErrStoreUnknown }
	return ErrUnavailable
}
func lifecycleCall[T any](ctx context.Context, g *lf.Gate, call lf.Call, fn func(context.Context) (T, error)) (T, error) {
	var result T
	err := g.Do(ctx, call, func(admitted context.Context) error {
		var e error
		result, e = fn(admitted)
		return e
	})
	if err != nil { var zero T; return zero, lifecycleError(err) }
	return result, nil
}
func lifecycleExecutor(ctx context.Context, db *sql.DB) (lf.Executor, error) {
	q, bound, err := lf.SQL(ctx, db)
	if err != nil { return nil, err }
	if bound { return q, nil }
	return db, nil
}
func managementLifecycleResult(err error) ManagementResult {
	return managementFailure(lifecycleError(err))
}
func managementLifecycleError(result ManagementResult) error {
	switch result.Status {
	case ManagementApplied, ManagementInspected: return nil
	case Denied: return ErrDenied
	case Conflict: return ErrConflict
	case Invalid: return ErrInvalid
	case Unknown: return ErrStoreUnknown
	default: return ErrUnavailable
	}
}
func (s *Service) lifecycleDependencies() error {
	if s.lifecycle == nil {
		if a, ok := s.deps.Authority.(*SQLAuthority); ok && a.lifecycle != nil { return ErrUnavailable }
		if store, ok := s.deps.Store.(*SQLStore); ok && store.lifecycle != nil { return ErrUnavailable }
		return nil
	}
	if s.lifecycle.Ready() != nil { return ErrUnavailable }
	a, ok := s.deps.Authority.(*SQLAuthority)
	if !ok || a.lifecycle != s.lifecycle { return ErrUnavailable }
	store, ok := s.deps.Store.(*SQLStore)
	if !ok || store.lifecycle != s.lifecycle || a.db != store.db { return ErrUnavailable }
	return nil
}
