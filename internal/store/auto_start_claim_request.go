package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/go-sql-driver/mysql"
)

// StartClaimRequestOriginal contains public immutable correlation only. It is
// neither a payload commitment nor an authority, retry, or generation model.
type StartClaimRequestOriginal struct {
	RequestID                string    `json:"request_id"`
	StreamID                 string    `json:"stream_id"`
	ActorUserID              string    `json:"actor_user_id"`
	Operation                string    `json:"operation"`
	Automatic                bool      `json:"automatic"`
	ExpectedStreamUpdatedAt  time.Time `json:"expected_stream_updated_at"`
	ExpectedSettingsRevision string    `json:"expected_settings_revision"`
	ExpectedRunID            string    `json:"expected_run_id"`
}
type startClaimRequestKey struct{}

func WithStartClaimRequest(ctx context.Context, original StartClaimRequestOriginal) context.Context {
	return context.WithValue(ctx, startClaimRequestKey{}, original)
}
func validateStartClaimOriginal(original StartClaimRequestOriginal, stream Stream) error {
	if !validHoldID(original.RequestID) || len(original.RequestID) > 96 || !validHoldID(original.StreamID) || !validHoldID(original.ActorUserID) || original.ExpectedStreamUpdatedAt.IsZero() || original.ExpectedSettingsRevision == "" || len(original.ExpectedSettingsRevision) > 128 || len(original.ExpectedRunID) > 128 {
		return lf.ErrHeld
	}
	if (original.Automatic && original.Operation != "auto-start") || (!original.Automatic && original.Operation != "manual-start") {
		return lf.ErrHeld
	}
	if original.StreamID != stream.ID || !original.ExpectedStreamUpdatedAt.Equal(stream.UpdatedAt) || original.ExpectedSettingsRevision != stream.SettingsRevision || original.ExpectedRunID != stream.ArchiveRunID {
		return lf.ErrConflict
	}
	return nil
}
func reserveStartClaimRequestSQL(ctx context.Context, q lf.Executor, streamID string) error {
	original, bound := ctx.Value(startClaimRequestKey{}).(StartClaimRequestOriginal)
	if !bound {
		return nil
	}
	if original.StreamID != streamID {
		return lf.ErrConflict
	}
	stream, readErr := scanStreamRow(q.QueryRowContext(ctx, streamListQuery("s.id=?"), streamID))
	if readErr != nil {
		return holdAttemptError(readErr)
	}
	if e := validateStartClaimOriginal(original, stream); e != nil {
		return e
	}
	body, e := holdEncode(original)
	if e != nil {
		return e
	}
	_, e = q.ExecContext(ctx, `INSERT INTO stream_auto_start_claim_requests(request_id,stream_id,body)VALUES(?,?,?)`, original.RequestID, original.StreamID, body)
	if e != nil {
		var duplicate *mysql.MySQLError
		if errors.As(e, &duplicate) && duplicate.Number == 1062 {
			return lf.ErrConflict
		}
		return holdAttemptError(e)
	}
	return nil
}
func (s *MemoryStreamStore) checkStartClaimRequestLocked(ctx context.Context, stream Stream) error {
	original, bound := ctx.Value(startClaimRequestKey{}).(StartClaimRequestOriginal)
	if !bound {
		return nil
	}
	if e := validateStartClaimOriginal(original, stream); e != nil {
		return e
	}
	if _, exists := s.autoStartClaimRequests[original.RequestID]; exists {
		return lf.ErrConflict
	}
	return nil
}
func (s *MemoryStreamStore) commitStartClaimRequestLocked(ctx context.Context) {
	original, bound := ctx.Value(startClaimRequestKey{}).(StartClaimRequestOriginal)
	if !bound {
		return
	}
	if s.autoStartClaimRequests == nil {
		s.autoStartClaimRequests = map[string]StartClaimRequestOriginal{}
	}
	s.autoStartClaimRequests[original.RequestID] = original
}

// LookupStartClaimRequest is pure original metadata. Existence establishes no
// remote Start result and cannot authorize a replay or release a held lane.
func (s MariaDBStreamStore) LookupStartClaimRequest(ctx context.Context, streamID, requestID string) (StartClaimRequestOriginal, error) {
	var body string
	e := s.db.QueryRowContext(ctx, `SELECT body FROM stream_auto_start_claim_requests WHERE request_id=? AND stream_id=?`, requestID, streamID).Scan(&body)
	if errors.Is(e, sql.ErrNoRows) {
		return StartClaimRequestOriginal{}, ErrNotFound
	}
	if e != nil {
		return StartClaimRequestOriginal{}, holdAttemptError(e)
	}
	original, e := holdDecode[StartClaimRequestOriginal](body)
	if e != nil || original.RequestID != requestID || original.StreamID != streamID {
		return StartClaimRequestOriginal{}, lf.ErrUnknown
	}
	return original, nil
}
func (s *MemoryStreamStore) LookupStartClaimRequest(ctx context.Context, streamID, requestID string) (StartClaimRequestOriginal, error) {
	if e := ctx.Err(); e != nil {
		return StartClaimRequestOriginal{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	original, exists := s.autoStartClaimRequests[requestID]
	if !exists || original.StreamID != streamID {
		return StartClaimRequestOriginal{}, ErrNotFound
	}
	return original, nil
}
