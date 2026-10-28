package mediaassets

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
)

var uploadOperationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ValidUploadOperationID(value string) bool { return uploadOperationPattern.MatchString(value) }

type DraftUploadAsset struct {
	Asset    Asset     `json:"asset"`
	Variants []Variant `json:"variants"`
}

type DraftUploadSnapshot struct {
	Session     UploadSession      `json:"session"`
	OperationID string             `json:"operation_id"`
	Assets      []DraftUploadAsset `json:"assets"`
}

// Inspection conveys committed metadata only. An empty result is never proof
// that an upload with a lost response cannot still commit later.
type DraftUploadInspector interface {
	InspectDraftUpload(context.Context, string, string, string) (DraftUploadSnapshot, error)
}

func existingOperationAsset(asset Asset, in UploadInput) (Asset, bool, error) {
	if asset.ID == "" {
		return Asset{}, false, nil
	}
	if asset.OwnerUserID != in.UserID || asset.ClientOperationSessionID != in.SessionID || asset.ClientOperationID != in.ClientOperationID || asset.UsageType != in.UsageType {
		return Asset{}, true, ErrForbidden
	}
	if asset.DeletedAt != nil {
		return Asset{}, true, ErrNotFound
	}
	if asset.OwnerType != "upload_draft" || asset.OwnerID != in.SessionID || asset.UploadSessionID != in.SessionID {
		return Asset{}, true, ErrDraftClaimed
	}
	return asset, true, nil
}

func sqlUploadOperation(ctx context.Context, query rowQuerier, in UploadInput) (Asset, bool, error) {
	if in.ClientOperationID == "" {
		return Asset{}, false, nil
	}
	asset, err := scanAsset(query.QueryRowContext(ctx, assetSelect+` WHERE client_operation_session_id=? AND client_operation_id=?`, in.SessionID, in.ClientOperationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, false, nil
	}
	if err != nil {
		return Asset{}, false, err
	}
	return existingOperationAsset(asset, in)
}

func (r *MemoryRepository) uploadOperationLocked(in UploadInput) (Asset, bool, error) {
	if in.ClientOperationID == "" {
		return Asset{}, false, nil
	}
	for _, asset := range r.assets {
		if asset.ClientOperationSessionID == in.SessionID && asset.ClientOperationID == in.ClientOperationID {
			return existingOperationAsset(asset, in)
		}
	}
	return Asset{}, false, nil
}

func (r *MemoryRepository) InspectDraftUpload(_ context.Context, userID, sessionID, operationID string) (DraftUploadSnapshot, error) {
	userID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(sessionID)
	if userID == "" || !ValidUploadOperationID(operationID) {
		return DraftUploadSnapshot{}, ErrForbidden
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok || session.UserID != userID {
		return DraftUploadSnapshot{}, ErrNotFound
	}
	result := DraftUploadSnapshot{Session: session, OperationID: operationID, Assets: []DraftUploadAsset{}}
	for _, asset := range r.assets {
		if asset.ClientOperationSessionID != sessionID || asset.OwnerUserID != userID || asset.ClientOperationID != operationID {
			continue
		}
		item := DraftUploadAsset{Asset: asset, Variants: []Variant{}}
		for _, variant := range r.variants {
			if variant.AssetID == asset.ID {
				item.Variants = append(item.Variants, variant)
			}
		}
		sort.Slice(item.Variants, func(i, j int) bool { return item.Variants[i].ID < item.Variants[j].ID })
		result.Assets = append(result.Assets, item)
	}
	return result, nil
}
