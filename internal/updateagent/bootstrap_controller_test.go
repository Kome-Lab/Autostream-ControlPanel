package updateagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

type bootstrapControllerTestPanel struct {
	claim             BootstrapJobClaim
	claimOK           bool
	claimErr          error
	accepted          []string
	acceptLeases      []string
	reports           []BootstrapJobReport
	reportJobs        []string
	acceptError       error
	reportError       error
	acceptErrors      []error
	reportErrors      []error
	claimFingerprints []string
}

func (p *bootstrapControllerTestPanel) ClaimBootstrap(_ context.Context, _ string, _ int64, fingerprint string) (BootstrapJobClaim, bool, error) {
	p.claimFingerprints = append(p.claimFingerprints, fingerprint)
	claim := p.claim
	claim.HostIDs = append([]string(nil), p.claim.HostIDs...)
	return claim, p.claimOK, p.claimErr
}

func (p *bootstrapControllerTestPanel) AcceptBootstrap(_ context.Context, jobID, _, lease string) error {
	p.accepted = append(p.accepted, jobID)
	p.acceptLeases = append(p.acceptLeases, lease)
	if len(p.acceptErrors) > 0 {
		err := p.acceptErrors[0]
		p.acceptErrors = append([]error(nil), p.acceptErrors[1:]...)
		return err
	}
	return p.acceptError
}

func (p *bootstrapControllerTestPanel) ReportBootstrap(_ context.Context, jobID string, report BootstrapJobReport) error {
	p.reportJobs = append(p.reportJobs, jobID)
	p.reports = append(p.reports, report)
	if len(p.reportErrors) > 0 {
		err := p.reportErrors[0]
		p.reportErrors = append([]error(nil), p.reportErrors[1:]...)
		return err
	}
	return p.reportError
}

type bootstrapControllerTestMaintenance struct {
	hosts   map[string]SSHHost
	targets map[string][]Target
	errs    map[string]error
	calls   []string
}

func (m *bootstrapControllerTestMaintenance) RunHostMaintenance(
	ctx context.Context,
	hostID string,
	run func(SSHHost, []Target) error,
) error {
	m.calls = append(m.calls, hostID)
	if err := m.errs[hostID]; err != nil {
		return err
	}
	host, ok := m.hosts[hostID]
	if !ok {
		return ErrHostMaintenanceUnknownHost
	}
	return run(host, append([]Target(nil), m.targets[hostID]...))
}

type bootstrapControllerTestDownloader struct {
	mu        sync.Mutex
	downloads []string
	destDirs  []string
}

func (d *bootstrapControllerTestDownloader) DownloadUpdateHostBootstrap(
	_ context.Context,
	version string,
	arch string,
	destDir string,
) (DownloadedArtifact, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.downloads = append(d.downloads, version+"/"+arch)
	d.destDirs = append(d.destDirs, destDir)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return DownloadedArtifact{}, err
	}
	return DownloadedArtifact{RootDir: filepath.Join(destDir, "root")}, nil
}

type bootstrapControllerTestRemote struct {
	mu       sync.Mutex
	probes   map[string]RemoteProbeResult
	probeErr map[string]error
}

func (r *bootstrapControllerTestRemote) Probe(_ context.Context, host SSHHost) (RemoteProbeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.probeErr[host.HostID]; err != nil {
		return RemoteProbeResult{}, err
	}
	return r.probes[host.HostID], nil
}

func (*bootstrapControllerTestRemote) Stage(context.Context, SSHHost, RemotePlan, RemoteSecret) (RemoteStageResult, error) {
	panic("unexpected Stage call")
}

func (*bootstrapControllerTestRemote) Apply(context.Context, SSHHost, RemotePlan, RemoteSecret) (ApplyResult, error) {
	panic("unexpected Apply call")
}

func (*bootstrapControllerTestRemote) Reconcile(context.Context, SSHHost, RemotePlan, RemoteSecret) (ApplyResult, error) {
	panic("unexpected Reconcile call")
}

type bootstrapControllerTestInstaller struct {
	mu               sync.Mutex
	remote           *bootstrapControllerTestRemote
	installErr       map[string]error
	installResultErr map[string]error
	inspectErr       map[string]error
	installHosts     []string
	credentialRefs   [][]byte
	passphraseRefs   [][]byte
	inspectHosts     []string
	managedKeys      map[string]string
	configByHost     map[string]HelperConfig
	databaseByHost   map[string]map[string]string
}

func (i *bootstrapControllerTestInstaller) InspectStandardSystemdDatabases(
	_ context.Context,
	host BootstrapSSHHost,
	credential BootstrapSSHCredential,
	targets []Target,
) (map[string]string, error) {
	i.mu.Lock()
	i.inspectHosts = append(i.inspectHosts, host.HostID)
	i.mu.Unlock()
	inspection := bootstrapDatabaseInspectionForTargets(targets)
	if inspection == bootstrapDatabaseInspectionNone {
		return map[string]string{}, nil
	}
	i.captureCredential(credential)
	if err := i.inspectErr[host.HostID]; err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for serviceType, database := range i.databaseByHost[host.HostID] {
		if inspection.includes(serviceType) {
			result[serviceType] = database
		}
	}
	return result, nil
}

func (i *bootstrapControllerTestInstaller) Install(
	_ context.Context,
	host BootstrapSSHHost,
	credential BootstrapSSHCredential,
	payload BootstrapPayload,
) (string, error) {
	i.captureCredential(credential)
	i.mu.Lock()
	defer i.mu.Unlock()
	i.installHosts = append(i.installHosts, host.HostID)
	i.managedKeys[host.HostID] = string(payload.ManagedPublicKey)
	var cfg HelperConfig
	if err := json.Unmarshal(payload.ConfigJSON, &cfg); err != nil {
		return "", err
	}
	i.configByHost[host.HostID] = cfg
	if err := i.installErr[host.HostID]; err != nil {
		return "", err
	}
	digest, err := cfg.SHA256()
	if err != nil {
		return "", err
	}
	targets := make([]RemoteProbeTarget, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets = append(targets, RemoteProbeTarget{
			TargetID: target.TargetID, ServiceType: target.ServiceType,
			DeploymentMode: target.DeploymentMode, CurrentVersion: "v1.2.2",
			EndpointVerified: true,
		})
	}
	i.remote.mu.Lock()
	i.remote.probes[host.HostID] = RemoteProbeResult{
		ProtocolVersion: RemoteProtocolVersion,
		HelperVersion:   "v1.2.3",
		HostID:          host.HostID,
		OS:              "linux",
		Arch:            host.Arch,
		ConfigSHA256:    digest,
		Targets:         targets,
	}
	i.remote.mu.Unlock()
	if err := i.installResultErr[host.HostID]; err != nil {
		return "", err
	}
	return "192.0.2.10/32", nil
}

func (i *bootstrapControllerTestInstaller) captureCredential(credential BootstrapSSHCredential) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.credentialRefs = append(i.credentialRefs, credential.PrivateKey)
	i.passphraseRefs = append(i.passphraseRefs, credential.Passphrase)
}

func TestBootstrapControllerPollOnceInstallsHappyBatchAndWipesCredentials(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a", "host-b")
	panel := bootstrapControllerPanelFixture("host-b", "host-a")
	remote := &bootstrapControllerTestRemote{probes: make(map[string]RemoteProbeResult), probeErr: make(map[string]error)}
	installer := &bootstrapControllerTestInstaller{
		remote: remote, installErr: make(map[string]error), inspectErr: make(map[string]error),
		managedKeys: make(map[string]string), configByHost: make(map[string]HelperConfig),
		databaseByHost: make(map[string]map[string]string),
	}
	downloader := &bootstrapControllerTestDownloader{}
	privateKey := []byte("PRIVATE-KEY-MATERIAL")
	passphrase := []byte("PASSPHRASE-MATERIAL")
	var binding BootstrapEnvelopeBinding
	controller := BootstrapController{
		Panel: panel,
		Decrypt: func(_ string, got BootstrapEnvelopeBinding, envelope []byte) (BootstrapAdministratorCredential, error) {
			binding = got
			if strings.Contains(string(envelope), "lease-secret") {
				t.Fatal("credential envelope was mixed with its lease")
			}
			return BootstrapAdministratorCredential{
				AdministratorUser: "deployer", PrivateKey: privateKey, Passphrase: passphrase,
			}, nil
		},
		NewDownloader:     func(RemoteSecret) BootstrapArtifactDownloader { return downloader },
		Installer:         installer,
		Remote:            remote,
		BuildHelperConfig: bootstrapControllerTestHelperConfig,
		CurrentVersion: func() string {
			return "v1.2.3"
		},
	}

	if err := controller.PollOnce(t.Context(), cfg, maintenance); err != nil {
		t.Fatal(err)
	}
	if len(panel.accepted) != 1 || panel.accepted[0] != panel.claim.ID {
		t.Fatalf("accepted jobs = %v", panel.accepted)
	}
	if got := append([]string(nil), binding.HostIDs...); !sort.StringsAreSorted(got) ||
		strings.Join(got, ",") != "host-a,host-b" {
		t.Fatalf("binding host IDs = %v", got)
	}
	if binding.UpdaterID != cfg.NodeID || binding.PolicyRevision != cfg.PolicyRevision || binding.JobID != panel.claim.ID {
		t.Fatalf("binding = %+v", binding)
	}
	if len(downloader.downloads) != 1 || downloader.downloads[0] != "v1.2.3/amd64" {
		t.Fatalf("artifact downloads = %v reports=%+v", downloader.downloads, panel.reports)
	}
	if len(installer.inspectHosts) != 0 {
		t.Fatalf("worker-only bootstrap performed database inspection on %v", installer.inspectHosts)
	}
	for _, hostID := range []string{"host-a", "host-b"} {
		if installer.managedKeys[hostID] != cfg.SSHClientPublicKeys[hostID] {
			t.Fatalf("%s managed key was not installed", hostID)
		}
		if status := lastBootstrapControllerStatus(panel.reports, hostID); status != BootstrapHostStatusSucceeded {
			t.Fatalf("%s final status = %q", hostID, status)
		}
	}
	for _, secretRef := range append(installer.credentialRefs, installer.passphraseRefs...) {
		for _, value := range secretRef {
			if value != 0 {
				t.Fatal("credential byte slice was not cleared after the job")
			}
		}
	}
	if len(downloader.destDirs) != 1 {
		t.Fatalf("artifact destinations = %v", downloader.destDirs)
	}
	if _, err := os.Stat(filepath.Dir(downloader.destDirs[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job-scoped artifact directory survived cleanup: %v", err)
	}
}

func TestBootstrapControllerDecryptFailureDoesNotAccept(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a")
	panel := bootstrapControllerPanelFixture("host-a")
	controller := BootstrapController{
		Panel: panel,
		Decrypt: func(string, BootstrapEnvelopeBinding, []byte) (BootstrapAdministratorCredential, error) {
			return BootstrapAdministratorCredential{}, errors.New("PRIVATE-KEY-MATERIAL")
		},
	}

	err := controller.PollOnce(t.Context(), cfg, maintenance)
	if !errors.Is(err, ErrBootstrapCredentialRejected) {
		t.Fatalf("decrypt error = %v", err)
	}
	if len(panel.accepted) != 0 {
		t.Fatalf("decrypt failure accepted job: %v", panel.accepted)
	}
	if strings.Contains(err.Error(), "PRIVATE-KEY-MATERIAL") {
		t.Fatalf("decrypt error leaked secret: %v", err)
	}
}

func TestBootstrapControllerHostFailureIsRedactedAndDoesNotStopBatch(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a", "host-b")
	panel := bootstrapControllerPanelFixture("host-a", "host-b")
	remote := &bootstrapControllerTestRemote{
		probes:   make(map[string]RemoteProbeResult),
		probeErr: map[string]error{"host-a": errors.New("PRIVATE-KEY-MATERIAL")},
	}
	installer := &bootstrapControllerTestInstaller{
		remote:     remote,
		installErr: map[string]error{"host-a": errors.New("PASSPHRASE-MATERIAL")},
		inspectErr: make(map[string]error), managedKeys: make(map[string]string),
		configByHost: make(map[string]HelperConfig), databaseByHost: make(map[string]map[string]string),
	}
	downloader := &bootstrapControllerTestDownloader{}
	controller := BootstrapController{
		Panel: panel,
		Decrypt: func(string, BootstrapEnvelopeBinding, []byte) (BootstrapAdministratorCredential, error) {
			return BootstrapAdministratorCredential{
				AdministratorUser: "deployer",
				PrivateKey:        []byte("PRIVATE-KEY-MATERIAL"),
				Passphrase:        []byte("PASSPHRASE-MATERIAL"),
			}, nil
		},
		NewDownloader:     func(RemoteSecret) BootstrapArtifactDownloader { return downloader },
		Installer:         installer,
		Remote:            remote,
		BuildHelperConfig: bootstrapControllerTestHelperConfig,
		CurrentVersion:    func() string { return "v1.2.3" },
	}

	if err := controller.PollOnce(t.Context(), cfg, maintenance); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(installer.installHosts, ","); got != "host-a,host-b" {
		t.Fatalf("install hosts = %s reports=%+v", got, panel.reports)
	}
	if status := lastBootstrapControllerStatus(panel.reports, "host-a"); status != BootstrapHostStatusFailed {
		t.Fatalf("host-a final status = %q", status)
	}
	if status := lastBootstrapControllerStatus(panel.reports, "host-b"); status != BootstrapHostStatusSucceeded {
		t.Fatalf("host-b final status = %q", status)
	}
	encoded, err := json.Marshal(panel.reports)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE-KEY-MATERIAL") || strings.Contains(string(encoded), "PASSPHRASE-MATERIAL") {
		t.Fatalf("host reports leaked a credential: %s", encoded)
	}
}

func TestBootstrapControllerReconcilesUncertainInstallWithProbe(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a")
	panel := bootstrapControllerPanelFixture("host-a")
	remote := &bootstrapControllerTestRemote{probes: make(map[string]RemoteProbeResult), probeErr: make(map[string]error)}
	installer := &bootstrapControllerTestInstaller{
		remote: remote, installErr: make(map[string]error),
		installResultErr: map[string]error{"host-a": errors.New("SSH result unknown")},
		inspectErr:       make(map[string]error), managedKeys: make(map[string]string),
		configByHost: make(map[string]HelperConfig), databaseByHost: make(map[string]map[string]string),
	}
	downloader := &bootstrapControllerTestDownloader{}
	controller := BootstrapController{
		Panel: panel,
		Decrypt: func(string, BootstrapEnvelopeBinding, []byte) (BootstrapAdministratorCredential, error) {
			return BootstrapAdministratorCredential{AdministratorUser: "deployer", PrivateKey: []byte("key")}, nil
		},
		NewDownloader:     func(RemoteSecret) BootstrapArtifactDownloader { return downloader },
		Installer:         installer,
		Remote:            remote,
		BuildHelperConfig: bootstrapControllerTestHelperConfig,
		CurrentVersion:    func() string { return "v1.2.3" },
	}
	// Simulate an SSH response that was lost after the remote transaction made
	// the exact helper configuration reachable.
	if err := controller.PollOnce(t.Context(), cfg, maintenance); err != nil {
		t.Fatal(err)
	}
	if status := lastBootstrapControllerStatus(panel.reports, "host-a"); status != BootstrapHostStatusSucceeded {
		t.Fatalf("uncertain install final status = %q", status)
	}
}

func TestBootstrapControllerRejectsCustomHostUserBeforeSSH(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a")
	host := maintenance.hosts["host-a"]
	host.User = "custom-updater"
	maintenance.hosts["host-a"] = host
	panel := bootstrapControllerPanelFixture("host-a")
	remote := &bootstrapControllerTestRemote{probes: make(map[string]RemoteProbeResult), probeErr: make(map[string]error)}
	installer := &bootstrapControllerTestInstaller{
		remote: remote, installErr: make(map[string]error), inspectErr: make(map[string]error),
		managedKeys: make(map[string]string), configByHost: make(map[string]HelperConfig),
		databaseByHost: make(map[string]map[string]string),
	}
	downloader := &bootstrapControllerTestDownloader{}
	controller := BootstrapController{
		Panel: panel,
		Decrypt: func(string, BootstrapEnvelopeBinding, []byte) (BootstrapAdministratorCredential, error) {
			return BootstrapAdministratorCredential{AdministratorUser: "deployer", PrivateKey: []byte("key")}, nil
		},
		NewDownloader:     func(RemoteSecret) BootstrapArtifactDownloader { return downloader },
		Installer:         installer,
		Remote:            remote,
		BuildHelperConfig: bootstrapControllerTestHelperConfig,
		CurrentVersion:    func() string { return "v1.2.3" },
	}

	if err := controller.PollOnce(t.Context(), cfg, maintenance); err != nil {
		t.Fatal(err)
	}
	if len(installer.credentialRefs) != 0 || len(installer.installHosts) != 0 {
		t.Fatalf("custom host user reached SSH installer: credentials=%d installs=%v", len(installer.credentialRefs), installer.installHosts)
	}
	if len(downloader.downloads) != 0 {
		t.Fatalf("custom host user downloaded artifacts: %v", downloader.downloads)
	}
	if status := lastBootstrapControllerStatus(panel.reports, "host-a"); status != BootstrapHostStatusFailed {
		t.Fatalf("custom host user final status = %q reports=%+v", status, panel.reports)
	}
	if failure := panel.reports[len(panel.reports)-1]; failure.Code != "bootstrap_profile_unsupported" {
		t.Fatalf("custom host user failure = %+v", failure)
	}
}

func TestValidateBootstrapInstalledProbeRequiresCurrentVersionBaseline(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a")
	host := maintenance.hosts["host-a"]
	targets := maintenance.targets["host-a"]
	helperConfig, err := bootstrapControllerTestHelperConfig(
		cfg.PanelURL,
		host.HostID,
		host.Arch,
		targets,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := helperConfig.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	probe := RemoteProbeResult{
		ProtocolVersion: RemoteProtocolVersion,
		HelperVersion:   "v1.2.3",
		HostID:          host.HostID,
		OS:              "linux",
		Arch:            host.Arch,
		ConfigSHA256:    digest,
		Targets: []RemoteProbeTarget{{
			TargetID:       targets[0].TargetID,
			ServiceType:    targets[0].ServiceType,
			DeploymentMode: targets[0].DeploymentMode,
		}},
	}
	err = validateBootstrapInstalledProbe(host, targets, helperConfig, probe)
	if err == nil || !strings.Contains(err.Error(), "current version baseline") {
		t.Fatalf("missing current version baseline error = %v", err)
	}
	probe.Targets[0].CurrentVersion = "v1.2.2"
	if err := validateBootstrapInstalledProbe(host, targets, helperConfig, probe); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("unverified target endpoint result = %v", err)
	}
	probe.Targets[0].EndpointVerified = true
	if err := validateBootstrapInstalledProbe(host, targets, helperConfig, probe); err != nil {
		t.Fatalf("valid current version and endpoint verification rejected: %v", err)
	}
}

func TestBootstrapDatabaseTargetsSelectsOnlyDatabaseOwners(t *testing.T) {
	targets := []Target{
		{TargetID: "worker", ServiceType: "worker"},
		{TargetID: "control", ServiceType: "control_panel"},
		{TargetID: "discord", ServiceType: "discord_bot"},
		{TargetID: "observability", ServiceType: "observability"},
	}
	selected := bootstrapDatabaseTargets(targets)
	if len(selected) != 2 || selected[0].ServiceType != "control_panel" || selected[1].ServiceType != "observability" {
		t.Fatalf("database inspection targets = %#v", selected)
	}
}

func TestBootstrapControllerRetriesAmbiguousAcceptWithSameLease(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a")
	panel := bootstrapControllerPanelFixture("host-a")
	panel.acceptErrors = []error{errors.New("response lost after commit"), nil}
	remote := &bootstrapControllerTestRemote{probes: make(map[string]RemoteProbeResult), probeErr: make(map[string]error)}
	installer := &bootstrapControllerTestInstaller{
		remote: remote, installErr: make(map[string]error), inspectErr: make(map[string]error),
		managedKeys: make(map[string]string), configByHost: make(map[string]HelperConfig),
		databaseByHost: make(map[string]map[string]string),
	}
	controller := BootstrapController{
		Panel: panel,
		Decrypt: func(string, BootstrapEnvelopeBinding, []byte) (BootstrapAdministratorCredential, error) {
			return BootstrapAdministratorCredential{AdministratorUser: "deployer", PrivateKey: []byte("key")}, nil
		},
		NewDownloader: func(RemoteSecret) BootstrapArtifactDownloader {
			return &bootstrapControllerTestDownloader{}
		},
		Installer: installer, Remote: remote,
		BuildHelperConfig: bootstrapControllerTestHelperConfig,
		CurrentVersion:    func() string { return "v1.2.3" },
	}

	if err := controller.PollOnce(t.Context(), cfg, maintenance); err != nil {
		t.Fatal(err)
	}
	if len(panel.accepted) != 2 || panel.accepted[0] != panel.accepted[1] ||
		panel.acceptLeases[0] != "lease-secret" || panel.acceptLeases[0] != panel.acceptLeases[1] {
		t.Fatalf("accept retries changed binding: jobs=%v leases=%v", panel.accepted, panel.acceptLeases)
	}
}

func TestBootstrapControllerRetriesSameProgressReportAfterTransientFailure(t *testing.T) {
	cfg, maintenance := bootstrapControllerFixture(t, "host-a")
	panel := bootstrapControllerPanelFixture("host-a")
	panel.reportErrors = []error{errors.New("temporary network failure"), nil}
	remote := &bootstrapControllerTestRemote{probes: make(map[string]RemoteProbeResult), probeErr: make(map[string]error)}
	installer := &bootstrapControllerTestInstaller{
		remote: remote, installErr: make(map[string]error), inspectErr: make(map[string]error),
		managedKeys: make(map[string]string), configByHost: make(map[string]HelperConfig),
		databaseByHost: make(map[string]map[string]string),
	}
	controller := BootstrapController{
		Panel: panel,
		Decrypt: func(string, BootstrapEnvelopeBinding, []byte) (BootstrapAdministratorCredential, error) {
			return BootstrapAdministratorCredential{AdministratorUser: "deployer", PrivateKey: []byte("key")}, nil
		},
		NewDownloader: func(RemoteSecret) BootstrapArtifactDownloader {
			return &bootstrapControllerTestDownloader{}
		},
		Installer: installer, Remote: remote,
		BuildHelperConfig: bootstrapControllerTestHelperConfig,
		CurrentVersion:    func() string { return "v1.2.3" },
	}

	if err := controller.PollOnce(t.Context(), cfg, maintenance); err != nil {
		t.Fatal(err)
	}
	if len(panel.reports) < 2 {
		t.Fatalf("reports = %+v", panel.reports)
	}
	first, retry := panel.reports[0], panel.reports[1]
	if first != retry {
		t.Fatalf("report retry payload changed: first=%+v retry=%+v", first, retry)
	}
}

func bootstrapControllerFixture(t *testing.T, hostIDs ...string) (Config, *bootstrapControllerTestMaintenance) {
	t.Helper()
	cfg := Config{
		PanelURL:                          "https://panel.example.test",
		NodeID:                            "updater-01",
		RuntimeToken:                      "runtime-token",
		StateDir:                          t.TempDir(),
		PolicyRevision:                    7,
		BootstrapEncryptionKeyFingerprint: base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		SSHClientPublicKeys:               make(map[string]string),
	}
	cfg.BootstrapEncryptionKeyFingerprint = "SHA256:" + cfg.BootstrapEncryptionKeyFingerprint
	maintenance := &bootstrapControllerTestMaintenance{
		hosts: make(map[string]SSHHost), targets: make(map[string][]Target), errs: make(map[string]error),
	}
	for index, hostID := range hostIDs {
		host := SSHHost{
			HostID: hostID, Name: hostID, Address: "192.0.2.10", Port: 22,
			User: bootstrapManagedHostUser, Arch: "amd64", HostPublicKey: "ssh-ed25519 AAAATEST",
		}
		target := Target{
			TargetID: "worker-" + hostID, HostID: hostID,
			ServiceType: "worker", DeploymentMode: ModeSystemd,
		}
		host.Address = "192.0.2." + string(rune('1'+index))
		cfg.Hosts = append(cfg.Hosts, host)
		cfg.Targets = append(cfg.Targets, target)
		cfg.SSHClientPublicKeys[hostID] = "ssh-ed25519 AAAAMANAGED" + hostID
		maintenance.hosts[hostID] = host
		maintenance.targets[hostID] = []Target{target}
	}
	return cfg, maintenance
}

func bootstrapControllerPanelFixture(hostIDs ...string) *bootstrapControllerTestPanel {
	return &bootstrapControllerTestPanel{
		claimOK: true,
		claim: BootstrapJobClaim{
			ID:               "11111111-1111-1111-1111-111111111111",
			UpdaterID:        "updater-01",
			ExpectedRevision: 7,
			HostIDs:          append([]string(nil), hostIDs...),
			Envelope: BootstrapCredentialEnvelope{
				Version: 1, EphemeralPublicKey: "public", Nonce: "nonce", Ciphertext: "ciphertext",
			},
			LeaseToken:   "lease-secret",
			ReleaseToken: NewRemoteSecret("release-secret"),
		},
	}
}

func lastBootstrapControllerStatus(reports []BootstrapJobReport, hostID string) string {
	status := ""
	for _, report := range reports {
		if report.HostID == hostID {
			status = report.Status
		}
	}
	return status
}

func bootstrapControllerTestHelperConfig(
	panelURL string,
	hostID string,
	arch string,
	targets []Target,
	_ map[string]string,
) (HelperConfig, error) {
	return HelperConfig{
		SchemaVersion: HelperConfigSchemaVersion,
		HostID:        hostID,
		PanelURL:      panelURL,
		Arch:          arch,
		StateDir:      "/var/lib/autostream-update-host",
		Targets:       append([]Target(nil), targets...),
	}, nil
}
