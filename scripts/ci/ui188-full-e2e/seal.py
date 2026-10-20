"""Freeze receiver inputs and binary identity; verify seals before and after analysis."""
import hashlib
import json
import os
from pathlib import Path
import sys

from gate import require, validate_identity


def sha_file(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def seal(root, identity, paths, expected=None):
    validate_identity(identity, identity)
    paths=list(paths)
    require(bool(paths) and len(paths)==len(set(paths)), 'BLOCKED_SEAL_EMPTY_OR_DUPLICATE')
    if expected is not None:
        require({p.relative_to(root).as_posix() for p in paths}==set(expected), 'BLOCKED_REQUIRED_RECEIVER_INPUTS')
    records = []
    for path in sorted(paths):
        require(not path.is_symlink() and path.is_file() and path.is_relative_to(root) and path.resolve().is_relative_to(root.resolve()), 'BLOCKED_SEAL_PATH')
        require(path.stat().st_mtime_ns >= int(identity['started_ns']), 'BLOCKED_STALE_RUN')
        records.append({'path': path.relative_to(root).as_posix(), 'sha256': sha_file(path), 'bytes': path.stat().st_size})
    return {'schema_version':2, 'kind':'FORMAL_RECEIVER_INPUTS' if expected is not None else 'FILE_INTEGRITY_ONLY', 'identity': identity, 'files': records}


def verify_seal(root, record, identity, expected=None):
    require(set(record)=={'schema_version','kind','identity','files'} and record['schema_version']==2, 'BLOCKED_SEAL_SCHEMA')
    validate_identity(record['identity'], identity)
    require(type(record['files']) is list and bool(record['files']), 'BLOCKED_SEAL_EMPTY')
    require(record['kind']==('FORMAL_RECEIVER_INPUTS' if expected is not None else 'FILE_INTEGRITY_ONLY'), 'BLOCKED_SEAL_KIND')
    if expected is not None:
        require(bool(expected) and {r['path'] for r in record['files']}==set(expected) and len(record['files'])==len(expected), 'BLOCKED_REQUIRED_RECEIVER_INPUTS')
    require(len({r['path'] for r in record['files']}) == len(record['files']), 'BLOCKED_SEAL_DUPLICATE')
    for item in record['files']:
        require(set(item)=={'path','sha256','bytes'} and type(item['bytes']) is int and item['bytes']>0, 'BLOCKED_SEAL_RECORD')
        path = root / item['path']
        require(not Path(item['path']).is_absolute() and '..' not in Path(item['path']).parts and ':' not in item['path'] and '\\' not in item['path'] and not path.is_symlink() and path.resolve().is_relative_to(root.resolve()), 'BLOCKED_SEAL_PATH')
        require(path.is_file() and sha_file(path) == item['sha256'] and path.stat().st_size == item['bytes'], 'BLOCKED_SEAL_CHANGED')


def build():
    lock = json.loads(Path('/opt/ui188/source-lock.json').read_text())
    rows = json.loads(Path('/opt/ui188/product-files.json').read_text())['bot']
    modes = {r['path']: r['mode'] for r in rows}
    patch = Path('/opt/ui188/bot-adapter.patch').read_bytes()
    require(hashlib.sha256(patch).hexdigest() == lock['test_adapter']['patch_sha256'], 'BLOCKED_ADAPTER_PATCH')
    for line in patch.decode().splitlines():
        if line.startswith('+++ b/'):
            modes.setdefault(line[6:], '100644')
    actual = []
    gitlinks = {r['path']: r for r in rows if r['mode'] == '160000'}
    for name, mode in modes.items():
        if mode == '160000':
            actual.append(gitlinks[name])
            continue
        data = (Path('/r6/bot-adapter') / name).read_bytes()
        actual.append({'path': name, 'mode': mode, 'oid': hashlib.sha1(b'blob '+str(len(data)).encode()+b'\0'+data).hexdigest()})
    from source import tree_hash
    require(tree_hash(actual) == lock['test_adapter']['tree'], 'BLOCKED_ADAPTER_TREE')
    binaries = [{'binary': p.name, 'sha256': sha_file(p)} for p in sorted(Path('/r6/bin').iterdir())]
    sources = {'schema_version':1,'sources':{}}
    for prefix,directory in [('chain',Path('/r6/cp/scripts/ci/ui188-r4-chain')),('platform',Path('/opt/ui188'))]:
        for file in sorted(directory.iterdir()):
            if file.is_file() and file.suffix in ('.py','.mts'):
                sources['sources'][prefix+'/'+file.name]=sha_file(file)
    Path('/r6/producer-sources.json').write_text(json.dumps(sources,sort_keys=True)+'\n')
    Path('/r6/build-provenance.json').write_text(json.dumps({'formal_binaries': binaries,
        'adapter_product': False, 'adapter_tree': lock['test_adapter']['tree'],
        'producer_sources_sha256':sha_file(Path('/r6/producer-sources.json')),
        'port_proof_sha256': sha_file(Path('/r6/port-proof.json'))}, indent=2) + '\n')
    import subprocess
    environment = {'os_release_sha256': sha_file(Path('/etc/os-release')), 'go': 'go1.26.5', 'node': 'v26.5.0',
                   'ffmpeg_version_sha256': hashlib.sha256(subprocess.check_output(['ffmpeg', '-version'])).hexdigest(),
                   'chromium_version_sha256': hashlib.sha256(subprocess.check_output(['chromium', '--version'])).hexdigest()}
    Path('/r6/environment.json').write_text(json.dumps(environment) + '\n')


if __name__ == '__main__':
    if sys.argv[1] == 'build':
        build()
