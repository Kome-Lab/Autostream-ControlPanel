"""Closed, run-bound startup observations. Absence always means NOT_RUN."""
import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import tempfile
import time

from gate import require, validate_identity
from run_identity import decode, read_bound, validate

PHASES = ('entry_setup', 'privilege_drop', 'broker_ready', 'xvfb_ready',
          'supervisor', 'runtime_privileges', 'db', 'worker_config',
          'worker_ready', 'filtered_rtp_fd', 'go_constructor', 'driver_exit',
          'broker_exit', 'worker_exit', 'container_exit')
PROOFS = {'identity': 'identity.json', 'network_setup': 'network-setup.json',
          'init_privileges': 'init-privileges.json', 'db_readiness': 'db-ready.json',
          'runtime_privileges': 'runtime-privileges.json',
          'broker_ready': 'tap-broker-ready.json', 'broker_exit': 'tap-broker-exit.json',
          'broker_owned_exit': 'broker-owned-exit.json',
          'worker_ready': 'bootstrap-worker.json', 'runtime': 'bootstrap-runtime.json',
          'constructor': 'go-contract/go-constructor-proof.json',
          'constructor_exit': 'go-contract/go-constructor-exit.json',
          'filtered_rtp_fd': 'filtered-rtp-proof.json',
          'driver_exit': 'driver-exit.json', 'supervisor_exit': 'supervisor-exit.json',
          'worker_exit': 'worker-b-exit.json', 'launcher_exit': 'launch-parent-exit.json',
          'shutdown_containers': 'shutdown-containers.json'}
FIELDS = {'schema_version', 'identity', 'phase', 'status', 'at_ns', 'duration_ms',
          'original_exit', 'signal', 'errno', 'pid', 'ppid', 'proof_sha256'}


def _integer(value, lo, hi):
    return type(value) is int and lo <= value <= hi


def _validate(value, identity):
    require(type(value) is dict and set(value) == FIELDS, 'BLOCKED_CHECKPOINT_SCHEMA')
    validate_identity(value['identity'], identity)
    require(type(value['schema_version']) is int and value['schema_version'] == 1 and
            value['phase'] in PHASES and value['status'] in ('RUNNING', 'PASS', 'FAIL'),
            'BLOCKED_CHECKPOINT_SCHEMA')
    require(_integer(value['at_ns'], int(identity['started_ns']), time.time_ns()+5_000_000_000) and
            _integer(value['duration_ms'], 0, 7_200_000) and
            value['duration_ms'] <= (value['at_ns']-int(identity['started_ns']))//1_000_000 and
            _integer(value['pid'], 1, 2**31-1) and _integer(value['ppid'], 0, 2**31-1),
            'BLOCKED_CHECKPOINT_NUMERIC')
    for key, lo, hi in (('original_exit', -255, 255), ('signal', 0, 64), ('errno', 0, 4095)):
        require(value[key] is None or _integer(value[key], lo, hi), 'BLOCKED_CHECKPOINT_NUMERIC')
    hashes = value['proof_sha256']
    require(type(hashes) is dict and set(hashes) <= set(PROOFS) and
            all(type(v) is str and re.fullmatch('[0-9a-f]{64}', v) for v in hashes.values()),
            'BLOCKED_CHECKPOINT_PROOF')
    if value['status'] == 'RUNNING':
        require(value['original_exit'] is value['signal'] is value['errno'] is None and
                value['duration_ms'] == 0 and not hashes, 'BLOCKED_CHECKPOINT_STATE')
    elif value['status'] == 'PASS':
        require(value['original_exit'] == 0 and value['signal'] is value['errno'] is None,
                'BLOCKED_CHECKPOINT_STATE')
    else:
        require(value['original_exit'] is not None and
                (value['original_exit'] != 0 or (value['signal'] or 0) > 0 or (value['errno'] or 0) > 0),
                'BLOCKED_CHECKPOINT_STATE')
    return value


def atomic_json(path, value, mode=0o644):
    """Readers never see a partial JSON file; root setup proofs remain readable."""
    path = Path(path)
    descriptor, temporary = tempfile.mkstemp(prefix=path.name+'.', suffix='.next', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w', encoding='utf8', newline='\n') as target:
            json.dump(value, target, sort_keys=True, separators=(',', ':'))
            target.write('\n')
            target.flush()
            os.fsync(target.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def read_checkpoint(path, identity):
    path = Path(path)
    require(not path.is_symlink() and path.is_file() and path.stat().st_size <= 65536,
            'BLOCKED_CHECKPOINT_PATH')
    return _validate(decode(path.read_bytes()), identity)


def _proof(root, name):
    require(name in PROOFS, 'BLOCKED_CHECKPOINT_PROOF')
    target = root/PROOFS[name]
    require(target.is_file() and not target.is_symlink() and
            all(not p.is_symlink() for p in target.parents if p != root.parent) and
            target.stat().st_size <= 4*1024*1024, 'BLOCKED_CHECKPOINT_PROOF')
    return target


def begin(root, identity, phase):
    validate(identity)
    require(phase in PHASES, 'BLOCKED_CHECKPOINT_PHASE')
    path = Path(root)/('checkpoint-'+phase+'.json')
    require(not path.exists() and not path.is_symlink(), 'BLOCKED_CHECKPOINT_REUSE')
    value = dict(schema_version=1, identity=identity, phase=phase, status='RUNNING',
                 at_ns=time.time_ns(), duration_ms=0, original_exit=None, signal=None,
                 errno=None, pid=os.getpid(), ppid=os.getppid(), proof_sha256={})
    atomic_json(path, _validate(value, identity))
    return time.monotonic_ns()


def finish(root, identity, phase, started=None, original_exit=0, signal=None, errno=None, proofs=None):
    require(phase in PHASES, 'BLOCKED_CHECKPOINT_PHASE')
    root = Path(root)
    path = root/('checkpoint-'+phase+'.json')
    previous = read_checkpoint(path, identity)
    require(previous['phase'] == phase and previous['status'] == 'RUNNING', 'BLOCKED_CHECKPOINT_REUSE')
    hashes = {}
    for name in (() if proofs is None else proofs):
        target = _proof(root, name)
        hashes[name] = hashlib.sha256(target.read_bytes()).hexdigest()
    now = time.time_ns()
    duration = (time.monotonic_ns()-started)//1_000_000 if started is not None else (now-previous['at_ns'])//1_000_000
    value = {**previous, 'status': 'PASS' if original_exit == 0 and signal is None and errno is None else 'FAIL',
             'at_ns': now, 'duration_ms': duration, 'original_exit': original_exit,
             'signal': signal, 'errno': errno, 'pid': os.getpid(), 'ppid': os.getppid(), 'proof_sha256': hashes}
    atomic_json(path, _validate(value, identity))
    return value


@contextmanager
def observe(root, identity, phase, proofs=None):
    started = begin(root, identity, phase)
    try:
        yield
    except BaseException as error:
        import subprocess
        code = error.returncode if isinstance(error, subprocess.CalledProcessError) else (
            124 if isinstance(error, subprocess.TimeoutExpired) else 1)
        if isinstance(error, SystemExit) and type(error.code) is int:
            code = error.code
        number = error.errno if isinstance(error, OSError) and _integer(error.errno, 0, 4095) else None
        finish(root, identity, phase, started, original_exit=code,
               signal=-code if code < 0 else None, errno=number)
        raise
    else:
        finish(root, identity, phase, started, proofs=proofs)


def project(root, identity):
    root = Path(root)
    result = {}
    for phase in PHASES:
        path = root/('checkpoint-'+phase+'.json')
        if not path.exists() and not path.is_symlink():
            result[phase] = {'status': 'NOT_RUN'}
            continue
        row = read_checkpoint(path, identity)
        require(row['phase'] == phase, 'BLOCKED_CHECKPOINT_PHASE')
        for name, digest in row['proof_sha256'].items():
            target = _proof(root, name)
            require(hashlib.sha256(target.read_bytes()).hexdigest() == digest, 'BLOCKED_CHECKPOINT_PROOF')
        result[phase] = row
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('operation', choices=('begin', 'finish'))
    parser.add_argument('phase', choices=PHASES)
    parser.add_argument('--exit', type=int, default=0, dest='original_exit')
    parser.add_argument('--signal', type=int)
    parser.add_argument('--proof', action='append', choices=tuple(PROOFS))
    args = parser.parse_args()
    root = Path('/evidence')
    identity = read_bound()
    if args.operation == 'begin':
        begin(root, identity, args.phase)
    else:
        finish(root, identity, args.phase, original_exit=args.original_exit, signal=args.signal, proofs=args.proof)


if __name__ == '__main__':
    try:
        main()
    except Exception:
        raise SystemExit(1) from None
