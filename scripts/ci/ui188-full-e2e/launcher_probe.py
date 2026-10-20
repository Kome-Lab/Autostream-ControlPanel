"""Same-image launcher comparison; never a product or historical-cause proof."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from run_identity import read_bound


def facts(pid):
    rows = dict(line.split(':', 1) for line in Path('/proc/'+str(pid)+'/status').read_text().splitlines() if ':' in line)
    return {'pid': pid, 'ppid': int(rows['PPid']), 'uids': [int(x) for x in rows['Uid'].split()],
            'cap_eff': int(rows['CapEff'], 16), 'cap_bnd': int(rows['CapBnd'], 16),
            'cap_amb': int(rows['CapAmb'], 16), 'nnp': int(rows['NoNewPrivs'])}


def snapshot(root, identity, payload=False):
    version = subprocess.check_output(['dpkg-query', '-W', '-f=${Version}', 'xvfb'], text=True, timeout=3).strip()
    if not re.fullmatch(r'[0-9A-Za-z:.+~\-]{1,100}', version):
        raise ValueError('BLOCKED_LAUNCHER_PACKAGE')
    init = Path('/proc/1/cmdline').read_bytes().split(b'\0')
    role = 'XVFB_RUN' if any(Path(x.decode()).name == 'xvfb-run' for x in init if x) else 'SUPERVISOR' if any(b'launcher_probe.py' in x for x in init) else 'OTHER'
    xvfb = []
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():
            continue
        try:
            args = (proc/'cmdline').read_bytes().split(b'\0')
            if args and Path(args[0].decode()).name == 'Xvfb':
                xvfb.append(facts(int(proc.name)))
        except (OSError, UnicodeError):
            continue
    display = os.environ.get('DISPLAY', '')
    ready = bool(re.fullmatch(r':[0-9]+', display) and Path('/tmp/.X11-unix/X'+display[1:]).is_socket())
    value = {'schema_version': 1, 'identity': identity, 'package': 'XVFB',
             'package_version_numbers': [int(x) for x in re.findall(r'[0-9]+', version)],
             'package_version_sha256': hashlib.sha256(version.encode()).hexdigest(),
             'xvfb_run_sha256': hashlib.sha256(Path('/usr/bin/xvfb-run').read_bytes()).hexdigest(),
             'xvfb_sha256': hashlib.sha256(Path('/usr/bin/Xvfb').read_bytes()).hexdigest(),
             'pid1_role': role, 'pid1': facts(1), 'observer': facts(os.getpid()), 'xvfb': xvfb,
             'ready': ready, 'payload_started': payload, 'payload_exit': 0 if payload else None}
    (root/('launcher-payload.json' if payload else 'launcher-snapshot.json')).write_text(json.dumps(value)+'\n')
    # The raw package query remains private. Public version numbers and its exact
    # hash identify the observation without permitting arbitrary public strings.
    (root/'package-version.private.log').write_text(version+'\n')
    return value


def main():
    root = Path('/evidence')
    identity = read_bound()
    mode = sys.argv[1]
    if mode == 'payload':
        value = snapshot(root, identity, True)
        return 0 if value['ready'] else 1
    if mode == 'snapshot':
        snapshot(root, identity)
        return 0
    if mode == 'parent':
        from launch_parent import run
        result = run(root, identity, ['xvfb-run', '-a', sys.executable, '-B', '/opt/ui188/launcher_probe.py', 'payload'], timeout_seconds=8, child_watch=())
        (root/'launcher-parent.json').write_text(json.dumps({'identity': identity, 'result': result})+'\n')
        return result['original_exit'] if result['original_exit'] else result['cleanup_exit']
    raise ValueError('BLOCKED_LAUNCHER_MODE')


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception:
        sys.exit(1)
