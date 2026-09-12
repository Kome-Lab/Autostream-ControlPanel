package httpapi

import (
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func secretStatusByName(statuses []store.SecretStatus, name string) store.SecretStatus {
	if name == "" {
		return store.SecretStatus{}
	}
	for _, status := range statuses {
		if status.Name == name {
			return status
		}
	}
	return store.SecretStatus{Name: name}
}

func secretStatusListForConfig(profile store.Profile, secretNameKey, fingerprintKey string) []store.SecretStatus {
	if fingerprint := strings.TrimSpace(configString(profile.Config, fingerprintKey)); fingerprint != "" {
		return []store.SecretStatus{{Name: configString(profile.Config, secretNameKey), Configured: true, Fingerprint: fingerprint}}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mapString(values map[string]any, key string) string {
	text, _ := values[key].(string)
	return text
}

func mapBool(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func mapBoolDefault(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func configString(config map[string]any, key string) string {
	value, ok := config[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}

func defaultConfigString(config map[string]any, key, fallback string) string {
	value := strings.TrimSpace(configString(config, key))
	if value == "" {
		return fallback
	}
	return value
}

func configInt(config map[string]any, key string) int {
	value, ok := config[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func configBool(config map[string]any, key string) bool {
	value, ok := config[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func configBoolDefault(config map[string]any, key string, fallback bool) bool {
	value, ok := config[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func youtubeCompleteOnStop(config map[string]any) bool {
	if normalizedYouTubeOutputMode(firstNonEmpty(configString(config, "mode"), configString(config, "output_mode"))) == "live_api_relay_static" {
		return true
	}
	return configBoolDefault(config, "complete_on_stop", true)
}

func configTime(config map[string]any, key string) time.Time {
	value := strings.TrimSpace(configString(config, key))
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
