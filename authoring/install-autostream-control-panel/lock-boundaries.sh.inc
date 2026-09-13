
require_absent_or_secure_root_directory() {
  local path=$1
  if [[ -e ${path} || -L ${path} ]]; then
    require_secure_root_directory "${path}"
  fi
}

ensure_permanent_lock_directory() {
  local path=$1

  # updater target lock is permanent installer coordination state.
  if [[ -e ${path} || -L ${path} ]]; then
    [[ -d ${path} &&
      ! -L ${path} &&
      $(readlink -f -- "${path}") == "${path}" &&
      $(stat -c '%U:%G:%a' -- "${path}") == "root:root:700" ]] || \
      die "permanent updater lock directory is unsafe: ${path}"
  else
    begin_installer_signal_transaction
    install -d -o root -g root -m 0700 "${path}"
    [[ -d ${path} &&
      ! -L ${path} &&
      $(readlink -f -- "${path}") == "${path}" &&
      $(stat -c '%U:%G:%a' -- "${path}") == "root:root:700" ]] || \
      die "could not create the permanent updater lock directory"
    sync -f "$(dirname -- "${path}")"
    finish_installer_signal_transaction
  fi
}

ensure_permanent_lock_path_atomically() {
  local path=$1
  local lock_mode

  if [[ ! -e ${path} && ! -L ${path} ]]; then
    create_journaled_temporary_path lock_create_stage file \
      /run/autostream-updater/.lock-create.XXXXXXXX || \
      die "could not create an updater lock candidate"
    [[ -f ${lock_create_stage} &&
      ! -L ${lock_create_stage} &&
      $(stat -c '%U:%G:%a' -- "${lock_create_stage}") == "root:root:600" ]] || \
      die "could not create a safe updater lock candidate"
    if ln -- "${lock_create_stage}" "${path}" 2>/dev/null; then
      sync -f /run/autostream-updater
    elif [[ ! -e ${path} && ! -L ${path} ]]; then
      die "could not atomically create a permanent updater lock"
    fi
    rm -f -- "${lock_create_stage}"
    lock_create_stage=""
  fi
  [[ -f ${path} && ! -L ${path} ]] || \
    die "updater lock path is unsafe: ${path}"
  [[ $(stat -c '%U:%G' -- "${path}") == "root:root" ]] || \
    die "updater lock must be owned by root:root: ${path}"
  lock_mode="$(stat -c '%a' -- "${path}")"
  (( (8#${lock_mode} & 0077) == 0 )) || \
    die "updater lock must not grant group/other permissions: ${path}"
}

for managed_parent in \
  /opt/autostream \
  "${MANAGED_ROOT}" \
  "${RELEASES_DIR}" \
  /var/lib/autostream \
  /etc/autostream \
  /etc/autostream-local-executor \
  /var/backups/autostream \
  /var/backups/autostream/install-migrations \
  "${INSTALL_BACKUP_ROOT}" \
  "${DATABASE_BACKUP_DIR}" \
  /run/autostream-updater; do
  require_absent_or_secure_root_directory "${managed_parent}"
done

existing_state_directory=false
if [[ -e ${STATE_DIR} || -L ${STATE_DIR} ]]; then
  [[ -d ${STATE_DIR} && ! -L ${STATE_DIR} ]] || \
    die "existing service state path is not a safe directory: ${STATE_DIR}"
  [[ $(readlink -f -- "${STATE_DIR}") == "${STATE_DIR}" ]] || \
    die "existing service state directory does not resolve to its fixed path: ${STATE_DIR}"
  state_mode="$(stat -c '%a' -- "${STATE_DIR}")"
  (( (8#${state_mode} & 07022) == 0 )) || \
    die "existing service state directory has unsafe mode bits: ${STATE_DIR}"
  existing_state_directory=true
fi

ensure_permanent_lock_directory /run/autostream-updater
readonly SHARED_HOST_SETUP_LOCK="/run/autostream-updater/.autostream-runtime-host-setup.lock"
ensure_permanent_lock_path_atomically "${SHARED_HOST_SETUP_LOCK}"
# A published lock pathname is intentionally persistent. Unlinking it while
# another process has the inode open can split mutual exclusion across inodes.
exec 8<>"${SHARED_HOST_SETUP_LOCK}"
shared_lock_opened=true
chown root:root /proc/self/fd/8
chmod 0600 /proc/self/fd/8
shared_lock_fd_identity="$(stat -Lc '%d:%i' -- /proc/self/fd/8)"
[[ -f ${SHARED_HOST_SETUP_LOCK} && ! -L ${SHARED_HOST_SETUP_LOCK} &&
  -f /proc/self/fd/8 &&
  $(stat -Lc '%U:%G:%a' -- /proc/self/fd/8) == "root:root:600" &&
  $(stat -c '%U:%G:%a' -- "${SHARED_HOST_SETUP_LOCK}") == "root:root:600" &&
  ${shared_lock_fd_identity} == "$(stat -c '%d:%i' -- "${SHARED_HOST_SETUP_LOCK}")" ]] || \
  die "shared host-setup lock identity changed while being opened"
flock -n 8 || die "another AutoStream installer is provisioning shared host state"
[[ -f ${SHARED_HOST_SETUP_LOCK} && ! -L ${SHARED_HOST_SETUP_LOCK} &&
  -f /proc/self/fd/8 &&
  $(stat -Lc '%U:%G:%a' -- /proc/self/fd/8) == "root:root:600" &&
  $(stat -c '%U:%G:%a' -- "${SHARED_HOST_SETUP_LOCK}") == "root:root:600" &&
  ${shared_lock_fd_identity} == "$(stat -c '%d:%i' -- "${SHARED_HOST_SETUP_LOCK}")" ]] || \
  die "shared host-setup lock identity changed after acquisition"

TARGET_LOCK_ID="$(
  printf '%s' "${UNIT_NAME}" | sha256sum | awk 'NR == 1 { print substr($1, 1, 12) }'
)" || die "could not derive the updater target lock identifier"
[[ ${TARGET_LOCK_ID} =~ ^[0-9a-f]{12}$ ]] || die "could not derive the updater target lock identifier"
readonly TARGET_LOCK_ID
readonly TARGET_LOCK="/run/autostream-updater/.autostream-updater-${TARGET_LOCK_ID}.lock"
ensure_permanent_lock_path_atomically "${TARGET_LOCK}"
exec 9<>"${TARGET_LOCK}"
target_lock_opened=true
chown root:root /proc/self/fd/9
chmod 0600 /proc/self/fd/9
target_lock_fd_identity="$(stat -Lc '%d:%i' -- /proc/self/fd/9)"
[[ -f ${TARGET_LOCK} && ! -L ${TARGET_LOCK} &&
  -f /proc/self/fd/9 &&
  $(stat -Lc '%U:%G:%a' -- /proc/self/fd/9) == "root:root:600" &&
  $(stat -c '%U:%G:%a' -- "${TARGET_LOCK}") == "root:root:600" &&
  ${target_lock_fd_identity} == "$(stat -c '%d:%i' -- "${TARGET_LOCK}")" ]] || \
  die "updater target lock identity changed while being opened"
flock -n 9 || die "another privileged update is already active for ${UNIT_NAME}"
[[ -f ${TARGET_LOCK} && ! -L ${TARGET_LOCK} &&
  -f /proc/self/fd/9 &&
  $(stat -Lc '%U:%G:%a' -- /proc/self/fd/9) == "root:root:600" &&
  $(stat -c '%U:%G:%a' -- "${TARGET_LOCK}") == "root:root:600" &&
  ${target_lock_fd_identity} == "$(stat -c '%d:%i' -- "${TARGET_LOCK}")" ]] || \
  die "updater target lock identity changed after acquisition"
