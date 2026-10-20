"""Missing, stale, duplicate and fabricated-success proof must fail closed."""
import copy
import hashlib
import json
import os
from pathlib import Path
import time
import unittest
from unittest.mock import patch
from acceptance import audio_dependencies
from gate import Blocked,GROUPS
from linux_smoke import CHECKS,EXITS,validate_public
from producer_contract import PRE_PRODUCERS,formal_inputs,outputs,required_inputs,validate_execution,validate_producers
from producer_runner import Recorder
from publish import project,validate_summary
from seal import seal,sha_file,verify_seal
from stages26 import CONTRACT,NAMES,evaluate as logical_evaluate,validate_shutdown
from test_contracts import Fixture


class EvidenceGateTests(Fixture):
    def test_cleanup_failure_preserves_original_driver_exit(self):
        from runtime_supervisor import retire
        with patch('runtime_supervisor.shutdown',side_effect=OSError('synthetic private detail')):
            self.assertEqual(retire(self.root,self.ident,7),1)
        driver=json.loads((self.root/'driver-exit.json').read_text());receipt=json.loads((self.root/'supervisor-exit.json').read_text())
        self.assertEqual(driver['original_exit'],7);self.assertEqual(receipt['original_driver_exit'],7)
        self.assertEqual(receipt['cleanup_reason'],'BROKER_CLEANUP_FAILED');self.assertNotIn('synthetic private detail',json.dumps(receipt))
        self.write('tap-broker-exit.json',{'identity':self.ident,'original_exit':0,'issued_taps':2})
        with patch('runtime_supervisor.shutdown'):
            self.assertEqual(retire(self.root,self.ident,0),0)
        self.assertEqual(json.loads((self.root/'supervisor-exit.json').read_text())['original_driver_exit'],0)

    def setup_streams(self):
        ids=('efefefef-efef-4fef-8fef-efefefefefe1','efefefef-efef-4fef-8fef-efefefefefe2')
        for sid,prefix in zip(ids,('acceptance-live-','s2-live-')):self.write(prefix+self.ident['case']+'.json',{'stream_id':sid})
        return ids

    def test_exact_required_set_rejects_empty_missing_extra_duplicate_and_old_run(self):
        self.setup_streams();required=required_inputs(self.root,self.ident)
        self.assertGreater(len(required),30)
        for name in required-{'identity.json'}:
            if not (self.root/name).exists():self.write(name,{'synthetic_unit_only':True})
        good=seal(self.root,self.ident,[self.root/n for n in required],expected=required)
        verify_seal(self.root,good,self.ident,expected=required)
        self.write('pre-receiver-producers.json',{'schema_version':1,'identity':self.ident,'producers':[]})
        good=seal(self.root,self.ident,[self.root/n for n in required],expected=required)
        verify_seal(self.root,good,self.ident,expected=required)
        # Byte integrity succeeds; completeness independently rejects no producers.
        with self.assertRaises(Blocked):formal_inputs(self.root,self.ident,good)
        for kind in ('empty','missing','extra','duplicate','old','unknown'):
            value=copy.deepcopy(good)
            if kind=='empty':value['files']=[]
            elif kind=='missing':value['files'].pop()
            elif kind=='extra':self.write('extra.json',{});value['files'].append({'path':'extra.json','sha256':sha_file(self.root/'extra.json'),'bytes':3})
            elif kind=='duplicate':value['files'].append(value['files'][0])
            elif kind=='old':value['identity']['github_run_id']=99
            else:value['unknown']=True
            with self.subTest(kind=kind),self.assertRaises((Blocked,ValueError)):verify_seal(self.root,value,self.ident,expected=required)
        with self.assertRaises(Blocked):seal(self.root,self.ident,[],expected=required)

    def test_recorder_executes_actual_node_process_and_rejects_fabricated_success(self):
        chain=self.root/'chain';chain.mkdir();script=chain/'browser.mts'
        expected=outputs(self.ident,self.setup_streams())['browser-S2']
        script.write_text("import {writeFileSync} from 'node:fs';\n"+''.join('writeFileSync('+json.dumps(str(self.root/name))+',JSON.stringify({synthetic_unit_only:true}));\n' for name in expected))
        source='chain/browser.mts';sources={source:sha_file(script)}
        recorder=Recorder(self.root,self.ident,sources,chain,self.root)
        process=recorder.start('browser-S2',['node','--experimental-strip-types',str(script)])
        self.assertEqual(recorder.settle('browser-S2',process,10,self.setup_streams()),0)
        for log in recorder.logs:log.close()
        row=recorder.rows['browser-S2']
        self.assertTrue(validate_execution(self.root,self.ident,row,expected,sources))
        for key,value in (('original_exit',7),('observed_process_exit',7),('observed_process_exit',False),('pid',0),('kind','NOT_EXECUTED'),('outputs',[]),('source_sha256','b'*64),('identity',{**self.ident,'github_run_id':99})):
            changed=copy.deepcopy(row);changed[key]=value
            with self.subTest(key=key),self.assertRaises((Blocked,ValueError)):validate_execution(self.root,self.ident,changed,expected,sources)
        with self.assertRaises(Blocked):validate_execution(self.root,self.ident,{'original_exit':0},expected,sources)
        with self.assertRaises(Blocked):recorder.start('browser-S2',['node','--experimental-strip-types',str(script)])
        with self.assertRaises(Blocked):recorder.start('browser-S1',['python3','-c','exit(0)'])
        # Rewriting a previously recorded output invalidates its evidence binding.
        (self.root/expected[0]).write_text('changed')
        with self.assertRaises(Blocked):validate_execution(self.root,self.ident,row,expected,sources)

    def test_formal_producer_denominator_and_unique_invocations_are_mandatory(self):
        for rows in ([],[{'producer':'runtime','invocation_uuid':'same'}]*18,[{'producer':p,'invocation_uuid':'same'} for p in PRE_PRODUCERS]):
            with self.subTest(rows=len(rows)),self.assertRaises(Blocked):validate_producers(self.root,self.ident,{'schema_version':1,'identity':self.ident,'producers':rows})

    def test_f07_rejects_each_required_audio_or_cover_dependency_fail_and_not_run(self):
        groups={name:'PASS' for name in GROUPS}
        self.assertTrue(audio_dependencies(groups))
        for name in ('COVER','HLS','FRAME','RTP','SOCKET','AAC','PCM'):
            for status in ('FAIL','NOT_RUN'):
                with self.subTest(group=name,status=status):self.assertFalse(audio_dependencies({**groups,name:status}))

    def test_26_logical_obligations_do_not_accept_zero_exit_only_or_missing_shutdown(self):
        self.assertEqual(len(CONTRACT),26);self.assertEqual(len(set(NAMES)),26)
        groups={'groups':{g:'PASS' for g in GROUPS}}
        value=logical_evaluate(self.root,self.ident,groups,{p:{'original_exit':0} for p in PRE_PRODUCERS})
        self.assertEqual(set(value),set(NAMES));self.assertNotIn('PASS',value.values());self.assertEqual(value['T26'],'NOT_RUN')
        with self.assertRaises((Blocked,KeyError)):validate_shutdown({'schema_version':1,'identity':self.ident,'services':{}},self.ident)

    def test_closed_failure_metadata_is_numeric_enum_hash_and_status_png_is_labeled(self):
        self.write('stages.json',{'browser-S2':{'original_exit':7,'started_ns':int(self.ident['started_ns'])+1000000,'finished_ns':int(self.ident['started_ns'])+9000000,
            'source_sha256':'c'*64,'invocation_uuid':'fixture','outputs':[],'private_log':'do not publish'}})
        value=project(self.root);validate_summary(value)
        self.assertEqual(value['image_kind'],'ENUM_STATUS_NOT_UI_CAPTURE');self.assertEqual(value['private_raw_storage'],'NOT_CONFIGURED')
        self.assertEqual(value['failure_metadata'][0]['duration_ms'],8)
        self.assertNotIn('private_log',json.dumps(value))
        for key,changed in (('log','private'),('source_sha256','private path'),('output_count',-1),('collection_scope','private')):
            bad=copy.deepcopy(value);bad['failure_metadata'][0][key]=changed
            with self.subTest(key=key),self.assertRaises(Blocked):validate_summary(bad)
        bad=copy.deepcopy(value);bad['aggregate']='PASS';bad['groups']={g:'PASS' for g in GROUPS}
        with self.assertRaises(Blocked):validate_summary(bad)

    def test_short_smoke_projection_rejects_unknown_paths_missing_proof_and_false_success(self):
        value={'schema_version':1,'kind':'CONTRACT_SMOKE','run_uuid':self.ident['run_uuid'],'case':self.ident['case'],'github_run_id':100,'github_run_attempt':1,
            'source_shas':{k:self.ident['source_shas'][k] for k in ('cp','bot')},'checks':{k:'NOT_RUN' for k in CHECKS},'original_exits':{},'aggregate':'FAIL',
            'full_e2e':'NOT_RUN','known_issue':'KNOWN_UNRESOLVED_MEDIA_ISSUE','private_raw_storage':'NOT_CONFIGURED','proof_sha256':{},'container_exits':{},'failure':None,'timeline_ms':{}}
        validate_public(value)
        for mutate in (lambda v:v.update(private_log='raw'),lambda v:v.update(aggregate='PASS'),lambda v:v.update(full_e2e='PASS'),
                       lambda v:v.update(checks={k:'PASS' for k in CHECKS},aggregate='PASS',original_exits={k:0 for k in EXITS})):
            bad=copy.deepcopy(value);mutate(bad)
            with self.assertRaises(Blocked):validate_public(bad)
