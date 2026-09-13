
record_created_backup() {
  local path=$1
  [[ -f ${path} && ! -L ${path} ]] || die "created install backup is missing or unsafe: ${path}"
  last_created_backup_identity="$(stat -c '%d:%i' -- "${path}")"
  created_backup_paths+=("${path}")
  created_backup_identities+=("${last_created_backup_identity}")
}

copy_created_backup_and_record() {
  local source=$1
  local destination=$2
  local copy_status=0

  # Copying an absent backup and claiming its inode in the rollback journal are
  # one deferred-signal transaction. A command that reports failure after
  # creating the file is still journaled for safe cleanup.
  begin_installer_signal_transaction
  if cp -a -- "${source}" "${destination}"; then
    :
  else
    copy_status=$?
  fi
  if [[ -f ${destination} && ! -L ${destination} ]]; then
    record_created_backup "${destination}"
  elif [[ ${copy_status} -eq 0 ]]; then
    copy_status=1
  fi
  finish_installer_signal_transaction
  [[ ${copy_status} -eq 0 ]]
}

remove_created_backups() {
  local index
  local path
  local identity
  local failed=false
  for ((index=${#created_backup_paths[@]} - 1; index >= 0; index--)); do
    path="${created_backup_paths[index]}"
    identity="${created_backup_identities[index]}"
    if [[ ! -e ${path} && ! -L ${path} ]]; then
      continue
    fi
    if [[ -f ${path} &&
      ! -L ${path} &&
      $(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) == "${identity}" ]]; then
      rm -f -- "${path}" || failed=true
    else
      failed=true
    fi
  done
  [[ ${failed} == false ]]
}

remove_pending_link() {
  local path=$1
  local identity=$2
  local target=$3
  [[ -n ${path} ]] || return 0
  if [[ ! -e ${path} && ! -L ${path} ]]; then
    return 0
  fi
  [[ -n ${identity} &&
    -L ${path} &&
    $(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) == "${identity}" &&
    $(readlink -- "${path}" 2>/dev/null || true) == "${target}" ]] || return 1
  rm -f -- "${path}"
  [[ ! -e ${path} && ! -L ${path} ]]
}

public_directory_tree_sha256() {
  local path=$1
  [[ -d ${path} && ! -L ${path} && $(readlink -f -- "${path}") == "${path}" ]] || return 1
  tar --sort=name --numeric-owner -cf - -C "${path}" . |
    sha256sum |
    awk 'NR == 1 { print $1 }'
}

rollback_public_links() {
  local index
  local path
  local kind
  local target
  local backup
  local backup_created
  local backup_complete
  local backup_identity
  local source_identity
  local backup_metadata
  local backup_sha256
  local previous_uid
  local previous_gid
  local previous_mode
  local previous_sha256
  local previous_tree_sha256
  local installed_target
  local installed_identity
  local rollback_next
  local failed=false
  for ((index=${#changed_public_paths[@]} - 1; index >= 0; index--)); do
    path="${changed_public_paths[index]}"
    kind="${previous_public_kinds[index]}"
    target="${previous_public_targets[index]}"
    backup="${previous_public_backups[index]}"
    backup_created="${previous_public_backup_created[index]}"
    backup_complete="${previous_public_backup_complete[index]}"
    backup_identity="${previous_public_backup_identities[index]}"
    source_identity="${previous_public_source_identities[index]}"
    backup_metadata="${previous_public_backup_metadata[index]}"
    backup_sha256="${previous_public_backup_sha256[index]}"
    previous_uid="${previous_public_uids[index]}"
    previous_gid="${previous_public_gids[index]}"
    previous_mode="${previous_public_modes[index]}"
    previous_sha256="${previous_public_sha256[index]}"
    previous_tree_sha256="${previous_public_tree_sha256[index]}"
    installed_target="${installed_public_targets[index]}"
    installed_identity="${installed_public_identities[index]}"
    case "${kind}" in
      file)
        if [[ -n ${backup_identity} &&
          -f ${backup} &&
          ! -L ${backup} &&
          $(stat -c '%d:%i' -- "${backup}" 2>/dev/null || true) == "${backup_identity}" &&
          $(stat -c '%u:%g:%a' -- "${backup}" 2>/dev/null || true) == "${backup_metadata}" &&
          $(sha256sum -- "${backup}" 2>/dev/null | awk 'NR == 1 { print $1 }') == \
            "${backup_sha256}" ]]; then
          rollback_next="${path}.rollback.$$"
          rm -f -- "${rollback_next}" || failed=true
          cp -a -- "${backup}" "${rollback_next}" || failed=true
          sync -f "$(dirname -- "${path}")" || failed=true
          mv -Tf -- "${rollback_next}" "${path}" || failed=true
          [[ -f ${path} && ! -L ${path} ]] || failed=true
          cmp -s -- "${path}" "${backup}" || failed=true
          [[ $(stat -c '%u:%g:%a' -- "${path}" 2>/dev/null || true) == \
            "${previous_uid}:${previous_gid}:${previous_mode}" ]] || failed=true
          [[ $(sha256sum -- "${path}" 2>/dev/null | awk 'NR == 1 { print $1 }') == \
            "${previous_sha256}" ]] || failed=true
          if [[ ${backup_created} == true ]]; then
            rm -f -- "${backup}" || failed=true
            [[ ! -e ${backup} && ! -L ${backup} ]] || failed=true
            sync -f "$(dirname -- "${backup}")" || failed=true
          else
            [[ $(stat -c '%d:%i' -- "${backup}" 2>/dev/null || true) == \
              "${backup_identity}" ]] || failed=true
            [[ $(stat -c '%u:%g:%a' -- "${backup}" 2>/dev/null || true) == \
              "${backup_metadata}" ]] || failed=true
            [[ $(sha256sum -- "${backup}" 2>/dev/null | awk 'NR == 1 { print $1 }') == \
              "${backup_sha256}" ]] || failed=true
          fi
        else
          failed=true
        fi
        ;;
      directory)
        if [[ ${backup_created} == true &&
          ${backup_complete} == false &&
          -n ${backup_identity} &&
          -d ${backup} &&
          ! -L ${backup} &&
          $(readlink -f -- "${backup}" 2>/dev/null || true) == "${backup}" &&
          $(stat -c '%d:%i' -- "${backup}" 2>/dev/null || true) == "${backup_identity}" &&
          -n ${source_identity} &&
          -d ${path} && ! -L ${path} &&
          $(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) == "${source_identity}" &&
          $(stat -c '%u:%g:%a' -- "${path}" 2>/dev/null || true) == \
            "${previous_uid}:${previous_gid}:${previous_mode}" &&
          $(public_directory_tree_sha256 "${path}" 2>/dev/null || true) == \
            "${previous_tree_sha256}" ]]; then
          rm -rf -- "${backup}" || failed=true
          [[ ! -e ${backup} && ! -L ${backup} ]] || failed=true
          sync -f "$(dirname -- "${backup}")" || failed=true
        elif [[ ${backup_created} == true &&
          ${backup_complete} == true &&
          -n ${backup_identity} &&
          -d ${backup} &&
          ! -L ${backup} &&
          $(readlink -f -- "${backup}" 2>/dev/null || true) == "${backup}" &&
          $(stat -c '%d:%i' -- "${backup}" 2>/dev/null || true) == "${backup_identity}" &&
          $(public_directory_tree_sha256 "${backup}" 2>/dev/null || true) == \
            "${previous_tree_sha256}" ]]; then
          if [[ ! -e ${path} && ! -L ${path} ]]; then
            :
          elif [[ -n ${installed_identity} &&
            -L ${path} &&
            $(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) == "${installed_identity}" &&
            $(readlink -- "${path}" 2>/dev/null || true) == "${installed_target}" ]]; then
            rm -f -- "${path}" || failed=true
          else
            failed=true
            continue
          fi
          if mv -T -- "${backup}" "${path}"; then
            [[ -d ${path} && ! -L ${path} &&
              ! -e ${backup} && ! -L ${backup} &&
              $(stat -c '%u:%g:%a' -- "${path}" 2>/dev/null || true) == \
                "${previous_uid}:${previous_gid}:${previous_mode}" &&
              $(public_directory_tree_sha256 "${path}" 2>/dev/null || true) == \
                "${previous_tree_sha256}" ]] || failed=true
          else
            failed=true
          fi
        elif [[ ${backup_created} == false &&
          ! -e ${backup} && ! -L ${backup} &&
          -n ${source_identity} &&
          -d ${path} && ! -L ${path} &&
          $(stat -c '%d:%i' -- "${path}" 2>/dev/null || true) == "${source_identity}" &&
          $(stat -c '%u:%g:%a' -- "${path}" 2>/dev/null || true) == \
            "${previous_uid}:${previous_gid}:${previous_mode}" &&
          $(public_directory_tree_sha256 "${path}" 2>/dev/null || true) == \
            "${previous_tree_sha256}" ]]; then
          :
        else
          failed=true
        fi
        ;;
      symlink)
        rollback_next="${path}.rollback.$$"
        rm -f -- "${rollback_next}" || failed=true
        ln -s -- "${target}" "${rollback_next}" || failed=true
        mv -Tf -- "${rollback_next}" "${path}" || failed=true
        [[ -L ${path} && $(readlink -- "${path}") == "${target}" ]] || failed=true
        ;;
      absent)
        rm -f -- "${path}" || failed=true
        [[ ! -e ${path} && ! -L ${path} ]] || failed=true
        ;;
      *)
        failed=true
        ;;
    esac
  done
  [[ ${failed} == false ]]
}

rollback_current_link() {
  local rollback_next
  [[ ${current_changed} == true ]] || return 0
  if [[ -n ${previous_current_target} ]]; then
    rollback_next="${MANAGED_ROOT}/.current.rollback.$$"
    rm -f -- "${rollback_next}" || return 1
    ln -s -- "${previous_current_target}" "${rollback_next}" || return 1
    mv -Tf -- "${rollback_next}" "${CURRENT_LINK}" || return 1
    [[ -L ${CURRENT_LINK} &&
      $(readlink -- "${CURRENT_LINK}") == "${previous_current_target}" ]] || return 1
  else
    rm -f -- "${CURRENT_LINK}" || return 1
    [[ ! -e ${CURRENT_LINK} && ! -L ${CURRENT_LINK} ]] || return 1
  fi
}

rollback_regular_destination() {
  local destination=$1
  local changed=$2
  local previous_kind=$3
  local backup=$4
  local backup_created=$5
  local backup_identity=$6
  local rollback_next
  [[ ${changed} == true ]] || return 0
  if [[ ${previous_kind} == "regular" &&
    -n ${backup_identity} &&
    -f ${backup} &&
    ! -L ${backup} &&
    $(stat -c '%d:%i' -- "${backup}" 2>/dev/null || true) == "${backup_identity}" ]]; then
    rollback_next="${destination}.rollback.$$"
    rm -f -- "${rollback_next}" || return 1
    cp -a -- "${backup}" "${rollback_next}" || return 1
    sync -f "$(dirname -- "${destination}")" || return 1
    mv -Tf -- "${rollback_next}" "${destination}" || return 1
    [[ -f ${destination} && ! -L ${destination} ]] || return 1
    cmp -s -- "${destination}" "${backup}" || return 1
    if [[ ${backup_created} == true ]]; then
      rm -f -- "${backup}" || return 1
      [[ ! -e ${backup} && ! -L ${backup} ]] || return 1
      sync -f "$(dirname -- "${backup}")" || return 1
    else
      [[ $(stat -c '%d:%i' -- "${backup}" 2>/dev/null || true) == \
        "${backup_identity}" ]] || return 1
    fi
  elif [[ ${previous_kind} == "absent" ]]; then
    rm -f -- "${destination}" || return 1
    [[ ! -e ${destination} && ! -L ${destination} ]] || return 1
  else
    return 1
  fi
}
