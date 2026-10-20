"""Semantic acceptance, real execution receipts and required proof are separate gates."""
import json
from gate import GROUPS,require,validate_identity
from cover_contract import verify_cover
from producer_contract import formal_inputs,outputs,validate_execution
from run_identity import decode
from seal import sha_file


def audio_dependencies(groups):
    return all(groups.get(name)=='PASS' for name in ('COVER','HLS','FRAME','RTP','SOCKET','AAC','PCM'))


def evaluate(root,identity,stages):
    from evaluate import archive,stop_receipt
    validate_identity(decode((root/'identity.json').read_bytes()),identity)
    require(identity['job']=='full-e2e','BLOCKED_SMOKE_IS_NOT_FULL_E2E')
    case=identity['case']
    result={'identity':identity,'groups':{g:'NOT_RUN' for g in GROUPS},'original_exits':{k:v.get('original_exit') for k,v in stages.items()},
            'known_issue':'KNOWN_UNRESOLVED_MEDIA_ISSUE','windows':'ENV_NOT_COVERED_BY_LINUX'}
    def load(name):
        path=root/name
        require(not path.is_symlink() and path.stat().st_mtime_ns>=int(identity['started_ns']),'BLOCKED_STALE_RUN')
        return decode(path.read_bytes())
    live=load('acceptance-live-'+case+'.json') if (root/('acceptance-live-'+case+'.json')).is_file() else {}
    second=load('s2-live-'+case+'.json') if (root/('s2-live-'+case+'.json')).is_file() else {}
    sid,sid2=live.get('stream_id'),second.get('stream_id')
    expected=outputs(identity,(sid or 'missing-S1',sid2 or 'missing-S2'))
    def group(name,producers,check):
        if any(stages.get(p,{}).get('original_exit') not in (0,None) for p in producers):result['groups'][name]='FAIL';return
        if any(stages.get(p,{}).get('original_exit') is None for p in producers):return
        try:
            sources=load('producer-sources.json')['sources']
            build=load('fixed-formal-binary-provenance.json')
            require(sha_file(root/'producer-sources.json')==build['producer_sources_sha256'],'BLOCKED_PRODUCER_SOURCE_SEAL')
            for producer in producers:
                names=expected.get(producer,['receiver-acceptance-'+case+'.json'])
                validate_execution(root,identity,stages[producer],names,sources)
            check();result['groups'][name]='PASS'
        except FileNotFoundError:result['groups'][name]='NOT_RUN'
        except Exception:result['groups'][name]='FAIL'
    def expect(value):require(value,'FAILED_ACCEPTANCE_CONTRACT')
    def covers():
        for slot,data in (('S1',live),('S2',second)):
            verify_cover(load('cover-contract-'+slot+'-'+case+'.json'),identity,slot,data)
    def media(slot):
        value=load('media-verification-'+case+('-s2' if slot=='S2' else '')+'.json')
        validate_identity(value['identity'],identity)
        expect(value['schema_version']==2 and value['producer']=='media-'+slot and value['stream_id']==(sid if slot=='S1' else sid2) and value['run_id']==case)
        return value
    def receiver():
        from receiver_contract import validate
        return validate(load('receiver-acceptance-'+case+'.json'),identity,(sid,sid2),load('media-inputs.json'))
    state=lambda:load('s4-state-success-'+case+'.json')
    s5=lambda:load('s5-success-'+case+'-1.json')
    group('S0',['runtime'],lambda:expect(load('s0-negative-b3.json')['aggregate']=='PASS' and load('s0-negative-b3.json')['run']==case))
    group('S1',['runtime'],lambda:expect(live['status']=='live' and sid!=sid2 and bool(sid)))
    group('S2',['s2'],lambda:expect(second['stream']['status']=='live' and second['runner_start_posts']==0 and second['voice_join_delta']==1 and second['s1_stream_id']==sid and second['cover']['job_generation']==1))
    group('S3',['browser-S1','browser-S2'],covers)
    group('S4',['s4','state'],lambda:expect(state()['all_active_rows_deleted'] is True and state()['normal_stops_no_resend'] is True))
    group('S5',['s5'],lambda:expect(s5()['scope']=='full S5' and s5()['consoleErrors']==0 and s5()['native200']['dpr']==2))
    group('OVERLAP',['runtime','s2','overlap'],lambda:expect(load('simultaneous-hold-'+case+'.json')['seconds']>=1000 and load('simultaneous-hold-'+case+'.json')['host_stop'] is False and load('simultaneous-hold-'+case+'.json')['same_source_processes'] is True and load('simultaneous-hold-'+case+'.json')['samples']>=990))
    def archives():
        archive(load('r9-report-accepted-'+sid+'-'+case+'.json'),sid,identity['services']['encoder-b'])
        archive(load('r9-report-accepted-'+sid2+'-'+case+'.json'),sid2,identity['services']['encoder-a'])
    group('ARCHIVE',['state'],archives)
    group('COVER',['browser-S1','browser-S2'],covers)
    def hls():
        covers()
        for slot in ('S1','S2'):expect(media(slot)['analyses']['hls_recording_identity']['first_picture_identity_verified'] is True)
    group('HLS',['browser-S1','browser-S2','media-S1','media-S2'],hls)
    for name in ('RTP','SOCKET','AAC','PCM'):
        def check(name=name):
            data=receiver()['receivers']
            for item in data.values():
                if name=='RTP':
                    value=item['RTP'];expect(value['count']>=49000 and value['discontinuities']==[] and 49.5<=value['packet_rate']<=50.5 and value['observer']['tap_drops']==0 and value['observer']['rejected_headers']==0 and value['observer']['unexpected_target_packets']==0)
                elif name=='SOCKET':expect(item['max_kernel_drops']==0 and item['actual_receive_buffer']['samples']>10 and item['actual_receive_buffer']['longest_sampled_90percent_saturation_seconds']<10)
                elif name=='AAC':expect(item['AAC']['pass'] is True and item['AAC']['max_uncovered']<=.05 and item['tone_failure_count']==0)
                else:expect(set(item['late_PCM_bands'])=={'250-400','580-700','800-950'} and all(item['late_PCM_bands'][str(lo)+'-'+str(hi)]['pass'] is True and item['late_PCM_bands'][str(lo)+'-'+str(hi)]['count']==hi-lo+1 for lo,hi in ((250,400),(580,700),(800,950))))
        group(name,['receiver'],check)
    def frames():
        for slot in ('S1','S2'):
            value=media(slot);analyses=value['analyses'];target=analyses['target_frames'];all_frames=analyses['independent_all_frames']
            expect(value['aggregate']=='PASS' and value['audio_original_exit']==0 and target['status']=='PASS' and target['ON']['count']>=30 and target['OFF']['count']>=30 and not target['ON']['failures'] and not target['OFF']['failures'])
            expect(all_frames['count']==analyses['video_pts']['count'] and all_frames['count']>0 and analyses['video_pts']['last']>=990 and all(frame['watermark_pass'] is True for frame in all_frames['frames']))
    group('FRAME',['media-S1','media-S2'],frames)
    def formal():
        receipt=load('formal-receiver.json');validate_identity(receipt['identity'],identity)
        expect(receipt['original_exit']==stages['receiver']['original_exit']==stages['receiver']['observed_process_exit']==0 and receipt['receiver_invocation_uuid']==stages['receiver']['invocation_uuid'])
        expect(receipt['unchanged_after'] is True and receipt['media_input_count']==2 and receiver()['aggregate']=='PASS')
        expect(sha_file(root/'receiver-inputs-sealed.json')==receipt['sealed_inputs_sha256'])
        formal_inputs(root,identity,load('receiver-inputs-sealed.json'))
    group('RECEIVER',['receiver'],formal)
    group('PROVENANCE',['final-proof','live-authority'],lambda:expect(load('final-runtime-proof-'+case+'.json')['all_five_formal_binary_bytes_unchanged'] is True and load('same-run-live-authority-'+case+'.json')['aggregate']=='PASS' and load('same-run-live-authority-'+case+'.json')['run']==case))
    group('F01',['runtime'],lambda:expect(len(load('create-required-service-negative-'+case+'.json')['cases'])==2 and load('create-required-service-negative-'+case+'.json')['aggregate']=='PASS'))
    group('F02',['s4'],lambda:stop_receipt(load('s4-stop-success-'+case+'-1.json'),sid))
    group('F03',['state'],archives)
    group('F04',['runtime','s2','state'],lambda:expect(len(load('r9-stale-authority-'+case+'.json'))==2 and all(row['CP_start_calls']==0 for row in load('r9-stale-authority-'+case+'.json')) and state()['no_existing_presence_restart'] is True))
    group('F05',['s4'],lambda:expect(load('s4-delete-success-'+case+'-1.json')['confirmation']['inputs']==0))
    group('F06',['state','s2'],lambda:expect(state()['all_active_rows_deleted'] is True and state()['no_existing_presence_restart'] is True))
    group('F07',['browser-S1','browser-S2','media-S1','media-S2','receiver'],lambda:expect(audio_dependencies(result['groups'])))
    group('F08',['s5'],lambda:expect(s5()['cancelPUTs']==0 and s5()['realUnboundImage']['status']==403 and s5()['realMissingPreset']['status']==404))
    group('F09',['s5','browser-S1','browser-S2'],lambda:expect(result['groups']['HLS']=='PASS' and s5()['native200']['dpr']==2))
    group('F10',['s5','browser-S1','browser-S2'],lambda:expect(s5()['japaneseState'] and result['groups']['COVER']=='PASS'))
    group('F11',['s5'],lambda:expect(load('r9-audit-pending-same-run-'+case+'.json')['immutable'] is True and bool(s5()['auditPending'])))
    result['denominator']=len(GROUPS)
    result['aggregate']='PASS' if all(v=='PASS' for v in result['groups'].values()) and all(v==0 for v in result['original_exits'].values()) else 'FAIL'
    return result
