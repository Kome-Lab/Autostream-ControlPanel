package httpapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/store"
)

func formatSafeHTTPSensitiveDiagnostic(value any) string {
	return fmt.Sprintf("type=%T details=redacted", value)
}

type failingAuditStore struct{}

func (failingAuditStore) WriteAudit(context.Context, store.AuditEvent) error {
	return errors.New("audit store unavailable")
}

func (failingAuditStore) ListAudit(context.Context, store.AuditFilter) ([]store.AuditEvent, error) {
	return nil, errors.New("audit store unavailable")
}

func artifactListContains(artifacts []store.StreamArtifact, name, relativePath string) bool {
	for _, artifact := range artifacts {
		if artifact.Name == name && (relativePath == "" || artifact.RelativePath == relativePath) {
			return true
		}
	}
	return false
}

type failOnStreamAssignmentRegistry struct {
	store.ServiceRegistryStore
	failServiceID string
}

func (s failOnStreamAssignmentRegistry) AssignServiceToStreamWithRole(ctx context.Context, serviceID, streamID, actorUserID, assignmentRole string) (store.RegisteredService, error) {
	if serviceID == s.failServiceID {
		return store.RegisteredService{}, errors.New("injected stream assignment failure")
	}
	return s.ServiceRegistryStore.AssignServiceToStreamWithRole(ctx, serviceID, streamID, actorUserID, assignmentRole)
}

func (s failOnStreamAssignmentRegistry) AssignServiceToStreamGuarded(ctx context.Context, mutation store.ServiceAssignmentMutation) (store.RegisteredService, error) {
	if mutation.ServiceID == s.failServiceID {
		return store.RegisteredService{}, errors.New("injected stream assignment failure")
	}
	return s.ServiceRegistryStore.AssignServiceToStreamGuarded(ctx, mutation)
}
