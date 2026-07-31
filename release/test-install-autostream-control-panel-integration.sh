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

if [[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS:-} != "1" ]]; then
  exec unshare --mount --propagation private bash -c '
    set -euo pipefail
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-scratch /mnt
    install -d -o root -g root -m 0755 \
      /mnt/usr-lower \
      /mnt/etc-lower \
      /mnt/var-lower \
      /mnt/run-lower
    mount --rbind /usr /mnt/usr-lower
    mount --make-rprivate /mnt/usr-lower
    mount --rbind /etc /mnt/etc-lower
    mount --make-rprivate /mnt/etc-lower
    mount --rbind /var /mnt/var-lower
    mount --make-rprivate /mnt/var-lower
    mount --rbind /run /mnt/run-lower
    mount --make-rprivate /mnt/run-lower
    install -d -o root -g root -m 0755 \
      /mnt/usr-upper \
      /mnt/usr-upper/local \
      /mnt/etc-upper \
      /mnt/etc-upper/systemd \
      /mnt/etc-upper/systemd/system \
      /mnt/var-upper \
      /mnt/var-upper/lib \
      /mnt/var-upper/backups \
      /mnt/run-upper
    install -d -o root -g root -m 1777 /mnt/var-upper/tmp
    install -d -o root -g root -m 0700 \
      /mnt/usr-work \
      /mnt/etc-work \
      /mnt/var-work \
      /mnt/run-work
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/usr-lower,upperdir=/mnt/usr-upper,workdir=/mnt/usr-work \
      autostream-control-panel-installer-test-usr /usr
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/etc-lower,upperdir=/mnt/etc-upper,workdir=/mnt/etc-work \
      autostream-control-panel-installer-test-etc /etc
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/var-lower,upperdir=/mnt/var-upper,workdir=/mnt/var-work \
      autostream-control-panel-installer-test-var /var
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/run-lower,upperdir=/mnt/run-upper,workdir=/mnt/run-work \
      autostream-control-panel-installer-test-run /run
    mount --rbind /mnt/run-lower/systemd /run/systemd
    mount --make-rprivate /run/systemd
    run_systemd_identity="$(stat -c "%d:%i" -- /mnt/run-lower/systemd)"
    [[ $(stat -c "%d:%i" -- /run/systemd) == "${run_systemd_identity}" ]]
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-bin /usr/local/bin
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-sbin /usr/local/sbin
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-opt /opt
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-share /usr/share
    mount -t tmpfs -o ro,nodev,nosuid,noexec,mode=0555,uid=0,gid=0 \
      autostream-control-panel-installer-test-sealed /mnt
    exec env \
      AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS=1 \
      AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_RUN_SYSTEMD_IDENTITY="${run_systemd_identity}" \
      bash "$1"
  ' autostream-control-panel-installer-test-mount "$0"
fi
grep -Eq ' /mnt .* - tmpfs autostream-control-panel-installer-test-scratch ' \
  /proc/self/mountinfo || die "isolated /mnt scratch mount is missing"
grep -Eq ' /usr .* - overlay autostream-control-panel-installer-test-usr ' \
  /proc/self/mountinfo || die "isolated /usr overlay mount is missing"
grep -Eq ' /etc .* - overlay autostream-control-panel-installer-test-etc ' \
  /proc/self/mountinfo || die "isolated /etc overlay mount is missing"
grep -Eq ' /var .* - overlay autostream-control-panel-installer-test-var ' \
  /proc/self/mountinfo || die "isolated /var overlay mount is missing"
grep -Eq ' /run .* - overlay autostream-control-panel-installer-test-run ' \
  /proc/self/mountinfo || die "isolated /run overlay mount is missing"
grep -Eq ' /mnt ro[^ ]*( [^ ]+)* - tmpfs autostream-control-panel-installer-test-sealed ' \
  /proc/self/mountinfo || die "sealed /mnt mount is missing or writable"
[[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_RUN_SYSTEMD_IDENTITY:-} =~ ^[0-9]+:[0-9]+$ ]] || \
  die "host-backed /run/systemd identity was not preserved"
[[ $(stat -c '%d:%i' -- /run/systemd) == \
  "${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_RUN_SYSTEMD_IDENTITY}" ]] || \
  die "host-backed /run/systemd bind changed identity"
[[ $(stat -c '%U:%G:%a' -- /mnt) == "root:root:555" ]] || \
  die "sealed /mnt mount ownership or mode is invalid"
if touch /mnt/autostream-control-panel-installer-test-write-probe 2>/dev/null; then
  rm -f -- /mnt/autostream-control-panel-installer-test-write-probe
  die "sealed /mnt unexpectedly permits writes to hidden host aliases"
fi
grep -Eq ' /usr/local/bin .* - tmpfs autostream-control-panel-installer-test-bin ' \
  /proc/self/mountinfo || die "isolated /usr/local/bin mount is missing"
grep -Eq ' /usr/local/sbin .* - tmpfs autostream-control-panel-installer-test-sbin ' \
  /proc/self/mountinfo || die "isolated /usr/local/sbin mount is missing"
grep -Eq ' /opt .* - tmpfs autostream-control-panel-installer-test-opt ' \
  /proc/self/mountinfo || die "isolated /opt mount is missing"
grep -Eq ' /usr/share .* - tmpfs autostream-control-panel-installer-test-share ' \
  /proc/self/mountinfo || die "isolated /usr/share mount is missing"
[[ $(stat -c '%m' -- /usr/local/bin) == "/usr/local/bin" ]] || \
  die "isolated /usr/local/bin mount is not effective"
[[ $(stat -c '%m' -- /usr/local/sbin) == "/usr/local/sbin" ]] || \
  die "isolated /usr/local/sbin mount is not effective"
[[ $(stat -c '%m' -- /opt) == "/opt" ]] || \
  die "isolated /opt mount is not effective"
[[ $(stat -c '%m' -- /usr/share) == "/usr/share" ]] || \
  die "isolated /usr/share mount is not effective"
[[ $(stat -c '%U:%G:%a' -- /usr) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr fixture"
[[ $(stat -c '%U:%G:%a' -- /etc) == "root:root:755" ]] || \
  die "could not create an isolated safe /etc fixture"
[[ $(stat -c '%U:%G:%a' -- /etc/systemd) == "root:root:755" ]] || \
  die "could not create an isolated safe /etc/systemd fixture"
[[ $(stat -c '%U:%G:%a' -- /etc/systemd/system) == "root:root:755" ]] || \
  die "could not create an isolated safe /etc/systemd/system fixture"
[[ $(stat -c '%U:%G:%a' -- /var) == "root:root:755" ]] || \
  die "could not create an isolated safe /var fixture"
[[ $(stat -c '%U:%G:%a' -- /var/lib) == "root:root:755" ]] || \
  die "could not create an isolated safe /var/lib fixture"
[[ $(stat -c '%U:%G:%a' -- /var/backups) == "root:root:755" ]] || \
  die "could not create an isolated safe /var/backups fixture"
[[ $(stat -c '%U:%G:%a' -- /var/tmp) == "root:root:1777" ]] || \
  die "could not create an isolated safe /var/tmp fixture"
[[ $(stat -c '%U:%G:%a' -- /run) == "root:root:755" ]] || \
  die "could not create an isolated safe /run fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/local) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/local fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/local/bin) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/local/bin fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/local/sbin) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/local/sbin fixture"
[[ $(stat -c '%U:%G:%a' -- /opt) == "root:root:755" ]] || \
  die "could not create an isolated safe /opt fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/share) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/share fixture"

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly INSTALLER_SOURCE="${SCRIPT_DIR}/install-autostream-control-panel"
readonly VERSION="v9.9.9"
readonly BUILD_COMMIT="0123456789abcdef0123456789abcdef01234567"
readonly BUILD_DATE="2026-07-31T00:00:00Z"
readonly ARTIFACT_ID="autostream-control-panel_${VERSION}_linux_amd64"
WORK_DIR="$(mktemp -d /var/tmp/autostream-control-panel-installer-test.XXXXXXXX)" || \
  die "could not create integration work directory"
readonly WORK_DIR
readonly ARTIFACTS_DIR="${WORK_DIR}/artifacts"
readonly EXTRACTED_ROOT="${ARTIFACTS_DIR}/${ARTIFACT_ID}"
readonly ARCHIVE="${ARTIFACTS_DIR}/${ARTIFACT_ID}.tar.gz"
readonly UNIT="autostream-control-panel.service"
readonly UNIT_PATH="/etc/systemd/system/${UNIT}"
readonly RUNTIME_UNIT_PATH="/run/systemd/system/${UNIT}"
[[ -d /run/systemd/system && ! -L /run/systemd/system &&
  $(readlink -f -- /run/systemd/system) == "/run/systemd/system" &&
  $(stat -c '%U:%G:%a' -- /run/systemd/system) == "root:root:755" ]] || \
  die "systemd runtime unit directory is unsafe"
readonly PUBLIC_BINARY="/usr/local/bin/control-panel"
readonly PUBLIC_WEB="/usr/share/autostream-control-panel"
readonly ENV_PATH="/etc/autostream/control-panel.env"
readonly STATE_DIR="/var/lib/autostream/control-panel"
readonly MANAGED_ROOT="/opt/autostream/control-panel"
readonly CURRENT_LINK="${MANAGED_ROOT}/current"
readonly BACKUP_EXECUTABLE="/usr/local/sbin/autostream-backup-control-panel"
readonly DATABASE_BACKUP_DIR="/var/backups/autostream/control-panel"
readonly INSTALL_BACKUP_ROOT="/var/backups/autostream/install-migrations/control-panel"
readonly MARIADB_DEFAULTS="/etc/autostream-local-executor/mariadb-backup.cnf"
readonly SHARED_HOST_SETUP_LOCK="/run/autostream-updater/.autostream-runtime-host-setup.lock"
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
fixture_paths_owned=false
fixture_service_start_attempted=false
old_pid=""
old_pid_start_time=""
runtime_unit_candidate=""
runtime_unit_identity=""
runtime_unit_owned=false
runtime_sync_precommit_hook=""
runtime_cleanup_preremove_hook=""
runtime_race_active=false
runtime_race_backup=""
runtime_race_foreign_stage=""
runtime_race_foreign_identity=""
runtime_race_foreign_hash=""

read_proc_pid_start_time() {
  local pid=$1
  local start_time=""
  local stat_line=""
  local stat_tail=""
  [[ ${pid} =~ ^[1-9][0-9]*$ && -r /proc/${pid}/stat ]] || return 1
  IFS= read -r stat_line < "/proc/${pid}/stat" || return 1
  [[ ${stat_line} == *") "* ]] || return 1
  stat_tail="${stat_line##*) }"
  set -- ${stat_tail}
  [[ $# -ge 20 ]] || return 1
  start_time="${20}"
  [[ ${start_time} =~ ^[0-9]+$ ]] || return 1
  printf '%s\n' "${start_time}"
}

record_fixture_process_identity() {
  local pid=$1
  local start_time=""
  [[ ${pid} =~ ^[1-9][0-9]*$ ]] || die "fixture service did not report a valid PID"
  start_time="$(read_proc_pid_start_time "${pid}")" || \
    die "fixture service process identity is unavailable"
  [[ ${start_time} =~ ^[1-9][0-9]*$ ]] || die "fixture service process start time is invalid"
  old_pid="${pid}"
  old_pid_start_time="${start_time}"
}

clear_fixture_process_identity() {
  old_pid=""
  old_pid_start_time=""
}

kill_recorded_fixture_process() {
  local current_start_time=""
  [[ -n ${old_pid} && -n ${old_pid_start_time} ]] || return 0
  current_start_time="$(read_proc_pid_start_time "${old_pid}" 2>/dev/null)" || return 0
  [[ ${current_start_time} == "${old_pid_start_time}" ]] || return 0
  kill "${old_pid}" >/dev/null 2>&1
}

assert_pid_reuse_guard() (
  local probe_pid=""
  local probe_start_time=""
  /usr/bin/sleep infinity &
  probe_pid=$!
  trap 'kill "${probe_pid}" >/dev/null 2>&1 || true; wait "${probe_pid}" >/dev/null 2>&1 || true' EXIT
  probe_start_time="$(read_proc_pid_start_time "${probe_pid}")"
  [[ ${probe_start_time} =~ ^[1-9][0-9]*$ ]] || \
    die "PID reuse guard probe could not read process identity"
  old_pid="${probe_pid}"
  old_pid_start_time=$((probe_start_time + 1))
  kill_recorded_fixture_process
  kill -0 "${probe_pid}" || die "PID reuse guard signaled an unrelated process"
)

runtime_unit_identity_is_owned() {
  [[ ${runtime_unit_owned} == true &&
    -n ${runtime_unit_identity} &&
    -f ${RUNTIME_UNIT_PATH} &&
    ! -L ${RUNTIME_UNIT_PATH} &&
    $(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == "${runtime_unit_identity}" ]]
}

restore_runtime_sync_race() {
  local current_identity=""
  [[ ${runtime_race_active} == true ]] || return 0
  [[ -n ${runtime_race_backup} &&
    -f ${runtime_race_backup} &&
    ! -L ${runtime_race_backup} &&
    $(stat -c '%d:%i' -- "${runtime_race_backup}") == "${runtime_unit_identity}" ]] || \
    return 1
  if [[ -f ${RUNTIME_UNIT_PATH} && ! -L ${RUNTIME_UNIT_PATH} ]]; then
    current_identity="$(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}")"
  fi
  if [[ ${current_identity} == "${runtime_race_foreign_identity}" ]]; then
    mv -Tf -- "${runtime_race_backup}" "${RUNTIME_UNIT_PATH}" || return 1
    runtime_race_backup=""
  elif [[ ${current_identity} == "${runtime_unit_identity}" ]]; then
    rm -f -- "${runtime_race_backup}" || return 1
    runtime_race_backup=""
  else
    return 1
  fi
  if [[ -n ${runtime_race_foreign_stage} ]]; then
    [[ -f ${runtime_race_foreign_stage} &&
      ! -L ${runtime_race_foreign_stage} &&
      $(stat -c '%d:%i' -- "${runtime_race_foreign_stage}") == \
        "${runtime_race_foreign_identity}" ]] || return 1
    rm -f -- "${runtime_race_foreign_stage}" || return 1
    runtime_race_foreign_stage=""
  fi
  sync -f /run/systemd/system || return 1
  runtime_unit_identity_is_owned || return 1
  runtime_race_active=false
  runtime_race_foreign_identity=""
  runtime_race_foreign_hash=""
}

replace_runtime_unit_for_precommit_probe() {
  runtime_unit_identity_is_owned || return 1
  runtime_race_backup="$(
    mktemp "/run/systemd/system/.${UNIT}.race-backup.XXXXXXXX"
  )" || return 1
  rm -f -- "${runtime_race_backup}" || return 1
  ln -- "${RUNTIME_UNIT_PATH}" "${runtime_race_backup}" || return 1
  [[ $(stat -c '%d:%i' -- "${runtime_race_backup}") == "${runtime_unit_identity}" ]] || \
    return 1
  runtime_race_active=true

  runtime_race_foreign_stage="$(
    mktemp "/run/systemd/system/.${UNIT}.race-foreign.XXXXXXXX"
  )" || return 1
  runtime_race_foreign_identity="$(
    stat -c '%d:%i' -- "${runtime_race_foreign_stage}"
  )" || return 1
  cat > "${runtime_race_foreign_stage}" <<EOF
[Unit]
Description=control-panel-installer-integration-foreign-runtime-unit

[Service]
Type=simple
User=nobody
ExecStart=/usr/bin/false

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "${runtime_race_foreign_stage}" || return 1
  runtime_race_foreign_hash="$(
    sha256sum "${runtime_race_foreign_stage}" | awk 'NR == 1 { print $1 }'
  )" || return 1
  sync -f "${runtime_race_foreign_stage}" || return 1
  mv -Tf -- "${runtime_race_foreign_stage}" "${RUNTIME_UNIT_PATH}" || return 1
  runtime_race_foreign_stage=""
  sync -f /run/systemd/system || return 1
  [[ $(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == \
    "${runtime_race_foreign_identity}" ]]
}

remove_owned_runtime_unit_for_cleanup() {
  if [[ -n ${runtime_cleanup_preremove_hook} ]] &&
    ! "${runtime_cleanup_preremove_hook}"; then
      return 76
  fi
  runtime_unit_identity_is_owned || return 75
  rm -f -- "${RUNTIME_UNIT_PATH}"
}

cleanup() {
  local exit_code=$?
  local cleanup_failed=false
  local load_state=""
  local runtime_unit_identity_matches=false
  local runtime_unit_removed=false
  set +e
  if [[ ${runtime_race_active} == true ]] && ! restore_runtime_sync_race; then
    cleanup_failed=true
    printf 'control-panel installer integration test: cleanup could not restore the runtime race probe\n' >&2
  fi
  if [[ ${runtime_unit_owned} == true &&
    -n ${runtime_unit_identity} &&
    -f ${RUNTIME_UNIT_PATH} &&
    ! -L ${RUNTIME_UNIT_PATH} &&
    $(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == "${runtime_unit_identity}" ]]; then
    runtime_unit_identity_matches=true
  fi
  if [[ ${runtime_unit_owned} == true &&
    ${runtime_unit_identity_matches} != true ]]; then
    cleanup_failed=true
    printf 'control-panel installer integration test: cleanup refused a missing or replaced runtime unit\n' >&2
  fi
  if [[ ${fixture_service_start_attempted} == true &&
    ${runtime_unit_identity_matches} == true ]]; then
    if systemctl stop "${UNIT}" >/dev/null 2>&1; then
      clear_fixture_process_identity
    else
      cleanup_failed=true
      printf 'control-panel installer integration test: cleanup could not stop the fixture service\n' >&2
    fi
  fi
  if [[ ${runtime_unit_owned} == true &&
    ${runtime_unit_identity_matches} == true ]]; then
    if remove_owned_runtime_unit_for_cleanup; then
      runtime_unit_removed=true
    else
      cleanup_failed=true
      printf 'control-panel installer integration test: cleanup refused a changed runtime unit or could not remove it\n' >&2
    fi
    if [[ ${runtime_unit_removed} == true ]] &&
      ! systemctl daemon-reload >/dev/null 2>&1; then
        cleanup_failed=true
        printf 'control-panel installer integration test: cleanup daemon-reload failed\n' >&2
    fi
  fi
  if [[ ${fixture_service_start_attempted} == true ]]; then
    kill_recorded_fixture_process
    if systemctl is-active --quiet "${UNIT}"; then
      cleanup_failed=true
      printf 'control-panel installer integration test: cleanup left the fixture service active\n' >&2
    fi
    load_state="$(systemctl show --property LoadState --value "${UNIT}" 2>/dev/null)"
    if [[ ${load_state} != "not-found" ]]; then
      cleanup_failed=true
      printf 'control-panel installer integration test: cleanup left the fixture unit loaded\n' >&2
    fi
  fi
  if [[ -n ${runtime_unit_candidate} ]]; then
    if ! rm -f -- "${runtime_unit_candidate}"; then
      cleanup_failed=true
      printf 'control-panel installer integration test: cleanup could not remove runtime staging\n' >&2
    fi
  fi
  if [[ ${fixture_paths_owned} == true ]]; then
    rm -f -- \
      "${UNIT_PATH}" \
      "${PUBLIC_BINARY}" \
      "${BACKUP_EXECUTABLE}" \
      "${ENV_PATH}" \
      "${MARIADB_DEFAULTS}" \
      "${SHARED_HOST_SETUP_LOCK}" \
      "${TARGET_LOCK}"
    rm -rf -- \
      "${PUBLIC_WEB}" \
      "${STATE_DIR}" \
      "${MANAGED_ROOT}" \
      "${DATABASE_BACKUP_DIR}" \
      "${INSTALL_BACKUP_ROOT}"
    rmdir \
      /var/backups/autostream/install-migrations \
      /var/backups/autostream \
      /var/lib/autostream \
      /opt/autostream \
      /etc/autostream \
      /etc/autostream-local-executor \
      /run/autostream-updater >/dev/null 2>&1
  fi
  if [[ ${created_mariadb_dump} == true ]]; then
    rm -f /usr/bin/mariadb-dump
  fi
  if [[ ${created_autostream_user} == true ]]; then
    userdel autostream >/dev/null 2>&1
    groupdel autostream >/dev/null 2>&1
  fi
  rm -rf -- "${WORK_DIR}"
  if [[ ${cleanup_failed} == true && ${exit_code} -eq 0 ]]; then
    exit_code=1
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

assert_pid_reuse_guard

for path in \
  "${UNIT_PATH}" \
  "${RUNTIME_UNIT_PATH}" \
  "${PUBLIC_BINARY}" \
  "${PUBLIC_WEB}" \
  "${ENV_PATH}" \
  "${STATE_DIR}" \
  "${MANAGED_ROOT}" \
  "${BACKUP_EXECUTABLE}" \
  "${DATABASE_BACKUP_DIR}" \
  "${INSTALL_BACKUP_ROOT}" \
  "${MARIADB_DEFAULTS}" \
  "${SHARED_HOST_SETUP_LOCK}" \
  "${TARGET_LOCK}"; do
  [[ ! -e ${path} && ! -L ${path} ]] || die "runner is not clean at ${path}"
done
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "runner already has an autostream account"
fi
fixture_paths_owned=true
created_autostream_user=true

install_runtime_unit_exclusive() {
  [[ ${runtime_unit_owned} == false ]] || die "runtime unit is already fixture-owned"
  [[ ! -e ${RUNTIME_UNIT_PATH} && ! -L ${RUNTIME_UNIT_PATH} ]] || \
    die "runtime unit path appeared after preflight"
  runtime_unit_candidate="$(mktemp "/run/systemd/system/.${UNIT}.XXXXXXXX")" || \
    die "could not create runtime unit candidate"
  install -o root -g root -m 0644 "${UNIT_PATH}" "${runtime_unit_candidate}" || \
    die "could not populate runtime unit candidate"
  sync -f "${runtime_unit_candidate}"
  if ! ln -- "${runtime_unit_candidate}" "${RUNTIME_UNIT_PATH}"; then
    rm -f -- "${runtime_unit_candidate}"
    runtime_unit_candidate=""
    die "runtime unit path appeared after the clean-runner preflight"
  fi
  runtime_unit_owned=true
  runtime_unit_identity="$(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}")"
  rm -f -- "${runtime_unit_candidate}"
  runtime_unit_candidate=""
  sync -f /run/systemd/system
  assert_owned_runtime_unit_identity
  cmp -s -- "${UNIT_PATH}" "${RUNTIME_UNIT_PATH}" || \
    die "atomic runtime unit creation changed the private unit"
}

replace_owned_runtime_unit() {
  assert_owned_runtime_unit_identity
  runtime_unit_candidate="$(mktemp "/run/systemd/system/.${UNIT}.XXXXXXXX")" || \
    die "could not create replacement runtime unit candidate"
  install -o root -g root -m 0644 "${UNIT_PATH}" "${runtime_unit_candidate}" || \
    die "could not populate replacement runtime unit candidate"
  cmp -s -- "${UNIT_PATH}" "${runtime_unit_candidate}" || \
    die "replacement runtime unit staging changed the private unit"
  sync -f "${runtime_unit_candidate}"
  if [[ -n ${runtime_sync_precommit_hook} ]] &&
    ! "${runtime_sync_precommit_hook}"; then
    rm -f -- "${runtime_unit_candidate}"
    runtime_unit_candidate=""
    return 76
  fi
  if ! runtime_unit_identity_is_owned; then
    rm -f -- "${runtime_unit_candidate}"
    runtime_unit_candidate=""
    return 75
  fi
  mv -Tf -- "${runtime_unit_candidate}" "${RUNTIME_UNIT_PATH}" || \
    die "could not atomically replace the owned runtime unit"
  runtime_unit_candidate=""
  runtime_unit_identity="$(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}")"
  sync -f /run/systemd/system
  assert_owned_runtime_unit_identity
  cmp -s -- "${UNIT_PATH}" "${RUNTIME_UNIT_PATH}" || \
    die "runtime unit does not match the migrated private unit"
}

assert_owned_runtime_unit_identity() {
  runtime_unit_identity_is_owned || die "runtime unit is not strictly fixture-owned"
  [[ $(stat -c '%U:%G:%a' -- "${RUNTIME_UNIT_PATH}") == "root:root:644" ]] || \
    die "runtime unit path has unsafe ownership or mode"
}

assert_legacy_runtime_unit_loaded() {
  assert_owned_runtime_unit_identity
  cmp -s -- "${UNIT_PATH}" "${RUNTIME_UNIT_PATH}" || \
    die "legacy runtime unit differs from the private rollback unit"
  [[ $(systemctl show --property FragmentPath --value "${UNIT}") == "${RUNTIME_UNIT_PATH}" ]] || \
    die "systemd did not keep the legacy runtime unit loaded"
  systemctl show --property ExecStart --value "${UNIT}" |
    grep -F -- "path=/usr/bin/sleep" >/dev/null || \
    die "systemd did not keep the legacy ExecStart loaded"
  if [[ -n ${old_pid} ]]; then
    [[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
      die "systemd replaced the running legacy process"
  fi
}

assert_managed_runtime_unit_loaded() {
  assert_owned_runtime_unit_identity
  cmp -s -- "${UNIT_PATH}" "${RUNTIME_UNIT_PATH}" || \
    die "managed runtime unit differs from the private installed unit"
  [[ $(systemctl show --property FragmentPath --value "${UNIT}") == "${RUNTIME_UNIT_PATH}" ]] || \
    die "systemd did not load the managed runtime unit"
  systemctl show --property ExecStart --value "${UNIT}" |
    grep -F -- "path=/usr/local/bin/control-panel" >/dev/null || \
    die "systemd did not load the managed ExecStart"
  [[ $(systemctl show --property User --value "${UNIT}") == "autostream" ]] || \
    die "systemd did not load the managed service user"
}

snapshot_managed_release_tree() {
  local release_dir=$1
  local output_path=$2
  (
    cd -- "${release_dir}"
    find . -printf '%P|%D:%i|%U:%G|%m|%s\n' | LC_ALL=C sort
    find . -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum
  ) > "${output_path}"
}

if [[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_PREFLIGHT_PROBE:-} != "1" ]]; then
  cat > "${UNIT_PATH}" <<'EOF'
[Unit]
Description=AutoStream Control Panel fixture preflight preservation probe

[Service]
Type=simple
ExecStart=/usr/bin/sleep infinity

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "${UNIT_PATH}"
  install_runtime_unit_exclusive
  rm -f -- "${UNIT_PATH}"
  systemctl daemon-reload
  fixture_service_start_attempted=true
  systemctl start "${UNIT}"
  preflight_probe_pid="$(systemctl show --property MainPID --value "${UNIT}")"
  record_fixture_process_identity "${preflight_probe_pid}"
  preflight_probe_identity="$(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}")"
  preflight_probe_hash="$(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }')"
  preflight_probe_enabled="$(systemctl is-enabled "${UNIT}" 2>/dev/null || true)"

  set +e
  AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS=1 \
    AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_PREFLIGHT_PROBE=1 \
    bash "$0" > "${WORK_DIR}/preflight-preservation.out" 2>&1
  preflight_probe_status=$?
  set -e
  [[ ${preflight_probe_status} -ne 0 ]] || \
    die "preflight preservation probe unexpectedly passed"
  grep -F -- "runner is not clean at ${RUNTIME_UNIT_PATH}" \
    "${WORK_DIR}/preflight-preservation.out" >/dev/null || \
    die "preflight preservation probe did not stop at the runtime unit"
  [[ $(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == "${preflight_probe_identity}" ]] || \
    die "preflight failure replaced the existing runtime unit"
  [[ $(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }') == \
    "${preflight_probe_hash}" ]] || \
    die "preflight failure changed the existing runtime unit"
  [[ $(systemctl show --property MainPID --value "${UNIT}") == "${preflight_probe_pid}" ]] || \
    die "preflight failure replaced the existing service process"
  kill -0 "${preflight_probe_pid}" || \
    die "preflight failure stopped the existing service process"
  [[ $(systemctl is-enabled "${UNIT}" 2>/dev/null || true) == "${preflight_probe_enabled}" ]] || \
    die "preflight failure changed the existing service enablement"

  systemctl stop "${UNIT}"
  fixture_service_start_attempted=false
  clear_fixture_process_identity
  assert_owned_runtime_unit_identity
  rm -f -- "${RUNTIME_UNIT_PATH}"
  systemctl daemon-reload
  runtime_unit_owned=false
  runtime_unit_identity=""
fi

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
  printf '%s\n' 'commit: 0123456789abcdef0123456789abcdef01234567'
  printf '%s\n' 'build_date: 2026-07-31T00:00:00Z'
  exit 0
fi
exit 99
EOF
chmod 0755 "${EXTRACTED_ROOT}/bin/control-panel"

cat > "${EXTRACTED_ROOT}/bin/autostream-updater" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  printf '%s\n' 'autostream-updater v9.9.9'
  printf '%s\n' 'commit: 0123456789abcdef0123456789abcdef01234567'
  printf '%s\n' 'build_date: 2026-07-31T00:00:00Z'
  exit 0
fi
exit 99
EOF
chmod 0755 "${EXTRACTED_ROOT}/bin/autostream-updater"

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

jq -n \
  --arg version "${VERSION}" \
  --arg commit "${BUILD_COMMIT}" \
  --arg build_date "${BUILD_DATE}" \
  --arg archive_name "${ARTIFACT_ID}.tar.gz" \
  --arg artifact_root "${ARTIFACT_ID}" \
  '{
    schema_version: 1,
    component: "control-panel",
    source_version: $version,
    commit: $commit,
    build_date: $build_date,
    platform: {
      os: "linux",
      arch: "amd64"
    },
    archive: {
      name: $archive_name,
      root: $artifact_root
    },
    compatibility: {
      minimum_agent_version: "v1.7.0",
      minimum_panel_version: null,
      rollback_compatible: true,
      database_schema: "backward_compatible"
    }
  }' > "${EXTRACTED_ROOT}/artifact-manifest.json"

rebuild_fixture_archive() {
  rm -f -- "${EXTRACTED_ROOT}/checksums.txt" "${ARCHIVE}"
  (
    cd -- "${EXTRACTED_ROOT}"
    find . -type f ! -path './checksums.txt' -print0 |
      sort -z |
      xargs -0 sha256sum > checksums.txt
  )
  tar -C "${ARTIFACTS_DIR}" -czf "${ARCHIVE}" "${ARTIFACT_ID}"
}

rebuild_fixture_archive
(
  grep -Eq '^[0-9a-f]{64}  \./bin/autostream-updater$' \
  "${EXTRACTED_ROOT}/checksums.txt" || \
    die "fixture checksum inventory does not cover bin/autostream-updater"
  grep -Eq '^[0-9a-f]{64}  \./artifact-manifest.json$' \
    "${EXTRACTED_ROOT}/checksums.txt" || \
    die "fixture checksum inventory does not cover artifact-manifest.json"
)
[[ $(find "${ARTIFACTS_DIR}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n') == \
  "${ARTIFACT_ID}.tar.gz" ]] || \
  die "fixture must begin with the archive as its only adjacent release file"

install -o root -g root -m 0600 "${EXTRACTED_ROOT}/artifact-manifest.json" \
  "${WORK_DIR}/artifact-manifest.valid.json"
jq '.component = "worker"' "${WORK_DIR}/artifact-manifest.valid.json" \
  > "${EXTRACTED_ROOT}/artifact-manifest.json"
rebuild_fixture_archive
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/invalid-artifact-manifest.out" 2>&1
invalid_artifact_manifest_status=$?
set -e
[[ ${invalid_artifact_manifest_status} -ne 0 ]] || \
  die "self-consistent archive with invalid artifact metadata unexpectedly passed"
grep -F -- "artifact-manifest.json does not authorize this exact artifact" \
  "${WORK_DIR}/invalid-artifact-manifest.out" >/dev/null || \
  die "invalid artifact metadata did not fail at the metadata boundary"
if grep -Eq '^jq: (error:|[0-9]+ compile errors?)' \
  "${WORK_DIR}/invalid-artifact-manifest.out"; then
  printf '%s\n' \
    'control-panel installer integration test: captured jq artifact-manifest verifier failure:' \
    >&2
  cat -- "${WORK_DIR}/invalid-artifact-manifest.out" >&2
  die "artifact manifest verifier emitted a jq parser or compile error"
fi
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "invalid artifact metadata mutated managed state"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "invalid artifact metadata mutated the service account"
fi
install -o root -g root -m 0600 "${WORK_DIR}/artifact-manifest.valid.json" \
  "${EXTRACTED_ROOT}/artifact-manifest.json"
rebuild_fixture_archive

printf '%s\n' 'canonical archive alias probe' \
  > "${ARTIFACTS_DIR}/control-panel-canonical-alias-file"
tar -C "${ARTIFACTS_DIR}" -czf "${ARCHIVE}" \
  "${ARTIFACT_ID}" \
  --transform="s#^control-panel-canonical-alias-file\$#${ARTIFACT_ID}#" \
  control-panel-canonical-alias-file
rm -f -- "${ARTIFACTS_DIR}/control-panel-canonical-alias-file"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/duplicate-archive-entry.out" 2>&1
duplicate_archive_status=$?
set -e
[[ ${duplicate_archive_status} -ne 0 ]] || \
  die "archive with a duplicate canonical path unexpectedly passed"
grep -F -- "release archive contains duplicate paths" \
  "${WORK_DIR}/duplicate-archive-entry.out" >/dev/null || \
  die "duplicate archive path did not fail at the archive layout boundary"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "duplicate archive path mutated managed state"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "duplicate archive path mutated the service account"
fi
rebuild_fixture_archive
archive_sha256="$(sha256sum "${ARCHIVE}" | awk 'NR == 1 { print $1 }')"

printf '%s\n' 'intentionally stale and ignored' > "${ARCHIVE}.sha256"
printf '%s\n' '{not valid json' > "${ARTIFACTS_DIR}/release-manifest.json"
printf '%s\n' 'intentionally stale and ignored' \
  > "${ARTIFACTS_DIR}/release-manifest.json.sha256"

restore_safe_root_anchor_fixture() {
  chmod 00755 /usr/local/bin
  [[ $(stat -c '%U:%G:%a' -- /usr/local/bin) == "root:root:755" ]] || \
    die "could not restore isolated /usr/local/bin to root:root mode 0755"
}

assert_unsafe_root_anchor_mode_rejected() {
  local mode=$1
  local output="${WORK_DIR}/unsafe-root-anchor-${mode}.out"
  local status
  local actual_mode
  restore_safe_root_anchor_fixture
  chmod "${mode}" /usr/local/bin
  actual_mode="$(stat -c '%a' -- /usr/local/bin)" || \
    die "could not inspect /usr/local/bin mode ${mode} for the unsafe root-anchor test"
  [[ ${actual_mode} == "${mode}" ]] || \
    die "could not set /usr/local/bin mode ${mode} for the unsafe root-anchor test; got ${actual_mode}"
  set +e
  "${EXTRACTED_ROOT}/install-autostream-control-panel" > "${output}" 2>&1
  status=$?
  set -e
  restore_safe_root_anchor_fixture
  [[ ${status} -ne 0 ]] || \
    die "unsafe root-anchor mode ${mode} unexpectedly passed"
  if ! grep -F -- "required system directory has unsafe mode bits: /usr/local/bin" \
    "${output}" >/dev/null; then
    printf 'control-panel installer integration test: captured installer output for unsafe root-anchor mode %s:\n' \
      "${mode}" >&2
    cat -- "${output}" >&2
    die "unsafe root-anchor mode ${mode} did not fail with the expected message"
  fi
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
if ! grep -F -- "existing service state path is not a safe directory" \
  "${WORK_DIR}/unsafe-state.out" >/dev/null; then
  printf 'control-panel installer integration test: captured installer output for unsafe service state symlink:\n' \
    >&2
  cat -- "${WORK_DIR}/unsafe-state.out" >&2
  die "unsafe service state symlink did not fail with the expected message"
fi
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

install -d -o root -g root -m 0700 /run/autostream-updater
printf '%s\n' 'control-panel shared host-setup lock sentinel' \
  > "${SHARED_HOST_SETUP_LOCK}"
chown root:root "${SHARED_HOST_SETUP_LOCK}"
chmod 0600 "${SHARED_HOST_SETUP_LOCK}"
shared_lock_before="$(stat -c '%d:%i:%u:%g:%a:%s' -- "${SHARED_HOST_SETUP_LOCK}")"
shared_lock_hash_before="$(
  sha256sum -- "${SHARED_HOST_SETUP_LOCK}" | awk 'NR == 1 { print $1 }'
)"
shared_parent_metadata_before="$(
  for shared_parent in \
    /opt \
    /usr/local/bin \
    /usr/local/sbin \
    /usr/share \
    /etc \
    /var/lib \
    /var/backups \
    /run/autostream-updater; do
    stat -c '%n|%d:%i:%u:%g:%a' -- "${shared_parent}"
  done
)"
(
  exec 7<>"${SHARED_HOST_SETUP_LOCK}"
  flock -n 7 || die "test could not acquire the shared host-setup lock"
  set +e
  "${EXTRACTED_ROOT}/install-autostream-control-panel" \
    7>&- > "${WORK_DIR}/shared-lock-contention.out" 2>&1
  shared_contention_status=$?
  set -e
  [[ ${shared_contention_status} -eq 1 ]] || \
    die "shared host-setup lock contention did not fail closed with the expected status"
)
if ! grep -Fx -- \
  "install-autostream-control-panel: another AutoStream installer is provisioning shared host state" \
  "${WORK_DIR}/shared-lock-contention.out" >/dev/null; then
  printf '%s\n' \
    'control-panel installer integration test: captured installer output for shared host-setup lock contention:' \
    >&2
  cat -- "${WORK_DIR}/shared-lock-contention.out" >&2
  die "shared host-setup lock contention did not report the expected error"
fi
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- "${SHARED_HOST_SETUP_LOCK}") == \
  "${shared_lock_before}" &&
  $(sha256sum -- "${SHARED_HOST_SETUP_LOCK}" | awk 'NR == 1 { print $1 }') == \
    "${shared_lock_hash_before}" ]] || \
  die "shared host-setup contention replaced or truncated the permanent lock"
shared_parent_metadata_after="$(
  for shared_parent in \
    /opt \
    /usr/local/bin \
    /usr/local/sbin \
    /usr/share \
    /etc \
    /var/lib \
    /var/backups \
    /run/autostream-updater; do
    stat -c '%n|%d:%i:%u:%g:%a' -- "${shared_parent}"
  done
)"
[[ ${shared_parent_metadata_after} == "${shared_parent_metadata_before}" ]] || \
  die "shared host-setup lock contention mutated account, parents, or current"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "shared host-setup lock contention mutated account, parents, or current"
fi
for shared_contention_absent_path in \
  /opt/autostream \
  /var/lib/autostream \
  /etc/autostream \
  /etc/autostream-local-executor \
  /var/backups/autostream \
  "${UNIT_PATH}" \
  "${PUBLIC_BINARY}" \
  "${PUBLIC_WEB}" \
  "${BACKUP_EXECUTABLE}" \
  "${MANAGED_ROOT}/current" \
  "${TARGET_LOCK}"; do
  [[ ! -e ${shared_contention_absent_path} &&
    ! -L ${shared_contention_absent_path} ]] || \
    die "shared host-setup lock contention mutated account, parents, or current"
done

install -o root -g root -m 0755 "$(command -v groupadd)" \
  "${WORK_DIR}/real-groupadd"
install -o root -g root -m 0755 "$(command -v groupdel)" \
  "${WORK_DIR}/real-groupdel"
install -o root -g root -m 0755 "$(command -v useradd)" \
  "${WORK_DIR}/real-useradd"
cat > "${WORK_DIR}/signal-groupadd" <<EOF
#!/bin/bash
set -euo pipefail
status=0
if "${WORK_DIR}/real-groupadd" "\$@"; then
  :
else
  status=\$?
fi
if [[ \${status} -eq 0 ]]; then
  printf '%s\n' delivered > "${WORK_DIR}/signal-groupadd.executed"
  kill -TERM "\${PPID}"
fi
exit "\${status}"
EOF
cat > "${WORK_DIR}/signal-groupdel" <<EOF
#!/bin/bash
set -euo pipefail
printf '%s\n' signal-safe > "${WORK_DIR}/signal-groupdel.executed"
"${WORK_DIR}/real-groupdel" "\$@"
EOF
chmod 0755 "${WORK_DIR}/signal-groupadd" "${WORK_DIR}/signal-groupdel"
signal_stage_before="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/signal-groupadd' /usr/sbin/groupadd &&
    mount --bind '${WORK_DIR}/signal-groupdel' /usr/sbin/groupdel &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/signal-account-rollback.out" 2>&1
signal_account_status=$?
set -e
if [[ ${signal_account_status} -ne 143 ]]; then
  printf 'control-panel installer integration test: signal-interrupted groupadd status=%s\n' \
    "${signal_account_status}" >&2
  for signal_marker in signal-groupadd.executed signal-groupdel.executed; do
    if [[ -f ${WORK_DIR}/${signal_marker} ]]; then
      printf 'control-panel installer integration test: %s=present\n' "${signal_marker}" >&2
    else
      printf 'control-panel installer integration test: %s=absent\n' "${signal_marker}" >&2
    fi
  done
  printf '%s\n' \
    'control-panel installer integration test: captured installer output for signal-interrupted groupadd:' \
    >&2
  cat -- "${WORK_DIR}/signal-account-rollback.out" >&2
  die "signal-interrupted groupadd did not exit with deferred TERM status 143"
fi
[[ -f ${WORK_DIR}/signal-groupadd.executed ]] || \
  die "signal-interrupted groupadd did not reach its injection boundary"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "signal-interrupted groupadd left the service account behind"
fi
[[ -f ${WORK_DIR}/signal-groupdel.executed ]] || \
  die "signal-safe groupdel wrapper did not execute"
signal_stage_after="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
[[ ${signal_stage_after} == "${signal_stage_before}" ]] || \
  die "signal during rollback left private input staging behind"
for signal_absent_path in \
  /opt/autostream \
  /var/lib/autostream \
  /etc/autostream \
  /etc/autostream-local-executor \
  /var/backups/autostream \
  "${UNIT_PATH}" \
  "${PUBLIC_BINARY}" \
  "${PUBLIC_WEB}" \
  "${BACKUP_EXECUTABLE}" \
  "${MANAGED_ROOT}/current"; do
  [[ ! -e ${signal_absent_path} && ! -L ${signal_absent_path} ]] || \
    die "signal-interrupted groupadd left persistent installer state"
done

report_failed_install_probe() {
  local boundary=$1
  local output_path=$2
  printf 'control-panel installer integration test: captured installer output for %s:\n' \
    "${boundary}" >&2
  if [[ -f ${output_path} && ! -L ${output_path} ]]; then
    cat -- "${output_path}" >&2
  else
    printf 'control-panel installer integration test: captured output is missing or unsafe: %s\n' \
      "${output_path}" >&2
  fi
}

assert_failed_install_rollback_clean() {
  local boundary=$1
  local stage_before=$2
  local output_path=$3
  local stage_after
  if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
    report_failed_install_probe "${boundary}" "${output_path}"
    die "${boundary} left the service account behind"
  fi
  stage_after="$(
    find /var/tmp -mindepth 1 -maxdepth 1 -type d \
      -name 'autostream-control-panel-install.*' -printf '%f\n' |
      LC_ALL=C sort
  )"
  if [[ ${stage_after} != "${stage_before}" ]]; then
    printf 'control-panel installer integration test: %s staging before:\n%s\n' \
      "${boundary}" "${stage_before}" >&2
    printf 'control-panel installer integration test: %s staging after:\n%s\n' \
      "${boundary}" "${stage_after}" >&2
    report_failed_install_probe "${boundary}" "${output_path}"
    die "${boundary} left private input staging behind"
  fi
  for absent_path in \
    /opt/autostream \
    /var/lib/autostream \
    /etc/autostream \
    /etc/autostream-local-executor \
    /var/backups/autostream \
    "${UNIT_PATH}" \
    "${PUBLIC_BINARY}" \
    "${PUBLIC_WEB}" \
    "${BACKUP_EXECUTABLE}" \
    "${MANAGED_ROOT}/current"; do
    if [[ -e ${absent_path} || -L ${absent_path} ]]; then
      report_failed_install_probe "${boundary}" "${output_path}"
      die "${boundary} left persistent installer state"
    fi
  done
}

cat > "${WORK_DIR}/partial-success-groupadd" <<EOF
#!/bin/bash
set -euo pipefail
"${WORK_DIR}/real-groupadd" "\$@"
printf '%s\n' delivered > "${WORK_DIR}/partial-success-groupadd.executed"
exit 73
EOF
cat > "${WORK_DIR}/cleanup-signal-groupdel" <<EOF
#!/bin/bash
set -euo pipefail
printf '%s\n' delivered > "${WORK_DIR}/cleanup-signal-groupdel.executed"
kill -TERM "\${PPID}"
"${WORK_DIR}/real-groupdel" "\$@"
EOF
chmod 0755 \
  "${WORK_DIR}/partial-success-groupadd" \
  "${WORK_DIR}/cleanup-signal-groupdel"
partial_success_stage_before="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/partial-success-groupadd' /usr/sbin/groupadd &&
    mount --bind '${WORK_DIR}/cleanup-signal-groupdel' /usr/sbin/groupdel &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/partial-success-groupadd.out" 2>&1
partial_success_status=$?
set -e
if [[ ${partial_success_status} -ne 1 ]]; then
  printf 'control-panel installer integration test: partial-success groupadd status=%s\n' \
    "${partial_success_status}" >&2
  for partial_marker in \
    partial-success-groupadd.executed \
    cleanup-signal-groupdel.executed; do
    if [[ -f ${WORK_DIR}/${partial_marker} ]]; then
      printf 'control-panel installer integration test: %s=present\n' "${partial_marker}" >&2
    else
      printf 'control-panel installer integration test: %s=absent\n' "${partial_marker}" >&2
    fi
  done
  printf '%s\n' \
    'control-panel installer integration test: captured installer output for partial-success groupadd:' \
    >&2
  cat -- "${WORK_DIR}/partial-success-groupadd.out" >&2
  die "partial-success groupadd did not exit with status 1"
fi
[[ -f ${WORK_DIR}/partial-success-groupadd.executed ]] || \
  die "partial-success groupadd did not reach its injection boundary"
[[ -f ${WORK_DIR}/cleanup-signal-groupdel.executed ]] || \
  die "cleanup-signal groupdel wrapper did not execute"
assert_failed_install_rollback_clean \
  "partial-success groupadd rollback" \
  "${partial_success_stage_before}" \
  "${WORK_DIR}/partial-success-groupadd.out"

cat > "${WORK_DIR}/signal-useradd" <<EOF
#!/bin/bash
set -euo pipefail
useradd_status=0
if "${WORK_DIR}/real-useradd" "\$@"; then
  :
else
  useradd_status=\$?
fi
if [[ \${useradd_status} -eq 0 ]]; then
  printf '%s\n' delivered > "${WORK_DIR}/signal-useradd.executed"
  kill -TERM "\${PPID}"
fi
exit "\${useradd_status}"
EOF
chmod 0755 "${WORK_DIR}/signal-useradd"
"${WORK_DIR}/real-groupadd" --system autostream
useradd_term_group_before="$(getent group autostream)"
useradd_term_group_digest_before="$(
  sha256sum -- /etc/group | awk 'NR == 1 { print $1 }'
)"
useradd_term_gshadow_digest_before="$(
  sha256sum -- /etc/gshadow | awk 'NR == 1 { print $1 }'
)"
[[ ${useradd_term_group_digest_before} =~ ^[0-9a-f]{64}$ &&
  ${useradd_term_gshadow_digest_before} =~ ^[0-9a-f]{64}$ ]] || \
  die "could not snapshot the local group databases before the useradd TERM transaction"
useradd_term_stage_before="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/signal-useradd' /usr/sbin/useradd &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/signal-useradd-rollback.out" 2>&1
useradd_term_status=$?
set -e
if [[ ${useradd_term_status} -ne 143 ]]; then
  report_failed_install_probe \
    "useradd TERM transaction" \
    "${WORK_DIR}/signal-useradd-rollback.out"
  die "useradd TERM transaction exited with ${useradd_term_status}, expected 143"
fi
if [[ ! -f ${WORK_DIR}/signal-useradd.executed ]]; then
  report_failed_install_probe \
    "useradd TERM transaction" \
    "${WORK_DIR}/signal-useradd-rollback.out"
  die "useradd TERM transaction did not reach its injection boundary"
fi
if id autostream >/dev/null 2>&1; then
  report_failed_install_probe \
    "useradd TERM transaction" \
    "${WORK_DIR}/signal-useradd-rollback.out"
  die "useradd TERM transaction left the invocation-created service user"
fi
if getent passwd autostream-install-rollback >/dev/null 2>&1 ||
  getent group autostream-install-rollback >/dev/null 2>&1; then
  report_failed_install_probe \
    "useradd TERM transaction" \
    "${WORK_DIR}/signal-useradd-rollback.out"
  die "useradd TERM transaction left the reserved rollback login"
fi
if [[ $(getent group autostream 2>/dev/null || true) != \
  "${useradd_term_group_before}" ]]; then
  report_failed_install_probe \
    "useradd TERM transaction" \
    "${WORK_DIR}/signal-useradd-rollback.out"
  die "useradd TERM transaction changed the pre-existing service group"
fi
if [[ $(sha256sum -- /etc/group | awk 'NR == 1 { print $1 }') != \
    "${useradd_term_group_digest_before}" ||
  $(sha256sum -- /etc/gshadow | awk 'NR == 1 { print $1 }') != \
    "${useradd_term_gshadow_digest_before}" ]]; then
  report_failed_install_probe \
    "useradd TERM transaction" \
    "${WORK_DIR}/signal-useradd-rollback.out"
  die "useradd TERM transaction changed the pre-existing local group databases"
fi
useradd_term_stage_after="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
if [[ ${useradd_term_stage_after} != "${useradd_term_stage_before}" ]]; then
  report_failed_install_probe \
    "useradd TERM transaction" \
    "${WORK_DIR}/signal-useradd-rollback.out"
  die "useradd TERM transaction left private input staging behind"
fi
for useradd_term_absent_path in \
  /opt/autostream \
  /var/lib/autostream \
  /etc/autostream \
  /etc/autostream-local-executor \
  /var/backups/autostream \
  "${UNIT_PATH}" \
  "${PUBLIC_BINARY}" \
  "${PUBLIC_WEB}" \
  "${BACKUP_EXECUTABLE}" \
  "${MANAGED_ROOT}/current"; do
  if [[ -e ${useradd_term_absent_path} || -L ${useradd_term_absent_path} ]]; then
    report_failed_install_probe \
      "useradd TERM transaction" \
      "${WORK_DIR}/signal-useradd-rollback.out"
    die "useradd TERM transaction left persistent installer state"
  fi
done
"${WORK_DIR}/real-groupdel" autostream

install -o root -g root -m 0755 "$(command -v install)" \
  "${WORK_DIR}/real-install"
cat > "${WORK_DIR}/signal-install" <<EOF
#!/bin/bash
set -euo pipefail
"${WORK_DIR}/real-install" "\$@"
if [[ "\$*" == *"/opt/autostream"* ]]; then
  printf '%s\n' delivered > "${WORK_DIR}/signal-install.executed"
  kill -TERM "\${PPID}"
fi
EOF
chmod 0755 "${WORK_DIR}/signal-install"
signal_directory_stage_before="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/signal-install' /usr/bin/install &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/signal-directory-rollback.out" 2>&1
signal_directory_status=$?
set -e
[[ ${signal_directory_status} -eq 143 ]] || \
  die "signal-interrupted directory mutation did not exit with deferred TERM status 143"
[[ -f ${WORK_DIR}/signal-install.executed ]] || \
  die "signal-interrupted directory mutation did not reach its injection boundary"
assert_failed_install_rollback_clean \
  directory-mutation \
  "${signal_directory_stage_before}" \
  "${WORK_DIR}/signal-directory-rollback.out"

install -o root -g root -m 0755 "$(command -v mktemp)" \
  "${WORK_DIR}/real-mktemp"
cat > "${WORK_DIR}/signal-mktemp" <<EOF
#!/bin/bash
set -euo pipefail
temporary_path="\$("${WORK_DIR}/real-mktemp" "\$@")"
printf '%s\n' "\${temporary_path}"
if [[ "\$*" == *".install-v9.9.9.XXXXXXXX"* ]]; then
  printf '%s\n' delivered > "${WORK_DIR}/signal-mktemp.executed"
  kill -TERM "\${PPID}"
fi
EOF
chmod 0755 "${WORK_DIR}/signal-mktemp"
signal_temporary_stage_before="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/signal-mktemp' /usr/bin/mktemp &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/signal-temporary-rollback.out" 2>&1
signal_temporary_status=$?
set -e
[[ ${signal_temporary_status} -eq 143 ]] || \
  die "signal-interrupted temporary allocation did not exit with deferred TERM status 143"
[[ -f ${WORK_DIR}/signal-mktemp.executed ]] || \
  die "signal-interrupted temporary allocation did not reach its injection boundary"
assert_failed_install_rollback_clean \
  temporary-allocation \
  "${signal_temporary_stage_before}" \
  "${WORK_DIR}/signal-temporary-rollback.out"

install -o root -g root -m 0755 "$(command -v mv)" \
  "${WORK_DIR}/real-mv"
cat > "${WORK_DIR}/signal-mv" <<EOF
#!/bin/bash
set -euo pipefail
"${WORK_DIR}/real-mv" "\$@"
last_argument="\${!#}"
if [[ \${last_argument} == "${CURRENT_LINK}" ]]; then
  printf '%s\n' delivered > "${WORK_DIR}/signal-mv.executed"
  kill -TERM "\${PPID}"
fi
EOF
chmod 0755 "${WORK_DIR}/signal-mv"
signal_link_stage_before="$(
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-control-panel-install.*' -printf '%f\n' |
    LC_ALL=C sort
)"
set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/signal-mv' /usr/bin/mv &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/signal-link-rollback.out" 2>&1
signal_link_status=$?
set -e
[[ ${signal_link_status} -eq 143 ]] || \
  die "signal-interrupted current-link mutation did not exit with deferred TERM status 143"
[[ -f ${WORK_DIR}/signal-mv.executed ]] || \
  die "signal-interrupted current-link mutation did not reach its injection boundary"
assert_failed_install_rollback_clean \
  current-link-mutation \
  "${signal_link_stage_before}" \
  "${WORK_DIR}/signal-link-rollback.out"

groupadd --system --gid 0 --non-unique autostream
hostile_group_before="$(getent group autostream)"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/hostile-gid-zero.out" 2>&1
hostile_gid_zero_status=$?
set -e
[[ ${hostile_gid_zero_status} -ne 0 ]] || \
  die "hostile GID 0 service group unexpectedly passed"
grep -F -- "autostream service group must not use GID 0" \
  "${WORK_DIR}/hostile-gid-zero.out" >/dev/null || \
  die "hostile GID 0 service group did not fail before user creation"
[[ $(getent group autostream) == "${hostile_group_before}" ]] || \
  die "hostile GID 0 changed the pre-existing service group"
if id autostream >/dev/null 2>&1; then
  die "hostile GID 0 mutated the service user or persistent paths"
fi
for hostile_gid_absent_path in \
  "${MANAGED_ROOT}" \
  "${STATE_DIR}" \
  "${ENV_PATH}" \
  "${UNIT_PATH}" \
  "${BACKUP_EXECUTABLE}" \
  "${DATABASE_BACKUP_DIR}" \
  "${INSTALL_BACKUP_ROOT}" \
  "${MARIADB_DEFAULTS}"; do
  [[ ! -e ${hostile_gid_absent_path} && ! -L ${hostile_gid_absent_path} ]] || \
    die "hostile GID 0 mutated the service user or persistent paths"
done
groupdel autostream

groupadd --system autostream
useradd --system --gid autostream --home-dir /var/lib/autostream \
  --no-create-home --shell /usr/sbin/nologin autostream
install -d -o root -g root -m 0700 /var/lib/autostream /etc/autostream
install -d -o autostream -g autostream -m 0700 "${STATE_DIR}"
printf '%s\n' 'preserve-existing-state-exactly' > "${STATE_DIR}/rollback-sentinel"
chown autostream:autostream "${STATE_DIR}/rollback-sentinel"
chmod 0600 "${STATE_DIR}/rollback-sentinel"
printf '%s\n' 'CONTROL_PANEL_INSTALLER_ROLLBACK_ENV=preserve-exactly' > "${ENV_PATH}"
chmod 0644 "${ENV_PATH}"
existing_state_metadata_before="$(stat -c '%d:%i:%u:%g:%a' -- "${STATE_DIR}")"
existing_state_sentinel_before="$(
  sha256sum -- "${STATE_DIR}/rollback-sentinel" | awk 'NR == 1 { print $1 }'
)"
existing_account_before="$(getent passwd autostream)"
existing_group_before="$(getent group autostream)"
existing_state_parent_before="$(stat -c '%d:%i:%u:%g:%a' -- /var/lib/autostream)"
existing_env_parent_before="$(stat -c '%d:%i:%u:%g:%a' -- /etc/autostream)"
existing_env_before="$(sha256sum -- "${ENV_PATH}" | awk 'NR == 1 { print $1 }')"

set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/late-env-existing-state.out" 2>&1
late_env_existing_state_status=$?
set -e
[[ ${late_env_existing_state_status} -ne 0 ]] || \
  die "late environment preflight with an existing state unexpectedly passed"
grep -F -- "existing environment file must be root-only or root-readable with mode 0600/0640" \
  "${WORK_DIR}/late-env-existing-state.out" >/dev/null || \
  die "late environment preflight did not fail at the expected boundary"
[[ $(stat -c '%d:%i:%u:%g:%a' -- "${STATE_DIR}") == \
  "${existing_state_metadata_before}" ]] || \
  die "late environment preflight changed the existing state directory"
[[ $(sha256sum -- "${STATE_DIR}/rollback-sentinel" | awk 'NR == 1 { print $1 }') == \
  "${existing_state_sentinel_before}" ]] || \
  die "late environment preflight changed the existing state directory"
[[ $(getent passwd autostream) == "${existing_account_before}" &&
  $(getent group autostream) == "${existing_group_before}" ]] || \
  die "late environment preflight changed the existing service account"
[[ $(stat -c '%d:%i:%u:%g:%a' -- /var/lib/autostream) == \
  "${existing_state_parent_before}" ]] || \
  die "late environment preflight changed the existing state boundary"
[[ $(stat -c '%d:%i:%u:%g:%a' -- /etc/autostream) == \
  "${existing_env_parent_before}" &&
  $(sha256sum -- "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == \
    "${existing_env_before}" ]] || \
  die "late environment preflight changed the existing environment boundary"
for rollback_absent_path in \
  /opt/autostream \
  /var/backups/autostream \
  /etc/autostream-local-executor; do
  [[ ! -e ${rollback_absent_path} && ! -L ${rollback_absent_path} ]] || \
    die "late environment preflight left persistent installer state"
done
[[ -d /run/autostream-updater &&
  ! -L /run/autostream-updater &&
  $(stat -c '%U:%G:%a' -- /run/autostream-updater) == "root:root:700" &&
  -f ${SHARED_HOST_SETUP_LOCK} &&
  ! -L ${SHARED_HOST_SETUP_LOCK} &&
  $(stat -c '%U:%G:%a' -- "${SHARED_HOST_SETUP_LOCK}") == "root:root:600" &&
  -f ${TARGET_LOCK} &&
  ! -L ${TARGET_LOCK} &&
  $(stat -c '%U:%G:%a' -- "${TARGET_LOCK}") == "root:root:600" ]] || \
  die "late environment preflight left an unsafe runtime lock boundary"

rm -f -- "${ENV_PATH}" "${STATE_DIR}/rollback-sentinel"
rmdir "${STATE_DIR}" /var/lib/autostream /etc/autostream
userdel autostream
if getent group autostream >/dev/null 2>&1; then
  groupdel autostream
fi
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "fixture account teardown left the service account behind"
fi

install -d -o root -g root -m 0700 /etc/autostream
printf '%s\n' 'CONTROL_PANEL_INSTALLER_ROLLBACK_ENV=fresh-account' > "${ENV_PATH}"
chmod 0644 "${ENV_PATH}"
fresh_failure_env_parent_before="$(stat -c '%d:%i:%u:%g:%a' -- /etc/autostream)"
fresh_failure_env_before="$(sha256sum -- "${ENV_PATH}" | awk 'NR == 1 { print $1 }')"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/late-env-fresh-account.out" 2>&1
late_env_fresh_account_status=$?
set -e
[[ ${late_env_fresh_account_status} -ne 0 ]] || \
  die "late environment preflight with a fresh account unexpectedly passed"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "late environment preflight left a fresh service account"
fi
[[ $(stat -c '%d:%i:%u:%g:%a' -- /etc/autostream) == \
  "${fresh_failure_env_parent_before}" &&
  $(sha256sum -- "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == \
    "${fresh_failure_env_before}" ]] || \
  die "late environment preflight changed the fresh environment boundary"
for rollback_absent_path in \
  /opt/autostream \
  /var/lib/autostream \
  /var/backups/autostream \
  /etc/autostream-local-executor; do
  [[ ! -e ${rollback_absent_path} && ! -L ${rollback_absent_path} ]] || \
    die "late environment preflight left persistent installer state"
done
rm -f -- "${ENV_PATH}"
rmdir /etc/autostream

"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/fresh.out"
[[ -L ${PUBLIC_BINARY} && -L ${PUBLIC_WEB} ]] || \
  die "fresh install did not install stable public links"
fresh_release="$(readlink -f -- "${MANAGED_ROOT}/current")"
[[ ${fresh_release} == "${MANAGED_ROOT}/releases/"* ]] || \
  die "fresh managed release resolved outside the release root"
for directory_path in \
  "${fresh_release}" \
  "${fresh_release}/bin" \
  "${fresh_release}/backup" \
  "${fresh_release}/share" \
  "${fresh_release}/share/autostream-control-panel" \
  "${fresh_release}/systemd"; do
  [[ $(stat -c '%U:%G:%a' -- "${directory_path}") == "root:root:755" ]] || \
    die "fresh managed directory was not normalized to root:root mode 0755"
done
for executable_path in \
  "${fresh_release}/bin/control-panel" \
  "${fresh_release}/bin/autostream-updater" \
  "${fresh_release}/backup/autostream-backup-control-panel" \
  "${fresh_release}/install-autostream-control-panel"; do
  [[ $(stat -c '%U:%G:%a' -- "${executable_path}") == "root:root:755" ]] || \
    die "fresh managed executable was not normalized to root:root mode 0755"
done
for regular_path in \
  "${fresh_release}/.env.example" \
  "${fresh_release}/artifact-manifest.json" \
  "${fresh_release}/checksums.txt" \
  "${fresh_release}/share/autostream-control-panel/index.html" \
  "${fresh_release}/systemd/autostream-control-panel.service.example"; do
  [[ $(stat -c '%U:%G:%a' -- "${regular_path}") == "root:root:644" ]] || \
    die "fresh managed regular file was not normalized to root:root mode 0644"
done
for marker_path in \
  "${fresh_release}/.artifact-sha256" \
  "${fresh_release}/.version"; do
  [[ $(stat -c '%U:%G:%a' -- "${marker_path}") == "root:root:444" ]] || \
    die "fresh managed marker was not normalized to root:root mode 0444"
done
runuser -u autostream -- "${fresh_release}/bin/control-panel" --version |
  grep -Fx -- "autostream-control-panel ${VERSION}" >/dev/null || \
  die "fresh managed release was not runnable by autostream"
snapshot_managed_release_tree "${fresh_release}" "${WORK_DIR}/fresh-release.before"
"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/fresh-idempotent.out"
snapshot_managed_release_tree "${fresh_release}" "${WORK_DIR}/fresh-release.after"
cmp -s -- "${WORK_DIR}/fresh-release.before" "${WORK_DIR}/fresh-release.after" || \
  die "idempotent reinstall changed existing managed release metadata or content"
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

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "${UNIT_PATH}"
install_runtime_unit_exclusive
systemctl daemon-reload
fixture_service_start_attempted=true
systemctl start "${UNIT}"
record_fixture_process_identity "$(systemctl show --property MainPID --value "${UNIT}")"
kill -0 "${old_pid}" || die "legacy service PID is not alive"
assert_legacy_runtime_unit_loaded
legacy_unit_file_state="$(systemctl is-enabled "${UNIT}" 2>/dev/null || true)"
[[ ${legacy_unit_file_state} == "disabled" ]] || \
  die "legacy fixture must begin disabled, got ${legacy_unit_file_state:-unknown}"

env_before="$(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }')"
db_before="$(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }')"
unit_before="$(sha256sum "${UNIT_PATH}" | awk 'NR == 1 { print $1 }')"
helper_before="$(sha256sum "${BACKUP_EXECUTABLE}" | awk 'NR == 1 { print $1 }')"

legacy_backup_dir="${INSTALL_BACKUP_ROOT}/${VERSION}-${archive_sha256:0:12}"
install -d -o root -g root -m 0700 "${legacy_backup_dir}"
install -o root -g root -m 0644 "${UNIT_PATH}" \
  "${legacy_backup_dir}/autostream-control-panel.service"
install -o root -g root -m 0700 "${BACKUP_EXECUTABLE}" \
  "${legacy_backup_dir}/autostream-backup-control-panel"
install -o root -g root -m 0755 "${PUBLIC_BINARY}" \
  "${legacy_backup_dir}/control-panel"
legacy_binary_live_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${PUBLIC_BINARY}"
)"
legacy_binary_live_hash_before="$(
  sha256sum -- "${PUBLIC_BINARY}" | awk 'NR == 1 { print $1 }'
)"
chown 65534:65534 "${legacy_backup_dir}/control-panel"
nonroot_legacy_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/control-panel"
)"
nonroot_legacy_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/control-panel" | awk 'NR == 1 { print $1 }'
)"
nonroot_state_metadata_before="$(stat -c '%d:%i:%u:%g:%a' -- "${STATE_DIR}")"
nonroot_account_before="$(getent passwd autostream)"
nonroot_group_before="$(getent group autostream)"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/nonroot-legacy-public-backup.out" 2>&1
nonroot_legacy_public_backup_status=$?
set -e
[[ ${nonroot_legacy_public_backup_status} -ne 0 ]] || \
  die "non-root legacy public backup unexpectedly passed"
grep -F -- "legacy public backup must be owned by root:root" \
  "${WORK_DIR}/nonroot-legacy-public-backup.out" >/dev/null || \
  die "non-root legacy public backup did not fail at the ownership boundary"
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- "${PUBLIC_BINARY}") == \
  "${legacy_binary_live_metadata_before}" &&
  $(sha256sum -- "${PUBLIC_BINARY}" | awk 'NR == 1 { print $1 }') == \
    "${legacy_binary_live_hash_before}" ]] || \
  die "non-root legacy public backup changed the live or backup boundary"
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/control-panel") == \
  "${nonroot_legacy_backup_metadata_before}" &&
  $(sha256sum -- "${legacy_backup_dir}/control-panel" | awk 'NR == 1 { print $1 }') == \
    "${nonroot_legacy_backup_hash_before}" ]] || \
  die "non-root legacy public backup changed the live or backup boundary"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "non-root legacy public backup changed the running legacy process"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} &&
  $(stat -c '%d:%i:%u:%g:%a' -- "${STATE_DIR}") == "${nonroot_state_metadata_before}" &&
  $(getent passwd autostream) == "${nonroot_account_before}" &&
  $(getent group autostream) == "${nonroot_group_before}" ]] || \
  die "non-root legacy public backup changed persistent installer state"
chown root:root "${legacy_backup_dir}/control-panel"
legacy_unit_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/autostream-control-panel.service"
)"
legacy_unit_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/autostream-control-panel.service" | awk 'NR == 1 { print $1 }'
)"
legacy_helper_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/autostream-backup-control-panel"
)"
legacy_helper_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/autostream-backup-control-panel" | awk 'NR == 1 { print $1 }'
)"
legacy_binary_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/control-panel"
)"
legacy_binary_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/control-panel" | awk 'NR == 1 { print $1 }'
)"

(
  exec 7<>"${TARGET_LOCK}"
  flock -n 7 || die "test could not acquire the updater target lock"
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
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- \
  "${legacy_backup_dir}/autostream-control-panel.service") == \
    "${legacy_unit_backup_metadata_before}" &&
  $(sha256sum -- "${legacy_backup_dir}/autostream-control-panel.service" |
    awk 'NR == 1 { print $1 }') == "${legacy_unit_backup_hash_before}" ]] || \
  die "late failure removed or changed a pre-existing legacy backup"
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- \
  "${legacy_backup_dir}/autostream-backup-control-panel") == \
    "${legacy_helper_backup_metadata_before}" &&
  $(sha256sum -- "${legacy_backup_dir}/autostream-backup-control-panel" |
    awk 'NR == 1 { print $1 }') == "${legacy_helper_backup_hash_before}" ]] || \
  die "late failure removed or changed a pre-existing legacy backup"
assert_legacy_runtime_unit_loaded
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
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/control-panel") == \
  "${legacy_binary_backup_metadata_before}" &&
  $(sha256sum -- "${legacy_backup_dir}/control-panel" |
    awk 'NR == 1 { print $1 }') == "${legacy_binary_backup_hash_before}" ]] || \
  die "late failure removed or changed a pre-existing legacy backup"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "sync failure rollback replaced the running legacy process"
assert_legacy_runtime_unit_loaded
kill -0 "${old_pid}" || die "sync failure rollback stopped the running legacy process"

"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/migration.out"
runtime_race_fragment_before="$(systemctl show --property FragmentPath --value "${UNIT}")"
runtime_race_exec_start_before="$(systemctl show --property ExecStart --value "${UNIT}")"
runtime_race_user_before="$(systemctl show --property User --value "${UNIT}")"
runtime_race_pid_before="$(systemctl show --property MainPID --value "${UNIT}")"
runtime_race_enabled_before="$(systemctl is-enabled "${UNIT}" 2>/dev/null || true)"
runtime_sync_precommit_hook=replace_runtime_unit_for_precommit_probe
set +e
replace_owned_runtime_unit
runtime_race_status=$?
set -e
runtime_sync_precommit_hook=""
[[ ${runtime_race_status} -eq 75 ]] || \
  die "runtime precommit race unexpectedly committed"
[[ ${runtime_race_active} == true ]] || \
  die "runtime precommit race did not retain recovery ownership"
[[ $(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == \
  "${runtime_race_foreign_identity}" ]] || \
  die "runtime precommit race changed the foreign unit inode"
[[ $(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }') == \
  "${runtime_race_foreign_hash}" ]] || \
  die "runtime precommit race changed the foreign unit hash"
[[ $(systemctl show --property FragmentPath --value "${UNIT}") == \
  "${runtime_race_fragment_before}" ]] || \
  die "runtime precommit race changed PID1 FragmentPath"
[[ $(systemctl show --property ExecStart --value "${UNIT}") == \
  "${runtime_race_exec_start_before}" ]] || \
  die "runtime precommit race changed PID1 ExecStart"
[[ $(systemctl show --property User --value "${UNIT}") == \
  "${runtime_race_user_before}" ]] || \
  die "runtime precommit race changed PID1 User"
[[ $(systemctl show --property MainPID --value "${UNIT}") == \
  "${runtime_race_pid_before}" ]] || \
  die "runtime precommit race changed PID1 MainPID"
[[ $(systemctl is-enabled "${UNIT}" 2>/dev/null || true) == \
  "${runtime_race_enabled_before}" ]] || \
  die "runtime precommit race changed the enabled state"
kill -0 "${old_pid}" || die "runtime precommit race stopped the legacy process"
restore_runtime_sync_race || die "could not restore the owned runtime unit after the race probe"
[[ $(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }') == \
  "${unit_before}" ]] || die "runtime race probe did not restore the legacy unit"

runtime_cleanup_preremove_hook=replace_runtime_unit_for_precommit_probe
set +e
remove_owned_runtime_unit_for_cleanup
runtime_cleanup_race_status=$?
set -e
runtime_cleanup_preremove_hook=""
[[ ${runtime_cleanup_race_status} -eq 75 ]] || \
  die "cleanup pre-remove race unexpectedly removed or accepted a foreign unit"
[[ ${runtime_race_active} == true ]] || \
  die "cleanup pre-remove race did not retain recovery ownership"
[[ $(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == \
  "${runtime_race_foreign_identity}" ]] || \
  die "cleanup pre-remove race changed the foreign unit inode"
[[ $(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }') == \
  "${runtime_race_foreign_hash}" ]] || \
  die "cleanup pre-remove race changed the foreign unit hash"
[[ $(systemctl show --property FragmentPath --value "${UNIT}") == \
  "${runtime_race_fragment_before}" ]] || \
  die "cleanup pre-remove race changed PID1 FragmentPath"
[[ $(systemctl show --property ExecStart --value "${UNIT}") == \
  "${runtime_race_exec_start_before}" ]] || \
  die "cleanup pre-remove race changed PID1 ExecStart"
[[ $(systemctl show --property User --value "${UNIT}") == \
  "${runtime_race_user_before}" ]] || \
  die "cleanup pre-remove race changed PID1 User"
[[ $(systemctl show --property MainPID --value "${UNIT}") == \
  "${runtime_race_pid_before}" ]] || \
  die "cleanup pre-remove race changed PID1 MainPID"
[[ $(systemctl is-enabled "${UNIT}" 2>/dev/null || true) == \
  "${runtime_race_enabled_before}" ]] || \
  die "cleanup pre-remove race changed the enabled state"
kill -0 "${old_pid}" || die "cleanup pre-remove race stopped the legacy process"
restore_runtime_sync_race || \
  die "could not restore the owned runtime unit after the cleanup race probe"
[[ $(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }') == \
  "${unit_before}" ]] || die "cleanup race probe did not restore the legacy unit"

replace_owned_runtime_unit
systemctl daemon-reload
assert_managed_runtime_unit_loaded
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
assert_managed_runtime_unit_loaded
systemctl is-enabled --quiet "${UNIT}" && die "idempotent reinstall unexpectedly enabled the service"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "idempotent reinstall replaced the running legacy process"
[[ $(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == "${env_before}" ]] || \
  die "idempotent reinstall changed the existing environment"
[[ $(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }') == "${db_before}" ]] || \
  die "idempotent reinstall changed the existing MariaDB defaults"

printf '%s\n' "Control Panel installer integration scenarios passed."
