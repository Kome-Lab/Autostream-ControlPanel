"""Analyze actual same-run media. Every independent analysis runs before aggregate failure."""
import array, datetime, json, math, pathlib, subprocess, sys
media=pathlib.Path(sys.argv[1]); out=pathlib.Path(sys.argv[2]); seconds=float(sys.argv[3]) if len(sys.argv)>3 else 65
def probe(args):
 return json.loads(subprocess.run(['ffprobe','-v','error','-read_intervals',f'0%+{seconds}',*args,'-of','json',str(media)],capture_output=True,text=True,check=True,timeout=120).stdout)
packets=probe(['-select_streams','a:0','-show_packets','-show_entries','packet=pts_time,dts_time,duration_time'])['packets']
items=[{'pts':float(p['pts_time']),'duration':float(p.get('duration_time',0))} for p in packets if 'pts_time' in p]
jumps=[{'previous':a,'next':b,'delta':b['pts']-a['pts'],'uncovered':b['pts']-(a['pts']+a['duration'])} for a,b in zip(items,items[1:])]
packet={'count':len(items),'max_adjacent':max(j['delta'] for j in jumps),'max_uncovered':max(j['uncovered'] for j in jumps),'over50ms':[j for j in jumps if j['delta']>=.05],'pass':bool(jumps) and all(0<=j['delta']<.05 for j in jumps)}
pcm=subprocess.run(['ffmpeg','-hide_banner','-loglevel','error','-threads','2','-i',str(media),'-map','0:a:0','-t',str(seconds),'-ac','1','-ar','8000','-f','f32le','-'],capture_output=True,check=True,timeout=120).stdout
samples=array.array('f');samples.frombytes(pcm)
def power(window,f):
 c=2*math.cos(2*math.pi*f/8000);s1=s2=0
 for x in window:s0=x+c*s1-s2;s2=s1;s1=s0
 return s1*s1+s2*s2-c*s1*s2
windows=[]
for second in range(2,len(samples)//8000-1):
 w=samples[second*8000:(second+1)*8000];rms=math.sqrt(sum(x*x for x in w)/len(w));dominant=max([400,430,440,450,480],key=lambda f:power(w,f))
 windows.append({'decoded_second':second,'rms':rms,'tone':dominant,'pass':rms>.015 and dominant==440})
tone={'decoded_samples':len(samples),'windows':windows,'pass':bool(windows) and all(w['pass'] for w in windows),'does_not_fill_pts_gaps':True}
frames=probe(['-select_streams','v:0','-show_frames','-show_entries','frame=pts_time,width,height'])['frames']
value={'media':str(media),'original_bound_ms':50,'packet':packet,'tone':tone,'frames':{'count':len(frames),'dimensions':sorted(set((f['width'],f['height']) for f in frames))},'independent_tone_and_frame_analysis_executed':True,'aggregate':'PASS' if packet['pass'] and tone['pass'] and frames else 'FAIL'}
out.write_text(json.dumps(value,indent=2)+'\n');print(json.dumps({'aggregate':value['aggregate'],'audio_packets':packet['count'],'max_adjacent':packet['max_adjacent'],'max_uncovered':packet['max_uncovered'],'gap_count':len(packet['over50ms']),'tone_windows':len(windows),'tone_fail':sum(not w['pass'] for w in windows),'frames':len(frames)}));sys.exit(0 if value['aggregate']=='PASS' else 1)
