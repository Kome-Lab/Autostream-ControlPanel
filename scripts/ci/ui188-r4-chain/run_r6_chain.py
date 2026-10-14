"""One fresh owned R6 run. Original exits survive independently of later steps."""
import json,os,pathlib,subprocess,sys,time
root=pathlib.Path(os.environ['UI188_EVIDENCE_DIRECTORY']).resolve();scripts=pathlib.Path(__file__).resolve().parent
assert root.drive.upper()=='C:' and scripts.drive.upper()=='C:'
docker='C:/Program Files/Docker/Docker/resources/bin/docker.exe';node='C:/nvm4w/nodejs/node.exe';case=os.environ.get('UI188_PROBE_ATTEMPT','candidate-1');results=[]
assert case in ['candidate-1','candidate-2']
schema='ui188_r7_candidate' if case=='candidate-1' else 'ui188_r7_candidate2'
base=['exec','-e','UI188_EVIDENCE_DIRECTORY=/evidence','-e','UI188_TEST_CREDENTIALS=/evidence/runtime-test-credentials.json','-e','UI188_PROBE_ATTEMPT='+case,'-e','UI188_DB_SCHEMA='+schema]
def save():
 (root/('chain-original-exits-'+case+'.json')).write_text(json.dumps(results,indent=2)+'\n',encoding='utf8')
def start(label,args):
 f=(root/(label+'.log')).open('xb');return subprocess.Popen(args,stdout=f,stderr=subprocess.STDOUT)
def waitfile(name,seconds=110):
 end=time.monotonic()+seconds
 while not (root/name).exists():
  assert runtime.poll() is None,'runtime exited before '+name
  assert time.monotonic()<end,'bounded missing '+name;time.sleep(.25)
def browser(kind):
 cache=root/'chrome-temp';cache.mkdir(exist_ok=True)
 env={**os.environ,'TEMP':str(cache),'TMP':str(cache),'UI188_EVIDENCE_DIRECTORY':str(root),'UI188_PROBE_ATTEMPT':case,'UI188_TEST_CREDENTIALS':str(root/'runtime-test-credentials.json'),'UI188_BROWSER_ATTEMPT':'1','UI188_S5_ATTEMPT':'1','UI188_LOST_STOP_RESPONSE':'1','UI188_LONG_AUDIO_SECONDS':'410','AUTOSTREAM_BROWSER_PATH':'C:/Program Files/Google/Chrome/Application/chrome.exe'}
 with (root/(kind+'-'+case+'-1.log')).open('xb') as f:p=subprocess.run([node,'--experimental-strip-types',str(scripts/(kind+'.mts'))],cwd=scripts.parents[2],env=env,stdout=f,stderr=subprocess.STDOUT)
 results.append({'step':kind,'exit':p.returncode});save();print(kind+' ORIGINAL_EXIT='+str(p.returncode),flush=True)
def owned(name):
 v=json.loads(subprocess.check_output([docker,'inspect',name]))[0];assert v['Config']['Labels']['autostream.scope']=='UI188-R7' and v['State']['Running'];return v
owned('autostream-ui188-r7-native')
p=subprocess.run([docker,'run','-d','--name','autostream-ui188-r7-worker-b-'+case,'--label','autostream.scope=UI188-R7','--network','container:autostream-ui188-r7-native','--cpus','1','--memory','1g','--pids-limit','128','-v','autostream-ui188-r7-toolcache:/r6:ro','-v',str(root)+':/evidence:ro','-e','UI188_DB_SCHEMA='+schema,'autostream-ui188-r2-tools:20261012','python3','-B','/evidence/worker-b-bootstrap.py'],capture_output=True);assert p.returncode==0
sampler=start('sampler-'+case,[docker,*base,'autostream-ui188-r7-native','python3','-B','/evidence/cp/scripts/ci/ui188-r4-chain/receiver_observation.py'])
runtime=start('runtime-'+case,[docker,*base,'-e','UI188_BOT_BINARY=/r6/bin/bot-test-r6-final','-e','UI188_CP_BINARY=/r6/bin/cp-r6-final','-e','UI188_ENCODER_BINARY=/r6/bin/encoder-r6-final','-e','UI188_STREAM_NAME=UI188 R6 final S1','autostream-ui188-r7-native','python3','-B','/evidence/cp/scripts/ci/ui188-r4-chain/runtime.py'])
s2=None
try:
 waitfile('acceptance-live-'+case+'.json')
 media_hold_deadline=time.monotonic()+1000
 s2=start('s2-'+case,[docker,*base,'-e','UI188_S2_ATTEMPT=1','autostream-ui188-r7-native','python3','-B','/evidence/cp/scripts/ci/ui188-r4-chain/s2.py']);waitfile('s2-live-'+case+'.json');results.append({'step':'S2 reached live','exit':0});save()
 browser('browser');browser('s5')
 while time.monotonic()<media_hold_deadline:
  assert runtime.poll() is None and s2.poll() is None,'same-run sources exited during the required long two-stream interval'
  time.sleep(1)
 results.append({'step':'same two-stream source run at least 1000s after first live','exit':0});save()
 live=json.loads((root/('acceptance-live-'+case+'.json')).read_text());source=live['archive_root']+'/tmp/'+live['stream_id']+'/preview';capture='/r6/hls-capture-'+case
 code="import pathlib,shutil,json,hashlib;p=pathlib.Path("+repr(source)+");d=pathlib.Path("+repr(capture+'/'+live['stream_id'])+");d.parent.mkdir(exist_ok=True);shutil.copytree(p,d);proof={'stream_id':"+repr(live['stream_id'])+",'source':str(p),'copy':str(d),'files':[{'name':f.name,'sha256':hashlib.sha256(f.read_bytes()).hexdigest()} for f in d.iterdir() if f.is_file()]};pathlib.Path('/evidence/hls-capture-'+"+repr(case)+"+'.json').write_text(json.dumps(proof,indent=2)+'\\n')"
 p=subprocess.run([docker,*base,'autostream-ui188-r7-native','python3','-B','-c',code],capture_output=True);(root/('hls-capture-'+case+'.original.log')).write_bytes(p.stdout+p.stderr);results.append({'step':'same-run HLS snapshot before stop','exit':p.returncode});save()
 state=start('s4-state-'+case,[docker,*base,'-e','UI188_S4_ATTEMPT=1','autostream-ui188-r7-native','python3','-B','/evidence/cp/scripts/ci/ui188-r4-chain/s4_state.py']);browser('s4')
 try:code=state.wait(timeout=210)
 except subprocess.TimeoutExpired:code=124
 results.append({'step':'s4_state','exit':code});save()
 # This step always runs, including after the original browser/audio failure.
 media=start('media-'+case,[docker,*base,'-e','UI188_BROWSER_ATTEMPT=1','-e','UI188_HLS_CAPTURE_ROOT='+capture,'autostream-ui188-r7-native','python3','-B','/evidence/cp/scripts/ci/ui188-r4-chain/media.py'])
 try:code=media.wait(timeout=600)
 except subprocess.TimeoutExpired:code=124
 results.append({'step':'independent media aggregate','exit':code});save()
except Exception as e:results.append({'step':'driver','exit':1,'error':str(e)});save()
finally:
 (root/('s2-finished-'+case)).touch();(root/('chain-finished-'+case)).touch()
 for label,p in [('S2 processes',s2),('runtime processes',runtime),('receiver sampler',sampler)]:
  if p:
   try:code=p.wait(timeout=40)
   except subprocess.TimeoutExpired:code=124
   results.append({'step':label,'exit':code});save()
 p=subprocess.run([docker,'cp','autostream-ui188-r7-native:/r6/diagnostic/consumer-'+case+'.jsonl',str(root/('consumer-'+case+'.jsonl'))],capture_output=True);results.append({'step':'receiver capture','exit':p.returncode});save()
 for suffix in ['a','b']:
  p=subprocess.run([docker,'cp','autostream-ui188-r7-native:/r6/private-'+schema+'/audio-bot-'+suffix+'.jsonl',str(root/('audio-'+case+'-bot-'+suffix+'.jsonl'))],capture_output=True);results.append({'step':'actual BOT audio journal '+suffix,'exit':p.returncode});save()
 worker=owned('autostream-ui188-r7-worker-b-'+case);p=subprocess.run([docker,'stop','--time','20',worker['Id']],capture_output=True);results.append({'step':'owned WorkerB process stop','exit':p.returncode});save()
print(json.dumps(results),flush=True);sys.exit(1 if any(r['exit']!=0 for r in results) else 0)
