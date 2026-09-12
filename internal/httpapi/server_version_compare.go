package httpapi

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

func parseLatestVersionResponse(body []byte) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err == nil {
		for _, key := range []string{"latest_version", "tag_name", "version", "name"} {
			if value, ok := decoded[key].(string); ok {
				if normalized := strings.TrimSpace(value); normalized != "" {
					return normalized
				}
			}
		}
	}
	return strings.TrimSpace(string(body))
}

func versionIsNewer(latest, current string) bool {
	latestVersion, latestOK := parseSemanticVersion(latest)
	currentVersion, currentOK := parseSemanticVersion(current)
	if !latestOK || !currentOK {
		return false
	}
	for i := 0; i < len(latestVersion.core); i++ {
		if latestVersion.core[i] > currentVersion.core[i] {
			return true
		}
		if latestVersion.core[i] < currentVersion.core[i] {
			return false
		}
	}
	return comparePrerelease(latestVersion.prerelease, currentVersion.prerelease) > 0
}

type semanticVersion struct {
	core       [3]int
	prerelease []string
}

func parseSemanticVersion(raw string) (semanticVersion, bool) {
	var result semanticVersion
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if value == "" || value == "dev" || strings.Count(value, "+") > 1 {
		return result, false
	}
	if plus := strings.IndexByte(value, '+'); plus >= 0 {
		if !validSemverIdentifiers(value[plus+1:], false) {
			return result, false
		}
		value = value[:plus]
	}
	prerelease := ""
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		prerelease = value[dash+1:]
		value = value[:dash]
		if !validSemverIdentifiers(prerelease, true) {
			return result, false
		}
		result.prerelease = strings.Split(prerelease, ".")
	}
	fields := strings.Split(value, ".")
	if len(fields) != 3 {
		return semanticVersion{}, false
	}
	for i, field := range fields {
		if field == "" || (len(field) > 1 && field[0] == '0') {
			return semanticVersion{}, false
		}
		parsed, err := strconv.Atoi(field)
		if err != nil || parsed < 0 {
			return semanticVersion{}, false
		}
		result.core[i] = parsed
	}
	return result, true
}

func validSemverIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, r := range identifier {
			if (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && r != '-' {
				return false
			}
			if r < '0' || r > '9' {
				numeric = false
			}
		}
		if rejectNumericLeadingZero && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func comparePrerelease(left, right []string) int {
	if len(left) == 0 && len(right) == 0 {
		return 0
	}
	if len(left) == 0 {
		return 1
	}
	if len(right) == 0 {
		return -1
	}
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		leftNumeric := semverIdentifierNumeric(left[i])
		rightNumeric := semverIdentifierNumeric(right[i])
		switch {
		case leftNumeric && rightNumeric:
			if len(left[i]) != len(right[i]) {
				if len(left[i]) > len(right[i]) {
					return 1
				}
				return -1
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		}
		if left[i] > right[i] {
			return 1
		}
		if left[i] < right[i] {
			return -1
		}
	}
	if len(left) > len(right) {
		return 1
	}
	if len(left) < len(right) {
		return -1
	}
	return 0
}

func semverIdentifierNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseVersionParts(raw string) ([3]int, bool) {
	var parts [3]int
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	if trimmed == "" || trimmed == "dev" {
		return parts, false
	}
	for i, field := range strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '.' || r == '-' || r == '+'
	}) {
		if i >= len(parts) {
			break
		}
		value, err := strconv.Atoi(field)
		if err != nil {
			return parts, false
		}
		parts[i] = value
		if i == len(parts)-1 {
			return parts, true
		}
	}
	return parts, false
}

func isLocalUpdateCheckHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1"
}

func trustedGitHubUpdateAPIURL(parsed *url.URL) bool {
	return parsed != nil && strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), "api.github.com")
}
