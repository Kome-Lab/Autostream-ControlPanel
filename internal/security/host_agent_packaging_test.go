package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostAgentIdentityExampleContainsOnlyDurableIdentity(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "release", "autostream-host-agent.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode host agent identity example: %v", err)
	}
	want := map[string]bool{
		"panel_url":     true,
		"node_id":       true,
		"runtime_token": true,
		"service_name":  true,
	}
	if len(fields) != len(want) {
		t.Fatalf("host agent identity fields = %v, want exactly %v", fields, want)
	}
	for name := range fields {
		if !want[name] {
			t.Fatalf("host agent identity example contains policy/runtime field %q", name)
		}
	}
}

func TestHostAgentInstallerExposesManagedUpgradeMode(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)
	for _, marker := range []string{
		`install-autostream-host-agent --upgrade`,
		`--upgrade)`,
		`install_mode="upgrade"`,
		`--prepare, --config, and --upgrade are mutually exclusive`,
		`--prepare, --config PATH, or --upgrade is required`,
		`manual-upgrade-host-runtime`,
		`--artifact-root "${EXTRACTED_ROOT}"`,
		`--archive-sha256 "${ARTIFACT_SHA256}"`,
		`--archive-size "${ARTIFACT_SIZE}"`,
		`manual_upgrade_candidate_is_child_job`,
		`record_manual_upgrade_signal`,
		`forward_manual_upgrade_signal "${pending_signal}"`,
		`trap '' INT TERM`,
		`exit "${manual_upgrade_status}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("Host Agent installer upgrade CLI is missing %q", marker)
		}
	}
	if strings.Count(installer, "manual_upgrade_candidate_is_child_job") < 2 {
		t.Fatal("Host Agent installer must guard candidate signal forwarding with the child job identity")
	}
}

func TestHostAgentSystemdUnitIsNonRootPortlessAndSandboxed(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "systemd", "autostream-host-agent.service.example"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(payload)
	for _, marker := range []string{
		"User=autostream-host-agent",
		"Group=autostream-host-agent",
		"ConditionPathExists=|/etc/autostream-host-agent/identity.json",
		"ConditionPathExists=|/etc/autostream/host-agent.json",
		"ExecStart=/usr/local/bin/autostream-host-agent run --config /etc/autostream-host-agent/identity.json",
		"NoNewPrivileges=true",
		"PrivateTmp=true",
		"PrivateDevices=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"ProtectKernelTunables=true",
		"ProtectKernelModules=true",
		"ProtectControlGroups=true",
		"RestrictSUIDSGID=true",
		"LockPersonality=true",
		"MemoryDenyWriteExecute=true",
		"CapabilityBoundingSet=",
		"AmbientCapabilities=",
		"SocketBindDeny=any",
		"ReadOnlyPaths=-/etc/autostream-host-agent",
		"ReadOnlyPaths=-/etc/autostream/host-agent.json",
		"ReadWritePaths=/var/lib/autostream-host-agent",
	} {
		if !strings.Contains(unit, marker) {
			t.Fatalf("host agent unit is missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"User=root",
		"8090",
		"ListenStream",
		"Environment=AUTOSTREAM",
		"EnvironmentFile=",
	} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("host agent unit contains forbidden listener/secret marker %q", forbidden)
		}
	}
}

func TestHostSelfUpdateRecoveryUnitUsesSupportedBootstrapConditions(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", "systemd", "autostream-host-self-update-recovery@.service.example",
	))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(payload)
	const stateCondition = "ConditionPathExists=/var/lib/autostream-local-executor/host-self-update/state.json"
	if !strings.Contains(unit, stateCondition) {
		t.Fatalf("Host self-update recovery unit is missing bootstrap state condition %q", stateCondition)
	}
	const condition = "ConditionFileIsExecutable=/opt/autostream/host-agent/slots/%i/bin/autostream-local-executor"
	if !strings.Contains(unit, condition) {
		t.Fatalf("Host self-update recovery unit is missing supported executable condition %q", condition)
	}
	if strings.Contains(unit, "ConditionPathIsExecutable=") {
		t.Fatal("Host self-update recovery unit contains unsupported ConditionPathIsExecutable directive")
	}
}

func TestHostAgentInstallersPreserveIdentityBoundary(t *testing.T) {
	installerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	uninstallerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "uninstall-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerPayload)
	uninstaller := string(uninstallerPayload)
	for _, marker := range []string{
		`install-autostream-host-agent --prepare`,
		`CONFIG_DIR="/etc/autostream-host-agent"`,
		`CONFIG_DEST="${CONFIG_DIR}/identity.json"`,
		`LEGACY_CONFIG_DEST="/etc/autostream/host-agent.json"`,
		`BINARY_DEST="/usr/local/bin/autostream-host-agent"`,
		`UNIT_DEST="/etc/systemd/system/autostream-host-agent.service"`,
		`STATE_DIR="/var/lib/autostream-host-agent"`,
		`LOCAL_EXECUTOR_CONFIG_DIR="/etc/autostream-local-executor"`,
		`LOCAL_EXECUTOR_POLICY_DEST="${LOCAL_EXECUTOR_CONFIG_DIR}/policy.json"`,
		`LOCAL_EXECUTOR_DATA_DIR="/opt/autostream/local-executor"`,
		`LOCAL_EXECUTOR_PORT_CONFIG_DIR="${LOCAL_EXECUTOR_DATA_DIR}/ports"`,
		`LOCAL_EXECUTOR_BINARY_DEST="/usr/local/libexec/autostream-local-executor"`,
		`LOCAL_EXECUTOR_SERVICE_DEST="/etc/systemd/system/autostream-local-executor.service"`,
		`LOCAL_EXECUTOR_SOCKET_DEST="/etc/systemd/system/autostream-local-executor.socket"`,
		`LOCAL_EXECUTOR_TMPFILES_DEST="/etc/tmpfiles.d/autostream-local-executor.conf"`,
		`install -o root -g "${AGENT_GROUP}" -m 0640`,
		`validate-config --config "${config_stage}"`,
		`systemctl enable --now autostream-host-agent.service`,
		`same-release local executor preparation asset is missing or unsafe`,
		`copy_verified_release_file "${BINARY_SOURCE}" "${binary_stage}" root root 0755 "Host Agent binary"`,
		`"${LOCAL_EXECUTOR_BINARY_SOURCE}" "${local_executor_binary_stage}" root root 0755 "local executor binary"`,
		`release source identity changed while it was copied`,
		`validate_root_parent_chain "${source}" "release source"`,
		`release asset ${source} must be owned by root:root`,
		`release asset ${source} must not be writable by group or other`,
		`readonly ARTIFACT_MANIFEST_NAME="artifact-manifest.json"`,
		`readonly MAX_ARCHIVE_SIZE=268435456`,
		`readonly ARCHIVE_SOURCE="${ARTIFACT_PARENT}/${ARCHIVE_NAME}"`,
		`$(stat -c '%U:%G:%a' -- "${BUNDLE_STAGE}") == "root:root:700"`,
		`copy_stable_bundle_archive`,
		`awk '{ sub(/\/$/, ""); print }' "${BUNDLE_STAGE}/archive.list"`,
		`die "bundle archive contains duplicate paths"`,
		`${entry} != *\\*`,
		`${entry} != *"/./"*`,
		`${entry} != *"//"*`,
		`verify_release_checksum_inventory "${EXTRACTED_ROOT}"`,
		`(.component == "host-agent")`,
		`(.minimum_agent_version == null)`,
		`(.minimum_panel_version == $version)`,
		`(.rollback_compatible == true)`,
		`(.database_schema == "none")`,
		`verify_binary_identity "${BINARY_SOURCE}" "autostream-host-agent"`,
		`verify_binary_identity "${LOCAL_EXECUTOR_BINARY_SOURCE}" "autostream-local-executor"`,
		`for command in awk basename chmod chown dd dirname find flock getent groupadd groupdel id install jq ln mkdir mktemp mv readlink rm rmdir runuser sha256sum sort stat sync systemctl tar test tr uname uniq useradd userdel usermod; do`,
		`HOST_RUNTIME_ROOT="/opt/autostream/host-agent"`,
		`HOST_RUNTIME_CURRENT="${HOST_RUNTIME_ROOT}/current"`,
		`create_symlink_with_journal`,
		`"slots/a" "${HOST_RUNTIME_CURRENT}"`,
		`"${HOST_RUNTIME_CURRENT}/bin/autostream-host-agent" "${BINARY_DEST}"`,
		`"${HOST_RUNTIME_CURRENT}/bin/autostream-local-executor"`,
		`config source must be root:root 0600`,
		`validate_root_parent_chain "${source}" "config source"`,
		`source_identity=$(stat -c '%d:%i:%s:%Y:%f:%u:%g' -- "${source}")`,
		`config source identity changed while it was copied`,
		`staged identity does not match the verified config source`,
		"agent group must not grant identity access to another user",
		"agent group must not be the primary group of another user",
		`validate_private_root_directory "${LOCAL_EXECUTOR_CONFIG_DIR}"`,
		`create_private_root_directory`,
		`rollback_prepared_private_directory`,
		`local_executor_port_config_dir_created`,
		`"${CONFIG_DIR}" root "${AGENT_GROUP}" 0750 \`,
		`die "${path} must be root:${AGENT_GROUP} 0750"`,
		`both current and legacy Host Agent identities exist; refusing an ambiguous migration`,
		`retire_legacy_identity`,
		`sync -f "${CONFIG_DIR}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("host agent installer is missing %q", marker)
		}
	}
	if strings.Contains(installer, `(.database_schema == "none")))`) {
		t.Fatal("host agent artifact-manifest jq filter has an extra closing parenthesis")
	}
	if strings.Contains(installer, "runtime_token=") || strings.Contains(installer, "--runtime-token") {
		t.Fatal("host agent installer must not copy the runtime token into argv or environment")
	}
	for _, forbidden := range []string{".artifact-sha256", "slot-binding"} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("host agent bootstrap must not write partial self-update binding marker %q", forbidden)
		}
	}
	bundleVerify := strings.Index(
		installer,
		`verify_binary_identity "${LOCAL_EXECUTOR_BINARY_SOURCE}" "autostream-local-executor"`,
	)
	accountMutation := strings.Index(installer, `groupadd --system "${AGENT_GROUP}"`)
	if bundleVerify < 0 || accountMutation < 0 || bundleVerify >= accountMutation {
		t.Fatal("Host Agent bundle and both binary identities must be verified before account mutation")
	}
	preflight := strings.Index(installer, `if validate_existing "${CONFIG_DEST}"`)
	commit := strings.LastIndex(installer, "commit_started=true")
	if preflight < 0 || commit < 0 || preflight >= commit {
		t.Fatal("host agent installer must preflight every existing destination before the rollback transaction starts")
	}
	for _, rollbackMarker := range []string{
		"was_enabled=false",
		"service_quiesce_attempted=false",
		`systemctl disable "${UNIT_NAME}"`,
		`if [[ ${service_quiesce_attempted} == true && ${was_active} == true ]]; then`,
	} {
		if !strings.Contains(installer, rollbackMarker) {
			t.Fatalf("host agent installer is missing rollback marker %q", rollbackMarker)
		}
	}
	for _, marker := range []string{
		`systemctl disable --now autostream-host-agent.service`,
		`CONFIG_DIR="/etc/autostream-host-agent"`,
		`CONFIG_DEST="${CONFIG_DIR}/identity.json"`,
		`WIPING_CONFIG_DEST="${CONFIG_DIR}/.identity.staged.wipe"`,
		`LEGACY_CONFIG_DEST="/etc/autostream/host-agent.json"`,
		`STATE_DIR="/var/lib/autostream-host-agent"`,
		`--purge`,
		"preserved",
		`! -name .identity.staged.wipe -print -quit`,
		`wipe_and_unlink_identity "${WIPING_CONFIG_DEST}" "${wiping_config_snapshot}" "quarantined staged identity config" true`,
	} {
		if !strings.Contains(uninstaller, marker) {
			t.Fatalf("host agent uninstaller is missing %q", marker)
		}
	}
}

func TestHostAgentPrepareModeFailsClosedWithoutIdentity(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)
	for _, marker := range []string{
		`! -e ${LEGACY_CONFIG_DEST} && ! -L ${LEGACY_CONFIG_DEST}`,
		`--prepare requires the Host Agent service to be inactive`,
		`--prepare requires the Host Agent service to be disabled`,
		`--prepare requires the local executor and self-update recovery timers to be inactive`,
		`--prepare must not create an identity configuration`,
		`--prepare must not create a local executor policy`,
		`"${LOCAL_EXECUTOR_CONFIG_DIR}"`,
		`"${LOCAL_EXECUTOR_PORT_CONFIG_DIR}"`,
		`die "${path} must be root:root 0700"`,
		`systemctl enable --now autostream-host-agent.service`,
		`Host Agent and local executor runtime prepared but disabled; root A/B recovery timers are active.`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("host agent prepare mode is missing fail-closed marker %q", marker)
		}
	}
}

func TestHostAgentInstallerRollsBackFreshBootstrapState(t *testing.T) {
	installerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerPayload)
	earlyTrap := strings.Index(installer, "arm_cleanup_handler cleanup_bootstrap")
	accountMutation := strings.Index(installer, `groupadd --system "${AGENT_GROUP}"`)
	if earlyTrap < 0 || accountMutation < 0 || earlyTrap >= accountMutation {
		t.Fatal("Host Agent bootstrap rollback trap must be armed before account mutation")
	}
	for _, marker := range []string{
		"agent_group_created=false",
		"agent_user_created=false",
		"state_dir_created=false",
		`rollback_created_directory "${STATE_DIR}" "${state_dir_identity}" "Host Agent state directory"`,
		"rollback_created_account",
		"self_update_recovery_timer_a_enable_attempted=true",
		"self_update_recovery_timer_b_enable_attempted=true",
		`systemctl stop "${SELF_UPDATE_RECOVERY_SERVICE_A}"`,
		`systemctl stop "${SELF_UPDATE_RECOVERY_SERVICE_B}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("Host Agent installer is missing bootstrap rollback marker %q", marker)
		}
	}

	smokePayload, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "security", "testdata",
		"run-host-agent-installer-prepare-smoke.sh",
	))
	if err != nil {
		t.Fatal(err)
	}
	smoke := string(smokePayload)
	hostileGIDStart := strings.Index(smoke, "hostile_gid_group_database_before=")
	if hostileGIDStart < 0 {
		t.Fatal("Host Agent root smoke is missing the hostile GID 0 database snapshot")
	}
	hostileGIDEndOffset := strings.Index(smoke[hostileGIDStart:], "\ntouch \\")
	if hostileGIDEndOffset < 0 {
		t.Fatal("Host Agent root smoke is missing the hostile GID 0 fixture boundary")
	}
	hostileGIDBody := smoke[hostileGIDStart : hostileGIDStart+hostileGIDEndOffset]
	hostileGIDCursor := 0
	for _, marker := range []string{
		"hostile_gid_group_database_before=",
		"hostile_gid_gshadow_database_before=",
		"groupadd --system --non-unique --gid 0 autostream-host-agent",
		"groupdel --force autostream-host-agent",
		"hostile GID 0 fixture cleanup left the Host Agent group behind",
		"hostile GID 0 fixture cleanup changed the local group databases",
	} {
		markerOffset := strings.Index(hostileGIDBody[hostileGIDCursor:], marker)
		if markerOffset < 0 {
			t.Fatalf("Host Agent hostile GID 0 fixture is missing safe cleanup marker %q", marker)
		}
		hostileGIDCursor += markerOffset + len(marker)
	}
	for _, marker := range []string{
		"late destination preflight failure left a fresh Host Agent account or group",
		"late destination preflight failure left fresh Host Agent directories",
		"failed prepare changed the existing Host Agent state directory",
		"timer enable failure left a recovery timer enabled or active",
	} {
		if !strings.Contains(smoke, marker) {
			t.Fatalf("Host Agent root smoke is missing rollback fixture %q", marker)
		}
	}
}

func TestHostAgentInstallerPreservesPreexistingGroupDuringCreatedUserRollback(t *testing.T) {
	installerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerPayload)
	for _, marker := range []string{
		`AGENT_USER_ROLLBACK_LOGIN=`,
		"preexisting_agent_group_identity=",
		"agent_account_databases_match_digests",
		`usermod --login "${AGENT_USER_ROLLBACK_LOGIN}" "${AGENT_USER}"`,
		`userdel "${AGENT_USER_ROLLBACK_LOGIN}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("Host Agent account rollback is missing group-preservation marker %q", marker)
		}
	}
	renameUser := strings.Index(installer, `usermod --login "${AGENT_USER_ROLLBACK_LOGIN}" "${AGENT_USER}"`)
	deleteUser := strings.Index(installer, `userdel "${AGENT_USER_ROLLBACK_LOGIN}"`)
	deleteGroup := strings.Index(installer, `groupdel "${AGENT_GROUP}"`)
	if renameUser < 0 || deleteUser < 0 || deleteGroup < 0 || renameUser >= deleteUser || deleteUser >= deleteGroup {
		t.Fatal("Host Agent rollback must rename the created login before deleting it and delete only its created group afterwards")
	}

	smokePayload, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "security", "testdata",
		"run-host-agent-installer-prepare-smoke.sh",
	))
	if err != nil {
		t.Fatal(err)
	}
	smoke := string(smokePayload)
	for _, marker := range []string{
		"artifact-manifest.json does not authorize this exact Host Agent bundle",
		"bundle archive contains duplicate paths",
		"release source parents must not be writable by group or other",
		"preexisting_group_record_before=",
		"pre-existing Host Agent group changed during rollback",
		"if getent group autostream-host-agent >/dev/null 2>&1; then",
	} {
		if !strings.Contains(smoke, marker) {
			t.Fatalf("Host Agent root smoke is missing account rollback guard %q", marker)
		}
	}
}

func TestHostAgentInstallerJournalsSignalSensitiveMutations(t *testing.T) {
	installerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerPayload)
	for _, marker := range []string{
		"pending_cleanup_signal=",
		"defer_cleanup_signals",
		"resume_cleanup_signals",
		`trap - EXIT`,
		`trap '' INT TERM`,
		"move_with_journal",
		"create_symlink_with_journal",
		"agent_group_creation_attempted=true",
		"agent_user_creation_attempted=true",
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("Host Agent installer is missing signal-safe mutation marker %q", marker)
		}
	}
	if strings.Contains(installer, "trap - EXIT INT TERM") {
		t.Fatal("Host Agent cleanup must ignore a second INT/TERM instead of restoring their default actions")
	}

	createStart := strings.Index(installer, "create_managed_directory()")
	createEnd := strings.Index(installer[createStart:], "\n}")
	if createStart < 0 || createEnd < 0 {
		t.Fatal("Host Agent managed-directory helper is missing")
	}
	createBody := installer[createStart : createStart+createEnd]
	deferIndex := strings.Index(createBody, "defer_cleanup_signals")
	mkdirIndex := strings.Index(createBody, `mkdir -- "${path}"`)
	journalIndex := strings.Index(createBody, `printf -v "${identity_variable}"`)
	resumeIndex := strings.Index(createBody, "resume_cleanup_signals")
	if deferIndex < 0 || mkdirIndex < 0 || journalIndex < 0 || resumeIndex < 0 ||
		!(deferIndex < mkdirIndex && mkdirIndex < journalIndex && journalIndex < resumeIndex) {
		t.Fatal("Host Agent directory creation must defer cleanup signals until its rollback identity is journaled")
	}
}

func TestHostAgentReleaseSmokePurgesNonEmptyAndEmptyWipeTombstones(t *testing.T) {
	smokePayload, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "security", "testdata",
		"run-host-agent-installer-prepare-smoke.sh",
	))
	if err != nil {
		t.Fatal(err)
	}
	smoke := string(smokePayload)
	for _, marker := range []string{
		`/etc/autostream-host-agent/identity.json \`,
		`/etc/autostream-host-agent/.identity.staged.wipe`,
		`install -o root -g autostream-host-agent -m 0640 /dev/null \`,
		`test "$(stat -c '%s:%U:%G:%a' \`,
		`"0:root:autostream-host-agent:640"`,
		`prepare mode accepted a self-consistent bundle with invalid artifact metadata`,
		`prepare mode accepted an archive with a duplicate canonical path`,
		`duplicate archive path mutated the Host Agent account`,
	} {
		if !strings.Contains(smoke, marker) {
			t.Fatalf("Host Agent root smoke is missing wipe tombstone marker %q", marker)
		}
	}
	if strings.Count(smoke, `"${PACKAGE_ROOT}/install/uninstall-autostream-host-agent" --purge`) < 2 {
		t.Fatal("Host Agent root smoke must execute separate non-empty and zero-byte tombstone purge cycles")
	}

	workflowPayload, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-host.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflowPayload), "run-host-agent-installer-prepare-smoke.sh") {
		t.Fatal("Host Release must execute the Host Agent installer/uninstaller root smoke")
	}
}

func TestHostReleaseAddsAttestedHostAgentAssetsWithoutRemovingLegacyAssets(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-host.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)
	for _, marker := range []string{
		`autostream-control-panel_${RELEASE_VERSION}_linux_amd64.tar.gz`,
		`autostream-update-host_${RELEASE_VERSION}_linux_amd64.tar.gz`,
		`host_agent_artifact="autostream-host-agent_${version}_linux_${arch}"`,
		`./cmd/autostream-host-agent`,
		`host-agent-manifest.json`,
		`channel: "host-agent"`,
		`- name: Attest host agent manifest`,
		`subject-path: artifacts/host-agent-manifest.json`,
		`- name: Attest Host Agent archives`,
		`artifacts/autostream-host-agent_${{ needs.release-host.outputs.version }}_linux_amd64.tar.gz`,
		`artifacts/autostream-host-agent_${{ needs.release-host.outputs.version }}_linux_arm64.tar.gz`,
		`if [[ "${#assets[@]}" -ne 18 ]]; then`,
		`(length == 18)`,
		`(.assets | length == 18)`,
		`-e "s/vX\\.Y\\.Z/${version}/g" \`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("host release workflow is missing %q", marker)
		}
	}
}

func TestHostUpgradePrivilegedLockInteropRunsInRootCI(t *testing.T) {
	workflows := []struct {
		name string
		step string
	}{
		{
			name: "ci.yml",
			step: "Test managed Host config and upgrade locks with root-owned Linux fixtures",
		},
		{
			name: "release-host.yml",
			step: "Test updater config and Host upgrade locks with root-owned Linux fixtures",
		},
	}
	for _, workflow := range workflows {
		t.Run(workflow.name, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join(
				"..", "..", ".github", "workflows", workflow.name,
			))
			if err != nil {
				t.Fatal(err)
			}
			text := string(payload)
			start := strings.Index(text, "      - name: "+workflow.step)
			if start < 0 {
				t.Fatalf("root-owned Host upgrade lock step is missing")
			}
			step := text[start:]
			if end := strings.Index(step[1:], "\n      - name:"); end >= 0 {
				step = step[:end+1]
			}
			for _, marker := range []string{
				"getent group autostream-host-agent >/dev/null 2>&1 || sudo groupadd --system autostream-host-agent",
				"sudo env",
				"TestManualHostUpgradeLocksFenceLegacyUpdateHostInstaller",
				"TestAcquireManualHostUpgradeTargetLocksInteroperatesWithLegacyTargetLock",
				"TestHostRuntimeSetupAndLifecycleLocksUsePermanentStrongInodes",
				`-json | tee "${result}"`,
				`expected_root_mode_skip="TestPreparedUpdaterConfigInitializationRejectsNonRootBeforeFilesystemMutation"`,
				`((.Test // "") | startswith("TestPreparedUpdaterConfig"))`,
				`.Test != $expected_root_mode_skip`,
				`.Test == $expected_root_mode_skip`,
				"An unexpected root-owned updater test was skipped",
				"Expected root-mode updater skip did not report",
				"Root-owned updater test did not report pass",
			} {
				if !strings.Contains(step, marker) {
					t.Fatalf("root-owned Host upgrade lock step is missing %q", marker)
				}
			}
			if count := strings.Count(
				step,
				`--arg expected_root_mode_skip "${expected_root_mode_skip}"`,
			); count != 2 {
				t.Fatalf("root-owned Host upgrade lock step has %d expected-skip jq arguments, want 2", count)
			}
		})
	}
}

func TestHostInstallerSmokesRunOfflineInPullRequestAndReleaseCI(t *testing.T) {
	const pinnedUbuntu = "ubuntu@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90"
	smokes := []string{
		"run-host-agent-installer-prepare-smoke.sh",
		"run-host-agent-installer-upgrade-smoke.sh",
	}
	for _, workflowName := range []string{"ci.yml", "release-host.yml"} {
		t.Run(workflowName, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join(
				"..", "..", ".github", "workflows", workflowName,
			))
			if err != nil {
				t.Fatal(err)
			}
			workflow := string(payload)
			for _, smoke := range smokes {
				marker := "/bin/bash /workspace/internal/security/testdata/" + smoke
				markerIndex := strings.Index(workflow, marker)
				if markerIndex < 0 {
					t.Fatalf("%s no longer executes %s", workflowName, smoke)
				}
				stepStart := strings.LastIndex(workflow[:markerIndex], "\n      - name:")
				if stepStart < 0 {
					t.Fatalf("%s smoke %s is not in a named workflow step", workflowName, smoke)
				}
				step := workflow[stepStart:]
				if stepEnd := strings.Index(step[1:], "\n      - name:"); stepEnd >= 0 {
					step = step[:stepEnd+1]
				}
				for _, required := range []string{
					"docker run --rm",
					"--user 0:0",
					"--network none",
					pinnedUbuntu,
					marker,
				} {
					if !strings.Contains(step, required) {
						t.Fatalf("%s smoke %s is missing %q", workflowName, smoke, required)
					}
				}
			}
		})
	}

	ciPayload, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ci := string(ciPayload)
	for _, smoke := range smokes {
		marker := "bash -n internal/security/testdata/" + smoke
		if !strings.Contains(ci, marker) {
			t.Fatalf("pull-request CI no longer syntax-checks %s", smoke)
		}
	}
}

func TestManualHostUpgradeGuidesUsePackagedReleaseRoot(t *testing.T) {
	const (
		archive = "autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz"
		root    = "/opt/autostream/releases/artifacts/autostream-host-agent_vX.Y.Z_linux_amd64"
	)
	flow := strings.Join([]string{
		"# Keep ../" + archive + " unchanged and adjacent.",
		"cd " + root,
		"sudo ./install/install-autostream-host-agent --upgrade",
	}, "\n")
	for _, guideName := range []string{"README.install.md", "README.local-executor.md"} {
		t.Run(guideName, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("..", "..", "release", guideName))
			if err != nil {
				t.Fatal(err)
			}
			guide := string(payload)
			if !strings.Contains(guide, flow) {
				t.Fatalf("%s must keep the matching archive adjacent, enter the new concrete release root, and then run --upgrade", guideName)
			}
			if !strings.Contains(guide, "`README.md`") {
				t.Fatalf("%s must reference the archive-contained README.md", guideName)
			}
			if strings.Contains(guide, "`README.host-agent.md`") {
				t.Fatalf("%s references a source filename that is not packaged", guideName)
			}
			for _, dependency := range []string{"`flock`", "`util-linux`"} {
				if !strings.Contains(guide, dependency) {
					t.Fatalf("%s no longer documents Host installer dependency %s", guideName, dependency)
				}
			}
		})
	}

	workflowPayload, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-host.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowPayload)
	for _, marker := range []string{
		`host_agent_artifact="autostream-host-agent_${version}_linux_${arch}"`,
		`cp release/README.host-agent.md "${host_agent_root}/README.md"`,
		`cp release/README.local-executor.md "${host_agent_root}/README.local-executor.md"`,
		`-e "s/vX\\.Y\\.Z/${version}/g" \`,
		`-e "s/linux_amd64/linux_${arch}/g" \`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("Host archive packaging no longer matches the documented upgrade flow: missing %q", marker)
		}
	}

	hostGuidePayload, err := os.ReadFile(filepath.Join("..", "..", "release", "README.host-agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	hostGuide := string(hostGuidePayload)
	for _, dependency := range []string{"`flock`", "`util-linux`"} {
		if !strings.Contains(hostGuide, dependency) {
			t.Fatalf("Host Agent guide no longer documents installer dependency %s", dependency)
		}
	}
}
