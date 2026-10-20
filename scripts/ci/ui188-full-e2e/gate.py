"""Fail closed before allocating a paid runner and before starting live services."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sys

SHA = re.compile(r'[0-9a-f]{40}')
HEX256 = re.compile(r'[0-9a-f]{64}')
GROUPS = tuple([f'S{i}' for i in range(6)] + [f'F{i:02}' for i in range(1, 12)] +
               ['OVERLAP', 'ARCHIVE', 'COVER', 'HLS', 'RTP', 'SOCKET', 'AAC', 'PCM', 'FRAME', 'RECEIVER', 'PROVENANCE'])


class Blocked(Exception):
    """Only a constant public reason may cross the Actions log boundary."""


def require(condition, reason):
    if not condition:
        raise Blocked(reason)


def authorize(env):
    require(env.get('GITHUB_EVENT_NAME') == 'workflow_dispatch' and env.get('GITHUB_REF') == 'refs/heads/main', 'BLOCKED_TRIGGER')
    require(env.get('GITHUB_REPOSITORY') == 'Kome-Lab/Autostream-ControlPanel', 'BLOCKED_OWNER')
    require(env.get('GITHUB_RUN_ATTEMPT') == '1', 'BLOCKED_AUTOMATIC_RETRY')
    require(env.get('UI188_ENABLED') == 'true', 'BLOCKED_RUNNER_NOT_PREPARED')
    require(env.get('COST_ACK') == 'true', 'BLOCKED_BILLING_APPROVAL')
    try:
        approval = json.loads(env.get('UI188_APPROVED_DISPATCH', ''))
    except (ValueError, TypeError):
        raise Blocked('BLOCKED_APPROVAL_RECORD') from None
    require(set(approval) == {'cp_sha', 'bot_sha', 'lock_sha256', 'approval_id', 'runner_label', 'billing_confirmed', 'max_minutes'}, 'BLOCKED_APPROVAL_RECORD')
    for key in ('cp_sha', 'bot_sha'):
        require(bool(SHA.fullmatch(env.get(key.upper(), ''))) and approval[key] == env[key.upper()], 'BLOCKED_UNTRUSTED_SHA')
    require(bool(HEX256.fullmatch(env.get('LOCK_SHA256', ''))) and approval['lock_sha256'] == env['LOCK_SHA256'], 'BLOCKED_SOURCE_LOCK')
    require(re.fullmatch(r'UI188-[A-Za-z0-9-]{8,64}', env.get('APPROVAL_ID', '')) is not None and approval['approval_id'] == env['APPROVAL_ID'], 'BLOCKED_BILLING_APPROVAL')
    require(approval['billing_confirmed'] is True and approval['max_minutes'] == 120 and approval['runner_label'] == 'ui188-linux-8core', 'BLOCKED_RUNNER_NOT_PREPARED')
    return {'cp_sha': approval['cp_sha'], 'bot_sha': approval['bot_sha'], 'lock_sha256': approval['lock_sha256']}


def capacity(cpu, memory_bytes, disk_bytes, system):
    require(system == 'Linux' and cpu >= 8 and memory_bytes >= 30 * 2**30 and disk_bytes >= 180 * 2**30, 'BLOCKED_RUNNER_CAPACITY')
    return {'cpu': cpu, 'memory_bytes': memory_bytes, 'disk_free_bytes': disk_bytes, 'platform': 'Linux'}


def validate_identity(record, identity):
    from run_identity import validate
    try:
        validate(record)
        validate(identity)
    except (ValueError, TypeError, KeyError):
        raise Blocked('BLOCKED_EVIDENCE_IDENTITY') from None
    require(record == identity, 'BLOCKED_STALE_RUN')


def main():
    if sys.argv[1] == 'authorize':
        result = authorize(os.environ)
        with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf8') as output:
            for k, v in result.items():
                output.write(k + '=' + v + '\n')
    elif sys.argv[1] == 'capacity':
        import platform
        require(os.environ.get('RUNNER_KIND') == 'github-hosted', 'BLOCKED_SELF_HOSTED_RUNNER')
        require(platform.machine().lower() in ('x86_64', 'amd64'), 'BLOCKED_RUNNER_ARCHITECTURE')
        memory = int(next(l.split()[1] for l in Path('/proc/meminfo').read_text().splitlines() if l.startswith('MemTotal:'))) * 1024
        cpu = min(os.cpu_count(), len(os.sched_getaffinity(0)))
        cgroup = Path('/sys/fs/cgroup')
        location = next((line.split(':', 2)[2] for line in Path('/proc/self/cgroup').read_text().splitlines() if line.startswith('0::')), '/')
        actual = cgroup / location.lstrip('/')
        if (actual / 'memory.max').is_file():
            limit = (actual / 'memory.max').read_text().strip()
            if limit != 'max':
                memory = min(memory, int(limit))
        if (actual / 'cpu.max').is_file():
            quota, period = (actual / 'cpu.max').read_text().split()
            if quota != 'max':
                cpu = min(cpu, int(quota) / int(period))
        result = capacity(cpu, memory, shutil.disk_usage(os.environ['RUNNER_TEMP']).free, platform.system())
        Path(sys.argv[2]).write_text(json.dumps(result) + '\n')
    else:
        raise Blocked('BLOCKED_COMMAND')


if __name__ == '__main__':
    try:
        main()
    except Blocked as error:
        print(str(error))
        sys.exit(1)
    except Exception:
        print('BLOCKED_GATE_INTERNAL')
        sys.exit(1)
