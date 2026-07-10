package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

const (
	maxUserAvatarBytes     = 768 << 10
	minUserAvatarDimension = 32
	maxUserAvatarDimension = 2048
)

func (s *Server) getCurrentUserAvatar(w http.ResponseWriter, r *http.Request) {
	if s.avatars == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "avatar_store_not_configured"})
		return
	}
	current := currentFromContext(r.Context())
	avatar, err := s.avatars.GetUserAvatar(r.Context(), current.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "avatar_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "avatar_store_failed"})
		return
	}
	etag := `"` + avatar.Fingerprint + `"`
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", avatar.UpdatedAt.UTC().Format(http.TimeFormat))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(avatar.Data)))
	w.Header().Set("Content-Type", avatar.ContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(avatar.Data)
}

func (s *Server) updateCurrentUserAvatar(w http.ResponseWriter, r *http.Request) {
	if s.avatars == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "avatar_store_not_configured"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxUserAvatarBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_avatar_image"})
		return
	}
	if len(data) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "avatar_required"})
		return
	}
	if len(data) > maxUserAvatarBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "avatar_too_large"})
		return
	}

	contentType := http.DetectContentType(data)
	config, supported, err := decodeUserAvatarConfig(contentType, data)
	if !supported {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "unsupported_avatar_type"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_avatar_image"})
		return
	}
	if config.Width < minUserAvatarDimension || config.Height < minUserAvatarDimension || config.Width > maxUserAvatarDimension || config.Height > maxUserAvatarDimension {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "avatar_dimensions_out_of_range"})
		return
	}

	current := currentFromContext(r.Context())
	sum := sha256.Sum256(data)
	info, err := s.avatars.UpsertUserAvatar(r.Context(), store.UserAvatar{
		UserID:      current.User.ID,
		ContentType: contentType,
		Data:        data,
		Fingerprint: hex.EncodeToString(sum[:]),
		UpdatedAt:   time.Now().UTC(),
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "user_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "avatar_store_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID: current.User.ID, ActorUsername: current.User.Username,
		Action: "auth.avatar.update", ResourceType: "user", ResourceID: current.User.ID, Result: "success",
		Metadata: map[string]any{"content_type": contentType, "size_bytes": len(data), "width": config.Width, "height": config.Height},
	})
	writeJSON(w, http.StatusOK, userAvatarResponse(info))
}

func (s *Server) deleteCurrentUserAvatar(w http.ResponseWriter, r *http.Request) {
	if s.avatars == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "avatar_store_not_configured"})
		return
	}
	current := currentFromContext(r.Context())
	if err := s.avatars.DeleteUserAvatar(r.Context(), current.User.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "avatar_store_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.avatar.delete", ResourceType: "user", ResourceID: current.User.ID, Result: "success"})
	w.WriteHeader(http.StatusNoContent)
}

func decodeUserAvatarConfig(contentType string, data []byte) (image.Config, bool, error) {
	var (
		config image.Config
		err    error
	)
	switch contentType {
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/png":
		config, err = png.DecodeConfig(bytes.NewReader(data))
	default:
		return image.Config{}, false, nil
	}
	return config, true, err
}

func userAvatarURL(info store.UserAvatarInfo) string {
	return "/auth/avatar?v=" + info.Fingerprint
}

func userAvatarResponse(info store.UserAvatarInfo) map[string]any {
	return map[string]any{
		"avatar_url":   userAvatarURL(info),
		"content_type": info.ContentType,
		"size_bytes":   info.SizeBytes,
		"updated_at":   info.UpdatedAt,
	}
}
