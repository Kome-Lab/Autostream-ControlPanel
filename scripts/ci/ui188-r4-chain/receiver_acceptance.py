"""Independently require both actual receivers, source RTP and late PCM bands."""
import bisect,json,math,os,pathlib,re,subprocess,sys
root=pathlib.Path(os.environ['UI188_EVIDENCE_DIRECTORY']);case=os.environ['UI188_PROBE_ATTEMPT'];failures=[];value={'case':case,'receivers':{},'failures':failures}
def failed(label,error):failures.append(label+': '+str(error))
state=json.loads((root/('s4-state-success-'+case+'.json')).read_text())
rows=[json.loads(line) for line in (root/('consumer-'+case+'.jsonl')).read_text().splitlines()]
hold=json.loads((root/('simultaneous-hold-'+case+'.json')).read_text());value['simultaneous_hold']=hold
if hold['seconds']<1000 or hold['host_stop']:failed('overlap','1000s two live source processes required')
for suffix,recording_key in [('', 'recording1'),('-s2','recording2')]:
 label='S1' if not suffix else 'S2';proof={};value['receivers'][label]=proof
 try:
  meta=json.loads((root/('rtp-trace-'+case+suffix+'-metadata.json')).read_text());port=f"{meta['udp_port']:04X}"
  sockets=[socket for row in rows for socket in row['udp'] if socket['local'].split(':')[1]==port]
  assert sockets,'actual owned SDP port was never observed'
  proof['SDP_port']=meta['udp_port'];proof['max_kernel_drops']=max(s['kernel_drops'] for s in sockets);proof['max_RecvQ_bytes']=max(int(s['queue_hex'].split(':')[1],16) for s in sockets)
  assert proof['max_kernel_drops']==0,'new receiver kernel drops must be zero'
  memory=[(row['monotonic'],item) for row in rows for item in row.get('socket_memory',[]) if item['udp_port']==meta['udp_port']]
  assert len(memory)>10 and all(item['receive_buffer_bytes']>0 for _,item in memory),'actual receiver capacity denominator'
  saturation_start=None;longest=0.0
  for at,item in memory:
   if item['rmem_alloc_bytes']>=.9*item['receive_buffer_bytes']:
    if saturation_start is None:saturation_start=at
    longest=max(longest,at-saturation_start)
   else:saturation_start=None
  proof['actual_receive_buffer']={'samples':len(memory),'capacity_bytes':sorted(set(item['receive_buffer_bytes'] for _,item in memory)),'max_allocation_ratio':max(item['rmem_alloc_bytes']/item['receive_buffer_bytes'] for _,item in memory),'longest_sampled_90percent_saturation_seconds':longest,'sampling_limit':'normally 5s, queue anomalies 1s; intervening transients unobserved'}
  assert longest<10,'sustained receiver saturation at actual 90percent capacity for 10s'
 except Exception as error:failed(label+' receiver',error)
 try:
  packets=[json.loads(line) for line in (root/('rtp-trace-'+case+suffix+'.jsonl')).read_text().splitlines()]
  discontinuities=[p['index'] for previous,p in zip(packets,packets[1:]) if (p['sequence']-previous['sequence'])%65536!=1 or (p['rtp_timestamp']-previous['rtp_timestamp'])%(2**32)!=960 or p['ssrc']!=previous['ssrc']]
  stats=json.loads((root/('rtp-observer-statistics-'+case+suffix+'.json')).read_text());duration=(packets[-1]['captured_monotonic_ns']-packets[0]['captured_monotonic_ns'])/1e9
  proof['RTP']={'count':len(packets),'capture_seconds':duration,'packet_rate':(len(packets)-1)/duration,'discontinuities':discontinuities,'observer':stats}
  assert len(packets)>=49000 and not discontinuities and stats['tap_drops']==0 and stats['rejected_headers']==0 and stats['unexpected_target_packets']==0
  assert 49.5<=proof['RTP']['packet_rate']<=50.5,'20ms source packet rate'
 except Exception as error:failed(label+' RTP',error)
 try:
  media=pathlib.Path(state[recording_key]['path']);target=root/('audio-analysis-'+case+suffix+'.json')
  archive='encoder-b-archives-' if not suffix else 'encoder-a-archives-'
  playlists=[entry for row in rows for entry in row['playlists'] if archive+case in entry['path']]
  assert len(playlists)>10,'same actual HLS playlist progression required'
  sequences=[max(map(int,re.findall(r'segment-(\d+)\.ts',entry['playlist']))) for entry in playlists]
  assert len(set(entry['mtime_ns'] for entry in playlists))>10 and sequences[-1]>sequences[0],'actual HLS segment progression'
  sink='tee-s1-progress-' if not suffix else 'tee-s2-progress-'
  sink_rows=[entry for row in rows for entry in row.get('external_tee_consumer_progress',[]) if sink+case in entry['path']]
  sink_times=[int(match.group(1))/1e6 for entry in sink_rows if (match:=re.search(r'out_time_us=(\d+)',entry['text']))]
  assert sink_times and max(sink_times)>=990,'same external test sink must consume actual long media'
  proof['actual_HLS_and_sink']={'playlist_samples':len(playlists),'first_last_segment_index':sequences[0],'final_last_segment_index':sequences[-1],'last_external_sink_seconds':max(sink_times)}
  if suffix:
   p=subprocess.run([sys.executable,'-B',str(pathlib.Path(__file__).with_name('audio_analysis.py')),str(media),str(target),'1200'],capture_output=True,text=True,timeout=150)
   (root/('audio-analysis-'+case+suffix+'.original.log')).write_text(p.stdout+p.stderr)
   proof['audio_original_exit']=p.returncode
   if p.returncode:failed(label+' original audio exit',p.returncode)
  audio=json.loads(target.read_text());proof['AAC']={k:audio['packet'][k] for k in ['count','max_adjacent','max_uncovered','pass']};proof['tone_failure_count']=sum(not window['pass'] for window in audio['tone']['windows'])
  if audio['aggregate']!='PASS' or audio['packet']['max_uncovered']>.05:failed(label+' original AAC/tone aggregate',audio['aggregate'])
  probe=json.loads(subprocess.check_output(['ffprobe','-v','error','-select_streams','a:0','-show_streams','-show_frames','-show_entries','stream=sample_rate:frame=best_effort_timestamp_time,nb_samples','-of','json',str(media)],timeout=120))
  rate=int(probe['streams'][0]['sample_rate']);starts=[];frames=[];duration=0.0
  for frame in probe['frames']:
   starts.append(duration);frames.append((duration,float(frame['best_effort_timestamp_time'])));duration+=int(frame['nb_samples'])/rate
  assert abs(duration-audio['tone']['decoded_samples']/8000)<.05,'actual decoder/PCM totals'
  mapped=[]
  for window in audio['tone']['windows']:
   center=window['decoded_second']+.5;index=bisect.bisect_right(starts,center)-1;start,pts=frames[index];mapped.append({**window,'actual_media_second':math.floor(pts+center-start),'actual_center_PTS':pts+center-start})
  proof['late_PCM_bands']={}
  for lo,hi in [(250,400),(580,700),(800,950)]:
   selected=[window for window in mapped if lo<=window['actual_media_second']<=hi];passed=len(selected)==hi-lo+1 and {w['actual_media_second'] for w in selected}==set(range(lo,hi+1)) and all(w['pass'] for w in selected)
   proof['late_PCM_bands'][f'{lo}-{hi}']={'count':len(selected),'pass':passed,'windows':selected}
   if not passed:failed(label+f' PCM {lo}-{hi}','all original tone/RMS windows required at actual decoder PTS')
 except Exception as error:failed(label+' audio',error)
value['aggregate']='FAIL' if failures else 'PASS';(root/('receiver-acceptance-'+case+'.json')).write_text(json.dumps(value,indent=2)+'\n');print(json.dumps({'aggregate':value['aggregate'],'failures':failures}));sys.exit(1 if failures else 0)
