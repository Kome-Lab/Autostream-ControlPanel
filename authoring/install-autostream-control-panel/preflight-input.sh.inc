#!/bin/bash
set -euo pipefail

umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LC_ALL=C

readonly SERVICE_NAME="control-panel"
readonly ARTIFACT_PREFIX="autostream-control-panel"
readonly ARTIFACT_MANIFEST_NAME="artifact-manifest.json"
readonly MAX_ARCHIVE_SIZE=268435456
readonly MANAGED_ROOT="/opt/autostream/control-panel"
readonly RELEASES_DIR="${MANAGED_ROOT}/releases"
readonly CURRENT_LINK="${MANAGED_ROOT}/current"
readonly PUBLIC_BINARY="/usr/local/bin/control-panel"
readonly PUBLIC_WEB="/usr/share/autostream-control-panel"
readonly ENV_DEST="/etc/autostream/control-panel.env"
readonly UNIT_DEST="/etc/systemd/system/autostream-control-panel.service"
readonly UNIT_NAME="autostream-control-panel.service"
readonly STATE_DIR="/var/lib/autostream/control-panel"
readonly INSTALL_BACKUP_ROOT="/var/backups/autostream/install-migrations/control-panel"
readonly BACKUP_EXECUTABLE="/usr/local/sbin/autostream-backup-control-panel"
readonly DATABASE_BACKUP_DIR="/var/backups/autostream/control-panel"
readonly MARIADB_DEFAULTS="/etc/autostream-local-executor/mariadb-backup.cnf"
readonly AUTOSTREAM_USER_ROLLBACK_LOGIN="autostream-install-rollback"

die() {
  printf 'install-autostream-control-panel: %s\n' "$*" >&2
  exit 1
}

[[ ${EUID} -eq 0 ]] || die "must run as root (use sudo)"
[[ $# -eq 0 ]] || die "this installer does not accept arguments"

for command in \
  awk basename chmod chown cmp cp dirname find getent grep groupadd groupdel id install jq \
  flock ln mktemp mv readlink rm rmdir runuser sha256sum sort stat sync systemctl tar uname uniq useradd userdel usermod; do
  command -v "${command}" >/dev/null 2>&1 || die "required command is unavailable: ${command}"
done
[[ -f /usr/bin/mariadb-dump && ! -L /usr/bin/mariadb-dump && -x /usr/bin/mariadb-dump ]] || \
  die "required MariaDB dump executable is missing or unsafe: /usr/bin/mariadb-dump"

require_secure_root_directory() {
  local path=$1
  local mode
  local resolved
  [[ -d ${path} && ! -L ${path} ]] || \
    die "required system directory is missing or unsafe: ${path}"
  resolved="$(readlink -f -- "${path}")" || \
    die "required system directory cannot be resolved: ${path}"
  [[ ${resolved} == "${path}" ]] || \
    die "required system directory does not resolve to its fixed path: ${path}"
  [[ $(stat -c '%U:%G' -- "${path}") == "root:root" ]] || \
    die "required system directory must be owned by root:root: ${path}"
  mode="$(stat -c '%a' -- "${path}")"
  (( (8#${mode} & 07022) == 0 )) || \
    die "required system directory has unsafe mode bits: ${path}"
}

# Reject unsafe immutable anchors before staging or executing any release input.
for fixed_parent in \
  / \
  /opt \
  /usr \
  /usr/local \
  /usr/local/bin \
  /usr/local/sbin \
  /usr/share \
  /etc \
  /etc/systemd \
  /etc/systemd/system \
  /var \
  /var/lib \
  /var/backups \
  /run; do
  require_secure_root_directory "${fixed_parent}"
done

# Reject the specialized state-path type boundary before release processing or
# generic managed-parent validation can obscure it.
if [[ -L ${STATE_DIR} ]]; then
  die "existing service state path is not a safe directory: ${STATE_DIR}"
fi

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)" || \
  die "could not resolve the installer directory"
[[ -n ${SCRIPT_DIR} && -d ${SCRIPT_DIR} && ! -L ${SCRIPT_DIR} ]] || \
  die "installer directory is missing or unsafe"
readonly SCRIPT_DIR
ARTIFACT_ID="$(basename -- "${SCRIPT_DIR}")" || die "could not resolve the release directory name"
readonly ARTIFACT_ID
if [[ ! ${ARTIFACT_ID} =~ ^autostream-control-panel_(v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?)_linux_(amd64|arm64)$ ]]; then
  die "the extracted directory name is invalid: ${ARTIFACT_ID}"
fi
readonly VERSION="${BASH_REMATCH[1]}"
readonly ARCH="${BASH_REMATCH[4]}"

case "$(uname -m)" in
  x86_64) readonly HOST_ARCH="amd64" ;;
  aarch64|arm64) readonly HOST_ARCH="arm64" ;;
  *) die "unsupported host architecture: $(uname -m)" ;;
esac
[[ ${ARCH} == "${HOST_ARCH}" ]] || die "artifact architecture ${ARCH} does not match host ${HOST_ARCH}"

ARTIFACT_PARENT="$(dirname -- "${SCRIPT_DIR}")" || die "could not resolve the release parent directory"
readonly ARTIFACT_PARENT
readonly ARCHIVE_NAME="${ARTIFACT_ID}.tar.gz"
readonly ARCHIVE_SOURCE="${ARTIFACT_PARENT}/${ARCHIVE_NAME}"

[[ -f ${ARCHIVE_SOURCE} && ! -L ${ARCHIVE_SOURCE} ]] || \
  die "required release archive is missing or unsafe: ${ARCHIVE_SOURCE}"
ARTIFACT_SOURCE_SIZE="$(stat -c %s -- "${ARCHIVE_SOURCE}")" || \
  die "could not determine the source archive size"
[[ ${ARTIFACT_SOURCE_SIZE} =~ ^[1-9][0-9]*$ ]] || die "source archive size is invalid"
[[ ${ARTIFACT_SOURCE_SIZE} -le ${MAX_ARCHIVE_SIZE} ]] || \
  die "source archive exceeds the ${MAX_ARCHIVE_SIZE}-byte limit"
readonly ARTIFACT_SOURCE_SIZE

cleanup_running=false
signal_transaction_active=false
deferred_termination_status=0

handle_installer_signal() {
  local status=$1
  if [[ ${cleanup_running} == true ]]; then
    trap '' HUP INT TERM
    return
  fi
  if [[ ${signal_transaction_active} == true ]]; then
    if [[ ${deferred_termination_status} -eq 0 ]]; then
      deferred_termination_status="${status}"
    fi
    return
  fi
  trap '' HUP INT TERM
  exit "${status}"
}

restore_installer_signal_traps() {
  if [[ ${cleanup_running} == true ]]; then
    trap '' HUP INT TERM
  else
    trap 'handle_installer_signal 129' HUP
    trap 'handle_installer_signal 130' INT
    trap 'handle_installer_signal 143' TERM
  fi
}

begin_installer_signal_transaction() {
  [[ ${signal_transaction_active} == false ]] || \
    die "nested installer signal transaction is not allowed"
  signal_transaction_active=true
}

finish_installer_signal_transaction() {
  local pending_status
  signal_transaction_active=false
  pending_status="${deferred_termination_status}"
  deferred_termination_status=0
  if [[ ${pending_status} -ne 0 ]]; then
    handle_installer_signal "${pending_status}"
  fi
}

INPUT_STAGE=""
INPUT_STAGE_IDENTITY=""

input_stage_is_owned() {
  [[ -n ${INPUT_STAGE} &&
    -n ${INPUT_STAGE_IDENTITY} &&
    -d ${INPUT_STAGE} &&
    ! -L ${INPUT_STAGE} &&
    $(readlink -f -- "${INPUT_STAGE}") == "${INPUT_STAGE}" &&
    $(stat -c '%d:%i' -- "${INPUT_STAGE}") == "${INPUT_STAGE_IDENTITY}" ]]
}

cleanup_early_input_stage() {
  trap '' HUP INT TERM
  cleanup_running=true
  signal_transaction_active=false
  deferred_termination_status=0
  trap - EXIT
  local status=$1
  set +e
  if [[ -z ${INPUT_STAGE} ]]; then
    :
  elif input_stage_is_owned; then
    rm -rf -- "${INPUT_STAGE}"
  else
    printf 'install-autostream-control-panel: refusing to remove replaced private input staging\n' >&2
    status=1
  fi
  exit "${status}"
}

trap 'cleanup_early_input_stage "$?"' EXIT
restore_installer_signal_traps

# The private recovery root is created before the full rollback journal exists.
# Defer termination until its exact inode is captured and the early EXIT cleanup
# can remove it without guessing ownership.
input_stage_status=0
begin_installer_signal_transaction
if INPUT_STAGE="$(mktemp -d /var/tmp/autostream-control-panel-install.XXXXXXXX)"; then
  :
else
  input_stage_status=$?
fi
if [[ -n ${INPUT_STAGE} &&
  ${INPUT_STAGE} == /var/tmp/autostream-control-panel-install.* &&
  -d ${INPUT_STAGE} &&
  ! -L ${INPUT_STAGE} &&
  $(readlink -f -- "${INPUT_STAGE}") == "${INPUT_STAGE}" &&
  $(stat -c '%U:%G:%a' -- "${INPUT_STAGE}") == "root:root:700" ]]; then
  INPUT_STAGE_IDENTITY="$(stat -c '%d:%i' -- "${INPUT_STAGE}")"
elif [[ ${input_stage_status} -eq 0 ]]; then
  input_stage_status=1
fi
finish_installer_signal_transaction
[[ ${input_stage_status} -eq 0 ]] || \
  die "could not create the private input staging directory safely (mktemp status ${input_stage_status})"
readonly INPUT_STAGE
readonly INPUT_STAGE_IDENTITY
readonly UNPACK_DIR="${INPUT_STAGE}/unpack"

managed_candidate=""
installation_complete=false
current_changed=false
previous_current_target=""
unit_stage=""
backup_exec_stage=""
env_stage=""
backup_config_stage=""
unit_changed=false
backup_exec_changed=false
env_created=false
backup_config_created=false
unit_previous_kind="absent"
backup_exec_previous_kind="absent"
unit_install_needed=true
backup_exec_install_needed=true
unit_backup=""
backup_exec_backup=""
unit_backup_created=false
unit_backup_identity=""
backup_exec_backup_created=false
backup_exec_backup_identity=""
service_user_created=false
service_group_created=false
created_service_uid=""
created_service_gid=""
created_service_user_record=""
created_service_group_record=""
preexisting_service_group_record=""
service_user_rollback_login_ready=false
release_dir_created=false
release_dir_created_identity=""
lock_create_stage=""
shared_lock_opened=false
target_lock_opened=false
current_next=""
current_next_identity=""
current_next_target=""
public_next=""
public_next_identity=""
public_next_target=""
declare -a changed_public_paths=()
declare -a previous_public_kinds=()
declare -a previous_public_targets=()
declare -a previous_public_backups=()
declare -a previous_public_backup_created=()
declare -a previous_public_backup_complete=()
declare -a previous_public_backup_identities=()
declare -a previous_public_source_identities=()
declare -a previous_public_backup_metadata=()
declare -a previous_public_backup_sha256=()
declare -a previous_public_uids=()
declare -a previous_public_gids=()
declare -a previous_public_modes=()
declare -a previous_public_sha256=()
declare -a previous_public_tree_sha256=()
declare -a installed_public_targets=()
declare -a installed_public_identities=()
declare -a prepared_directory_paths=()
declare -a prepared_directory_existed=()
declare -a prepared_directory_identities=()
declare -a prepared_directory_uids=()
declare -a prepared_directory_gids=()
declare -a prepared_directory_modes=()
declare -A prepared_directory_seen=()
declare -a created_backup_paths=()
declare -a created_backup_identities=()
declare -A preexisting_public_backup_identities=()
declare -A preexisting_public_backup_metadata=()
declare -A preexisting_public_backup_sha256=()
last_created_backup_identity=""
