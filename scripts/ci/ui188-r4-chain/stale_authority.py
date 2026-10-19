"""B5 direct stale inputs: actual SDK object and saved actual callback authority."""
import json,time

def verify_stale(request,wait,sid,evidence,case):
    base='http://127.0.0.1:19086';control='/r9/stale-authority';stages=[]
    observe=lambda:request('GET',control,base=base)
    gateway=lambda:request('GET','/observations',base=base)
    def invoke(operation,stream=False):
        return request('POST',control,{'operation':operation,**({'stream_id':sid} if stream else {})},base=base)
    def save(): (evidence/('r9-stale-authority-'+case+'.json')).write_text(json.dumps(stages,indent=2)+'\n')
    wait(lambda:gateway()['active_gateway_connections']==1)
    wait(lambda:request('GET',control+'?stream_id='+sid,base=base)['waiting_target_ready'])
    assignment1=next(a for a in request('GET','/service-health/bot-b/runtime-config')['assignments'] if a['stream_id']==sid and a['service_id']=='bot-b')
    armed=invoke('capture-old',True);assert armed['old_ref_current'] and armed['armed']
    request('POST','/input',{'user_id':'1010','channel_id':'1003','is_bot':False},202,base=base)
    wait(lambda:observe()['held'])
    request('POST','/input',{'user_id':'1010','channel_id':'','is_bot':False},202,base=base)
    # Read actual SDK location using the captured reference, before replacement.
    # A missing location cannot substitute for the peer's leave admission.
    wait(lambda:observe()['capture_user_location']=='absent' and gateway()['voice_joins']==0 and request('GET','/streams/'+sid)['status'] in ['created','ready'])
    request('DELETE','/services/bot-b/assignment');wait(lambda:gateway()['active_gateway_connections']==0)
    request('POST','/services/bot-b/assign',{'stream_id':sid,'assignment_role':'primary'})
    wait(lambda:gateway()['active_gateway_connections']==1)
    wait(lambda:request('GET',control+'?stream_id='+sid,base=base)['waiting_target_ready'])
    current=invoke('capture-current',True)
    request('POST','/input',{'user_id':'1010','channel_id':'','is_bot':False,'member_only':True},202,base=base)
    wait(lambda:observe()['current_human_known'] and observe()['capture_user_location']=='absent')
    assignment2=next(a for a in request('GET','/service-health/bot-b/runtime-config')['assignments'] if a['stream_id']==sid and a['service_id']=='bot-b')
    assert assignment1['assigned_at']!=assignment2['assigned_at']
    assert not current['old_ref_current'] and current['current_ref_current'] and current['replacement_ready']
    assert current['held_matches_old'] and not current['held_matches_current'] and not current['overflow']
    baseline=observe();external=gateway();assert external['voice_joins']==0 and baseline['starter_calls']==0
    stages.append({'phase':'real-replacement','old_assignment':{k:assignment1[k] for k in ['stream_id','service_id','assigned_at']},'current_assignment':{k:assignment2[k] for k in ['stream_id','service_id','assigned_at']},'preconditions':current})
    for operation in ['old-session','old-epoch']:
        before=observe();after=invoke(operation);time.sleep(2.2);settled=observe();node=gateway();stream=request('GET','/streams/'+sid)
        assert settled['starter_calls']==before['starter_calls']==0 and node['voice_joins']==external['voice_joins']==0
        assert stream['status'] in ['created','ready'] and settled['current_ref_current'] and not settled['old_ref_current']
        transport=request('GET','/r9/transport',base='http://127.0.0.1:18841');assert not transport['control_calls'],'stale input reached real Worker/Encoder control transport'
        if operation=='old-session':assert after['old_session_entries']==1 and after['controller_entries']==before['controller_entries']
        else:assert after['old_epoch_entries']==1+after['extra_held'] and after['controller_entries']-before['controller_entries']==after['old_epoch_entries']
        stages.append({'phase':operation,'before':before,'after':after,'settled':settled,'external':node,'stream_status':stream['status'],'CP_start_calls':0,'worker_encoder_dispatch_observer':'same-run transport counter checked separately'});save()
    return stages
