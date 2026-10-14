"""Authenticated real CP requests. Stores status/code only, never credentials."""
import http.cookiejar,json,os,pathlib,time,urllib.request,urllib.error
class API:
 def __init__(self,label):
  self.evidence=pathlib.Path(os.environ['UI188_EVIDENCE_DIRECTORY']);self.label=label;self.events=[];self.csrf=''
  self.client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
  credentials=json.loads(pathlib.Path(os.environ['UI188_TEST_CREDENTIALS']).read_text())
  self.csrf=self.call('POST','/auth/login',{'username':'ui188-r4','password':credentials['admin_password']})['csrf_token']
 def call(self,method,path,body=None,status=200,base='http://127.0.0.1:18841'):
  headers={'Content-Type':'application/json','Accept':'application/json'}
  if self.csrf and base.endswith(':18841'):headers['X-CSRF-Token']=self.csrf
  request=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),method=method,headers=headers)
  try:
   with self.client.open(request,timeout=30) as response:code=response.status;data=response.read()
  except urllib.error.HTTPError as response:code=response.code;data=response.read()
  value=json.loads(data) if data else None
  self.events.append({'at':time.time(),'method':method,'path':path,'status':code,'code':value.get('code') if isinstance(value,dict) else None})
  (self.evidence/(self.label+'-requests.json')).write_text(json.dumps(self.events,indent=2)+'\n')
  assert code==status,f'{method} {path}: actual {code}, expected {status}'
  return value
 def wait(self,check,seconds=45):
  deadline=time.monotonic()+seconds;last=None
  while time.monotonic()<deadline:
   last=check()
   if last:return last
   time.sleep(.5)
  raise AssertionError('bounded real-state wait expired')
 def save(self,name,value):
  (self.evidence/name).write_text(json.dumps(value,indent=2)+'\n')
