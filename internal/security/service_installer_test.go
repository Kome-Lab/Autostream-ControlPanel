package security

import (
	"os"
	"path/filepath"
	"regexp"
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
		`find "${managed_candidate}" -type d -exec chmod 0755 {} +`,
		`find "${managed_candidate}" -type f -exec chmod 0644 {} +`,
		`"${managed_candidate}/bin/autostream-updater"`,
		`verify_managed_release "${managed_candidate}"`,
		`candidate_version_output="$(runuser -u autostream -- "${managed_candidate}/bin/control-panel" --version)"`,
		`sync -f "${RELEASES_DIR}"`,
		`managed_version_output="$(runuser -u autostream -- "${RELEASE_DIR}/bin/control-panel" --version)"`,
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
		`normalize_managed_release_modes`,
		`find "${RELEASE_DIR}" -type d -exec chmod`,
		`find "${RELEASE_DIR}" -type f -exec chmod`,
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
		`-o "${root}/bin/autostream-updater" ./cmd/autostream-updater`,
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
		`exec unshare --mount --propagation private bash -c '
    set -euo pipefail`,
		"autostream-control-panel-installer-test-scratch /mnt",
		"mount --rbind /usr /mnt/usr-lower",
		"mount --make-rprivate /mnt/usr-lower",
		"mount --rbind /etc /mnt/etc-lower",
		"mount --make-rprivate /mnt/etc-lower",
		"mount --rbind /var /mnt/var-lower",
		"mount --make-rprivate /mnt/var-lower",
		"mount --rbind /run /mnt/run-lower",
		"mount --make-rprivate /mnt/run-lower",
		"/mnt/usr-upper/local",
		"/mnt/var-upper/backups",
		"/mnt/run-work",
		"lowerdir=/mnt/usr-lower,upperdir=/mnt/usr-upper,workdir=/mnt/usr-work",
		"lowerdir=/mnt/etc-lower,upperdir=/mnt/etc-upper,workdir=/mnt/etc-work",
		"lowerdir=/mnt/var-lower,upperdir=/mnt/var-upper,workdir=/mnt/var-work",
		"lowerdir=/mnt/run-lower,upperdir=/mnt/run-upper,workdir=/mnt/run-work",
		"autostream-control-panel-installer-test-usr /usr",
		"autostream-control-panel-installer-test-etc /etc",
		"autostream-control-panel-installer-test-var /var",
		"autostream-control-panel-installer-test-run /run",
		"mount --rbind /mnt/run-lower/systemd /run/systemd",
		"autostream-control-panel-installer-test-bin /usr/local/bin",
		"autostream-control-panel-installer-test-sbin /usr/local/sbin",
		"autostream-control-panel-installer-test-opt /opt",
		"autostream-control-panel-installer-test-share /usr/share",
		"autostream-control-panel-installer-test-sealed /mnt",
		` /mnt ro[^ ]*( [^ ]+)* - tmpfs autostream-control-panel-installer-test-sealed `,
		`grep -Eq ' /mnt .* - tmpfs autostream-control-panel-installer-test-scratch '`,
		`grep -Eq ' /usr .* - overlay autostream-control-panel-installer-test-usr '`,
		`grep -Eq ' /etc .* - overlay autostream-control-panel-installer-test-etc '`,
		`grep -Eq ' /var .* - overlay autostream-control-panel-installer-test-var '`,
		`grep -Eq ' /run .* - overlay autostream-control-panel-installer-test-run '`,
		"AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_RUN_SYSTEMD_IDENTITY",
		`[[ $(stat -c '%U:%G:%a' -- /mnt) == "root:root:555" ]]`,
		"sealed /mnt unexpectedly permits writes to hidden host aliases",
		`[[ $(stat -c '%U:%G:%a' -- /usr) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /etc) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /etc/systemd) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /etc/systemd/system) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /var) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /var/lib) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /var/backups) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /var/tmp) == "root:root:1777" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /run) == "root:root:755" ]]`,
		`[[ $(stat -c '%U:%G:%a' -- /usr/local) == "root:root:755" ]]`,
		`[[ $(stat -c '%m' -- /usr/local/bin) == "/usr/local/bin" ]]`,
		`[[ $(stat -c '%m' -- /usr/local/sbin) == "/usr/local/sbin" ]]`,
		`[[ $(stat -c '%m' -- /opt) == "/opt" ]]`,
		`[[ $(stat -c '%m' -- /usr/share) == "/usr/share" ]]`,
		"isolated /usr/local/bin mount is missing",
		"isolated /usr/local/sbin mount is missing",
		"isolated /opt mount is missing",
		"isolated /usr/share mount is missing",
		"sealed /mnt mount is missing or writable",
		"could not create an isolated safe /usr fixture",
		"could not create an isolated safe /usr/local fixture",
		"could not create an isolated safe /usr/local/bin fixture",
		"could not create an isolated safe /usr/local/sbin fixture",
		"could not create an isolated safe /opt fixture",
		"could not create an isolated safe /usr/share fixture",
		"could not restore isolated /usr/local/bin to root:root mode 0755",
		"chmod 00755 /usr/local/bin",
		"unsafe service state symlink unexpectedly passed",
		"installer ignored updater lock contention",
		"prefix-colliding binary version unexpectedly passed",
		"daemon-reload failure injection unexpectedly succeeded",
		"activation sync failure injection unexpectedly succeeded",
		"sync failure injection did not reach the post-activation durability boundary",
		"successful migration replaced the running legacy process",
		"idempotent reinstall changed the existing environment",
		"fresh installer unexpectedly started the service",
		`cat > "${EXTRACTED_ROOT}/bin/autostream-updater"`,
		`chmod 0755 "${EXTRACTED_ROOT}/bin/autostream-updater"`,
		`grep -Eq '^[0-9a-f]{64}  \./bin/autostream-updater$'`,
		`"${fresh_release}/bin/autostream-updater"`,
		"fresh managed release was not runnable by autostream",
		"snapshot_managed_release_tree()",
		`find . -printf '%P|%D:%i|%U:%G|%m|%s\n'`,
		"idempotent reinstall changed existing managed release metadata or content",
		`legacy_unit_file_state="$(systemctl is-enabled "${UNIT}" 2>/dev/null || true)"`,
		"legacy fixture must begin disabled",
		`readonly RUNTIME_UNIT_PATH="/run/systemd/system/${UNIT}"`,
		"systemd runtime unit directory is unsafe",
		"fixture_paths_owned=false",
		"fixture_service_start_attempted=false",
		"old_pid_start_time=\"\"",
		"runtime_unit_owned=false",
		"runtime_unit_identity=\"\"",
		"runtime_cleanup_preremove_hook=\"\"",
		"read_proc_pid_start_time()",
		`if [[ ${fixture_paths_owned} == true ]]; then`,
		`if [[ ${fixture_service_start_attempted} == true &&`,
		`$(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == "${runtime_unit_identity}"`,
		"install_runtime_unit_exclusive()",
		`ln -- "${runtime_unit_candidate}" "${RUNTIME_UNIT_PATH}"`,
		"replace_owned_runtime_unit()",
		`mv -Tf -- "${runtime_unit_candidate}" "${RUNTIME_UNIT_PATH}"`,
		`sync -f "${runtime_unit_candidate}"
  if [[ -n ${runtime_sync_precommit_hook} ]]`,
		`if ! runtime_unit_identity_is_owned; then`,
		"runtime_unit_identity_is_owned()",
		"replace_runtime_unit_for_precommit_probe()",
		"restore_runtime_sync_race()",
		"remove_owned_runtime_unit_for_cleanup()",
		`runtime_sync_precommit_hook=replace_runtime_unit_for_precommit_probe`,
		"runtime precommit race unexpectedly committed",
		"runtime precommit race changed the foreign unit inode",
		"runtime precommit race changed PID1 ExecStart",
		"could not restore the owned runtime unit after the race probe",
		`runtime_cleanup_preremove_hook=replace_runtime_unit_for_precommit_probe`,
		"cleanup pre-remove race unexpectedly removed or accepted a foreign unit",
		"cleanup pre-remove race changed the foreign unit inode",
		"cleanup pre-remove race changed PID1 ExecStart",
		"could not restore the owned runtime unit after the cleanup race probe",
		"assert_owned_runtime_unit_identity()",
		"record_fixture_process_identity()",
		"kill_recorded_fixture_process()",
		`[[ ${current_start_time} == "${old_pid_start_time}" ]]`,
		"assert_pid_reuse_guard()",
		"PID reuse guard signaled an unrelated process",
		`sync -f /run/systemd/system`,
		"assert_legacy_runtime_unit_loaded()",
		"assert_managed_runtime_unit_loaded()",
		`systemctl show --property FragmentPath --value "${UNIT}"`,
		`systemctl show --property ExecStart --value "${UNIT}"`,
		`systemctl show --property User --value "${UNIT}"`,
		`"${TARGET_LOCK}"; do`,
		"AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_PREFLIGHT_PROBE",
		"preflight preservation probe unexpectedly passed",
		"preflight failure replaced the existing runtime unit",
		"preflight failure changed the existing runtime unit",
		"preflight failure stopped the existing service process",
		"preflight failure changed the existing service enablement",
		"cleanup_failed=false",
		"cleanup refused a missing or replaced runtime unit",
		"cleanup left the fixture service active",
		"cleanup left the fixture unit loaded",
		`if [[ ${cleanup_failed} == true && ${exit_code} -eq 0 ]]; then`,
	} {
		if !strings.Contains(integration, marker) {
			t.Fatalf("installer integration test is missing scenario %q", marker)
		}
	}
	sealedMountPattern := regexp.MustCompile(
		` /mnt ro[^ ]*( [^ ]+)* - tmpfs autostream-control-panel-installer-test-sealed `,
	)
	for _, mountInfoLine := range []string{
		"42 31 0:39 / /mnt ro,nosuid,nodev,noexec,relatime - tmpfs autostream-control-panel-installer-test-sealed ro",
		"42 31 0:39 / /mnt ro,nosuid,nodev,noexec,relatime shared:9 - tmpfs autostream-control-panel-installer-test-sealed ro",
	} {
		if !sealedMountPattern.MatchString(mountInfoLine) {
			t.Fatalf("sealed /mnt pattern rejected valid mountinfo line %q", mountInfoLine)
		}
	}
	namespaceIndex := strings.Index(
		integration,
		`if [[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS:-} != "1" ]]; then`,
	)
	strictModeIndex := strings.Index(
		integration,
		`exec unshare --mount --propagation private bash -c '
    set -euo pipefail`,
	)
	scratchMountIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-scratch /mnt",
	)
	usrLowerBindIndex := strings.Index(integration, "mount --rbind /usr /mnt/usr-lower")
	etcLowerBindIndex := strings.Index(integration, "mount --rbind /etc /mnt/etc-lower")
	varLowerBindIndex := strings.Index(integration, "mount --rbind /var /mnt/var-lower")
	runLowerBindIndex := strings.Index(integration, "mount --rbind /run /mnt/run-lower")
	usrLowerPrivateIndex := strings.Index(integration, "mount --make-rprivate /mnt/usr-lower")
	etcLowerPrivateIndex := strings.Index(integration, "mount --make-rprivate /mnt/etc-lower")
	varLowerPrivateIndex := strings.Index(integration, "mount --make-rprivate /mnt/var-lower")
	runLowerPrivateIndex := strings.Index(integration, "mount --make-rprivate /mnt/run-lower")
	upperLocalIndex := strings.Index(integration, "/mnt/usr-upper/local")
	overlayWorkIndex := strings.Index(
		integration,
		"/mnt/run-work",
	)
	usrOverlayIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-usr /usr",
	)
	etcOverlayIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-etc /etc",
	)
	varOverlayIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-var /var",
	)
	runOverlayIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-run /run",
	)
	systemdBindIndex := strings.Index(
		integration,
		"mount --rbind /mnt/run-lower/systemd /run/systemd",
	)
	binMountIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-bin /usr/local/bin",
	)
	sbinMountIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-sbin /usr/local/sbin",
	)
	optMountIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-opt /opt",
	)
	shareMountIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-share /usr/share",
	)
	sealedMountIndex := strings.Index(
		integration,
		"autostream-control-panel-installer-test-sealed /mnt",
	)
	workDirIndex := strings.Index(integration, `WORK_DIR="$(mktemp`)
	if namespaceIndex < 0 ||
		strictModeIndex < 0 ||
		scratchMountIndex < 0 ||
		usrLowerBindIndex < 0 ||
		etcLowerBindIndex < 0 ||
		varLowerBindIndex < 0 ||
		runLowerBindIndex < 0 ||
		usrLowerPrivateIndex < 0 ||
		etcLowerPrivateIndex < 0 ||
		varLowerPrivateIndex < 0 ||
		runLowerPrivateIndex < 0 ||
		upperLocalIndex < 0 ||
		overlayWorkIndex < 0 ||
		usrOverlayIndex < 0 ||
		etcOverlayIndex < 0 ||
		varOverlayIndex < 0 ||
		runOverlayIndex < 0 ||
		systemdBindIndex < 0 ||
		binMountIndex < 0 ||
		sbinMountIndex < 0 ||
		optMountIndex < 0 ||
		shareMountIndex < 0 ||
		sealedMountIndex < 0 ||
		workDirIndex < 0 ||
		namespaceIndex >= strictModeIndex ||
		strictModeIndex >= scratchMountIndex ||
		scratchMountIndex >= usrLowerBindIndex ||
		usrLowerBindIndex >= usrLowerPrivateIndex ||
		usrLowerPrivateIndex >= etcLowerBindIndex ||
		etcLowerBindIndex >= etcLowerPrivateIndex ||
		etcLowerPrivateIndex >= varLowerBindIndex ||
		varLowerBindIndex >= varLowerPrivateIndex ||
		varLowerPrivateIndex >= runLowerBindIndex ||
		runLowerBindIndex >= runLowerPrivateIndex ||
		runLowerPrivateIndex >= upperLocalIndex ||
		upperLocalIndex >= overlayWorkIndex ||
		overlayWorkIndex >= usrOverlayIndex ||
		usrOverlayIndex >= etcOverlayIndex ||
		etcOverlayIndex >= varOverlayIndex ||
		varOverlayIndex >= runOverlayIndex ||
		runOverlayIndex >= systemdBindIndex ||
		systemdBindIndex >= binMountIndex ||
		systemdBindIndex >= sbinMountIndex ||
		systemdBindIndex >= optMountIndex ||
		systemdBindIndex >= shareMountIndex ||
		binMountIndex >= sealedMountIndex ||
		sbinMountIndex >= sealedMountIndex ||
		optMountIndex >= sealedMountIndex ||
		shareMountIndex >= sealedMountIndex ||
		sealedMountIndex >= workDirIndex {
		t.Fatal("installer integration fixture must enter strict mode, isolate /usr, /etc, /var, and /run, bind host systemd runtime, mount child fixtures, seal /mnt, then create mutable state")
	}
	if count := strings.Count(integration, "restore_safe_root_anchor_fixture"); count != 3 {
		t.Fatalf("unsafe root-anchor fixture safe reset count = %d, want helper plus before/after calls", count)
	}
	if count := strings.Count(integration, "[Install]\nWantedBy=multi-user.target"); count != 4 {
		t.Fatalf("integration fixture must define four enable-capable but disabled units, got %d", count)
	}

	candidateNormalizeIndex := strings.Index(
		installer,
		`find "${managed_candidate}" -type d -exec chmod 0755 {} +`,
	)
	candidateVerifyIndex := strings.Index(
		installer,
		`verify_managed_release "${managed_candidate}"`,
	)
	candidateExecutableNormalizeIndex := strings.Index(
		installer,
		`"${managed_candidate}/install-autostream-control-panel"`,
	)
	candidateFileNormalizeIndex := strings.Index(
		installer,
		`find "${managed_candidate}" -type f -exec chmod 0644 {} +`,
	)
	candidateMarkerNormalizeIndex := strings.Index(
		installer,
		`chmod 0444 "${managed_candidate}/.artifact-sha256" "${managed_candidate}/.version"`,
	)
	candidateRunIndex := strings.Index(
		installer,
		`candidate_version_output="$(runuser -u autostream -- "${managed_candidate}/bin/control-panel" --version)"`,
	)
	candidateSyncIndex := strings.Index(installer, `sync -f "${managed_candidate}"`)
	candidateMoveIndex := strings.Index(
		installer,
		`mv -T -- "${managed_candidate}" "${RELEASE_DIR}"`,
	)
	releasesParentSyncIndex := strings.Index(installer, `sync -f "${RELEASES_DIR}"`)
	postMoveVerifyIndex := strings.LastIndex(
		installer,
		`verify_managed_release "${RELEASE_DIR}"`,
	)
	if candidateNormalizeIndex < 0 ||
		candidateFileNormalizeIndex < 0 ||
		candidateExecutableNormalizeIndex < 0 ||
		candidateMarkerNormalizeIndex < 0 ||
		candidateVerifyIndex < 0 ||
		candidateRunIndex < 0 ||
		candidateSyncIndex < 0 ||
		candidateMoveIndex < 0 ||
		releasesParentSyncIndex < 0 ||
		postMoveVerifyIndex < 0 ||
		candidateNormalizeIndex >= candidateFileNormalizeIndex ||
		candidateFileNormalizeIndex >= candidateExecutableNormalizeIndex ||
		candidateExecutableNormalizeIndex >= candidateMarkerNormalizeIndex ||
		candidateMarkerNormalizeIndex >= candidateVerifyIndex ||
		candidateVerifyIndex >= candidateRunIndex ||
		candidateRunIndex >= candidateSyncIndex ||
		candidateSyncIndex >= candidateMoveIndex ||
		candidateMoveIndex >= releasesParentSyncIndex ||
		releasesParentSyncIndex >= postMoveVerifyIndex {
		t.Fatal("managed candidate must be fully normalized, verified, run as autostream, synced, atomically moved, parent-synced, and reverified")
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
