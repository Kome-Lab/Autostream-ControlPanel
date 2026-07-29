package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlPanelReleaseShipsManagedServiceInstaller(t *testing.T) {
	root := filepath.Join("..", "..")
	installerPath := filepath.Join(root, "release", "install-autostream-control-panel")
	installerBytes, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerBytes)

	for _, marker := range []string{
		"set -euo pipefail",
		`readonly SERVICE_NAME="control-panel"`,
		`readonly MANAGED_ROOT="/opt/autostream/control-panel"`,
		`readonly PUBLIC_BINARY="/usr/local/bin/control-panel"`,
		`readonly PUBLIC_WEB="/usr/share/autostream-control-panel"`,
		`readonly ENV_DEST="/etc/autostream/control-panel.env"`,
		`readonly UNIT_DEST="/etc/systemd/system/autostream-control-panel.service"`,
		`readonly INSTALL_BACKUP_ROOT="/var/backups/autostream/install-migrations/control-panel"`,
		"sha256sum --check --strict",
		"release-manifest.json",
		`(.minimum_agent_version | type == "string"`,
		`(.database_schema == "backward_compatible")`,
		`([.artifacts[].arch] | sort == ["amd64", "arm64"])`,
		`.size | type == "number" and . > 0 and . <= 268435456`,
		`verify_release_checksum_inventory "${release_dir}" true`,
		`release file is not listed in checksums.txt`,
		".artifact-sha256",
		".version",
		`existing environment file must be root-only or root-readable with mode 0600/0640`,
		`[[ -f /usr/bin/mariadb-dump && ! -L /usr/bin/mariadb-dump && -x /usr/bin/mariadb-dump ]]`,
		`[[ ${binary_version_output%%$'\n'*} == "autostream-control-panel ${VERSION}" ]]`,
		`ensure_root_only_backup_directory "${INSTALL_BACKUP_ROOT}"`,
		`require_secure_root_directory "${fixed_parent}"`,
		`required system directory does not resolve to its fixed path`,
		`existing service state path is not a safe directory`,
		`sync_installer_filesystems || die "could not durably commit the installed files"`,
		`flock -n 9 || die "another privileged update is already active for ${UNIT_NAME}"`,
		`rollback was incomplete; root-only recovery evidence is retained`,
		`cp -a -- "${link_path}" "${backup_path}"`,
		"set +e",
		"systemctl daemon-reload",
		"systemctl is-active --quiet",
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("service installer is missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		`systemctl restart "${UNIT_NAME}"`,
		`systemctl enable --now "${UNIT_NAME}"`,
	} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("service installer must print, not automatically execute, %q", forbidden)
		}
	}
	if strings.Contains(installer, "& 7022") {
		t.Fatal("service installer uses a decimal permission mask instead of the intended octal 07022 mask")
	}
	if count := strings.Count(installer, "& 07022"); count != 4 {
		t.Fatalf("service installer octal unsafe-mode guard count = %d, want 4", count)
	}

	workflowBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-host.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	for _, marker := range []string{
		`bash -n release/install-autostream-control-panel`,
		`sudo bash release/test-install-autostream-control-panel-integration.sh`,
		`cp release/install-autostream-control-panel "${root}/install-autostream-control-panel"`,
		`chmod 0755 "${root}/install-autostream-control-panel"`,
		`- name: Attest Control Panel archives`,
		`artifacts/autostream-control-panel_${{ needs.release-host.outputs.version }}_linux_amd64.tar.gz`,
		`artifacts/autostream-control-panel_${{ needs.release-host.outputs.version }}_linux_arm64.tar.gz`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("host release workflow is missing installer packaging marker %q", marker)
		}
	}
	ciBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ciBytes), `bash -n release/install-autostream-control-panel`) {
		t.Fatal("CI must syntax-check the service installer")
	}
	if !strings.Contains(
		string(ciBytes),
		`sudo bash release/test-install-autostream-control-panel-integration.sh`,
	) {
		t.Fatal("CI must execute the service installer migration integration test")
	}

	integrationBytes, err := os.ReadFile(
		filepath.Join(root, "release", "test-install-autostream-control-panel-integration.sh"),
	)
	if err != nil {
		t.Fatal(err)
	}
	integration := string(integrationBytes)
	for _, marker := range []string{
		"mktemp failure injection did not execute the installer mktemp boundary",
		"mktemp failure mutated the service account",
		"unsafe root-anchor mode ${mode} unexpectedly passed",
		"unsafe root-anchor mode ${mode} mutated managed state",
		"AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS",
		"autostream-control-panel-installer-test-bin /usr/local/bin",
		"autostream-control-panel-installer-test-sbin /usr/local/sbin",
		"autostream-control-panel-installer-test-opt /opt",
		"autostream-control-panel-installer-test-share /usr/share",
		"isolated /usr/local/bin mount is missing",
		"isolated /usr/local/sbin mount is missing",
		"isolated /opt mount is missing",
		"isolated /usr/share mount is missing",
		"could not create an isolated safe /usr/local/bin fixture",
		"could not create an isolated safe /usr/local/sbin fixture",
		"could not create an isolated safe /opt fixture",
		"could not create an isolated safe /usr/share fixture",
		"unsafe service state symlink unexpectedly passed",
		"installer ignored updater lock contention",
		"prefix-colliding binary version unexpectedly passed",
		"daemon-reload failure injection unexpectedly succeeded",
		"activation sync failure injection unexpectedly succeeded",
		"sync failure injection did not reach the post-activation durability boundary",
		"successful migration replaced the running legacy process",
		"idempotent reinstall changed the existing environment",
		"fresh installer unexpectedly started the service",
		`legacy_unit_file_state="$(systemctl is-enabled "${UNIT}" 2>/dev/null || true)"`,
		"legacy fixture must begin disabled",
	} {
		if !strings.Contains(integration, marker) {
			t.Fatalf("installer integration test is missing scenario %q", marker)
		}
	}
	namespaceIndex := strings.Index(
		integration,
		`if [[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS:-} != "1" ]]; then`,
	)
	workDirIndex := strings.Index(integration, `WORK_DIR="$(mktemp`)
	if namespaceIndex < 0 || workDirIndex < 0 || namespaceIndex >= workDirIndex {
		t.Fatal("installer integration fixture must enter its isolated mount namespace before creating mutable state")
	}
	if count := strings.Count(integration, "[Install]\nWantedBy=multi-user.target"); count != 2 {
		t.Fatalf("integration fixture must define two enable-capable but disabled units, got %d", count)
	}

	unitBytes, err := os.ReadFile(filepath.Join(root, "systemd", "autostream-control-panel.service.example"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(unitBytes)
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/control-panel") {
		t.Fatal("Control Panel systemd unit must use the stable public binary path")
	}
	if strings.Contains(unit, "ExecStart=/opt/autostream/control-panel/current/") {
		t.Fatal("Control Panel systemd unit exposes installer-owned release internals")
	}

	guideBytes, err := os.ReadFile(filepath.Join(root, "release", "README.install.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(guideBytes)
	for _, marker := range []string{
		"sudo ./install-autostream-control-panel",
		"gh attestation verify autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz",
		"gh attestation verify release-manifest.json",
		"--repo Kome-Lab/Autostream-ControlPanel",
		"sudo install -o root -g root -m 0644 /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz",
		"sudo tar --no-same-owner --no-same-permissions -xzf",
		"AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel",
		"installer-owned",
	} {
		if !strings.Contains(guide, marker) {
			t.Fatalf("install guide is missing simple installer marker %q", marker)
		}
	}
}
