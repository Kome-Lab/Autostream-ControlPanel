import copy,importlib.util,json,pathlib,sys,unittest,zipfile,stat
spec=importlib.util.spec_from_file_location('recover095',pathlib.Path(__file__).with_name('recover-release-095.py'));r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)
c=r.cfg();original=pathlib.Path(sys.argv.pop())
with zipfile.ZipFile(original) as z:data={n:z.read(n) for n in z.namelist()}
class FixedRecoveryTests(unittest.TestCase):
 def metadata(self):
  run={'id':36754237494,'run_attempt':1,'event':'push','head_branch':'v2.0.0','head_sha':r.SOURCE,'status':'completed','conclusion':'failure'}
  job={'id':110020288484,'run_id':run['id'],'head_sha':r.SOURCE,'run_attempt':1,'status':'completed','conclusion':'success'}
  ci={'id':36716775255,'head_sha':r.SOURCE,'run_attempt':1,'conclusion':'success'}
  ref={'object':{'type':'tag','sha':r.TAG}};tag={'object':{'sha':r.SOURCE,'type':'commit'},'verification':{'verified':True}}
  artifact=dict(c['artifact'],expired=False,workflow_run={'id':run['id'],'head_sha':r.SOURCE,'head_branch':'v2.0.0'})
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
 def test_changed_member_bytes_rejected(self):
  changed=dict(data);changed['release-manifest.json']=data['release-manifest.json'][:-1]+b'X'
  with self.assertRaises(AssertionError):r.verify_members(c,changed)
 def test_unexpected_member_rejected(self):
  changed=dict(data,extra=b'x')
  with self.assertRaises(AssertionError):r.verify_members(c,changed)
 def test_draft_and_release_block_creation(self):
  for draft in [True,False]:
   with self.subTest(draft=draft),self.assertRaises(AssertionError):r.require_absent(1,{'status':'404'},[{'tag_name':'v2.0.0','draft':draft}])
 def test_denial_not_absence(self):
  with self.assertRaises(AssertionError):r.require_absent(1,{'status':'403'},[])
 def test_path_and_link_rejected(self):
  expected={x['member']:(x['bytes'],x['sha256']) for x in c['payload_members']}
  for name in ['../release-manifest.json','/release-manifest.json','extra']:
   with self.assertRaises(AssertionError):r.safe_member(zipfile.ZipInfo(name),expected)
  info=zipfile.ZipInfo('release-manifest.json');info.file_size=946;info.external_attr=(stat.S_IFLNK|0o777)<<16
  with self.assertRaises(AssertionError):r.safe_member(info,expected)
if __name__=='__main__':unittest.main(verbosity=2)
