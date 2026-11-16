package lifecyclefoundation

import (
	"context"
	"database/sql"
	"sync"
)

// approvalTransaction is a local management participant, not a lifecycle Gate
// admission or authority. Only its owner can complete the actual SQL transaction.
// Its private fields prove the DB, transaction, context and immutable request.
type approvalTransaction struct {
	*sql.Tx
	mu      sync.Mutex
	db      *sql.DB
	ctx     context.Context
	binding string
	active  bool
}

// BeginApprovalSQL is explicitly opt-in. A lifecycle transaction cannot be joined
// through this path: B0/B1 keep their existing owner and lock ordering.
func BeginApprovalSQL(ctx context.Context, db *sql.DB, binding string) (SQLTransaction, error) {
	if ctx == nil || ctx.Err() != nil || db == nil || binding == "" {
		return nil, ErrHeld
	}
	if _, bound, err := SQL(ctx, db); err != nil || bound {
		return nil, ErrHeld
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	return &approvalTransaction{Tx: tx, db: db, ctx: ctx, binding: binding, active: true}, nil
}

// ApprovalSQL exposes only an executor of the very transaction created above.
// A raw *sql.Tx, another pool, a copied request or a completed handle is rejected.
func ApprovalSQL(ctx context.Context, db *sql.DB, transaction SQLTransaction, binding string) (Executor, error) {
	tx, ok := transaction.(*approvalTransaction)
	if !ok || tx == nil {
		return nil, ErrHeld
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if !tx.active || tx.db != db || tx.ctx != ctx || tx.binding != binding || ctx.Err() != nil {
		return nil, ErrHeld
	}
	return &approvalExecutor{owner: tx}, nil
}

type approvalExecutor struct{ owner *approvalTransaction }

func (e *approvalExecutor) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return e.owner.Tx.QueryRowContext(ctx, q, args...)
}
func (e *approvalExecutor) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return e.owner.Tx.QueryContext(ctx, q, args...)
}
func (e *approvalExecutor) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return e.owner.Tx.ExecContext(ctx, q, args...)
}
func (t *approvalTransaction) Commit() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return sql.ErrTxDone
	}
	t.active = false
	return t.Tx.Commit()
}
func (t *approvalTransaction) Rollback() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return sql.ErrTxDone
	}
	t.active = false
	return t.Tx.Rollback()
}
