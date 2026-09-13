package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s MariaDBStreamStore) DeleteStream(ctx context.Context, id string) (err error) {
	defer func() {
		if isMariaDBLockConflict(err) {
			err = mariaDBLockConflictAsAssignmentConflict(err)
		}
	}()
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound
	}
	discoveredRows, err := discoverMariaDBAssignmentsForStream(ctx, s.db, id)
	if err != nil {
		return err
	}
	discoveredCurrentServiceIDs, err := discoverMariaDBCurrentStreamServiceIDs(ctx, s.db, id)
	if err != nil {
		return err
	}
	serviceIDs := append([]string(nil), discoveredCurrentServiceIDs...)
	for _, row := range discoveredRows {
		serviceIDs = append(serviceIDs, row.ServiceID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockedStreams, err := lockMariaDBStreamsSorted(ctx, tx, []string{id})
	if err != nil {
		return err
	}
	if target, ok := lockedStreams[id]; !ok || target.DeletedAt != nil {
		return ErrNotFound
	}
	lockedServices, err := lockMariaDBServicesSorted(ctx, tx, serviceIDs)
	if err != nil {
		return ErrServiceAssignmentConflict
	}
	if err := lockMariaDBAssignmentRowsSorted(ctx, tx, discoveredRows); err != nil {
		return err
	}
	revalidatedRows, err := discoverMariaDBAssignmentsForStream(ctx, tx, id)
	if err != nil {
		return err
	}
	revalidatedCurrentServiceIDs, err := discoverMariaDBCurrentStreamServiceIDs(ctx, tx, id)
	if err != nil {
		return err
	}
	if !mariaDBAssignmentRowsEqual(discoveredRows, revalidatedRows) || !equalSortedStrings(discoveredCurrentServiceIDs, revalidatedCurrentServiceIDs) {
		return ErrServiceAssignmentConflict
	}
	assignmentServiceIDs := make([]string, 0, len(revalidatedRows))
	for _, row := range revalidatedRows {
		assignmentServiceIDs = append(assignmentServiceIDs, row.ServiceID)
	}
	if !equalSortedStrings(assignmentServiceIDs, revalidatedCurrentServiceIDs) {
		return ErrServiceAssignmentConflict
	}
	for _, row := range revalidatedRows {
		service, exists := lockedServices[row.ServiceID]
		if !exists || service.ServiceType != row.ServiceType {
			return ErrServiceAssignmentConflict
		}
		owner, role, consistencyErr := consistentMariaDBServiceAssignment(ctx, tx, service)
		if consistencyErr != nil || owner != id || role != normalizeAssignmentRole(row.AssignmentRole) {
			return ErrServiceAssignmentConflict
		}
	}
	state, err := mariaDBStreamAssignmentProtectionAfterLocks(ctx, tx, id)
	if err != nil {
		return err
	}
	if hasClaim, err := hasStreamYouTubeRelayBindingClaimForStreamTx(ctx, tx, id); err != nil {
		return err
	} else if hasClaim {
		return ErrYouTubeRelayBindingClaimActive
	}
	if state.protected() {
		return ErrServiceUnassignProtectedStream
	}
	var archiveEncoderID string
	encoderRows := append([]mariaDBAssignmentRow(nil), revalidatedRows...)
	sort.Slice(encoderRows, func(i, j int) bool {
		leftPrimary := normalizeAssignmentRole(encoderRows[i].AssignmentRole) == "primary"
		rightPrimary := normalizeAssignmentRole(encoderRows[j].AssignmentRole) == "primary"
		if leftPrimary != rightPrimary {
			return leftPrimary
		}
		return encoderRows[i].ServiceID < encoderRows[j].ServiceID
	})
	for _, row := range encoderRows {
		if row.ServiceType == "encoder_recorder" {
			archiveEncoderID = row.ServiceID
			break
		}
	}
	if archiveEncoderID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE stream_artifacts SET source_service_id = ? WHERE stream_id = ? AND COALESCE(TRIM(source_service_id), '') = ''`, archiveEncoderID, id); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	for _, serviceID := range sortedUniqueStrings(assignmentServiceIDs) {
		result, updateErr := tx.ExecContext(ctx, `UPDATE services SET current_stream_id = NULL, status = CASE WHEN status = 'assigned' THEN 'registered' ELSE status END, updated_at = ? WHERE service_id = ? AND current_stream_id = ?`, now, serviceID, id)
		if updateErr != nil {
			return updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrServiceAssignmentConflict
		}
	}
	for _, row := range revalidatedRows {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM stream_service_assignments WHERE id = ?`, row.ID)
		if deleteErr != nil {
			return deleteErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrServiceAssignmentConflict
		}
	}
	for _, query := range []string{
		`DELETE FROM runtime_secret_leases WHERE stream_id = ?`,
		`DELETE FROM service_remediation_executions WHERE stream_id = ?`,
		`DELETE FROM service_stream_events WHERE stream_id = ?`,
		`DELETE FROM stream_youtube_runtimes WHERE stream_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE streams SET status = 'completed', deleted_at = COALESCE(deleted_at, ?), updated_at = ? WHERE id = ?`, now, now, id)
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
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s MariaDBStreamStore) GetStream(ctx context.Context, id string) (Stream, error) {
	stream, err := scanStreamRow(s.db.QueryRowContext(ctx, streamListQuery("s.id = ?"), id))
	if err == sql.ErrNoRows {
		return Stream{}, ErrNotFound
	}
	if err != nil {
		return Stream{}, err
	}
	return stream, nil
}

func (s MariaDBStreamStore) UpdateStreamSettings(ctx context.Context, id string, settings StreamSettings) (Stream, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Stream{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, err
	}
	defer tx.Rollback()
	var streamID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id = ? FOR UPDATE`, id).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return Stream{}, ErrNotFound
	}
	if err != nil {
		return Stream{}, err
	}
	var claimOutputID string
	err = tx.QueryRowContext(ctx, `SELECT youtube_output_id FROM stream_youtube_relay_binding_claims WHERE stream_id = ? FOR UPDATE`, id).Scan(&claimOutputID)
	if err == nil && claimOutputID != strings.TrimSpace(settings.YouTubeOutputID) {
		return Stream{}, ErrYouTubeRelayBindingClaimActive
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Stream{}, err
	}
	now := time.Now().UTC()
	if err := UpsertStreamSettingsTx(ctx, tx, id, settings, now); err != nil {
		return Stream{}, err
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, err
	}
	return s.GetStream(ctx, id)
}

// UpsertStreamSettingsTx persists non-visual stream settings within an existing
// transaction so a draft asset claim and stream creation commit atomically.
func UpsertStreamSettingsTx(ctx context.Context, tx *sql.Tx, id string, settings StreamSettings, now time.Time) error {
	if tx == nil || strings.TrimSpace(id) == "" {
		return ErrNotFound
	}
	now = now.UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE streams SET name = COALESCE(NULLIF(?, ''), name), scheduled_start_at = ?, scheduled_end_at = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(settings.Name), nullableTime(settings.ScheduledStartAt), nullableTime(settings.ScheduledEndAt), now, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO stream_settings (stream_id, discord_config_id, auto_start_trigger, encoder_profile_id, caption_profile_id, overlay_profile_id, encoder_audio_gain_db, archive_profile_id, archive_drive_destination_id, archive_oauth_account_id, archive_shared_drive, archive_shared_drive_id, archive_file_name, youtube_output_id, encoder_input_url, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE discord_config_id = VALUES(discord_config_id), auto_start_trigger = VALUES(auto_start_trigger), encoder_profile_id = VALUES(encoder_profile_id), caption_profile_id = VALUES(caption_profile_id), overlay_profile_id = VALUES(overlay_profile_id), encoder_audio_gain_db = VALUES(encoder_audio_gain_db), archive_profile_id = VALUES(archive_profile_id), archive_drive_destination_id = VALUES(archive_drive_destination_id), archive_oauth_account_id = VALUES(archive_oauth_account_id), archive_shared_drive = VALUES(archive_shared_drive), archive_shared_drive_id = VALUES(archive_shared_drive_id), archive_file_name = VALUES(archive_file_name), youtube_output_id = VALUES(youtube_output_id), encoder_input_url = VALUES(encoder_input_url), updated_at = VALUES(updated_at)`,
		id, nullEmpty(settings.DiscordConfigID), strings.TrimSpace(settings.AutoStartTrigger), nullEmpty(settings.EncoderProfileID), nullEmpty(settings.CaptionProfileID), nullEmpty(settings.OverlayProfileID), settings.EncoderAudioGainDB, nullEmpty(settings.ArchiveProfileID), nullEmpty(settings.ArchiveDriveDestinationID), nullEmpty(settings.ArchiveOAuthAccountID), settings.ArchiveSharedDrive, nullEmpty(settings.ArchiveSharedDriveID), nullEmpty(settings.ArchiveFileName), nullEmpty(settings.YouTubeOutputID), nullEmpty(settings.EncoderInputURL), now)
	return err
}

func (s MariaDBStreamStore) UpdateStreamEncoderRuntimeSettings(ctx context.Context, id string, audioGainDB float64, overlayProfileID string) (Stream, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Stream{}, ErrNotFound
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stream{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE streams SET updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return Stream{}, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		if err != nil {
			return Stream{}, err
		}
		return Stream{}, ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_settings (stream_id, overlay_profile_id, encoder_audio_gain_db, updated_at)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE overlay_profile_id = VALUES(overlay_profile_id), encoder_audio_gain_db = VALUES(encoder_audio_gain_db), updated_at = VALUES(updated_at)`, id, nullEmpty(overlayProfileID), audioGainDB, now)
	if err != nil {
		return Stream{}, err
	}
	if err := tx.Commit(); err != nil {
		return Stream{}, err
	}
	return s.GetStream(ctx, id)
}

func (s MariaDBStreamStore) UpdateStreamStatus(ctx context.Context, id, status string) (Stream, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE streams SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	if err != nil {
		return Stream{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Stream{}, err
	}
	if affected == 0 {
		return Stream{}, ErrNotFound
	}
	return s.GetStream(ctx, id)
}

func (s MariaDBStreamStore) TransitionStreamStatus(ctx context.Context, id, expectedStatus, status string) (Stream, bool, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE streams
SET status = ?, updated_at = ?
WHERE id = ? AND LOWER(TRIM(status)) = LOWER(TRIM(?))`, status, now, id, expectedStatus)
	if err != nil {
		return Stream{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Stream{}, false, err
	}
	stream, err := s.GetStream(ctx, id)
	if err != nil {
		return Stream{}, false, err
	}
	return stream, affected > 0, nil
}
