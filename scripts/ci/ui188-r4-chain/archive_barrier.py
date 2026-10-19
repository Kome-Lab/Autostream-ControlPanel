"""Same-run actual Encoder report gate, CP mutation guards and DB catalog proof."""
import json,os,subprocess
from datetime import datetime,timezone

def rows(value):
    # MariaDB's zero-row slice is JSON null; Memory's is []. Unknown shapes fail.
    assert value is None or isinstance(value,list),'catalog/list must be null or array'
    return [] if value is None else value

def verify_catalog_authority(artifacts,current,database_rows,encoder):
    assert len(artifacts)==3 and len(database_rows)==3,'exact artifact denominator'
    api_ids=[r['id'] for r in artifacts];db_ids=[r['id'] for r in database_rows]
    assert len(set(api_ids))==3 and set(api_ids)==set(db_ids),'same actual artifact IDs'
    expected_start=datetime.fromisoformat(current['archive_started_at'].replace('Z','+00:00'))
    for row in artifacts+database_rows:
        assert row['stream_id']==current['id'] and row['archive_run_id']==current['archive_run_id'],'same stream/run'
        at=datetime.fromisoformat(row['archive_started_at'].replace('Z','+00:00'))
        if at.tzinfo is None:at=at.replace(tzinfo=timezone.utc)
        assert at==expected_start,'same archive start'
    assert all(row['source_service_id']==encoder for row in database_rows),'actual DB owner'
    assert {r['kind'] for r in artifacts}=={'archive','metadata','logs'},'API kinds'
    assert {(r['id'],r['kind']) for r in artifacts}=={(r['id'],r['kind']) for r in database_rows},'DB/API ID kinds'

def catalog(api,sid,encoder):
    artifacts=rows(api.call('GET','/streams/'+sid+'/artifacts'));current=api.call('GET','/streams/'+sid)
    assert len(artifacts)==3 and current['archive_reported_at']
    assert all(c in '0123456789abcdef-' for c in sid)
    credentials=json.loads(open(os.environ['UI188_TEST_CREDENTIALS']).read())
    query="SELECT COUNT(*), COUNT(DISTINCT kind), COUNT(DISTINCT source_service_id), COUNT(DISTINCT archive_run_id) FROM stream_artifacts WHERE stream_id='"+sid+"' AND source_service_id='"+encoder+"';"
    p=subprocess.run(['mariadb','--host=db','--user=ui188_r9','--batch','--skip-column-names',os.environ['UI188_DB_SCHEMA'],'--execute',query],env={**os.environ,'MYSQL_PWD':credentials['db_password']},capture_output=True)
    assert p.returncode==0 and p.stdout.decode().split()==['3','3','1','1']
    detail="SELECT id,stream_id,archive_run_id,archive_started_at,kind,source_service_id FROM stream_artifacts WHERE stream_id='"+sid+"' ORDER BY id;"
    q=subprocess.run(['mariadb','--host=db','--user=ui188_r9','--batch','--skip-column-names',os.environ['UI188_DB_SCHEMA'],'--execute',detail],env={**os.environ,'MYSQL_PWD':credentials['db_password']},capture_output=True)
    assert q.returncode==0
    fields=['id','stream_id','archive_run_id','archive_started_at','kind','source_service_id'];database_rows=[]
    for line in q.stdout.decode().splitlines():
        cells=line.split('\t');assert len(cells)==len(fields);database_rows.append(dict(zip(fields,cells)))
    verify_catalog_authority(artifacts,current,database_rows,encoder)
    return {'stream':current,'artifacts':artifacts,'DB_current_run_counts':[3,3,1,1],'DB_artifact_authority':database_rows,'DB_original_exit':p.returncode,'DB_detail_original_exit':q.returncode}

def verify_barrier(api,sid,encoder,other,case,ui=False):
    transport=lambda:api.call('GET','/r9/transport',base='http://127.0.0.1:18841')
    report=api.wait(lambda:transport()['reports'].get(encoder),60)
    before=api.call('GET','/streams/'+sid);assert before['status'] in ['ready','completed'] and not before.get('archive_profile_id') and not before.get('archive_reported_at')
    assert report['stream_id']==sid and report['archive_run_id']==before['archive_run_id'] and report['archive_started_at']==before['archive_started_at'] and report['artifact_count']==3
    pending_catalog=api.call('GET','/streams/'+sid+'/artifacts')
    api.save('r9-pending-catalog-wire-'+sid+'-'+case+'.json',{'actual_HTTP200_JSON_value':pending_catalog})
    assert rows(pending_catalog)==[]
    config=api.call('GET','/service-health/'+encoder+'/runtime-config');assignment=next(a for a in config['assignments'] if a['stream_id']==sid)
    results=[]
    for method,path,body,code in [
        ('DELETE','/streams/'+sid,None,'service_unassign_protected_stream'),
        ('DELETE','/services/'+encoder+'/assignment',None,'service_unassign_protected_stream'),
        ('POST','/services/'+encoder+'/assign',{'stream_id':other,'assignment_role':'primary'},'service_assignment_protected_stream')]:
        result=api.call(method,path,body,status=409);assert result['code']==code;results.append({'method':method,'path':path,'code':code,'status':409})
    after=api.call('GET','/streams/'+sid);assert before==after
    after_assignment=next(a for a in api.call('GET','/service-health/'+encoder+'/runtime-config')['assignments'] if a['stream_id']==sid);assert assignment==after_assignment
    pending=rows(api.call('GET','/archive/processing-streams'));assert any(s['id']==sid for s in pending)
    evidence={'before':before,'after':after,'assignment':assignment,'assignment_unchanged':True,'pending_list_contains_run':True,'rejections':results,'real_held_report':report}
    api.save('r9-pending-'+sid+'-'+case+'.json',evidence)
    if ui:
        api.save('r9-pending-proof-'+case+'.json',{'stream_id':sid,'expected_code':'service_unassign_protected_stream'})
        api.wait(lambda:(api.evidence/('r9-pending-ui-observed-'+case)).exists(),15)
    api.call('POST','/r9/release',{'service_id':encoder},status=204,base='http://127.0.0.1:18841')
    completed=api.wait(lambda:(row if (row:=transport()['reports'][encoder]).get('client_response_delivered') else False),15)
    assert completed['actual_upstream_status']==202 and completed['body_sha256']==completed['forwarded_body_sha256']
    accepted=catalog(api,sid,encoder)
    assert not any(s['id']==sid for s in rows(api.call('GET','/archive/processing-streams')))
    api.save('r9-report-accepted-'+sid+'-'+case+'.json',{'transport':completed,**accepted})
    if ui:api.save('r9-report-ready-'+case+'.json',{'stream_id':sid,'artifact_count':3,'actual_upstream_status':202})
    return accepted
