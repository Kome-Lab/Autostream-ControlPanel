"""Short Linux privilege/bootstrap proof. No product stack or media-quality claim."""
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import time
import uuid
from adapter_smoke import run_actual
from checkpoints import atomic_json,begin,finish,observe,read_checkpoint
from gate import require,validate_identity
from run_identity import read_bound
from tap_broker import request_tap,status


def wait_json(path,seconds=25,identity=None):
    end=time.monotonic()+seconds
    while not path.is_file():
        if identity is not None:
            for phase in ('worker_exit','broker_exit'):
                checkpoint=path.parent/('checkpoint-'+phase+'.json')
                if checkpoint.is_file():
                    observed=read_checkpoint(checkpoint,identity)
                    require(observed['status']!='FAIL','BLOCKED_BOOTSTRAP_CHILD_EXIT')
        require(time.monotonic()<end,'BLOCKED_BOOTSTRAP_READINESS');time.sleep(.1)
    return json.loads(path.read_text())


def denied(call):
    try:
        value=call()
        if hasattr(value,'close'):value.close()
    except OSError:return True
    return False


def worker_probe(root,config,identity,facts,state_root=Path('/var/lib/autostream/worker'),stop_requested=lambda:False):
    require(bool((config/'config.yml').read_text()) and bool(json.loads((config/'credentials/node-listener.json').read_text())),'BLOCKED_WORKER_SECRET_READ')
    require(denied(lambda:(config/'forbidden-write').open('w')),'BLOCKED_WORKER_CONFIG_WRITABLE')
    state=state_root/'bootstrap-owned';state.write_text(identity['run_uuid'])
    require(not Path('/r6/private-'+identity['schema']).exists() and not Path('/run/ui188').exists(),'BLOCKED_WORKER_FOREIGN_MOUNT')
    address=json.loads((root/'network-setup.json').read_text())['db_ipv4']
    require(__import__('ipaddress').ip_address(address).is_private,'BLOCKED_WORKER_DB_ADDRESS')
    with socket.create_connection((address,3306),timeout=2):pass
    atomic_json(root/'bootstrap-worker.json',{'identity':identity,'facts':facts,'config_readable':True,'config_read_only':True,'separate_state_writable':True,'foreign_runtime_private_absent':True,'db_readiness':True,'kind':'BOOTSTRAP_PROBE'})
    finish(root,identity,'worker_ready',proofs=('worker_ready',))
    end=time.monotonic()+40
    while not (root/'runtime-finished').exists():
        require(not stop_requested() and time.monotonic()<end,'BLOCKED_WORKER_NORMAL_SHUTDOWN');time.sleep(.1)
    return 0


def main():
    root=Path('/evidence');identity=read_bound();facts=status()
    require(identity['job']=='contract-smoke','BLOCKED_BOOTSTRAP_SCOPE')
    require(facts['uid']==1000 and int(facts['CapEff'],16)==int(facts['CapBnd'],16)==0,'BLOCKED_BOOTSTRAP_PRIVILEGES')
    require(denied(lambda:socket.socket(socket.AF_PACKET,socket.SOCK_DGRAM,0)),'BLOCKED_RAW_CAP_RETAINED')
    with (root/'bootstrap-setup-negative.private.log').open('xb') as log:
        setup=subprocess.run(['iptables','-L','OUTPUT'],stdout=log,stderr=subprocess.STDOUT,timeout=3)
    require(setup.returncode!=0 and denied(lambda:os.chown('/worker-config',0,0)),'BLOCKED_SETUP_CAP_RETAINED')
    secret=json.loads(Path(os.environ['UI188_TEST_CREDENTIALS']).read_text())
    defaults=Path('/tmp/ui188-secrets/db-client.cnf')
    defaults.write_text('[client]\nuser=ui188_r9\nhost=db\npassword='+secret['db_password']+'\ndatabase='+identity['schema']+'\n');defaults.chmod(0o600)
    with observe(root,identity,'db',proofs=('db_readiness',)):
        with (root/'bootstrap-db.private.log').open('xb') as log:
            command=['mariadb','--defaults-extra-file='+str(defaults),'--batch','--skip-column-names','-e','SELECT 1, DATABASE();']
            db=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,timeout=10)
        if db.returncode!=0:raise subprocess.CalledProcessError(db.returncode,command)
        require((root/'bootstrap-db.private.log').read_text().strip()=='1\t'+identity['schema'],'BLOCKED_DB_READINESS')
        atomic_json(root/'db-ready.json',{'identity':identity,'original_exit':db.returncode,'query_verified':True})
    with observe(root,identity,'worker_config'):
        config=Path('/worker-config');(config/'credentials').mkdir(mode=0o700)
        (config/'config.yml').write_text('bootstrap: isolated-worker-only\n');(config/'config.yml').chmod(0o600)
        atomic_json(config/'credentials/node-listener.json',{'run_uuid':identity['run_uuid'],'service_id':'worker-b'},mode=0o600)
    worker=wait_json(root/'bootstrap-worker.json',identity=identity);validate_identity(worker['identity'],identity)
    require(worker['facts']['pid_namespace']!=facts['pid_namespace'] and worker['facts']['cgroup_namespace']!=facts['cgroup_namespace'] and worker['facts']['cgroup_root_inode']!=facts['cgroup_root_inode'] and worker['facts']['network_namespace']==facts['network_namespace'],'BLOCKED_WORKER_ISOLATION')
    # Real kernel BPF, real SCM_RIGHTS transfer and capability-free consumer.
    with observe(root,identity,'filtered_rtp_fd',proofs=('filtered_rtp_fd',)):
        for owner in ('encoder-b','encoder-a'):
            with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as receiver:
                receiver.bind(('127.0.0.1',0));port=receiver.getsockname()[1]
                directory=Path('/r6/data/'+owner+'-archives-'+identity['case'])/'tmp'/str(uuid.uuid4());directory.mkdir(parents=True)
                (directory/'audio.sdp').write_text('v=0\nm=audio '+str(port)+' RTP/AVP 96\na=rtpmap:96 L16/48000/2\n')
                with request_tap(port) as tap:
                    with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sender:
                        sender.sendto(struct.pack('!BBHII',0x80,96,1,960,111)+b'\0'*3840,('127.0.0.1',port))
                    packet=tap.recv(64);require(20<=len(packet)<=64 and packet[9]==17,'BLOCKED_FILTERED_RTP_FD')
        atomic_json(root/'filtered-rtp-proof.json',{'identity':identity,'original_exit':0,'receivers':2,'header_bytes_max':64})
    started=begin(root,identity,'go_constructor')
    try:
        go=run_actual(Path('/opt/ui188/bot-constructor'),root/'go-contract',identity)
    except BaseException:
        path=root/'go-contract/go-constructor-exit.json'
        receipt=json.loads(path.read_text()) if path.is_file() else None
        if receipt is not None:validate_identity(receipt['identity'],identity)
        code=receipt['original_exit'] if receipt is not None and receipt['original_exit']!=0 else 1
        finish(root,identity,'go_constructor',started,original_exit=code,signal=-code if code<0 else None,
               proofs=('constructor_exit',) if receipt is not None else ())
        raise
    else:
        finish(root,identity,'go_constructor',started,proofs=('constructor','constructor_exit'))
    atomic_json(root/'bootstrap-runtime.json',{'identity':identity,'facts':facts,'db_readiness':True,'setup_permissions_lost':True,'raw_socket_direct_denied':True,'filtered_RTP_FD_verified':True,'go_constructor':go,'kind':'BOOTSTRAP_PROBE','media_seconds':0,'full_e2e':'NOT_RUN'})
    return 0

if __name__=='__main__':
    try:sys.exit(main())
    except Exception:sys.exit(1)
