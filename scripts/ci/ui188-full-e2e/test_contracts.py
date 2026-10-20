"""Synthetic unit fixtures only. These tests are never Full E2E evidence."""
import copy
import hashlib
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
import zipfile
import sys

from gate import Blocked, GROUPS, authorize, capacity, validate_identity
from evaluate import archive, evaluate, stop_receipt
from port import transform
from publish import package_public, project, scan_public, status_png, validate_summary, verify_archive
from seal import seal, verify_seal, sha_file
from source import tree_hash
from run_identity import create,encoded


def identity():
    return create('a'*64,{k:str(i)*40 for i,k in enumerate(('cp','bot','encoder','worker'),1)},100,
        '66ef039c-304f-4c06-9788-fb15b3e8b773',time.time_ns()-10000000)


def approval_env():
    approval = {'cp_sha': '1'*40, 'bot_sha': '2'*40, 'lock_sha256': 'a'*64, 'approval_id': 'UI188-approved-20261020',
                'runner_label': 'ui188-linux-8core', 'billing_confirmed': True, 'max_minutes': 120}
    return {'GITHUB_EVENT_NAME': 'workflow_dispatch', 'GITHUB_REF': 'refs/heads/main',
            'GITHUB_REPOSITORY': 'Kome-Lab/Autostream-ControlPanel', 'GITHUB_RUN_ATTEMPT': '1',
            'UI188_ENABLED': 'true', 'COST_ACK': 'true', 'CP_SHA': approval['cp_sha'], 'BOT_SHA': approval['bot_sha'],
            'LOCK_SHA256': approval['lock_sha256'], 'APPROVAL_ID': approval['approval_id'], 'UI188_APPROVED_DISPATCH': json.dumps(approval)}


def stop_fixture(sid):
    return {'before': {'id': sid}, 'outcome': {'stream': {'id': sid}, 'stop_confirmed': True},
            'stopPOSTs': 1, 'detail': {'status': 'ready'}, 'listed': {'status': 'ready'},
            'actualLostResponse': [{'status': 200}], 'confirmation': {'inputs': 0}}


def archive_fixture(sid, owner):
    start = '2026-10-20T00:00:00Z'
    artifacts = [{'id': str(i), 'kind': kind, 'stream_id': sid, 'archive_run_id': 'same-run', 'archive_started_at': start} for i, kind in enumerate(('archive', 'metadata', 'logs'))]
    return {'stream': {'id': sid, 'archive_run_id': 'same-run', 'archive_started_at': start}, 'artifacts': artifacts,
            'DB_artifact_authority': [{**r, 'source_service_id': owner} for r in artifacts],
            'DB_original_exit': 0, 'DB_detail_original_exit': 0, 'DB_current_run_counts': [3, 3, 1, 1],
            'transport': {'service_id': owner, 'stream_id': sid, 'actual_upstream_status': 202, 'body_sha256': 'c'*64, 'forwarded_body_sha256': 'c'*64}}


class Fixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='ui188-unit-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.ident = identity()
        self.write('identity.json', self.ident)
    def write(self, name, value):
        (self.root / name).write_bytes(encoded(value) if name=='identity.json' else (json.dumps(value)+'\n').encode())


class GateTests(Fixture):
    def test_approved_exact_source_set(self):
        self.assertEqual(authorize(approval_env())['cp_sha'], '1'*40)

    def test_rejects_unauthorized_events_forks_refs_retries_and_cost(self):
        mutations = [('GITHUB_EVENT_NAME', 'push'), ('GITHUB_EVENT_NAME', 'pull_request_target'), ('GITHUB_EVENT_NAME', 'pull_request'),
                     ('GITHUB_REPOSITORY', 'attacker/Autostream-ControlPanel'), ('GITHUB_REF', 'refs/heads/feature'),
                     ('GITHUB_RUN_ATTEMPT', '2'), ('UI188_ENABLED', ''), ('COST_ACK', 'false'),
                     ('CP_SHA', 'main'), ('CP_SHA', '1'*40+';echo injected'), ('BOT_SHA', '3'*40),
                     ('LOCK_SHA256', 'b'*64), ('APPROVAL_ID', 'wrong'), ('UI188_APPROVED_DISPATCH', '{}')]
        for key, value in mutations:
            with self.subTest(key=key, value=value):
                env = approval_env(); env[key] = value
                with self.assertRaises(Blocked):
                    authorize(env)

    def test_rejects_unconfirmed_runner_or_billing(self):
        for key, value in [('billing_confirmed', False), ('runner_label', 'self-hosted'), ('max_minutes', 121)]:
            with self.subTest(key=key):
                env = approval_env(); raw = json.loads(env['UI188_APPROVED_DISPATCH']); raw[key] = value
                env['UI188_APPROVED_DISPATCH'] = json.dumps(raw)
                with self.assertRaises(Blocked):
                    authorize(env)

    def test_capacity_measured_and_standard_runner_rejected(self):
        capacity(8, 32*2**30, 250*2**30, 'Linux')
        for args in [(4,16*2**30,14*2**30,'Linux'), (8,29*2**30,250*2**30,'Linux'), (8,32*2**30,179*2**30,'Linux'), (8,32*2**30,250*2**30,'Windows')]:
            with self.subTest(args=args):
                with self.assertRaisesRegex(Blocked, 'BLOCKED_RUNNER_CAPACITY'):
                    capacity(*args)

    def test_no_run_or_source_identity_substitution(self):
        validate_identity(self.ident, self.ident)
        for key, value in [('run_uuid','other'), ('case','old'), ('source_lock_sha256','b'*64), ('github_run_id',99), ('github_run_attempt',2)]:
            with self.subTest(key=key):
                stale = {**self.ident, key: value}
                with self.assertRaises(Blocked):
                    validate_identity(stale, self.ident)


class AuthorityTests(Fixture):
    def test_normal_stop_receipt(self):
        stop_receipt(stop_fixture('S1'), 'S1')

    def test_stop_rejects_resend_unknown_wrong_owner_and_typed_confirmation(self):
        for mutate in [lambda p:p.update(stopPOSTs=2), lambda p:p['outcome'].update(stop_confirmed=False),
                       lambda p:p['outcome']['stream'].update(id='S2'), lambda p:p['confirmation'].update(inputs=1),
                       lambda p:p['listed'].update(status='live')]:
            proof = stop_fixture('S1'); mutate(proof)
            with self.subTest(proof=proof):
                with self.assertRaises(Blocked):
                    stop_receipt(proof, 'S1')

    def test_archive_three_per_stream_actual_owner(self):
        archive(archive_fixture('S1', 'encoder-b'), 'S1', 'encoder-b')

    def test_archive_rejects_wrong_owner_run_count_catalog_or_exit(self):
        changes = [lambda p:p['DB_artifact_authority'][0].update(source_service_id='encoder-a'),
                   lambda p:p['artifacts'][0].update(archive_run_id='old-run'), lambda p:p['DB_current_run_counts'].__setitem__(0,2),
                   lambda p:p['artifacts'].pop(), lambda p:p['transport'].update(actual_upstream_status=500),
                   lambda p:p.update(DB_original_exit=1), lambda p:p['DB_artifact_authority'][0].update(archive_started_at='2026-10-19T00:00:00Z')]
        for index, mutate in enumerate(changes):
            with self.subTest(index=index):
                proof = archive_fixture('S1', 'encoder-b'); mutate(proof)
                with self.assertRaises(Blocked):
                    archive(proof,'S1','encoder-b')

    def test_canonical_git_tree_hash_and_path_escape(self):
        self.assertEqual(tree_hash([]), '4b825dc642cb6eb9a060e54bf8d69288fbee4904')
        # Independently known Git blob identity for hello newline.
        self.assertEqual(hashlib.sha1(b'blob 6\0hello\n').hexdigest(), 'ce013625030ba8dba906f756967f9e9ca394464a')
        for name in ('../outside', '/absolute', 'a/../b', 'C:/private', 'a\\b'):
            with self.subTest(name=name):
                with self.assertRaises(Blocked):
                    tree_hash([{'path':name,'mode':'100644','oid':'1'*40}])

    def test_sealed_receiver_inputs_unchanged(self):
        self.write('input.json', {'numeric_observation':10})
        record = seal(self.root,self.ident,[self.root/'input.json'])
        verify_seal(self.root,record,self.ident)
        self.write('input.json',{'numeric_observation':11})
        with self.assertRaisesRegex(Blocked,'BLOCKED_SEAL_CHANGED'):
            verify_seal(self.root,record,self.ident)

    def test_rejects_old_evidence_mtime_and_seal_traversal(self):
        self.write('input.json',{})
        os.utime(self.root/'input.json',ns=(0,0))
        with self.assertRaisesRegex(Blocked,'BLOCKED_STALE_RUN'):
            seal(self.root,self.ident,[self.root/'input.json'])
        record = {'schema_version':2,'kind':'FILE_INTEGRITY_ONLY','identity':self.ident,'files':[{'path':'../outside','sha256':'a'*64,'bytes':1}]}
        with self.assertRaisesRegex(Blocked,'BLOCKED_SEAL_PATH'):
            verify_seal(self.root,record,self.ident)

    def test_seal_rejects_absolute_windows_ads_and_backslash_paths(self):
        for name in ('/outside', 'C:/private/file', 'input.json:secret', 'nested\\file'):
            with self.subTest(name=name):
                record = {'schema_version':2,'kind':'FILE_INTEGRITY_ONLY','identity':self.ident,'files':[{'path':name,'sha256':'a'*64,'bytes':1}]}
                with self.assertRaises(Blocked):
                    verify_seal(self.root,record,self.ident)

    def test_failed_tool_output_stays_private_original_exit_preserved(self):
        from host import command
        sentinel='SYNTHETIC_PRIVATE_LOG_ONLY_20261020'
        with self.assertRaisesRegex(Blocked,'BLOCKED_PREPARATION_OR_RUNTIME'):
            command([sys.executable,'-c','import sys;print('+repr(sentinel)+');sys.exit(7)'],self.root,'unit-tool',timeout=10)
        self.assertIn(sentinel,(self.root/'unit-tool.private.log').read_text())
        self.assertEqual(json.loads((self.root/'unit-tool.exit.json').read_text())['original_exit'],7)

    def test_linux_port_retains_acceptance_thresholds(self):
        chain=Path(__file__).resolve().parent.parent/'ui188-r4-chain'
        text = (chain/'runtime.py').read_text()
        changed = transform('runtime.py',text)
        self.assertIn("SCHEMA == UI188_IDENTITY['schema']",changed)
        media=transform('media_independent.py',(chain/'media_independent.py').read_text())
        self.assertIn("[(\'250_400\',250,400),(\'580_700\',580,700),(\'800_950\',800,950)]",media)
        final = transform('capture-final-runtime-proof.py',(chain/'capture-final-runtime-proof.py').read_text())
        self.assertNotIn('ui188_r9_b5',final)
        self.assertNotIn('runtime-test-credentials.json',final)


class ParserTests(Fixture):
    def complete(self):
        case=self.ident['case']; sid='S1'; sid2='S2'
        self.write('acceptance-live-'+case+'.json',{'stream_id':sid,'status':'live'})
        self.write('s2-live-'+case+'.json',{'stream_id':sid2,'stream':{'status':'live'},'runner_start_posts':0,'voice_join_delta':1,'s1_stream_id':sid})
        self.write('s0-negative-b3.json',{'aggregate':'PASS'})
        self.write('create-required-service-negative-'+case+'.json',{'aggregate':'PASS','cases':[{},{}]})
        self.write('simultaneous-hold-'+case+'.json',{'seconds':1000,'host_stop':False,'same_source_processes':True,'samples':1000})
        self.write('browser-chain-cover-success-'+case+'-1.json',{'sid':sid,'phases':[{'phase':p} for p in ['A','B','C','OFF']], 'consoleErrors':0,'offObservations':[{}], 'lateCheckpoints':[{'threshold':580},{'threshold':900}], 'narrow':{'width':390}})
        self.write('s4-state-success-'+case+'.json',{'all_active_rows_deleted':True,'normal_stops_no_resend':True,'no_existing_presence_restart':True})
        self.write('s4-stop-success-'+case+'-1.json',stop_fixture(sid))
        self.write('s4-delete-success-'+case+'-1.json',{'confirmation':{'inputs':0}})
        self.write('s5-success-'+case+'-1.json',{'scope':'full S5','consoleErrors':0,'native200':{'dpr':2},'cancelPUTs':0,'realUnboundImage':{'status':403},'realMissingPreset':{'status':404},'japaneseState':'日本語実状態','auditPending':True})
        self.write('r9-audit-pending-same-run-'+case+'.json',{'immutable':True})
        self.write('r9-stale-authority-'+case+'.json',[{'CP_start_calls':0},{'CP_start_calls':0}])
        self.write('r9-report-accepted-'+sid+'-'+case+'.json',archive_fixture(sid,'encoder-b'))
        self.write('r9-report-accepted-'+sid2+'-'+case+'.json',archive_fixture(sid2,'encoder-a'))
        self.write('media-verification-'+case+'.json',{'aggregate':'PASS','analyses':{'hls_recording_identity':{'first_picture_identity_verified':True},'target_frames':{'status':'PASS'},'independent_all_frames':{'count':30000}}})
        receiver={'aggregate':'PASS','receivers':{}}
        for name in ('S1','S2'):
            receiver['receivers'][name]={'RTP':{'count':50000,'discontinuities':[],'packet_rate':50,'observer':{'tap_drops':0}},
                'max_kernel_drops':0,'actual_receive_buffer':{'samples':200,'longest_sampled_90percent_saturation_seconds':0},
                'AAC':{'pass':True,'max_uncovered':0},'late_PCM_bands':{f'{lo}-{hi}':{'pass':True,'count':hi-lo+1} for lo,hi in [(250,400),(580,700),(800,950)]}}
        self.write('receiver-acceptance-'+case+'.json',receiver)
        self.write('final-runtime-proof-'+case+'.json',{'all_five_formal_binary_bytes_unchanged':True})
        self.write('same-run-live-authority-'+case+'.json',{'aggregate':'PASS'})
        record=seal(self.root,self.ident,[self.root/('s0-negative-b3.json')])
        self.write('receiver-inputs-sealed.json',record)
        self.write('formal-receiver.json',{'identity':self.ident,'original_exit':0,'unchanged_after':True,'media_input_count':2,'sealed_inputs_sha256':sha_file(self.root/'receiver-inputs-sealed.json')})
        stages={k:{'original_exit':0} for k in ['runtime','s2','browser-S1','browser-S2','s5','s4','state','media-S1','media-S2','receiver','live-authority','final-proof']}
        return stages

    def test_legacy_synthetic_files_do_not_prove_all_28_groups(self):
        result=evaluate(self.root,self.ident,self.complete())
        self.assertEqual(result['denominator'],28)
        self.assertEqual(result['aggregate'],'FAIL')
        self.assertEqual(set(result['groups']),set(GROUPS))
        self.assertNotIn('PASS',set(result['groups'].values()))

    def test_missing_is_not_run_zero_test_not_pass(self):
        result=evaluate(self.root,self.ident,{})
        self.assertEqual(set(result['groups'].values()),{'NOT_RUN'})
        self.assertNotEqual(result['aggregate'],'PASS')

    def test_nonzero_stays_failed_independent_results_retained(self):
        stages=self.complete();stages['browser-S1']['original_exit']=1
        result=evaluate(self.root,self.ident,stages)
        self.assertEqual(result['groups']['S3'],'FAIL')
        self.assertNotEqual(result['groups']['S5'],'PASS')
        self.assertNotEqual(result['groups']['AAC'],'PASS')
        self.assertEqual(result['aggregate'],'FAIL')

    def test_rejects_shortened_hold_hls_drop_aac_pcm_and_preset_only(self):
        mutations=[('simultaneous-hold-',lambda p:p.update(seconds=999)),
                   ('receiver-acceptance-',lambda p:p['receivers']['S2'].update(max_kernel_drops=1)),
                   ('receiver-acceptance-',lambda p:p['receivers']['S1']['AAC'].update(max_uncovered=.051)),
                   ('receiver-acceptance-',lambda p:p['receivers']['S2']['late_PCM_bands']['800-950'].update(count=150))]
        for prefix,mutate in mutations:
            with self.subTest(prefix=prefix):
                stages=self.complete();filename=prefix+self.ident['case']+'.json';p=json.loads((self.root/filename).read_text());mutate(p);self.write(filename,p)
                self.assertEqual(evaluate(self.root,self.ident,stages)['aggregate'],'FAIL')
        stages=self.complete();p=json.loads((self.root/('s5-success-'+self.ident['case']+'-1.json')).read_text());p['scope']='checkpoint only';self.write('s5-success-'+self.ident['case']+'-1.json',p)
        self.assertNotEqual(evaluate(self.root,self.ident,stages)['groups']['S5'],'PASS')


class PublicationTests(Fixture):
    def summary(self):
        return project(self.root)
    def candidate(self, secret='SYNTHETIC_ONLY_CREDENTIAL_20261020'):
        target=self.root/'public';target.mkdir(exist_ok=True)
        summary=self.summary()
        (target/'summary.json').write_text(json.dumps(summary))
        (target/'summary.png').write_bytes(status_png(summary))
        return target,secret

    def test_safe_projection_drops_private_fields_and_logs(self):
        stages=ParserTests(methodName='test_missing_is_not_run_zero_test_not_pass')
        self.write('secret.private.log',{'Cookie':'SECRET_VALUE'})
        summary=self.summary();validate_summary(summary)
        self.assertNotIn('Cookie',json.dumps(summary))
        self.assertEqual(summary['private_evidence'],'PRIVATE_EVIDENCE_NOT_UPLOADED')
        target,secret=self.candidate();scan_public(target,[secret])

    def test_rejects_unexpected_raw_media_log_db_and_directories(self):
        target,secret=self.candidate()
        for name in ('recording.mkv','original.log','dump.sql','nested'):
            with self.subTest(name=name):
                p=target/name
                if name=='nested':p.mkdir()
                else:p.write_bytes(b'private')
                with self.assertRaisesRegex(Blocked,'BLOCKED_PUBLIC_PATH_ALLOWLIST'):
                    scan_public(target,[secret])
                if p.is_dir():p.rmdir()
                else:p.unlink()

    def test_rejects_synthetic_secret_in_permitted_sha(self):
        # A secret that fits an otherwise permitted SHA field still must be rejected.
        target,_=self.candidate();summary=self.summary();summary['source_shas']['cp']='f'*40
        (target/'summary.json').write_text(json.dumps(summary));(target/'summary.png').write_bytes(status_png(summary))
        with self.assertRaisesRegex(Blocked,'BLOCKED_PUBLIC_SECRET_SCAN'):
            scan_public(target,['f'*40])

    def test_unknown_json_and_unreviewed_png_rejected(self):
        target,secret=self.candidate();summary=self.summary();summary['nonce']='raw'
        (target/'summary.json').write_text(json.dumps(summary))
        with self.assertRaisesRegex(Blocked,'BLOCKED_PUBLIC_SCHEMA'):
            scan_public(target,[secret])
        (target/'summary.json').write_text(json.dumps(self.summary()));(target/'summary.png').write_bytes(b'unreviewed image')
        with self.assertRaisesRegex(Blocked,'BLOCKED_UNREVIEWED_PNG'):
            scan_public(target,[secret])

    def test_nonfinite_and_false_pass_rejected(self):
        summary=self.summary();summary['metrics']['S1']={'max_kernel_drops':float('nan')}
        with self.assertRaises(Blocked):validate_summary(summary)
        summary=self.summary();summary['aggregate']='PASS'
        with self.assertRaisesRegex(Blocked,'BLOCKED_FALSE_PASS'):validate_summary(summary)

    def test_package_outside_hash_crc_and_members(self):
        credentials=self.root/'credentials.json';credentials.write_text(json.dumps({'synthetic':'SYNTHETIC_ONLY_CREDENTIAL_20261020'}))
        output=self.root/'output';package_public(self.root,credentials,output)
        verified=json.loads((output/'VERIFIED.json').read_text())
        self.assertEqual(verified['sha256'],sha_file(output/'evidence.zip'))
        self.assertEqual(verified['payload_hashes_verified'],2)
        with zipfile.ZipFile(output/'evidence.zip') as archive:
            manifest=json.loads(archive.read('MANIFEST.json'))
            self.assertIsNone(archive.testzip())
        verify_archive(output/'evidence.zip',manifest)
        with zipfile.ZipFile(output/'evidence.zip','a') as archive:
            archive.writestr('../private.log','blocked')
        with self.assertRaisesRegex(Blocked,'BLOCKED_ARTIFACT_MEMBERS'):
            verify_archive(output/'evidence.zip',manifest)


if __name__ == '__main__':
    unittest.main()
