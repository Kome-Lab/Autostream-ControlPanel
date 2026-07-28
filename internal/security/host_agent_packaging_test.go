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
		`for command in awk dd dirname getent groupadd id install ln mktemp mv readlink rm rmdir runuser sha256sum sort stat sync systemctl test tr useradd; do`,
		`HOST_RUNTIME_ROOT="/opt/autostream/host-agent"`,
		`HOST_RUNTIME_CURRENT="${HOST_RUNTIME_ROOT}/current"`,
		`ln -s "slots/a" "${HOST_RUNTIME_CURRENT}"`,
		`ln -s "${HOST_RUNTIME_CURRENT}/bin/autostream-host-agent" "${BINARY_DEST}"`,
		`ln -s "${HOST_RUNTIME_CURRENT}/bin/autostream-local-executor" "${LOCAL_EXECUTOR_BINARY_DEST}"`,
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
		`install -d -o root -g "${AGENT_GROUP}" -m 0750 "${CONFIG_DIR}"`,
		`die "${path} must be root:${AGENT_GROUP} 0750"`,
		`both current and legacy Host Agent identities exist; refusing an ambiguous migration`,
		`retire_legacy_identity`,
		`sync -f "${CONFIG_DIR}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("host agent installer is missing %q", marker)
		}
	}
	if strings.Contains(installer, "runtime_token=") || strings.Contains(installer, "--runtime-token") {
		t.Fatal("host agent installer must not copy the runtime token into argv or environment")
	}
	preflight := strings.Index(installer, `if validate_existing "${CONFIG_DEST}"`)
	commit := strings.LastIndex(installer, "commit_started=true")
	if preflight < 0 || commit < 0 || preflight >= commit {
		t.Fatal("host agent installer must preflight every existing destination before the rollback transaction starts")
	}
	for _, rollbackMarker := range []string{
		"was_enabled=false",
		"service_quiesced=false",
		`systemctl disable "${UNIT_NAME}"`,
		`if [[ ${service_quiesced} == true && ${was_active} == true ]]; then`,
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
		`if [[ "${#assets[@]}" -ne 18 ]]; then`,
		`(length == 18)`,
		`(.assets | length == 18)`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("host release workflow is missing %q", marker)
		}
	}
}
