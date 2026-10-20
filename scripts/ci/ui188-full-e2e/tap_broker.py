"""NET_RAW belongs only to this broker; clients receive an exact-port filtered FD."""
import array
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import socket
import struct
import sys
import time

from gate import require
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


def serve():
    identity=read_bound();root=Path('/evidence')
    facts=status()
    require(facts['uid']==facts['euid']==1000 and int(facts['CapEff'],16)==int(facts['CapBnd'],16)==1<<13,'BLOCKED_OBSERVER_CAPABILITIES')
    spec=importlib.util.spec_from_file_location('immutable_packet_capture','/opt/ui188/original_packet_capture.py')
    original=importlib.util.module_from_spec(spec);spec.loader.exec_module(original)
    server=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);server.bind(ADDRESS);os.chmod(ADDRESS,0o600);server.listen(4);server.settimeout(1)
    (root/'tap-broker-ready.json').write_text(json.dumps({'identity':identity,'facts':facts,'original_filter_sha256':hashlib.sha256(Path('/opt/ui188/original_packet_capture.py').read_bytes()).hexdigest()})+'\n')
    issued=set();exit_code=1;end=time.monotonic()+4500
    try:
        while time.monotonic()<end:
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
    finally:
        server.close()
        (root/'tap-broker-exit.json').write_text(json.dumps({'identity':identity,'original_exit':exit_code,'issued_taps':len(issued)})+'\n')
    return exit_code


def shutdown():
    identity=read_bound()
    with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as client:
        client.settimeout(3);client.connect(ADDRESS)
        client.sendall(json.dumps({'operation':'shutdown','run_uuid':identity['run_uuid']}).encode())


if __name__=='__main__':
    try: sys.exit(serve())
    except Exception: sys.exit(1)
