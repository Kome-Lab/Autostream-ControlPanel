"""Only schema-projected metadata and a generated status PNG may leave the runner."""
import base64
import binascii
import hashlib
import json
import math
import os
from pathlib import Path
import re
import struct
import tempfile
import urllib.parse
import uuid
import zipfile
import zlib

from gate import GROUPS, HEX256, SHA, require, validate_identity
from seal import sha_file
from producer_contract import PRE_PRODUCERS
from stages26 import NAMES

STAGES = (*PRE_PRODUCERS, 'receiver', 'receiver-diagnostic', 'receiver-input-gate', 'driver', 'gate-or-analysis', 'container')
PUBLIC_FILES = {'summary.json', 'summary.png'}


def finite(value, minimum=0):
    require(type(value) in (int, float) and math.isfinite(value) and value >= minimum, 'BLOCKED_PUBLIC_NUMBER')
    return value


def validate_summary(summary):
    require(set(summary) == {'schema_version', 'run_uuid', 'case', 'job', 'stream_ids', 'environment', 'evidence_hashes', 'github_run_id', 'github_run_attempt', 'source_shas', 'source_lock_sha256', 'groups', 'original_exits', 'aggregate', 'denominator', 'metrics', 'known_issue', 'windows', 'private_evidence', 'logical_stages', 'logical_denominator', 'failure_metadata', 'image_kind', 'private_raw_storage'}, 'BLOCKED_PUBLIC_SCHEMA')
    run=uuid.UUID(summary['run_uuid'])
    require(summary['schema_version'] == 2 and str(run) == summary['run_uuid'] and run.version==4 and summary['case']=='gha-'+run.hex[:16], 'BLOCKED_PUBLIC_SCHEMA')
    require(type(summary['github_run_id']) is int and summary['github_run_id'] > 0 and summary['github_run_attempt'] == 1, 'BLOCKED_PUBLIC_RUN')
    require(re.fullmatch(r'gha-[0-9a-f]{16}', summary['case']) and summary['job'] == 'full-e2e', 'BLOCKED_PUBLIC_RUN')
    require(set(summary['stream_ids']) <= {'S1', 'S2'} and all(str(uuid.UUID(v)) == v for v in summary['stream_ids'].values()), 'BLOCKED_PUBLIC_STREAM')
    environment = summary['environment']
    require(set(environment) <= {'os_release_sha256', 'go', 'node', 'ffmpeg_version_sha256', 'chromium_version_sha256'}, 'BLOCKED_PUBLIC_ENVIRONMENT')
    for k, value in environment.items():
        require((value == ('go1.26.5' if k == 'go' else 'v26.5.0')) if k in ('go', 'node') else bool(HEX256.fullmatch(value)), 'BLOCKED_PUBLIC_ENVIRONMENT')
    require(set(summary['evidence_hashes']) <= {'build', 'receiver_seal', 'media_inputs', 'cgroup_observer', 'environment'}, 'BLOCKED_PUBLIC_HASH')
    require(all(HEX256.fullmatch(v) for v in summary['evidence_hashes'].values()), 'BLOCKED_PUBLIC_HASH')
    require(set(summary['source_shas']) == {'cp', 'bot', 'encoder', 'worker'} and all(SHA.fullmatch(v) for v in summary['source_shas'].values()), 'BLOCKED_PUBLIC_SOURCE')
    require(HEX256.fullmatch(summary['source_lock_sha256']), 'BLOCKED_PUBLIC_SOURCE')
    require(set(summary['groups']) == set(GROUPS) and all(v in ('PASS', 'FAIL', 'NOT_RUN') for v in summary['groups'].values()), 'BLOCKED_PUBLIC_DENOMINATOR')
    require(summary['denominator'] == len(GROUPS) and summary['aggregate'] in ('PASS', 'FAIL', 'NOT_RUN'), 'BLOCKED_PUBLIC_DENOMINATOR')
    require(set(summary['logical_stages'])==set(NAMES) and summary['logical_denominator']==26 and all(v in ('PASS','FAIL','NOT_RUN') for v in summary['logical_stages'].values()),'BLOCKED_PUBLIC_STAGE_DENOMINATOR')
    require(set(summary['original_exits']) <= set(STAGES) and all(v is None or type(v) is int and -255 <= v <= 255 for v in summary['original_exits'].values()), 'BLOCKED_PUBLIC_EXIT')
    require(summary['aggregate'] != 'PASS' or all(v == 'PASS' for v in summary['groups'].values()) and all(v == 'PASS' for v in summary['logical_stages'].values()) and set(summary['original_exits'])==set(PRE_PRODUCERS)|{'receiver','container'} and all(v == 0 for v in summary['original_exits'].values()), 'BLOCKED_FALSE_PASS')
    require(summary['known_issue'] == 'KNOWN_UNRESOLVED_MEDIA_ISSUE' and summary['windows'] == 'ENV_NOT_COVERED_BY_LINUX' and summary['private_evidence'] == 'PRIVATE_EVIDENCE_NOT_UPLOADED', 'BLOCKED_PUBLIC_SCOPE')
    require(summary['image_kind']=='ENUM_STATUS_NOT_UI_CAPTURE' and summary['private_raw_storage']=='NOT_CONFIGURED','BLOCKED_PUBLIC_SCOPE')
    failures=summary['failure_metadata']
    require(type(failures) is list and len(failures)<=len(STAGES) and len({v['stage'] for v in failures})==len(failures),'BLOCKED_PUBLIC_FAILURE')
    require({v['stage'] for v in failures}=={k for k,v in summary['original_exits'].items() if v not in (0,None)},'BLOCKED_PUBLIC_FAILURE')
    for row in failures:
        require(set(row)=={'stage','original_exit','start_offset_ms','duration_ms','source_sha256','output_count','collection_scope'} and row['stage'] in STAGES and row['original_exit']==summary['original_exits'][row['stage']],'BLOCKED_PUBLIC_FAILURE')
        require(type(row['output_count']) is int and 0<=row['output_count']<=50 and row['collection_scope'] in ('EXECUTION_RECEIPT','EXIT_ONLY'),'BLOCKED_PUBLIC_FAILURE')
        require(all(v is None or type(v) is int and 0<=v<=86400000 for v in (row['start_offset_ms'],row['duration_ms'])) and (row['source_sha256'] is None or HEX256.fullmatch(row['source_sha256'])),'BLOCKED_PUBLIC_FAILURE')
    metrics = summary['metrics']
    require(set(metrics) <= {'capacity', 'S1', 'S2', 'cgroup'}, 'BLOCKED_PUBLIC_METRIC')
    if 'capacity' in metrics:
        require(set(metrics['capacity']) == {'cpu', 'memory_bytes', 'disk_free_bytes'}, 'BLOCKED_PUBLIC_METRIC')
        for v in metrics['capacity'].values():
            finite(v)
    for key in ('S1', 'S2'):
        if key in metrics:
            require(set(metrics[key]) <= {'max_kernel_drops', 'RTP_packets', 'AAC_max_uncovered_seconds', 'socket_samples'}, 'BLOCKED_PUBLIC_METRIC')
            for value in metrics[key].values():
                finite(value)
    if 'cgroup' in metrics:
        require(set(metrics['cgroup']) <= {'samples', 'memory_peak_bytes', 'cpu_usage_usec', 'nr_throttled', 'oom_kill'}, 'BLOCKED_PUBLIC_METRIC')
        for value in metrics['cgroup'].values():
            finite(value)


def project(root):
    identity = json.loads((root / 'identity.json').read_text())
    validate_identity(identity, identity)
    path=root/('final-result.json' if (root/'final-result.json').is_file() else 'result.json')
    raw = json.loads(path.read_text()) if path.is_file() else {}
    if raw:
        validate_identity(raw['identity'], identity)
    stages = json.loads((root / 'stages.json').read_text()) if (root / 'stages.json').is_file() else {}
    exits = {k: v.get('original_exit') for k, v in stages.items()}
    if (root / 'runtime-exit.json').exists():
        exits['container'] = json.loads((root / 'runtime-exit.json').read_text())['original_exit']
    groups = raw.get('groups', {g: 'NOT_RUN' for g in GROUPS})
    result = {'schema_version': 2, 'run_uuid': identity['run_uuid'], 'case': identity['case'], 'job': 'full-e2e',
              'stream_ids': {}, 'environment': {}, 'evidence_hashes': {}, 'github_run_id': identity['github_run_id'],
              'github_run_attempt': identity['github_run_attempt'], 'source_shas': identity['source_shas'],
              'source_lock_sha256': identity['source_lock_sha256'], 'groups': groups, 'original_exits': exits,
              'aggregate': raw.get('aggregate', 'FAIL' if any(v not in (None, 0) for v in exits.values()) else 'NOT_RUN'), 'denominator': len(GROUPS), 'metrics': {},
              'known_issue': 'KNOWN_UNRESOLVED_MEDIA_ISSUE', 'windows': 'ENV_NOT_COVERED_BY_LINUX',
              'private_evidence': 'PRIVATE_EVIDENCE_NOT_UPLOADED', 'private_raw_storage':'NOT_CONFIGURED','image_kind':'ENUM_STATUS_NOT_UI_CAPTURE',
              'logical_stages':raw.get('logical_stages',{name:'NOT_RUN' for name in NAMES}),'logical_denominator':26,'failure_metadata':[]}
    if result['aggregate']=='PASS' and not (root/'final-result.json').is_file():result['aggregate']='NOT_RUN'
    for label,code in exits.items():
        if code in (0,None):continue
        row=stages.get(label,{})
        started,finished=row.get('started_ns'),row.get('finished_ns')
        result['failure_metadata'].append({'stage':label,'original_exit':code,'start_offset_ms':None if type(started) is not int else (started-int(identity['started_ns']))//1000000,
            'duration_ms':None if type(started) is not int or type(finished) is not int else (finished-started)//1000000,'source_sha256':row.get('source_sha256'),
            'output_count':len(row.get('outputs',[])),'collection_scope':'EXECUTION_RECEIPT' if 'invocation_uuid' in row else 'EXIT_ONLY'})
    for group, filename in [('S1', 'acceptance-live-'), ('S2', 's2-live-')]:
        p = root / (filename + identity['case'] + '.json')
        if p.is_file():
            result['stream_ids'][group] = json.loads(p.read_text())['stream_id']
    if (root / 'environment.json').is_file():
        environment = json.loads((root / 'environment.json').read_text())
        result['environment'] = {k: environment[k] for k in ('os_release_sha256', 'go', 'node', 'ffmpeg_version_sha256', 'chromium_version_sha256')}
    for key, filename in [('build', 'fixed-formal-binary-provenance.json'), ('receiver_seal', 'receiver-inputs-sealed.json'),
                          ('media_inputs', 'media-inputs.json'), ('cgroup_observer', 'consumer-' + identity['case'] + '.jsonl'), ('environment', 'environment.json')]:
        if (root / filename).is_file():
            result['evidence_hashes'][key] = sha_file(root / filename)
    consumer = root / ('consumer-' + identity['case'] + '.jsonl')
    if consumer.is_file():
        metric = {'samples': 0, 'memory_peak_bytes': 0, 'cpu_usage_usec': 0, 'nr_throttled': 0, 'oom_kill': 0}
        with consumer.open() as stream:
            for line in stream:
                value = json.loads(line)
                cpu = dict(row.split() for row in value['cpu_stat'].splitlines())
                events = dict(row.split() for row in value['memory_events'].splitlines())
                metric['samples'] += 1
                for key, number in [('memory_peak_bytes', value['memory_peak']), ('cpu_usage_usec', cpu['usage_usec']),
                                    ('nr_throttled', cpu['nr_throttled']), ('oom_kill', events['oom_kill'])]:
                    metric[key] = max(metric[key], int(number))
        result['metrics']['cgroup'] = metric
    if (root / 'capacity.json').is_file():
        capacity = json.loads((root / 'capacity.json').read_text())
        result['metrics']['capacity'] = {k: capacity[k] for k in ('cpu', 'memory_bytes', 'disk_free_bytes')}
    receiver = root / ('receiver-acceptance-' + identity['case'] + '.json')
    if receiver.is_file():
        values = json.loads(receiver.read_text())['receivers']
        for key in ('S1', 'S2'):
            if key not in values:
                continue
            value = values[key]
            safe = {}
            if 'max_kernel_drops' in value:
                safe['max_kernel_drops'] = value['max_kernel_drops']
            if 'RTP' in value:
                safe['RTP_packets'] = value['RTP']['count']
            if 'AAC' in value:
                safe['AAC_max_uncovered_seconds'] = value['AAC']['max_uncovered']
            if 'actual_receive_buffer' in value:
                safe['socket_samples'] = value['actual_receive_buffer']['samples']
            result['metrics'][key] = safe
    validate_summary(result)
    return result


def status_png(summary):
    # Synthesized from enums. Raw browser screenshots are never copied or claimed sanitized.
    width, height = 280, 160
    colors = {'PASS': (40, 160, 80), 'FAIL': (200, 50, 50), 'NOT_RUN': (140, 140, 140)}
    rows = []
    for y in range(height):
        row = bytearray()
        for x in range(width):
            index = (y // 40) * 7 + x // 40
            row.extend(colors[summary['groups'][GROUPS[index]]])
        rows.append(b'\0' + bytes(row))
    def chunk(kind, data):
        return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', binascii.crc32(kind + data) & 0xffffffff)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!2I5B', width, height, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(b''.join(rows))) + chunk(b'IEND', b'')


def scan_public(root, secrets):
    entries = list(root.iterdir())
    require({p.name for p in entries} == PUBLIC_FILES and all(p.is_file() and not p.is_symlink() for p in entries), 'BLOCKED_PUBLIC_PATH_ALLOWLIST')
    summary = json.loads((root / 'summary.json').read_text())
    validate_summary(summary)
    expected_png = status_png(summary)
    require((root / 'summary.png').read_bytes() == expected_png, 'BLOCKED_UNREVIEWED_PNG')
    forbidden = re.compile(rb'authorization\s*[:=]|bearer\s+|cookie\s*:|mysql://|@tcp\(|-----BEGIN [A-Z ]*PRIVATE KEY', re.I)
    variants = set()
    for value in secrets:
        require(isinstance(value, str) and len(value) >= 8, 'BLOCKED_SECRET_SCANNER_INPUT')
        variants.update([value.encode(), base64.b64encode(value.encode()), urllib.parse.quote(value, safe='').encode()])
    for p in entries:
        data = p.read_bytes()
        require(len(data) <= 128 * 1024 and not forbidden.search(data) and not any(v in data for v in variants), 'BLOCKED_PUBLIC_SECRET_SCAN')
    return summary


def verify_archive(path, expected):
    require(not path.is_symlink(), 'BLOCKED_PUBLIC_PATH')
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        require(len(names) == len(set(names)) == 3 and set(names) == PUBLIC_FILES | {'MANIFEST.json'}, 'BLOCKED_ARTIFACT_MEMBERS')
        require(archive.testzip() is None, 'BLOCKED_ARTIFACT_CRC')
        manifest = json.loads(archive.read('MANIFEST.json'))
        require(manifest == expected, 'BLOCKED_ARTIFACT_MANIFEST')
        for name, record in manifest.items():
            data = archive.read(name)
            require(len(data) == record['bytes'] and hashlib.sha256(data).hexdigest() == record['sha256'], 'BLOCKED_ARTIFACT_HASH')


def package_public(root, credentials_path, output):
    require(not output.exists() and not root.is_symlink() and not credentials_path.is_symlink(), 'BLOCKED_PUBLIC_OUTPUT_REUSE')
    credentials = json.loads(credentials_path.read_text())
    summary = project(root)
    # A temporary publication directory is populated from schema projection only.
    with tempfile.TemporaryDirectory(prefix='ui188-public-', dir=output.parent) as directory:
        candidate = Path(directory)
        (candidate / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
        (candidate / 'summary.png').write_bytes(status_png(summary))
        scan_public(candidate, list(credentials.values()))
        manifest = {p.name: {'sha256': sha_file(p), 'bytes': p.stat().st_size} for p in sorted(candidate.iterdir())}
        output.mkdir(mode=0o700)
        target = output / 'evidence.zip'
        with zipfile.ZipFile(target, 'x', compression=zipfile.ZIP_DEFLATED) as archive:
            for name in sorted(PUBLIC_FILES):
                archive.write(candidate / name, name)
            archive.writestr('MANIFEST.json', json.dumps(manifest, indent=2) + '\n')
        verify_archive(target, manifest)
        (output / 'VERIFIED.json').write_text(json.dumps({'sha256': sha_file(target), 'bytes': target.stat().st_size,
            'CRC': 'PASS', 'members': 3, 'payload_hashes_verified': 2, 'private_evidence': 'PRIVATE_EVIDENCE_NOT_UPLOADED'}, indent=2) + '\n')
