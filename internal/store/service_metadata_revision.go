package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

var serviceMetadataRevisionPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidServiceMetadataRevision(revision string) bool {
	return serviceMetadataRevisionPattern.MatchString(revision)
}

// ServiceMetadataRevision binds editable metadata and durable endpoint/ownership
// authority. It deliberately excludes runtime reports, token material, health,
// metrics and UpdatedAt: an ordinary heartbeat must not invalidate an edit.
func ServiceMetadataRevision(service RegisteredService) string {
	desired := service.DesiredEndpoint
	if desired == nil {
		desired = serviceEndpoint(service.Host, service.Port, service.SSLEnabled, service.PublicURL)
	}
	var desiredValue ServiceEndpoint
	if desired != nil {
		desiredValue = *desired
	}
	// A fixed struct is the canonical whitelist. Do not replace it with the full
	// RegisteredService or arbitrary capabilities/configuration maps.
	canonical := struct {
		Format           string
		ServiceID        string
		ServiceType      string
		ServiceName      string
		Description      string
		Host             string
		Port             int
		SSLEnabled       bool
		PublicURL        string
		DesiredEndpoint  ServiceEndpoint
		EndpointRevision int64
		TransportMode    string
		ExecutionHostID  string
		OwnershipEpoch   int64
		CurrentStreamID  string
	}{
		Format: "node-metadata-v1", ServiceID: service.ServiceID, ServiceType: service.ServiceType,
		ServiceName: service.ServiceName, Description: service.Description,
		Host: service.Host, Port: service.Port, SSLEnabled: service.SSLEnabled, PublicURL: service.PublicURL,
		DesiredEndpoint: desiredValue, EndpointRevision: service.EndpointRevision,
		TransportMode: service.TransportMode, ExecutionHostID: service.ExecutionHostID,
		OwnershipEpoch: service.OwnershipEpoch, CurrentStreamID: service.CurrentStreamID,
	}
	// Every field above has a JSON primitive or fixed primitive-only struct type.
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
