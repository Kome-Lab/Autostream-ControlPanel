package servicecall

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"time"
)

func (c Client) getWorkerEvents(ctx context.Context, service store.RegisteredService, endpoint string) WorkerEventsResult {
	result := WorkerEventsResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/json")
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	var body struct {
		Events []WorkerEvent `json:"events"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		result.Error = "decode response failed"
		return result
	}
	result.Events = body.Events
	result.Success = true
	return RedactWorkerEventsResult(result)
}

func (c Client) getEncoderPreflight(ctx context.Context, service store.RegisteredService, endpoint string) ServicePreflightResult {
	result := ServicePreflightResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/json")
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	var body struct {
		Ready     bool                    `json:"ready"`
		CheckedAt time.Time               `json:"checked_at"`
		Checks    []ServicePreflightCheck `json:"checks"`
		Summary   map[string]any          `json:"summary"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		result.Error = "decode response failed"
		return result
	}
	result.Ready = body.Ready
	result.CheckedAt = body.CheckedAt
	result.Checks = body.Checks
	result.Summary = body.Summary
	result.Success = true
	return RedactServicePreflightResult(result)
}

func (c Client) getAudioStatus(ctx context.Context, service store.RegisteredService, endpoint string) AudioStatusResult {
	result := AudioStatusResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/json")
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	if err := json.NewDecoder(response.Body).Decode(&result.AudioBridgeState); err != nil {
		result.Error = "decode response failed"
		return result
	}
	result.Success = true
	return result
}
