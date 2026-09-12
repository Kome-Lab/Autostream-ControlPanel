package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

const (
	adminAuditNotificationTimeout        = 40 * time.Second
	notificationObservabilityCallTimeout = 35 * time.Second
)

func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "audit_store_not_configured"})
		return
	}
	events, err := s.audit.ListAudit(r.Context(), auditFilterFromRequest(r, 100))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_audit_logs_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicAuditEvents(events))
}

func (s *Server) exportAuditLogs(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "audit_store_not_configured"})
		return
	}
	events, err := s.audit.ListAudit(r.Context(), auditFilterFromRequest(r, 500))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "export_audit_logs_failed"})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="autostream-audit-logs.csv"`)
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "timestamp", "actor_user_id", "actor_username", "actor_ip", "user_agent", "action", "resource_type", "resource_id", "result", "request_id"})
	for _, event := range events {
		_ = writer.Write([]string{safeCSVCell(event.ID), event.Timestamp.Format(time.RFC3339), safeCSVCell(event.ActorUserID), safeCSVCell(event.ActorUsername), safeCSVCell(event.ActorIP), safeCSVCell(event.UserAgent), safeCSVCell(event.Action), safeCSVCell(event.ResourceType), safeCSVCell(event.ResourceID), safeCSVCell(event.Result), safeCSVCell(event.RequestID)})
	}
	writer.Flush()
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

var auditActionGroups = map[string][]string{
	"service_assignment": {"services.assign", "services.unassign", "workers.assign", "workers.unassign"},
	"service_runtime":    {"services.register", "services.heartbeat", "archive.artifacts.reported", "nodes.registration_token.create"},
	// Service registration is an agent-to-panel report, not a human operation.
	// Keep it with runtime configuration reads so the operations view does not
	// fill up with periodic Host Agent/Node registration traffic.
	"service_runtime_reads": {"services.register", "services.runtime_config.read"},
	"node_activity":         {"services.register", "services.runtime_config.read", "services.heartbeat", "observability.signals.ingest", "archive.artifacts.reported"},
	"stream_lifecycle":      {"streams.create", "streams.start", "streams.stop", "streams.mark_failed", "streams.retry_upload"},
	"security":              {"auth.login", "auth.logout", "auth.change_password", "users.create", "users.update", "users.disable", "users.lock", "users.unlock", "users.reset_password", "users.force_password_change", "roles.create", "roles.update", "roles.delete"},
	"secrets":               {"secrets.update", "security.settings.update", "api_tokens.create", "api_tokens.revoke", "api_tokens.rotate"},
	"notifications":         {"notification_channels.create", "notification_channels.update", "notification_channels.delete", "notification_channels.test", "notifications.email.send"},
}

func auditFilterFromRequest(r *http.Request, defaultLimit int) store.AuditFilter {
	query := r.URL.Query()
	filter := store.AuditFilter{
		Limit:  parseLimit(r, defaultLimit),
		Result: query.Get("result"),
		Query:  query.Get("q"),
		From:   parseAuditBoundary(query.Get("from"), false),
		To:     parseAuditBoundary(query.Get("to"), true),
	}
	if group := query.Get("action_group"); group != "" && group != "all" {
		filter.Actions = append(filter.Actions, auditActionGroups[group]...)
	}
	if group := query.Get("exclude_action_group"); group != "" && group != "all" {
		filter.ExcludedActions = append(filter.ExcludedActions, auditActionGroups[group]...)
	}
	for _, action := range strings.Split(query.Get("action"), ",") {
		action = strings.TrimSpace(action)
		if action != "" {
			filter.Actions = append(filter.Actions, action)
		}
	}
	return filter
}

func parseAuditBoundary(value string, exclusiveEnd bool) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		if exclusiveEnd {
			return parsed.Add(24 * time.Hour).UTC()
		}
		return parsed.UTC()
	}
	return time.Time{}
}

func publicAuditEvents(events []store.AuditEvent) []store.AuditEvent {
	out := make([]store.AuditEvent, 0, len(events))
	for _, event := range events {
		out = append(out, publicAuditEvent(event))
	}
	return out
}

func publicAuditEvent(event store.AuditEvent) store.AuditEvent {
	event.ActorUserID = redactAuditResponseString(event.ActorUserID)
	event.ActorUsername = redactAuditResponseString(event.ActorUsername)
	event.ActorIP = redactAuditResponseString(event.ActorIP)
	event.UserAgent = redactAuditResponseString(event.UserAgent)
	event.ResourceID = redactAuditResponseString(event.ResourceID)
	event.RequestID = redactAuditResponseString(event.RequestID)
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
		return event
	}
	event.Metadata = redactAuditResponseValue(event.Metadata).(map[string]any)
	return event
}

func redactAuditResponseValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if secretResponseKey(key) {
				out[key] = "<redacted>"
				continue
			}
			out[key] = redactAuditResponseValue(nested)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if secretResponseKey(key) {
				out[key] = "<redacted>"
				continue
			}
			out[key] = redactAuditResponseValue(nested)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			out = append(out, redactAuditResponseValue(nested))
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, nested := range typed {
			out = append(out, redactAuditResponseString(nested))
		}
		return out
	case string:
		return redactAuditResponseString(typed)
	default:
		return value
	}
}

func redactAuditResponseString(value string) string {
	if secretResponseValue(value) {
		return "<redacted>"
	}
	return value
}

func (s *Server) writeAudit(r *http.Request, event store.AuditEvent) {
	if s.audit == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.RequestID == "" {
		event.RequestID = requestID()
	}
	if event.ActorIP == "" {
		event.ActorIP = clientIP(r)
	}
	if event.UserAgent == "" {
		event.UserAgent = r.UserAgent()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	current := currentFromContext(r.Context())
	if event.ActorUserID == "" {
		event.ActorUserID = current.User.ID
	}
	if event.ActorUsername == "" {
		event.ActorUsername = current.User.Username
	}
	if err := s.audit.WriteAudit(r.Context(), event); err != nil {
		log.Printf("audit write failed: action=%s resource_type=%s result=%s error=%v", event.Action, event.ResourceType, event.Result, err)
		return
	}
	s.appendStreamAuditLog(r.Context(), event)
	s.notifyAdminAuditEvent(event)
}

func (s *Server) writeSystemAudit(ctx context.Context, event store.AuditEvent) {
	if s.audit == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.ActorUsername == "" {
		event.ActorUsername = "system"
	}
	if event.RequestID == "" {
		event.RequestID = requestID()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	if err := s.audit.WriteAudit(ctx, event); err != nil {
		log.Printf("system audit write failed: action=%s resource_type=%s result=%s error=%v", event.Action, event.ResourceType, event.Result, err)
		return
	}
	s.appendStreamAuditLog(ctx, event)
	s.notifyAdminAuditEvent(event)
}

func (s *Server) appendStreamAuditLog(ctx context.Context, event store.AuditEvent) {
	if s.streams == nil || strings.ToLower(strings.TrimSpace(event.ResourceType)) != "stream" || strings.TrimSpace(event.ResourceID) == "" {
		return
	}
	redacted := store.RedactAuditEvent(event)
	// RetryArchiveUpload already persists its successful request as the
	// canonical stream log. Keep failed audit attempts here, but do not create a
	// second success row for the same operator action.
	if strings.TrimSpace(redacted.Action) == "streams.retry_upload" && strings.EqualFold(strings.TrimSpace(redacted.Result), "success") {
		return
	}
	level := "info"
	if result := strings.ToLower(strings.TrimSpace(redacted.Result)); result != "" && result != "success" && result != "ok" {
		level = "error"
	}
	fields := map[string]any{
		"source":         "audit",
		"action":         strings.TrimSpace(redacted.Action),
		"result":         strings.TrimSpace(redacted.Result),
		"actor_username": strings.TrimSpace(redacted.ActorUsername),
		"request_id":     strings.TrimSpace(redacted.RequestID),
	}
	if metadata := safeStreamLogMetadata(redacted.Metadata); len(metadata) > 0 {
		fields["metadata"] = metadata
	}
	if _, err := s.streams.AppendStreamLog(ctx, store.StreamLog{
		StreamID:  redacted.ResourceID,
		Level:     level,
		Message:   redacted.Action,
		Fields:    fields,
		CreatedAt: redacted.Timestamp,
	}); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("stream log append failed: action=%s stream_id=%s error=%v", redacted.Action, redacted.ResourceID, err)
	}
}

func safeStreamLogMetadata(metadata map[string]any) map[string]any {
	filtered := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if streamLogSensitiveKey(key) {
			continue
		}
		if safe, ok := safeStreamLogValue(value); ok {
			filtered[key] = safe
		}
	}
	return filtered
}

func safeStreamLogValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return safeStreamLogMetadata(typed), true
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if streamLogSensitiveKey(key) {
				continue
			}
			if safe, ok := safeStreamLogValue(item); ok {
				out[key] = safe
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			if safe, ok := safeStreamLogValue(item); ok {
				out = append(out, safe)
			}
		}
		return out, true
	case []string:
		return append([]string(nil), typed...), true
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), true
	case string, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, nil:
		return typed, true
	default:
		return nil, false
	}
}

func streamLogSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, fragment := range []string{"authorization", "cookie", "credential", "password", "secret", "token", "webhook", "url"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func (s *Server) notifyAdminAuditEvent(event store.AuditEvent) {
	if !adminAuditEventNotificationAllowed(event) {
		return
	}
	redacted := store.RedactAuditEvent(event)
	payload := map[string]any{
		"event_type":     "admin.audit",
		"severity":       adminAuditNotificationSeverity(redacted),
		"status":         strings.TrimSpace(redacted.Result),
		"action":         strings.TrimSpace(redacted.Action),
		"service_id":     adminAuditNotificationServiceID(redacted),
		"resource_type":  strings.TrimSpace(redacted.ResourceType),
		"resource_id":    strings.TrimSpace(redacted.ResourceID),
		"actor_username": strings.TrimSpace(redacted.ActorUsername),
		"summary":        adminAuditNotificationSummary(redacted),
		"details":        adminAuditNotificationDetails(redacted),
		"timestamp":      redacted.Timestamp.UTC().Format(time.RFC3339),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), adminAuditNotificationTimeout)
		defer cancel()
		obs, configured, err := s.observabilityClient(ctx)
		if err != nil || !configured {
			return
		}
		if obs.Timeout < notificationObservabilityCallTimeout {
			obs.Timeout = notificationObservabilityCallTimeout
		}
		if _, err := obs.Post(ctx, "/notification-events", payload); err != nil {
			log.Printf("admin audit notification failed: action=%s resource_type=%s result=%s error=%v", redacted.Action, redacted.ResourceType, redacted.Result, err)
		}
	}()
}

func adminAuditEventNotificationAllowed(event store.AuditEvent) bool {
	action := strings.ToLower(strings.TrimSpace(event.Action))
	if action == "" {
		return false
	}
	// The test endpoint already sends its own canonical notification. Forwarding
	// its audit record would make every destination receive the same test twice.
	if action == "notification_channels.test" {
		return false
	}
	actorUserID := strings.ToLower(strings.TrimSpace(event.ActorUserID))
	actorUsername := strings.ToLower(strings.TrimSpace(event.ActorUsername))
	if strings.HasPrefix(actorUserID, "service:") || strings.HasPrefix(actorUsername, "service:") {
		switch action {
		case "system_updates.succeeded", "system_updates.rolled_back", "system_updates.failed",
			"system_updates.bootstrap.succeeded", "system_updates.bootstrap.failed":
			return true
		default:
			return false
		}
	}
	if strings.TrimSpace(event.ActorUserID) != "" {
		return true
	}
	return actorUsername == "system"
}

func adminAuditNotificationSeverity(event store.AuditEvent) string {
	result := strings.ToLower(strings.TrimSpace(event.Result))
	if result != "" && result != "success" && result != "ok" {
		return "warning"
	}
	action := strings.ToLower(strings.TrimSpace(event.Action))
	if strings.HasPrefix(action, "security.") || strings.HasPrefix(action, "secrets.") || strings.HasPrefix(action, "users.delete") || strings.HasPrefix(action, "roles.delete") {
		return "warning"
	}
	return "info"
}

func adminAuditNotificationSummary(event store.AuditEvent) string {
	action := strings.TrimSpace(event.Action)
	if action == "" {
		action = "admin action"
	}
	result := strings.TrimSpace(event.Result)
	if result == "" {
		result = "recorded"
	}
	return "管理イベント: " + action + " / " + result
}

func adminAuditNotificationServiceID(event store.AuditEvent) string {
	for _, key := range []string{"service_id", "target_service_id", "agent_service_id"} {
		if value := adminAuditMetadataString(event.Metadata, key); value != "" && value != "<redacted>" && !strings.EqualFold(value, "observability") {
			return value
		}
	}
	// target_id is only a service identifier for update/runtime operations. For
	// stream, OAuth, and user operations it is commonly a resource or job ID;
	// treating every target_id as a service produced misleading service labels.
	if adminAuditMetadataIndicatesService(event.Metadata) {
		if value := adminAuditMetadataString(event.Metadata, "target_id"); value != "" && value != "<redacted>" {
			return value
		}
	}
	if strings.EqualFold(strings.TrimSpace(event.ResourceType), "service") && strings.TrimSpace(event.ResourceID) != "" && strings.TrimSpace(event.ResourceID) != "<redacted>" {
		return strings.TrimSpace(event.ResourceID)
	}
	return "control-panel"
}

func adminAuditMetadataIndicatesService(metadata map[string]any) bool {
	if strings.TrimSpace(adminAuditMetadataString(metadata, "target_service_type")) != "" {
		return true
	}
	serviceType := strings.ToLower(strings.TrimSpace(adminAuditMetadataString(metadata, "service_type")))
	switch serviceType {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability", "updater", "host_agent":
		return true
	}
	return false
}

func adminAuditNotificationDetails(event store.AuditEvent) string {
	labels := map[string]string{
		"reason":                "理由",
		"code":                  "コード",
		"target_id":             "対象ID",
		"stream_id":             "配信枠ID",
		"service_id":            "サービスID",
		"target_service_type":   "対象種別",
		"service_type":          "サービス種別",
		"assignment_role":       "割当ロール",
		"source":                "発生元",
		"provider_type":         "プロバイダ種別",
		"event_type":            "イベント種別",
		"operation":             "操作",
		"deployment_mode":       "配置方式",
		"transport_mode":        "転送方式",
		"current_version":       "現行バージョン",
		"target_version":        "更新先バージョン",
		"progress":              "進捗",
		"status":                "状態",
		"update_status":         "更新状態",
		"trigger":               "トリガー",
		"strategy":              "方式",
		"job_id":                "ジョブID",
		"update_job_id":         "更新ジョブID",
		"port_result":           "ポート結果",
		"old_port":              "旧ポート",
		"new_port":              "新ポート",
		"status_code":           "HTTPステータス",
		"missing_service_types": "不足サービス種別",
		"readiness_issues":      "開始前チェック",
		"youtube_output_id":     "YouTube出力ID",
		"archive_profile_id":    "アーカイブプロファイルID",
		"discord_config_id":     "Discord設定ID",
		"artifact_id":           "録画ファイルID",
		"artifact_name":         "録画ファイル名",
		"share_id":              "共有リンクID",
		"previous_status":       "直前の状態",
		"current_status":        "現在の状態",
		"waiting_stream_id":     "待機枠ID",
		"completed":             "完了処理",
		"skipped":               "スキップ",
		"dispatch":              "配信結果",
	}
	keys := []string{
		"reason", "code", "target_id", "stream_id", "service_id", "target_service_type", "service_type", "assignment_role", "source", "provider_type", "event_type", "operation", "deployment_mode", "transport_mode",
		"current_version", "target_version", "progress", "status", "update_status", "trigger", "strategy", "job_id", "update_job_id",
		"port_result", "old_port", "new_port", "status_code", "missing_service_types", "readiness_issues", "youtube_output_id", "archive_profile_id", "discord_config_id",
		"artifact_id", "artifact_name", "share_id", "previous_status", "current_status", "waiting_stream_id", "completed", "skipped", "dispatch",
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := adminAuditMetadataString(event.Metadata, key)
		if value == "" || value == "<redacted>" {
			continue
		}
		label := labels[key]
		if label == "" {
			label = key
		}
		parts = append(parts, label+": "+value)
	}
	return truncateAdminAuditNotificationText(strings.Join(parts, " / "), 2400)
}

func adminAuditMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func truncateAdminAuditNotificationText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

func (s *Server) writeServiceAudit(r *http.Request, token store.ServiceToken, action, resourceType, resourceID, result string, metadata map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["service_type"] = token.ServiceType
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   "service:" + token.ServiceType,
		ActorUsername: token.ServiceType,
		Action:        action,
		ResourceType:  resourceType,
		ResourceID:    resourceID,
		Result:        result,
		Metadata:      metadata,
	})
}
