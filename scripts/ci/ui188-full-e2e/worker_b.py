"""Worker B has only its own config volume, state, PID namespace and cgroup."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
from gate import require
from run_identity import read_bound
from tap_broker import status

def main():
    root=Path('/evidence');config=Path('/worker-config');end=time.monotonic()+180;stop=[False]
    signal.signal(signal.SIGTERM,lambda *_:stop.__setitem__(0,True))
    while True:
        try:
            identity=read_bound()
            if (config/'config.yml').is_file() and (config/'credentials/node-listener.json').is_file():break
        except (OSError,ValueError):pass
        require(not stop[0] and time.monotonic()<end,'BLOCKED_WORKER_READINESS')
        time.sleep(.1)
    facts=status()
    require(facts['uid']==facts['euid']==1000 and all(int(facts[k],16)==0 for k in ('CapEff','CapBnd','CapAmb')),'BLOCKED_WORKER_PRIVILEGES')
    if os.environ.get('UI188_BOOTSTRAP_ONLY')=='1':
        from bootstrap_probe import worker_probe
        return worker_probe(root,config,identity,facts)
    canary=json.loads((root/'closed-canary.json').read_text())
    require(canary['namespace']==facts['network_namespace'],'BLOCKED_WORKER_NETWORK')
    env={**os.environ,'AUTOSTREAM_NODE_CONFIG':str(config/'config.yml'),'CREDENTIALS_DIRECTORY':str(config/'credentials'),
        'CONTROL_PANEL_HEARTBEAT_INTERVAL_SEC':'1','CONTROL_PANEL_RUNTIME_CONFIG_REFRESH_INTERVAL':'1s'}
    with (root/'worker-b.private.log').open('xb') as log:
        process=subprocess.Popen(['/r6/bin/worker'],env=env,stdout=log,stderr=subprocess.STDOUT)
        try:
            while process.poll() is None and not stop[0] and not (root/'runtime-finished').exists():
                stat=Path('/proc/'+str(process.pid)+'/stat').read_text().split()
                proof={'identity':identity,'pid':process.pid,'start_ticks':int(stat[21]),'at_ns':time.time_ns(),'namespace':facts['network_namespace'],
                       'pid_namespace':facts['pid_namespace'],'cgroup_sha256':facts['cgroup_sha256'],'facts':facts,'alive':stat[2]!='Z'}
                temporary=root/'worker-b-liveness.next';temporary.write_text(json.dumps(proof)+'\n');temporary.replace(root/'worker-b-liveness.json');time.sleep(.25)
        finally:
            requested=process.poll() is None
            if requested:process.terminate()
            try:code=process.wait(timeout=20)
            except subprocess.TimeoutExpired:process.kill();code=process.wait(timeout=5)
            (root/'worker-b-exit.json').write_text(json.dumps({'identity':identity,'original_exit':code,'stop_requested':requested})+'\n')
    return 0 if requested and code==0 else 1

if __name__=='__main__':
    try:sys.exit(main())
    except Exception:sys.exit(1)
