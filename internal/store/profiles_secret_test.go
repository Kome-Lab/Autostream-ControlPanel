package store

import (
	"errors"
	"testing"
)

func TestProfileConfigAllowsConfiguredStreamKeySelectorBooleanOnly(t *testing.T) {
	profiles := NewMemoryProfileStore()
	if _, err := profiles.CreateProfile(t.Context(), ProfileYouTubeOutput, "managed configured key", map[string]any{
		"mode":                      "live_api",
		"use_configured_stream_key": true,
		"stream_key_secret_name":    "youtube_stream_key_output-id",
	}); err != nil {
		t.Fatalf("boolean configured-key selector was rejected: %v", err)
	}

	for _, value := range []any{"raw-stream-key", 1, map[string]any{"value": "raw-stream-key"}} {
		if _, err := profiles.CreateProfile(t.Context(), ProfileYouTubeOutput, "invalid configured key selector", map[string]any{
			"mode":                      "live_api",
			"use_configured_stream_key": value,
		}); !errors.Is(err, ErrProfileRawSecretConfig) {
			t.Fatalf("configured-key selector value %#v error=%v, want ErrProfileRawSecretConfig", value, err)
		}
	}
}
