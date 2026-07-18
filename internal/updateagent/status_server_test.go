package updateagent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusServerHealthVersionAndAuthenticatedStatus(t *testing.T) {
	agent := &Agent{Config: Config{NodeID: "updater-01", RuntimeToken: "runtime-secret", Targets: []Target{{TargetID: "worker", DeploymentMode: ModeDocker}}}}
	server := httptest.NewServer(agent.statusHandler())
	defer server.Close()
	for _, path := range []string{"/health", "/version"} {
		resp, err := http.Get(server.URL + path)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s unavailable: %v status=%v", path, err, resp)
		}
		_ = resp.Body.Close()
	}
	resp, _ := http.Get(server.URL + "/status")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/status", nil)
	req.Header.Set("Authorization", "Bearer runtime-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status unavailable: %v", err)
	}
	defer resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response headers: %v", resp.Header)
	}
}
