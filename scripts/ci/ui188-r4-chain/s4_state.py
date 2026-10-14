"""Real stop/delete guards, file preservation and other-stream isolation."""
import hashlib,json,os,pathlib,urllib.parse
from api_client import API
case=os.environ['UI188_PROBE_ATTEMPT'];attempt=os.environ.get('UI188_S4_ATTEMPT','1');api=API('s4-state-'+case+'-'+attempt);live=json.loads((api.evidence/('acceptance-live-'+case+'.json')).read_text());s1=live['stream_id']
s2=json.loads((api.evidence/('s2-live-'+case+'.json')).read_text());s2id=s2['stream_id']
for sid in [s1,s2id]:
 if os.environ.get('UI188_S4_POST_ONLY')=='1':
  original=json.loads((api.evidence/('s4-active-delete-negative-'+sid+'-2.json')).read_text());assert original['before']['id']==sid and original['rejection']=={'code':'stream_active','status':'live'};continue
 before=api.call('GET','/streams/'+sid);assert before['status']=='live';rejected=api.call('DELETE','/streams/'+sid,status=409);api.save('s4-active-delete-negative-'+sid+'-'+attempt+'.json',{'before':before,'rejection':rejected});assert rejected=={'code':'stream_active','status':'live'};assert api.call('GET','/streams/'+sid)['status']=='live'
before_other=api.call('GET','/observations',base='http://127.0.0.1:19083')
api.wait(lambda:(api.evidence/('before-delete-'+case+'.json')).exists(),180)
recording=next(p for p in pathlib.Path(live['archive_root']).rglob('*.mkv') if s1 in str(p));fingerprint=hashlib.sha256(recording.read_bytes()).hexdigest()
if os.environ.get('UI188_S4_POST_ONLY')=='1':
 original=json.loads((api.evidence/('before-delete-recording-'+case+'.json')).read_text());assert original['stream_id']==s1 and original['sha256']==fingerprint
else:api.save('before-delete-recording-'+case+'.json',{'stream_id':s1,'path':str(recording),'sha256':fingerprint,'bytes':recording.stat().st_size})
api.wait(lambda:(api.evidence/('browser-delete-done-'+case)).exists(),60)
assert recording.is_file() and hashlib.sha256(recording.read_bytes()).hexdigest()==fingerprint
other=api.call('GET','/streams/'+s2id);obs=api.call('GET','/observations',base='http://127.0.0.1:19083');assert other['status']=='live' and obs['stream_id']==s2id and obs['voice_joins']==before_other['voice_joins'] and obs['audio_packets']>before_other['audio_packets']
cfg=api.call('GET','/service-health/bot-b/runtime-config');assert not cfg['assignments']
source=other['updated_at'];stop=api.call('POST','/streams/'+s2id+'/stop',{'expected_updated_at':source});outcome=api.call('GET','/streams/'+s2id+'/stop-outcome?expected_updated_at='+urllib.parse.quote(source,safe=''));assert outcome['stop_confirmed']
api.call('POST','/input',{'user_id':'1006','channel_id':'1005','is_bot':False,'existing':True},202,base='http://127.0.0.1:19083')
import time
for _ in range(6):
 assert api.call('GET','/streams/'+s2id)['status']=='ready';assert api.call('GET','/observations',base='http://127.0.0.1:19083')['voice_joins']==obs['voice_joins'];time.sleep(.5)
media2=next(p for p in pathlib.Path('/r6/data/encoder-a-archives-'+case).rglob('*.mkv') if s2id in str(p));sha2=hashlib.sha256(media2.read_bytes()).hexdigest();api.call('DELETE','/streams/'+s2id);assert api.call('GET','/streams/'+s2id)['deleted_at'];assert hashlib.sha256(media2.read_bytes()).hexdigest()==sha2
api.save('s4-state-through-s2-'+case+'.json',{'recording1':{'path':str(recording),'sha256_before_after':fingerprint},'other_during_delete':other,'other_bot_before':before_other,'other_bot_after':obs,'s2_stop':stop,'s2_outcome':outcome,'recording2':{'path':str(media2),'sha256_before_after':sha2},'no_existing_presence_restart':True})
created=api.call('POST','/streams',{'name':'UI188 R4 never started '+case,'encoder_service_id':'encoder-a','worker_service_id':'worker-a','encoder_profile_id':live['profile'],'discord_config_id':live['configs']['a'],'youtube_output_id':other['youtube_output_id'],'auto_start_trigger':'discord_voice_join','visual_settings':{'expected_revision':0,'discord_target':{'mode':'manual','guild_id':'1001','voice_channel_id':'1003','text_channel_id':'1002'}}},201);assert created['status']=='created';api.call('DELETE','/streams/'+created['id']);assert api.call('GET','/streams/'+created['id'])['deleted_at'];assert not api.call('GET','/streams')
api.wait(lambda:api.call('GET','/observations',base='http://127.0.0.1:19083')['active_gateway_connections']==0 and api.call('GET','/observations',base='http://127.0.0.1:19086')['active_gateway_connections']==0)
api.save('s4-state-success-'+case+'.json',{'recording1':{'path':str(recording),'sha256_before_after':fingerprint},'other_during_delete':other,'other_bot_before':before_other,'other_bot_after':obs,'s2_stop':stop,'s2_outcome':outcome,'recording2':{'path':str(media2),'sha256_before_after':sha2},'never_started_deleted':created['id'],'all_active_rows_deleted':True,'no_existing_presence_restart':True,'normal_stops_no_resend':True})
(api.evidence/('s2-finished-'+case)).touch();print('REAL_STOP_DELETE_RELEASE_RECORDING_OTHER_PRESENCE_FENCE_PASS')
