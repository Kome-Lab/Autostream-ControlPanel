"""NET_RAW belongs only to this broker; clients receive an exact-port filtered FD."""
import array
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import socket
import struct
import sys
import time

from gate import require
from checkpoints import atomic_json,begin,finish
from run_identity import read_bound

ADDRESS = '/tmp/ui188-secrets/rtp-tap.sock'


def status():
    fields = dict(line.split(':',1) for line in Path('/proc/self/status').read_text().splitlines() if ':' in line)
    return {'uid':os.getuid(),'euid':os.geteuid(),'CapEff':fields['CapEff'].strip(),'CapBnd':fields['CapBnd'].strip(),
            'CapAmb':fields['CapAmb'].strip(),'NoNewPrivs':fields['NoNewPrivs'].strip(),
            'pid_namespace':os.readlink('/proc/self/ns/pid'),'network_namespace':os.readlink('/proc/self/ns/net'),
            'cgroup_namespace':os.readlink('/proc/self/ns/cgroup'),'cgroup_root_inode':Path('/sys/fs/cgroup').stat().st_ino,
            'memory_max':Path('/sys/fs/cgroup/memory.max').read_text().strip(),'cpu_max':Path('/sys/fs/cgroup/cpu.max').read_text().strip(),
            'cgroup_sha256':hashlib.sha256(Path('/proc/self/cgroup').read_bytes()).hexdigest()}


def request_tap(port):
    require(type(port) is int and 0<port<65536,'BLOCKED_TAP_PORT')
    identity=read_bound()
    client=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
    client.settimeout(3)
    try:
        client.connect(ADDRESS)
        client.sendall(json.dumps({'operation':'tap','run_uuid':identity['run_uuid'],'port':port}).encode())
        message,control,flags,_=client.recvmsg(32,socket.CMSG_SPACE(array.array('i').itemsize))
        require(message==b'FILTERED_RTP_FD' and not flags,'BLOCKED_TAP_REPLY')
        descriptors=array.array('i')
        for level,kind,data in control:
            require(level==socket.SOL_SOCKET and kind==socket.SCM_RIGHTS,'BLOCKED_TAP_REPLY')
            descriptors.frombytes(data[:len(data)-len(data)%descriptors.itemsize])
        require(len(descriptors)==1,'BLOCKED_TAP_REPLY')
        tap=socket.socket(fileno=descriptors[0]);tap.settimeout(1)
        return tap
    finally:
        client.close()


def serve(root=Path('/evidence')):
    identity=read_bound();started=begin(root,identity,'broker_ready')
    issued=set();exit_code=1;server=None;ready=False;number=None;received=[None]
    previous={n:signal.signal(n,lambda value,_:received.__setitem__(0,value)) for n in (signal.SIGTERM,signal.SIGINT)}
    try:
        facts=status()
        require(facts['uid']==facts['euid']==1000 and int(facts['CapEff'],16)==int(facts['CapBnd'],16)==1<<13 and int(facts['CapAmb'],16)==1<<13 and facts['NoNewPrivs']=='1','BLOCKED_OBSERVER_CAPABILITIES')
        spec=importlib.util.spec_from_file_location('immutable_packet_capture','/opt/ui188/original_packet_capture.py')
        original=importlib.util.module_from_spec(spec);spec.loader.exec_module(original)
        server=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);server.bind(ADDRESS);os.chmod(ADDRESS,0o600);server.listen(4);server.settimeout(1)
        atomic_json(root/'tap-broker-ready.json',{'identity':identity,'facts':facts,'original_filter_sha256':hashlib.sha256(Path('/opt/ui188/original_packet_capture.py').read_bytes()).hexdigest()})
        finish(root,identity,'broker_ready',started,proofs=('broker_ready',));ready=True
        end=time.monotonic()+4500
        while time.monotonic()<end and received[0] is None:
            try: connection,_=server.accept()
            except socket.timeout: continue
            with connection:
                connection.settimeout(2)
                _,uid,_=struct.unpack('3i',connection.getsockopt(socket.SOL_SOCKET,socket.SO_PEERCRED,12))
                require(uid==1000,'BLOCKED_TAP_PEER')
                request=json.loads(connection.recv(1024))
                require(request.get('run_uuid')==identity['run_uuid'],'BLOCKED_TAP_IDENTITY')
                if request=={'operation':'shutdown','run_uuid':identity['run_uuid']}:
                    exit_code=0;break
                require(set(request)=={'operation','run_uuid','port'} and request['operation']=='tap','BLOCKED_TAP_REQUEST')
                port=request['port'];require(type(port) is int and 0<port<65536 and port not in issued and len(issued)<2,'BLOCKED_TAP_DUPLICATE')
                matches=[]
                for owner in ('encoder-a','encoder-b'):
                    for path in Path('/r6/data/'+owner+'-archives-'+identity['case']).glob('tmp/*/*.sdp'):
                        if not path.is_symlink() and re.search(r'^m=audio '+str(port)+r' RTP/AVP 96$',path.read_text(),re.M) and 'L16/48000/2' in path.read_text():matches.append(path)
                require(len(matches)==1,'BLOCKED_TAP_SDP_OWNER')
                with original.open_rtp_header_tap(port) as tap:
                    descriptors=array.array('i',[tap.fileno()])
                    connection.sendmsg([b'FILTERED_RTP_FD'],[(socket.SOL_SOCKET,socket.SCM_RIGHTS,descriptors)])
                issued.add(port)
        if received[0] is not None:exit_code=-received[0]
    except BaseException as error:
        number=error.errno if isinstance(error,OSError) and error.errno is not None else None
        raise
    finally:
        if received[0] is not None:exit_code=-received[0]
        if server is not None:server.close()
        if not ready:
            finish(root,identity,'broker_ready',started,original_exit=exit_code,signal=received[0],errno=number)
        end_started=begin(root,identity,'broker_exit')
        atomic_json(root/'tap-broker-exit.json',{'identity':identity,'original_exit':exit_code,'signal':received[0],'issued_taps':len(issued)})
        finish(root,identity,'broker_exit',end_started,original_exit=exit_code,signal=received[0],errno=number,proofs=('broker_exit',))
        for n,handler in previous.items():signal.signal(n,handler)
    return exit_code


def shutdown():
    identity=read_bound()
    with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as client:
        client.settimeout(3);client.connect(ADDRESS)
        client.sendall(json.dumps({'operation':'shutdown','run_uuid':identity['run_uuid']}).encode())


if __name__=='__main__':
    try: sys.exit(serve())
    except Exception: sys.exit(1)
