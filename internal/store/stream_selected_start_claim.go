package store

import (
	"strings"
)

func validateSelectedStartClaim(request StreamStartClaimRequest, stream Stream) (map[string]RegisteredService, error) {
	if request.ExpectedNodeSelections == nil || streamNodeSelections(stream) != *request.ExpectedNodeSelections {
		return nil, ErrServiceAssignmentConflict
	}
	expected, err := expectedPrimaryStartAssignments(request.ExpectedPrimaryAssignments)
	if err != nil || len(expected) != 3 {
		return nil, ErrServiceAssignmentConflict
	}
	for _, item := range []struct{ serviceType, serviceID string }{
		{"encoder_recorder", stream.SelectedEncoderID},
		{"worker", stream.SelectedWorkerID},
		{"discord_bot", stream.SelectedDiscordID},
	} {
		service, ok := expected[item.serviceType]
		if !ok || strings.TrimSpace(item.serviceID) == "" || service.ServiceID != strings.TrimSpace(item.serviceID) {
			return nil, ErrServiceAssignmentConflict
		}
	}
	return expected, nil
}
