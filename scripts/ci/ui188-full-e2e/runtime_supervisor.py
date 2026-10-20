"""Unprivileged supervisor retains the driver's original exit and retires its broker."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from gate import require
from run_identity import read_bound
from tap_broker import shutdown,status


def retire(root,identity,code):
    # Persist the observed driver exit before attempting fallible broker cleanup.
    (root/'driver-exit.json').write_text(json.dumps({'identity':identity,'original_exit':code})+'\n')
    (root/'runtime-finished').touch()
    cleanup_exit=1;broker_exit=None
    try:
        shutdown()
        end=time.monotonic()+10
        while not (root/'tap-broker-exit.json').is_file() and time.monotonic()<end:time.sleep(.1)
        value=json.loads((root/'tap-broker-exit.json').read_text())
        from gate import validate_identity
        validate_identity(value['identity'],identity)
        broker_exit=value['original_exit']
        require(type(broker_exit) is int and broker_exit==0,'BLOCKED_BROKER_EXIT')
        cleanup_exit=0
    except Exception:
        pass
    finally:
        (root/'supervisor-exit.json').write_text(json.dumps({'identity':identity,'original_driver_exit':code,'broker_exit':broker_exit,
            'cleanup_exit':cleanup_exit,'cleanup_reason':'NORMAL' if cleanup_exit==0 else 'BROKER_CLEANUP_FAILED'})+'\n')
    return cleanup_exit


def main():
    root=Path('/evidence');identity=read_bound();facts=status()
    require(facts['uid']==facts['euid']==1000 and all(int(facts[k],16)==0 for k in ('CapEff','CapBnd','CapAmb')) and facts['NoNewPrivs']=='1','BLOCKED_RUNTIME_PRIVILEGES')
    init=dict(line.split(':',1) for line in Path('/proc/1/status').read_text().splitlines() if ':' in line)
    require(all(int(value)==1000 for value in init['Uid'].split()) and int(init['CapEff'],16)==int(init['CapBnd'],16)==0,'BLOCKED_PRIVILEGED_INIT_RETAINED')
    secret=Path(os.environ['UI188_TEST_CREDENTIALS'])
    require(secret.stat().st_uid==1000 and secret.stat().st_mode&0o777==0o600 and bool(json.loads(secret.read_text())),'BLOCKED_SECRET_READ')
    # The original mounted secret remains root-only, outside the runtime credential copy.
    try: Path('/run/ui188/credentials.json').read_bytes()
    except PermissionError: original_denied=True
    else: original_denied=False
    require(original_denied,'BLOCKED_SETUP_SECRET_RETAINED')
    (root/'runtime-privileges.json').write_text(json.dumps({'identity':identity,'facts':facts,'secret_readable':True,'setup_secret_unreadable':True})+'\n')
    code=1
    try:
        target='bootstrap_probe.py' if os.environ.get('UI188_BOOTSTRAP_ONLY')=='1' else 'execute.py'
        code=subprocess.call([sys.executable,'-B','/opt/ui188/'+target])
    finally:
        cleanup=retire(root,identity,code)
    require(cleanup==0,'BLOCKED_BROKER_EXIT')
    return code

if __name__=='__main__':
    try:sys.exit(main())
    except Exception:sys.exit(1)
