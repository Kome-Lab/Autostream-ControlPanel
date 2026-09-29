package servicecall

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"time"
)

const CapabilityStreamStartPrepareCommitV2 = "stream_start_prepare_commit_v2"

func startPreparationCapabilityIssue(services []store.RegisteredService) (ReadinessIssue, bool) {
	encoder := firstService(services, "encoder_recorder")
	missing := workerVideoCapabilitiesEnabled(services) && !reportedCapabilityTrue(encoder.ReportedCapabilities, CapabilityStreamStartPrepareCommitV2)
	return ReadinessIssue{ServiceID: encoder.ServiceID, ServiceType: "encoder_recorder", Code: "start_preparation_capability_unavailable", Message: "Encoder must report stream_start_prepare_commit_v2"}, missing
}

// A phase-specific copy preserves the ordinary Worker/Bot HTTP limits.
func (c Client) preparationJSON(ctx context.Context, service store.RegisteredService, method, endpoint string, payload any, limit time.Duration) (DispatchResult, []byte) {
	result := DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	fail := func(phase, code string) (DispatchResult, []byte) {
		result.FailurePhase = phase
		result.Code = code
		result.Error = code
		return result, nil
	}
	if !c.Enabled() {
		return fail("pre_dispatch", "start_preparation_auth_unavailable")
	}
	token, err := c.authToken(service)
	if err != nil {
		return fail("pre_dispatch", "start_preparation_auth_unavailable")
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		return fail("pre_dispatch", serviceURLIssueCode(err))
	}
	var reader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fail("pre_dispatch", "start_preparation_payload_invalid")
		}
		reader = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, joinURL(service.PublicURL, endpoint), reader)
	if err != nil {
		return fail("pre_dispatch", "start_preparation_request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.Config.Timeout = limit
	client := *c.httpClient()
	client.Timeout = limit
	response, err := client.Do(req)
	if err != nil {
		return fail("transport", "start_preparation_response_unknown")
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return fail("protocol", "start_preparation_response_invalid")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &e)
		result.Code = sanitizeServiceErrorValue(e.Code)
		result.Error = "start preparation rejected"
		return result, nil
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		return fail("protocol", "start_preparation_cache_policy_invalid")
	}
	result.Success = true
	return result, data
}
func preparationAction(identity contracts.EncoderStartPreparationIdentity) contracts.EncoderStartPreparationAction {
	return contracts.EncoderStartPreparationAction{SchemaVersion: 2, StreamID: identity.StreamID, StartID: identity.StartID, EncoderServiceID: identity.EncoderServiceID, JobGeneration: identity.JobGeneration}
}
