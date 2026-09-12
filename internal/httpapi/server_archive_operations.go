package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

var requiredRetryUploadServiceTypes = []string{"encoder_recorder"}

func (s *Server) registerArchivePreviewRoutes() {
	s.mux.HandleFunc("GET /streams/{id}/artifacts", s.requirePermission("archives.read", s.streamArtifacts))
	s.mux.HandleFunc("GET /streams/{id}/artifacts/{artifact_id}/download", s.requirePermission("archives.download", s.downloadStreamArtifact))
	s.mux.HandleFunc("GET /streams/{id}/artifacts/{artifact_id}/shares", s.requirePermission("archives.read", s.listStreamArtifactShares))
	s.mux.HandleFunc("POST /streams/{id}/artifacts/{artifact_id}/shares", s.requirePermission("archives.download", s.createStreamArtifactShare))
	s.mux.HandleFunc("DELETE /streams/{id}/artifacts/{artifact_id}/shares/{share_id}", s.requirePermission("archives.delete", s.revokeStreamArtifactShare))
	s.mux.HandleFunc("DELETE /streams/{id}/artifacts/{artifact_id}", s.requirePermission("archives.delete", s.deleteStreamArtifact))
	s.mux.HandleFunc("PUT /streams/{id}/artifacts/{artifact_id}", s.requirePermission("archives.delete", s.renameStreamArtifact))
	s.mux.HandleFunc("GET /archive-shares/{token}", s.publicArchiveShare)
	s.mux.HandleFunc("GET /archive-shares/{token}/download", s.downloadPublicArchiveShare)
	s.mux.HandleFunc("GET /stream-previews/{token}/{name}", s.publicStreamPreviewAsset)
	s.mux.HandleFunc("GET /stream-previews/{token}/participants", s.publicStreamPreviewParticipants)
}

func (s *Server) retryUpload(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"missing_service_types": missing}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	archiveConfig, err := s.retryArchiveConfig(r.Context(), stream)
	if err != nil {
		code := archiveConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "archive_profile_id": stream.ArchiveProfileID}})
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	stream, err = s.beginStreamArchiveRetryGuarded(r.Context(), stream, primaryAssignments)
	if err != nil {
		code, status := archiveRetryGuardHTTPError(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	results := s.dispatcher.RetryArchiveUpload(r.Context(), stream, primaryAssignments, archiveConfig)
	results = sanitizeDispatchResults(results)
	if hasDispatchFailure(results) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"dispatch": results}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": results})
		return
	}
	logEntry, err := s.streams.RetryArchiveUpload(r.Context(), stream.ID, current.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "retry_upload_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"dispatch": results}})
	writeJSON(w, http.StatusAccepted, map[string]any{"log": logEntry, "dispatch": results})
}

func (s *Server) streamArtifacts(w http.ResponseWriter, r *http.Request) {
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return
	}
	writeJSON(w, http.StatusOK, artifacts)
}

func (s *Server) downloadStreamArtifact(w http.ResponseWriter, r *http.Request) {
	stream, artifact, assignments, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	result := s.dispatcher.DownloadArchiveArtifact(r.Context(), stream, assignments, artifact, r.Header.Get("Range"))
	if !result.Success || result.Body == nil {
		if result.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			if contentRange := validatedPreviewUnsatisfiedContentRange(result.ContentRange); contentRange != "" {
				w.Header().Set("Content-Range", contentRange)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_download_failed", "dispatch": sanitizeArchiveDownloadResult(result)})
		return
	}
	defer result.Body.Close()
	fileName := artifact.Name
	if result.FileName != "" {
		fileName = result.FileName
	}
	contentType := result.ContentType
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition+`; filename="`+sanitizeDownloadFileName(fileName)+`"`)
	if strings.EqualFold(strings.TrimSpace(result.AcceptRanges), "bytes") {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	status := http.StatusOK
	if result.StatusCode == http.StatusPartialContent {
		if contentRange := validatedPreviewContentRange(result.ContentRange, int(result.SizeBytes)); contentRange != "" {
			w.Header().Set("Content-Range", contentRange)
			status = http.StatusPartialContent
		}
	}
	if result.SizeBytes >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(result.SizeBytes, 10))
	}
	current := currentFromContext(r.Context())
	if disposition == "attachment" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.download", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "artifact_name": artifact.Name}})
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	_, _ = io.Copy(w, result.Body)
}

func (s *Server) listStreamArtifactShares(w http.ResponseWriter, r *http.Request) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return
	}
	stream, artifact, _, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	shares, err := shareStore.ListStreamArtifactShares(r.Context(), stream.ID, artifact.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_archive_shares_failed"})
		return
	}
	out := make([]map[string]any, 0, len(shares))
	for _, share := range shares {
		out = append(out, publicArchiveShareAdmin(share))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createStreamArtifactShare(w http.ResponseWriter, r *http.Request) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return
	}
	stream, artifact, _, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpiresAt      string `json:"expires_at"`
		ExpiresInHours int    `json:"expires_in_hours"`
		AllowDownload  *bool  `json:"allow_download"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	expiresAt, ok := archiveShareExpiry(body.ExpiresAt, body.ExpiresInHours)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_archive_share_expiry"})
		return
	}
	allowDownload := true
	if body.AllowDownload != nil {
		allowDownload = *body.AllowDownload
	}
	rawToken, err := security.RandomToken(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_archive_share_token_failed"})
		return
	}
	current := currentFromContext(r.Context())
	share, err := shareStore.CreateStreamArtifactShare(r.Context(), store.StreamArtifactShare{
		TokenHash:       security.HashToken(rawToken),
		StreamID:        stream.ID,
		ArtifactID:      artifact.ID,
		CreatedByUserID: current.User.ID,
		AllowDownload:   allowDownload,
		ExpiresAt:       expiresAt,
	})
	if errors.Is(err, store.ErrInvalidStreamArtifact) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_archive_share"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_archive_share_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.share.create", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "artifact_name": artifact.Name, "share_id": share.ID, "expires_at": share.ExpiresAt, "allow_download": share.AllowDownload}})
	response := publicArchiveShareAdmin(share)
	response["token"] = rawToken
	response["url"] = archiveSharePageURL(r, rawToken)
	response["api_url"] = "/archive-shares/" + url.PathEscape(rawToken)
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func (s *Server) revokeStreamArtifactShare(w http.ResponseWriter, r *http.Request) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return
	}
	streamID := strings.TrimSpace(r.PathValue("id"))
	artifactID := strings.TrimSpace(r.PathValue("artifact_id"))
	shareID := strings.TrimSpace(r.PathValue("share_id"))
	if err := shareStore.RevokeStreamArtifactShare(r.Context(), streamID, artifactID, shareID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "revoke_archive_share_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.share.revoke", ResourceType: "stream", ResourceID: streamID, Result: "success", Metadata: map[string]any{"artifact_id": artifactID, "share_id": shareID}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) publicArchiveShare(w http.ResponseWriter, r *http.Request) {
	share, stream, artifact, ok := s.resolvePublicArchiveShare(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, publicArchiveSharePayload(share, stream, artifact, r.PathValue("token")))
}

func (s *Server) downloadPublicArchiveShare(w http.ResponseWriter, r *http.Request) {
	share, stream, artifact, ok := s.resolvePublicArchiveShare(w, r)
	if !ok {
		return
	}
	asDownload := r.URL.Query().Get("download") == "1"
	if asDownload && !share.AllowDownload {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "archive_share_download_disabled"})
		return
	}
	primaryAssignments, err := s.archiveArtifactAssignments(r.Context(), stream.ID, artifact)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := s.dispatcher.DownloadArchiveArtifact(r.Context(), stream, primaryAssignments, artifact, r.Header.Get("Range"))
	if !result.Success || result.Body == nil {
		if result.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			if contentRange := validatedPreviewUnsatisfiedContentRange(result.ContentRange); contentRange != "" {
				w.Header().Set("Content-Range", contentRange)
			}
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_download_failed", "dispatch": sanitizeArchiveDownloadResult(result)})
		return
	}
	defer result.Body.Close()
	fileName := artifact.Name
	if result.FileName != "" {
		fileName = result.FileName
	}
	contentType := strings.TrimSpace(result.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := "inline"
	if asDownload {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+sanitizeDownloadFileName(fileName)+`"`)
	if strings.EqualFold(strings.TrimSpace(result.AcceptRanges), "bytes") {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	status := http.StatusOK
	if result.StatusCode == http.StatusPartialContent {
		if contentRange := validatedPreviewContentRange(result.ContentRange, int(result.SizeBytes)); contentRange != "" {
			w.Header().Set("Content-Range", contentRange)
			status = http.StatusPartialContent
		}
	}
	if result.SizeBytes >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(result.SizeBytes, 10))
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	_, _ = io.Copy(w, result.Body)
}

func (s *Server) deleteStreamArtifact(w http.ResponseWriter, r *http.Request) {
	stream, artifact, assignments, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	result := s.dispatcher.DeleteArchiveArtifact(r.Context(), stream, assignments, artifact)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_delete_failed", "dispatch": sanitizeDispatchResults([]servicecall.DispatchResult{result})})
		return
	}
	admin, ok := s.streams.(store.StreamArtifactAdminStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stream_artifact_admin_not_configured"})
		return
	}
	if err := admin.DeleteStreamArtifact(r.Context(), stream.ID, artifact.ID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_stream_artifact_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.delete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "artifact_name": artifact.Name}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) renameStreamArtifact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	stream, artifact, assignments, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	if !store.ValidStreamArtifactFileName(body.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_artifact"})
		return
	}
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return
	}
	for _, existing := range artifacts {
		if existing.ID != artifact.ID && existing.Kind == artifact.Kind && existing.Name == body.Name {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_artifact_exists"})
			return
		}
	}
	result := s.dispatcher.RenameArchiveArtifact(r.Context(), stream, assignments, artifact, body.Name)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_rename_failed", "dispatch": sanitizeDispatchResults([]servicecall.DispatchResult{result})})
		return
	}
	admin, ok := s.streams.(store.StreamArtifactAdminStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stream_artifact_admin_not_configured"})
		return
	}
	renamed, err := admin.RenameStreamArtifact(r.Context(), stream.ID, artifact.ID, body.Name)
	if errors.Is(err, store.ErrInvalidStreamArtifact) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_artifact"})
		return
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_artifact_exists"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "rename_stream_artifact_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.rename", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "from": artifact.Name, "to": renamed.Name}})
	writeJSON(w, http.StatusOK, renamed)
}

func (s *Server) prepareStreamArtifactAction(w http.ResponseWriter, r *http.Request) (store.Stream, store.StreamArtifact, []store.RegisteredService, bool) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	artifact, ok := artifactByID(artifacts, r.PathValue("artifact_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	primaryAssignments, err := s.archiveArtifactAssignments(r.Context(), stream.ID, artifact)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	return stream, artifact, primaryAssignments, true
}

func artifactByID(artifacts []store.StreamArtifact, id string) (store.StreamArtifact, bool) {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return store.StreamArtifact{}, false
}

func archiveShareExpiry(raw string, expiresInHours int) (time.Time, bool) {
	now := time.Now().UTC()
	var expiresAt time.Time
	if strings.TrimSpace(raw) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
		if err != nil {
			return time.Time{}, false
		}
		expiresAt = parsed.UTC()
	} else {
		if expiresInHours == 0 {
			expiresInHours = 24
		}
		expiresAt = now.Add(time.Duration(expiresInHours) * time.Hour)
	}
	if expiresAt.Before(now.Add(time.Hour)) || expiresAt.After(now.Add(30*24*time.Hour)) {
		return time.Time{}, false
	}
	return expiresAt, true
}

func archiveSharePageURL(r *http.Request, token string) string {
	base := strings.TrimRight(panelBaseURL(r), "/")
	if base == "" {
		return "/archive/share/?token=" + url.QueryEscape(token)
	}
	return base + "/archive/share/?token=" + url.QueryEscape(token)
}

func (s *Server) resolvePublicArchiveShare(w http.ResponseWriter, r *http.Request) (store.StreamArtifactShare, store.Stream, store.StreamArtifact, bool) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	share, err := shareStore.GetStreamArtifactShareByTokenHash(r.Context(), security.HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_lookup_failed"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if share.RevokedAt != nil {
		writeJSON(w, http.StatusGone, map[string]string{"code": "archive_share_revoked"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if !share.ExpiresAt.After(time.Now().UTC()) {
		writeJSON(w, http.StatusGone, map[string]string{"code": "archive_share_expired"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	stream, err := s.streams.GetStream(r.Context(), share.StreamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	artifact, ok := artifactByID(artifacts, share.ArtifactID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	return share, stream, artifact, true
}

func sanitizeArchiveDownloadResult(result servicecall.ArchiveArtifactDownloadResult) servicecall.ArchiveArtifactDownloadResult {
	result.Body = nil
	return result
}

func sanitizeDownloadFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "archive"
	}
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	return name
}
