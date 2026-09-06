package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Cleanup is confined to the fixture's exact host/service/token identities.
// It uses its own bounded context after crash tests have closed the child pool.
func (f *mariaDBSTPortV2Fixture) registerCleanup(t *testing.T, db *sql.DB) {
	t.Helper()
	hostID, agentID, targetID, obsID := f.policy.ExecutionHostID, f.policy.UpdaterID, f.targetID, f.observabilityID
	initialAgentToken, userID := f.agentToken.ID, "self-update-user-"+f.suffix
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Error("fixture cleanup could not start transaction")
			return
		}
		defer tx.Rollback()
		tokens := map[string]bool{initialAgentToken: true}
		rows, err := tx.QueryContext(ctx, `SELECT token_id FROM services WHERE service_id IN (?,?,?) UNION SELECT staged_node_token_id FROM services WHERE service_id IN (?,?,?) UNION SELECT previous_token_id FROM system_update_runtime_token_rotations WHERE execution_host_id=? UNION SELECT staged_token_id FROM system_update_runtime_token_rotations WHERE execution_host_id=? UNION SELECT emergency_revoked_token_id FROM system_update_runtime_token_rotations WHERE execution_host_id=?`, agentID, targetID, obsID, agentID, targetID, obsID, hostID, hostID, hostID)
		if err != nil {
			t.Error("fixture cleanup could not enumerate own token references")
			return
		}
		for rows.Next() {
			var id sql.NullString
			if err = rows.Scan(&id); err != nil {
				break
			}
			if id.Valid && id.String != "" {
				tokens[id.String] = true
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			t.Error("fixture cleanup token enumeration failed")
			return
		}
		for _, table := range []string{"system_update_host_self_update_grants", "system_update_host_self_updates", "system_update_port_transactions", "system_update_jobs", "system_update_runtime_token_rotations", "service_port_reservations"} {
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE execution_host_id=?`, hostID); err != nil {
				t.Errorf("fixture cleanup failed for %s", table)
				return
			}
		}
		for _, operation := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM update_agent_policies WHERE service_id=?`, []any{agentID}},
			{`DELETE FROM system_update_execution_hosts WHERE execution_host_id=?`, []any{hostID}},
			{`DELETE FROM services WHERE service_id IN (?,?,?)`, []any{agentID, targetID, obsID}},
			{`DELETE FROM users WHERE id=?`, []any{userID}},
		} {
			if _, err = tx.ExecContext(ctx, operation.query, operation.args...); err != nil {
				t.Error("fixture cleanup failed for owned identities")
				return
			}
		}
		for id := range tokens {
			if id != "" {
				if _, err = tx.ExecContext(ctx, `DELETE FROM service_tokens WHERE id=?`, id); err != nil {
					t.Error("fixture cleanup failed for owned token")
					return
				}
			}
		}
		if err = tx.Commit(); err != nil {
			t.Error("fixture cleanup commit failed")
		}
	})
}
