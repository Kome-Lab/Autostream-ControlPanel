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
		`readonly ARTIFACT_MANIFEST_NAME="artifact-manifest.json"`,
		`readonly MAX_ARCHIVE_SIZE=268435456`,
		`$(stat -c '%U:%G:%a' -- "${INPUT_STAGE}") == "root:root:700"`,
		`[[ ${ARTIFACT_SIZE} -le ${MAX_ARCHIVE_SIZE} ]]`,
		`awk '{ sub(/\/$/, ""); print }' "${INPUT_STAGE}/archive.list"`,
		`die "release archive contains duplicate paths"`,
		`${entry} != *\\*`,
		`${entry} != *"/./"*`,
		`${entry} != *"//"*`,
		`["archive", "build_date", "commit", "compatibility", "component",`,
		`(.component == "control-panel")`,
		`(.minimum_agent_version == "v1.7.0")`,
		`(.minimum_panel_version == null)`,
		`(.rollback_compatible == true)`,
		`(.database_schema == "backward_compatible")`,
		`(.name == $archive_name)`,
		`(.root == $artifact_id)`,
		`(.os == "linux")`,
		`(.arch == $arch)`,
		`verify_release_checksum_inventory "${release_dir}" true`,
		`release file is not listed in checksums.txt`,
		".artifact-sha256",
		".version",
		`existing environment file must be root-only or root-readable with mode 0600/0640`,
		`[[ -f /usr/bin/mariadb-dump && ! -L /usr/bin/mariadb-dump && -x /usr/bin/mariadb-dump ]]`,
		`ensure_root_only_backup_directory "${INSTALL_BACKUP_ROOT}"`,
		`require_secure_root_directory "${fixed_parent}"`,
		`required system directory does not resolve to its fixed path`,
		`existing service state path is not a safe directory`,
		`sync_installer_filesystems || die "could not durably commit the installed files"`,
		`ensure_permanent_lock_directory /run/autostream-updater`,
		`ensure_permanent_lock_path_atomically()`,
		`if ln -- "${lock_create_stage}" "${path}" 2>/dev/null; then`,
		`readonly SHARED_HOST_SETUP_LOCK="/run/autostream-updater/.autostream-runtime-host-setup.lock"`,
		`ensure_permanent_lock_path_atomically "${SHARED_HOST_SETUP_LOCK}"`,
		`exec 8<>"${SHARED_HOST_SETUP_LOCK}"`,
		`chown root:root /proc/self/fd/8`,
		`chmod 0600 /proc/self/fd/8`,
		`shared_lock_fd_identity="$(stat -Lc '%d:%i' -- /proc/self/fd/8)"`,
		`-f /proc/self/fd/8`,
		`$(stat -Lc '%U:%G:%a' -- /proc/self/fd/8) == "root:root:600"`,
		`die "shared host-setup lock identity changed while being opened"`,
		`flock -n 8 || die "another AutoStream installer is provisioning shared host state"`,
		`die "shared host-setup lock identity changed after acquisition"`,
		`ensure_permanent_lock_path_atomically "${TARGET_LOCK}"`,
		`exec 9<>"${TARGET_LOCK}"`,
		`chown root:root /proc/self/fd/9`,
		`chmod 0600 /proc/self/fd/9`,
		`target_lock_fd_identity="$(stat -Lc '%d:%i' -- /proc/self/fd/9)"`,
		`-f /proc/self/fd/9`,
		`$(stat -Lc '%U:%G:%a' -- /proc/self/fd/9) == "root:root:600"`,
		`die "updater target lock identity changed while being opened"`,
		`flock -n 9 || die "another privileged update is already active for ${UNIT_NAME}"`,
		`die "updater target lock identity changed after acquisition"`,
		`rollback was incomplete; root-only recovery evidence is retained`,
		`prepare_install_directory`,
		`rollback_prepared_directories`,
		`service_user_created`,
		`service_group_created`,
		`release_dir_created`,
		`A published lock pathname is intentionally persistent`,
		`shared_lock_opened`,
		`target_lock_opened`,
		`remove_pending_link`,
		`record_created_backup`,
		`unit_backup_created=false`,
		`unit_backup_identity=""`,
		`backup_exec_backup_created=false`,
		`backup_exec_backup_identity=""`,
		`previous_public_backup_created`,
		`previous_public_backup_complete`,
		`previous_public_backup_identities`,
		`previous_public_source_identities`,
		`previous_public_backup_sha256`,
		`previous_public_uids`,
		`previous_public_gids`,
		`previous_public_modes`,
		`previous_public_sha256`,
		`previous_public_tree_sha256`,
		`installed_public_targets`,
		`installed_public_identities`,
		`public_directory_tree_sha256()`,
		`die "legacy public backup must be owned by root:root: ${backup_path}"`,
		`service_group_gid="$(getent group autostream | awk -F: 'NR == 1 { print $3 }')"`,
		`die "autostream service group must not use GID 0"`,
		`service_user_gid="$(id -g autostream)"`,
		`[[ ${service_user_gid} == "${service_group_gid}" ]]`,
		`userdel "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
		`groupdel autostream`,
		`cleanup_running=false`,
		`signal_transaction_active=false`,
		`deferred_termination_status=0`,
		`handle_installer_signal()`,
		`restore_installer_signal_traps()`,
		`trap '' HUP INT TERM`,
		`trap 'handle_installer_signal 129' HUP`,
		`trap 'handle_installer_signal 130' INT`,
		`trap 'handle_installer_signal 143' TERM`,
		`begin_installer_signal_transaction()`,
		`finish_installer_signal_transaction()`,
		`create_journaled_temporary_path()`,
		`copy_created_backup_and_record()`,
		`create_autostream_group()`,
		`create_autostream_user()`,
		`create_journaled_temporary_path managed_candidate directory`,
		`create_journaled_temporary_path lock_create_stage file`,
		`create_journaled_temporary_path env_stage file`,
		`create_journaled_temporary_path unit_stage file`,
		`create_journaled_temporary_path backup_exec_stage file`,
		`create_journaled_temporary_path backup_config_stage file`,
		`copy_created_backup_and_record "${link_path}" "${backup_path}"`,
		`find "${managed_candidate}" -type d -exec chmod 0755 {} +`,
		`find "${managed_candidate}" -type f -exec chmod 0644 {} +`,
		`verify_managed_release "${managed_candidate}"`,
		`verify_binary_identity "${EXTRACTED_ROOT}/bin/control-panel" "autostream-control-panel"`,
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
	publicDirectoryStart := strings.Index(
		installer,
		`elif [[ -d ${link_path} && ! -L ${link_path} ]]; then`,
	)
	if publicDirectoryStart < 0 {
		t.Fatal("service installer is missing the legacy public directory migration branch")
	}
	publicDirectoryEndOffset := strings.Index(
		installer[publicDirectoryStart:],
		`elif [[ -e ${link_path} ]]; then`,
	)
	if publicDirectoryEndOffset < 0 {
		t.Fatal("service installer is missing the legacy public directory migration boundary")
	}
	publicDirectoryBody := installer[publicDirectoryStart : publicDirectoryStart+publicDirectoryEndOffset]
	publicDirectoryCursor := 0
	for _, marker := range []string{
		`previous_source_identity="$(stat -c '%d:%i' -- "${link_path}")"`,
		`previous_tree_sha256="$(public_directory_tree_sha256 "${link_path}")"`,
		`begin_installer_signal_transaction`,
		`previous_public_backup_complete+=("${directory_backup_complete}")`,
		`previous_public_source_identities+=("${previous_source_identity}")`,
		`previous_public_tree_sha256+=("${previous_tree_sha256}")`,
		`installed_public_targets+=("")`,
		`installed_public_identities+=("")`,
		`if mv -T -- "${link_path}" "${backup_path}"; then`,
		`$(public_directory_tree_sha256 "${backup_path}" 2>/dev/null || true) ==`,
		`previous_backup_identity="$(stat -c '%d:%i' -- "${backup_path}")"`,
		`previous_public_backup_identities[journal_index]="${previous_backup_identity}"`,
		`previous_public_backup_created[journal_index]=true`,
		`previous_public_backup_complete[journal_index]=true`,
		`previous_public_backup_complete[journal_index]=false`,
		`sync -f "${LEGACY_BACKUP_DIR}"`,
		`finish_installer_signal_transaction`,
		`if [[ ${directory_move_status} -ne 0 ]]; then`,
		`return "${directory_move_status}"`,
	} {
		markerOffset := strings.Index(publicDirectoryBody[publicDirectoryCursor:], marker)
		if markerOffset < 0 {
			t.Fatalf("legacy public directory migration is missing ordered cross-filesystem marker %q", marker)
		}
		publicDirectoryCursor += markerOffset + len(marker)
	}
	rollbackPublicStart := strings.Index(installer, "rollback_public_links() {")
	rollbackPublicEnd := strings.Index(installer, "rollback_current_link() {")
	if rollbackPublicStart < 0 || rollbackPublicEnd < 0 || rollbackPublicStart >= rollbackPublicEnd {
		t.Fatal("service installer is missing the public-link rollback boundary")
	}
	rollbackPublicBody := installer[rollbackPublicStart:rollbackPublicEnd]
	for _, marker := range []string{
		`source_identity="${previous_public_source_identities[index]}"`,
		`backup_complete="${previous_public_backup_complete[index]}"`,
		`previous_tree_sha256="${previous_public_tree_sha256[index]}"`,
		`installed_target="${installed_public_targets[index]}"`,
		`installed_identity="${installed_public_identities[index]}"`,
		`${backup_complete} == false`,
		`rm -rf -- "${backup}"`,
		`${backup_complete} == true`,
		`$(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) == "${installed_identity}"`,
		`$(readlink -- "${path}" 2>/dev/null || true) == "${installed_target}"`,
		`$(public_directory_tree_sha256 "${path}" 2>/dev/null || true) ==`,
		`! -e ${backup} && ! -L ${backup}`,
		`$(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) == "${source_identity}"`,
	} {
		if !strings.Contains(rollbackPublicBody, marker) {
			t.Fatalf("legacy public directory rollback is missing safe cross-filesystem marker %q", marker)
		}
	}
	publicPublishStart := strings.Index(installer, `public_next="${link_next}"`)
	publicPublishEnd := strings.Index(installer, "\n}\n\nif [[ ${unit_previous_kind}")
	if publicPublishStart < 0 || publicPublishEnd < 0 || publicPublishStart >= publicPublishEnd {
		t.Fatal("service installer is missing the public-link publication boundary")
	}
	publicPublishBody := installer[publicPublishStart:publicPublishEnd]
	publicPublishCursor := 0
	for _, marker := range []string{
		`public_next_identity="$(stat -c '%d:%i' -- "${link_next}")"`,
		`if mv -Tf -- "${link_next}" "${link_path}"; then`,
		`$(stat -c '%d:%i' -- "${link_path}" 2>/dev/null || true) == "${public_next_identity}"`,
		`installed_public_targets[journal_index]="${target}"`,
		`installed_public_identities[journal_index]="${public_next_identity}"`,
		`finish_installer_signal_transaction`,
		`if [[ ${public_link_status} -ne 0 ]]; then`,
		`return "${public_link_status}"`,
	} {
		markerOffset := strings.Index(publicPublishBody[publicPublishCursor:], marker)
		if markerOffset < 0 {
			t.Fatalf("public-link publication is missing ordered partial-success marker %q", marker)
		}
		publicPublishCursor += markerOffset + len(marker)
	}
	for _, forbidden := range []string{
		`systemctl restart "${UNIT_NAME}"`,
		`systemctl enable --now "${UNIT_NAME}"`,
		`normalize_managed_release_modes`,
		`find "${RELEASE_DIR}" -type d -exec chmod`,
		`find "${RELEASE_DIR}" -type f -exec chmod`,
		"ARCHIVE_CHECKSUM_SOURCE",
		"MANIFEST_SOURCE",
		"release-manifest.json",
		`exec 8>"${SHARED_HOST_SETUP_LOCK}"`,
		`exec 9>"${TARGET_LOCK}"`,
		`exec 9>>"${TARGET_LOCK}"`,
		`stat -Lc '%F:%U:%G:%a'`,
		`rm -f -- "${SHARED_HOST_SETUP_LOCK}"`,
		`rm -f -- "${TARGET_LOCK}"`,
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
	rootAnchorCheck := strings.Index(installer, "for fixed_parent in")
	inputStageAllocation := strings.Index(
		installer,
		`mktemp -d /var/tmp/autostream-control-panel-install.XXXXXXXX`,
	)
	if rootAnchorCheck < 0 ||
		inputStageAllocation < 0 ||
		rootAnchorCheck >= inputStageAllocation {
		t.Fatal("Control Panel must reject unsafe fixed root anchors before allocating release staging")
	}
	stateSymlinkCheck := strings.Index(installer, `if [[ -L ${STATE_DIR} ]]; then`)
	managedParentCheck := strings.Index(installer, "for managed_parent in")
	if stateSymlinkCheck < 0 ||
		managedParentCheck < 0 ||
		stateSymlinkCheck >= inputStageAllocation ||
		stateSymlinkCheck >= managedParentCheck {
		t.Fatal("Control Panel must reject an unsafe service-state symlink before release staging and generic managed-parent validation")
	}
	bundleVerify := strings.Index(
		installer,
		`verify_binary_identity "${EXTRACTED_ROOT}/bin/control-panel" "autostream-control-panel"`,
	)
	accountMutation := strings.Index(installer, "groupadd --system autostream")
	if bundleVerify < 0 || accountMutation < 0 || bundleVerify >= accountMutation {
		t.Fatal("Control Panel archive, manifest, and binary identities must be verified before account mutation")
	}
	sharedLockAcquisition := strings.Index(
		installer,
		`flock -n 8 || die "another AutoStream installer is provisioning shared host state"`,
	)
	targetLockAcquisition := strings.Index(
		installer,
		`flock -n 9 || die "another privileged update is already active for ${UNIT_NAME}"`,
	)
	sharedLockPrecheck := strings.Index(
		installer,
		`die "shared host-setup lock identity changed while being opened"`,
	)
	sharedLockPostcheck := strings.Index(
		installer,
		`die "shared host-setup lock identity changed after acquisition"`,
	)
	targetLockPrecheck := strings.Index(
		installer,
		`die "updater target lock identity changed while being opened"`,
	)
	targetLockPostcheck := strings.Index(
		installer,
		`die "updater target lock identity changed after acquisition"`,
	)
	parentMutation := strings.Index(
		installer,
		`prepare_install_directory /opt/autostream root root 0755`,
	)
	if sharedLockAcquisition < 0 ||
		targetLockAcquisition < 0 ||
		sharedLockPrecheck < 0 ||
		sharedLockPostcheck < 0 ||
		targetLockPrecheck < 0 ||
		targetLockPostcheck < 0 ||
		accountMutation < 0 ||
		parentMutation < 0 ||
		sharedLockPrecheck >= sharedLockAcquisition ||
		sharedLockAcquisition >= sharedLockPostcheck ||
		sharedLockPostcheck >= targetLockPrecheck ||
		targetLockPrecheck >= targetLockAcquisition ||
		targetLockAcquisition >= targetLockPostcheck ||
		sharedLockAcquisition >= targetLockAcquisition ||
		targetLockAcquisition >= accountMutation ||
		targetLockAcquisition >= parentMutation ||
		targetLockPostcheck >= accountMutation ||
		targetLockPostcheck >= parentMutation {
		t.Fatal("Control Panel must acquire the shared host lock, then its target lock, before shared mutations")
	}
	groupGIDValidation := strings.Index(installer, `die "autostream service group must not use GID 0"`)
	userMutation := strings.Index(installer, `create_autostream_user "${service_group_gid}"`)
	if groupGIDValidation < 0 || userMutation < 0 || groupGIDValidation >= userMutation {
		t.Fatal("Control Panel service group numeric GID must be validated before user creation")
	}
	groupHelperStart := strings.Index(installer, "create_autostream_group()")
	userHelperStart := strings.Index(installer, "create_autostream_user()")
	groupHelperMask := strings.Index(installer[groupHelperStart:userHelperStart], "begin_installer_signal_transaction")
	groupHelperMutation := strings.Index(installer[groupHelperStart:userHelperStart], "groupadd --system autostream")
	groupHelperJournal := strings.Index(installer[groupHelperStart:userHelperStart], "service_group_created=true")
	groupHelperRestore := strings.Index(installer[groupHelperStart:userHelperStart], "finish_installer_signal_transaction")
	if groupHelperStart < 0 ||
		userHelperStart < 0 ||
		groupHelperMask < 0 ||
		groupHelperMutation < 0 ||
		groupHelperJournal < 0 ||
		groupHelperRestore < 0 ||
		groupHelperMask >= groupHelperMutation ||
		groupHelperMutation >= groupHelperJournal ||
		groupHelperJournal >= groupHelperRestore {
		t.Fatal("Control Panel group creation and rollback journal capture must share one deferred-signal window")
	}
	userHelperEnd := strings.Index(
		installer[userHelperStart:],
		"\n}\n\nprepare_autostream_user_rollback_login",
	)
	if userHelperEnd < 0 {
		t.Fatal("Control Panel user helper boundary is missing")
	}
	userHelper := installer[userHelperStart : userHelperStart+userHelperEnd]
	userHelperMask := strings.Index(userHelper, "begin_installer_signal_transaction")
	userHelperMutation := strings.Index(userHelper, "useradd --system")
	userHelperJournal := strings.Index(userHelper, "service_user_created=true")
	userHelperRestore := strings.Index(userHelper, "finish_installer_signal_transaction")
	if userHelperMask < 0 ||
		userHelperMutation < 0 ||
		userHelperJournal < 0 ||
		userHelperRestore < 0 ||
		userHelperMask >= userHelperMutation ||
		userHelperMutation >= userHelperJournal ||
		userHelperJournal >= userHelperRestore {
		t.Fatal("Control Panel user creation and rollback journal capture must share one deferred-signal window")
	}
	finishHelperStart := strings.Index(installer, "finish_installer_signal_transaction() {")
	if finishHelperStart < 0 {
		t.Fatal("Control Panel deferred-signal completion helper is missing")
	}
	finishHelperEnd := strings.Index(installer[finishHelperStart:], "\n}\n\nINPUT_STAGE=")
	if finishHelperEnd < 0 {
		t.Fatal("Control Panel deferred-signal completion helper boundary is missing")
	}
	finishHelper := installer[finishHelperStart : finishHelperStart+finishHelperEnd]
	finishDeactivate := strings.Index(finishHelper, "signal_transaction_active=false")
	finishCapture := strings.Index(finishHelper, `pending_status="${deferred_termination_status}"`)
	finishClear := strings.Index(finishHelper, "deferred_termination_status=0")
	finishDispatch := strings.Index(finishHelper, `handle_installer_signal "${pending_status}"`)
	if finishDeactivate < 0 ||
		finishCapture < 0 ||
		finishClear < 0 ||
		finishDispatch < 0 ||
		finishDeactivate >= finishCapture ||
		finishCapture >= finishClear ||
		finishClear >= finishDispatch {
		t.Fatal("Control Panel must close the signal transaction before capturing and clearing its deferred status")
	}
	cleanupStart := strings.Index(installer, "\ncleanup() {")
	cleanupTrap := strings.Index(installer, `trap 'cleanup "$?"' EXIT`)
	if cleanupStart < 0 || cleanupTrap < 0 || cleanupStart >= cleanupTrap {
		t.Fatal("Control Panel cleanup boundary is missing")
	}
	cleanupBody := installer[cleanupStart:cleanupTrap]
	cleanupMask := strings.Index(cleanupBody, "trap '' HUP INT TERM")
	cleanupJournal := strings.Index(cleanupBody, "cleanup_running=true")
	cleanupExitTrapRemoval := strings.Index(cleanupBody, "trap - EXIT")
	if cleanupMask < 0 ||
		cleanupJournal < 0 ||
		cleanupExitTrapRemoval < 0 ||
		cleanupMask >= cleanupJournal ||
		cleanupJournal >= cleanupExitTrapRemoval {
		t.Fatal("Control Panel cleanup must mask termination before disabling its EXIT trap")
	}

	workflowBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-host.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	for _, marker := range []string{
		`bash scripts/ci/build-control-panel-release-candidate.sh`,
		`- name: Attest Control Panel archives`,
		`autostream-control-panel_*.tar.gz`,
		`release-manifest.json`,
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
		"self-consistent archive with invalid artifact metadata unexpectedly passed",
		`grep -Eq '^jq: (error:|[0-9]+ compile errors?)'`,
		"artifact manifest verifier emitted a jq parser or compile error",
		"invalid artifact metadata mutated the service account",
		"archive with a duplicate canonical path unexpectedly passed",
		"duplicate archive path mutated the service account",
		"late environment preflight changed the existing state directory",
		"late environment preflight changed the existing service account",
		"late environment preflight left a fresh service account",
		"late environment preflight left persistent installer state",
		"hostile GID 0 mutated the service user or persistent paths",
		"shared host-setup lock contention mutated account, parents, or current",
		"shared host-setup contention replaced or truncated the permanent lock",
		"non-root legacy public backup changed the live or backup boundary",
		"late failure removed or changed a pre-existing legacy backup",
		"intentionally stale and ignored",
		"fixture must begin with the archive as its only adjacent release file",
		"signal-interrupted groupadd did not exit with deferred TERM status 143",
		"signal-interrupted groupadd left the service account behind",
		"signal during rollback left private input staging behind",
		"signal-safe groupdel wrapper did not execute",
		"signal-interrupted directory mutation did not exit with deferred TERM status 143",
		"signal-interrupted temporary allocation did not exit with deferred TERM status 143",
		"signal-interrupted current-link mutation did not exit with deferred TERM status 143",
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
		"legacy web and install backup fixtures must use different filesystems",
		"sync-post-activation.executed",
		"sync failure rollback changed the legacy web directory",
		"sync failure rollback changed the legacy web directory metadata",
		"sync failure rollback did not restore the legacy web tree exactly",
		"sync failure rollback left the legacy web backup behind",
		"sync failure rollback reported an earlier migration or incomplete rollback",
		"snapshot_legacy_web_tree()",
		`tar --sort=name --numeric-owner -cf "${output_path}" -C "${web_dir}" .`,
		`install -d -o root -g root -m 0710 "${PUBLIC_WEB}/assets"`,
		`ln -s -- ../legacy.txt "${PUBLIC_WEB}/assets/legacy-link"`,
		"post-mutation legacy web move did not preserve status 73",
		"post-mutation legacy web move rollback changed the legacy web tree",
		"post-mutation legacy web move rollback left the legacy web backup behind",
		"partial legacy web move did not preserve status 72",
		"partial legacy web move rollback changed the legacy web tree",
		"partial legacy web move rollback left the partial backup behind",
		"successful migration replaced the running legacy process",
		"idempotent reinstall changed the existing environment",
		"fresh installer unexpectedly started the service",
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
		`trap 'kill "${probe_pid}" >/dev/null 2>&1 || true; wait "${probe_pid}" >/dev/null 2>&1 || true' EXIT`,
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
	hostileGIDStart := strings.Index(
		integration,
		"hostile_gid_group_database_before=",
	)
	if hostileGIDStart < 0 {
		t.Fatal("installer integration test is missing the hostile GID 0 database snapshot")
	}
	hostileGIDEndOffset := strings.Index(
		integration[hostileGIDStart:],
		"\ngroupadd --system autostream",
	)
	if hostileGIDEndOffset < 0 {
		t.Fatal("installer integration test is missing the hostile GID 0 fixture boundary")
	}
	hostileGIDBody := integration[hostileGIDStart : hostileGIDStart+hostileGIDEndOffset]
	hostileGIDCursor := 0
	for _, marker := range []string{
		"hostile_gid_group_database_before=",
		"hostile_gid_gshadow_database_before=",
		"groupadd --system --gid 0 --non-unique autostream",
		"groupdel --force autostream",
		"hostile GID 0 fixture cleanup left the service group behind",
		"hostile GID 0 fixture cleanup changed the local group databases",
	} {
		markerOffset := strings.Index(hostileGIDBody[hostileGIDCursor:], marker)
		if markerOffset < 0 {
			t.Fatalf("hostile GID 0 fixture is missing safe cleanup marker %q", marker)
		}
		hostileGIDCursor += markerOffset + len(marker)
	}
	probeBody := func(name string, declaration string) string {
		t.Helper()
		start := strings.Index(integration, declaration)
		if start < 0 {
			t.Fatalf("installer integration test is missing the %s probe start", name)
		}
		bodyStart := start + len(declaration)
		bodyEnd := strings.Index(integration[bodyStart:], "\nEOF\n")
		if bodyEnd < 0 {
			t.Fatalf("installer integration test is missing the %s probe end", name)
		}
		return integration[bodyStart : bodyStart+bodyEnd]
	}
	requireMarkersInOrder := func(name string, body string, markers ...string) {
		t.Helper()
		cursor := 0
		for _, marker := range markers {
			offset := strings.Index(body[cursor:], marker)
			if offset < 0 {
				t.Fatalf("%s probe is missing ordered marker %q", name, marker)
			}
			cursor += offset + len(marker)
		}
	}
	mvPostMutation := probeBody(
		"post-mutation legacy web move",
		`cat > "${WORK_DIR}/mv-post-mutation-fail" <<EOF`,
	)
	requireMarkersInOrder(
		"post-mutation legacy web move",
		mvPostMutation,
		`"${WORK_DIR}/real-mv" "\$@"`,
		`status=\$?`,
		`mv-post-mutation.executed`,
		`exit 73`,
	)
	mvPartialDestination := probeBody(
		"partial legacy web move",
		`cat > "${WORK_DIR}/mv-partial-destination-fail" <<EOF`,
	)
	requireMarkersInOrder(
		"partial legacy web move",
		mvPartialDestination,
		`destination="\${!#}"`,
		`install -d -o root -g root -m 0755 "\${destination}"`,
		`partial-copy.txt`,
		`mv-partial-destination.executed`,
		`exit 72`,
	)
	syncFailure := probeBody(
		"activation sync failure",
		`cat > "${WORK_DIR}/sync-fail" <<EOF`,
	)
	requireMarkersInOrder(
		"activation sync failure",
		syncFailure,
		`"\$*" == "-f /usr/local/bin"`,
		`-L "${CURRENT_LINK}"`,
		`-L "${PUBLIC_BINARY}"`,
		`\$(readlink -- "${PUBLIC_BINARY}") == "${CURRENT_LINK}/bin/control-panel"`,
		`-L "${PUBLIC_WEB}"`,
		`\$(readlink -- "${PUBLIC_WEB}") ==`,
		`sync-post-activation.executed`,
		`exit 74`,
	)
	if strings.Contains(syncFailure, "sync-usr-local-bin.count") {
		t.Fatal("activation sync failure probe must gate on published links instead of call count")
	}
	webRestoreAssertion := strings.Index(
		integration,
		"sync failure rollback did not restore the legacy web directory",
	)
	webContentAssertion := strings.Index(
		integration,
		"sync failure rollback changed the legacy web directory",
	)
	webTreeAssertion := strings.Index(
		integration,
		"sync failure rollback did not restore the legacy web tree exactly",
	)
	webBackupAssertion := strings.Index(
		integration,
		"sync failure rollback left the legacy web backup behind",
	)
	activationMarkerAssertion := strings.Index(
		integration,
		`if [[ ! -f ${WORK_DIR}/sync-post-activation.executed ]]; then`,
	)
	if webRestoreAssertion < 0 || webContentAssertion < 0 || webTreeAssertion < 0 ||
		webBackupAssertion < 0 || activationMarkerAssertion < 0 ||
		!(webRestoreAssertion < webContentAssertion && webContentAssertion < webTreeAssertion &&
			webTreeAssertion < webBackupAssertion && webBackupAssertion < activationMarkerAssertion) {
		t.Fatal("activation sync rollback must restore the full legacy web tree and remove its backup before accepting the injection marker")
	}
	signalGroupadd := probeBody(
		"deferred-TERM groupadd",
		`cat > "${WORK_DIR}/signal-groupadd" <<EOF`,
	)
	for _, marker := range []string{
		`status=\$?`,
		`signal-groupadd.executed`,
		`kill -TERM "\${PPID}"`,
		`exit "\${status}"`,
	} {
		if !strings.Contains(signalGroupadd, marker) {
			t.Fatalf("deferred-TERM groupadd probe is missing marker %q", marker)
		}
	}
	if strings.Contains(signalGroupadd, "exit 73") {
		t.Fatal("deferred-TERM groupadd probe must not mix a synthetic command failure into the signal boundary")
	}
	partialSuccessGroupadd := probeBody(
		"partial-success groupadd",
		`cat > "${WORK_DIR}/partial-success-groupadd" <<EOF`,
	)
	requireMarkersInOrder(
		"partial-success groupadd",
		partialSuccessGroupadd,
		`"${WORK_DIR}/real-groupadd" "\$@"`,
		`partial-success-groupadd.executed`,
		"exit 73",
	)
	cleanupSignalGroupdel := probeBody(
		"cleanup-signal groupdel",
		`cat > "${WORK_DIR}/cleanup-signal-groupdel" <<EOF`,
	)
	requireMarkersInOrder(
		"cleanup-signal groupdel",
		cleanupSignalGroupdel,
		`cleanup-signal-groupdel.executed`,
		`kill -TERM "\${PPID}"`,
		`"${WORK_DIR}/real-groupdel" "\$@"`,
	)
	for _, marker := range []string{
		"partial-success groupadd did not exit with status 1",
		"captured installer output for partial-success groupadd",
		"captured installer output for signal-interrupted groupadd",
	} {
		if !strings.Contains(integration, marker) {
			t.Fatalf("installer integration test is missing split account rollback marker %q", marker)
		}
	}
	if strings.Contains(
		integration,
		`trap 'kill "${probe_pid}" >/dev/null 2>&1; wait "${probe_pid}" >/dev/null 2>&1' EXIT`,
	) {
		t.Fatal("PID reuse probe EXIT trap must absorb the expected SIGTERM wait status")
	}
	if !strings.Contains(
		integration,
		`7>&- > "${WORK_DIR}/shared-lock-contention.out" 2>&1`,
	) {
		t.Fatal("shared host-setup contention probe must not inherit the fixture's locked file descriptor")
	}
	if !strings.Contains(
		integration,
		"captured installer output for shared host-setup lock contention",
	) {
		t.Fatal("shared host-setup contention mismatch must expose the captured installer error")
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
		"gh attestation verify /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz",
		"--repo Kome-Lab/Autostream-ControlPanel",
		"sudo install -o root -g root -m 0644 /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz",
		"sudo tar --no-same-owner --no-same-permissions -xzf",
		"サーバーへ転送する release asset は、この `.tar.gz` 1 個だけです",
		"AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel",
		"installer-owned",
	} {
		if !strings.Contains(guide, marker) {
			t.Fatalf("install guide is missing simple installer marker %q", marker)
		}
	}
}

func TestControlPanelInstallerRollbackPreservesPreexistingAutostreamGroup(t *testing.T) {
	root := filepath.Join("..", "..")
	installerBytes, err := os.ReadFile(filepath.Join(
		root,
		"release",
		"install-autostream-control-panel",
	))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerBytes)

	for _, marker := range []string{
		`readonly AUTOSTREAM_USER_ROLLBACK_LOGIN="autostream-install-rollback"`,
		"prepare_autostream_user_rollback_login()",
		"local_account_member_fields_are_clear()",
		"local_account_database_matches_digests()",
		"restore_created_service_user_login()",
		"remove_created_service_user_preserving_group()",
		"created_service_user_record",
		"created_service_group_record",
		"preexisting_service_group_record",
		`usermod --login "${AUTOSTREAM_USER_ROLLBACK_LOGIN}" autostream`,
		`userdel "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
		`usermod --login autostream "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
		`sha256sum -- /etc/group`,
		`sha256sum -- /etc/gshadow`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing pre-existing service-group rollback guard %q", marker)
		}
	}
	for _, forbidden := range []string{
		`elif ! userdel autostream; then`,
		`userdel autostream`,
		`usermod --gid`,
		`usermod --home`,
	} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("installer rollback can mutate the protected service group or user identity through %q", forbidden)
		}
	}

	restoreStart := strings.Index(installer, "restore_created_service_user_login()")
	removeStart := strings.Index(installer, "remove_created_service_user_preserving_group()")
	rollbackStart := strings.Index(installer, "rollback_created_service_account()")
	if restoreStart < 0 || removeStart < 0 || rollbackStart < 0 ||
		restoreStart >= removeStart || removeStart >= rollbackStart {
		t.Fatal("installer service-account rollback helper boundaries are invalid")
	}
	restoreBody := installer[restoreStart:removeStart]
	restoreRename := strings.Index(
		restoreBody,
		`usermod --login autostream "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
	)
	restoreDigestCheck := strings.Index(
		restoreBody,
		"local_account_database_matches_digests",
	)
	restoreGroupCheck := strings.Index(
		restoreBody,
		`getent group autostream 2>/dev/null || true) == "${expected_group_record}"`,
	)
	if restoreRename < 0 || restoreDigestCheck < 0 || restoreGroupCheck < 0 ||
		restoreRename >= restoreDigestCheck || restoreDigestCheck >= restoreGroupCheck {
		t.Fatal("installer must restore the original login before validating the protected group databases")
	}

	removeBody := installer[removeStart:rollbackStart]
	groupSnapshot := strings.Index(removeBody, "group_database_digest_before=")
	membershipCheck := strings.Index(removeBody, "local_account_member_fields_are_clear")
	forwardRename := strings.Index(
		removeBody,
		`usermod --login "${AUTOSTREAM_USER_ROLLBACK_LOGIN}" autostream`,
	)
	postRenameDigestOffset := -1
	if forwardRename >= 0 {
		postRenameDigestOffset = strings.Index(
			removeBody[forwardRename:],
			"local_account_database_matches_digests",
		)
	}
	deleteRenamed := strings.Index(
		removeBody,
		`userdel "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
	)
	finalDigestOffset := -1
	if deleteRenamed >= 0 {
		finalDigestOffset = strings.LastIndex(
			removeBody[deleteRenamed:],
			"local_account_database_matches_digests",
		)
	}
	if groupSnapshot < 0 || membershipCheck < 0 || forwardRename < 0 ||
		postRenameDigestOffset < 0 || deleteRenamed < 0 || finalDigestOffset < 0 ||
		groupSnapshot >= membershipCheck || membershipCheck >= forwardRename ||
		forwardRename+postRenameDigestOffset >= deleteRenamed ||
		deleteRenamed >= deleteRenamed+finalDigestOffset {
		t.Fatal("installer must snapshot and validate group databases, rename the user, delete only the renamed login, then revalidate")
	}
	if count := strings.Count(
		removeBody,
		`userdel "${AUTOSTREAM_USER_ROLLBACK_LOGIN}"`,
	); count != 1 {
		t.Fatalf("installer renamed-login deletion count = %d, want 1", count)
	}

	integrationBytes, err := os.ReadFile(filepath.Join(
		root,
		"release",
		"test-install-autostream-control-panel-integration.sh",
	))
	if err != nil {
		t.Fatal(err)
	}
	integration := string(integrationBytes)
	currentLinkDefinition := strings.Index(
		integration,
		`readonly CURRENT_LINK="${MANAGED_ROOT}/current"`,
	)
	currentLinkProbe := strings.Index(
		integration,
		`cat > "${WORK_DIR}/signal-mv" <<EOF`,
	)
	if currentLinkDefinition < 0 || currentLinkProbe < 0 ||
		currentLinkDefinition >= currentLinkProbe {
		t.Fatal("integration fixture must define CURRENT_LINK before expanding the current-link fault probe")
	}
	for _, marker := range []string{
		"useradd TERM transaction exited with",
		"useradd TERM transaction did not reach its injection boundary",
		"useradd TERM transaction changed the pre-existing service group",
		"useradd TERM transaction changed the pre-existing local group databases",
		"useradd TERM transaction left the reserved rollback login",
		"useradd TERM transaction left private input staging behind",
		"report_failed_install_probe()",
		`"${WORK_DIR}/signal-useradd-rollback.out"`,
		"fixture account teardown left the service account behind",
	} {
		if !strings.Contains(integration, marker) {
			t.Fatalf("integration fixture is missing pre-existing service-group rollback marker %q", marker)
		}
	}
	if regexp.MustCompile(`userdel autostream\s*\n\s*groupdel autostream`).MatchString(integration) {
		t.Fatal("integration fixture unconditionally deletes an autostream group that userdel may already remove")
	}
}
