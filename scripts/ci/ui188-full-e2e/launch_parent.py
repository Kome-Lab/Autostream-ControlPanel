"""Capability-free PID 1 keeps xvfb-run as a child and bounds owned cleanup."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

from checkpoints import atomic_json, begin, finish, read_checkpoint
from gate import require, validate_identity
from run_identity import read_bound
from tap_broker import shutdown, status


def _reap_group(group):
    if os.name != 'posix':
        return
    # Do not steal Popen's leader and turn ECHILD into a false exit 0.
    for path in Path('/proc').iterdir():
        if not path.name.isdigit() or int(path.name) == group:
            continue
        try:
            # comm may contain spaces, so locate its closing parenthesis first.
            raw=(path/'stat').read_text(); fields=raw[raw.rfind(')')+2:].split()
            if int(fields[1]) == os.getpid() and int(fields[2]) == group:
                os.waitpid(int(path.name), os.WNOHANG)
        except (OSError, ValueError, IndexError):
            continue


def _signal_child(process, number):
    try:
        if os.name == 'posix':
            os.killpg(process.pid, number)
        elif process.poll() is None:
            process.terminate() if number == signal.SIGTERM else process.kill()
    except ProcessLookupError:
        pass


def _group_alive(process):
    if os.name != 'posix':
        return process.poll() is None
    _reap_group(process.pid)
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return False
    return True


def stop_owned(process, grace=3):
    """Only the session created for this exact child is signalled."""
    # Popen must collect its leader before group reaping can observe descendants.
    process.poll()
    if not _group_alive(process):
        process.wait(timeout=.1)
        return 0
    _signal_child(process, signal.SIGTERM)
    end = time.monotonic()+grace
    while time.monotonic() < end:
        process.poll()
        if not _group_alive(process):
            process.wait(timeout=.1)
            return 0
        time.sleep(.025)
    _signal_child(process, signal.SIGKILL)
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        return 1
    end = time.monotonic()+1
    while _group_alive(process) and time.monotonic() < end:
        time.sleep(.025)
    return 1  # Escalation stays a cleanup failure even when SIGKILL succeeds.


def run(root, identity, argv, timeout_seconds, child_watch=()):
    """Return primary and cleanup outcomes independently; never imply bootstrap PASS."""
    from run_identity import validate
    validate(identity)
    require(type(timeout_seconds) in (int, float) and 0 < timeout_seconds <= 4500,
            'BLOCKED_LAUNCH_DEADLINE')
    require(all(role in ('broker', 'worker') and callable(probe) for role, probe in child_watch),
            'BLOCKED_LAUNCH_WATCH')
    root = Path(root)
    started = time.monotonic_ns()
    received = [None]
    previous = {}
    for number in (signal.SIGTERM, signal.SIGINT):
        previous[number] = signal.signal(number, lambda signum, _: received.__setitem__(0, signum))
    result = {'primary_role': 'launch', 'original_exit': 1, 'signal': None,
              'cleanup_exit': 0, 'duration_ms': 0, 'pid': os.getpid(), 'ppid': os.getppid(),
              'payload_exit': None}
    process = None
    try:
        with (root/'launcher-child.private.log').open('xb') as log:
            try:
                process = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT,
                                           start_new_session=os.name == 'posix')
            except FileNotFoundError:
                result['original_exit'] = 127
            except OSError:
                result['original_exit'] = 126
            if process is not None:
                end = time.monotonic()+timeout_seconds
                while True:
                    code = process.poll()
                    if code is not None:
                        result.update(primary_role='payload', original_exit=code,
                                      signal=-code if code < 0 else None)
                        break
                    if received[0] is not None:
                        result.update(primary_role='signal', original_exit=-received[0], signal=received[0])
                        break
                    failed = False
                    for role, probe in child_watch:
                        code = probe()
                        if code is not None:
                            require(type(code) is int and -255 <= code <= 255, 'BLOCKED_LAUNCH_WATCH_EXIT')
                            result.update(primary_role=role, original_exit=code, signal=-code if code < 0 else None)
                            failed = True
                            break
                    if failed:
                        break
                    if time.monotonic() >= end:
                        result.update(primary_role='deadline', original_exit=124)
                        break
                    time.sleep(.025)
    finally:
        if process is not None:
            result['cleanup_exit'] = stop_owned(process)
            result['payload_exit'] = process.returncode
        for number, handler in previous.items():
            signal.signal(number, handler)
        result['duration_ms'] = (time.monotonic_ns()-started)//1_000_000
    return result


def _broker_exit(root, identity, broker_pid):
    def expected_retirement(code):
        if code != 0 or not (root/'runtime-finished').is_file():
            return False
        driver = root/'driver-exit.json'
        if driver.is_file():
            validate_identity(json.loads(driver.read_text())['identity'], identity)
            return True
        rejected = root/'checkpoint-supervisor.json'
        return rejected.is_file() and read_checkpoint(rejected, identity)['status'] == 'FAIL'

    path = root/'tap-broker-exit.json'
    if path.is_file():
        row = json.loads(path.read_text())
        validate_identity(row['identity'], identity)
        code = row['original_exit']
        # A clean broker retirement after the driver finished is expected.
        if expected_retirement(code):
            return None
        return code
    try:
        # Do not reap: the final owner cleanup still has to validate/reap this child.
        observed = os.waitid(os.P_PID, broker_pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
    except ChildProcessError:
        return 1
    if observed is None:
        return None
    code = observed.si_status if observed.si_code == os.CLD_EXITED else -observed.si_status
    # Recheck after waitid: the atomic proof may have appeared since is_file().
    if path.is_file():
        row = json.loads(path.read_text())
        validate_identity(row['identity'], identity)
        require(row['original_exit'] == code, 'BLOCKED_BROKER_EXIT_CHANGED')
        if expected_retirement(code):
            return None
    return code


def _worker_exit(root, identity):
    path = root/'checkpoint-worker_exit.json'
    if path.is_file():
        row = read_checkpoint(path, identity)
        if row['status'] == 'FAIL':
            return row['original_exit']
    return None


def retire_broker(root, identity, broker_pid):
    """Retire the broker also when supervisor validation never ran."""
    def retain(wait_status):
        actual=os.waitstatus_to_exitcode(wait_status)
        atomic_json(root/'broker-owned-exit.json',{'identity':identity,'original_exit':actual,
                    'signal':-actual if actual<0 else None})
        return actual
    (root/'runtime-finished').touch(exist_ok=True)
    path = root/'tap-broker-exit.json'
    try:
        if not path.is_file():
            ready=json.loads((root/'tap-broker-ready.json').read_text())
            validate_identity(ready['identity'],identity)
            shutdown()
    except Exception:
        pass
    end = time.monotonic()+3
    while time.monotonic() < end:
        try:
            child, wait_status = os.waitpid(broker_pid, os.WNOHANG)
        except ChildProcessError:
            return 1  # No owned child may be signalled after ECHILD/PID reuse.
        if child:
            actual=retain(wait_status)
            try:
                row = json.loads(path.read_text())
                validate_identity(row['identity'], identity)
                return 0 if row['original_exit'] == actual == 0 else 1
            except Exception:
                return 1
        time.sleep(.025)
    # A final child relationship check closes the gap before signalling this PID.
    try:
        child, wait_status = os.waitpid(broker_pid, os.WNOHANG)
    except ChildProcessError:
        return 1
    if child:
        retain(wait_status)
        return 1
    try:
        os.kill(broker_pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    end = time.monotonic()+1
    while time.monotonic() < end:
        try:
            child, wait_status = os.waitpid(broker_pid, os.WNOHANG)
        except ChildProcessError:
            return 1
        if child:
            retain(wait_status)
            return 1
        time.sleep(.025)
    try:
        os.kill(broker_pid, signal.SIGKILL)
        _,wait_status=os.waitpid(broker_pid, 0)
        retain(wait_status)
    except (ProcessLookupError, ChildProcessError):
        pass
    return 1


def main():
    root = Path('/evidence')
    identity = read_bound()
    broker_pid = int(os.environ['UI188_BROKER_PID'])
    require(broker_pid > 1, 'BLOCKED_BROKER_PID')
    result = None
    cleanup = 1
    received=[None]
    previous={n:signal.signal(n,lambda value,_:received.__setitem__(0,value)) for n in (signal.SIGTERM,signal.SIGINT)}
    try:
        started = begin(root, identity, 'privilege_drop')
        facts = status()
        require(os.getpid() == 1 and facts['uid'] == facts['euid'] == 1000 and
                all(int(facts[k], 16) == 0 for k in ('CapEff', 'CapBnd', 'CapAmb')) and
                facts['NoNewPrivs'] == '1', 'BLOCKED_PRIVILEGED_INIT_RETAINED')
        atomic_json(root/'init-privileges.json',{'identity':identity,'facts':facts,'pid':os.getpid(),'ppid':os.getppid()})
        finish(root, identity, 'privilege_drop', started, proofs=('identity','init_privileges','entry_permissions'))
        begin(root, identity, 'xvfb_ready')
        result = run(root, identity, ['xvfb-run', '-a', sys.executable, '-B', '/opt/ui188/runtime_supervisor.py'],
                     timeout_seconds=80 if identity['job'] == 'contract-smoke' else 4500,
                     child_watch=(('broker', lambda: _broker_exit(root, identity, broker_pid)),
                                  ('worker', lambda: _worker_exit(root, identity))))
        pending = root/'checkpoint-xvfb_ready.json'
        if read_checkpoint(pending, identity)['status'] == 'RUNNING':
            code = result['original_exit'] if result['original_exit'] != 0 else 1
            finish(root, identity, 'xvfb_ready', original_exit=code, signal=result['signal'])
    except BaseException as error:
        result = {'primary_role': 'launch', 'original_exit': 1, 'signal': None,
                  'cleanup_exit': 0, 'duration_ms': 0, 'pid': os.getpid(), 'ppid': os.getppid(), 'payload_exit': None}
        path = root/'checkpoint-privilege_drop.json'
        if path.is_file() and read_checkpoint(path, identity)['status'] == 'RUNNING':
            finish(root, identity, 'privilege_drop', original_exit=1,
                   errno=error.errno if isinstance(error, OSError) and error.errno is not None else None)
    finally:
        cleanup = retire_broker(root, identity, broker_pid)
        if result is not None:
            if received[0] is not None:
                result.update(primary_role='signal',original_exit=-received[0],signal=received[0])
            atomic_json(root/'launch-parent-exit.json', {'identity': identity,'result':result,'broker_cleanup_exit':cleanup})
        for n,handler in previous.items():signal.signal(n,handler)
    return (result['original_exit'] if 0 < result['original_exit'] <= 255 else 1) if (
        result['primary_role'] != 'payload' or result['original_exit'] != 0 or result['cleanup_exit'] != 0 or cleanup != 0) else 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception:
        raise SystemExit(1) from None
