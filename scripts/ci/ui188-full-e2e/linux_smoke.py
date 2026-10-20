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
from run_identity import create,encoded,environment
from seal import sha_file

HERE=Path(__file__).resolve().parent
CHECKS=('identity','go_constructor','uid_and_capabilities','setup_permissions_lost','secret_read','worker_isolation','db_readiness','filtered_rtp_fd','normal_shutdown')
EXITS=('compile','image','secret-owner','compose-config','up','runtime-wait','worker-wait','stop','read-owner','secret-read-owner','down')
ERROR_CODES=('BLOCKED_LINUX_BOOTSTRAP','BLOCKED_ACTUAL_GO_CONSTRUCTOR_COMPILE','BLOCKED_SMOKE_CONTAINER','BLOCKED_SMOKE_CONTAINER_OWNER',
    'BLOCKED_SMOKE_CONTAINER_EXIT','BLOCKED_SMOKE_IMAGE','BLOCKED_BOOTSTRAP_UID','BLOCKED_BOOTSTRAP_CAPABILITIES','BLOCKED_BOOTSTRAP_SETUP',
    'BLOCKED_BOOTSTRAP_WORKER','BLOCKED_BOOTSTRAP_DB_OR_RTP','BLOCKED_BOOTSTRAP_GO','BLOCKED_BOOTSTRAP_EXIT','BLOCKED_BOOTSTRAP_CONTAINER_EXIT',
    'CONTRACT_VALIDATION_FAILED')
TIMELINE={}
PHASE='compile'


def failure(error):
    code=str(error) if isinstance(error,Blocked) and str(error) in ERROR_CODES else 'CONTRACT_VALIDATION_FAILED'
    return {'stage':PHASE,'code':code}


def validate_public(value):
    require(set(value)=={'schema_version','kind','run_uuid','case','github_run_id','github_run_attempt','source_shas','checks','original_exits','aggregate','full_e2e','known_issue','private_raw_storage','proof_sha256','container_exits','failure','timeline_ms'},'BLOCKED_SMOKE_PUBLIC_SCHEMA')
    require(value['schema_version']==1 and value['kind']=='CONTRACT_SMOKE' and value['full_e2e']=='NOT_RUN' and value['known_issue']=='KNOWN_UNRESOLVED_MEDIA_ISSUE' and value['private_raw_storage']=='NOT_CONFIGURED','BLOCKED_SMOKE_PUBLIC_SCOPE')
    import uuid
    run=uuid.UUID(value['run_uuid']);require(str(run)==value['run_uuid'] and run.version==4 and value['case']=='gha-'+run.hex[:16],'BLOCKED_SMOKE_PUBLIC_RUN')
    require(type(value['github_run_id']) is int and value['github_run_id']>0 and value['github_run_attempt']==1,'BLOCKED_SMOKE_PUBLIC_RUN')
    require(set(value['source_shas'])=={'cp','bot'} and all(SHA.fullmatch(v) for v in value['source_shas'].values()),'BLOCKED_SMOKE_PUBLIC_SOURCE')
    require(set(value['checks'])==set(CHECKS) and all(v in ('PASS','FAIL','NOT_RUN') for v in value['checks'].values()),'BLOCKED_SMOKE_PUBLIC_CHECK')
    require(set(value['original_exits'])<=set(EXITS) and all(type(v) is int and -255<=v<=255 for v in value['original_exits'].values()),'BLOCKED_SMOKE_PUBLIC_EXIT')
    require(set(value['proof_sha256'])<=set(CHECKS) and all(HEX256.fullmatch(v) for v in value['proof_sha256'].values()),'BLOCKED_SMOKE_PUBLIC_HASH')
    require(set(value['container_exits'])<={'runtime','worker-b','db'},'BLOCKED_SMOKE_PUBLIC_CONTAINER')
    for row in value['container_exits'].values():require(set(row)=={'exit','oom','running'} and type(row['exit']) is int and -255<=row['exit']<=255 and type(row['oom']) is bool and type(row['running']) is bool,'BLOCKED_SMOKE_PUBLIC_CONTAINER')
    require(set(value['timeline_ms'])<=set(EXITS) and all(type(v) is int and 0<=v<=720000 for v in value['timeline_ms'].values()),'BLOCKED_SMOKE_PUBLIC_TIME')
    if value['failure'] is not None:require(set(value['failure'])=={'stage','code'} and value['failure']['stage'] in (*EXITS,'contract-proof') and value['failure']['code'] in ERROR_CODES,'BLOCKED_SMOKE_PUBLIC_FAILURE')
    require(value['aggregate']!='PASS' or value['failure'] is None and set(value['container_exits'])=={'runtime','worker-b','db'} and all(v=={'exit':0,'oom':False,'running':False} for v in value['container_exits'].values()),'BLOCKED_SMOKE_FALSE_PASS')
    require(value['aggregate'] in ('PASS','FAIL') and (value['aggregate']!='PASS' or set(value['proof_sha256'])==set(CHECKS) and all(v=='PASS' for v in value['checks'].values()) and set(value['original_exits'])==set(EXITS) and all(v==0 for v in value['original_exits'].values())),'BLOCKED_SMOKE_FALSE_PASS')
    return value


def command(state,label,argv,exits,timeout=90):
    global PHASE
    PHASE=label;started=time.monotonic_ns()
    require(label in EXITS and label not in exits,'BLOCKED_SMOKE_STAGE_REUSE')
    with (state/(label+'.private.log')).open('xb') as log:
        try:code=subprocess.run(argv,stdout=log,stderr=subprocess.STDOUT,timeout=timeout).returncode
        except subprocess.TimeoutExpired:code=124
    exits[label]=code
    TIMELINE[label]=(time.monotonic_ns()-started)//1000000
    (state/'original-exits.json').write_text(json.dumps(exits)+'\n')
    require(code==0,'BLOCKED_LINUX_BOOTSTRAP')


def prove(root,identity):
    rows={name:json.loads((root/(name+'.json')).read_text()) for name in ('runtime-privileges','bootstrap-runtime','bootstrap-worker','tap-broker-ready','tap-broker-exit','worker-b-exit','supervisor-exit')}
    for value in rows.values():validate_identity(value['identity'],identity)
    runtime,worker,broker=(rows[name]['facts'] for name in ('bootstrap-runtime','bootstrap-worker','tap-broker-ready'))
    require(runtime['uid']==runtime['euid']==worker['uid']==worker['euid']==broker['uid']==broker['euid']==1000,'BLOCKED_BOOTSTRAP_UID')
    require(all(int(f[k],16)==0 for f in (runtime,worker) for k in ('CapEff','CapBnd','CapAmb')) and int(broker['CapEff'],16)==int(broker['CapBnd'],16)==1<<13,'BLOCKED_BOOTSTRAP_CAPABILITIES')
    require(rows['runtime-privileges']['secret_readable'] is True and rows['runtime-privileges']['setup_secret_unreadable'] is True and rows['bootstrap-runtime']['setup_permissions_lost'] is True,'BLOCKED_BOOTSTRAP_SETUP')
    require(worker['pid_namespace']!=runtime['pid_namespace'] and worker['cgroup_namespace']!=runtime['cgroup_namespace'] and worker['cgroup_root_inode']!=runtime['cgroup_root_inode'] and worker['network_namespace']==runtime['network_namespace'] and int(worker['memory_max'])<int(runtime['memory_max']),'BLOCKED_BOOTSTRAP_WORKER')
    require(all(rows['bootstrap-worker'][k] is True for k in ('config_readable','config_read_only','separate_state_writable','foreign_runtime_private_absent','db_readiness')),'BLOCKED_BOOTSTRAP_WORKER')
    require(rows['bootstrap-runtime']['db_readiness'] is True and rows['bootstrap-runtime']['filtered_RTP_FD_verified'] is True and rows['bootstrap-runtime']['raw_socket_direct_denied'] is True,'BLOCKED_BOOTSTRAP_DB_OR_RTP')
    require(rows['bootstrap-runtime']['go_constructor']['original_exit']==0 and rows['bootstrap-runtime']['go_constructor']['producer']=='ACTUAL_GO_CONSTRUCTOR','BLOCKED_BOOTSTRAP_GO')
    require(rows['tap-broker-exit']['original_exit']==rows['worker-b-exit']['original_exit']==rows['supervisor-exit']['original_driver_exit']==rows['supervisor-exit']['broker_exit']==rows['supervisor-exit']['cleanup_exit']==0 and rows['tap-broker-exit']['issued_taps']==2,'BLOCKED_BOOTSTRAP_EXIT')
    shutdown=json.loads((root/'shutdown-containers.json').read_text());validate_identity(shutdown['identity'],identity)
    require(set(shutdown['services'])=={'runtime','worker-b','db'} and all(v=={'exit':0,'oom':False,'running':False} for v in shutdown['services'].values()),'BLOCKED_BOOTSTRAP_CONTAINER_EXIT')
    return {name:'PASS' for name in CHECKS},{name:sha_file(root/filename) for name,filename in (
        ('identity','identity.json'),('go_constructor','go-contract/go-constructor-proof.json'),('uid_and_capabilities','runtime-privileges.json'),
        ('setup_permissions_lost','bootstrap-runtime.json'),('secret_read','runtime-privileges.json'),('worker_isolation','bootstrap-worker.json'),
        ('db_readiness','bootstrap-runtime.json'),('filtered_rtp_fd','tap-broker-ready.json'),('normal_shutdown','shutdown-containers.json'))}


def main():
    global PHASE
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
    exits={};checks={name:'NOT_RUN' for name in CHECKS};hashes={};created=False;original=1;failed=None;services={}
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
        credentials={'db_password':secrets.token_hex(24)}
        for name,value in (('credentials.json',json.dumps(credentials)),('db_app',credentials['db_password']),('db_root',secrets.token_hex(24))):
            target=state/'secrets'/name;target.write_text(value);target.chmod(0o600)
        shutil.copyfile(state/'secrets/credentials.json',state/'setup-secret/credentials.json');(state/'setup-secret/credentials.json').chmod(0o600)
        command(state,'secret-owner',['sudo','chown','-R','0:0',str(state/'setup-secret')],exits,20)
        command(state,'compose-config',[*compose,'config','--quiet'],exits,20)
        created=True
        command(state,'up',[*compose,'up','--detach'],exits,150)
        for service,label in (('runtime','runtime-wait'),('worker-b','worker-wait')):
            cid=subprocess.check_output([*compose,'ps','-aq',service],text=True).strip()
            require(len(cid)>=12 and all(c in '0123456789abcdef' for c in cid),'BLOCKED_SMOKE_CONTAINER')
            info=json.loads(subprocess.check_output(['docker','inspect',cid]))[0]
            require(info['Config']['Labels']['autostream.ui188.run']==identity['run_uuid'],'BLOCKED_SMOKE_CONTAINER_OWNER')
            command(state,label,['docker','wait',cid],exits,80)
            require((state/(label+'.private.log')).read_text().strip()=='0','BLOCKED_SMOKE_CONTAINER_EXIT')
        original=0
    except Exception as error:
        original=1;failed=failure(error)
    finally:
        if created:
            try:command(state,'stop',[*compose,'stop','--timeout','15'],exits,50)
            except Exception as error:original=1;failed=failed or failure(error)
        for label,folder in (('read-owner','evidence'),('secret-read-owner','setup-secret')):
            try:command(state,label,['sudo','chown','-R',str(os.getuid())+':'+str(os.getgid()),str(state/folder)],exits,20)
            except Exception as error:original=1;failed=failed or failure(error)
        if created:
            try:
                services={}
                for service in ('runtime','worker-b','db'):
                    cid=subprocess.check_output([*compose,'ps','-aq',service],text=True).strip()
                    item=json.loads(subprocess.check_output(['docker','inspect',cid]))[0]
                    require(item['Config']['Labels']['autostream.ui188.run']==identity['run_uuid'],'BLOCKED_SMOKE_CONTAINER_OWNER')
                    services[service]={'exit':item['State']['ExitCode'],'oom':item['State']['OOMKilled'],'running':item['State']['Running']}
                (state/'evidence/shutdown-containers.json').write_text(json.dumps({'identity':identity,'services':services})+'\n')
            except Exception as error:original=1;failed=failed or failure(error)
        if original==0:
            try:PHASE='contract-proof';checks,hashes=prove(state/'evidence',identity)
            except Exception as error:checks={name:'FAIL' for name in CHECKS};original=1;failed=failed or failure(error)
        if created:
            try:command(state,'down',[*compose,'down','--volumes','--remove-orphans'],exits,60)
            except Exception as error:original=1;failed=failed or failure(error)
        summary={'schema_version':1,'kind':'CONTRACT_SMOKE','run_uuid':identity['run_uuid'],'case':identity['case'],'github_run_id':identity['github_run_id'],'github_run_attempt':1,
            'source_shas':{k:identity['source_shas'][k] for k in ('cp','bot')},'checks':checks,'original_exits':exits,'aggregate':'PASS' if original==0 else 'FAIL','full_e2e':'NOT_RUN',
            'known_issue':'KNOWN_UNRESOLVED_MEDIA_ISSUE','private_raw_storage':'NOT_CONFIGURED','proof_sha256':hashes,
            'container_exits':services,'failure':failed,'timeline_ms':TIMELINE}
        validate_public(summary)
        output=Path.cwd()/'ui188-smoke-public';require(not output.exists(),'BLOCKED_SMOKE_PUBLIC_REUSE');output.mkdir()
        raw=(json.dumps(summary,sort_keys=True,indent=2)+'\n').encode()
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
