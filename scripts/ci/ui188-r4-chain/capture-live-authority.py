"""Supplemental same-B5 read-only authority/normal secret-followup capture."""
import json
import os
from pathlib import Path
import sys
from datetime import datetime, timezone

root=Path('/evidence');assert os.environ['UI188_DB_SCHEMA']=='ui188_r9_b5'
sys.path.insert(0,str(root/'chain-runner/scripts/ci/ui188-r4-chain'))
from api_client import API
api=API('live-authority-b5')
s1=json.loads((root/'acceptance-live-b5.json').read_text());s2=json.loads((root/'s2-live-b5.json').read_text())
audit=api.call('GET','/audit-logs?action=services.runtime_secret.resolve')
successes={row['resource_id'] for row in audit if row['action']=='services.runtime_secret.resolve' and row['result']=='success' and row['resource_id'] in ['bot-a','bot-b']}
assert successes=={'bot-a','bot-b'},'normal same-run BOT secret resolve audit required'
proof=[]
for service,stream_id,epoch in [('bot-a',s2['stream_id'],s2['after_epoch']),('bot-b',s1['stream_id'],json.loads((root/'chain-bot-b-runtime-sanitized-b5.json').read_text())['selected_assignment']['assigned_at'])]:
    current=api.call('GET','/service-health/'+service+'/runtime-config')
    selected=next(row for row in current['assignments'] if row['service_id']==service and row['stream_id']==stream_id)
    assert selected['assigned_at']==epoch
    proof.append({'service_id':service,'stream_id':stream_id,'same_current_assignment_epoch_as_live':True,'assigned_at':selected['assigned_at'],'normal_secret_resolve_success_audit_seen':True})
api.save('same-run-live-authority-b5.json',{'at_UTC':datetime.now(timezone.utc).isoformat(),'run':'B5','S0_normal_service_secret_followup':'PASS','current_assignments':proof,'manual_secret_resolve_success_POSTs':0,'mutating_requests_except_normal_auth_login':0,'aggregate':'PASS'})
print('SAME_B5_NORMAL_SECRET_FOLLOWUP_AND_LIVE_EPOCH_STABILITY_PASS')
