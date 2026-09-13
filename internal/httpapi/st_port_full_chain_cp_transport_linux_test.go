//go:build linux

package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

func (f *stPortChainCP) transportBoundary() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); authorization != "" {
			f.rememberSecret(authorization)
		}
		for _, cookie := range r.Cookies() {
			f.rememberSecret(cookie.Value)
		}
		isReport := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/services/update-jobs/") && strings.HasSuffix(r.URL.Path, "/report")
		var terminal *contracts.SystemUpdatePortResultV2
		var requestBody []byte
		if r.Method == http.MethodPost && isSystemUpdateExecutionPath(r.URL.Path) {
			var err error
			requestBody, err = io.ReadAll(io.LimitReader(r.Body, stPortChainBound+1))
			if err != nil || len(requestBody) > stPortChainBound {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(requestBody))
			f.rememberWireSecrets(requestBody)
		}
		if isReport {
			var report contracts.UpdaterResultEnvelope
			if json.Unmarshal(requestBody, &report) == nil {
				terminal = report.PortReconfigure
			}
			r = r.WithContext(store.WithSystemUpdatePortCommitObserver(r.Context(), func(observation store.SystemUpdatePortCommitObservation) {
				f.mu.Lock()
				wanted := "c11_" + observation.Phase
				if f.fault != wanted {
					f.mu.Unlock()
					return
				}
				f.fault, f.phase, f.phaseJob = "", observation.Phase, observation.JobID
				f.phaseBodySHA = contracts.ComputeSystemUpdatePortBytesSHA256(requestBody)
				f.gate = make(chan struct{})
				gate := f.gate
				f.mu.Unlock()
				select {
				case <-gate:
				case <-r.Context().Done():
				}
			}))
		}
		buffer := httptest.NewRecorder()
		f.handler.ServeHTTP(buffer, r)
		if buffer.Body.Len() > stPortChainBound {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.rememberWireSecrets(buffer.Body.Bytes())
		for _, cookie := range buffer.Result().Cookies() {
			f.rememberSecret(cookie.Value)
		}
		isConsume := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/consume") && buffer.Code == http.StatusNoContent
		isIssuedGrant := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/services/update-jobs/") && strings.HasSuffix(r.URL.Path, "/mutation-grants") && buffer.Code == http.StatusCreated
		acceptedTerminal := terminal != nil && buffer.Code == http.StatusOK && contracts.IsAcceptedSystemUpdatePortResult(*terminal)
		jobID := ""
		if acceptedTerminal {
			jobID = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/services/update-jobs/"), "/report")
			job, err := store.NewMariaDBSystemUpdateStore(f.reads).GetSystemUpdateJob(r.Context(), jobID)
			if err != nil || job.PortResult == nil || !contracts.EqualSystemUpdatePortResults(*job.PortResult, *terminal) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		f.mu.Lock()
		if terminal != nil && terminal.Result == contracts.SystemUpdatePortReconfigurationRollbackFailed && buffer.Code == http.StatusOK && len(f.lastFailureBody) == 0 {
			f.lastFailurePath, f.lastFailureBody = r.URL.Path, append([]byte(nil), requestBody...)
		}
		if r.URL.Path == "/services/update-jobs/claim" && buffer.Code == http.StatusOK {
			f.claims++
		}
		if isConsume {
			f.consumes++
		}
		if acceptedTerminal {
			f.terminals++
			digest := contracts.ComputeSystemUpdatePortBytesSHA256(requestBody)
			if f.terminalJob != jobID {
				f.terminalJob, f.firstTerminalSHA = jobID, digest
			}
			f.lastTerminalSHA = digest
		}
		drop := false
		if f.fault == "create_after" && r.Method == http.MethodPost && r.URL.Path == "/system-updates" && buffer.Code == http.StatusCreated {
			drop = true
		}
		if f.fault == "consume_after" && isConsume {
			drop = true
			f.consumePhase = "response_dropped"
			f.consumeJob = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/services/update-jobs/"), "/mutation-grants/consume")
			f.gate = make(chan struct{})
		}
		if (f.fault == "terminal_after" || f.fault == "terminal_after_pause") && acceptedTerminal {
			drop = true
			if f.fault == "terminal_after_pause" {
				f.gate = make(chan struct{})
			}
		}
		if drop {
			f.fault = ""
			f.dropped++
		}
		gate := f.gate
		holdIssuedGrant := isIssuedGrant && gate != nil && f.consumeJob == strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/services/update-jobs/"), "/mutation-grants")
		f.mu.Unlock()
		if drop {
			if hijacker, ok := w.(http.Hijacker); ok {
				if connection, _, err := hijacker.Hijack(); err == nil {
					_ = connection.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		if (acceptedTerminal || holdIssuedGrant) && gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		for key, values := range buffer.Header() {
			w.Header()[key] = append([]string(nil), values...)
		}
		w.WriteHeader(buffer.Code)
		_, _ = w.Write(buffer.Body.Bytes())
	})
}
