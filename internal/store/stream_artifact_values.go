package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func isSafeArtifactFileName(name string) bool {
	return ValidStreamArtifactFileName(name)
}

func ValidStreamArtifactFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return false
	}
	allowedExt := map[string]bool{
		".mp4": true, ".mkv": true, ".json": true, ".jsonl": true, ".vtt": true,
	}
	if !allowedExt[strings.ToLower(path.Ext(name))] {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validArchiveRunID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) || !isASCIIAlphaNumeric(id[0]) {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func streamArtifactRelativePath(streamID, archiveRunID, name string) string {
	return path.Join("final", streamID, archiveRunID, name)
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func isArchiveRecordingArtifact(artifact StreamArtifact) bool {
	if !strings.EqualFold(strings.TrimSpace(artifact.Kind), "archive") {
		return false
	}
	switch strings.ToLower(path.Ext(strings.TrimSpace(artifact.Name))) {
	case ".mp4", ".webm", ".m4v", ".mov", ".mkv":
		return true
	default:
		return false
	}
}

func NormalizeStreamArtifacts(streamID string, artifacts []StreamArtifact) []StreamArtifact {
	normalized := make([]StreamArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifact.ID = ""
		artifact.StreamID = streamID
		artifact.Kind = strings.TrimSpace(artifact.Kind)
		artifact.Name = strings.TrimSpace(artifact.Name)
		artifact.RelativePath = strings.TrimSpace(artifact.RelativePath)
		artifact.ArchiveRunID = strings.TrimSpace(artifact.ArchiveRunID)
		if artifact.ArchiveStartedAt != nil {
			startedAt := artifact.ArchiveStartedAt.UTC()
			artifact.ArchiveStartedAt = &startedAt
		}
		artifact.CreatedAt = time.Time{}
		normalized = append(normalized, artifact)
	}
	return normalized
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate uuid: %w", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func nullEmpty(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

func truncateString(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

func isSafeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.ContainsAny(path, `\:`) {
		return false
	}
	clean := filepath.Clean(path)
	slashClean := strings.ReplaceAll(clean, `\`, "/")
	if slashClean == "." || slashClean != path || strings.HasPrefix(slashClean, "../") || strings.Contains(slashClean, "/../") {
		return false
	}
	return true
}
