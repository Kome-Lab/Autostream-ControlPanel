"""Existing Discord presence through real Gateway SDK/controller and CP dispatch."""
import json,os,pathlib,subprocess,time
from api_client import API
case=os.environ['UI188_PROBE_ATTEMPT'];trial=os.environ.get('UI188_S2_ATTEMPT','1')
api=API('s2-'+case+'-'+trial);live=json.loads((api.evidence/('acceptance-live-'+case+'.json')).read_text());s1=live['stream_id']
observations=lambda suffix:api.call('GET','/observations',base='http://127.0.0.1:'+('19083' if suffix=='a' else '19086'))
fixture_name='UI188 R4 S2 existing '+case+' attempt '+trial
saved=[s for s in api.call('GET','/streams') if s['name']==fixture_name]
assert len(saved)<=1
if saved:
 assert saved[0]['status'] in ['created','ready'], 'resume only the preserved unstarted fixture'
 row=next(s for s in api.call('GET','/service-health') if s['service_id']=='bot-a')
 if row.get('current_stream_id')==saved[0]['id']:api.call('DELETE','/services/bot-a/assignment')
 api.wait(lambda:observations('a')['active_gateway_connections']==0)
assert observations('a')['active_gateway_connections']==0,'unselected BOT must remain dormant'
before_b=observations('b');assert before_b['stream_id']==s1 and before_b['active_gateway_connections']==1
before_a=observations('a');assert not before_a['stream_id']
api.call('POST','/input',{'user_id':'1006','channel_id':'1005','is_bot':False,'existing':True},202,base='http://127.0.0.1:19083')
configs=[c for c in api.call('GET','/discord/configs') if c['name']=='UI188 R4 existing presence '+case];assert len(configs)<=1
config=configs[0] if configs else api.call('POST','/discord/configs',{'name':'UI188 R4 existing presence '+case,'service_id':'bot-a','guild_id':'1001','voice_channel_id':'1005','text_channel_id':'1002','bot_token':'UI188-ONLY-EXTERNAL-TEST-TOKEN-a','caption_enabled':False,'audio_forward_enabled':True},201)
private=pathlib.Path('/r6/private-'+os.environ.get('UI188_DB_SCHEMA','ui188_r7_acceptance'));cert=next(private.glob('sink-'+os.environ['UI188_PROBE_ATTEMPT']+'.crt'));key=cert.with_suffix('.key');processes=[]
def start(name,args,env={}):
 log=(api.evidence/(name+'-'+case+'-'+trial+'.log')).open('xb');p=subprocess.Popen(args,env={**os.environ,**env},stdout=log,stderr=subprocess.STDOUT);processes.append(p)
try:
 start('s2-external-rtmp',['ffmpeg','-hide_banner','-loglevel','warning','-listen','1','-i','rtmp://127.0.0.1:1938/live/r3s2','-c','copy','-progress',str(private/('tee-s2-progress-'+case+'.txt')),'-f','null','-'])
 start('s2-external-rtmps',['python3','-B',str(pathlib.Path(__file__).with_name('external_tls_sink.py')),str(cert),str(key)],{'UI188_EXTERNAL_TLS_PORT':'1937','UI188_EXTERNAL_RTMP_PORT':'1938'})
 outputs=[o for o in api.call('GET','/youtube/outputs') if o['name']=='UI188 R4 S2 sink '+case];assert len(outputs)<=1
 output=outputs[0] if outputs else api.call('POST','/youtube/outputs',{'name':'UI188 R4 S2 sink '+case,'mode':'stream_key','rtmp_url':'rtmps://127.0.0.1:1937/live','stream_key':'r3s2'},201)
 api.call('PUT','/youtube/outputs/'+output['id'],{'name':output['name'],'mode':'stream_key','rtmp_url':'rtmps://127.0.0.1:1937/live','watch_url':'https://www.youtube.com/watch?v=UI188R3test0'})
 payload={'name':fixture_name,'encoder_service_id':'encoder-a','worker_service_id':'worker-a','encoder_profile_id':live['profile'],'discord_config_id':live['configs']['a'],'youtube_output_id':output['id'],'auto_start_trigger':'discord_voice_join','visual_settings':{'expected_revision':0,'discord_target':{'mode':'manual','guild_id':'1001','voice_channel_id':'1003','text_channel_id':'1002'}}}
 if saved:
  sid=saved[0]['id'];initial=dict(payload);initial.pop('visual_settings');api.call('PUT','/streams/'+sid+'/settings',initial)
 else:sid=api.call('POST','/streams',payload,201)['id']
 api.call('POST','/services/bot-a/assign',{'stream_id':sid,'assignment_role':'primary'})
 api.wait(lambda:observations('a')['active_gateway_connections']==1)
 epoch_before=next(a['assigned_at'] for a in api.call('GET','/service-health/bot-a/runtime-config')['assignments'] if a['stream_id']==sid and a['service_id']=='bot-a')
 api.save('s2-before-'+case+'-'+trial+'.json',{'stream_id':sid,'epoch_before':epoch_before,'bot_before':before_a,'other_bot_before':before_b})
 for event in [{'user_id':'1008','channel_id':'1003','is_bot':True,'existing':False},{'user_id':'1999','channel_id':'1003','is_bot':False,'existing':False},{'user_id':'1009','channel_id':'1003','is_bot':False,'member_known':False,'existing':False},{'user_id':'1007','channel_id':'1004','is_bot':False,'existing':False}]:api.call('POST','/input',event,202,base='http://127.0.0.1:19083')
 time.sleep(2);assert api.call('GET','/streams/'+sid)['status'] in ['created','ready'],'BOT-only and wrong VC must not start'
 api.call('GET','/streams/'+sid+'/video-cover-state',status=404)
 api.call('DELETE','/services/bot-a/assignment');api.wait(lambda:observations('a')['active_gateway_connections']==0)
 assert observations('b')['active_gateway_connections']==1 and api.call('GET','/streams/'+s1)['status']=='live','other stream BOT must be maintained'
 payload.pop('visual_settings');payload['discord_config_id']=config['id'];api.call('PUT','/streams/'+sid+'/settings',payload)
 # Visual target is changed through its real revision/CAS API as well.
 visual=api.call('GET','/streams/'+sid+'/visual-settings')
 api.call('PUT','/streams/'+sid+'/visual-settings',{'expected_revision':visual['revision'],'discord_target':{'mode':'manual','guild_id':'1001','voice_channel_id':'1005','text_channel_id':'1002'}})
 api.call('POST','/services/bot-a/assign',{'stream_id':sid,'assignment_role':'primary'})
 current=api.wait(lambda:(row if (row:=api.call('GET','/streams/'+sid))['status']=='live' else False),60)
 assert api.call('GET','/streams/'+sid+'/video-cover-state')['job_generation']==1
 for _ in range(3):api.call('POST','/input',{'user_id':'1006','channel_id':'1005','is_bot':False,'existing':False},202,base='http://127.0.0.1:19083')
 time.sleep(2);current=api.call('GET','/streams/'+sid);cover=api.call('GET','/streams/'+sid+'/video-cover-state');assert cover['job_generation']==1
 observed=observations('a');assert observed['voice_joins']==before_a['voice_joins']+1 and observed['audio_packets']>before_a['audio_packets'] and observed['audio_failures']==0
 assignments=api.call('GET','/service-health/bot-a/runtime-config')['assignments'];after_epoch=next(a['assigned_at'] for a in assignments if a['stream_id']==sid and a['service_id']=='bot-a');assert epoch_before!=after_epoch
 api.save('s2-live-'+case+'.json',{'stream_id':sid,'stream':current,'cover':cover,'s1_stream_id':s1,'before_epoch':epoch_before,'after_epoch':after_epoch,'assignments':assignments,'bot_before':before_a,'bot':observed,'other_bot':observations('b'),'existing_presence':True,'runner_start_posts':0,'voice_join_delta':observed['voice_joins']-before_a['voice_joins']})
 start('s2-rtp-a',['python3','-B',str(pathlib.Path(__file__).with_name('rtp_trace.py')),sid,live['archive_root'].replace('encoder-b-archives-','encoder-a-archives-'),str(api.evidence),case+'-s2'],{'UI188_RTP_FINISH_CASE':case})
 print('REAL_EXISTING_PRESENCE_STARTED_ONCE '+sid,flush=True)
 api.wait(lambda:(api.evidence/('s2-finished-'+os.environ['UI188_PROBE_ATTEMPT'])).exists(),2400)
finally:
 for p in reversed(processes):
  p.terminate()
  try:p.wait(timeout=10)
  except subprocess.TimeoutExpired:p.kill();p.wait()
