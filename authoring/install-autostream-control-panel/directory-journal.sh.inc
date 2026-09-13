
sync_installer_filesystems() {
  local path
  local failed=false
  for path in \
    "${MANAGED_ROOT}" \
    /usr/local/bin \
    /usr/local/sbin \
    /usr/share \
    /etc/systemd/system \
    /etc/autostream \
    /etc/autostream-local-executor \
    /var/backups/autostream; do
    if [[ -d ${path} && ! -L ${path} ]]; then
      sync -f "${path}" || failed=true
    fi
  done
  [[ ${failed} == false ]]
}

create_journaled_temporary_path() {
  local output_variable=$1
  local path_kind=$2
  local template=$3
  local path=""
  local create_status=0

  # mktemp and publishing its path to the parent-shell rollback journal are one
  # deferred-signal transaction. This helper must be called directly, never from
  # command substitution, or the by-reference journal update would be lost.
  begin_installer_signal_transaction
  if [[ ${path_kind} == "directory" ]]; then
    if path="$(mktemp -d "${template}")"; then
      [[ -d ${path} && ! -L ${path} ]] || create_status=1
    else
      create_status=$?
    fi
  elif [[ ${path_kind} == "file" ]]; then
    if path="$(mktemp "${template}")"; then
      [[ -f ${path} && ! -L ${path} ]] || create_status=1
    else
      create_status=$?
    fi
  else
    create_status=1
  fi
  if [[ -n ${path} &&
    ( ( ${path_kind} == "directory" && -d ${path} && ! -L ${path} ) ||
      ( ${path_kind} == "file" && -f ${path} && ! -L ${path} ) ) ]]; then
    printf -v "${output_variable}" '%s' "${path}"
  elif [[ ${create_status} -eq 0 ]]; then
    create_status=1
  fi
  finish_installer_signal_transaction
  [[ ${create_status} -eq 0 ]]
}

prepare_install_directory() {
  local path=$1
  local owner=$2
  local group=$3
  local requested_mode=$4
  local expected_mode="${requested_mode#0}"
  local existed=false
  local identity=""
  local uid=""
  local gid=""
  local mode=""
  if [[ -n ${prepared_directory_seen["${path}"]+present} ]]; then
    [[ -d ${path} && ! -L ${path} ]] || \
      die "prepared installer directory changed type: ${path}"
    [[ $(stat -c '%U:%G:%a' -- "${path}") == "${owner}:${group}:${expected_mode}" ]] || \
      die "prepared installer directory changed ownership or mode: ${path}"
    return
  fi
  if [[ -e ${path} || -L ${path} ]]; then
    [[ -d ${path} && ! -L ${path} ]] || \
      die "installer directory destination is not a safe directory: ${path}"
    [[ $(readlink -f -- "${path}") == "${path}" ]] || \
      die "installer directory destination does not resolve to its fixed path: ${path}"
    existed=true
    identity="$(stat -c '%d:%i' -- "${path}")"
    uid="$(stat -c '%u' -- "${path}")"
    gid="$(stat -c '%g' -- "${path}")"
    mode="$(stat -c '%a' -- "${path}")"
  fi
  local install_status=0
  # The journal append, directory mutation, and new inode capture must be
  # indivisible with respect to HUP/INT/TERM.
  begin_installer_signal_transaction
  prepared_directory_paths+=("${path}")
  prepared_directory_existed+=("${existed}")
  prepared_directory_identities+=("${identity}")
  prepared_directory_uids+=("${uid}")
  prepared_directory_gids+=("${gid}")
  prepared_directory_modes+=("${mode}")
  prepared_directory_seen["${path}"]=1
  if install -d -o "${owner}" -g "${group}" -m "${requested_mode}" "${path}"; then
    :
  else
    install_status=$?
    if [[ ${existed} == false &&
      -d ${path} &&
      ! -L ${path} &&
      $(readlink -f -- "${path}" 2>/dev/null || true) == "${path}" ]]; then
      prepared_directory_identities[${#prepared_directory_identities[@]}-1]="$(stat -c '%d:%i' -- "${path}")"
    fi
  fi
  if [[ ${install_status} -eq 0 && ${existed} == false ]]; then
    prepared_directory_identities[${#prepared_directory_identities[@]}-1]="$(stat -c '%d:%i' -- "${path}")"
  fi
  finish_installer_signal_transaction
  [[ ${install_status} -eq 0 ]] || return "${install_status}"
  [[ -d ${path} && ! -L ${path} && $(readlink -f -- "${path}") == "${path}" ]] || \
    die "prepared installer directory is missing or unsafe: ${path}"
  [[ $(stat -c '%U:%G:%a' -- "${path}") == "${owner}:${group}:${expected_mode}" ]] || \
    die "prepared installer directory ownership or mode is invalid: ${path}"
}

rollback_prepared_directories() {
  local index
  local path
  local existed
  local identity
  local uid
  local gid
  local mode
  local failed=false
  for ((index=${#prepared_directory_paths[@]} - 1; index >= 0; index--)); do
    path="${prepared_directory_paths[index]}"
    existed="${prepared_directory_existed[index]}"
    identity="${prepared_directory_identities[index]}"
    uid="${prepared_directory_uids[index]}"
    gid="${prepared_directory_gids[index]}"
    mode="${prepared_directory_modes[index]}"
    if [[ ! -d ${path} || -L ${path} ||
      $(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) != "${identity}" ]]; then
      printf 'install-autostream-control-panel: rollback refused changed installer directory: %s\n' \
        "${path}" >&2
      failed=true
      continue
    fi
    if [[ ${existed} == true ]]; then
      chown "${uid}:${gid}" "${path}" || failed=true
      chmod "${mode}" "${path}" || failed=true
      [[ $(stat -c '%u:%g:%a' -- "${path}" 2>/dev/null || true) == "${uid}:${gid}:${mode}" ]] || \
        failed=true
    elif ! rmdir -- "${path}"; then
      printf 'install-autostream-control-panel: rollback could not remove new installer directory: %s\n' \
        "${path}" >&2
      failed=true
    fi
  done
  [[ ${failed} == false ]]
}

rollback_created_release() {
  [[ ${release_dir_created} == true ]] || return 0
  if [[ ! -e ${RELEASE_DIR-} && ! -L ${RELEASE_DIR-} ]]; then
    return 0
  fi
  [[ -n ${RELEASE_DIR-} &&
    ${RELEASE_DIR} == "${RELEASES_DIR}/"* &&
    -d ${RELEASE_DIR} &&
    ! -L ${RELEASE_DIR} &&
    $(stat -c '%d:%i' -- "${RELEASE_DIR}" 2>/dev/null || true) == "${release_dir_created_identity}" ]] || \
    return 1
  rm -rf -- "${RELEASE_DIR}"
  [[ ! -e ${RELEASE_DIR} && ! -L ${RELEASE_DIR} ]]
}
