package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemUpdatePublicTargetOmitsEmptyHostID(t *testing.T) {
	payload, err := json.Marshal(systemUpdateTargetResponse{TargetID: "unassigned", ServiceType: "worker", Name: "Unassigned"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"host_id"`) {
		t.Fatalf("empty host_id violates public schema: %s", payload)
	}
}
