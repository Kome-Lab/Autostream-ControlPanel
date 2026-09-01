package videocover

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestDesiredAppliedGenerationRevisionIdempotencyAndAmbiguity(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	initial, err := repository.EnsureGeneration(ctx, "stream-1", 1, "variant-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if initial.DesiredRevision != 1 || initial.AppliedRevision != nil || initial.AppliedActive != nil {
		t.Fatalf("initial=%#v", initial)
	}
	request := ActionRequest{Active: true, ExpectedJobGeneration: 1, ExpectedRevision: 1, IdempotencyKey: "show-1"}
	prepared, err := repository.PrepareAction(ctx, "stream-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Dispatch || prepared.Replay || prepared.RequestedRevision != 2 || !prepared.State.DesiredActive || prepared.State.AppliedRevision != nil {
		t.Fatalf("prepared=%#v", prepared)
	}
	replay, err := repository.PrepareAction(ctx, "stream-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Dispatch || !replay.Replay {
		t.Fatalf("duplicate automatically resent: %#v", replay)
	}
	conflict := request
	conflict.Active = false
	conflict.HideConfirmed = true
	if _, err = repository.PrepareAction(ctx, "stream-1", conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key changed payload=%v", err)
	}
	confirming, err := repository.RecordAmbiguous(ctx, "stream-1", 1, "show-1")
	if err != nil {
		t.Fatal(err)
	}
	if confirming.Status != "confirming" || confirming.AppliedRevision != nil || confirming.AppliedActive != nil || confirming.LastErrorCode != "transport_outcome_unknown" {
		t.Fatalf("ambiguous fabricated applied state: %#v", confirming)
	}
	again, err := repository.PrepareAction(ctx, "stream-1", request)
	if err != nil || again.Dispatch {
		t.Fatalf("ambiguous replay resent: %#v err=%v", again, err)
	}
	if _, err = repository.PrepareAction(ctx, "stream-1", ActionRequest{Active: true, ExpectedJobGeneration: 1, ExpectedRevision: 1, IdempotencyKey: "stale"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision=%v", err)
	}
	applied, err := repository.RecordApplied(ctx, "stream-1", 1, "show-1", true, 2)
	if err != nil {
		t.Fatal(err)
	}
	if applied.AppliedActive == nil || !*applied.AppliedActive || applied.AppliedRevision == nil || *applied.AppliedRevision != 2 {
		t.Fatalf("applied=%#v", applied)
	}
	if _, err = repository.EnsureGeneration(ctx, "stream-1", 2, "variant-2", false); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.PrepareAction(ctx, "stream-1", ActionRequest{Active: true, ExpectedJobGeneration: 1, ExpectedRevision: 2, IdempotencyKey: "old-generation"}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale generation=%v", err)
	}
}

func TestRecordStartAppliedRequiresExactDesiredGenerationAndRevision(t *testing.T) {
	repository := NewMemoryRepository()
	if _, err := repository.EnsureGeneration(t.Context(), "stream-start", 3, "variant-3", true); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordStartApplied(t.Context(), "stream-start", 2, true, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong generation error=%v", err)
	}
	if _, err := repository.RecordStartApplied(t.Context(), "stream-start", 3, false, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("wrong desired state error=%v", err)
	}
	if _, err := repository.RecordStartApplied(t.Context(), "stream-start", 3, true, 2); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("wrong revision error=%v", err)
	}
	applied, err := repository.RecordStartApplied(t.Context(), "stream-start", 3, true, 1)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != "applied" || applied.AppliedActive == nil || !*applied.AppliedActive || applied.AppliedRevision == nil || *applied.AppliedRevision != 1 {
		t.Fatalf("start applied=%#v", applied)
	}
	if replay, err := repository.RecordStartApplied(t.Context(), "stream-start", 3, true, 1); err != nil || replay.Status != "applied" {
		t.Fatalf("exact replay=%#v err=%v", replay, err)
	}
}

func TestHistoricalActionResultCannotOverwriteCurrentActionState(t *testing.T) {
	repository := NewMemoryRepository()
	if _, err := repository.EnsureGeneration(t.Context(), "stream-race", 1, "variant-1", false); err != nil {
		t.Fatal(err)
	}
	first, err := repository.PrepareAction(t.Context(), "stream-race", ActionRequest{
		Active: true, ExpectedJobGeneration: 1, ExpectedRevision: 1, IdempotencyKey: "show-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.PrepareAction(t.Context(), "stream-race", ActionRequest{
		Active: false, ExpectedJobGeneration: 1, ExpectedRevision: first.RequestedRevision,
		IdempotencyKey: "hide-b", HideConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := repository.RecordAmbiguous(t.Context(), "stream-race", 1, "hide-b")
	if err != nil || current.Status != "confirming" || current.LastIdempotencyKey != "hide-b" {
		t.Fatalf("newer action was not confirming: %#v err=%v", current, err)
	}
	late, err := repository.RecordApplied(t.Context(), "stream-race", 1, "show-a", true, first.RequestedRevision)
	if err != nil {
		t.Fatal(err)
	}
	if late.Status != "confirming" || late.LastIdempotencyKey != "hide-b" || late.DesiredRevision != second.RequestedRevision || late.AppliedRevision != nil {
		t.Fatalf("historical result overwrote current action: %#v", late)
	}
	settled, err := repository.RecordApplied(t.Context(), "stream-race", 1, "hide-b", false, second.RequestedRevision)
	if err != nil || settled.Status != "applied" {
		t.Fatalf("current action did not settle: %#v err=%v", settled, err)
	}
	terminal, err := repository.RecordFailed(t.Context(), "stream-race", 1, "hide-b", "cover_graph_unavailable")
	if err != nil || terminal.Status != "applied" || terminal.LastErrorCode != "" {
		t.Fatalf("terminal action was overwritten: %#v err=%v", terminal, err)
	}
	replayed, found, err := repository.LookupActionReplay(t.Context(), "stream-race", ActionRequest{
		Active: false, ExpectedJobGeneration: 1, ExpectedRevision: first.RequestedRevision,
		IdempotencyKey: "hide-b", HideConfirmed: true,
	})
	if err != nil || !found || replayed.Outcome != "applied" || replayed.SafeErrorCode != "" {
		t.Fatalf("immutable terminal outcome unavailable: %#v found=%t err=%v", replayed, found, err)
	}
}

func TestExactActionOutcomeSurvivesGenerationRollover(t *testing.T) {
	repository := NewMemoryRepository()
	request := ActionRequest{Active: true, ExpectedJobGeneration: 1, ExpectedRevision: 1, IdempotencyKey: "show-before-rollover"}
	if _, err := repository.EnsureGeneration(t.Context(), "stream-rollover", 1, "variant-1", false); err != nil {
		t.Fatal(err)
	}
	prepared, err := repository.PrepareAction(t.Context(), "stream-rollover", request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.EnsureGeneration(t.Context(), "stream-rollover", 2, "variant-2", false); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.RecordFailed(t.Context(), "stream-rollover", 1, request.IdempotencyKey, "media_asset_integrity"); err != nil {
		t.Fatal(err)
	}
	if replay, found, err := repository.LookupActionReplay(t.Context(), "stream-rollover", request); err != nil || found || replay.State.JobGeneration != 2 {
		t.Fatalf("preflight replay ignored current-generation fence: %#v found=%t err=%v", replay, found, err)
	}
	outcome, found, err := repository.LookupActionOutcome(t.Context(), "stream-rollover", request)
	if err != nil || !found || outcome.RequestedRevision != prepared.RequestedRevision || outcome.Outcome != "failed" || outcome.SafeErrorCode != "media_asset_integrity" || outcome.State.JobGeneration != 1 {
		t.Fatalf("exact terminal outcome unavailable after rollover: %#v found=%t err=%v", outcome, found, err)
	}
}

func TestHideConfirmationAndPipelineWatermarkIndependence(t *testing.T) {
	if err := ValidateRequest(ActionRequest{Active: false, ExpectedJobGeneration: 1, ExpectedRevision: 1, IdempotencyKey: "hide"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("hide without confirmation=%v", err)
	}
	expected := []string{"base_or_worker_scene", "video_cover", "watermark", "video_encode", "tee_live_archive_preview"}
	if !reflect.DeepEqual(PipelineOrder, expected) {
		t.Fatalf("pipeline=%#v", PipelineOrder)
	}
	state := NormalizeState(State{})
	if !state.CoverWatermarkIndependent || !reflect.DeepEqual(state.PipelineOrder, expected) {
		t.Fatalf("normalized=%#v", state)
	}
}

func TestActionRequestRejectsNonCanonicalRawJSONBeforeMutation(t *testing.T) {
	for name, body := range map[string][]byte{
		"missing_active":       []byte(`{"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1"}`),
		"null_active":          []byte(`{"active":null,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1"}`),
		"duplicate_active":     []byte(`{"active":true,"active":false,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1","hide_confirmed":true}`),
		"show_with_hide":       []byte(`{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1","hide_confirmed":true}`),
		"hide_without_confirm": []byte(`{"active":false,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"hide-1"}`),
		"padded_key":           []byte(`{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":" show-1"}`),
		"nel_padded_key":       []byte("{\"active\":true,\"expected_job_generation\":1,\"expected_revision\":1,\"idempotency_key\":\"\\u0085show-1\"}"),
		"bom_padded_key":       []byte("{\"active\":true,\"expected_job_generation\":1,\"expected_revision\":1,\"idempotency_key\":\"\\ufeffshow-1\"}"),
		"invalid_utf8":         append([]byte(`{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"`), 0xff, '"', '}'),
	} {
		t.Run(name, func(t *testing.T) {
			var request ActionRequest
			if err := json.Unmarshal(body, &request); err == nil {
				t.Fatalf("non-canonical request accepted: %q", body)
			}
		})
	}

	for name, body := range map[string][]byte{
		"show": []byte(`{"active":true,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"show-1"}`),
		"hide": []byte(`{"active":false,"expected_job_generation":1,"expected_revision":1,"idempotency_key":"hide-1","hide_confirmed":true}`),
	} {
		t.Run("valid_"+name, func(t *testing.T) {
			var request ActionRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatalf("canonical request rejected: %v", err)
			}
		})
	}
}

func TestVideoCoverPresetUpdateDoesNotMutateStreamSnapshot(t *testing.T) {
	repository := NewMemoryRepository()
	created, err := repository.CreatePreset(context.Background(), Preset{Name: "Release", AssetID: "asset-1", AssetVariantID: "variant-1", Enabled: true, CreatedByUserID: "user-a"})
	if err != nil {
		t.Fatal(err)
	}
	snapshotAsset, snapshotVariant, snapshotRevision := created.AssetID, created.AssetVariantID, created.Revision
	updated, err := repository.UpdatePreset(context.Background(), created.ID, Preset{Name: "Release 2", AssetID: "asset-2", AssetVariantID: "variant-2", Enabled: true, UpdatedByUserID: "user-a"}, created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision == snapshotRevision || snapshotAsset != "asset-1" || snapshotVariant != "variant-1" {
		t.Fatalf("snapshot drifted: updated=%#v snapshot=%s/%s/%d", updated, snapshotAsset, snapshotVariant, snapshotRevision)
	}
	deleted, err := repository.DeletePreset(context.Background(), created.ID, "user-a", updated.Revision)
	if err != nil || deleted.DeletedAt == nil {
		t.Fatalf("delete=%#v err=%v", deleted, err)
	}
	if snapshotAsset != "asset-1" || snapshotVariant != "variant-1" {
		t.Fatal("delete mutated snapshot")
	}
}
