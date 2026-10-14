"""Same-run audio and every-frame analyses finish before aggregate failure."""
import bisect,hashlib,json,math,os,pathlib,shutil,subprocess,sys
root=pathlib.Path(os.environ['UI188_EVIDENCE_DIRECTORY']);case=os.environ['UI188_PROBE_ATTEMPT'];attempt=case+'-'+os.environ['UI188_BROWSER_ATTEMPT']
live=json.loads((root/('acceptance-live-'+case+'.json')).read_text());sid=live['stream_id'];archive=pathlib.Path(live['archive_root']);failures=[]
value={'stream_id':sid,'run_id':case,'analyses':{},'failures':failures}
def fail(label,e):failures.append(label+': '+str(e))
def pts(media):
 data=subprocess.check_output(['ffprobe','-v','error','-select_streams','v:0','-show_frames','-show_entries','frame=best_effort_timestamp_time','-of','json',str(media)],timeout=120)
 return [float(f['best_effort_timestamp_time']) for f in json.loads(data)['frames']]
def first_picture(media):
 return subprocess.check_output(['ffmpeg','-hide_banner','-loglevel','error','-threads','2','-i',str(media),'-frames:v','1','-f','rawvideo','-pix_fmt','rgb24','-'],timeout=60)
recordings=[p for p in archive.rglob('*.mkv') if sid in str(p)];recording=recordings[0] if len(recordings)==1 else None;times=[];frames=[];browser=None;audio=None
try:
 browser=json.loads((root/('browser-chain-cover-success-'+attempt+'.json')).read_text());assert browser['sid']==sid;value['job_generation']=browser['phases'][0]['state']['job_generation']
except Exception as e:fail('full browser',e)
if recording is None:fail('recording',f'expected one actual same-run recording, got {len(recordings)}')
else:
 value.update(recording=str(recording),recording_sha256=hashlib.sha256(recording.read_bytes()).hexdigest())
 try:times=pts(recording);assert times;value['analyses']['video_pts']={'count':len(times),'first':times[0],'last':times[-1]}
 except Exception as e:fail('video PTS',e)
 try:
  target=root/('audio-analysis-'+case+'.json');bound=max(65,times[-1]+1) if times else 900
  p=subprocess.run([sys.executable,'-B',str(pathlib.Path(__file__).with_name('audio_analysis.py')),str(recording),str(target),str(bound)],capture_output=True,text=True,timeout=350)
  (root/('audio-analysis-'+case+'.original.log')).write_text(p.stdout+p.stderr);value['audio_original_exit']=p.returncode;audio=json.loads(target.read_text());value['audio']=audio
  if p.returncode or audio['aggregate']!='PASS':fail('original 50ms audio/tone gate',audio['aggregate'])
  if audio['packet']['count']<=600 or len(audio['tone']['windows'])<27:fail('original denominator','missing')
  band=[w for w in audio['tone']['windows'] if 250<=w['decoded_second']<=400];value['analyses']['tone250_400']={'count':len(band),'windows':band}
  if len(band)!=151 or not all(w['pass'] for w in band):fail('250-400s','all 151 one-second PCM/tone windows required')
  # Map the unfilled decoded PCM back to the decoder's actual frame PTS.
  # This is metadata only: no PCM padding, media substitution or PTS rewrite.
  probe=json.loads(subprocess.check_output(['ffprobe','-v','error','-select_streams','a:0','-show_streams','-show_frames','-show_entries','stream=sample_rate:frame=best_effort_timestamp_time,nb_samples','-of','json',str(recording)],timeout=120))
  rate=int(probe['streams'][0]['sample_rate']);starts=[];metadata=[];duration=0.0
  for frame in probe['frames']:
   starts.append(duration);metadata.append({'decoded_start':duration,'PTS':float(frame['best_effort_timestamp_time']),'duration':int(frame['nb_samples'])/rate});duration+=int(frame['nb_samples'])/rate
  assert abs(duration-audio['tone']['decoded_samples']/8000)<.05,'decoder source/PCM sample totals must align'
  mapped=[]
  for window in audio['tone']['windows']:
   center=window['decoded_second']+.5;index=bisect.bisect_right(starts,center)-1;frame=metadata[index];media_center=frame['PTS']+center-frame['decoded_start']
   mapped.append({**window,'observed_media_second':math.floor(media_center),'observed_center_PTS':media_center,'decoder_frame_PTS':frame['PTS']})
  value['analyses']['PCM_PTS_authority']={'decoder_frames':len(metadata),'source_sample_duration':duration,'decoded_PCM_duration':audio['tone']['decoded_samples']/8000,'mapping':'actual decoder frame PTS plus decoded sample offset; gaps not filled or retimed'}
  for label,lo,hi in [('250_400',250,400),('580_700',580,700),('800_950',800,950)]:
   selected=[w for w in mapped if lo<=w['observed_media_second']<=hi];expected=set(range(lo,hi+1));observed=[w['observed_media_second'] for w in selected]
   passed=len(selected)==hi-lo+1 and set(observed)==expected and all(w['pass'] for w in selected)
   value['analyses']['tone_PTS_'+label]={'expected_seconds':[lo,hi],'count':len(selected),'pass':passed,'windows':selected}
   if not passed:fail('PTS-mapped tone '+label,'all independently declared media-time windows required')
 except Exception as e:fail('independent audio',e)
 # This analysis is independent of audio and the browser success path.
 try:
  graph='[0:v]format=rgb24,split=2[c][w];[c]crop=1:1:420:240[c1];[w]crop=1:1:30:25[w1];[c1][w1]hstack=inputs=2[pixels]'
  pixels=subprocess.check_output(['ffmpeg','-hide_banner','-loglevel','error','-threads','2','-filter_complex_threads','1','-i',str(recording),'-filter_complex',graph,'-map','[pixels]','-fps_mode','passthrough','-f','rawvideo','-pix_fmt','rgb24','-'],timeout=180)
  assert len(pixels)%6==0 and len(pixels)//6==len(times)
  colors=[[255,0,0],[0,0,255],[0,128,0]]
  for i,t in enumerate(times):
   center=list(pixels[i*6:i*6+3]);wm=list(pixels[i*6+3:i*6+6]);frames.append({'index':i,'pts':t,'center':center,'watermark':wm,'cover_color':any(all(abs(c-v)<40 for c,v in zip(color,center)) for color in colors),'watermark_pass':all(v>220 for v in wm)})
  value['analyses']['independent_all_frames']={'count':len(frames),'scope':'every recording frame at two specified spatial points; target acceptance is separate','frames':frames}
 except Exception as e:fail('independent every-frame decode',e)
 try:
  preview=archive/'tmp'/sid/'preview'
  if not (preview/'index.m3u8').is_file():preview=pathlib.Path(os.environ['UI188_HLS_CAPTURE_ROOT'])/sid
  manifest=preview/'index.m3u8';files=[manifest]+[preview/line for line in manifest.read_text().splitlines() if line and not line.startswith('#')]
  assert len(files)>=3 and all(f.is_file() and f.parent==preview for f in files)
  destination=root/('media-'+case);destination.mkdir(exist_ok=False);hashes=[]
  for f in files:shutil.copy2(f,destination/f.name);hashes.append({'path':f.name,'bytes':f.stat().st_size,'sha256':hashlib.sha256(f.read_bytes()).hexdigest()})
  value['hls_graph']=hashes;offset=pts(files[1])[0]-times[0]
  assert hashlib.sha256(first_picture(files[1])).digest()==hashlib.sha256(first_picture(recording)).digest()
  value['analyses']['hls_recording_identity']={'pts_offset':offset,'first_picture_identity_verified':True}
  assert browser is not None and frames
  stop=json.loads((root/('s4-stop-success-'+case+'-1.json')).read_text());assert stop['outcome']['stop_confirmed']
  start,end=times[0],browser['protectedIntervalHLS']['end']-offset;off=browser['offIntervalHLS'];off_start,off_end=off['start']-offset,times[-1]
  assert 0<=start<end<off_start<off_end<=times[-1] and end-start>=410 and times[-1]>=990
  on=[f for f in frames if start<=f['pts']<=end];off_frames=[f for f in frames if off_start<=f['pts']<=off_end]
  bad=[f for f in on if not f['cover_color'] or not f['watermark_pass']];off_bad=[f for f in off_frames if f['cover_color'] or not f['watermark_pass']]
  value['analyses']['target_frames']={'status':'PASS' if len(on)>=30 and len(off_frames)>=30 and not bad and not off_bad else 'FAIL','ON':{'start':start,'end':end,'count':len(on),'failures':bad},'OFF':{'start':off_start,'end':off_end,'end_authority':'unchanged explicit OFF through the confirmed regular stop, final recording timestamp','count':len(off_frames),'failures':off_bad},'scope':'every decoded frame in independently declared ON/OFF intervals; center and Watermark points'}
  if len(on)<30 or len(off_frames)<30 or bad or off_bad:fail('target frame gate','ON/OFF or Watermark failure')
 except Exception as e:value['analyses'].setdefault('target_frames',{'status':'NOT_RUN','reason':str(e)});fail('HLS/target boundaries',e)
value['aggregate']='FAIL' if failures else 'PASS';(root/('media-verification-'+case+'.json')).write_text(json.dumps(value,indent=2)+'\n');print(json.dumps({'aggregate':value['aggregate'],'audio_original_exit':value.get('audio_original_exit'),'failures':failures}));sys.exit(1 if failures else 0)
