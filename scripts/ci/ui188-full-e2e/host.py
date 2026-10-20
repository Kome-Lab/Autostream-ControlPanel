"""Actions host controller. Preparation has internet; live namespaces deny egress."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

from gate import Blocked, HEX256, SHA, require
from source import verify
from run_identity import create, encoded, environment

HERE = Path(__file__).resolve().parent


def sha_file(p):
    with p.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def command(argv, state, label, cwd=None, timeout=3600):
    # All stdout/stderr, including compiler, application and Docker output, is private.
    log = state / (label + '.private.log')
    with log.open('xb') as output:
        try:
            result = subprocess.run(argv, cwd=cwd, stdout=output, stderr=subprocess.STDOUT, timeout=timeout)
            code = result.returncode
        except subprocess.TimeoutExpired:
            code = 124
    (state / (label + '.exit.json')).write_text(json.dumps({'original_exit': code}) + '\n')
    require(code == 0, 'BLOCKED_PREPARATION_OR_RUNTIME')


def export(repo, dest):
    dest.mkdir(parents=True, exist_ok=False)
    data = subprocess.check_output(['git', '-C', str(repo), 'archive', '--format=tar', 'HEAD'])
    import io
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        archive.extractall(dest, filter='data')
    for row in subprocess.check_output(['git', '-C', str(repo), 'ls-tree', '-rz', 'HEAD']).split(b'\0'):
        if not row:
            continue
        meta, name = row.split(b'\t')
        mode, kind, oid = meta.split()
        if mode == b'160000':
            sub = repo / name.decode()
            require(subprocess.check_output(['git', '-C', str(sub), 'rev-parse', 'HEAD']).strip() == oid, 'BLOCKED_SUBMODULE_PIN')
            target = dest / name.decode()
            if target.exists():
                require(not any(target.iterdir()), 'BLOCKED_EXPORT_COLLISION')
                target.rmdir()
            export(sub, target)


def state_path():
    pointer = Path(os.environ['RUNNER_TEMP']) / 'ui188-pointer.json'
    info = json.loads(pointer.read_text())
    state = Path(info['state']).resolve()
    require(state.parent == Path(os.environ['RUNNER_TEMP']).resolve() and state.name == 'ui188-' + info['run_uuid'], 'BLOCKED_STATE_PATH')
    require(not state.is_symlink(), 'BLOCKED_STATE_PATH')
    return state


def compose(state, *args):
    info = json.loads((state / 'state.json').read_text())
    os.environ.update(info['compose_env'])
    return ['docker', 'compose', '--project-name', info['project'], '--file', str(HERE / 'compose.yml'), *args]


def owned_containers(state):
    info = json.loads((state / 'state.json').read_text())
    ids = subprocess.check_output(compose(state, 'ps', '-aq'), text=True).split()
    for ident in ids:
        require(len(ident) >= 12 and all(c in '0123456789abcdef' for c in ident), 'BLOCKED_CONTAINER_ID')
        record = json.loads(subprocess.check_output(['docker', 'inspect', ident]))[0]
        labels = record['Config']['Labels']
        require(labels.get('autostream.ui188.run') == info['run_uuid'] and labels.get('com.docker.compose.project') == info['project'], 'BLOCKED_CONTAINER_OWNER')
    return ids


def prepare():
    require(os.environ.get('GITHUB_EVENT_NAME') == 'workflow_dispatch' and os.environ.get('GITHUB_RUN_ATTEMPT') == '1', 'BLOCKED_TRIGGER')
    require(os.environ.get('GITHUB_REPOSITORY') == 'Kome-Lab/Autostream-ControlPanel' and os.environ.get('GITHUB_REF') == 'refs/heads/main', 'BLOCKED_OWNER')
    cp_root = HERE.parents[2]
    require(SHA.fullmatch(os.environ.get('CP_SHA', '')) and SHA.fullmatch(os.environ.get('BOT_SHA', '')), 'BLOCKED_UNTRUSTED_SHA')
    require(HEX256.fullmatch(os.environ.get('LOCK_SHA256', '')) and sha_file(HERE / 'source-lock.json') == os.environ['LOCK_SHA256'], 'BLOCKED_SOURCE_LOCK')
    require(subprocess.check_output(['git', '-C', str(cp_root), 'rev-parse', 'HEAD'], text=True).strip() == os.environ['CP_SHA'], 'BLOCKED_CP_COMMIT')
    lock = json.loads((HERE / 'source-lock.json').read_text())
    require(sha_file(HERE / 'product-files.json') == lock['source_manifest_sha256'], 'BLOCKED_SOURCE_MANIFEST')
    verify(cp_root, 'cp')
    run_uuid = str(uuid.uuid4())
    state = Path(os.environ['RUNNER_TEMP']) / ('ui188-' + run_uuid)
    state.mkdir(mode=0o700)
    (Path(os.environ['RUNNER_TEMP']) / 'ui188-pointer.json').write_text(json.dumps({'state': str(state), 'run_uuid': run_uuid}) + '\n')
    for name in ('sources', 'context', 'secrets', 'setup-secret', 'evidence'):
        (state / name).mkdir(mode=0o700)
    case = 'gha-' + run_uuid.replace('-', '')[:16]
    schema = 'ui188_gha_' + run_uuid.replace('-', '')
    identity = create(os.environ['LOCK_SHA256'], {'cp': os.environ['CP_SHA'], 'bot': os.environ['BOT_SHA'],
        'encoder': lock['components']['encoder']['dispatch_commit'], 'worker': lock['components']['worker']['dispatch_commit']},
        os.environ['GITHUB_RUN_ID'], run_uuid)
    identity_path = state / 'evidence/identity.json'
    identity_path.write_bytes(encoded(identity))
    identity_path.chmod(0o444)
    shutil.copyfile(Path(os.environ['RUNNER_TEMP']) / 'ui188-capacity.json', state / 'evidence/capacity.json')
    import socket
    import urllib.request
    with socket.create_connection(('1.1.1.1', 443), timeout=10):
        pass
    with urllib.request.urlopen('https://example.com/', timeout=15) as response:
        require(response.status == 200, 'BLOCKED_CANARY_POSITIVE_CONTROL')
    (state / 'evidence/preparation-internet-control.json').write_text(json.dumps({'VM_internet_positive': True}) + '\n')
    context = state / 'context'
    export(cp_root, context / 'cp')
    for component in ('bot', 'encoder', 'worker'):
        spec = lock['components'][component]
        commit = identity['source_shas'][component]
        require(SHA.fullmatch(commit), 'BLOCKED_SOURCE_COMMIT')
        repo = state / 'sources' / component
        command(['git', 'clone', '--no-checkout', '--filter=blob:none', 'https://github.com/' + spec['repository'] + '.git', str(repo)], state, 'clone-' + component)
        command(['git', '-C', str(repo), 'switch', '--detach', commit], state, 'select-' + component)
        require(subprocess.check_output(['git', '-C', str(repo), 'rev-parse', 'HEAD'], text=True).strip() == commit, 'BLOCKED_SOURCE_COMMIT')
        if component == 'bot':
            command(['git', '-C', str(repo), 'submodule', 'update', '--init', '--recursive'], state, 'dave-submodules')
        verify(repo, component)
        if component == 'bot':
            # vcpkg consumes its pinned Git history during the online preparation build.
            # This fresh public clone has no persisted checkout credential or private files.
            shutil.copytree(repo, context / component)
        else:
            export(repo, context / component)
    import secrets
    credentials = {k: secrets.token_hex(32) for k in ('db_password', 'encryption_key', 'ingest_key', 'setup_token', 'admin_password')}
    for filename, raw in [('credentials.json', json.dumps(credentials)), ('db_app', credentials['db_password']), ('db_root', secrets.token_hex(32))]:
        p = state / 'secrets' / filename
        p.write_text(raw)
        p.chmod(0o600)
    shutil.copyfile(state/'secrets/credentials.json',state/'setup-secret/credentials.json')
    (state/'setup-secret/credentials.json').chmod(0o600)
    image_file = state / 'image-id'
    args = ['docker', 'build', '--platform', 'linux/amd64', '--iidfile', str(image_file), '--label', 'autostream.ui188.run=' + run_uuid,
            '--build-arg', 'GO_IMAGE=' + lock['images']['go']['image'], '--build-arg', 'NODE_IMAGE=' + lock['images']['node']['image'],
            '--file', str(HERE / 'Dockerfile'), str(context)]
    command(args, state, 'prepare-image', timeout=3600)
    image = image_file.read_text().strip()
    require(image.startswith('sha256:') and HEX256.fullmatch(image[7:]), 'BLOCKED_IMAGE_ID')
    # Compose's unprivileged client can read its DB file secrets. Only the
    # runtime setup credential mount becomes root-only before container setup.
    command(['sudo', 'chown', '-R', '0:0', str(state / 'setup-secret')], state, 'setup-secret-owner', timeout=30)
    config = {'run_uuid': run_uuid, 'project': 'ui188-' + run_uuid.replace('-', ''),
              'compose_env': {'UI188_PRIVATE_DIR': str(state), 'UI188_RUN_UUID': run_uuid,
                              'UI188_DB_SCHEMA': schema, 'UI188_CASE': case, 'UI188_RUNTIME_IMAGE': image,
                              **environment(identity, '/evidence/identity.json')}}
    (state / 'state.json').write_text(json.dumps(config) + '\n')
    command(compose(state, 'config', '--quiet'), state, 'compose-config', timeout=30)
    print('PREPARATION_READY_LIVE_EGRESS_NOT_YET_VERIFIED')


def run():
    state = state_path()
    try:
        command(compose(state, 'up', '--detach'), state, 'compose-up', timeout=180)
        owned_containers(state)
        runtime = subprocess.check_output(compose(state, 'ps', '-q', 'runtime'), text=True).strip()
        with (state / 'docker-wait.private.log').open('xb') as log:
            p = subprocess.run(['docker', 'wait', runtime], stdout=log, stderr=subprocess.STDOUT, timeout=4500)
        require(p.returncode == 0, 'BLOCKED_RUNTIME_WAIT')
        raw = (state / 'docker-wait.private.log').read_text().strip()
        require(raw.isdecimal(), 'BLOCKED_RUNTIME_WAIT')
        # Evidence is UID1000/0700 until all containers have stopped.
        (state / 'runtime-exit.json').write_text(json.dumps({'original_exit': int(raw)}) + '\n')
        require(int(raw) == 0, 'FULL_E2E_FAILED_OR_NOT_RUN')
    finally:
        owned_containers(state)
        command(compose(state, 'stop', '--timeout', '30'), state, 'compose-stop', timeout=120)
        services = {}
        for name in ('runtime', 'worker-b', 'db'):
            cid = subprocess.check_output(compose(state, 'ps', '-aq', name), text=True).strip()
            item = json.loads(subprocess.check_output(['docker', 'inspect', cid]))[0]
            services[name] = {'exit': item['State']['ExitCode'], 'oom': item['State']['OOMKilled'], 'running': item['State']['Running']}
        (state/'shutdown-containers.json').write_text(json.dumps(services)+'\n')


def package():
    state = state_path()
    # Runtime uid differs from the ephemeral Actions login uid. Only this validated new directory.
    command(['sudo', 'chown', '-R', str(os.getuid()) + ':' + str(os.getgid()), str(state / 'evidence')], state, 'evidence-read-owner', timeout=60)
    command(['sudo', 'chown', '-R', str(os.getuid()) + ':' + str(os.getgid()), str(state / 'setup-secret')], state, 'retired-secret-read-owner', timeout=30)
    root=state/'evidence'
    if (state/'runtime-exit.json').is_file():
        shutil.copyfile(state/'runtime-exit.json',root/'runtime-exit.json')
    if (root/'result.json').is_file():
        identity=json.loads((root/'identity.json').read_text())
        shutdown=None
        if all((root/name).is_file() for name in ('supervisor-exit.json','worker-b-exit.json','tap-broker-exit.json')) and (state/'shutdown-containers.json').is_file():
            shutdown={'schema_version':1,'identity':identity,'services':json.loads((state/'shutdown-containers.json').read_text()),
                **{key:json.loads((root/name).read_text()) for key,name in (('supervisor','supervisor-exit.json'),('worker','worker-b-exit.json'),('broker','tap-broker-exit.json'))}}
            (root/'host-finalization.json').write_text(json.dumps(shutdown)+'\n')
        from stages26 import finalize
        finalize(root,shutdown)
    from publish import package_public
    package_public(state / 'evidence', state / 'secrets/credentials.json', Path.cwd() / 'ui188-public')
    print('PRIVATE_EVIDENCE_NOT_UPLOADED')


def cleanup():
    try:
        state = state_path()
    except (OSError, ValueError, Blocked):
        print('NO_OWNED_RUNTIME_CREATED')
        return
    if not (state / 'state.json').exists():
        print('NO_OWNED_COMPOSE_CREATED')
        return
    owned_containers(state)
    command(compose(state, 'down', '--volumes', '--remove-orphans'), state, 'owned-cleanup', timeout=120)
    print('OWNED_EPHEMERAL_RUNTIME_STOPPED')


if __name__ == '__main__':
    try:
        require(len(sys.argv) == 2 and sys.argv[1] in ('prepare', 'run', 'package', 'cleanup'), 'BLOCKED_COMMAND')
        globals()[sys.argv[1]]()
    except Blocked as error:
        print(str(error))
        sys.exit(1)
    except Exception:
        # Never print exception repr/argv: tools may include private application responses.
        print('BLOCKED_HOST_CONTROLLER')
        sys.exit(1)
