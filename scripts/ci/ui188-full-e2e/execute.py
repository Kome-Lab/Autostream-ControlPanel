"""One owned Linux live run. Original failures survive independent later analysis."""
import http.server
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import threading
import time

from gate import Blocked, require, validate_identity
from seal import seal, verify_seal, sha_file
from run_identity import read_bound
from producer_runner import Recorder
from producer_contract import formal_inputs,required_inputs,stream_ids

ROOT = Path('/evidence')
CHAIN = Path('/r6/cp/scripts/ci/ui188-r4-chain')
STAGES = {}
LOGS = []
RECORDER = None
STREAM_IDS = None


def save_stages():
    (ROOT / 'stages.json').write_text(json.dumps(STAGES, indent=2) + '\n')


def start(label, args, env=None):
    return RECORDER.start(label,args,env)


def settle(label, process, seconds, terminate_on_timeout=True):
    return RECORDER.settle(label,process,seconds,STREAM_IDS or ('pending-S1','pending-S2'))


def step(label, argv, timeout, env=None):
    return settle(label, start(label, argv,env), timeout)


def script(name):
    return ['python3', '-B', str(CHAIN / (name + '.py'))]


def browser(name):
    return step(name, ['node', '--experimental-strip-types', str(CHAIN / (name + '.mts'))], 1250 if name == 'browser' else 180)


def source_processes():
    found = {}
    roles = {'/r6/bin/cp': 'cp', '/r6/bin/encoder': 'encoder', '/r6/bin/bot-test': 'bot', '/r6/bin/worker': 'worker-a'}
    for p in Path('/proc').iterdir():
        if not p.name.isdecimal():
            continue
        try:
            role = roles.get(os.readlink(p / 'exe'))
            if role:
                stat = (p / 'stat').read_text().split()
                require(stat[2] != 'Z', 'BLOCKED_SOURCE_EXIT')
                facts=dict(line.split(':',1) for line in (p/'status').read_text().splitlines() if ':' in line)
                require(all(int(value)==1000 for value in facts['Uid'].split()) and int(facts['CapEff'],16)==int(facts['CapBnd'],16)==0,'BLOCKED_PRODUCT_PRIVILEGES')
                found[p.name] = {'role': role, 'start_ticks': int(stat[21])}
        except OSError:
            continue
    require({role: sum(row['role'] == role for row in found.values()) for role in roles.values()} ==
            {'cp': 1, 'encoder': 2, 'bot': 2, 'worker-a': 1}, 'BLOCKED_SOURCE_PROCESS_DENOMINATOR')
    worker = json.loads((ROOT / 'worker-b-liveness.json').read_text())
    require(worker['alive'] is True and time.time_ns() - worker['at_ns'] < 3_000_000_000 and
            worker['namespace'] == os.readlink('/proc/self/ns/net'), 'BLOCKED_WORKER_SOURCE_EXIT')
    require(worker['pid_namespace']!=os.readlink('/proc/self/ns/pid') and worker['facts']['cgroup_namespace']!=os.readlink('/proc/self/ns/cgroup') and worker['facts']['cgroup_root_inode']!=Path('/sys/fs/cgroup').stat().st_ino,'BLOCKED_WORKER_ISOLATION')
    found['worker-b-separate-pid-namespace'] = {'pid': worker['pid'], 'start_ticks': worker['start_ticks']}
    return found


def wait_file(name, processes, seconds=180):
    deadline = time.monotonic() + seconds
    while not (ROOT / name).is_file():
        require(all(p.poll() is None for p in processes), 'BLOCKED_SOURCE_EXIT')
        require(time.monotonic() < deadline and not (ROOT / 'host-stop-requested.json').exists(), 'BLOCKED_START_OR_SAFETY')
        time.sleep(.1)


def canary():
    status = dict(l.split(':', 1) for l in Path('/proc/self/status').read_text().splitlines() if ':' in l)
    require(os.getuid()==1000 and int(status['CapEff'].strip(),16)==int(status['CapBnd'].strip(),16)==0, 'BLOCKED_NETWORK_ADMIN_RETAINED')
    denied = []
    for address in [('1.1.1.1', 443), ('169.254.169.254', 80), ('127.0.0.11', 53)]:
        try:
            with socket.create_connection(address, timeout=2):
                denied.append(False)
        except OSError:
            denied.append(True)
    require(all(denied), 'BLOCKED_EGRESS_CANARY')
    with socket.create_connection(('db', 3306), timeout=2):
        pass

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'UI188_LOOPBACK_CANARY')
        def log_message(self, *args):
            pass
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 18840), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        require(step('browser-canary', ['node', '--experimental-strip-types', '/opt/ui188/canary.mts'], 110) == 0, 'BLOCKED_BROWSER_CANARY')
    finally:
        server.shutdown()
        server.server_close()
    (ROOT / 'closed-canary.json').write_text(json.dumps({'process_denied': denied, 'db_positive': True,
        'browser_positive_and_denied': True, 'namespace': os.readlink('/proc/self/ns/net'), 'aggregate': 'PASS'}) + '\n')
    RECORDER.canary_postprocess(STREAM_IDS or ('pending-S1','pending-S2'))


def main():
    global RECORDER,STAGES,LOGS,STREAM_IDS
    identity = read_bound()
    validate_identity(identity, identity)
    require(os.environ['UI188_RUN_UUID'] == identity['run_uuid'] and os.environ['UI188_PROBE_ATTEMPT'] == identity['case'], 'BLOCKED_RUN_IDENTITY')
    case = identity['case']
    # Fresh host-created root must contain no evidence of a previous run.
    require({p.name for p in ROOT.iterdir()} == {'identity.json','capacity.json','preparation-internet-control.json','network-setup.json','tap-broker.private.log','tap-broker-ready.json','runtime-privileges.json'}, 'BLOCKED_OLD_EVIDENCE')
    require(json.loads((ROOT / 'preparation-internet-control.json').read_text())['VM_internet_positive'] is True, 'BLOCKED_CANARY_POSITIVE_CONTROL')
    (ROOT / 'chain-runner').symlink_to('/r6/cp', target_is_directory=True)
    shutil.copyfile('/r6/build-provenance.json', ROOT / 'fixed-formal-binary-provenance.json')
    shutil.copyfile('/r6/environment.json', ROOT / 'environment.json')
    shutil.copyfile('/r6/producer-sources.json',ROOT/'producer-sources.json')
    sources=json.loads((ROOT/'producer-sources.json').read_text())['sources']
    RECORDER=Recorder(ROOT,identity,sources,CHAIN,Path('/opt/ui188'))
    STAGES=RECORDER.rows;LOGS=RECORDER.logs
    canary()
    runtime = sampler = s2 = state = None
    liveness_stop = threading.Event()
    liveness_thread = None
    try:
        sampler = start('sampler', script('receiver_observation'))
        runtime = start('runtime', script('runtime'))
        wait_file('acceptance-live-' + case + '.json', [runtime])
        step('cache-preflight', ['node', '--experimental-strip-types', str(CHAIN / 'probe_cover_cache.mts')], 120)
        step('audit-preflight',['node','--experimental-strip-types',str(CHAIN/'audit_preflight.mts')],120)
        s2 = start('s2', script('s2'))
        wait_file('s2-live-' + case + '.json', [runtime, s2])
        STREAM_IDS=stream_ids(ROOT,identity)
        step('live-authority', script('capture-live-authority'), 90)
        expected_processes = source_processes()
        liveness = {'same_source_processes': True, 'samples': 0}
        def sample_liveness():
            with (ROOT / 'source-liveness.private.jsonl').open('x') as output:
                while not liveness_stop.is_set():
                    try:
                        same = source_processes() == expected_processes
                    except Exception:
                        same = False
                    liveness['samples'] += 1
                    liveness['same_source_processes'] &= same
                    output.write(json.dumps({'monotonic_ns': time.monotonic_ns(), 'same_source_processes': same}) + '\n')
                    output.flush()
                    liveness_stop.wait(1)
        overlap = time.monotonic()
        liveness_thread = threading.Thread(target=sample_liveness, daemon=True)
        liveness_thread.start()
        first=start('browser-S1',['node','--experimental-strip-types',str(CHAIN/'browser.mts')],{'UI188_BROWSER_SLOT':'S1'})
        second=start('browser-S2',['node','--experimental-strip-types',str(CHAIN/'browser.mts')],{'UI188_BROWSER_SLOT':'S2'})
        settle('browser-S1',first,1250)
        settle('browser-S2',second,1250)
        # A browser failure does not skip independent S5, stop, archive or receiver proof.
        browser('s5')
        while time.monotonic() - overlap < 1000:
            require(runtime.poll() is None and s2.poll() is None, 'BLOCKED_SOURCE_EXIT')
            require(not (ROOT / 'host-stop-requested.json').exists(), 'BLOCKED_SAFETY_STOP')
            time.sleep(.5)
        held = time.monotonic() - overlap
        require(source_processes() == expected_processes and runtime.poll() is None and s2.poll() is None, 'BLOCKED_SOURCE_EXIT')
        liveness_stop.set()
        liveness_thread.join(timeout=5)
        require(not liveness_thread.is_alive() and liveness['same_source_processes'], 'BLOCKED_SOURCE_LIFETIME')
        RECORDER.builtin('overlap',lambda:(ROOT/('simultaneous-hold-'+case+'.json')).write_text(json.dumps({'seconds':held,'required_seconds':1000,'host_stop':False,'same_source_processes':True,'samples':liveness['samples'],'source_identities':expected_processes})+'\n'),STREAM_IDS)
        def capture_hls():
            capture_root=Path(os.environ['UI188_HLS_CAPTURE_ROOT']);capture_root.mkdir(exist_ok=False);proof=[]
            for prefix in ('acceptance-live-','s2-live-'):
                live=json.loads((ROOT/(prefix+case+'.json')).read_text());source=Path(live['archive_root'])/'tmp'/live['stream_id']/'preview'
                target=capture_root/live['stream_id'];shutil.copytree(source,target)
                proof.append({'stream_id':live['stream_id'],'files':[{'name':p.name,'sha256':sha_file(p)} for p in sorted(target.iterdir()) if p.is_file()]})
            (ROOT/('hls-snapshot-'+case+'.json')).write_text(json.dumps({'identity':identity,'streams':proof})+'\n')
        RECORDER.builtin('hls-snapshot',capture_hls,STREAM_IDS)
        state = start('state', script('s4_state'))
        browser('s4')
        settle('state', state, 240)
        state = None
        step('final-proof', script('capture-final-runtime-proof'), 90)
    except Exception:
        STAGES['driver'] = {'original_exit': 1}
        save_stages()
    finally:
        liveness_stop.set()
        if liveness_thread is not None:
            liveness_thread.join(timeout=5)
        for marker in ('s2-finished-' + case, 'chain-finished-' + case):
            (ROOT / marker).touch()
        for label, process in [('state', state), ('s2', s2), ('runtime', runtime), ('sampler', sampler)]:
            if process is not None:
                settle(label, process, 50)
    if not (ROOT / 'host-stop-requested.json').exists():
        media_inputs=[]
        def capture_inputs():
            source=Path('/r6/diagnostic/consumer-'+case+'.jsonl');shutil.copyfile(source,ROOT/('consumer-'+case+'.jsonl'))
            for suffix in ('a','b'):
                shutil.copyfile(Path('/r6/private-'+identity['schema'])/('audio-bot-'+suffix+'.jsonl'),ROOT/('audio-'+case+'-bot-'+suffix+'.jsonl'))
            state_proof=json.loads((ROOT/('s4-state-success-'+case+'.json')).read_text())
            for index,(key,owner) in enumerate([('recording1','encoder-b'),('recording2','encoder-a')]):
                recording = Path(state_proof[key]['path'])
                allowed = Path('/r6/data/' + owner + '-archives-' + case)
                require(recording.is_relative_to(allowed) and recording.resolve().is_relative_to(allowed) and not recording.is_symlink(), 'BLOCKED_RECEIVER_MEDIA_PATH')
                digest = sha_file(recording)
                require(digest == state_proof[key]['sha256_before_after'], 'BLOCKED_RECEIVER_MEDIA_CHANGED')
                media_inputs.append({'path':str(recording),'sha256':digest,'bytes':recording.stat().st_size,'stream_id':STREAM_IDS[index],'owner':owner})
            (ROOT/'media-inputs.json').write_text(json.dumps(media_inputs)+'\n')
        try:RECORDER.builtin('capture',capture_inputs,STREAM_IDS or ('pending-S1','pending-S2'))
        except Exception:pass # Preserve capture failure; still run both independent analyses.
        for slot in ('S1','S2'):step('media-'+slot,script('media'),700,{'UI188_MEDIA_SLOT':slot})
        RECORDER.freeze()
        try:
            expected=required_inputs(ROOT,identity)
            sealed=seal(ROOT,identity,[ROOT/name for name in expected],expected=expected)
            (ROOT/'receiver-inputs-sealed.json').write_text(json.dumps(sealed,indent=2)+'\n')
            formal_inputs(ROOT,identity,sealed)
        except Exception:
            STAGES['receiver-input-gate']={'original_exit':1};save_stages()
            sealed=None
        before = sha_file(Path('/r6/build-provenance.json'))
        receiver_exit=step('receiver',script('receiver_acceptance'),350) if sealed is not None else None
        if sealed is None:
            step('receiver-diagnostic',script('receiver_acceptance'),350,{'UI188_RECEIVER_DIAGNOSTIC':'1'})
        if sealed is not None:formal_inputs(ROOT,identity,sealed)
        for item in media_inputs:
            require(sha_file(Path(item['path'])) == item['sha256'] and Path(item['path']).stat().st_size == item['bytes'], 'BLOCKED_RECEIVER_MEDIA_CHANGED')
        expected_binaries = json.loads(Path('/r6/build-provenance.json').read_text())['formal_binaries']
        require(all(sha_file(Path('/r6/bin') / item['binary']) == item['sha256'] for item in expected_binaries), 'BLOCKED_RECEIVER_BINARY_CHANGED')
        require(before == sha_file(Path('/r6/build-provenance.json')), 'BLOCKED_RECEIVER_BINARY_CHANGED')
        (ROOT / 'formal-receiver.json').write_text(json.dumps({'identity': identity, 'original_exit': receiver_exit,
            'receiver_invocation_uuid':STAGES.get('receiver',{}).get('invocation_uuid'),
            'sealed_inputs_sha256':sha_file(ROOT/'receiver-inputs-sealed.json') if sealed is not None else None,'unchanged_after':sealed is not None,
            'binary_manifest_sha256': before, 'media_input_count': len(media_inputs)}) + '\n')
    from evaluate import evaluate
    summary = evaluate(ROOT, identity, STAGES)
    from stages26 import evaluate as logical_stages
    summary['quality_aggregate']=summary['aggregate']
    summary['logical_stages']=logical_stages(ROOT,identity,summary,STAGES)
    summary['logical_denominator']=26
    # Normal container/Worker/broker shutdown is observed by the host afterwards.
    summary['aggregate']='NOT_RUN' if summary['quality_aggregate']=='PASS' else 'FAIL'
    (ROOT / 'result.json').write_text(json.dumps(summary, indent=2) + '\n')
    return 0 if summary['quality_aggregate'] == 'PASS' else 1


if __name__ == '__main__':
    code = 1
    try:
        code = main()
    except Exception:
        STAGES['gate-or-analysis'] = {'original_exit': 1}
        save_stages()
    finally:
        (ROOT / 'runtime-finished').touch()
        for log in LOGS:
            log.close()
    sys.exit(code)
