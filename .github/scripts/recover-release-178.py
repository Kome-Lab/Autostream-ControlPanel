"""UI178: verify and publish one fixed original artifact without rebuilding."""
import argparse,hashlib,json,os,pathlib,re,stat,struct,subprocess,sys,tarfile,zipfile
REPO='Kome-Lab/Autostream-ControlPanel'
BRANCH='refs/heads/fix/part5-ui-178-publication-recovery'
SOURCE='9c75188147daf8435d005651a8266ae31ce39653'
TAG='e540351b4e8649ab75a1e0c02952aef1de1e36a9'
ARTIFACT=11541007570
ZIP_SHA='acdbfd6630f658d2e39480cba34ceb7a9b36f1b8d9a6f38f7cce96b0a7bca4b0'
ALLOW={'.github/workflows/release-host.yml','.github/scripts/recover-release-178.py','.github/scripts/test-recover-release-178.py','.github/scripts/recovery-inputs-178.json'}
INPUTS=pathlib.Path(__file__).with_name('recovery-inputs-178.json')

def digest(b):return hashlib.sha256(b).hexdigest()
def cfg():
 c=json.loads(INPUTS.read_text(encoding='utf-8'));assert c['repository']==REPO and c['source_commit']==SOURCE and c['source_tag_object']==TAG and c['artifact']['id']==ARTIFACT and c['artifact']['digest']=='sha256:'+ZIP_SHA and c['recovery_branch']==BRANCH.removeprefix('refs/heads/')
 return c
def write(p,obj):p.write_text(json.dumps(obj,indent=2,ensure_ascii=False)+'\n',encoding='utf-8')
def command(args,allowed=(0,),data=None):
 r=subprocess.run(args,input=data,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 if r.returncode not in allowed:raise RuntimeError('Command failed: '+args[0]+'; exit '+str(r.returncode))
 return r
def api(path,method='GET',body=None,allowed=(0,)):
 args=['gh','api','--method',method,path]
 if body is not None:args+=['--input','-']
 r=command(args,allowed=allowed,data=None if body is None else json.dumps(body).encode())
 try:o=json.loads(r.stdout)
 except ValueError:o=None
 return r.returncode,o
def require_absent(code,release,listing):
 assert code!=0 and isinstance(release,dict) and release.get('status')=='404'
 assert not any(x['tag_name']=='v2.0.1' for x in listing),'Existing draft or release blocks creation'
def release_absence():
 code,m=api('repos/'+REPO+'/releases/tags/v2.0.1',allowed=(0,1));allitems=[]
 for page in range(1,11):
  _,items=api('repos/'+REPO+'/releases?per_page=100&page='+str(page));allitems+=items
  if len(items)<100:break
 else:raise RuntimeError('Release listing incomplete')
 require_absent(code,m,allitems)
def identity():
 assert os.environ['GITHUB_REPOSITORY']==REPO and os.environ['GITHUB_EVENT_NAME']=='workflow_dispatch' and os.environ['GITHUB_REF']==BRANCH and os.environ['RECOVERY_VERSION']=='v2.0.1'
 assert os.environ['RECOVERY_PUBLICATION']=='true'
 sha=os.environ['GITHUB_SHA'];assert re.fullmatch('[0-9a-f]{40}',sha)
 event=json.loads(pathlib.Path(os.environ['GITHUB_EVENT_PATH']).read_text());assert event['inputs']['version']=='v2.0.1'
 assert event['inputs']['publication_recovery'] is True or event['inputs']['publication_recovery']=='true'
 diff=command(['git','diff','--name-only',SOURCE,sha]).stdout.decode().splitlines();assert set(diff)<=ALLOW and diff
 assert command(['git','rev-parse',SOURCE+':.github/workflows/release-host.yml']).stdout.decode().strip()==cfg()['original_workflow_blob']
 assert command(['git','rev-parse','HEAD']).stdout.decode().strip()==sha
 return {'workflow':'.github/workflows/release-host.yml','commit':sha,'ref':BRANCH,'event':'workflow_dispatch','run_id':int(os.environ['GITHUB_RUN_ID']),'attempt':int(os.environ['GITHUB_RUN_ATTEMPT'])}
def verify_metadata(c,run,job,ci,tagref,tagobj,artifact):
 assert run['id']==c['original_run_id'] and run['run_attempt']==1 and run['event']=='push' and run['head_branch']=='v2.0.1' and run['head_sha']==SOURCE and run['status']=='completed' and run['conclusion']=='failure'
 assert job['id']==c['successful_build_job_id'] and job['run_id']==run['id'] and job['head_sha']==SOURCE and job['run_attempt']==1 and job['status']=='completed' and job['conclusion']=='success'
 assert ci['id']==c['original_exact_ci_run_id'] and ci['head_sha']==SOURCE and ci['run_attempt']==1 and ci['conclusion']=='success'
 assert tagref['object']['type']=='tag' and tagref['object']['sha']==TAG and tagobj['object']['sha']==SOURCE and tagobj['object']['type']=='commit' and tagobj['verification']['verified'] is True
 assert artifact['id']==ARTIFACT and artifact['name']==c['artifact']['name'] and artifact['size_in_bytes']==c['artifact']['size_in_bytes'] and artifact['digest']=='sha256:'+ZIP_SHA and not artifact['expired']
 assert artifact['workflow_run']['id']==run['id'] and artifact['workflow_run']['head_sha']==SOURCE and artifact['workflow_run']['head_branch']=='v2.0.1'
def verify_members(c,data):
 expected={x['member']:(x['bytes'],x['sha256']) for x in c['payload_members']};assert len(expected)==7 and set(data)==set(expected)
 for n,b in data.items():assert (len(b),digest(b))==expected[n],n
 def sums(b):
  d={}
  for l in b.decode().splitlines():
   m=re.fullmatch(r'([0-9a-f]{64}) [ *](.+)',l);assert m and m[2] in data and m[2] not in d;d[m[2]]=m[1]
  return d
 assert set(sums(data['SHA256SUMS']))==set(data)-{'SHA256SUMS'}
 for n,b in data.items():
  if n.endswith('.sha256') or n=='SHA256SUMS':
   d=sums(b)
   if n.endswith('.sha256'):assert set(d)=={n.removesuffix('.sha256')}
   for target,want in d.items():assert digest(data[target])==want
 assert json.loads(data['release-manifest.json'])==c['release_manifest']
def safe_member(info,expected):
 assert info.filename in expected and pathlib.PurePosixPath(info.filename).name==info.filename and '\\' not in info.filename and not info.is_dir()
 assert stat.S_IFMT(info.external_attr>>16) in [0,stat.S_IFREG] and info.file_size==expected[info.filename][0]
def verify_zip(c,path):
 raw=path.read_bytes();assert len(raw)==c['artifact']['size_in_bytes'] and digest(raw)==ZIP_SHA
 expected={x['member']:(x['bytes'],x['sha256']) for x in c['payload_members']}
 with zipfile.ZipFile(path) as z:
  names=z.namelist();assert len(names)==len(set(names))==7 and set(names)==set(expected) and z.testzip() is None
  for info in z.infolist():safe_member(info,expected)
  assert sum(i.file_size for i in z.infolist())<30*1024*1024
  data={n:z.read(n) for n in names}
 verify_members(c,data);return data
def bundle(path,artifact):
 root=artifact['name'][:-7];arch=artifact['arch'];table=[]
 with tarfile.open(path,'r:gz') as t:
  members=t.getmembers();names=[x.name for x in members];assert len(names)==len(set(names))
  for x in members:assert (x.name==root or x.name.startswith(root+'/')) and '..' not in pathlib.PurePosixPath(x.name).parts and not x.name.startswith('/') and (x.isfile() or x.isdir()) and not x.issym() and not x.islnk() and x.size<256*1024*1024
  manifest=json.loads(t.extractfile(root+'/artifact-manifest.json').read());assert manifest['component']=='control-panel' and manifest['commit']==SOURCE and manifest['source_version']=='v2.0.1' and manifest['platform']=={'os':'linux','arch':arch} and manifest['archive']=={'name':artifact['name'],'root':root}
  sums=t.extractfile(root+'/checksums.txt').read().decode().splitlines();declared={}
  for l in sums:
   m=re.fullmatch(r'([0-9a-f]{64}) [ *](.+)',l);assert m;rel=m[2].removeprefix('./');assert '..' not in pathlib.PurePosixPath(rel).parts and not rel.startswith('/') and rel not in declared;declared[rel]=m[1]
  assert set(declared)=={x.name[len(root)+1:] for x in members if x.isfile()}-{'checksums.txt'}
  for rel,want in declared.items():
   member=t.getmember(root+'/'+rel);h=hashlib.sha256();head=b''
   with t.extractfile(member) as f:
    for b in iter(lambda:f.read(1024*1024),b''):
     if not head:head=b[:64]
     h.update(b)
   assert h.hexdigest()==want
   if rel.startswith('bin/'):assert head[:6]==b'\x7fELF\x02\x01' and struct.unpack('<H',head[18:20])[0]=={'amd64':62,'arm64':183}[arch] and member.mode==0o755
   table.append({'member':member.name,'bytes':member.size,'mode':oct(member.mode),'sha256':want})
 return {'archive':artifact['name'],'arch':arch,'members':table,'static_arch_verified':True,'binary_executed':False}
def prepare(work):
 c=cfg();who=identity();assert not work.exists();work.mkdir()
 metadata={}
 for key,path in [('run','actions/runs/'+str(c['original_run_id'])),('job','actions/jobs/'+str(c['successful_build_job_id'])),('ci','actions/runs/'+str(c['original_exact_ci_run_id'])),('tagref','git/ref/tags/v2.0.1'),('tagobj','git/tags/'+TAG),('artifact','actions/artifacts/'+str(ARTIFACT))]:
  _,metadata[key]=api('repos/'+REPO+'/'+path)
 verify_metadata(c,metadata['run'],metadata['job'],metadata['ci'],metadata['tagref'],metadata['tagobj'],metadata['artifact']);release_absence();write(work/'source-metadata.json',metadata)
 zipfile_path=work/'original-artifact.zip'
 with zipfile_path.open('xb') as f:
  r=subprocess.run(['gh','api','--method','GET','repos/'+REPO+'/actions/artifacts/'+str(ARTIFACT)+'/zip'],stdout=f,stderr=subprocess.PIPE)
 assert r.returncode==0,'Fixed artifact REST GET failed'
 data=verify_zip(c,zipfile_path);payload=work/'payload';payload.mkdir()
 for n,b in data.items():(payload/n).write_bytes(b)
 bundles=[bundle(payload/a['name'],a) for a in c['release_manifest']['components'][0]['artifacts']];write(work/'static-bundles.json',bundles)
 predicate={'operation':'verify-and-publish-existing-artifact','rebuilt':False,'repository':REPO,'version':'v2.0.1','original':{'source_commit':SOURCE,'tag_object':TAG,'peeled_commit':SOURCE,'workflow_path':c['original_workflow_path'],'workflow_blob':c['original_workflow_blob'],'run_id':c['original_run_id'],'run_attempt':1,'event':'push','ref':'refs/tags/v2.0.1','successful_build_job_id':c['successful_build_job_id'],'artifact_id':ARTIFACT,'artifact_zip_sha256':ZIP_SHA,'payload_members':c['payload_members']},'recovery':who,'verification':{'original_metadata':'PASS','zip_digest_and_size':'PASS','exact_seven_payload_hashes':'PASS','outer_and_inner_checksums':'PASS','static_member_mode_arch':'PASS','release_absent_before_attestation':True}}
 write(work/'recovery-predicate.json',predicate)
 body='## AutoStream Control Panel v2.0.1\n\nOriginal application source: `'+SOURCE+'`. Existing signed tag: `'+TAG+'`.\n\nOriginal successful build: https://github.com/'+REPO+'/actions/runs/'+str(c['original_run_id'])+'/attempts/1 (build job '+str(c['successful_build_job_id'])+'). Original ZIP SHA256: `'+ZIP_SHA+'`. No application rebuild or recompression.\n\nPublication recovery: https://github.com/'+REPO+'/actions/runs/'+str(who['run_id'])+'/attempts/'+str(who['attempt'])+' at recovery commit `'+who['commit']+'`, `'+BRANCH+'`, workflow_dispatch.\n\nThe original two archives and release manifest have a signed publication-handoff attestation of predicate type `'+c['recovery_predicate_type']+'`, issued for the actual recovery identity. It is not original tag-build provenance. Runtime/updater trust-policy compatibility is not asserted.\n\n'+c['known_issues']+'\n'
 body+='\nPublication uses the explicitly selected `publication_recovery=true` input on the fixed v2.0.1 recovery branch. Normal tag publication and build-only rehearsal gates remain unchanged.\n'
 (work/'release-body.md').write_text(body,encoding='utf-8');print('FIXED_ORIGINAL_ARTIFACT_VERIFIED_NO_REBUILD',flush=True)
def verify_attestations(work):
 c=cfg();who=identity();predicate=json.loads((work/'recovery-predicate.json').read_text());assert predicate['recovery']==who
 results=[]
 subjects=[a['name'] for a in c['release_manifest']['components'][0]['artifacts']]+['release-manifest.json']
 for n in subjects:
  args=['gh','attestation','verify',str(work/'payload'/n),'--predicate-type',c['recovery_predicate_type'],'--repo',REPO,'--signer-workflow',REPO+'/'+c['recovery_workflow_path'],'--signer-digest',who['commit'],'--source-digest',who['commit'],'--source-ref',BRANCH,'--deny-self-hosted-runners','--format','json']
  r=command(args);o=json.loads(r.stdout);assert o
  expected=digest((work/'payload'/n).read_bytes())
  assert all(x['verificationResult']['statement']['predicate']==predicate and x['verificationResult']['statement']['predicateType']==c['recovery_predicate_type'] for x in o)
  assert any(any(s['name']==n and s['digest']['sha256']==expected for s in x['verificationResult']['statement']['subject']) for x in o)
  (work/('attestation-'+n+'.json')).write_bytes(r.stdout);results.append({'name':n,'sha256':expected,'cryptographic_verification':'PASS','actual_recovery_commit':who['commit'],'actual_recovery_ref':BRANCH})
 write(work/'attestation-verification.json',results);print('THREE_RECOVERY_SUBJECTS_VERIFIED',flush=True)
def publish(work):
 c=cfg();who=identity();assert json.loads((work/'recovery-predicate.json').read_text())['recovery']==who
 assert len(json.loads((work/'attestation-verification.json').read_text()))==3
 verify_members(c,{x['member']:(work/'payload'/x['member']).read_bytes() for x in c['payload_members']});release_absence()
 _,ref=api('repos/'+REPO+'/git/ref/tags/v2.0.1');_,tag=api('repos/'+REPO+'/git/tags/'+TAG);assert ref['object']['sha']==TAG and tag['object']['sha']==SOURCE
 ledger={'release_creations':0,'asset_upload_attempts':[],'publish_attempts':0,'source':SOURCE,'recovery':who};write(work/'publication-ledger.json',ledger)
 body=(work/'release-body.md').read_text();assert c['known_issues'] in body
 ledger['release_creations']=1;write(work/'publication-ledger.json',ledger)
 _,release=api('repos/'+REPO+'/releases',method='POST',body={'tag_name':'v2.0.1','name':'AutoStream Control Panel v2.0.1','body':body,'draft':True,'prerelease':False,'target_commitish':SOURCE});rid=release['id'];assert release['draft'] and release['tag_name']=='v2.0.1'
 ledger['release_id']=rid;write(work/'publication-ledger.json',ledger)
 for item in c['payload_members']:
  n=item['member'];ledger['asset_upload_attempts'].append(n);write(work/'publication-ledger.json',ledger)
  r=command(['gh','api','--method','POST','-H','Content-Type: application/octet-stream','https://uploads.github.com/repos/'+REPO+'/releases/'+str(rid)+'/assets?name='+n,'--input',str(work/'payload'/n)])
  a=json.loads(r.stdout);assert a['name']==n and a['size']==item['bytes'] and a['digest']=='sha256:'+item['sha256']
 _,staged=api('repos/'+REPO+'/releases/'+str(rid));expect={x['member']:(x['bytes'],'sha256:'+x['sha256']) for x in c['payload_members']};assert {a['name']:(a['size'],a['digest']) for a in staged['assets']}==expect and staged['body']==body and staged['draft']
 ledger['publish_attempts']=1;write(work/'publication-ledger.json',ledger)
 _,published=api('repos/'+REPO+'/releases/'+str(rid),method='PATCH',body={'draft':False})
 _,readback=api('repos/'+REPO+'/releases/'+str(rid));assert not readback['draft'] and readback['tag_name']=='v2.0.1' and readback['body']==body and {a['name']:(a['size'],a['digest']) for a in readback['assets']}==expect
 write(work/'published-release.json',readback)
 if readback.get('immutable'):
  r=command(['gh','release','verify','v2.0.1','--repo',REPO,'--format','json']);(work/'immutable-release-verification.json').write_bytes(r.stdout)
 print('ORIGINAL_SEVEN_ASSETS_PUBLISHED',rid,readback['html_url'],flush=True)
if __name__=='__main__':
 a=argparse.ArgumentParser();a.add_argument('mode',choices=['prepare','verify-attestations','publish','local-verify']);a.add_argument('--work',type=pathlib.Path,required=True);a.add_argument('--zip',type=pathlib.Path);args=a.parse_args()
 if args.mode=='prepare':prepare(args.work)
 elif args.mode=='verify-attestations':verify_attestations(args.work)
 elif args.mode=='publish':publish(args.work)
 else:
  data=verify_zip(cfg(),args.zip);args.work.mkdir();out=[]
  for item in cfg()['release_manifest']['components'][0]['artifacts']:
   p=args.work/item['name'];p.write_bytes(data[item['name']]);out.append(bundle(p,item))
  write(args.work/'static-bundles.json',out);print('LOCAL_ORIGINAL_BYTES_VERIFIED_NO_BUILD')
