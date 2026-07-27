package updateagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	BootstrapHostStatusQueued     = "queued"
	BootstrapHostStatusConnecting = "connecting"
	BootstrapHostStatusVerifying  = "verifying"
	BootstrapHostStatusUploading  = "uploading"
	BootstrapHostStatusInstalling = "installing"
	BootstrapHostStatusProbing    = "probing"
	BootstrapHostStatusSucceeded  = "succeeded"
	BootstrapHostStatusFailed     = "failed"

	bootstrapClaimResponseMaxBytes = 1 << 20
)

type BootstrapJobClaim struct {
	ID               string                      `json:"id"`
	UpdaterID        string                      `json:"updater_id"`
	ExpectedRevision int64                       `json:"expected_revision"`
	HostIDs          []string                    `json:"host_ids"`
	Envelope         BootstrapCredentialEnvelope `json:"envelope"`
	LeaseToken       string                      `json:"lease_token"`
	ReleaseToken     RemoteSecret                `json:"release_token"`
}

type BootstrapJobReport struct {
	ServiceID  string `json:"service_id"`
	LeaseToken string `json:"lease_token"`
	HostID     string `json:"host_id"`
	Status     string `json:"status"`
	Progress   int    `json:"progress,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (c PanelClient) ClaimBootstrap(
	ctx context.Context,
	serviceID string,
	currentRevision int64,
	recipientKeyFingerprint string,
) (BootstrapJobClaim, bool, error) {
	if !identifierPattern.MatchString(strings.TrimSpace(serviceID)) || currentRevision <= 0 ||
		!validBootstrapRecipientKeyFingerprint(recipientKeyFingerprint) {
		return BootstrapJobClaim{}, false, errors.New("bootstrap claim identity is invalid")
	}
	if err := validateBootstrapPanelTransport(c.BaseURL); err != nil {
		return BootstrapJobClaim{}, false, err
	}
	payload, err := json.Marshal(map[string]any{
		"service_id":                strings.TrimSpace(serviceID),
		"current_revision":          currentRevision,
		"recipient_key_fingerprint": recipientKeyFingerprint,
	})
	if err != nil {
		return BootstrapJobClaim{}, false, errors.New("encode bootstrap claim")
	}
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		base+"/services/update-agent/bootstrap-jobs/claim",
		bytes.NewReader(payload),
	)
	if err != nil {
		return BootstrapJobClaim{}, false, errors.New("create bootstrap claim")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	configuredClient := c.bootstrapHTTPClient()
	response, err := configuredClient.Do(request)
	if err != nil {
		return BootstrapJobClaim{}, false, errors.New("claim bootstrap job")
	}
	defer response.Body.Close()
	if !responseNoStore(response.Header.Values("Cache-Control")) {
		return BootstrapJobClaim{}, false, errors.New("bootstrap claim response must use Cache-Control no-store")
	}
	if response.StatusCode == http.StatusNoContent {
		return BootstrapJobClaim{}, false, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		var apiError struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &apiError)
		return BootstrapJobClaim{}, false, &PanelHTTPError{
			Status: response.StatusCode,
			Code:   safeBootstrapPanelErrorCode(apiError.Code),
		}
	}
	limited := &io.LimitedReader{R: response.Body, N: bootstrapClaimResponseMaxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil || len(data) == 0 || len(data) > bootstrapClaimResponseMaxBytes {
		return BootstrapJobClaim{}, false, errors.New("read bootstrap claim response")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var claim BootstrapJobClaim
	if err := decoder.Decode(&claim); err != nil {
		return BootstrapJobClaim{}, false, errors.New("decode bootstrap claim response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return BootstrapJobClaim{}, false, errors.New("bootstrap claim response contains trailing data")
	}
	if err := claim.Validate(serviceID, currentRevision); err != nil {
		return BootstrapJobClaim{}, false, err
	}
	return claim, true, nil
}

func (c PanelClient) AcceptBootstrap(ctx context.Context, jobID, serviceID, leaseToken string) error {
	if err := validateBootstrapPanelTransport(c.BaseURL); err != nil {
		return err
	}
	if !identifierPattern.MatchString(strings.TrimSpace(serviceID)) ||
		!validBootstrapJobID(jobID) ||
		!validRemoteSecret(leaseToken) {
		return errors.New("bootstrap accept identity is invalid")
	}
	return c.postBootstrap(
		ctx,
		"/services/update-agent/bootstrap-jobs/"+url.PathEscape(strings.TrimSpace(jobID))+"/accept",
		map[string]string{
			"service_id":  strings.TrimSpace(serviceID),
			"lease_token": leaseToken,
		},
	)
}

func (c PanelClient) ReportBootstrap(ctx context.Context, jobID string, report BootstrapJobReport) error {
	if err := validateBootstrapPanelTransport(c.BaseURL); err != nil {
		return err
	}
	if !validBootstrapJobID(jobID) ||
		!identifierPattern.MatchString(strings.TrimSpace(report.ServiceID)) ||
		!validRemoteSecret(report.LeaseToken) ||
		!identifierPattern.MatchString(strings.TrimSpace(report.HostID)) ||
		!validBootstrapHostStatus(report.Status) ||
		report.Progress < 0 || report.Progress > 100 ||
		len(report.Code) > 128 || len(report.Message) > 512 ||
		containsUnsafeBootstrapReportText(report.Code) || containsUnsafeBootstrapReportText(report.Message) {
		return errors.New("bootstrap report is invalid")
	}
	return c.postBootstrap(
		ctx,
		"/services/update-agent/bootstrap-jobs/"+url.PathEscape(strings.TrimSpace(jobID))+"/report",
		report,
	)
}

func (c PanelClient) postBootstrap(ctx context.Context, path string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return errors.New("encode bootstrap request")
	}
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		base+path,
		bytes.NewReader(payload),
	)
	if err != nil {
		return errors.New("create bootstrap request")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.bootstrapHTTPClient().Do(request)
	if err != nil {
		return errors.New("send bootstrap request")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	var apiError struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(data, &apiError)
	return &PanelHTTPError{
		Status: response.StatusCode,
		Code:   safeBootstrapPanelErrorCode(apiError.Code),
	}
}

func (c PanelClient) bootstrapHTTPClient() *http.Client {
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	configuredClient := *client
	if configuredClient.Timeout <= 0 || configuredClient.Timeout > 15*time.Second {
		configuredClient.Timeout = 15 * time.Second
	}
	configuredClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &configuredClient
}

func (claim BootstrapJobClaim) Validate(serviceID string, currentRevision int64) error {
	if !validBootstrapJobID(claim.ID) ||
		strings.TrimSpace(claim.UpdaterID) != strings.TrimSpace(serviceID) ||
		claim.ExpectedRevision != currentRevision ||
		len(claim.HostIDs) == 0 || len(claim.HostIDs) > 128 ||
		!validRemoteSecret(claim.LeaseToken) ||
		claim.ReleaseToken.Empty() {
		return errors.New("bootstrap claim response identity is invalid")
	}
	seen := make(map[string]bool, len(claim.HostIDs))
	for _, hostID := range claim.HostIDs {
		hostID = strings.TrimSpace(hostID)
		if !identifierPattern.MatchString(hostID) || seen[hostID] {
			return errors.New("bootstrap claim response hosts are invalid")
		}
		seen[hostID] = true
	}
	if claim.Envelope.Version != 1 ||
		strings.TrimSpace(claim.Envelope.EphemeralPublicKey) == "" ||
		strings.TrimSpace(claim.Envelope.Nonce) == "" ||
		strings.TrimSpace(claim.Envelope.Ciphertext) == "" {
		return errors.New("bootstrap claim response envelope is invalid")
	}
	return nil
}

func validateBootstrapPanelTransport(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("bootstrap panel URL is invalid")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.Trim(parsed.Hostname(), "[]")
	ip := net.ParseIP(host)
	if parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("bootstrap credential transport requires HTTPS")
}

func validBootstrapJobID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return false
			}
		}
	}
	return true
}

func validBootstrapHostStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case BootstrapHostStatusQueued,
		BootstrapHostStatusConnecting,
		BootstrapHostStatusVerifying,
		BootstrapHostStatusUploading,
		BootstrapHostStatusInstalling,
		BootstrapHostStatusProbing,
		BootstrapHostStatusSucceeded,
		BootstrapHostStatusFailed:
		return true
	default:
		return false
	}
}

func safeBootstrapPanelErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case "bootstrap_job_not_found",
		"bootstrap_job_conflict",
		"bootstrap_job_expired",
		"bootstrap_lease_invalid",
		"bootstrap_policy_revision_mismatch",
		"bootstrap_recipient_key_changed",
		"updater_release_token_not_configured",
		"secure_transport_required":
		return strings.TrimSpace(code)
	default:
		return ""
	}
}

func validBootstrapRecipientKeyFingerprint(value string) bool {
	if value != strings.TrimSpace(value) || !strings.HasPrefix(value, "SHA256:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "SHA256:")
	decoded, err := base64.RawStdEncoding.DecodeString(encoded)
	return err == nil && len(decoded) == sha256.Size &&
		base64.RawStdEncoding.EncodeToString(decoded) == encoded
}

func containsUnsafeBootstrapReportText(value string) bool {
	if containsUnsafeText(value) {
		return true
	}
	for _, r := range value {
		switch r {
		case '\u061c', '\u200e', '\u200f',
			'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
			'\u2066', '\u2067', '\u2068', '\u2069':
			return true
		}
	}
	return false
}
