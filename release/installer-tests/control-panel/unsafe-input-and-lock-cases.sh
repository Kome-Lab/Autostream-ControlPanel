
restore_safe_root_anchor_fixture() {
  chmod 00755 /usr/local/bin
  [[ $(stat -c '%U:%G:%a' -- /usr/local/bin) == "root:root:755" ]] || \
    die "could not restore isolated /usr/local/bin to root:root mode 0755"
}

assert_unsafe_root_anchor_mode_rejected() {
  local mode=$1
  local output="${WORK_DIR}/unsafe-root-anchor-${mode}.out"
  local status
  local actual_mode
  restore_safe_root_anchor_fixture
  chmod "${mode}" /usr/local/bin
  actual_mode="$(stat -c '%a' -- /usr/local/bin)" || \
    die "could not inspect /usr/local/bin mode ${mode} for the unsafe root-anchor test"
  [[ ${actual_mode} == "${mode}" ]] || \
    die "could not set /usr/local/bin mode ${mode} for the unsafe root-anchor test; got ${actual_mode}"
  set +e
  "${EXTRACTED_ROOT}/install-autostream-control-panel" > "${output}" 2>&1
  status=$?
  set -e
  restore_safe_root_anchor_fixture
  [[ ${status} -ne 0 ]] || \
    die "unsafe root-anchor mode ${mode} unexpectedly passed"
  if ! grep -F -- "required system directory has unsafe mode bits: /usr/local/bin" \
    "${output}" >/dev/null; then
    printf 'control-panel installer integration test: captured installer output for unsafe root-anchor mode %s:\n' \
      "${mode}" >&2
    cat -- "${output}" >&2
    die "unsafe root-anchor mode ${mode} did not fail with the expected message"
  fi
  [[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
    die "unsafe root-anchor mode ${mode} mutated managed state"
  if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
    die "unsafe root-anchor mode ${mode} mutated the service account"
  fi
}

for unsafe_root_anchor_mode in 777 4755 2755 1755; do
  assert_unsafe_root_anchor_mode_rejected "${unsafe_root_anchor_mode}"
done

cat > "${WORK_DIR}/failing-mktemp" <<EOF
#!/bin/sh
printf '%s\n' "\$*" > "${WORK_DIR}/mktemp-failure.boundary"
exit 73
EOF
chmod 0755 "${WORK_DIR}/failing-mktemp"

set +e
unshare --mount --propagation private bash -c \
  "mount --bind '${WORK_DIR}/failing-mktemp' /usr/bin/mktemp &&
    '${EXTRACTED_ROOT}/install-autostream-control-panel'" \
  > "${WORK_DIR}/mktemp-failure.out" 2>&1
mktemp_failure_status=$?
set -e
[[ ${mktemp_failure_status} -eq 1 ]] || \
  die "INPUT_STAGE mktemp failure did not fail closed with the expected status"
[[ -f ${WORK_DIR}/mktemp-failure.boundary ]] || \
  die "mktemp failure injection did not execute the installer mktemp boundary"
grep -Fx -- "-d /var/tmp/autostream-control-panel-install.XXXXXXXX" \
  "${WORK_DIR}/mktemp-failure.boundary" >/dev/null || \
  die "mktemp failure injection did not reach the INPUT_STAGE allocation"
grep -F -- "could not create the private input staging directory" \
  "${WORK_DIR}/mktemp-failure.out" >/dev/null || \
  die "mktemp failure did not report the expected fail-closed error"
[[ ! -e /unpack && ! -L /unpack ]] || die "mktemp failure created a root-level /unpack path"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "mktemp failure mutated managed state"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "mktemp failure mutated the service account"
fi

install -d -o root -g root -m 0755 /var/lib/autostream
ln -s -- "${WORK_DIR}" "${STATE_DIR}"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/unsafe-state.out" 2>&1
unsafe_state_status=$?
set -e
[[ ${unsafe_state_status} -ne 0 ]] || die "unsafe service state symlink unexpectedly passed"
if ! grep -F -- "existing service state path is not a safe directory" \
  "${WORK_DIR}/unsafe-state.out" >/dev/null; then
  printf 'control-panel installer integration test: captured installer output for unsafe service state symlink:\n' \
    >&2
  cat -- "${WORK_DIR}/unsafe-state.out" >&2
  die "unsafe service state symlink did not fail with the expected message"
fi
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "unsafe service state validation mutated the service account"
fi
rm -f -- "${STATE_DIR}"
rmdir /var/lib/autostream

set +e
AUTOSTREAM_INSTALLER_TEST_PREFIX_VERSION=1 \
  "${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/prefix-version.out" 2>&1
prefix_version_status=$?
set -e
[[ ${prefix_version_status} -ne 0 ]] || \
  die "prefix-colliding binary version unexpectedly passed"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "wrong binary version mutated managed state"

install -d -o root -g root -m 0700 /run/autostream-updater
printf '%s\n' 'control-panel shared host-setup lock sentinel' \
  > "${SHARED_HOST_SETUP_LOCK}"
chown root:root "${SHARED_HOST_SETUP_LOCK}"
chmod 0600 "${SHARED_HOST_SETUP_LOCK}"
shared_lock_before="$(stat -c '%d:%i:%u:%g:%a:%s' -- "${SHARED_HOST_SETUP_LOCK}")"
shared_lock_hash_before="$(
  sha256sum -- "${SHARED_HOST_SETUP_LOCK}" | awk 'NR == 1 { print $1 }'
)"
shared_parent_metadata_before="$(
  for shared_parent in \
    /opt \
    /usr/local/bin \
    /usr/local/sbin \
    /usr/share \
    /etc \
    /var/lib \
    /var/backups \
    /run/autostream-updater; do
    stat -c '%n|%d:%i:%u:%g:%a' -- "${shared_parent}"
  done
)"
(
  exec 7<>"${SHARED_HOST_SETUP_LOCK}"
  flock -n 7 || die "test could not acquire the shared host-setup lock"
  set +e
  "${EXTRACTED_ROOT}/install-autostream-control-panel" \
    7>&- > "${WORK_DIR}/shared-lock-contention.out" 2>&1
  shared_contention_status=$?
  set -e
  [[ ${shared_contention_status} -eq 1 ]] || \
    die "shared host-setup lock contention did not fail closed with the expected status"
)
if ! grep -Fx -- \
  "install-autostream-control-panel: another AutoStream installer is provisioning shared host state" \
  "${WORK_DIR}/shared-lock-contention.out" >/dev/null; then
  printf '%s\n' \
    'control-panel installer integration test: captured installer output for shared host-setup lock contention:' \
    >&2
  cat -- "${WORK_DIR}/shared-lock-contention.out" >&2
  die "shared host-setup lock contention did not report the expected error"
fi
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- "${SHARED_HOST_SETUP_LOCK}") == \
  "${shared_lock_before}" &&
  $(sha256sum -- "${SHARED_HOST_SETUP_LOCK}" | awk 'NR == 1 { print $1 }') == \
    "${shared_lock_hash_before}" ]] || \
  die "shared host-setup contention replaced or truncated the permanent lock"
shared_parent_metadata_after="$(
  for shared_parent in \
    /opt \
    /usr/local/bin \
    /usr/local/sbin \
    /usr/share \
    /etc \
    /var/lib \
    /var/backups \
    /run/autostream-updater; do
    stat -c '%n|%d:%i:%u:%g:%a' -- "${shared_parent}"
  done
)"
[[ ${shared_parent_metadata_after} == "${shared_parent_metadata_before}" ]] || \
  die "shared host-setup lock contention mutated account, parents, or current"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "shared host-setup lock contention mutated account, parents, or current"
fi
for shared_contention_absent_path in \
  /opt/autostream \
  /var/lib/autostream \
  /etc/autostream \
  /etc/autostream-local-executor \
  /var/backups/autostream \
  "${UNIT_PATH}" \
  "${PUBLIC_BINARY}" \
  "${PUBLIC_WEB}" \
  "${BACKUP_EXECUTABLE}" \
  "${MANAGED_ROOT}/current" \
  "${TARGET_LOCK}"; do
  [[ ! -e ${shared_contention_absent_path} &&
    ! -L ${shared_contention_absent_path} ]] || \
    die "shared host-setup lock contention mutated account, parents, or current"
done
