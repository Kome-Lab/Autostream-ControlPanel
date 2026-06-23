package store

import (
	"net/url"
	"regexp"
	"strings"
)

var auditSecretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)discord\.com/api/webhooks/[0-9A-Za-z_-]+/[0-9A-Za-z_-]+`),
	regexp.MustCompile(`(?i)hooks\.slack\.com/services/[0-9A-Za-z/_-]+`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{20,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\bast_svc_[A-Za-z0-9_-]{16,}\b`),
	regexp.MustCompile(`\bast_ingest_v1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----`),
	regexp.MustCompile(`\b1//[0-9A-Za-z_-]{20,}\b`),
	regexp.MustCompile(`(?i)\braw-[0-9a-z_-]*(token|secret|password|stream-key|folder-id)[0-9a-z_-]*\b`),
	regexp.MustCompile(`(?i)\b(stream[_ -]?key|refresh[_ -]?token|access[_ -]?token|client[_ -]?secret|smtp[_ -]?password|folder[_ -]?id)\s*[:=]\s*[^\s,;]+`),
}

func redactedAuditEvent(event AuditEvent) AuditEvent {
	event.ActorUserID = redactedAuditString(event.ActorUserID)
	event.ActorUsername = redactedAuditString(event.ActorUsername)
	event.ActorIP = redactedAuditString(event.ActorIP)
	event.UserAgent = redactedAuditString(event.UserAgent)
	event.ResourceID = redactedAuditString(event.ResourceID)
	event.RequestID = redactedAuditString(event.RequestID)
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
		return event
	}
	event.Metadata = redactedAuditMetadata(event.Metadata).(map[string]any)
	return event
}

func redactedAuditString(value string) string {
	if auditSecretValue(value) {
		return "<redacted>"
	}
	return value
}

func redactedAuditMetadata(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if auditSecretKey(key) {
				out[key] = "<redacted>"
				continue
			}
			out[key] = redactedAuditMetadata(nested)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			out = append(out, redactedAuditMetadata(nested))
		}
		return out
	case string:
		if auditSecretValue(typed) {
			return "<redacted>"
		}
		return typed
	default:
		return value
	}
}

func auditSecretKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(key, "has_") || strings.HasSuffix(key, "_configured") || strings.HasSuffix(key, "_status") || strings.HasSuffix(key, "_fingerprint") {
		return false
	}
	if key == "value" || key == "raw" || key == "authorization" {
		return true
	}
	for _, token := range []string{"password", "passwd", "token", "secret", "api_key", "apikey", "private_key", "credential", "webhook_url", "stream_key", "refresh_token", "access_token", "client_secret", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func auditSecretValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, pattern := range auditSecretValuePatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" && parsed.User != nil {
		return true
	}
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		for key, values := range parsed.Query() {
			if !auditSecretKey(key) {
				continue
			}
			for _, queryValue := range values {
				if strings.TrimSpace(queryValue) != "" {
					return true
				}
			}
		}
	}
	return false
}
