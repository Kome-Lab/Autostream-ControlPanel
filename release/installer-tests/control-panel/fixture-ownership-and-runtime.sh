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

snapshot_legacy_web_tree() {
  local web_dir=$1
  local output_path=$2
  [[ -d ${web_dir} && ! -L ${web_dir} ]] || return 1
  tar --sort=name --numeric-owner -cf "${output_path}" -C "${web_dir}" .
}
