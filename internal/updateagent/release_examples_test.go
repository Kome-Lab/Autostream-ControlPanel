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
		"## Prepare the updater backup command",
		`test -x "$RELEASE_DIR/backup/autostream-backup-control-panel"`,
		`sudo install -o root -g root -m 0700 "$RELEASE_DIR/backup/autostream-backup-control-panel" /usr/local/sbin/autostream-backup-control-panel`,
		"sudo chmod 0600 /etc/autostream/mariadb-backup.cnf",
		"GRANT SELECT, SHOW VIEW, TRIGGER ON \\`${DATABASE_NAME}\\`.*",
		"exact `DATABASE_NAME` must be used for the MariaDB grant, the real dump, and the",
		"sudo /usr/local/sbin/autostream-backup-control-panel",
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("Control Panel install guide is missing %q", want)
		}
	}
	backupCheck := strings.Index(guide, "sudo /usr/local/sbin/autostream-backup-control-panel")
	managedSwitch := strings.Index(guide, `sudo ln -s "$RELEASE_DIR" "${CURRENT_LINK}.next"`)
	if backupCheck < 0 || managedSwitch < 0 || backupCheck > managedSwitch {
		t.Fatal("Control Panel install guide must verify a real database backup before switching the managed release")
	}
	backupSectionStart := strings.Index(guide, "## Prepare the updater backup command")
	activationStart := strings.Index(guide, "## Activate the managed release")
	if backupSectionStart < 0 || activationStart < 0 || backupSectionStart >= activationStart {
		t.Fatal("Control Panel install guide has invalid backup and activation sections")
	}
	if strings.Contains(guide[backupSectionStart:activationStart], "readlink -f /opt/autostream/control-panel/current") {
		t.Fatal("backup preparation must select the verified new release before the current link exists")
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
		"`/usr/local/bin/autostream-updater`",
		"`/usr/share/autostream-control-panel`",
		"Installing only the central updater does not require migrating an existing `/usr/local/bin/control-panel`",
		"For that existing direct layout, skip to **Install the central updater once**.",
		"sudo install -d -o root -g root -m 0755 /etc/autostream",
		"sudo /usr/local/bin/autostream-updater configure --panel-url \"https://control.example.com\" --node \"central-updater\"",
		"reads it from the TTY or bounded standard input with echo disabled",
		"`/etc/autostream/updater.json` as `root:autostream-updater 0640`",
		"sudo -u autostream-updater test -r /etc/autostream/updater.json",
		"The generated file contains only the Control Panel connection identity",
		"Do not edit it",
		"**Application Info > System Updates**",
		"The current runtime is pinned to this public Control Panel repository",
		"Changing the repository to private requires a separate trust-root and provenance-policy implementation",
		"It is write-only in the Control Panel and is never shown after saving.",
		"delivers it only once to the updater that claims an authorized update job",
		"complete SSH server public key, verified through an independent channel",
		"Do not trust `ssh-keyscan` output by itself",
		"Saving starts automatic pull and validation. No service restart is required.",
		"generates a separate Ed25519 client key and reports only its public key",
		"Automatic bootstrap requires the saved SSH user to be exactly `autostream-update-host`",
		"For a standard systemd host, use **helper automatic setup**",
		"This temporary administrator is separate from the saved `autostream-update-host` user",
		"Control Panel on `8080`, Encoder / Recorder on `8081`, Observability on `8082`, Discord Bot on `8083`, and Worker on `8084`",
		"The managed host does not run another updater daemon or listener",
		"no helper-specific port, environment file, Node Runtime Token, or `/etc/autostream/updater.json`",
		"That identity-only `updater.json` belongs only to the central updater host.",
		"For Docker, a non-standard port or path, or a custom target",
		"`applied` means the updater accepted the desired revision",
		"defers applying the new revision until that job reaches a safe terminal state",
	} {
		if !strings.Contains(guide, marker) {
			t.Fatalf("control panel install guide is missing managed updater marker %q", marker)
		}
	}
	for _, obsolete := range []string{
		"if ! sudo test -e /etc/autostream/updater.json; then",
		`"$RELEASE_DIR/autostream-updater.json.example" /etc/autostream/updater.json`,
		"`/opt/autostream/control-panel/current/autostream-updater.json.example` as",
		"`--init-from",
		"known_hosts",
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
