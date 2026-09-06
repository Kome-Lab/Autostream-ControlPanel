package store

import "context"

// SystemUpdatePortCommitObservation identifies the local database boundary.
// It contains no token, payload, or mutable authorization decision.
type SystemUpdatePortCommitObservation struct {
	Phase  string
	JobID  string
	Status string
}

const (
	SystemUpdatePortBeforeCommit = "before_commit"
	SystemUpdatePortAfterCommit  = "after_commit"
)

type systemUpdatePortCommitObserverKey struct{}

// WithSystemUpdatePortCommitObserver attaches an in-process observer to one
// operation. The normal request path never installs one. It cannot alter a
// result or grant authority, and is not exposed through HTTP or configuration.
func WithSystemUpdatePortCommitObserver(ctx context.Context, observer func(SystemUpdatePortCommitObservation)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, systemUpdatePortCommitObserverKey{}, observer)
}

func observeSystemUpdatePortCommit(ctx context.Context, job SystemUpdateJob, status, phase string) {
	observer, _ := ctx.Value(systemUpdatePortCommitObserverKey{}).(func(SystemUpdatePortCommitObservation))
	if observer != nil && isSystemUpdatePortV2(job) && isTerminalSystemUpdateStatus(status) {
		observer(SystemUpdatePortCommitObservation{Phase: phase, JobID: job.ID, Status: status})
	}
}
