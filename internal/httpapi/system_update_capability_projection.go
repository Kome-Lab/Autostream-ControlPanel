package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func systemUpdateManagedPolicyReport(agent store.RegisteredService) (int64, string, string) {
	revision, _ := capabilityInt64(agent.ReportedCapabilities["policy_revision"])
	status := strings.ToLower(strings.TrimSpace(capabilityString(agent.ReportedCapabilities["policy_status"])))
	switch status {
	case "applied", "pending", "failed":
	default:
		status = "pending"
	}
	errorCode := strings.ToLower(strings.TrimSpace(capabilityString(agent.ReportedCapabilities["policy_error_code"])))
	if !validSystemUpdatePolicyErrorCode(errorCode) ||
		(status != "failed" && !(status == "pending" && errorCode == "active_job_pending")) {
		errorCode = ""
	}
	return revision, status, errorCode
}

func capabilityInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		if typed >= 0 {
			return int64(typed), true
		}
	case int64:
		if typed >= 0 {
			return typed, true
		}
	case float64:
		integer := int64(typed)
		if typed >= 0 && float64(integer) == typed {
			return integer, true
		}
	case json.Number:
		integer, err := typed.Int64()
		if err == nil && integer >= 0 {
			return integer, true
		}
	case string:
		integer, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err == nil && integer >= 0 {
			return integer, true
		}
	}
	return 0, false
}

func capabilityString(value any) string {
	text, _ := value.(string)
	return text
}

func validSystemUpdatePolicyErrorCode(value string) bool {
	switch value {
	case "", "policy_fetch_failed", "policy_invalid", "policy_snapshot_failed", "coordinator_start_failed", "active_job_pending":
		return true
	default:
		return false
	}
}

func validSystemUpdateCapabilityIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 191 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validSystemUpdateHostDisplayName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 191 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func systemUpdateDisplayName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if !validSystemUpdateHostDisplayName(name) {
		return fallback
	}
	return name
}

func systemUpdateAgentVersion(agent store.RegisteredService) string {
	if value := strings.TrimSpace(agent.ReportedVersion); value != "" {
		return value
	}
	return strings.TrimSpace(agent.Version)
}

func sortedApprovedSystemUpdateTargetIDs(targets map[string]systemUpdateApprovedTarget) []string {
	ids := make([]string, 0, len(targets))
	for targetID := range targets {
		ids = append(ids, targetID)
	}
	sort.Strings(ids)
	return ids
}

func sortedCapabilityTargetIDs(modes map[string]string) []string {
	ids := make([]string, 0, len(modes))
	for targetID := range modes {
		ids = append(ids, targetID)
	}
	sort.Strings(ids)
	return ids
}

func systemUpdateAgentVersionAtLeast(current, minimum string) bool {
	if !validMinimumUpdateAgentVersion(minimum) || !strings.HasPrefix(strings.TrimSpace(current), "v") {
		return false
	}
	currentParts, currentOK := parseVersionParts(current)
	minimumParts, minimumOK := parseVersionParts(minimum)
	if !currentOK || !minimumOK {
		return false
	}
	for index := range currentParts {
		if currentParts[index] != minimumParts[index] {
			return currentParts[index] > minimumParts[index]
		}
	}
	return !strings.Contains(strings.TrimPrefix(strings.TrimSpace(current), "v"), "-")
}

func (s *Server) activeSystemUpdateForStreamTargets(ctx context.Context, assignments []store.RegisteredService) (store.SystemUpdateJob, bool, error) {
	if s.systemUpdates == nil {
		return store.SystemUpdateJob{}, false, nil
	}
	targetIDs := make([]string, 0, len(assignments)+1)
	targetIDs = append(targetIDs, "control-panel")
	seen := map[string]bool{"control-panel": true}
	for _, assignment := range assignments {
		targetID := strings.TrimSpace(assignment.ServiceID)
		if targetID != "" && !seen[targetID] {
			seen[targetID] = true
			targetIDs = append(targetIDs, targetID)
		}
	}
	for _, targetID := range targetIDs {
		job, err := s.systemUpdates.GetActiveSystemUpdateJob(ctx, targetID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return store.SystemUpdateJob{}, false, err
		}
		if job.Status != store.SystemUpdateStatusQueued {
			return job, true, nil
		}
	}
	return store.SystemUpdateJob{}, false, nil
}

func capabilityStringSlice(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case []string:
		raw = append(raw, typed...)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				raw = append(raw, text)
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" && len(item) <= 191 && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func capabilityStringMap(value any) map[string]string {
	out := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			out[strings.TrimSpace(key)] = strings.TrimSpace(item)
		}
	case map[string]any:
		for key, item := range typed {
			if text, ok := item.(string); ok {
				out[strings.TrimSpace(key)] = strings.TrimSpace(text)
			}
		}
	}
	return out
}

func capabilityInt64Map(value any) map[string]int64 {
	out := map[string]int64{}
	switch typed := value.(type) {
	case map[string]int:
		for key, item := range typed {
			if item >= 0 {
				out[strings.TrimSpace(key)] = int64(item)
			}
		}
	case map[string]int64:
		for key, item := range typed {
			if item >= 0 {
				out[strings.TrimSpace(key)] = item
			}
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := capabilityInt64(item); ok {
				out[strings.TrimSpace(key)] = parsed
			}
		}
	}
	return out
}

func capabilityBool(value any) bool {
	parsed, _ := value.(bool)
	return parsed
}

func capabilityBoolMap(value any) map[string]bool {
	out := map[string]bool{}
	switch typed := value.(type) {
	case map[string]bool:
		for key, item := range typed {
			out[strings.TrimSpace(key)] = item
		}
	case map[string]any:
		for key, item := range typed {
			if parsed, ok := item.(bool); ok {
				out[strings.TrimSpace(key)] = parsed
			}
		}
	}
	return out
}

func systemUpdateAgentAvailable(agent store.RegisteredService, now time.Time) bool {
	switch strings.ToLower(strings.TrimSpace(agent.Status)) {
	case "offline", "disabled", "pending":
		return false
	}
	if agent.LastHeartbeatAt == nil {
		return false
	}
	heartbeatAt := agent.LastHeartbeatAt.UTC()
	age := now.Sub(heartbeatAt)
	return age >= 0 && age <= heartbeatOfflineAfter()
}

func validSystemUpdateVersion(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) <= 128 && systemUpdateVersionPattern.MatchString(raw)
}

func systemUpdateStatusTerminal(status string) bool {
	return status == store.SystemUpdateStatusSucceeded || status == store.SystemUpdateStatusRolledBack || status == store.SystemUpdateStatusFailed
}
