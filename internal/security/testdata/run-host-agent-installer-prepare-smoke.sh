#!/bin/bash
set -euo pipefail

export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LC_ALL=C

[[ $(id -u) -eq 0 ]] || {
  printf '%s\n' 'host agent installer prepare smoke requires root' >&2
  exit 1
}
[[ $# -eq 1 ]] || {
  printf '%s\n' 'usage: run-host-agent-installer-prepare-smoke.sh REPOSITORY_ROOT' >&2
  exit 1
}

readonly REPOSITORY_ROOT=$1
readonly PACKAGE_ROOT=/root/autostream-host-agent-package
readonly SYSTEMCTL_LOG=/tmp/autostream-host-agent-systemctl.log
readonly BINARY_LOG=/tmp/autostream-host-agent-binary.log
readonly LOCAL_EXECUTOR_BINARY_LOG=/tmp/autostream-local-executor-prepare-binary.log

rm -rf -- "${PACKAGE_ROOT}"
mkdir -p "${PACKAGE_ROOT}/bin" "${PACKAGE_ROOT}/install" "${PACKAGE_ROOT}/systemd"
install -m 0755 \
  "${REPOSITORY_ROOT}/release/install-autostream-host-agent" \
  "${PACKAGE_ROOT}/install/install-autostream-host-agent"
install -m 0755 \
  "${REPOSITORY_ROOT}/release/uninstall-autostream-host-agent" \
  "${PACKAGE_ROOT}/install/uninstall-autostream-host-agent"
install -m 0755 \
  "${REPOSITORY_ROOT}/release/install-autostream-local-executor" \
  "${PACKAGE_ROOT}/install/install-autostream-local-executor"
install -m 0755 \
  "${REPOSITORY_ROOT}/release/uninstall-autostream-local-executor" \
  "${PACKAGE_ROOT}/install/uninstall-autostream-local-executor"
install -m 0644 \
  "${REPOSITORY_ROOT}/release/autostream-local-executor-policy.json.example" \
  "${PACKAGE_ROOT}/autostream-local-executor-policy.json.example"
install -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-host-agent.service.example" \
  "${PACKAGE_ROOT}/systemd/autostream-host-agent.service"
install -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-local-executor.service.example" \
  "${PACKAGE_ROOT}/systemd/autostream-local-executor.service"
install -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-local-executor.socket.example" \
  "${PACKAGE_ROOT}/systemd/autostream-local-executor.socket"
install -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-local-executor.tmpfiles.example" \
  "${PACKAGE_ROOT}/systemd/autostream-local-executor.tmpfiles"
install -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-host-self-update-recovery@.service.example" \
  "${PACKAGE_ROOT}/systemd/autostream-host-self-update-recovery@.service"
install -m 0644 \
  "${REPOSITORY_ROOT}/systemd/autostream-host-self-update-recovery@.timer.example" \
  "${PACKAGE_ROOT}/systemd/autostream-host-self-update-recovery@.timer"

fake_binary=$(mktemp)
cat > "${fake_binary}" <<'EOF'
#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> /tmp/autostream-host-agent-binary.log
case "${1:-}" in
  --version)
    printf '%s\n' 'autostream-host-agent smoke'
    ;;
  validate-config)
    test -f "${3:-}"
    ;;
  *)
    printf 'unexpected Host Agent invocation: %s\n' "$*" >&2
    exit 91
    ;;
esac
EOF
install -m 0755 "${fake_binary}" "${PACKAGE_ROOT}/bin/autostream-host-agent"
rm -f -- "${fake_binary}"

fake_local_executor=$(mktemp)
cat > "${fake_local_executor}" <<'EOF'
#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> /tmp/autostream-local-executor-prepare-binary.log
case "${1:-}" in
  --version)
    printf '%s\n' 'autostream-local-executor smoke'
    ;;
  validate-policy)
    test -f "${3:-}"
    printf '%s\n' \
      'local executor policy valid' \
      'host_id: host-smoke' \
      "agent_uid: $(id -u autostream-host-agent)" \
      "agent_gid: $(getent group autostream-host-agent | awk -F: 'NR == 1 { print $3 }')" \
      'policy_revision: 1' \
      'policy_sha256: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
    ;;
  *)
    printf 'unexpected local executor invocation: %s\n' "$*" >&2
    exit 93
    ;;
esac
EOF
install -m 0755 "${fake_local_executor}" "${PACKAGE_ROOT}/bin/autostream-local-executor"
rm -f -- "${fake_local_executor}"

chmod 0777 "${PACKAGE_ROOT}"
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted a group/other-writable release root' >&2
  exit 1
fi
chmod 0755 "${PACKAGE_ROOT}"
test ! -e /usr/local/bin/autostream-host-agent
test ! -e /usr/local/libexec/autostream-local-executor

systemctl_path=/usr/bin/systemctl
if [[ -e ${systemctl_path} ]]; then
  mv -- "${systemctl_path}" "${systemctl_path}.real"
fi
fake_systemctl=$(mktemp)
cat > "${fake_systemctl}" <<'EOF'
#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> /tmp/autostream-host-agent-systemctl.log
unit=${!#}
case "${1:-}" in
  daemon-reload)
    if [[ -e /tmp/autostream-host-agent-fail-daemon-reload ]]; then
      exit 94
    fi
    ;;
  is-active)
    if [[ ${unit} == autostream-host-self-update-recovery@?.timer ]]; then
      marker=/tmp/"${unit}".active
    elif [[ ${unit} == autostream-host-agent.service ]]; then
      marker=/tmp/autostream-host-agent-active
    else
      marker=/tmp/"${unit}".active
    fi
    if [[ -e ${marker} ]]; then
      [[ " $* " == *" --quiet "* ]] || printf '%s\n' active
      exit 0
    fi
    [[ " $* " == *" --quiet "* ]] || printf '%s\n' inactive
    exit 3
    ;;
  is-enabled)
    if [[ ${unit} == autostream-host-self-update-recovery@?.timer ]]; then
      marker=/tmp/"${unit}".enabled
    elif [[ ${unit} == autostream-host-agent.service ]]; then
      marker=/tmp/autostream-host-agent-enabled
    else
      marker=/tmp/"${unit}".enabled
    fi
    if [[ -e ${marker} ]]; then
      [[ " $* " == *" --quiet "* ]] || printf '%s\n' enabled
      exit 0
    fi
    [[ " $* " == *" --quiet "* ]] || printf '%s\n' disabled
    exit 1
    ;;
  enable)
    if [[ ${unit} == autostream-host-self-update-recovery@?.timer ]]; then
      touch /tmp/"${unit}".enabled
      [[ " $* " != *" --now "* ]] || touch /tmp/"${unit}".active
      exit 0
    fi
    if [[ ${unit} == autostream-host-agent.service ]]; then
      [[ -e /tmp/autostream-host-agent-allow-enable ]] || {
        printf 'prepare mode attempted forbidden service mutation: %s\n' "$*" >&2
        exit 92
      }
      touch /tmp/autostream-host-agent-enabled
      [[ " $* " != *" --now "* ]] || touch /tmp/autostream-host-agent-active
    else
      touch /tmp/"${unit}".enabled
      [[ " $* " != *" --now "* ]] || touch /tmp/"${unit}".active
    fi
    ;;
  start)
    if [[ ${unit} == autostream-host-agent.service ]]; then
      [[ -e /tmp/autostream-host-agent-allow-enable ]] || {
        printf 'prepare mode attempted forbidden service mutation: %s\n' "$*" >&2
        exit 92
      }
      touch /tmp/autostream-host-agent-active
    else
      if [[ ${unit} == autostream-local-executor.service ]]; then
        install -d -o root -g root -m 0700 /var/lib/autostream-local-executor
      fi
      touch /tmp/"${unit}".active
    fi
    ;;
  stop)
    if [[ ${unit} == autostream-host-agent.service ]]; then
      rm -f -- /tmp/autostream-host-agent-active
    else
      rm -f -- /tmp/"${unit}".active
    fi
    ;;
  disable)
    if [[ ${unit} == autostream-host-self-update-recovery@?.timer ]]; then
      rm -f -- /tmp/"${unit}".enabled
      [[ " $* " != *" --now "* ]] || rm -f -- /tmp/"${unit}".active
    elif [[ ${unit} == autostream-host-agent.service ]]; then
      rm -f -- /tmp/autostream-host-agent-enabled
      [[ " $* " != *" --now "* ]] || rm -f -- /tmp/autostream-host-agent-active
    else
      rm -f -- /tmp/"${unit}".enabled
      [[ " $* " != *" --now "* ]] || rm -f -- /tmp/"${unit}".active
    fi
    ;;
  *)
    exit 0
    ;;
esac
EOF
install -m 0755 "${fake_systemctl}" "${systemctl_path}"
rm -f -- "${fake_systemctl}"

tmpfiles_path=/usr/bin/systemd-tmpfiles
if [[ -e ${tmpfiles_path} ]]; then
  mv -- "${tmpfiles_path}" "${tmpfiles_path}.real"
fi
fake_tmpfiles=$(mktemp)
cat > "${fake_tmpfiles}" <<'EOF'
#!/bin/bash
set -euo pipefail
[[ ${1:-} == "--create" ]] || {
  printf 'unexpected systemd-tmpfiles invocation: %s\n' "$*" >&2
  exit 95
}
if [[ ${2:-} == "/etc/tmpfiles.d/autostream-local-executor.conf" ]]; then
  install -d -o root -g autostream-host-agent -m 0750 /run/autostream-local-executor
fi
EOF
install -m 0755 "${fake_tmpfiles}" "${tmpfiles_path}"
rm -f -- "${fake_tmpfiles}"

touch /tmp/autostream-host-agent-fail-daemon-reload
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode survived an injected post-commit daemon-reload failure' >&2
  exit 1
fi
test ! -e /etc/autostream-host-agent
test ! -e /etc/autostream/host-agent.json
test ! -e /etc/autostream-local-executor/policy.json
test ! -e /etc/autostream-local-executor
test ! -e /opt/autostream/local-executor
test ! -e /usr/local/bin/autostream-host-agent
test ! -e /etc/systemd/system/autostream-host-agent.service
test ! -e /usr/local/libexec/autostream-local-executor
test ! -e /etc/systemd/system/autostream-local-executor.service
test ! -e /etc/systemd/system/autostream-local-executor.socket
test ! -e /etc/tmpfiles.d/autostream-local-executor.conf
test ! -e /etc/systemd/system/autostream-host-self-update-recovery@.service
test ! -e /etc/systemd/system/autostream-host-self-update-recovery@.timer
test ! -e /opt/autostream/host-agent

ln -s /root /etc/autostream-local-executor
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted a symlink policy directory' >&2
  exit 1
fi
rm -f -- /etc/autostream-local-executor

install -o root -g root -m 0600 /dev/null /etc/autostream-local-executor
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted a non-directory policy path' >&2
  exit 1
fi
rm -f -- /etc/autostream-local-executor

install -d -o root -g root -m 0755 /etc/autostream-local-executor
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted an over-permissive policy directory' >&2
  exit 1
fi
rmdir -- /etc/autostream-local-executor

install -d -o root -g root -m 0700 /etc/autostream-local-executor
chown 1:1 /etc/autostream-local-executor
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted a non-root-owned policy directory' >&2
  exit 1
fi
chown root:root /etc/autostream-local-executor
rmdir -- /etc/autostream-local-executor

install -d -o root -g root -m 0700 /opt/autostream/local-executor
ln -s /root /opt/autostream/local-executor/ports
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted a symlink port directory' >&2
  exit 1
fi
rm -f -- /opt/autostream/local-executor/ports
install -d -o root -g root -m 0755 /opt/autostream/local-executor/ports
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted an over-permissive port directory' >&2
  exit 1
fi
rmdir -- /opt/autostream/local-executor/ports
rmdir -- /opt/autostream/local-executor

install -d -o root -g root -m 0700 \
  /etc/autostream-local-executor \
  /opt/autostream/local-executor \
  /opt/autostream/local-executor/ports
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode survived failure with existing private directories' >&2
  exit 1
fi
for private_dir in \
  /etc/autostream-local-executor \
  /opt/autostream/local-executor \
  /opt/autostream/local-executor/ports; do
  test "$(stat -c '%U:%G:%a' "${private_dir}")" = "root:root:700"
done
rm -f -- /tmp/autostream-host-agent-fail-daemon-reload
rmdir -- /opt/autostream/local-executor/ports
rmdir -- /opt/autostream/local-executor
rmdir -- /etc/autostream-local-executor

"${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare

test "$(stat -c '%U:%G:%a' /etc/autostream-host-agent)" = "root:autostream-host-agent:750"
test ! -e /etc/autostream-host-agent/identity.json
test ! -e /etc/autostream/host-agent.json
test ! -e /etc/autostream-local-executor/policy.json
test "$(stat -c '%U:%G:%a' /etc/autostream-local-executor)" = "root:root:700"
test "$(stat -c '%U:%G:%a' /opt/autostream/local-executor)" = "root:root:700"
test "$(stat -c '%U:%G:%a' /opt/autostream/local-executor/ports)" = "root:root:700"
test -L /usr/local/bin/autostream-host-agent
test "$(readlink /usr/local/bin/autostream-host-agent)" = \
  "/opt/autostream/host-agent/current/bin/autostream-host-agent"
test "$(readlink /opt/autostream/host-agent/current)" = "slots/a"
test "$(stat -c '%U:%G:%a' /opt/autostream/host-agent/slots/a/bin/autostream-host-agent)" = "root:root:755"
test "$(stat -c '%U:%G:%a' /etc/systemd/system/autostream-host-agent.service)" = "root:root:644"
test -L /usr/local/libexec/autostream-local-executor
test "$(readlink /usr/local/libexec/autostream-local-executor)" = \
  "/opt/autostream/host-agent/current/bin/autostream-local-executor"
test "$(stat -c '%U:%G:%a' /opt/autostream/host-agent/slots/a/bin/autostream-local-executor)" = "root:root:755"
test "$(stat -c '%U:%G:%a' /etc/systemd/system/autostream-local-executor.service)" = "root:root:644"
test "$(stat -c '%U:%G:%a' /etc/systemd/system/autostream-local-executor.socket)" = "root:root:644"
test "$(stat -c '%U:%G:%a' /etc/tmpfiles.d/autostream-local-executor.conf)" = "root:root:644"
test "$(stat -c '%U:%G:%a' /etc/systemd/system/autostream-host-self-update-recovery@.service)" = "root:root:644"
test "$(stat -c '%U:%G:%a' /etc/systemd/system/autostream-host-self-update-recovery@.timer)" = "root:root:644"
test -e /tmp/autostream-host-self-update-recovery@a.timer.enabled
test -e /tmp/autostream-host-self-update-recovery@a.timer.active
test -e /tmp/autostream-host-self-update-recovery@b.timer.enabled
test -e /tmp/autostream-host-self-update-recovery@b.timer.active
test ! -e /run/autostream-local-executor
test ! -e /var/lib/autostream-local-executor
grep -qx -- 'ConditionPathExists=|/etc/autostream-host-agent/identity.json' \
  /etc/systemd/system/autostream-host-agent.service
grep -qx -- 'ConditionPathExists=|/etc/autostream/host-agent.json' \
  /etc/systemd/system/autostream-host-agent.service
test "$(stat -c '%U:%G:%a' /var/lib/autostream-host-agent)" = "autostream-host-agent:autostream-host-agent:700"
test "$(id -u autostream-host-agent)" -ne 0
test "$(id -gn autostream-host-agent)" = "autostream-host-agent"
test "$(id -Gn autostream-host-agent)" = "autostream-host-agent"
grep -qx -- '--version' "${BINARY_LOG}"
grep -qx -- '--version' "${LOCAL_EXECUTOR_BINARY_LOG}"
grep -qx -- 'daemon-reload' "${SYSTEMCTL_LOG}"
if grep -Eq '^(enable|start)( |$).*(autostream-host-agent\.service|autostream-local-executor\.(service|socket))' "${SYSTEMCTL_LOG}"; then
  printf '%s\n' 'prepare mode enabled or started a runtime unit' >&2
  exit 1
fi
grep -qx -- 'enable --now autostream-host-self-update-recovery@a.timer' "${SYSTEMCTL_LOG}"
grep -qx -- 'enable --now autostream-host-self-update-recovery@b.timer' "${SYSTEMCTL_LOG}"

install -o root -g root -m 0600 \
  "${REPOSITORY_ROOT}/release/autostream-local-executor-policy.json.example" \
  /root/autostream-local-executor-policy.json
prepared_executor_sha=$(sha256sum \
  /opt/autostream/host-agent/slots/a/bin/autostream-local-executor |
  awk '{print $1}')
touch /tmp/autostream-host-agent-fail-daemon-reload
if "${PACKAGE_ROOT}/install/install-autostream-local-executor" \
  --policy /root/autostream-local-executor-policy.json; then
  printf '%s\n' 'composed local installer survived an injected daemon-reload failure' >&2
  exit 1
fi
rm -f -- /tmp/autostream-host-agent-fail-daemon-reload
test -L /usr/local/libexec/autostream-local-executor
test "$(readlink /usr/local/libexec/autostream-local-executor)" = \
  "/opt/autostream/host-agent/current/bin/autostream-local-executor"
test "$(sha256sum /opt/autostream/host-agent/slots/a/bin/autostream-local-executor |
  awk '{print $1}')" = "${prepared_executor_sha}"
test ! -e /etc/autostream-local-executor/policy.json
"${PACKAGE_ROOT}/install/install-autostream-local-executor" \
  --policy /root/autostream-local-executor-policy.json
test -L /usr/local/libexec/autostream-local-executor
test "$(readlink /usr/local/libexec/autostream-local-executor)" = \
  "/opt/autostream/host-agent/current/bin/autostream-local-executor"
test "$(sha256sum /opt/autostream/host-agent/slots/a/bin/autostream-local-executor |
  awk '{print $1}')" = "${prepared_executor_sha}"
test "$(stat -c '%U:%G:%a' /etc/autostream-local-executor/policy.json)" = \
  "root:root:600"
test -e /tmp/autostream-local-executor.socket.enabled
test -e /tmp/autostream-local-executor.socket.active
test -e /tmp/autostream-local-executor.service.active
: > "${SYSTEMCTL_LOG}"

binary_sha=$(sha256sum /usr/local/bin/autostream-host-agent | awk '{print $1}')
install -o root -g autostream-host-agent -m 0640 \
  "${REPOSITORY_ROOT}/release/autostream-host-agent.json.example" \
  /etc/autostream-host-agent/identity.json
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted an existing current identity' >&2
  exit 1
fi
test "$(sha256sum /usr/local/bin/autostream-host-agent | awk '{print $1}')" = "${binary_sha}"
rm -f -- /etc/autostream-host-agent/identity.json

touch /tmp/autostream-host-agent-enabled
if "${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare; then
  printf '%s\n' 'prepare mode accepted an enabled Host Agent service' >&2
  exit 1
fi
rm -f -- /tmp/autostream-host-agent-enabled
test ! -e /etc/autostream-host-agent/identity.json

if grep -Eq '^(enable|start)( |$).*(autostream-host-agent\.service|autostream-local-executor\.(service|socket))' "${SYSTEMCTL_LOG}"; then
  printf '%s\n' 'a failed prepare path enabled or started a runtime unit' >&2
  exit 1
fi

touch /tmp/autostream-host-agent-allow-enable
install -o root -g root -m 0600 \
  "${REPOSITORY_ROOT}/release/autostream-host-agent.json.example" \
  /root/autostream-host-agent.json
install -o root -g autostream-host-agent -m 0640 \
  "${REPOSITORY_ROOT}/release/autostream-host-agent.json.example" \
  /etc/autostream/host-agent.json
"${PACKAGE_ROOT}/install/install-autostream-host-agent" \
  --config /root/autostream-host-agent.json
test "$(stat -c '%U:%G:%a' /etc/autostream-host-agent)" = "root:autostream-host-agent:750"
test "$(stat -c '%U:%G:%a' /etc/autostream-host-agent/identity.json)" = "root:autostream-host-agent:640"
test ! -e /etc/autostream/host-agent.json
test -e /tmp/autostream-host-agent-enabled
test -e /tmp/autostream-host-agent-active
grep -Eq '^validate-config --config /etc/autostream-host-agent/\.identity\.json\.new\.' "${BINARY_LOG}"
grep -qx -- 'validate-config --config /etc/autostream-host-agent/identity.json' "${BINARY_LOG}"
grep -Eq '^enable --now autostream-host-agent\.service$' "${SYSTEMCTL_LOG}"

"${PACKAGE_ROOT}/install/uninstall-autostream-host-agent"
test ! -e /usr/local/bin/autostream-host-agent
test ! -e /etc/systemd/system/autostream-host-agent.service
test -e /etc/autostream-host-agent/identity.json
test -d /var/lib/autostream-host-agent
test -e /usr/local/libexec/autostream-local-executor
test -e /etc/systemd/system/autostream-local-executor.service
test -e /etc/systemd/system/autostream-local-executor.socket
test -e /etc/tmpfiles.d/autostream-local-executor.conf

"${PACKAGE_ROOT}/install/uninstall-autostream-local-executor" --purge
test ! -e /usr/local/libexec/autostream-local-executor
test ! -e /etc/systemd/system/autostream-local-executor.service
test ! -e /etc/systemd/system/autostream-local-executor.socket
test ! -e /etc/tmpfiles.d/autostream-local-executor.conf

install -o root -g autostream-host-agent -m 0640 \
  /etc/autostream-host-agent/identity.json \
  /etc/autostream-host-agent/.identity.staged.wipe
"${PACKAGE_ROOT}/install/uninstall-autostream-host-agent" --purge
test ! -e /etc/autostream-host-agent/.identity.staged.wipe
test ! -e /etc/autostream-host-agent
test ! -e /etc/autostream/host-agent.json
test ! -e /var/lib/autostream-host-agent
if id autostream-host-agent >/dev/null 2>&1 || getent group autostream-host-agent >/dev/null; then
  printf '%s\n' 'Host Agent purge preserved its dedicated account or group' >&2
  exit 1
fi

"${PACKAGE_ROOT}/install/install-autostream-host-agent" --prepare
"${PACKAGE_ROOT}/install/install-autostream-host-agent" \
  --config /root/autostream-host-agent.json
"${PACKAGE_ROOT}/install/install-autostream-local-executor" \
  --policy /root/autostream-local-executor-policy.json
"${PACKAGE_ROOT}/install/uninstall-autostream-local-executor" --purge
install -o root -g autostream-host-agent -m 0640 /dev/null \
  /etc/autostream-host-agent/.identity.staged.wipe
test "$(stat -c '%s:%U:%G:%a' \
  /etc/autostream-host-agent/.identity.staged.wipe)" = \
  "0:root:autostream-host-agent:640"
"${PACKAGE_ROOT}/install/uninstall-autostream-host-agent" --purge
test ! -e /etc/autostream-host-agent/.identity.staged.wipe
test ! -e /etc/autostream-host-agent
test ! -e /var/lib/autostream-host-agent
if id autostream-host-agent >/dev/null 2>&1 || getent group autostream-host-agent >/dev/null; then
  printf '%s\n' 'second Host Agent purge preserved its dedicated account or group' >&2
  exit 1
fi
