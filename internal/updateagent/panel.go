package updateagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/version"
)

type UpdateJob struct {
	ID             string `json:"id"`
	HostID         string `json:"host_id,omitempty"`
	TargetID       string `json:"target_id"`
	TargetType     string `json:"target_type,omitempty"`
	ServiceType    string `json:"service_type"`
	DeploymentMode string `json:"deployment_mode"`
	CurrentVersion string `json:"current_version,omitempty"`
	TargetVersion  string `json:"target_version"`
	Version        string `json:"version,omitempty"`
	LeaseToken     string `json:"lease_token,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	Status         string `json:"status,omitempty"`
	Progress       int    `json:"progress,omitempty"`
	Sequence       uint64 `json:"sequence,omitempty"`
	// ReportSequence is local-only. Claim responses define it as the exact
	// sequence to use for the first report, while Sequence remains the last
	// sequence stored by the server.
	ReportSequence   uint64 `json:"-"`
	LeaseGeneration  uint64 `json:"lease_generation,omitempty"`
	RecoveryRequired bool   `json:"recovery_required,omitempty"`
}

func (j UpdateJob) EffectiveVersion() string {
	if strings.TrimSpace(j.TargetVersion) != "" {
		return strings.TrimSpace(j.TargetVersion)
	}
	return strings.TrimSpace(j.Version)
}

func (j UpdateJob) EffectiveType() string {
	if strings.TrimSpace(j.ServiceType) != "" {
		return strings.TrimSpace(j.ServiceType)
	}
	return strings.TrimSpace(j.TargetType)
}

type ClaimResponse struct {
	Job              *UpdateJob `json:"job,omitempty"`
	LeaseToken       string     `json:"lease_token,omitempty"`
	LeaseExpiresAt   string     `json:"lease_expires_at,omitempty"`
	ReportSequence   uint64     `json:"report_sequence,omitempty"`
	LeaseGeneration  uint64     `json:"lease_generation,omitempty"`
	RecoveryRequired bool       `json:"recovery_required,omitempty"`
	LastStatus       string     `json:"last_status,omitempty"`
	ClearActiveJobID bool       `json:"clear_active_job_id,omitempty"`
	UpdateJob
}

type JobReport struct {
	ServiceID       string `json:"service_id"`
	LeaseToken      string `json:"lease_token"`
	Sequence        uint64 `json:"sequence"`
	LeaseGeneration uint64 `json:"lease_generation"`
	Status          string `json:"status"`
	Progress        int    `json:"progress,omitempty"`
	Code            string `json:"code,omitempty"`
	Message         string `json:"message,omitempty"`
	ArtifactDigest  string `json:"artifact_digest,omitempty"`
	PreviousDigest  string `json:"previous_digest,omitempty"`
}

type MutationGrantBinding struct {
	LeaseGeneration uint64 `json:"lease_generation"`
	HostID          string `json:"host_id"`
	TargetID        string `json:"target_id"`
	TargetVersion   string `json:"target_version"`
	DeploymentMode  string `json:"deployment_mode"`
	Operation       string `json:"operation"`
	PlanSHA256      string `json:"plan_sha256"`
	SessionID       string `json:"session_id"`
}

type MutationGrantRequest struct {
	ServiceID  string `json:"service_id"`
	LeaseToken string `json:"lease_token"`
	MutationGrantBinding
}

type MutationGrant struct {
	Token     string `json:"grant_token"`
	ExpiresAt string `json:"expires_at"`
}

type PanelClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// HostHeartbeat is the coordinator's last bounded SSH probe result.  It is
// reported separately from the coordinator heartbeat so the Control Panel can
// distinguish "central updater offline" from "one managed host unreachable".
// Code must be a stable allow-listed value; raw SSH errors never leave the
// coordinator.
type HostHeartbeat struct {
	Name         string
	Reachability string
	CheckedAt    time.Time
	Code         string
	Arch         string
}

func (c PanelClient) Register(ctx context.Context, cfg Config, deployedVersions map[string]string) error {
	return c.RegisterWithHosts(ctx, cfg, deployedVersions, nil)
}

func (c PanelClient) RegisterWithHosts(ctx context.Context, cfg Config, deployedVersions map[string]string, hosts map[string]HostHeartbeat) error {
	hostname, _ := os.Hostname()
	name := strings.TrimSpace(cfg.ServiceName)
	if name == "" {
		name = "Autostream Updater"
	}
	body := map[string]any{
		"service_id": cfg.NodeID, "service_type": ServiceTypeUpdateAgent, "service_name": name,
		"host": cfg.API.Host, "port": cfg.API.Port, "ssl_enabled": cfg.API.SSLEnabled,
		"public_url": cfg.API.PublicURL(), "version": version.Current(), "commit": version.Commit,
		"build_date": version.BuildDate, "hostname": hostname, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"capabilities": coordinatorCapabilities(cfg, deployedVersions, hosts),
	}
	return c.post(ctx, "/services/register", body, nil)
}

func (c PanelClient) Heartbeat(ctx context.Context, cfg Config, status string, deployedVersions map[string]string) error {
	return c.HeartbeatWithHosts(ctx, cfg, status, deployedVersions, nil)
}

func (c PanelClient) HeartbeatWithHosts(ctx context.Context, cfg Config, status string, deployedVersions map[string]string, hosts map[string]HostHeartbeat) error {
	hostname, _ := os.Hostname()
	body := map[string]any{
		"service_id": cfg.NodeID, "status": status, "version": version.Current(), "commit": version.Commit,
		"build_date": version.BuildDate, "hostname": hostname, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"capabilities": coordinatorCapabilities(cfg, deployedVersions, hosts),
		"api":          map[string]any{"host": cfg.API.Host, "port": cfg.API.Port, "sslEnabled": cfg.API.SSLEnabled},
	}
	return c.post(ctx, "/services/heartbeat", body, nil)
}

func coordinatorCapabilities(cfg Config, deployedVersions map[string]string, hosts map[string]HostHeartbeat) map[string]any {
	modes := map[string]string{}
	targetHosts := map[string]string{}
	targets := make([]string, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets = append(targets, target.TargetID)
		modes[target.TargetID] = target.DeploymentMode
		if strings.TrimSpace(target.HostID) != "" {
			targetHosts[target.TargetID] = strings.TrimSpace(target.HostID)
		}
	}
	hostStatuses := map[string]string{}
	hostCheckedAt := map[string]string{}
	hostCodes := map[string]string{}
	hostNames := map[string]string{}
	hostArches := map[string]string{}
	for hostID, host := range hosts {
		hostID = strings.TrimSpace(hostID)
		if hostID == "" {
			continue
		}
		switch host.Reachability {
		case "reachable", "unreachable":
			hostStatuses[hostID] = host.Reachability
		default:
			hostStatuses[hostID] = "unknown"
		}
		if !host.CheckedAt.IsZero() {
			hostCheckedAt[hostID] = host.CheckedAt.UTC().Format(time.RFC3339)
		}
		if host.Code != "" {
			hostCodes[hostID] = host.Code
		}
		if host.Name != "" {
			hostNames[hostID] = host.Name
		}
		if host.Arch == "amd64" || host.Arch == "arm64" {
			hostArches[hostID] = host.Arch
		}
	}
	return map[string]any{
		"update_executor": true, "managed_targets": targets, "deployment_modes": modes,
		"target_hosts": targetHosts, "deployed_versions": deployedVersions,
		"host_statuses": hostStatuses, "host_checked_at": hostCheckedAt,
		"host_codes": hostCodes, "host_names": hostNames, "host_arches": hostArches,
	}
}

func (c PanelClient) Claim(ctx context.Context, serviceID, activeJobID string) (*UpdateJob, bool, error) {
	return c.ClaimHost(ctx, serviceID, "", activeJobID)
}

func (c PanelClient) ClaimHost(ctx context.Context, serviceID, hostID, activeJobID string) (*UpdateJob, bool, error) {
	var response ClaimResponse
	body := map[string]string{"service_id": serviceID}
	if strings.TrimSpace(hostID) != "" {
		body["host_id"] = strings.TrimSpace(hostID)
	}
	if strings.TrimSpace(activeJobID) != "" {
		body["active_job_id"] = strings.TrimSpace(activeJobID)
	}
	err := c.post(ctx, "/services/update-jobs/claim", body, &response)
	if errors.Is(err, errNoContent) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if response.ClearActiveJobID {
		return nil, true, nil
	}
	job := response.Job
	if job == nil && response.UpdateJob.ID != "" {
		copy := response.UpdateJob
		job = &copy
	}
	if job == nil || strings.TrimSpace(job.ID) == "" {
		return nil, false, errors.New("claim response did not include a job")
	}
	if job.LeaseToken == "" {
		job.LeaseToken = response.LeaseToken
	}
	if job.LeaseExpiresAt == "" {
		job.LeaseExpiresAt = response.LeaseExpiresAt
	}
	if response.ReportSequence > 0 {
		job.ReportSequence = response.ReportSequence
	}
	if response.LeaseGeneration > 0 {
		job.LeaseGeneration = response.LeaseGeneration
	}
	if response.RecoveryRequired {
		job.RecoveryRequired = true
	}
	if response.LastStatus != "" {
		job.Status = response.LastStatus
	}
	if job.LeaseToken == "" {
		return nil, false, errors.New("claim response did not include a lease token")
	}
	return job, false, nil
}

func (c PanelClient) Report(ctx context.Context, jobID string, report JobReport) error {
	return c.post(ctx, "/services/update-jobs/"+url.PathEscape(jobID)+"/report", report, nil)
}

func (c PanelClient) Authorize(ctx context.Context, jobID string, body map[string]any) error {
	return c.post(ctx, "/services/update-jobs/"+url.PathEscape(jobID)+"/authorize", body, nil)
}

func (c PanelClient) IssueMutationGrant(ctx context.Context, jobID string, request MutationGrantRequest) (MutationGrant, error) {
	var grant MutationGrant
	err := c.post(ctx, "/services/update-jobs/"+url.PathEscape(jobID)+"/mutation-grants", request, &grant)
	if err != nil {
		return MutationGrant{}, err
	}
	if strings.TrimSpace(grant.Token) == "" || strings.TrimSpace(grant.ExpiresAt) == "" {
		return MutationGrant{}, errors.New("mutation grant response is incomplete")
	}
	return grant, nil
}

func ConsumeMutationGrant(ctx context.Context, panelURL, jobID, grantToken string, binding MutationGrantBinding, client *http.Client) error {
	return (PanelClient{BaseURL: panelURL, Token: grantToken, HTTP: client}).post(ctx, "/services/update-jobs/"+url.PathEscape(jobID)+"/mutation-grants/consume", binding, nil)
}

type PanelHTTPError struct {
	Status int
	Code   string
}

func (e *PanelHTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("panel returned HTTP %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("panel returned HTTP %d", e.Status)
}

func safePanelErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case "system_update_job_not_found",
		"system_update_lease_invalid",
		"system_update_sequence_stale",
		"system_update_transition_invalid",
		"invalid_system_update_report",
		"report_system_update_failed",
		"system_update_authorization_state_invalid",
		"system_update_authorization_mismatch",
		"invalid_system_update_authorization",
		"authorize_system_update_failed",
		"system_update_mutation_grant_required",
		"system_update_mutation_grant_unavailable",
		"system_update_mutation_grant_state_invalid",
		"system_update_mutation_grant_binding_mismatch",
		"system_update_mutation_grant_conflict",
		"invalid_system_update_mutation_grant",
		"invalid_system_update_mutation_grant_consumption",
		"issue_system_update_mutation_grant_failed",
		"consume_system_update_mutation_grant_failed":
		return strings.TrimSpace(code)
	default:
		return ""
	}
}

func IsPermanentReportError(err error) bool {
	var httpErr *PanelHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Status == http.StatusNotFound || (httpErr.Status == http.StatusConflict && (httpErr.Code == "system_update_lease_invalid" || httpErr.Code == "system_update_sequence_stale"))
}

func IsFatalReportError(err error) bool {
	var httpErr *PanelHTTPError
	return errors.As(err, &httpErr) && httpErr.Status >= 400 && httpErr.Status < 500 && !IsPermanentReportError(err)
}

var errNoContent = errors.New("no content")

func (c PanelClient) post(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	base := strings.TrimRight(c.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		if out != nil {
			return errNoContent
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &body)
		return &PanelHTTPError{Status: resp.StatusCode, Code: safePanelErrorCode(body.Code)}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return nil
}
