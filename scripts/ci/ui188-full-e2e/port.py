"""Checked environment-only edits to an image copy of the immutable R9 runner."""
import hashlib
import json
from pathlib import Path

HERE = Path(__file__).resolve().parent


def transform(name, text, proof=None):
    marker='UI188_ACTIONS_PORT_V2'
    if marker in text:
        raise ValueError('BLOCKED_PORT_ALREADY_APPLIED')
    edits = []
    def replace(before, after, count=1):
        nonlocal text
        actual = text.count(before)
        if actual != count or before == after:
            raise ValueError('BLOCKED_PORT_REPLACEMENT_COUNT:'+name)
        text = text.replace(before, after)
        edits.append({'before_sha256': hashlib.sha256(before.encode()).hexdigest(), 'expected': count, 'replaced': actual})
    if name == 'runtime.py':
        replace("assert SCHEMA == 'ui188_r9_b5'", "from run_identity import read_bound\nUI188_IDENTITY=read_bound()\nassert SCHEMA == UI188_IDENTITY['schema']")
        replace("'/evidence/resource_snapshot.py'", "'/opt/ui188/resource_snapshot.py'")
        replace("directory=PRIVATE/name;config=directory/'config.yml'", "assert UI188_IDENTITY['services'].get(name)==name\n directory=pathlib.Path('/worker-config') if name=='worker-b' else PRIVATE/name;config=directory/'config.yml'")
        replace("directory=PRIVATE/name;directory.mkdir(mode=0o700)", "directory=pathlib.Path('/worker-config') if name=='worker-b' else PRIVATE/name;directory.mkdir(mode=0o700,exist_ok=name=='worker-b')")
    elif name == 's0_negative.py':
        replace("schema == 'ui188_r9_b5' and attempt == 'b5'", "schema == __import__('run_identity').read_bound()['schema'] and attempt == __import__('run_identity').read_bound()['case']")
        replace("'run': 'B3'", "'run': attempt")
    elif name == 'receiver_observation.py':
        replace("sys.path.insert(0,'/evidence')", "sys.path.insert(0,'/opt/ui188')")
        replace("root.glob('private-ui188_r9_b5/tee-s*-progress-'+case+'.txt')", "root.glob('private-'+os.environ['UI188_DB_SCHEMA']+'/tee-s*-progress-'+case+'.txt')")
    elif name == 'capture-live-authority.py':
        replace("assert os.environ['UI188_DB_SCHEMA']=='ui188_r9_b5'", "assert os.environ['UI188_DB_SCHEMA']==__import__('run_identity').read_bound()['schema']")
        replace("str(root/'chain-runner/scripts/ci/ui188-r4-chain')", "'/r6/cp/scripts/ci/ui188-r4-chain'")
        # Replace only the old run-name locators; no authority/acceptance checks change.
        replace("'live-authority-b5'", "'live-authority-'+os.environ['UI188_PROBE_ATTEMPT']")
        for prefix in ('acceptance-live-', 's2-live-', 'chain-bot-b-runtime-sanitized-', 'same-run-live-authority-'):
            replace("'"+prefix+"b5.json'", "'"+prefix+"'+os.environ['UI188_PROBE_ATTEMPT']+'.json'")
        replace("'run':'B5'", "'run':os.environ['UI188_PROBE_ATTEMPT']")
        replace('SAME_B5_NORMAL_SECRET_FOLLOWUP_AND_LIVE_EPOCH_STABILITY_PASS','SAME_ACTIONS_RUN_NORMAL_SECRET_FOLLOWUP_AND_LIVE_EPOCH_STABILITY_PASS')
    elif name == 'capture-final-runtime-proof.py':
        replace("assert schema=='ui188_r9_b5'", "assert schema==__import__('run_identity').read_bound()['schema']")
        replace("(root/'runtime-test-credentials.json')", "Path(os.environ['UI188_TEST_CREDENTIALS'])")
    elif name == 'real_browser.mts':
        before = 'launchBrowserProcessWithRetry({browserPath:'
        replace(before, 'launchBrowserProcessWithRetry({manualZoom:"headed",browserPath:')
    elif name == 'packet_capture.py':
        replace('    assert 0 < port < 65536', "    from tap_broker import request_tap\n    return request_tap(port)\n\ndef original_open_rtp_header_tap(port):\n    assert 0 < port < 65536")
    elif name == 's2.py':
        replace("payload={'name':fixture_name,", "payload={'overlay_profile_id':live['visual']['overlay_profile_id'],'name':fixture_name,")
        replace("'visual_settings':{'expected_revision':0,", "'visual_settings':{**live['visual']['visual_settings'],'expected_revision':0,")
        replace("{'stream_id':sid,'stream':current,'cover':cover,", "{'stream_id':sid,'status':current['status'],'visual':live['visual'],'profile':live['profile'],'archive_root':live['archive_root'].replace('encoder-b-archives-','encoder-a-archives-'),'stream':current,'cover':cover,")
    elif name == 'browser.mts':
        replace('import assert from "node:assert/strict";', 'import assert from "node:assert/strict";\nimport {readIdentity,coverProof} from "/opt/ui188/cover_proof.mts";\nconst identity=readIdentity();\nconst slot=process.env.UI188_BROWSER_SLOT||"S1";\nassert.ok(slot==="S1"||slot==="S2");')
        replace('"acceptance-live-" + process.env.UI188_PROBE_ATTEMPT', '(slot==="S1"?"acceptance-live-":"s2-live-") + process.env.UI188_PROBE_ATTEMPT')
        replace('const attempt = process.env.UI188_PROBE_ATTEMPT + "-"', 'const attempt = process.env.UI188_PROBE_ATTEMPT + (slot==="S2"?"-s2":"") + "-"')
        replace('  console.log("REAL_BROWSER_COVER_HLS_PASS " + sid);', '  save("cover-contract-"+slot+"-"+identity.case+".json",coverProof(identity,slot,live,JSON.parse(readFileSync(resolve(evidence,"browser-chain-cover-success-"+attempt+".json"),"utf8")),frames));\n  console.log("REAL_BROWSER_COVER_HLS_PASS " + sid);')
    elif name == 'media_independent.py':
        replace("case=os.environ['UI188_PROBE_ATTEMPT'];attempt=case+'-'+os.environ['UI188_BROWSER_ATTEMPT']", "case=os.environ['UI188_PROBE_ATTEMPT'];slot=os.environ.get('UI188_MEDIA_SLOT','S1');suffix='-s2' if slot=='S2' else '';attempt=case+suffix+'-'+os.environ['UI188_BROWSER_ATTEMPT']")
        replace("root/('acceptance-live-'+case+'.json')", "root/(('s2-live-' if slot=='S2' else 'acceptance-live-')+case+'.json')")
        replace("'audio-analysis-'+case+'.json'", "'audio-analysis-'+case+suffix+'.json'")
        replace("'audio-analysis-'+case+'.original.log'", "'audio-analysis-'+case+suffix+'.original.log'")
        replace("'media-'+case", "'media-'+case+suffix")
        replace("stop=json.loads((root/('s4-stop-success-'+case+'-1.json')).read_text());assert stop['outcome']['stop_confirmed']", "stop=json.loads((root/('s4-state-success-'+case+'.json' if slot=='S2' else 's4-stop-success-'+case+'-1.json')).read_text());assert (stop['s2_outcome'] if slot=='S2' else stop['outcome'])['stop_confirmed']")
        replace("'media-verification-'+case+'.json'", "'media-verification-'+case+suffix+'.json'")
        replace("value={'stream_id':sid,'run_id':case,", "value={'schema_version':2,'identity':__import__('run_identity').read_bound(),'producer':'media-'+slot,'stream_id':sid,'run_id':case,")
    elif name == 'receiver_acceptance.py':
        replace("target=root/('audio-analysis-'+case+suffix+'.json')", "target=root/(('receiver-audio-analysis-' if suffix else 'audio-analysis-')+case+suffix+'.json')")
        replace("value={'case':case,'receivers':{},'failures':failures}", "diagnostic=os.environ.get('UI188_RECEIVER_DIAGNOSTIC')=='1';value={'schema_version':2,'identity':__import__('run_identity').read_bound(),'producer':'receiver-diagnostic' if diagnostic else 'receiver','case':case,'receivers':{},'failures':failures}\nfrom producer_contract import formal_inputs\nif not diagnostic:formal_inputs(root,value['identity'],json.loads((root/'receiver-inputs-sealed.json').read_text()))")
        replace("'receiver-acceptance-'+case+'.json'", "('receiver-diagnostic-' if diagnostic else 'receiver-acceptance-')+case+'.json'")
        replace("label='S1' if not suffix else 'S2';proof={};value['receivers'][label]=proof", "label='S1' if not suffix else 'S2';from producer_contract import stream_ids\n proof={'stream_id':stream_ids(root,value['identity'])[0 if not suffix else 1],'owner':'encoder-b' if not suffix else 'encoder-a','recording_sha256':state[recording_key]['sha256_before_after']};value['receivers'][label]=proof")
    else:
        raise ValueError('BLOCKED_UNKNOWN_PORT_INPUT')
    if not edits:
        raise ValueError('BLOCKED_EMPTY_PORT')
    if proof is not None:
        proof.extend(edits)
    return ('// ' if name.endswith('.mts') else '# ')+marker+'\n'+text


def main():
    chain = Path('/r6/cp/scripts/ci/ui188-r4-chain')
    expected = json.loads((HERE / 'port-inputs.json').read_text())
    records = []
    for name, digest in expected.items():
        target = chain / name
        data = target.read_bytes()
        assert hashlib.sha256(data).hexdigest() == digest, name
        edits = []
        updated = transform(name, data.decode(), edits).encode()
        target.write_bytes(updated)
        records.append({'file': name, 'original_sha256': digest, 'linux_sha256': hashlib.sha256(updated).hexdigest(), 'edits': edits})
    Path('/r6/port-proof.json').write_text(json.dumps(records, indent=2) + '\n')


if __name__ == '__main__':
    main()
