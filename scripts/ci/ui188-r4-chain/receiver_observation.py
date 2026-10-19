"""Owned scope metadata only; no command-line URLs, headers or media payload."""
import collections,datetime,json,os,pathlib,re,socket,struct,time
import sys
sys.path.insert(0,'/evidence')
from resource_snapshot import snapshot
def socket_memory():
 result=[]
 with socket.socket(socket.AF_NETLINK,socket.SOCK_RAW,4) as channel:
  channel.bind((0,0));channel.settimeout(1)
  request=struct.pack('=BBBBI',socket.AF_INET,socket.IPPROTO_UDP,64,0,0xffffffff)+bytes(48)
  channel.sendto(struct.pack('=IHHII',16+len(request),20,0x301,1,0)+request,(0,0))
  while True:
   data=channel.recv(65536);offset=0
   while offset+16<=len(data):
    length,kind,flags,sequence,pid=struct.unpack_from('=IHHII',data,offset);assert length>=16
    body=data[offset+16:offset+length];offset+=(length+3)&~3
    if kind==3:return result
    if kind==2:
     error=struct.unpack_from('=i',body)[0]
     if error:raise OSError(-error,'socket diagnostic request rejected')
     continue
    assert len(body)>=72
    port=struct.unpack_from('!H',body,4)[0];inode=struct.unpack_from('=I',body,68)[0];attribute=72
    while attribute+4<=len(body):
     size,key=struct.unpack_from('=HH',body,attribute);assert size>=4
     if key==7:
      values=struct.unpack_from('=9I',body,attribute+4)
      result.append({'udp_port':port,'inode':inode,'rmem_alloc_bytes':values[0],'receive_buffer_bytes':values[1],'kernel_drops':values[8]})
     attribute+=(size+3)&~3
root=pathlib.Path('/r6');case=os.environ['UI188_PROBE_ATTEMPT'];out=root/'diagnostic';out.mkdir(exist_ok=True)
burst_remaining=0;last_burst=-1e9;last_drops=0
with (out/('consumer-'+case+'.jsonl')).open('x') as f:
 while not (pathlib.Path('/evidence')/('chain-finished-'+case)).exists():
  row={'monotonic':time.monotonic(),'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'cpu_stat':pathlib.Path('/sys/fs/cgroup/cpu.stat').read_text(),'cpu_max':pathlib.Path('/sys/fs/cgroup/cpu.max').read_text(),'memory':pathlib.Path('/sys/fs/cgroup/memory.current').read_text(),'memory_events':pathlib.Path('/sys/fs/cgroup/memory.events').read_text(),'swap_current':pathlib.Path('/sys/fs/cgroup/memory.swap.current').read_text(),'pressure':{name:pathlib.Path('/sys/fs/cgroup',name+'.pressure').read_text() for name in ['cpu','memory','io']},'ffmpeg':[],'playlists':[],'udp':[]}
  resource=snapshot();row['memory_stat']=resource['cgroup']['memory.stat'];row['memory_peak']=resource['cgroup']['memory.peak'];row['memory_events_local']=resource['cgroup']['memory.events.local'];row['process_memory']=resource['process_memory_no_argv_or_environment']
  for p in pathlib.Path('/proc').iterdir():
   if not p.name.isdigit():continue
   try:
    if p.joinpath('comm').read_text().strip()!='ffmpeg':continue
    args=p.joinpath('cmdline').read_bytes().split(b'\0');owned=any(b'/r6/data/encoder-' in a for a in args)
    if not owned:continue
    tasks=collections.Counter(q.joinpath('wchan').read_text().strip() for q in p.joinpath('task').iterdir());stats=p.joinpath('stat').read_text().split()
    row['ffmpeg'].append({'pid':int(p.name),'cpu_ticks':int(stats[13])+int(stats[14]),'state':stats[2],'threads':stats[19],'wchan':dict(tasks),'io':p.joinpath('io').read_text()})
   except (OSError,ProcessLookupError):continue
  for p in root.glob('data/encoder-*-archives-'+case+'/tmp/**/*'):
   try:
    if p.name.endswith('.m3u8'):row['playlists'].append({'path':str(p),'mtime_ns':p.stat().st_mtime_ns,'size':p.stat().st_size,'playlist':p.read_text()[-700:]})
    elif 'progress' in p.name and p.is_file():row.setdefault('progress',[]).append({'path':str(p),'text':p.read_text()[-1500:]})
   except OSError:pass
  ports=set()
  for sdp in root.glob('data/encoder-*-archives-'+case+'/tmp/**/*.sdp'):
   match=re.search(r'^m=audio (\d+) RTP/AVP 96$',sdp.read_text(),re.M)
   if match:ports.add(f'{int(match[1]):04X}')
  for progress in root.glob('private-ui188_r9_b5/tee-s*-progress-'+case+'.txt'):
   row.setdefault('external_tee_consumer_progress',[]).append({'path':str(progress),'text':progress.read_text()[-1000:]})
  for line in pathlib.Path('/proc/net/udp').read_text().splitlines()[1:]:
   a=line.split()
   if len(a)>=13 and a[1].split(':')[-1] in ports:row['udp'].append({'local':a[1],'inode':a[9],'queue_hex':a[4],'kernel_drops':int(a[-1])})
  try:
   row['socket_memory']=[item for item in socket_memory() if f"{item['udp_port']:04X}" in ports and any(str(item['inode'])==udp['inode'] for udp in row['udp'])]
  except Exception as error:row['socket_memory_error']=str(error)
  if any(int(line.split()[1]) for line in row['memory_events'].splitlines() if line.split()[0] in ['oom','oom_kill']):
   marker=pathlib.Path('/evidence/host-stop-requested.json')
   if not marker.exists():
    temporary=marker.with_suffix('.observer-tmp');temporary.write_text(json.dumps({'reason':'R7_NATIVE_OOM_OBSERVED','at':row['at']})+'\n');temporary.replace(marker)
  f.write(json.dumps(row)+'\n');f.flush()
  # Limit normal observation overhead; increase resolution for queue growth/loss.
  drops=sum(u['kernel_drops'] for u in row['udp']);abnormal=drops>last_drops or any(int(u['queue_hex'].split(':')[1],16)>=131072 for u in row['udp']);last_drops=drops
  if abnormal and time.monotonic()-last_burst>=40 and burst_remaining==0:burst_remaining=20;last_burst=time.monotonic()
  if burst_remaining:burst_remaining-=1;time.sleep(1)
  else:time.sleep(5)
