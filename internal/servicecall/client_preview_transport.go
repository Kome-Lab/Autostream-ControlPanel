package servicecall

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (c Client) getPreviewAsset(ctx context.Context, service store.RegisteredService, endpoint, name, byteRange string) PreviewAssetResult {
	result := PreviewAssetResult{ServiceID: service.ServiceID, ServiceType: service.ServiceType, Endpoint: endpoint}
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
	request.Header.Set("Accept", previewAcceptHeader(name))
	if name != "index.m3u8" {
		if rangeValue := normalizedPreviewByteRange(byteRange); rangeValue != "" {
			request.Header.Set("Range", rangeValue)
		}
	}
	response, err := c.httpClient().Do(request)
	if err != nil {
		result.Error = "service request failed"
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	result.ContentType = response.Header.Get("Content-Type")
	result.ContentRange = response.Header.Get("Content-Range")
	result.AcceptRanges = response.Header.Get("Accept-Ranges")
	// A valid RFC 7233 unsatisfied range is not an upstream availability
	// failure. The Control Panel validates the only safe form of
	// Content-Range before returning it to the browser.
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		result.Success = true
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var errorBody struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&errorBody); err == nil {
			result.Code = sanitizeServiceErrorValue(errorBody.Code)
		}
		result.Error = fmt.Sprintf("service returned status %d", response.StatusCode)
		return result
	}
	limit := int64(maxPreviewSegmentBytes)
	if name == "index.m3u8" {
		limit = maxPreviewPlaylistBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		result.Error = "read preview asset failed"
		return result
	}
	if int64(len(body)) > limit {
		result.Code = "preview_asset_too_large"
		result.Error = "preview asset exceeded size limit"
		return result
	}
	result.Success = true
	result.Body = body
	return result
}

// normalizedPreviewByteRange accepts exactly one RFC 7233 byte range. Preview
// is a bounded proxy, so forwarding arbitrary or multi-range headers would
// create avoidable request amplification at the Encoder.
func normalizedPreviewByteRange(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes=") {
		return ""
	}
	spec := strings.TrimSpace(strings.TrimPrefix(value, "bytes="))
	if spec == "" || strings.Contains(spec, ",") {
		return ""
	}
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return ""
	}
	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])
	if start == "" && end == "" {
		return ""
	}
	if start != "" && !decimalPreviewRangeValue(start) {
		return ""
	}
	if end != "" && !decimalPreviewRangeValue(end) {
		return ""
	}
	if start != "" && end != "" {
		startValue, startErr := strconv.ParseUint(start, 10, 64)
		endValue, endErr := strconv.ParseUint(end, 10, 64)
		if startErr != nil || endErr != nil || endValue < startValue {
			return ""
		}
	}
	return "bytes=" + start + "-" + end
}

func decimalPreviewRangeValue(value string) bool {
	if value == "" || len(value) > 20 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func previewAcceptHeader(name string) string {
	if name == "index.m3u8" {
		return "application/vnd.apple.mpegurl, application/x-mpegURL"
	}
	return "video/mp2t"
}
