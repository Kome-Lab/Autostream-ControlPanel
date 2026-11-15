package v3authorization

import (
	"context"
	"testing"
)

func TestIUWS1ExplicitCompositionRequired(t *testing.T) {
	if service, err := NewApprovalManagementService(nil, nil); err == nil || service != nil {
		t.Fatal("missing isolated core/authority became a configured approval path")
	}
	if NewManagementService(nil, nil).Bootstrap(context.Background(), ManagementActor{}, ManagementMetadata{}, ManagementGrant{}).Status == ManagementApplied {
		t.Fatal("legacy nil composition issued management authority")
	}
}
