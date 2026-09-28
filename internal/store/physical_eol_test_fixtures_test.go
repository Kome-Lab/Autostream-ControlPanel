package store

import "testing"

func physicalEOLPullAgentRegistration(serviceID string) ServiceRegistration {
	return ServiceRegistration{
		ServiceID: serviceID, ServiceType: "update_agent", ServiceName: serviceID,
		TransportMode: SystemUpdateTransportPullV2, ExecutionHostID: "host-" + serviceID,
		Capabilities: map[string]any{},
	}
}

func newPhysicalEOLOwnedUpdateStore(t *testing.T, owners map[string]string) *MemorySystemUpdateStore {
	t.Helper()
	updates := NewMemorySystemUpdateStore()
	for hostID, agentID := range owners {
		if _, err := updates.SwitchSystemUpdateExecutionHost(
			t.Context(), hostID, 0, SystemUpdateTransportPullV2, agentID, 7,
		); err != nil {
			t.Fatal(err)
		}
	}
	return updates
}
