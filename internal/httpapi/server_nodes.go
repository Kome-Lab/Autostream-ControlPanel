package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_nodes_failed"})
		return
	}
	writeJSON(w, http.StatusOK, nodeListResponses(services, time.Now().UTC()))
}

func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	var body struct {
		Name        *string `json:"name"`
		ServiceName *string `json:"service_name"`
		Description *string `json:"description"`
		Host        *string `json:"host"`
		Port        *int    `json:"port"`
		SSLEnabled  *bool   `json:"ssl_enabled"`
		PublicURL   *string `json:"public_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.systemUpdateOperationMu.Lock()
	defer s.systemUpdateOperationMu.Unlock()
	existing, err = s.services.GetService(r.Context(), existing.ServiceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	serviceName := existing.ServiceName
	if body.ServiceName != nil {
		serviceName = strings.TrimSpace(*body.ServiceName)
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		serviceName = strings.TrimSpace(*body.Name)
	}
	description := existing.Description
	if body.Description != nil {
		description = strings.TrimSpace(*body.Description)
	}
	endpointless := existing.ServiceType == "update_agent" && existing.TransportMode == "pull_v2"
	endpointFieldsProvided := body.Host != nil ||
		body.Port != nil ||
		body.SSLEnabled != nil ||
		body.PublicURL != nil
	endpointFieldsExactNoOp := endpointFieldsProvided &&
		(body.Host == nil || strings.TrimSpace(*body.Host) == existing.Host) &&
		(body.Port == nil || *body.Port == existing.Port) &&
		(body.SSLEnabled == nil || *body.SSLEnabled == existing.SSLEnabled) &&
		(body.PublicURL == nil || strings.TrimSpace(*body.PublicURL) == existing.PublicURL)
	host, port, sslEnabled, publicURL := existing.Host, existing.Port, existing.SSLEnabled, existing.PublicURL
	preserveEndpoint := false
	if endpointless {
		if endpointFieldsProvided {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_endpoint"})
			return
		}
		host, port, sslEnabled, publicURL = "", 0, false, ""
	} else if !endpointFieldsProvided || endpointFieldsExactNoOp {
		preserveEndpoint = true
	} else {
		if body.PublicURL != nil && strings.TrimSpace(*body.PublicURL) != "" {
			parsedHost, parsedPort, parsedSSL := nodeEndpointFromURL(*body.PublicURL)
			host = parsedHost
			port = parsedPort
			sslEnabled = parsedSSL
		}
		if body.Host != nil {
			host = strings.TrimSpace(*body.Host)
		}
		if body.Port != nil {
			port = *body.Port
		}
		if body.SSLEnabled != nil {
			sslEnabled = *body.SSLEnabled
		}
		publicURL = buildNodeAgentURL(host, port, sslEnabled)
		if publicURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_endpoint"})
			return
		}
		if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL(publicURL); err != nil {
			code := "invalid_node_endpoint"
			if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
				code = "node_endpoint_blocked"
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
			return
		}
		preserveEndpoint = sameNodeEndpoint(existing, host, port, sslEnabled, publicURL)
		if !preserveEndpoint {
			managed, ownershipErr := s.activePullManagedSystemdTarget(r.Context(), existing)
			if ownershipErr != nil || managed {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "node_endpoint_managed_by_updater"})
				return
			}
		}
	}
	updated, err := s.services.UpdateServiceMetadata(r.Context(), existing.ServiceID, store.ServiceMetadataUpdate{
		ServiceName:      serviceName,
		Description:      description,
		Host:             host,
		Port:             port,
		SSLEnabled:       sslEnabled,
		PublicURL:        publicURL,
		Endpointless:     endpointless,
		PreserveEndpoint: preserveEndpoint,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrInvalidServiceRegistration) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_registration"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_node_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "nodes.update",
		ResourceType:  "node",
		ResourceID:    updated.ServiceID,
		Result:        "success",
		Metadata:      map[string]any{"node_type": updated.ServiceType, "public_url": updated.PublicURL},
	})
	writeJSON(w, http.StatusOK, nodeListResponses([]store.RegisteredService{updated}, time.Now().UTC())[0])
}
