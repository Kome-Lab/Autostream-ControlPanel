package servicecall

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"net/http"
	"os"
	"strings"
	"time"
)

func sanitizeServiceErrorValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 80 {
		value = value[:80]
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r == ':' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "invalid_error_value"
		}
	}
	return value
}

func joinURL(baseURL, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return c.Config.URLPolicy.HTTPClient(c.Config.Timeout)
}

func serviceURLIssueCode(err error) string {
	if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
		return "service_public_url_blocked"
	}
	return "service_public_url_invalid"
}

func encoderURLIssueCode(err error) string {
	if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
		return "encoder_public_url_blocked"
	}
	return "encoder_public_url_invalid"
}

func serviceURLMessage(err error) string {
	if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
		return "service public_url is blocked by outbound policy"
	}
	return "service public_url must be an absolute http or https URL without credentials"
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value + "s")
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func envMinutes(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value + "m")
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
