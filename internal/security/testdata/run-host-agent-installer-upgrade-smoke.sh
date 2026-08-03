#!/bin/bash
set -euo pipefail

umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LC_ALL=C

[[ $(id -u) -eq 0 ]] || {
  printf '%s\n' 'host agent installer upgrade smoke requires root' >&2
  exit 1
}
[[ $# -eq 1 ]] || {
  printf '%s\n' 'usage: run-host-agent-installer-upgrade-smoke.sh REPOSITORY_ROOT' >&2
  exit 1
}

readonly REPOSITORY_ROOT=$1
readonly VERSION=v9.9.9
readonly BUILD_COMMIT=0123456789abcdef0123456789abcdef01234567
readonly BUILD_DATE=2026-07-31T00:00:00Z
case "$(uname -m)" in
  x86_64) readonly ARCH=amd64 ;;
  aarch64|arm64) readonly ARCH=arm64 ;;
  *)
    printf 'unsupported smoke architecture: %s\n' "$(uname -m)" >&2
    exit 1
    ;;
esac
readonly ARTIFACT_ID="autostream-host-agent_${VERSION}_linux_${ARCH}"
readonly PACKAGE_ROOT="/root/${ARTIFACT_ID}"
readonly ARCHIVE="/root/${ARTIFACT_ID}.tar.gz"
readonly INSTALLER="${PACKAGE_ROOT}/install/install-autostream-host-agent"
readonly HELPER_LOG=/root/autostream-host-agent-upgrade-helper.log
readonly HELPER_FAIL_MARKER=/root/autostream-host-agent-upgrade-helper.fail
readonly HELPER_SIGNAL_MODE=/root/autostream-host-agent-upgrade-helper.signal-mode
readonly HELPER_SIGNAL_READY=/root/autostream-host-agent-upgrade-helper.signal-ready
readonly HELPER_SIGNAL_RECEIVED=/root/autostream-host-agent-upgrade-helper.signal-received
readonly HELPER_SIGNAL_FINISHED=/root/autostream-host-agent-upgrade-helper.signal-finished
readonly SIGNAL_OUTPUT=/root/autostream-host-agent-upgrade-signal-output.log
readonly SYSTEMCTL_LOG=/root/autostream-host-agent-upgrade-systemctl.log
readonly ARCHIVE_BACKUP="/root/.${ARTIFACT_ID}.valid.tar.gz"
readonly HOST_BINARY_BACKUP=/root/.autostream-host-agent-upgrade-smoke.valid
readonly IDENTITY_PATH=/etc/autostream-host-agent/identity.json
readonly POLICY_PATH=/etc/autostream-local-executor/policy.json
readonly COMPLETION='Managed Host Agent and Local Executor runtime upgrade complete.'

for path in \
  "${PACKAGE_ROOT}" \
  "${ARCHIVE}" \
  "${ARCHIVE_BACKUP}" \
  "${HOST_BINARY_BACKUP}" \
  "${HELPER_LOG}" \
  "${HELPER_FAIL_MARKER}" \
  "${HELPER_SIGNAL_MODE}" \
  "${HELPER_SIGNAL_READY}" \
  "${HELPER_SIGNAL_RECEIVED}" \
  "${HELPER_SIGNAL_FINISHED}" \
  "${SIGNAL_OUTPUT}" \
  "${SYSTEMCTL_LOG}" \
  /etc/autostream-host-agent \
  /etc/autostream-local-executor; do
  [[ ! -e ${path} && ! -L ${path} ]] || {
    printf 'upgrade smoke requires an isolated container; path already exists: %s\n' \
      "${path}" >&2
    exit 1
  }
done

install -d -o root -g root -m 0755 \
  "${PACKAGE_ROOT}/bin" \
  "${PACKAGE_ROOT}/install" \
  "${PACKAGE_ROOT}/systemd"
install -o root -g root -m 0755 \
  "${REPOSITORY_ROOT}/release/install-autostream-host-agent" \
  "${INSTALLER}"
install -o root -g root -m 0755 \
  "${REPOSITORY_ROOT}/release/uninstall-autostream-host-agent" \
  "${PACKAGE_ROOT}/install/uninstall-autostream-host-agent"
install -o root -g root -m 0755 \
  "${REPOSITORY_ROOT}/release/install-autostream-local-executor" \
  "${PACKAGE_ROOT}/install/install-autostream-local-executor"
install -o root -g root -m 0755 \
  "${REPOSITORY_ROOT}/release/uninstall-autostream-local-executor" \
  "${PACKAGE_ROOT}/install/uninstall-autostream-local-executor"
install -o root -g root -m 0644 \
  "${REPOSITORY_ROOT}/release/autostream-local-executor-policy.json.example" \
  "${PACKAGE_ROOT}/autostream-local-executor-policy.json.example"
install -o root -g root -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-host-agent.service.example" \
  "${PACKAGE_ROOT}/systemd/autostream-host-agent.service"
install -o root -g root -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-local-executor.service.example" \
  "${PACKAGE_ROOT}/systemd/autostream-local-executor.service"
install -o root -g root -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-local-executor.socket.example" \
  "${PACKAGE_ROOT}/systemd/autostream-local-executor.socket"
install -o root -g root -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-local-executor.tmpfiles.example" \
  "${PACKAGE_ROOT}/systemd/autostream-local-executor.tmpfiles"
install -o root -g root -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-host-self-update-recovery@.service.example" \
  "${PACKAGE_ROOT}/systemd/autostream-host-self-update-recovery@.service"
install -o root -g root -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-host-self-update-recovery@.timer.example" \
  "${PACKAGE_ROOT}/systemd/autostream-host-self-update-recovery@.timer"

fake_host_agent=$(mktemp)
cat > "${fake_host_agent}" <<'EOF'
#!/bin/bash
set -euo pipefail
[[ ${1:-} == --version ]] || {
  printf 'unexpected Host Agent invocation: %s\n' "$*" >&2
  exit 91
}
printf '%s\n' \
  'autostream-host-agent v9.9.9' \
  'commit: 0123456789abcdef0123456789abcdef01234567' \
  'build_date: 2026-07-31T00:00:00Z'
EOF
install -o root -g root -m 0755 \
  "${fake_host_agent}" "${PACKAGE_ROOT}/bin/autostream-host-agent"
rm -f -- "${fake_host_agent}"

fake_local_executor=$(mktemp)
cat > "${fake_local_executor}" <<'EOF'
#!/bin/bash
set -euo pipefail
readonly HELPER_LOG=/root/autostream-host-agent-upgrade-helper.log
readonly HELPER_FAIL_MARKER=/root/autostream-host-agent-upgrade-helper.fail
readonly HELPER_SIGNAL_MODE=/root/autostream-host-agent-upgrade-helper.signal-mode
readonly HELPER_SIGNAL_READY=/root/autostream-host-agent-upgrade-helper.signal-ready
readonly HELPER_SIGNAL_RECEIVED=/root/autostream-host-agent-upgrade-helper.signal-received
readonly HELPER_SIGNAL_FINISHED=/root/autostream-host-agent-upgrade-helper.signal-finished
case "${1:-}" in
  --version)
    printf '%s\n' \
      'autostream-local-executor v9.9.9' \
      'commit: 0123456789abcdef0123456789abcdef01234567' \
      'build_date: 2026-07-31T00:00:00Z' \
      'mutation_protocol: 2' \
      'recovery_protocol: 2'
    ;;
  manual-upgrade-host-runtime)
    [[ $# -eq 7 &&
      ${2:-} == --artifact-root && -n ${3:-} &&
      ${4:-} == --archive-sha256 && -n ${5:-} &&
      ${6:-} == --archive-size && ${7:-} =~ ^[1-9][0-9]*$ ]] || {
      printf 'unexpected manual upgrade invocation: %s\n' "$*" >&2
      exit 92
    }
    [[ $(stat -c '%U:%G:%a:%h' -- \
      "${3}/systemd/autostream-host-self-update-recovery@.service") == \
      root:root:644:1 ]] || {
      printf '%s\n' \
        'candidate Host recovery service was not normalized to root:root 0644 nlink 1' >&2
      exit 95
    }
    {
      printf 'artifact-root=%s\n' "$3"
      printf 'archive-sha256=%s\n' "$5"
      printf 'archive-size=%s\n' "$7"
    } > "${HELPER_LOG}"
    chmod 0600 "${HELPER_LOG}"
    [[ ! -e ${HELPER_FAIL_MARKER} ]] || exit 73
    if [[ -f ${HELPER_SIGNAL_MODE} && ! -L ${HELPER_SIGNAL_MODE} ]]; then
      signal_mode="$(<"${HELPER_SIGNAL_MODE}")"
      [[ ${signal_mode} == success || ${signal_mode} == failure ]] || exit 94
      finish_after_forwarded_signal() {
        local signal=$1
        printf '%s\n' "${signal}" > "${HELPER_SIGNAL_RECEIVED}"
        chmod 0600 "${HELPER_SIGNAL_RECEIVED}"
        sleep 1
        printf '%s\n' "${signal}" > "${HELPER_SIGNAL_FINISHED}"
        chmod 0600 "${HELPER_SIGNAL_FINISHED}"
        if [[ ${signal_mode} == success ]]; then
          exit 0
        fi
        exit 75
      }
      trap 'finish_after_forwarded_signal INT' INT
      trap 'finish_after_forwarded_signal TERM' TERM
      printf '%s\n' "$$" > "${HELPER_SIGNAL_READY}"
      chmod 0600 "${HELPER_SIGNAL_READY}"
      while true; do
        sleep 1
      done
    fi
    ;;
  *)
    printf 'unexpected Local Executor invocation: %s\n' "$*" >&2
    exit 93
    ;;
esac
EOF
install -o root -g root -m 0755 \
  "${fake_local_executor}" \
  "${PACKAGE_ROOT}/bin/autostream-local-executor"
rm -f -- "${fake_local_executor}"

cat > "${PACKAGE_ROOT}/artifact-manifest.json" <<EOF
{
  "schema_version": 1,
  "component": "host-agent",
  "source_version": "${VERSION}",
  "commit": "${BUILD_COMMIT}",
  "build_date": "${BUILD_DATE}",
  "platform": {
    "os": "linux",
    "arch": "${ARCH}"
  },
  "archive": {
    "name": "${ARTIFACT_ID}.tar.gz",
    "root": "${ARTIFACT_ID}"
  },
  "compatibility": {
    "minimum_agent_version": null,
    "minimum_panel_version": "${VERSION}",
    "rollback_compatible": true,
    "database_schema": "none"
  }
}
EOF

rebuild_bundle_archive() {
  rm -f -- "${PACKAGE_ROOT}/checksums.txt" "${ARCHIVE}"
  (
    cd -- "${PACKAGE_ROOT}"
    find . -type f ! -path './checksums.txt' -print0 |
      sort -z |
      xargs -0 sha256sum > checksums.txt
  )
  tar -C /root -czf "${ARCHIVE}" "${ARTIFACT_ID}"
}

private_stage_listing() {
  find /var/tmp -mindepth 1 -maxdepth 1 -type d \
    -name 'autostream-host-agent-install.*' -printf '%f\n' |
    LC_ALL=C sort
}

readonly PRIVATE_STAGE_BEFORE="$(private_stage_listing)"
assert_private_stage_cleaned() {
  local after
  after="$(private_stage_listing)"
  [[ ${after} == "${PRIVATE_STAGE_BEFORE}" ]] || {
    printf '%s\n%s\n' \
      'Host Agent upgrade installer left a private bundle stage behind:' \
      "${after}" >&2
    exit 1
  }
  [[ ! -e ${SYSTEMCTL_LOG} && ! -L ${SYSTEMCTL_LOG} ]] || {
    printf '%s\n' 'delegating Host Agent upgrade unexpectedly invoked systemctl' >&2
    exit 1
  }
}

assert_no_completion() {
  local output=$1
  if grep -Fq -- "${COMPLETION}" <<<"${output}"; then
    printf '%s\n%s\n' \
      'failed Host Agent upgrade printed its completion marker:' \
      "${output}" >&2
    exit 1
  fi
}

assert_helper_not_called() {
  [[ ! -e ${HELPER_LOG} && ! -L ${HELPER_LOG} ]] || {
    printf '%s\n' 'Host Agent upgrade invoked the candidate helper before validation completed' >&2
    exit 1
  }
}

assert_helper_arguments() {
  local artifact_root
  [[ -f ${HELPER_LOG} && ! -L ${HELPER_LOG} ]] || {
    printf '%s\n' 'candidate Local Executor did not record the manual upgrade request' >&2
    exit 1
  }
  [[ $(stat -c '%U:%G:%a' -- "${HELPER_LOG}") == root:root:600 ]] || {
    printf '%s\n' 'candidate Local Executor request log is not root:root 0600' >&2
    exit 1
  }
  artifact_root=$(awk -F= '$1 == "artifact-root" { sub(/^artifact-root=/, ""); print }' \
    "${HELPER_LOG}")
  case "${artifact_root}" in
    /var/tmp/autostream-host-agent-install.*/unpack/"${ARTIFACT_ID}") ;;
    *)
      printf 'candidate Local Executor received an unexpected artifact root: %s\n' \
        "${artifact_root}" >&2
      exit 1
      ;;
  esac
  grep -Fx -- "archive-sha256=${EXPECTED_ARCHIVE_SHA256}" \
    "${HELPER_LOG}" >/dev/null
  grep -Fx -- "archive-size=${EXPECTED_ARCHIVE_SIZE}" \
    "${HELPER_LOG}" >/dev/null
}

getent group autostream-host-agent >/dev/null 2>&1 || \
  groupadd --system autostream-host-agent
install -d -o root -g autostream-host-agent -m 0750 \
  /etc/autostream-host-agent
install -d -o root -g root -m 0700 \
  /etc/autostream-local-executor
printf '%s\n' \
  '{"panel_url":"https://panel.example.com","node_id":"host-smoke","runtime_token":"sentinel-token","service_name":"Host smoke"}' \
  > "${IDENTITY_PATH}"
chown root:autostream-host-agent "${IDENTITY_PATH}"
chmod 0640 "${IDENTITY_PATH}"
printf '%s\n' '{"sentinel":"local-executor-policy"}' > "${POLICY_PATH}"
chown root:root "${POLICY_PATH}"
chmod 0600 "${POLICY_PATH}"

readonly IDENTITY_STAT_BEFORE="$(stat -c '%d:%i:%u:%g:%a' -- "${IDENTITY_PATH}")"
readonly IDENTITY_SHA_BEFORE="$(sha256sum -- "${IDENTITY_PATH}" | awk 'NR == 1 { print $1 }')"
readonly POLICY_STAT_BEFORE="$(stat -c '%d:%i:%u:%g:%a' -- "${POLICY_PATH}")"
readonly POLICY_SHA_BEFORE="$(sha256sum -- "${POLICY_PATH}" | awk 'NR == 1 { print $1 }')"

assert_sentinels_unchanged() {
  [[ $(stat -c '%d:%i:%u:%g:%a' -- "${IDENTITY_PATH}") == \
      "${IDENTITY_STAT_BEFORE}" &&
    $(sha256sum -- "${IDENTITY_PATH}" | awk 'NR == 1 { print $1 }') == \
      "${IDENTITY_SHA_BEFORE}" ]] || {
    printf '%s\n' 'Host Agent upgrade changed the installed identity sentinel' >&2
    exit 1
  }
  [[ $(stat -c '%d:%i:%u:%g:%a' -- "${POLICY_PATH}") == \
      "${POLICY_STAT_BEFORE}" &&
    $(sha256sum -- "${POLICY_PATH}" | awk 'NR == 1 { print $1 }') == \
      "${POLICY_SHA_BEFORE}" ]] || {
    printf '%s\n' 'Host Agent upgrade changed the Local Executor policy sentinel' >&2
    exit 1
  }
}

rebuild_bundle_archive
expected_manifest_sha="$(sha256sum -- "${PACKAGE_ROOT}/artifact-manifest.json" |
  awk 'NR == 1 { print $1 }')"
if ! command -v jq >/dev/null 2>&1; then
  jq_shim=$(mktemp)
  cat > "${jq_shim}" <<EOF
#!/bin/bash
set -euo pipefail
manifest="\${!#}"
actual_sha="\$(sha256sum -- "\${manifest}" | awk 'NR == 1 { print \$1 }')"
[[ \${actual_sha} == "${expected_manifest_sha}" ]] || exit 1
case " \$* " in
  *" .commit "*)
    printf '%s\n' '${BUILD_COMMIT}'
    ;;
  *" .build_date "*)
    printf '%s\n' '${BUILD_DATE}'
    ;;
  *)
    exit 0
    ;;
esac
EOF
  install -o root -g root -m 0755 "${jq_shim}" /usr/bin/jq
  rm -f -- "${jq_shim}"
fi
if ! command -v systemctl >/dev/null 2>&1; then
  systemctl_shim=$(mktemp)
  cat > "${systemctl_shim}" <<'EOF'
#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" > /root/autostream-host-agent-upgrade-systemctl.log
exit 98
EOF
  install -o root -g root -m 0755 "${systemctl_shim}" /usr/bin/systemctl
  rm -f -- "${systemctl_shim}"
fi

rm -f -- "${HELPER_LOG}"
if mode_output="$("${INSTALLER}" --upgrade --prepare 2>&1)"; then
  printf '%s\n' 'Host Agent installer accepted --upgrade with --prepare' >&2
  exit 1
fi
grep -Fq -- '--prepare, --config, and --upgrade are mutually exclusive' \
  <<<"${mode_output}"
assert_no_completion "${mode_output}"
assert_helper_not_called
assert_private_stage_cleaned
assert_sentinels_unchanged

rm -f -- "${HELPER_LOG}"
if mode_output="$("${INSTALLER}" --upgrade --config "${IDENTITY_PATH}" 2>&1)"; then
  printf '%s\n' 'Host Agent installer accepted --upgrade with --config' >&2
  exit 1
fi
grep -Fq -- '--prepare, --config, and --upgrade are mutually exclusive' \
  <<<"${mode_output}"
assert_no_completion "${mode_output}"
assert_helper_not_called
assert_private_stage_cleaned
assert_sentinels_unchanged

mv -- "${ARCHIVE}" "${ARCHIVE_BACKUP}"
rm -f -- "${HELPER_LOG}"
if missing_output="$("${INSTALLER}" --upgrade 2>&1)"; then
  printf '%s\n' 'Host Agent upgrade accepted a missing adjacent archive' >&2
  exit 1
fi
mv -- "${ARCHIVE_BACKUP}" "${ARCHIVE}"
assert_no_completion "${missing_output}"
assert_helper_not_called
assert_private_stage_cleaned
assert_sentinels_unchanged

install -o root -g root -m 0600 "${ARCHIVE}" "${ARCHIVE_BACKUP}"
install -o root -g root -m 0755 \
  "${PACKAGE_ROOT}/bin/autostream-host-agent" "${HOST_BINARY_BACKUP}"
printf '%s\n' '# adjacent archive checksum tamper' >> \
  "${PACKAGE_ROOT}/bin/autostream-host-agent"
rm -f -- "${ARCHIVE}"
tar -C /root -czf "${ARCHIVE}" "${ARTIFACT_ID}"
rm -f -- "${HELPER_LOG}"
if tampered_output="$("${INSTALLER}" --upgrade 2>&1)"; then
  printf '%s\n' 'Host Agent upgrade accepted a modified adjacent archive' >&2
  exit 1
fi
install -o root -g root -m 0755 \
  "${HOST_BINARY_BACKUP}" "${PACKAGE_ROOT}/bin/autostream-host-agent"
install -o root -g root -m 0600 "${ARCHIVE_BACKUP}" "${ARCHIVE}"
rm -f -- "${HOST_BINARY_BACKUP}" "${ARCHIVE_BACKUP}"
assert_no_completion "${tampered_output}"
assert_helper_not_called
assert_private_stage_cleaned
assert_sentinels_unchanged

readonly EXPECTED_ARCHIVE_SHA256="$(sha256sum -- "${ARCHIVE}" |
  awk 'NR == 1 { print $1 }')"
readonly EXPECTED_ARCHIVE_SIZE="$(stat -c %s -- "${ARCHIVE}")"

run_wrapper_signal_case() {
  local signal_mode=$1
  local expect_success=$2
  local expected_status=$3
  local wrapper_pid
  local wrapper_status
  local candidate_pid
  local attempts=0

  printf '%s\n' "${signal_mode}" > "${HELPER_SIGNAL_MODE}"
  chmod 0600 "${HELPER_SIGNAL_MODE}"
  rm -f -- \
    "${HELPER_LOG}" \
    "${HELPER_SIGNAL_READY}" \
    "${HELPER_SIGNAL_RECEIVED}" \
    "${HELPER_SIGNAL_FINISHED}" \
    "${SIGNAL_OUTPUT}"

  "${INSTALLER}" --upgrade > "${SIGNAL_OUTPUT}" 2>&1 &
  wrapper_pid=$!
  while [[ ! -f ${HELPER_SIGNAL_READY} ]]; do
    if ! kill -0 "${wrapper_pid}" 2>/dev/null; then
      if wait "${wrapper_pid}"; then
        wrapper_status=0
      else
        wrapper_status=$?
      fi
      printf 'Host Agent upgrade wrapper exited with status %s before its candidate was ready:\n' \
        "${wrapper_status}" >&2
      sed -n '1,160p' "${SIGNAL_OUTPUT}" >&2
      exit 1
    fi
    attempts=$((attempts + 1))
    if [[ ${attempts} -ge 200 ]]; then
      kill -TERM "${wrapper_pid}" 2>/dev/null || true
      wait "${wrapper_pid}" 2>/dev/null || true
      printf '%s\n' 'timed out waiting for the candidate Local Executor signal fixture' >&2
      exit 1
    fi
    sleep 0.05
  done

  candidate_pid="$(<"${HELPER_SIGNAL_READY}")"
  [[ ${candidate_pid} =~ ^[1-9][0-9]*$ ]] || {
    printf 'candidate signal fixture recorded an invalid PID: %s\n' "${candidate_pid}" >&2
    exit 1
  }
  kill -TERM "${wrapper_pid}"
  if wait "${wrapper_pid}"; then
    wrapper_status=0
  else
    wrapper_status=$?
  fi

  [[ ${wrapper_status} -eq ${expected_status} ]] || {
    printf 'Host Agent upgrade wrapper status=%s, expected candidate status=%s:\n' \
      "${wrapper_status}" "${expected_status}" >&2
    sed -n '1,160p' "${SIGNAL_OUTPUT}" >&2
    exit 1
  }

  [[ -f ${HELPER_SIGNAL_RECEIVED} && ! -L ${HELPER_SIGNAL_RECEIVED} &&
    $(<"${HELPER_SIGNAL_RECEIVED}") == TERM ]] || {
    printf '%s\n' 'wrapper-only SIGTERM was not forwarded to the candidate Local Executor' >&2
    exit 1
  }
  [[ -f ${HELPER_SIGNAL_FINISHED} && ! -L ${HELPER_SIGNAL_FINISHED} &&
    $(<"${HELPER_SIGNAL_FINISHED}") == TERM ]] || {
    kill -TERM "${candidate_pid}" 2>/dev/null || true
    printf '%s\n' 'Host Agent upgrade wrapper did not wait for the signaled candidate to finish' >&2
    exit 1
  }
  if kill -0 "${candidate_pid}" 2>/dev/null; then
    kill -TERM "${candidate_pid}" 2>/dev/null || true
    printf '%s\n' 'candidate Local Executor remained alive after its wrapper returned' >&2
    exit 1
  fi

  if [[ ${expect_success} == true ]]; then
    grep -Fq -- "${COMPLETION}" "${SIGNAL_OUTPUT}"
  else
    [[ ${wrapper_status} -ne 0 ]] || {
      printf '%s\n' 'Host Agent upgrade converted candidate cancellation failure to success' >&2
      exit 1
    }
    assert_no_completion "$(<"${SIGNAL_OUTPUT}")"
    grep -Fq -- 'managed Host runtime upgrade failed' "${SIGNAL_OUTPUT}"
  fi

  rm -f -- \
    "${HELPER_SIGNAL_MODE}" \
    "${HELPER_SIGNAL_READY}" \
    "${HELPER_SIGNAL_RECEIVED}" \
    "${HELPER_SIGNAL_FINISHED}" \
    "${SIGNAL_OUTPUT}"
  assert_helper_arguments
  assert_private_stage_cleaned
  assert_sentinels_unchanged
}

install -o root -g root -m 0600 /dev/null "${HELPER_FAIL_MARKER}"
rm -f -- "${HELPER_LOG}"
helper_failure_status=0
helper_failure_output="$("${INSTALLER}" --upgrade 2>&1)" || helper_failure_status=$?
if [[ ${helper_failure_status} -ne 73 ]]; then
  printf '%s\n' 'Host Agent upgrade survived a candidate Local Executor failure' >&2
  printf 'status=%s, expected=73\n' "${helper_failure_status}" >&2
  exit 1
fi
rm -f -- "${HELPER_FAIL_MARKER}"
assert_no_completion "${helper_failure_output}"
assert_helper_arguments
assert_private_stage_cleaned
assert_sentinels_unchanged

run_wrapper_signal_case failure false 75
run_wrapper_signal_case success true 0

rm -f -- "${HELPER_LOG}"
upgrade_output="$("${INSTALLER}" --upgrade 2>&1)"
grep -Fq -- "${COMPLETION}" <<<"${upgrade_output}"
grep -Fq -- "Verified Host Agent bundle archive SHA-256: ${EXPECTED_ARCHIVE_SHA256}" \
  <<<"${upgrade_output}"
assert_helper_arguments
assert_private_stage_cleaned
assert_sentinels_unchanged
