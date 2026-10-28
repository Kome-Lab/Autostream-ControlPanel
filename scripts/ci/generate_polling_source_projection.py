#!/usr/bin/env python3
"""Generate only the new, reviewed 65a polling/create/upload source layer.

This does not update any earlier fixture, validator, historical pin or oracle.
After the first generation, changing the explicit path set requires review and
--accept-path-changes. Read the actual staged immutable tree after source freeze;
Git clean-filter previews are not authority for published bytes.
"""
from __future__ import annotations

import argparse
import difflib
import hashlib
import json
from pathlib import Path
import re
import subprocess


BASE = "65a601fc9d4f15c93083e9be4ab9e71586fdba69"
FIXTURE = "web/tests/fixtures/ui-regression/polling-current-source-delta.json"
CONTRACT = "web/tests/ui-regression/polling-current-source-contract.mts"
LEGACY = "web/tests/fixtures/ui-regression/polling-legacy-parity.json"
ENTRY = "web/tests/ui-regression/parity.test.mts"
INFRASTRUCTURE = [
    "scripts/ci/generate_polling_source_projection.py",
    FIXTURE, CONTRACT, LEGACY,
    "web/tests/ui-regression/polling-current-source-delta.mts",
    "web/tests/ui-regression/polling-current-source-delta.test.mts",
    "web/tests/ui-regression/polling-fixed-parity.mts",
    "web/tests/ui-regression/run-polling-current-parity.mts",
]
NESTED = "Linux native overall shell; Windows never discovers or launches a Bash/WSL alias"


def git(root: Path, *args: str, body: bytes | None = None) -> bytes:
    result = subprocess.run(["git", "-C", str(root), *args], input=body,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            check=True, timeout=60)
    return result.stdout


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def blob(data: bytes) -> str:
    return hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest()


def encode(value: object) -> bytes:
    return (json.dumps(value, indent=2, ensure_ascii=False) + "\n").encode()


def snapshot(data: bytes, oid: str) -> dict:
    data.decode("utf-8")
    if not data or b"\0" in data:
        raise ValueError("only nonempty UTF-8 authored source/data belongs to this layer")
    if blob(data) != oid:
        raise ValueError("raw source Git blob identity mismatch")
    return {"mode": "100644", "blob": oid, "sha256": sha(data)}


def contexts(before: bytes, after: bytes) -> list[dict]:
    before_lines, after_lines = before.splitlines(keepends=True), after.splitlines(keepends=True)
    before_offsets, after_offsets = [0], [0]
    for line in before_lines:
        before_offsets.append(before_offsets[-1] + len(line))
    for line in after_lines:
        after_offsets.append(after_offsets[-1] + len(line))
    matcher = difflib.SequenceMatcher(None, before_lines, after_lines, autojunk=False)
    for count in range(3, max(len(before_lines), len(after_lines)) + 4):
        hunks = []
        for group in matcher.get_grouped_opcodes(count):
            left, right = group[0], group[-1]
            a, b = before_offsets[left[1]], before_offsets[right[2]]
            c, d = after_offsets[left[3]], after_offsets[right[4]]
            old, new = before[a:b], after[c:d]
            if not old or not new or before.find(old) != a or after.find(new) != c or before.find(old, a + 1) >= 0 or after.find(new, c + 1) >= 0:
                break
            hunks.append({"offsetBefore": a, "offsetAfter": c,
                          "before": old.decode("utf-8"), "after": new.decode("utf-8")})
        else:
            if 0 < len(hunks) <= 64:
                restored = after
                for hunk in reversed(hunks):
                    at, old, new = hunk["offsetAfter"], hunk["before"].encode(), hunk["after"].encode()
                    if restored[at:at + len(new)] != new:
                        raise ValueError("inverse context offset differs")
                    restored = restored[:at] + old + restored[at + len(new):]
                if restored != before:
                    raise ValueError("inverse contexts fail full raw reconstruction")
                return hunks
    raise ValueError("could not construct bounded unique bidirectional contexts")


class ImmutableTree:
    """Only actual raw objects in one explicitly named, regular source tree."""

    def __init__(self, root: Path, tree: str):
        if not re.fullmatch(r"[a-f0-9]{40}", tree):
            raise ValueError("a full immutable tree SHA is required")
        if git(root, "cat-file", "-t", tree) != b"tree\n":
            raise ValueError("--tree must name an actual Git tree object")
        self.root, self.tree, self.entries, self.cache = root, tree, {}, {}
        for row in git(root, "ls-tree", "-rz", tree).split(b"\0"):
            if not row:
                continue
            metadata, name = row.split(b"\t", 1)
            mode, kind, oid = metadata.decode("ascii").split()
            path = name.decode("utf-8")
            if mode != "100644" or kind != "blob" or path in self.entries:
                raise ValueError(f"unreviewed source mode/type: {path}")
            self.entries[path] = oid
        if not self.entries:
            raise ValueError("empty immutable source tree")

    def preload(self, paths: list[str]) -> None:
        ids = sorted({self.entries[path] for path in paths if path in self.entries} - self.cache.keys())
        if not ids:
            return
        data = git(self.root, "cat-file", "--batch", body="".join(oid + "\n" for oid in ids).encode())
        offset = 0
        for expected in ids:
            end = data.index(b"\n", offset)
            oid, kind, size = data[offset:end].decode("ascii").split()
            size = int(size)
            value = data[end + 1:end + 1 + size]
            if oid != expected or kind != "blob" or len(value) != size or data[end + 1 + size:end + 2 + size] != b"\n" or blob(value) != oid:
                raise ValueError("immutable source batch blob identity mismatch")
            self.cache[oid] = value
            offset = end + 2 + size
        if offset != len(data):
            raise ValueError("unexpected immutable source batch bytes")

    def entry(self, path: str) -> tuple[str, bytes] | None:
        oid = self.entries.get(path)
        if oid is None:
            return None
        self.preload([path])
        return oid, self.cache[oid]


def legacy_inventory(fixed: ImmutableTree, current: ImmutableTree) -> dict:
    pending, seen, sources = [ENTRY], set(), []
    while pending:
        path = pending.pop(0)
        if path in seen:
            continue
        seen.add(path)
        entry = fixed.entry(path)
        if entry is None:
            raise ValueError("fixed legacy test source is missing")
        oid, data = entry
        text = data.decode("utf-8")
        names = [json.loads(match[1]) for match in re.finditer(r'(?m)^test\(("(?:[^"\\]|\\.)*")\s*,', text)]
        sources.append({"path": path, "mode": "100644", "blob": oid, "sha256": sha(data), "tests": names})
        for match in re.finditer(r'(?m)^import\s+"(\./[^"\n]+\.test\.mts)";', text):
            pending.append((Path(path).parent / match[1]).as_posix())
    names = [name for source in sources for name in source["tests"]]
    if len(sources) != 12 or len(names) != 58 or len(set(names)) != 58:
        raise ValueError("the fixed twelve-file / 58-top-level legacy denominator changed")
    for source in sources:
        # Every older positive/negative test source remains byte unchanged in
        # the current tree too; only the separate driver registration changes.
        entry = current.entry(source["path"])
        if entry is None or entry[0] != source["blob"] or sha(entry[1]) != source["sha256"]:
            raise ValueError(f"frozen legacy test bytes changed: {source['path']}")
    return {"schemaVersion": 1, "commit": BASE, "entry": ENTRY,
            "topLevelCount": 58, "nestedCount": 1, "executionCount": 59,
            "requiredPlatform": "linux", "nestedTests": [NESTED], "sources": sources}


def generate(root: Path, input_tree: str, accept_path_changes: bool, receipt: Path | None = None) -> dict:
    git(root, "merge-base", "--is-ancestor", BASE, "HEAD")
    tree = git(root, "rev-parse", BASE + "^{tree}").decode().strip()
    fixed, current = ImmutableTree(root, tree), ImmutableTree(root, input_tree)
    changed = set(git(root, "diff", "--no-renames", "--name-only", "-z", BASE, input_tree).decode().rstrip("\0").split("\0"))
    changed.difference_update(INFRASTRUCTURE)
    changed.discard("")
    paths = sorted(changed)
    if not paths:
        raise ValueError("empty polling source denominator")
    prior_fixture = current.entry(FIXTURE)
    if prior_fixture is not None:
        old_paths = [row["path"] for row in json.loads(prior_fixture[1])["records"]]
        if paths != old_paths and not accept_path_changes:
            raise ValueError("explicit source path set changed; review then use --accept-path-changes")
    fixed.preload(paths)
    current.preload(paths)
    records = []
    for path in paths:
        if not re.fullmatch(r"[A-Za-z0-9_./-]+", path) or any(part in {"", ".", ".."} for part in path.split("/")):
            raise ValueError("unsafe polling source path")
        entry = current.entry(path)
        if entry is None:
            raise ValueError(f"missing/nonregular immutable polling source: {path}")
        oid, after = entry
        original = fixed.entry(path)
        before = original[1] if original else None
        if before == after:
            raise ValueError(f"unchanged source in polling delta: {path}")
        record = {"path": path, "before": snapshot(before, original[0]) if original else None,
                  "after": snapshot(after, oid), "hunks": contexts(before, after) if before is not None else
                  [{"offsetBefore": 0, "offsetAfter": 0, "before": "", "after": after.decode("utf-8")}]}
        records.append(record)
    fixture = encode({"schemaVersion": 1, "beforeCommit": BASE, "beforeTree": tree, "records": records})
    legacy = encode(legacy_inventory(fixed, current))
    module = ("// Generated only by scripts/ci/generate_polling_source_projection.py.\n"
              "// Review every new path/context; all earlier source oracles remain unchanged.\n"
              f"export const pollingCurrentBase = {json.dumps(BASE)};\n"
              f"export const pollingCurrentTree = {json.dumps(tree)};\n"
              f"export const pollingCurrentFixturePath = {json.dumps(FIXTURE)};\n"
              f"export const pollingCurrentFixtureSHA256 = {json.dumps(sha(fixture))};\n"
              f"export const pollingLegacyFixturePath = {json.dumps(LEGACY)};\n"
              f"export const pollingLegacyFixtureSHA256 = {json.dumps(sha(legacy))};\n"
              "export const pollingCurrentPaths = Object.freeze([\n" +
              "".join(f"  {json.dumps(path)},\n" for path in paths) + "] as const);\n"
              "export const pollingInfrastructurePaths = Object.freeze([\n" +
              "".join(f"  {json.dumps(path)},\n" for path in INFRASTRUCTURE) + "] as const);\n")
    outputs = {FIXTURE: fixture, LEGACY: legacy, CONTRACT: module.encode()}
    report = {"beforeCommit": BASE, "beforeTree": tree, "currentInputTree": input_tree,
              "currentAuthority": "actual-immutable-tree-raw-blobs", "records": len(records),
              "introduced": sum(record["before"] is None for record in records),
              "legacyTests": 59, "requiresFinalFreeze": True,
              "receiptBeforeCopies": "worktree output bytes for preservation only; not source authority",
              "outputs": [{"path": path, "sha256": sha(data)} for path, data in outputs.items()]}
    if receipt is not None:
        receipt = receipt.resolve()
        if receipt.exists() or root == receipt or root in receipt.parents:
            raise ValueError("receipt must be a new evidence directory outside the repository")
        receipt.mkdir(parents=True)
        for path, data in outputs.items():
            for side, content in [("before", (root / path).read_bytes() if (root / path).exists() else None), ("after", data)]:
                if content is None:
                    continue
                copy = receipt / side / path
                copy.parent.mkdir(parents=True, exist_ok=True)
                copy.write_bytes(content)
        (receipt / "generation.json").write_bytes(encode(report))
    for path, data in outputs.items():
        target = root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--tree", required=True, help="full staged immutable input tree SHA; no worktree or clean-filter fallback")
    parser.add_argument("--accept-path-changes", action="store_true")
    parser.add_argument("--receipt", type=Path, help="new before/after evidence directory outside this repository")
    args = parser.parse_args()
    print(json.dumps(generate(args.root.resolve(), args.tree, args.accept_path_changes, args.receipt)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
