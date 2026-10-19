import hashlib
import json
import os
from pathlib import Path
import subprocess
import uuid

root=Path('/evidence');case=os.environ['UI188_PROBE_ATTEMPT'];schema=os.environ['UI188_DB_SCHEMA'];assert schema=='ui188_r9_b5'
credentials=json.loads((root/'runtime-test-credentials.json').read_text())
ids=[json.loads((root/name).read_text())['stream_id'] for name in ['acceptance-live-'+case+'.json','s2-live-'+case+'.json']]
assert all(str(uuid.UUID(sid))==sid for sid in ids)
where=','.join("'"+sid+"'" for sid in ids)
queries={'audits':"SELECT action,resource_id,result,COUNT(*) FROM audit_logs WHERE resource_id IN ("+where+") GROUP BY action,resource_id,result ORDER BY resource_id,action,result",
         'assignments':"SELECT COUNT(*) FROM stream_service_assignments WHERE stream_id IN ("+where+")",
         'tombstones':"SELECT id,status,deleted_at IS NOT NULL FROM streams WHERE id IN ("+where+") ORDER BY id"}
results={}
for name,query in queries.items():
    p=subprocess.run(['mariadb','--host=db','--user=ui188_r9','--batch','--raw','--skip-column-names',schema,'--execute',query],env={**os.environ,'MYSQL_PWD':credentials['db_password']},capture_output=True)
    assert p.returncode==0,'read-only '+name+' failed'
    results[name]=[line.split('\t') for line in p.stdout.decode().splitlines()]
starts=[row for row in results['audits'] if row[0]=='streams.start' and row[2]=='success']
assert len(starts)==2 and {r[1] for r in starts}==set(ids) and all(r[3]=='1' for r in starts)
if (root/('s4-state-success-'+case+'.json')).exists():
    assert results['assignments']==[['0']]
    assert len(results['tombstones'])==2 and all(row[2]=='1' for row in results['tombstones'])
expected=json.loads((root/'fixed-formal-binary-provenance.json').read_text())['formal_binaries']
assert all(hashlib.sha256((Path('/r6/bin')/row['binary']).read_bytes()).hexdigest()==row['sha256'] for row in expected)
(root/('final-runtime-proof-'+case+'.json')).write_text(json.dumps({'schema':schema,'read_only_DB_original_exits':[0,0,0],'safe_DB_results':results,'successful_start_exactly_once_each':True,'all_five_formal_binary_bytes_unchanged':True,'does_not_override_failed_steps':True},indent=2)+'\n')
print('FINAL_SAME_RUN_STARTS_ASSIGNMENT_TOMBSTONE_AND_BINARY_PROOF_CAPTURED')
