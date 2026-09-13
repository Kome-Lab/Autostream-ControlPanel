package servicecall

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
)

func (c Client) getArchiveArtifact(ctx context.Context, service store.RegisteredService, endpoint, fallbackName, byteRange string) ArchiveArtifactDownloadResult {
	result := ArchiveArtifactDownloadResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint, FileName: fallbackName}
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
		result.Code = serviceURLIssueCode(err)
		result.Error = serviceURLMessage(err)
		return result
	}
	reqCtx, cancel := context.WithTimeout(ctx, archiveTransferTimeout)
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, joinURL(service.PublicURL, endpoint), nil)
	if err != nil {
		cancel()
		result.Error = "build request failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+authToken)
	request.Header.Set("Accept", "application/octet-stream")
	if value := normalizedPreviewByteRange(byteRange); value != "" {
		request.Header.Set("Range", value)
	}
	client := c.archiveHTTPClient()
	response, err := client.Do(request)
	if err != nil {
		cancel()
		result.Error = "service request failed"
		return result
	}
	result.StatusCode = response.StatusCode
	result.ContentRange = response.Header.Get("Content-Range")
	result.AcceptRanges = response.Header.Get("Accept-Ranges")
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		defer cancel()
		defer response.Body.Close()
		result.Success = true
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer cancel()
		defer response.Body.Close()
		var errorBody struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(response.Body).Decode(&errorBody); err == nil {
			result.Code = sanitizeServiceErrorValue(errorBody.Code)
		}
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		if result.Code != "" {
			result.Error += ": " + result.Code
		}
		return result
	}
	result.Success = true
	result.ContentType = response.Header.Get("Content-Type")
	result.SizeBytes = response.ContentLength
	result.Body = &cancelOnCloseReadCloser{ReadCloser: response.Body, cancel: cancel}
	return result
}

func (c Client) archiveHTTPClient() *http.Client {
	client := c.httpClient()
	clone := *client
	clone.Timeout = archiveTransferTimeout
	return &clone
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return err
}
