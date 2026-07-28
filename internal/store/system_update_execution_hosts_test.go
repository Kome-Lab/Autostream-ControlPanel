package store

import (
	"errors"
	"sync"
	"testing"
)

func TestMemoryExecutionHostOwnershipUsesSyntheticLegacyDefaultAndCASFencing(t *testing.T) {
	ctx := t.Context()
	updates := NewMemorySystemUpdateStore()

	missing, err := updates.GetSystemUpdateExecutionHost(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if missing.ExecutionHostID != "host-a" ||
		missing.TransportMode != SystemUpdateTransportSSHV1 ||
		missing.AgentServiceID != "" ||
		missing.OwnershipEpoch != 0 ||
		missing.PolicyRevision != 0 {
		t.Fatalf("missing host ownership = %#v", missing)
	}

	first, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"updater-host-a",
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.OwnershipEpoch != 1 ||
		first.TransportMode != SystemUpdateTransportPullV2 ||
		first.AgentServiceID != "updater-host-a" ||
		first.LegacyAgentServiceID != "" ||
		first.PolicyRevision != 7 {
		t.Fatalf("first ownership = %#v", first)
	}

	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportSSHV1,
		"updater-central",
		8,
	); !errors.Is(err, ErrSystemUpdateExecutionHostStale) {
		t.Fatalf("stale switch err = %v", err)
	}

	second, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		1,
		SystemUpdateTransportSSHV1,
		"updater-central",
		8,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.OwnershipEpoch != 2 ||
		second.TransportMode != SystemUpdateTransportSSHV1 ||
		second.AgentServiceID != "updater-central" ||
		second.LegacyAgentServiceID != "updater-central" ||
		second.PolicyRevision != 8 {
		t.Fatalf("second ownership = %#v", second)
	}
}

func TestMemoryExecutionHostOwnershipRejectsEveryNonterminalJobOnHost(t *testing.T) {
	ctx := t.Context()
	updates := NewMemorySystemUpdateStore()
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"updater-host-a",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}

	job, created, err := updates.CreateSystemUpdateJob(ctx, CreateSystemUpdateJobParams{
		TargetID:          "worker-a",
		TargetServiceType: "worker",
		AgentServiceID:    "updater-host-a",
		ExecutionHostID:   "host-a",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "ownership-busy-host-a",
		RequestedByUserID: "admin",
	})
	if err != nil || !created {
		t.Fatalf("create job: created=%v err=%v", created, err)
	}

	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		ownership.OwnershipEpoch,
		SystemUpdateTransportSSHV1,
		"updater-central",
		2,
	); !errors.Is(err, ErrSystemUpdateExecutionHostBusy) {
		t.Fatalf("switch with queued job err = %v", err)
	}

	if _, err := updates.CancelSystemUpdateJob(ctx, job.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	switched, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		ownership.OwnershipEpoch,
		SystemUpdateTransportSSHV1,
		"updater-central",
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if switched.OwnershipEpoch != ownership.OwnershipEpoch+1 {
		t.Fatalf("ownership epoch = %d", switched.OwnershipEpoch)
	}
}

func TestMemoryExecutionHostOwnershipCASAllowsOneConcurrentSwitch(t *testing.T) {
	ctx := t.Context()
	updates := NewMemorySystemUpdateStore()
	const contenders = 16
	start := make(chan struct{})
	results := make(chan error, contenders)
	var ready sync.WaitGroup
	ready.Add(contenders)
	for range contenders {
		go func() {
			ready.Done()
			<-start
			_, err := updates.SwitchSystemUpdateExecutionHost(
				ctx,
				"host-cas",
				0,
				SystemUpdateTransportPullV2,
				"updater-host-cas",
				1,
			)
			results <- err
		}()
	}
	ready.Wait()
	close(start)

	succeeded, stale := 0, 0
	for range contenders {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrSystemUpdateExecutionHostStale):
			stale++
		default:
			t.Fatalf("concurrent switch err = %v", err)
		}
	}
	if succeeded != 1 || stale != contenders-1 {
		t.Fatalf("concurrent switch results: succeeded=%d stale=%d", succeeded, stale)
	}
	ownership, err := updates.GetSystemUpdateExecutionHost(ctx, "host-cas")
	if err != nil {
		t.Fatal(err)
	}
	if ownership.OwnershipEpoch != 1 {
		t.Fatalf("ownership epoch = %d", ownership.OwnershipEpoch)
	}
}

func TestMemoryServicePortReservationIsExactIdempotentAndReleasable(t *testing.T) {
	ctx := t.Context()
	updates := NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-a",
		0,
		SystemUpdateTransportPullV2,
		"updater-host-a",
		1,
	); err != nil {
		t.Fatal(err)
	}
	reservation := ServicePortReservation{
		ExecutionHostID:  "host-a",
		NetworkNamespace: "host",
		Protocol:         "tcp",
		Port:             18080,
		ServiceID:        "worker-a",
		ServiceRole:      "api",
	}

	first, created, err := updates.ReserveServicePort(ctx, reservation)
	if err != nil || !created {
		t.Fatalf("first reserve: reservation=%#v created=%v err=%v", first, created, err)
	}
	replayed, created, err := updates.ReserveServicePort(ctx, reservation)
	if err != nil || created || replayed != first {
		t.Fatalf("idempotent reserve: reservation=%#v created=%v err=%v", replayed, created, err)
	}

	conflict := reservation
	conflict.ServiceID = "worker-b"
	if _, _, err := updates.ReserveServicePort(ctx, conflict); !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("conflicting service err = %v", err)
	}
	conflict = reservation
	conflict.ServiceRole = "health"
	if _, _, err := updates.ReserveServicePort(ctx, conflict); !errors.Is(err, ErrServicePortReserved) {
		t.Fatalf("conflicting role err = %v", err)
	}

	secondNamespace := reservation
	secondNamespace.NetworkNamespace = "container.worker-a"
	if _, created, err := updates.ReserveServicePort(ctx, secondNamespace); err != nil || !created {
		t.Fatalf("same port in another namespace: created=%v err=%v", created, err)
	}
	udp := reservation
	udp.Protocol = "udp"
	if _, created, err := updates.ReserveServicePort(ctx, udp); err != nil || !created {
		t.Fatalf("same port for udp: created=%v err=%v", created, err)
	}

	list, err := updates.ListServicePortReservations(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("list length = %d, reservations=%#v", len(list), list)
	}

	if err := updates.ReleaseServicePort(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if err := updates.ReleaseServicePort(ctx, reservation); err != nil {
		t.Fatalf("idempotent release err = %v", err)
	}
	if _, created, err := updates.ReserveServicePort(ctx, conflict); err != nil || !created {
		t.Fatalf("reserve after release: created=%v err=%v", created, err)
	}
}

func TestMemoryServicePortReservationValidatesBoundaryAndNamespace(t *testing.T) {
	valid := ServicePortReservation{
		ExecutionHostID:  "host-a",
		NetworkNamespace: "host",
		Protocol:         "tcp",
		Port:             1024,
		ServiceID:        "worker-a",
		ServiceRole:      "api",
	}
	tests := []ServicePortReservation{
		func() ServicePortReservation { value := valid; value.ExecutionHostID = ""; return value }(),
		func() ServicePortReservation { value := valid; value.NetworkNamespace = "Host"; return value }(),
		func() ServicePortReservation { value := valid; value.NetworkNamespace = "bad namespace"; return value }(),
		func() ServicePortReservation { value := valid; value.Protocol = "sctp"; return value }(),
		func() ServicePortReservation { value := valid; value.Port = 1023; return value }(),
		func() ServicePortReservation { value := valid; value.Port = 65536; return value }(),
		func() ServicePortReservation { value := valid; value.ServiceID = ""; return value }(),
		func() ServicePortReservation { value := valid; value.ServiceRole = ""; return value }(),
	}
	for _, reservation := range tests {
		updates := NewMemorySystemUpdateStore()
		if _, _, err := updates.ReserveServicePort(t.Context(), reservation); !errors.Is(err, ErrInvalidServicePortReservation) {
			t.Errorf("reservation %#v err = %v", reservation, err)
		}
	}

	valid.Port = 65535
	updates := NewMemorySystemUpdateStore()
	if _, err := updates.SwitchSystemUpdateExecutionHost(
		t.Context(),
		valid.ExecutionHostID,
		0,
		SystemUpdateTransportPullV2,
		"updater-host-a",
		1,
	); err != nil {
		t.Fatal(err)
	}
	if _, created, err := updates.ReserveServicePort(t.Context(), valid); err != nil || !created {
		t.Fatalf("upper boundary reserve: created=%v err=%v", created, err)
	}
}
