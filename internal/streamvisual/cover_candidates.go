package streamvisual

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/example/autostream-control-panel/internal/mediaassets"
)

type rowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadCoverCandidates(ctx context.Context, q rowsQueryer, settings *Settings) error {
	rows, err := q.QueryContext(ctx, `SELECT display_name,source,COALESCE(preset_id,''),COALESCE(preset_revision,0),asset_id,variant_id FROM stream_video_cover_candidates WHERE stream_id=? ORDER BY sort_order,variant_id`, settings.StreamID)
	if err != nil {
		return err
	}
	defer rows.Close()
	settings.CoverCandidates = []CoverCandidate{}
	for rows.Next() {
		var candidate CoverCandidate
		if err := rows.Scan(&candidate.Name, &candidate.Source, &candidate.PresetID, &candidate.PresetRevision, &candidate.AssetID, &candidate.VariantID); err != nil {
			return err
		}
		settings.CoverCandidates = append(settings.CoverCandidates, candidate)
	}
	return rows.Err()
}

func candidateFromSettings(s Settings) CoverCandidate {
	return CoverCandidate{Name: "フタ画像", Source: s.CoverSource, PresetID: s.CoverPresetID, PresetRevision: s.CoverPresetRevision, AssetID: s.CoverAssetID, VariantID: s.CoverVariantID}
}

func SelectCoverCandidate(s Settings, variantID string) (Settings, bool) {
	for _, c := range s.CoverCandidates {
		if c.VariantID == variantID {
			s.CoverSource, s.CoverPresetID, s.CoverPresetRevision, s.CoverAssetID, s.CoverVariantID = c.Source, c.PresetID, c.PresetRevision, c.AssetID, c.VariantID
			return s, true
		}
	}
	return s, false
}

func (r *SQLRepository) resolveCoverCandidates(ctx context.Context, tx *sql.Tx, userID string, current Settings, next *Settings, update Update) error {
	if update.CoverCandidates == nil {
		next.CoverCandidates = append([]CoverCandidate{}, current.CoverCandidates...)
		if next.CoverSource != "none" && next.CoverVariantID != "" {
			if _, found := SelectCoverCandidate(*next, next.CoverVariantID); !found {
				next.CoverCandidates = append(next.CoverCandidates, candidateFromSettings(*next))
			}
		}
		if err := validateCoverUploadLimit(current, *next); err != nil {
			return err
		}
		return saveCoverCandidates(ctx, tx, *next)
	}
	if len(*update.CoverCandidates) > 32 {
		return ErrInvalidSettings
	}
	presetIDs := []string{}
	for _, c := range *update.CoverCandidates {
		if c.Source == "preset" {
			presetIDs = append(presetIDs, c.PresetID)
		}
	}
	sort.Strings(presetIDs)
	for _, id := range presetIDs {
		var locked string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM video_cover_presets WHERE id=? FOR UPDATE`, id).Scan(&locked); err != nil {
			return ErrInvalidSettings
		}
	}
	next.CoverCandidates = make([]CoverCandidate, 0, len(*update.CoverCandidates))
	seen := map[string]bool{}
	for _, input := range *update.CoverCandidates {
		input.Name = strings.TrimSpace(input.Name)
		if input.Name == "" || utf8.RuneCountInString(input.Name) > 255 {
			return ErrInvalidSettings
		}
		candidateSettings := *next
		candidateSettings.CoverSource, candidateSettings.CoverPresetID, candidateSettings.CoverPresetRevision, candidateSettings.CoverAssetID, candidateSettings.CoverVariantID = input.Source, input.PresetID, input.PresetRevision, input.AssetID, input.VariantID
		old, exists := SelectCoverCandidate(current, input.VariantID)
		if exists {
			// Existing snapshots survive preset edits/deletion. Only their display
			// name can change; a different descriptor must be resolved as new.
			if old.CoverSource != input.Source || old.CoverPresetID != input.PresetID || old.CoverPresetRevision != input.PresetRevision || old.CoverAssetID != input.AssetID {
				return ErrInvalidSettings
			}
		} else {
			if input.Source != "preset" && input.Source != "upload" {
				return ErrInvalidSettings
			}
			if err := r.resolveAndValidate(ctx, tx, userID, next.StreamID, &candidateSettings, updateChanges{coverSelection: true}); err != nil {
				return err
			}
			if input.VariantID != "" && input.VariantID != candidateSettings.CoverVariantID || input.AssetID != "" && input.AssetID != candidateSettings.CoverAssetID {
				return ErrInvalidSettings
			}
			if input.Source == "preset" && input.PresetRevision != 0 && input.PresetRevision != candidateSettings.CoverPresetRevision {
				return ErrRevisionConflict
			}
			input.PresetRevision, input.AssetID, input.VariantID = candidateSettings.CoverPresetRevision, candidateSettings.CoverAssetID, candidateSettings.CoverVariantID
		}
		if input.VariantID == "" || seen[input.VariantID] {
			return ErrInvalidSettings
		}
		seen[input.VariantID] = true
		next.CoverCandidates = append(next.CoverCandidates, input)
	}
	if next.CoverSource != "none" {
		selected, found := SelectCoverCandidate(*next, next.CoverVariantID)
		if !found {
			return ErrInvalidSettings
		}
		*next = selected
	}
	if err := validateCoverUploadLimit(current, *next); err != nil {
		return err
	}
	// Pending/confirmed output remains protected even after a later settings edit.
	for _, old := range current.CoverCandidates {
		if seen[old.VariantID] {
			continue
		}
		var protected bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stream_video_cover_runtime WHERE stream_id=? AND retired_at IS NULL AND job_generation=(SELECT MAX(latest.job_generation) FROM stream_video_cover_runtime latest WHERE latest.stream_id=stream_video_cover_runtime.stream_id) AND (applied_asset_variant_id=? AND applied_active=TRUE OR asset_variant_id=? AND reconciliation_status IN ('idle','confirming')))`, next.StreamID, old.VariantID, old.VariantID).Scan(&protected); err != nil {
			return err
		}
		if protected {
			return ErrStreamStateLocked
		}
	}
	return saveCoverCandidates(ctx, tx, *next)
}

// Existing over-limit collections may be retained or reduced without losing
// data. Any addition or replacement must converge to the four-upload limit.
// Multiple variants of one original upload consume one slot; presets consume
// none. Include the selected descriptor for the candidate-omitted API path.
func validateCoverUploadLimit(current, next Settings) error {
	oldAssets := coverUploadAssets(current)
	nextAssets := coverUploadAssets(next)
	if len(nextAssets) <= mediaassets.MaxStreamCoverUploads {
		return nil
	}
	for id := range nextAssets {
		if !oldAssets[id] {
			return mediaassets.ErrCoverUploadLimit
		}
	}
	return nil
}

func coverUploadAssets(settings Settings) map[string]bool {
	assets := map[string]bool{}
	for _, candidate := range settings.CoverCandidates {
		if candidate.Source == "upload" && strings.TrimSpace(candidate.AssetID) != "" {
			assets[strings.TrimSpace(candidate.AssetID)] = true
		}
	}
	if settings.CoverSource == "upload" && strings.TrimSpace(settings.CoverAssetID) != "" {
		assets[strings.TrimSpace(settings.CoverAssetID)] = true
	}
	return assets
}

func saveCoverCandidates(ctx context.Context, tx *sql.Tx, s Settings) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM stream_video_cover_candidates WHERE stream_id=?`, s.StreamID); err != nil {
		return err
	}
	for index, c := range s.CoverCandidates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_video_cover_candidates(stream_id,variant_id,asset_id,display_name,source,preset_id,preset_revision,sort_order) VALUES(?,?,?,?,?,?,?,?)`, s.StreamID, c.VariantID, c.AssetID, c.Name, c.Source, nullString(c.PresetID), nullUint(c.PresetRevision), index); err != nil {
			return err
		}
	}
	return nil
}
