package httpapi

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/store"
	"net/http"
	"time"
)

func (s *Server) getStreamStopOutcome(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("expected_updated_at")
	stamp, err := time.Parse(time.RFC3339Nano, source)
	if err != nil || stamp.IsZero() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stop_source_version"})
		return
	}
	source = stamp.UTC().Format(time.RFC3339Nano)
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	receipts, ok := s.streams.(store.NormalStopReceiptStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stop_receipt_unavailable"})
		return
	}
	confirmed, err := receipts.HasNormalStopReceipt(r.Context(), stream.ID, source)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "read_stop_receipt_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stream": stream, "stop_confirmed": confirmed, "source_updated_at": source})
}
