"""Compile and exercise the actual Go constructor without DAVE, media or Discord."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

from gate import require
from run_identity import create, encoded, environment
from source import HERE, tree_hash, verify
from seal import sha_file


def prepare(bot, state):
    verify(bot, 'bot')
    destination = state / 'adapter'
    shutil.copytree(bot, destination, ignore=shutil.ignore_patterns('.git'))
    # Specify both roots explicitly; never use the checkout's index for patching.
    gitdir = subprocess.check_output(['git', '-C', str(bot), 'rev-parse', '--absolute-git-dir'], text=True).strip()
    command = ['git', '--git-dir='+gitdir, '--work-tree='+str(destination), '-c', 'core.autocrlf=false', 'apply']
    patch = HERE / 'bot-adapter.patch'
    lock = json.loads((HERE / 'source-lock.json').read_text())
    require(sha_file(patch) == lock['test_adapter']['patch_sha256'], 'BLOCKED_ADAPTER_PATCH')
    subprocess.run([*command, '--check', str(patch)], cwd=destination, check=True, capture_output=True)
    subprocess.run([*command, str(patch)], cwd=destination, check=True, capture_output=True)
    manifest = json.loads((HERE / 'product-files.json').read_text())['bot']
    modes = {row['path']: row['mode'] for row in manifest}
    for line in patch.read_text().splitlines():
        if line.startswith('+++ b/'):
            modes.setdefault(line[6:], '100644')
    rows = []
    for name, mode in modes.items():
        if mode == '160000':
            rows.append(next(row for row in manifest if row['path'] == name))
        else:
            data = (destination / name).read_bytes()
            rows.append({'path': name, 'mode': mode, 'oid': hashlib.sha1(b'blob '+str(len(data)).encode()+b'\0'+data).hexdigest()})
    require(tree_hash(rows) == lock['test_adapter']['tree'], 'BLOCKED_ADAPTER_TREE')
    directory = destination / 'cmd/discord-bot'
    source = directory / 'ui188_cb16_refresh_linux_test.go'
    declarations = re.findall(r'^const ui188CB16AssignmentConflictCode = "[a-z_]+"$', source.read_text(), re.M)
    require(len(declarations) == 1, 'BLOCKED_CONSTRUCTOR_DEPENDENCY')
    constant = directory / 'ui188_actions_smoke_const_test.go'
    constant.write_text('package main\n'+declarations[0]+'\n')
    files = [directory / name for name in ('ui188_mc13_heartbeat_linux_test.go', 'ui188_actions_identity_linux_test.go', constant.name)]
    return destination, files, rows


def compile_actual(bot, state):
    destination, files, rows = prepare(bot, state)
    binary = state / ('bot-constructor.exe' if os.name == 'nt' else 'bot-constructor')
    env = {**os.environ, 'GOWORK': 'off', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '2GiB', 'GIT_OPTIONAL_LOCKS': '0', 'CGO_ENABLED': '0'}
    with (state / 'compile.private.log').open('xb') as log:
        result = subprocess.run(['go', 'test', '-mod=readonly', '-p', '1', '-tags', 'ui188_chain', '-c', '-o', str(binary), *map(str, files)],
                                cwd=destination, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=150)
    require(result.returncode == 0, 'BLOCKED_ACTUAL_GO_CONSTRUCTOR_COMPILE')
    (state / 'compile-proof.json').write_text(json.dumps({'original_exit': result.returncode, 'binary_sha256': sha_file(binary),
        'actual_constructor_source_sha256': sha_file(files[0]), 'actual_identity_source_sha256': sha_file(files[1]),
        'adapter_tree': tree_hash(rows), 'DAVE_or_media_built': False})+'\n')
    return binary


def run_actual(binary, root, identity):
    root.mkdir(mode=0o700)
    manifest = root / 'identity.json'
    manifest.write_bytes(encoded(identity))
    env = {**os.environ, **environment(identity, manifest), 'UI188_EVIDENCE_DIRECTORY': str(root)}
    with (root / 'go-constructor.private.log').open('xb') as log:
        result = subprocess.run([str(binary), '-test.run', '^TestUI188ActionsActualConstructor$', '-test.v', '-test.timeout', '30s'],
                                env=env, stdout=log, stderr=subprocess.STDOUT, timeout=40)
    require(result.returncode == 0, 'BLOCKED_ACTUAL_GO_CONSTRUCTOR')
    value = json.loads((root / 'go-constructor-proof.json').read_text())
    require(set(value) == {'schema_version', 'actual_constructor', 'accepted_services', 'legacy_rejected', 'identity_sha256', 'run_uuid', 'case', 'schema', 'github_run_id', 'github_run_attempt'}, 'BLOCKED_CONSTRUCTOR_PRODUCER_SCHEMA')
    require(value['actual_constructor'] is True and value['legacy_rejected'] is True and value['accepted_services'] == ['bot-a', 'bot-b'] and value['schema_version'] == 1, 'BLOCKED_CONSTRUCTOR_PRODUCER')
    for name in ('run_uuid', 'case', 'schema', 'github_run_attempt'):
        require(value[name] == identity[name], 'BLOCKED_CONSTRUCTOR_IDENTITY')
    require(value['github_run_id'] == str(identity['github_run_id']) and value['identity_sha256'] == sha_file(manifest), 'BLOCKED_CONSTRUCTOR_IDENTITY')
    return {'original_exit': result.returncode, 'producer': 'ACTUAL_GO_CONSTRUCTOR', 'identity_sha256': value['identity_sha256'], 'scope': 'CONTRACT_SMOKE_ONLY'}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bot-root', type=Path, required=True)
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--compile-only', action='store_true')
    args = parser.parse_args()
    args.state.mkdir(mode=0o700, parents=True)
    binary = compile_actual(args.bot_root.resolve(), args.state.resolve())
    if not args.compile_only:
        lock = json.loads((HERE / 'source-lock.json').read_text())
        identity = create(sha_file(HERE / 'source-lock.json'), {name: row['dispatch_commit'] for name, row in lock['components'].items()},
                          os.environ.get('GITHUB_RUN_ID', '100'), job='contract-smoke')
        receipt = run_actual(binary, args.state.resolve() / 'run', identity)
        (args.state / 'constructor-receipt.json').write_text(json.dumps(receipt)+'\n')
    print('ACTUAL_GO_CONSTRUCTOR_CONTRACT_SMOKE_READY')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('BLOCKED_ADAPTER_CONTRACT_SMOKE')
        sys.exit(1)
