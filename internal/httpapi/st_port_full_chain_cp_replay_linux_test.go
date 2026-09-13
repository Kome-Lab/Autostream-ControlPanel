//go:build linux

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"net/http"
	"time"
)

func (f *stPortChainCP) replayFailure(ctx context.Context, command stPortChainCPCommand) stPortChainCPResponse {
	f.mu.Lock()
	path, body := f.lastFailurePath, append([]byte(nil), f.lastFailureBody...)
	f.mu.Unlock()
	if path == "" || len(body) == 0 {
		return stPortChainCPResponse{ErrorCode: "saved_failure_unavailable"}
	}
	var packet contracts.UpdaterResultEnvelope
	if json.Unmarshal(body, &packet) != nil || packet.PortReconfigure == nil || packet.PortReconfigure.Result != contracts.SystemUpdatePortReconfigurationRollbackFailed {
		return stPortChainCPResponse{ErrorCode: "saved_failure_invalid"}
	}
	if command.JobID != "" && command.JobID != packet.JobID {
		return stPortChainCPResponse{ErrorCode: "foreign_replay_job"}
	}
	readUpdates := store.NewMariaDBSystemUpdateStore(f.reads)
	job, err := readUpdates.GetSystemUpdateJob(ctx, packet.JobID)
	if err != nil || job.PortResult == nil || job.PortResult.Result != contracts.SystemUpdatePortReconfigurationRolledBack || job.PortReconfigure == nil {
		return stPortChainCPResponse{ErrorCode: "rollback_not_accepted"}
	}
	// Freeze all relevant public state from a separate connection before sending
	// the hostile old envelope through normal HTTPS authentication/validation.
	before, err := f.replayState(ctx, job.ID)
	if err != nil {
		return stPortChainCPResponse{ErrorCode: "replay_prestate_unavailable"}
	}
	if command.Command == "replay_old_applied" {
		ref := job.PortReconfigure.Target
		if ref == nil {
			return stPortChainCPResponse{ErrorCode: "target_intent_unavailable"}
		}
		at := packet.PortReconfigure.Observation.ObservedAt
		// Deliberately invalid old-lease packet, never runtime success evidence.
		packet.Status, packet.Outcome = contracts.SystemUpdateSucceeded, contracts.UpdaterOutcomeSucceeded
		packet.AppliedRevision, packet.SafeError = ref.ConfigRevision, nil
		packet.Evidence = []contracts.UpdaterEvidence{{EvidenceCode: "application_probe_verified", ObservedAt: at, ObservedRevision: ref.ConfigRevision}}
		packet.PortReconfigure = &contracts.SystemUpdatePortResultV2{Result: contracts.SystemUpdatePortReconfigurationApplied,
			ObservedSnapshotID: ref.SnapshotID, ObservedSnapshotSHA256: ref.SnapshotSHA256,
			ObservedConfigRevision: ref.ConfigRevision, ObservedConfigSHA256: ref.ConfigSHA256,
			ObservedExecutorPolicyRevision: ref.ExecutorPolicyRevision, ObservedExecutorPolicySHA256: ref.ExecutorPolicySHA256,
			Observation: contracts.SystemUpdatePortObservation{PolicyDiskVerified: true, PolicyMemoryVerified: true, AgentProjectionVerified: true, ListenerVerified: true, ObservedAt: at}}
		body, err = json.Marshal(packet)
		if err != nil {
			return stPortChainCPResponse{ErrorCode: "encode_negative_packet"}
		}
	}
	agent, err := f.auth.GetService(ctx, stPortChainAgent)
	if err != nil {
		return stPortChainCPResponse{ErrorCode: "replay_identity_unavailable"}
	}
	token, err := security.DecryptSecret(agent.NodeTokenCiphertext, agent.NodeTokenNonce, f.key)
	if err != nil {
		return stPortChainCPResponse{ErrorCode: "replay_identity_unavailable"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.config.PanelURL+path, bytes.NewReader(body))
	if err != nil {
		return stPortChainCPResponse{ErrorCode: "negative_request_invalid"}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(systemUpdateContractMajorHeader, systemUpdateContractMajorV2)
	client := &http.Client{Transport: f.client.Transport, Timeout: 20 * time.Second, CheckRedirect: f.client.CheckRedirect}
	response, err := client.Do(request)
	if err != nil {
		return stPortChainCPResponse{ErrorCode: "negative_response_unavailable"}
	}
	_, drainErr := io.Copy(io.Discard, io.LimitReader(response.Body, stPortChainBound+1))
	closeErr := response.Body.Close()
	if drainErr != nil || closeErr != nil {
		return stPortChainCPResponse{ErrorCode: "negative_response_incomplete"}
	}
	after, err := f.replayState(ctx, job.ID)
	if err != nil || !bytes.Equal(before, after) {
		return stPortChainCPResponse{ErrorCode: "old_packet_changed_accepted_state"}
	}
	return f.observe(ctx, stPortChainCPCommand{Command: "observe", JobID: job.ID})
}

func (f *stPortChainCP) replayState(ctx context.Context, jobID string) ([]byte, error) {
	updates := store.NewMariaDBSystemUpdateStore(f.reads)
	job, err := updates.GetSystemUpdateJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	policy, err := store.NewMariaDBUpdaterPolicyAdminStore(f.reads, "").GetUpdaterPolicy(ctx, stPortChainAgent)
	if err != nil {
		return nil, err
	}
	reservations, err := updates.ListServicePortReservations(ctx, stPortChainHost)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Job          store.SystemUpdateJob
		Policy       store.UpdaterPolicy
		Reservations []store.ServicePortReservation
	}{job, policy, reservations})
}

func (f *stPortChainCP) release() {
	f.mu.Lock()
	if f.gate != nil {
		close(f.gate)
		f.gate = nil
	}
	f.mu.Unlock()
}

func (f *stPortChainCP) rememberSecret(value string) {
	if value == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.secretValues) >= 4096 {
		f.secretOverflow = true
		return
	}
	f.secretValues[value] = struct{}{}
}

func (f *stPortChainCP) rememberWireSecrets(body []byte) {
	var data any
	if json.Unmarshal(body, &data) != nil {
		return
	}
	var visit func(any)
	visit = func(value any) {
		switch object := value.(type) {
		case map[string]any:
			for key, nested := range object {
				switch key {
				case "token", "grant_token", "csrf_token", "runtime_token", "lease_token", "activation_token", "configure_token", "access_token", "refresh_token":
					if secret, ok := nested.(string); ok {
						f.rememberSecret(secret)
					}
				}
				visit(nested)
			}
		case []any:
			for _, nested := range object {
				visit(nested)
			}
		}
	}
	visit(data)
}
