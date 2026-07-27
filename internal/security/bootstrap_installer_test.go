package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateHostInstallerCreatesOnlyPlannedSystemdPathsInsideTransaction(t *testing.T) {
	installerPath := filepath.Join("..", "..", "release", "install-autostream-update-host")
	payload, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)

	for _, marker := range []string{
		`^/opt/autostream/(control-panel|worker|encoder-recorder|discord-bot|observability)(/releases)?$`,
		`"${binary_stage}" installer-systemd-paths --config "${config_stage}"`,
		`mapfile -t systemd_bootstrap_paths`,
		`bootstrap_created_dirs+=("${current}")`,
		`rmdir -- "${bootstrap_created_dirs[bootstrap_index]}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing systemd bootstrap transaction marker %q", marker)
		}
	}

	transactionStart := strings.Index(installer, "commit_started=true")
	createLoop := -1
	if transactionStart >= 0 {
		if relative := strings.Index(installer[transactionStart:], `for bootstrap_path in "${systemd_bootstrap_paths[@]}"; do`); relative >= 0 {
			createLoop = transactionStart + relative
		}
	}
	activateKey := strings.Index(installer, `mv -f -- "${authorized_keys_stage}" "${AUTHORIZED_KEYS_DEST}"`)
	if transactionStart < 0 || createLoop < transactionStart || activateKey < createLoop {
		t.Fatalf("systemd path creation must occur inside the rollback transaction and before key activation")
	}
}

func TestBootstrapInstallerOptInInstallsFixedSSHDPolicyBeforeKeyActivation(t *testing.T) {
	installerPath := filepath.Join("..", "..", "release", "install-autostream-update-host")
	payload, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)

	for _, marker := range []string{
		`readonly SSHD_POLICY_DEST="/etc/ssh/sshd_config.d/90-autostream-update-host.conf"`,
		`readonly -a SSHD_RELOAD_UNITS=("ssh.service" "sshd.service")`,
		`--install-sshd-policy)`,
		`install_sshd_policy=false`,
		`install_sshd_policy=true`,
		`sshd_policy_stage=$(mktemp /etc/ssh/sshd_config.d/.90-autostream-update-host.conf.new.XXXXXX)`,
		`chmod 0644 "${sshd_policy_stage}"`,
		`Match User autostream-update-host`,
		`AuthenticationMethods publickey`,
		`AuthorizedKeysFile .ssh/authorized_keys`,
		`cmp -s "${sshd_policy_stage}" "${SSHD_POLICY_DEST}" || die "existing sshd policy differs; review or remove it explicitly before rerunning"`,
		`mv -f -- "${sshd_policy_stage}" "${SSHD_POLICY_DEST}"`,
		`reload_sshd || die "could not reload ssh.service or sshd.service"`,
		`require_restricted_sshd_policy`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing automatic sshd policy marker %q", marker)
		}
	}

	activatePolicy := strings.Index(installer, `mv -f -- "${sshd_policy_stage}" "${SSHD_POLICY_DEST}"`)
	if activatePolicy < 0 {
		t.Fatal("sshd policy activation is missing")
	}
	recordPolicyRollback := strings.LastIndex(
		installer[:activatePolicy],
		`sshd_policy_activated=true`,
	)
	if recordPolicyRollback < 0 {
		t.Fatal("sshd policy rollback intent must be recorded before the policy rename")
	}
	quiesceKey := strings.LastIndex(
		installer[:activatePolicy],
		`mv -f -- "${AUTHORIZED_KEYS_DEST}" "${authorized_keys_backup}"`,
	)
	if quiesceKey < 0 {
		t.Fatal("an existing updater key must be quiesced before sshd policy activation")
	}
	syntaxCheckRelative := strings.Index(installer[activatePolicy:], `sshd -t`)
	reloadRelative := strings.Index(installer[activatePolicy:], `reload_sshd || die "could not reload ssh.service or sshd.service"`)
	effectiveRelative := strings.Index(installer[activatePolicy:], `require_restricted_sshd_policy`)
	activateKeyRelative := strings.Index(installer[activatePolicy:], `mv -f -- "${authorized_keys_stage}" "${AUTHORIZED_KEYS_DEST}"`)
	if syntaxCheckRelative < 0 || reloadRelative <= syntaxCheckRelative ||
		effectiveRelative <= reloadRelative || activateKeyRelative <= effectiveRelative {
		t.Fatalf("sshd policy must be syntax-checked, reloaded, and effectively verified before key activation")
	}
}

func TestBootstrapInstallerRollsBackAutomaticSSHDPolicyBeforeRestoringAccess(t *testing.T) {
	installerPath := filepath.Join("..", "..", "release", "install-autostream-update-host")
	payload, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)

	for _, marker := range []string{
		`rollback_sshd_policy()`,
		`sshd_reload_attempted=false`,
		`sshd_reload_succeeded=false`,
		`sshd_reload_attempted=true`,
		`sshd_reload_succeeded=true`,
		`if [[ ${sshd_reload_attempted} == true && ${sshd_reload_succeeded} != true ]]; then`,
		`rm -f -- "${SSHD_POLICY_DEST}"`,
		`reload_sshd_after_rollback`,
		`if ! rollback_sshd_policy; then`,
		`CONSOLE RECOVERY REQUIRED: automatic sshd policy rollback, reload, or effective-policy verification failed`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing sshd rollback marker %q", marker)
		}
	}

	rollbackFunction := installer[strings.Index(installer, `rollback_sshd_policy()`):]
	rollbackFunction = rollbackFunction[:strings.Index(rollbackFunction, "\n}\n")]
	if strings.Contains(rollbackFunction, `[[ ${sshd_policy_activated} == true ]] || return 0`) {
		t.Fatal("an identical existing drop-in must not bypass effective-policy verification before restoring access")
	}
	if !strings.Contains(rollbackFunction, `if [[ ${authorized_keys_existed} == true ]]; then
    restricted_sshd_policy_is_effective || return 1
  fi`) {
		t.Fatal("rollback must verify the effective policy before an existing updater key can be restored")
	}

	rollback := strings.Index(installer, `if ! rollback_sshd_policy; then`)
	restoreAccess := strings.Index(installer, `mv -f -- "${authorized_keys_backup}" "${AUTHORIZED_KEYS_DEST}"`)
	if rollback < 0 || restoreAccess < 0 || rollback >= restoreAccess {
		t.Fatalf("sshd policy rollback must finish before prior SSH access is restored")
	}
}

func TestBootstrapInstallerReconfiguresOnlyRecordedManagedState(t *testing.T) {
	installerPath := filepath.Join("..", "..", "release", "install-autostream-update-host")
	payload, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)

	for _, marker := range []string{
		`readonly INSTALL_STATE_DEST="/etc/autostream/update-host.install-state"`,
		`verify_recorded_managed_state`,
		`current_config_sha256=$(sha256_file "${CONFIG_DEST}")`,
		`current_authorized_keys_sha256=$(sha256_file "${AUTHORIZED_KEYS_DEST}")`,
		`[[ ${recorded_config_sha256} == "${current_config_sha256}" ]]`,
		`[[ ${recorded_authorized_keys_sha256} == "${current_authorized_keys_sha256}" ]]`,
		`preflight_destination "${INSTALL_STATE_DEST}" 600 'install state'`,
		`[[ -f ${path} && ! -L ${path} ]] || die "existing ${label} is not a regular non-symlink file"`,
		`[[ $(stat -c '%U:%G:%a' "${path}") == "root:root:${expected_mode}" ]]`,
		`[[ ${#state_lines[@]} -eq 3 ]]`,
		`[[ ${state_lines[0]} == "schema_version=1" ]]`,
		`existing install state has invalid or unknown fields`,
		`existing install state config digest is invalid`,
		`existing install state authorized_keys digest is invalid`,
		`existing config changed outside the installer; refusing automatic reconfiguration`,
		`existing authorized_keys changed outside the installer; refusing automatic reconfiguration`,
		`install -o root -g root -m 0600 "${CONFIG_DEST}" "${config_backup}"`,
		`install -o root -g root -m 0600 "${INSTALL_STATE_DEST}" "${install_state_backup}"`,
		`restore_destination "${CONFIG_DEST}" "${config_backup}" "${config_existed}"`,
		`restore_destination "${INSTALL_STATE_DEST}" "${install_state_backup}" "${install_state_existed}"`,
		`mv -f -- "${install_state_stage}" "${INSTALL_STATE_DEST}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing managed reconfiguration marker %q", marker)
		}
	}
	for _, obsolete := range []string{
		`existing config differs; review and update it separately before rerunning`,
		`existing authorized key or source CIDR differs; rotate it through an explicit maintenance procedure`,
	} {
		if strings.Contains(installer, obsolete) {
			t.Fatalf("installer still refuses a recorded managed change with %q", obsolete)
		}
	}

	verifyState := strings.Index(installer, `  verify_recorded_managed_state`)
	quiesceKey := strings.LastIndex(installer, `mv -f -- "${AUTHORIZED_KEYS_DEST}" "${authorized_keys_backup}"`)
	activateState := strings.LastIndex(installer, `mv -f -- "${install_state_stage}" "${INSTALL_STATE_DEST}"`)
	activateKey := strings.LastIndex(installer, `mv -f -- "${authorized_keys_stage}" "${AUTHORIZED_KEYS_DEST}"`)
	if verifyState < 0 || quiesceKey <= verifyState || activateState <= quiesceKey || activateKey <= activateState {
		t.Fatal("recorded state must be verified before quiescing access, and the new receipt must be committed before the new key")
	}

	cleanupStart := strings.Index(installer, "cleanup() {")
	cleanupEnd := -1
	if cleanupStart >= 0 {
		cleanupEnd = strings.Index(installer[cleanupStart:], "\n}\ntrap cleanup EXIT")
	}
	if cleanupEnd < 0 {
		t.Fatal("installer cleanup transaction is missing")
	}
	cleanup := installer[cleanupStart : cleanupStart+cleanupEnd]
	restoreConfig := strings.Index(cleanup, `restore_destination "${CONFIG_DEST}" "${config_backup}" "${config_existed}"`)
	restoreState := strings.Index(cleanup, `restore_destination "${INSTALL_STATE_DEST}" "${install_state_backup}" "${install_state_existed}"`)
	restorePolicy := strings.Index(cleanup, `if ! rollback_sshd_policy; then`)
	restoreKey := strings.Index(cleanup, `mv -f -- "${authorized_keys_backup}" "${AUTHORIZED_KEYS_DEST}"`)
	if restoreConfig < 0 || restoreState <= restoreConfig || restorePolicy <= restoreState || restoreKey <= restorePolicy {
		t.Fatal("rollback must restore config and receipt, verify the sshd policy, and only then restore the old key")
	}
}

func TestBootstrapInstallerLegacyAdoptionRequiresExactGeneratedPair(t *testing.T) {
	installerPath := filepath.Join("..", "..", "release", "install-autostream-update-host")
	payload, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)

	for _, marker := range []string{
		`validate_legacy_managed_install`,
		`legacy managed install is incomplete; config and authorized_keys must either both exist or both be absent`,
		`"${binary_stage}" validate-config --config "${CONFIG_DEST}"`,
		`"${binary_stage}" installer-standard-systemd-config --config "${CONFIG_DEST}"`,
		`validate_managed_authorized_keys "${AUTHORIZED_KEYS_DEST}"`,
		`local expected_prefix='restrict,from="'`,
		`local expected_suffix='",command="'"${FORCED_COMMAND}"'" ssh-ed25519 '`,
		`legacy authorized_keys is not an exact installer-generated forced entry`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing fail-closed legacy adoption marker %q", marker)
		}
	}
}
