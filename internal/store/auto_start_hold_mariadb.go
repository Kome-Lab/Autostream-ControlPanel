package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/go-sql-driver/mysql"
)

func holdDecode[T any](body string) (T, error) {
	var value T
	if len(body) > 1024*1024 || json.Unmarshal([]byte(body), &value) != nil {
		return value, lf.ErrUnknown
	}
	canonical, err := json.Marshal(value)
	if err != nil || string(canonical) != body {
		return value, lf.ErrUnknown
	}
	return value, nil
}

func holdEncode(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 1024*1024 {
		return "", lf.ErrHeld
	}
	return string(body), nil
}

// Caller holds the Stream row before locking this CP-owned metadata row.
// Comparing to the latest immutable history detects a hold-only partial restore.
// This does not establish independent freshness for a co-restored whole CP DB.
func observeAutoStartHoldSQL(ctx context.Context, q lf.Executor, id string, lock bool) (AutoStartHold, error) {
	query := `SELECT revision,body FROM stream_auto_start_holds WHERE stream_id=?`
	if lock {
		query += ` FOR UPDATE`
	}
	var revision uint64
	var body string
	if err := q.QueryRowContext(ctx, query, id).Scan(&revision, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return unknownAutoStartHold(id), lf.ErrUnknown
		}
		return unknownAutoStartHold(id), holdAttemptError(err)
	}
	hold, err := holdDecode[AutoStartHold](body)
	if err != nil || hold.StreamID != id || revision == 0 || hold.Revision != revision {
		return unknownAutoStartHold(id), lf.ErrUnknown
	}
	var historyBody string
	if err := q.QueryRowContext(ctx, `SELECT body FROM stream_auto_start_hold_history WHERE stream_id=? ORDER BY sequence DESC LIMIT 1`, id).Scan(&historyBody); err != nil {
		return unknownAutoStartHold(id), holdAttemptError(err)
	}
	history, err := holdDecode[AutoStartHoldHistory](historyBody)
	if err != nil || !reflect.DeepEqual(history.Hold, hold) {
		return unknownAutoStartHold(id), lf.ErrUnknown
	}
	if hold.PendingAttemptID != "" {
		attempt, e := holdAttemptSQL(ctx, q, hold.PendingAttemptID, false)
		if e != nil || !reflect.DeepEqual(attempt.Hold, hold) {
			return unknownAutoStartHold(id), lf.ErrUnknown
		}
	}
	return hold, nil
}

func (s MariaDBStreamStore) ObserveAutoStartHold(ctx context.Context, id string) (AutoStartHold, error) {
	tx, err := lf.BeginSQL(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return unknownAutoStartHold(id), holdAttemptError(err)
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM streams WHERE id=?)`, id).Scan(&exists); err != nil {
		return unknownAutoStartHold(id), holdAttemptError(err)
	}
	if !exists {
		return AutoStartHold{}, ErrNotFound
	}
	hold, err := observeAutoStartHoldSQL(ctx, tx, id, false)
	if err != nil {
		return hold, err
	}
	if err := tx.Commit(); err != nil {
		return unknownAutoStartHold(id), holdAttemptError(err)
	}
	return hold, nil
}

func holdAttemptSQL(ctx context.Context, q lf.Executor, id string, lock bool) (AutoStartHoldAttempt, error) {
	query := `SELECT request_body,body FROM stream_auto_start_attempts WHERE attempt_id=?`
	if lock {
		query += ` FOR UPDATE`
	}
	var original, body string
	if err := q.QueryRowContext(ctx, query, id).Scan(&original, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AutoStartHoldAttempt{}, ErrNotFound
		}
		return AutoStartHoldAttempt{}, holdAttemptError(err)
	}
	a, err := holdDecode[AutoStartHoldAttempt](body)
	if err != nil || a.Request.RequestID != id {
		return AutoStartHoldAttempt{}, lf.ErrUnknown
	}
	expected, err := holdEncode(a.Request)
	if err != nil || expected != original {
		return AutoStartHoldAttempt{}, lf.ErrUnknown
	}
	return a, nil
}

func saveHoldAttemptSQL(ctx context.Context, q lf.Executor, a AutoStartHoldAttempt, create bool) error {
	body, err := holdEncode(a)
	if err != nil {
		return err
	}
	original, err := holdEncode(a.Request)
	if err != nil {
		return err
	}
	if create {
		_, err = q.ExecContext(ctx, `INSERT INTO stream_auto_start_attempts(attempt_id,stream_id,request_body,body) VALUES(?,?,?,?)`, a.Request.RequestID, a.Request.StreamID, original, body)
	} else {
		result, e := q.ExecContext(ctx, `UPDATE stream_auto_start_attempts SET body=? WHERE attempt_id=? AND stream_id=? AND request_body=?`, body, a.Request.RequestID, a.Request.StreamID, original)
		if e != nil {
			return holdAttemptError(e)
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return lf.ErrConflict
		}
	}
	return holdAttemptError(err)
}

func persistAutoStartHoldSQL(ctx context.Context, q lf.Executor, before AutoStartHold, hold AutoStartHold, requestID, operation string) error {
	body, err := holdEncode(hold)
	if err != nil {
		return err
	}
	if before.Revision == 0 {
		if _, err = q.ExecContext(ctx, `INSERT INTO stream_auto_start_holds(stream_id,revision,body) VALUES(?,?,?)`, hold.StreamID, hold.Revision, body); err != nil {
			return holdAttemptError(err)
		}
	} else {
		old, err := holdEncode(before)
		if err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE stream_auto_start_holds SET revision=?,body=? WHERE stream_id=? AND revision=? AND body=?`, hold.Revision, body, hold.StreamID, before.Revision, old)
		if err != nil {
			return holdAttemptError(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return lf.ErrConflict
		}
	}
	auditID := newUUID()
	history := AutoStartHoldHistory{StreamID: hold.StreamID, RequestID: requestID, Operation: operation, BeforeRevision: before.Revision, Hold: hold, AuditID: auditID, CreatedAt: hold.UpdatedAt}
	historyBody, err := holdEncode(history)
	if err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `INSERT INTO stream_auto_start_hold_history(stream_id,request_id,hold_revision,body) VALUES(?,?,?,?)`, hold.StreamID, requestID, hold.Revision, historyBody); err != nil {
		return holdAttemptError(err)
	}
	metadata, err := holdEncode(map[string]any{"attempt_id": requestID, "hold_revision": hold.Revision, "held_known": hold.Known, "held": hold.Held, "boundary": "CP_HOLD_RECORD", "external_start_proven": false})
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO audit_logs(id,timestamp,actor_user_id,action,resource_type,resource_id,result,metadata,request_id) VALUES(?,?,?,?,?,?,?,?,?)`, auditID, hold.UpdatedAt, nullString(hold.ActorUserID), "streams.auto_start_hold."+operation, "stream", hold.StreamID, "success", metadata, newUUID())
	return holdAttemptError(err)
}

func lockHoldStreamSQL(ctx context.Context, q lf.Executor, id string) (Stream, error) {
	stream, err := scanStreamRow(q.QueryRowContext(ctx, streamListQuery("s.id=?")+" FOR UPDATE", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Stream{}, ErrNotFound
	}
	return stream, holdAttemptError(err)
}

func holdForMutationSQL(ctx context.Context, q lf.Executor, id string) (AutoStartHold, error) {
	hold, err := observeAutoStartHoldSQL(ctx, q, id, true)
	if !errors.Is(err, lf.ErrUnknown) {
		return hold, err
	}
	// Only a genuinely absent record can be established by an explicit Human
	// operation. A restored/malformed existing row must never be overwritten.
	var exists bool
	if e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_auto_start_holds WHERE stream_id=?)`, id).Scan(&exists); e != nil {
		return hold, holdAttemptError(e)
	}
	if exists {
		return hold, lf.ErrUnknown
	}
	return unknownAutoStartHold(id), nil
}

func beginManualStopHoldSQL(ctx context.Context, q lf.Executor, stream Stream, req AutoStartHoldRequest) error {
	if err := validateHoldRequest(req); err != nil {
		return err
	}
	if req.Operation != AutoStartManualStop && req.Operation != AutoStartForceStop {
		return lf.ErrHeld
	}
	if err := checkHoldRequestStream(req, stream); err != nil {
		return err
	}
	if prior, err := holdAttemptSQL(ctx, q, req.RequestID, true); err == nil {
		if !sameHoldRequest(prior.Request, req) {
			return lf.ErrConflict
		}
		return lf.ErrConflict // Inspect only; never execute an existing Stop Attempt again.
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	before, err := holdForMutationSQL(ctx, q, req.StreamID)
	if err != nil {
		return err
	}
	if before.Revision != req.ExpectedHoldRevision || before.PendingAttemptID != "" {
		return lf.ErrConflict
	}
	hold, err := nextHold(before, req, true)
	if err != nil {
		return err
	}
	hold.Known, hold.Held, hold.Reason, hold.OriginalAttemptID = true, true, req.Operation, req.RequestID
	if err = persistAutoStartHoldSQL(ctx, q, before, hold, req.RequestID, "intent"); err != nil {
		return err
	}
	return saveHoldAttemptSQL(ctx, q, newHoldAttempt(req, hold), true)
}

func (s MariaDBStreamStore) BeginExplicitResume(ctx context.Context, req AutoStartHoldRequest) (AutoStartHoldAttempt, bool, error) {
	if err := validateHoldRequest(req); err != nil || req.Operation != AutoStartExplicitResume {
		return AutoStartHoldAttempt{}, false, lf.ErrHeld
	}
	tx, err := lf.BeginSQL(ctx, s.db, nil)
	if err != nil {
		return AutoStartHoldAttempt{}, false, holdAttemptError(err)
	}
	defer tx.Rollback()
	stream, err := lockHoldStreamSQL(ctx, tx, req.StreamID)
	if err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	if prior, e := holdAttemptSQL(ctx, tx, req.RequestID, true); e == nil {
		if !sameHoldRequest(prior.Request, req) {
			return AutoStartHoldAttempt{}, false, lf.ErrConflict
		}
		if e = tx.Commit(); e != nil {
			return AutoStartHoldAttempt{}, false, holdAttemptError(e)
		}
		return prior, false, nil
	} else if !errors.Is(e, ErrNotFound) {
		return AutoStartHoldAttempt{}, false, e
	}
	if err := checkHoldRequestStream(req, stream); err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	before, err := holdForMutationSQL(ctx, tx, req.StreamID)
	if err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	if before.Revision != req.ExpectedHoldRevision || before.PendingAttemptID != "" {
		return AutoStartHoldAttempt{}, false, lf.ErrConflict
	}
	hold, err := nextHold(before, req, true)
	if err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	hold.OriginalAttemptID = req.RequestID
	a := newHoldAttempt(req, hold)
	if err = persistAutoStartHoldSQL(ctx, tx, before, hold, req.RequestID, "resume_intent"); err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	if err = saveHoldAttemptSQL(ctx, tx, a, true); err != nil {
		return AutoStartHoldAttempt{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return AutoStartHoldAttempt{}, false, holdAttemptError(err)
	}
	return a, true, nil
}

func (s MariaDBStreamStore) finishAutoStartHold(ctx context.Context, req AutoStartHoldRequest, condition lifecycle.Fact[bool], evidence []lifecycle.Record, resume bool) (AutoStartHoldAttempt, error) {
	if err := validateHoldRequest(req); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if resume && validateHoldCondition(condition) != nil {
		return AutoStartHoldAttempt{}, lf.ErrUnknown
	}
	if resume && string(condition.Observation.Scope) != req.StreamID {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	tx, err := lf.BeginSQL(ctx, s.db, nil)
	if err != nil {
		return AutoStartHoldAttempt{}, holdAttemptError(err)
	}
	defer tx.Rollback()
	stream, err := lockHoldStreamSQL(ctx, tx, req.StreamID)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	a, err := holdAttemptSQL(ctx, tx, req.RequestID, true)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if !sameHoldRequest(a.Request, req) {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	if a.Final {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	if resume {
		if req.Operation != AutoStartExplicitResume {
			return AutoStartHoldAttempt{}, lf.ErrHeld
		}
		if err := checkHoldRequestStream(req, stream); err != nil {
			return AutoStartHoldAttempt{}, err
		}
	} else if req.Operation != AutoStartManualStop && req.Operation != AutoStartForceStop {
		return AutoStartHoldAttempt{}, lf.ErrHeld
	}
	if !resume && (stream.SettingsRevision != req.ExpectedSettingsRevision || stream.ArchiveRunID != req.ExpectedRunID) {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	before, err := observeAutoStartHoldSQL(ctx, tx, req.StreamID, true)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if before.PendingAttemptID != req.RequestID || before.Revision != a.Hold.Revision {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	if err = holdFenceEvidence(req, before, evidence); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	hold, err := nextHold(before, req, false)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	operation := "confirmed"
	if resume {
		hold.Known, hold.Held, hold.Reason = true, false, AutoStartExplicitResume
		operation = "resumed"
	}
	a.Hold, a.FenceEvidence, a.UpdatedAt, a.Final = hold, cloneAutoStart(evidence), hold.UpdatedAt, true
	a.HoldCommit = knownHoldBoundary(hold)
	if resume {
		a.Condition = cloneAutoStart(condition)
	}
	// Gate qualification and any external Start remain independent facts. This
	// metadata commit does not manufacture a verified lifecycle/Authority gate.
	a.Gate = lifecycle.NotQualified
	if err = persistAutoStartHoldSQL(ctx, tx, before, hold, req.RequestID, operation); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if err = saveHoldAttemptSQL(ctx, tx, a, false); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if err = tx.Commit(); err != nil {
		return AutoStartHoldAttempt{}, holdAttemptError(err)
	}
	return a, nil
}

func (s MariaDBStreamStore) CommitExplicitResume(ctx context.Context, req AutoStartHoldRequest, condition lifecycle.Fact[bool], evidence []lifecycle.Record) (AutoStartHoldAttempt, error) {
	return s.finishAutoStartHold(ctx, req, condition, evidence, true)
}

func (s MariaDBStreamStore) ConfirmManualStopHold(ctx context.Context, req AutoStartHoldRequest, evidence []lifecycle.Record) (AutoStartHoldAttempt, error) {
	return s.finishAutoStartHold(ctx, req, lifecycle.Fact[bool]{}, evidence, false)
}

func (s MariaDBStreamStore) InspectAutoStartAttempt(ctx context.Context, streamID, attemptID string) (AutoStartHoldAttempt, error) {
	tx, err := lf.BeginSQL(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return AutoStartHoldAttempt{}, holdAttemptError(err)
	}
	defer tx.Rollback()
	if _, err = observeAutoStartHoldSQL(ctx, tx, streamID, false); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	a, err := holdAttemptSQL(ctx, tx, attemptID, false)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if a.Request.StreamID != streamID {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return AutoStartHoldAttempt{}, holdAttemptError(err)
	}
	return a, nil
}

func (s MariaDBStreamStore) ListHoldHistory(ctx context.Context, streamID string) ([]AutoStartHoldHistory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,body FROM stream_auto_start_hold_history WHERE stream_id=? ORDER BY sequence`, streamID)
	if err != nil {
		return nil, holdAttemptError(err)
	}
	defer rows.Close()
	out := []AutoStartHoldHistory{}
	for rows.Next() {
		var sequence uint64
		var body string
		if err = rows.Scan(&sequence, &body); err != nil {
			return nil, holdAttemptError(err)
		}
		h, err := holdDecode[AutoStartHoldHistory](body)
		if err != nil {
			return nil, err
		}
		h.Sequence = sequence
		out = append(out, h)
	}
	return out, holdAttemptError(rows.Err())
}

func (s MariaDBStreamStore) RecordAutoStartStartResult(ctx context.Context, streamID, attemptID string, result lifecycle.BoundaryObservation) (AutoStartHoldAttempt, error) {
	if err := validateStartBoundary(result, streamID); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	tx, err := lf.BeginSQL(ctx, s.db, nil)
	if err != nil {
		return AutoStartHoldAttempt{}, holdAttemptError(err)
	}
	defer tx.Rollback()
	if _, err = lockHoldStreamSQL(ctx, tx, streamID); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	a, err := holdAttemptSQL(ctx, tx, attemptID, true)
	if err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if a.Request.StreamID != streamID || !a.Final || a.Request.Operation != AutoStartExplicitResume || string(result.Address.Resource.ResourceID) != streamID {
		return AutoStartHoldAttempt{}, lf.ErrHeld
	}
	if a.StartResultRecorded {
		return AutoStartHoldAttempt{}, lf.ErrConflict
	}
	a.StartResult, a.StartResultRecorded, a.UpdatedAt = cloneAutoStart(result), true, time.Now().UTC().Truncate(time.Microsecond)
	if err = saveHoldAttemptSQL(ctx, tx, a, false); err != nil {
		return AutoStartHoldAttempt{}, err
	}
	if err = tx.Commit(); err != nil {
		return AutoStartHoldAttempt{}, holdAttemptError(err)
	}
	return a, nil
}

func checkAutomaticStartHoldSQL(ctx context.Context, q lf.Executor, streamID string) error {
	expected, automatic := ctx.Value(automaticStartHoldKey{}).(uint64)
	manualRevision, manual := ctx.Value(manualStartHoldKey{}).(uint64)
	if automatic && manual {
		return lf.ErrHeld
	}
	if !automatic && !manual {
		return nil
	}
	if manual {
		expected = manualRevision
	}
	hold, err := observeAutoStartHoldSQL(ctx, q, streamID, true)
	if err != nil {
		return err
	}
	if !hold.Known || hold.Revision == 0 {
		return lf.ErrUnknown
	}
	if (automatic && hold.Held) || hold.PendingAttemptID != "" || hold.Revision != expected {
		return lf.ErrHeld
	}
	return nil
}

func initializeAutoStartHoldSQL(ctx context.Context, q lf.Executor, stream Stream) error {
	now := stream.CreatedAt.UTC().Truncate(time.Microsecond)
	hold := AutoStartHold{StreamID: stream.ID, Known: true, Held: false, Revision: 1, Reason: "new_stream", CreatedAt: now, UpdatedAt: now}
	return persistAutoStartHoldSQL(ctx, q, AutoStartHold{}, hold, "", "new_stream")
}

func (s MariaDBStreamStore) SaveAutoStartAdmission(ctx context.Context, a AutoStartAdmission) error {
	if err := validateAutoStartAdmission(a); err != nil {
		return err
	}
	tx, err := lf.BeginSQL(ctx, s.db, nil)
	if err != nil {
		return holdAttemptError(err)
	}
	defer tx.Rollback()
	stream, err := lockHoldStreamSQL(ctx, tx, a.StreamID)
	if err != nil {
		return err
	}
	hold, err := observeAutoStartHoldSQL(ctx, tx, a.StreamID, true)
	if err != nil {
		return err
	}
	if err = checkAutoStartAdmission(a, stream, hold); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_auto_start_admissions WHERE request_id=?)`, a.RequestID).Scan(&exists); err != nil {
		return holdAttemptError(err)
	}
	if exists {
		return lf.ErrConflict
	}
	body, err := holdEncode(a)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO stream_auto_start_admissions(request_id,stream_id,body) VALUES(?,?,?)`, a.RequestID, a.StreamID, body); err != nil {
		return holdAttemptError(err)
	}
	metadata, _ := holdEncode(map[string]any{"attempt_id": a.RequestID, "hold_revision": a.HoldRevision, "automatic": a.Automatic, "boundary": "START_ADMISSION_RECORD", "external_start_proven": false})
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(id,timestamp,action,resource_type,resource_id,result,metadata,request_id) VALUES(?,?,?,?,?,?,?,?)`, newUUID(), time.Now().UTC(), "streams.auto_start_admission.record", "stream", a.StreamID, "success", metadata, newUUID()); err != nil {
		return holdAttemptError(err)
	}
	return holdAttemptError(tx.Commit())
}

func (s MariaDBStreamStore) InspectAutoStartAdmission(ctx context.Context, streamID, requestID string) (AutoStartAdmission, error) {
	tx, err := lf.BeginSQL(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return AutoStartAdmission{}, holdAttemptError(err)
	}
	defer tx.Rollback()
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT body FROM stream_auto_start_admissions WHERE request_id=? AND stream_id=?`, requestID, streamID).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AutoStartAdmission{}, ErrNotFound
		}
		return AutoStartAdmission{}, holdAttemptError(err)
	}
	a, err := holdDecode[AutoStartAdmission](body)
	if err != nil || a.StreamID != streamID || a.RequestID != requestID {
		return AutoStartAdmission{}, lf.ErrUnknown
	}
	stream, err := scanStreamRow(tx.QueryRowContext(ctx, streamListQuery("s.id=?"), streamID))
	if err != nil {
		return AutoStartAdmission{}, holdAttemptError(err)
	}
	hold, err := observeAutoStartHoldSQL(ctx, tx, streamID, false)
	if err != nil {
		return AutoStartAdmission{}, err
	}
	if err = checkAutoStartAdmission(a, stream, hold); err != nil {
		return AutoStartAdmission{}, err
	}
	if err = tx.Commit(); err != nil {
		return AutoStartAdmission{}, holdAttemptError(err)
	}
	return a, nil
}

func autoStartHoldSchemaInstalled(ctx context.Context, q lf.Executor) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT stream_id FROM stream_auto_start_holds LIMIT 0`)
	if err != nil {
		// Missing schema is an explicit legacy-schema fact, not an unheld value.
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1146 {
			return false, nil
		}
		return false, err
	}
	return true, rows.Close()
}

// LookupAutoStartAdmission returns only the original immutable correlation. It
// does not attest currentness and must never be used as a prelaunch fence.
func (s MariaDBStreamStore) LookupAutoStartAdmission(ctx context.Context, streamID, requestID string) (AutoStartAdmission, error) {
	var body string
	if err := s.db.QueryRowContext(ctx, `SELECT body FROM stream_auto_start_admissions WHERE request_id=? AND stream_id=?`, requestID, streamID).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AutoStartAdmission{}, ErrNotFound
		}
		return AutoStartAdmission{}, holdAttemptError(err)
	}
	admission, err := holdDecode[AutoStartAdmission](body)
	if err != nil || admission.StreamID != streamID || admission.RequestID != requestID || validateAutoStartAdmission(admission) != nil {
		return AutoStartAdmission{}, lf.ErrUnknown
	}
	return admission, nil
}

// InitializeNewStreamAutoStartHold is called only inside an actual new-Stream
// constructor's transaction. It never backfills or clears a historical row.
// Schema absence preserves legacy construction with an UNKNOWN Hold lookup.
func InitializeNewStreamAutoStartHold(ctx context.Context, q lf.Executor, stream Stream) error {
	installed, err := autoStartHoldSchemaInstalled(ctx, q)
	if err != nil {
		return err
	}
	if !installed {
		return nil
	}
	return initializeAutoStartHoldSQL(ctx, q, stream)
}
