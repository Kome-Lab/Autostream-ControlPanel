"""Closed failure projection and live-monitor decisions; no Linux runtime claim."""
import copy
import hashlib
import json
import os
from pathlib import Path
import time
from unittest.mock import patch
from checkpoints import PHASES,begin,finish
from gate import Blocked
from launcher_compare import validate_comparison
import linux_smoke
from linux_smoke import CHECKS,EXITS,checkpoint_projection,load_public,validate_public,wait_service
from test_contracts import Fixture


class SmokeClosureTests(Fixture):
    def summary(self):
        return {'schema_version':2,'kind':'CONTRACT_SMOKE','run_uuid':self.ident['run_uuid'],'case':self.ident['case'],
            'github_run_id':self.ident['github_run_id'],'github_run_attempt':1,'source_shas':{k:self.ident['source_shas'][k] for k in ('cp','bot')},
            'checks':{k:'NOT_RUN' for k in CHECKS},'original_exits':{},'aggregate':'FAIL','full_e2e':'NOT_RUN',
            'known_issue':'KNOWN_UNRESOLVED_MEDIA_ISSUE','private_raw_storage':'NOT_CONFIGURED','proof_sha256':{},'container_exits':{},
            'failure':None,'timeline_ms':{},'source_lock_sha256':self.ident['source_lock_sha256'],
            'identity_sha256':hashlib.sha256((self.root/'identity.json').read_bytes()).hexdigest(),
            'checkpoints':{p:{'status':'NOT_RUN'} for p in PHASES},'launcher_comparison':{'status':'NOT_RUN'},
            'cleanup_failures':{},'observed_exits':{},'primary_process_failure':None}

    def test_failure_schema_retains_unstarted_phases_without_inventing_exits(self):
        value=self.summary();validate_public(value)
        self.assertEqual(len(value['checks']),9);self.assertEqual(len(value['checkpoints']),15)
        self.assertTrue(all(r=={'status':'NOT_RUN'} for r in value['checkpoints'].values()))
        for mutate in (lambda v:v.update(schema_version=1),lambda v:v.update(raw_credential='private'),
                       lambda v:v['checkpoints'].update(private_log='private'),
                       lambda v:v.update(cleanup_failures={'stop':0}),
                       lambda v:v.update(primary_process_failure={'role':'worker-b','original_exit':1,'signal':9}),
                       lambda v:v.update(observed_exits={'runtime':{'exit':0,'running':True,'oom':False}})):
            bad=copy.deepcopy(value);mutate(bad)
            with self.subTest(value=bad),self.assertRaises(Blocked):validate_public(bad)

    def test_zero_exits_and_hashes_cannot_replace_phase_or_launcher_execution(self):
        value=self.summary();value.update(aggregate='PASS',checks={k:'PASS' for k in CHECKS},
            original_exits={k:0 for k in EXITS},proof_sha256={k:'a'*64 for k in CHECKS},
            container_exits={k:{'exit':0,'oom':False,'running':False} for k in ('runtime','worker-b','db')})
        with self.assertRaises(Blocked):validate_public(value)

    def test_public_consumer_rejects_duplicate_stale_run_attempt_and_source(self):
        good=self.summary();load_public(json.dumps(good).encode(),self.ident)
        for key,value in (('github_run_id',99),('github_run_attempt',2),('github_run_attempt',True),('run_uuid','66ef039c-304f-4c06-9788-fb15b3e8b774'),('source_lock_sha256','b'*64),('identity_sha256','b'*64)):
            bad=copy.deepcopy(good);bad[key]=value
            with self.subTest(key=key),self.assertRaises((Blocked,ValueError)):
                load_public(json.dumps(bad).encode(),self.ident)
        bad=copy.deepcopy(good);bad['source_shas']['cp']='f'*40
        with self.assertRaises(Blocked):load_public(json.dumps(bad).encode(),self.ident)
        raw=json.dumps(good).encode()[:-1]+b',"aggregate":"PASS"}'
        with self.assertRaises(ValueError):load_public(raw,self.ident)

    def test_comparison_requires_real_candidate_readiness_and_same_package(self):
        def facts(pid,ppid):return {'pid':pid,'ppid':ppid,'uids':[1000]*4,'cap_eff':0,'cap_bnd':0,'cap_amb':0,'nnp':1}
        def observation(parent):return {'schema_version':1,'package':'XVFB','package_version_numbers':[2,21,1,7,3,12,10],
            'package_version_sha256':'a'*64,'xvfb_run_sha256':'b'*64,'xvfb_sha256':'c'*64,
            'pid1_role':'SUPERVISOR' if parent else 'XVFB_RUN','pid1':facts(1,0),'observer':facts(5,2),
            'xvfb':[facts(3,2 if parent else 1)],'ready':parent,'payload_started':parent,'payload_exit':0 if parent else None}
        value={'status':'PASS','image_sha256':'d'*64,'historical_cause':'NOT_ESTABLISHED','cases':{
            case:{'original_exit':0 if parent else 124,'timed_out':not parent,'container_exit':0 if parent else 137,
                  'duration_ms':500 if parent else 11000,'deadline_ms':10000,'proof_sha256':'e'*64,'observation':observation(parent)}
            for case,parent in (('XVFB_PID1',False),('SUPERVISOR_PID1',True))}}
        validate_comparison(value)
        for mutate in (lambda v:v['cases'].pop('XVFB_PID1'),
                       lambda v:v['cases']['SUPERVISOR_PID1']['observation'].update(ready=False),
                       lambda v:v['cases']['SUPERVISOR_PID1']['observation'].update(payload_exit=None),
                       lambda v:v['cases']['SUPERVISOR_PID1']['observation']['xvfb'][0].update(ppid=1),
                       lambda v:v['cases']['SUPERVISOR_PID1']['observation'].update(xvfb_sha256='f'*64),
                       lambda v:v['cases']['SUPERVISOR_PID1']['observation']['pid1'].update(cap_eff=8192),
                       lambda v:v['cases']['SUPERVISOR_PID1']['observation'].update(raw_log='private')):
            bad=copy.deepcopy(value);mutate(bad)
            with self.subTest(value=bad),self.assertRaises(Blocked):validate_comparison(bad)

    def test_checkpoint_projection_detects_stale_duplicate_and_tampered_proof(self):
        mark=begin(self.root,self.ident,'entry_setup')
        self.write('network-setup.json',{'db_ipv4':'192.0.2.1'})
        finish(self.root,self.ident,'entry_setup',mark,proofs=('identity','network_setup'))
        self.assertEqual(checkpoint_projection(self.root,self.ident)['entry_setup']['status'],'PASS')
        self.assertEqual(checkpoint_projection(self.root,self.ident)['go_constructor'],{'status':'NOT_RUN'})
        (self.root/'identity.json').write_bytes((self.root/'identity.json').read_bytes()+b' ')
        self.assertEqual(checkpoint_projection(self.root,self.ident)['entry_setup']['status'],'INVALID')
        self.write('identity.json',self.ident)
        path=self.root/'checkpoint-entry_setup.json'
        good=json.loads(path.read_text());old=copy.deepcopy(good);old['identity']['github_run_id']=99
        path.write_text(json.dumps(old))
        self.assertEqual(checkpoint_projection(self.root,self.ident)['entry_setup']['status'],'INVALID')
        path.write_text(json.dumps(good)[:-1]+',"phase":"db"}')
        self.assertEqual(checkpoint_projection(self.root,self.ident)['entry_setup']['status'],'INVALID')

    def test_monitor_observes_worker_early_death_and_preserves_its_exit(self):
        observed={};exits={};live={'exit':None,'oom':False,'running':True}
        state={'runtime':live,'db':live,'worker-b':{'exit':7,'oom':False,'running':False}}
        with patch.object(linux_smoke,'WORK_DEADLINE',time.monotonic()+20),patch.object(linux_smoke,'inspect_services',return_value=state) as inspect:
            with self.assertRaisesRegex(Blocked,'BLOCKED_SMOKE_EARLY_CHILD'):
                wait_service([],self.root,self.ident,'runtime','runtime-wait',exits,observed)
        self.assertEqual(inspect.call_count,1)
        failure=json.loads((self.root/'primary-process-failure.json').read_text())
        self.assertEqual(failure,{'role':'worker-b','original_exit':7,'signal':None})
        self.assertIsNone(observed['runtime']['exit'])

    def test_monitor_deadline_does_not_invent_a_container_signal_or_exit(self):
        observed={};exits={};live={'exit':None,'oom':False,'running':True}
        with patch.object(linux_smoke,'WORK_DEADLINE',time.monotonic()-1),patch.object(linux_smoke,'inspect_services',return_value={k:live for k in ('runtime','worker-b','db')}):
            with self.assertRaises(Blocked):wait_service([],self.root,self.ident,'runtime','runtime-wait',exits,observed)
        self.assertEqual(exits['runtime-wait'],124)
        self.assertIsNone(observed['runtime']['exit'])
        self.assertFalse((self.root/'primary-process-failure.json').exists())


if __name__=='__main__':
    import unittest
    unittest.main()
