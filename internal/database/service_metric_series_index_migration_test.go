package database

import (
	"strings"
	"testing"
)

func TestServiceMetricSeriesIndexMigrationSupportsRangeQueries(t *testing.T) {
	body, err := embeddedMigrations.ReadFile("migrations/078_service_metric_series_index.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, required := range []string{"service_metric_snapshots", "service_id", "metric_name", "observed_at", "id"} {
		if !strings.Contains(text, required) {
			t.Fatalf("metric series index migration missing %q:\n%s", required, text)
		}
	}
}
