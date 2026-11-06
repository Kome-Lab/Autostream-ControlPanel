package store

import (
	"context"
	"strings"
	"time"
)

// StreamNodeSelections are saved preferences, not execution ownership.
type StreamNodeSelections struct {
	EncoderServiceID string
	WorkerServiceID  string
	DiscordServiceID string
}

// StreamNodeSelectionUpdate distinguishes an omitted preference from a clear.
// It is populated by the Panel after validating its public request.
type StreamNodeSelectionUpdate struct {
	EncoderServiceID *string `json:"-"`
	WorkerServiceID  *string `json:"-"`
	DiscordServiceID *string `json:"-"`
}

type StreamNodeSelectionBinding struct {
	StreamID    string
	ServiceID   string
	ServiceType string
	// UpdatedAt identifies the settings snapshot, independently of heartbeats.
	UpdatedAt time.Time
}

type StreamNodeSelectionStore interface {
	GetStreamNodeSelections(context.Context, string) (StreamNodeSelections, error)
	ListStreamNodeSelectionsForService(context.Context, string) ([]StreamNodeSelectionBinding, error)
}

func streamNodeSelections(stream Stream) StreamNodeSelections {
	return StreamNodeSelections{
		EncoderServiceID: strings.TrimSpace(stream.SelectedEncoderID),
		WorkerServiceID:  strings.TrimSpace(stream.SelectedWorkerID),
		DiscordServiceID: strings.TrimSpace(stream.SelectedDiscordID),
	}
}

func mergeStreamNodeSelections(current StreamNodeSelections, update *StreamNodeSelectionUpdate) StreamNodeSelections {
	if update == nil {
		return current
	}
	if update.EncoderServiceID != nil {
		current.EncoderServiceID = strings.TrimSpace(*update.EncoderServiceID)
	}
	if update.WorkerServiceID != nil {
		current.WorkerServiceID = strings.TrimSpace(*update.WorkerServiceID)
	}
	if update.DiscordServiceID != nil {
		current.DiscordServiceID = strings.TrimSpace(*update.DiscordServiceID)
	}
	return current
}

func applyStreamNodeSelections(stream *Stream, selected StreamNodeSelections) {
	stream.NodeSelectionsConfigured = true
	stream.SelectedEncoderID = selected.EncoderServiceID
	stream.SelectedWorkerID = selected.WorkerServiceID
	stream.SelectedDiscordID = selected.DiscordServiceID
}

func (s StreamNodeSelections) bindings(streamID string, updatedAt time.Time) []StreamNodeSelectionBinding {
	var result []StreamNodeSelectionBinding
	for _, selection := range []struct {
		serviceType string
		serviceID   string
	}{
		{"encoder_recorder", s.EncoderServiceID},
		{"worker", s.WorkerServiceID},
		{"discord_bot", s.DiscordServiceID},
	} {
		if id := strings.TrimSpace(selection.serviceID); id != "" {
			result = append(result, StreamNodeSelectionBinding{
				StreamID: streamID, ServiceID: id,
				ServiceType: selection.serviceType, UpdatedAt: updatedAt,
			})
		}
	}
	return result
}

func streamNodeSelectionsChanged(settings StreamSettings) bool {
	return settings.NodeSelections != nil || settings.ExpectedDiscordServiceID != nil
}

func validateExpectedDiscordNodeSelection(settings StreamSettings, selected StreamNodeSelections) error {
	if settings.ExpectedDiscordServiceID != nil &&
		strings.TrimSpace(*settings.ExpectedDiscordServiceID) != selected.DiscordServiceID {
		return ErrServiceAssignmentConflict
	}
	if (selected.DiscordServiceID == "") != (strings.TrimSpace(settings.DiscordConfigID) == "") {
		return ErrServiceAssignmentConflict
	}
	return nil
}
