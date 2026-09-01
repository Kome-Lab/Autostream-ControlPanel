package servicecall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"unicode"
	"unicode/utf8"

	"github.com/example/autostream-control-panel/internal/store"
)

// VideoCoverDispatcher is the Bundle 4 controller boundary. Bundle 5 may
// attach an Encoder transport implementation, but Control Panel semantics do
// not depend on an endpoint or payload before live_video_cover_v1 is present.
type VideoCoverDispatcher interface {
	DispatchVideoCover(context.Context, store.RegisteredService, VideoCoverDispatchRequest) VideoCoverDispatchResult
}

type VideoCoverReconciler interface {
	ReconcileVideoCover(context.Context, store.RegisteredService, VideoCoverReconcileRequest) VideoCoverDispatchResult
}

type VideoCoverDispatchRequest struct {
	StreamID       string
	JobGeneration  uint64
	Revision       uint64
	Active         bool
	AssetVariantID string
	IdempotencyKey string
	CoverAsset     *MediaAssetDescriptor
	HideConfirmed  bool
}

type VideoCoverDispatchResult struct {
	Applied       bool
	Ambiguous     bool
	SafeErrorCode string
}

type VideoCoverReconcileRequest struct {
	StreamID       string
	JobGeneration  uint64
	Revision       uint64
	Active         bool
	AssetVariantID string
}

const maxVideoCoverControlResponseBytes = 1 << 20

// DispatchVideoCover reconciles the Encoder graph generation with a fresh GET
// before issuing one fenced mutation. In particular, a PUT is never retried:
// transport loss or an invalid response after dispatch is represented as an
// ambiguous result for the Control Panel's durable desired/applied state.
func (c Client) DispatchVideoCover(ctx context.Context, service store.RegisteredService, request VideoCoverDispatchRequest) VideoCoverDispatchResult {
	if service.ServiceType != "encoder_recorder" || !reportedCapabilityTrue(service.ReportedCapabilities, CapabilityLiveVideoCoverV1) {
		return VideoCoverDispatchResult{SafeErrorCode: "capability_required"}
	}
	if !validVideoCoverDispatchRequest(request) {
		return VideoCoverDispatchResult{SafeErrorCode: "revision_payload_conflict"}
	}
	state, ok := c.fetchVideoCoverRuntimeState(ctx, service, request.StreamID)
	if !ok {
		return VideoCoverDispatchResult{SafeErrorCode: "cover_graph_unavailable"}
	}
	if state.JobGeneration != request.JobGeneration {
		return VideoCoverDispatchResult{SafeErrorCode: "stale_job_generation"}
	}
	apply := EncoderVideoCoverApplyRequest{
		StreamID: request.StreamID, JobGeneration: request.JobGeneration,
		ExpectedGeneration: state.Generation, Revision: request.Revision,
		Active: request.Active, IdempotencyKey: request.IdempotencyKey,
		CoverAsset: request.CoverAsset, HideConfirmed: request.HideConfirmed,
	}
	return c.putVideoCoverRuntimeState(ctx, service, apply)
}

// ReconcileVideoCover performs a read only. It is the only path used after an
// ambiguous mutation replay, so a lost hide response can never cause a second
// PUT while the Encoder's actual graph state is unknown.
func (c Client) ReconcileVideoCover(ctx context.Context, service store.RegisteredService, request VideoCoverReconcileRequest) VideoCoverDispatchResult {
	if service.ServiceType != "encoder_recorder" || !reportedCapabilityTrue(service.ReportedCapabilities, CapabilityLiveVideoCoverV1) {
		return VideoCoverDispatchResult{SafeErrorCode: "capability_required"}
	}
	if !validVisualIdentifier(request.StreamID) || request.JobGeneration == 0 || request.Revision == 0 ||
		(request.Active && !validVisualIdentifier(request.AssetVariantID)) {
		return VideoCoverDispatchResult{SafeErrorCode: "revision_payload_conflict"}
	}
	state, ok := c.fetchVideoCoverRuntimeState(ctx, service, request.StreamID)
	if !ok {
		return VideoCoverDispatchResult{Ambiguous: true}
	}
	if state.JobGeneration != request.JobGeneration {
		return VideoCoverDispatchResult{SafeErrorCode: "stale_job_generation"}
	}
	if state.Applied.State == "known" && state.Applied.Active != nil && state.Applied.Revision == request.Revision && *state.Applied.Active == request.Active {
		if request.Active && (state.Applied.VariantID != request.AssetVariantID || state.Cover.VariantID != request.AssetVariantID) {
			return VideoCoverDispatchResult{SafeErrorCode: "revision_payload_conflict"}
		}
		if !request.Active && (state.Applied.VariantID != "" || state.Cover.Enabled) {
			return VideoCoverDispatchResult{SafeErrorCode: "revision_payload_conflict"}
		}
		return VideoCoverDispatchResult{Applied: true}
	}
	return VideoCoverDispatchResult{Ambiguous: true}
}

func validVideoCoverDispatchRequest(request VideoCoverDispatchRequest) bool {
	if !validVisualIdentifier(request.StreamID) || request.JobGeneration == 0 || request.Revision == 0 ||
		!validVisualIdempotencyKey(request.IdempotencyKey) {
		return false
	}
	if request.Active {
		return request.CoverAsset != nil && request.AssetVariantID == request.CoverAsset.VariantID &&
			validMediaAssetDescriptor(*request.CoverAsset) && !request.HideConfirmed
	}
	return request.CoverAsset == nil && request.AssetVariantID != "" && request.HideConfirmed
}

func (c Client) fetchVideoCoverRuntimeState(ctx context.Context, service store.RegisteredService, streamID string) (VideoCoverRuntimeState, bool) {
	body, status, _, received := c.videoCoverJSONRequest(ctx, service, http.MethodGet,
		"/streams/"+url.PathEscape(streamID)+"/video-cover-state", nil)
	if !received || status != http.StatusOK {
		return VideoCoverRuntimeState{}, false
	}
	var state VideoCoverRuntimeState
	if !decodeStrictSingleJSON(body, &state) || !validateVideoCoverRuntimeState(body, state) || state.StreamID != streamID {
		return VideoCoverRuntimeState{}, false
	}
	return state, true
}

func (c Client) putVideoCoverRuntimeState(ctx context.Context, service store.RegisteredService, request EncoderVideoCoverApplyRequest) VideoCoverDispatchResult {
	body, status, attempted, received := c.videoCoverJSONRequest(ctx, service, http.MethodPut,
		"/streams/"+url.PathEscape(request.StreamID)+"/video-cover-state", request)
	if !attempted {
		return VideoCoverDispatchResult{SafeErrorCode: "cover_graph_unavailable"}
	}
	if !received {
		return VideoCoverDispatchResult{Ambiguous: true}
	}
	if status == http.StatusNotFound {
		if decodeVideoCoverUnavailableResponse(body) {
			return VideoCoverDispatchResult{SafeErrorCode: "capability_required"}
		}
		return VideoCoverDispatchResult{Ambiguous: true}
	}
	var response EncoderVideoCoverApplyResponse
	if !decodeStrictSingleJSON(body, &response) || !validateVideoCoverApplyResponse(body, status, request, response) {
		return VideoCoverDispatchResult{Ambiguous: true}
	}
	switch response.Outcome {
	case "applied":
		return VideoCoverDispatchResult{Applied: true}
	case "ambiguous":
		return VideoCoverDispatchResult{Ambiguous: true}
	case "rejected":
		return VideoCoverDispatchResult{SafeErrorCode: response.Error.Code}
	default:
		return VideoCoverDispatchResult{Ambiguous: true}
	}
}

func (c Client) videoCoverJSONRequest(ctx context.Context, service store.RegisteredService, method, endpoint string, payload any) ([]byte, int, bool, bool) {
	if !c.Enabled() || c.Config.URLPolicy.ValidateURL(service.PublicURL) != nil {
		return nil, 0, false, false
	}
	token, err := c.authToken(service)
	if err != nil {
		return nil, 0, false, false
	}
	var reader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, false, false
		}
		reader = bytes.NewReader(encoded)
	}
	reqCtx := ctx
	if c.Config.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout)
		defer cancel()
	}
	httpRequest, err := http.NewRequestWithContext(reqCtx, method, joinURL(service.PublicURL, endpoint), reader)
	if err != nil {
		return nil, 0, false, false
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient().Do(httpRequest)
	if err != nil {
		return nil, 0, true, false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxVideoCoverControlResponseBytes+1))
	if err != nil || len(body) > maxVideoCoverControlResponseBytes {
		return nil, response.StatusCode, true, true
	}
	return body, response.StatusCode, true, true
}

func decodeStrictSingleJSON(body []byte, dst any) bool {
	if !utf8.Valid(body) || rejectDuplicateVideoCoverJSONFields(body) != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func decodeVideoCoverUnavailableResponse(body []byte) bool {
	var response struct {
		Code string `json:"code"`
	}
	if !decodeStrictSingleJSON(body, &response) || response.Code != "capability_required" {
		return false
	}
	object, ok := requiredJSONObject(body, "code")
	return ok && len(object) == 1
}

func validateVideoCoverApplyResponse(raw []byte, status int, request EncoderVideoCoverApplyRequest, response EncoderVideoCoverApplyResponse) bool {
	object, ok := requiredJSONObject(raw, "stream_id", "job_generation", "requested_revision", "actual_generation", "accepted", "rejected", "applied", "outcome", "actual")
	if !ok || response.StreamID != request.StreamID || response.StreamID != response.Actual.StreamID ||
		response.JobGeneration != response.Actual.JobGeneration ||
		response.RequestedRevision != request.Revision || response.ActualGeneration == 0 ||
		response.ActualGeneration != response.Actual.Generation || !validateVideoCoverRuntimeState(object["actual"], response.Actual) {
		return false
	}
	errorRaw, errorPresent := object["error"]
	if errorPresent != (response.Error != nil) ||
		response.Error != nil && !validateVisualSafeErrorShape(errorRaw, *response.Error) {
		return false
	}
	switch status {
	case http.StatusOK:
		return response.JobGeneration == request.JobGeneration && response.ActualGeneration == request.ExpectedGeneration &&
			response.Outcome == "applied" && response.Accepted && !response.Rejected && response.Applied && !errorPresent &&
			response.Actual.JobGeneration == request.JobGeneration && response.Actual.Desired.Revision == request.Revision &&
			response.Actual.Applied.State == "known" && response.Actual.Applied.Active != nil && *response.Actual.Applied.Active == request.Active &&
			response.Actual.Applied.Revision == request.Revision && response.Actual.AppliedWitness != nil && response.Actual.AppliedWitness.GraphApplied &&
			videoCoverRuntimeMatchesApplyRequest(response.Actual, request)
	case http.StatusAccepted:
		return response.JobGeneration == request.JobGeneration && response.ActualGeneration == request.ExpectedGeneration &&
			response.Outcome == "ambiguous" && response.Accepted && !response.Rejected && !response.Applied &&
			response.Error != nil && response.Error.Code == "cover_apply_ambiguous" && response.Actual.Readiness == "unknown" &&
			response.Actual.Applied.State == "unknown" && response.Actual.Error != nil && response.Actual.Error.Code == response.Error.Code &&
			response.Actual.NoAutomaticResend &&
			videoCoverRuntimeMatchesApplyRequest(response.Actual, request)
	case http.StatusConflict:
		if response.Outcome != "rejected" || response.Accepted || !response.Rejected || response.Applied ||
			response.Error == nil || !isVideoCoverFenceError(response.Error.Code) {
			return false
		}
		switch response.Error.Code {
		case "stale_job_generation":
			return response.JobGeneration != request.JobGeneration
		case "stale_cover_generation":
			return response.JobGeneration == request.JobGeneration && response.ActualGeneration != request.ExpectedGeneration
		default:
			return response.JobGeneration == request.JobGeneration && response.ActualGeneration == request.ExpectedGeneration
		}
	case http.StatusBadGateway:
		return response.JobGeneration == request.JobGeneration && response.ActualGeneration == request.ExpectedGeneration &&
			response.Outcome == "rejected" && !response.Accepted && response.Rejected && !response.Applied &&
			response.Error != nil && isVideoCoverGraphOrAssetError(response.Error.Code) && response.Actual.Readiness != VisualReadinessReady &&
			response.Actual.Error != nil && response.Actual.Error.Code == response.Error.Code
	default:
		return false
	}
}

func videoCoverRuntimeMatchesApplyRequest(actual VideoCoverRuntimeState, request EncoderVideoCoverApplyRequest) bool {
	if actual.Desired.Active != request.Active || actual.Desired.Revision != request.Revision {
		return false
	}
	if !request.Active {
		return request.CoverAsset == nil && actual.Desired.Source == "none" && actual.Desired.VariantID == "" && actual.CoverAsset == nil
	}
	return request.CoverAsset != nil && actual.CoverAsset != nil && actual.Desired.Source == "upload" &&
		actual.Desired.VariantID == request.CoverAsset.VariantID && equalMediaAssetDescriptor(*actual.CoverAsset, *request.CoverAsset)
}

func equalMediaAssetDescriptor(left, right MediaAssetDescriptor) bool {
	return left.AssetID == right.AssetID && left.VariantID == right.VariantID && left.Usage == right.Usage &&
		left.MediaType == right.MediaType && left.Width == right.Width && left.Height == right.Height &&
		left.ByteSize == right.ByteSize && left.PixelCount == right.PixelCount && left.Animated == right.Animated &&
		equalOptionalInt(left.AspectRatioErrorPPM, right.AspectRatioErrorPPM) && equalOptionalBool(left.Opaque, right.Opaque) &&
		left.SHA256 == right.SHA256 && left.Revision == right.Revision && left.Readiness == right.Readiness &&
		equalOptionalVisualSafeError(left.Error, right.Error)
}

func equalOptionalInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalBool(left, right *bool) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalVisualSafeError(left, right *VisualSafeError) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func validateVideoCoverRuntimeState(raw []byte, state VideoCoverRuntimeState) bool {
	object, ok := requiredJSONObject(raw, "stream_id", "job_generation", "generation", "capability", "readiness", "desired", "applied", "cover", "watermark", "pipeline", "no_automatic_resend")
	if !ok || !validVisualIdentifier(state.StreamID) || state.JobGeneration == 0 || state.Generation == 0 ||
		state.Capability != CapabilityLiveVideoCoverV1 || !validVisualReadiness(state.Readiness) || !state.NoAutomaticResend {
		return false
	}
	if !validateVideoCoverDesiredState(object["desired"], state.Desired) {
		return false
	}
	if !validateVideoCoverAppliedState(object["applied"], state.Applied) {
		return false
	}
	if !validateVideoVisualLayerState(object["cover"], state.Cover) {
		return false
	}
	if !validateVideoVisualLayerState(object["watermark"], state.Watermark) {
		return false
	}
	if !validateVisualPipeline(object["pipeline"], state.Pipeline) {
		return false
	}
	assetRaw, assetPresent := object["cover_asset"]
	if assetPresent != (state.CoverAsset != nil) {
		return false
	}
	if state.CoverAsset != nil {
		if !validMediaAssetDescriptorShape(assetRaw) || !validMediaAssetDescriptor(*state.CoverAsset) {
			return false
		}
	}
	if state.Desired.Active {
		if !assetPresent || state.CoverAsset.VariantID != state.Desired.VariantID {
			return false
		}
	} else if assetPresent {
		return false
	}
	errorRaw, errorPresent := object["error"]
	if errorPresent != (state.Error != nil) ||
		state.Error != nil && !validateVisualSafeErrorShape(errorRaw, *state.Error) {
		return false
	}
	if state.Readiness == VisualReadinessReady {
		if errorPresent || state.Applied.State != "known" {
			return false
		}
	} else if !errorPresent {
		return false
	}
	lastGoodRaw, lastGoodPresent := object["last_good_applied"]
	if lastGoodPresent != (state.LastGoodApplied != nil) {
		return false
	}
	if state.LastGoodApplied != nil {
		if state.LastGoodApplied.State != "known" || !validateVideoCoverAppliedState(lastGoodRaw, *state.LastGoodApplied) {
			return false
		}
	}
	witnessRaw, witnessPresent := object["applied_witness"]
	if witnessPresent != (state.AppliedWitness != nil) {
		return false
	}
	switch state.Applied.State {
	case "known":
		if state.AppliedWitness == nil || !validateVideoCoverAppliedWitness(witnessRaw, *state.AppliedWitness) || state.Applied.Active == nil ||
			state.Cover.Enabled != *state.Applied.Active || state.Cover.Revision != state.Applied.Revision || state.Cover.VariantID != state.Applied.VariantID ||
			state.AppliedWitness.Generation != state.Generation || state.AppliedWitness.Revision != state.Applied.Revision || state.AppliedWitness.Active != *state.Applied.Active ||
			!equalVideoLayer(state.AppliedWitness.Cover, state.Cover) || !equalVideoLayer(state.AppliedWitness.Watermark, state.Watermark) || !equalVisualPipeline(state.AppliedWitness.Pipeline, state.Pipeline) {
			return false
		}
		if state.Readiness == VisualReadinessReady &&
			(state.Desired.Active != *state.Applied.Active || state.Desired.Revision != state.Applied.Revision || state.Desired.VariantID != state.Applied.VariantID) {
			return false
		}
	case "unknown":
		if state.Readiness == VisualReadinessReady || witnessPresent || state.LastGoodApplied == nil {
			return false
		}
	default:
		return false
	}
	return true
}

func validateVisualPipeline(raw []byte, pipeline VisualPipelineInvariant) bool {
	object, ok := requiredJSONObject(raw, "layers", "watermark_topmost", "cover_watermark_independent", "output_parity", "audio_continuity")
	if !ok || !pipeline.WatermarkTopmost || !pipeline.CoverWatermarkIndependent ||
		!equalStrings(pipeline.Layers, []string{"base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"}) ||
		!equalStrings(pipeline.OutputParity, []string{"live", "archive", "preview"}) {
		return false
	}
	if _, ok = requiredJSONObject(object["audio_continuity"], "process_restart", "audio_encoder_restart", "audio_mux_restart", "graph_rebuild", "reconnect", "sequence_loss", "timestamp_discontinuity", "intentional_mute_insertion"); !ok {
		return false
	}
	return pipeline.AudioContinuity == (VisualAudioContinuity{})
}

func requiredJSONObject(raw []byte, fields ...string) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, false
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, false
		}
	}
	return object, true
}

func rejectDuplicateVideoCoverJSONFields(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeUniqueVideoCoverJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid video cover JSON")
	}
	return nil
}

func consumeUniqueVideoCoverJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid video cover JSON object key")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate video cover JSON field")
			}
			seen[key] = struct{}{}
			if err := consumeUniqueVideoCoverJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid video cover JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueVideoCoverJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid video cover JSON array")
		}
	default:
		return errors.New("invalid video cover JSON delimiter")
	}
	return nil
}

func isVideoCoverFenceError(code string) bool {
	switch code {
	case "stale_job_generation", "stale_cover_generation", "stale_cover_revision", "idempotency_conflict", "revision_payload_conflict":
		return true
	default:
		return false
	}
}

func isVideoCoverGraphOrAssetError(code string) bool {
	switch code {
	case "media_asset_format_unsupported", "media_asset_too_large", "media_asset_decode_failed", "media_asset_aspect_ratio_invalid",
		"media_asset_variant_processing", "media_asset_variant_failed", "media_asset_unauthorized", "media_asset_not_found",
		"media_asset_hash_mismatch", "media_asset_dimension_mismatch", "media_asset_timeout", "cover_graph_unavailable", "capability_required":
		return true
	default:
		return false
	}
}

func validateVideoCoverDesiredState(raw []byte, state VideoCoverDesiredState) bool {
	object, ok := requiredJSONObject(raw, "active", "revision", "source")
	if !ok || !validVideoCoverDesiredState(state) {
		return false
	}
	_, variantPresent := object["variant_id"]
	return variantPresent == state.Active
}

func validVideoCoverDesiredState(state VideoCoverDesiredState) bool {
	if state.Revision == 0 {
		return false
	}
	if state.Active {
		return (state.Source == "preset" || state.Source == "upload") && validVisualIdentifier(state.VariantID)
	}
	return state.Source == "none" && state.VariantID == ""
}

func validateVideoCoverAppliedState(raw []byte, state VideoCoverAppliedState) bool {
	object, ok := requiredJSONObject(raw, "state")
	if !ok || !validVideoCoverAppliedState(state) {
		return false
	}
	_, activePresent := object["active"]
	_, revisionPresent := object["revision"]
	_, variantPresent := object["variant_id"]
	if state.State == "unknown" {
		return !activePresent && !revisionPresent && !variantPresent
	}
	if !activePresent || !revisionPresent {
		return false
	}
	return variantPresent == (state.Active != nil && *state.Active)
}

func validVideoCoverAppliedState(state VideoCoverAppliedState) bool {
	switch state.State {
	case "known":
		if state.Active == nil || state.Revision == 0 {
			return false
		}
		if *state.Active {
			return validVisualIdentifier(state.VariantID)
		}
		return state.VariantID == ""
	case "unknown":
		return state.Active == nil && state.Revision == 0 && state.VariantID == ""
	default:
		return false
	}
}

func validateVideoVisualLayerState(raw []byte, state VideoVisualLayerState) bool {
	object, ok := requiredJSONObject(raw, "enabled", "revision")
	if !ok || !validVideoVisualLayerState(state) {
		return false
	}
	_, variantPresent := object["variant_id"]
	return variantPresent == state.Enabled
}

func validVideoVisualLayerState(state VideoVisualLayerState) bool {
	if state.Revision == 0 {
		return false
	}
	if state.Enabled {
		return validVisualIdentifier(state.VariantID)
	}
	return state.VariantID == ""
}

func validVideoCoverAppliedWitness(witness VideoCoverAppliedWitness) bool {
	return witness.GraphApplied && witness.Generation > 0 && witness.Revision > 0 &&
		validVideoVisualLayerState(witness.Cover) && validVideoVisualLayerState(witness.Watermark) &&
		witness.Active == witness.Cover.Enabled && witness.Revision == witness.Cover.Revision &&
		witness.Pipeline.WatermarkTopmost && witness.Pipeline.CoverWatermarkIndependent &&
		equalStrings(witness.Pipeline.Layers, []string{"base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"}) &&
		equalStrings(witness.Pipeline.OutputParity, []string{"live", "archive", "preview"}) &&
		witness.Pipeline.AudioContinuity == (VisualAudioContinuity{})
}

func validateVideoCoverAppliedWitness(raw []byte, witness VideoCoverAppliedWitness) bool {
	object, ok := requiredJSONObject(raw, "graph_applied", "generation", "revision", "active", "cover", "watermark", "pipeline")
	return ok && validVideoCoverAppliedWitness(witness) &&
		validateVideoVisualLayerState(object["cover"], witness.Cover) &&
		validateVideoVisualLayerState(object["watermark"], witness.Watermark) &&
		validateVisualPipeline(object["pipeline"], witness.Pipeline)
}

func validMediaAssetDescriptor(asset MediaAssetDescriptor) bool {
	if !validVisualIdentifier(asset.AssetID) || !validVisualIdentifier(asset.VariantID) || asset.Usage != "video_cover" ||
		(asset.MediaType != "image/png" && asset.MediaType != "image/jpeg" && asset.MediaType != "image/webp") ||
		asset.Width < 1 || asset.Width > 8192 || asset.Height < 1 || asset.Height > 8192 ||
		asset.ByteSize < 1 || asset.ByteSize > 20*1024*1024 || asset.PixelCount != int64(asset.Width)*int64(asset.Height) ||
		asset.PixelCount < 1 || asset.PixelCount > 40_000_000 || asset.Animated || asset.Revision == 0 ||
		asset.Readiness != VisualReadinessReady || asset.Error != nil || !validVisualSHA256(asset.SHA256) {
		return false
	}
	return asset.AspectRatioErrorPPM != nil && *asset.AspectRatioErrorPPM >= 0 && *asset.AspectRatioErrorPPM <= 1000 &&
		asset.Opaque != nil && *asset.Opaque
}

func validMediaAssetDescriptorShape(raw []byte) bool {
	object, ok := requiredJSONObject(raw, "asset_id", "variant_id", "usage", "media_type", "width", "height", "byte_size", "pixel_count", "animated", "sha256", "revision", "readiness", "aspect_ratio_error_ppm", "opaque")
	if !ok {
		return false
	}
	if errorRaw, exists := object["error"]; exists {
		_, ok = requiredJSONObject(errorRaw, "code")
		return ok
	}
	return true
}

func validVisualSafeError(safeError VisualSafeError) bool {
	if safeError.RequestID != "" && !validVisualIdentifier(safeError.RequestID) {
		return false
	}
	switch safeError.Code {
	case "invalid_theme_id", "media_asset_format_unsupported", "media_asset_too_large", "media_asset_decode_failed",
		"media_asset_aspect_ratio_invalid", "media_asset_variant_processing", "media_asset_variant_failed",
		"media_asset_unauthorized", "media_asset_not_found", "media_asset_hash_mismatch", "media_asset_dimension_mismatch",
		"media_asset_timeout", "discord_target_invalid", "preset_not_found", "preset_revision_conflict",
		"stale_job_generation", "stale_cover_generation", "stale_cover_revision", "idempotency_conflict",
		"cover_apply_ambiguous", "cover_graph_unavailable", "revision_payload_conflict", "capability_required":
		return true
	default:
		return false
	}
}

func validateVisualSafeErrorShape(raw []byte, safeError VisualSafeError) bool {
	object, ok := requiredJSONObject(raw, "code")
	if !ok || !validVisualSafeError(safeError) {
		return false
	}
	requestIDRaw, requestIDPresent := object["request_id"]
	if requestIDPresent != (safeError.RequestID != "") {
		return false
	}
	if requestIDPresent {
		var requestID string
		if err := json.Unmarshal(requestIDRaw, &requestID); err != nil || requestID != safeError.RequestID {
			return false
		}
	}
	return true
}

func validVisualReadiness(value string) bool {
	return value == "ready" || value == "not_ready" || value == "unknown"
}

func validVisualIdempotencyKey(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	if length < 1 || length > 128 {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if visualIdempotencyEdgeSpace(first) || visualIdempotencyEdgeSpace(last) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func visualIdempotencyEdgeSpace(character rune) bool {
	return unicode.IsSpace(character) || character == '\ufeff'
}

func validVisualIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || i > 0 && (r == '.' || r == '_' || r == ':' || r == '-') {
			continue
		}
		return false
	}
	return true
}

func validVisualSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func equalStrings(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

func equalVideoLayer(left, right VideoVisualLayerState) bool {
	return left.Enabled == right.Enabled && left.Revision == right.Revision && left.VariantID == right.VariantID
}

func equalVisualPipeline(left, right VisualPipelineInvariant) bool {
	return left.WatermarkTopmost == right.WatermarkTopmost && left.CoverWatermarkIndependent == right.CoverWatermarkIndependent &&
		equalStrings(left.Layers, right.Layers) && equalStrings(left.OutputParity, right.OutputParity) && left.AudioContinuity == right.AudioContinuity
}
