"""One immutable runtime identity shared by the host, adapter and proof producers."""
import hashlib
import json
import os
from pathlib import Path
import re
import time
import uuid

FIELDS = {'schema_version', 'scope', 'run_uuid', 'case', 'schema', 'services', 'source_lock_sha256',
          'github_run_id', 'github_run_attempt', 'job', 'started_ns', 'source_shas'}
ROLES = ('bot-a', 'bot-b', 'encoder-a', 'encoder-b', 'worker-a', 'worker-b')


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('BLOCKED_DUPLICATE_IDENTITY_KEY')
        result[key] = value
    return result


def decode(raw):
    return json.loads(raw, object_pairs_hook=unique_object)


def validate(identity):
    if set(identity) != FIELDS:
        raise ValueError('BLOCKED_IDENTITY_FIELDS')
    value = uuid.UUID(identity['run_uuid'])
    if str(value) != identity['run_uuid'] or value.version != 4:
        raise ValueError('BLOCKED_IDENTITY_UUID')
    expected = {'schema_version': 2, 'scope': 'UI188_ACTIONS_RUN', 'case': 'gha-'+value.hex[:16],
                'schema': 'ui188_gha_'+value.hex, 'services': {role: role for role in ROLES},
                'github_run_attempt': 1}
    if any(identity[k] != v for k, v in expected.items()):
        raise ValueError('BLOCKED_IDENTITY_BINDING')
    if identity['job'] not in ('full-e2e', 'contract-smoke'):
        raise ValueError('BLOCKED_IDENTITY_JOB')
    if type(identity['github_run_id']) is not int or not 1 <= identity['github_run_id'] <= 2**53-1 or type(identity['github_run_attempt']) is not int:
        raise ValueError('BLOCKED_IDENTITY_GITHUB_RUN')
    if not isinstance(identity['started_ns'], str) or not re.fullmatch('[1-9][0-9]{18}', identity['started_ns']) or int(identity['started_ns']) > time.time_ns():
        raise ValueError('BLOCKED_IDENTITY_TIME')
    if not re.fullmatch('[0-9a-f]{64}', identity['source_lock_sha256']):
        raise ValueError('BLOCKED_IDENTITY_LOCK')
    if set(identity['source_shas']) != {'cp', 'bot', 'encoder', 'worker'} or not all(re.fullmatch('[0-9a-f]{40}', v) for v in identity['source_shas'].values()):
        raise ValueError('BLOCKED_IDENTITY_SOURCE')
    return identity


def create(lock_sha256, source_shas, run_id, run_uuid=None, started_ns=None, job='full-e2e'):
    value = uuid.UUID(run_uuid) if run_uuid else uuid.uuid4()
    return validate(dict(schema_version=2, scope='UI188_ACTIONS_RUN', run_uuid=str(value),
        case='gha-'+value.hex[:16], schema='ui188_gha_'+value.hex, services={role: role for role in ROLES},
        source_lock_sha256=lock_sha256, github_run_id=int(run_id), github_run_attempt=1, job=job,
        started_ns=str(started_ns if started_ns is not None else time.time_ns()), source_shas=source_shas))


def encoded(identity):
    validate(identity)
    return (json.dumps(identity, sort_keys=True, separators=(',', ':'))+'\n').encode()


def environment(identity, path):
    validate(identity)
    return {'UI188_RUN_UUID': identity['run_uuid'], 'UI188_WAIT_CALLER_RUN_ID': identity['run_uuid'],
        'UI188_PROBE_ATTEMPT': identity['case'], 'UI188_DB_SCHEMA': identity['schema'],
        'UI188_GITHUB_RUN_ID': str(identity['github_run_id']), 'UI188_GITHUB_RUN_ATTEMPT': '1',
        'UI188_IDENTITY_JOB': identity['job'],
        'UI188_SOURCE_LOCK_SHA256': identity['source_lock_sha256'],
        'UI188_RUN_IDENTITY': str(path), 'UI188_IDENTITY_SHA256': hashlib.sha256(encoded(identity)).hexdigest()}


def read_bound(path=None, env=None):
    env = os.environ if env is None else env
    target = Path(path or env['UI188_RUN_IDENTITY'])
    if not target.is_absolute() or target.is_symlink() or target.name != 'identity.json':
        raise ValueError('BLOCKED_IDENTITY_PATH')
    raw = target.read_bytes()
    if len(raw) > 16384 or hashlib.sha256(raw).hexdigest() != env.get('UI188_IDENTITY_SHA256'):
        raise ValueError('BLOCKED_IDENTITY_DIGEST')
    identity = validate(decode(raw))
    if raw != encoded(identity) or any(env.get(k) != v for k, v in environment(identity, target).items()):
        raise ValueError('BLOCKED_IDENTITY_ENVIRONMENT')
    return identity
