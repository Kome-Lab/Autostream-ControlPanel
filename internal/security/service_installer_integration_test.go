package security

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func assertControlPanelInstallerIntegration(t *testing.T, root, installer string) {
	integrationBytes, err := readInstallerScenarioSource(
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
}
