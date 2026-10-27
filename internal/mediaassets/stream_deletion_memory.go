package mediaassets

import (
	"context"
	"time"
)

// RetireStreamCoverAssets mirrors SQL's atomic guarded stream retirement for
// the in-memory server used by isolated fixtures. Call only after DeleteStream.
func (r *MemoryRepository) RetireStreamCoverAssets(_ context.Context, streamID string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.references, streamID)
	for id, asset := range r.assets {
		if asset.OwnerType != "stream" || asset.OwnerID != streamID || asset.UsageType != "video_cover" {
			continue
		}
		referenced := false
		for _, refs := range r.references {
			for variantID := range refs {
				if r.variants[variantID].AssetID == id {
					referenced = true
				}
			}
		}
		if referenced {
			continue
		}
		asset.DeletedAt = &now
		asset.RetentionUntil = &now
		asset.UpdatedAt = now
		r.assets[id] = asset
	}
	return nil
}
