package updateagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	PolicyStatusApplied = "applied"
	PolicyStatusPending = "pending"
	PolicyStatusFailed  = "failed"

	PolicyErrorFetch       = "policy_fetch_failed"
	PolicyErrorInvalid     = "policy_invalid"
	PolicyErrorSSHIdentity = "ssh_identity_failed"
	PolicyErrorSnapshot    = "policy_snapshot_failed"
	PolicyErrorCoordinator = "coordinator_start_failed"
	PolicyErrorActiveJob   = "active_job_pending"

	managedPolicyResponseMaxBytes = 1 << 20
)

type ManagedPolicyRequest struct {
	ServiceID       string `json:"service_id"`
	CurrentRevision int64  `json:"current_revision"`
}

// ManagedPolicy is the non-secret desired state owned by the Control Panel.
// Privileged paths and commands remain in each destination host's root-owned
// update-host.json and are intentionally absent here.
type ManagedPolicy struct {
	UpdaterID                string              `json:"updater_id"`
	Revision                 int64               `json:"revision"`
	API                      APIConfig           `json:"api"`
	PollIntervalSeconds      int                 `json:"poll_interval_seconds,omitempty"`
	HeartbeatIntervalSeconds int                 `json:"heartbeat_interval_seconds,omitempty"`
	Hosts                    []ManagedPolicyHost `json:"hosts"`
	Targets                  []Target            `json:"targets"`
	UpdatedAt                time.Time           `json:"updated_at"`
}

type ManagedPolicyHost struct {
	HostID                   string `json:"host_id"`
	Name                     string `json:"name"`
	Address                  string `json:"address"`
	Port                     int    `json:"port"`
	User                     string `json:"user"`
	Arch                     string `json:"arch"`
	HostPublicKey            string `json:"host_public_key"`
	HostPublicKeyFingerprint string `json:"host_public_key_fingerprint"`
}

// FetchManagedPolicy asks for a newer desired revision. A nil policy with
// changed=false means the caller already has the current revision.
func (c PanelClient) FetchManagedPolicy(ctx context.Context, serviceID string, currentRevision int64) (*ManagedPolicy, bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	if !identifierPattern.MatchString(serviceID) || currentRevision < 0 {
		return nil, false, errors.New("managed policy request identity is invalid")
	}
	payload, err := json.Marshal(ManagedPolicyRequest{ServiceID: serviceID, CurrentRevision: currentRevision})
	if err != nil {
		return nil, false, errors.New("encode managed policy request")
	}
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/services/update-agent/policy", bytes.NewReader(payload))
	if err != nil {
		return nil, false, errors.New("create managed policy request")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	configuredClient := *client
	if configuredClient.Timeout <= 0 {
		configuredClient.Timeout = 15 * time.Second
	}
	configuredClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := configuredClient.Do(request)
	if err != nil {
		return nil, false, errors.New("fetch managed policy")
	}
	defer response.Body.Close()
	if !responseNoStore(response.Header.Values("Cache-Control")) {
		return nil, false, errors.New("managed policy response must use Cache-Control no-store")
	}
	if response.StatusCode == http.StatusNoContent {
		return nil, false, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		var apiError struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &apiError)
		return nil, false, &PanelHTTPError{Status: response.StatusCode, Code: safePanelErrorCode(apiError.Code)}
	}
	limited := &io.LimitedReader{R: response.Body, N: managedPolicyResponseMaxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil || len(data) == 0 || len(data) > managedPolicyResponseMaxBytes {
		return nil, false, errors.New("read managed policy response")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var policy ManagedPolicy
	if err := decoder.Decode(&policy); err != nil {
		return nil, false, errors.New("decode managed policy response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, false, errors.New("managed policy response contains trailing data")
	}
	if strings.TrimSpace(policy.UpdaterID) != serviceID || policy.Revision <= currentRevision {
		return nil, false, errors.New("managed policy response identity or revision is invalid")
	}
	return &policy, true, nil
}

func responseNoStore(values []string) bool {
	for _, value := range values {
		for _, directive := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(directive), "no-store") {
				return true
			}
		}
	}
	return false
}

// Materialize creates or reuses a local per-host SSH identity and combines the
// identity-only bootstrap with Panel-owned non-secret policy.
func (p ManagedPolicy) Materialize(bootstrap Config) (Config, error) {
	if !bootstrap.IsManagedBootstrap() {
		return Config{}, errors.New("managed policy requires an identity-only updater bootstrap")
	}
	if p.Revision <= 0 || strings.TrimSpace(p.UpdaterID) != strings.TrimSpace(bootstrap.NodeID) {
		return Config{}, errors.New("managed policy identity or revision is invalid")
	}
	if p.UpdatedAt.IsZero() {
		return Config{}, errors.New("managed policy updated_at is required")
	}
	if len(p.Hosts) == 0 || len(p.Targets) == 0 {
		return Config{}, errors.New("managed policy requires at least one host and target")
	}
	cfg := Config{
		PanelURL:                 strings.TrimSpace(bootstrap.PanelURL),
		NodeID:                   strings.TrimSpace(bootstrap.NodeID),
		RuntimeToken:             strings.TrimSpace(bootstrap.RuntimeToken),
		ServiceName:              strings.TrimSpace(bootstrap.ServiceName),
		API:                      p.API,
		StateDir:                 ManagedUpdaterStateDir,
		PollIntervalSeconds:      p.PollIntervalSeconds,
		HeartbeatIntervalSeconds: p.HeartbeatIntervalSeconds,
		Targets:                  append([]Target(nil), p.Targets...),
		hostsSpecified:           true,
		PolicyRevision:           p.Revision,
		PolicyDesiredRevision:    p.Revision,
		PolicyStatus:             PolicyStatusApplied,
		SSHClientPublicKeys:      make(map[string]string, len(p.Hosts)),
		SSHClientKeyFingerprints: make(map[string]string, len(p.Hosts)),
	}
	cfg.Hosts = make([]SSHHost, 0, len(p.Hosts))
	for index, policyHost := range p.Hosts {
		hostPublicKey := strings.TrimSpace(policyHost.HostPublicKey)
		parsedHostKey, _, _, trailing, err := ssh.ParseAuthorizedKey([]byte(hostPublicKey))
		if err != nil || len(bytes.TrimSpace(trailing)) != 0 {
			return Config{}, fmt.Errorf("hosts[%d]: host_public_key is invalid", index)
		}
		fingerprint := ssh.FingerprintSHA256(parsedHostKey)
		if fingerprint != strings.TrimSpace(policyHost.HostPublicKeyFingerprint) {
			return Config{}, fmt.Errorf("hosts[%d]: host_public_key_fingerprint does not match host_public_key", index)
		}
		privatePath, clientPublicKey, clientFingerprint, err := EnsureManagedSSHIdentity(ManagedUpdaterStateDir, strings.TrimSpace(policyHost.HostID))
		if err != nil {
			return Config{}, fmt.Errorf("hosts[%d]: initialize local SSH identity: %w", index, err)
		}
		host := SSHHost{
			HostID:        strings.TrimSpace(policyHost.HostID),
			Name:          strings.TrimSpace(policyHost.Name),
			Address:       strings.TrimSpace(policyHost.Address),
			Port:          policyHost.Port,
			User:          strings.TrimSpace(policyHost.User),
			IdentityFile:  privatePath,
			Arch:          strings.TrimSpace(policyHost.Arch),
			HostPublicKey: hostPublicKey,
		}
		cfg.Hosts = append(cfg.Hosts, host)
		cfg.SSHClientPublicKeys[host.HostID] = clientPublicKey
		cfg.SSHClientKeyFingerprints[host.HostID] = clientFingerprint
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate managed policy: %w", err)
	}
	return cfg, nil
}

func safePolicyErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case PolicyErrorFetch, PolicyErrorInvalid, PolicyErrorSSHIdentity, PolicyErrorSnapshot, PolicyErrorCoordinator, PolicyErrorActiveJob:
		return strings.TrimSpace(code)
	default:
		return PolicyErrorInvalid
	}
}
