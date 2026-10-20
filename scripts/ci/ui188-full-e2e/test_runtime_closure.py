"""Portable fault contracts; actual Linux privilege/PID1 proof remains the smoke job."""
import copy
import hashlib
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import time
import unittest

from checkpoints import PHASES, atomic_json, begin, finish, observe, project, read_checkpoint
from gate import Blocked
from launch_parent import run, stop_owned
from run_identity import create, encoded, environment

HERE=Path(__file__).resolve().parent


class Fixture(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='ui188-closure-')
        self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name)
        self.identity=create('a'*64,{name:str(i)*40 for i,name in enumerate(('cp','bot','encoder','worker'),1)},100,
                             job='contract-smoke')
        (self.root/'identity.json').write_bytes(encoded(self.identity))

    def child(self,text,*args):
        return [sys.executable,'-B','-c',text,*map(str,args)]

    def env(self):
        return {**os.environ,**environment(self.identity,self.root/'identity.json'),
                'PYTHONPATH':str(HERE),'DISPLAY':''}


class CheckpointTests(Fixture):
    def test_absence_and_started_unfinished_never_pass(self):
        self.assertEqual(set(project(self.root,self.identity)),set(PHASES))
        self.assertEqual({v['status'] for v in project(self.root,self.identity).values()},{'NOT_RUN'})
        started=begin(self.root,self.identity,'db')
        self.assertEqual(project(self.root,self.identity)['db']['status'],'RUNNING')
        finish(self.root,self.identity,'db',started)
        self.assertEqual(project(self.root,self.identity)['db']['status'],'PASS')
        self.assertEqual(project(self.root,self.identity)['go_constructor']['status'],'NOT_RUN')

    def test_actual_refused_db_preserves_errno_and_downstream_absence(self):
        with socket.socket() as unused:
            unused.bind(('127.0.0.1',0));port=unused.getsockname()[1]
        with self.assertRaises(OSError):
            with observe(self.root,self.identity,'db'):
                socket.create_connection(('127.0.0.1',port),timeout=.2)
        row=project(self.root,self.identity)['db']
        self.assertEqual(row['status'],'FAIL')
        if os.name=='posix':self.assertTrue(0 < row['errno'] <= 4095)
        else:self.assertTrue(row['errno'] is None or 0 < row['errno'] <= 4095)
        self.assertEqual(row['original_exit'],1)
        self.assertEqual(project(self.root,self.identity)['worker_config']['status'],'NOT_RUN')

    def test_observed_subprocess_exit_and_timeout_preserved(self):
        with self.assertRaises(subprocess.CalledProcessError):
            with observe(self.root,self.identity,'db'):
                subprocess.run(self.child('raise SystemExit(19)'),check=True,timeout=3)
        self.assertEqual(project(self.root,self.identity)['db']['original_exit'],19)
        with self.assertRaises(subprocess.TimeoutExpired):
            with observe(self.root,self.identity,'go_constructor'):
                subprocess.run(self.child('import time;time.sleep(5)'),timeout=.1,check=True)
        row=project(self.root,self.identity)['go_constructor']
        self.assertEqual(row['original_exit'],124)
        self.assertIsNone(row['signal'])

    def test_duplicate_stale_unknown_and_false_pass_rejected(self):
        started=begin(self.root,self.identity,'db')
        original=finish(self.root,self.identity,'db',started,original_exit=7)
        changes=[lambda p:p.update(raw_log='SYNTHETIC_PRIVATE'),lambda p:p.update(status='PASS'),
                 lambda p:p.update(original_exit=True),lambda p:p.update(duration_ms=-1),
                 lambda p:p.update(at_ns=0),lambda p:p.update(signal=65),lambda p:p.update(errno=4096),
                 lambda p:p.update(pid=False),lambda p:p['identity'].update(github_run_id=101)]
        path=self.root/'checkpoint-db.json'
        for change in changes:
            with self.subTest(change=changes.index(change)):
                value=copy.deepcopy(original);change(value);atomic_json(path,value)
                with self.assertRaises(Blocked):project(self.root,self.identity)
        path.write_text('{"schema_version":1,"schema_version":1}')
        with self.assertRaises(ValueError):project(self.root,self.identity)

    def test_phase_reuse_and_missing_or_changed_proof_rejected(self):
        started=begin(self.root,self.identity,'entry_setup')
        with self.assertRaises(Blocked):begin(self.root,self.identity,'entry_setup')
        with self.assertRaises(Blocked):finish(self.root,self.identity,'entry_setup',started,proofs=('network_setup',))
        atomic_json(self.root/'network-setup.json',{'numeric':1})
        finish(self.root,self.identity,'entry_setup',started,proofs=('network_setup',))
        self.assertEqual(project(self.root,self.identity)['entry_setup']['status'],'PASS')
        with self.assertRaises(Blocked):finish(self.root,self.identity,'entry_setup',started)
        atomic_json(self.root/'network-setup.json',{'numeric':2})
        with self.assertRaises(Blocked):project(self.root,self.identity)

    def test_wrong_phase_and_unknown_proof_rejected(self):
        started=begin(self.root,self.identity,'db')
        with self.assertRaises(Blocked):finish(self.root,self.identity,'db',started,proofs=('../private.log',))
        row=finish(self.root,self.identity,'db',started)
        row['phase']='worker_ready';atomic_json(self.root/'checkpoint-db.json',row)
        with self.assertRaises(Blocked):project(self.root,self.identity)


class ProcessTests(Fixture):
    def test_actual_child_failure_preserved_and_output_private(self):
        value=run(self.root,self.identity,self.child("print('SYNTHETIC_PRIVATE_CHILD_OUTPUT');raise SystemExit(7)"),2)
        self.assertEqual((value['primary_role'],value['original_exit'],value['cleanup_exit']),('payload',7,0))
        self.assertEqual(value['payload_exit'],7)
        self.assertNotIn('SYNTHETIC_PRIVATE_CHILD_OUTPUT',json.dumps(value))
        self.assertIn('SYNTHETIC_PRIVATE_CHILD_OUTPUT',(self.root/'launcher-child.private.log').read_text())

    def test_missing_xvfb_launch_rejected_without_success_receipt(self):
        value=run(self.root,self.identity,[str(self.root/'nonexistent-xvfb-run')],1)
        self.assertEqual((value['primary_role'],value['original_exit']),('launch',127))
        self.assertIsNone(value['payload_exit'])
        self.assertEqual(project(self.root,self.identity)['xvfb_ready']['status'],'NOT_RUN')

    def test_actual_payload_deadline_retains_primary_and_cleanup(self):
        started=time.monotonic()
        value=run(self.root,self.identity,self.child('import time;time.sleep(5)'),.15)
        self.assertLess(time.monotonic()-started,3)
        self.assertEqual((value['primary_role'],value['original_exit'],value['cleanup_exit']),('deadline',124,0))
        self.assertIsNone(value['signal'])
        self.assertIsNotNone(value['payload_exit'])

    def test_actual_broker_early_death_stops_payload_promptly(self):
        with subprocess.Popen(self.child('import time;time.sleep(.1);raise SystemExit(23)')) as broker:
            started=time.monotonic()
            value=run(self.root,self.identity,self.child('import time;time.sleep(5)'),4,
                      child_watch=(('broker',broker.poll),))
            broker.wait(timeout=2)
        self.assertLess(time.monotonic()-started,2)
        self.assertEqual((value['primary_role'],value['original_exit']),('broker',23))
        self.assertNotEqual(value['payload_exit'],0)
        self.assertEqual(value['cleanup_exit'],0)

    @unittest.skipUnless(hasattr(os,'waitid') and hasattr(os,'WNOWAIT'),'Linux non-reaping child observation requires waitid')
    def test_actual_normal_broker_exit_during_payload_finish_remains_success(self):
        from launch_parent import _broker_exit,retire_broker
        script=('import pathlib,sys,time;from checkpoints import atomic_json;from run_identity import read_bound;'
                'root=pathlib.Path(sys.argv[1]);identity=read_bound();time.sleep(.1);'
                'atomic_json(root/"driver-exit.json",{"identity":identity,"original_exit":0});'
                '(root/"runtime-finished").touch();atomic_json(root/"tap-broker-exit.json",{"identity":identity,"original_exit":0});')
        with subprocess.Popen(self.child(script,self.root),env=self.env()) as broker:
            value=run(self.root,self.identity,self.child('import time;time.sleep(.35)'),2,
                      child_watch=(('broker',lambda:_broker_exit(self.root,self.identity,broker.pid)),))
            self.assertEqual((value['primary_role'],value['original_exit'],value['cleanup_exit']),('payload',0,0))
            self.assertEqual(retire_broker(self.root,self.identity,broker.pid),0)
        bad=self.root/'false-broker-proof';bad.mkdir()
        atomic_json(bad/'tap-broker-exit.json',{'identity':self.identity,'original_exit':0})
        with subprocess.Popen(self.child('raise SystemExit(7)')) as failed:
            self.assertEqual(retire_broker(bad,self.identity,failed.pid),1)
        self.assertEqual(json.loads((bad/'broker-owned-exit.json').read_text())['original_exit'],7)

    def test_bad_worker_config_actual_parser_failure_stops_payload(self):
        config=self.root/'worker-config';config.mkdir();(config/'credentials').mkdir()
        (config/'config.yml').write_text('bootstrap: isolated-worker-only\n')
        (config/'credentials/node-listener.json').write_text('{bad json')
        worker_log=(self.root/'worker-fault.private.log').open('wb')
        self.addCleanup(worker_log.close)
        script='import pathlib,sys,bootstrap_probe;from run_identity import read_bound;bootstrap_probe.worker_probe(pathlib.Path(sys.argv[1]),pathlib.Path(sys.argv[2]),read_bound(),{})'
        with subprocess.Popen(self.child(script,self.root,config),env=self.env(),stdout=worker_log,stderr=subprocess.STDOUT) as worker:
            value=run(self.root,self.identity,self.child('import time;time.sleep(5)'),4,
                      child_watch=(('worker',worker.poll),))
            worker.wait(timeout=2)
        self.assertEqual((value['primary_role'],value['original_exit']),('worker',1))
        self.assertNotEqual(value['payload_exit'],0)

    @unittest.skipUnless(os.name=='posix','POSIX signal escalation is measured on Linux CI')
    def test_actual_shutdown_timeout_is_not_normal_cleanup(self):
        ready=self.root/'ready'
        child=subprocess.Popen(self.child('import signal,time,pathlib,sys;signal.signal(signal.SIGTERM,signal.SIG_IGN);pathlib.Path(sys.argv[1]).touch();time.sleep(10)',ready),
                               start_new_session=True)
        self.addCleanup(lambda:child.kill() if child.poll() is None else None)
        end=time.monotonic()+2
        while not ready.exists() and time.monotonic()<end:time.sleep(.01)
        self.assertTrue(ready.exists())
        self.assertEqual(stop_owned(child,grace=.1),1)
        self.assertEqual(child.returncode,-signal.SIGKILL)

    def test_actual_driver_failure_and_deadline_receipts(self):
        from runtime_supervisor import run_driver
        code,cleanup=run_driver(self.root,self.identity,self.child('raise SystemExit(31)'),2)
        self.assertEqual((code,cleanup),(31,0))
        row=json.loads((self.root/'driver-exit.json').read_text())
        self.assertEqual((row['original_exit'],row['payload_exit']),(31,31))
        self.assertEqual(project(self.root,self.identity)['driver_exit']['status'],'FAIL')

    def test_retire_cannot_replace_observed_primary_exit(self):
        from runtime_supervisor import retire
        atomic_json(self.root/'driver-exit.json',{'identity':self.identity,'original_exit':7})
        before=(self.root/'driver-exit.json').read_bytes()
        atomic_json(self.root/'tap-broker-exit.json',{'identity':self.identity,'original_exit':0,'issued_taps':0})
        self.assertEqual(retire(self.root,self.identity,8),1)
        self.assertEqual((self.root/'driver-exit.json').read_bytes(),before)
        receipt=json.loads((self.root/'supervisor-exit.json').read_text())
        self.assertEqual(receipt['original_driver_exit'],7)
        self.assertEqual(receipt['cleanup_reason'],'DRIVER_EXIT_CONFLICT')

    def test_actual_supervisor_initial_rejection_still_runs_cleanup(self):
        begin(self.root,self.identity,'xvfb_ready')
        command=self.child('import pathlib,sys,runtime_supervisor;sys.exit(runtime_supervisor.main(pathlib.Path(sys.argv[1])))',self.root)
        with (self.root/'supervisor-fault.private.log').open('wb') as log:
            result=subprocess.run(command,env=self.env(),stdout=log,stderr=subprocess.STDOUT,timeout=5)
        self.assertNotEqual(result.returncode,0)
        receipt=json.loads((self.root/'supervisor-exit.json').read_text())
        self.assertIsNone(receipt['original_driver_exit'])
        self.assertEqual(receipt['cleanup_exit'],1)
        self.assertTrue((self.root/'runtime-finished').is_file())
        self.assertEqual(project(self.root,self.identity)['supervisor']['status'],'FAIL')
        self.assertEqual(project(self.root,self.identity)['driver_exit']['status'],'NOT_RUN')

    def test_actual_constructor_nonzero_retains_exit_without_fake_producer(self):
        from adapter_smoke import run_actual
        destination=self.root/'go-contract'
        with self.assertRaises(Blocked):run_actual(Path(sys.executable),destination,self.identity)
        row=json.loads((destination/'go-constructor-exit.json').read_text())
        self.assertEqual(row['original_exit'],2)
        self.assertFalse(row['deadline_expired'])
        self.assertEqual(row['identity'],self.identity)
        self.assertFalse((destination/'go-constructor-proof.json').exists())

    def test_actual_broker_initial_exception_has_both_phase_and_exit(self):
        command=self.child('import pathlib,sys,tap_broker;sys.exit(tap_broker.serve(pathlib.Path(sys.argv[1])))',self.root)
        with (self.root/'broker-fault.private.log').open('wb') as log:
            result=subprocess.run(command,env=self.env(),stdout=log,stderr=subprocess.STDOUT,timeout=5)
        self.assertNotEqual(result.returncode,0)
        row=json.loads((self.root/'tap-broker-exit.json').read_text())
        self.assertEqual(row['original_exit'],1)
        observations=project(self.root,self.identity)
        self.assertEqual((observations['broker_ready']['status'],observations['broker_exit']['status']),('FAIL','FAIL'))
        self.assertEqual(observations['go_constructor']['status'],'NOT_RUN')


if __name__=='__main__':
    unittest.main()
