package store

import (
	"testing"
	"time"
)

func TestMemoryServiceMetricHistoryCapsEachSeriesIndependently(t *testing.T) {
	st := NewMemoryAuthStore()
	now := time.Now().UTC()
	for index := range 10 {
		st.metricHistory = append(st.metricHistory, ServiceMetricSnapshot{
			Name: "worker.busy", ServiceID: "worker-01", ServiceType: "worker",
			Value: float64(index), ObservedAt: now.Add(time.Duration(index) * time.Second),
		})
	}
	for index := range 2 {
		st.metricHistory = append(st.metricHistory, ServiceMetricSnapshot{
			Name: "encoder.alive", ServiceID: "encoder-01", ServiceType: "encoder_recorder",
			Value: float64(index), ObservedAt: now.Add(time.Duration(index) * time.Second),
		})
	}

	rows, err := st.ListServiceMetricSnapshots(t.Context(), now.Add(-time.Minute), 2)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.ServiceID+"/"+row.Name]++
	}
	if counts["worker-01/worker.busy"] != 2 || counts["encoder-01/encoder.alive"] != 2 {
		t.Fatalf("high-frequency series starved a low-frequency series: %#v", counts)
	}
}
