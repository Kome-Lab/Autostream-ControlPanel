//go:build linux

package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"github.com/go-sql-driver/mysql"
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
	for _, pipe := range []*os.File{input, output} {
		info, err := pipe.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatal("control descriptor is not an inherited pipe")
		}
	}
	configBytes, err := stPortChainReadRootFile(os.Getenv("AUTOSTREAM_ST_PORT_CHAIN_CONFIG"), false)
	if err != nil {
		t.Fatal("read protected CP fixture configuration")
	}
	var config stPortChainCPConfig
	if stPortChainDecode(configBytes, &config) != nil || config.PanelURL != "https://localhost:18443" || config.ListenAddr != "127.0.0.1:18443" ||
		config.AgentUID == 0 || config.AgentGID == 0 || config.validateRuntime() != nil || !filepath.IsAbs(config.FixtureDir) {
		t.Fatal("invalid bounded CP fixture configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 34*time.Minute)
	defer cancel()
	f, err := stPortChainOpenCP(ctx, config)
	if err != nil {
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
	certBytes, certErr := stPortChainReadRootFile(config.TLSCert, false)
	keyBytes, keyErr := stPortChainReadRootFile(config.TLSKey, true)
	certificate, tlsErr := tls.X509KeyPair(certBytes, keyBytes)
	if certErr != nil || keyErr != nil || tlsErr != nil {
		t.Fatal("load fixture TLS identity")
	}
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
	if err := f.login(ctx); err != nil {
		t.Fatal("authenticate fixture operator over verified HTTPS")
	}
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

func stPortChainDecode(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) > stPortChainBound {
		return errors.New("bounded input exceeded")
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func stPortChainReadRootFile(path string, secret bool) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute fixture path required")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("open protected fixture file")
	}
	file := os.NewFile(uintptr(fd), "protected-fixture-file")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, errors.New("stat protected fixture file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || secret && info.Mode().Perm()&0077 != 0 || info.Size() > stPortChainBound {
		return nil, errors.New("unprotected fixture file")
	}
	return io.ReadAll(io.LimitReader(file, stPortChainBound+1))
}

func stPortChainOpenCP(ctx context.Context, config stPortChainCPConfig) (*stPortChainCP, error) {
	input, err := stPortChainReadRootFile(config.DSNFile, true)
	if err != nil {
		return nil, err
	}
	dsn, err := database.NormalizeMySQLDSN(strings.TrimSpace(string(input)))
	if err != nil {
		return nil, errors.New("invalid fixture DSN")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("invalid fixture DSN")
	}
	fixtureTransport := parsed.Net == "unix" && parsed.Addr == "/run/mysqld/mysqld.sock"
	if parsed.Net == "tcp" {
		host, _, splitErr := net.SplitHostPort(parsed.Addr)
		fixtureTransport = splitErr == nil && (host == "127.0.0.1" || host == "localhost" || host == "::1")
	}
	if !fixtureTransport ||
		!strings.HasPrefix(parsed.DBName, "st_port_chain_") || strings.ContainsAny(parsed.DBName, "./\\") {
		return nil, errors.New("database is outside the isolated fixture")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, errors.New("open fixture database")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	f := &stPortChainCP{config: config, db: db, createBodies: map[string][]byte{}, secretValues: map[string]struct{}{}}
	ready := false
	defer func() {
		if !ready {
			_ = db.Close()
			if f.reads != nil {
				_ = f.reads.Close()
			}
		}
	}()
	if db.PingContext(ctx) != nil || database.RunEmbeddedMigrations(ctx, db) != nil {
		return nil, errors.New("prepare fixture database")
	}
	f.reads, err = sql.Open("mysql", dsn)
	if err != nil {
		return nil, errors.New("open independent observer database")
	}
	f.reads.SetMaxOpenConns(2)
	f.reads.SetMaxIdleConns(1)
	var users int
	if db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&users) != nil {
		return nil, errors.New("read fixture initialization state")
	}
	keyPath := filepath.Join(config.FixtureDir, "cp-bootstrap-key")
	keyBytes, keyErr := stPortChainReadRootFile(keyPath, true)
	if keyErr != nil && users == 0 {
		info, statErr := os.Lstat(config.FixtureDir)
		if statErr != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("fixture directory is not private")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return nil, errors.New("fixture directory is not root owned")
		}
		keyBytes = make([]byte, 32)
		if _, err = rand.Read(keyBytes); err != nil {
			return nil, errors.New("create fixture encryption key")
		}
		keyBytes = []byte(hex.EncodeToString(keyBytes))
		file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, errors.New("persist fixture encryption key")
		}
		_, writeErr := file.Write(keyBytes)
		syncErr, closeErr := file.Sync(), file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return nil, errors.New("persist fixture encryption key")
		}
	} else if keyErr != nil {
		return nil, errors.New("existing fixture lost its encryption key")
	}
	f.key = string(keyBytes)
	adminDigest := sha256.Sum256(append([]byte("st-port-fixture-admin\x00"), keyBytes...))
	f.password = hex.EncodeToString(adminDigest[:])
	f.rememberSecret(f.key)
	f.rememberSecret(f.password)
	f.auth = store.NewMariaDBAuthStoreWithSecretKey(db, f.key)
	f.policies = store.NewMariaDBUpdaterPolicyAdminStore(db, "")
	f.updates = store.NewMariaDBSystemUpdateStore(db)
	if users == 0 {
		if err := f.seed(ctx); err != nil {
			return nil, err
		}
	}
	user, err := f.auth.FindUserByUsername(ctx, stPortChainAdmin)
	if err != nil {
		return nil, errors.New("existing fixture has no bound operator")
	}
	f.userID = user.ID
	f.handler = NewServer(store.NewMariaDBStreamStore(db), WithAuthStore(f.auth), WithServiceRegistryStore(f.auth),
		WithSystemUpdateStore(f.updates), WithUpdaterPolicyStore(f.policies), WithSecuritySettingsStore(store.NewMariaDBSecuritySettingsStore(db)),
		WithAppSettingsStore(store.NewMariaDBAppSettingsStore(db)))
	trust, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("load normal client certificate trust")
	}
	jar, _ := cookiejar.New(nil)
	f.client = &http.Client{Timeout: 20 * time.Second, Jar: jar, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trust, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: false},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ready = true
	return f, nil
}

func (f *stPortChainCP) seed(ctx context.Context) error {
	localPort := f.config.initialLocalListenPort()
	if _, err := f.auth.CreateFirstAdmin(ctx, stPortChainAdmin, f.password, []string{"system_updates.read", "system_updates.execute"}); err != nil {
		return errors.New("create fixture operator")
	}
	register := func(registration store.ServiceRegistration, scopes []string) (store.ServiceToken, error) {
		token, err := f.auth.CreateServiceToken(ctx, registration.ServiceType, scopes)
		if err != nil {
			return store.ServiceToken{}, err
		}
		if _, err := f.auth.PrecreateService(ctx, token, registration); err != nil {
			return store.ServiceToken{}, err
		}
		if _, err := f.auth.RegisterService(ctx, token, registration); err != nil {
			return store.ServiceToken{}, err
		}
		ciphertext, nonce, err := security.EncryptSecret(token.RawToken, f.key)
		if err != nil {
			return store.ServiceToken{}, err
		}
		if _, err := f.auth.SetServiceNodeTokenSecret(ctx, registration.ServiceID, ciphertext, nonce); err != nil {
			return store.ServiceToken{}, err
		}
		return token, nil
	}
	if _, err := register(store.ServiceRegistration{ServiceID: stPortChainTarget, ServiceType: "worker", ServiceName: "Worker smoke",
		PublicURL: fmt.Sprintf("https://worker.example.test:%d", localPort), Version: f.config.WorkerVersion, Capabilities: map[string]any{}},
		[]string{"service.register", "service.heartbeat", "service.config.read"}); err != nil {
		return errors.New("register fixture target")
	}
	if err := f.primeDockerBootstrapTarget(ctx); err != nil {
		return err
	}
	agentToken, err := register(store.ServiceRegistration{ServiceID: stPortChainAgent, ServiceType: "update_agent", ServiceName: stPortChainAgent,
		TransportMode: store.SystemUpdateTransportPullV2, ExecutionHostID: stPortChainHost, Version: "v2.0.0", Capabilities: map[string]any{"observe_only": true}},
		[]string{"service.register", "service.heartbeat", "service.config.read", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		return errors.New("register fixture agent")
	}
	policy, err := f.policies.SavePullUpdaterPolicy(ctx, f.updates, stPortChainAgent, 0, 0, store.UpdaterPolicy{TransportMode: store.SystemUpdateTransportPullV2,
		ExecutionHostID: stPortChainHost, LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64), PollIntervalSeconds: 15, HeartbeatIntervalSeconds: 30,
		Targets: []store.UpdaterPolicyTarget{{TargetID: stPortChainTarget, ServiceID: stPortChainTarget, ServiceType: "worker", DeploymentMode: f.config.Mode}}})
	if err != nil {
		return errors.New("save fixture bootstrap policy")
	}
	if err := f.primeDockerBootstrapPolicy(ctx, &policy); err != nil {
		return err
	}
	// Initial activation is fixture setup through the existing store API.
	// ST-PORT v2 readiness/baseline is never synthesized here: the real Agent
	// must observe the installed root and send its own subsequent heartbeat.
	capabilities := map[string]any{"host_agent": true, "observe_only": true, "update_executor": true, "mutation_enabled": false, "recovery_pending": false,
		"transport_mode": "pull_v2", "agent_protocol_version": "2", "execution_host_id": stPortChainHost, "ownership_epoch": int64(0),
		"policy_revision": policy.ProjectionRevision, "policy_status": "applied", "target_availability": map[string]any{stPortChainTarget: "available"},
		"target_availability_codes": map[string]any{stPortChainTarget: "executor_verified"}, "reported_ports": map[string]any{stPortChainTarget: int64(localPort)},
		"port_drift": map[string]any{stPortChainTarget: false}, "reported_service_types": map[string]any{stPortChainTarget: "worker"},
		"reported_deployment_modes": map[string]any{stPortChainTarget: f.config.Mode}, "reported_executor_policy_revisions": map[string]any{stPortChainTarget: policy.LocalExecutorPolicyRevision},
		"reported_executor_policy_sha256": map[string]any{stPortChainTarget: policy.LocalExecutorPolicySHA256}, "reported_config_revisions": map[string]any{stPortChainTarget: int64(1)},
		"reported_config_sha256": map[string]any{stPortChainTarget: "sha256:" + strings.Repeat("c", 64)}}
	if err := f.addDockerBootstrapCapabilities(capabilities); err != nil {
		return err
	}
	if _, err := f.auth.Heartbeat(ctx, agentToken, store.ServiceHeartbeat{ServiceID: stPortChainAgent, Status: "online", Version: "v2.0.0", Capabilities: capabilities}); err != nil {
		return errors.New("initialize bootstrap observation")
	}
	activated, err := f.policies.ActivatePullUpdaterOwnership(ctx, f.auth, f.updates, store.ActivatePullUpdaterOwnershipParams{ServiceID: stPortChainAgent, ExecutionHostID: stPortChainHost,
		ExpectedSourcePolicyRevision: policy.Revision, ExpectedProjectionRevision: policy.ProjectionRevision, ExpectedLocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ExpectedLocalExecutorPolicySHA256: policy.LocalExecutorPolicySHA256})
	if err != nil {
		return errors.New("activate fixture ownership")
	}
	policy = activated.Policy
	policy.Revision, policy.ProjectionRevision, policy.LocalExecutorPolicyRevision = 11, 17, 23
	if f.config.Mode == "systemd" {
		policy.Targets[0].LocalListenPort = localPort
	}
	config, err := f.config.initialPortConfig()
	if err != nil {
		return errors.New("materialize initial listener")
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE services SET endpoint_revision=3,applied_endpoint_revision=3,applied_config_revision=31,applied_config_sha256=?,endpoint_status='applied',host='worker.example.test',port=443,ssl_enabled=1,public_url='https://worker.example.test:443/',desired_host='worker.example.test',desired_port=443,desired_ssl_enabled=1,desired_public_url='https://worker.example.test:443/' WHERE service_id=?`, contracts.ComputeSystemUpdatePortBytesSHA256(config), stPortChainTarget); err != nil {
		return errors.New("seed target revisions")
	}
	if f.config.Mode == "systemd" {
		if _, err := f.db.ExecContext(ctx, `INSERT INTO update_agent_target_local_listeners (updater_service_id,target_id,binding_policy_revision,local_listen_port,updated_at) VALUES (?,?,11,?,?) ON DUPLICATE KEY UPDATE binding_policy_revision=11,local_listen_port=VALUES(local_listen_port)`, stPortChainAgent, stPortChainTarget, localPort, time.Now().UTC()); err != nil {
			return errors.New("seed bound listener")
		}
	}
	worker, err := f.auth.GetService(ctx, stPortChainTarget)
	if err != nil {
		return errors.New("read seeded target")
	}
	projection, err := f.rootProjection(policy, worker)
	if err != nil {
		return errors.New("materialize fixed root authority")
	}
	policy.LocalExecutorPolicySHA256 = projection.SHA256
	encoded, err := json.Marshal(policy)
	if err != nil {
		return errors.New("encode initial policy")
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE update_agent_policies SET revision=11,projection_revision=17,local_executor_policy_revision=23,policy_json=? WHERE service_id=?`, encoded, stPortChainAgent); err != nil {
		return errors.New("seed policy revisions")
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE system_update_execution_hosts SET policy_revision=17 WHERE execution_host_id=?`, stPortChainHost); err != nil {
		return errors.New("seed ownership projection")
	}
	return nil
}

func (f *stPortChainCP) rootProjection(policy store.UpdaterPolicy, worker store.RegisteredService) (updateradapter.ConfigurePolicyProjection, error) {
	if len(policy.Targets) != 1 || policy.Targets[0].ServiceID != stPortChainTarget || worker.AppliedEndpoint == nil {
		return updateradapter.ConfigurePolicyProjection{}, errors.New("unexpected fixture policy")
	}
	source := updateradapter.HostAgentConfigurePolicySource{PanelURL: f.config.PanelURL, ExecutionHostID: stPortChainHost,
		AgentUID: f.config.AgentUID, AgentGID: f.config.AgentGID, SourcePolicyRevision: policy.Revision, ProjectionRevision: policy.ProjectionRevision, LocalExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		Targets: []updateradapter.HostAgentConfigurePolicyTarget{{ServiceID: stPortChainTarget, ServiceType: "worker", DeploymentMode: f.config.Mode, EndpointRevision: worker.AppliedEndpointRevision,
			AppliedConfigRevision: worker.AppliedConfigRevision, AppliedConfigSHA256: worker.AppliedConfigSHA256, AppliedEndpointPort: worker.AppliedEndpoint.Port, LocalListenPort: policy.Targets[0].LocalListenPort}}}
	if f.config.Mode == "docker" {
		source.Targets[0].LocalListenPort = f.config.Docker.PublishedPort
		docker := f.config.Docker.snapshot()
		source.Targets[0].DockerSnapshot = &docker
		source.Targets[0].DockerRoot = &contracts.UpdaterPortDockerRootBaseline{ComposeConfigSHA256: f.config.Docker.ComposeConfigSHA256, CurrentVersion: f.config.Docker.CurrentVersion}
		return updateradapter.BuildSystemUpdatePortPolicy(source)
	}
	return updateradapter.BuildHostAgentConfigurePolicy(source)
}

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

func (f *stPortChainCP) create(ctx context.Context, command stPortChainCPCommand) stPortChainCPResponse {
	key := command.IdempotencyKey
	if key == "" {
		return stPortChainCPResponse{ErrorCode: "idempotency_key_required"}
	}
	body, exists := f.createBodies[key]
	if command.Command == "retry_create" && !exists {
		return stPortChainCPResponse{ErrorCode: "immutable_create_unavailable"}
	}
	if !exists {
		status, response, err := f.request(ctx, http.MethodGet, "/system-updates", nil)
		if err != nil || status != http.StatusOK {
			return stPortChainCPResponse{ErrorCode: "canonical_get_failed"}
		}
		var listing struct {
			Targets []systemUpdateTargetResponse `json:"targets"`
		}
		if json.Unmarshal(response, &listing) != nil {
			return stPortChainCPResponse{ErrorCode: "canonical_get_invalid"}
		}
		var target *systemUpdateTargetResponse
		for index := range listing.Targets {
			if listing.Targets[index].TargetID == stPortChainTarget {
				target = &listing.Targets[index]
				break
			}
		}
		if target == nil || target.PortPolicySnapshotID == "" {
			return stPortChainCPResponse{ErrorCode: "baseline_not_ready"}
		}
		desired := target.AppliedConfigRevision
		request := map[string]any{"operation": "port_reconfigure", "protocol_version": 2, "port_contract_version": 2, "mode": command.Mode,
			"target_id": stPortChainTarget, "expected_snapshot_id": target.PortPolicySnapshotID,
			"expected_endpoint_revision": target.EndpointRevision, "fence": target.OwnershipEpoch, "required_capability": "host.port", "idempotency_key": key}
		if f.config.Mode == "docker" {
			if command.LocalPort != 0 || target.PortMapping == nil || target.PortMapping.Mode != "docker" || target.PortMapping.PublishedPort != target.LocalListenPort {
				return stPortChainCPResponse{ErrorCode: "docker_create_baseline_unavailable"}
			}
			if command.PublishedPort != target.PortMapping.PublishedPort || command.ContainerPort != target.PortMapping.ContainerPort {
				desired++
			}
			request["new_published_port"], request["new_container_port"] = command.PublishedPort, command.ContainerPort
		} else {
			if command.PublishedPort != 0 || command.ContainerPort != 0 {
				return stPortChainCPResponse{ErrorCode: "invalid_systemd_create_fields"}
			}
			if command.LocalPort != target.LocalListenPort {
				desired++
			}
			request["new_local_listen_port"] = command.LocalPort
		}
		request["desired_revision"] = desired
		if command.Mode == contracts.SystemUpdatePortModeLocalAndAdvertised {
			request["new_advertised_port"] = command.AdvertisedPort
		}
		body, _ = json.Marshal(request)
		if contracts.ValidateSystemUpdatePortCreateRequest(body) != nil {
			return stPortChainCPResponse{ErrorCode: "invalid_create_intent"}
		}
		f.createBodies[key] = append([]byte(nil), body...)
	}
	f.mu.Lock()
	before := f.fault == "create_before"
	if before {
		f.fault = ""
		f.dropped++
	}
	f.mu.Unlock()
	if before {
		return stPortChainCPResponse{ErrorCode: "response_lost"}
	}
	status, response, err := f.request(ctx, http.MethodPost, "/system-updates", body)
	if err != nil {
		return stPortChainCPResponse{ErrorCode: "response_lost"}
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return stPortChainCPResponse{ErrorCode: "create_rejected"}
	}
	var result store.SystemUpdateJob
	if json.Unmarshal(response, &result) != nil || result.ID == "" || result.TargetID != stPortChainTarget {
		return stPortChainCPResponse{ErrorCode: "create_response_invalid"}
	}
	return stPortChainCPResponse{OK: true, Job: response}
}

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
