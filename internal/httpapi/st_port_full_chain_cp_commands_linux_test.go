//go:build linux

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"time"
)

func (f *stPortChainCP) request(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, f.config.PanelURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("construct fixture HTTPS request")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", f.config.PanelURL)
		req.Header.Set("X-CSRF-Token", f.csrf)
	}
	response, err := f.client.Do(req)
	if err != nil {
		return 0, nil, errors.New("fixture HTTPS response unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, stPortChainBound+1))
	if err != nil || len(data) > stPortChainBound {
		return 0, nil, errors.New("fixture HTTPS response not bounded")
	}
	return response.StatusCode, data, nil
}

func (f *stPortChainCP) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": stPortChainAdmin, "password": f.password})
	status, response, err := f.request(ctx, http.MethodPost, "/auth/login", body)
	if err != nil || status != http.StatusOK {
		return errors.New("fixture HTTPS login failed")
	}
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	if json.Unmarshal(response, &login) != nil || login.CSRFToken == "" {
		return errors.New("fixture login did not return CSRF")
	}
	f.csrf = login.CSRFToken
	f.rememberSecret(f.csrf)
	return nil
}

func (f *stPortChainCP) command(ctx context.Context, command stPortChainCPCommand) stPortChainCPResponse {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	switch command.Command {
	case "init":
		policy, err := f.policies.GetUpdaterPolicy(ctx, stPortChainAgent)
		worker, workerErr := f.auth.GetService(ctx, stPortChainTarget)
		agent, agentErr := f.auth.GetService(ctx, stPortChainAgent)
		if err != nil || workerErr != nil || agentErr != nil {
			return stPortChainCPResponse{ErrorCode: "fixture_source_unavailable"}
		}
		projection, err := f.rootProjectionForInit(ctx, policy, worker)
		if err != nil || projection.SHA256 != policy.LocalExecutorPolicySHA256 {
			return stPortChainCPResponse{ErrorCode: "root_projection_mismatch"}
		}
		token, err := security.DecryptSecret(agent.NodeTokenCiphertext, agent.NodeTokenNonce, f.key)
		if err != nil || token == "" {
			return stPortChainCPResponse{ErrorCode: "fixture_identity_unavailable"}
		}
		workerToken, err := security.DecryptSecret(worker.NodeTokenCiphertext, worker.NodeTokenNonce, f.key)
		if err != nil || workerToken == "" {
			return stPortChainCPResponse{ErrorCode: "fixture_worker_identity_unavailable"}
		}
		f.rememberSecret(token)
		f.rememberSecret(workerToken)
		// Both installed parsers require block YAML. JSON string quoting keeps
		// scalar credentials safe without changing the accepted document shape.
		quote := func(value string) string { encoded, _ := json.Marshal(value); return string(encoded) }
		identity := fmt.Sprintf("panel_url: %s\nnode_id: %s\nruntime_token: %s\nservice_name: %s\n",
			quote(f.config.PanelURL), quote(stPortChainAgent), quote(token), quote(stPortChainAgent))
		workerIdentity := fmt.Sprintf("panel:\n  url: %s\nnode:\n  id: %s\n  name: %s\n  type: worker\napi:\n  host: %s\n  port: %d\n  ssl_enabled: %t\nlistener:\n  credential: node-listener.json\nauth:\n  token: %s\n",
			quote(f.config.PanelURL), quote(stPortChainTarget), quote("Worker smoke"), quote(worker.AppliedEndpoint.Host),
			worker.AppliedEndpoint.Port, worker.AppliedEndpoint.SSLEnabled, quote(workerToken))
		return stPortChainCPResponse{OK: true, RootPolicy: projection.Policy, AgentIdentityYAML: identity, WorkerIdentityYAML: workerIdentity}
	case "get":
		status, body, err := f.request(ctx, http.MethodGet, "/system-updates", nil)
		if err != nil || status != http.StatusOK {
			return stPortChainCPResponse{ErrorCode: "canonical_get_failed"}
		}
		f.mu.Lock()
		clean := !f.secretOverflow
		for secret := range f.secretValues {
			if bytes.Contains(body, []byte(secret)) {
				clean = false
				break
			}
		}
		f.mu.Unlock()
		if !clean {
			return stPortChainCPResponse{ErrorCode: "canonical_get_contains_secret"}
		}
		return stPortChainCPResponse{OK: true, SystemUpdates: body}
	case "observe", "lookup":
		return f.observe(ctx, command)
	case "create", "retry_create":
		return f.create(ctx, command)
	case "replay_last_failure", "replay_old_applied":
		return f.replayFailure(ctx, command)
	case "arm_fault":
		switch command.Fault {
		case "create_before", "create_after", "consume_after", "terminal_after", "terminal_after_pause", "c11_before_commit", "c11_after_commit":
		default:
			return stPortChainCPResponse{ErrorCode: "unknown_fault"}
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fault != "" || f.gate != nil {
			return stPortChainCPResponse{ErrorCode: "fault_already_armed"}
		}
		f.fault, f.phase, f.phaseJob = command.Fault, "", ""
		f.phaseBodySHA = ""
		f.consumePhase, f.consumeJob = "", ""
		return stPortChainCPResponse{OK: true}
	case "release":
		f.release()
		return stPortChainCPResponse{OK: true}
	default:
		return stPortChainCPResponse{ErrorCode: "unknown_command"}
	}
}

func (f *stPortChainCP) observe(ctx context.Context, command stPortChainCPCommand) stPortChainCPResponse {
	result := stPortChainCPResponse{OK: true}
	f.mu.Lock()
	result.ClaimCount, result.ConsumeCount, result.TerminalCount, result.DroppedCount = f.claims, f.consumes, f.terminals, f.dropped
	result.C11Phase, result.C11JobID = f.phase, f.phaseJob
	result.C11BodySHA256 = f.phaseBodySHA
	result.ConsumePhase, result.ConsumeJobID = f.consumePhase, f.consumeJob
	if command.JobID == "" || command.JobID == f.terminalJob {
		result.TerminalBodySHA256, result.LastTerminalBodySHA256 = f.firstTerminalSHA, f.lastTerminalSHA
	}
	commitPaused := f.gate != nil && f.phase != ""
	f.mu.Unlock()
	if commitPaused {
		// The parent must be able to kill CP at this exact commit boundary.
		// Returning the observer metadata performs no database work while the
		// transaction is deliberately held; it is not a snapshot observation.
		return result
	}
	updates := store.NewMariaDBSystemUpdateStore(f.reads)
	auth := store.NewMariaDBAuthStoreWithSecretKey(f.reads, f.key)
	policies := store.NewMariaDBUpdaterPolicyAdminStore(f.reads, "")
	policy, policyErr := policies.GetUpdaterPolicy(ctx, stPortChainAgent)
	if policyErr != nil {
		return stPortChainCPResponse{ErrorCode: "policy_observer_failed"}
	}
	result.DBSourcePolicyRevision, result.DBProjectionRevision, result.DBExecutorRevision = policy.Revision, policy.ProjectionRevision, policy.LocalExecutorPolicyRevision
	if f.reads.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_update_jobs WHERE target_id=?`, stPortChainTarget).Scan(&result.JobsCount) != nil ||
		f.reads.QueryRowContext(ctx, `SELECT COUNT(*) FROM service_port_reservations WHERE execution_host_id=?`, stPortChainHost).Scan(&result.Reservations) != nil {
		return stPortChainCPResponse{ErrorCode: "observer_read_failed"}
	}
	var job store.SystemUpdateJob
	var err error
	if command.Command == "lookup" {
		job, err = updates.GetSystemUpdateJobByIdempotency(ctx, f.userID, command.IdempotencyKey)
	} else if command.JobID != "" {
		job, err = updates.GetSystemUpdateJob(ctx, command.JobID)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return stPortChainCPResponse{ErrorCode: "job_observer_failed"}
	}
	if job.ID != "" {
		if job.TargetID != stPortChainTarget {
			return stPortChainCPResponse{ErrorCode: "foreign_fixture_job"}
		}
		result.Job, _ = json.Marshal(job)
		result.TerminalResult = job.PortResult
	}
	snapshot, err := updates.GetSystemUpdatePortPolicySnapshot(ctx, auth, policies, store.SystemUpdatePortSnapshotParams{TargetID: stPortChainTarget, BuildPolicySnapshot: f.handler.systemUpdatePortSnapshotBuilder(f.config.PanelURL)})
	if err == nil {
		result.Snapshot = &snapshot.Ref
	} else {
		result.ErrorCode = "snapshot_unavailable"
	}
	return result
}
