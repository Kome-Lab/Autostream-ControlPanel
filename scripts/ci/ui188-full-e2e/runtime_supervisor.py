"""Preserve primary failures and retire the owned broker even on initial rejection."""
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import time

from checkpoints import atomic_json, begin, finish, observe
from gate import require, validate_identity
from run_identity import read_bound
from tap_broker import shutdown, status


def retire(root, identity, code):
    metadata_error = False
    retained_code = code
    try:
        if code is not None:
            require(type(code) is int and -255 <= code <= 255, 'BLOCKED_DRIVER_EXIT_CHANGED')
            path = root/'driver-exit.json'
            if path.exists():
                existing = json.loads(path.read_text())
                validate_identity(existing['identity'], identity)
                retained_code = existing['original_exit']
                require(retained_code == code, 'BLOCKED_DRIVER_EXIT_CHANGED')
            else:
                atomic_json(path, {'identity': identity, 'original_exit': code})
    except Exception:
        metadata_error = True
    # The marker asks the Worker to stop; it never asserts driver success.
    (root/'runtime-finished').touch(exist_ok=True)
    cleanup_exit = 1
    broker_exit = None
    try:
        path = root/'tap-broker-exit.json'
        if not path.is_file():
            ready = json.loads((root/'tap-broker-ready.json').read_text())
            validate_identity(ready['identity'], identity)
            shutdown()
        end = time.monotonic()+3
        while not path.is_file() and time.monotonic() < end:
            time.sleep(.025)
        value = json.loads(path.read_text())
        validate_identity(value['identity'], identity)
        broker_exit = value['original_exit']
        require(type(broker_exit) is int and broker_exit == 0, 'BLOCKED_BROKER_EXIT')
        cleanup_exit = 1 if metadata_error else 0
    except Exception:
        pass
    finally:
        atomic_json(root/'supervisor-exit.json', {'identity': identity,
            'original_driver_exit': retained_code, 'broker_exit': broker_exit, 'cleanup_exit': cleanup_exit,
            'cleanup_reason': 'DRIVER_EXIT_CONFLICT' if metadata_error else ('NORMAL' if cleanup_exit == 0 else 'BROKER_CLEANUP_FAILED')})
    return cleanup_exit


def run_driver(root, identity, argv, timeout_seconds):
    """Actually execute the driver and retain its exit independently of cleanup."""
    started = begin(root, identity, 'driver_exit')
    process = None
    code = 1
    cleanup_exit = 0
    received = [None]
    old = {number: signal.signal(number, lambda n, _: received.__setitem__(0, n))
           for number in (signal.SIGTERM, signal.SIGINT)}
    try:
        try:
            # Inherit the launcher's session so PID 1 can retire every descendant.
            process = subprocess.Popen(argv)
        except FileNotFoundError:
            code = 127
        except OSError:
            code = 126
        if process is not None:
            end = time.monotonic()+timeout_seconds
            while True:
                actual = process.poll()
                if actual is not None:
                    code = actual
                    break
                if received[0] is not None:
                    code = -received[0]
                    break
                if time.monotonic() >= end:
                    code = 124
                    break
                time.sleep(.025)
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=1)
                cleanup_exit = 1
        actual = process.returncode if process is not None else None
        atomic_json(root/'driver-exit.json', {'identity': identity, 'original_exit': code,
            'signal': -code if code < 0 else None, 'payload_exit': actual, 'cleanup_exit': cleanup_exit})
        finish(root, identity, 'driver_exit', started, original_exit=code,
               signal=-code if code < 0 else None, proofs=('driver_exit',))
        for number, handler in old.items():
            signal.signal(number, handler)
    return code, cleanup_exit


def check_runtime(root, identity):
    with observe(root, identity, 'runtime_privileges', proofs=('runtime_privileges',)):
        facts = status()
        require(facts['uid'] == facts['euid'] == 1000 and
                all(int(facts[k], 16) == 0 for k in ('CapEff', 'CapBnd', 'CapAmb')) and
                facts['NoNewPrivs'] == '1', 'BLOCKED_RUNTIME_PRIVILEGES')
        init = dict(line.split(':', 1) for line in Path('/proc/1/status').read_text().splitlines() if ':' in line)
        require(all(int(v) == 1000 for v in init['Uid'].split()) and
                all(int(init[k], 16) == 0 for k in ('CapEff', 'CapBnd', 'CapAmb')) and
                init['NoNewPrivs'].strip() == '1', 'BLOCKED_PRIVILEGED_INIT_RETAINED')
        secret = Path(os.environ['UI188_TEST_CREDENTIALS'])
        require(secret.stat().st_uid == 1000 and secret.stat().st_mode & 0o777 == 0o600 and
                bool(json.loads(secret.read_text())), 'BLOCKED_SECRET_READ')
        try:
            Path('/run/ui188/credentials.json').read_bytes()
        except PermissionError:
            original_denied = True
        else:
            original_denied = False
        require(original_denied, 'BLOCKED_SETUP_SECRET_RETAINED')
        atomic_json(root/'runtime-privileges.json', {'identity': identity, 'facts': facts,
                    'init_facts': {'pid': 1, 'uid': [int(v) for v in init['Uid'].split()],
                        **{k: init[k].strip() for k in ('CapEff', 'CapBnd', 'CapAmb', 'NoNewPrivs')}},
                    'secret_readable': True, 'setup_secret_unreadable': True})


def main(root=Path('/evidence')):
    identity = read_bound()
    code = None  # No driver launch means NOT_RUN rather than an invented driver exit.
    driver_cleanup = 0
    supervisor_start = begin(root, identity, 'supervisor')
    try:
        display = re.fullmatch(r':([0-9]{1,5})(?:\.[0-9]+)?', os.environ.get('DISPLAY', ''))
        require(display is not None, 'BLOCKED_XVFB_DISPLAY')
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as probe:
            probe.settimeout(1)
            probe.connect('/tmp/.X11-unix/X'+display[1])
        finish(root, identity, 'xvfb_ready')
        check_runtime(root, identity)
        target = 'bootstrap_probe.py' if os.environ.get('UI188_BOOTSTRAP_ONLY') == '1' else 'execute.py'
        code, driver_cleanup = run_driver(root, identity, [sys.executable, '-B', '/opt/ui188/'+target],
                                          70 if identity['job'] == 'contract-smoke' else 4400)
    except BaseException as error:
        # This is a supervisor rejection, not an invented driver result.
        finish(root, identity, 'supervisor', supervisor_start, original_exit=1,
               errno=error.errno if isinstance(error, OSError) and error.errno is not None else None)
        raise
    else:
        finish(root, identity, 'supervisor', supervisor_start, original_exit=code,
               signal=-code if code < 0 else None)
    finally:
        cleanup = retire(root, identity, code)
    return code if code is not None and code != 0 else (1 if cleanup != 0 or driver_cleanup != 0 else 0)


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception:
        raise SystemExit(1) from None
