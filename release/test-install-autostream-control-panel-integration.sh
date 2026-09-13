#!/bin/bash
set -euo pipefail

umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LC_ALL=C

die() {
  printf 'control-panel installer integration test: %s\n' "$*" >&2
  exit 1
}

[[ ${EUID} -eq 0 ]] || die "must run as root"
[[ $(uname -m) == "x86_64" ]] || die "this integration fixture requires an amd64 Linux runner"

if [[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS:-} != "1" ]]; then
  exec unshare --mount --propagation private bash -c '
    set -euo pipefail
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-scratch /mnt
    install -d -o root -g root -m 0755 \
      /mnt/usr-lower \
      /mnt/etc-lower \
      /mnt/var-lower \
      /mnt/run-lower
    mount --rbind /usr /mnt/usr-lower
    mount --make-rprivate /mnt/usr-lower
    mount --rbind /etc /mnt/etc-lower
    mount --make-rprivate /mnt/etc-lower
    mount --rbind /var /mnt/var-lower
    mount --make-rprivate /mnt/var-lower
    mount --rbind /run /mnt/run-lower
    mount --make-rprivate /mnt/run-lower
    install -d -o root -g root -m 0755 \
      /mnt/usr-upper \
      /mnt/usr-upper/local \
      /mnt/etc-upper \
      /mnt/etc-upper/systemd \
      /mnt/etc-upper/systemd/system \
      /mnt/var-upper \
      /mnt/var-upper/lib \
      /mnt/var-upper/backups \
      /mnt/run-upper
    install -d -o root -g root -m 1777 /mnt/var-upper/tmp
    install -d -o root -g root -m 0700 \
      /mnt/usr-work \
      /mnt/etc-work \
      /mnt/var-work \
      /mnt/run-work
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/usr-lower,upperdir=/mnt/usr-upper,workdir=/mnt/usr-work \
      autostream-control-panel-installer-test-usr /usr
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/etc-lower,upperdir=/mnt/etc-upper,workdir=/mnt/etc-work \
      autostream-control-panel-installer-test-etc /etc
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/var-lower,upperdir=/mnt/var-upper,workdir=/mnt/var-work \
      autostream-control-panel-installer-test-var /var
    mount -t overlay \
      -o nodev,nosuid,lowerdir=/mnt/run-lower,upperdir=/mnt/run-upper,workdir=/mnt/run-work \
      autostream-control-panel-installer-test-run /run
    mount --rbind /mnt/run-lower/systemd /run/systemd
    mount --make-rprivate /run/systemd
    run_systemd_identity="$(stat -c "%d:%i" -- /mnt/run-lower/systemd)"
    [[ $(stat -c "%d:%i" -- /run/systemd) == "${run_systemd_identity}" ]]
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-bin /usr/local/bin
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-sbin /usr/local/sbin
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-opt /opt
    mount -t tmpfs -o nodev,nosuid,mode=0755,uid=0,gid=0 \
      autostream-control-panel-installer-test-share /usr/share
    mount -t tmpfs -o ro,nodev,nosuid,noexec,mode=0555,uid=0,gid=0 \
      autostream-control-panel-installer-test-sealed /mnt
    exec env \
      AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_MOUNT_NS=1 \
      AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_RUN_SYSTEMD_IDENTITY="${run_systemd_identity}" \
      bash "$1"
  ' autostream-control-panel-installer-test-mount "$0"
fi
grep -Eq ' /mnt .* - tmpfs autostream-control-panel-installer-test-scratch ' \
  /proc/self/mountinfo || die "isolated /mnt scratch mount is missing"
grep -Eq ' /usr .* - overlay autostream-control-panel-installer-test-usr ' \
  /proc/self/mountinfo || die "isolated /usr overlay mount is missing"
grep -Eq ' /etc .* - overlay autostream-control-panel-installer-test-etc ' \
  /proc/self/mountinfo || die "isolated /etc overlay mount is missing"
grep -Eq ' /var .* - overlay autostream-control-panel-installer-test-var ' \
  /proc/self/mountinfo || die "isolated /var overlay mount is missing"
grep -Eq ' /run .* - overlay autostream-control-panel-installer-test-run ' \
  /proc/self/mountinfo || die "isolated /run overlay mount is missing"
grep -Eq ' /mnt ro[^ ]*( [^ ]+)* - tmpfs autostream-control-panel-installer-test-sealed ' \
  /proc/self/mountinfo || die "sealed /mnt mount is missing or writable"
[[ ${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_RUN_SYSTEMD_IDENTITY:-} =~ ^[0-9]+:[0-9]+$ ]] || \
  die "host-backed /run/systemd identity was not preserved"
[[ $(stat -c '%d:%i' -- /run/systemd) == \
  "${AUTOSTREAM_CONTROL_PANEL_INSTALLER_TEST_RUN_SYSTEMD_IDENTITY}" ]] || \
  die "host-backed /run/systemd bind changed identity"
[[ $(stat -c '%U:%G:%a' -- /mnt) == "root:root:555" ]] || \
  die "sealed /mnt mount ownership or mode is invalid"
if touch /mnt/autostream-control-panel-installer-test-write-probe 2>/dev/null; then
  rm -f -- /mnt/autostream-control-panel-installer-test-write-probe
  die "sealed /mnt unexpectedly permits writes to hidden host aliases"
fi
grep -Eq ' /usr/local/bin .* - tmpfs autostream-control-panel-installer-test-bin ' \
  /proc/self/mountinfo || die "isolated /usr/local/bin mount is missing"
grep -Eq ' /usr/local/sbin .* - tmpfs autostream-control-panel-installer-test-sbin ' \
  /proc/self/mountinfo || die "isolated /usr/local/sbin mount is missing"
grep -Eq ' /opt .* - tmpfs autostream-control-panel-installer-test-opt ' \
  /proc/self/mountinfo || die "isolated /opt mount is missing"
grep -Eq ' /usr/share .* - tmpfs autostream-control-panel-installer-test-share ' \
  /proc/self/mountinfo || die "isolated /usr/share mount is missing"
[[ $(stat -c '%m' -- /usr/local/bin) == "/usr/local/bin" ]] || \
  die "isolated /usr/local/bin mount is not effective"
[[ $(stat -c '%m' -- /usr/local/sbin) == "/usr/local/sbin" ]] || \
  die "isolated /usr/local/sbin mount is not effective"
[[ $(stat -c '%m' -- /opt) == "/opt" ]] || \
  die "isolated /opt mount is not effective"
[[ $(stat -c '%m' -- /usr/share) == "/usr/share" ]] || \
  die "isolated /usr/share mount is not effective"
[[ $(stat -c '%d' -- /usr/share) != $(stat -c '%d' -- /var/backups) ]] || \
  die "legacy web and install backup fixtures must use different filesystems"
[[ $(stat -c '%U:%G:%a' -- /usr) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr fixture"
[[ $(stat -c '%U:%G:%a' -- /etc) == "root:root:755" ]] || \
  die "could not create an isolated safe /etc fixture"
[[ $(stat -c '%U:%G:%a' -- /etc/systemd) == "root:root:755" ]] || \
  die "could not create an isolated safe /etc/systemd fixture"
[[ $(stat -c '%U:%G:%a' -- /etc/systemd/system) == "root:root:755" ]] || \
  die "could not create an isolated safe /etc/systemd/system fixture"
[[ $(stat -c '%U:%G:%a' -- /var) == "root:root:755" ]] || \
  die "could not create an isolated safe /var fixture"
[[ $(stat -c '%U:%G:%a' -- /var/lib) == "root:root:755" ]] || \
  die "could not create an isolated safe /var/lib fixture"
[[ $(stat -c '%U:%G:%a' -- /var/backups) == "root:root:755" ]] || \
  die "could not create an isolated safe /var/backups fixture"
[[ $(stat -c '%U:%G:%a' -- /var/tmp) == "root:root:1777" ]] || \
  die "could not create an isolated safe /var/tmp fixture"
[[ $(stat -c '%U:%G:%a' -- /run) == "root:root:755" ]] || \
  die "could not create an isolated safe /run fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/local) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/local fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/local/bin) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/local/bin fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/local/sbin) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/local/sbin fixture"
[[ $(stat -c '%U:%G:%a' -- /opt) == "root:root:755" ]] || \
  die "could not create an isolated safe /opt fixture"
[[ $(stat -c '%U:%G:%a' -- /usr/share) == "root:root:755" ]] || \
  die "could not create an isolated safe /usr/share fixture"

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly INSTALLER_SOURCE="${SCRIPT_DIR}/install-autostream-control-panel"

# Modules share this shell and preserve the original scenario order.
source "${SCRIPT_DIR}/installer-tests/control-panel/fixture-ownership-and-runtime.sh"
source "${SCRIPT_DIR}/installer-tests/control-panel/archive-fixture-and-validation.sh"
source "${SCRIPT_DIR}/installer-tests/control-panel/unsafe-input-and-lock-cases.sh"
source "${SCRIPT_DIR}/installer-tests/control-panel/account-and-staging-rollback-cases.sh"
source "${SCRIPT_DIR}/installer-tests/control-panel/fresh-install-and-legacy-fixture.sh"
source "${SCRIPT_DIR}/installer-tests/control-panel/legacy-rollback-cases.sh"
source "${SCRIPT_DIR}/installer-tests/control-panel/migration-and-runtime-race-cases.sh"
