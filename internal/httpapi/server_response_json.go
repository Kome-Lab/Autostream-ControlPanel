package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOneTimeSecretJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, status, value)
}

func writeRawJSON(w http.ResponseWriter, status int, body json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(redactRawJSON(body))
}

func writeObservabilityJSON(w http.ResponseWriter, status int, endpoint string, body json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(sanitizeObservabilityResponse(endpoint, body))
}

func sanitizeObservabilityResponse(endpoint string, body json.RawMessage) []byte {
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case endpoint == "/notification-channels":
		return sanitizeNotificationChannelBody(body)
	case strings.HasPrefix(endpoint, "/notification-channels/") && strings.HasSuffix(endpoint, "/test"):
		return sanitizeNotificationDeliveryBody(body)
	case strings.HasPrefix(endpoint, "/notification-channels/"):
		return sanitizeNotificationChannelBody(body)
	case endpoint == "/notification-deliveries":
		return sanitizeNotificationDeliveryBody(body)
	default:
		return redactRawJSON(body)
	}
}

func sanitizeNotificationChannelBody(body json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return []byte(`{"code":"invalid_upstream_json"}`)
	}
	return marshalSanitizedObservabilityValue(value, publicNotificationChannelFromValue)
}

func sanitizeNotificationDeliveryBody(body json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return []byte(`{"code":"invalid_upstream_json"}`)
	}
	return marshalSanitizedObservabilityValue(value, publicNotificationDeliveryFromValue)
}

func marshalSanitizedObservabilityValue(value any, project func(map[string]any) map[string]any) []byte {
	switch typed := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				out = append(out, project(row))
			}
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return []byte(`[]`)
		}
		return encoded
	case map[string]any:
		encoded, err := json.Marshal(project(typed))
		if err != nil {
			return []byte(`{}`)
		}
		return encoded
	default:
		return redactRawJSON(mustMarshalRaw(value))
	}
}

func publicNotificationChannelFromValue(row map[string]any) map[string]any {
	out := map[string]any{}
	copyAllowedJSONField(out, row, "id")
	copyAllowedJSONField(out, row, "name")
	copyAllowedJSONField(out, row, "type")
	copyAllowedJSONField(out, row, "enabled")
	copyAllowedJSONField(out, row, "uses_global_smtp")
	copyAllowedJSONField(out, row, "masked_webhook_url")
	copyAllowedJSONField(out, row, "masked_email_target")
	copyAllowedJSONField(out, row, "severity_filter")
	copyAllowedJSONField(out, row, "event_type_filter")
	copyAllowedJSONField(out, row, "created_at")
	copyAllowedJSONField(out, row, "updated_at")
	return redactAllowedJSONValues(out)
}

func publicNotificationDeliveryFromValue(row map[string]any) map[string]any {
	out := map[string]any{}
	copyAllowedJSONField(out, row, "id")
	copyAllowedJSONField(out, row, "event_type")
	copyAllowedJSONField(out, row, "channel")
	copyAllowedJSONField(out, row, "incident_id")
	copyAllowedJSONField(out, row, "status")
	copyAllowedJSONField(out, row, "target")
	copyAllowedJSONField(out, row, "error")
	copyAllowedJSONField(out, row, "metadata")
	copyAllowedJSONField(out, row, "created_at")
	return redactAllowedJSONValues(out)
}

func copyAllowedJSONField(out, row map[string]any, key string) {
	if value, ok := row[key]; ok {
		out[key] = value
	}
}

func redactAllowedJSONValues(value map[string]any) map[string]any {
	for key, nested := range value {
		if secretResponseKey(key) {
			delete(value, key)
			continue
		}
		value[key] = redactJSONValueForResponse(nested)
	}
	return value
}

func mustMarshalRaw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return json.RawMessage(encoded)
}

func redactRawJSON(body json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return []byte(`{"code":"invalid_upstream_json"}`)
	}
	value = redactJSONValueForResponse(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return redacted
}

func redactJSONValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if secretResponseKey(key) {
				delete(typed, key)
				continue
			}
			typed[key] = redactJSONValueForResponse(nested)
		}
	case []any:
		for i, nested := range typed {
			typed[i] = redactJSONValueForResponse(nested)
		}
	}
}

func redactJSONValueForResponse(value any) any {
	switch typed := value.(type) {
	case string:
		if secretResponseValue(typed) {
			return "<redacted>"
		}
		return typed
	case map[string]any:
		redactJSONValue(typed)
		return typed
	case []any:
		redactJSONValue(typed)
		return typed
	default:
		return value
	}
}

func secretResponseKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	allowedMaskedKeys := []string{"masked_webhook_url", "masked_email_target", "configured", "fingerprint", "has_webhook_url", "has_smtp_password", "smtp_password_configured"}
	for _, allowed := range allowedMaskedKeys {
		if normalized == allowed {
			return false
		}
	}
	secretTokens := []string{"webhook_url", "token", "secret", "password", "private_key", "credential", "authorization", "stream_key", "refresh_token", "access_token", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id", "email_recipients", "smtp_host", "smtp_port", "smtp_tls", "smtp_from", "smtp_username"}
	for _, token := range secretTokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func secretResponseValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "<WEBHOOK_PATH>") || strings.Contains(trimmed, "****") {
		return false
	}
	lower := strings.ToLower(trimmed)
	patterns := []string{
		"ast_svc_",
		"ast_ingest_v1.",
		"ya29.",
		"discord.com/api/webhooks/",
		"hooks.slack.com/services/",
		"token=",
		"api_key=",
		"apikey=",
		"client_secret=",
		"stream_key=",
		"passphrase=",
		"password=",
		"secret=",
		"access_token",
		"refresh_token",
		"authorization",
		"bearer ",
		"private_key",
		"credential",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	if strings.Contains(lower, "://") {
		afterScheme := lower[strings.Index(lower, "://")+3:]
		if at := strings.Index(afterScheme, "@"); at >= 0 {
			userInfo := afterScheme[:at]
			return strings.Contains(userInfo, ":")
		}
	}
	return false
}
