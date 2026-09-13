
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

hostile_gid_group_database_before="$(
  sha256sum -- /etc/group | awk 'NR == 1 { print $1 }'
)"
hostile_gid_gshadow_database_before="$(
  sha256sum -- /etc/gshadow | awk 'NR == 1 { print $1 }'
)"
[[ ${hostile_gid_group_database_before} =~ ^[0-9a-f]{64}$ &&
  ${hostile_gid_gshadow_database_before} =~ ^[0-9a-f]{64}$ ]] || \
  die "could not snapshot local group databases before the hostile GID 0 fixture"
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
groupdel --force autostream
if getent group autostream >/dev/null 2>&1; then
  die "hostile GID 0 fixture cleanup left the service group behind"
fi
if [[ $(sha256sum -- /etc/group | awk 'NR == 1 { print $1 }') != \
    "${hostile_gid_group_database_before}" ||
  $(sha256sum -- /etc/gshadow | awk 'NR == 1 { print $1 }') != \
    "${hostile_gid_gshadow_database_before}" ]]; then
  die "hostile GID 0 fixture cleanup changed the local group databases"
fi

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
