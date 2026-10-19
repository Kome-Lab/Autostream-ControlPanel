"""Real-service chain: external Discord/Voice/output only are controlled.
Browser/media acceptance is performed by the companion runner, never inferred.
"""
import http.cookiejar, json, os, pathlib, subprocess, time, urllib.error, urllib.request
ROOT=pathlib.Path(os.environ.get('UI188_RUNTIME_ROOT','/r6')); EVIDENCE=pathlib.Path(os.environ.get('UI188_EVIDENCE_DIRECTORY','/evidence'))
ATTEMPT=os.environ.get('UI188_PROBE_ATTEMPT','1')
STREAM_NAME=os.environ.get('UI188_STREAM_NAME','UI188 R4 acceptance S1')
CREDENTIALS=json.loads(pathlib.Path(os.environ['UI188_TEST_CREDENTIALS']).read_text())
SCHEMA=os.environ.get('UI188_DB_SCHEMA','ui188_closure_acceptance')
assert SCHEMA == 'ui188_r9_b5'
PRIVATE=ROOT/('private-'+SCHEMA); PRIVATE.mkdir(exist_ok=True); PRIVATE.chmod(0o700)
processes=[]; events=[]; jar=http.cookiejar.CookieJar(); client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar)); csrf=''
def request(method,path,body=None,expected=200,base='http://127.0.0.1:18841',raw=None,content_type='application/json'):
 headers={'Accept':'application/json','Content-Type':content_type}
 if csrf: headers['X-CSRF-Token']=csrf
 req=urllib.request.Request(base+path,data=raw if raw is not None else None if body is None else json.dumps(body).encode(),method=method,headers=headers)
 try:
  with client.open(req,timeout=25) as res: status=res.status; data=res.read()
 except urllib.error.HTTPError as err: status=err.code;data=err.read()
 value=json.loads(data) if data else None
 events.append({'method':method,'path':path,'status':status,'code':value.get('code') if isinstance(value,dict) else None})
 (EVIDENCE/f'chain-probe-requests-{ATTEMPT}.json').write_text(json.dumps(events,indent=2)+'\n')
 if status!=expected: raise AssertionError(f'{method} {path}: {status}, code={value.get("code") if isinstance(value,dict) else "non-object"}')
 return value
def wait(check,seconds=35):
 end=time.monotonic()+seconds;last=None
 while time.monotonic()<end:
  try:
   value=check()
   if value:return value
  except (OSError,urllib.error.URLError,AssertionError) as err:last=type(err).__name__
  time.sleep(.5)
 raise AssertionError(f'bounded wait expired; last={last}')
def start(name,args,env):
 log=open(EVIDENCE/(name+'-'+ATTEMPT+'.log'),'xb');p=subprocess.Popen(args,cwd=ROOT,env={**os.environ,**env},stdout=log,stderr=subprocess.STDOUT,start_new_session=True);processes.append((name,p));return p
def node(kind,name,port):
 directory=PRIVATE/name;config=directory/'config.yml';credentials=directory/'credentials'
 if config.exists():
  nodes=request('GET','/nodes');assert any(n['service_id']==name and n['service_type']==kind for n in nodes), 'existing fixture node identity missing'
  return {'AUTOSTREAM_NODE_CONFIG':str(config),'CREDENTIALS_DIRECTORY':str(credentials),'CONTROL_PANEL_HEARTBEAT_INTERVAL_SEC':'1','CONTROL_PANEL_RUNTIME_CONFIG_REFRESH_INTERVAL':'1s'}
 value=request('POST','/nodes/registration-tokens',{'node_type':kind,'node_id':name,'name':name,'host':'127.0.0.1','port':port,'ssl_enabled':False,'allow_runtime_secrets':True},201)
 directory=PRIVATE/name;directory.mkdir(mode=0o700);credentials=directory/'credentials';credentials.mkdir(mode=0o700)
 config=directory/'config.yml';config.write_text(value['configuration_yaml']);config.chmod(0o600)
 if kind in ['worker','encoder_recorder']: assert 'stream_ingest:\n' in value['configuration_yaml'], 'Panel-provided ingest verification configuration required'
 listener=credentials/'node-listener.json';listener.write_text(json.dumps({'schema_version':2,'service_type':kind,'bind_address':f'127.0.0.1:{port+10000}','config_revision':1}));listener.chmod(0o600)
 return {'AUTOSTREAM_NODE_CONFIG':str(config),'CREDENTIALS_DIRECTORY':str(credentials),'CONTROL_PANEL_HEARTBEAT_INTERVAL_SEC':'1','CONTROL_PANEL_RUNTIME_CONFIG_REFRESH_INTERVAL':'1s'}
def run():
 start('r9-forwarding-observer',['python3','-B',str(pathlib.Path(__file__).with_name('artifact_transport.py'))],{})
 cp_env={'DATABASE_URL':'ui188_r9:'+CREDENTIALS['db_password']+'@tcp(db:3306)/'+SCHEMA+'?parseTime=true','AUTOSTREAM_SECRET_ENCRYPTION_KEY':CREDENTIALS['encryption_key'],'AUTOSTREAM_STREAM_INGEST_SIGNING_KEY':CREDENTIALS['ingest_key'],'AUTOSTREAM_SETUP_TOKEN':CREDENTIALS['setup_token'],'AUTOSTREAM_SERVICE_ALLOWED_HOSTS':'127.0.0.1','AUTOSTREAM_PUBLIC_URL':'http://127.0.0.1:18841','AUTOSTREAM_WEB_DIR':'/r6/cp/web/out','AUTOSTREAM_MEDIA_ASSET_DIR':'/r6/data/media-'+SCHEMA,'AUTOSTREAM_BIND_ADDR':'127.0.0.1:18842'}
 start('chain-cp',[os.environ.get('UI188_CP_BINARY','/r6/bin/cp-r4')],cp_env);wait(lambda:request('GET','/health'))
 (PRIVATE/'cp-ready').write_text(ATTEMPT)
 if request('GET','/setup/status')['setup_required']:
  request('POST','/setup/first-admin',{'setup_token':CREDENTIALS['setup_token'],'username':'ui188-r4','password':CREDENTIALS['admin_password']},201)
 global csrf
 csrf=request('POST','/auth/login',{'username':'ui188-r4','password':CREDENTIALS['admin_password']})['csrf_token']
 if os.environ.get('UI188_DIAGNOSTIC')=='1':
  streams=request('GET','/streams')
  (EVIDENCE/f'chain-retained-stream-{ATTEMPT}.json').write_text(json.dumps(streams,indent=2)+'\n')
  audit=request('GET','/audit-logs?action=streams.start')
  (EVIDENCE/f'chain-start-audit-{ATTEMPT}.json').write_text(json.dumps(audit,indent=2)+'\n')
  profiles=request('GET','/profiles/encoder') or []
  details=[request('GET','/profiles/encoder/'+p['id']) for p in profiles]
  (EVIDENCE/f'chain-readonly-encoder-profiles-{ATTEMPT}.json').write_text(json.dumps(details,indent=2)+'\n')
  print(json.dumps({'streams':[{k:s.get(k) for k in ['id','status','current_job_generation']} for s in streams], 'audit':audit}),flush=True)
  return
 (PRIVATE/'browser-login.json').write_text(json.dumps({'username':'ui188-r4','password':CREDENTIALS['admin_password']}));(PRIVATE/'browser-login.json').chmod(0o600)
 if os.environ.get('UI188_BROWSER_ONLY')=='1':
  print('R3_REAL_CP_BROWSER_READY',flush=True)
  wait(lambda:(EVIDENCE/os.environ.get('UI188_HOLD_MARKER','browser-environment-finished')).exists(),1200)
  return
 request('POST','/streams',{'name':'UI188 missing roles','visual_settings':{}},400)
 existing_streams=request('GET','/streams')
 assert all(s['name']!='UI188 missing roles' for s in existing_streams), 'rejected create persisted a stream'
 existing_streams=[s for s in existing_streams if s['name']==STREAM_NAME]
 if not (ROOT/'data/tone.opus').exists():
  subprocess.run(['ffmpeg','-hide_banner','-loglevel','error','-f','lavfi','-i','sine=frequency=440:sample_rate=48000','-t','2','-c:a','libopus','-frame_duration','20','/r6/data/tone.opus'],check=True)
 for suffix,offset in [('a',0),('b',3)]:
  for binary,kind,port in [('worker','worker',18081+offset),('encoder','encoder_recorder',18082+offset),('bot-test','discord_bot',18083+offset)]:
   name=binary.removesuffix('-test')+'-'+suffix;env=node(kind,name,port)
   env.update({'AUTOSTREAM_ARCHIVE_DIR':'/r6/data/'+name+'-archives-'+ATTEMPT,'AUTOSTREAM_INPUT_ALLOWED_HOSTS':'127.0.0.1','AUTOSTREAM_OUTPUT_RELAY_MODE':'direct','CONTROL_PANEL_ARTIFACT_REPORT_TIMEOUT_SEC':'30','ENCODER_ARTIFACT_REPORT_TIMEOUT_SEC':'30'})
   if binary=='bot-test':env.update({'AUTOSTREAM_UI188_R4_CHILD':'bot','AUTOSTREAM_UI188_R4_OPUS':'/r6/data/tone.opus','AUTOSTREAM_UI188_R4_EXTERNAL_ADDR':f'127.0.0.1:{port+1000}','UI188_AUDIO_JOURNAL_DIRECTORY':str(PRIVATE)})
   if name=='worker-b':continue # Separate owned container shares only the test network namespace.
   executable=os.environ.get('UI188_BOT_BINARY','/r6/bin/bot-test-r4') if binary=='bot-test' else os.environ.get('UI188_ENCODER_BINARY','/r6/bin/encoder') if binary=='encoder' else '/r6/bin/'+binary
   start('chain-'+name,[executable]+(['-test.run','^TestUI188R4NodeChild$','-test.timeout','70m','-test.v'] if binary=='bot-test' else []),env)
 wait(lambda:all(row['status']=='online' for row in request('GET','/service-health')))
 configs={}
 saved_configs=request('GET','/discord/configs')
 for suffix in ['a','b']:
  matches=[c for c in saved_configs if c['name']=='UI188 Discord '+suffix and c['service_id']=='bot-'+suffix]
  assert len(matches)<=1
  configs[suffix]=(matches[0] if matches else request('POST','/discord/configs',{'name':'UI188 Discord '+suffix,'service_id':'bot-'+suffix,'guild_id':'1001','voice_channel_id':'1003','text_channel_id':'1002','bot_token':'UI188-ONLY-EXTERNAL-TEST-TOKEN-'+suffix,'caption_enabled':False,'audio_forward_enabled':True},201))['id']
 saved_profiles=request('GET','/profiles/encoder') or [];matches=[p for p in saved_profiles if p['name'] in ['UI188 R4 acceptance 480p']];assert len(matches)<=1
 profile=(matches[0] if matches else request('POST','/profiles/encoder',{'name':'UI188 R4 acceptance 480p','config':{'width':854,'height':480,'fps':15,'video_codec':'h264','video_bitrate_kbps':800,'audio_bitrate_kbps':96}},201))['id']
 same=request('PUT','/profiles/encoder/'+profile,{'name':'UI188 R4 acceptance 480p','config':{'width':854,'height':480,'fps':15,'video_codec':'h264','video_bitrate_kbps':800,'audio_bitrate_kbps':96}})
 assert same==request('GET','/profiles/encoder/'+profile)
 (EVIDENCE/f'chain-s0-{ATTEMPT}.json').write_text(json.dumps({'profile_id':profile,'normal_put_status':200,'db_read_matches':True,'profile':same},indent=2)+'\n')
 from s0_negative import verify_s0
 verify_s0(request,client,csrf,CREDENTIALS,SCHEMA,profile,same,configs,EVIDENCE,ATTEMPT)
 subprocess.run(['python3','-B','/evidence/resource_snapshot.py','before-FFmpeg'],check=True)
 key=PRIVATE/f'sink-{ATTEMPT}.key';cert=PRIVATE/f'sink-{ATTEMPT}.crt'
 subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(key),'-out',str(cert),'-subj','/CN=UI188-R3-local-sink','-days','1'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True);key.chmod(0o600)
 start('chain-external-rtmp',['ffmpeg','-hide_banner','-loglevel','warning','-listen','1','-i','rtmp://127.0.0.1:1936/live/r6','-c','copy','-progress',str(PRIVATE/('tee-s1-progress-'+ATTEMPT+'.txt')),'-f','null','-'],{})
 start('chain-external-rtmps',['python3','-B',str(pathlib.Path(__file__).with_name('external_tls_sink.py')),str(cert),str(key)],{})
 outputs=request('GET','/youtube/outputs') or [];matches=[o for o in outputs if o['name']=='UI188 local external sink'];assert len(matches)<=1
 output=(matches[0] if matches else request('POST','/youtube/outputs',{'name':'UI188 local external sink','mode':'stream_key','rtmp_url':'rtmps://127.0.0.1:1935/live','stream_key':'r2'},201))['id']
 # Syntactic notification link only; the internal-only network cannot contact YouTube.
 request('PUT','/youtube/outputs/'+output,{'name':'UI188 local external sink','mode':'stream_key','rtmp_url':'rtmps://127.0.0.1:1935/live','watch_url':'https://www.youtube.com/watch?v=UI188R2test0'})
 from visual_fixture import prepare_visual
 visual=prepare_visual(request,ROOT,EVIDENCE)
 payload={'name':STREAM_NAME,'encoder_service_id':'encoder-a','worker_service_id':'worker-a','discord_config_id':configs['a'],'encoder_profile_id':profile,'youtube_output_id':output,'auto_start_trigger':'discord_voice_join','overlay_profile_id':visual['overlay_profile_id'],'visual_settings':{**visual['visual_settings'],'expected_revision':0,'discord_target':{'mode':'manual','guild_id':'1001','voice_channel_id':'1003','text_channel_id':'1002'}}}
 if existing_streams:
  assert len(existing_streams)==1 and existing_streams[0]['name']==STREAM_NAME and existing_streams[0]['status']=='created', 'resume only the retained, unstarted probe stream'
  sid=existing_streams[0]['id']
  registered=request('GET','/service-health')
  for service in registered:
   if service['service_id'] in ['bot-a','bot-b'] and service.get('current_stream_id')==sid:
    request('DELETE','/services/'+service['service_id']+'/assignment')
  initial=dict(payload);initial.pop('visual_settings');request('PUT','/streams/'+sid+'/settings',initial)
 else:
  baseline=request('GET','/streams');assert not baseline, 'fresh B4 schema only'
  negative=[]
  for field,code in [('encoder_service_id','encoder_service_required'),('worker_service_id','worker_service_required')]:
   invalid=dict(payload);invalid.pop(field);rejected=request('POST','/streams',invalid,400)
   assert rejected.get('code')==code and request('GET','/streams')==baseline
   negative.append({'missing_field':field,'status':400,'code':code,'stream_list_unchanged':True})
  db=subprocess.run(['mariadb','--host=db','--user=ui188_r9','--batch','--skip-column-names',SCHEMA,'--execute','SELECT COUNT(*) FROM streams; SELECT COUNT(*) FROM stream_service_assignments;'],env={**os.environ,'MYSQL_PWD':CREDENTIALS['db_password']},capture_output=True)
  assert db.returncode==0 and db.stdout.decode().split()==['0','0']
  (EVIDENCE/('create-required-service-negative-'+ATTEMPT+'.json')).write_text(json.dumps({'cases':negative,'DB_streams_and_assignment_counts':[0,0],'DB_SELECT_original_exit':db.returncode,'aggregate':'PASS'},indent=2)+'\n')
  stream=request('POST','/streams',payload,201);sid=stream['id']
 observed=request('GET','/streams/'+sid);assert observed['assigned_encoder_id']=='encoder-a' and observed['assigned_worker_id']=='worker-a'
 request('POST','/services/bot-a/assign',{'stream_id':sid,'assignment_role':'primary'})
 payload.update({'encoder_service_id':'encoder-b','worker_service_id':'worker-b','discord_config_id':configs['b']});payload.pop('visual_settings')
 request('DELETE','/services/bot-a/assignment')
 request('PUT','/streams/'+sid+'/settings',payload)
 request('POST','/services/bot-b/assign',{'stream_id':sid,'assignment_role':'primary'})
 from stale_authority import verify_stale
 verify_stale(request,wait,sid,EVIDENCE,ATTEMPT)
 runtime=request('GET','/service-health/bot-b/runtime-config');assignments=[{k:a[k] for k in ['stream_id','service_id','service_type','assigned_at','is_primary'] if k in a} for a in runtime['assignments']];authority=next(a for a in assignments if a['stream_id']==sid and a['service_id']=='bot-b');assert authority.get('assigned_at'),'actual assignment authority missing';(EVIDENCE/f'chain-bot-b-runtime-sanitized-{ATTEMPT}.json').write_text(json.dumps({'assignments':assignments,'selected_assignment':authority},indent=2))
 readiness=wait(lambda:(r if (r:=request('POST','/streams/'+sid+'/start-readiness',{}))['ready'] else False),35)
 (EVIDENCE/f'chain-start-readiness-{ATTEMPT}.json').write_text(json.dumps(readiness,indent=2)+'\n')
 assert readiness['ready'], 'real CP start-readiness rejected fixture; inspect recorded issues'
 archive_root=str(ROOT/('data/encoder-b-archives-'+ATTEMPT))
 start('chain-rtp-b',['python3','-B',str(pathlib.Path(__file__).with_name('rtp_trace.py')),sid,archive_root,str(EVIDENCE),ATTEMPT],{})
 wait(lambda:request('GET','/observations',base='http://127.0.0.1:19086')['active_gateway_connections']==1)
 request('POST','/input',{'user_id':'1004','channel_id':'1003','is_bot':False,'member_known':False,'existing':False},202,base='http://127.0.0.1:19086')
 for _ in range(4):
  assert request('GET','/streams/'+sid)['status'] in ['created','ready'], 'unknown identity must never start'
  assert request('GET','/observations',base='http://127.0.0.1:19086')['voice_joins']==0
  time.sleep(.5)
 request('POST','/input',{'user_id':'1004','channel_id':'1003','is_bot':False,'member_only':True,'existing':False},202,base='http://127.0.0.1:19086')
 observed=wait(lambda:(s if (s:=request('GET','/streams/'+sid))['status']=='live' else False),60)
 current_starter=request('GET','/r9/stale-authority',base='http://127.0.0.1:19086');assert current_starter['starter_calls']==1
 (EVIDENCE/f'r9-current-human-start-once-{ATTEMPT}.json').write_text(json.dumps(current_starter,indent=2)+'\n')
 (EVIDENCE/f'acceptance-live-{ATTEMPT}.json').write_text(json.dumps({'stream_id':sid,'status':observed['status'],'configs':configs,'profile':profile,'visual':visual,'archive_root':archive_root},indent=2))
 print('REAL_CHAIN_REACHED_LIVE '+sid,flush=True)
 # Keep source-owned services for the separately bounded real browser/media phase.
 wait(lambda:(EVIDENCE/f'chain-finished-{ATTEMPT}').exists(),3600)
try:run()
finally:
 for name,p in reversed(processes):
  if p.poll() is None:
   p.terminate()
   try:p.wait(timeout=20)
   except subprocess.TimeoutExpired:p.kill();p.wait()
 (EVIDENCE/f'chain-probe-process-exits-{ATTEMPT}.json').write_text(json.dumps([{'name':name,'exit':p.returncode} for name,p in processes],indent=2))
