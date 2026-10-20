"""Observe an owned broker without signal permission; retire it as its own UID."""
import argparse
import errno
import json
import os
from pathlib import Path
import signal
import time

from checkpoints import atomic_json
from gate import require, validate_identity
from run_identity import read_bound

STATES = {'R': 'RUNNING', 'S': 'SLEEPING', 'D': 'DISK_SLEEP', 'I': 'IDLE',
          'Z': 'ZOMBIE', 'X': 'DEAD', 'x': 'DEAD', 'T': 'STOPPED', 't': 'TRACING'}
TERMINAL = ('ZOMBIE', 'DEAD')
ACTIVE = ('RUNNING', 'SLEEPING', 'DISK_SLEEP', 'IDLE')


def facts(pid, proc=Path('/proc')):
    directory = proc/str(pid)
    try:
        raw = (directory/'stat').read_text()
        fields = raw[raw.rfind(')')+2:].split()
        status = dict(line.split(':', 1) for line in (directory/'status').read_text().splitlines() if ':' in line)
    except FileNotFoundError:
        return None
    require(fields[0] in STATES, 'BLOCKED_ENTRY_CHILD_STATE')
    return {'pid': pid, 'ppid': int(fields[1]), 'start_ticks': int(fields[19]),
            'state': STATES[fields[0]], 'uids': [int(x) for x in status['Uid'].split()],
            'cap_eff': int(status['CapEff'], 16), 'cap_bnd': int(status['CapBnd'], 16),
            'cap_amb': int(status['CapAmb'], 16), 'nnp': int(status['NoNewPrivs'])}


def owned(pid, parent, start_ticks=None, proc=Path('/proc')):
    row = facts(pid, proc)
    if row is not None:
        require(row['ppid'] == parent and (start_ticks is None or row['start_ticks'] == start_ticks),
                'BLOCKED_ENTRY_CHILD_IDENTITY')
    return row


def wait_ready(root, identity, pid, parent, timeout_seconds=5, proc=Path('/proc'), kill0=os.kill):
    """A failed kill(0) never selects a wait/reap branch."""
    root = Path(root)
    require(not (root/'entry-broker-owner.json').exists(), 'BLOCKED_ENTRY_OWNER_REUSE')
    owner = None
    end = time.monotonic()+timeout_seconds
    while time.monotonic() < end:
        row = owned(pid, parent, None if owner is None else owner['start_ticks'], proc)
        if row is None or row['state'] in TERMINAL:
            return 3  # The Bash parent may now reap its already-terminal child.
        if owner is None:
            owner = {'identity': identity, 'pid': pid, 'ppid': parent, 'start_ticks': row['start_ticks']}
            atomic_json(root/'entry-broker-owner.json', owner)
        if row['state'] not in ACTIVE:
            return 4  # Stopped/traced is not terminal and must never be waited on.
        ready = root/'tap-broker-ready.json'
        if ready.is_file():
            validate_identity(json.loads(ready.read_text())['identity'], identity)
            current = owned(pid, parent, owner['start_ticks'], proc)
            require(current is not None and current['state'] in ACTIVE, 'BLOCKED_ENTRY_CHILD_EXIT')
            observer = facts(os.getpid(), proc)
            require(observer is not None, 'BLOCKED_ENTRY_OBSERVER')
            init = facts(parent, proc)
            require(init is not None and all(observer[k] == init[k] for k in ('uids', 'cap_eff', 'cap_bnd', 'cap_amb', 'nnp')),
                    'BLOCKED_ENTRY_ROOT_IDENTITY')
            result = {'original_exit': 0, 'errno': None}
            try:
                kill0(pid, 0)
            except OSError as error:
                result = {'original_exit': -1, 'errno': error.errno}
            after = owned(pid, parent, owner['start_ticks'], proc)
            alive = after is not None and after['state'] in ACTIVE
            value = {'schema_version': 1, 'identity': identity,
                     'root': {k: v for k, v in observer.items() if k not in ('start_ticks', 'state')},
                     'broker': current, 'owned_alive': alive, 'ready': True, 'kill0': result}
            atomic_json(root/'entry-permissions.json', value)
            require(observer['pid'] > 1 and observer['ppid'] == parent and observer['uids'] == [0]*4 and
                    observer['cap_eff'] == observer['cap_bnd'] == 12739 and observer['cap_amb'] == 0 and observer['nnp'] == 1 and
                    current['uids'] == [1000]*4 and current['cap_eff'] == current['cap_bnd'] == current['cap_amb'] == 1 << 13 and
                    current['nnp'] == 1 and alive and result == {'original_exit': -1, 'errno': errno.EPERM},
                    'BLOCKED_ENTRY_PERMISSION_PROOF')
            return 0
        time.sleep(.025)
    return 124


def cleanup_owned(root, identity, pid, parent, expected_uid=1000, proc=Path('/proc'), grace=1):
    """The unprivileged helper uses a pidfd; its sibling is reaped by Bash only."""
    owner = json.loads((root/'entry-broker-owner.json').read_text())
    validate_identity(owner['identity'], identity)
    require(set(owner) == {'identity', 'pid', 'ppid', 'start_ticks'} and owner['pid'] == pid and owner['ppid'] == parent,
            'BLOCKED_ENTRY_CHILD_IDENTITY')
    receipt = {'schema_version': 1, 'identity': identity, 'pid': pid, 'ppid': parent,
               'start_ticks': owner['start_ticks'], 'result': 'UNKNOWN', 'terminal': False,
               'sent_signal': None, 'original_exit': None, 'errno': None}
    descriptor = None
    code = 1
    try:
        row = owned(pid, parent, owner['start_ticks'], proc)
        if row is None or row['state'] in TERMINAL:
            receipt.update(result='ALREADY_TERMINAL', terminal=True)
            return 0
        require(row['uids'] == [expected_uid]*4, 'BLOCKED_ENTRY_CHILD_UID')
        # Never signal a subsequently reused PID. There is deliberately no unsafe
        # numeric-kill fallback if pidfd is unavailable or denied.
        descriptor = os.pidfd_open(pid)
        for number, label in ((signal.SIGTERM, 'TERM'), (signal.SIGKILL, 'KILL')):
            row = owned(pid, parent, owner['start_ticks'], proc)
            if row is None or row['state'] in TERMINAL:
                receipt.update(result='ALREADY_TERMINAL', terminal=True)
                return 0
            signal.pidfd_send_signal(descriptor, number)
            receipt.update(result=label, sent_signal=number)
            end = time.monotonic()+grace
            while time.monotonic() < end:
                row = owned(pid, parent, owner['start_ticks'], proc)
                if row is None or row['state'] in TERMINAL:
                    receipt['terminal'] = True
                    return 0
                time.sleep(.025)
        receipt['result'] = 'DEADLINE'
        return code
    except OSError as error:
        receipt['errno'] = error.errno if type(error.errno) is int and 0 <= error.errno <= 4095 else None
        return code
    finally:
        if descriptor is not None:
            os.close(descriptor)
        atomic_json(root/'entry-broker-cleanup.json', receipt)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('operation', choices=('ready', 'cleanup', 'reaped'))
    parser.add_argument('--pid', type=int, required=True)
    parser.add_argument('--parent', type=int, required=True)
    parser.add_argument('--exit', dest='original_exit', type=int)
    args = parser.parse_args()
    root = Path('/evidence')
    identity = read_bound()
    require(args.pid > 1 and args.parent == 1, 'BLOCKED_ENTRY_CHILD_IDENTITY')
    if args.operation == 'ready':
        return wait_ready(root, identity, args.pid, args.parent)
    if args.operation == 'cleanup':
        observer = facts(os.getpid())
        require(observer is not None and observer['uids'] == [1000]*4 and
                observer['cap_eff'] == observer['cap_bnd'] == observer['cap_amb'] == 0 and observer['nnp'] == 1,
                'BLOCKED_ENTRY_CLEANUP_PRIVILEGES')
        return cleanup_owned(root, identity, args.pid, args.parent)
    require(type(args.original_exit) is int and 0 <= args.original_exit <= 255, 'BLOCKED_ENTRY_EXIT')
    path = root/'entry-broker-cleanup.json'
    if path.is_file():
        row = json.loads(path.read_text())
        validate_identity(row['identity'], identity)
    else:
        owner_path = root/'entry-broker-owner.json'
        owner = json.loads(owner_path.read_text()) if owner_path.is_file() else None
        if owner is not None:validate_identity(owner['identity'], identity)
        row = {'schema_version':1,'identity':identity,'pid':args.pid,'ppid':args.parent,
               'start_ticks':owner['start_ticks'] if owner is not None else None,
               'result':'CHILD_TERMINAL','terminal':True,'sent_signal':None,'original_exit':None,'errno':None}
    require(row['pid'] == args.pid and row['ppid'] == args.parent and row['terminal'], 'BLOCKED_ENTRY_REAP_RUNNING')
    row['original_exit'] = args.original_exit
    atomic_json(path, row)
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception:
        raise SystemExit(1) from None
