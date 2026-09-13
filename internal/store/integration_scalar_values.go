package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func publicDriveDestination(destination DriveDestination) DriveDestination {
	destination.FolderID = ""
	return destination
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func marshalStringSlice(values []string) (string, error) {
	body, err := json.Marshal(cleanStringSlice(values))
	return string(body), err
}

func marshalStringSlices(first, second []string) (string, string, error) {
	a, err := marshalStringSlice(first)
	if err != nil {
		return "", "", err
	}
	b, err := marshalStringSlice(second)
	if err != nil {
		return "", "", err
	}
	return a, b, nil
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func normalizedOAuthRefreshTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func oauthAccountTokenRevisionMatches(actual, expected uint64) bool {
	return actual != 0 && expected != 0 && actual == expected
}

func nextOAuthAccountTokenRevision(current uint64) uint64 {
	if current == 0 {
		return 1
	}
	return current + 1
}

func normalizedOAuthTokenRefreshFailure(code string, relinkRequired bool) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(code)) {
	case OAuthTokenRefreshFailureCredentialsUnavailable,
		OAuthTokenRefreshFailureProviderNotReady,
		OAuthTokenRefreshFailureProviderUnavailable,
		OAuthTokenRefreshFailureProviderCredentialsInvalid,
		OAuthTokenRefreshFailureTimedOut,
		OAuthTokenRefreshFailureInvalidResponse:
		return strings.TrimSpace(strings.ToLower(code)), false
	case OAuthTokenRefreshFailureReauthorizationRequired:
		return OAuthTokenRefreshFailureReauthorizationRequired, relinkRequired
	default:
		return OAuthTokenRefreshFailureUnknown, false
	}
}

func notFoundOnNoRows(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func maskIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 10 {
		return "<configured>"
	}
	return value[:4] + "..." + value[len(value)-4:]
}
