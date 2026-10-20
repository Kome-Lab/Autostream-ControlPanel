"""Bind the real receiver producer output to both distinct streams."""
from gate import HEX256, require, validate_identity


def validate(value, identity, sids, media_inputs):
    require(set(value)=={'schema_version','identity','producer','case','receivers','failures','simultaneous_hold','aggregate'},'BLOCKED_RECEIVER_SCHEMA')
    validate_identity(value['identity'],identity)
    require(value['schema_version']==2 and value['producer']=='receiver' and value['case']==identity['case'],'BLOCKED_DIAGNOSTIC_IS_NOT_FORMAL_RECEIVER')
    require(set(value['receivers'])=={'S1','S2'} and len(set(sids))==2,'BLOCKED_RECEIVER_DENOMINATOR')
    require(type(media_inputs) is list and len(media_inputs)==2 and {v['stream_id'] for v in media_inputs}==set(sids),'BLOCKED_RECEIVER_MEDIA_DENOMINATOR')
    for slot,sid,owner in zip(('S1','S2'),sids,('encoder-b','encoder-a')):
        row=value['receivers'][slot]
        require(row['stream_id']==sid and row['owner']==owner and HEX256.fullmatch(row['recording_sha256']),'BLOCKED_RECEIVER_STREAM_BINDING')
        media=next(v for v in media_inputs if v['stream_id']==sid)
        require(media['owner']==owner and row['recording_sha256']==media['sha256'],'BLOCKED_RECEIVER_RECORDING_BINDING')
    require(type(value['failures']) is list and value['aggregate'] in ('PASS','FAIL') and (value['aggregate']!='PASS' or not value['failures']),'BLOCKED_RECEIVER_FALSE_PASS')
    return value
