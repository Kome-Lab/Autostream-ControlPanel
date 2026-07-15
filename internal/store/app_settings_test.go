package store

import "testing"

func TestNormalizeAppSettingsGoogleAnalytics(t *testing.T) {
	t.Parallel()
	normalized, err := NormalizeAppSettings(AppSettings{
		AppName:                      "AutoStream",
		Timezone:                     "Asia/Tokyo",
		GoogleAnalyticsEnabled:       true,
		GoogleAnalyticsMeasurementID: " g-abcd1234 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !normalized.GoogleAnalyticsEnabled || normalized.GoogleAnalyticsMeasurementID != "G-ABCD1234" {
		t.Fatalf("unexpected analytics settings: %#v", normalized)
	}
}

func TestNormalizeAppSettingsRejectsInvalidGoogleAnalyticsID(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "UA-1234", "G-ABC_123", "G-ABC<script>", "G-ABCDEFGHIJKLMNOPQRSTUVWXYZ"} {
		_, err := NormalizeAppSettings(AppSettings{
			AppName:                      "AutoStream",
			Timezone:                     "Asia/Tokyo",
			GoogleAnalyticsEnabled:       true,
			GoogleAnalyticsMeasurementID: value,
		})
		if err != ErrInvalidSettings {
			t.Fatalf("measurement ID %q error = %v, want ErrInvalidSettings", value, err)
		}
	}
}

func TestNormalizeAppSettingsClearsDisabledGoogleAnalyticsID(t *testing.T) {
	t.Parallel()
	normalized, err := NormalizeAppSettings(AppSettings{
		AppName:                      "AutoStream",
		Timezone:                     "Asia/Tokyo",
		GoogleAnalyticsMeasurementID: "G-ABCD1234",
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.GoogleAnalyticsEnabled || normalized.GoogleAnalyticsMeasurementID != "" {
		t.Fatalf("disabled analytics settings were not cleared: %#v", normalized)
	}
}
