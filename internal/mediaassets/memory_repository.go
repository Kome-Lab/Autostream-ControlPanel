package mediaassets

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

type MemoryRepository struct {
	mu         sync.Mutex
	storage    *DiskStorage
	processor  *Processor
	sessions   map[string]UploadSession
	assets     map[string]Asset
	variants   map[string]Variant
	references map[string]map[string]bool
	now        func() time.Time
}

func NewMemoryRepository(storageRoot string) (*MemoryRepository, error) {
	storage, err := NewDiskStorage(storageRoot)
	if err != nil {
		return nil, err
	}
	return &MemoryRepository{
		storage: storage, processor: NewProcessor(storage), sessions: map[string]UploadSession{},
		assets: map[string]Asset{}, variants: map[string]Variant{}, references: map[string]map[string]bool{},
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (r *MemoryRepository) CreateUploadSession(_ context.Context, userID string, expiresAt time.Time) (UploadSession, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UploadSession{}, ErrForbidden
	}
	now := r.now()
	if expiresAt.IsZero() {
		expiresAt = now.Add(DraftRetention)
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(DraftRetention)) {
		return UploadSession{}, ErrDraftExpired
	}
	id, err := newID()
	if err != nil {
		return UploadSession{}, err
	}
	session := UploadSession{ID: id, UserID: userID, OwnerType: "upload_draft", ExpiresAt: expiresAt.UTC(), CreatedAt: now, UpdatedAt: now}
	r.mu.Lock()
	r.sessions[id] = session
	r.mu.Unlock()
	return session, nil
}

func (r *MemoryRepository) Upload(_ context.Context, in UploadInput) (Asset, error) {
	in.SessionID, in.UserID, in.UsageType = strings.TrimSpace(in.SessionID), strings.TrimSpace(in.UserID), strings.TrimSpace(in.UsageType)
	if in.UsageType != "scene_background" && in.UsageType != "video_cover" {
		return Asset{}, ErrForbidden
	}
	r.mu.Lock()
	session, ok := r.sessions[in.SessionID]
	if !ok {
		r.mu.Unlock()
		return Asset{}, ErrNotFound
	}
	if session.UserID != in.UserID {
		r.mu.Unlock()
		return Asset{}, ErrForbidden
	}
	if session.OwnerType != "upload_draft" || session.ClaimedStreamID != "" {
		r.mu.Unlock()
		return Asset{}, ErrDraftClaimed
	}
	if !session.ExpiresAt.After(r.now()) {
		r.mu.Unlock()
		return Asset{}, ErrDraftExpired
	}
	r.mu.Unlock()
	processed, err := r.processor.ProcessUpload(in.Filename, in.ContentType, in.Body)
	if err != nil {
		return Asset{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok = r.sessions[in.SessionID]
	if !ok || session.UserID != in.UserID {
		return Asset{}, ErrForbidden
	}
	if session.OwnerType != "upload_draft" || !session.ExpiresAt.After(r.now()) {
		return Asset{}, ErrDraftClaimed
	}
	id, err := newID()
	if err != nil {
		return Asset{}, err
	}
	now := r.now()
	asset := Asset{ID: id, OwnerUserID: in.UserID, OwnerType: "upload_draft", OwnerID: in.SessionID, UploadSessionID: in.SessionID, UsageType: in.UsageType, StorageKey: processed.StorageKey, SHA256: processed.SHA256, MediaType: processed.MediaType, ByteSize: processed.ByteSize, Width: processed.Width, Height: processed.Height, Opaque: processed.Opaque, ProcessorRevision: ProcessorRevision, CreatedAt: now, UpdatedAt: now}
	r.assets[id] = asset
	return asset, nil
}

func (r *MemoryRepository) GetAsset(_ context.Context, userID, assetID string) (Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	asset, ok := r.assets[strings.TrimSpace(assetID)]
	if !ok || asset.OwnerUserID != strings.TrimSpace(userID) || asset.DeletedAt != nil {
		return Asset{}, ErrNotFound
	}
	return asset, nil
}

func (r *MemoryRepository) EnsureVariant(ctx context.Context, userID, assetID string, width, height int, opaque bool) (Variant, error) {
	asset, err := r.GetAsset(ctx, userID, assetID)
	if err != nil {
		return Variant{}, err
	}
	if err := validateDimensions(width, height); err != nil {
		return Variant{}, err
	}
	crop := "center_crop"
	if opaque {
		crop = "center_crop_opaque"
	}
	r.mu.Lock()
	for _, variant := range r.variants {
		if variant.AssetID == asset.ID && variant.TargetWidth == width && variant.TargetHeight == height && variant.CropMode == crop && variant.ProcessorRevision == ProcessorRevision {
			r.mu.Unlock()
			if variant.Status == "ready" && r.processor.Verify(processedFromVariant(variant)) != nil {
				return Variant{}, ErrIntegrity
			}
			return variant, nil
		}
	}
	r.mu.Unlock()
	processed, err := r.processor.CreateVariant(processedFromAsset(asset), width, height, opaque)
	if err != nil {
		return Variant{}, err
	}
	id, err := newID()
	if err != nil {
		return Variant{}, err
	}
	now := r.now()
	variant := Variant{ID: id, AssetID: asset.ID, TargetWidth: width, TargetHeight: height, CropMode: crop, ProcessorRevision: ProcessorRevision, Status: "ready", StorageKey: processed.StorageKey, SHA256: processed.SHA256, MediaType: processed.MediaType, ByteSize: processed.ByteSize, Width: processed.Width, Height: processed.Height, Opaque: processed.Opaque, CreatedAt: now, UpdatedAt: now}
	r.mu.Lock()
	r.variants[id] = variant
	r.mu.Unlock()
	return variant, nil
}

func (r *MemoryRepository) SoftDeleteAsset(_ context.Context, userID, assetID string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	asset, ok := r.assets[strings.TrimSpace(assetID)]
	if !ok || asset.OwnerUserID != strings.TrimSpace(userID) {
		return ErrNotFound
	}
	if now.IsZero() {
		now = r.now()
	}
	retention := now.Add(DraftRetention)
	asset.DeletedAt = &now
	asset.RetentionUntil = &retention
	asset.UpdatedAt = now
	r.assets[asset.ID] = asset
	return nil
}

func (r *MemoryRepository) ClaimDraft(_ context.Context, userID, sessionID, streamID string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	if session.UserID != userID {
		return ErrForbidden
	}
	if session.OwnerType == "stream" && session.ClaimedStreamID == streamID {
		return nil
	}
	if session.OwnerType != "upload_draft" || session.ClaimedStreamID != "" {
		return ErrDraftClaimed
	}
	if !session.ExpiresAt.After(now) {
		return ErrDraftExpired
	}
	session.OwnerType = "stream"
	session.ClaimedStreamID = streamID
	session.UpdatedAt = now
	claimedAt := now
	session.ClaimedAt = &claimedAt
	r.sessions[sessionID] = session
	for id, asset := range r.assets {
		if asset.UploadSessionID == sessionID {
			if asset.OwnerUserID != userID {
				return ErrForbidden
			}
			asset.OwnerType = "stream"
			asset.OwnerID = streamID
			asset.UpdatedAt = now
			r.assets[id] = asset
		}
	}
	return nil
}

func (r *MemoryRepository) ReferenceVariant(streamID, variantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.references[streamID] == nil {
		r.references[streamID] = map[string]bool{}
	}
	r.references[streamID][variantID] = true
}

func (r *MemoryRepository) OpenInternalVariant(_ context.Context, streamID, variantID string) (InternalAsset, error) {
	r.mu.Lock()
	variant, ok := r.variants[variantID]
	referenced := r.references[streamID][variantID]
	asset := r.assets[variant.AssetID]
	r.mu.Unlock()
	if !ok || !referenced {
		return InternalAsset{}, ErrForbidden
	}
	if variant.Status != "ready" || r.processor.Verify(processedFromVariant(variant)) != nil {
		return InternalAsset{}, ErrIntegrity
	}
	reader, err := r.storage.Open(variant.StorageKey)
	if err != nil {
		return InternalAsset{}, ErrIntegrity
	}
	return InternalAsset{Asset: asset, Variant: variant, Reader: reader}, nil
}

func (r *MemoryRepository) VerifyVariant(_ context.Context, assetID, variantID string) error {
	r.mu.Lock()
	variant, ok := r.variants[variantID]
	r.mu.Unlock()
	if !ok || variant.AssetID != assetID || variant.Status != "ready" {
		return ErrNotFound
	}
	return r.processor.Verify(processedFromVariant(variant))
}

func (r *MemoryRepository) GarbageCollect(_ context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, errors.New("gc limit must be between 1 and 500")
	}
	if now.IsZero() {
		now = r.now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for id, asset := range r.assets {
		if removed >= limit {
			break
		}
		eligible := asset.DeletedAt != nil && asset.RetentionUntil != nil && !asset.RetentionUntil.After(now)
		if asset.OwnerType == "upload_draft" {
			if s, ok := r.sessions[asset.UploadSessionID]; ok && !s.ExpiresAt.After(now) {
				eligible = true
			}
		}
		if !eligible {
			continue
		}
		referenced := false
		for _, refs := range r.references {
			for variantID := range refs {
				if v, ok := r.variants[variantID]; ok && v.AssetID == id {
					referenced = true
				}
			}
		}
		if referenced {
			continue
		}
		storageKeys := map[string]bool{asset.StorageKey: true}
		for variantID, v := range r.variants {
			if v.AssetID == id {
				storageKeys[v.StorageKey] = true
				delete(r.variants, variantID)
			}
		}
		delete(r.assets, id)
		for storageKey := range storageKeys {
			if !r.storageKeyReferencedLocked(storageKey) {
				_ = r.storage.Remove(storageKey)
			}
		}
		removed++
	}
	for sessionID, session := range r.sessions {
		if session.ExpiresAt.After(now) {
			continue
		}
		if session.OwnerType == "stream" || !r.sessionHasAssetsLocked(sessionID) {
			delete(r.sessions, sessionID)
		}
	}
	return removed, nil
}

func (r *MemoryRepository) sessionHasAssetsLocked(sessionID string) bool {
	for _, asset := range r.assets {
		if asset.UploadSessionID == sessionID {
			return true
		}
	}
	return false
}

func (r *MemoryRepository) storageKeyReferencedLocked(storageKey string) bool {
	for _, asset := range r.assets {
		if asset.StorageKey == storageKey {
			return true
		}
	}
	for _, variant := range r.variants {
		if variant.StorageKey == storageKey {
			return true
		}
	}
	return false
}

var _ Repository = (*MemoryRepository)(nil)
var _ IntegrityInspector = (*MemoryRepository)(nil)
