import copy,importlib.util,json,pathlib,sys,unittest,zipfile,stat,tempfile,os
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('recover178',pathlib.Path(__file__).with_name('recover-release-178.py'));r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)
c=r.cfg();original=pathlib.Path(sys.argv.pop())
with zipfile.ZipFile(original) as z:data={n:z.read(n) for n in z.namelist()}
class FixedRecoveryTests(unittest.TestCase):
 def test_recovery_requires_explicit_publication_input(self):
  env={'GITHUB_REPOSITORY':r.REPO,'GITHUB_EVENT_NAME':'workflow_dispatch','GITHUB_REF':r.BRANCH,'RECOVERY_VERSION':'v2.0.1','GITHUB_SHA':'0'*40,'RECOVERY_PUBLICATION':'true'}
  with tempfile.TemporaryDirectory() as directory:
   event=pathlib.Path(directory)/'event.json';env['GITHUB_EVENT_PATH']=str(event)
   for value in [False,'false',None,1]:
    event.write_text(json.dumps({'inputs':{'version':'v2.0.1','publication_recovery':value}}),encoding='utf-8')
    with self.subTest(value=value),patch.dict(os.environ,env),self.assertRaises(AssertionError):r.identity()
   env['RECOVERY_PUBLICATION']='false'
   event.write_text(json.dumps({'inputs':{'version':'v2.0.1','publication_recovery':'true'}}),encoding='utf-8')
   with patch.dict(os.environ,env),self.assertRaises(AssertionError):r.identity()
 def test_wrong_dispatch_context_rejected(self):
  env={'GITHUB_REPOSITORY':r.REPO,'GITHUB_EVENT_NAME':'workflow_dispatch','GITHUB_REF':r.BRANCH,'RECOVERY_VERSION':'v2.0.1','RECOVERY_PUBLICATION':'true'}
  for key,value in [('GITHUB_REPOSITORY','Kome-Lab/Other'),('GITHUB_EVENT_NAME','push'),('GITHUB_REF','refs/heads/main'),('RECOVERY_VERSION','v2.0.2')]:
   changed=dict(env);changed[key]=value
   with self.subTest(key=key),patch.dict(os.environ,changed),self.assertRaises(AssertionError):r.identity()
 def test_wrong_recovery_branch_rejected(self):
  changed=copy.deepcopy(c);changed['recovery_branch']='unrelated-release-branch'
  with tempfile.TemporaryDirectory() as directory:
   inputs=pathlib.Path(directory)/'inputs.json';inputs.write_text(json.dumps(changed),encoding='utf-8')
   with patch.object(r,'INPUTS',inputs),self.assertRaises(AssertionError):r.cfg()
 def metadata(self):
  run={'id':37758047568,'run_attempt':1,'event':'push','head_branch':'v2.0.1','head_sha':r.SOURCE,'status':'completed','conclusion':'failure'}
  job={'id':113247388433,'run_id':run['id'],'head_sha':r.SOURCE,'run_attempt':1,'status':'completed','conclusion':'success'}
  ci={'id':37561937948,'head_sha':r.SOURCE,'run_attempt':1,'conclusion':'success'}
  ref={'object':{'type':'tag','sha':r.TAG}};tag={'object':{'sha':r.SOURCE,'type':'commit'},'verification':{'verified':True}}
  artifact=dict(c['artifact'],expired=False,workflow_run={'id':run['id'],'head_sha':r.SOURCE,'head_branch':'v2.0.1'})
  return [run,job,ci,ref,tag,artifact]
 def test_original_metadata_and_seven_payloads(self):r.verify_metadata(c,*self.metadata());r.verify_members(c,data)
 def test_wrong_run_rejected(self):
  m=self.metadata();m[0]['id']+=1
  with self.assertRaises(AssertionError):r.verify_metadata(c,*m)
 def test_wrong_source_rejected(self):
  m=self.metadata();m[0]['head_sha']='0'*40
  with self.assertRaises(AssertionError):r.verify_metadata(c,*m)
 def test_failed_build_rejected(self):
  m=self.metadata();m[1]['conclusion']='failure'
  with self.assertRaises(AssertionError):r.verify_metadata(c,*m)
 def test_wrong_zip_hash_rejected(self):
  m=self.metadata();m[5]['digest']='sha256:'+'0'*64
  with self.assertRaises(AssertionError):r.verify_metadata(c,*m)
 def test_wrong_tag_signature_ci_and_artifact_identity_rejected(self):
  cases=[(0,'head_branch','v2.0.2'),(1,'run_id',0),(1,'run_attempt',2),(2,'head_sha','0'*40),(2,'conclusion','failure'),(5,'id',0),(5,'name','unrelated'),(5,'expired',True)]
  for index,key,value in cases:
   with self.subTest(index=index,key=key,value=value),self.assertRaises(AssertionError):
    m=self.metadata();m[index][key]=value;r.verify_metadata(c,*m)
  for index,path,value in [(3,['object','sha'],'0'*40),(4,['object','sha'],'0'*40),(4,['verification','verified'],False),(5,['workflow_run','head_sha'],'0'*40),(5,['workflow_run','head_branch'],'v2.0.2')]:
   with self.subTest(index=index,path=path),self.assertRaises(AssertionError):
    m=self.metadata();m[index][path[0]][path[1]]=value;r.verify_metadata(c,*m)
 def test_changed_member_bytes_rejected(self):
  changed=dict(data);changed['release-manifest.json']=data['release-manifest.json'][:-1]+b'X'
  with self.assertRaises(AssertionError):r.verify_members(c,changed)
 def test_unexpected_member_rejected(self):
  changed=dict(data,extra=b'x')
  with self.assertRaises(AssertionError):r.verify_members(c,changed)
 def test_draft_and_release_block_creation(self):
  for draft in [True,False]:
   with self.subTest(draft=draft),self.assertRaises(AssertionError):r.require_absent(1,{'status':'404'},[{'tag_name':'v2.0.1','draft':draft}])
 def test_denial_not_absence(self):
  with self.assertRaises(AssertionError):r.require_absent(1,{'status':'403'},[])
 def test_path_and_link_rejected(self):
  expected={x['member']:(x['bytes'],x['sha256']) for x in c['payload_members']}
  for name in ['../release-manifest.json','/release-manifest.json','extra']:
   with self.assertRaises(AssertionError):r.safe_member(zipfile.ZipInfo(name),expected)
  info=zipfile.ZipInfo('release-manifest.json');info.file_size=946;info.external_attr=(stat.S_IFLNK|0o777)<<16
  with self.assertRaises(AssertionError):r.safe_member(info,expected)
if __name__=='__main__':unittest.main(verbosity=2)
