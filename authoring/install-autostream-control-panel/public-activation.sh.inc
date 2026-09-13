
preflight_public_path() {
  local link_path=$1
  local backup_name
  local backup_path
  local path_mode
  backup_name="$(basename -- "${link_path}")" || die "could not resolve the public path name"
  backup_path="${LEGACY_BACKUP_DIR}/${backup_name}"
  [[ -d $(dirname -- "${link_path}") && ! -L $(dirname -- "${link_path}") ]] || \
    die "public path parent is missing or unsafe: $(dirname -- "${link_path}")"
  if [[ -L ${link_path} ]]; then
    [[ $(stat -c '%U:%G' -- "${link_path}") == "root:root" ]] || \
      die "existing public symlink must be owned by root:root: ${link_path}"
  elif [[ -e ${link_path} ]]; then
    [[ ( -f ${link_path} || -d ${link_path} ) && ! -L ${link_path} ]] || \
      die "existing public path is not replaceable: ${link_path}"
    [[ $(readlink -f -- "${link_path}") == "${link_path}" ]] || \
      die "existing public path does not resolve to its fixed path: ${link_path}"
    [[ $(stat -c '%U:%G' -- "${link_path}") == "root:root" ]] || \
      die "existing public path must be owned by root:root: ${link_path}"
    path_mode="$(stat -c '%a' -- "${link_path}")"
    (( (8#${path_mode} & 07022) == 0 )) || \
      die "existing public path has unsafe mode bits: ${link_path}"
  fi
  if [[ -e ${link_path} && ! -L ${link_path} && ( -e ${backup_path} || -L ${backup_path} ) ]]; then
    [[ -f ${link_path} && ! -L ${link_path} && -f ${backup_path} && ! -L ${backup_path} ]] || \
      die "legacy backup destination conflicts with the live public path: ${backup_path}"
    [[ $(stat -c '%u:%g' -- "${backup_path}") == "0:0" ]] || \
      die "legacy public backup must be owned by root:root: ${backup_path}"
    [[ $(stat -c '%a' -- "${link_path}") == "$(stat -c '%a' -- "${backup_path}")" ]] &&
      cmp -s -- "${link_path}" "${backup_path}" || \
      die "legacy backup destination conflicts with the live public path: ${backup_path}"
    preexisting_public_backup_identities["${backup_path}"]="$(stat -c '%d:%i' -- "${backup_path}")"
    preexisting_public_backup_metadata["${backup_path}"]="$(stat -c '%u:%g:%a' -- "${backup_path}")"
    preexisting_public_backup_sha256["${backup_path}"]="$(
      sha256sum -- "${backup_path}" | awk 'NR == 1 { print $1 }'
    )"
  fi
}

preflight_public_path "${PUBLIC_BINARY}"
preflight_public_path "${PUBLIC_WEB}"
[[ ! -e ${CURRENT_LINK} || -L ${CURRENT_LINK} ]] || die "managed current path must be absent or a symlink"
if systemctl is-active --quiet "${UNIT_NAME}" &&
  [[ ! -f ${PUBLIC_BINARY} || ! -x ${PUBLIC_BINARY} ]]; then
  die "active legacy service requires an executable canonical public binary: ${PUBLIC_BINARY}"
fi

install_public_link() {
  local target=$1
  local link_path=$2
  local backup_name
  local backup_path
  local link_next="${link_path}.next.$$"
  local previous_kind
  local previous_target=""
  local previous_backup=""
  local previous_backup_created=false
  local previous_backup_identity=""
  local previous_source_identity=""
  local previous_backup_metadata=""
  local previous_backup_sha256=""
  local previous_uid=""
  local previous_gid=""
  local previous_mode=""
  local previous_sha256=""
  local previous_tree_sha256=""
  local journal_recorded=false
  local journal_index=0
  local directory_move_status=0
  local directory_sync_status=0
  local directory_backup_complete=false
  local public_link_status=0
  backup_name="$(basename -- "${link_path}")" || die "could not resolve the public path name"
  backup_path="${LEGACY_BACKUP_DIR}/${backup_name}"
  if [[ -L ${link_path} ]]; then
    previous_kind="symlink"
    previous_target="$(readlink -- "${link_path}")"
  elif [[ -f ${link_path} && ! -L ${link_path} ]]; then
    previous_kind="file"
    previous_uid="$(stat -c '%u' -- "${link_path}")"
    previous_gid="$(stat -c '%g' -- "${link_path}")"
    previous_mode="$(stat -c '%a' -- "${link_path}")"
    previous_sha256="$(sha256sum -- "${link_path}" | awk 'NR == 1 { print $1 }')"
    prepare_install_directory "${LEGACY_BACKUP_DIR}" root root 0700
    if [[ ! -e ${backup_path} && ! -L ${backup_path} ]]; then
      copy_created_backup_and_record "${link_path}" "${backup_path}" || \
        die "could not create the legacy public file backup: ${backup_path}"
      previous_backup_created=true
      previous_backup_identity="${last_created_backup_identity}"
      previous_backup_metadata="$(stat -c '%u:%g:%a' -- "${backup_path}")"
      previous_backup_sha256="$(sha256sum -- "${backup_path}" | awk 'NR == 1 { print $1 }')"
      [[ -f ${backup_path} && ! -L ${backup_path} ]] || \
        die "legacy public file backup is missing or unsafe: ${backup_path}"
      cmp -s -- "${link_path}" "${backup_path}" || \
        die "legacy public file backup differs from its source: ${link_path}"
      [[ $(stat -c '%a' -- "${link_path}") == "$(stat -c '%a' -- "${backup_path}")" ]] || \
        die "legacy public file backup mode differs from its source: ${link_path}"
      sync -f "${LEGACY_BACKUP_DIR}"
    else
      previous_backup_identity="${preexisting_public_backup_identities["${backup_path}"]-}"
      previous_backup_metadata="${preexisting_public_backup_metadata["${backup_path}"]-}"
      previous_backup_sha256="${preexisting_public_backup_sha256["${backup_path}"]-}"
      [[ -n ${previous_backup_identity} &&
        $(stat -c '%d:%i' -- "${backup_path}" 2>/dev/null || true) == \
          "${previous_backup_identity}" &&
        $(stat -c '%u:%g:%a' -- "${backup_path}" 2>/dev/null || true) == \
          "${previous_backup_metadata}" &&
        $(sha256sum -- "${backup_path}" | awk 'NR == 1 { print $1 }') == \
          "${previous_backup_sha256}" ]] || \
        die "pre-existing legacy public backup identity changed before commit: ${backup_path}"
    fi
    previous_backup="${backup_path}"
  elif [[ -d ${link_path} && ! -L ${link_path} ]]; then
    previous_kind="directory"
    previous_backup="${backup_path}"
    previous_source_identity="$(stat -c '%d:%i' -- "${link_path}")"
    previous_uid="$(stat -c '%u' -- "${link_path}")"
    previous_gid="$(stat -c '%g' -- "${link_path}")"
    previous_mode="$(stat -c '%a' -- "${link_path}")"
    previous_tree_sha256="$(public_directory_tree_sha256 "${link_path}")" || \
      die "could not snapshot the legacy public directory: ${link_path}"
    [[ ${previous_tree_sha256} =~ ^[0-9a-f]{64}$ ]] || \
      die "legacy public directory snapshot is invalid: ${link_path}"
    prepare_install_directory "${LEGACY_BACKUP_DIR}" root root 0700
    begin_installer_signal_transaction
    changed_public_paths+=("${link_path}")
    previous_public_kinds+=("${previous_kind}")
    previous_public_targets+=("${previous_target}")
    previous_public_backups+=("${previous_backup}")
    previous_public_backup_created+=("${previous_backup_created}")
    previous_public_backup_complete+=("${directory_backup_complete}")
    previous_public_backup_identities+=("${previous_backup_identity}")
    previous_public_source_identities+=("${previous_source_identity}")
    previous_public_backup_metadata+=("${previous_backup_metadata}")
    previous_public_backup_sha256+=("${previous_backup_sha256}")
    previous_public_uids+=("${previous_uid}")
    previous_public_gids+=("${previous_gid}")
    previous_public_modes+=("${previous_mode}")
    previous_public_sha256+=("${previous_sha256}")
    previous_public_tree_sha256+=("${previous_tree_sha256}")
    installed_public_targets+=("")
    installed_public_identities+=("")
    journal_index=$((${#previous_public_backup_identities[@]} - 1))
    journal_recorded=true
    if [[ -d ${link_path} && ! -L ${link_path} &&
      $(stat -c '%d:%i' -- "${link_path}" 2>/dev/null || true) == \
        "${previous_source_identity}" &&
      $(stat -c '%u:%g:%a' -- "${link_path}" 2>/dev/null || true) == \
        "${previous_uid}:${previous_gid}:${previous_mode}" ]]; then
      if mv -T -- "${link_path}" "${backup_path}"; then
        :
      else
        directory_move_status=$?
      fi
    else
      directory_move_status=1
    fi
    if [[ ! -e ${link_path} && ! -L ${link_path} &&
      -d ${backup_path} && ! -L ${backup_path} &&
      $(readlink -f -- "${backup_path}" 2>/dev/null || true) == "${backup_path}" &&
      $(stat -c '%u:%g:%a' -- "${backup_path}" 2>/dev/null || true) == \
        "${previous_uid}:${previous_gid}:${previous_mode}" &&
      $(public_directory_tree_sha256 "${backup_path}" 2>/dev/null || true) == \
        "${previous_tree_sha256}" ]]; then
      previous_backup_identity="$(stat -c '%d:%i' -- "${backup_path}")"
      previous_backup_created=true
      directory_backup_complete=true
      previous_public_backup_identities[journal_index]="${previous_backup_identity}"
      previous_public_backup_created[journal_index]=true
      previous_public_backup_complete[journal_index]=true
    elif [[ -d ${link_path} && ! -L ${link_path} &&
      $(stat -c '%d:%i' -- "${link_path}" 2>/dev/null || true) == \
        "${previous_source_identity}" &&
      $(stat -c '%u:%g:%a' -- "${link_path}" 2>/dev/null || true) == \
        "${previous_uid}:${previous_gid}:${previous_mode}" &&
      -d ${backup_path} && ! -L ${backup_path} &&
      $(readlink -f -- "${backup_path}" 2>/dev/null || true) == "${backup_path}" ]]; then
      previous_backup_identity="$(stat -c '%d:%i' -- "${backup_path}")"
      previous_backup_created=true
      previous_public_backup_identities[journal_index]="${previous_backup_identity}"
      previous_public_backup_created[journal_index]=true
      previous_public_backup_complete[journal_index]=false
      [[ ${directory_move_status} -ne 0 ]] || directory_move_status=1
    elif [[ ${directory_move_status} -eq 0 ]]; then
      directory_move_status=1
    fi
    if [[ ${previous_backup_created} == true ]]; then
      if sync -f "${LEGACY_BACKUP_DIR}"; then
        :
      else
        directory_sync_status=$?
      fi
    fi
    finish_installer_signal_transaction
    if [[ ${directory_move_status} -ne 0 ]]; then
      printf 'install-autostream-control-panel: could not safely move the legacy public directory into backup (mv status %s): %s\n' \
        "${directory_move_status}" "${backup_path}" >&2
      return "${directory_move_status}"
    fi
    if [[ ${directory_sync_status} -ne 0 ]]; then
      printf 'install-autostream-control-panel: could not durably commit the legacy public directory backup (sync status %s): %s\n' \
        "${directory_sync_status}" "${backup_path}" >&2
      return "${directory_sync_status}"
    fi
  elif [[ -e ${link_path} ]]; then
    die "public path is not a replaceable file or directory: ${link_path}"
  else
    previous_kind="absent"
  fi
  begin_installer_signal_transaction
  public_next="${link_next}"
  public_next_identity=""
  public_next_target="${target}"
  rm -f -- "${link_next}"
  ln -s -- "${target}" "${link_next}"
  public_next_identity="$(stat -c '%d:%i' -- "${link_next}")"
  if [[ ${journal_recorded} == false ]]; then
    changed_public_paths+=("${link_path}")
    previous_public_kinds+=("${previous_kind}")
    previous_public_targets+=("${previous_target}")
    previous_public_backups+=("${previous_backup}")
    previous_public_backup_created+=("${previous_backup_created}")
    previous_public_backup_complete+=("${directory_backup_complete}")
    previous_public_backup_identities+=("${previous_backup_identity}")
    previous_public_source_identities+=("${previous_source_identity}")
    previous_public_backup_metadata+=("${previous_backup_metadata}")
    previous_public_backup_sha256+=("${previous_backup_sha256}")
    previous_public_uids+=("${previous_uid}")
    previous_public_gids+=("${previous_gid}")
    previous_public_modes+=("${previous_mode}")
    previous_public_sha256+=("${previous_sha256}")
    previous_public_tree_sha256+=("${previous_tree_sha256}")
    installed_public_targets+=("")
    installed_public_identities+=("")
    journal_index=$((${#previous_public_backup_identities[@]} - 1))
  fi
  if mv -Tf -- "${link_next}" "${link_path}"; then
    :
  else
    public_link_status=$?
  fi
  if [[ -L ${link_path} &&
    $(stat -c '%d:%i' -- "${link_path}" 2>/dev/null || true) == "${public_next_identity}" &&
    $(readlink -- "${link_path}" 2>/dev/null || true) == "${target}" ]]; then
    installed_public_targets[journal_index]="${target}"
    installed_public_identities[journal_index]="${public_next_identity}"
    public_next=""
    public_next_identity=""
    public_next_target=""
  elif [[ ${public_link_status} -eq 0 ]]; then
    public_link_status=1
  fi
  finish_installer_signal_transaction
  if [[ ${public_link_status} -ne 0 ]]; then
    printf 'install-autostream-control-panel: could not safely publish the managed public link (mv status %s): %s\n' \
      "${public_link_status}" "${link_path}" >&2
    return "${public_link_status}"
  fi
}

if [[ ${unit_previous_kind} == "regular" || ${backup_exec_previous_kind} == "regular" ]]; then
  prepare_install_directory "${LEGACY_BACKUP_DIR}" root root 0700
fi
if [[ ${unit_install_needed} == true ]]; then
  if [[ ${unit_previous_kind} == "regular" ]]; then
    if [[ ! -e ${unit_backup} && ! -L ${unit_backup} ]]; then
      copy_created_backup_and_record "${UNIT_DEST}" "${unit_backup}" || \
        die "could not create the legacy systemd unit backup"
      unit_backup_created=true
      unit_backup_identity="${last_created_backup_identity}"
      [[ -f ${unit_backup} && ! -L ${unit_backup} ]] || \
        die "legacy systemd unit backup is missing or unsafe"
      cmp -s -- "${UNIT_DEST}" "${unit_backup}" || \
        die "legacy systemd unit backup differs from its source"
      sync -f "${LEGACY_BACKUP_DIR}"
    else
      [[ -n ${unit_backup_identity} &&
        $(stat -c '%d:%i' -- "${unit_backup}" 2>/dev/null || true) == \
          "${unit_backup_identity}" ]] || \
        die "pre-existing legacy systemd unit backup identity changed before commit"
    fi
  fi
  unit_changed=true
  mv -Tf -- "${unit_stage}" "${UNIT_DEST}"
  unit_stage=""
fi

if [[ ${backup_exec_install_needed} == true ]]; then
  if [[ ${backup_exec_previous_kind} == "regular" ]]; then
    if [[ ! -e ${backup_exec_backup} && ! -L ${backup_exec_backup} ]]; then
      copy_created_backup_and_record "${BACKUP_EXECUTABLE}" "${backup_exec_backup}" || \
        die "could not create the legacy backup executable backup"
      backup_exec_backup_created=true
      backup_exec_backup_identity="${last_created_backup_identity}"
      [[ -f ${backup_exec_backup} && ! -L ${backup_exec_backup} ]] || \
        die "legacy backup executable backup is missing or unsafe"
      cmp -s -- "${BACKUP_EXECUTABLE}" "${backup_exec_backup}" || \
        die "legacy backup executable backup differs from its source"
      sync -f "${LEGACY_BACKUP_DIR}"
    else
      [[ -n ${backup_exec_backup_identity} &&
        $(stat -c '%d:%i' -- "${backup_exec_backup}" 2>/dev/null || true) == \
          "${backup_exec_backup_identity}" ]] || \
        die "pre-existing legacy backup executable backup identity changed before commit"
    fi
  fi
  backup_exec_changed=true
  mv -Tf -- "${backup_exec_stage}" "${BACKUP_EXECUTABLE}"
  backup_exec_stage=""
fi

if [[ -n ${env_stage} ]]; then
  env_created=true
  mv -T -- "${env_stage}" "${ENV_DEST}"
  env_stage=""
fi
if [[ -n ${backup_config_stage} ]]; then
  backup_config_created=true
  mv -T -- "${backup_config_stage}" "${MARIADB_DEFAULTS}"
  backup_config_stage=""
fi

if [[ -n ${existing_env_identity} ]]; then
  [[ -f ${ENV_DEST} && ! -L ${ENV_DEST} ]] || die "existing environment file changed during installation"
  [[ $(stat -c '%d:%i:%s:%Y:%f' -- "${ENV_DEST}") == "${existing_env_identity}" ]] || \
    die "existing environment file identity changed during installation"
  [[ $(sha256sum -- "${ENV_DEST}" | awk 'NR == 1 { print $1 }') == "${existing_env_sha256}" ]] || \
    die "existing environment file content changed during installation"
fi
if [[ -n ${existing_backup_config_sha256} ]]; then
  [[ $(sha256sum -- "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }') == \
    "${existing_backup_config_sha256}" ]] || \
    die "existing MariaDB backup config changed during installation"
fi
[[ $(stat -c '%U:%G:%a' -- "${UNIT_DEST}") == "root:root:644" ]] || \
  die "installed systemd unit ownership or mode is invalid"
[[ $(stat -c '%U:%G:%a' -- "${BACKUP_EXECUTABLE}") == "root:root:700" ]] || \
  die "installed backup executable ownership or mode is invalid"

sync_installer_filesystems || die "could not durably commit the installed files"
systemctl daemon-reload

begin_installer_signal_transaction
current_next="${MANAGED_ROOT}/.current.next.$$"
current_next_identity=""
current_next_target="${RELEASE_DIR}"
rm -f -- "${current_next}"
ln -s -- "${RELEASE_DIR}" "${current_next}"
current_next_identity="$(stat -c '%d:%i' -- "${current_next}")"
current_changed=true
mv -Tf -- "${current_next}" "${CURRENT_LINK}"
current_next=""
current_next_identity=""
current_next_target=""
finish_installer_signal_transaction
[[ $(readlink -f -- "${CURRENT_LINK}") == "${RELEASE_DIR}" ]] || die "managed current link verification failed"
sync -f "${MANAGED_ROOT}"

install_public_link "${CURRENT_LINK}/bin/control-panel" "${PUBLIC_BINARY}"
install_public_link "${CURRENT_LINK}/share/autostream-control-panel" "${PUBLIC_WEB}"
[[ $(readlink -f -- "${PUBLIC_BINARY}") == "${RELEASE_DIR}/bin/control-panel" ]] || \
  die "public binary link verification failed"
[[ $(readlink -f -- "${PUBLIC_WEB}") == "${RELEASE_DIR}/share/autostream-control-panel" ]] || \
  die "public web link verification failed"
sync_installer_filesystems || die "could not durably activate the installed release"
installation_complete=true

printf 'Installed AutoStream Control Panel %s.\n' "${VERSION}"
printf 'Existing settings were preserved. Review %s, then run:\n' "${ENV_DEST}"
if systemctl is-active --quiet "${UNIT_NAME}"; then
  printf '  sudo systemctl restart %s\n' "${UNIT_NAME}"
else
  printf '  sudo systemctl enable --now %s\n' "${UNIT_NAME}"
fi
printf '  sudo systemctl status %s\n' "${UNIT_NAME}"
