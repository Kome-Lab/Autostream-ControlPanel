"""One bounded standard-runner bootstrap; all tool/application output stays private."""
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time
from adapter_smoke import compile_actual
from source import verify
from gate import Blocked,HEX256,SHA,require,validate_identity
from run_identity import create,decode,encoded,environment
from seal import sha_file
from checkpoints import PHASES,PROOFS,begin,finish,read_checkpoint
from launcher_compare import compare,validate_comparison

HERE=Path(__file__).resolve().parent
CHECKS=('identity','go_constructor','uid_and_capabilities','setup_permissions_lost','secret_read','worker_isolation','db_readiness','filtered_rtp_fd','normal_shutdown')
EXITS=('compile','image','secret-owner','compose-config','up','runtime-wait','worker-wait','stop','read-owner','secret-read-owner','down','launcher-comparison')
ERROR_CODES=('BLOCKED_LINUX_BOOTSTRAP','BLOCKED_ACTUAL_GO_CONSTRUCTOR_COMPILE','BLOCKED_SMOKE_CONTAINER','BLOCKED_SMOKE_CONTAINER_OWNER',
    'BLOCKED_SMOKE_CONTAINER_EXIT','BLOCKED_SMOKE_IMAGE','BLOCKED_BOOTSTRAP_UID','BLOCKED_BOOTSTRAP_CAPABILITIES','BLOCKED_BOOTSTRAP_SETUP',
    'BLOCKED_BOOTSTRAP_WORKER','BLOCKED_BOOTSTRAP_DB_OR_RTP','BLOCKED_BOOTSTRAP_GO','BLOCKED_BOOTSTRAP_EXIT','BLOCKED_BOOTSTRAP_CONTAINER_EXIT',
    'CONTRACT_VALIDATION_FAILED','BLOCKED_SMOKE_EARLY_CHILD','BLOCKED_SMOKE_PHASE_EVIDENCE')
TIMELINE={}
PHASE='compile'
WORK_DEADLINE=None
PHASE_PROOFS={'entry_setup':{'identity','network_setup'},'privilege_drop':{'identity','init_privileges'},
    'broker_ready':{'broker_ready'},'runtime_privileges':{'runtime_privileges'},'db':{'db_readiness'},
    'worker_ready':{'worker_ready'},'filtered_rtp_fd':{'filtered_rtp_fd'},'go_constructor':{'constructor','constructor_exit'},
    'driver_exit':{'driver_exit'},'broker_exit':{'broker_exit'},'worker_exit':{'worker_exit'},
    'container_exit':{'shutdown_containers','launcher_exit','broker_owned_exit'}}


def checkpoint_projection(root,identity):
    result={}
    for phase in PHASES:
        path=root/('checkpoint-'+phase+'.json')
        if not path.exists() and not path.is_symlink():
            result[phase]={'status':'NOT_RUN'};continue
        try:
            row=read_checkpoint(path,identity)
            require(row['phase']==phase,'BLOCKED_SMOKE_PHASE_EVIDENCE')
            for name,digest in row['proof_sha256'].items():
                target=root/PROOFS[name]
                require(target.is_file() and not target.is_symlink() and sha_file(target)==digest,'BLOCKED_SMOKE_PHASE_EVIDENCE')
            require(row['status']!='PASS' or PHASE_PROOFS.get(phase,set())<=set(row['proof_sha256']),'BLOCKED_SMOKE_PHASE_EVIDENCE')
            result[phase]={k:v for k,v in row.items() if k not in ('schema_version','identity','phase')}
        except FileNotFoundError:result[phase]={'status':'NOT_RUN'}
        except Exception:result[phase]={'status':'INVALID','code':'IDENTITY_OR_SCHEMA_REJECTED'}
    return result


def validate_checkpoints(rows):
    require(set(rows)==set(PHASES),'BLOCKED_SMOKE_PHASE_EVIDENCE')
    for phase,row in rows.items():
        if row=={'status':'NOT_RUN'} or row=={'status':'INVALID','code':'IDENTITY_OR_SCHEMA_REJECTED'}:continue
        require(set(row)=={'status','at_ns','duration_ms','original_exit','signal','errno','pid','ppid','proof_sha256'} and row['status'] in ('RUNNING','PASS','FAIL'),'BLOCKED_SMOKE_PHASE_EVIDENCE')
        require(all(type(row[k]) is int and row[k]>=0 for k in ('at_ns','duration_ms','pid','ppid')) and row['at_ns']>0 and row['duration_ms']<=720000,'BLOCKED_SMOKE_PHASE_EVIDENCE')
        for key,maximum in (('signal',64),('errno',4095)):
            require(row[key] is None or type(row[key]) is int and 0<=row[key]<=maximum,'BLOCKED_SMOKE_PHASE_EVIDENCE')
        require(row['original_exit'] is None or type(row['original_exit']) is int and -255<=row['original_exit']<=255,'BLOCKED_SMOKE_PHASE_EVIDENCE')
        require(row['status']!='PASS' or row['original_exit']==0 and row['signal'] is None and row['errno'] is None,'BLOCKED_SMOKE_PHASE_EVIDENCE')
        require(row['status']!='RUNNING' or row['original_exit'] is None,'BLOCKED_SMOKE_PHASE_EVIDENCE')
        require(type(row['proof_sha256']) is dict and set(row['proof_sha256'])<=set(PROOFS) and all(HEX256.fullmatch(v) for v in row['proof_sha256'].values()),'BLOCKED_SMOKE_PHASE_EVIDENCE')
        require(row['status']!='PASS' or PHASE_PROOFS.get(phase,set())<=set(row['proof_sha256']),'BLOCKED_SMOKE_PHASE_EVIDENCE')
    return rows


def failure(error):
    code=str(error) if isinstance(error,Blocked) and str(error) in ERROR_CODES else 'CONTRACT_VALIDATION_FAILED'
    return {'stage':PHASE,'code':code}


def validate_public(value):
    require(set(value)=={'schema_version','kind','run_uuid','case','github_run_id','github_run_attempt','source_shas','checks','original_exits','aggregate','full_e2e','known_issue','private_raw_storage','proof_sha256','container_exits','failure','timeline_ms','source_lock_sha256','identity_sha256','checkpoints','launcher_comparison','cleanup_failures','observed_exits','primary_process_failure'},'BLOCKED_SMOKE_PUBLIC_SCHEMA')
    require(value['schema_version']==2 and value['kind']=='CONTRACT_SMOKE' and value['full_e2e']=='NOT_RUN' and value['known_issue']=='KNOWN_UNRESOLVED_MEDIA_ISSUE' and value['private_raw_storage']=='NOT_CONFIGURED','BLOCKED_SMOKE_PUBLIC_SCOPE')
    require(HEX256.fullmatch(value['source_lock_sha256']) and HEX256.fullmatch(value['identity_sha256']),'BLOCKED_SMOKE_PUBLIC_SOURCE')
    validate_checkpoints(value['checkpoints']);validate_comparison(value['launcher_comparison'])
    require(set(value['cleanup_failures'])<= {'stop','read-owner','secret-read-owner','down'} and all(type(v) is int and v!=0 and -255<=v<=255 for v in value['cleanup_failures'].values()),'BLOCKED_SMOKE_PUBLIC_CLEANUP')
    require(set(value['observed_exits'])<={'runtime','worker-b','db'},'BLOCKED_SMOKE_PUBLIC_EXIT')
    for row in value['observed_exits'].values():
        require(set(row)=={'exit','oom','running'} and type(row['running']) is bool and type(row['oom']) is bool and (row['exit'] is None if row['running'] else type(row['exit']) is int and -255<=row['exit']<=255),'BLOCKED_SMOKE_PUBLIC_EXIT')
    if value['primary_process_failure'] is not None:
        row=value['primary_process_failure'];require(set(row)=={'role','original_exit','signal'} and row['role'] in ('runtime','worker-b','db') and type(row['original_exit']) is int and -255<=row['original_exit']<=255 and row['signal'] is None,'BLOCKED_SMOKE_PUBLIC_EXIT')
    import uuid
    run=uuid.UUID(value['run_uuid']);require(str(run)==value['run_uuid'] and run.version==4 and value['case']=='gha-'+run.hex[:16],'BLOCKED_SMOKE_PUBLIC_RUN')
    require(type(value['github_run_id']) is int and value['github_run_id']>0 and type(value['github_run_attempt']) is int and value['github_run_attempt']==1,'BLOCKED_SMOKE_PUBLIC_RUN')
    require(set(value['source_shas'])=={'cp','bot'} and all(SHA.fullmatch(v) for v in value['source_shas'].values()),'BLOCKED_SMOKE_PUBLIC_SOURCE')
    require(set(value['checks'])==set(CHECKS) and all(v in ('PASS','FAIL','NOT_RUN') for v in value['checks'].values()),'BLOCKED_SMOKE_PUBLIC_CHECK')
    require(set(value['original_exits'])<=set(EXITS) and all(type(v) is int and -255<=v<=255 for v in value['original_exits'].values()),'BLOCKED_SMOKE_PUBLIC_EXIT')
    require(set(value['proof_sha256'])<=set(CHECKS) and all(HEX256.fullmatch(v) for v in value['proof_sha256'].values()),'BLOCKED_SMOKE_PUBLIC_HASH')
    require(set(value['container_exits'])<={'runtime','worker-b','db'},'BLOCKED_SMOKE_PUBLIC_CONTAINER')
    for row in value['container_exits'].values():require(set(row)=={'exit','oom','running'} and type(row['oom']) is bool and type(row['running']) is bool and (row['exit'] is None if row['running'] else type(row['exit']) is int and -255<=row['exit']<=255),'BLOCKED_SMOKE_PUBLIC_CONTAINER')
    require(set(value['timeline_ms'])<=set(EXITS) and all(type(v) is int and 0<=v<=720000 for v in value['timeline_ms'].values()),'BLOCKED_SMOKE_PUBLIC_TIME')
    if value['failure'] is not None:require(set(value['failure'])=={'stage','code'} and value['failure']['stage'] in (*EXITS,'contract-proof','phase-evidence') and value['failure']['code'] in ERROR_CODES,'BLOCKED_SMOKE_PUBLIC_FAILURE')
    require(value['aggregate']!='PASS' or value['failure'] is None and set(value['container_exits'])=={'runtime','worker-b','db'} and all(v=={'exit':0,'oom':False,'running':False} for v in value['container_exits'].values()),'BLOCKED_SMOKE_FALSE_PASS')
    require(value['aggregate'] in ('PASS','FAIL') and (value['aggregate']!='PASS' or set(value['proof_sha256'])==set(CHECKS) and all(v=='PASS' for v in value['checks'].values()) and set(value['original_exits'])==set(EXITS) and all(v==0 for v in value['original_exits'].values())),'BLOCKED_SMOKE_FALSE_PASS')
    require(value['aggregate']!='PASS' or not value['cleanup_failures'] and value['primary_process_failure'] is None and value['launcher_comparison']['status']=='PASS' and all(row['status']=='PASS' for row in value['checkpoints'].values()),'BLOCKED_SMOKE_FALSE_PASS')
    return value


def load_public(raw,expected=None):
    value=validate_public(decode(raw))
    if expected is not None:
        require(all(value[k]==expected[k] for k in ('run_uuid','case','github_run_id','github_run_attempt','source_lock_sha256')) and
                value['source_shas']=={k:expected['source_shas'][k] for k in ('cp','bot')} and
                value['identity_sha256']==hashlib.sha256(encoded(expected)).hexdigest(),'BLOCKED_SMOKE_PUBLIC_IDENTITY')
    return value


def command(state,label,argv,exits,timeout=90,cleanup=False):
    global PHASE
    PHASE=label;started=time.monotonic_ns()
    require(label in EXITS and label not in exits,'BLOCKED_SMOKE_STAGE_REUSE')
    with (state/(label+'.private.log')).open('xb') as log:
        try:
            remaining=(WORK_DEADLINE+(90 if cleanup else 0)-time.monotonic()) if WORK_DEADLINE else timeout
            code=124 if remaining<=0 else subprocess.run(argv,stdout=log,stderr=subprocess.STDOUT,timeout=min(timeout,max(.1,remaining))).returncode
        except subprocess.TimeoutExpired:code=124
    exits[label]=code
    TIMELINE[label]=(time.monotonic_ns()-started)//1000000
    (state/'original-exits.json').write_text(json.dumps(exits)+'\n')
    require(code==0,'BLOCKED_LINUX_BOOTSTRAP')


def inspect_services(compose,state,identity):
    result={}
    with (state/'container-observations.private.log').open('ab') as log:
        for service in ('runtime','worker-b','db'):
            cid=subprocess.check_output([*compose,'ps','-aq',service],stderr=log,text=True,timeout=5).strip()
            require(len(cid)>=12 and all(c in '0123456789abcdef' for c in cid),'BLOCKED_SMOKE_CONTAINER')
            row=json.loads(subprocess.check_output(['docker','inspect',cid],stderr=log,timeout=5))[0]
            require(row['Config']['Labels']['autostream.ui188.run']==identity['run_uuid'],'BLOCKED_SMOKE_CONTAINER_OWNER')
            facts=row['State']
            result[service]={'exit':None if facts['Running'] else facts['ExitCode'],'oom':facts['OOMKilled'],'running':facts['Running']}
    return result


def wait_service(compose,state,identity,service,label,exits,observed):
    """Observe sibling failure while waiting; never replace its original exit."""
    global PHASE
    PHASE=label;started=time.monotonic_ns();deadline=min(time.monotonic()+80,WORK_DEADLINE)
    require(label not in exits,'BLOCKED_SMOKE_STAGE_REUSE')
    code=1
    try:
        while True:
            observed.update(inspect_services(compose,state,identity))
            (state/'precleanup-services.json').write_text(json.dumps({'identity':identity,'services':observed})+'\n')
            primary=None
            if not observed[service]['running']:
                code=0
                if observed[service]['exit']!=0:primary=service
                else:return
            elif not observed['db']['running']:primary='db'
            elif not observed['worker-b']['running'] and observed['worker-b']['exit']!=0:primary='worker-b'
            if primary:
                (state/'primary-process-failure.json').write_text(json.dumps({'role':primary,'original_exit':observed[primary]['exit'],'signal':None})+'\n')
                raise Blocked('BLOCKED_SMOKE_EARLY_CHILD')
            if time.monotonic()>=deadline:
                code=124;raise Blocked('BLOCKED_LINUX_BOOTSTRAP')
            time.sleep(.25)
    finally:
        exits[label]=code;TIMELINE[label]=(time.monotonic_ns()-started)//1000000
        (state/'original-exits.json').write_text(json.dumps(exits)+'\n')


def prove(root,identity):
    rows={name:json.loads((root/(name+'.json')).read_text()) for name in ('runtime-privileges','bootstrap-runtime','bootstrap-worker','tap-broker-ready','tap-broker-exit','worker-b-exit','supervisor-exit','launch-parent-exit','broker-owned-exit')}
    for value in rows.values():validate_identity(value['identity'],identity)
    runtime,worker,broker=(rows[name]['facts'] for name in ('bootstrap-runtime','bootstrap-worker','tap-broker-ready'))
    require(runtime['uid']==runtime['euid']==worker['uid']==worker['euid']==broker['uid']==broker['euid']==1000,'BLOCKED_BOOTSTRAP_UID')
    require(all(int(f[k],16)==0 for f in (runtime,worker) for k in ('CapEff','CapBnd','CapAmb')) and int(broker['CapEff'],16)==int(broker['CapBnd'],16)==1<<13,'BLOCKED_BOOTSTRAP_CAPABILITIES')
    init=rows['runtime-privileges']['init_facts']
    require(init['pid']==1 and init['uid']==[1000]*4 and all(int(init[k],16)==0 for k in ('CapEff','CapBnd','CapAmb')) and
            all(f['NoNewPrivs']=='1' for f in (init,runtime,worker,broker)),'BLOCKED_BOOTSTRAP_CAPABILITIES')
    require(rows['runtime-privileges']['secret_readable'] is True and rows['runtime-privileges']['setup_secret_unreadable'] is True and rows['bootstrap-runtime']['setup_permissions_lost'] is True,'BLOCKED_BOOTSTRAP_SETUP')
    require(worker['pid_namespace']!=runtime['pid_namespace'] and worker['cgroup_namespace']!=runtime['cgroup_namespace'] and worker['cgroup_root_inode']!=runtime['cgroup_root_inode'] and worker['network_namespace']==runtime['network_namespace'] and int(worker['memory_max'])<int(runtime['memory_max']),'BLOCKED_BOOTSTRAP_WORKER')
    require(all(rows['bootstrap-worker'][k] is True for k in ('config_readable','config_read_only','separate_state_writable','foreign_runtime_private_absent','db_readiness')),'BLOCKED_BOOTSTRAP_WORKER')
    require(rows['bootstrap-runtime']['db_readiness'] is True and rows['bootstrap-runtime']['filtered_RTP_FD_verified'] is True and rows['bootstrap-runtime']['raw_socket_direct_denied'] is True,'BLOCKED_BOOTSTRAP_DB_OR_RTP')
    require(rows['bootstrap-runtime']['go_constructor']['original_exit']==0 and rows['bootstrap-runtime']['go_constructor']['producer']=='ACTUAL_GO_CONSTRUCTOR','BLOCKED_BOOTSTRAP_GO')
    require(rows['tap-broker-exit']['original_exit']==rows['worker-b-exit']['original_exit']==rows['supervisor-exit']['original_driver_exit']==rows['supervisor-exit']['broker_exit']==rows['supervisor-exit']['cleanup_exit']==0 and rows['tap-broker-exit']['issued_taps']==2,'BLOCKED_BOOTSTRAP_EXIT')
    parent=rows['launch-parent-exit']
    require(parent['result']['original_exit']==parent['result']['payload_exit']==parent['result']['cleanup_exit']==parent['broker_cleanup_exit']==0,'BLOCKED_BOOTSTRAP_EXIT')
    require(rows['broker-owned-exit']['original_exit']==0 and rows['broker-owned-exit']['signal'] is None,'BLOCKED_BOOTSTRAP_EXIT')
    shutdown=json.loads((root/'shutdown-containers.json').read_text());validate_identity(shutdown['identity'],identity)
    require(set(shutdown['services'])=={'runtime','worker-b','db'} and all(v=={'exit':0,'oom':False,'running':False} for v in shutdown['services'].values()),'BLOCKED_BOOTSTRAP_CONTAINER_EXIT')
    return {name:'PASS' for name in CHECKS},{name:sha_file(root/filename) for name,filename in (
        ('identity','identity.json'),('go_constructor','go-contract/go-constructor-proof.json'),('uid_and_capabilities','runtime-privileges.json'),
        ('setup_permissions_lost','bootstrap-runtime.json'),('secret_read','runtime-privileges.json'),('worker_isolation','bootstrap-worker.json'),
        ('db_readiness','bootstrap-runtime.json'),('filtered_rtp_fd','tap-broker-ready.json'),('normal_shutdown','shutdown-containers.json'))}


def main():
    global PHASE,WORK_DEADLINE
    WORK_DEADLINE=time.monotonic()+540
    TIMELINE.clear()
    require(sys.platform=='linux' and os.environ.get('GITHUB_EVENT_NAME')=='push' and os.environ.get('GITHUB_RUN_ATTEMPT')=='1','BLOCKED_SMOKE_TRIGGER')
    require(os.environ.get('GITHUB_REPOSITORY')=='Kome-Lab/Autostream-ControlPanel' and os.environ.get('RUNNER_ENVIRONMENT')=='github-hosted','BLOCKED_SMOKE_OWNER')
    require(os.environ.get('GITHUB_REF')=='refs/heads/fix/part5-ui-188-release-integration-actions','BLOCKED_SMOKE_REF')
    lock=json.loads((HERE/'source-lock.json').read_text())
    verify(HERE.parents[2],'cp')
    identity=create(sha_file(HERE/'source-lock.json'),{name:(os.environ['GITHUB_SHA'] if name=='cp' else row['dispatch_commit']) for name,row in lock['components'].items()},os.environ['GITHUB_RUN_ID'],job='contract-smoke')
    state=Path(os.environ['RUNNER_TEMP'])/('ui188-smoke-'+identity['run_uuid']);state.mkdir(mode=0o700)
    (Path(os.environ['RUNNER_TEMP'])/'ui188-smoke-state.json').write_text(json.dumps({'path':str(state),'run_uuid':identity['run_uuid']})+'\n')
    for name in ('evidence','context','secrets','setup-secret'):(state/name).mkdir(mode=0o700)
    (state/'evidence/identity.json').write_bytes(encoded(identity));(state/'evidence/identity.json').chmod(0o444)
    exits={};checks={name:'NOT_RUN' for name in CHECKS};hashes={};created=False;original=1;failed=None;services={};observed={};cleanup_failures={};launcher={'status':'NOT_RUN'}
    env={**environment(identity,'/evidence/identity.json'),'UI188_PRIVATE_DIR':str(state),'UI188_RUN_UUID':identity['run_uuid'],'UI188_DB_SCHEMA':identity['schema'],'UI188_CASE':identity['case']}
    compose=['docker','compose','--project-name','ui188-smoke-'+identity['run_uuid'].replace('-',''),'--file',str(HERE/'compose.yml'),'--file',str(HERE/'compose.smoke.yml')]
    os.environ.update(env)
    try:
        try:
            started=time.monotonic_ns();PHASE='compile'
            binary=compile_actual(Path(os.environ['UI188_SMOKE_BOT_ROOT']),state/'compile');exits['compile']=0
        except Exception:exits['compile']=1;raise
        finally:TIMELINE['compile']=(time.monotonic_ns()-started)//1000000
        context=state/'context';shutil.copytree(HERE,context/'platform',ignore=shutil.ignore_patterns('__pycache__'))
        shutil.copyfile(HERE.parents[0]/'ui188-r4-chain/packet_capture.py',context/'original_packet_capture.py')
        shutil.copyfile(binary,context/'bot-constructor')
        iid=state/'image-id'
        command(state,'image',['docker','build','--iidfile',str(iid),'--file',str(HERE/'Dockerfile.smoke'),str(context)],exits,360)
        image=iid.read_text().strip();require(image.startswith('sha256:') and HEX256.fullmatch(image[7:]),'BLOCKED_SMOKE_IMAGE')
        os.environ['UI188_RUNTIME_IMAGE']=image
        PHASE='launcher-comparison';started=time.monotonic_ns()
        try:
            launcher=compare(image,state,identity);exits[PHASE]=0
        except Exception:
            exits[PHASE]=1
            receipt=state/'launcher-comparison-result.json'
            if receipt.is_file():launcher=json.loads(receipt.read_text())
            raise
        finally:TIMELINE['launcher-comparison']=(time.monotonic_ns()-started)//1000000
        credentials={'db_password':secrets.token_hex(24)}
        for name,value in (('credentials.json',json.dumps(credentials)),('db_app',credentials['db_password']),('db_root',secrets.token_hex(24))):
            target=state/'secrets'/name;target.write_text(value);target.chmod(0o600)
        shutil.copyfile(state/'secrets/credentials.json',state/'setup-secret/credentials.json');(state/'setup-secret/credentials.json').chmod(0o600)
        command(state,'secret-owner',['sudo','chown','-R','0:0',str(state/'setup-secret')],exits,20)
        command(state,'compose-config',[*compose,'config','--quiet'],exits,20)
        created=True
        command(state,'up',[*compose,'up','--detach'],exits,150)
        for service,label in (('runtime','runtime-wait'),('worker-b','worker-wait')):
            wait_service(compose,state,identity,service,label,exits,observed)
        original=0
    except Exception as error:
        original=1;failed=failure(error)
    finally:
        if created:
            try:command(state,'stop',[*compose,'stop','--timeout','15'],exits,30,cleanup=True)
            except Exception as error:original=1;failed=failed or failure(error);cleanup_failures['stop']=exits.get('stop',1)
        for label,folder in (('read-owner','evidence'),('secret-read-owner','setup-secret')):
            try:command(state,label,['sudo','chown','-R',str(os.getuid())+':'+str(os.getgid()),str(state/folder)],exits,10,cleanup=True)
            except Exception as error:original=1;failed=failed or failure(error);cleanup_failures[label]=exits.get(label,1)
        if created:
            try:
                services=inspect_services(compose,state,identity)
                (state/'evidence/shutdown-containers.json').write_text(json.dumps({'identity':identity,'services':services})+'\n')
                mark=begin(state/'evidence',identity,'container_exit')
                good=all(row=={'exit':0,'oom':False,'running':False} for row in services.values())
                proof_names=['shutdown_containers']+[name for name in ('launcher_exit','broker_owned_exit') if (state/'evidence'/PROOFS[name]).is_file()]
                finish(state/'evidence',identity,'container_exit',mark,original_exit=0 if good else 1,proofs=proof_names)
            except Exception as error:original=1;failed=failed or failure(error)
        if original==0:
            try:PHASE='contract-proof';checks,hashes=prove(state/'evidence',identity)
            except Exception as error:checks={name:'FAIL' for name in CHECKS};original=1;failed=failed or failure(error)
        if created:
            try:command(state,'down',[*compose,'down','--volumes','--remove-orphans'],exits,30,cleanup=True)
            except Exception as error:original=1;failed=failed or failure(error);cleanup_failures['down']=exits.get('down',1)
        PHASE='phase-evidence'
        checkpoints=checkpoint_projection(state/'evidence',identity)
        if original==0 and not all(row['status']=='PASS' for row in checkpoints.values()):
            original=1;failed=failed or {'stage':PHASE,'code':'BLOCKED_SMOKE_PHASE_EVIDENCE'}
        primary=state/'primary-process-failure.json'
        summary={'schema_version':2,'kind':'CONTRACT_SMOKE','run_uuid':identity['run_uuid'],'case':identity['case'],'github_run_id':identity['github_run_id'],'github_run_attempt':1,
            'source_shas':{k:identity['source_shas'][k] for k in ('cp','bot')},'checks':checks,'original_exits':exits,'aggregate':'PASS' if original==0 else 'FAIL','full_e2e':'NOT_RUN',
            'known_issue':'KNOWN_UNRESOLVED_MEDIA_ISSUE','private_raw_storage':'NOT_CONFIGURED','proof_sha256':hashes,
            'container_exits':services,'failure':failed,'timeline_ms':TIMELINE,
            'source_lock_sha256':identity['source_lock_sha256'],'identity_sha256':hashlib.sha256(encoded(identity)).hexdigest(),
            'checkpoints':checkpoints,'launcher_comparison':launcher,'cleanup_failures':cleanup_failures,'observed_exits':observed,
            'primary_process_failure':json.loads(primary.read_text()) if primary.is_file() else None}
        validate_public(summary)
        output=Path.cwd()/'ui188-smoke-public';require(not output.exists(),'BLOCKED_SMOKE_PUBLIC_REUSE');output.mkdir()
        raw=(json.dumps(summary,sort_keys=True,indent=2)+'\n').encode()
        load_public(raw,identity)
        # Only a closed enum/numeric/hash projection leaves the runner.
        if (state/'secrets/credentials.json').is_file():
            require(not any(value.encode() in raw for value in json.loads((state/'secrets/credentials.json').read_text()).values()),'BLOCKED_SMOKE_PUBLIC_SECRET')
        (output/'summary.json').write_bytes(raw)
    print('UI188_LINUX_BOOTSTRAP_CONTRACT_SMOKE_'+('PASS' if original==0 else 'FAIL'))
    return original

if __name__=='__main__':
    try:sys.exit(main())
    except Exception:
        print('BLOCKED_LINUX_BOOTSTRAP_CONTRACT_SMOKE');sys.exit(1)
