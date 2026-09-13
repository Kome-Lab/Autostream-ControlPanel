package security

import (
	"strings"
	"testing"
)

func assertControlPanelInstallerTransactions(t *testing.T, installer string) {
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
}
