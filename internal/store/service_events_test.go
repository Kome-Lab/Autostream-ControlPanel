package store

import "testing"

func TestWriteStreamEventRestrictsTypeAndRedactsPayload(t *testing.T) {
	st := NewMemoryAuthStore()
	token, err := st.CreateServiceToken(t.Context(), "worker", []string{"service.register", "service.heartbeat", "service.status.write"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PrecreateService(t.Context(), token, ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker",
		PublicURL:   "https://worker.example.com",
	}); err != nil {
		t.Fatal(err)
	}
	service, err := st.RegisterService(t.Context(), token, ServiceRegistration{
		ServiceID:   "worker-01",
		ServiceType: "worker",
		ServiceName: "Worker",
		PublicURL:   "https://worker.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AssignServiceToStream(t.Context(), service.ServiceID, "stream-01", "user-01"); err != nil {
		t.Fatal(err)
	}

	err = st.WriteStreamEvent(t.Context(), token, ServiceStreamEvent{
		ServiceID: service.ServiceID,
		StreamID:  "stream-01",
		EventType: "discord.voice_connected",
		Payload:   map[string]any{"ok": true},
	})
	if err != ErrInvalidServiceStreamEvent {
		t.Fatalf("expected invalid worker event type to be rejected, got %v", err)
	}

	err = st.WriteStreamEvent(t.Context(), token, ServiceStreamEvent{
		ServiceID: service.ServiceID,
		StreamID:  "stream-01",
		EventType: "worker.overlay",
		Payload: map[string]any{
			"ok":          true,
			"webhook_url": "https://discord.com/api/webhooks/123456789012345678/raw-secret-token",
			"endpoint":    "rtsp://user:password@camera.example.com/live",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.streamEvents) != 1 {
		t.Fatalf("expected one event, got %d", len(st.streamEvents))
	}
	payload := st.streamEvents[0].Payload
	if _, ok := payload["webhook_url"]; ok {
		t.Fatalf("secret key should have been removed: %#v", payload)
	}
	if payload["endpoint"] != "<redacted>" {
		t.Fatalf("credential URL should have been redacted: %#v", payload)
	}
}
