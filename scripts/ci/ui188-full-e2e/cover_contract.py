"""Consumer for the actual Node cover producer, for both distinct live streams."""
from gate import require, validate_identity

FIELDS = {'schema_version','identity','producer','stream_id','job_generation','revisions','hls','frames','protected_frames','off_frames','watermark_checked','normal_sandbox','console_errors','narrow_width'}


def verify_cover(proof, identity, slot, live):
    require(set(proof)==FIELDS, 'BLOCKED_COVER_SCHEMA')
    validate_identity(proof['identity'], identity)
    require(proof['schema_version']==1 and proof['producer']=='browser-'+slot and proof['stream_id']==live['stream_id'], 'BLOCKED_COVER_OWNER')
    require(type(proof['job_generation']) is int and proof['job_generation']>0, 'BLOCKED_COVER_GENERATION')
    if slot=='S2':
        require(proof['job_generation']==live['cover']['job_generation'], 'BLOCKED_COVER_GENERATION')
    phases=proof['revisions']
    require([p['phase'] for p in phases]==['A','B','C','OFF'], 'BLOCKED_COVER_PHASES')
    prior=-1
    for index,row in enumerate(phases):
        require(set(row)=={'phase','desired_revision','applied_revision','active','asset_variant_id'}, 'BLOCKED_COVER_REVISION_SCHEMA')
        require(type(row['applied_revision']) is int and row['applied_revision']==row['desired_revision'] and row['applied_revision']>prior and row['active'] is (index<3), 'BLOCKED_COVER_REVISION')
        prior=row['applied_revision']
        if index<3:
            require(row['asset_variant_id']==live['visual']['candidates'][index]['variant_id'], 'BLOCKED_COVER_ASSET')
    require(len(proof['hls'])==2 and {r['threshold'] for r in proof['hls']}=={580,900}, 'BLOCKED_COVER_HLS_DENOMINATOR')
    for row in proof['hls']:
        require(set(row)=={'threshold','before','after','frame_delta','media_delta'} and row['before']>=row['threshold'] and row['after']>row['before']+1 and row['frame_delta']>0 and row['media_delta']>1, 'BLOCKED_COVER_HLS_PROGRESS')
    require(proof['watermark_checked'] is True and proof['normal_sandbox'] is True and proof['console_errors']==0 and proof['narrow_width']==390, 'BLOCKED_COVER_RENDERING')
    require(proof['protected_frames']>=30 and proof['off_frames']>=30 and proof['frames']==proof['protected_frames']+proof['off_frames'], 'BLOCKED_COVER_FRAME_DENOMINATOR')
    return True
