"""Bounded old/new launchers in the exact same smoke image, without DB or secrets."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
from gate import HEX256, require, validate_identity
from run_identity import encoded, environment

CASES = ('XVFB_PID1', 'SUPERVISOR_PID1')


def validate_facts(value):
    require(set(value) == {'pid', 'ppid', 'uids', 'cap_eff', 'cap_bnd', 'cap_amb', 'nnp'}, 'BLOCKED_LAUNCHER_FACTS')
    require(all(type(value[k]) is int and value[k] >= 0 for k in ('pid', 'ppid', 'cap_eff', 'cap_bnd', 'cap_amb', 'nnp')) and value['pid'] > 0, 'BLOCKED_LAUNCHER_FACTS')
    require(value['uids'] == [1000]*4 and all(value[k] == 0 for k in ('cap_eff', 'cap_bnd', 'cap_amb')) and value['nnp'] == 1, 'BLOCKED_LAUNCHER_PRIVILEGES')


def validate_observation(value, identity=None):
    keys = {'schema_version', 'package', 'package_version_numbers', 'package_version_sha256', 'xvfb_run_sha256', 'xvfb_sha256', 'pid1_role', 'pid1', 'observer', 'xvfb', 'ready', 'payload_started', 'payload_exit'}
    require(set(value) == keys | ({'identity'} if identity is not None else set()), 'BLOCKED_LAUNCHER_OBSERVATION')
    if identity is not None:
        validate_identity(value['identity'], identity)
    require(value['schema_version'] == 1 and value['package'] == 'XVFB' and value['pid1_role'] in ('XVFB_RUN', 'SUPERVISOR', 'OTHER'), 'BLOCKED_LAUNCHER_OBSERVATION')
    require(type(value['package_version_numbers']) is list and 1 <= len(value['package_version_numbers']) <= 12 and all(type(n) is int and 0 <= n < 1000000 for n in value['package_version_numbers']), 'BLOCKED_LAUNCHER_PACKAGE')
    require(all(HEX256.fullmatch(value[k]) for k in ('package_version_sha256', 'xvfb_run_sha256', 'xvfb_sha256')), 'BLOCKED_LAUNCHER_HASH')
    for row in (value['pid1'], value['observer'], *value['xvfb']):
        validate_facts(row)
    require(value['pid1']['pid'] == 1 and value['pid1']['ppid'] == 0 and type(value['ready']) is bool and type(value['payload_started']) is bool and (value['payload_exit'] is None or type(value['payload_exit']) is int and -255 <= value['payload_exit'] <= 255), 'BLOCKED_LAUNCHER_OBSERVATION')
    return {k: v for k, v in value.items() if k != 'identity'}


def validate_comparison(value):
    if value == {'status': 'NOT_RUN'}:
        return value
    require(set(value) == {'status', 'image_sha256', 'historical_cause', 'cases'} and value['status'] in ('PASS', 'FAIL') and
            HEX256.fullmatch(value['image_sha256']) and value['historical_cause'] == 'NOT_ESTABLISHED' and set(value['cases']) <= set(CASES), 'BLOCKED_LAUNCHER_SCHEMA')
    for row in value['cases'].values():
        require(set(row) == {'original_exit', 'timed_out', 'container_exit', 'duration_ms', 'deadline_ms', 'proof_sha256', 'observation'}, 'BLOCKED_LAUNCHER_SCHEMA')
        require(all(type(row[k]) is int and -255 <= row[k] <= 255 for k in ('original_exit', 'container_exit')) and type(row['timed_out']) is bool and
                type(row['duration_ms']) is int and 0 <= row['duration_ms'] <= 60000 and row['deadline_ms'] == 10000 and HEX256.fullmatch(row['proof_sha256']), 'BLOCKED_LAUNCHER_SCHEMA')
        require(not row['timed_out'] or row['original_exit'] == 124, 'BLOCKED_LAUNCHER_FALSE_EXIT')
        validate_observation(row['observation'])
    if value['status'] == 'PASS':
        require(set(value['cases']) == set(CASES), 'BLOCKED_LAUNCHER_FALSE_PASS')
        old, new = (value['cases'][case] for case in CASES)
        require(old['observation']['pid1_role'] == 'XVFB_RUN' and new['observation']['pid1_role'] == 'SUPERVISOR' and
                new['original_exit'] == new['container_exit'] == 0 and not new['timed_out'] and new['observation']['ready'] and
                new['observation']['payload_started'] and new['observation']['payload_exit'] == 0 and len(new['observation']['xvfb']) > 0 and
                all(p['ppid'] > 1 for p in new['observation']['xvfb']) and
                all(old['observation'][k] == new['observation'][k] for k in ('package_version_sha256', 'xvfb_run_sha256', 'xvfb_sha256')), 'BLOCKED_LAUNCHER_FALSE_PASS')
    return value


def compare(image, state, identity):
    require(image.startswith('sha256:') and HEX256.fullmatch(image[7:]), 'BLOCKED_LAUNCHER_IMAGE')
    base = state/'launcher-comparison'
    base.mkdir(mode=0o700)
    result = {'status': 'FAIL', 'image_sha256': image[7:], 'historical_cause': 'NOT_ESTABLISHED', 'cases': {}}
    receipt = state/'launcher-comparison-result.json'
    receipt.write_text(json.dumps(result)+'\n')
    with (state/'launcher-comparison.private.log').open('xb') as log:
        def run(argv, timeout=15):
            process = subprocess.run(argv, stdout=subprocess.PIPE, stderr=log, timeout=timeout)
            require(process.returncode == 0, 'BLOCKED_LAUNCHER_TOOL')
            return process.stdout
        for case in CASES:
            root = base/case
            root.mkdir(mode=0o700)
            (root/'identity.json').write_bytes(encoded(identity))
            (root/'identity.json').chmod(0o444)
            cid = None
            started = time.monotonic_ns()
            try:
                run(['sudo', 'chown', '1000:1000', str(root)])
                name = 'ui188-launch-'+identity['run_uuid']+'-'+case.lower().replace('_','-')
                argv = ['docker', 'run', '--detach', '--name', name, '--label', 'autostream.ui188.run='+identity['run_uuid'],
                        '--network', 'none', '--read-only', '--tmpfs', '/tmp:rw,nosuid,nodev,mode=1777,size=128m',
                        '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--user', '1000:1000',
                        '--cpus', '1', '--memory', '256m', '--pids-limit', '64', '--mount', 'type=bind,src='+str(root)+',dst=/evidence']
                for key, value in environment(identity, '/evidence/identity.json').items():
                    argv += ['--env', key+'='+value]
                argv += ['--env', 'HOME=/tmp', '--entrypoint', 'xvfb-run' if case == CASES[0] else 'python3', image]
                argv += ['-a', 'python3', '-B', '/opt/ui188/launcher_probe.py', 'payload'] if case == CASES[0] else ['-B', '/opt/ui188/launcher_probe.py', 'parent']
                cid = run(argv).decode().strip()
                require(len(cid) == 64 and all(c in '0123456789abcdef' for c in cid), 'BLOCKED_LAUNCHER_CONTAINER')
                end = time.monotonic()+10
                while True:
                    info = json.loads(run(['docker', 'inspect', cid]))[0]
                    require(info['Config']['Image'] == image and info['Config']['Labels']['autostream.ui188.run'] == identity['run_uuid'], 'BLOCKED_LAUNCHER_CONTAINER')
                    if not info['State']['Running'] or time.monotonic() >= end:
                        break
                    time.sleep(.2)
                timed_out = info['State']['Running']
                if timed_out:
                    run(['docker', 'exec', '--user', '1000:1000', cid, 'python3', '-B', '/opt/ui188/launcher_probe.py', 'snapshot'], 5)
                    run(['docker', 'stop', '--time', '1', cid], 8)
                    info = json.loads(run(['docker', 'inspect', cid]))[0]
                run(['sudo', 'chown', '-R', str(os.getuid())+':'+str(os.getgid()), str(root)])
                proof = root/('launcher-payload.json' if (root/'launcher-payload.json').is_file() else 'launcher-snapshot.json')
                observation = validate_observation(json.loads(proof.read_text()), identity)
                result['cases'][case] = {'original_exit': 124 if timed_out else info['State']['ExitCode'], 'timed_out': timed_out,
                    'container_exit': info['State']['ExitCode'], 'duration_ms': (time.monotonic_ns()-started)//1000000,
                    'deadline_ms': 10000, 'proof_sha256': hashlib.sha256(proof.read_bytes()).hexdigest(), 'observation': observation}
                if case == CASES[1]:
                    parent = json.loads((root/'launcher-parent.json').read_text())
                    validate_identity(parent['identity'], identity)
                    require(parent['result']['original_exit'] == parent['result']['cleanup_exit'] == 0 and not timed_out and info['State']['ExitCode'] == 0 and observation['pid1_role'] == 'SUPERVISOR' and observation['ready'] and observation['payload_started'] and observation['payload_exit'] == 0 and all(p['ppid'] > 1 for p in observation['xvfb']), 'BLOCKED_LAUNCHER_CANDIDATE')
            finally:
                if cid:
                    subprocess.run(['docker', 'rm', '--force', cid], stdout=log, stderr=log, timeout=10)
                subprocess.run(['sudo', 'chown', '-R', str(os.getuid())+':'+str(os.getgid()), str(root)], stdout=log, stderr=log, timeout=10)
                receipt.write_text(json.dumps(result)+'\n')
        old, new = (result['cases'][case]['observation'] for case in CASES)
        require(old['pid1_role'] == 'XVFB_RUN' and all(old[k] == new[k] for k in ('package_version_sha256', 'xvfb_run_sha256', 'xvfb_sha256')), 'BLOCKED_LAUNCHER_SAME_IMAGE')
        result['status'] = 'PASS'
        validate_comparison(result)
        receipt.write_text(json.dumps(result)+'\n')
    return result
