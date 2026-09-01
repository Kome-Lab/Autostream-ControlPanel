package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
)

func (s *Server) getStreamVisualSettings(w http.ResponseWriter, r *http.Request) {
	if s.streamVisual == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_visual_settings_unavailable"})
		return
	}
	settings, err := s.streamVisual.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStreamVisualError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateStreamVisualSettings(w http.ResponseWriter, r *http.Request) {
	if s.streamVisual == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_visual_settings_unavailable"})
		return
	}
	streamID := r.PathValue("id")
	unlockLifecycle := s.lockStreamLifecycle(streamID)
	defer unlockLifecycle()
	var update streamvisual.Update
	if decodeSingleJSONWithRequiredField(r, &update, "expected_revision") != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	settings, err := s.streamVisual.Update(r.Context(), streamID, current.User.ID, update)
	if err != nil {
		writeStreamVisualError(w, err)
		return
	}
	s.writeAudit(r, store.AuditEvent{Action: "streams.visual_settings.update", ResourceType: "stream", ResourceID: settings.StreamID, Result: "success", Metadata: map[string]any{"revision": settings.Revision, "background_mode": settings.BackgroundMode, "header_title_mode": settings.HeaderTitleMode, "discord_target_mode": settings.DiscordTargetMode, "discord_target_preset_id": settings.DiscordTargetPresetID, "discord_target_preset_revision": settings.DiscordTargetPresetRevision, "cover_source": settings.CoverSource, "cover_preset_id": settings.CoverPresetID, "cover_preset_revision": settings.CoverPresetRevision, "changed_fields": visualChangedFields(update)}})
	writeJSON(w, http.StatusOK, settings)
}

func visualChangedFields(update streamvisual.Update) []string {
	fields := []string{}
	if update.BackgroundMode.Set || update.BackgroundAssetID.Set || update.BackgroundVariantID.Set {
		fields = append(fields, "background")
	}
	if update.HeaderTitleMode.Set || update.HeaderTitleValue.Set {
		fields = append(fields, "header_title")
	}
	if update.DiscordTargetMode.Set || update.DiscordTargetPresetID.Set || update.DiscordTargetPresetRevision != nil || update.DiscordGuildID.Set || update.DiscordTextChannelID.Set || update.DiscordVoiceChannelID.Set {
		fields = append(fields, "discord_target")
	}
	if update.CoverSource.Set || update.CoverPresetID.Set || update.CoverAssetID.Set || update.CoverVariantID.Set || update.CoverStartActive != nil {
		fields = append(fields, "video_cover")
	}
	return fields
}

func writeStreamVisualError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "invalid_visual_settings"
	switch {
	case errors.Is(err, streamvisual.ErrNotFound), errors.Is(err, store.ErrNotFound), errors.Is(err, mediaassets.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, streamvisual.ErrRevisionConflict):
		status, code = http.StatusConflict, "revision_conflict"
	case errors.Is(err, streamvisual.ErrStreamStateLocked):
		status, code = http.StatusConflict, "stream_state_locked"
	case errors.Is(err, mediaassets.ErrForbidden):
		status, code = http.StatusForbidden, "asset_claim_forbidden"
	case errors.Is(err, streamvisual.ErrAssetClaim):
		status, code = http.StatusConflict, "asset_claim_failed"
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func (s *Server) controlPlatformReadinessIssues(ctx context.Context, streamID string, assignments []store.RegisteredService) ([]servicecall.ReadinessIssue, error) {
	if s.streamVisual == nil {
		return nil, nil
	}
	settings, err := s.streamVisual.Get(ctx, streamID)
	if errors.Is(err, streamvisual.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	assets, err := s.streamVisual.InspectAssets(ctx, settings)
	if err != nil {
		return nil, err
	}
	capabilities := streamvisual.RuntimeCapabilities{}
	for _, service := range assignments {
		switch service.ServiceType {
		case "worker":
			capabilities.SceneAppearance = reportedBool(service, "scene_appearance_v1")
		case "encoder_recorder":
			capabilities.VideoCover = reportedBool(service, "live_video_cover_v1")
		case "discord_bot":
			capabilities.DiscordTargetAccessible = true
		}
	}
	capabilities.VideoCoverAction = capabilities.VideoCover && s.videoCoverDispatcher != nil
	evaluated := streamvisual.EvaluateReadiness(settings, assets, capabilities)
	issues := make([]servicecall.ReadinessIssue, 0, len(evaluated))
	for _, issue := range evaluated {
		issues = append(issues, servicecall.ReadinessIssue{Code: issue.Code, Message: issue.Message})
	}
	return issues, nil
}

func reportedBool(service store.RegisteredService, key string) bool {
	value, ok := service.ReportedCapabilities[key]
	if !ok {
		return false
	}
	result, ok := value.(bool)
	return ok && result
}

func (s *Server) beginVideoCoverGeneration(ctx context.Context, streamID string) (videocover.State, error) {
	if s.videoCovers == nil {
		return videocover.State{}, nil
	}
	generation := uint64(1)
	if current, err := s.videoCovers.GetCurrentState(ctx, streamID); err == nil {
		generation = current.JobGeneration + 1
	} else if !errors.Is(err, videocover.ErrNotFound) {
		return videocover.State{}, err
	}
	variantID := ""
	desiredActive := false
	if s.streamVisual != nil {
		if settings, err := s.streamVisual.Get(ctx, streamID); err == nil {
			variantID = settings.CoverVariantID
			desiredActive = settings.CoverSource != "none" && settings.CoverStartActive
		} else if !errors.Is(err, streamvisual.ErrNotFound) {
			return videocover.State{}, err
		}
	}
	return s.videoCovers.EnsureGeneration(ctx, streamID, generation, variantID, desiredActive)
}

func (s *Server) materializeVisualStartSnapshot(ctx context.Context, streamID string, assignments []store.RegisteredService, generation videocover.State, request *servicecall.StartRequest) error {
	if request == nil || s.streamVisual == nil {
		return nil
	}
	settings, err := s.streamVisual.Get(ctx, streamID)
	if errors.Is(err, streamvisual.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if settings.DiscordSnapshotRevision == 0 {
		return streamvisual.ErrInvalidSettings
	}
	request.DiscordTargetRevision = settings.DiscordSnapshotRevision
	if settings.DiscordTargetMode == "manual" || settings.DiscordTargetMode == "preset" {
		request.DiscordGuildID = settings.DiscordGuildID
		request.DiscordTextChannelID = settings.DiscordTextChannelID
		request.DiscordVoiceChannelID = settings.DiscordVoiceChannelID
	}

	workerSupportsScene := false
	encoderSupportsCover := false
	for _, service := range assignments {
		switch service.ServiceType {
		case "worker":
			workerSupportsScene = reportedBool(service, servicecall.CapabilitySceneAppearanceV1)
		case "encoder_recorder":
			encoderSupportsCover = reportedBool(service, servicecall.CapabilityLiveVideoCoverV1)
		}
	}
	customScene := settings.BackgroundMode == "image" || settings.HeaderTitleMode == "custom"
	if customScene && !workerSupportsScene {
		return streamvisual.ErrInvalidSettings
	}
	// Revision zero identifies a legacy stream that has no v2 visual row. Its
	// default appearance stays on the exact omitted-field compatibility path.
	if workerSupportsScene && settings.Revision > 0 {
		appearance := &servicecall.SceneAppearance{
			Generation: generation.JobGeneration, Revision: settings.Revision,
			Capability: servicecall.CapabilitySceneAppearanceV1, Readiness: servicecall.VisualReadinessReady,
			BackgroundMode: settings.BackgroundMode, HeaderTitleMode: settings.HeaderTitleMode,
		}
		if settings.BackgroundMode == "image" {
			appearance.Background, err = s.visualAssetDescriptor(ctx, streamID, settings.BackgroundAssetID, settings.BackgroundVariantID, "scene_background")
			if err != nil {
				return err
			}
		}
		if settings.HeaderTitleMode == "custom" {
			appearance.CustomTitle = settings.HeaderTitleValue
		}
		request.SceneAppearance = appearance
	}

	coverConfigured := settings.CoverSource != "none"
	if coverConfigured && !encoderSupportsCover {
		return streamvisual.ErrInvalidSettings
	}
	if encoderSupportsCover {
		if generation.JobGeneration == 0 || generation.DesiredRevision == 0 {
			return videocover.ErrInvalidRequest
		}
		start := &servicecall.VideoCoverStartSnapshot{
			JobGeneration: generation.JobGeneration, Revision: generation.DesiredRevision,
			Active:         generation.DesiredActive,
			IdempotencyKey: "stream-start:" + strconv.FormatUint(generation.JobGeneration, 10) + ":" + strconv.FormatUint(generation.DesiredRevision, 10),
		}
		if start.Active {
			start.CoverAsset, err = s.visualAssetDescriptor(ctx, streamID, settings.CoverAssetID, settings.CoverVariantID, "video_cover")
			if err != nil {
				return err
			}
		}
		request.VideoCoverStart = start
	}
	return nil
}

func (s *Server) visualAssetDescriptor(ctx context.Context, streamID, expectedAssetID, variantID, usage string) (*servicecall.MediaAssetDescriptor, error) {
	if s.mediaAssets == nil {
		return nil, mediaassets.ErrNotFound
	}
	internal, err := s.mediaAssets.OpenInternalVariant(ctx, streamID, variantID)
	if err != nil {
		return nil, err
	}
	defer internal.Reader.Close()
	if internal.Asset.ID != expectedAssetID || internal.Variant.ID != variantID || internal.Variant.AssetID != expectedAssetID ||
		internal.Asset.UsageType != usage || internal.Variant.Status != "ready" || internal.Variant.ProcessorRevision < 1 {
		return nil, mediaassets.ErrIntegrity
	}
	descriptor := &servicecall.MediaAssetDescriptor{
		AssetID: internal.Asset.ID, VariantID: internal.Variant.ID, Usage: usage,
		MediaType: internal.Variant.MediaType, Width: internal.Variant.Width, Height: internal.Variant.Height,
		ByteSize: internal.Variant.ByteSize, PixelCount: int64(internal.Variant.Width) * int64(internal.Variant.Height),
		Animated: false, SHA256: strings.ToLower(internal.Variant.SHA256),
		Revision: uint64(internal.Variant.ProcessorRevision), Readiness: servicecall.VisualReadinessReady,
	}
	if usage == "video_cover" {
		opaque := internal.Variant.Opaque
		descriptor.Opaque = &opaque
		denominator := int64(internal.Variant.Height) * 16
		delta := int64(internal.Variant.Width)*9 - int64(internal.Variant.Height)*16
		if delta < 0 {
			delta = -delta
		}
		aspectPPM := 0
		if denominator > 0 {
			aspectPPM = int(delta * 1_000_000 / denominator)
		}
		descriptor.AspectRatioErrorPPM = &aspectPPM
	}
	return descriptor, nil
}

type videoCoverPresetRequest struct {
	Name             string `json:"name"`
	AssetID          string `json:"asset_id"`
	AssetVariantID   string `json:"asset_variant_id"`
	Enabled          bool   `json:"enabled"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

func (s *Server) listVideoCoverPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := s.videoCovers.ListPresets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_video_cover_presets_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": presets})
}
func (s *Server) getVideoCoverPreset(w http.ResponseWriter, r *http.Request) {
	preset, err := s.videoCovers.GetPreset(r.Context(), r.PathValue("id"), false)
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preset)
}
func (s *Server) createVideoCoverPreset(w http.ResponseWriter, r *http.Request) {
	var body videoCoverPresetRequest
	if decodeSingleJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	preset, err := s.videoCovers.CreatePreset(r.Context(), videocover.Preset{Name: body.Name, AssetID: body.AssetID, AssetVariantID: body.AssetVariantID, Enabled: body.Enabled, CreatedByUserID: current.User.ID})
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	s.writeVideoCoverPresetAudit(r, "video_cover_presets.create", preset, []string{"name", "asset_id", "asset_variant_id", "enabled"})
	writeJSON(w, http.StatusCreated, preset)
}
func (s *Server) updateVideoCoverPreset(w http.ResponseWriter, r *http.Request) {
	var body videoCoverPresetRequest
	if decodeSingleJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	preset, err := s.videoCovers.UpdatePreset(r.Context(), r.PathValue("id"), videocover.Preset{Name: body.Name, AssetID: body.AssetID, AssetVariantID: body.AssetVariantID, Enabled: body.Enabled, UpdatedByUserID: current.User.ID}, body.ExpectedRevision)
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	s.writeVideoCoverPresetAudit(r, "video_cover_presets.update", preset, []string{"name", "asset_id", "asset_variant_id", "enabled"})
	writeJSON(w, http.StatusOK, preset)
}
func (s *Server) deleteVideoCoverPreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedRevision uint64 `json:"expected_revision"`
	}
	if decodeSingleJSON(r, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	preset, err := s.videoCovers.DeletePreset(r.Context(), r.PathValue("id"), current.User.ID, body.ExpectedRevision)
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	s.writeVideoCoverPresetAudit(r, "video_cover_presets.delete", preset, []string{"deleted_at"})
	writeJSON(w, http.StatusOK, preset)
}
func (s *Server) writeVideoCoverPresetAudit(r *http.Request, action string, preset videocover.Preset, changed []string) {
	s.writeAudit(r, store.AuditEvent{Action: action, ResourceType: "video_cover_preset", ResourceID: preset.ID, Result: "success", Metadata: map[string]any{"preset_id": preset.ID, "revision": preset.Revision, "asset_id": preset.AssetID, "asset_variant_id": preset.AssetVariantID, "changed_fields": changed}})
}

func (s *Server) getVideoCoverState(w http.ResponseWriter, r *http.Request) {
	state, err := s.videoCovers.GetCurrentState(r.Context(), r.PathValue("id"))
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) updateVideoCoverState(w http.ResponseWriter, r *http.Request) {
	var request videocover.ActionRequest
	if decodeSingleJSON(r, &request) != nil || videocover.ValidateRequest(request) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_video_cover_request"})
		return
	}
	current := currentFromContext(r.Context())
	required, action := "streams.show_cover", "streams.video_cover.show"
	if !request.Active {
		required, action = "streams.hide_cover", "streams.video_cover.hide"
	}
	if !security.HasPermission(current.Permissions, required) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	// An already-recorded action is an immutable idempotency result. Resolve any
	// historical exact replay before consulting mutable runtime capability or
	// asset health, while still re-evaluating the caller's permission above.
	coverState, err := s.videoCovers.GetCurrentState(r.Context(), r.PathValue("id"))
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	replay, replayFound, replayErr := s.videoCovers.LookupActionReplay(r.Context(), r.PathValue("id"), request)
	if replayErr != nil {
		writeVideoCoverError(w, replayErr)
		return
	}
	if replayFound {
		s.serveVideoCoverReplay(w, r, action, request, replay)
		return
	}
	assignments, err := s.streamAssignments(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	encoder, found := primaryVideoCoverEncoder(assignments)
	if !found || !reportedBool(encoder, "live_video_cover_v1") {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "video_cover_capability_unavailable"})
		return
	}
	if s.videoCoverDispatcher == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "video_cover_action_unavailable"})
		return
	}
	if coverState.JobGeneration != request.ExpectedJobGeneration {
		writeVideoCoverError(w, videocover.ErrStaleGeneration)
		return
	}
	if s.streamVisual == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "video_cover_action_unavailable"})
		return
	}
	settings, err := s.streamVisual.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "video_cover_variant_unavailable"})
		return
	}
	if settings.CoverSource == "none" || strings.TrimSpace(settings.CoverVariantID) == "" || settings.CoverVariantID != coverState.AssetVariantID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "video_cover_variant_unavailable"})
		return
	}
	assets, err := s.streamVisual.InspectAssets(r.Context(), settings)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "video_cover_asset_check_failed"})
		return
	}
	if !assets.CoverVariantReady {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "video_cover_variant_unavailable"})
		return
	}
	if !assets.MediaAssetIntegrity {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "media_asset_integrity"})
		return
	}
	prepared, err := s.videoCovers.PrepareAction(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	if prepared.Replay {
		s.serveVideoCoverReplay(w, r, action, request, prepared)
		return
	}
	var coverAsset *servicecall.MediaAssetDescriptor
	if request.Active {
		coverAsset, err = s.visualAssetDescriptor(r.Context(), r.PathValue("id"), settings.CoverAssetID, settings.CoverVariantID, "video_cover")
		if err != nil {
			if _, err = s.videoCovers.RecordFailed(r.Context(), r.PathValue("id"), request.ExpectedJobGeneration, request.IdempotencyKey, "media_asset_integrity"); err != nil {
				writeVideoCoverError(w, err)
				return
			}
			recorded, status, resultText, outcomeOK := s.recordedVideoCoverOutcome(r.Context(), r.PathValue("id"), request)
			if !outcomeOK {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "video_cover_action_outcome_unavailable"})
				return
			}
			s.writeVideoCoverActionAudit(r, action, request, recorded, resultText, false)
			writeJSON(w, status, recorded.State)
			return
		}
	}
	result := s.videoCoverDispatcher.DispatchVideoCover(r.Context(), encoder, servicecall.VideoCoverDispatchRequest{
		StreamID: r.PathValue("id"), JobGeneration: request.ExpectedJobGeneration, Revision: prepared.RequestedRevision,
		Active: request.Active, AssetVariantID: prepared.State.AssetVariantID, IdempotencyKey: request.IdempotencyKey,
		CoverAsset: coverAsset, HideConfirmed: request.HideConfirmed,
	})
	if result.Ambiguous {
		_, err = s.videoCovers.RecordAmbiguous(r.Context(), r.PathValue("id"), request.ExpectedJobGeneration, request.IdempotencyKey)
	} else if result.Applied {
		_, err = s.videoCovers.RecordApplied(r.Context(), r.PathValue("id"), request.ExpectedJobGeneration, request.IdempotencyKey, request.Active, prepared.RequestedRevision)
	} else {
		_, err = s.videoCovers.RecordFailed(r.Context(), r.PathValue("id"), request.ExpectedJobGeneration, request.IdempotencyKey, result.SafeErrorCode)
	}
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	recorded, status, resultText, outcomeOK := s.recordedVideoCoverOutcome(r.Context(), r.PathValue("id"), request)
	if !outcomeOK {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "video_cover_action_outcome_unavailable"})
		return
	}
	s.writeVideoCoverActionAudit(r, action, request, recorded, resultText, false)
	writeJSON(w, status, recorded.State)
}

func (s *Server) serveVideoCoverReplay(w http.ResponseWriter, r *http.Request, action string, request videocover.ActionRequest, prepared videocover.PreparedAction) {
	if !prepared.Replay || prepared.Dispatch {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "idempotency_conflict"})
		return
	}
	if prepared.Outcome != "confirming" || prepared.State.LastIdempotencyKey != request.IdempotencyKey || prepared.State.Status != "confirming" {
		writeJSON(w, http.StatusOK, prepared.State)
		return
	}
	reconciler, ok := s.videoCoverDispatcher.(servicecall.VideoCoverReconciler)
	if !ok {
		writeJSON(w, http.StatusAccepted, prepared.State)
		return
	}
	assignments, err := s.streamAssignments(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	encoder, found := primaryVideoCoverEncoder(assignments)
	if !found || !reportedBool(encoder, servicecall.CapabilityLiveVideoCoverV1) {
		writeJSON(w, http.StatusAccepted, prepared.State)
		return
	}
	result := reconciler.ReconcileVideoCover(r.Context(), encoder, servicecall.VideoCoverReconcileRequest{
		StreamID: r.PathValue("id"), JobGeneration: request.ExpectedJobGeneration,
		Revision: prepared.RequestedRevision, Active: request.Active,
		AssetVariantID: prepared.State.AssetVariantID,
	})
	switch {
	case result.Applied:
		_, err = s.videoCovers.RecordApplied(r.Context(), r.PathValue("id"), request.ExpectedJobGeneration, request.IdempotencyKey, request.Active, prepared.RequestedRevision)
	case result.Ambiguous:
		_, err = s.videoCovers.RecordAmbiguous(r.Context(), r.PathValue("id"), request.ExpectedJobGeneration, request.IdempotencyKey)
	case result.SafeErrorCode != "":
		_, err = s.videoCovers.RecordFailed(r.Context(), r.PathValue("id"), request.ExpectedJobGeneration, request.IdempotencyKey, result.SafeErrorCode)
	}
	if err != nil {
		writeVideoCoverError(w, err)
		return
	}
	recorded, status, resultText, outcomeOK := s.recordedVideoCoverOutcome(r.Context(), r.PathValue("id"), request)
	if !outcomeOK {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "video_cover_action_outcome_unavailable"})
		return
	}
	s.writeVideoCoverActionAudit(r, action, request, recorded, resultText, true)
	writeJSON(w, status, recorded.State)
}

func (s *Server) recordedVideoCoverOutcome(ctx context.Context, streamID string, request videocover.ActionRequest) (videocover.PreparedAction, int, string, bool) {
	recorded, found, err := s.videoCovers.LookupActionOutcome(ctx, streamID, request)
	if err != nil || !found || !recorded.Replay || recorded.Dispatch {
		return videocover.PreparedAction{}, 0, "", false
	}
	switch recorded.Outcome {
	case "applied":
		return recorded, http.StatusOK, "success", true
	case "confirming":
		return recorded, http.StatusAccepted, "failure", true
	case "failed":
		return recorded, http.StatusBadGateway, "failure", true
	default:
		return videocover.PreparedAction{}, 0, "", false
	}
}

func (s *Server) writeVideoCoverActionAudit(r *http.Request, action string, request videocover.ActionRequest, recorded videocover.PreparedAction, resultText string, reconciled bool) {
	var appliedRevision any
	if recorded.Outcome == "applied" {
		appliedRevision = recorded.RequestedRevision
	}
	metadata := map[string]any{
		"job_generation":   request.ExpectedJobGeneration,
		"desired_active":   request.Active,
		"desired_revision": recorded.RequestedRevision,
		"applied_revision": appliedRevision,
		"status":           recorded.Outcome,
		"error_code":       recorded.SafeErrorCode,
	}
	if reconciled {
		metadata["reconciled"] = true
	}
	s.writeAudit(r, store.AuditEvent{Action: action, ResourceType: "stream", ResourceID: r.PathValue("id"), Result: resultText, Metadata: metadata})
}

func primaryVideoCoverEncoder(assignments []store.RegisteredService) (store.RegisteredService, bool) {
	for _, service := range assignments {
		if service.ServiceType == "encoder_recorder" && (strings.TrimSpace(service.AssignmentRole) == "" || strings.EqualFold(strings.TrimSpace(service.AssignmentRole), "primary")) {
			return service, true
		}
	}
	return store.RegisteredService{}, false
}

func writeVideoCoverError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "invalid_video_cover_request"
	switch {
	case errors.Is(err, videocover.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, videocover.ErrStaleGeneration):
		status, code = http.StatusConflict, "stale_job_generation"
	case errors.Is(err, videocover.ErrRevisionConflict):
		status, code = http.StatusConflict, "revision_conflict"
	case errors.Is(err, videocover.ErrIdempotencyConflict):
		status, code = http.StatusConflict, "idempotency_conflict"
	}
	writeJSON(w, status, map[string]string{"code": code})
}

// createStreamVisualEnvelope is decoded separately from the legacy stream
// settings so draft claim and visual persistence can share one transaction.
type createStreamVisualEnvelope struct {
	UploadSessionID string          `json:"upload_session_id,omitempty"`
	VisualSettings  json.RawMessage `json:"visual_settings,omitempty"`
}
