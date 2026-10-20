#!/usr/bin/env bash
set -euo pipefail
umask 077
export PYTHONPATH=/opt/ui188
entry_signal=0
broker_pid=''
entry_failed() {
  local code=$?
  trap - EXIT
  if (( code != 0 )); then
    if (( entry_signal != 0 )); then
      python3 -B /opt/ui188/checkpoints.py finish entry_setup --exit "$code" --signal "$entry_signal" || true
    else
      python3 -B /opt/ui188/checkpoints.py finish entry_setup --exit "$code" || true
    fi
    if [[ -n "$broker_pid" ]]; then
      # UID0 lacks CAP_KILL. A same-UID, capability-free helper retires only the
      # recorded child through a pidfd. Reap only after it proves termination.
      if setpriv --reuid=1000 --regid=1000 --clear-groups --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs \
        python3 -B /opt/ui188/entry_handoff.py cleanup --pid "$broker_pid" --parent "$$" > /evidence/entry-cleanup.private.log 2>&1; then
        set +e
        wait "$broker_pid"
        child_code=$?
        set -e
        python3 -B /opt/ui188/entry_handoff.py reaped --pid "$broker_pid" --parent "$$" --exit "$child_code" || true
      fi
    fi
  fi
  exit "$code"
}
trap entry_failed EXIT
trap 'entry_signal=15; exit 143' TERM
trap 'entry_signal=2; exit 130' INT
python3 -B /opt/ui188/checkpoints.py begin entry_setup
db_ip=$(getent ahostsv4 db | awk 'NR==1 {print $1}')
[[ "$db_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
printf '%s db\n' "$db_ip" >> /etc/hosts
# Docker DNS would otherwise proxy internet DNS through a loopback address.
iptables -P OUTPUT DROP
iptables -A OUTPUT -d 127.0.0.11 -j REJECT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -d "$db_ip" -p tcp --dport 3306 -j ACCEPT
ip6tables -P OUTPUT DROP
ip6tables -A OUTPUT -o lo -j ACCEPT
printf '{"db_ipv4":"%s"}\n' "$db_ip" > /evidence/network-setup.json
chmod 444 /evidence/network-setup.json
mkdir -m 700 /tmp/ui188-secrets
cp /run/ui188/credentials.json /tmp/ui188-secrets/credentials.json
chmod 600 /tmp/ui188-secrets/credentials.json
# Bind mounts initially belong to the host login uid. CAP_CHOWN first gives
# setup ownership; owner-only chmod then needs no CAP_FOWNER.
chown 0:0 /evidence /worker-config
chmod 700 /evidence /worker-config
chown 1000:1000 /evidence /worker-config
chown -R 1000:1000 /tmp/ui188-secrets
export UI188_TEST_CREDENTIALS=/tmp/ui188-secrets/credentials.json
export UI188_BROWSER_ATTEMPT=1 UI188_S4_ATTEMPT=1 UI188_S5_ATTEMPT=1 UI188_LOST_STOP_RESPONSE=1 UI188_LONG_AUDIO_SECONDS=900
export UI188_HLS_CAPTURE_ROOT="/r6/hls-capture-$UI188_PROBE_ATTEMPT"
python3 -B /opt/ui188/checkpoints.py finish entry_setup --proof identity --proof network_setup
# Only the exact-port FD broker retains NET_RAW. Products, Chrome and the driver
# receive no permitted/effective/inheritable/ambient/bounding capabilities.
setpriv --reuid=1000 --regid=1000 --clear-groups --bounding-set=-all,+net_raw --inh-caps=-all,+net_raw --ambient-caps=-all,+net_raw --no-new-privs \
  python3 -B /opt/ui188/tap_broker.py > /evidence/tap-broker.private.log 2>&1 &
broker_pid=$!
export UI188_BROKER_PID="$broker_pid"
set +e
python3 -B /opt/ui188/entry_handoff.py ready --pid "$broker_pid" --parent "$$"
handoff_code=$?
set -e
if (( handoff_code != 0 )); then
  if (( handoff_code == 3 )); then
    # /proc proved the exact child terminal, so this wait cannot block.
    set +e
    wait "$broker_pid"
    child_code=$?
    set -e
    python3 -B /opt/ui188/entry_handoff.py reaped --pid "$broker_pid" --parent "$$" --exit "$child_code" || true
    broker_pid=''
    if (( child_code == 0 )); then exit 1; else exit "$child_code"; fi
  fi
  exit "$handoff_code"
fi
exec setpriv --reuid=1000 --regid=1000 --clear-groups --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs \
  python3 -B /opt/ui188/launch_parent.py
