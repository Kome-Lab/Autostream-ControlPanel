package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

const streamPreviewLinkTTL = 12 * time.Hour

func (s *Server) streamPreviewAsset(w http.ResponseWriter, r *http.Request) {
	s.writeStreamPreviewAsset(w, r, strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("name")), false)
}

func (s *Server) createStreamPreviewLink(w http.ResponseWriter, r *http.Request) {
	streamID := strings.TrimSpace(r.PathValue("id"))
	stream, assignments, ok := s.prepareActiveStreamPreview(w, r, streamID, false)
	if !ok {
		return
	}
	if _, ok := s.dispatcher.(previewServiceDispatcher); !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_not_supported"})
		return
	}
	if strings.TrimSpace(s.previewSigningKey) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_signing_key_required"})
		return
	}
	now := time.Now().UTC()
	// Round expiry to an hourly bucket. Together with the compact token this
	// makes repeated details-panel opens return the same preview capability
	// while keeping the maximum lifetime bounded by the normal preview TTL.
	expiresAt := now.Add(streamPreviewLinkTTL).Truncate(time.Hour)
	token, err := ingesttoken.IssuePreview(s.previewSigningKey, stream.ID, expiresAt.Unix())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stream_preview_link_failed"})
		return
	}
	playbackPath := "/stream-previews/" + url.PathEscape(token) + "/index.m3u8"
	playerPath := "/stream-preview/?token=" + url.QueryEscape(token)
	playbackURL := configuredStreamPreviewURL(playbackPath)
	playerURL := configuredStreamPreviewURL(playerPath)
	videoOverlayBurnIn := s.streamVideoOverlayBurnIn(r.Context(), stream.ID)
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.preview_link.create", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"expires_at": expiresAt, "assignment_count": len(assignments)}})
	w.Header().Set("Cache-Control", "no-store")
	// Keep url as the legacy HLS URL for API compatibility. New callers should
	// display/copy player_url; playback_url is explicit for embedded players.
	writeJSON(w, http.StatusCreated, map[string]any{
		"stream_id":             stream.ID,
		"url":                   playbackURL,
		"playback_url":          playbackURL,
		"player_url":            playerURL,
		"expires_at":            expiresAt,
		"video_overlay_burn_in": videoOverlayBurnIn,
	})
}

func configuredStreamPreviewURL(path string) string {
	publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")), "/")
	parsed, err := url.Parse(publicURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return path
	}
	return publicURL + path
}

func (s *Server) publicStreamPreviewAsset(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.previewSigningKey) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_unavailable"})
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	streamID, _, compactErr := ingesttoken.VerifyPreview(s.previewSigningKey, token, time.Now().UTC())
	if compactErr == nil {
		s.writeStreamPreviewAsset(w, r, streamID, strings.TrimSpace(r.PathValue("name")), true)
		return
	}
	// Accept the previous long ingest-token format until old links expire.
	claims, err := ingesttoken.Verify(s.previewSigningKey, token, ingesttoken.Expected{
		ServiceID:   "control-panel",
		ServiceType: "control_panel",
		Purpose:     "stream_preview",
		Audience:    "external_player",
		Now:         time.Now().UTC(),
	})
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_preview_not_found"})
		return
	}
	s.writeStreamPreviewAsset(w, r, claims.StreamID, strings.TrimSpace(r.PathValue("name")), true)
}

type publicPreviewParticipant struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	IsBot       bool   `json:"is_bot,omitempty"`
	Speaking    bool   `json:"speaking,omitempty"`
}

type publicPreviewParticipantsResponse struct {
	Participants       []publicPreviewParticipant `json:"participants"`
	ActiveSpeakerID    string                     `json:"active_speaker_id,omitempty"`
	UpdatedAt          time.Time                  `json:"updated_at,omitempty"`
	VideoOverlayBurnIn bool                       `json:"video_overlay_burn_in"`
}

// publicStreamPreviewParticipants exposes only the non-secret participant
// snapshot carried by worker overlay events. The same short-lived preview
// bearer token that authorizes HLS assets authorizes this endpoint; no service
// token or Discord credential is exposed to the browser.
func (s *Server) publicStreamPreviewParticipants(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.previewSigningKey) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_unavailable"})
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	streamID, ok := s.publicPreviewStreamID(token)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_preview_not_found"})
		return
	}
	stream, assignments, ok := s.prepareActiveStreamPreview(w, r, streamID, true)
	if !ok {
		return
	}
	dispatcher, ok := s.dispatcher.(serviceDispatcher)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_not_supported"})
		return
	}
	result := servicecall.RedactWorkerEventsResult(dispatcher.WorkerEvents(r.Context(), stream, assignments))
	if !result.Success {
		status := http.StatusBadGateway
		if result.StatusCode == http.StatusConflict {
			status = http.StatusGone
		}
		writeJSON(w, status, map[string]string{"code": "stream_preview_participants_unavailable"})
		return
	}
	response := publicPreviewParticipantsFromEvents(result.Events)
	response.VideoOverlayBurnIn = s.streamVideoOverlayBurnIn(r.Context(), stream.ID)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) streamVideoOverlayBurnIn(ctx context.Context, streamID string) bool {
	mediaRuntimeStore, ok := s.streams.(store.StreamMediaRuntimeStore)
	if !ok {
		return false
	}
	runtime, err := mediaRuntimeStore.GetStreamMediaRuntime(ctx, streamID)
	if err != nil {
		// Missing/unknown must preserve the legacy DOM overlay. Never infer this
		// value from mutable current service capabilities after a restart.
		return false
	}
	return runtime.VideoOverlayBurnIn
}

func (s *Server) publicPreviewStreamID(token string) (string, bool) {
	if streamID, _, err := ingesttoken.VerifyPreview(s.previewSigningKey, token, time.Now().UTC()); err == nil {
		return streamID, true
	}
	claims, err := ingesttoken.Verify(s.previewSigningKey, token, ingesttoken.Expected{
		ServiceID:   "control-panel",
		ServiceType: "control_panel",
		Purpose:     "stream_preview",
		Audience:    "external_player",
		Now:         time.Now().UTC(),
	})
	if err != nil {
		return "", false
	}
	return claims.StreamID, true
}

func publicPreviewParticipantsFromEvents(events []servicecall.WorkerEvent) publicPreviewParticipantsResponse {
	response := publicPreviewParticipantsResponse{Participants: []publicPreviewParticipant{}}
	ordered := append([]servicecall.WorkerEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Timestamp.IsZero() || ordered[j].Timestamp.IsZero() {
			return false
		}
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})
	for _, event := range ordered {
		if event.Timestamp.After(response.UpdatedAt) {
			response.UpdatedAt = event.Timestamp
		}
		switch event.Type {
		case "overlay.participants":
			var participants []publicPreviewParticipant
			encoded, err := json.Marshal(event.Payload["participants"])
			if err != nil || json.Unmarshal(encoded, &participants) != nil {
				continue
			}
			response.Participants = participants
			response.ActiveSpeakerID = ""
			for _, participant := range participants {
				if participant.Speaking {
					response.ActiveSpeakerID = participant.UserID
					break
				}
			}
		case "overlay.active_speaker":
			userID, _ := event.Payload["user_id"].(string)
			speaking := true
			if rawSpeaking, ok := event.Payload["speaking"].(bool); ok {
				speaking = rawSpeaking
			}
			if speaking {
				response.ActiveSpeakerID = strings.TrimSpace(userID)
			} else if response.ActiveSpeakerID == strings.TrimSpace(userID) || strings.TrimSpace(userID) == "" {
				response.ActiveSpeakerID = ""
			}
		}
	}
	found := false
	for index := range response.Participants {
		response.Participants[index].Speaking = response.ActiveSpeakerID != "" && response.Participants[index].UserID == response.ActiveSpeakerID
		if response.Participants[index].Speaking {
			found = true
		}
	}
	if !found {
		response.ActiveSpeakerID = ""
	}
	return response
}

func (s *Server) writeStreamPreviewAsset(w http.ResponseWriter, r *http.Request, streamID, name string, public bool) {
	if !validStreamPreviewAssetName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_preview_asset"})
		return
	}
	if public {
		// Network preview links are bearer URLs intended for an external player.
		// HLS.js fetches both the playlist and its relative segments from the
		// browser, so the public capability endpoint must opt into cross-origin
		// reads without relying on session credentials.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range, Content-Type")
	}
	stream, assignments, ok := s.prepareActiveStreamPreview(w, r, streamID, public)
	if !ok {
		return
	}
	dispatcher, ok := s.dispatcher.(previewServiceDispatcher)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_not_supported"})
		return
	}
	byteRange := ""
	if name != "index.m3u8" {
		byteRange = r.Header.Get("Range")
	}
	result := dispatcher.PreviewAsset(r.Context(), stream, assignments, name, byteRange)
	if !result.Success {
		status := http.StatusBadGateway
		code := "stream_preview_fetch_failed"
		if result.StatusCode == http.StatusNotFound {
			status = http.StatusNotFound
			code = "stream_preview_not_ready"
		} else if result.StatusCode == http.StatusConflict {
			status = http.StatusConflict
			code = "stream_preview_not_active"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if name != "index.m3u8" && result.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		contentRange := validatedPreviewUnsatisfiedContentRange(result.ContentRange)
		if contentRange == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "invalid_stream_preview_range"})
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=30")
		w.Header().Set("Content-Range", contentRange)
		if strings.EqualFold(strings.TrimSpace(result.AcceptRanges), "bytes") {
			w.Header().Set("Accept-Ranges", "bytes")
		}
		w.Header().Set("Content-Length", "0")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	body := result.Body
	if name == "index.m3u8" {
		var valid bool
		body, valid = validatedStreamPreviewPlaylist(body)
		if !valid {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "invalid_stream_preview_playlist"})
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "private, max-age=30")
		if strings.EqualFold(strings.TrimSpace(result.AcceptRanges), "bytes") {
			w.Header().Set("Accept-Ranges", "bytes")
		}
		if result.StatusCode == http.StatusPartialContent {
			contentRange := validatedPreviewContentRange(result.ContentRange, len(body))
			if contentRange == "" {
				writeJSON(w, http.StatusBadGateway, map[string]string{"code": "invalid_stream_preview_range"})
				return
			}
			w.Header().Set("Content-Range", contentRange)
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Referrer-Policy", "no-referrer")
	status := http.StatusOK
	if name != "index.m3u8" && result.StatusCode == http.StatusPartialContent {
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func validatedPreviewContentRange(value string, bodyLength int) string {
	value = strings.TrimSpace(value)
	if bodyLength < 0 || !strings.HasPrefix(value, "bytes ") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes "), "/")
	if len(parts) != 2 || parts[1] == "" {
		return ""
	}
	rangeParts := strings.Split(parts[0], "-")
	if len(rangeParts) != 2 {
		return ""
	}
	start, startErr := strconv.ParseInt(rangeParts[0], 10, 64)
	end, endErr := strconv.ParseInt(rangeParts[1], 10, 64)
	total, totalErr := strconv.ParseInt(parts[1], 10, 64)
	if startErr != nil || endErr != nil || totalErr != nil || start < 0 || end < start || total <= end || end-start+1 != int64(bodyLength) {
		return ""
	}
	return "bytes " + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end, 10) + "/" + strconv.FormatInt(total, 10)
}

func validatedPreviewUnsatisfiedContentRange(value string) string {
	value = strings.TrimSpace(value)
	const prefix = "bytes */"
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	total, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
	if err != nil || total < 0 {
		return ""
	}
	return prefix + strconv.FormatInt(total, 10)
}

func (s *Server) prepareActiveStreamPreview(w http.ResponseWriter, r *http.Request, streamID string, public bool) (store.Stream, []store.RegisteredService, bool) {
	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.Stream{}, nil, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return store.Stream{}, nil, false
	}
	if !isStreamPreviewActive(stream.Status) {
		status := http.StatusConflict
		if public {
			status = http.StatusGone
		}
		writeJSON(w, status, map[string]string{"code": "stream_preview_not_active"})
		return store.Stream{}, nil, false
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return store.Stream{}, nil, false
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return store.Stream{}, nil, false
	}
	return stream, primaryAssignments, true
}

func isStreamPreviewActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping":
		return true
	default:
		return false
	}
}

func validStreamPreviewAssetName(name string) bool {
	if name == "index.m3u8" {
		return true
	}
	if len(name) != len("segment-000000.ts") || !strings.HasPrefix(name, "segment-") || !strings.HasSuffix(name, ".ts") {
		return false
	}
	for _, char := range strings.TrimSuffix(strings.TrimPrefix(name, "segment-"), ".ts") {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

const maxPreviewPlaylistLines = 32 << 10

func validatedStreamPreviewPlaylist(body []byte) ([]byte, bool) {
	if len(body) == 0 || len(body) > 1<<20 || strings.ContainsRune(string(body), '\x00') {
		return nil, false
	}
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > maxPreviewPlaylistLines {
		return nil, false
	}
	firstContent := ""
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		for _, char := range line {
			if char < 0x20 || (char >= 0x7f && char <= 0x9f) {
				return nil, false
			}
		}
		if firstContent == "" {
			firstContent = line
		}
		if strings.HasPrefix(line, "#") {
			if strings.Contains(strings.ToUpper(line), "URI=") {
				return nil, false
			}
			continue
		}
		if !validStreamPreviewAssetName(line) || line == "index.m3u8" {
			return nil, false
		}
	}
	if firstContent != "#EXTM3U" {
		return nil, false
	}
	return []byte(strings.TrimRight(normalized, "\n") + "\n"), true
}
