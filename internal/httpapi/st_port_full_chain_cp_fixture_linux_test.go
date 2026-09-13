//go:build linux

package httpapi

import (
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
	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
	"github.com/go-sql-driver/mysql"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

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
	bootstrapTarget := store.UpdaterPolicyTarget{TargetID: stPortChainTarget, ServiceID: stPortChainTarget, ServiceType: "worker", DeploymentMode: f.config.Mode}
	if f.config.Mode == "systemd" {
		bootstrapTarget.LocalListenPort = localPort
	}
	policy, err := f.policies.SavePullUpdaterPolicy(ctx, f.updates, stPortChainAgent, 0, 0, store.UpdaterPolicy{TransportMode: store.SystemUpdateTransportPullV2,
		ExecutionHostID: stPortChainHost, LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("a", 64), PollIntervalSeconds: 15, HeartbeatIntervalSeconds: 30,
		Targets: []store.UpdaterPolicyTarget{bootstrapTarget}})
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
