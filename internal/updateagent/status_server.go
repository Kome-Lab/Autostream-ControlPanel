package updateagent

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/version"
)

func (a *Agent) startStatusServer() (*http.Server, <-chan error) {
	server := &http.Server{Addr: a.Config.API.BindAddress(), Handler: a.statusHandler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20}
	errs := make(chan error, 1)
	go func() {
		var err error
		if a.Config.API.SSLEnabled {
			err = server.ListenAndServeTLS(a.Config.API.TLSCertFile, a.Config.API.TLSKeyFile)
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
		close(errs)
	}()
	return server, errs
}

func (a *Agent) statusHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(w, http.StatusOK, map[string]any{"status": "ok", "service_type": ServiceTypeUpdateAgent, "version": version.Current()})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(w, http.StatusOK, map[string]any{"service": "autostream-updater", "version": version.Current(), "commit": version.Commit, "build_date": version.BuildDate})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		if !validBearerToken(r.Header.Get("Authorization"), a.Config.RuntimeToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeStatusJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		modes := map[string]string{}
		targets := make([]string, 0, len(a.Config.Targets))
		for _, target := range a.Config.Targets {
			targets = append(targets, target.TargetID)
			modes[target.TargetID] = target.DeploymentMode
		}
		status := "online"
		if a.updating.Load() {
			status = "updating"
		}
		writeStatusJSON(w, http.StatusOK, map[string]any{"status": status, "service_id": a.Config.NodeID, "service_type": ServiceTypeUpdateAgent, "version": version.Current(), "managed_targets": targets, "deployment_modes": modes})
	})
	return mux
}

func validBearerToken(header, expected string) bool {
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	actualHash := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(actualHash[:], expectedHash[:]) == 1
}

func writeStatusJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
