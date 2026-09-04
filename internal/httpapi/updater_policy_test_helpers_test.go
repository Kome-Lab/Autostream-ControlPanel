package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
	"golang.org/x/crypto/ssh"
)

func registerUpdateAgentForPolicyTest(t *testing.T, auth *store.MemoryAuthStore, serviceID string) store.ServiceToken {
	t.Helper()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", []string{
		"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{
		ServiceID: serviceID, ServiceType: "update_agent", ServiceName: "Updater",
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: "host-01",
		Capabilities: map[string]any{"host_agent": true, "observe_only": true}, Version: "v1.0.0",
	})
	return token
}

func updaterPolicyTargetRequestsForTest(targets []store.UpdaterPolicyTarget) []updaterPolicyTargetRequest {
	requests := make([]updaterPolicyTargetRequest, 0, len(targets))
	for _, target := range targets {
		request := updaterPolicyTargetRequest{
			TargetID: target.TargetID, ServiceID: target.ServiceID, HostID: target.HostID,
			ServiceType: target.ServiceType, DeploymentMode: target.DeploymentMode,
		}
		if target.DatabaseName != "" {
			encoded, _ := json.Marshal(target.DatabaseName)
			request.DatabaseName = encoded
		}
		if target.LocalListenPort != 0 {
			request.LocalListenPort = json.RawMessage(strconv.Itoa(target.LocalListenPort))
		}
		requests = append(requests, request)
	}
	return requests
}

func updaterPolicyForHTTPTest(hostPublicKey string) store.UpdaterPolicy {
	return store.UpdaterPolicy{
		TransportMode:             store.SystemUpdateTransportPullV2,
		ExecutionHostID:           "host-01",
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64),
		PollIntervalSeconds:       15,
		HeartbeatIntervalSeconds:  30,
		Hosts: []store.UpdaterPolicyHost{{
			HostID: "host-01", Name: "Host 01", Address: "10.0.0.10", Port: 55850,
			User: "autostream-update-host", Arch: "amd64", HostPublicKey: hostPublicKey,
		}},
		Targets: []store.UpdaterPolicyTarget{{
			TargetID: "worker-01", ServiceID: "worker-01", HostID: "host-01",
			ServiceType: "worker", DeploymentMode: "systemd", LocalListenPort: 8084,
		}},
	}
}

func savePullUpdaterPolicyForHTTPTest(t *testing.T, policies *store.MemoryUpdaterPolicyStore, serviceID string, policy store.UpdaterPolicy) store.UpdaterPolicy {
	t.Helper()
	updates := store.NewMemorySystemUpdateStore()
	saved, err := policies.SavePullUpdaterPolicy(t.Context(), updates, serviceID, 0, 0, policy)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func ed25519AuthorizedKeyForTest(t *testing.T, comment string) (string, string) {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey)))
	if strings.TrimSpace(comment) != "" {
		authorized += " " + strings.TrimSpace(comment)
	}
	return authorized, ssh.FingerprintSHA256(sshPublicKey)
}
