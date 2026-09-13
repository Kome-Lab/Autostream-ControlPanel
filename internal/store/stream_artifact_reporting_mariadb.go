package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s MariaDBStreamStore) WriteStreamArtifactReport(ctx context.Context, token ServiceToken, event ServiceStreamEvent, artifacts []StreamArtifact) error {
	if event.ServiceID == "" || event.StreamID == "" || event.EventType == "" {
		return errors.New("missing required stream event field")
	}
	if err := ValidateStreamArtifactReport(event.StreamID, artifacts); err != nil {
		return err
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.Payload = sanitizeServiceEventPayload(event.Payload)
	body, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	normalized := NormalizeStreamArtifacts(event.StreamID, artifacts)
	auth := MariaDBAuthStore{db: s.db}
	discoveredService, err := auth.getService(ctx, event.ServiceID)
	if err != nil {
		return err
	}
	discoveredRows, err := discoverMariaDBAssignmentsForService(ctx, s.db, event.ServiceID)
	if err != nil {
		return err
	}
	streamIDs := []string{event.StreamID, discoveredService.CurrentStreamID}
	for _, row := range discoveredRows {
		streamIDs = append(streamIDs, row.StreamID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, streamIDs)
	if err != nil {
		return ErrForbidden
	}
	if target, ok := lockedStreams[event.StreamID]; !ok || target.DeletedAt != nil {
		return ErrForbidden
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, []string{event.ServiceID})
	if err != nil {
		return ErrForbidden
	}
	service, ok := lockedServices[event.ServiceID]
	if !ok || service.ServiceType != discoveredService.ServiceType || strings.TrimSpace(service.CurrentStreamID) != strings.TrimSpace(discoveredService.CurrentStreamID) || service.TokenID != token.ID {
		return ErrForbidden
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		if errors.Is(err, ErrServiceAssignmentConflict) {
			return ErrForbidden
		}
		return err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForService(ctx, tx, event.ServiceID)
	if err != nil {
		return err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) {
		return ErrForbidden
	}
	if !serviceStreamEventAllowed(service.ServiceType, event.EventType) {
		return ErrInvalidServiceStreamEvent
	}
	owner, _, err := consistentMariaDBServiceAssignment(ctx, tx, service)
	if err != nil || owner != event.StreamID {
		return ErrForbidden
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, event.StreamID)
	if err != nil {
		return err
	}
	authority := state.Stream
	var activeTokenID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM service_tokens WHERE id = ? AND revoked_at IS NULL FOR UPDATE`, token.ID).Scan(&activeTokenID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_stream_events (id, service_id, stream_id, event_type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`, newUUID(), event.ServiceID, event.StreamID, event.EventType, string(body), time.Now().UTC()); err != nil {
		return err
	}
	for _, artifact := range normalized {
		artifact.ID = newUUID()
		artifact.CreatedAt = time.Now().UTC()
		artifact.SourceServiceID = strings.TrimSpace(event.ServiceID)
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_artifacts (id, stream_id, archive_run_id, archive_started_at, kind, name, relative_path, size_bytes, created_at, source_service_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE relative_path = VALUES(relative_path), size_bytes = VALUES(size_bytes), source_service_id = IF(VALUES(source_service_id) <> '', VALUES(source_service_id), source_service_id)`,
			artifact.ID, event.StreamID, artifact.ArchiveRunID, artifact.ArchiveStartedAt, artifact.Kind, artifact.Name, artifact.RelativePath, artifact.SizeBytes, artifact.CreatedAt, artifact.SourceServiceID); err != nil {
			return err
		}
	}
	if streamArtifactReportMatchesArchiveAuthority(authority, normalized) {
		if err := markStreamArchiveRunReported(ctx, tx, event.StreamID, authority, normalized); err != nil {
			return err
		}
		closedAt := time.Now().UTC()
		if err := closeMariaDBStreamLogGuard(ctx, tx, event.StreamID, archiveRetryAssignmentGuardLogMessage, archiveRetryAssignmentGuardClosedLogMessage, closedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type streamArtifactScanner interface {
	Scan(dest ...any) error
}

func scanStreamArtifact(scanner streamArtifactScanner) (StreamArtifact, error) {
	var artifact StreamArtifact
	var archiveStartedAt sql.NullTime
	if err := scanner.Scan(&artifact.ID, &artifact.StreamID, &artifact.ArchiveRunID, &archiveStartedAt, &artifact.Kind, &artifact.Name, &artifact.RelativePath, &artifact.SizeBytes, &artifact.CreatedAt, &artifact.SourceServiceID); err != nil {
		return StreamArtifact{}, err
	}
	artifact.ArchiveStartedAt = nullTimePtr(archiveStartedAt)
	return artifact, nil
}

type streamArchiveRunReporter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func markStreamArchiveRunReported(ctx context.Context, reporter streamArchiveRunReporter, streamID string, authority Stream, artifacts []StreamArtifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	now := time.Now().UTC()
	_, err := reporter.ExecContext(ctx, `UPDATE streams
SET updated_at = CASE WHEN archive_reported_at IS NULL THEN ? ELSE updated_at END,
    archive_reported_at = COALESCE(archive_reported_at, ?)
WHERE id = ? AND archive_started_at = ? AND archive_run_id = ?`, now, now, streamID, artifacts[0].ArchiveStartedAt, artifacts[0].ArchiveRunID)
	return err
}

func streamArtifactReportMatchesArchiveAuthority(stream Stream, artifacts []StreamArtifact) bool {
	if len(artifacts) == 0 {
		return false
	}
	report := artifacts[0]
	currentRunID := strings.TrimSpace(stream.ArchiveRunID)
	reportRunID := strings.TrimSpace(report.ArchiveRunID)
	return currentRunID != "" && stream.ArchiveStartedAt != nil &&
		reportRunID == currentRunID &&
		report.ArchiveStartedAt != nil &&
		report.ArchiveStartedAt.UTC().Equal(stream.ArchiveStartedAt.UTC())
}

func ValidateStreamArtifactReport(streamID string, artifacts []StreamArtifact) error {
	if strings.TrimSpace(streamID) == "" || len(artifacts) == 0 || len(artifacts) > 20 {
		return errors.New("invalid artifact report")
	}
	allowedKinds := map[string]bool{
		"archive": true, "caption": true, "transcript": true, "metadata": true, "logs": true,
	}
	allowedNames := map[string]string{
		"archive":    "final.mp4",
		"caption":    "captions.vtt",
		"transcript": "transcript.json",
		"metadata":   "metadata.json",
		"logs":       "logs.jsonl",
	}
	seen := map[string]bool{}
	reportRunID := ""
	var reportStartedAt *time.Time
	reportRunSet := false
	for _, artifact := range NormalizeStreamArtifacts(streamID, artifacts) {
		kind := artifact.Kind
		name := artifact.Name
		if !allowedKinds[kind] || name == "" || len(name) > 255 || strings.ContainsAny(name, `/\`) {
			return errors.New("invalid artifact metadata")
		}
		if allowedNames[kind] != name {
			return errors.New("unsupported artifact name")
		}
		if artifact.SizeBytes < 0 || len(artifact.RelativePath) > 1024 || !isSafeRelativePath(artifact.RelativePath) {
			return errors.New("unsafe artifact path")
		}
		if !validArchiveRunID(artifact.ArchiveRunID) || artifact.ArchiveStartedAt == nil || artifact.ArchiveStartedAt.IsZero() {
			return errors.New("invalid archive run metadata")
		}
		if artifact.RelativePath != streamArtifactRelativePath(streamID, artifact.ArchiveRunID, name) {
			return errors.New("artifact path does not match stream and name")
		}
		if !reportRunSet {
			reportRunID = artifact.ArchiveRunID
			reportStartedAt = artifact.ArchiveStartedAt
			reportRunSet = true
		} else if reportRunID != artifact.ArchiveRunID || !sameOptionalTime(reportStartedAt, artifact.ArchiveStartedAt) {
			return errors.New("mixed archive runs in one report")
		}
		key := kind + "\x00" + name
		if seen[key] {
			return errors.New("duplicate artifact")
		}
		seen[key] = true
	}
	return nil
}
