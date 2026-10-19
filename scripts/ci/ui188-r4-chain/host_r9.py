import json
import os
from pathlib import Path
import subprocess
import sys
import time
from datetime import datetime, timezone

root=Path(os.environ['UI188_R9_ROOT']).resolve();runner=root/'chain-runner/scripts/ci/ui188-r4-chain'
docker='C:/Program Files/Docker/Docker/resources/bin/docker.exe';node='C:/nvm4w/nodejs/node.exe'
prefix=os.environ.get('UI188_R9_CONTAINER_PREFIX','autostream-ui188-r9-');label='ui188-r9-archive-finalization';case='b5';schema='ui188_r9_b5';results=[]
assert prefix in ['autostream-ui188-r9-','autostream-ui188-r9-final-','autostream-ui188-r9-final2-','autostream-ui188-r9-acceptance-','autostream-ui188-r9-closure-']
registry_path=Path(os.environ.get('UI188_R9_REGISTRY',str(root/'owned-containers.json')))
stop_marker=Path(os.environ.get('UI188_R9_HOST_STOP',str(root/'host-stop-requested.json')))
budget=json.loads((root/'budget.json').read_text());assert datetime.now(timezone.utc)<datetime.fromisoformat(budget['CI_start_deadline_UTC'])
assert not (root/'B5-started.json').exists() and not stop_marker.exists()
assert all(row['original_exit']==0 for row in json.loads((root/'setup-original-exits.json').read_text()))
with (root/'B5-started.json').open('x') as f:json.dump({'at_UTC':datetime.now(timezone.utc).isoformat(),'fixed_product_sources':True,'long_run_attempt':1,'old_results_merged':False},f,indent=2)
base=['exec','-e','UI188_EVIDENCE_DIRECTORY=/evidence','-e','UI188_TEST_CREDENTIALS=/evidence/runtime-test-credentials.json','-e','UI188_PROBE_ATTEMPT='+case,'-e','UI188_DB_SCHEMA='+schema]
def save():
    (root/'B5-original-exits.json').write_text(json.dumps(results,indent=2)+'\n')
def note(step,code):
    results.append({'step':step,'original_exit':code,'at_UTC':datetime.now(timezone.utc).isoformat()});save()
def start(name,args,env=None):
    log=(root/(name+'.original.log')).open('xb')
    return subprocess.Popen(args,cwd=root/'chain-runner',env=env,stdout=log,stderr=subprocess.STDOUT)
def sync(name,args,env=None,timeout=120):
    p=start(name,args,env)
    try:code=p.wait(timeout=timeout)
    except subprocess.TimeoutExpired:code=124;p.terminate();p.wait(timeout=30)
    note(name,code);return code
def waitfile(name,seconds=120,process=None):
    end=time.monotonic()+seconds
    while not (root/name).exists():
        assert not stop_marker.exists(),'host safety stop'
        if process is not None:assert process.poll() is None,'source process ended before '+name
        assert time.monotonic()<end,'bounded file wait '+name
        time.sleep(.2)
def inspect(item):
    value=json.loads(subprocess.check_output([docker,'inspect',item['id']]))[0]
    assert value['Id']==item['id'] and value['Config']['Labels']['autostream.scope']==label
    return value
def resource_guard():
    rows=[]
    for item in json.loads(registry_path.read_text()):
        v=inspect(item);rows.append({'name':item['name'],'running':v['State']['Running'],'CPU':v['HostConfig']['NanoCpus']/1e9,'GiB':v['HostConfig']['Memory']/2**30})
    assert sum(r['CPU'] for r in rows)<=5.7 and sum(r['GiB'] for r in rows)<=10
    (root/'B5-resource-declarations.json').write_text(json.dumps(rows,indent=2)+'\n')
env={**os.environ,'PYTHONDONTWRITEBYTECODE':'1','PYTHONUNBUFFERED':'1','TEMP':str(root/'chrome-temp'),'TMP':str(root/'chrome-temp'),'UI188_EVIDENCE_DIRECTORY':str(root),'UI188_PROBE_ATTEMPT':case,'UI188_TEST_CREDENTIALS':str(root/'runtime-test-credentials.json'),'UI188_BROWSER_ATTEMPT':'1','UI188_S5_ATTEMPT':'1','UI188_LOST_STOP_RESPONSE':'1','UI188_LONG_AUDIO_SECONDS':'900','AUTOSTREAM_BROWSER_PATH':'C:/Program Files/Google/Chrome/Application/chrome.exe'}
(root/'chrome-temp').mkdir(exist_ok=True)
sampler=runtime=s2=None
try:
    assert sync('normal-sandbox-Chrome-startup',[node,'--input-type=module','-e','const {RealBrowser}=await import(process.argv[1]); const b=await RealBrowser.launch(); try { console.log(JSON.stringify({facts:b.ownedLaunchFacts(),ready:await b.evaluate("1+1")})); } finally { await b.close(); }',(runner/'real_browser.mts').as_uri()],env,60)==0
    p=subprocess.run([docker,'run','-d','--name',prefix+'worker-b','--label','autostream.scope='+label,'--network','container:'+prefix+'native','--cpus','.9','--memory','896m','--memory-swap','896m','--pids-limit','128','-v',prefix+'runtime:/r6:ro','-v',str(root)+':/evidence:ro','-e','UI188_DB_SCHEMA='+schema,'autostream-ui188-r2-tools:20261012','python3','-B','/evidence/worker-b-bootstrap.py'],capture_output=True)
    note('owned WorkerB create',p.returncode);assert p.returncode==0
    registry=json.loads(registry_path.read_text());registry.append({'role':'worker','name':prefix+'worker-b','id':p.stdout.decode().strip()});registry_path.write_text(json.dumps(registry,indent=2)+'\n');resource_guard()
    sampler=start('sampler-b5',[docker,*base,prefix+'native','python3','-B','/evidence/chain-runner/scripts/ci/ui188-r4-chain/receiver_observation.py'])
    runtime=start('runtime-b5',[docker,*base,'-e','UI188_CP_BINARY=/r6/bin/cp-r9','-e','UI188_BOT_BINARY=/r6/bin/bot-test-r9','-e','UI188_ENCODER_BINARY=/r6/bin/encoder-closure-final','-e','UI188_STREAM_NAME=UI188 R9 final S1',prefix+'native','python3','-B','/evidence/chain-runner/scripts/ci/ui188-r4-chain/runtime.py'])
    waitfile('acceptance-live-b5.json',180,runtime)
    assert sync('actual-CP-cache-preflight',[node,str(runner/'probe_cover_cache.mts')],env,120)==0
    assert sync('native200-JA-EN-audit-preflight',[node,str(runner/'audit_preflight.mts')],env,120)==0
    assert datetime.now(timezone.utc)<datetime.fromisoformat(budget['CI_start_deadline_UTC'])
    (root/'phase1-preflight-complete.json').write_text(json.dumps({'at_UTC':datetime.now(timezone.utc).isoformat(),'actual_CP_query_cache_probe':'PASS','runtime_memory_5GiB_preflight':'PASS','formal_binaries_reused_exact':True,'B5_long_hold_not_started_yet':True},indent=2)+'\n')
    s2=start('s2-b5',[docker,*base,'-e','UI188_S2_ATTEMPT=1',prefix+'native','python3','-B','/evidence/chain-runner/scripts/ci/ui188-r4-chain/s2.py']);waitfile('s2-live-b5.json',100,s2);note('S2 reached live',0)
    assert sync('live-authority-proof',[docker,*base,prefix+'native','python3','-B','/evidence/chain-runner/scripts/ci/ui188-r4-chain/capture-live-authority.py'])==0
    overlap=time.monotonic();deadline=overlap+1000
    sync('browser-b5',[node,str(runner/'browser.mts')],env,1200)
    if not stop_marker.exists():sync('s5-b5',[node,str(runner/'s5.mts')],env,120)
    while time.monotonic()<deadline:
        assert not stop_marker.exists(),'host safety stop'
        assert runtime.poll() is None and s2.poll() is None,'same source processes must survive hold'
        time.sleep(1)
    held=time.monotonic()-overlap
    (root/'simultaneous-hold-b5.json').write_text(json.dumps({'seconds':held,'required_seconds':1000,'host_stop':False,'authority':'monotonic interval after actual S2 live while both same source processes alive'},indent=2)+'\n');note('same two-stream at least1000s',0 if held>=1000 else 1)
    live=json.loads((root/'acceptance-live-b5.json').read_text());source=live['archive_root']+'/tmp/'+live['stream_id']+'/preview'
    code="import pathlib,shutil; p=pathlib.Path("+repr(source)+");d=pathlib.Path('/r6/hls-capture-b5')/"+repr(live['stream_id'])+";d.parent.mkdir(exist_ok=True);shutil.copytree(p,d)"
    sync('HLS-snapshot',[docker,*base,prefix+'native','python3','-B','-c',code])
    state=start('s4-state-b5',[docker,*base,'-e','UI188_S4_ATTEMPT=1',prefix+'native','python3','-B','/evidence/chain-runner/scripts/ci/ui188-r4-chain/s4_state.py'])
    sync('s4-browser-b5',[node,str(runner/'s4.mts')],env,160)
    try:state_exit=state.wait(timeout=210)
    except subprocess.TimeoutExpired:state_exit=124
    note('S4 state/delete/recording/other',state_exit)
    sync('final-runtime-proof',[docker,*base,prefix+'native','python3','-B','/evidence/chain-runner/scripts/ci/ui188-r4-chain/capture-final-runtime-proof.py'])
except Exception as error:
    results.append({'step':'driver','original_exit':1,'error':str(error)});save()
finally:
    (root/'s2-finished-b5').touch();(root/'chain-finished-b5').touch()
    for name,process in [('S2 source process',s2),('runtime source processes',runtime),('receiver sampler',sampler)]:
        if process is not None:
            try:code=process.wait(timeout=45)
            except subprocess.TimeoutExpired:code=124
            note(name,code)
    for source,target in [('/r6/diagnostic/consumer-b5.jsonl','consumer-b5.jsonl'),('/r6/private-ui188_r9_b5/audio-bot-a.jsonl','audio-b5-bot-a.jsonl'),('/r6/private-ui188_r9_b5/audio-bot-b.jsonl','audio-b5-bot-b.jsonl')]:
        sync('capture-'+target,[docker,'cp',prefix+'native:'+source,str(root/target)])
    sync('resource-after-run',[docker,'exec',prefix+'native','python3','-B','/evidence/resource_snapshot.py','after-run'])
    registry=json.loads(registry_path.read_text())
    for item in sorted(registry,key=lambda r:{'worker':0,'native':1,'browser':2,'db':3}.get(r['role'],4)):
        v=inspect(item)
        if v['State']['Running']:sync(item['name']+'-normal-stop',[docker,'stop','--time','20',item['id']],timeout=40)
        sync(item['name']+'-retired-cap',[docker,'update','--cpus','.025','--memory','32m','--memory-swap','32m',item['id']])
    resource_guard()
if not stop_marker.exists():
    p=subprocess.run([docker,'run','-d','--name',prefix+'analysis','--label','autostream.scope='+label,'--network','none','--cpus','4','--memory','8g','--memory-swap','8g','--pids-limit','512','-v',prefix+'runtime:/r6:ro','-v',str(root)+':/evidence','autostream-ui188-r2-tools:20261012','sleep','3600'],capture_output=True);note('separate owned analysis cgroup create',p.returncode)
    if p.returncode==0:
        registry=json.loads(registry_path.read_text());registry.append({'role':'analysis','name':prefix+'analysis','id':p.stdout.decode().strip()});registry_path.write_text(json.dumps(registry,indent=2)+'\n');resource_guard()
        analysis=['exec','-e','UI188_EVIDENCE_DIRECTORY=/evidence','-e','UI188_PROBE_ATTEMPT=b5','-e','UI188_BROWSER_ATTEMPT=1','-e','UI188_HLS_CAPTURE_ROOT=/r6/hls-capture-b5',prefix+'analysis','python3','-B']
        sync('independent-same-run-media',[docker,*analysis,'/evidence/chain-runner/scripts/ci/ui188-r4-chain/media.py'],timeout=600)
        sync('both-receiver-RTP-kernel-AAC-PCM',[docker,*analysis,'/evidence/chain-runner/scripts/ci/ui188-r4-chain/receiver_acceptance.py'],timeout=300)
        sync('analysis-normal-stop',[docker,'stop','--time','20',p.stdout.decode().strip()],timeout=40)
# Guardian remains active through final closure.
result={'ended_at_UTC':datetime.now(timezone.utc).isoformat(),'original_exit':1 if any(r['original_exit']!=0 for r in results) else 0,'steps':len(results),'old_results_merged':False}
(root/'B5-driver-original-exit.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result),flush=True);sys.exit(result['original_exit'])
