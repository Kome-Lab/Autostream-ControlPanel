
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
snapshot_legacy_web_tree "${PUBLIC_WEB}" "${WORK_DIR}/failed-install-web.after.tar" || \
  die "failed migration could not snapshot the restored legacy web directory"
cmp -s -- "${WORK_DIR}/legacy-web.before.tar" \
  "${WORK_DIR}/failed-install-web.after.tar" || \
  die "failed migration did not restore the legacy web tree exactly"
[[ ! -e ${legacy_backup_dir}/autostream-control-panel &&
  ! -L ${legacy_backup_dir}/autostream-control-panel ]] || \
  die "failed migration left the legacy web backup behind"
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

install -o root -g root -m 0755 /usr/bin/mv "${WORK_DIR}/real-mv"
cat > "${WORK_DIR}/mv-post-mutation-fail" <<EOF
#!/bin/bash
set -euo pipefail
set +e
"${WORK_DIR}/real-mv" "\$@"
status=\$?
set -e
[[ \${status} -eq 0 ]] || exit "\${status}"
destination="\${!#}"
if [[ \${destination} == "${legacy_backup_dir}/autostream-control-panel" &&
  ! -e "${WORK_DIR}/mv-post-mutation.executed" ]]; then
  printf '%s\n' delivered > "${WORK_DIR}/mv-post-mutation.executed"
  exit 73
fi
EOF
chmod 0755 "${WORK_DIR}/mv-post-mutation-fail"

set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/mv-post-mutation-fail' /usr/bin/mv &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/mv-post-mutation.out" 2>&1
mv_post_mutation_status=$?
set -e
if [[ ${mv_post_mutation_status} -ne 73 ]]; then
  report_failed_install_probe \
    "post-mutation legacy web move" "${WORK_DIR}/mv-post-mutation.out"
  die "post-mutation legacy web move did not preserve status 73"
fi
[[ -f ${WORK_DIR}/mv-post-mutation.executed ]] || \
  die "post-mutation legacy web move did not reach its injection boundary"
[[ ! -e ${MANAGED_ROOT}/current && ! -L ${MANAGED_ROOT}/current ]] || \
  die "post-mutation legacy web move rollback left current activated"
[[ -f ${PUBLIC_BINARY} && ! -L ${PUBLIC_BINARY} ]] || \
  die "post-mutation legacy web move rollback did not restore the legacy binary"
grep -Fx -- "${LEGACY_BINARY_CONTENT}" "${PUBLIC_BINARY}" >/dev/null || \
  die "post-mutation legacy web move rollback changed the legacy binary"
snapshot_legacy_web_tree \
  "${PUBLIC_WEB}" "${WORK_DIR}/mv-post-mutation-web.after.tar" || \
  die "post-mutation legacy web move rollback did not restore the legacy web directory"
cmp -s -- "${WORK_DIR}/legacy-web.before.tar" \
  "${WORK_DIR}/mv-post-mutation-web.after.tar" || \
  die "post-mutation legacy web move rollback changed the legacy web tree"
[[ ! -e ${legacy_backup_dir}/autostream-control-panel &&
  ! -L ${legacy_backup_dir}/autostream-control-panel ]] || \
  die "post-mutation legacy web move rollback left the legacy web backup behind"
if grep -F -- "rollback was incomplete" "${WORK_DIR}/mv-post-mutation.out" >/dev/null; then
  report_failed_install_probe \
    "post-mutation legacy web move" "${WORK_DIR}/mv-post-mutation.out"
  die "post-mutation legacy web move reported an incomplete rollback"
fi
assert_legacy_runtime_unit_loaded
kill -0 "${old_pid}" || \
  die "post-mutation legacy web move rollback stopped the running legacy process"

cat > "${WORK_DIR}/mv-partial-destination-fail" <<EOF
#!/bin/bash
set -euo pipefail
destination="\${!#}"
if [[ \${destination} == "${legacy_backup_dir}/autostream-control-panel" &&
  ! -e "${WORK_DIR}/mv-partial-destination.executed" ]]; then
  install -d -o root -g root -m 0755 "\${destination}"
  printf '%s\n' partial > "\${destination}/partial-copy.txt"
  printf '%s\n' delivered > "${WORK_DIR}/mv-partial-destination.executed"
  exit 72
fi
exec "${WORK_DIR}/real-mv" "\$@"
EOF
chmod 0755 "${WORK_DIR}/mv-partial-destination-fail"

set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/mv-partial-destination-fail' /usr/bin/mv &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/mv-partial-destination.out" 2>&1
mv_partial_destination_status=$?
set -e
if [[ ${mv_partial_destination_status} -ne 72 ]]; then
  report_failed_install_probe \
    "partial legacy web move" "${WORK_DIR}/mv-partial-destination.out"
  die "partial legacy web move did not preserve status 72"
fi
[[ -f ${WORK_DIR}/mv-partial-destination.executed ]] || \
  die "partial legacy web move did not reach its injection boundary"
[[ ! -e ${MANAGED_ROOT}/current && ! -L ${MANAGED_ROOT}/current ]] || \
  die "partial legacy web move rollback left current activated"
[[ -f ${PUBLIC_BINARY} && ! -L ${PUBLIC_BINARY} ]] || \
  die "partial legacy web move rollback did not restore the legacy binary"
grep -Fx -- "${LEGACY_BINARY_CONTENT}" "${PUBLIC_BINARY}" >/dev/null || \
  die "partial legacy web move rollback changed the legacy binary"
snapshot_legacy_web_tree \
  "${PUBLIC_WEB}" "${WORK_DIR}/mv-partial-destination-web.after.tar" || \
  die "partial legacy web move rollback did not retain the legacy web directory"
cmp -s -- "${WORK_DIR}/legacy-web.before.tar" \
  "${WORK_DIR}/mv-partial-destination-web.after.tar" || \
  die "partial legacy web move rollback changed the legacy web tree"
[[ ! -e ${legacy_backup_dir}/autostream-control-panel &&
  ! -L ${legacy_backup_dir}/autostream-control-panel ]] || \
  die "partial legacy web move rollback left the partial backup behind"
if grep -F -- "rollback was incomplete" "${WORK_DIR}/mv-partial-destination.out" >/dev/null; then
  report_failed_install_probe \
    "partial legacy web move" "${WORK_DIR}/mv-partial-destination.out"
  die "partial legacy web move reported an incomplete rollback"
fi
assert_legacy_runtime_unit_loaded
kill -0 "${old_pid}" || \
  die "partial legacy web move rollback stopped the running legacy process"

install -o root -g root -m 0755 /usr/bin/sync "${WORK_DIR}/real-sync"
cat > "${WORK_DIR}/sync-fail" <<EOF
#!/bin/bash
printf '%s\n' "\$*" >> "${WORK_DIR}/sync-fail.log"
if [[ "\$*" == "-f /usr/local/bin" &&
  ! -e "${WORK_DIR}/sync-post-activation.executed" &&
  -L "${CURRENT_LINK}" &&
  -L "${PUBLIC_BINARY}" &&
  \$(readlink -- "${PUBLIC_BINARY}") == "${CURRENT_LINK}/bin/control-panel" &&
  -L "${PUBLIC_WEB}" &&
  \$(readlink -- "${PUBLIC_WEB}") == \
    "${CURRENT_LINK}/share/autostream-control-panel" ]]; then
  printf '%s\n' delivered > "${WORK_DIR}/sync-post-activation.executed"
  exit 74
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
[[ ! -e ${MANAGED_ROOT}/current && ! -L ${MANAGED_ROOT}/current ]] || \
  die "sync failure rollback left current activated"
[[ -f ${PUBLIC_BINARY} && ! -L ${PUBLIC_BINARY} ]] || \
  die "sync failure rollback did not restore the legacy binary"
grep -Fx -- "${LEGACY_BINARY_CONTENT}" "${PUBLIC_BINARY}" >/dev/null || \
  die "sync failure rollback changed the legacy binary"
if [[ ! -d ${PUBLIC_WEB} || -L ${PUBLIC_WEB} ]]; then
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure rollback did not restore the legacy web directory"
fi
grep -Fx -- "${LEGACY_WEB_CONTENT}" "${PUBLIC_WEB}/legacy.txt" >/dev/null || {
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure rollback changed the legacy web directory"
}
[[ $(stat -c '%u:%g:%a' -- "${PUBLIC_WEB}") == "0:0:755" ]] || {
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure rollback changed the legacy web directory metadata"
}
snapshot_legacy_web_tree "${PUBLIC_WEB}" "${WORK_DIR}/sync-failure-web.after.tar" || {
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure rollback could not snapshot the restored legacy web directory"
}
cmp -s -- "${WORK_DIR}/legacy-web.before.tar" \
  "${WORK_DIR}/sync-failure-web.after.tar" || {
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure rollback did not restore the legacy web tree exactly"
}
[[ ! -e ${legacy_backup_dir}/autostream-control-panel &&
  ! -L ${legacy_backup_dir}/autostream-control-panel ]] || {
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure rollback left the legacy web backup behind"
}
if [[ ! -f ${WORK_DIR}/sync-post-activation.executed ]]; then
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure injection did not reach the post-activation durability boundary"
fi
if grep -F -e "legacy public directory backup identity changed during commit" \
  -e "rollback was incomplete" "${WORK_DIR}/sync-failure.out" >/dev/null; then
  report_failed_install_probe \
    "activation sync failure" "${WORK_DIR}/sync-failure.out"
  die "sync failure rollback reported an earlier migration or incomplete rollback"
fi
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
