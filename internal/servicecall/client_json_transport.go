package servicecall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"strings"
	"time"
)

func (c Client) authToken(service store.RegisteredService) (string, error) {
	if c.RuntimeTokenResolver != nil {
		token, err := c.RuntimeTokenResolver(service)
		if err != nil || strings.TrimSpace(token) == "" {
			return "", errors.New("node runtime token could not be resolved")
		}
		return strings.TrimSpace(token), nil
	}
	if strings.TrimSpace(service.NodeTokenCiphertext) != "" && strings.TrimSpace(service.NodeTokenNonce) != "" {
		key := strings.TrimSpace(c.Config.NodeTokenKey)
		if key == "" {
			return "", errors.New("node runtime token encryption key is not configured")
		}
		token, err := security.DecryptSecret(service.NodeTokenCiphertext, service.NodeTokenNonce, key)
		if err != nil || strings.TrimSpace(token) == "" {
			return "", errors.New("node runtime token could not be decrypted")
		}
		return token, nil
	}
	return "", errors.New("node runtime token is not configured")
}

func (c Client) post(ctx context.Context, service store.RegisteredService, endpoint string, payload any) DispatchResult {
	return c.serviceJSONAction(ctx, service, http.MethodPost, endpoint, payload)
}

func (c Client) postCapturingWorkerVideoIngest(ctx context.Context, service store.RegisteredService, endpoint string, payload any, route *workerVideoIngestRoute) DispatchResult {
	return c.serviceJSONActionInternal(ctx, service, http.MethodPost, endpoint, payload, route)
}

// postWithTimeout uses a copied Client so the Encoder's bounded graceful stop
// window cannot shorten other service calls or mutate a caller-supplied HTTP
// client shared by them.
func (c Client) postWithTimeout(ctx context.Context, service store.RegisteredService, endpoint string, payload any, timeout time.Duration) DispatchResult {
	c.Config.Timeout = timeout
	if c.HTTP != nil {
		client := *c.HTTP
		client.Timeout = timeout
		c.HTTP = &client
	}
	return c.post(ctx, service, endpoint, payload)
}

func encoderStopRequestTimeout(configured time.Duration) time.Duration {
	if configured > encoderStopTimeout {
		return configured
	}
	return encoderStopTimeout
}

func (c Client) serviceJSONAction(ctx context.Context, service store.RegisteredService, method, endpoint string, payload any) DispatchResult {
	return c.serviceJSONActionInternal(ctx, service, method, endpoint, payload, nil)
}

func (c Client) serviceJSONActionInternal(ctx context.Context, service store.RegisteredService, method, endpoint string, payload any, workerVideoRoute *workerVideoIngestRoute) DispatchResult {
	result := DispatchResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
	if !c.Enabled() {
		result.FailurePhase = "pre_dispatch"
		result.Error = "node runtime token encryption key is not configured"
		return result
	}
	authToken, err := c.authToken(service)
	if err != nil {
		result.FailurePhase = "pre_dispatch"
		result.Error = err.Error()
		return result
	}
	if err := c.Config.URLPolicy.ValidateURL(service.PublicURL); err != nil {
		result.FailurePhase = "pre_dispatch"
		result.Code = serviceURLIssueCode(err)
		result.Error = serviceURLMessage(err)
		return result
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			result.FailurePhase = "pre_dispatch"
			result.Error = "marshal payload failed"
			return result
		}
		body = bytes.NewReader(encoded)
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(reqCtx, method, joinURL(service.PublicURL, endpoint), body)
	if err != nil {
		result.FailurePhase = "pre_dispatch"
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.httpClient()
	response, err := client.Do(request)
	if err != nil {
		result.FailurePhase = "transport"
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var successBody struct {
			MessageID     string                 `json:"message_id"`
			AlreadySent   bool                   `json:"already_sent"`
			JobGeneration uint64                 `json:"job_generation"`
			VideoIngest   workerVideoIngestRoute `json:"video_ingest"`
		}
		workerStartResponse := service.ServiceType == "worker" && endpoint == "/jobs/start"
		var decoder *json.Decoder
		if workerStartResponse {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, maxWorkerStartResponseBytes+1))
			if readErr != nil {
				result.Code = "worker_job_generation_response_invalid"
				result.FailurePhase = "protocol"
				result.Error = "Worker start response could not be read"
				return result
			}
			if len(body) > maxWorkerStartResponseBytes {
				result.Code = "worker_job_generation_response_invalid"
				result.FailurePhase = "protocol"
				result.Error = "Worker start response exceeded size limit"
				return result
			}
			decoder = json.NewDecoder(bytes.NewReader(body))
		} else {
			decoder = json.NewDecoder(io.LimitReader(response.Body, 1<<20))
		}
		if err := decoder.Decode(&successBody); err == nil {
			if workerStartResponse {
				var trailing any
				if err := decoder.Decode(&trailing); err != io.EOF {
					result.Code = "worker_job_generation_response_invalid"
					result.FailurePhase = "protocol"
					result.Error = "Worker start response did not contain exactly one JSON value"
					return result
				}
			}
			result.MessageID = sanitizeServiceErrorValue(successBody.MessageID)
			result.AlreadySent = successBody.AlreadySent
			result.JobGeneration = successBody.JobGeneration
			if workerVideoRoute != nil {
				*workerVideoRoute = successBody.VideoIngest
			}
		}
		result.Success = true
		return result
	}
	var errorBody struct {
		Code         string `json:"code"`
		FailurePhase string `json:"failure_phase"`
		ErrorClass   string `json:"error_class"`
		Retryable    bool   `json:"retryable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&errorBody); err == nil {
		result.Code = sanitizeServiceErrorValue(errorBody.Code)
		result.FailurePhase = sanitizeServiceErrorValue(errorBody.FailurePhase)
		result.ErrorClass = sanitizeServiceErrorValue(errorBody.ErrorClass)
		result.Retryable = errorBody.Retryable
	}
	result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
	if result.Code != "" {
		result.Error += ": " + result.Code
	}
	return result
}
