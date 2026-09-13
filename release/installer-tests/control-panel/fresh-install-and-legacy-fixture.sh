
"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/fresh.out"
[[ -L ${PUBLIC_BINARY} && -L ${PUBLIC_WEB} ]] || \
  die "fresh install did not install stable public links"
fresh_release="$(readlink -f -- "${MANAGED_ROOT}/current")"
[[ ${fresh_release} == "${MANAGED_ROOT}/releases/"* ]] || \
  die "fresh managed release resolved outside the release root"
for directory_path in \
  "${fresh_release}" \
  "${fresh_release}/bin" \
  "${fresh_release}/backup" \
  "${fresh_release}/share" \
  "${fresh_release}/share/autostream-control-panel" \
  "${fresh_release}/systemd"; do
  [[ $(stat -c '%U:%G:%a' -- "${directory_path}") == "root:root:755" ]] || \
    die "fresh managed directory was not normalized to root:root mode 0755"
done
for executable_path in \
  "${fresh_release}/bin/control-panel" \
  "${fresh_release}/backup/autostream-backup-control-panel" \
  "${fresh_release}/install-autostream-control-panel"; do
  [[ $(stat -c '%U:%G:%a' -- "${executable_path}") == "root:root:755" ]] || \
    die "fresh managed executable was not normalized to root:root mode 0755"
done
for regular_path in \
  "${fresh_release}/.env.example" \
  "${fresh_release}/artifact-manifest.json" \
  "${fresh_release}/checksums.txt" \
  "${fresh_release}/share/autostream-control-panel/index.html" \
  "${fresh_release}/systemd/autostream-control-panel.service.example"; do
  [[ $(stat -c '%U:%G:%a' -- "${regular_path}") == "root:root:644" ]] || \
    die "fresh managed regular file was not normalized to root:root mode 0644"
done
for marker_path in \
  "${fresh_release}/.artifact-sha256" \
  "${fresh_release}/.version"; do
  [[ $(stat -c '%U:%G:%a' -- "${marker_path}") == "root:root:444" ]] || \
    die "fresh managed marker was not normalized to root:root mode 0444"
done
runuser -u autostream -- "${fresh_release}/bin/control-panel" --version |
  grep -Fx -- "autostream-control-panel ${VERSION}" >/dev/null || \
  die "fresh managed release was not runnable by autostream"
snapshot_managed_release_tree "${fresh_release}" "${WORK_DIR}/fresh-release.before"
"${EXTRACTED_ROOT}/install-autostream-control-panel" > "${WORK_DIR}/fresh-idempotent.out"
snapshot_managed_release_tree "${fresh_release}" "${WORK_DIR}/fresh-release.after"
cmp -s -- "${WORK_DIR}/fresh-release.before" "${WORK_DIR}/fresh-release.after" || \
  die "idempotent reinstall changed existing managed release metadata or content"
[[ -f ${ENV_PATH} && ! -L ${ENV_PATH} ]] || die "fresh install did not seed the environment"
[[ -f ${MARIADB_DEFAULTS} && ! -L ${MARIADB_DEFAULTS} ]] || \
  die "fresh install did not seed the MariaDB defaults"
[[ $(stat -c '%U:%G:%a' -- "${ENV_PATH}") == "root:root:640" ]] || \
  die "fresh environment ownership or mode is invalid"
[[ $(stat -c '%U:%G:%a' -- "${MARIADB_DEFAULTS}") == "root:root:600" ]] || \
  die "fresh MariaDB defaults ownership or mode is invalid"
systemctl is-active --quiet "${UNIT}" && die "fresh installer unexpectedly started the service"
systemctl is-enabled --quiet "${UNIT}" && die "fresh installer unexpectedly enabled the service"
grep -F -- "sudo systemctl enable --now ${UNIT}" "${WORK_DIR}/fresh.out" >/dev/null || \
  die "fresh install did not print the explicit start command"

rm -f -- \
  "${PUBLIC_BINARY}" \
  "${ENV_PATH}" \
  "${UNIT_PATH}" \
  "${BACKUP_EXECUTABLE}" \
  "${MARIADB_DEFAULTS}"
rm -rf -- \
  "${PUBLIC_WEB}" \
  "${STATE_DIR}" \
  "${MANAGED_ROOT}" \
  "${DATABASE_BACKUP_DIR}" \
  "${INSTALL_BACKUP_ROOT}"
systemctl daemon-reload

install -d -o root -g root -m 0755 /etc/autostream /var/lib/autostream
install -d -o autostream -g autostream -m 0750 "${STATE_DIR}"
install -d -o root -g root -m 0755 "${PUBLIC_WEB}"
install -d -o root -g root -m 0710 "${PUBLIC_WEB}/assets"
printf '%s\n' "${LEGACY_BINARY_CONTENT}" > "${PUBLIC_BINARY}"
chmod 0755 "${PUBLIC_BINARY}"
printf '%s\n' "${LEGACY_WEB_CONTENT}" > "${PUBLIC_WEB}/legacy.txt"
printf '%s\n' "nested legacy web content" > "${PUBLIC_WEB}/assets/nested.txt"
chown root:root "${PUBLIC_WEB}/legacy.txt" "${PUBLIC_WEB}/assets/nested.txt"
chmod 0644 "${PUBLIC_WEB}/legacy.txt"
chmod 0640 "${PUBLIC_WEB}/assets/nested.txt"
ln -s -- ../legacy.txt "${PUBLIC_WEB}/assets/legacy-link"
printf '%s\n' "${LEGACY_ENV_CONTENT}" > "${ENV_PATH}"
chmod 0640 "${ENV_PATH}"
printf '%s\n' "${LEGACY_HELPER_CONTENT}" > "${BACKUP_EXECUTABLE}"
chmod 0700 "${BACKUP_EXECUTABLE}"
install -d -o root -g root -m 0700 /etc/autostream-local-executor
printf '%s\n' "${LEGACY_DB_CONTENT}" > "${MARIADB_DEFAULTS}"
chmod 0600 "${MARIADB_DEFAULTS}"
cat > "${UNIT_PATH}" <<EOF
[Unit]
Description=${LEGACY_UNIT_CONTENT}

[Service]
Type=simple
ExecStart=/usr/bin/sleep infinity

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "${UNIT_PATH}"
install_runtime_unit_exclusive
systemctl daemon-reload
fixture_service_start_attempted=true
systemctl start "${UNIT}"
record_fixture_process_identity "$(systemctl show --property MainPID --value "${UNIT}")"
kill -0 "${old_pid}" || die "legacy service PID is not alive"
assert_legacy_runtime_unit_loaded
legacy_unit_file_state="$(systemctl is-enabled "${UNIT}" 2>/dev/null || true)"
[[ ${legacy_unit_file_state} == "disabled" ]] || \
  die "legacy fixture must begin disabled, got ${legacy_unit_file_state:-unknown}"

env_before="$(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }')"
db_before="$(sha256sum "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }')"
unit_before="$(sha256sum "${UNIT_PATH}" | awk 'NR == 1 { print $1 }')"
helper_before="$(sha256sum "${BACKUP_EXECUTABLE}" | awk 'NR == 1 { print $1 }')"
snapshot_legacy_web_tree "${PUBLIC_WEB}" "${WORK_DIR}/legacy-web.before.tar" || \
  die "could not snapshot the legacy web fixture"

legacy_backup_dir="${INSTALL_BACKUP_ROOT}/${VERSION}-${archive_sha256:0:12}"
install -d -o root -g root -m 0700 "${legacy_backup_dir}"
install -o root -g root -m 0644 "${UNIT_PATH}" \
  "${legacy_backup_dir}/autostream-control-panel.service"
install -o root -g root -m 0700 "${BACKUP_EXECUTABLE}" \
  "${legacy_backup_dir}/autostream-backup-control-panel"
install -o root -g root -m 0755 "${PUBLIC_BINARY}" \
  "${legacy_backup_dir}/control-panel"
legacy_binary_live_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${PUBLIC_BINARY}"
)"
legacy_binary_live_hash_before="$(
  sha256sum -- "${PUBLIC_BINARY}" | awk 'NR == 1 { print $1 }'
)"
chown 65534:65534 "${legacy_backup_dir}/control-panel"
nonroot_legacy_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/control-panel"
)"
nonroot_legacy_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/control-panel" | awk 'NR == 1 { print $1 }'
)"
nonroot_state_metadata_before="$(stat -c '%d:%i:%u:%g:%a' -- "${STATE_DIR}")"
nonroot_account_before="$(getent passwd autostream)"
nonroot_group_before="$(getent group autostream)"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/nonroot-legacy-public-backup.out" 2>&1
nonroot_legacy_public_backup_status=$?
set -e
[[ ${nonroot_legacy_public_backup_status} -ne 0 ]] || \
  die "non-root legacy public backup unexpectedly passed"
grep -F -- "legacy public backup must be owned by root:root" \
  "${WORK_DIR}/nonroot-legacy-public-backup.out" >/dev/null || \
  die "non-root legacy public backup did not fail at the ownership boundary"
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- "${PUBLIC_BINARY}") == \
  "${legacy_binary_live_metadata_before}" &&
  $(sha256sum -- "${PUBLIC_BINARY}" | awk 'NR == 1 { print $1 }') == \
    "${legacy_binary_live_hash_before}" ]] || \
  die "non-root legacy public backup changed the live or backup boundary"
[[ $(stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/control-panel") == \
  "${nonroot_legacy_backup_metadata_before}" &&
  $(sha256sum -- "${legacy_backup_dir}/control-panel" | awk 'NR == 1 { print $1 }') == \
    "${nonroot_legacy_backup_hash_before}" ]] || \
  die "non-root legacy public backup changed the live or backup boundary"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "non-root legacy public backup changed the running legacy process"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} &&
  $(stat -c '%d:%i:%u:%g:%a' -- "${STATE_DIR}") == "${nonroot_state_metadata_before}" &&
  $(getent passwd autostream) == "${nonroot_account_before}" &&
  $(getent group autostream) == "${nonroot_group_before}" ]] || \
  die "non-root legacy public backup changed persistent installer state"
chown root:root "${legacy_backup_dir}/control-panel"
legacy_unit_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/autostream-control-panel.service"
)"
legacy_unit_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/autostream-control-panel.service" | awk 'NR == 1 { print $1 }'
)"
legacy_helper_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/autostream-backup-control-panel"
)"
legacy_helper_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/autostream-backup-control-panel" | awk 'NR == 1 { print $1 }'
)"
legacy_binary_backup_metadata_before="$(
  stat -c '%d:%i:%u:%g:%a:%s' -- "${legacy_backup_dir}/control-panel"
)"
legacy_binary_backup_hash_before="$(
  sha256sum -- "${legacy_backup_dir}/control-panel" | awk 'NR == 1 { print $1 }'
)"

(
  exec 7<>"${TARGET_LOCK}"
  flock -n 7 || die "test could not acquire the updater target lock"
  set +e
  "${EXTRACTED_ROOT}/install-autostream-control-panel" \
    > "${WORK_DIR}/contention.out" 2>&1
  contention_status=$?
  set -e
  [[ ${contention_status} -ne 0 ]] || die "installer ignored updater lock contention"
)
grep -F -- "another privileged update is already active for ${UNIT}" \
  "${WORK_DIR}/contention.out" >/dev/null || \
  die "lock contention did not fail with the expected message"
[[ $(systemctl show --property MainPID --value "${UNIT}") == "${old_pid}" ]] || \
  die "lock contention changed the running legacy process"
[[ $(sha256sum "${ENV_PATH}" | awk 'NR == 1 { print $1 }') == "${env_before}" ]] || \
  die "lock contention changed the existing environment"
