package updateagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validRemotePlan() RemotePlan {
	plan := RemotePlan{JobID: "job-01", HostID: "edge-01", TargetID: "worker-01", ServiceType: "worker", DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0", ConfigSHA256: "sha256:" + strings.Repeat("f", 64), TargetVersion: "v1.1.0", LeaseGeneration: 2, ArtifactDigest: strings.Repeat("a", 64), ExpectedVersion: "v1.1.0", SessionID: "session-00000001"}
	plan.PlanSHA256, _ = plan.ComputePlanSHA256()
	return plan
}

func TestRemoteRPCMutationRoundTripAndSecretRedaction(t *testing.T) {
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: ptrRemotePlan(validRemotePlan()), MutationGrant: NewRemoteSecret("mutation-secret")}
	var payload bytes.Buffer
	if err := EncodeRemoteRPCRequest(&payload, request); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRemoteRPCRequest(bytes.NewReader(payload.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Plan == nil || decoded.Plan.TargetID != "worker-01" || decoded.MutationGrant.Reveal() != "mutation-secret" || !decoded.ReleaseToken.Empty() {
		t.Fatalf("decoded request = %#v", decoded)
	}
	formatted := fmt.Sprintf("%v %#v %s %q %d %x %d", request, request, request.MutationGrant, request.ReleaseToken, request.MutationGrant, request.ReleaseToken, request)
	if strings.Contains(formatted, "mutation-secret") || !strings.Contains(formatted, "REDACTED") {
		t.Fatalf("formatted request exposed a credential: %s", formatted)
	}
	structured, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(structured), "mutation-secret") || !strings.Contains(string(structured), "REDACTED") {
		t.Fatalf("structured request exposed a credential: %s", structured)
	}
	stageRequest := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "stage", Plan: ptrRemotePlan(validRemotePlan()), ReleaseToken: NewRemoteSecret("release-secret")}
	payload.Reset()
	if err := EncodeRemoteRPCRequest(&payload, stageRequest); err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeRemoteRPCRequest(bytes.NewReader(payload.Bytes()))
	if err != nil || decoded.ReleaseToken.Reveal() != "release-secret" || !decoded.MutationGrant.Empty() {
		t.Fatalf("decoded stage request = %#v, %v", decoded, err)
	}
}

func TestRemoteRPCDecoderIsBoundedStrictAndDoesNotEchoSecrets(t *testing.T) {
	secret := "must-never-appear-in-errors"
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{name: "unknown", body: []byte(`{"version":1,"operation":"probe","release_token":"` + secret + `","unknown":true}`), want: "decode"},
		{name: "trailing", body: []byte(`{"version":1,"operation":"probe"} {"release_token":"` + secret + `"}`), want: "trailing"},
		{name: "oversized", body: bytes.Repeat([]byte("x"), RemoteProtocolMaxFrameBytes+1), want: "size limit"},
		{name: "malformed", body: []byte(`{"version":1,"operation":"apply","release_token":"` + secret), want: "decode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeRemoteRPCRequest(bytes.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decode result = %v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("decoder error exposed secret: %v", err)
			}
		})
	}
}

func TestRemoteFrameLimitIncludesDelimiter(t *testing.T) {
	var payload bytes.Buffer
	if err := encodeRemoteFrame(&payload, strings.Repeat("x", RemoteProtocolMaxFrameBytes-3), "test"); err != nil {
		t.Fatalf("exact-size frame rejected: %v", err)
	}
	if payload.Len() != RemoteProtocolMaxFrameBytes {
		t.Fatalf("frame size = %d", payload.Len())
	}
	payload.Reset()
	if err := encodeRemoteFrame(&payload, strings.Repeat("x", RemoteProtocolMaxFrameBytes-2), "test"); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("delimiter overflow result = %v", err)
	}
}

func TestRemoteRPCOperationContracts(t *testing.T) {
	if err := (RemoteRPCRequest{Version: 1, Operation: "probe", ReleaseToken: NewRemoteSecret("secret")}).Validate(); err == nil || !strings.Contains(err.Error(), "must not include") {
		t.Fatalf("credential-bearing probe result = %v", err)
	}
	if err := (RemoteRPCRequest{Version: 1, Operation: "apply", Plan: ptrRemotePlan(validRemotePlan())}).Validate(); err == nil || !strings.Contains(err.Error(), "mutation credential") {
		t.Fatalf("credential-free mutation result = %v", err)
	}
	plan := validRemotePlan()
	plan.LeaseGeneration = 0
	if err := (RemoteRPCRequest{Version: 1, Operation: "reconcile", Plan: &plan, MutationGrant: NewRemoteSecret("grant")}).Validate(); err == nil || !strings.Contains(err.Error(), "lease generation") {
		t.Fatalf("unfenced mutation result = %v", err)
	}
	if err := (RemoteRPCRequest{Version: 2, Operation: "probe"}).Validate(); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("future protocol result = %v", err)
	}
	if err := (RemoteRPCRequest{Version: 1, Operation: "apply", Plan: ptrRemotePlan(validRemotePlan()), MutationGrant: NewRemoteSecret("grant"), ReleaseToken: NewRemoteSecret("release")}).Validate(); err == nil || !strings.Contains(err.Error(), "only") {
		t.Fatalf("release-bearing apply result = %v", err)
	}
	if err := (RemoteRPCRequest{Version: 1, Operation: "stage", Plan: ptrRemotePlan(validRemotePlan()), ReleaseToken: NewRemoteSecret("release\nheader")}).Validate(); err == nil || !strings.Contains(err.Error(), "release credential") {
		t.Fatalf("control-bearing release token result = %v", err)
	}
}

func TestRemotePlanBindsCanonicalMutationDigestAndModeMetadata(t *testing.T) {
	plan := validRemotePlan()
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan: %v", err)
	}
	if got, err := MutationPlanSHA256(plan.ApplyPlan()); err != nil || got != plan.PlanSHA256 {
		t.Fatalf("canonical digest = %q, %v; want %q", got, err, plan.PlanSHA256)
	}
	tampered := plan
	tampered.TargetVersion = "v1.1.1"
	tampered.ExpectedVersion = "v1.1.1"
	if err := tampered.Validate(); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered plan result = %v", err)
	}
	tampered = plan
	tampered.ConfigSHA256 = "sha256:" + strings.Repeat("0", 64)
	if err := tampered.Validate(); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("helper config tamper result = %v", err)
	}
	missingBaseline := plan
	missingBaseline.CurrentVersion = ""
	missingBaseline.PlanSHA256, _ = missingBaseline.ComputePlanSHA256()
	if err := missingBaseline.Validate(); err == nil || !strings.Contains(err.Error(), "current version") {
		t.Fatalf("unknown current version result = %v", err)
	}
	tampered = plan
	tampered.ExpectedImageDigest = "sha256:" + strings.Repeat("b", 64)
	tampered.PlanSHA256, _ = tampered.ComputePlanSHA256()
	if err := tampered.Validate(); err == nil || !strings.Contains(err.Error(), "systemd plan") {
		t.Fatalf("mixed-mode plan result = %v", err)
	}
	docker := plan
	docker.DeploymentMode = ModeDocker
	docker.ArtifactDigest = strings.Repeat("c", 64)
	docker.ExpectedVersion = "v1.1.0"
	docker.ExpectedImageDigest = "sha256:" + strings.Repeat("d", 64)
	docker.ExpectedPlatformDigest = "sha256:" + strings.Repeat("e", 64)
	docker.PlanSHA256, _ = docker.ComputePlanSHA256()
	if err := docker.Validate(); err != nil {
		t.Fatalf("valid Docker plan: %v", err)
	}
}

func TestRemoteRPCResponseRequiresOneStrictOutcome(t *testing.T) {
	probe := RemoteProbeResult{ProtocolVersion: 1, HelperVersion: "v1.0.0", HostID: "edge-01", OS: "linux", Arch: "amd64", ConfigSHA256: "sha256:" + strings.Repeat("a", 64), Targets: []RemoteProbeTarget{{TargetID: "worker-01", ServiceType: "worker", DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0"}}}
	response := RemoteRPCResponse{Version: 1, Probe: &probe}
	var payload bytes.Buffer
	if err := EncodeRemoteRPCResponse(&payload, response); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRemoteRPCResponse(bytes.NewReader(payload.Bytes()))
	if err != nil || decoded.Probe == nil || decoded.Probe.HostID != "edge-01" {
		t.Fatalf("probe response = %#v, %v", decoded, err)
	}
	result := ApplyResult{Status: "succeeded"}
	resultBinding := RemoteRPCResponse{Version: 1, Result: &result, SessionID: "session-00000001", PlanSHA256: strings.Repeat("a", 64)}
	if err := resultBinding.Validate(); err != nil {
		t.Fatalf("bound result response = %v", err)
	}
	if err := (RemoteRPCResponse{Version: 1, Probe: &probe, Result: &result, SessionID: "session-00000001", PlanSHA256: strings.Repeat("a", 64)}).Validate(); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("ambiguous response result = %v", err)
	}
	if err := (RemoteRPCResponse{Version: 1, Error: &RemoteRPCFailure{Code: "BAD-CODE", Message: "failure"}}).Validate(); err == nil || !strings.Contains(err.Error(), "failure is invalid") {
		t.Fatalf("unsafe error response result = %v", err)
	}
	if err := (RemoteRPCResponse{Version: 1, Error: &RemoteRPCFailure{Code: "plausible_but_unlisted", Message: "failure"}}).Validate(); err == nil || !strings.Contains(err.Error(), "failure is invalid") {
		t.Fatalf("unlisted error response result = %v", err)
	}
	if err := (RemoteRPCResponse{Version: 1, Error: &RemoteRPCFailure{Code: "remote_failed", Message: "forged\nentry"}}).Validate(); err == nil || !strings.Contains(err.Error(), "failure is invalid") {
		t.Fatalf("multiline error response result = %v", err)
	}
}

func TestRemoteProbeRequiresCompleteBoundedPolicyIdentity(t *testing.T) {
	probe := RemoteProbeResult{ProtocolVersion: 1, HelperVersion: "v1.0.0", HostID: "edge-01", OS: "linux", Arch: "amd64", ConfigSHA256: "sha256:" + strings.Repeat("a", 64), Targets: []RemoteProbeTarget{{TargetID: "worker-01", ServiceType: "worker", DeploymentMode: ModeSystemd}}}
	if err := probe.Validate(); err != nil {
		t.Fatalf("valid probe: %v", err)
	}
	probe.ConfigSHA256 = ""
	if err := probe.Validate(); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("missing config digest result = %v", err)
	}
	probe.ConfigSHA256 = "sha256:" + strings.Repeat("a", 64)
	probe.Targets[0].ServiceType = "arbitrary"
	if err := probe.Validate(); err == nil || !strings.Contains(err.Error(), "service type") {
		t.Fatalf("invalid service type result = %v", err)
	}
}

func ptrRemotePlan(plan RemotePlan) *RemotePlan { return &plan }
