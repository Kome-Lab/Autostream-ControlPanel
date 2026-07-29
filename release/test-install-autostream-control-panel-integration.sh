#!/bin/bash
set -euo pipefail

umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LC_ALL=C

die() {
  printf 'control-panel installer integration test: %s\n' "$*" >&2
  exit 1
}

[[ ${EUID} -eq 0 ]] || die "must run as root"
[[ $(uname -m) == "x86_64" ]] || die "this integration fixture requires an amd64 Linux runner"

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly INSTALLER_SOURCE="${SCRIPT_DIR}/install-autostream-control-panel"
readonly VERSION="v9.9.9"
readonly ARTIFACT_ID="autostream-control-panel_${VERSION}_linux_amd64"
WORK_DIR="$(mktemp -d /var/tmp/autostream-control-panel-installer-test.XXXXXXXX)" || \
  die "could not create integration work directory"
readonly WORK_DIR
readonly ARTIFACTS_DIR="${WORK_DIR}/artifacts"
readonly EXTRACTED_ROOT="${ARTIFACTS_DIR}/${ARTIFACT_ID}"
readonly ARCHIVE="${ARTIFACTS_DIR}/${ARTIFACT_ID}.tar.gz"
readonly UNIT="autostream-control-panel.service"
readonly UNIT_PATH="/etc/systemd/system/${UNIT}"
readonly PUBLIC_BINARY="/usr/local/bin/control-panel"
readonly PUBLIC_WEB="/usr/share/autostream-control-panel"
readonly ENV_PATH="/etc/autostream/control-panel.env"
readonly STATE_DIR="/var/lib/autostream/control-panel"
readonly MANAGED_ROOT="/opt/autostream/control-panel"
readonly BACKUP_EXECUTABLE="/usr/local/sbin/autostream-backup-control-panel"
readonly DATABASE_BACKUP_DIR="/var/backups/autostream/control-panel"
readonly INSTALL_BACKUP_ROOT="/var/backups/autostream/install-migrations/control-panel"
readonly MARIADB_DEFAULTS="/etc/autostream-local-executor/mariadb-backup.cnf"
TARGET_LOCK_ID="$(printf '%s' "${UNIT}" | sha256sum | awk 'NR == 1 { print substr($1, 1, 12) }')"
[[ ${TARGET_LOCK_ID} =~ ^[0-9a-f]{12}$ ]] || die "could not derive updater target lock ID"
readonly TARGET_LOCK_ID
readonly TARGET_LOCK="/run/autostream-updater/.autostream-updater-${TARGET_LOCK_ID}.lock"
readonly LEGACY_UNIT_CONTENT="control-panel-installer-integration-legacy-unit"
readonly LEGACY_BINARY_CONTENT="control-panel-installer-integration-legacy-binary"
readonly LEGACY_WEB_CONTENT="control-panel-installer-integration-legacy-web"
readonly LEGACY_HELPER_CONTENT="control-panel-installer-integration-legacy-helper"
readonly LEGACY_ENV_CONTENT="CONTROL_PANEL_INSTALLER_INTEGRATION_ENV=preserve-exactly"
readonly LEGACY_DB_CONTENT="[client]
password=control-panel-installer-integration-preserve-exactly"

created_autostream_user=false
created_mariadb_dump=false
old_pid=""
usr_local_bin_mode_captured=false
usr_local_bin_original_mode=""
usr_local_bin_original_identity=""

cleanup() {
  local exit_code=$?
  local cleanup_failed=false
  set +e
  systemctl stop "${UNIT}" >/dev/null 2>&1
  systemctl disable "${UNIT}" >/dev/null 2>&1
  rm -f -- "${UNIT_PATH}"
  systemctl daemon-reload >/dev/null 2>&1
  if [[ -n ${old_pid} ]]; then
    kill "${old_pid}" >/dev/null 2>&1
  fi
  rm -f -- \
    "${PUBLIC_BINARY}" \
    "${BACKUP_EXECUTABLE}" \
    "${ENV_PATH}" \
    "${MARIADB_DEFAULTS}" \
    "${TARGET_LOCK}"
  rm -rf -- \
    "${PUBLIC_WEB}" \
    "${STATE_DIR}" \
    "${MANAGED_ROOT}" \
    "${DATABASE_BACKUP_DIR}" \
    "${INSTALL_BACKUP_ROOT}" \
    "${WORK_DIR}"
  rmdir \
    /var/backups/autostream/install-migrations \
    /var/backups/autostream \
    /var/lib/autostream \
    /opt/autostream \
    /etc/autostream \
    /etc/autostream-local-executor \
    /run/autostream-updater >/dev/null 2>&1
  if [[ ${created_mariadb_dump} == true ]]; then
    rm -f /usr/bin/mariadb-dump
  fi
  if [[ ${created_autostream_user} == true ]]; then
    userdel autostream >/dev/null 2>&1
    groupdel autostream >/dev/null 2>&1
  fi
  if [[ ${usr_local_bin_mode_captured} == true ]]; then
    if [[ -d /usr/local/bin &&
      ! -L /usr/local/bin &&
      $(readlink -f -- /usr/local/bin) == "/usr/local/bin" &&
      $(stat -c '%U:%G' -- /usr/local/bin) == "root:root" &&
      $(stat -c '%d:%i' -- /usr/local/bin) == "${usr_local_bin_original_identity}" ]] &&
      chmod "${usr_local_bin_original_mode}" /usr/local/bin &&
      [[ $(stat -c '%U:%G:%a' -- /usr/local/bin) == \
        "root:root:${usr_local_bin_original_mode}" ]]; then
      :
    else
      printf '%s\n' \
        "control-panel installer integration test: failed to restore /usr/local/bin mode ${usr_local_bin_original_mode}" \
        >&2
      cleanup_failed=true
    fi
  fi
  if [[ ${cleanup_failed} == true && ${exit_code} -eq 0 ]]; then
    exit_code=1
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

[[ -d /usr/local/bin && ! -L /usr/local/bin ]] || \
  die "/usr/local/bin must be a real directory"
[[ $(readlink -f -- /usr/local/bin) == "/usr/local/bin" ]] || \
  die "/usr/local/bin must resolve to its canonical path"
[[ $(stat -c '%U:%G' -- /usr/local/bin) == "root:root" ]] || \
  die "/usr/local/bin must be owned by root:root"
usr_local_bin_original_mode=$(stat -c '%a' -- /usr/local/bin) || \
  die "could not capture /usr/local/bin mode"
[[ ${usr_local_bin_original_mode} =~ ^[0-7]{3,4}$ ]] || \
  die "/usr/local/bin mode is invalid"
usr_local_bin_original_identity=$(stat -c '%d:%i' -- /usr/local/bin) || \
  die "could not capture /usr/local/bin identity"
[[ ${usr_local_bin_original_identity} =~ ^[0-9]+:[0-9]+$ ]] || \
  die "/usr/local/bin identity is invalid"
usr_local_bin_mode_captured=true
chmod 0755 /usr/local/bin
[[ $(stat -c '%U:%G:%a' -- /usr/local/bin) == "root:root:755" ]] || \
  die "failed to normalize /usr/local/bin to root:root mode 0755"

for path in \
  "${UNIT_PATH}" \
  "${PUBLIC_BINARY}" \
  "${PUBLIC_WEB}" \
  "${ENV_PATH}" \
  "${STATE_DIR}" \
  "${MANAGED_ROOT}" \
  "${BACKUP_EXECUTABLE}" \
  "${DATABASE_BACKUP_DIR}" \
  "${INSTALL_BACKUP_ROOT}" \
  "${MARIADB_DEFAULTS}"; do
  [[ ! -e ${path} && ! -L ${path} ]] || die "runner is not clean at ${path}"
done
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "runner already has an autostream account"
fi
created_autostream_user=true

if [[ ! -e /usr/bin/mariadb-dump && ! -L /usr/bin/mariadb-dump ]]; then
  install -o root -g root -m 0755 /dev/null /usr/bin/mariadb-dump
  created_mariadb_dump=true
fi
[[ -f /usr/bin/mariadb-dump && ! -L /usr/bin/mariadb-dump && -x /usr/bin/mariadb-dump ]] || \
  die "runner has an unsafe /usr/bin/mariadb-dump"

install -d -o root -g root -m 0755 \
  "${ARTIFACTS_DIR}" \
  "${EXTRACTED_ROOT}/bin" \
  "${EXTRACTED_ROOT}/backup" \
  "${EXTRACTED_ROOT}/share/autostream-control-panel" \
  "${EXTRACTED_ROOT}/systemd"
install -o root -g root -m 0755 "${INSTALLER_SOURCE}" \
  "${EXTRACTED_ROOT}/install-autostream-control-panel"

cat > "${EXTRACTED_ROOT}/bin/control-panel" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  if [ "${AUTOSTREAM_INSTALLER_TEST_PREFIX_VERSION:-}" = "1" ]; then
    printf '%s\n' 'autostream-control-panel v9.9.90'
  else
    printf '%s\n' 'autostream-control-panel v9.9.9'
  fi
  printf '%s\n' 'commit: integration-test'
  printf '%s\n' 'build_date: integration-test'
  exit 0
fi
exit 99
EOF
chmod 0755 "${EXTRACTED_ROOT}/bin/control-panel"

cat > "${EXTRACTED_ROOT}/backup/autostream-backup-control-panel" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "${EXTRACTED_ROOT}/backup/autostream-backup-control-panel"

cat > "${EXTRACTED_ROOT}/systemd/autostream-control-panel.service.example" <<'EOF'
[Unit]
Description=AutoStream Control Panel integration fixture

[Service]
Type=simple
User=autostream
Group=autostream
EnvironmentFile=-/etc/autostream/control-panel.env
ExecStart=/usr/local/bin/control-panel

[Install]
WantedBy=multi-user.target
EOF
printf '%s\n' 'AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel' \
  > "${EXTRACTED_ROOT}/.env.example"
printf '%s\n' 'integration-web-asset' \
  > "${EXTRACTED_ROOT}/share/autostream-control-panel/index.html"

(
  cd -- "${EXTRACTED_ROOT}"
  find . -type f ! -path './checksums.txt' -print0 |
    sort -z |
    xargs -0 sha256sum > checksums.txt
)
tar -C "${ARTIFACTS_DIR}" -czf "${ARCHIVE}" "${ARTIFACT_ID}"
(
  cd -- "${ARTIFACTS_DIR}"
  sha256sum "${ARTIFACT_ID}.tar.gz" > "${ARTIFACT_ID}.tar.gz.sha256"
)
archive_sha256="$(sha256sum "${ARCHIVE}" | awk 'NR == 1 { print $1 }')"
archive_size="$(stat -c %s "${ARCHIVE}")"
  jq -n \
  --arg version "${VERSION}" \
  --arg name "${ARTIFACT_ID}.tar.gz" \
  --arg sha256 "${archive_sha256}" \
  --argjson size "${archive_size}" \
  '{
    schema_version: 1,
    release_id: $version,
    channel: "host",
    published_at: "2026-07-29T00:00:00Z",
    minimum_agent_version: "v1.7.0",
    components: [{
      service: "control-panel",
      source_version: $version,
      commit: "0123456789abcdef0123456789abcdef01234567",
      rollback_compatible: true,
      database_schema: "backward_compatible",
      artifacts: [
        {
          os: "linux",
          arch: "amd64",
          name: $name,
          sha256: $sha256,
          size: $size
        },
        {
          os: "linux",
          arch: "arm64",
          name: ("autostream-control-panel_" + $version + "_linux_arm64.tar.gz"),
          sha256: "0000000000000000000000000000000000000000000000000000000000000000",
          size: 1
        }
      ]
    }]
  }' > "${ARTIFACTS_DIR}/release-manifest.json"
(
  cd -- "${ARTIFACTS_DIR}"
  sha256sum release-manifest.json > release-manifest.json.sha256
)

assert_unsafe_root_anchor_mode_rejected() {
  local mode=$1
  local output="${WORK_DIR}/unsafe-root-anchor-${mode}.out"
  local status
  chmod "${mode}" /usr/local/bin
  [[ $(stat -c '%a' -- /usr/local/bin) == "${mode}" ]] || \
    die "could not set /usr/local/bin mode ${mode} for the unsafe root-anchor test"
  set +e
  "${EXTRACTED_ROOT}/install-autostream-control-panel" > "${output}" 2>&1
  status=$?
  set -e
  chmod 0755 /usr/local/bin
  [[ ${status} -ne 0 ]] || \
    die "unsafe root-anchor mode ${mode} unexpectedly passed"
  grep -F -- "required system directory has unsafe mode bits: /usr/local/bin" \
    "${output}" >/dev/null || \
    die "unsafe root-anchor mode ${mode} did not fail with the expected message"
  [[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
    die "unsafe root-anchor mode ${mode} mutated managed state"
  if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
    die "unsafe root-anchor mode ${mode} mutated the service account"
  fi
}

for unsafe_root_anchor_mode in 777 4755 2755 1755; do
  assert_unsafe_root_anchor_mode_rejected "${unsafe_root_anchor_mode}"
done

cat > "${WORK_DIR}/failing-mktemp" <<EOF
#!/bin/sh
printf '%s\n' "\$*" > "${WORK_DIR}/mktemp-failure.boundary"
exit 73
EOF
chmod 0755 "${WORK_DIR}/failing-mktemp"

set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/failing-mktemp' /usr/bin/mktemp &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/mktemp-failure.out" 2>&1
mktemp_failure_status=$?
set -e
[[ ${mktemp_failure_status} -eq 1 ]] || \
  die "INPUT_STAGE mktemp failure did not fail closed with the expected status"
[[ -f ${WORK_DIR}/mktemp-failure.boundary ]] || \
  die "mktemp failure injection did not execute the installer mktemp boundary"
grep -Fx -- "-d /var/tmp/autostream-control-panel-install.XXXXXXXX" \
  "${WORK_DIR}/mktemp-failure.boundary" >/dev/null || \
  die "mktemp failure injection did not reach the INPUT_STAGE allocation"
grep -F -- "could not create the private input staging directory" \
  "${WORK_DIR}/mktemp-failure.out" >/dev/null || \
  die "mktemp failure did not report the expected fail-closed error"
[[ ! -e /unpack && ! -L /unpack ]] || die "mktemp failure created a root-level /unpack path"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "mktemp failure mutated managed state"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "mktemp failure mutated the service account"
fi

install -d -o root -g root -m 0755 /var/lib/autostream
ln -s -- "${WORK_DIR}" "${STATE_DIR}"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/unsafe-state.out" 2>&1
unsafe_state_status=$?
set -e
[[ ${unsafe_state_status} -ne 0 ]] || die "unsafe service state symlink unexpectedly passed"
grep -F -- "existing service state path is not a safe directory" \
  "${WORK_DIR}/unsafe-state.out" >/dev/null || \
  die "unsafe service state symlink did not fail with the expected message"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "unsafe service state validation mutated the service account"
fi
rm -f -- "${STATE_DIR}"
rmdir /var/lib/autostream

set +e
AUTOSTREAM_INSTALLER_TEST_PREFIX_VERSION=1 \
  "${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/prefix-version.out" 2>&1
prefix_version_status=$?
set -e
[[ ${prefix_version_status} -ne 0 ]] || \
  die "prefix-colliding binary version unexpectedly passed"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "wrong binary version mutated managed state"

"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/fresh.out"
[[ -L ${PUBLIC_BINARY} && -L ${PUBLIC_WEB} ]] || \
  die "fresh install did not install stable public links"
[[ -f ${ENV_PATH} && ! -L ${ENV_PATH} ]] || die "fresh install did not seed the environment"
[[ -f ${MARIADB_DEFAULTS} && ! -L ${MARIADB_DEFAULTS} ]] || \
  die "fresh install did not seed the MariaDB defaults"
[[ $(stat -c '%U:%G:%a' -- "${ENV_PATH}") == "root:root:640" ]] || \
  die "fresh environment ownership or mode is invalid"
[[ $(stat -c '%U:%G:%a' -- "${MARIADB_DEFAULTS}") == "root:root:600" ]] || \
  die "fresh MariaDB defaults ownership or mode is invalid"
systemctl is-active --quiet "${UNIT}" && die "fresh installer unexpectedly started the service"
systemctl is-enabled --quiet "${UNIT}" && die "fresh installer unexpectedly enabled the service"
grep -F -- "sudo systemctl enable --now ${UNIT}" "${WORK_DIR}/fresh.out" >/dev/null || \
  die "fresh install did not print the explicit start command"

rm -f -- \
  "${PUBLIC_BINARY}" \
  "${ENV_PATH}" \
  "${UNIT_PATH}" \
  "${BACKUP_EXECUTABLE}" \
  "${MARIADB_DEFAULTS}"
rm -rf -- \
  "${PUBLIC_WEB}" \
  "${STATE_DIR}" \
  "${MANAGED_ROOT}" \
  "${DATABASE_BACKUP_DIR}" \
  "${INSTALL_BACKUP_ROOT}"
systemctl daemon-reload

install -d -o root -g root -m 0755 /etc/autostream /var/lib/autostream
install -d -o autostream -g autostream -m 0750 "${STATE_DIR}"
install -d -o root -g root -m 0755 "${PUBLIC_WEB}"
printf '%s\n' "${LEGACY_BINARY_CONTENT}" > "${PUBLIC_BINARY}"
chmod 0755 "${PUBLIC_BINARY}"
printf '%s\n' "${LEGACY_WEB_CONTENT}" > "${PUBLIC_WEB}/legacy.txt"
printf '%s\n' "${LEGACY_ENV_CONTENT}" > "${ENV_PATH}"
chmod 0640 "${ENV_PATH}"
printf '%s\n' "${LEGACY_HELPER_CONTENT}" > "${BACKUP_EXECUTABLE}"
chmod 0700 "${BACKUP_EXECUTABLE}"
install -d -o root -g root -m 0700 /etc/autostream-local-executor
printf '%s\n' "${LEGACY_DB_CONTENT}" > "${MARIADB_DEFAULTS}"
chmod 0600 "${MARIADB_DEFAULTS}"
cat > "${UNIT_PATH}" <<EOF
[Unit]
Description=${LEGACY_UNIT_CONTENT}

[Service]
Type=simple
ExecStart=/usr/bin/sleep infinity
EOF
chmod 0644 "${UNIT_PATH}"
systemctl daemon-reload
systemctl start "${UNIT}"
old_pid="$(systemctl show --property MainPID --value "${UNIT}")"
[[ ${old_pid} =~ ^[1-9][0-9]*$ ]] || die "legacy service did not start"
kill -0 "${old_pid}" || die "legacy service PID is not alive"
systemctl is-enabled --quiet "${UNIT}" && die "legacy fixture must begin disabled"

env_before="$(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }')"
db_before="$(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }')"
unit_before="$(sha256sum "${UNIT_PATH}" | awk 'NR == 1 { print $1 }')"
helper_before="$(sha256sum "${BACKUP_EXECUTABLE}" | awk 'NR == 1 { print $1 }')"

(
  exec 8>"${TARGET_LOCK}"
  flock -n 8 || die "test could not acquire the updater target lock"
  set +e
  "${EXTRACTED_ROOT}/install-autostream-control-panel" \
    > "${WORK_DIR}/contention.out" 2>&1
  contention_status=$?
  set -e
  [[ ${contention_status} -ne 0 ]] || die "installer ignored updater lock contention"
)
grep -F -- "another privileged update is already active for ${UNIT}" \
  "${WORK_DIR}/contention.out" >/dev/null || \
  die "lock contention did not fail with the expected message"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "lock contention changed the running legacy process"
[[ $(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == "${env_before}" ]] || \
  die "lock contention changed the existing environment"

install -o root -g root -m 0755 /usr/bin/systemctl "${WORK_DIR}/real-systemctl"
cat > "${WORK_DIR}/systemctl-fail" <<EOF
#!/bin/bash
printf '%s\n' "\$*" >> "${WORK_DIR}/systemctl-fail.log"
if [[ \${1:-} == "daemon-reload" ]]; then
  count=0
  if [[ -f "${WORK_DIR}/systemctl-daemon-reload.count" ]]; then
    count=\$(<"${WORK_DIR}/systemctl-daemon-reload.count")
  fi
  count=\$((count + 1))
  printf '%s\n' "\${count}" > "${WORK_DIR}/systemctl-daemon-reload.count"
  if [[ \${count} -eq 1 ]]; then
    exit 71
  fi
fi
exec "${WORK_DIR}/real-systemctl" "\$@"
EOF
chmod 0755 "${WORK_DIR}/systemctl-fail"

set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/systemctl-fail' /usr/bin/systemctl &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/failed-install.out" 2>&1
failed_status=$?
set -e
[[ ${failed_status} -ne 0 ]] || die "daemon-reload failure injection unexpectedly succeeded"
[[ -f ${WORK_DIR}/systemctl-fail.log ]] || \
  die "failure injection did not execute the installer systemctl boundary"
grep -Fx -- "daemon-reload" "${WORK_DIR}/systemctl-fail.log" >/dev/null || \
  die "failure injection did not reach daemon-reload"
[[ -f ${WORK_DIR}/systemctl-daemon-reload.count &&
  $(<"${WORK_DIR}/systemctl-daemon-reload.count") -ge 2 ]] || \
  die "daemon-reload failure rollback did not reload the restored unit"
if grep -F -- "rollback was incomplete" "${WORK_DIR}/failed-install.out" >/dev/null; then
  die "daemon-reload failure unexpectedly left an incomplete rollback"
fi
[[ ! -e ${MANAGED_ROOT}/current && ! -L ${MANAGED_ROOT}/current ]] || \
  die "failed migration left current activated"
[[ -f ${PUBLIC_BINARY} && ! -L ${PUBLIC_BINARY} ]] || \
  die "failed migration did not restore the legacy binary"
grep -Fx -- "${LEGACY_BINARY_CONTENT}" "${PUBLIC_BINARY}" >/dev/null || \
  die "failed migration changed the legacy binary"
[[ -d ${PUBLIC_WEB} && ! -L ${PUBLIC_WEB} ]] || \
  die "failed migration did not restore the legacy web directory"
grep -Fx -- "${LEGACY_WEB_CONTENT}" "${PUBLIC_WEB}/legacy.txt" >/dev/null || \
  die "failed migration changed the legacy web directory"
[[ $(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == "${env_before}" ]] || \
  die "failed migration changed the existing environment"
[[ $(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }') == "${db_before}" ]] || \
  die "failed migration changed the existing MariaDB defaults"
[[ $(sha256sum "${UNIT_PATH}" | awk 'NR == 1 { print $1 }') == "${unit_before}" ]] || \
  die "failed migration did not restore the systemd unit"
[[ $(sha256sum "${BACKUP_EXECUTABLE}" | awk 'NR == 1 { print $1 }') == "${helper_before}" ]] || \
  die "failed migration did not restore the backup executable"
kill -0 "${old_pid}" || die "failed migration stopped the running legacy process"

install -o root -g root -m 0755 /usr/bin/sync "${WORK_DIR}/real-sync"
cat > "${WORK_DIR}/sync-fail" <<EOF
#!/bin/bash
printf '%s\n' "\$*" >> "${WORK_DIR}/sync-fail.log"
if [[ "\$*" == "-f /usr/local/bin" ]]; then
  count=0
  if [[ -f "${WORK_DIR}/sync-usr-local-bin.count" ]]; then
    count=\$(<"${WORK_DIR}/sync-usr-local-bin.count")
  fi
  count=\$((count + 1))
  printf '%s\n' "\${count}" > "${WORK_DIR}/sync-usr-local-bin.count"
  if [[ \${count} -eq 2 ]]; then
    exit 74
  fi
fi
exec "${WORK_DIR}/real-sync" "\$@"
EOF
chmod 0755 "${WORK_DIR}/sync-fail"

set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/sync-fail' /usr/bin/sync &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/sync-failure.out" 2>&1
sync_failure_status=$?
set -e
[[ ${sync_failure_status} -ne 0 ]] || die "activation sync failure injection unexpectedly succeeded"
[[ -f ${WORK_DIR}/sync-usr-local-bin.count &&
  $(<"${WORK_DIR}/sync-usr-local-bin.count") -ge 2 ]] || \
  die "sync failure injection did not reach the post-activation durability boundary"
[[ ! -e ${MANAGED_ROOT}/current && ! -L ${MANAGED_ROOT}/current ]] || \
  die "sync failure rollback left current activated"
[[ -f ${PUBLIC_BINARY} && ! -L ${PUBLIC_BINARY} ]] || \
  die "sync failure rollback did not restore the legacy binary"
grep -Fx -- "${LEGACY_BINARY_CONTENT}" "${PUBLIC_BINARY}" >/dev/null || \
  die "sync failure rollback changed the legacy binary"
[[ -d ${PUBLIC_WEB} && ! -L ${PUBLIC_WEB} ]] || \
  die "sync failure rollback did not restore the legacy web directory"
[[ $(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == "${env_before}" ]] || \
  die "sync failure rollback changed the existing environment"
[[ $(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }') == "${db_before}" ]] || \
  die "sync failure rollback changed the existing MariaDB defaults"
[[ $(sha256sum "${UNIT_PATH}" | awk 'NR == 1 { print $1 }') == "${unit_before}" ]] || \
  die "sync failure rollback did not restore the systemd unit"
[[ $(sha256sum "${BACKUP_EXECUTABLE}" | awk 'NR == 1 { print $1 }') == "${helper_before}" ]] || \
  die "sync failure rollback did not restore the backup executable"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "sync failure rollback replaced the running legacy process"
kill -0 "${old_pid}" || die "sync failure rollback stopped the running legacy process"

"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/migration.out"
[[ -L ${MANAGED_ROOT}/current ]] || die "successful migration did not activate current"
[[ -L ${PUBLIC_BINARY} && -L ${PUBLIC_WEB} ]] || \
  die "successful migration did not install stable public links"
[[ $(readlink -f -- "${PUBLIC_BINARY}") == \
  "${MANAGED_ROOT}/releases/${VERSION}-${archive_sha256:0:12}/bin/control-panel" ]] || \
  die "public binary does not resolve to the verified release"
[[ $(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == "${env_before}" ]] || \
  die "successful migration changed the existing environment"
[[ $(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }') == "${db_before}" ]] || \
  die "successful migration changed the existing MariaDB defaults"
grep -Fx -- "${LEGACY_BINARY_CONTENT}" \
  "${INSTALL_BACKUP_ROOT}/${VERSION}-${archive_sha256:0:12}/control-panel" >/dev/null || \
  die "successful migration did not retain the legacy binary"
grep -Fx -- "${LEGACY_WEB_CONTENT}" \
  "${INSTALL_BACKUP_ROOT}/${VERSION}-${archive_sha256:0:12}/autostream-control-panel/legacy.txt" >/dev/null || \
  die "successful migration did not retain the legacy web directory"
grep -F -- "sudo systemctl restart ${UNIT}" "${WORK_DIR}/migration.out" >/dev/null || \
  die "active migration did not print the explicit restart command"
systemctl is-enabled --quiet "${UNIT}" && die "migration unexpectedly enabled the service"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "successful migration replaced the running legacy process"
kill -0 "${old_pid}" || die "successful migration stopped the running legacy process"

"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/idempotent.out"
systemctl is-enabled --quiet "${UNIT}" && die "idempotent reinstall unexpectedly enabled the service"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "idempotent reinstall replaced the running legacy process"
[[ $(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == "${env_before}" ]] || \
  die "idempotent reinstall changed the existing environment"
[[ $(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }') == "${db_before}" ]] || \
  die "idempotent reinstall changed the existing MariaDB defaults"

printf '%s\n' "Control Panel installer integration scenarios passed."
