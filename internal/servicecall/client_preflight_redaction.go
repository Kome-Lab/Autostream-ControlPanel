package servicecall

import (
	"net/url"
	"strings"
)

func RedactServicePreflightResult(result ServicePreflightResult) ServicePreflightResult {
	result.Error = redactPreflightString(result.Error)
	if len(result.Checks) > 0 {
		checks := make([]ServicePreflightCheck, 0, len(result.Checks))
		for _, check := range result.Checks {
			check.ID = redactPreflightString(check.ID)
			check.Status = redactPreflightString(check.Status)
			check.Severity = redactPreflightString(check.Severity)
			check.Message = redactPreflightString(check.Message)
			checks = append(checks, check)
		}
		result.Checks = checks
	}
	if result.Summary != nil {
		if redacted, ok := redactPreflightValue(result.Summary).(map[string]any); ok {
			result.Summary = redacted
		} else {
			result.Summary = nil
		}
	}
	return result
}

func RedactWorkerEventsResult(result WorkerEventsResult) WorkerEventsResult {
	result.Error = redactPreflightString(result.Error)
	for i := range result.Events {
		if result.Events[i].Payload == nil {
			continue
		}
		if redacted, ok := redactPreflightValue(result.Events[i].Payload).(map[string]any); ok {
			result.Events[i].Payload = redacted
		} else {
			result.Events[i].Payload = nil
		}
	}
	return result
}

func redactPreflightValue(value any) any {
	switch typed := value.(type) {
	case string:
		return redactPreflightString(typed)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, nil:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactPreflightValue(item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if preflightSecretKey(key) {
				out[key] = "<redacted>"
				continue
			}
			out[key] = redactPreflightValue(nested)
		}
		return out
	default:
		return nil
	}
}

func redactPreflightString(value string) string {
	if preflightSecretValue(value) {
		return "<redacted>"
	}
	return value
}

func preflightSecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	for _, token := range []string{
		"webhook_url",
		"token",
		"secret",
		"password",
		"private_key",
		"credential",
		"authorization",
		"stream_key",
		"refresh_token",
		"access_token",
		"folder_id",
		"drive_folder_id",
		"google_drive_folder_id",
		"gdrive_folder_id",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func preflightSecretValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "<redacted>") || strings.Contains(trimmed, "<WEBHOOK_PATH>") || strings.Contains(trimmed, "****") {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, pattern := range []string{
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
		"-----begin private key-----",
		"ast_svc_",
		"ast_ingest_v1.",
		"ya29.",
	} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && parsed.User != nil {
		return true
	}
	return false
}
