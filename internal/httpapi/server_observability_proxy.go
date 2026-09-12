package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/observability"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) registerObservabilityRoutes() {
	s.mux.HandleFunc("GET /observability/incidents", s.requirePermission("incidents.read", s.observabilityGet("/incidents")))
	s.mux.HandleFunc("GET /observability/diagnostics", s.requirePermission("diagnostics.read", s.observabilityGet("/diagnostics")))
	s.mux.HandleFunc("GET /observability/metrics", s.requirePermission("metrics.read", s.observabilityMetrics))
	s.mux.HandleFunc("GET /observability/remediation-actions", s.requirePermission("remediation.read", s.observabilityGet("/remediation-actions")))
	s.mux.HandleFunc("POST /observability/remediation-actions/{id}/approve", s.requirePermission("remediation.approve", s.observabilityPostAction("/remediation-actions/{id}/approve")))
	s.mux.HandleFunc("POST /observability/remediation-actions/{id}/execute", s.requirePermission("remediation.execute", s.observabilityPostAction("/remediation-actions/{id}/execute")))
	s.mux.HandleFunc("POST /observability/incidents/{id}/diagnostics/rerun", s.requirePermission("diagnostics.run", s.observabilityPostActionWithAudit("/incidents/{id}/diagnostics/rerun", "diagnostics.run", "incident")))
	s.mux.HandleFunc("POST /observability/incidents/{id}/acknowledge", s.requirePermission("incidents.acknowledge", s.observabilityPostActionWithAudit("/incidents/{id}/acknowledge", "incidents.acknowledge", "incident")))
	s.mux.HandleFunc("POST /observability/incidents/{id}/resolve", s.requirePermission("incidents.resolve", s.observabilityPostActionWithAudit("/incidents/{id}/resolve", "incidents.resolve", "incident")))
	s.mux.HandleFunc("GET /observability/notification-deliveries", s.requirePermission("notification_channels.read", s.observabilityGet("/notification-deliveries")))
	s.mux.HandleFunc("GET /observability/notification-channels", s.requirePermission("notification_channels.read", s.observabilityGet("/notification-channels")))
	s.mux.HandleFunc("POST /observability/notification-channels", s.requirePermission("notification_channels.create", s.observabilityNotificationChannelPostProxyStatus("/notification-channels", "notification_channels.create", "notification_channel", http.StatusCreated)))
	s.mux.HandleFunc("GET /observability/notification-channels/{id}", s.requirePermission("notification_channels.read", s.observabilityGetAction("/notification-channels/{id}")))
	s.mux.HandleFunc("PUT /observability/notification-channels/{id}", s.requirePermission("notification_channels.update", s.observabilityNotificationChannelPutProxy("/notification-channels/{id}", "notification_channels.update", "notification_channel")))
	s.mux.HandleFunc("DELETE /observability/notification-channels/{id}", s.requirePermission("notification_channels.delete", s.observabilityDeleteProxy("/notification-channels/{id}", "notification_channels.delete", "notification_channel")))
	s.mux.HandleFunc("POST /observability/notification-channels/{id}/test", s.requirePermission("notification_channels.test", s.observabilityPostActionWithAuditStatus("/notification-channels/{id}/test", "notification_channels.test", "notification_channel", http.StatusAccepted)))
}

func (s *Server) observabilityGet(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		proxyEndpoint := endpoint
		query := url.Values{}
		for _, key := range []string{"limit", "before", "before_id", "status"} {
			if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
				query.Set(key, value)
			}
		}
		if encoded := query.Encode(); encoded != "" {
			proxyEndpoint += "?" + encoded
		}
		body, err := obs.Get(r.Context(), proxyEndpoint)
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityMetrics(w http.ResponseWriter, r *http.Request) {
	rangeDuration, err := observabilityMetricRange(r.URL.Query().Get("range_sec"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_metric_range"})
		return
	}
	rangeSeconds := int(rangeDuration / time.Second)
	localMetrics, localErr := s.serviceMetricSnapshots(r.Context(), time.Now().UTC().Add(-rangeDuration))
	obs, configured, err := s.observabilityClient(r.Context())
	if err != nil {
		if localErr == nil && len(localMetrics) > 0 {
			writeJSON(w, http.StatusOK, localMetrics)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	if !configured {
		if localErr == nil && len(localMetrics) > 0 {
			writeJSON(w, http.StatusOK, localMetrics)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	body, err := obs.Get(r.Context(), "/metrics?range_sec="+strconv.Itoa(rangeSeconds))
	if err != nil {
		if localErr == nil && len(localMetrics) > 0 {
			writeJSON(w, http.StatusOK, localMetrics)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "observability_request_failed"})
		return
	}
	if localErr != nil {
		writeObservabilityJSON(w, http.StatusOK, "/metrics", body)
		return
	}
	merged, err := mergeMetricSnapshots(sanitizeObservabilityResponse("/metrics", body), localMetrics)
	if err != nil {
		writeObservabilityJSON(w, http.StatusOK, "/metrics", body)
		return
	}
	writeJSON(w, http.StatusOK, merged)
}

func observabilityMetricRange(raw string) (time.Duration, error) {
	const (
		minimum = 15 * time.Minute
		maximum = 3 * time.Hour
	)
	if strings.TrimSpace(raw) == "" {
		return maximum, nil
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	duration := time.Duration(seconds) * time.Second
	if duration < minimum || duration > maximum {
		return 0, fmt.Errorf("metric range must be between %s and %s", minimum, maximum)
	}
	return duration, nil
}

func (s *Server) serviceMetricSnapshots(ctx context.Context, since time.Time) ([]map[string]any, error) {
	if s.services == nil {
		return nil, nil
	}
	history, err := s.services.ListServiceMetricSnapshots(ctx, since.UTC(), 360)
	if err == nil && len(history) > 0 {
		out := make([]map[string]any, 0, len(history))
		for _, snapshot := range history {
			out = append(out, map[string]any{
				"name":         snapshot.Name,
				"service_id":   snapshot.ServiceID,
				"service_type": snapshot.ServiceType,
				"status":       snapshot.Status,
				"value":        snapshot.Value,
				"updated_at":   snapshot.ObservedAt.UTC().Format(time.RFC3339Nano),
			})
		}
		return out, nil
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for _, service := range services {
		if len(service.Metrics) == 0 {
			continue
		}
		metricNames := make([]string, 0, len(service.Metrics))
		for name := range service.Metrics {
			metricNames = append(metricNames, name)
		}
		sort.Strings(metricNames)
		updatedAt := service.UpdatedAt
		if service.LastHeartbeatAt != nil {
			updatedAt = *service.LastHeartbeatAt
		}
		for _, name := range metricNames {
			value, ok := serviceMetricNumber(service.Metrics[name])
			if !ok {
				continue
			}
			out = append(out, map[string]any{
				"name":         name,
				"service_id":   service.ServiceID,
				"service_type": service.ServiceType,
				"status":       service.Status,
				"value":        value,
				"updated_at":   updatedAt.UTC().Format(time.RFC3339Nano),
			})
		}
	}
	return out, nil
}

func mergeMetricSnapshots(upstream []byte, local []map[string]any) ([]map[string]any, error) {
	var merged []map[string]any
	if len(strings.TrimSpace(string(upstream))) > 0 {
		if err := json.Unmarshal(upstream, &merged); err != nil {
			return nil, err
		}
	}
	seen := map[string]struct{}{}
	for _, row := range merged {
		if key := metricSnapshotKey(row); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, row := range local {
		if key := metricSnapshotKey(row); key != "" {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		merged = append(merged, row)
	}
	return merged, nil
}

func metricSnapshotKey(row map[string]any) string {
	serviceID := jsonStringField(row, "service_id")
	streamID := jsonStringField(row, "stream_id")
	name := jsonStringField(row, "name")
	updatedAt := jsonStringField(row, "updated_at")
	if serviceID == "" || name == "" {
		return ""
	}
	return serviceID + "\x00" + streamID + "\x00" + name + "\x00" + updatedAt
}

func jsonStringField(row map[string]any, key string) string {
	value, _ := row[key].(string)
	return strings.TrimSpace(value)
}

func serviceMetricNumber(raw any) (float64, bool) {
	switch value := raw.(type) {
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float32:
		return float64(value), true
	case float64:
		return value, true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *Server) observabilityGetAction(template string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Get(r.Context(), endpoint)
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityPostAction(template string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Post(r.Context(), endpoint, map[string]any{})
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		current := currentFromContext(r.Context())
		action := "remediation.approve"
		if strings.HasSuffix(template, "/execute") {
			action = "remediation.execute"
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "remediation_action", ResourceID: r.PathValue("id"), Result: "success"})
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityPostActionWithAudit(template, action, resourceType string) http.HandlerFunc {
	return s.observabilityPostActionWithAuditStatus(template, action, resourceType, http.StatusOK)
}

func (s *Server) observabilityPostActionWithAuditStatus(template, action, resourceType string, successStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		if action == "notification_channels.test" && obs.Timeout < notificationObservabilityCallTimeout {
			obs.Timeout = notificationObservabilityCallTimeout
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Post(r.Context(), endpoint, map[string]any{})
		if err != nil {
			status, code := observabilityProxyError(err)
			current := currentFromContext(r.Context())
			s.writeAudit(r, store.AuditEvent{
				ActorUserID:   current.User.ID,
				ActorUsername: current.User.Username,
				Action:        action,
				ResourceType:  resourceType,
				ResourceID:    r.PathValue("id"),
				Result:        "failure",
				Metadata: map[string]any{
					"code":   code,
					"reason": code,
					"status": status,
				},
			})
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: resourceType, ResourceID: r.PathValue("id"), Result: "success"})
		writeObservabilityJSON(w, successStatus, endpoint, body)
	}
}

func (s *Server) observabilityPostProxy(endpoint, action, resourceType string) http.HandlerFunc {
	return s.observabilityPostProxyStatus(endpoint, action, resourceType, http.StatusOK)
}

func (s *Server) observabilityPostProxyStatus(endpoint, action, resourceType string, successStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.observabilityProxyJSONStatus(w, r, http.MethodPost, endpoint, action, resourceType, successStatus)
	}
}

func (s *Server) observabilityNotificationChannelPostProxyStatus(endpoint, action, resourceType string, successStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.observabilityProxyJSONStatusWithTransform(w, r, http.MethodPost, endpoint, action, resourceType, successStatus, sanitizeNotificationChannelCreateProxyPayload)
	}
}

func (s *Server) observabilityPutProxy(template, action, resourceType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		s.observabilityProxyJSONStatus(w, r, http.MethodPut, endpoint, action, resourceType, http.StatusOK)
	}
}

func (s *Server) observabilityNotificationChannelPutProxy(template, action, resourceType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		s.observabilityProxyJSONStatusWithTransform(w, r, http.MethodPut, endpoint, action, resourceType, http.StatusOK, sanitizeNotificationChannelUpdateProxyPayload)
	}
}

func (s *Server) observabilityDeleteProxy(template, action, resourceType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Delete(r.Context(), endpoint)
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: resourceType, ResourceID: r.PathValue("id"), Result: "success"})
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityProxyJSON(w http.ResponseWriter, r *http.Request, method, endpoint, action, resourceType string) {
	s.observabilityProxyJSONStatus(w, r, method, endpoint, action, resourceType, http.StatusOK)
}

func (s *Server) observabilityProxyJSONStatus(w http.ResponseWriter, r *http.Request, method, endpoint, action, resourceType string, successStatus int) {
	s.observabilityProxyJSONStatusWithTransform(w, r, method, endpoint, action, resourceType, successStatus, nil)
}

func (s *Server) observabilityProxyJSONStatusWithTransform(w http.ResponseWriter, r *http.Request, method, endpoint, action, resourceType string, successStatus int, transform func(map[string]any)) {
	obs, ok := s.observabilityClientForRequest(w, r)
	if !ok {
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if transform != nil {
		transform(payload)
	}
	var (
		body json.RawMessage
		err  error
	)
	if method == http.MethodPut {
		body, err = obs.Put(r.Context(), endpoint, payload)
	} else {
		body, err = obs.Post(r.Context(), endpoint, payload)
	}
	if err != nil {
		status, code := observabilityProxyError(err)
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{
			ActorUserID:   current.User.ID,
			ActorUsername: current.User.Username,
			Action:        action,
			ResourceType:  resourceType,
			ResourceID:    r.PathValue("id"),
			Result:        "failure",
			Metadata: map[string]any{
				"reason":            code,
				"has_webhook_url":   payload["webhook_url"] != nil,
				"has_smtp_password": payload["smtp_password"] != nil,
			},
		})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        action,
		ResourceType:  resourceType,
		ResourceID:    r.PathValue("id"),
		Result:        "success",
		Metadata: map[string]any{
			"has_webhook_url":   payload["webhook_url"] != nil,
			"has_smtp_password": payload["smtp_password"] != nil,
		},
	})
	writeObservabilityJSON(w, successStatus, endpoint, body)
}

func sanitizeNotificationChannelCreateProxyPayload(payload map[string]any) {
	for key := range payload {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "smtp_") {
			delete(payload, key)
		}
	}
	channelType, _ := payload["type"].(string)
	if strings.EqualFold(strings.TrimSpace(channelType), "email") {
		payload["uses_global_smtp"] = true
	}
}

func sanitizeNotificationChannelUpdateProxyPayload(payload map[string]any) {
	migrateToGlobalSMTP := false
	for key, value := range payload {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "migrate_to_global_smtp" {
			requested, ok := value.(bool)
			if ok && requested {
				migrateToGlobalSMTP = true
			}
			delete(payload, key)
			continue
		}
		if strings.HasPrefix(normalized, "smtp_") || normalized == "uses_global_smtp" {
			delete(payload, key)
		}
	}
	if migrateToGlobalSMTP {
		payload["uses_global_smtp"] = true
	}
}

var publicObservabilityErrorCodes = map[string]struct{}{
	"bad_request":                       {},
	"event_type_required":               {},
	"incident_context_missing":          {},
	"invalid_incident_status":           {},
	"invalid_notification_channel":      {},
	"invalid_notification_event":        {},
	"invalid_smtp_channel":              {},
	"invalid_webhook_url":               {},
	"not_found":                         {},
	"remediation_action_not_executable": {},
	"remediation_action_terminal":       {},
	"stream_context_missing":            {},
}

func writeObservabilityProxyError(w http.ResponseWriter, err error) {
	status, code := observabilityProxyError(err)
	writeJSON(w, status, map[string]string{"code": code})
}

func observabilityProxyError(err error) (int, string) {
	var upstream *observability.ResponseError
	if !errors.As(err, &upstream) {
		return http.StatusBadGateway, "observability_request_failed"
	}
	if upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden {
		return http.StatusBadGateway, "observability_auth_failed"
	}
	if upstream.StatusCode == http.StatusServiceUnavailable {
		if upstream.Code == "secret_encryption_key_required" {
			return http.StatusServiceUnavailable, upstream.Code
		}
		return http.StatusServiceUnavailable, "observability_unavailable"
	}
	if upstream.StatusCode == http.StatusTooManyRequests {
		return http.StatusTooManyRequests, "observability_rate_limited"
	}
	if upstream.StatusCode >= 400 && upstream.StatusCode < 500 {
		code := "observability_request_rejected"
		if _, ok := publicObservabilityErrorCodes[upstream.Code]; ok {
			code = upstream.Code
		}
		return upstream.StatusCode, code
	}
	return http.StatusBadGateway, "observability_request_failed"
}

func (s *Server) observabilityClientForRequest(w http.ResponseWriter, r *http.Request) (observability.Client, bool) {
	client, ok, err := s.observabilityClient(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return observability.Client{}, false
	}
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return observability.Client{}, false
	}
	return client, true
}

func (s *Server) observabilityClient(ctx context.Context) (observability.Client, bool, error) {
	if s.services == nil {
		return observability.Client{}, false, nil
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return observability.Client{}, false, err
	}
	service, ok := preferredObservabilityService(services, time.Now().UTC())
	if !ok {
		return observability.Client{}, false, nil
	}
	token, err := nodeRuntimeToken(service)
	if err != nil {
		return observability.Client{}, false, err
	}
	client := observability.Client{
		BaseURL: service.PublicURL,
		Token:   token,
		Timeout: s.obs.Timeout,
		HTTP:    s.obs.HTTP,
	}
	if client.Timeout <= 0 {
		client.Timeout = 5 * time.Second
	}
	return client, true, nil
}

func preferredObservabilityService(services []store.RegisteredService, now time.Time) (store.RegisteredService, bool) {
	var selected store.RegisteredService
	selectedRank := -1
	found := false
	for _, service := range services {
		if service.ServiceType != "observability" || strings.TrimSpace(service.PublicURL) == "" || strings.TrimSpace(service.Status) == "pending" {
			continue
		}
		health, _, _ := serviceHealthFields(service, now)
		rank := observabilityServiceHealthRank(health)
		if !found || rank > selectedRank || (rank == selectedRank && observabilityServiceBefore(service, selected)) {
			selected = service
			selectedRank = rank
			found = true
		}
	}
	return selected, found
}

func observabilityServiceHealthRank(health string) int {
	switch health {
	case "healthy":
		return 3
	case "warning":
		return 2
	case "unconfigured":
		return 1
	default:
		return 0
	}
}

func observabilityServiceBefore(candidate, current store.RegisteredService) bool {
	if candidate.LastHeartbeatAt != nil || current.LastHeartbeatAt != nil {
		if candidate.LastHeartbeatAt == nil {
			return false
		}
		if current.LastHeartbeatAt == nil {
			return true
		}
		if !candidate.LastHeartbeatAt.Equal(*current.LastHeartbeatAt) {
			return candidate.LastHeartbeatAt.After(*current.LastHeartbeatAt)
		}
	}
	candidateName := strings.TrimSpace(candidate.ServiceName)
	currentName := strings.TrimSpace(current.ServiceName)
	if candidateName != currentName {
		return candidateName < currentName
	}
	return candidate.ServiceID < current.ServiceID
}

func nodeRuntimeToken(service store.RegisteredService) (string, error) {
	if strings.TrimSpace(service.NodeTokenCiphertext) == "" || strings.TrimSpace(service.NodeTokenNonce) == "" {
		return "", errors.New("node runtime token is not configured")
	}
	key, err := nodeRuntimeTokenEncryptionKey()
	if err != nil {
		return "", err
	}
	token, err := security.DecryptSecret(service.NodeTokenCiphertext, service.NodeTokenNonce, key)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", errors.New("node runtime token could not be decrypted")
	}
	return token, nil
}

func replacePathID(template, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", errors.New("invalid path id")
	}
	return strings.ReplaceAll(template, "{id}", id), nil
}
