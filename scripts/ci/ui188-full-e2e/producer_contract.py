"""Required producer/output ownership is independent of which files happen to exist."""
import hashlib
import json
from pathlib import Path
import uuid
from gate import HEX256,require,validate_identity
from run_identity import decode

PRE_PRODUCERS=('browser-canary','runtime','sampler','cache-preflight','audit-preflight','s2','live-authority',
    'browser-S1','browser-S2','s5','overlap','hls-snapshot','state','s4','final-proof','capture','media-S1','media-S2')
BUILTINS={'overlap','hls-snapshot','capture'}
SOURCES={name:'chain/'+filename for name,filename in (
    ('runtime','runtime.py'),('sampler','receiver_observation.py'),('cache-preflight','probe_cover_cache.mts'),
    ('audit-preflight','audit_preflight.mts'),('s2','s2.py'),('live-authority','capture-live-authority.py'),
    ('browser-S1','browser.mts'),('browser-S2','browser.mts'),('s5','s5.mts'),('s4','s4.mts'),('state','s4_state.py'),
    ('final-proof','capture-final-runtime-proof.py'),('media-S1','media.py'),('media-S2','media.py'))}
SOURCES.update({name:'platform/execute.py' for name in BUILTINS})
SOURCES['browser-canary']='platform/canary.mts'
SOURCES['receiver']='chain/receiver_acceptance.py'
SOURCES['receiver-diagnostic']='chain/receiver_acceptance.py'


def outputs(identity,sids):
    case=identity['case'];sid,sid2=sids
    values={
        'browser-canary':['browser-canary.json','closed-canary.json'],
        'runtime':['acceptance-live-'+case+'.json','s0-negative-b3.json','create-required-service-negative-'+case+'.json','r9-stale-authority-'+case+'.json'],
        'sampler':[], # Actual long-running sampler output is copied and bound by capture.
        'cache-preflight':['cover-cache-probe-'+case+'.json'],
        'audit-preflight':['r9-audit-preflight-'+case+'.json'],
        's2':['s2-live-'+case+'.json'],
        'live-authority':['same-run-live-authority-'+case+'.json'],
        's5':['s5-success-'+case+'-1.json','r9-audit-pending-same-run-'+case+'.json'],
        'overlap':['simultaneous-hold-'+case+'.json'],
        'hls-snapshot':['hls-snapshot-'+case+'.json'],
        's4':['s4-stop-success-'+case+'-1.json','s4-delete-success-'+case+'-1.json'],
        'state':['s4-state-success-'+case+'.json',*['r9-'+kind+'-'+stream+'-'+case+'.json' for stream in sids for kind in ('pending','report-accepted')]],
        'final-proof':['final-runtime-proof-'+case+'.json'],
        'capture':['consumer-'+case+'.jsonl','audio-'+case+'-bot-a.jsonl','audio-'+case+'-bot-b.jsonl','media-inputs.json'],
        'media-S1':['media-verification-'+case+'.json','audio-analysis-'+case+'.json'],
        'media-S2':['media-verification-'+case+'-s2.json','audio-analysis-'+case+'-s2.json']}
    for slot,suffix in (('S1',''),('S2','-s2')):
        values['browser-'+slot]=['browser-chain-cover-success-'+case+suffix+'-1.json','browser-chain-frames-'+case+suffix+'-1.json','cover-contract-'+slot+'-'+case+'.json']
        values['runtime' if slot=='S1' else 's2'].extend(['rtp-trace-'+case+suffix+'-metadata.json','rtp-trace-'+case+suffix+'.jsonl','rtp-observer-statistics-'+case+suffix+'.json'])
    require(set(values)==set(PRE_PRODUCERS),'BLOCKED_PRODUCER_DENOMINATOR')
    return values


def stream_ids(root,identity):
    case=identity['case'];ids=[]
    for prefix in ('acceptance-live-','s2-live-'):
        value=decode((root/(prefix+case+'.json')).read_bytes());sid=value['stream_id']
        require(str(uuid.UUID(sid))==sid,'BLOCKED_RECEIVER_STREAM')
        ids.append(sid)
    require(ids[0]!=ids[1],'BLOCKED_RECEIVER_STREAM')
    return ids


def required_inputs(root,identity):
    ids=stream_ids(root,identity)
    names=['identity.json','fixed-formal-binary-provenance.json','environment.json','producer-sources.json',
           'pre-receiver-producers.json','runtime-privileges.json','tap-broker-ready.json']
    names.extend(name for files in outputs(identity,ids).values() for name in files)
    require(len(names)==len(set(names)) and len(names)>30,'BLOCKED_REQUIRED_INPUT_DENOMINATOR')
    return set(names)


def file_record(root,name):
    path=root/name
    require(not path.is_symlink() and path.is_file() and path.resolve().is_relative_to(root.resolve()),'BLOCKED_PRODUCER_OUTPUT')
    require(path.stat().st_size>0,'BLOCKED_EMPTY_PRODUCER_OUTPUT')
    with path.open('rb') as stream:digest=hashlib.file_digest(stream,'sha256').hexdigest()
    return {'path':name,'sha256':digest,'bytes':path.stat().st_size}


def validate_producers(root,identity,record):
    require(set(record)=={'schema_version','identity','producers'} and record['schema_version']==1,'BLOCKED_PRODUCER_SCHEMA')
    validate_identity(record['identity'],identity)
    rows=record['producers'];require(type(rows) is list and len(rows)==len(PRE_PRODUCERS),'BLOCKED_PRODUCER_DENOMINATOR')
    require({row['producer'] for row in rows}==set(PRE_PRODUCERS),'BLOCKED_PRODUCER_DUPLICATE_OR_EXTRA')
    require(len({row['invocation_uuid'] for row in rows})==len(rows),'BLOCKED_PRODUCER_DUPLICATE')
    expected=outputs(identity,stream_ids(root,identity))
    sources=decode((root/'producer-sources.json').read_bytes())
    build=decode((root/'fixed-formal-binary-provenance.json').read_bytes())
    require(hashlib.sha256((root/'producer-sources.json').read_bytes()).hexdigest()==build['producer_sources_sha256'],'BLOCKED_PRODUCER_SOURCE_SEAL')
    require(set(sources)=={'schema_version','sources'} and sources['schema_version']==1,'BLOCKED_PRODUCER_SOURCES')
    for row in rows:
        validate_execution(root,identity,row,expected[row['producer']],sources['sources'])
    return True


def validate_execution(root,identity,row,expected,sources):
    require(set(row)=={'producer','invocation_uuid','identity','kind','pid','started_ns','finished_ns','original_exit','observed_process_exit','source','source_sha256','outputs'},'BLOCKED_PRODUCER_SCHEMA')
    validate_identity(row['identity'],identity)
    invocation=uuid.UUID(row['invocation_uuid'])
    require(str(invocation)==row['invocation_uuid'] and invocation.version==4 and type(row['pid']) is int and row['pid']>0,'BLOCKED_PRODUCER_INVOCATION')
    require(type(row['started_ns']) is int and type(row['finished_ns']) is int and int(identity['started_ns'])<=row['started_ns']<row['finished_ns'],'BLOCKED_PRODUCER_TIME')
    require(type(row['original_exit']) is type(row['observed_process_exit']) is int and row['original_exit']==row['observed_process_exit']==0,'BLOCKED_PRODUCER_ORIGINAL_EXIT')
    require(row['kind']==('IN_PROCESS' if row['producer'] in BUILTINS else 'PROCESS'),'BLOCKED_PRODUCER_EXECUTION')
    require(row['producer'] in SOURCES and row['source']==SOURCES[row['producer']] and row['source'] in sources and sources[row['source']]==row['source_sha256'] and HEX256.fullmatch(row['source_sha256']),'BLOCKED_PRODUCER_SOURCE')
    actual=row['outputs'];require(len(actual)==len(expected) and {r['path'] for r in actual}==set(expected),'BLOCKED_PRODUCER_OUTPUT_SET')
    for item in actual:
        require(item==file_record(root,item['path']),'BLOCKED_PRODUCER_OUTPUT_CHANGED')
        modified=(root/item['path']).stat().st_mtime_ns
        require(row['started_ns']<=modified<=row['finished_ns'],'BLOCKED_PRODUCER_STALE_OUTPUT')
    return True


def formal_inputs(root,identity,record):
    from seal import verify_seal
    require(identity['job']=='full-e2e','BLOCKED_SMOKE_IS_NOT_FULL_E2E')
    expected=required_inputs(root,identity)
    verify_seal(root,record,identity,expected=expected)
    validate_producers(root,identity,decode((root/'pre-receiver-producers.json').read_bytes()))
    # Two actual recording bindings must agree with S4's immutable source values.
    state=decode((root/('s4-state-success-'+identity['case']+'.json')).read_bytes())
    media=decode((root/'media-inputs.json').read_bytes())
    require(type(media) is list and len(media)==2,'BLOCKED_RECEIVER_MEDIA_INPUTS')
    for item,key,owner in zip(media,('recording1','recording2'),('encoder-b','encoder-a')):
        require(set(item)=={'path','sha256','bytes','stream_id','owner'} and item['owner']==owner and item['stream_id']==stream_ids(root,identity)[0 if key=='recording1' else 1],'BLOCKED_RECEIVER_MEDIA_OWNER')
        require(item['path']==state[key]['path'] and item['sha256']==state[key]['sha256_before_after'] and HEX256.fullmatch(item['sha256']) and type(item['bytes']) is int and item['bytes']>0,'BLOCKED_RECEIVER_S4_BINDING')
        path=Path(item['path']);allowed=Path('/r6/data/'+owner+'-archives-'+identity['case'])
        require(path.is_relative_to(allowed) and path.resolve().is_relative_to(allowed) and not path.is_symlink() and item['stream_id'] in str(path) and path.is_file(),'BLOCKED_RECEIVER_MEDIA_PATH')
        with path.open('rb') as stream:digest=hashlib.file_digest(stream,'sha256').hexdigest()
        require(path.stat().st_size==item['bytes'] and digest==item['sha256'],'BLOCKED_RECEIVER_MEDIA_CHANGED')
    ports=[]
    for sid,owner,suffix in zip(stream_ids(root,identity),('encoder-b','encoder-a'),('','-s2')):
        metadata=decode((root/('rtp-trace-'+identity['case']+suffix+'-metadata.json')).read_bytes())
        path=Path(metadata['actual_sdp']);allowed=Path('/r6/data/'+owner+'-archives-'+identity['case'])/'tmp'/sid
        require(metadata['stream_id']==sid and path.is_relative_to(allowed) and metadata['payload_written'] is False and type(metadata['udp_port']) is int and 0<metadata['udp_port']<65536,'BLOCKED_RTP_STREAM_BINDING')
        ports.append(metadata['udp_port'])
    require(len(set(ports))==2,'BLOCKED_RTP_DUPLICATE_PORT')
    require(len({item['path'] for item in media})==2,'BLOCKED_RECEIVER_MEDIA_DUPLICATE')
    return expected
