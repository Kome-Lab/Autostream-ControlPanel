package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/videocover"
)

// CoverRecordActor carries only local authenticated identity. It is not a
// Workload/System principal, a provider credential, or a Lifecycle permit.
type CoverRecordActor struct {
	UserID              string
	SessionHash         string `json:"-"`
	OperationalRevision uint64
}

type CoverOutcomeStore interface {
	RecordCoverObservation(context.Context, AuditStore, videocover.Repository, CoverRecordActor, videocover.State, string, string, AuditEvent) (videocover.State, error)
}

func coverCriticalAudit(event AuditEvent, user User, state videocover.State, status, code string) (AuditEvent, []byte, error) {
	if user.ID == "" || event.ActorUserID != user.ID || event.ResourceID != state.StreamID || state.StreamID == "" || state.LastIdempotencyKey == "" {
		return AuditEvent{}, nil, ErrForbidden
	}
	action := "streams.video_cover.show"
	if !state.DesiredActive {
		action = "streams.video_cover.hide"
	}
	if event.Action != action || event.ResourceType != "stream" || status != "applied" && status != "failed" {
		return AuditEvent{}, nil, ErrForbidden
	}
	event.ActorUsername = user.Username
	event.Result = "success"
	if status == "failed" {
		event.Result = "failure"
	}
	event.Metadata = map[string]any{"status": status, "reconciled": true, "desired_revision": state.DesiredRevision, "job_generation": state.JobGeneration, "cover_outcome_ref": coverOutcomeAuditRef(state)}
	if status == "failed" {
		event.Metadata["error_code"] = code
	}
	event = redactedAuditEvent(event)
	if event.ID == "" {
		event.ID = newUUID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	body, err := json.Marshal(event.Metadata)
	return event, body, err
}

// This is only a nonsecret idempotency address, never a secret commitment or
// lifecycle qualification. Raw request keys are not copied into audit metadata.
func coverOutcomeAuditRef(state videocover.State) string {
	sum := sha256.Sum256([]byte("IU-WS2-01-COVER-OUTCOME\x00" + state.StreamID + "\x00" + strconv.FormatUint(state.JobGeneration, 10) + "\x00" + state.LastIdempotencyKey))
	return hex.EncodeToString(sum[:])
}

func sameCoverCriticalAudit(existing, requested AuditEvent) bool {
	return existing.Action == requested.Action && existing.ResourceType == requested.ResourceType && existing.ResourceID == requested.ResourceID && existing.Result == requested.Result &&
		existing.Metadata["cover_outcome_ref"] == requested.Metadata["cover_outcome_ref"] && existing.Metadata["job_generation"] == requested.Metadata["job_generation"] && existing.Metadata["desired_revision"] == requested.Metadata["desired_revision"]
}

func (s *MemoryAuthStore) RecordCoverObservation(ctx context.Context, audit AuditStore, repository videocover.Repository, actor CoverRecordActor, expected videocover.State, status, code string, event AuditEvent) (videocover.State, error) {
	if err := ctx.Err(); err != nil {
		return videocover.State{}, err
	}
	// A different audit owner cannot be made atomic by calling WriteAudit later.
	if owned, ok := audit.(*MemoryAuthStore); !ok || owned != s {
		return videocover.State{}, lf.ErrHeld
	}
	covers, ok := repository.(*videocover.MemoryRepository)
	if !ok || covers == nil {
		return videocover.State{}, lf.ErrHeld
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[actor.UserID]
	if !ok || user.Status != "active" {
		return videocover.State{}, ErrForbidden
	}
	if actor.OperationalRevision != 0 {
		generation, err := s.operationalRevisionLocked()
		if err != nil || generation != actor.OperationalRevision {
			return videocover.State{}, ErrForbidden
		}
	}
	permissions := s.permissions[user.ID]
	if actor.OperationalRevision != 0 {
		permissions = nil
		for _, id := range user.RoleIDs {
			role, found := s.roles[id]
			if !found {
				return videocover.State{}, ErrForbidden
			}
			permissions = append(permissions, role.Permissions...)
		}
	}
	if !security.HasPermission(permissions, "streams.read") {
		return videocover.State{}, ErrForbidden
	}
	session, ok := s.sessions[actor.SessionHash]
	now := time.Now().UTC()
	if !ok || session.UserID != user.ID || !now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		return videocover.State{}, ErrUnauthorized
	}
	if s.streamAssignmentGuard == nil {
		return videocover.State{}, lf.ErrHeld
	}
	s.streamAssignmentGuard.mu.Lock()
	defer s.streamAssignmentGuard.mu.Unlock()
	target, found := s.streamAssignmentGuard.streams[expected.StreamID]
	if !found || target.DeletedAt != nil {
		return videocover.State{}, ErrNotFound
	}
	event, _, err := coverCriticalAudit(event, user, expected, status, code)
	if err != nil {
		return videocover.State{}, err
	}
	newAudit := false
	state, err := covers.RecordObservation(ctx, expected, status, code, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, existing := range s.auditEvents {
			if sameCoverCriticalAudit(existing, event) {
				return nil
			}
		}
		s.auditEvents = append(s.auditEvents, event)
		newAudit = true
		return nil
	})
	if err == nil {
		state.RecordedAction = nil
		if newAudit {
			state.RecordedAction = &videocover.PreparedAction{RequestedRevision: expected.DesiredRevision, Outcome: status, SafeErrorCode: state.LastErrorCode}
		}
	}
	return state, err
}

func (s MariaDBAuthStore) RecordCoverObservation(ctx context.Context, audit AuditStore, repository videocover.Repository, actor CoverRecordActor, expected videocover.State, status, code string, event AuditEvent) (videocover.State, error) {
	// D4 and the full Identity HTTP composition remain unqualified. This path
	// cannot silently open an untracked transaction in required L2 composition.
	if s.identityGate != nil || s.db == nil {
		return videocover.State{}, lf.ErrHeld
	}
	if _, joined, err := lf.SQL(ctx, s.db); err != nil || joined {
		return videocover.State{}, lf.ErrHeld
	}
	auditOwner, ok := audit.(MariaDBAuditStore)
	if !ok || auditOwner.db != s.db {
		return videocover.State{}, lf.ErrHeld
	}
	covers, ok := repository.(*videocover.SQLRepository)
	if !ok {
		return videocover.State{}, lf.ErrHeld
	}
	// The current-generation range must remain stable through the audit insert;
	// InnoDB next-key locking also fences a concurrent higher-generation insert.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return videocover.State{}, err
	}
	defer tx.Rollback()
	var generation uint64
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM operational_authorization_epoch WHERE singleton=1 FOR UPDATE`).Scan(&generation); err != nil || generation == 0 {
		return videocover.State{}, lf.ErrHeld
	}
	if err = oa.CheckOperationalSchema(ctx, tx); err != nil {
		return videocover.State{}, lf.ErrHeld
	}
	if actor.OperationalRevision != 0 && generation != actor.OperationalRevision {
		return videocover.State{}, ErrForbidden
	}
	var user User
	user.ID = actor.UserID
	if err = tx.QueryRowContext(ctx, `SELECT username,status FROM users WHERE id=? FOR UPDATE`, actor.UserID).Scan(&user.Username, &user.Status); err != nil {
		return videocover.State{}, err
	}
	if user.Status != "active" {
		return videocover.State{}, ErrForbidden
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT rp.permission FROM role_permissions rp JOIN user_roles ur ON ur.role_id=rp.role_id WHERE ur.user_id=?`, actor.UserID)
	if err != nil {
		return videocover.State{}, err
	}
	var permissions []string
	for rows.Next() {
		var permission string
		if err = rows.Scan(&permission); err != nil {
			rows.Close()
			return videocover.State{}, err
		}
		permissions = append(permissions, permission)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !security.HasPermission(permissions, "streams.read") {
		return videocover.State{}, ErrForbidden
	}
	var sessionUser string
	var idle, absolute time.Time
	err = tx.QueryRowContext(ctx, `SELECT user_id,idle_expires_at,absolute_expires_at FROM sessions WHERE id=? FOR UPDATE`, actor.SessionHash).Scan(&sessionUser, &idle, &absolute)
	if err != nil || sessionUser != actor.UserID || !time.Now().UTC().Before(idle) || !time.Now().UTC().Before(absolute) {
		return videocover.State{}, ErrUnauthorized
	}
	var targetID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM streams WHERE id=? AND deleted_at IS NULL FOR UPDATE`, expected.StreamID).Scan(&targetID); err != nil {
		return videocover.State{}, ErrNotFound
	}
	event, body, err := coverCriticalAudit(event, user, expected, status, code)
	if err != nil {
		return videocover.State{}, err
	}
	state, _, err := covers.RecordObservationTx(ctx, s.db, tx, expected, status, code)
	if err != nil {
		return videocover.State{}, err
	}
	// A legacy writer can have resolved the action after the external read.
	// Its best-effort audit is not a critical-audit witness. Verify this exact
	// product record under the same epoch/transaction, even for a terminal no-op.
	var recorded bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_logs WHERE action=? AND resource_type=? AND resource_id=? AND result=? AND JSON_UNQUOTE(JSON_EXTRACT(metadata,'$.cover_outcome_ref'))=? AND JSON_EXTRACT(metadata,'$.job_generation')=? AND JSON_EXTRACT(metadata,'$.desired_revision')=?)`, event.Action, event.ResourceType, event.ResourceID, event.Result, event.Metadata["cover_outcome_ref"], expected.JobGeneration, expected.DesiredRevision).Scan(&recorded)
	if err != nil {
		return videocover.State{}, err
	}
	if !recorded {
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs (id,timestamp,actor_user_id,actor_username,actor_ip,user_agent,action,resource_type,resource_id,result,metadata,request_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, event.ID, event.Timestamp, nullString(event.ActorUserID), nullString(event.ActorUsername), nullString(event.ActorIP), nullString(event.UserAgent), event.Action, event.ResourceType, event.ResourceID, event.Result, string(body), event.RequestID)
		if err != nil {
			return videocover.State{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		// A failed Commit ACK is not evidence of rollback and must never cause a
		// provider resend. A later GET observes the same action key.
		return videocover.State{}, errors.Join(lf.ErrUnknown, err)
	}
	state.RecordedAction = nil
	if !recorded {
		state.RecordedAction = &videocover.PreparedAction{RequestedRevision: expected.DesiredRevision, Outcome: status, SafeErrorCode: state.LastErrorCode}
	}
	return state, nil
}
