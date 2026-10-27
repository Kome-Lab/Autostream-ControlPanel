package streamvisual

import (
	"errors"
	"fmt"
	"testing"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestCoverUploadLimitTransitions(t *testing.T) {
	uploads := func(count int) Settings {
		s := DefaultSettings("quota-stream")
		for i := 0; i < count; i++ {
			s.CoverCandidates = append(s.CoverCandidates, CoverCandidate{Name: fmt.Sprint(i), Source: "upload", AssetID: fmt.Sprintf("asset-%d", i), VariantID: fmt.Sprintf("variant-%d", i)})
		}
		return s
	}
	tests := []struct {
		name          string
		current, next Settings
		limited       bool
	}{
		{"empty", uploads(0), uploads(0), false},
		{"four new", uploads(0), uploads(4), false},
		{"five new", uploads(0), uploads(5), true},
		{"fifth added", uploads(4), uploads(5), true},
		{"legacy six retained", uploads(6), uploads(6), false},
		{"legacy reduced to five", uploads(6), uploads(5), false},
		{"legacy reduced to four", uploads(6), uploads(4), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCoverUploadLimit(test.current, test.next)
			if errors.Is(err, mediaassets.ErrCoverUploadLimit) != test.limited {
				t.Fatalf("limit error=%v want limited=%t", err, test.limited)
			}
		})
	}

	t.Run("variants and presets do not consume upload slots", func(t *testing.T) {
		next := uploads(4)
		next.CoverCandidates = append(next.CoverCandidates,
			CoverCandidate{Name: "another resolution", Source: "upload", AssetID: "asset-0", VariantID: "other-variant"},
			CoverCandidate{Name: "shared preset", Source: "preset", AssetID: "preset-asset", VariantID: "preset-variant"})
		if err := validateCoverUploadLimit(uploads(0), next); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("legacy replacement must converge", func(t *testing.T) {
		next := uploads(5)
		next.CoverCandidates[4].AssetID = "replacement"
		if err := validateCoverUploadLimit(uploads(6), next); !errors.Is(err, mediaassets.ErrCoverUploadLimit) {
			t.Fatalf("replacement error=%v", err)
		}
	})
	t.Run("omitted candidates cannot hide a fifth selected upload", func(t *testing.T) {
		current := uploads(4)
		next, _, err := applyUpdate(current, Update{
			CoverSource:    OptionalString{Set: true, Valid: true, Value: "upload"},
			CoverAssetID:   OptionalString{Set: true, Valid: true, Value: "fifth"},
			CoverVariantID: OptionalString{Set: true, Valid: true, Value: "fifth-variant"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = validateCoverUploadLimit(current, next); !errors.Is(err, mediaassets.ErrCoverUploadLimit) {
			t.Fatalf("omitted candidate limit error=%v", err)
		}
	})
}

func TestMemoryVisualUpdateCannotRecreateDeletedStreamCandidates(t *testing.T) {
	streams := store.NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "deleted quota stream")
	if err != nil {
		t.Fatal(err)
	}
	visuals := NewMemoryRepository(streams)
	current, err := visuals.Get(t.Context(), stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = streams.DeleteStream(t.Context(), stream.ID); err != nil {
		t.Fatal(err)
	}
	candidates := []CoverCandidate{{Name: "new upload", Source: "upload", AssetID: "new-asset", VariantID: "new-variant"}}
	if _, err = visuals.Update(t.Context(), stream.ID, "quota-user", Update{ExpectedRevision: current.Revision, UploadSessionID: "new-session", CoverCandidates: &candidates}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted stream update error=%v", err)
	}
	if len(visuals.settings[stream.ID].CoverCandidates) != 0 {
		t.Fatal("deleted stream candidates were recreated")
	}
}
