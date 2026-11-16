package httpapi

import (
	"fmt"
	"testing"
)

// These expected routes are independently specified by the IU-WS2-10 API
// contract. Every accepted baseline statement and its relative order remains.
func addManualHoldRouteCharacterization(t *testing.T, fixture *routeCharacterizationFixture) {
	t.Helper()
	index := -1
	for i, row := range fixture.Registrations {
		if row.Pattern == "GET /streams/{id}/youtube-live-notification" {
			if index != -1 {
				t.Fatal("ambiguous manual Hold route anchor")
			}
			index = i
		}
	}
	if index < 0 {
		t.Fatal("accepted route anchor missing")
	}
	additions := []struct{ pattern, permission, handler string }{
		{"GET /streams/{id}/auto-start-hold", "streams.read", "getManualAutoStartHold"},
		{"POST /streams/{id}/auto-start/resume", "streams.start", "resumeManualAutoStart"},
		{"GET /streams/{id}/auto-start/resume-attempts/{request_id}", "streams.start", "inspectManualAutoStart"},
		{"GET /streams/{id}/auto-start/hold-attempts/{request_id}", "streams.read", "inspectManualAutoStart"},
		{"POST /services/streams/{id}/start-admission/inspect", "", "inspectServiceStartAdmission"},
		{"GET /services/streams/{id}/auto-start/attempts/{request_id}", "", "inspectServiceAutomaticStart"},
	}
	for offset, addition := range additions {
		row := fixture.Registrations[0]
		row.Pattern = addition.pattern
		if addition.permission == "" {
			row.Statement = fmt.Sprintf("s.mux.HandleFunc(%q, s.%s)", addition.pattern, addition.handler)
		} else {
			row.Statement = fmt.Sprintf("s.mux.HandleFunc(%q, s.requirePermission(%q, s.%s))", addition.pattern, addition.permission, addition.handler)
		}
		position := index + offset
		fixture.Registrations = append(fixture.Registrations, row)
		copy(fixture.Registrations[position+1:], fixture.Registrations[position:len(fixture.Registrations)-1])
		fixture.Registrations[position] = row
	}
}
