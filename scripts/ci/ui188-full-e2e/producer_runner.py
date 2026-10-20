"""Actual process receipts; original failures and incomplete producer outputs are retained."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import uuid
from gate import require
from producer_contract import SOURCES,file_record,outputs


class Recorder:
    def __init__(self,root,identity,sources,chain,platform):
        self.root=root;self.identity=identity;self.sources=sources;self.chain=chain;self.platform=platform
        self.rows={};self.logs=[]

    def save(self):
        (self.root/'stages.json').write_text(json.dumps(self.rows,indent=2)+'\n')

    def verify_sources(self):
        from seal import sha_file
        for name,digest in self.sources.items():
            parts=name.split('/')
            require(len(parts)==2 and parts[0] in ('chain','platform') and parts[1] not in ('','.','..'),'BLOCKED_PRODUCER_SOURCE')
            target=(self.chain if parts[0]=='chain' else self.platform)/parts[1]
            require(sha_file(target)==digest,'BLOCKED_PRODUCER_SOURCE_DRIFT')

    def begin(self,label,pid,source,kind):
        require(label not in self.rows,'BLOCKED_STAGE_REUSE')
        require(source in self.sources,'BLOCKED_PRODUCER_SOURCE')
        self.rows[label]={'producer':label,'invocation_uuid':str(uuid.uuid4()),'identity':self.identity,'kind':kind,'pid':pid,
            'started_ns':time.time_ns(),'finished_ns':None,'original_exit':None,'observed_process_exit':None,
            'source':source,'source_sha256':self.sources[source],'outputs':[]}
        self.save()

    def start(self,label,args,env=None):
        self.verify_sources()
        require(label in SOURCES or label=='receiver','BLOCKED_UNKNOWN_PRODUCER')
        require(not (self.root/'host-stop-requested.json').exists(),'BLOCKED_SAFETY_STOP')
        source=SOURCES.get(label,'chain/receiver_acceptance.py')
        target=(self.chain if source.startswith('chain/') else self.platform)/source.split('/',1)[1]
        from seal import sha_file
        require(sha_file(target)==self.sources.get(source),'BLOCKED_PRODUCER_SOURCE_DRIFT')
        expected=['node','--experimental-strip-types',str(target)] if target.suffix=='.mts' else ['python3','-B',str(target)]
        require(list(args)==expected,'BLOCKED_PRODUCER_COMMAND')
        self.begin(label,os.getpid(),source,'PROCESS')
        log=(self.root/(label+'.private.log')).open('xb');self.logs.append(log)
        process=subprocess.Popen(args,cwd=self.chain.parents[2],env={**os.environ,**(env or {})},stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
        self.rows[label]['pid']=process.pid;self.save()
        return process

    def finish(self,label,code,observed,sids=None):
        row=self.rows[label]
        require(row['original_exit'] is None,'BLOCKED_STAGE_REUSE')
        row.update(original_exit=code,observed_process_exit=observed)
        if sids is not None and label in SOURCES:
            expected=outputs(self.identity,sids).get(label)
            if expected is None:expected=[('receiver-diagnostic-' if label=='receiver-diagnostic' else 'receiver-acceptance-')+self.identity['case']+'.json']
            for name in expected:
                if (self.root/name).is_file():row['outputs'].append(file_record(self.root,name))
        row['finished_ns']=time.time_ns();self.save()
        return code

    def settle(self,label,process,seconds,sids=None):
        try:code=process.wait(timeout=seconds)
        except subprocess.TimeoutExpired:
            code=124
            if os.name=='posix':os.killpg(process.pid,signal.SIGTERM)
            else:process.terminate()
            try:process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                if os.name=='posix':os.killpg(process.pid,signal.SIGKILL)
                else:process.kill()
                process.wait(timeout=5)
        return self.finish(label,code,process.returncode,sids)

    def canary_postprocess(self,sids):
        row=self.rows['browser-canary']
        require(row['original_exit']==row['observed_process_exit']==0,'BLOCKED_BROWSER_CANARY')
        row['outputs']=[file_record(self.root,name) for name in outputs(self.identity,sids)['browser-canary']]
        row['finished_ns']=time.time_ns();self.save()

    def builtin(self,label,call,sids):
        self.verify_sources()
        self.begin(label,os.getpid(),'platform/execute.py','IN_PROCESS')
        code=1
        try:call();code=0
        finally:self.finish(label,code,code,sids)
        return code

    def freeze(self):
        value={'schema_version':1,'identity':self.identity,'producers':list(self.rows.values())}
        (self.root/'pre-receiver-producers.json').write_text(json.dumps(value,indent=2)+'\n')
        return value
