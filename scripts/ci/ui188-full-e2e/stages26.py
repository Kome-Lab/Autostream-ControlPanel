"""The 26 scenario obligations are distinct from process exits and 28 groups."""
import json
from gate import require, validate_identity
from producer_contract import outputs, stream_ids, validate_execution

# Historical administration rows are retained in stages.json. These named
# obligations retain the original scenario, capture, media and shutdown work.
CONTRACT = (
    ('T01', ('browser-canary',), ()),
    ('T02', (), ('OVERLAP',)),
    ('T03', ('cache-preflight',), ()),
    ('T04', ('audit-preflight',), ()),
    ('T05', ('runtime',), ('S0', 'F01')),
    ('T06', ('runtime',), ('S1',)),
    ('T07', ('s2',), ('S2', 'F04')),
    ('T08', ('live-authority',), ('PROVENANCE',)),
    ('T09', ('browser-S1', 'browser-S2'), ('COVER',)),
    ('T10', ('overlap',), ('OVERLAP',)),
    ('T11', ('hls-snapshot',), ('HLS',)),
    ('T12', ('s5',), ('S5', 'F08', 'F09', 'F10', 'F11')),
    ('T13', ('s4',), ('S4', 'F02', 'F05')),
    ('T14', ('state',), ('ARCHIVE', 'F03', 'F06')),
    ('T15', ('final-proof',), ('PROVENANCE',)),
    ('T16', ('s2',), ()),
    ('T17', ('runtime',), ()),
    ('T18', ('sampler',), ()),
    ('T19', ('capture',), ()),
    ('T20', ('capture',), ('RTP',)),
    ('T21', ('capture',), ('RTP',)),
    ('T22', ('capture', 'sampler'), ('SOCKET',)),
    ('T23', ('media-S1', 'media-S2'), ('AAC', 'PCM')),
    ('T24', ('receiver',), ('RECEIVER', 'F07')),
    ('T25', ('media-S1', 'media-S2'), ('FRAME',)),
    ('T26', (), ()),
)
NAMES = tuple(row[0] for row in CONTRACT)


def evaluate(root, identity, quality, stages, shutdown=None):
    require(len(NAMES) == len(set(NAMES)) == 26, 'BLOCKED_STAGE_DENOMINATOR')
    result = {name: 'NOT_RUN' for name in NAMES}
    expected = None
    try:
        expected = outputs(identity, stream_ids(root, identity))
        source_map = json.loads((root/'producer-sources.json').read_text())['sources']
    except (OSError, ValueError, KeyError):
        source_map = {}
    for name, producers, groups in CONTRACT[:-1]:
        values = [quality['groups'][g] for g in groups]
        if 'FAIL' in values or any(stages.get(p, {}).get('original_exit') not in (0, None) for p in producers):
            result[name] = 'FAIL'
            continue
        if any(v != 'PASS' for v in values) or expected is None or any(p not in stages for p in producers):
            continue
        try:
            for producer in producers:
                files = expected.get(producer, ['receiver-acceptance-'+identity['case']+'.json'])
                validate_execution(root, identity, stages[producer], files, source_map)
            if name == 'T02':
                worker = json.loads((root/'worker-b-liveness.json').read_text())
                validate_identity(worker['identity'], identity)
                require(worker['alive'] is True and worker['pid'] > 0, 'BLOCKED_WORKER_LIFETIME')
            result[name] = 'PASS'
        except FileNotFoundError:
            pass
        except Exception:
            result[name] = 'FAIL'
    if shutdown is not None:
        try:
            validate_shutdown(shutdown, identity)
            result['T26'] = 'PASS'
        except Exception:
            result['T26'] = 'FAIL'
    return result


def validate_shutdown(value, identity):
    require(set(value) == {'schema_version', 'identity', 'services', 'supervisor', 'worker', 'broker'} and value['schema_version'] == 1, 'BLOCKED_SHUTDOWN_SCHEMA')
    validate_identity(value['identity'], identity)
    require(set(value['services']) == {'runtime', 'worker-b', 'db'} and all(v == {'exit': 0, 'oom': False, 'running': False} for v in value['services'].values()), 'BLOCKED_CONTAINER_SHUTDOWN')
    for name in ('supervisor', 'worker', 'broker'):
        validate_identity(value[name]['identity'], identity)
    require(value['supervisor']['original_driver_exit'] == value['supervisor']['broker_exit'] == value['supervisor']['cleanup_exit'] == value['worker']['original_exit'] == value['broker']['original_exit'] == 0, 'BLOCKED_ORIGINAL_SHUTDOWN_EXIT')
    require(value['worker']['stop_requested'] is True and value['broker']['issued_taps'] == 2, 'BLOCKED_NORMAL_SHUTDOWN')
    return True


def finalize(root, shutdown):
    original = json.loads((root/'result.json').read_text())
    identity = json.loads((root/'identity.json').read_text())
    validate_identity(original['identity'], identity)
    stages = json.loads((root/'stages.json').read_text())
    logical = evaluate(root, identity, original, stages, shutdown)
    final = {**original, 'logical_stages': logical, 'logical_denominator': 26}
    final['aggregate'] = 'PASS' if original['quality_aggregate'] == 'PASS' and all(v == 'PASS' for v in logical.values()) else 'FAIL'
    (root/'final-result.json').write_text(json.dumps(final, indent=2)+'\n')
    return final
