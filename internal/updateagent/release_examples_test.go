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
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("bootstrap guide is missing %q", want)
		}
	}
}
