
if [[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_PREFLIGHT_PROBE:-} != "1" ]]; then
  cat > "${UNIT_PATH}" <<'EOF'
[Unit]
Description=AutoStream Control Panel fixture preflight preservation probe

[Service]
Type=simple
ExecStart=/usr/bin/sleep infinity

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "${UNIT_PATH}"
  install_runtime_unit_exclusive
  rm -f -- "${UNIT_PATH}"
  systemctl daemon-reload
  fixture_service_start_attempted=true
  systemctl start "${UNIT}"
  preflight_probe_pid="$(systemctl show --property MainPID --value "${UNIT}")"
  record_fixture_process_identity "${preflight_probe_pid}"
  preflight_probe_identity="$(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}")"
  preflight_probe_hash="$(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }')"
  preflight_probe_enabled="$(systemctl is-enabled "${UNIT}" 2>/dev/null || true)"

  set +e
  AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS=1 \
    AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_PREFLIGHT_PROBE=1 \
    bash "$0" > "${WORK_DIR}/preflight-preservation.out" 2>&1
  preflight_probe_status=$?
  set -e
  [[ ${preflight_probe_status} -ne 0 ]] || \
    die "preflight preservation probe unexpectedly passed"
  grep -F -- "runner is not clean at ${RUNTIME_UNIT_PATH}" \
    "${WORK_DIR}/preflight-preservation.out" >/dev/null || \
    die "preflight preservation probe did not stop at the runtime unit"
  [[ $(stat -c '%d:%i' -- "${RUNTIME_UNIT_PATH}") == "${preflight_probe_identity}" ]] || \
    die "preflight failure replaced the existing runtime unit"
  [[ $(sha256sum "${RUNTIME_UNIT_PATH}" | awk 'NR == 1 { print $1 }') == \
    "${preflight_probe_hash}" ]] || \
    die "preflight failure changed the existing runtime unit"
  [[ $(systemctl show --property MainPID --value "${UNIT}") == "${preflight_probe_pid}" ]] || \
    die "preflight failure replaced the existing service process"
  kill -0 "${preflight_probe_pid}" || \
    die "preflight failure stopped the existing service process"
  [[ $(systemctl is-enabled "${UNIT}" 2>/dev/null || true) == "${preflight_probe_enabled}" ]] || \
    die "preflight failure changed the existing service enablement"

  systemctl stop "${UNIT}"
  fixture_service_start_attempted=false
  clear_fixture_process_identity
  assert_owned_runtime_unit_identity
  rm -f -- "${RUNTIME_UNIT_PATH}"
  systemctl daemon-reload
  runtime_unit_owned=false
  runtime_unit_identity=""
fi

if [[ ! -e /usr/bin/mariadb-dump && ! -L /usr/bin/mariadb-dump ]]; then
  install -o root -g root -m 0755 /dev/null /usr/bin/mariadb-dump
  created_mariadb_dump=true
fi
[[ -f /usr/bin/mariadb-dump && ! -L /usr/bin/mariadb-dump && -x /usr/bin/mariadb-dump ]] || \
  die "runner has an unsafe /usr/bin/mariadb-dump"

install -d -o root -g root -m 0755 \
  "${ARTIFACTS_DIR}" \
  "${EXTRACTED_ROOT}/bin" \
  "${EXTRACTED_ROOT}/backup" \
  "${EXTRACTED_ROOT}/share/autostream-control-panel" \
  "${EXTRACTED_ROOT}/systemd"
install -o root -g root -m 0755 "${INSTALLER_SOURCE}" \
  "${EXTRACTED_ROOT}/install-autostream-control-panel"

cat > "${EXTRACTED_ROOT}/bin/control-panel" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  if [ "${AUTOSTREAM_INSTALLER_TEST_PREFIX_VERSION:-}" = "1" ]; then
    printf '%s\n' 'autostream-control-panel v9.9.90'
  else
    printf '%s\n' 'autostream-control-panel v9.9.9'
  fi
  printf '%s\n' 'commit: 0123456789abcdef0123456789abcdef01234567'
  printf '%s\n' 'build_date: 2026-07-31T00:00:00Z'
  exit 0
fi
exit 99
EOF
chmod 0755 "${EXTRACTED_ROOT}/bin/control-panel"

cat > "${EXTRACTED_ROOT}/backup/autostream-backup-control-panel" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "${EXTRACTED_ROOT}/backup/autostream-backup-control-panel"

cat > "${EXTRACTED_ROOT}/systemd/autostream-control-panel.service.example" <<'EOF'
[Unit]
Description=AutoStream Control Panel integration fixture

[Service]
Type=simple
User=autostream
Group=autostream
EnvironmentFile=-/etc/autostream/control-panel.env
ExecStart=/usr/local/bin/control-panel

[Install]
WantedBy=multi-user.target
EOF
printf '%s\n' 'AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel' \
  > "${EXTRACTED_ROOT}/.env.example"
printf '%s\n' 'integration-web-asset' \
  > "${EXTRACTED_ROOT}/share/autostream-control-panel/index.html"

jq -n \
  --arg version "${VERSION}" \
  --arg commit "${BUILD_COMMIT}" \
  --arg build_date "${BUILD_DATE}" \
  --arg archive_name "${ARTIFACT_ID}.tar.gz" \
  --arg artifact_root "${ARTIFACT_ID}" \
  '{
    schema_version: 1,
    component: "control-panel",
    source_version: $version,
    commit: $commit,
    build_date: $build_date,
    platform: {
      os: "linux",
      arch: "amd64"
    },
    archive: {
      name: $archive_name,
      root: $artifact_root
    },
    compatibility: {
      minimum_agent_version: "v1.7.0",
      minimum_panel_version: null,
      rollback_compatible: true,
      database_schema: "backward_compatible"
    }
  }' > "${EXTRACTED_ROOT}/artifact-manifest.json"

rebuild_fixture_archive() {
  rm -f -- "${EXTRACTED_ROOT}/checksums.txt" "${ARCHIVE}"
  (
    cd -- "${EXTRACTED_ROOT}"
    find . -type f ! -path './checksums.txt' -print0 |
      sort -z |
      xargs -0 sha256sum > checksums.txt
  )
  tar -C "${ARTIFACTS_DIR}" -czf "${ARCHIVE}" "${ARTIFACT_ID}"
}

rebuild_fixture_archive
(
  grep -Eq '^[0-9a-f]{64}  \./artifact-manifest.json$' \
    "${EXTRACTED_ROOT}/checksums.txt" || \
    die "fixture checksum inventory does not cover artifact-manifest.json"
)
[[ $(find "${ARTIFACTS_DIR}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n') == \
  "${ARTIFACT_ID}.tar.gz" ]] || \
  die "fixture must begin with the archive as its only adjacent release file"

install -o root -g root -m 0600 "${EXTRACTED_ROOT}/artifact-manifest.json" \
  "${WORK_DIR}/artifact-manifest.valid.json"
jq '.component = "worker"' "${WORK_DIR}/artifact-manifest.valid.json" \
  > "${EXTRACTED_ROOT}/artifact-manifest.json"
rebuild_fixture_archive
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/invalid-artifact-manifest.out" 2>&1
invalid_artifact_manifest_status=$?
set -e
[[ ${invalid_artifact_manifest_status} -ne 0 ]] || \
  die "self-consistent archive with invalid artifact metadata unexpectedly passed"
grep -F -- "artifact-manifest.json does not authorize this exact artifact" \
  "${WORK_DIR}/invalid-artifact-manifest.out" >/dev/null || \
  die "invalid artifact metadata did not fail at the metadata boundary"
if grep -Eq '^jq: (error:|[0-9]+ compile errors?)' \
  "${WORK_DIR}/invalid-artifact-manifest.out"; then
  printf '%s\n' \
    'control-panel installer integration test: captured jq artifact-manifest verifier failure:' \
    >&2
  cat -- "${WORK_DIR}/invalid-artifact-manifest.out" >&2
  die "artifact manifest verifier emitted a jq parser or compile error"
fi
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "invalid artifact metadata mutated managed state"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "invalid artifact metadata mutated the service account"
fi
install -o root -g root -m 0600 "${WORK_DIR}/artifact-manifest.valid.json" \
  "${EXTRACTED_ROOT}/artifact-manifest.json"
rebuild_fixture_archive

printf '%s\n' 'canonical archive alias probe' \
  > "${ARTIFACTS_DIR}/control-panel-canonical-alias-file"
tar -C "${ARTIFACTS_DIR}" -czf "${ARCHIVE}" \
  "${ARTIFACT_ID}" \
  --transform="s#^control-panel-canonical-alias-file\$#${ARTIFACT_ID}#" \
  control-panel-canonical-alias-file
rm -f -- "${ARTIFACTS_DIR}/control-panel-canonical-alias-file"
set +e
"${EXTRACTED_ROOT}/install-autostream-control-panel" \
  > "${WORK_DIR}/duplicate-archive-entry.out" 2>&1
duplicate_archive_status=$?
set -e
[[ ${duplicate_archive_status} -ne 0 ]] || \
  die "archive with a duplicate canonical path unexpectedly passed"
grep -F -- "release archive contains duplicate paths" \
  "${WORK_DIR}/duplicate-archive-entry.out" >/dev/null || \
  die "duplicate archive path did not fail at the archive layout boundary"
[[ ! -e ${MANAGED_ROOT} && ! -L ${MANAGED_ROOT} ]] || \
  die "duplicate archive path mutated managed state"
if id autostream >/dev/null 2>&1 || getent group autostream >/dev/null 2>&1; then
  die "duplicate archive path mutated the service account"
fi
rebuild_fixture_archive
archive_sha256="$(sha256sum "${ARCHIVE}" | awk 'NR == 1 { print $1 }')"

printf '%s\n' 'intentionally stale and ignored' > "${ARCHIVE}.sha256"
printf '%s\n' '{not valid json' > "${ARTIFACTS_DIR}/release-manifest.json"
printf '%s\n' 'intentionally stale and ignored' \
  > "${ARTIFACTS_DIR}/release-manifest.json.sha256"
