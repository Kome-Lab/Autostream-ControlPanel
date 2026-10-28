package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/example/autostream-control-panel/internal/store"
)

// An omitted legacy baseline differs from an explicit null or non-string.
// The corresponding store validates the timestamp under its own authority.
func parseExpectedUpdatedAt(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var expected *string
	if err := json.Unmarshal(raw, &expected); err != nil || expected == nil || *expected == "" {
		return nil, store.ErrInvalidResourceRevision
	}
	return expected, nil
}

// These rejections occur before any row/secret writes in the atomic stores.
// Unknown database/commit failures stay server errors and cannot authorize resend.
func writeResourceUpdateRevisionError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrInvalidResourceRevision):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "revision_conflict"})
	case errors.Is(err, store.ErrYouTubeRelayBindingClaimActive):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "youtube_relay_binding_release_pending"})
	case errors.Is(err, store.ErrSecretKeyRequired):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
	default:
		return false
	}
	return true
}
