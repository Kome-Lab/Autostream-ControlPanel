
cleanup() {
  trap '' HUP INT TERM
  cleanup_running=true
  signal_transaction_active=false
  deferred_termination_status=0
  trap - EXIT
  local exit_code=$1
  local rollback_failed=false
  local recovery_evidence_ready=true
  set +e
  if [[ ${installation_complete} != true ]]; then
    if ! rollback_regular_destination \
      "${UNIT_DEST}" \
      "${unit_changed}" \
      "${unit_previous_kind}" \
      "${unit_backup}" \
      "${unit_backup_created}" \
      "${unit_backup_identity}"; then
      rollback_failed=true
    fi
    if ! rollback_regular_destination \
      "${BACKUP_EXECUTABLE}" \
      "${backup_exec_changed}" \
      "${backup_exec_previous_kind}" \
      "${backup_exec_backup}" \
      "${backup_exec_backup_created}" \
      "${backup_exec_backup_identity}"; then
      rollback_failed=true
    fi
    if [[ ${env_created} == true ]]; then
      rm -f -- "${ENV_DEST}" || rollback_failed=true
      [[ ! -e ${ENV_DEST} && ! -L ${ENV_DEST} ]] || rollback_failed=true
    fi
    if [[ ${backup_config_created} == true ]]; then
      rm -f -- "${MARIADB_DEFAULTS}" || rollback_failed=true
      [[ ! -e ${MARIADB_DEFAULTS} && ! -L ${MARIADB_DEFAULTS} ]] || rollback_failed=true
    fi
    if ! rollback_public_links; then
      rollback_failed=true
    fi
    if ! rollback_current_link; then
      rollback_failed=true
    fi
    if ! remove_pending_link \
      "${current_next}" "${current_next_identity}" "${current_next_target}"; then
      rollback_failed=true
    fi
    if ! remove_pending_link \
      "${public_next}" "${public_next_identity}" "${public_next_target}"; then
      rollback_failed=true
    fi
    if ! rollback_created_release; then
      rollback_failed=true
    fi
    if [[ ${unit_changed} == true ]] &&
      ! systemctl daemon-reload >/dev/null 2>&1; then
      rollback_failed=true
    fi
  fi
  if [[ -n ${managed_candidate} && -e ${managed_candidate} ]]; then
    rm -rf -- "${managed_candidate}"
  fi
  rm -f -- \
    "${unit_stage}" \
    "${backup_exec_stage}" \
    "${env_stage}" \
    "${backup_config_stage}" \
    "${lock_create_stage}"
  if [[ ${installation_complete} != true ]]; then
    if [[ ${rollback_failed} == false ]] && ! remove_created_backups; then
      rollback_failed=true
    fi
    if ! rollback_prepared_directories; then
      rollback_failed=true
    fi
    if ! rollback_created_service_account; then
      rollback_failed=true
    fi
    if ! sync_installer_filesystems; then
      rollback_failed=true
    fi
    if [[ ${target_lock_opened} == true ]]; then
      exec 9>&-
      target_lock_opened=false
    fi
    if [[ ${shared_lock_opened} == true ]]; then
      exec 8>&-
      shared_lock_opened=false
    fi
  fi
  if [[ ${rollback_failed} == true ]]; then
    if ! {
      printf 'service=%s\n' "${SERVICE_NAME}"
      printf 'unit_backup=%s\n' "${unit_backup}"
      printf 'backup_executable_backup=%s\n' "${backup_exec_backup}"
      printf 'previous_current_target=%s\n' "${previous_current_target}"
      printf 'legacy_backup_directory=%s\n' "${LEGACY_BACKUP_DIR-}"
    } > "${INPUT_STAGE}/recovery-state.txt"; then
      recovery_evidence_ready=false
    fi
    chown -R root:root "${INPUT_STAGE}" || recovery_evidence_ready=false
    chmod -R go-rwx "${INPUT_STAGE}" || recovery_evidence_ready=false
    [[ -f ${INPUT_STAGE}/recovery-state.txt && ! -L ${INPUT_STAGE}/recovery-state.txt ]] || \
      recovery_evidence_ready=false
    sync -f "${INPUT_STAGE}" || recovery_evidence_ready=false
    if [[ ${recovery_evidence_ready} == true ]]; then
      printf 'install-autostream-control-panel: rollback was incomplete; root-only recovery evidence is retained at %s\n' \
        "${INPUT_STAGE}" >&2
    else
      printf 'install-autostream-control-panel: WARNING: rollback was incomplete and recovery evidence could not be fully sealed; inspect %s immediately\n' \
        "${INPUT_STAGE}" >&2
    fi
  else
    if input_stage_is_owned; then
      rm -rf -- "${INPUT_STAGE}"
    else
      printf 'install-autostream-control-panel: refusing to remove replaced private input staging\n' >&2
      exit_code=1
    fi
  fi
  exit "${exit_code}"
}
trap 'cleanup "$?"' EXIT
install -d -o root -g root -m 0700 "${UNPACK_DIR}"
