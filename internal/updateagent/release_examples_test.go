package updateagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlPanelInstallGuidePreparesUpdaterBackup(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "release", "README.install.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(body)
	for _, want := range []string{
		"sudo ./install-autostream-control-panel",
		"## Prepare the updater backup command",
		"The installer creates the private backup directory",
		"`/usr/local/sbin/autostream-backup-control-panel`",
		"`/etc/autostream-local-executor/mariadb-backup.cnf`",
		"GRANT SELECT, SHOW VIEW, TRIGGER ON \\`${DATABASE_NAME}\\`.*",
		"exact `DATABASE_NAME` must be used for the MariaDB grant, the real dump, and the",
		"sudo /usr/local/sbin/autostream-backup-control-panel",
		"Save this exact database name in **Application Info > System Updates**",
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("Control Panel install guide is missing %q", want)
		}
	}

	installerBody, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"release",
		"install-autostream-control-panel",
	))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerBody)
	backupInstall := strings.Index(
		installer,
		`mv -Tf -- "${backup_exec_stage}" "${BACKUP_EXECUTABLE}"`,
	)
	managedSwitch := strings.Index(installer, `mv -Tf -- "${current_next}" "${CURRENT_LINK}"`)
	if backupInstall < 0 || managedSwitch < 0 || backupInstall > managedSwitch {
		t.Fatal("Control Panel installer must prepare the fixed backup executable before switching current")
	}

	backupSectionStart := strings.Index(guide, "## Prepare the updater backup command")
	activationStart := strings.Index(guide, "## Review settings and start the service")
	if backupSectionStart < 0 || activationStart < 0 || backupSectionStart >= activationStart {
		t.Fatal("Control Panel install guide has invalid backup and activation sections")
	}
	if strings.Contains(guide, `sudo ln -s "$RELEASE_DIR"`) {
		t.Fatal("Control Panel install guide must not expose installer-owned current-link switching")
	}
}

func TestControlPanelSystemdUnitLoadsConfigurePortSidecarAfterBaseEnvironment(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"systemd",
		"autostream-control-panel.service.example",
	))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(body)
	base := strings.Index(
		unit,
		"EnvironmentFile=/etc/autostream/control-panel.env",
	)
	sidecar := strings.Index(
		unit,
		"EnvironmentFile=-/opt/autostream/local-executor/ports/control-panel.env",
	)
	if base < 0 || sidecar < 0 || sidecar <= base {
		t.Fatal("Control Panel systemd unit must load the optional configure sidecar after its base environment")
	}
}

func TestReleaseDoesNotShipLegacyUpdaterPolicySample(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", "release", "autostream-updater.json.example")); !os.IsNotExist(err) {
		t.Fatalf("obsolete updater policy sample must not be shipped; stat error = %v", err)
	}
}

func TestControlPanelInstallGuideUsesManagedUpdaterSettings(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "release", "README.install.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := strings.Join(strings.Fields(string(body)), " ")
	for _, marker := range []string{
		"## Install the pull_v2 Host Agent and Local Executor",
		"one Host Agent for the physical host",
		"assign both the synthetic `control-panel` target and the registered Observability service",
		"sudo ./install/install-autostream-host-agent --prepare",
		"sudo /usr/local/bin/autostream-host-agent configure",
		"reads it from the TTY or bounded standard input with echo disabled",
		"`/etc/autostream-host-agent/identity.json`",
		"`/etc/autostream-local-executor/policy.json`",
		"sudo ./install/install-autostream-local-executor",
		"sudo systemctl enable --now autostream-host-agent.service",
		"**Application Info > System Updates**",
		"`autostream_control_panel`",
		"`autostream_observability`",
		"MariaDBデータベース名",
		"exact final component of each service's real `DATABASE_URL`",
		"does not place either database name in the configure command",
		"outbound-only `pull_v2`",
		"No SSH key, `known_hosts`, `/etc/autostream/updater.json`, or central `autostream-updater` daemon",
		"activate ownership",
	} {
		if !strings.Contains(guide, marker) {
			t.Fatalf("control panel install guide is missing managed updater marker %q", marker)
		}
	}
	for _, obsolete := range []string{
		"## Install the central updater once",
		"## Update the central updater binary",
		"/usr/local/bin/autostream-updater configure",
		"systemctl enable --now autostream-updater",
		"helper automatic setup",
		"ssh-keyscan",
		`"backup_argv":`,
		"if ! sudo test -e /etc/autostream/updater.json; then",
		`"$RELEASE_DIR/autostream-updater.json.example" /etc/autostream/updater.json`,
		"`/opt/autostream/control-panel/current/autostream-updater.json.example` as",
		"`--init-from",
		"Rerun the exact same token-free Auto Configure command",
		"sudo ssh-keygen",
		`--config "/etc/autostream/updater.json"`,
		"sudo install -d -o root -g root -m 0750 /etc/autostream",
		"private-release GitHub Token",
		"The GitHub Release Token is required for every managed update, whether the repository is public or private.",
		"Copy that reported public key into a file for that host's bootstrap administrator",
	} {
		if strings.Contains(guide, obsolete) {
			t.Fatalf("control panel install guide contains obsolete updater setup %q", obsolete)
		}
	}
}

func TestPullV2ReleaseGuidesDocumentDatabaseBackupBoundary(t *testing.T) {
	hostAgentBody, err := os.ReadFile(filepath.Join("..", "..", "release", "README.host-agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	localExecutorBody, err := os.ReadFile(filepath.Join("..", "..", "release", "README.local-executor.md"))
	if err != nil {
		t.Fatal(err)
	}
	hostAgent := strings.Join(strings.Fields(string(hostAgentBody)), " ")
	localExecutor := strings.Join(strings.Fields(string(localExecutorBody)), " ")
	for _, marker := range []string{
		"one Host Agent per physical host",
		"server-owned `database_name`",
		"`/etc/autostream-local-executor/mariadb-backup.cnf`",
		"configure does not create or transmit the credential",
		"never accepts the database name in argv",
	} {
		if !strings.Contains(hostAgent, marker) {
			t.Fatalf("Host Agent guide is missing database boundary marker %q", marker)
		}
	}
	for _, marker := range []string{
		"server-owned `database_name`",
		"`/etc/autostream-local-executor/mariadb-backup.cnf`",
		"`/var/backups/autostream/control-panel`",
		"`/var/backups/autostream/observability`",
		"Control Panel participates in initial configure",
	} {
		if !strings.Contains(localExecutor, marker) {
			t.Fatalf("Local Executor guide is missing database boundary marker %q", marker)
		}
	}
	for _, guide := range []string{hostAgent, localExecutor} {
		if strings.Contains(guide, `"backup_argv":`) {
			t.Fatal("pull_v2 release guides must not require hand-editing backup_argv")
		}
	}
}

func TestPullV2ReleaseGuidesDocumentManagedHostUpgrade(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "release", "README.install.md"),
		filepath.Join("..", "..", "release", "README.host-agent.md"),
		filepath.Join("..", "..", "release", "README.local-executor.md"),
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		guide := strings.Join(strings.Fields(string(body)), " ")
		for _, marker := range []string{
			"install-autostream-host-agent --upgrade",
			"Host Agent and Local Executor",
		} {
			if !strings.Contains(guide, marker) {
				t.Fatalf("%s is missing managed Host upgrade marker %q", path, marker)
			}
		}
	}
}

func TestPullV2ReleaseGuidesDocumentLegacyWriterRollbackDrain(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "release", "README.install.md"),
		filepath.Join("..", "..", "release", "README.host-agent.md"),
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		guide := strings.Join(strings.Fields(string(body)), " ")
		for _, marker := range []string{
			"deactivate `pull_v2` ownership",
			"`single-writer` drain",
			"Do not save System Updates settings with the old binary",
			"roll forward to the current Control Panel as the sole writer",
			"Re-save the exact MariaDB database names",
			"rerun the generated Host Agent configure command",
			"Reactivate ownership only after",
		} {
			if !strings.Contains(strings.ToLower(guide), strings.ToLower(marker)) {
				t.Fatalf("%s is missing rollback marker %q", path, marker)
			}
		}
	}
}

func TestDockerDraftWorkerUsesCanonicalLoopbackPort(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "release", "autostream-update-host.docker-draft.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Targets []struct {
			ServiceType string `json:"service_type"`
			HealthURL   string `json:"health_url"`
			VersionURL  string `json:"version_url"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatalf("decode Docker draft: %v", err)
	}
	for _, target := range config.Targets {
		if target.ServiceType != "worker" {
			continue
		}
		if target.HealthURL != "http://127.0.0.1:8084/health" || target.VersionURL != "http://127.0.0.1:8084/updater/version" {
			t.Fatalf("Worker Docker draft uses health_url=%q version_url=%q", target.HealthURL, target.VersionURL)
		}
		return
	}
	t.Fatal("Docker draft has no Worker target")
}

func TestBootstrapGuideRequiresEndpointCapableBaseline(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "release", "README.bootstrap.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(body)
	for _, want := range []string{
		"Every `version_url` must use the common",
		"`/updater/version` path.",
		"A pre-endpoint release must",
		"not be used as the first managed release or rollback baseline.",
		"The pinned `Kome-Lab/Autostream-ControlPanel` release repository must remain",
		"public.",
		"private Control Panel release",
		"repositories are not supported.",
		"Every managed update still requires a",
		"job-scoped GitHub Release Token.",
		"The helper receives it only over bounded",
		"standard input during stage and never persists it.",
		"This release does",
		"not expose a supported client-key rotation action in System Updates.",
		"stop the central updater and remove that key's",
		"authorization from the managed host.",
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("bootstrap guide is missing %q", want)
		}
	}
	for _, obsolete := range []string{
		"GitHub token that can read the private Docker release metadata",
		"optional GitHub Release Token",
	} {
		if strings.Contains(guide, obsolete) {
			t.Fatalf("bootstrap guide contains obsolete token guidance %q", obsolete)
		}
	}
}

func TestBootstrapGuideDocumentsReceiptGatedReconfiguration(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "release", "README.bootstrap.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"`/etc/autostream/update-host.install-state` as `root:root 0600`",
		"`schema_version=1` receipt records the SHA-256 digests",
		"only when the current config and `authorized_keys` bytes still match the recorded digests",
		"malformed receipt, unknown field, digest drift, or partial managed state fails closed",
		"A receipt-less legacy install is adopted only when both the config and `authorized_keys` exist",
		"root-only internal `installer-standard-systemd-config` gate",
		"`BuildStandardSystemdHelperConfig` and requires a full exact round-trip match",
		"including its loopback port, unit, release/current paths, commands, state directory, and backup policy",
		"Docker, mixed-deployment, custom, or otherwise non-standard legacy policies require manual root review",
		"exactly one installer-generated `restrict,from=\"...\",command=\"...\" ssh-ed25519 ...` entry",
		"the new receipt is committed before the new forced key is activated last",
		"then run **helper automatic setup** again",
		"remote bootstrap derives an exact `/32` or `/128` from that SSH connection",
		"before the binary, config, and `/etc/autostream/update-host.install-state` receipt",
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("bootstrap guide is missing managed reconfiguration marker %q", want)
		}
	}
	for _, obsolete := range []string{
		"A different existing config or key fails closed so a normal reinstall cannot silently broaden host authority.",
		"rerun the verified helper installer with the same reported public key after confirming that no update is active.",
	} {
		if strings.Contains(guide, obsolete) {
			t.Fatalf("bootstrap guide contains obsolete reconfiguration guidance %q", obsolete)
		}
	}
}
