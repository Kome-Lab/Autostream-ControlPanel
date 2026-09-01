package videocover

import (
	"context"
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
