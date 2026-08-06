package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/updateagent"
)

func TestLocalExecutorPolicyExampleIsValidMutationPolicy(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "release", "autostream-local-executor-policy.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	var policy updateagent.LocalExecutorPolicy
	if err := json.Unmarshal(payload, &policy); err != nil {
		t.Fatalf("decode local executor policy example: %v", err)
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("validate local executor policy example: %v", err)
	}
	if policy.SourcePolicyRevision < 1 || policy.ProjectionRevision < 1 ||
		policy.PolicyRevision < 1 || len(policy.Targets) == 0 ||
		policy.Targets[0].EndpointRevision < 1 ||
		policy.Targets[0].ConfigRevision < 1 ||
		policy.Targets[0].ConfigSHA256 == "" {
		t.Fatal("local executor policy example is not ready for a revision-bound port mutation")
	}
	if policy.SocketPath != updateagent.LocalExecutorSocketPath {
		t.Fatalf("socket_path=%q", policy.SocketPath)
	}
	for _, forbidden := range []string{"command", "operation", "health_url", "version_url", "runtime_token", "github_token"} {
		if strings.Contains(string(payload), `"`+forbidden+`"`) {
			t.Fatalf("policy example contains IPC/mutable field %q", forbidden)
		}
	}
}

func TestRuntimeInstallersSharePermanentHostSetupLock(t *testing.T) {
	hostPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-host-agent"))
	if err != nil {
		t.Fatal(err)
	}
	localPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-local-executor"))
	if err != nil {
		t.Fatal(err)
	}
	hostInstaller := string(hostPayload)
	localInstaller := string(localPayload)
	for name, installer := range map[string]string{
		"Host Agent":     hostInstaller,
		"Local Executor": localInstaller,
	} {
		for _, marker := range []string{
			`.autostream-runtime-host-setup.lock`,
			`.autostream-host-lifecycle.lock`,
			`acquire_shared_host_setup_lock`,
			`acquire_host_lifecycle_lock`,
			`flock -n 8 || die "another AutoStream installer is provisioning shared host state"`,
			`flock -n 9 || die "another privileged Host lifecycle operation is active"`,
			`/proc/self/fd/8`,
			`/proc/self/fd/9`,
			`"root:root:700"`,
			`"root:root:600:1"`,
		} {
			if !strings.Contains(installer, marker) {
				t.Fatalf("%s installer shared setup lock is missing %q", name, marker)
			}
		}
		commandLineStart := strings.Index(installer, "for command in ")
		if commandLineStart < 0 {
			t.Fatalf("%s installer required-command loop is missing", name)
		}
		commandLineEnd := strings.Index(installer[commandLineStart:], "; do")
		if commandLineEnd < 0 ||
			!strings.Contains(installer[commandLineStart:commandLineStart+commandLineEnd], " flock ") {
			t.Fatalf("%s installer does not require flock", name)
		}
	}

	hostUpgrade := strings.Index(hostInstaller, `if [[ ${install_mode} == "upgrade" ]]`)
	hostSetupLock := strings.LastIndex(hostInstaller, "acquire_shared_host_setup_lock")
	hostLifecycleLock := strings.LastIndex(hostInstaller, "acquire_host_lifecycle_lock")
	hostPersistentMutation := strings.Index(hostInstaller, "\nprepare_agent_user_rollback_login ||")
	if hostUpgrade < 0 || hostSetupLock < 0 || hostLifecycleLock < 0 ||
		hostPersistentMutation < 0 || hostUpgrade >= hostSetupLock ||
		hostSetupLock >= hostLifecycleLock || hostLifecycleLock >= hostPersistentMutation {
		t.Fatal("Host Agent installer must bypass shell locks for --upgrade and acquire setup then lifecycle before prepare/config mutations")
	}
	localSetupLock := strings.LastIndex(localInstaller, "acquire_shared_host_setup_lock")
	localLifecycleLock := strings.LastIndex(localInstaller, "acquire_host_lifecycle_lock")
	localAgentIdentity := strings.LastIndex(localInstaller, `agent_uid=$(id -u "${AGENT_USER}")`)
	localPersistentMutation := strings.Index(localInstaller, `ensure_root_directory "${CONFIG_DIR}" 0700`)
	if localSetupLock < 0 || localLifecycleLock < 0 || localAgentIdentity < 0 ||
		localPersistentMutation < 0 || localSetupLock >= localLifecycleLock ||
		localLifecycleLock >= localAgentIdentity || localAgentIdentity >= localPersistentMutation {
		t.Fatal("Local Executor installer must acquire setup then lifecycle and discover the Agent identity before managed directory mutations")
	}
}

func TestRuntimeUninstallersUseCanonicalPermanentHostLocks(t *testing.T) {
	for _, scriptName := range []string{
		"uninstall-autostream-host-agent",
		"uninstall-autostream-local-executor",
	} {
		payload, err := os.ReadFile(filepath.Join("..", "..", "release", scriptName))
		if err != nil {
			t.Fatal(err)
		}
		script := string(payload)
		for _, marker := range []string{
			`.autostream-runtime-host-setup.lock`,
			`.autostream-host-lifecycle.lock`,
			`acquire_host_runtime_locks`,
			`flock -n 8 || die "another AutoStream installer is provisioning shared host state"`,
			`flock -n 9 || die "another privileged Host lifecycle operation is active"`,
			`"root:root:700"`,
			`"root:root:600:1"`,
		} {
			if !strings.Contains(script, marker) {
				t.Fatalf("%s lock boundary is missing %q", scriptName, marker)
			}
		}
		setupLock := strings.Index(script, "flock -n 8")
		lifecycleLock := strings.Index(script, "flock -n 9")
		lockCall := strings.LastIndex(script, "acquire_host_runtime_locks")
		firstManagedPreflight := strings.Index(script, "\nverify_managed_file()")
		if setupLock < 0 || lifecycleLock <= setupLock || lockCall <= lifecycleLock ||
			firstManagedPreflight <= lockCall {
			t.Fatalf("%s must acquire setup then lifecycle before managed-state preflight", scriptName)
		}
	}
}

func TestLocalExecutorSystemdUnitsUseRootSocketActivationWithoutTCP(t *testing.T) {
	servicePayload, err := os.ReadFile(filepath.Join("..", "..", "systemd", "autostream-local-executor.service.example"))
	if err != nil {
		t.Fatal(err)
	}
	socketPayload, err := os.ReadFile(filepath.Join("..", "..", "systemd", "autostream-local-executor.socket.example"))
	if err != nil {
		t.Fatal(err)
	}
	tmpfilesPayload, err := os.ReadFile(filepath.Join("..", "..", "systemd", "autostream-local-executor.tmpfiles.example"))
	if err != nil {
		t.Fatal(err)
	}
	service := string(servicePayload)
	socket := string(socketPayload)
	tmpfiles := string(tmpfilesPayload)
	for _, marker := range []string{
		"ExecStart=/usr/local/libexec/autostream-local-executor run --policy /etc/autostream-local-executor/policy.json",
		"Requires=autostream-local-executor.socket",
		"UMask=0077",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"Environment=HOME=/",
		"Environment=DOCKER_CONFIG=/etc/autostream-local-executor/docker",
		"ProtectHome=true",
		"CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH CAP_SYS_PTRACE CAP_SETUID CAP_SETGID",
		"AmbientCapabilities=",
		"SocketBindDeny=any",
		"InaccessiblePaths=/etc/autostream",
		"ReadOnlyPaths=/etc/autostream-local-executor",
		"ReadWritePaths=/etc/autostream-host-agent",
		"ReadWritePaths=/var/lib/autostream-local-executor",
		"ReadWritePaths=-/opt/autostream/control-panel",
		"ReadWritePaths=-/opt/autostream/worker",
		"ReadWritePaths=-/opt/autostream/encoder-recorder",
		"ReadWritePaths=-/opt/autostream/discord-bot",
		"ReadWritePaths=-/opt/autostream/observability",
		"ReadWritePaths=-/opt/autostream/host-agent",
		"ReadWritePaths=-/opt/autostream/local-executor",
		"ReadWritePaths=-/var/backups/autostream/control-panel",
		"ReadWritePaths=-/var/backups/autostream/observability",
		"StateDirectory=autostream-local-executor",
		"StateDirectoryMode=0700",
	} {
		if !strings.Contains(service, marker) {
			t.Fatalf("local executor service is missing %q", marker)
		}
	}
	for _, line := range strings.Split(service, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "User=") || strings.HasPrefix(line, "Group=") {
			t.Fatalf("local executor service must inherit the system manager root identity, found %q", line)
		}
	}
	if strings.Contains(service, "ReadWritePaths=/var/lib/autostream-local-executor /opt/autostream") ||
		strings.Contains(service, "ReadWritePaths=/opt/autostream\n") {
		t.Fatal("local executor service grants broad write access to /opt/autostream")
	}
	if strings.Contains(service, "ReadWritePaths=/var/backups/autostream\n") ||
		strings.Contains(service, "ReadWritePaths=-/var/backups/autostream\n") {
		t.Fatal("local executor service grants broad write access to /var/backups/autostream")
	}
	for _, marker := range []string{
		"ListenStream=/run/autostream-local-executor/executor.sock",
		"FileDescriptorName=autostream-local-executor",
		"SocketUser=root",
		"SocketGroup=autostream-host-agent",
		"SocketMode=0660",
		"DirectoryMode=0750",
		"RemoveOnStop=true",
		"Service=autostream-local-executor.service",
	} {
		if !strings.Contains(socket, marker) {
			t.Fatalf("local executor socket is missing %q", marker)
		}
	}
	if strings.TrimSpace(tmpfiles) != strings.Join([]string{
		"d /run/autostream-local-executor 0750 root autostream-host-agent -",
		"d /run/autostream-updater 0700 root root -",
	}, "\n") {
		t.Fatalf("tmpfiles policy=%q", tmpfiles)
	}
	for _, forbidden := range []string{
		"ProtectProc=invisible",
		"ProcSubset=pid",
		"PrivateUsers=true",
		"IPAddressDeny=",
		"IPAddressAllow=",
	} {
		if strings.Contains(service, forbidden) {
			t.Fatalf("local executor service hides process metadata required for identity proof: %q", forbidden)
		}
	}
	for _, payload := range []string{service, socket, tmpfiles} {
		for _, forbidden := range []string{"8090", "ListenStream=0.", "ListenStream=[", "Environment=AUTOSTREAM", "EnvironmentFile="} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("local executor packaging contains forbidden TCP/secret marker %q", forbidden)
			}
		}
	}
}

func TestLocalExecutorInstallersPreserveRootPolicyBoundary(t *testing.T) {
	installerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-local-executor"))
	if err != nil {
		t.Fatal(err)
	}
	uninstallerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "uninstall-autostream-local-executor"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerPayload)
	uninstaller := string(uninstallerPayload)
	for _, marker := range []string{
		`CONFIG_DIR="/etc/autostream-local-executor"`,
		`POLICY_DEST="${CONFIG_DIR}/policy.json"`,
		`BINARY_DEST="/usr/local/libexec/autostream-local-executor"`,
		`SERVICE_DEST="/etc/systemd/system/autostream-local-executor.service"`,
		`SOCKET_DEST="/etc/systemd/system/autostream-local-executor.socket"`,
		`TMPFILES_DEST="/etc/tmpfiles.d/autostream-local-executor.conf"`,
		`STATE_DIR="/var/lib/autostream-local-executor"`,
		`DOCKER_CONFIG_DIR="${CONFIG_DIR}/docker"`,
		`EXECUTOR_DATA_DIR="/opt/autostream/local-executor"`,
		`PORT_CONFIG_DIR="${EXECUTOR_DATA_DIR}/ports"`,
		`DOCKER_VERSION_DIR="${EXECUTOR_DATA_DIR}/docker"`,
		`install -o root -g root -m 0600`,
		`validate-policy --policy "${policy_stage}"`,
		`systemd-tmpfiles --create "${TMPFILES_DEST}"`,
		`systemctl enable --now autostream-local-executor.socket`,
		`[[ $(unit_active_state "${SERVICE_NAME}") == "active" ]]`,
		`AGENT_USER="autostream-host-agent"`,
		`AGENT_GROUP="autostream-host-agent"`,
		`policy agent_uid does not match`,
		`policy agent_gid does not match`,
		`ensure_root_directory /usr/local/libexec 0755`,
		`ensure_root_directory "${CONFIG_DIR}" 0700`,
		`ensure_root_directory /etc/tmpfiles.d 0755`,
		`ensure_root_directory /opt/autostream 0755`,
		`die "${private_dir} must be root:root 0700"`,
		`"${DOCKER_CONFIG_DIR}" -mindepth 1 -maxdepth 1 ! -name config.json`,
		`"${DOCKER_CONFIG_FILE} must be root:root 0600"`,
		`root:root:700`,
		`copy_verified_release_file "${BINARY_SOURCE}" "${binary_stage}" root root 0755 "binary"`,
		`release source identity changed while it was copied`,
		`staged ${label} does not match the verified release source`,
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
		`verify_binary_identity "${BINARY_SOURCE}" "autostream-local-executor"`,
		`HOST_RUNTIME_CURRENT="${HOST_RUNTIME_ROOT}/current"`,
		`validate_managed_runtime_binary`,
		`binary destination symlink is outside the fixed Host Agent A/B runtime`,
		`managed A/B binary does not match this verified Host Agent release`,
		`if [[ ${binary_managed_link} == false ]]; then`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("local executor installer is missing %q", marker)
		}
	}
	if strings.Contains(installer, `(.database_schema == "none")))`) {
		t.Fatal("local executor artifact-manifest jq filter has an extra closing parenthesis")
	}
	for _, forbidden := range []string{
		"curl ",
		"wget ",
		"--command",
		"--unit",
		"--url",
		"systemctl enable --now autostream-local-executor.service",
		".artifact-sha256",
		"slot-binding",
	} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("local executor installer contains forbidden authority/network marker %q", forbidden)
		}
	}
	bundleVerify := strings.Index(
		installer,
		`verify_binary_identity "${BINARY_SOURCE}" "autostream-local-executor"`,
	)
	persistentMutation := strings.Index(installer, `ensure_root_directory "${CONFIG_DIR}" 0700`)
	if bundleVerify < 0 || persistentMutation < 0 || bundleVerify >= persistentMutation {
		t.Fatal("Local Executor bundle and binary identity must be verified before persistent mutation")
	}
	for _, marker := range []string{
		`systemctl disable --now autostream-local-executor.socket`,
		`systemctl stop autostream-local-executor.service`,
		`CONFIG_DIR="/etc/autostream-local-executor"`,
		`POLICY_DEST="${CONFIG_DIR}/policy.json"`,
		`STATE_DIR="/var/lib/autostream-local-executor"`,
		`--purge`,
		`policy was preserved`,
		`state was preserved`,
		`rmdir -- /run/autostream-local-executor`,
		`quarantine_file "${BINARY_DEST}"`,
		`restore_quarantined_file "${binary_backup}" "${BINARY_DEST}"`,
		`for command in dirname find flock install mktemp mountpoint mv readlink rm rmdir stat sync systemctl systemd-tmpfiles; do`,
		`state directory contains a mount point; refusing removal`,
		`freeze_host_runtime_producers`,
		`systemctl disable --now "${HOST_AGENT_SERVICE_NAME}"`,
		`systemctl disable --now "${unit}"`,
		`systemctl stop "${unit}"`,
		`state=$(systemctl is-active "${unit}" 2>/dev/null || true)`,
		`producer unit %s active state is %s; refusing purge`,
		`state=$(systemctl is-enabled "${unit}" 2>/dev/null || true)`,
		`producer unit %s enabled state is %s; refusing purge`,
		`A failed purge deliberately leaves these producers frozen.`,
	} {
		if !strings.Contains(uninstaller, marker) {
			t.Fatalf("local executor uninstaller is missing %q", marker)
		}
	}
	runtimePreflight := strings.Index(uninstaller, `stat -c '%U:%G:%a' -- "${SOCKET_DIR}"`)
	removeManagedFiles := strings.Index(uninstaller, `quarantine_file "${BINARY_DEST}"`)
	if runtimePreflight < 0 || removeManagedFiles < 0 || runtimePreflight >= removeManagedFiles {
		t.Fatal("local executor uninstaller must validate the runtime directory before deleting managed files")
	}
	stateDelete := strings.Index(uninstaller, `find "${state_backup}" -xdev -mindepth 1 -delete`)
	irreversible := -1
	if stateDelete >= 0 {
		irreversible = strings.LastIndex(uninstaller[:stateDelete], `restore_service_state=false`)
	}
	if stateDelete < 0 || irreversible < 0 || irreversible >= stateDelete {
		t.Fatal("local executor purge must stop rollback before destructive ledger deletion")
	}
	stateMove := strings.Index(uninstaller, `mv -T -- "${STATE_DIR}" "${state_backup}"`)
	producerFreeze := -1
	if stateMove >= 0 {
		producerFreeze = strings.LastIndex(uninstaller[:stateMove], "\n  freeze_host_runtime_producers\n")
	}
	if producerFreeze < 0 || stateMove < 0 || producerFreeze >= stateMove {
		t.Fatal("local executor purge must freeze Host Agent and recovery producers before moving durable state")
	}
}

func TestLocalExecutorInstallerRollsBackPrerequisitesAndRuntimeState(t *testing.T) {
	installerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-local-executor"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerPayload)
	earlyTrap := strings.Index(installer, "arm_cleanup_handler cleanup_prerequisites")
	firstPersistentMutation := strings.Index(installer, `ensure_root_directory "${CONFIG_DIR}" 0700`)
	if earlyTrap < 0 || firstPersistentMutation < 0 || earlyTrap >= firstPersistentMutation {
		t.Fatal("Local Executor prerequisite rollback trap must be armed before directory mutation")
	}
	for _, marker := range []string{
		"config_dir_identity=",
		"executor_data_dir_identity=",
		"state_snapshot=",
		`[[ ${state_dir_existed} == true ]] || return 0`,
		`[[ ${replacement_started} == true ]] || return 0`,
		`restore_executor_state`,
		`rollback_created_directory "${CONFIG_DIR}" "${config_dir_identity}"`,
		`verify_unit_inactive "${SERVICE_NAME}"`,
		`verify_unit_inactive "${SOCKET_NAME}"`,
		`verify_unit_disabled "${SOCKET_NAME}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("Local Executor installer is missing rollback marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		`systemctl stop "${SERVICE_NAME}" >/dev/null 2>&1 || true`,
		`systemctl disable --now "${SOCKET_NAME}" >/dev/null 2>&1 || true`,
	} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("Local Executor installer ignores a quiesce failure: %q", forbidden)
		}
	}

	smokePayload, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "security", "testdata",
		"run-local-executor-installer-smoke.sh",
	))
	if err != nil {
		t.Fatal(err)
	}
	smoke := string(smokePayload)
	for _, marker := range []string{
		"unsafe late state path left fresh Local Executor directories",
		"quiesce failure replaced Local Executor managed files",
		"post-start failure left fresh Local Executor state",
		"post-start failure changed existing Local Executor state",
	} {
		if !strings.Contains(smoke, marker) {
			t.Fatalf("Local Executor root smoke is missing rollback fixture %q", marker)
		}
	}
}

func TestLocalExecutorInstallerRestoresEarlyRuntimeStateAndSignalJournals(t *testing.T) {
	installerPayload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-local-executor"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerPayload)
	for _, marker := range []string{
		"managed_file_change_started=false",
		"restore_initial_runtime_state",
		`if [[ ${managed_file_change_started} == false &&`,
		"verify_initial_runtime_state",
		"pending_cleanup_signal=",
		"defer_cleanup_signals",
		"resume_cleanup_signals",
		`trap - EXIT`,
		`trap '' INT TERM`,
		"move_with_journal",
		"tmpfiles_creation_attempted=true",
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("Local Executor installer is missing rollback/signal marker %q", marker)
		}
	}
	if strings.Contains(installer, "trap - EXIT INT TERM") {
		t.Fatal("Local Executor cleanup must ignore a second INT/TERM instead of restoring their default actions")
	}
	mainCommit := strings.LastIndex(installer, "\ncommit_started=true")
	if mainCommit < 0 {
		t.Fatal("Local Executor installer is missing its main commit boundary")
	}
	mainTransaction := installer[mainCommit:]
	for _, marker := range []string{
		`systemctl disable --now "${SOCKET_NAME}" >/dev/null 2>&1 ||`,
		`die "could not disable the Local Executor socket"`,
		`systemctl stop "${SERVICE_NAME}" >/dev/null 2>&1 ||`,
		`die "could not stop the Local Executor service"`,
	} {
		if !strings.Contains(mainTransaction, marker) {
			t.Fatalf("Local Executor main quiesce must fail closed on command errors: missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"if ! systemctl disable --now \"${SOCKET_NAME}\" >/dev/null 2>&1; then\n  :\nfi",
		"if ! systemctl stop \"${SERVICE_NAME}\" >/dev/null 2>&1; then\n  :\nfi",
	} {
		if strings.Contains(mainTransaction, forbidden) {
			t.Fatalf("Local Executor main quiesce ignores a command error: %q", forbidden)
		}
	}

	createStart := strings.Index(installer, "create_managed_directory()")
	createEnd := strings.Index(installer[createStart:], "\n}")
	if createStart < 0 || createEnd < 0 {
		t.Fatal("Local Executor managed-directory helper is missing")
	}
	createBody := installer[createStart : createStart+createEnd]
	deferIndex := strings.Index(createBody, "defer_cleanup_signals")
	mkdirIndex := strings.Index(createBody, `mkdir -- "${path}"`)
	journalIndex := strings.Index(createBody, `printf -v "${identity_variable}"`)
	resumeIndex := strings.Index(createBody, "resume_cleanup_signals")
	if deferIndex < 0 || mkdirIndex < 0 || journalIndex < 0 || resumeIndex < 0 ||
		!(deferIndex < mkdirIndex && mkdirIndex < journalIndex && journalIndex < resumeIndex) {
		t.Fatal("Local Executor directory creation must defer cleanup signals until its rollback identity is journaled")
	}
}

func TestHostReleaseIncludesLocalExecutorAsPartOfHostAgentBundle(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release-host.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)
	for _, marker := range []string{
		`-o "${host_agent_root}/bin/autostream-local-executor" ./cmd/autostream-local-executor`,
		`run-local-executor-installer-smoke.sh`,
		`release/autostream-local-executor-policy.json.example`,
		`release/install-autostream-local-executor`,
		`release/uninstall-autostream-local-executor`,
		`release/README.local-executor.md`,
		`systemd/autostream-local-executor.service.example`,
		`systemd/autostream-local-executor.socket.example`,
		`systemd/autostream-local-executor.tmpfiles.example`,
		`local_executor_protocol_version: 2`,
		`local_executor_probe_only: false`,
		`local_executor_protocol_min_version: 1`,
		`local_executor_protocol_max_version: 2`,
		`local_executor_probe_compatible: true`,
		`local_executor_mutation_protocol_version: 2`,
		`local_executor_mutation_enabled: true`,
		`local_executor_mutation_requires_root_policy: true`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("host release workflow is missing local executor bundle marker %q", marker)
		}
	}
	smokePayload, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "security", "testdata",
		"run-local-executor-installer-smoke.sh",
	))
	if err != nil {
		t.Fatal(err)
	}
	smoke := string(smokePayload)
	for _, marker := range []string{
		`installer accepted a self-consistent bundle with invalid artifact metadata`,
		`installer accepted an archive with a duplicate canonical path`,
		`test ! -e /etc/autostream-local-executor`,
		`test ! -e /opt/autostream/local-executor`,
	} {
		if !strings.Contains(smoke, marker) {
			t.Fatalf("Local Executor root smoke is missing archive validation marker %q", marker)
		}
	}
}
