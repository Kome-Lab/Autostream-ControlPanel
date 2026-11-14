package store

import (
	"context"
	"database/sql"
	"errors"

	oa "github.com/example/autostream-control-panel/internal/operationalauthorization"
)

var errOperationalSource = errors.New("operational source unavailable or inconsistent")

// Epoch lock precedes every consistent read. Legacy authorization writes join
// this generation through transactional triggers, without a copied authority.
func operationalRead(ctx context.Context, db *sql.DB, f func(*sql.Tx, uint64) error) error {
	if ctx == nil || db == nil {
		return errOperationalSource
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation uint64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM operational_authorization_epoch WHERE singleton=1 FOR UPDATE`).Scan(&generation); err != nil {
		return errOperationalSource
	}
	if generation == 0 {
		return errOperationalSource
	}
	if err := oa.CheckOperationalSchema(ctx, tx); err != nil {
		return err
	}
	if err := f(tx, generation); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errOperationalSource
	}
	return nil
}

func (s MariaDBAuthStore) Revision(ctx context.Context) (uint64, error) {
	var generation uint64
	err := operationalRead(ctx, s.db, func(_ *sql.Tx, current uint64) error { generation = current; return nil })
	return generation, err
}

func (s MariaDBAuthStore) Snapshot(ctx context.Context, principalID string) (oa.Snapshot, error) {
	var snapshot oa.Snapshot
	err := operationalRead(ctx, s.db, func(tx *sql.Tx, generation uint64) error {
		principal := oa.Principal{Kind: oa.KindHuman}
		if err := tx.QueryRowContext(ctx, `SELECT id,status FROM users WHERE id=?`, principalID).Scan(&principal.ID, &principal.State); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT ur.role_id,r.name,rp.permission FROM user_roles ur LEFT JOIN roles r ON r.id=ur.role_id LEFT JOIN role_permissions rp ON rp.role_id=ur.role_id WHERE ur.user_id=? ORDER BY ur.role_id,rp.permission`, principalID)
		if err != nil {
			return err
		}
		var roles []oa.Role
		for rows.Next() {
			var roleID string
			var name, operation sql.NullString
			if err := rows.Scan(&roleID, &name, &operation); err != nil {
				rows.Close()
				return err
			}
			if !name.Valid || roleID == "" {
				rows.Close()
				return errOperationalSource
			}
			if len(roles) == 0 || roles[len(roles)-1].ID != roleID {
				roles = append(roles, oa.Role{ID: roleID, Name: name.String})
				principal.RoleIDs = append(principal.RoleIDs, roleID)
			}
			if operation.Valid {
				roles[len(roles)-1].Operations = append(roles[len(roles)-1].Operations, operation.String)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		projected, err := oa.Project(principal, roles, generation)
		if err != nil {
			return err
		}
		snapshot = projected
		return nil
	})
	if err != nil {
		return oa.Snapshot{}, err
	}
	return snapshot, nil
}
