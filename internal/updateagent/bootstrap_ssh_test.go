package updateagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type bootstrapSSHObservation struct {
	command string
	stdin   []byte
}

type bootstrapSSHTestServer struct {
	listener              net.Listener
	hostSigner            ssh.Signer
	observations          chan bootstrapSSHObservation
	done                  chan error
	allowHandshakeFailure bool
	response              []byte
}

func newBootstrapSSHTestServer(t *testing.T, authorizedKey ssh.PublicKey, response []byte) *bootstrapSSHTestServer {
	t.Helper()
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
	server := &bootstrapSSHTestServer{
		listener:     listener,
		hostSigner:   hostSigner,
		observations: make(chan bootstrapSSHObservation, 1),
		done:         make(chan error, 1),
		response:     append([]byte(nil), response...),
	}
	go func() {
		server.done <- server.serveOnce(authorizedKey)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-server.done:
			if err != nil && !errors.Is(err, net.ErrClosed) && !server.allowHandshakeFailure {
				t.Errorf("bootstrap SSH test server: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("bootstrap SSH test server did not stop")
		}
	})
	return server
}

func (s *bootstrapSSHTestServer) serveOnce(authorizedKey ssh.PublicKey) error {
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
		for request := range sessionRequests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			var execPayload struct{ Command string }
			if err := ssh.Unmarshal(request.Payload, &execPayload); err != nil {
				return err
			}
			if err := request.Reply(true, nil); err != nil {
				return err
			}
			stdin, err := io.ReadAll(channel)
			if err != nil {
				return err
			}
			s.observations <- bootstrapSSHObservation{command: execPayload.Command, stdin: stdin}
			if _, err := channel.Write(s.response); err != nil {
				return err
			}
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			return channel.Close()
		}
		_ = channel.Close()
	}
	return nil
}

func bootstrapSSHKey(t *testing.T, passphrase []byte) (BootstrapSSHCredential, ssh.PublicKey) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if len(passphrase) == 0 {
		block, err = ssh.MarshalPrivateKey(privateKey, "bootstrap-test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(privateKey, "bootstrap-test", passphrase)
	}
	if err != nil {
		t.Fatal(err)
	}
	return BootstrapSSHCredential{
		PrivateKey: append([]byte(nil), pem.EncodeToMemory(block)...),
		Passphrase: append([]byte(nil), passphrase...),
	}, signer.PublicKey()
}

func bootstrapSSHHostForServer(t *testing.T, server *bootstrapSSHTestServer) BootstrapSSHHost {
	t.Helper()
	address, portText, err := net.SplitHostPort(server.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	return BootstrapSSHHost{
		HostID:        "edge-01",
		Address:       address,
		Port:          port,
		AdminUser:     "deployer",
		Arch:          "amd64",
		HostPublicKey: bootstrapAuthorizedKeyLine(server.hostSigner.PublicKey()),
	}
}

func bootstrapPayloadFixture(t *testing.T, managedPublicKey ssh.PublicKey) (BootstrapPayload, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"bin/autostream-update-host":             []byte("verified helper binary"),
		"sudoers/autostream-update-host":         []byte("verified sudoers policy\n"),
		"install/install-autostream-update-host": []byte("#!/bin/bash\nexit 0\n"),
		"config/update-host.json":                []byte(`{"host_id":"edge-01","targets":[]}`),
		"keys/autostream-updater.pub":            ssh.MarshalAuthorizedKey(managedPublicKey),
	}
	for name, value := range files {
		if strings.HasPrefix(name, "config/") || strings.HasPrefix(name, "keys/") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "untrusted-extra"), []byte("must not be uploaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	return BootstrapPayload{
		ArtifactRootDir:  root,
		ConfigJSON:       append([]byte(nil), files["config/update-host.json"]...),
		ManagedPublicKey: append([]byte(nil), files["keys/autostream-updater.pub"]...),
	}, files
}

func readBootstrapArchive(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	files := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("archive entry %q type = %d, want regular file", header.Name, header.Typeflag)
		}
		if _, exists := files[header.Name]; exists {
			t.Fatalf("duplicate archive entry %q", header.Name)
		}
		value, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = value
	}
	return files
}

func TestBootstrapSSHInstallUsesPinnedKeyFixedCommandAndAllowlistedArchive(t *testing.T) {
	credential, authorizedKey := bootstrapSSHKey(t, []byte("one-time-passphrase"))
	server := newBootstrapSSHTestServer(t, authorizedKey, []byte(bootstrapInstallSourceCIDRPrefix+"127.0.0.1/32\n"))
	host := bootstrapSSHHostForServer(t, server)
	_, managedPublicKey := bootstrapSSHKey(t, nil)
	payload, expected := bootstrapPayloadFixture(t, managedPublicKey)

	sourceCIDR, err := (BootstrapSSHExecutor{DialTimeout: 3 * time.Second}).Install(context.Background(), host, credential, payload)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if sourceCIDR != "127.0.0.1/32" {
		t.Fatalf("source CIDR = %q", sourceCIDR)
	}
	observation := <-server.observations
	if observation.command != bootstrapInstallRemoteCommand {
		t.Fatalf("remote command = %q, want fixed bootstrap command", observation.command)
	}
	archive := readBootstrapArchive(t, observation.stdin)
	checksums := archive["checksums.sha256"]
	delete(archive, "checksums.sha256")
	if !reflect.DeepEqual(archive, expected) {
		t.Fatalf("archive files = %#v, want exact allowlist %#v", archive, expected)
	}
	if bytes.Contains(checksums, []byte("untrusted-extra")) {
		t.Fatal("checksums included a non-allowlisted artifact")
	}
	for name, value := range expected {
		sum := sha256Bytes(value)
		line := hex.EncodeToString(sum) + "  " + name + "\n"
		if !bytes.Contains(checksums, []byte(line)) {
			t.Fatalf("checksums are missing %q", line)
		}
	}
}

func TestBootstrapSSHInstallRejectsOversizedPayloadBeforeDial(t *testing.T) {
	credential, _ := bootstrapSSHKey(t, nil)
	_, pinnedHostKey := bootstrapSSHKey(t, nil)
	_, managedPublicKey := bootstrapSSHKey(t, nil)
	payload, _ := bootstrapPayloadFixture(t, managedPublicKey)
	payload.ConfigJSON = append([]byte(`{"marker":"DO_NOT_LEAK","padding":"`), bytes.Repeat([]byte("x"), bootstrapConfigMaxBytes)...)
	host := BootstrapSSHHost{
		HostID:        "edge-01",
		Address:       "127.0.0.1",
		Port:          1,
		AdminUser:     "deployer",
		Arch:          "amd64",
		HostPublicKey: bootstrapAuthorizedKeyLine(pinnedHostKey),
	}

	_, err := (BootstrapSSHExecutor{}).Install(context.Background(), host, credential, payload)
	if err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("oversized payload error = %v", err)
	}
	if strings.Contains(err.Error(), "DO_NOT_LEAK") {
		t.Fatalf("payload leaked into error: %v", err)
	}
}

func TestBootstrapSSHInstallRejectsHostKeyMismatch(t *testing.T) {
	credential, authorizedKey := bootstrapSSHKey(t, nil)
	server := newBootstrapSSHTestServer(t, authorizedKey, nil)
	server.allowHandshakeFailure = true
	host := bootstrapSSHHostForServer(t, server)
	_, wrongHostKey := bootstrapSSHKey(t, nil)
	host.HostPublicKey = bootstrapAuthorizedKeyLine(wrongHostKey)
	_, managedPublicKey := bootstrapSSHKey(t, nil)
	payload, _ := bootstrapPayloadFixture(t, managedPublicKey)

	_, err := (BootstrapSSHExecutor{DialTimeout: 3 * time.Second}).Install(context.Background(), host, credential, payload)
	var transportErr *SSHTransportError
	if err == nil || !errors.As(err, &transportErr) || transportErr.Code != SSHErrorHostKeyMismatch {
		t.Fatalf("host key mismatch error = %v", err)
	}
}

func TestBootstrapSSHInstallRejectsAuthenticationWithoutLeakingCredential(t *testing.T) {
	_, authorizedKey := bootstrapSSHKey(t, nil)
	server := newBootstrapSSHTestServer(t, authorizedKey, nil)
	server.allowHandshakeFailure = true
	host := bootstrapSSHHostForServer(t, server)
	wrongCredential, _ := bootstrapSSHKey(t, []byte("credential-secret-passphrase"))
	_, managedPublicKey := bootstrapSSHKey(t, nil)
	payload, _ := bootstrapPayloadFixture(t, managedPublicKey)

	_, err := (BootstrapSSHExecutor{DialTimeout: 3 * time.Second}).Install(context.Background(), host, wrongCredential, payload)
	var transportErr *SSHTransportError
	if err == nil || !errors.As(err, &transportErr) || transportErr.Code != SSHErrorAuthFailed {
		t.Fatalf("authentication error = %v", err)
	}
	for _, secret := range []string{"credential-secret-passphrase", "PRIVATE KEY"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("credential leaked into error: %v", err)
		}
	}
}

func TestBootstrapSSHInspectStandardSystemdDatabasesUsesTargetSpecificFixedScript(t *testing.T) {
	tests := []struct {
		name          string
		targets       []Target
		response      string
		want          map[string]string
		wantCommand   string
		forbiddenPath string
	}{
		{
			name: "control panel only", targets: []Target{{ServiceType: "control_panel"}},
			response:      "control_panel=autostream_control\n",
			want:          map[string]string{"control_panel": "autostream_control"},
			wantCommand:   bootstrapInspectControlPanelDatabaseRemoteCommand,
			forbiddenPath: "/etc/autostream/observability.env",
		},
		{
			name: "observability only", targets: []Target{{ServiceType: "observability"}},
			response:      "observability=autostream_observability\n",
			want:          map[string]string{"observability": "autostream_observability"},
			wantCommand:   bootstrapInspectObservabilityDatabaseRemoteCommand,
			forbiddenPath: "/etc/autostream/control-panel.env",
		},
		{
			name: "both", targets: []Target{{ServiceType: "control_panel"}, {ServiceType: "observability"}},
			response:    "control_panel=autostream_control\nobservability=autostream_observability\n",
			want:        map[string]string{"control_panel": "autostream_control", "observability": "autostream_observability"},
			wantCommand: bootstrapInspectBothDatabasesRemoteCommand,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credential, authorizedKey := bootstrapSSHKey(t, nil)
			server := newBootstrapSSHTestServer(t, authorizedKey, []byte(test.response))
			host := bootstrapSSHHostForServer(t, server)

			databases, err := (BootstrapSSHExecutor{DialTimeout: 3 * time.Second}).InspectStandardSystemdDatabases(context.Background(), host, credential, test.targets)
			if err != nil {
				t.Fatalf("inspect databases: %v", err)
			}
			if !reflect.DeepEqual(databases, test.want) {
				t.Fatalf("databases = %#v, want %#v", databases, test.want)
			}
			observation := <-server.observations
			if observation.command != test.wantCommand {
				t.Fatalf("remote command = %q, want fixed database inspection command", observation.command)
			}
			if test.forbiddenPath != "" && strings.Contains(observation.command, test.forbiddenPath) {
				t.Fatalf("target-specific command inspected unrelated env file %q", test.forbiddenPath)
			}
			if len(observation.stdin) != 0 {
				t.Fatalf("database inspection sent %d stdin bytes", len(observation.stdin))
			}
		})
	}
}

func TestBootstrapSSHWorkerOnlySkipsDatabaseInspection(t *testing.T) {
	databases, err := (BootstrapSSHExecutor{}).InspectStandardSystemdDatabases(
		context.Background(),
		BootstrapSSHHost{},
		BootstrapSSHCredential{},
		[]Target{{ServiceType: "worker"}},
	)
	if err != nil || len(databases) != 0 {
		t.Fatalf("worker-only database inspection = %#v, %v", databases, err)
	}
}

func TestBootstrapSSHInspectRejectsUnsafeDatabaseOutput(t *testing.T) {
	credential, authorizedKey := bootstrapSSHKey(t, nil)
	server := newBootstrapSSHTestServer(t, authorizedKey, []byte("control_panel=../../password\n"))
	host := bootstrapSSHHostForServer(t, server)

	_, err := (BootstrapSSHExecutor{DialTimeout: 3 * time.Second}).InspectStandardSystemdDatabases(
		context.Background(),
		host,
		credential,
		[]Target{{ServiceType: "control_panel"}},
	)
	if err == nil {
		t.Fatal("unsafe database output was accepted")
	}
	if strings.Contains(err.Error(), "../../password") {
		t.Fatalf("remote output leaked into error: %v", err)
	}
}

func TestBootstrapSSHSourceCIDRRequiresOneNumericHost(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "IPv4", value: "192.0.2.10/32", want: "192.0.2.10/32"},
		{name: "IPv6", value: "2001:db8::10/128", want: "2001:db8::10/128"},
		{name: "IPv4 network", value: "192.0.2.0/24", wantErr: true},
		{name: "IPv6 network", value: "2001:db8::/64", wantErr: true},
		{name: "hostname", value: "updater.example.com/32", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseBootstrapSourceCIDR([]byte(bootstrapInstallSourceCIDRPrefix + test.value + "\n"))
			if test.wantErr {
				if err == nil {
					t.Fatalf("source CIDR %q was accepted", test.value)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("source CIDR = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func bootstrapAuthorizedKeyLine(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func sha256Bytes(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}
