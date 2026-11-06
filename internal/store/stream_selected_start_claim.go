package store

import (
	"fmt"
	"strings"
)

// Capacity conflicts remain assignment conflicts for existing callers, with
// a distinct code so VC presence can retry after the current run releases it.
var ErrNodeExecutionBusy = fmt.Errorf("node execution busy: %w", ErrServiceAssignmentConflict)

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

// Service identities are audited on the request, not stored in the CHAR(36)
// user-reference column. A registered Node ID can be longer than a user UUID.
func startClaimUserID(actorID string) string {
	actorID = strings.TrimSpace(actorID)
	if strings.HasPrefix(actorID, "service:") {
		return ""
	}
	return actorID
}
