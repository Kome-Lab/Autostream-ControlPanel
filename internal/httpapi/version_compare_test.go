package httpapi

import "testing"

func TestVersionIsNewerUsesSemanticVersionPrereleasePrecedence(t *testing.T) {
	tests := []struct {
		name            string
		latest, current string
		want            bool
	}{
		{"rc to stable", "v1.2.3", "v1.2.3-rc.1", true},
		{"rc increment", "v1.2.3-rc.2", "v1.2.3-rc.1", true},
		{"stable to rc is downgrade", "v1.2.3-rc.2", "v1.2.3", false},
		{"numeric before text", "v1.2.3-beta", "v1.2.3-2", true},
		{"longer prerelease", "v1.2.3-rc.1.1", "v1.2.3-rc.1", true},
		{"build metadata ignored", "v1.2.3+build.2", "v1.2.3+build.1", false},
		{"core increment", "v1.3.0-rc.1", "v1.2.9", true},
		{"invalid leading zero", "v1.2.3-rc.01", "v1.2.3-rc.1", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := versionIsNewer(test.latest, test.current); got != test.want {
				t.Fatalf("versionIsNewer(%q, %q) = %v, want %v", test.latest, test.current, got, test.want)
			}
		})
	}
}
