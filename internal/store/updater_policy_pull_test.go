package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizePullUpdaterPolicyNeedsNoSSHOrInboundAPI(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	normalized, err := NormalizeUpdaterPolicy("host-agent-a", UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: digest,
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	})
	if err != nil {
		t.Fatalf("NormalizeUpdaterPolicy: %v", err)
	}
	if normalized.TransportMode != SystemUpdateTransportPullV2 ||
		normalized.ExecutionHostID != "host-a" ||
		normalized.LocalExecutorPolicySHA256 != digest {
		t.Fatalf("pull binding = %#v", normalized)
	}
	if normalized.API != (UpdaterPolicyAPI{}) || len(normalized.Hosts) != 0 {
		t.Fatalf("pull policy retained inbound/SSH settings: api=%#v hosts=%#v", normalized.API, normalized.Hosts)
	}
	if len(normalized.Targets) != 1 || normalized.Targets[0].HostID != "host-a" {
		t.Fatalf("pull target host was not server-bound: %#v", normalized.Targets)
	}
}

func TestPullUpdaterPolicyTargetLocalListenPortResolution(t *testing.T) {
	t.Parallel()

	publicService := RegisteredService{
		AppliedEndpoint: &ServiceEndpoint{
			Host:       "observability.example.com",
			Port:       443,
			SSLEnabled: true,
			PublicURL:  "https://observability.example.com",
		},
	}
	legacyService := RegisteredService{
		AppliedEndpoint: &ServiceEndpoint{
			Host:      "127.0.0.1",
			Port:      18082,
			PublicURL: "http://127.0.0.1:18082",
		},
	}

	tests := []struct {
		name    string
		target  UpdaterPolicyTarget
		service RegisteredService
		want    int
		ok      bool
	}{
		{
			name: "explicit systemd listener wins over public endpoint",
			target: UpdaterPolicyTarget{
				TargetID: "observability-a", ServiceID: "observability-a",
				ServiceType: "observability", DeploymentMode: "systemd",
				LocalListenPort: 18082,
			},
			service: publicService,
			want:    18082,
			ok:      true,
		},
		{
			name: "legacy systemd listener uses applied endpoint port",
			target: UpdaterPolicyTarget{
				TargetID: "worker-a", ServiceID: "worker-a",
				ServiceType: "worker", DeploymentMode: "systemd",
			},
			service: legacyService,
			want:    18082,
			ok:      true,
		},
		{
			name: "invalid explicit listener does not fall back",
			target: UpdaterPolicyTarget{
				TargetID: "worker-a", ServiceID: "worker-a",
				ServiceType: "worker", DeploymentMode: "systemd",
				LocalListenPort: 443,
			},
			service: legacyService,
		},
		{
			name: "docker has no systemd listener",
			target: UpdaterPolicyTarget{
				TargetID: "worker-a", ServiceID: "worker-a",
				ServiceType: "worker", DeploymentMode: "docker",
				LocalListenPort: 18082,
			},
			service: legacyService,
		},
		{
			name: "control panel forbids an explicit override",
			target: UpdaterPolicyTarget{
				TargetID: "control-panel", ServiceID: "control-panel",
				ServiceType: "control_panel", DeploymentMode: "systemd",
				LocalListenPort: 18080,
			},
			service: legacyService,
		},
		{
			name: "control panel uses its synthetic applied listener",
			target: UpdaterPolicyTarget{
				TargetID: "control-panel", ServiceID: "control-panel",
				ServiceType: "control_panel", DeploymentMode: "systemd",
			},
			service: legacyService,
			want:    18082,
			ok:      true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := PullUpdaterPolicyTargetLocalListenPort(test.target, test.service)
			if got != test.want || ok != test.ok {
				t.Fatalf("resolution = (%d, %v), want (%d, %v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestNormalizePullUpdaterPolicyKeepsLocalListenerOutOfPolicyJSON(t *testing.T) {
	t.Parallel()

	policy := UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{{
			TargetID:        "observability-a",
			ServiceID:       "observability-a",
			ServiceType:     "observability",
			DeploymentMode:  "systemd",
			DatabaseName:    "autostream_observability",
			LocalListenPort: 18082,
		}},
	}
	normalized, err := NormalizeUpdaterPolicy("host-agent-a", policy)
	if err != nil {
		t.Fatalf("NormalizeUpdaterPolicy: %v", err)
	}
	if normalized.Targets[0].LocalListenPort != 18082 {
		t.Fatalf("local listener = %d", normalized.Targets[0].LocalListenPort)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "local_listen_port") ||
		strings.Contains(string(encoded), "18082") {
		t.Fatalf("rollback-incompatible local listener leaked into policy_json: %s", encoded)
	}

	legacy := policy
	legacy.Targets = append([]UpdaterPolicyTarget(nil), policy.Targets...)
	legacy.Targets[0].LocalListenPort = 0
	if _, err := NormalizeUpdaterPolicy("host-agent-a", legacy); err != nil {
		t.Fatalf("legacy missing local listener was rejected: %v", err)
	}

	invalid := policy
	invalid.Targets = append([]UpdaterPolicyTarget(nil), policy.Targets...)
	invalid.Targets[0].LocalListenPort = 443
	if _, err := NormalizeUpdaterPolicy("host-agent-a", invalid); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("invalid explicit listener error = %v, want ErrInvalidSettings", err)
	}
}

func TestMemoryPullUpdaterPolicyLocalListenerRoundTrip(t *testing.T) {
	policies := NewMemoryUpdaterPolicyStore()
	input := UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{{
			TargetID:        "worker-a",
			ServiceID:       "worker-a",
			ServiceType:     "worker",
			DeploymentMode:  "systemd",
			LocalListenPort: 18084,
		}},
	}

	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(), NewMemorySystemUpdateStore(), "host-agent-a", 0, 0, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := policies.GetUpdaterPolicy(t.Context(), saved.UpdaterID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Targets[0].LocalListenPort != 18084 {
		t.Fatalf("stored local listener = %#v", loaded.Targets)
	}
}

func TestNormalizePullUpdaterPolicyRejectsLegacyOrUnpinnedAuthority(t *testing.T) {
	t.Parallel()

	base := UpdaterPolicy{
		TransportMode:            SystemUpdateTransportPullV2,
		ExecutionHostID:          "host-a",
		PollIntervalSeconds:      15,
		HeartbeatIntervalSeconds: 30,
		Targets: []UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			ServiceID:      "worker-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	tests := map[string]func(*UpdaterPolicy){
		"inbound API": func(policy *UpdaterPolicy) {
			policy.API.Port = 8090
		},
		"SSH host": func(policy *UpdaterPolicy) {
			policy.Hosts = validUpdaterPolicy().Hosts
		},
		"wrong target host": func(policy *UpdaterPolicy) {
			policy.Targets[0].HostID = "host-b"
		},
		"bad digest": func(policy *UpdaterPolicy) {
			policy.LocalExecutorPolicySHA256 = "sha256:not-a-digest"
		},
		"missing service id": func(policy *UpdaterPolicy) {
			policy.Targets[0].ServiceID = ""
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Targets = append([]UpdaterPolicyTarget(nil), base.Targets...)
			mutate(&candidate)
			if _, err := NormalizeUpdaterPolicy("host-agent-a", candidate); err == nil {
				t.Fatal("unsafe pull policy was accepted")
			}
		})
	}
}

func TestNormalizePullUpdaterPolicyRejectsDuplicateServiceID(t *testing.T) {
	t.Parallel()

	_, err := NormalizeUpdaterPolicy("host-agent-a", UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{
			{
				TargetID:       "worker-primary",
				ServiceID:      "worker-a",
				ServiceType:    "worker",
				DeploymentMode: "systemd",
			},
			{
				TargetID:       "worker-alias",
				ServiceID:      "worker-a",
				ServiceType:    "worker",
				DeploymentMode: "systemd",
			},
		},
	})
	if !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("duplicate pull ServiceID error = %v, want ErrInvalidSettings", err)
	}
}

func TestNormalizePullUpdaterPolicyRequiresOnlyFixedDatabaseBindings(t *testing.T) {
	t.Parallel()

	digest := "sha256:" + strings.Repeat("a", 64)
	base := UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: digest,
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{{
			TargetID:       "control-panel",
			ServiceID:      "control-panel",
			ServiceType:    "control_panel",
			DeploymentMode: "systemd",
			DatabaseName:   "autostream_panel",
		}},
	}
	normalized, err := NormalizeUpdaterPolicy("host-agent-a", base)
	if err != nil {
		t.Fatalf("NormalizeUpdaterPolicy: %v", err)
	}
	if got := normalized.Targets[0].DatabaseName; got != "autostream_panel" {
		t.Fatalf("database binding = %q", got)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "database_name") ||
		strings.Contains(string(encoded), "autostream_panel") {
		t.Fatalf("rollback-incompatible database binding leaked into policy_json: %s", encoded)
	}
	if _, err := decodeUpdaterPolicyRevisions(
		"host-agent-a",
		1,
		1,
		1,
		encoded,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("v1.8.1-compatible policy_json did not decode: %v", err)
	}

	tests := map[string]func(*UpdaterPolicy){
		"missing database": func(policy *UpdaterPolicy) {
			policy.Targets[0].DatabaseName = ""
		},
		"unsafe database": func(policy *UpdaterPolicy) {
			policy.Targets[0].DatabaseName = "--all-databases"
		},
		"docker database": func(policy *UpdaterPolicy) {
			policy.Targets[0].DeploymentMode = "docker"
		},
		"non database service": func(policy *UpdaterPolicy) {
			policy.Targets[0].ServiceType = "worker"
		},
		"forged control panel identity": func(policy *UpdaterPolicy) {
			policy.Targets[0].TargetID = "control-panel-alias"
			policy.Targets[0].ServiceID = "control-panel-alias"
		},
		"docker control panel alias without database": func(policy *UpdaterPolicy) {
			policy.Targets[0].TargetID = "control-panel-alias"
			policy.Targets[0].ServiceID = "control-panel-alias"
			policy.Targets[0].DeploymentMode = "docker"
			policy.Targets[0].DatabaseName = ""
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Targets = append([]UpdaterPolicyTarget(nil), base.Targets...)
			mutate(&candidate)
			if _, err := NormalizeUpdaterPolicy("host-agent-a", candidate); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("error = %v, want ErrInvalidSettings", err)
			}
		})
	}
}

func TestNormalizeLegacyUpdaterPolicyRejectsDatabaseBindings(t *testing.T) {
	t.Parallel()

	policy := validUpdaterPolicy()
	policy.Targets[0].DatabaseName = "must_not_persist"
	if _, err := NormalizeUpdaterPolicy("updater-01", policy); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("legacy database binding error = %v, want ErrInvalidSettings", err)
	}
}

func TestMemoryPullUpdaterPolicyDatabaseBindingChangesAdvanceAllRevisions(t *testing.T) {
	policies := NewMemoryUpdaterPolicyStore()
	updates := NewMemorySystemUpdateStore()
	input := UpdaterPolicy{
		TransportMode:             SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-a",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Targets: []UpdaterPolicyTarget{{
			TargetID:       "observability-a",
			ServiceID:      "observability-a",
			ServiceType:    "observability",
			DeploymentMode: "systemd",
			DatabaseName:   "autostream_o11y",
		}},
	}
	created, err := policies.SavePullUpdaterPolicy(
		t.Context(), updates, "host-agent-a", 0, 0, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	input.Targets[0].DatabaseName = "autostream_o11y_next"
	updated, err := policies.SavePullUpdaterPolicy(
		t.Context(), updates, "host-agent-a", created.Revision, 0, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != created.Revision+1 ||
		updated.ProjectionRevision != updated.Revision ||
		updated.LocalExecutorPolicyRevision != updated.Revision ||
		updated.Targets[0].DatabaseName != "autostream_o11y_next" {
		t.Fatalf("updated database-bound policy = %#v", updated)
	}
	stored, err := policies.GetUpdaterPolicy(t.Context(), updated.UpdaterID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Targets[0].DatabaseName != "autostream_o11y_next" {
		t.Fatalf("stored database binding = %#v", stored.Targets)
	}
}

func TestNormalizeLegacyUpdaterPolicyDefaultsToSSHV1(t *testing.T) {
	t.Parallel()

	normalized, err := NormalizeUpdaterPolicy("updater-01", validUpdaterPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if normalized.TransportMode != SystemUpdateTransportSSHV1 ||
		normalized.ExecutionHostID != "" ||
		normalized.LocalExecutorPolicySHA256 != "" {
		t.Fatalf("legacy defaults changed: %#v", normalized)
	}
}

func TestMemoryBindPullUpdaterConfigurePolicyIsRevisionBoundAndIdempotent(t *testing.T) {
	policies := NewMemoryUpdaterPolicyStore()
	initialDigest := "sha256:" + strings.Repeat("a", 64)
	generatedDigest := "sha256:" + strings.Repeat("b", 64)
	saved, err := policies.SavePullUpdaterPolicy(
		t.Context(),
		NewMemorySystemUpdateStore(),
		"host-agent-a",
		0,
		0,
		UpdaterPolicy{
			TransportMode:             SystemUpdateTransportPullV2,
			ExecutionHostID:           "host-a",
			LocalExecutorPolicySHA256: initialDigest,
			PollIntervalSeconds:       15,
			HeartbeatIntervalSeconds:  30,
			Targets: []UpdaterPolicyTarget{{
				TargetID:       "worker-a",
				ServiceID:      "worker-a",
				ServiceType:    "worker",
				DeploymentMode: "systemd",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	params := BindPullUpdaterConfigurePolicyParams{
		ServiceID:                           saved.UpdaterID,
		ExpectedSourcePolicyRevision:        saved.Revision,
		ExpectedProjectionRevision:          saved.ProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: saved.LocalExecutorPolicyRevision,
		LocalExecutorPolicySHA256:           generatedDigest,
	}
	bound, err := policies.BindPullUpdaterConfigurePolicy(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if bound.LocalExecutorPolicySHA256 != generatedDigest ||
		bound.Revision != saved.Revision ||
		bound.ProjectionRevision != saved.ProjectionRevision ||
		bound.LocalExecutorPolicyRevision != saved.LocalExecutorPolicyRevision {
		t.Fatalf("bound policy = %#v", bound)
	}
	replayed, err := policies.BindPullUpdaterConfigurePolicy(t.Context(), params)
	if err != nil || replayed.LocalExecutorPolicySHA256 != generatedDigest {
		t.Fatalf("idempotent bind = %#v, %v", replayed, err)
	}
	stale := params
	stale.ExpectedProjectionRevision++
	stale.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("c", 64)
	if _, err := policies.BindPullUpdaterConfigurePolicy(t.Context(), stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale bind error = %v", err)
	}
	current, err := policies.GetUpdaterPolicy(t.Context(), saved.UpdaterID)
	if err != nil || current.LocalExecutorPolicySHA256 != generatedDigest {
		t.Fatalf("stale bind mutated policy = %#v, %v", current, err)
	}
}

func TestDecodeStoredLegacyUpdaterPolicyWithoutTransportFields(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(validUpdaterPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(payload, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "transport_mode")
	delete(legacy, "execution_host_id")
	delete(legacy, "local_executor_policy_sha256")
	payload, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeUpdaterPolicy("updater-01", 7, payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("decodeUpdaterPolicy: %v", err)
	}
	if decoded.TransportMode != SystemUpdateTransportSSHV1 ||
		decoded.ExecutionHostID != "" ||
		decoded.LocalExecutorPolicySHA256 != "" ||
		decoded.Revision != 7 {
		t.Fatalf("decoded legacy policy = %#v", decoded)
	}
}
