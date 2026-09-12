package httpapi

// routes composes domains in the accepted registration order.
func (s *Server) routes() {
	s.registerPublicRoutes()
	s.registerLoginRoutes()
	s.registerServiceRuntimeRoutes()
	s.registerServiceUpdaterRoutes()
	s.registerAccountRoutes()
	s.registerUserRoleRoutes()
	s.registerResourceRoutes()
	s.registerIntegrationRoutes()
	s.registerNodeServiceRoutes()
	s.registerStreamRoutes()
	s.registerArchivePreviewRoutes()
	s.registerAuditSettingsRoutes()
	s.registerSystemUpdateRoutes()
	s.registerSecretRoutes()
	s.registerObservabilityRoutes()
}
