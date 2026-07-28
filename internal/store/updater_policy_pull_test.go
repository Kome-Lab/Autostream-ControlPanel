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
