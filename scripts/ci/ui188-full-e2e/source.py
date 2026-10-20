"""Verify immutable product bytes without staging, filters or dependency re-resolution."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

from gate import Blocked, require

HERE = Path(__file__).resolve().parent


def tree_hash(rows):
    tree = {}
    for row in rows:
        node = tree
        parts = row['path'].split('/')
        require(all(p not in ('', '.', '..') and ':' not in p and '\\' not in p for p in parts), 'BLOCKED_SOURCE_PATH')
        for part in parts[:-1]:
            node = node.setdefault(part, {})
        node[parts[-1]] = (row['mode'], row['oid'])

    def digest(node):
        data = b''
        for name, value in sorted(node.items(), key=lambda kv: (kv[0] + ('/' if isinstance(kv[1], dict) else '')).encode()):
            mode, oid = ('40000', digest(value)) if isinstance(value, dict) else value
            data += mode.encode() + b' ' + name.encode() + b'\0' + bytes.fromhex(oid)
        return hashlib.sha1(b'tree ' + str(len(data)).encode() + b'\0' + data).hexdigest()
    return digest(tree)


def verify(root, component, manifest=None, lock=None):
    lock = lock or json.loads((HERE / 'source-lock.json').read_text())
    if manifest is None:
        raw_manifest = (HERE / 'product-files.json').read_bytes()
        require(hashlib.sha256(raw_manifest).hexdigest() == lock['source_manifest_sha256'], 'BLOCKED_SOURCE_MANIFEST')
        manifest = json.loads(raw_manifest)
    require(lock['components']['cp']['product_tree'] == '44ae3a676b6e9a462ea2a437bb8391f2198e7af4' and
            lock['components']['bot']['product_tree'] == '1141c416accf9fcb5d828f1174a7641863404972', 'BLOCKED_PRODUCT_AUTHORITY')
    require(lock['components']['encoder']['dispatch_commit'] == 'c51339fbe3d05b7ad96620c5a1adb83d2e5fc145' and
            lock['components']['worker']['dispatch_commit'] == '381790940e0fed8532d9396f0a2a40c0bfd0a612', 'BLOCKED_CROSS_REPO_PIN')
    require(lock['dave_gitlink'] == '52cd56dc550f447fb354b3a06c9e2d2e2a4309c6' and lock['observability'] == 'c2daf86c3ae621372dfde1c5afc3bfb54164ed62', 'BLOCKED_CROSS_REPO_PIN')
    require(lock['acceptance'] == {'two_stream_overlap_seconds':1000,'HLS_checkpoints':[580,900],
            'archive_per_stream':3,'socket_drops':0,'AAC_uncovered_over_50ms':0,'PCM_PTS_bands':[[250,400],[580,700],[800,950]],
            'S_denominator':6,'F_denominator':11,'group_denominator':28}, 'BLOCKED_ACCEPTANCE_RELAXED')
    rows = manifest[component]
    actual = []
    for row in rows:
        dest = root / row['path']
        require(not dest.is_symlink() and all(not p.is_symlink() for p in dest.parents if p != root.parent), 'BLOCKED_SOURCE_LINK')
        if row['mode'] == '160000':
            value = subprocess.check_output(['git', '-C', str(root), 'ls-tree', 'HEAD', row['path']], text=True).split()
            require(value[:3] == ['160000', 'commit', row['oid']], 'BLOCKED_DAVE_GITLINK')
            actual.append(dict(row))
            continue
        require(dest.is_file(), 'BLOCKED_SOURCE_MISSING')
        if os.name=='posix':
            require(bool(dest.stat().st_mode & 0o111)==(row['mode']=='100755'),'BLOCKED_SOURCE_MODE')
        raw = dest.read_bytes()
        require(hashlib.sha256(raw).hexdigest() == row['sha256'], 'BLOCKED_SOURCE_BYTES')
        oid = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
        require(oid == row['oid'], 'BLOCKED_SOURCE_BLOB')
        actual.append({**row, 'oid': oid})
    require(tree_hash(actual) == lock['components'][component]['product_tree'], 'BLOCKED_SOURCE_TREE')
    gomod = (root / 'go.mod').read_text()
    require(re.search(r'^go 1\.26\.5$', gomod, re.M), 'BLOCKED_GO_VERSION')
    require(lock['components'][component]['contracts_version'] in gomod, 'BLOCKED_CONTRACTS_PIN')
    return len(rows)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--component', choices=['cp', 'bot', 'encoder', 'worker'], required=True)
    args = parser.parse_args()
    try:
        print('SOURCE_PATHS_VERIFIED=' + str(verify(args.root.resolve(), args.component)))
    except (Blocked, OSError, ValueError, subprocess.SubprocessError):
        raise SystemExit('BLOCKED_SOURCE_INTEGRITY') from None


if __name__ == '__main__':
    main()
