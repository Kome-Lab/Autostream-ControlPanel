package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Scalar lookups keep every existing stream scanner and lock query on the same
// projection without changing its join or lock cardinality.
const streamNodeSelectionSelectFields = `COALESCE((SELECT selected.service_id FROM stream_node_selections selected WHERE selected.stream_id=s.id AND selected.service_type='encoder_recorder'), ''),
  COALESCE((SELECT selected.service_id FROM stream_node_selections selected WHERE selected.stream_id=s.id AND selected.service_type='worker'), ''),
  COALESCE((SELECT selected.service_id FROM stream_node_selections selected WHERE selected.stream_id=s.id AND selected.service_type='discord_bot'), '')`

func scanStreamNodeSelections(row streamRowScanner) (StreamNodeSelections, error) {
	var selected StreamNodeSelections
	err := row.Scan(&selected.EncoderServiceID, &selected.WorkerServiceID, &selected.DiscordServiceID)
	if errors.Is(err, sql.ErrNoRows) {
		return StreamNodeSelections{}, ErrNotFound
	}
	return selected, err
}

func (s MariaDBStreamStore) GetStreamNodeSelections(ctx context.Context, streamID string) (StreamNodeSelections, error) {
	return scanStreamNodeSelections(s.db.QueryRowContext(ctx, `SELECT `+streamNodeSelectionSelectFields+`
FROM streams s WHERE s.id=? AND s.deleted_at IS NULL`, strings.TrimSpace(streamID)))
}

func streamNodeSelectionsTx(ctx context.Context, tx *sql.Tx, streamID string) (StreamNodeSelections, error) {
	return scanStreamNodeSelections(tx.QueryRowContext(ctx, `SELECT `+streamNodeSelectionSelectFields+`
FROM streams s WHERE s.id=? AND s.deleted_at IS NULL`, streamID))
}

func (s MariaDBStreamStore) ListStreamNodeSelectionsForService(ctx context.Context, serviceID string) ([]StreamNodeSelectionBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.stream_id,n.service_id,n.service_type,COALESCE(ss.updated_at,n.updated_at)
FROM stream_node_selections n
JOIN streams s ON s.id=n.stream_id AND s.deleted_at IS NULL
LEFT JOIN stream_settings ss ON ss.stream_id=s.id
WHERE n.service_id=?
ORDER BY n.stream_id,n.service_type`, strings.TrimSpace(serviceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bindings []StreamNodeSelectionBinding
	for rows.Next() {
		var binding StreamNodeSelectionBinding
		if err := rows.Scan(&binding.StreamID, &binding.ServiceID, &binding.ServiceType, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

// The stream row is held before services and profiles. No current owner or
// assignment row is changed while validating or saving these preferences.
func validateStreamNodeSelectionsTx(ctx context.Context, tx *sql.Tx, streamID string, settings StreamSettings) error {
	if !streamNodeSelectionsChanged(settings) {
		return nil
	}
	selected, err := streamNodeSelectionsTx(ctx, tx, streamID)
	if err != nil {
		return err
	}
	selected = mergeStreamNodeSelections(selected, settings.NodeSelections)
	if err := validateExpectedDiscordNodeSelection(settings, selected); err != nil {
		return err
	}
	bindings := selected.bindings(streamID, time.Time{})
	serviceIDs := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		serviceIDs = append(serviceIDs, binding.ServiceID)
	}
	services, err := lockMariaDBServicesSorted(ctx, tx, serviceIDs)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		service, exists := services[binding.ServiceID]
		if !exists || service.ServiceType != binding.ServiceType {
			return ErrInvalidServiceAssignment
		}
	}
	if selected.DiscordServiceID != "" {
		profile, err := scanProfile(tx.QueryRowContext(ctx, `SELECT id,kind,name,config,created_at,updated_at,youtube_relay_binding_revision FROM profiles WHERE id=? AND kind=? FOR UPDATE`, strings.TrimSpace(settings.DiscordConfigID), string(ProfileDiscordConfig)))
		if err != nil || currentDiscordConfigString(profile.Config, "service_id") != selected.DiscordServiceID {
			return ErrServiceAssignmentConflict
		}
	}
	return ctx.Err()
}

func saveStreamNodeSelectionUpdateTx(ctx context.Context, tx *sql.Tx, streamID string, update *StreamNodeSelectionUpdate, now time.Time) error {
	if update == nil {
		return nil
	}
	current, err := streamNodeSelectionsTx(ctx, tx, streamID)
	if err != nil {
		return err
	}
	return saveStreamNodeSelectionsTx(ctx, tx, streamID, mergeStreamNodeSelections(current, update), now)
}

func saveStreamNodeSelectionsTx(ctx context.Context, tx *sql.Tx, streamID string, selected StreamNodeSelections, now time.Time) error {
	for _, selection := range []struct {
		serviceType string
		serviceID   string
	}{
		{"discord_bot", selected.DiscordServiceID},
		{"encoder_recorder", selected.EncoderServiceID},
		{"worker", selected.WorkerServiceID},
	} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_node_selections(stream_id,service_type,service_id,created_at,updated_at)
VALUES(?,?,?,?,?)
ON DUPLICATE KEY UPDATE service_id=VALUES(service_id),updated_at=VALUES(updated_at)`, streamID, selection.serviceType, selection.serviceID, now, now); err != nil {
			return err
		}
	}
	return nil
}
