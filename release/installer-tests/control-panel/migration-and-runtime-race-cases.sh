
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
snapshot_legacy_web_tree \
  "${INSTALL_BACKUP_ROOT}/${VERSION}-${archive_sha256:0:12}/autostream-control-panel" \
  "${WORK_DIR}/successful-migration-web.tar" || \
  die "successful migration could not snapshot the retained legacy web directory"
cmp -s -- "${WORK_DIR}/legacy-web.before.tar" \
  "${WORK_DIR}/successful-migration-web.tar" || \
  die "successful migration did not retain the legacy web tree exactly"
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
