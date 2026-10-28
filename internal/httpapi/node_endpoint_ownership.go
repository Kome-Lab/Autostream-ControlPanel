package httpapi

import (
	"context"

	"github.com/example/autostream-control-panel/internal/store"
)

// activePullManagedSystemdTarget verifies the complete durable ownership
// binding before treating a target as pull-managed. A configured pull target
// with inconsistent or unavailable ownership state is returned as an error so
// callers cannot fall back to an unfenced direct endpoint mutation.
func (s *Server) activePullManagedSystemdTarget(
	ctx context.Context,
	service store.RegisteredService,
) (bool, error) {
	ownerships, _ := s.systemUpdates.(store.SystemUpdateExecutionHostStore)
	return store.ServiceEndpointPullManaged(ctx, service, s.services, s.updaterPolicies, ownerships)
}

func sameNodeEndpoint(
	service store.RegisteredService,
	host string,
	port int,
	sslEnabled bool,
	publicURL string,
) bool {
	return service.Host == host &&
		service.Port == port &&
		service.SSLEnabled == sslEnabled &&
		service.PublicURL == publicURL
}
