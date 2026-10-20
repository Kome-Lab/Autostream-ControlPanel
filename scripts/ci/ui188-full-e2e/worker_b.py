"""Worker B has only its own config volume, state, PID namespace and cgroup."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
from gate import require
from checkpoints import atomic_json,begin,finish,read_checkpoint
from run_identity import read_bound
from tap_broker import status

def main():
    root=Path('/evidence');config=Path('/worker-config');end=time.monotonic()+180;received=[None]
    for number in (signal.SIGTERM,signal.SIGINT):
        signal.signal(number,lambda n,_:received.__setitem__(0,n))
    identity=None;ready_started=None;code=1;child_code=None;requested=False
    try:
        while True:
            try:
                identity=read_bound()
                if ready_started is None:ready_started=begin(root,identity,'worker_ready')
                if (config/'config.yml').is_file() and (config/'credentials/node-listener.json').is_file():break
            except (OSError,ValueError):pass
            require(not (root/'runtime-finished').exists() and received[0] is None and time.monotonic()<end,'BLOCKED_WORKER_READINESS')
            time.sleep(.1)
        facts=status()
        require(facts['uid']==facts['euid']==1000 and all(int(facts[k],16)==0 for k in ('CapEff','CapBnd','CapAmb')) and facts['NoNewPrivs']=='1','BLOCKED_WORKER_PRIVILEGES')
        if os.environ.get('UI188_BOOTSTRAP_ONLY')=='1':
            from bootstrap_probe import worker_probe
            code=worker_probe(root,config,identity,facts,stop_requested=lambda:received[0] is not None)
            requested=(root/'runtime-finished').exists()
            return code
        canary=json.loads((root/'closed-canary.json').read_text())
        require(canary['namespace']==facts['network_namespace'],'BLOCKED_WORKER_NETWORK')
        env={**os.environ,'AUTOSTREAM_NODE_CONFIG':str(config/'config.yml'),'CREDENTIALS_DIRECTORY':str(config/'credentials'),
            'CONTROL_PANEL_HEARTBEAT_INTERVAL_SEC':'1','CONTROL_PANEL_RUNTIME_CONFIG_REFRESH_INTERVAL':'1s'}
        with (root/'worker-b.private.log').open('xb') as log:
            process=subprocess.Popen(['/r6/bin/worker'],env=env,stdout=log,stderr=subprocess.STDOUT)
            finish(root,identity,'worker_ready',ready_started)
            try:
                while process.poll() is None and received[0] is None and not (root/'runtime-finished').exists():
                    stat=Path('/proc/'+str(process.pid)+'/stat').read_text().split()
                    proof={'identity':identity,'pid':process.pid,'start_ticks':int(stat[21]),'at_ns':time.time_ns(),'namespace':facts['network_namespace'],
                           'pid_namespace':facts['pid_namespace'],'cgroup_sha256':facts['cgroup_sha256'],'facts':facts,'alive':stat[2]!='Z'}
                    atomic_json(root/'worker-b-liveness.json',proof);time.sleep(.25)
            finally:
                requested=process.poll() is None
                if requested:process.terminate()
                try:child_code=process.wait(timeout=20)
                except subprocess.TimeoutExpired:process.kill();child_code=process.wait(timeout=5)
            code=0 if requested and child_code==0 else 1
            return code
    finally:
        if identity is not None:
            original=-received[0] if received[0] is not None else code
            path=root/'checkpoint-worker_ready.json'
            if path.is_file() and read_checkpoint(path,identity)['status']=='RUNNING':
                finish(root,identity,'worker_ready',ready_started,original_exit=original if original!=0 else 1,
                       signal=received[0])
            begun=begin(root,identity,'worker_exit')
            atomic_json(root/'worker-b-exit.json',{'identity':identity,'original_exit':original if child_code is None else child_code,
                'wrapper_exit':original,'signal':received[0],'stop_requested':requested,
                'kind':'BOOTSTRAP_PROBE' if os.environ.get('UI188_BOOTSTRAP_ONLY')=='1' else 'PRODUCT_WORKER'})
            finish(root,identity,'worker_exit',begun,original_exit=original,signal=received[0],proofs=('worker_exit',))

if __name__=='__main__':
    try:sys.exit(main())
    except Exception:sys.exit(1)
