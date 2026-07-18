package updateagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type remoteSSHObservation struct {
	command string
	request RemoteRPCRequest
}

type remoteSSHTestServer struct {
	listener              net.Listener
	hostSigner            ssh.Signer
	observations          chan remoteSSHObservation
	done                  chan error
	allowHandshakeFailure bool
}

func newRemoteSSHTestServer(t *testing.T, authorizedKey ssh.PublicKey, response func(RemoteRPCRequest) RemoteRPCResponse) *remoteSSHTestServer {
	return newRemoteSSHTestServerWithPrefix(t, authorizedKey, nil, response)
}

func newRemoteSSHTestServerWithPrefix(t *testing.T, authorizedKey ssh.PublicKey, prefix []byte, response func(RemoteRPCRequest) RemoteRPCResponse) *remoteSSHTestServer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &remoteSSHTestServer{
		listener:     listener,
		hostSigner:   hostSigner,
		observations: make(chan remoteSSHObservation, 1),
		done:         make(chan error, 1),
	}
	go func() {
		server.done <- server.serveOnce(authorizedKey, prefix, response)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-server.done:
			if err != nil && !errors.Is(err, net.ErrClosed) && !server.allowHandshakeFailure {
				t.Errorf("SSH test server: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("SSH test server did not stop")
		}
	})
	return server
}

func (s *remoteSSHTestServer) serveOnce(authorizedKey ssh.PublicKey, prefix []byte, response func(RemoteRPCRequest) RemoteRPCResponse) error {
	connection, err := s.listener.Accept()
	if err != nil {
		return err
	}
	defer connection.Close()
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), authorizedKey.Marshal()) {
				return nil, errors.New("unauthorized public key")
			}
			return nil, nil
		},
	}
	config.AddHostKey(s.hostSigner)
	serverConnection, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return err
	}
	defer serverConnection.Close()
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, sessionRequests, err := channelRequest.Accept()
		if err != nil {
			return err
		}
		for sessionRequest := range sessionRequests {
			if sessionRequest.Type != "exec" {
				_ = sessionRequest.Reply(false, nil)
				continue
			}
			var execPayload struct{ Command string }
			if err := ssh.Unmarshal(sessionRequest.Payload, &execPayload); err != nil {
				return err
			}
			if err := sessionRequest.Reply(true, nil); err != nil {
				return err
			}
			request, err := DecodeRemoteRPCRequest(channel)
			if err != nil {
				return err
			}
			s.observations <- remoteSSHObservation{command: execPayload.Command, request: request}
			if len(prefix) > 0 {
				if _, err := channel.Write(prefix); err != nil {
					return err
				}
			}
			if err := EncodeRemoteRPCResponse(channel, response(request)); err != nil {
				return err
			}
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			return channel.Close()
		}
		_ = channel.Close()
	}
	return nil
}

func (s *remoteSSHTestServer) address(t *testing.T) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	return host, port
}

func writeRemoteSSHIdentity(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "autostream-test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, signer.PublicKey()
}

func writeRemoteSSHKnownHosts(t *testing.T, address string, key ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(address)}, key) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func remoteSSHHostForServer(t *testing.T, server *remoteSSHTestServer, identityPath string, knownKey ssh.PublicKey) SSHHost {
	t.Helper()
	address, port := server.address(t)
	dialAddress := net.JoinHostPort(address, fmt.Sprint(port))
	return SSHHost{
		HostID: "edge-01", Name: "Edge 01", Address: address, Port: port, User: "autostream_update",
		IdentityFile: identityPath, KnownHostsFile: writeRemoteSSHKnownHosts(t, dialAddress, knownKey), Arch: "amd64",
	}
}

func TestSSHRemoteExecutorUsesPinnedHostKeyFixedCommandAndStdinProtocol(t *testing.T) {
	identityPath, authorizedKey := writeRemoteSSHIdentity(t)
	server := newRemoteSSHTestServer(t, authorizedKey, func(request RemoteRPCRequest) RemoteRPCResponse {
		probe := localProbePlatform("edge-01", "v1.0.0", "sha256:"+strings.Repeat("a", 64), []RemoteProbeTarget{{TargetID: "worker-01", ServiceType: "worker", DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0"}})
		probe.OS = "linux"
		probe.Arch = "amd64"
		return RemoteRPCResponse{Version: RemoteProtocolVersion, Probe: &probe}
	})
	host := remoteSSHHostForServer(t, server, identityPath, server.hostSigner.PublicKey())
	probe, err := (SSHRemoteExecutor{DialTimeout: 3 * time.Second}).Probe(context.Background(), host)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.HostID != host.HostID || probe.Arch != host.Arch {
		t.Fatalf("probe identity = %#v", probe)
	}
	observation := <-server.observations
	if observation.command != RemoteFixedCommand {
		t.Fatalf("remote command = %q", observation.command)
	}
	if observation.request.Operation != "probe" || observation.request.Plan != nil || !observation.request.MutationGrant.Empty() || !observation.request.ReleaseToken.Empty() {
		t.Fatalf("remote probe request = %#v", observation.request)
	}
}

func TestSSHRemoteExecutorTransfersEphemeralMutationCredentialsAndRedactsRemoteFailure(t *testing.T) {
	identityPath, authorizedKey := writeRemoteSSHIdentity(t)
	grant := "mutation_precondition_failed"
	server := newRemoteSSHTestServer(t, authorizedKey, func(request RemoteRPCRequest) RemoteRPCResponse {
		failure := RemoteRPCFailure{Code: "mutation_precondition_failed", Message: "rejected " + request.MutationGrant.Reveal()}
		return RemoteRPCResponse{Version: RemoteProtocolVersion, Error: &failure}
	})
	host := remoteSSHHostForServer(t, server, identityPath, server.hostSigner.PublicKey())
	plan := validRemotePlan()
	_, err := (SSHRemoteExecutor{DialTimeout: 3 * time.Second}).Apply(context.Background(), host, plan, NewRemoteSecret(grant))
	if err == nil {
		t.Fatal("apply unexpectedly succeeded")
	}
	var remoteErr *RemoteExecutionError
	if !errors.As(err, &remoteErr) {
		t.Fatalf("apply error type = %T: %v", err, err)
	}
	if strings.Contains(err.Error(), grant) || strings.Contains(remoteErr.Message, grant) {
		t.Fatalf("remote failure exposed a credential: %v / %q", err, remoteErr.Message)
	}
	if !strings.Contains(remoteErr.Message, "[REDACTED]") {
		t.Fatalf("remote failure was not redacted: %q", remoteErr.Message)
	}
	if remoteErr.Code != "internal_error" {
		t.Fatalf("credential-reflecting error code was not redacted: %q", remoteErr.Code)
	}
	observation := <-server.observations
	if observation.request.Operation != "apply" || observation.request.Plan == nil || observation.request.MutationGrant.Reveal() != grant || !observation.request.ReleaseToken.Empty() {
		t.Fatalf("remote mutation request = %#v", observation.request)
	}
}

func TestSSHRemoteExecutorStagesBeforeGrantUsingOnlyReleaseCredential(t *testing.T) {
	identityPath, authorizedKey := writeRemoteSSHIdentity(t)
	releaseToken := "release-token-secret"
	plan := validRemotePlan()
	server := newRemoteSSHTestServer(t, authorizedKey, func(request RemoteRPCRequest) RemoteRPCResponse {
		stage := RemoteStageResult{Status: "staged", SessionID: request.Plan.SessionID, PlanSHA256: request.Plan.PlanSHA256, ArtifactDigest: request.Plan.ArtifactDigest}
		return RemoteRPCResponse{Version: RemoteProtocolVersion, Stage: &stage}
	})
	host := remoteSSHHostForServer(t, server, identityPath, server.hostSigner.PublicKey())
	stage, err := (SSHRemoteExecutor{DialTimeout: 3 * time.Second}).Stage(context.Background(), host, plan, NewRemoteSecret(releaseToken))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if stage.PlanSHA256 != plan.PlanSHA256 || stage.SessionID != plan.SessionID {
		t.Fatalf("stage result = %#v", stage)
	}
	observation := <-server.observations
	if observation.request.Operation != "stage" || observation.request.ReleaseToken.Reveal() != releaseToken || !observation.request.MutationGrant.Empty() {
		t.Fatalf("remote stage request = %#v", observation.request)
	}
}

func TestSSHRemoteExecutorCorrelatesMutationResultToSubmittedPlan(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RemoteRPCResponse)
		wantErr bool
	}{
		{name: "valid"},
		{name: "session mismatch", wantErr: true, mutate: func(response *RemoteRPCResponse) { response.SessionID = "session-99999999" }},
		{name: "plan mismatch", wantErr: true, mutate: func(response *RemoteRPCResponse) { response.PlanSHA256 = strings.Repeat("f", 64) }},
		{name: "artifact mismatch", wantErr: true, mutate: func(response *RemoteRPCResponse) {
			response.Result.ArtifactDigest = "sha256:" + strings.Repeat("f", 64)
		}},
		{name: "rollback flag mismatch", wantErr: true, mutate: func(response *RemoteRPCResponse) { response.Result.Status = "rolled_back" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			identityPath, authorizedKey := writeRemoteSSHIdentity(t)
			server := newRemoteSSHTestServer(t, authorizedKey, func(request RemoteRPCRequest) RemoteRPCResponse {
				result := ApplyResult{Status: "succeeded", ArtifactDigest: normalizeDigest(request.Plan.ArtifactDigest)}
				response := RemoteRPCResponse{Version: RemoteProtocolVersion, Result: &result, SessionID: request.Plan.SessionID, PlanSHA256: request.Plan.PlanSHA256}
				if tc.mutate != nil {
					tc.mutate(&response)
				}
				return response
			})
			host := remoteSSHHostForServer(t, server, identityPath, server.hostSigner.PublicKey())
			result, err := (SSHRemoteExecutor{DialTimeout: 3 * time.Second}).Apply(context.Background(), host, validRemotePlan(), NewRemoteSecret("mutation-grant"))
			if !tc.wantErr {
				if err != nil || result.Status != "succeeded" {
					t.Fatalf("valid mutation result = %#v, %v", result, err)
				}
				return
			}
			var transportErr *SSHTransportError
			if err == nil || !errors.As(err, &transportErr) || transportErr.Code != SSHErrorRemoteConfigInvalid {
				t.Fatalf("uncorrelated mutation result = %#v, %v", result, err)
			}
		})
	}
}

func TestSSHRemoteExecutorRejectsUnpinnedHostKey(t *testing.T) {
	identityPath, authorizedKey := writeRemoteSSHIdentity(t)
	server := newRemoteSSHTestServer(t, authorizedKey, func(RemoteRPCRequest) RemoteRPCResponse {
		panic("remote command must not run with an unpinned host key")
	})
	server.allowHandshakeFailure = true
	_, wrongPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongSigner, err := ssh.NewSignerFromKey(wrongPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	host := remoteSSHHostForServer(t, server, identityPath, wrongSigner.PublicKey())
	_, err = (SSHRemoteExecutor{DialTimeout: 3 * time.Second}).Probe(context.Background(), host)
	var transportErr *SSHTransportError
	if err == nil || !errors.As(err, &transportErr) || transportErr.Code != SSHErrorHostKeyMismatch {
		t.Fatalf("unpinned host key result = %v", err)
	}
}

func TestSSHRemoteExecutorCancelsWhileSessionOpenIsStalled(t *testing.T) {
	identityPath, authorizedKey := writeRemoteSSHIdentity(t)
	_, hostPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	channelSeen := make(chan struct{}, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), authorizedKey.Marshal()) {
				return nil, errors.New("unauthorized")
			}
			return nil, nil
		}}
		config.AddHostKey(hostSigner)
		serverConnection, channels, requests, handshakeErr := ssh.NewServerConn(connection, config)
		if handshakeErr != nil {
			return
		}
		defer serverConnection.Close()
		go ssh.DiscardRequests(requests)
		if _, ok := <-channels; ok {
			channelSeen <- struct{}{}
			_ = serverConnection.Wait()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-serverDone:
		case <-time.After(3 * time.Second):
			t.Error("stalled SSH server did not stop")
		}
	})
	address, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	host := SSHHost{HostID: "edge-01", Name: "Edge 01", Address: address, Port: port, User: "autostream_update", IdentityFile: identityPath, KnownHostsFile: writeRemoteSSHKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey()), Arch: "amd64"}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, probeErr := (SSHRemoteExecutor{DialTimeout: 3 * time.Second}).Probe(ctx, host)
		result <- probeErr
	}()
	select {
	case <-channelSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not request an SSH session")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled session result = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session cancellation did not unblock NewSession")
	}
}

func TestSSHRemoteExecutorHasSafetyTimeoutWithoutCallerDeadline(t *testing.T) {
	identityPath, _ := writeRemoteSSHIdentity(t)
	_, hostPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		buffer := make([]byte, 1)
		_, _ = connection.Read(buffer)
		for {
			if _, readErr := connection.Read(buffer); readErr != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-connectionDone:
		case <-time.After(3 * time.Second):
			t.Error("silent SSH server did not stop")
		}
	})
	address, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	host := SSHHost{HostID: "edge-01", Name: "Edge 01", Address: address, Port: port, User: "autostream_update", IdentityFile: identityPath, KnownHostsFile: writeRemoteSSHKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey()), Arch: "amd64"}
	started := time.Now()
	_, err = (SSHRemoteExecutor{DialTimeout: 5 * time.Second, OperationTimeout: 100 * time.Millisecond}).Probe(context.Background(), host)
	var transportErr *SSHTransportError
	if err == nil || !errors.As(err, &transportErr) || transportErr.Code != SSHErrorTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("safety timeout result = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("safety timeout took %v", elapsed)
	}
}

type sshDeadlineRaceTimeout struct{}

func (sshDeadlineRaceTimeout) Error() string   { return "socket deadline elapsed" }
func (sshDeadlineRaceTimeout) Timeout() bool   { return true }
func (sshDeadlineRaceTimeout) Temporary() bool { return true }

func TestSSHContextOwnedNetworkDeadlinesCanonicalizeTimeoutRace(t *testing.T) {
	for _, phase := range []string{"DialContext", "NewClientConn"} {
		t.Run(phase, func(t *testing.T) {
			now := time.Now()
			ctx, cancel := context.WithDeadline(context.Background(), now.Add(100*time.Millisecond))
			defer cancel()
			deadline, fromContext := effectiveSSHDeadline(ctx, now.Add(5*time.Second))
			if !fromContext || deadline.After(now.Add(200*time.Millisecond)) {
				t.Fatalf("effective deadline = %v context_owned=%v", deadline, fromContext)
			}
			err := sshNetTimeoutError(sshDeadlineRaceTimeout{}, fromContext)
			var transportErr *SSHTransportError
			if !errors.As(err, &transportErr) || transportErr.Code != SSHErrorTimeout || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("context-owned %s timeout = %v", phase, err)
			}
		})
	}
}

func TestSSHRemoteExecutorClosesConnectionOnOutputOverflow(t *testing.T) {
	identityPath, authorizedKey := writeRemoteSSHIdentity(t)
	server := newRemoteSSHTestServerWithPrefix(t, authorizedKey, bytes.Repeat([]byte("x"), 4096), func(RemoteRPCRequest) RemoteRPCResponse {
		probe := localProbePlatform("edge-01", "v1.0.0", "sha256:"+strings.Repeat("a", 64), []RemoteProbeTarget{{TargetID: "worker-01", ServiceType: "worker", DeploymentMode: ModeSystemd}})
		probe.OS = "linux"
		probe.Arch = "amd64"
		return RemoteRPCResponse{Version: RemoteProtocolVersion, Probe: &probe}
	})
	server.allowHandshakeFailure = true
	host := remoteSSHHostForServer(t, server, identityPath, server.hostSigner.PublicKey())
	_, err := (SSHRemoteExecutor{DialTimeout: 3 * time.Second, OutputLimit: 1024}).Probe(context.Background(), host)
	var transportErr *SSHTransportError
	if err == nil || !errors.As(err, &transportErr) || transportErr.Code != SSHErrorRemoteConfigInvalid || !errors.Is(err, errSSHOutputLimit) {
		t.Fatalf("overflow result = %v (cause=%v)", err, transportErr.cause)
	}
}

func TestSSHHostValidationRejectsUnsafeAccountsFilesAndAddresses(t *testing.T) {
	identityPath, publicKey := writeRemoteSSHIdentity(t)
	knownHostsPath := writeRemoteSSHKnownHosts(t, "edge-01.example.com", publicKey)
	base := SSHHost{HostID: "edge-01", Name: "Edge 01", Address: "edge-01.example.com", Port: 22, User: "autostream_update", IdentityFile: identityPath, KnownHostsFile: knownHostsPath, Arch: "amd64"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid host: %v", err)
	}
	cases := []struct {
		name string
		edit func(*SSHHost)
	}{
		{name: "root", edit: func(host *SSHHost) { host.User = "root" }},
		{name: "command injection address", edit: func(host *SSHHost) { host.Address = "edge;id" }},
		{name: "unsupported architecture", edit: func(host *SSHHost) { host.Arch = "386" }},
		{name: "relative identity", edit: func(host *SSHHost) { host.IdentityFile = "id_ed25519" }},
		{name: "same key and known hosts", edit: func(host *SSHHost) { host.KnownHostsFile = host.IdentityFile }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := base
			tc.edit(&host)
			if err := host.Validate(); err == nil {
				t.Fatalf("unsafe host was accepted: %#v", host)
			}
		})
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(identityPath, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := base.Validate(); err != nil {
			t.Fatalf("root-group-readable identity rejected: %v", err)
		}
		if err := os.Chmod(identityPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "group-readable") {
			t.Fatalf("world-readable identity result = %v", err)
		}
	}
}
