#!/usr/bin/env bash
set -euo pipefail
umask 077
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
export PYTHONPATH=/opt/ui188
export UI188_BROWSER_ATTEMPT=1 UI188_S4_ATTEMPT=1 UI188_S5_ATTEMPT=1 UI188_LOST_STOP_RESPONSE=1 UI188_LONG_AUDIO_SECONDS=900
export UI188_HLS_CAPTURE_ROOT="/r6/hls-capture-$UI188_PROBE_ATTEMPT"
# Only the exact-port FD broker retains NET_RAW. Products, Chrome and the driver
# receive no permitted/effective/inheritable/ambient/bounding capabilities.
setpriv --reuid=1000 --regid=1000 --clear-groups --bounding-set=-all,+net_raw --inh-caps=-all,+net_raw --ambient-caps=-all,+net_raw --no-new-privs \
  python3 -B /opt/ui188/tap_broker.py > /evidence/tap-broker.private.log 2>&1 &
for _ in $(seq 1 50); do
  test ! -f /evidence/tap-broker-ready.json || break
  sleep 0.1
done
test -f /evidence/tap-broker-ready.json
exec setpriv --reuid=1000 --regid=1000 --clear-groups --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs \
  xvfb-run -a python3 -B /opt/ui188/runtime_supervisor.py
