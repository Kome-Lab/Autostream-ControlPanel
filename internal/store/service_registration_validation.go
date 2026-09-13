package store

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"net/url"
	"strconv"
	"strings"
)

func validateServiceRegistration(registration ServiceRegistration) error {
	registration = normalizeServiceRegistration(registration)
	if !serviceIDPattern.MatchString(registration.ServiceID) || strings.EqualFold(registration.ServiceID, "control-panel") || strings.TrimSpace(registration.ServiceName) == "" {
		return ErrInvalidServiceRegistration
	}
	if err := validateServiceType(registration.ServiceType); err != nil {
		return err
	}
	if registration.ServiceType == "update_agent" {
		if registration.TransportMode != SystemUpdateTransportPullV2 ||
			!executionHostIDPattern.MatchString(registration.ExecutionHostID) ||
			registration.OwnershipEpoch < 0 ||
			registration.Host != "" ||
			registration.Port != 0 ||
			registration.SSLEnabled ||
			registration.PublicURL != "" {
			return ErrInvalidServiceRegistration
		}
		return nil
	} else if registration.TransportMode != "" || registration.ExecutionHostID != "" || registration.OwnershipEpoch != 0 {
		return ErrInvalidServiceRegistration
	}
	if strings.TrimSpace(registration.PublicURL) == "" || registration.Port < 1 || registration.Port > 65535 {
		return ErrInvalidServiceRegistration
	}
	if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL(registration.PublicURL); err != nil {
		return ErrInvalidServiceRegistration
	}
	return nil
}

func validateServiceMetadataUpdate(update ServiceMetadataUpdate) error {
	update = normalizeServiceMetadataUpdate(update)
	if strings.TrimSpace(update.ServiceName) == "" {
		return ErrInvalidServiceRegistration
	}
	if update.PreserveEndpoint {
		if update.Endpointless {
			return ErrInvalidServiceRegistration
		}
		return nil
	}
	if update.Endpointless {
		if update.Host != "" || update.Port != 0 || update.SSLEnabled || update.PublicURL != "" {
			return ErrInvalidServiceRegistration
		}
		return nil
	}
	if strings.TrimSpace(update.PublicURL) == "" || update.Port < 1 || update.Port > 65535 {
		return ErrInvalidServiceRegistration
	}
	if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL(update.PublicURL); err != nil {
		return ErrInvalidServiceRegistration
	}
	return nil
}

func normalizeServiceRegistration(registration ServiceRegistration) ServiceRegistration {
	registration.ServiceID = strings.TrimSpace(registration.ServiceID)
	registration.ServiceType = strings.TrimSpace(registration.ServiceType)
	registration.ServiceName = strings.TrimSpace(registration.ServiceName)
	registration.Description = strings.TrimSpace(registration.Description)
	registration.TransportMode = strings.ToLower(strings.TrimSpace(registration.TransportMode))
	registration.ExecutionHostID = strings.TrimSpace(registration.ExecutionHostID)
	registration.Host = strings.TrimSpace(registration.Host)
	registration.PublicURL = strings.TrimSpace(registration.PublicURL)
	registration.Version = strings.TrimSpace(registration.Version)
	registration.Commit = truncateServiceReportedValue(strings.TrimSpace(registration.Commit), 80)
	registration.BuildDate = truncateServiceReportedValue(strings.TrimSpace(registration.BuildDate), 80)
	registration.Hostname = strings.TrimSpace(registration.Hostname)
	registration.OS = strings.TrimSpace(registration.OS)
	registration.Arch = strings.TrimSpace(registration.Arch)
	if registration.ServiceType == "update_agent" {
		return registration
	}
	if registration.Host == "" || registration.Port == 0 {
		host, port, sslEnabled := endpointFromServiceURL(registration.PublicURL)
		if registration.Host == "" {
			registration.Host = host
		}
		if registration.Port == 0 {
			registration.Port = port
		}
		registration.SSLEnabled = sslEnabled
	}
	if registration.PublicURL == "" {
		registration.PublicURL = buildServiceURL(registration.Host, registration.Port, registration.SSLEnabled)
	}
	return registration
}

func bindPrecreatedUpdateAgentRegistration(
	registration ServiceRegistration,
	transportMode string,
	executionHostID string,
	ownershipEpoch int64,
) ServiceRegistration {
	if registration.ServiceType != "update_agent" {
		return registration
	}
	registration.TransportMode = strings.ToLower(strings.TrimSpace(transportMode))
	registration.ExecutionHostID = strings.TrimSpace(executionHostID)
	registration.OwnershipEpoch = ownershipEpoch
	return registration
}

func serviceEndpoint(host string, port int, sslEnabled bool, publicURL string) *ServiceEndpoint {
	host = strings.TrimSpace(host)
	publicURL = strings.TrimSpace(publicURL)
	if host == "" || port == 0 {
		urlHost, urlPort, urlSSL := endpointFromServiceURL(publicURL)
		if host == "" {
			host = urlHost
		}
		if port == 0 {
			port = urlPort
		}
		if urlHost != "" {
			sslEnabled = urlSSL
		}
	}
	if publicURL == "" {
		publicURL = buildServiceURL(host, port, sslEnabled)
	}
	if host == "" || port < 1 || port > 65535 || publicURL == "" {
		return nil
	}
	return &ServiceEndpoint{
		Host:       host,
		Port:       port,
		SSLEnabled: sslEnabled,
		PublicURL:  publicURL,
	}
}

func copyServiceEndpoint(endpoint *ServiceEndpoint) *ServiceEndpoint {
	if endpoint == nil {
		return nil
	}
	copy := *endpoint
	return &copy
}

func hydrateServiceEndpointState(service *RegisteredService) {
	if service == nil {
		return
	}
	if service.AppliedEndpoint == nil {
		service.AppliedEndpoint = serviceEndpoint(service.Host, service.Port, service.SSLEnabled, service.PublicURL)
	}
	if service.DesiredEndpoint == nil {
		service.DesiredEndpoint = copyServiceEndpoint(service.AppliedEndpoint)
	}
	if service.EndpointRevision == 0 {
		service.EndpointRevision = 1
	}
	if service.EndpointStatus == "" {
		service.EndpointStatus = "applied"
	}
	if service.AppliedConfigRevision == 0 {
		service.AppliedConfigRevision = 1
	}
}

func normalizeServiceMetadataUpdate(update ServiceMetadataUpdate) ServiceMetadataUpdate {
	update.ServiceName = strings.TrimSpace(update.ServiceName)
	update.Description = strings.TrimSpace(update.Description)
	update.Host = strings.TrimSpace(update.Host)
	update.PublicURL = strings.TrimSpace(update.PublicURL)
	if update.Host == "" || update.Port == 0 {
		host, port, sslEnabled := endpointFromServiceURL(update.PublicURL)
		if update.Host == "" {
			update.Host = host
		}
		if update.Port == 0 {
			update.Port = port
		}
		if host != "" {
			update.SSLEnabled = sslEnabled
		}
	}
	if update.PublicURL == "" {
		update.PublicURL = buildServiceURL(update.Host, update.Port, update.SSLEnabled)
	}
	return update
}

func buildServiceURL(host string, port int, sslEnabled bool) string {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return ""
	}
	scheme := "http"
	if sslEnabled {
		scheme = "https"
	}
	return scheme + "://" + host + ":" + strconv.Itoa(port)
}

func endpointFromServiceURL(raw string) (string, int, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", 0, false
	}
	port := 0
	if parsed.Port() != "" {
		if value, err := strconv.Atoi(parsed.Port()); err == nil {
			port = value
		}
	}
	sslEnabled := parsed.Scheme == "https"
	if port == 0 {
		if sslEnabled {
			port = 443
		} else if parsed.Scheme == "http" {
			port = 80
		}
	}
	return parsed.Hostname(), port, sslEnabled
}

func fillServiceEndpointFromURL(service *RegisteredService) {
	if service == nil {
		return
	}
	host, port, sslEnabled := endpointFromServiceURL(service.PublicURL)
	if service.Host == "" {
		service.Host = host
	}
	if service.Port == 0 {
		service.Port = port
	}
	if host != "" {
		service.SSLEnabled = sslEnabled
	}
}

func validateServiceType(serviceType string) error {
	switch serviceType {
	case "discord_bot", "encoder_recorder", "worker", "observability", "update_agent":
		return nil
	default:
		return errors.New("invalid service type")
	}
}

func validateServiceScopes(scopes []string) error {
	if len(scopes) == 0 {
		return ErrInvalidServiceScope
	}
	allowed := map[string]bool{
		"service.register": true, "service.heartbeat": true, "service.logs.write": true, "service.status.write": true, "service.config.read": true, "service.secret.resolve": true,
		"worker.events.write": true, "encoder.status.write": true, "discord.status.write": true, "observability.ingest": true,
		"notifications.email.send": true,
		"streams.start":            true,
		"streams.stop":             true,
		"remediation.execute":      true,
		"updates.claim":            true,
		"updates.report":           true,
		"updates.authorize":        true,
	}
	for _, scope := range scopes {
		if !allowed[scope] {
			return ErrInvalidServiceScope
		}
	}
	return nil
}

// ValidateServiceTokenScopes classifies persisted or projected service-token
// scopes against the single store-owned allowlist. HTTP permission projections
// and mutations use this function so an unknown stored scope always fails
// closed instead of being interpreted by a consumer.
func ValidateServiceTokenScopes(scopes []string) error {
	return validateServiceScopes(scopes)
}

func validateRequiredUpdateAgentScopes(serviceType string, scopes []string) error {
	if strings.TrimSpace(serviceType) != "update_agent" {
		return nil
	}
	for _, required := range []string{"updates.claim", "updates.report", "updates.authorize"} {
		if !hasString(scopes, required) {
			return ErrInvalidServiceScope
		}
	}
	return nil
}

// ValidateRequiredUpdateAgentScopes preserves the mandatory Host Agent
// authority as a shared invariant for token creation, rotation, and read-only
// permission projection.
func ValidateRequiredUpdateAgentScopes(serviceType string, scopes []string) error {
	return validateRequiredUpdateAgentScopes(serviceType, scopes)
}

func hasString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
