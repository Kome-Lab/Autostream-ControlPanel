package lifecyclefoundation

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

type SQLStore struct {
	db      *sql.DB
	binding Binding
	commit  func(*sql.Tx) error
}

// NewSQLStore performs no migration, enrollment, credential lookup or network I/O.
func NewSQLStore(db *sql.DB, binding Binding) (*SQLStore, error) {
	if db == nil || binding.Validate() != nil {
		return nil, ErrHeld
	}
	return &SQLStore{db: db, binding: binding, commit: func(tx *sql.Tx) error { return tx.Commit() }}, nil
}

type participationKey struct{}

type participation struct {
	mu      sync.Mutex
	store   *SQLStore
	request Request
	active  bool
}

// Transaction has no Commit/Rollback method. Its context is private, exact-request
// bound and invalid as soon as the owning callback returns.
type Transaction struct {
	tx    *sql.Tx
	proof *participation
}

func (t *Transaction) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if t == nil || ctx == nil || t.proof == nil || t.tx == nil {
		return nil, ErrHeld
	}
	p, ok := ctx.Value(participationKey{}).(*participation)
	if !ok || p != t.proof {
		return nil, ErrHeld
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active || p.request.Intent.Mode == Read || ctx.Err() != nil {
		return nil, ErrHeld
	}
	return t.tx.ExecContext(ctx, query, args...)
}

func RequireParticipation(ctx context.Context, store *SQLStore, request Request) error {
	if ctx == nil || store == nil {
		return ErrHeld
	}
	p, ok := ctx.Value(participationKey{}).(*participation)
	if !ok || p == nil {
		return ErrHeld
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active || p.store != store || p.request != request || ctx.Err() != nil {
		return ErrHeld
	}
	return nil
}

type Effect func(context.Context, *Transaction) (Checkpoint, error)

func (s *SQLStore) head(ctx context.Context, q sqlQuery, lock bool) (Frame, error) {
	var body string
	query := "SELECT body FROM v3_cp_lifecycle_heads WHERE deployment=?"
	if lock {
		query += " FOR UPDATE"
	}
	if err := q.QueryRowContext(ctx, query, s.binding.Deployment).Scan(&body); err != nil {
		return Frame{}, held(err)
	}
	var frame Frame
	if decode(body, &frame) != nil || frame.Binding != s.binding || frame.Checkpoint.Validate() != nil {
		return Frame{}, ErrHeld
	}
	if frame.Sequence == 0 && frame.Attempt != "" || frame.Sequence != 0 && !validID(frame.Attempt) {
		return Frame{}, ErrHeld
	}
	switch frame.Phase {
	case Completed, CPCommitted, ReceiptSaved:
	default:
		return Frame{}, ErrHeld
	}
	return frame, nil
}

func (s *SQLStore) current(ctx context.Context) (Frame, error) {
	if s == nil || ctx == nil || ctx.Err() != nil {
		return Frame{}, ErrHeld
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Frame{}, held(err)
	}
	defer tx.Rollback()
	if err = checkSchema(ctx, tx); err != nil {
		return Frame{}, err
	}
	frame, err := s.head(ctx, tx, false)
	if err != nil {
		return Frame{}, err
	}
	var unknown int
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_lifecycle_unknowns WHERE deployment=?", s.binding.Deployment).Scan(&unknown) != nil || unknown != 0 {
		return Frame{}, ErrHeld
	}
	if frame.Phase != Completed && frame.Phase != ReceiptSaved {
		return Frame{}, ErrHeld
	}
	return frame, nil
}

func (s *SQLStore) alreadyUsed(ctx context.Context, id string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_lifecycle_attempts WHERE deployment=? AND attempt=?", s.binding.Deployment, id).Scan(&count); err != nil {
		return held(err)
	}
	if count != 0 {
		return ErrConflict
	}
	return nil
}

func nextCheckpoint(before, after Checkpoint, intent Intent) bool {
	if after.Validate() != nil {
		return false
	}
	if intent.Mode == Read {
		return after == before
	}
	if after.Content == before.Content || after.HistoryHead == before.HistoryHead ||
		after.OperationalRevision < before.OperationalRevision ||
		after.AuthorityEpoch < before.AuthorityEpoch || after.IdentityRevision < before.IdentityRevision {
		return false
	}
	switch intent.Purpose {
	case "AUTHORITY_CHANGE":
		return before.AuthorityEpoch != ^uint64(0) && after.AuthorityEpoch == before.AuthorityEpoch+1
	case "OPERATIONAL_CHANGE":
		return before.OperationalRevision != ^uint64(0) && after.OperationalRevision == before.OperationalRevision+1
	case "IDENTITY_CHANGE":
		return before.IdentityRevision != ^uint64(0) && after.IdentityRevision == before.IdentityRevision+1
	}
	return false
}

func (s *SQLStore) apply(ctx context.Context, r Reservation, auth Authorizer, effect Effect) (CommitRecord, error) {
	if auth == nil || effect == nil || r.Binding != s.binding || r.Request.Validate() != nil ||
		validateAuthorization(r.Authorization, r.Request.Intent) != nil || r.Before.Sequence == ^uint64(0) ||
		r.Sequence != r.Before.Sequence+1 {
		return CommitRecord{}, ErrHeld
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return CommitRecord{}, held(err)
	}
	defer tx.Rollback()
	if err = checkSchema(ctx, tx); err != nil {
		return CommitRecord{}, err
	}
	frame, err := s.head(ctx, tx, true)
	if err != nil || frame != r.Before {
		return CommitRecord{}, ErrConflict
	}
	var unknown int
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_lifecycle_unknowns WHERE deployment=?", s.binding.Deployment).Scan(&unknown) != nil || unknown != 0 {
		return CommitRecord{}, ErrHeld
	}
	if err = auth.ValidateTx(ctx, tx, r.Authorization, frame.Checkpoint); err != nil {
		return CommitRecord{}, ErrHeld
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_lifecycle_attempts VALUES(?,?,?)", s.binding.Deployment, r.Request.ID, encode(r)); err != nil {
		return CommitRecord{}, ErrConflict
	}
	if approval := r.Authorization.Approval; approval != nil {
		if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_lifecycle_consumptions VALUES(?,?,?,?)", s.binding.Deployment, approval.Origin, approval.ID, r.Request.ID); err != nil {
			return CommitRecord{}, ErrConflict
		}
	}
	p := &participation{store: s, request: r.Request, active: true}
	partCtx := context.WithValue(ctx, participationKey{}, p)
	after, effectErr := func() (Checkpoint, error) {
		defer func() {
			p.mu.Lock()
			p.active = false
			p.mu.Unlock()
		}()
		return effect(partCtx, &Transaction{tx: tx, proof: p})
	}()
	if effectErr != nil {
		return CommitRecord{}, effectErr
	}
	if !nextCheckpoint(frame.Checkpoint, after, r.Request.Intent) {
		return CommitRecord{}, ErrHeld
	}
	record := CommitRecord{Reservation: r, After: after}
	frame.Checkpoint = after
	frame.Sequence = r.Sequence
	frame.Attempt = r.Request.ID
	frame.Phase = CPCommitted
	if _, err = tx.ExecContext(ctx, "UPDATE v3_cp_lifecycle_heads SET body=? WHERE deployment=?", encode(frame), s.binding.Deployment); err != nil {
		return CommitRecord{}, held(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE v3_cp_lifecycle_attempts SET body=? WHERE deployment=? AND attempt=?", encode(record), s.binding.Deployment, r.Request.ID); err != nil {
		return CommitRecord{}, held(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_lifecycle_history(deployment,attempt,phase,body) VALUES(?,?,?,?)", s.binding.Deployment, r.Request.ID, CPCommitted, encode(record)); err != nil {
		return CommitRecord{}, held(err)
	}
	if err = s.commit(tx); err != nil {
		return CommitRecord{}, ErrUnknown
	}
	return record, nil
}

func (s *SQLStore) saveReceipt(ctx context.Context, receipt Receipt) (SavedReceipt, error) {
	r := receipt.Commit.Reservation
	if r.Binding != s.binding || receipt.LedgerSequence != r.Sequence {
		return SavedReceipt{}, ErrHeld
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return SavedReceipt{}, held(err)
	}
	defer tx.Rollback()
	if err = checkSchema(ctx, tx); err != nil {
		return SavedReceipt{}, err
	}
	frame, err := s.head(ctx, tx, true)
	if err != nil || frame.Phase != CPCommitted || frame.Attempt != r.Request.ID ||
		frame.Sequence != r.Sequence || frame.Checkpoint != receipt.Commit.After {
		return SavedReceipt{}, ErrHeld
	}
	var body string
	if tx.QueryRowContext(ctx, "SELECT body FROM v3_cp_lifecycle_attempts WHERE deployment=? AND attempt=?", s.binding.Deployment, r.Request.ID).Scan(&body) != nil ||
		body != encode(receipt.Commit) {
		return SavedReceipt{}, ErrHeld
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_lifecycle_receipts VALUES(?,?,?)", s.binding.Deployment, r.Request.ID, encode(receipt)); err != nil {
		return SavedReceipt{}, ErrConflict
	}
	frame.Phase = ReceiptSaved
	if _, err = tx.ExecContext(ctx, "UPDATE v3_cp_lifecycle_heads SET body=? WHERE deployment=?", encode(frame), s.binding.Deployment); err != nil {
		return SavedReceipt{}, held(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO v3_cp_lifecycle_history(deployment,attempt,phase,body) VALUES(?,?,?,?)", s.binding.Deployment, r.Request.ID, ReceiptSaved, encode(receipt)); err != nil {
		return SavedReceipt{}, held(err)
	}
	if err = s.commit(tx); err != nil {
		return SavedReceipt{}, ErrUnknown
	}
	return SavedReceipt{Receipt: receipt, proof: &receiptConfirmation{db: s.db, body: encode(receipt)}}, nil
}

func (s *SQLStore) quarantine(ctx context.Context, id string, phase Phase) error {
	if s == nil || ctx == nil || !validID(id) {
		return ErrUnknown
	}
	// Only the safety journal may outlive a cancelled caller. No business effect,
	// retry, Finalize or slot release is performed by this bounded operation.
	safetyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(safetyCtx, "INSERT INTO v3_cp_lifecycle_unknowns VALUES(?,?,?)", s.binding.Deployment, id, phase)
	if err != nil {
		return errors.Join(ErrUnknown, err)
	}
	return nil
}

func (s *SQLStore) inspect(ctx context.Context, id string, auth Authorization, source Authorizer) (*CommitRecord, *Receipt, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, false, held(err)
	}
	defer tx.Rollback()
	if err = checkSchema(ctx, tx); err != nil {
		return nil, nil, false, err
	}
	frame, err := s.head(ctx, tx, false)
	if err != nil || source.ValidateTx(ctx, tx, auth, frame.Checkpoint) != nil {
		return nil, nil, false, ErrHeld
	}
	var count int
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM v3_cp_lifecycle_unknowns WHERE deployment=? AND attempt=?", s.binding.Deployment, id).Scan(&count) != nil {
		return nil, nil, false, ErrHeld
	}
	var record *CommitRecord
	var receipt *Receipt
	var body string
	err = tx.QueryRowContext(ctx, "SELECT body FROM v3_cp_lifecycle_attempts WHERE deployment=? AND attempt=?", s.binding.Deployment, id).Scan(&body)
	if err == nil {
		record = new(CommitRecord)
		if decode(body, record) != nil || record.Reservation.Binding != s.binding || record.Reservation.Request.ID != id {
			return nil, nil, false, ErrHeld
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, held(err)
	}
	err = tx.QueryRowContext(ctx, "SELECT body FROM v3_cp_lifecycle_receipts WHERE deployment=? AND attempt=?", s.binding.Deployment, id).Scan(&body)
	if err == nil {
		receipt = new(Receipt)
		if decode(body, receipt) != nil || record == nil || !same(receipt.Commit, *record) {
			return nil, nil, false, ErrHeld
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, held(err)
	}
	return record, receipt, count != 0, nil
}
