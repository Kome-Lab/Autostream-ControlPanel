//go:build linux

package httpapi

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	stPortChainAgent  = "agent-st-port-chain"
	stPortChainHost   = "host-st-port-chain"
	stPortChainTarget = "worker-smoke"
	stPortChainAdmin  = "st-port-chain-admin"
	stPortChainBound  = 1 << 20
)

type stPortChainCPConfig struct {
	DSNFile       string                     `json:"dsn_file"`
	PanelURL      string                     `json:"panel_url"`
	ListenAddr    string                     `json:"listen_addr"`
	TLSCert       string                     `json:"tls_cert"`
	TLSKey        string                     `json:"tls_key"`
	AgentUID      uint32                     `json:"agent_uid"`
	AgentGID      uint32                     `json:"agent_gid"`
	WorkerVersion string                     `json:"worker_version"`
	Mode          string                     `json:"mode"`
	FixtureDir    string                     `json:"fixture_dir"`
	Docker        *stPortChainDockerCPConfig `json:"docker,omitempty"`
}

type stPortChainCPCommand struct {
	Command        string                         `json:"command"`
	Mode           contracts.SystemUpdatePortMode `json:"mode,omitempty"`
	LocalPort      int                            `json:"local_port,omitempty"`
	PublishedPort  int                            `json:"published_port,omitempty"`
	ContainerPort  int                            `json:"container_port,omitempty"`
	AdvertisedPort int                            `json:"advertised_port,omitempty"`
	IdempotencyKey string                         `json:"idempotency_key,omitempty"`
	JobID          string                         `json:"job_id,omitempty"`
	Fault          string                         `json:"fault,omitempty"`
}

type stPortChainCPResponse struct {
	OK                     bool                                   `json:"ok"`
	ErrorCode              string                                 `json:"error_code,omitempty"`
	FailureStage           string                                 `json:"failure_stage,omitempty"`
	FailureHTTPStatus      int                                    `json:"failure_http_status,omitempty"`
	FailureCode            string                                 `json:"failure_code,omitempty"`
	RootPolicy             json.RawMessage                        `json:"root_policy,omitempty"`
	AgentIdentityYAML      string                                 `json:"agent_identity_yaml,omitempty"`
	WorkerIdentityYAML     string                                 `json:"worker_identity_yaml,omitempty"`
	SystemUpdates          json.RawMessage                        `json:"system_updates,omitempty"`
	Job                    json.RawMessage                        `json:"job,omitempty"`
	Snapshot               *contracts.SystemUpdatePortSnapshotRef `json:"snapshot,omitempty"`
	JobsCount              int                                    `json:"jobs_count"`
	Reservations           int                                    `json:"reservations"`
	ClaimCount             int                                    `json:"claim_count"`
	ConsumeCount           int                                    `json:"consume_count"`
	TerminalCount          int                                    `json:"terminal_count"`
	DroppedCount           int                                    `json:"dropped_count"`
	C11Phase               string                                 `json:"c11_phase,omitempty"`
	C11JobID               string                                 `json:"c11_job_id,omitempty"`
	C11BodySHA256          string                                 `json:"c11_body_sha256,omitempty"`
	ConsumePhase           string                                 `json:"consume_phase,omitempty"`
	ConsumeJobID           string                                 `json:"consume_job_id,omitempty"`
	DBSourcePolicyRevision int64                                  `json:"db_source_policy_revision,omitempty"`
	DBProjectionRevision   int64                                  `json:"db_projection_revision,omitempty"`
	DBExecutorRevision     int64                                  `json:"db_executor_policy_revision,omitempty"`
	TerminalBodySHA256     string                                 `json:"terminal_body_sha256,omitempty"`
	LastTerminalBodySHA256 string                                 `json:"last_terminal_body_sha256,omitempty"`
	TerminalResult         *contracts.SystemUpdatePortResultV2    `json:"terminal_result,omitempty"`
}

type stPortChainCP struct {
	secretValues                                   map[string]struct{}
	secretOverflow                                 bool
	config                                         stPortChainCPConfig
	db, reads                                      *sql.DB
	auth                                           store.MariaDBAuthStore
	policies                                       store.MariaDBUpdaterPolicyStore
	updates                                        *store.MariaDBSystemUpdateStore
	handler                                        *Server
	client                                         *http.Client
	key, password, csrf, userID                    string
	createBodies                                   map[string][]byte
	mu                                             sync.Mutex
	fault                                          string
	gate                                           chan struct{}
	phase, phaseJob                                string
	phaseBodySHA                                   string
	consumePhase, consumeJob                       string
	claims, consumes, terminals, dropped           int
	terminalJob, firstTerminalSHA, lastTerminalSHA string
	lastFailurePath                                string
	lastFailureBody                                []byte
}

// This is the real CP half of the cross-repository process harness. It uses
// real MariaDB stores, migrations, authentication, handlers, and TLS. Only
// fixture bootstrap and transport/commit stop points live in this test binary.
// Production binaries have no command FD, environment failpoint, or route.
func TestSTPortFullChainControlPanelProcess(t *testing.T) {
	if os.Getenv("AUTOSTREAM_ST_PORT_CHAIN_CHILD") != "cp" {
		t.Skip("launched only by the isolated ST-PORT full-chain parent")
	}
	input, output := os.NewFile(3, "st-port-control"), os.NewFile(4, "st-port-results")
	if input == nil || output == nil {
		t.Fatal("protected command pipes unavailable")
	}
	defer input.Close()
	defer output.Close()
	setupPhase, setupReady := "control_pipes", false
	defer func() {
		if !setupReady {
			// Only fixed phase codes cross the private pipe; process logs and
			// database errors may contain credentials and remain private.
			_ = json.NewEncoder(output).Encode(stPortChainCPResponse{ErrorCode: "cp_setup_" + setupPhase})
		}
	}()
	for _, pipe := range []*os.File{input, output} {
		info, err := pipe.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatal("control descriptor is not an inherited pipe")
		}
	}
	setupPhase = "protected_config"
	configBytes, err := stPortChainReadRootFile(os.Getenv("AUTOSTREAM_ST_PORT_CHAIN_CONFIG"), false)
	if err != nil {
		t.Fatal("read protected CP fixture configuration")
	}
	setupPhase = "bounded_config"
	var config stPortChainCPConfig
	if stPortChainDecode(configBytes, &config) != nil || config.PanelURL != "https://localhost:18443" || config.ListenAddr != "127.0.0.1:18443" ||
		config.AgentUID == 0 || config.AgentGID == 0 || config.validateRuntime() != nil || !filepath.IsAbs(config.FixtureDir) {
		t.Fatal("invalid bounded CP fixture configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 34*time.Minute)
	defer cancel()
	// The isolated fixture registers this exact synthetic public identity.
	// Keep the normal allowlist requirement and all transport boundaries.
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "worker.example.test")
	setupPhase = "database_fixture"
	f, err := stPortChainOpenCP(ctx, config)
	if err != nil {
		for message, phase := range map[string]string{
			"prepare fixture database":               "prepare_database",
			"create fixture operator":                "create_operator",
			"register fixture target":                "register_target",
			"register fixture agent":                 "register_agent",
			"save fixture bootstrap policy":          "save_policy",
			"initialize bootstrap observation":       "bootstrap_observation",
			"activate fixture ownership":             "activate_ownership",
			"seed target revisions":                  "seed_target_revisions",
			"seed bound listener":                    "seed_listener",
			"seed policy revisions":                  "seed_policy_revisions",
			"seed ownership projection":              "seed_ownership_projection",
			"materialize initial Docker mapping":     "docker_initial_mapping",
			"align registered Docker fixture config": "docker_target_config",
			"read registered Docker fixture":         "docker_registered_target",
			"materialize Docker activation profile":  "docker_activation_profile",
			"encode Docker activation policy":        "docker_policy_encoding",
			"align Docker activation policy":         "docker_policy_persist",
			"materialize Docker bootstrap mapping":   "docker_bootstrap_mapping",
		} {
			if err.Error() == message {
				setupPhase = phase
				break
			}
		}
		t.Fatalf("initialize isolated CP database fixture: %v", err)
	}
	defer f.db.Close()
	defer f.reads.Close()
	defer f.client.CloseIdleConnections()
	// Established non-secret overrides exclude release-provider traffic. They
	// do not replace policy, job, grant, authentication, or database behavior.
	for _, target := range append(append([]versionUpdateTarget{controlPanelVersionUpdateTarget}, nodeVersionUpdateTargets...), dockerVersionUpdateTarget) {
		t.Setenv(target.latestVersionEnv, config.WorkerVersion)
	}
	setupPhase = "tls_identity"
	certBytes, certErr := stPortChainReadRootFile(config.TLSCert, false)
	keyBytes, keyErr := stPortChainReadRootFile(config.TLSKey, true)
	certificate, tlsErr := tls.X509KeyPair(certBytes, keyBytes)
	if certErr != nil || keyErr != nil || tlsErr != nil {
		t.Fatal("load fixture TLS identity")
	}
	setupPhase = "https_listener"
	listener, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		t.Fatal("bind fixture HTTPS listener")
	}
	server := &http.Server{Handler: f.transportBoundary(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
		ErrorLog:  log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	served := make(chan error, 1)
	go func() { served <- server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	defer func() {
		f.release()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
	}()
	setupPhase = "operator_login"
	if err := f.login(ctx); err != nil {
		t.Fatal("authenticate fixture operator over verified HTTPS")
	}
	setupReady = true
	commands := make(chan []byte)
	go func() {
		defer close(commands)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), stPortChainBound)
		for scanner.Scan() {
			body := append([]byte(nil), scanner.Bytes()...)
			select {
			case commands <- body:
			case <-ctx.Done():
				return
			}
		}
	}()
	encoder := json.NewEncoder(output)
	for {
		select {
		case <-ctx.Done():
			t.Fatal("bounded CP fixture lifetime expired")
		case err := <-served:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Fatal("fixture HTTPS server stopped")
			}
			return
		case body, ok := <-commands:
			if !ok {
				return
			}
			var command stPortChainCPCommand
			response := stPortChainCPResponse{ErrorCode: "invalid_command"}
			if stPortChainDecode(body, &command) == nil {
				response = f.command(ctx, command)
			}
			if err := encoder.Encode(response); err != nil {
				t.Fatal("write protected CP response")
			}
		}
	}
}
