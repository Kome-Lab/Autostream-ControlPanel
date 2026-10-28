package httpapi

import (
	"net/http"
	"net/url"

	"github.com/example/autostream-control-panel/internal/mediaassets"
)

func (s *Server) inspectMediaUploadOperation(w http.ResponseWriter, r *http.Request) {
	parameters, err := url.ParseQuery(r.URL.RawQuery)
	operations := parameters["operation_id"]
	if err != nil || len(operations) != 1 || !mediaassets.ValidUploadOperationID(operations[0]) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	inspector, ok := s.mediaAssets.(mediaassets.DraftUploadInspector)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "media_assets_unavailable"})
		return
	}
	current := currentFromContext(r.Context())
	snapshot, err := inspector.InspectDraftUpload(r.Context(), current.User.ID, r.PathValue("session_id"), operations[0])
	if err != nil {
		writeMediaAssetError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snapshot)
}
