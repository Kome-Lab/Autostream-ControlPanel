package store

import (
	"context"
	"database/sql"
	"time"
)

// retireStreamCoverAssetsTx runs only after the normal DeleteStream guards.
// The stream tombstone and these pending asset tombstones commit together;
// physical file removal remains the responsibility of mediaassets GC.
func retireStreamCoverAssetsTx(ctx context.Context, tx *sql.Tx, streamID string, now time.Time) error {
	// Keep action IDs, fingerprints and outcomes for audit, while retiring this
	// completed stream's file references so they cannot pin its uploads forever.
	for _, mutation := range []struct {
		query string
		args  []any
	}{
		{`UPDATE stream_video_cover_actions SET requested_asset_variant_id=NULL WHERE stream_id=?`, []any{streamID}},
		{`UPDATE stream_video_cover_runtime SET asset_variant_id=NULL,applied_asset_variant_id=NULL,retired_at=COALESCE(retired_at,?) WHERE stream_id=?`, []any{now, streamID}},
		{`DELETE FROM stream_video_cover_candidates WHERE stream_id=?`, []any{streamID}},
		{`UPDATE stream_visual_settings SET cover_source='none',cover_preset_id=NULL,cover_preset_revision=NULL,cover_asset_id=NULL,cover_variant_id=NULL,cover_start_active=FALSE WHERE stream_id=?`, []any{streamID}},
	} {
		if _, err := tx.ExecContext(ctx, mutation.query, mutation.args...); err != nil {
			return err
		}
	}
	// Ownership, rather than owner_user_id or membership in the current
	// candidate list, finds excluded uploads too. Shared presets, backgrounds,
	// other streams and any retained live reference stay usable and protected.
	_, err := tx.ExecContext(ctx, `UPDATE media_assets a SET
	  a.deleted_at=COALESCE(a.deleted_at,?),a.retention_until=?,a.updated_at=?
	  WHERE a.owner_type='stream' AND a.owner_id=? AND a.usage_type='video_cover'
	  AND NOT EXISTS (SELECT 1 FROM video_cover_presets p WHERE p.asset_id=a.id AND p.deleted_at IS NULL)
	  AND NOT EXISTS (SELECT 1 FROM stream_visual_settings sv WHERE sv.background_asset_id=a.id OR sv.cover_asset_id=a.id)
	  AND NOT EXISTS (SELECT 1 FROM stream_video_cover_candidates c WHERE c.asset_id=a.id)
	  AND NOT EXISTS (SELECT 1 FROM stream_video_cover_runtime vr JOIN media_asset_variants v ON v.id=vr.asset_variant_id OR v.id=vr.applied_asset_variant_id WHERE v.asset_id=a.id)
	  AND NOT EXISTS (SELECT 1 FROM stream_video_cover_actions ca JOIN media_asset_variants v ON v.id=ca.requested_asset_variant_id WHERE v.asset_id=a.id)`, now, now, now, streamID)
	if err != nil {
		return err
	}
	// stream_logs are append-only retained history. Keep identity and content
	// digest after GC purges the original/variant metadata, without retaining a
	// live foreign-key reference that would permanently pin the files.
	_, err = tx.ExecContext(ctx, `INSERT INTO stream_logs(id,stream_id,level,message,fields,created_at)
	  SELECT UUID(),?,'info','stream cover asset deletion scheduled',
	    JSON_OBJECT('asset_id',a.id,'sha256',a.sha256,'cleanup_status','scheduled'),?
	  FROM media_assets a
	  WHERE a.owner_type='stream' AND a.owner_id=? AND a.usage_type='video_cover'
	    AND a.deleted_at IS NOT NULL AND a.retention_until=? AND a.updated_at=?`, streamID, now, streamID, now, now)
	return err
}
