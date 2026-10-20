"""Closed denominators. Missing, failed and skipped proof never become PASS."""
import json
from pathlib import Path

from gate import GROUPS, require, validate_identity
from seal import verify_seal, sha_file


def stop_receipt(proof, stream_id):
    require(proof['before']['id'] == stream_id and proof['outcome']['stream']['id'] == stream_id, 'BLOCKED_STOP_OWNER')
    require(proof['outcome']['stop_confirmed'] is True and proof['stopPOSTs'] == 1, 'BLOCKED_STOP_RECEIPT')
    require(proof['detail']['status'] == proof['listed']['status'] and proof['detail']['status'] in ('ready', 'completed'), 'BLOCKED_STOP_CONVERGENCE')
    require(len(proof['actualLostResponse']) == 1 and proof['actualLostResponse'][0]['status'] == 200 and proof['confirmation']['inputs'] == 0, 'BLOCKED_STOP_RESEND_OR_CONFIRMATION')


def archive(proof, stream_id, owner):
    from datetime import datetime, timezone
    require(proof['stream']['id'] == stream_id and proof['DB_original_exit'] == proof['DB_detail_original_exit'] == 0, 'BLOCKED_ARCHIVE_OWNER')
    require(proof['DB_current_run_counts'] == [3, 3, 1, 1] and len(proof['artifacts']) == len(proof['DB_artifact_authority']) == 3, 'BLOCKED_ARCHIVE_DENOMINATOR')
    db = proof['DB_artifact_authority']
    require({r['kind'] for r in db} == {'archive', 'metadata', 'logs'} and len({r['id'] for r in db}) == 3, 'BLOCKED_ARCHIVE_KINDS')
    require({(r['id'], r['kind']) for r in db} == {(r['id'], r['kind']) for r in proof['artifacts']}, 'BLOCKED_ARCHIVE_CATALOG')
    require(all(r['source_service_id'] == owner and r['stream_id'] == stream_id and r['archive_run_id'] == proof['stream']['archive_run_id'] for r in db), 'BLOCKED_ARCHIVE_OWNER')
    def timestamp(value):
        dt = datetime.fromisoformat(value.replace('Z', '+00:00'))
        return dt if dt.tzinfo else dt.replace(tzinfo=timezone.utc)
    require(all(r['stream_id'] == stream_id and r['archive_run_id'] == proof['stream']['archive_run_id'] and timestamp(r['archive_started_at']) == timestamp(proof['stream']['archive_started_at']) for r in db + proof['artifacts']), 'BLOCKED_ARCHIVE_RUN')
    transport = proof['transport']
    require(transport['service_id'] == owner and transport['stream_id'] == stream_id and transport['actual_upstream_status'] == 202 and transport['body_sha256'] == transport['forwarded_body_sha256'], 'BLOCKED_ARCHIVE_REPORT')



def evaluate(root,identity,stages):
    from acceptance import evaluate as accept
    return accept(root,identity,stages)
