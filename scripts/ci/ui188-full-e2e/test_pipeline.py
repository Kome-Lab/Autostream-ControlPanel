"""Execute real producer code with bounded synthetic inputs, then consume it."""
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch
from cover_contract import verify_cover
from gate import Blocked
from port import transform
from receiver_contract import validate as receiver_validate
from run_identity import encoded,environment,read_bound
from test_contracts import Fixture

HERE=Path(__file__).resolve().parent


class ProducerPipelineTests(Fixture):
    def data(self,slot):
        live={'stream_id':'ddc1fac3-9fb0-4b81-b2a1-631c5a7a333'+('1' if slot=='S1' else '2'),'status':'live',
              'cover':{'job_generation':1},'visual':{'candidates':[{'variant_id':v} for v in ('red','blue','green')]}}
        phases=[{'phase':p,'state':{'status':'applied','desired_revision':i+1,'applied_revision':i+1,'job_generation':1,'applied_active':i<3,
                 'applied_asset_variant_id':('red','blue','green',None)[i]}} for i,p in enumerate(('A','B','C','OFF'))]
        frames=[{'protected':i<30,'center':[255,0,0] if i<30 else [50,50,50],'watermark':[255,255,255]} for i in range(60)]
        return {'slot':slot,'live':live,'frames':frames,'observed':{'sid':live['stream_id'],'normalSandbox':True,'consoleErrors':0,'narrow':{'width':390},
            'phases':phases,'frameCount':60,'protectedFrameCount':30,'lateCheckpoints':[{'threshold':n,'before':{'time':n,'mediaTime':n,'frameCount':10},
            'after':{'time':n+3,'mediaTime':n+3,'frameCount':12,'error':None,'paused':False,'ready':4}} for n in (580,900)]}}

    def node(self,data,env=None):
        return subprocess.run(['node','--experimental-strip-types',str(HERE/'test_cover_producer.mts')],input=json.dumps(data),text=True,
            env={**os.environ,**environment(self.ident,self.root/'identity.json'),**(env or {})},capture_output=True,timeout=15)

    def test_actual_node_cover_producer_to_python_consumer_for_both_streams(self):
        for slot in ('S1','S2'):
            with self.subTest(slot=slot):
                data=self.data(slot);process=self.node(data)
                self.assertEqual(process.returncode,0,process.stderr)
                value=json.loads(process.stdout)
                self.assertTrue(verify_cover(value,self.ident,slot,data['live']))
                for mutate in (lambda v:v['hls'].pop(),lambda v:v['revisions'].pop(),lambda v:v.update(producer='browser-S1' if slot=='S2' else 'browser-S2'),
                               lambda v:v['identity'].update(github_run_id=9),lambda v:v.update(job_generation=99) if slot=='S2' else v.update(off_frames=0)):
                    changed=copy.deepcopy(value);mutate(changed)
                    with self.assertRaises((Blocked,ValueError)):verify_cover(changed,self.ident,slot,data['live'])

    def test_actual_node_producer_rejects_missing_hls_phases_frames_or_wrong_identity(self):
        for slot in ('S1','S2'):
            for part in ('hls','phase','generation','revision','frames','watermark'):
                with self.subTest(slot=slot,part=part):
                    value=self.data(slot)
                    if part=='hls':value['observed']['lateCheckpoints'].pop()
                    elif part=='phase':value['observed']['phases'].pop()
                    elif part=='generation':value['observed']['phases'][2]['state']['job_generation']=2
                    elif part=='revision':value['observed']['phases'][2]['state']['applied_revision']=0
                    elif part=='frames':value['frames'].pop()
                    else:value['frames'][0]['watermark']=[0,0,0]
                    self.assertNotEqual(self.node(value).returncode,0)
        for env in ({'UI188_GITHUB_RUN_ATTEMPT':'2'},{'UI188_PROBE_ATTEMPT':'cb16r'},{'UI188_DB_SCHEMA':'old'}):
            self.assertNotEqual(self.node(self.data('S1'),env).returncode,0)

    def test_actual_receiver_diagnostic_output_is_never_formal_evidence(self):
        case=self.ident['case'];sids=[]
        for slot,prefix in (('S1','acceptance-live-'),('S2','s2-live-')):
            live=self.data(slot)['live'];sids.append(live['stream_id']);self.write(prefix+case+'.json',live)
        self.write('s4-state-success-'+case+'.json',{k:{'path':'missing-owned-recording','sha256_before_after':'a'*64} for k in ('recording1','recording2')})
        self.write('consumer-'+case+'.jsonl',[])
        # A JSONL sampler has zero rows here, rather than one JSON array row.
        (self.root/('consumer-'+case+'.jsonl')).write_text('')
        self.write('simultaneous-hold-'+case+'.json',{'seconds':0,'host_stop':False})
        actual=HERE.parent/'ui188-r4-chain/receiver_acceptance.py'
        script=self.root/'receiver_acceptance.py';script.write_text(transform(actual.name,actual.read_text()))
        env={**os.environ,**environment(self.ident,self.root/'identity.json'),'UI188_EVIDENCE_DIRECTORY':str(self.root),'PYTHONPATH':str(HERE),'UI188_RECEIVER_DIAGNOSTIC':'1'}
        process=subprocess.run([sys.executable,'-B',str(script)],env=env,capture_output=True,timeout=10)
        self.assertEqual(process.returncode,1)
        value=json.loads((self.root/('receiver-diagnostic-'+case+'.json')).read_text())
        self.assertEqual(value['aggregate'],'FAIL');self.assertEqual(set(value['receivers']),{'S1','S2'})
        media=[{'stream_id':sid,'owner':owner,'sha256':'a'*64} for sid,owner in zip(sids,('encoder-b','encoder-a'))]
        with self.assertRaises(Blocked):receiver_validate(value,self.ident,sids,media)
        # Consume the actual producer's FAIL payload to exercise the stream/hash
        # join independently of the explicit diagnostic-producer rejection.
        contract_input=copy.deepcopy(value);contract_input['producer']='receiver'
        self.assertEqual(receiver_validate(contract_input,self.ident,sids,media)['aggregate'],'FAIL')
        for slot in ('S1','S2'):
            changed=copy.deepcopy(contract_input);changed['receivers'][slot]['recording_sha256']='b'*64
            with self.subTest(slot=slot),self.assertRaises(Blocked):receiver_validate(changed,self.ident,sids,media)
        env.pop('UI188_RECEIVER_DIAGNOSTIC')
        process=subprocess.run([sys.executable,'-B',str(script)],env=env,capture_output=True,timeout=10)
        self.assertNotEqual(process.returncode,0)
        self.assertFalse((self.root/('receiver-acceptance-'+case+'.json')).exists())

    def test_python_manifest_env_duplicates_and_closed_legacy_identity_rejected(self):
        env=environment(self.ident,self.root/'identity.json')
        self.assertEqual(read_bound(env=env),self.ident)
        for key in env:
            changed=dict(env);changed[key]='old'
            with self.subTest(key=key),self.assertRaises((ValueError,KeyError,OSError)):read_bound(env=changed)
        raw=encoded(self.ident);duplicate=raw.replace(b'"schema_version":2',b'"schema_version":2,"schema_version":2')
        import hashlib
        (self.root/'identity.json').write_bytes(duplicate);env['UI188_IDENTITY_SHA256']=hashlib.sha256(duplicate).hexdigest()
        with self.assertRaises(ValueError):read_bound(env=env)

    def test_all_checked_ports_reject_zero_replacements_and_double_application(self):
        names=json.loads((HERE/'port-inputs.json').read_text())
        for name in names:
            with self.subTest(name=name):
                raw=(HERE.parent/'ui188-r4-chain'/name).read_text();proof=[]
                updated=transform(name,raw,proof);self.assertTrue(proof)
                with self.assertRaises(ValueError):transform(name,'# missing original anchors\n')
                with self.assertRaises(ValueError):transform(name,updated)
