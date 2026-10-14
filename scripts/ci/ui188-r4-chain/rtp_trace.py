"""Observe metadata on one owned internal RTP UDP port; never record payloads."""
import argparse,ctypes,datetime,json,os,pathlib,re,signal,socket,struct,time
parser=argparse.ArgumentParser();parser.add_argument('stream_id');parser.add_argument('archive_root');parser.add_argument('evidence_root');parser.add_argument('case');args=parser.parse_args()
assert re.fullmatch('[0-9a-f-]{36}',args.stream_id)
archive=pathlib.Path(args.archive_root);evidence=pathlib.Path(args.evidence_root)
end=time.monotonic()+4200;port=None
while time.monotonic()<end:
 for p in (archive/'tmp'/args.stream_id).glob('*.sdp'):
  text=p.read_text();match=re.search(r'^m=audio (\d+) RTP/AVP 96$',text,re.M)
  if match and 'L16/48000/2' in text:port=int(match.group(1));sdp=p;break
 if port:break
 time.sleep(.02)
assert port and 0<port<65536,'owned actual audio bridge SDP not found'
class Filter(ctypes.Structure):_fields_=[('code',ctypes.c_ushort),('jt',ctypes.c_ubyte),('jf',ctypes.c_ubyte),('k',ctypes.c_uint)]
class Program(ctypes.Structure):_fields_=[('length',ctypes.c_ushort),('filters',ctypes.POINTER(Filter))]
# Kernel filter: IPv4 UDP, exact destination port, RTP v2. Return a short
# header-only prefix. HTTP/credential traffic never enters the Python reader.
instructions=[(0x30,0,0,9),(0x15,0,7,17),(0xb1,0,0,0),(0x48,0,0,2),(0x15,0,4,port),(0x50,0,0,8),(0x54,0,0,0xc0),(0x15,0,1,0x80),(0x06,0,0,64),(0x06,0,0,0)]
filters=(Filter*len(instructions))(*(Filter(*i) for i in instructions));program=Program(len(instructions),filters)
sock=socket.socket(socket.AF_PACKET,socket.SOCK_DGRAM,socket.htons(0x0800));sock.bind(('lo',0));sock.setsockopt(socket.SOL_SOCKET,26,bytes(program));sock.settimeout(1)
seen=set();count=0;rejected_headers=0;unexpected_target_packets=0
stop=[False];signal.signal(signal.SIGTERM,lambda *_:stop.__setitem__(0,True));tap_packets=0;tap_drops=0
finish_case=os.environ.get('UI188_RTP_FINISH_CASE',args.case)
meta={'stream_id':args.stream_id,'actual_sdp':str(sdp),'udp_port':port,'interface':'lo','kernel_filter':'IPv4 UDP exact destination RTP v2, 64-byte header capture','payload_written':False,'time_namespace':os.readlink('/proc/self/ns/time'),'realtime_resolution':time.clock_getres(time.CLOCK_REALTIME),'monotonic_resolution':time.clock_getres(time.CLOCK_MONOTONIC)}
(evidence/('rtp-trace-'+args.case+'-metadata.json')).write_text(json.dumps(meta,indent=2)+'\n')
with (evidence/('rtp-trace-'+args.case+'.jsonl')).open('x') as output, (evidence/('rtp-rejected-'+args.case+'.jsonl')).open('x') as rejected:
 while time.monotonic()<end and not stop[0] and not (evidence/('chain-finished-'+finish_case)).exists():
  try:body=sock.recv(64)
  except socket.timeout:continue
  ihl=(body[0]&15)*4;udp=body[ihl:]
  if len(udp)<20 or body[9]!=17 or struct.unpack('!H',udp[2:4])[0]!=port:
   rejected_headers+=1;rejected.write(json.dumps({'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'reason':'non-target header despite kernel filter','ip_protocol':body[9],'destination_port':struct.unpack('!H',udp[2:4])[0] if len(udp)>=4 else None})+'\n');rejected.flush();continue
  length=struct.unpack('!H',udp[4:6])[0];rtp=udp[8:]
  if length!=3860 or rtp[0]&0xc0!=0x80 or rtp[1]&0x7f!=96:
   rejected_headers+=1;is_target=rtp[0]&0xc0==0x80 and rtp[1]&0x7f==96;unexpected_target_packets+=int(is_target)
   rejected.write(json.dumps({'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'reason':'unexpected target L16 packet' if is_target else 'non-L16 control/header observation','udp_length':length,'rtp_version':rtp[0]>>6,'payload_type':rtp[1]&0x7f})+'\n');rejected.flush();continue
  if len(rtp)<12:continue
  sequence,timestamp,ssrc=struct.unpack('!HII',rtp[2:12]);key=(sequence,timestamp,ssrc)
  if key in seen:continue
  seen.add(key);count+=1
  record={'index':count,'sequence':sequence,'rtp_timestamp':timestamp,'ssrc':ssrc,'udp_length':length,'captured_monotonic_ns':time.monotonic_ns(),'captured_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
  if count==1 or count%25==0:
   packets,drops=struct.unpack('II',sock.getsockopt(263,6,8));tap_packets+=packets;tap_drops+=drops
   record['header_observer_statistics']={'packets_cumulative':tap_packets,'drops_cumulative':tap_drops,'authority':'AF_PACKET PACKET_STATISTICS; separate from FFmpeg UDP receiver drops'}
   suffix=f':{port:04X}';receivers=[]
   for line in pathlib.Path('/proc/net/udp').read_text().splitlines():
    fields=line.split()
    if len(fields)>=13 and fields[1].endswith(suffix):receivers.append({'inode':fields[9],'queue_hex':fields[4],'kernel_drops':int(fields[-1])})
   record['actual_receiver_sockets']=receivers
  output.write(json.dumps(record)+'\n');output.flush()
packets,drops=struct.unpack('II',sock.getsockopt(263,6,8));tap_packets+=packets;tap_drops+=drops
(evidence/('rtp-observer-statistics-'+args.case+'.json')).write_text(json.dumps({'captured_unique_packets':count,'tap_packets':tap_packets,'tap_drops':tap_drops,'rejected_headers':rejected_headers,'unexpected_target_packets':unexpected_target_packets},indent=2)+'\n')
print('OWNED_RTP_HEADER_RECORDS='+str(count)+' REJECTED_NON_MATCHING_HEADERS='+str(rejected_headers)+' OBSERVER_DROPS='+str(tap_drops))
if unexpected_target_packets:raise AssertionError('unexpected target RTP packet count='+str(unexpected_target_packets))
if tap_drops:raise AssertionError('header observation lost packets; source continuity unverified')
