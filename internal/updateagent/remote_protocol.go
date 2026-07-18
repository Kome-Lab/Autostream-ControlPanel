package updateagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strings"
)

const (
	RemoteProtocolVersion       = 1
	RemoteProtocolMaxFrameBytes = 128 << 10
	RemoteFixedCommand          = "autostream-update-rpc-v1"
)

var (
	remotePlanHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	remoteSessionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$`)
)

func validRemoteFailureCode(code string) bool {
	switch code {
	case "invalid_request", "target_mismatch", "target_unavailable", "target_busy",
		"config_mismatch", "state_unavailable", "state_invalid", "stage_failed", "stage_required",
		"stage_invalid", "plan_conflict", "reconcile_required", "already_terminal",
		"mutation_precondition_failed", "launcher_unavailable", "operation_continues",
		"internal_error":
		return true
	default:
		return false
	}
}

// RemoteSecret is deliberately redacted by fmt. Reveal should only be used at
// the final HTTP/credential boundary; callers must never include it in errors.
type RemoteSecret string

func NewRemoteSecret(value string) RemoteSecret { return RemoteSecret(value) }
func (s RemoteSecret) Reveal() string           { return string(s) }
func (s RemoteSecret) Empty() bool              { return len(s) == 0 }
func (RemoteSecret) String() string             { return "[REDACTED]" }
func (RemoteSecret) GoString() string           { return "updateagent.RemoteSecret([REDACTED])" }
func (RemoteSecret) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[REDACTED]")
}

// MarshalJSON deliberately redacts the credential so structured logging of a
// request cannot serialize bearer secrets. EncodeRemoteRPCRequest uses its own
// private wire DTO at the single audited transport boundary.
func (RemoteSecret) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED]")
}

func (s *RemoteSecret) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("remote secret must be a string")
	}
	if value != "" && !validRemoteSecret(value) {
		return errors.New("remote secret is invalid")
	}
	*s = RemoteSecret(value)
	return nil
}

func validRemoteSecret(value string) bool {
	if len(value) == 0 || len(value) > 16<<10 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// RemotePlan contains only job and target identity. Privileged paths, units,
// image repositories, commands and endpoints are always reloaded from the
// root-owned HelperConfig on the destination host.
type RemotePlan struct {
	JobID                  string `json:"job_id"`
	HostID                 string `json:"host_id"`
	TargetID               string `json:"target_id"`
	ServiceType            string `json:"service_type"`
	DeploymentMode         string `json:"deployment_mode"`
	CurrentVersion         string `json:"current_version"`
	ConfigSHA256           string `json:"config_sha256"`
	TargetVersion          string `json:"target_version"`
	LeaseGeneration        uint64 `json:"lease_generation"`
	ArtifactDigest         string `json:"artifact_digest,omitempty"`
	ExpectedVersion        string `json:"expected_version,omitempty"`
	ExpectedImageDigest    string `json:"expected_image_digest,omitempty"`
	ExpectedPlatformDigest string `json:"expected_platform_digest,omitempty"`
	SessionID              string `json:"session_id"`
	PlanSHA256             string `json:"plan_sha256"`
}

func (p RemotePlan) Validate() error {
	if !identifierPattern.MatchString(p.JobID) || !identifierPattern.MatchString(p.HostID) || !identifierPattern.MatchString(p.TargetID) {
		return errors.New("remote plan contains an invalid identity")
	}
	switch p.ServiceType {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
	default:
		return errors.New("remote plan contains an unsupported service type")
	}
	if p.DeploymentMode != ModeSystemd && p.DeploymentMode != ModeDocker {
		return errors.New("remote plan contains an unsupported deployment mode")
	}
	if !versionPattern.MatchString(strings.TrimSpace(p.TargetVersion)) {
		return errors.New("remote plan contains an invalid target version")
	}
	if current := strings.TrimSpace(p.CurrentVersion); !versionPattern.MatchString(current) || current != p.CurrentVersion {
		return errors.New("remote plan contains an invalid current version")
	}
	if configSHA256 := strings.TrimSpace(strings.ToLower(p.ConfigSHA256)); !digestPattern.MatchString(configSHA256) || configSHA256 != p.ConfigSHA256 {
		return errors.New("remote plan contains an invalid helper config digest")
	}
	if p.LeaseGeneration == 0 {
		return errors.New("remote plan is missing its lease generation")
	}
	if !remoteSessionPattern.MatchString(p.SessionID) || !remotePlanHashPattern.MatchString(p.PlanSHA256) {
		return errors.New("remote plan authorization binding is invalid")
	}
	switch p.DeploymentMode {
	case ModeSystemd:
		if !remotePlanHashPattern.MatchString(p.ArtifactDigest) || p.ExpectedVersion != p.TargetVersion || p.ExpectedImageDigest != "" || p.ExpectedPlatformDigest != "" {
			return errors.New("remote systemd plan release binding is invalid")
		}
	case ModeDocker:
		if !remotePlanHashPattern.MatchString(p.ArtifactDigest) || !versionPattern.MatchString(p.ExpectedVersion) || !digestPattern.MatchString(p.ExpectedImageDigest) || !digestPattern.MatchString(p.ExpectedPlatformDigest) {
			return errors.New("remote Docker plan release binding is invalid")
		}
	}
	computed, err := p.ComputePlanSHA256()
	if err != nil || computed != p.PlanSHA256 {
		return errors.New("remote plan digest does not match its immutable fields")
	}
	return nil
}

// ApplyPlan returns the exact secret-free input consumed by the canonical
// MutationPlanSHA256 function. Root-only paths and the coordinator lease token
// are deliberately absent and must be resolved independently by each side.
func (p RemotePlan) ApplyPlan() ApplyPlan {
	return ApplyPlan{
		JobID: p.JobID, HostID: p.HostID, TargetID: p.TargetID, ServiceType: p.ServiceType,
		DeploymentMode: p.DeploymentMode, TargetVersion: p.TargetVersion, CurrentVersion: p.CurrentVersion,
		ConfigSHA256:    p.ConfigSHA256,
		LeaseGeneration: p.LeaseGeneration, ArtifactDigest: p.ArtifactDigest, ExpectedVersion: p.ExpectedVersion,
		ExpectedImageDigest: p.ExpectedImageDigest, ExpectedPlatformDigest: p.ExpectedPlatformDigest,
	}
}

func (p RemotePlan) ComputePlanSHA256() (string, error) {
	return MutationPlanSHA256(p.ApplyPlan())
}

// ResultArtifactDigest is the canonical requested-target digest carried by
// both succeeded and rolled_back results. PreviousDigest identifies the state
// that was restored when a rollback occurs.
func (p RemotePlan) ResultArtifactDigest() string {
	if p.DeploymentMode == ModeDocker {
		return normalizeDigest(p.ExpectedImageDigest)
	}
	return normalizeDigest(p.ArtifactDigest)
}

type RemoteRPCRequest struct {
	Version       int          `json:"version"`
	Operation     string       `json:"operation"`
	Plan          *RemotePlan  `json:"plan,omitempty"`
	MutationGrant RemoteSecret `json:"mutation_grant,omitempty"`
	ReleaseToken  RemoteSecret `json:"release_token,omitempty"`
}

func (r RemoteRPCRequest) String() string {
	operation := r.Operation
	if operation != "probe" && operation != "stage" && operation != "apply" && operation != "reconcile" {
		operation = "<invalid>"
	}
	target := ""
	if r.Plan != nil {
		target = r.Plan.TargetID
		if !identifierPattern.MatchString(target) {
			target = "<invalid>"
		}
	}
	return fmt.Sprintf("RemoteRPCRequest{version:%d operation:%s target:%s mutation_grant:[REDACTED] release_token:[REDACTED]}", r.Version, operation, target)
}

func (r RemoteRPCRequest) GoString() string { return r.String() }
func (r RemoteRPCRequest) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, r.String())
}

func (r RemoteRPCRequest) Validate() error {
	if r.Version != RemoteProtocolVersion {
		return errors.New("unsupported remote RPC protocol version")
	}
	switch r.Operation {
	case "probe":
		if r.Plan != nil || !r.MutationGrant.Empty() || !r.ReleaseToken.Empty() {
			return errors.New("probe request must not include a plan or credentials")
		}
	case "stage":
		if r.Plan == nil {
			return errors.New("stage request requires a plan")
		}
		if err := r.Plan.Validate(); err != nil {
			return err
		}
		if !r.MutationGrant.Empty() || !validRemoteSecret(r.ReleaseToken.Reveal()) {
			return errors.New("stage request requires only its ephemeral release credential")
		}
	case "apply", "reconcile":
		if r.Plan == nil {
			return errors.New("mutation request requires a plan")
		}
		if err := r.Plan.Validate(); err != nil {
			return err
		}
		if !validRemoteSecret(r.MutationGrant.Reveal()) || !r.ReleaseToken.Empty() {
			return errors.New("mutation request requires only its ephemeral mutation credential")
		}
	default:
		return errors.New("unsupported remote RPC operation")
	}
	return nil
}

type RemoteProbeTarget struct {
	TargetID       string `json:"target_id"`
	ServiceType    string `json:"service_type"`
	DeploymentMode string `json:"deployment_mode"`
	CurrentVersion string `json:"current_version,omitempty"`
}

type RemoteProbeResult struct {
	ProtocolVersion int                 `json:"protocol_version"`
	HelperVersion   string              `json:"helper_version"`
	HostID          string              `json:"host_id"`
	OS              string              `json:"os"`
	Arch            string              `json:"arch"`
	ConfigSHA256    string              `json:"config_sha256,omitempty"`
	Targets         []RemoteProbeTarget `json:"targets"`
}

func (p RemoteProbeResult) Validate() error {
	if p.ProtocolVersion != RemoteProtocolVersion || !identifierPattern.MatchString(p.HostID) || (p.HelperVersion != "dev" && !versionPattern.MatchString(p.HelperVersion)) {
		return errors.New("remote probe identity is invalid")
	}
	if p.OS != "linux" || (p.Arch != "amd64" && p.Arch != "arm64") {
		return errors.New("remote probe platform is unsupported")
	}
	if !digestPattern.MatchString(p.ConfigSHA256) || len(p.Targets) == 0 {
		return errors.New("remote probe config digest is invalid")
	}
	seen := make(map[string]bool, len(p.Targets))
	for _, target := range p.Targets {
		if !identifierPattern.MatchString(target.TargetID) || seen[target.TargetID] {
			return errors.New("remote probe target identity is invalid or duplicated")
		}
		seen[target.TargetID] = true
		switch target.ServiceType {
		case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
		default:
			return errors.New("remote probe target service type is invalid")
		}
		if target.DeploymentMode != ModeSystemd && target.DeploymentMode != ModeDocker {
			return errors.New("remote probe target mode is invalid")
		}
		if current := strings.TrimSpace(target.CurrentVersion); current != "" && !versionPattern.MatchString(current) {
			return errors.New("remote probe target version is invalid")
		}
	}
	return nil
}

type RemoteRPCFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RemoteStageResult struct {
	Status         string `json:"status"`
	SessionID      string `json:"session_id"`
	PlanSHA256     string `json:"plan_sha256"`
	ArtifactDigest string `json:"artifact_digest"`
}

func (r RemoteStageResult) Validate() error {
	if r.Status != "staged" || !remoteSessionPattern.MatchString(r.SessionID) || !remotePlanHashPattern.MatchString(r.PlanSHA256) || !remotePlanHashPattern.MatchString(r.ArtifactDigest) {
		return errors.New("remote stage result is invalid")
	}
	return nil
}

type RemoteRPCResponse struct {
	Version    int                `json:"version"`
	Probe      *RemoteProbeResult `json:"probe,omitempty"`
	Stage      *RemoteStageResult `json:"stage,omitempty"`
	Result     *ApplyResult       `json:"result,omitempty"`
	SessionID  string             `json:"session_id,omitempty"`
	PlanSHA256 string             `json:"plan_sha256,omitempty"`
	Error      *RemoteRPCFailure  `json:"error,omitempty"`
}

func (r RemoteRPCResponse) Validate() error {
	if r.Version != RemoteProtocolVersion {
		return errors.New("unsupported remote RPC response version")
	}
	fields := 0
	if r.Probe != nil {
		fields++
		if err := r.Probe.Validate(); err != nil {
			return err
		}
	}
	if r.Stage != nil {
		fields++
		if err := r.Stage.Validate(); err != nil {
			return err
		}
	}
	if r.Result != nil {
		fields++
		if !remoteSessionPattern.MatchString(r.SessionID) || !remotePlanHashPattern.MatchString(r.PlanSHA256) {
			return errors.New("remote RPC result binding is invalid")
		}
		if r.Result.Status != "succeeded" && r.Result.Status != "rolled_back" {
			return errors.New("remote RPC result status is invalid")
		}
		if (r.Result.ArtifactDigest != "" && !digestPattern.MatchString(normalizeDigest(r.Result.ArtifactDigest))) ||
			(r.Result.PreviousDigest != "" && !digestPattern.MatchString(normalizeDigest(r.Result.PreviousDigest))) ||
			(r.Result.Message != "" && !safeRemoteMessage(r.Result.Message)) {
			return errors.New("remote RPC result is invalid")
		}
	}
	if r.Error != nil {
		fields++
		if !validRemoteFailureCode(r.Error.Code) || !safeRemoteMessage(r.Error.Message) {
			return errors.New("remote RPC failure is invalid")
		}
	}
	if r.Result == nil && (r.SessionID != "" || r.PlanSHA256 != "") {
		return errors.New("remote RPC result binding is unexpected")
	}
	if fields != 1 {
		return errors.New("remote RPC response must contain exactly one outcome")
	}
	return nil
}

func safeRemoteMessage(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 500 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func EncodeRemoteRPCRequest(w io.Writer, request RemoteRPCRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	wire := remoteRPCRequestWire{
		Version: request.Version, Operation: request.Operation, Plan: request.Plan,
		MutationGrant: request.MutationGrant.Reveal(), ReleaseToken: request.ReleaseToken.Reveal(),
	}
	return encodeRemoteFrame(w, wire, "request")
}

func DecodeRemoteRPCRequest(r io.Reader) (RemoteRPCRequest, error) {
	var wire remoteRPCRequestWire
	if err := decodeRemoteFrame(r, &wire, "request"); err != nil {
		return RemoteRPCRequest{}, err
	}
	request := RemoteRPCRequest{Version: wire.Version, Operation: wire.Operation, Plan: wire.Plan, MutationGrant: NewRemoteSecret(wire.MutationGrant), ReleaseToken: NewRemoteSecret(wire.ReleaseToken)}
	if err := request.Validate(); err != nil {
		return RemoteRPCRequest{}, err
	}
	return request, nil
}

type remoteRPCRequestWire struct {
	Version       int         `json:"version"`
	Operation     string      `json:"operation"`
	Plan          *RemotePlan `json:"plan,omitempty"`
	MutationGrant string      `json:"mutation_grant,omitempty"`
	ReleaseToken  string      `json:"release_token,omitempty"`
}

func EncodeRemoteRPCResponse(w io.Writer, response RemoteRPCResponse) error {
	if err := response.Validate(); err != nil {
		return err
	}
	return encodeRemoteFrame(w, response, "response")
}

func DecodeRemoteRPCResponse(r io.Reader) (RemoteRPCResponse, error) {
	var response RemoteRPCResponse
	if err := decodeRemoteFrame(r, &response, "response"); err != nil {
		return RemoteRPCResponse{}, err
	}
	if err := response.Validate(); err != nil {
		return RemoteRPCResponse{}, err
	}
	return response, nil
}

func encodeRemoteFrame(w io.Writer, value any, kind string) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode remote RPC %s", kind)
	}
	if len(payload)+1 > RemoteProtocolMaxFrameBytes {
		return fmt.Errorf("remote RPC %s exceeds the size limit", kind)
	}
	payload = append(payload, '\n')
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write remote RPC %s: %w", kind, err)
	}
	return nil
}

func decodeRemoteFrame(r io.Reader, out any, kind string) error {
	data, err := io.ReadAll(io.LimitReader(r, RemoteProtocolMaxFrameBytes+1))
	if err != nil {
		return fmt.Errorf("read remote RPC %s: %w", kind, err)
	}
	if len(data) == 0 || len(data) > RemoteProtocolMaxFrameBytes {
		return fmt.Errorf("remote RPC %s is empty or exceeds the size limit", kind)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode remote RPC %s", kind)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("remote RPC %s contains trailing data", kind)
	}
	return nil
}

// RemoteExecutor is the coordinator-side boundary. Implementations must keep
// credentials in stdin payloads and must never add them to argv, environment,
// returned errors or logs.
type RemoteExecutor interface {
	Probe(context.Context, SSHHost) (RemoteProbeResult, error)
	Stage(context.Context, SSHHost, RemotePlan, RemoteSecret) (RemoteStageResult, error)
	Apply(context.Context, SSHHost, RemotePlan, RemoteSecret) (ApplyResult, error)
	Reconcile(context.Context, SSHHost, RemotePlan, RemoteSecret) (ApplyResult, error)
}

func localProbePlatform(hostID, helperVersion, configDigest string, targets []RemoteProbeTarget) RemoteProbeResult {
	return RemoteProbeResult{ProtocolVersion: RemoteProtocolVersion, HelperVersion: helperVersion, HostID: hostID, OS: runtime.GOOS, Arch: runtime.GOARCH, ConfigSHA256: configDigest, Targets: targets}
}
