package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// This boundary is needed only before the first use of saved Node selections.
// The normal handlers retain authentication, v2 leases, CSRF, and audit rules.
type nodeSelectionActivation interface {
	Ready() bool
	ProbeReady() bool
	UpdateJobID() string
	UpdateAgentID() string
}

func withNodeSelectionActivation(next http.Handler, gate nodeSelectionActivation) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gate.Ready() || activationRecoveryRequest(r, gate) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "2")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/services/update-jobs/") && r.Header.Get("X-AutoStream-Contract-Major") != "" {
			w.Header().Set("X-AutoStream-Contract-Major", "2")
		}
		http.Error(w, "Control Panel update verification is in progress.", http.StatusServiceUnavailable)
	})
}

func activationRecoveryRequest(r *http.Request, gate nodeSelectionActivation) bool {
	if r.Method == http.MethodGet && (r.URL.Path == "/health" || r.URL.Path == "/updater/version") {
		return gate.ProbeReady()
	}
	if r.Method != http.MethodPost || gate.UpdateJobID() == "" {
		return false
	}
	switch r.URL.Path {
	case "/services/host-agent/policy":
		return true
	case "/services/update-jobs/claim":
		return activationRequestIdentity(r, "active_job_id", gate.UpdateJobID(), false)
	case "/services/heartbeat":
		return activationRequestIdentity(r, "service_id", gate.UpdateAgentID(), false)
	case "/services/register":
		return activationRequestIdentity(r, "service_id", gate.UpdateAgentID(), true)
	}
	prefix := "/services/update-jobs/" + gate.UpdateJobID() + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	switch strings.TrimPrefix(r.URL.Path, prefix) {
	case "report", "mutation-grants", "mutation-grants/consume":
		return true
	}
	return false
}

// Inspect bounded non-secret identity fields without changing the bytes the
// authenticated handler will validate. Requests for new jobs stay closed.
func activationRequestIdentity(r *http.Request, field, expected string, registration bool) bool {
	if r.Body == nil || expected == "" {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) > 1<<20 {
		return false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	var identity string
	if json.Unmarshal(value[field], &identity) != nil || identity != expected {
		return false
	}
	if registration {
		var kind string
		if json.Unmarshal(value["service_type"], &kind) != nil || kind != "update_agent" {
			return false
		}
	}
	return true
}

func runAfterNodeSelectionActivation(ctx context.Context, ready <-chan struct{}, run func(context.Context)) {
	select {
	case <-ctx.Done():
		return
	case <-ready:
		if ctx.Err() == nil {
			run(ctx)
		}
	}
}
