package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) listServiceTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.services.ListServiceTokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_api_tokens_failed"})
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) createServiceToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServiceType     string         `json:"service_type"`
		Scopes          []string       `json:"scopes"`
		ServiceID       string         `json:"service_id,omitempty"`
		ServiceName     string         `json:"service_name,omitempty"`
		TransportMode   string         `json:"transport_mode,omitempty"`
		ExecutionHostID string         `json:"execution_host_id,omitempty"`
		OwnershipEpoch  int64          `json:"ownership_epoch,omitempty"`
		PublicURL       string         `json:"public_url,omitempty"`
		Version         string         `json:"version,omitempty"`
		Capabilities    map[string]any `json:"capabilities,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.ServiceID = strings.TrimSpace(body.ServiceID)
	if stringSliceContains(body.Scopes, "service.register") && body.ServiceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_required"})
		return
	}
	if body.ServiceID != "" && !stringSliceContains(body.Scopes, "service.register") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_register_scope_required"})
		return
	}
	if body.ServiceID != "" &&
		strings.TrimSpace(body.ServiceType) == "update_agent" &&
		strings.EqualFold(strings.TrimSpace(body.TransportMode), store.SystemUpdateTransportPullV2) {
		if body.OwnershipEpoch != 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_registration"})
			return
		}
	}
	if !validUpdateAgentServiceTokenScopes(body.ServiceType, body.Scopes) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_scope"})
		return
	}
	if strings.TrimSpace(body.ServiceType) == "update_agent" {
		if err := validateNodeConfigurationSecretPermissions(currentFromContext(r.Context()).Permissions, body.ServiceType); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return
		}
	}
	if err := validateServiceTokenScopePermissions(currentFromContext(r.Context()).Permissions, body.Scopes); err != nil {
		status := http.StatusBadRequest
		code := "invalid_service_scope"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	token, err := s.services.CreateServiceToken(r.Context(), body.ServiceType, body.Scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_api_token_failed"})
		return
	}
	var precreatedService *store.RegisteredService
	if body.ServiceID != "" {
		service, err := s.services.PrecreateService(r.Context(), token, store.ServiceRegistration{
			ServiceID:       body.ServiceID,
			ServiceType:     body.ServiceType,
			ServiceName:     body.ServiceName,
			TransportMode:   body.TransportMode,
			ExecutionHostID: body.ExecutionHostID,
			OwnershipEpoch:  body.OwnershipEpoch,
			PublicURL:       body.PublicURL,
			Version:         body.Version,
			Capabilities:    body.Capabilities,
		})
		if err != nil {
			_ = s.services.RevokeServiceToken(r.Context(), token.ID)
			if errors.Is(err, store.ErrAlreadyExists) {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "service_already_exists"})
				return
			}
			if errors.Is(err, store.ErrInvalidServiceRegistration) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_registration"})
				return
			}
			if errors.Is(err, store.ErrForbidden) {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_mismatch"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "precreate_service_failed"})
			return
		}
		precreatedService = &service
	}
	current := currentFromContext(r.Context())
	metadata := map[string]any{"service_type": token.ServiceType, "scopes": token.Scopes}
	if precreatedService != nil {
		metadata["service_id"] = precreatedService.ServiceID
		metadata["precreated_service"] = true
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "api_tokens.create", ResourceType: "service_token", ResourceID: token.ID, Result: "success", Metadata: metadata})
	writeOneTimeSecretJSON(w, http.StatusCreated, token)
}

func (s *Server) createNodeRegistrationToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeType            string         `json:"node_type"`
		ServiceType         string         `json:"service_type"`
		NodeID              string         `json:"node_id"`
		ServiceID           string         `json:"service_id"`
		Name                string         `json:"name"`
		ServiceName         string         `json:"service_name"`
		Description         string         `json:"description"`
		Host                string         `json:"host"`
		Port                int            `json:"port"`
		SSLEnabled          bool           `json:"ssl_enabled"`
		PublicURL           string         `json:"public_url"`
		Version             string         `json:"version"`
		Capabilities        map[string]any `json:"capabilities,omitempty"`
		AllowRuntimeSecrets bool           `json:"allow_runtime_secrets"`
		AllowRemediation    bool           `json:"allow_remediation"`
		TransportMode       string         `json:"transport_mode"`
		ExecutionHostID     string         `json:"execution_host_id"`
		OwnershipEpoch      int64          `json:"ownership_epoch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	serviceType := strings.TrimSpace(body.NodeType)
	if serviceType == "" {
		serviceType = strings.TrimSpace(body.ServiceType)
	}
	serviceID := strings.TrimSpace(body.NodeID)
	if serviceID == "" {
		serviceID = strings.TrimSpace(body.ServiceID)
	}
	serviceName := strings.TrimSpace(body.Name)
	if serviceName == "" {
		serviceName = strings.TrimSpace(body.ServiceName)
	}
	host := strings.TrimSpace(body.Host)
	port := body.Port
	sslEnabled := body.SSLEnabled
	transportMode := strings.ToLower(strings.TrimSpace(body.TransportMode))
	executionHostID := strings.TrimSpace(body.ExecutionHostID)
	isEndpointlessPullAgent := serviceType == "update_agent" &&
		transportMode == store.SystemUpdateTransportPullV2
	ownershipEpoch := body.OwnershipEpoch
	if isEndpointlessPullAgent {
		if executionHostID == "" || body.OwnershipEpoch != 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_registration"})
			return
		}
	}
	publicURL := ""
	if isEndpointlessPullAgent {
		if host != "" || port != 0 || sslEnabled || strings.TrimSpace(body.PublicURL) != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_endpoint"})
			return
		}
	} else {
		if host == "" || port == 0 {
			parsedHost, parsedPort, parsedSSL := nodeEndpointFromURL(strings.TrimSpace(body.PublicURL))
			if host == "" {
				host = parsedHost
			}
			if port == 0 {
				port = parsedPort
			}
			if parsedHost != "" {
				sslEnabled = parsedSSL
			}
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
	}
	if !requireNodeStreamIngestSigningKey(w, serviceType) {
		return
	}
	scopes := nodeRegistrationScopes(serviceType, body.AllowRuntimeSecrets, body.AllowRemediation)
	if err := validateServiceTokenScopePermissions(currentFromContext(r.Context()).Permissions, scopes); err != nil {
		status := http.StatusBadRequest
		code := "invalid_node_scope"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if err := validateNodeConfigurationSecretPermissions(currentFromContext(r.Context()).Permissions, serviceType); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
		return
	}
	if _, err := nodeRuntimeTokenEncryptionKey(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	token, err := s.services.CreateServiceToken(r.Context(), serviceType, scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_node_registration_token_failed"})
		return
	}
	service, err := s.services.PrecreateService(r.Context(), token, store.ServiceRegistration{
		ServiceID:       serviceID,
		ServiceType:     serviceType,
		ServiceName:     serviceName,
		Description:     strings.TrimSpace(body.Description),
		TransportMode:   transportMode,
		ExecutionHostID: executionHostID,
		OwnershipEpoch:  ownershipEpoch,
		Host:            host,
		Port:            port,
		SSLEnabled:      sslEnabled,
		PublicURL:       publicURL,
		Version:         "",
		Capabilities:    map[string]any{},
	})
	if err != nil {
		_ = s.services.RevokeServiceToken(r.Context(), token.ID)
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "node_already_exists"})
			return
		}
		if errors.Is(err, store.ErrInvalidServiceRegistration) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_registration"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "node_type_mismatch"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "precreate_node_failed"})
		return
	}
	service, err = s.persistNodeRuntimeToken(r.Context(), service.ServiceID, token.RawToken)
	if err != nil {
		_ = s.services.RevokeServiceToken(r.Context(), token.ID)
		_ = s.services.DeleteService(r.Context(), service.ServiceID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	var configureToken string
	var configureExpiresAt time.Time
	configureToken, configureExpiresAt, err = s.issueNodeConfigureToken(r.Context(), service.ServiceID)
	if err != nil {
		_ = s.services.RevokeServiceToken(r.Context(), token.ID)
		_ = s.services.DeleteService(r.Context(), service.ServiceID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_node_configure_token_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "nodes.registration_token.create",
		ResourceType:  "node",
		ResourceID:    service.ServiceID,
		Result:        "success",
		Metadata:      map[string]any{"node_type": service.ServiceType, "token_id": token.ID, "scopes": token.Scopes},
	})
	response := map[string]any{
		"id":               token.ID,
		"service_type":     token.ServiceType,
		"node_type":        token.ServiceType,
		"scopes":           token.Scopes,
		"runtime_token_id": token.ID,
		"runtime_token":    token.RawToken,
		"created_at":       token.CreatedAt,
		"node":             service,
	}
	if configureToken != "" {
		response["token"] = configureToken
		response["configure_token"] = configureToken
		response["configure_token_expires_at"] = configureExpiresAt
	}
	addNodeConfigurationMetadata(response, r, service, token.ID, token.RawToken, configureToken)
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func nodeRegistrationScopes(serviceType string, allowRuntimeSecrets, allowRemediation bool) []string {
	scopes := []string{"service.register", "service.heartbeat", "service.config.read", "service.logs.write", "service.status.write"}
	switch serviceType {
	case "discord_bot":
		scopes = append(scopes, "discord.status.write", "streams.start", "streams.stop")
	case "encoder_recorder":
		scopes = append(scopes, "encoder.status.write", "observability.ingest")
	case "worker":
		scopes = append(scopes, "worker.events.write", "observability.ingest")
	case "observability":
		scopes = append(scopes, "observability.ingest", "notifications.email.send")
		if allowRemediation {
			scopes = append(scopes, "remediation.execute")
		}
	case "update_agent":
		scopes = append(scopes, "updates.claim", "updates.report", "updates.authorize")
	}
	if serviceType == "encoder_recorder" || allowRuntimeSecrets {
		scopes = append(scopes, "service.secret.resolve")
	}
	return scopes
}

func (s *Server) issueNodeConfigureToken(ctx context.Context, nodeID string) (string, time.Time, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	token := "ast_cfg_" + raw
	expiresAt := time.Now().UTC().Add(nodeConfigureTokenTTL())
	if _, err := s.services.SetServiceConfigureToken(ctx, nodeID, security.HashToken(token), expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (s *Server) persistNodeRuntimeToken(ctx context.Context, nodeID, rawToken string) (store.RegisteredService, error) {
	seal, err := nodeRuntimeTokenSealer()
	if err != nil || strings.TrimSpace(rawToken) == "" {
		return store.RegisteredService{}, errors.New("node runtime token encryption key is not configured")
	}
	ciphertext, nonce, err := seal(rawToken)
	if err != nil {
		return store.RegisteredService{}, err
	}
	return s.services.SetServiceNodeTokenSecret(ctx, nodeID, ciphertext, nonce)
}

func nodeRuntimeTokenEncryptionKey() (string, error) {
	key := strings.TrimSpace(os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))
	upper := strings.ToUpper(key)
	placeholder := strings.Contains(upper, "CHANGE_ME") ||
		strings.Contains(upper, "REPLACE_ME") ||
		strings.Contains(upper, "YOUR_ENCRYPTION_KEY") ||
		(strings.HasPrefix(key, "<") && strings.HasSuffix(key, ">"))
	if len([]byte(key)) < minSecretEncryptionKeyLen || placeholder {
		return "", errors.New("node runtime token encryption key must be a non-placeholder value of at least 32 bytes")
	}
	return key, nil
}

func nodeRuntimeTokenSealer() (store.NodeTokenSealer, error) {
	key, err := nodeRuntimeTokenEncryptionKey()
	if err != nil {
		return nil, err
	}
	return func(rawToken string) (string, string, error) {
		if strings.TrimSpace(rawToken) == "" {
			return "", "", errors.New("node runtime token is empty")
		}
		return security.EncryptSecret(rawToken, key)
	}, nil
}

func nodeConfigureTokenTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("AUTOSTREAM_NODE_CONFIGURE_TOKEN_TTL"))
	if raw == "" {
		return defaultNodeConfigureTokenTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return defaultNodeConfigureTokenTTL
	}
	return ttl
}

func nodeConfigureCommand(r *http.Request, service store.RegisteredService, rawToken, configPath string) string {
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "https://control.example.com"
	}
	if configPath == "" {
		configPath = nodeDefaultConfigPath(service)
	}
	configureBinary := nodeConfigureBinary(service)
	if service.ServiceType == "update_agent" {
		return `sudo ` + configureBinary + ` configure --panel-url ` + posixShellQuote(panelURL) +
			" --node " + posixShellQuote(service.ServiceID) +
			" --config " + posixShellQuote(configPath)
	}
	return `sudo ` + configureBinary + ` configure --panel-url ` + posixShellQuote(panelURL) +
		" --token " + posixShellQuote(rawToken) +
		" --node " + posixShellQuote(service.ServiceID) +
		" --config " + posixShellQuote(configPath)
}

func posixShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func nodeConfigureBinary(service store.RegisteredService) string {
	if service.ServiceType == "update_agent" {
		return "/usr/local/bin/autostream-host-agent"
	}
	switch service.ServiceType {
	case "encoder_recorder":
		return "autostream-encoder-recorder"
	case "discord_bot":
		return "autostream-discord-bot"
	case "observability":
		return "autostream-observability"
	default:
		return "autostream-worker"
	}
}

func nodeDefaultConfigPath(service store.RegisteredService) string {
	if strings.TrimSpace(service.ServiceType) == "update_agent" {
		return "/etc/autostream/updater/agent.yaml"
	}
	return "/etc/autostream-" + nodeServiceDirectoryName(service.ServiceType) + "/config.yml"
}

func isPullV2HostAgent(service store.RegisteredService) bool {
	return service.ServiceType == "update_agent" &&
		service.TransportMode == store.SystemUpdateTransportPullV2
}

func nodeAgentDataDir(serviceType string) string {
	return "/var/lib/autostream/" + nodeServiceDirectoryName(serviceType)
}

func nodeAgentLogDir(serviceType string) string {
	return "/var/log/autostream/" + nodeServiceDirectoryName(serviceType)
}

func nodeServiceDirectoryName(serviceType string) string {
	switch serviceType {
	case "encoder_recorder":
		return "encoder-recorder"
	case "discord_bot":
		return "discord-bot"
	case "observability":
		return "observability"
	case "worker":
		return "worker"
	default:
		value := strings.Trim(strings.ToLower(strings.ReplaceAll(serviceType, "_", "-")), "-")
		if value == "" {
			return "worker"
		}
		return value
	}
}

func panelBaseURL(r *http.Request) string {
	if publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")), "/"); publicURL != "" {
		return publicURL
	}
	if r != nil && r.Host != "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "https" || forwarded == "http" {
			scheme = forwarded
		}
		return scheme + "://" + r.Host
	}
	return ""
}

func nodeConfigurationYAML(r *http.Request, service store.RegisteredService, tokenID, rawToken string) string {
	if service.ServiceType == "update_agent" {
		return ""
	}
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "https://control.example.com"
	}
	tokenValue := rawToken
	if tokenValue == "" {
		tokenValue = "<regenerate-runtime-token>"
	}
	tokenIDValue := tokenID
	if tokenIDValue == "" {
		tokenIDValue = service.TokenID
	}
	lines := []string{
		"panel:",
		"  url: " + yamlQuote(panelURL),
		"",
		"node:",
		"  id: " + yamlQuote(service.ServiceID),
		"  name: " + yamlQuote(service.ServiceName),
		"  type: " + yamlQuote(service.ServiceType),
		"  description: " + yamlQuote(service.Description),
		"",
		"api:",
		"  host: " + yamlQuote(service.Host),
		"  port: " + strconv.Itoa(service.Port),
		"  ssl_enabled: " + strconv.FormatBool(service.SSLEnabled),
		"",
		"listener:",
		"  credential: node-listener.json",
		"",
		"auth:",
		"  token_id: " + yamlQuote(tokenIDValue),
		"  token: " + yamlQuote(tokenValue),
		"",
	}
	if signingKey := nodeStreamIngestSigningKey(service.ServiceType, rawToken != ""); signingKey != "" {
		lines = append(lines,
			"stream_ingest:",
			"  signing_key: "+yamlQuote(signingKey),
			"",
		)
	}
	lines = append(lines,
		"agent:",
		"  data_dir: "+yamlQuote(nodeAgentDataDir(service.ServiceType)),
		"  log_dir: "+yamlQuote(nodeAgentLogDir(service.ServiceType)),
		"",
	)
	return strings.Join(lines, "\n")
}

func addNodeConfigurationMetadata(response map[string]any, r *http.Request, service store.RegisteredService, tokenID, rawToken, configureToken string) {
	if service.ServiceType == "update_agent" {
		response["configuration_path"] = nodeDefaultConfigPath(service)
		if configureToken != "" {
			response["configure_command"] = nodeConfigureCommand(r, service, configureToken, "")
		}
		return
	}
	response["configuration_yaml"] = nodeConfigurationYAML(r, service, tokenID, rawToken)
	if configureToken != "" {
		response["configure_command"] = nodeConfigureCommand(r, service, configureToken, "")
	}
}

func nodeStreamIngestSigningKey(serviceType string, includeSecret bool) string {
	if !includeSecret {
		return ""
	}
	if nodeStreamIngestSigningKeyErrorCode(serviceType) != "" {
		return ""
	}
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder":
		return strings.TrimSpace(os.Getenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY"))
	default:
		return ""
	}
}

func nodeStreamIngestSigningKeyErrorCode(serviceType string) string {
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder":
	default:
		return ""
	}
	key := strings.TrimSpace(os.Getenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY"))
	if key == "" {
		return "stream_ingest_signing_key_required"
	}
	upper := strings.ToUpper(key)
	placeholder := strings.Contains(upper, "CHANGE_ME") ||
		strings.Contains(upper, "REPLACE_ME") ||
		strings.Contains(upper, "YOUR_SIGNING_KEY") ||
		(strings.HasPrefix(key, "<") && strings.HasSuffix(key, ">"))
	if len([]byte(key)) < minStreamIngestSigningKeyLen || placeholder {
		return "stream_ingest_signing_key_invalid"
	}
	return ""
}

func requireNodeStreamIngestSigningKey(w http.ResponseWriter, serviceType string) bool {
	if code := nodeStreamIngestSigningKeyErrorCode(serviceType); code != "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
		return false
	}
	return true
}

func yamlQuote(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func buildNodeAgentURL(host string, port int, sslEnabled bool) string {
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

func nodeEndpointFromURL(raw string) (string, int, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", 0, false
	}
	port := 0
	if parsed.Port() != "" {
		if parsedPort, err := strconv.Atoi(parsed.Port()); err == nil {
			port = parsedPort
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
