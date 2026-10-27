package mediaassets

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// queueFileDeletionsTx also serializes publication of metadata for the same
// content-addressed file. Writers verify the file while holding this row lock;
// the collector holds the same lock across its reference check and removal.
func queueFileDeletionsTx(ctx context.Context, tx *sql.Tx, keys []string, now time.Time) error {
	unique := map[string]bool{}
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			unique[key] = true
		}
	}
	ordered := make([]string, 0, len(unique))
	for key := range unique {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		// Acquire an exclusive duplicate-key lock directly. INSERT IGNORE
		// followed by a lock upgrade can deadlock two identical uploads.
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_asset_file_deletions(storage_key,created_at,updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE storage_key=VALUES(storage_key)`, key, now, now); err != nil {
			return err
		}
		var locked string
		if err := tx.QueryRowContext(ctx, `SELECT storage_key FROM media_asset_file_deletions WHERE storage_key=? FOR UPDATE`, key).Scan(&locked); err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLRepository) queueFileDeletions(ctx context.Context, keys []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := queueFileDeletionsTx(ctx, tx, keys, r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLRepository) queueUnpublishedFile(ctx context.Context, key string, published *bool, resultErr *error) {
	if *published {
		return
	}
	// Request cancellation must not erase the retry record for an already
	// processed file. Bound this independent cleanup transaction.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	*resultErr = errors.Join(*resultErr, r.queueFileDeletions(cleanupCtx, []string{key}))
}

func (r *SQLRepository) collectFileDeletions(ctx context.Context, now time.Time, limit int) error {
	rows, err := r.db.QueryContext(ctx, `SELECT storage_key FROM media_asset_file_deletions ORDER BY updated_at,storage_key LIMIT ?`, limit)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var failures []error
	for _, key := range keys {
		if err := r.collectFileDeletion(ctx, key, now); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (r *SQLRepository) collectFileDeletion(ctx context.Context, key string, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT storage_key FROM media_asset_file_deletions WHERE storage_key=? FOR UPDATE`, key).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	var references int
	if err := tx.QueryRowContext(ctx, `SELECT
	  (SELECT COUNT(*) FROM media_assets WHERE storage_key=?) +
	  (SELECT COUNT(*) FROM media_asset_variants WHERE storage_key=?)`, key, key).Scan(&references); err != nil {
		return err
	}
	if references == 0 {
		if err := r.storage.Remove(key); err != nil {
			if _, updateErr := tx.ExecContext(ctx, `UPDATE media_asset_file_deletions SET attempts=attempts+1,last_error_code='media_asset_file_remove_failed',updated_at=? WHERE storage_key=?`, now, key); updateErr != nil {
				return updateErr
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return commitErr
			}
			return errors.New("media_asset_file_remove_failed")
		}
	}
	// A shared live file is retained. Its eventual final metadata deletion
	// enqueues the key again; this row need not remain permanently pending.
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_asset_file_deletions WHERE storage_key=?`, key); err != nil {
		return err
	}
	return tx.Commit()
}
