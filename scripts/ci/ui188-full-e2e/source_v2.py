"""Verify the separated current-product source authority (schema v2).

The v2 authority is intentionally independent of the historical product manifest.
It pins runtime, tests, browser harness, database schema and CI/governance components
as separate exact path sets and rejects unlisted files in protected product roots.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess

HERE = Path(__file__).resolve().parent
AUTHORITY_PATH = HERE / "source-authority-v2.json"
EXPECTED_COMPONENTS = {
    "runtime-source", "test-source", "browser-test-harness", "database-schema",
    "ci-integration-harness", "workflow-policy", "build-packaging-config",
    "dependency-lock", "documentation", "repository-governance",
    "source-governance", "source-governance-tests", "web-tooling",
    "diagnostic-excluded",
}
PROTECTED_COMPONENTS = {
    "runtime-source", "test-source", "browser-test-harness",
    "database-schema", "diagnostic-excluded",
}
EXPECTED_ACCEPTANCE = {
    "two_stream_overlap_seconds": 1000,
    "HLS_checkpoints": [580, 900],
    "archive_per_stream": 3,
    "socket_drops": 0,
    "AAC_uncovered_over_50ms": 0,
    "PCM_PTS_bands": [[250, 400], [580, 700], [800, 950]],
    "S_denominator": 6,
    "F_denominator": 11,
    "group_denominator": 28,
}


class Blocked(Exception):
    pass


def require(condition, code):
    if not condition:
        raise Blocked(code)


def unique_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "BLOCKED_V2_DUPLICATE_JSON_KEY")
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=pairs)


def tree_hash(rows):
    tree = {}
    for row in rows:
        parts = row["path"].split("/")
        require(
            all(part not in ("", ".", "..") and ":" not in part and "\\" not in part for part in parts),
            "BLOCKED_V2_PATH",
        )
        node = tree
        for part in parts[:-1]:
            existing = node.setdefault(part, {})
            require(type(existing) is dict, "BLOCKED_V2_PATH_COLLISION")
            node = existing
        require(parts[-1] not in node, "BLOCKED_V2_DUPLICATE_PATH")
        node[parts[-1]] = (row["mode"], row["oid"])

    def digest(node):
        data = bytearray()
        for name, value in sorted(
            node.items(),
            key=lambda item: (item[0] + ("/" if isinstance(item[1], dict) else "")).encode("utf-8"),
        ):
            mode, oid = ("40000", digest(value)) if isinstance(value, dict) else value
            data.extend(mode.encode("ascii"))
            data.extend(b" ")
            data.extend(name.encode("utf-8"))
            data.extend(b"\\0")
            data.extend(bytes.fromhex(oid))
        payload = bytes(data)
        header = b"tree " + str(len(payload)).encode("ascii") + b"\\0"
        return hashlib.sha1(header + payload).hexdigest()

    return digest(tree)


def validate_authority(authority):
    require(type(authority) is dict, "BLOCKED_V2_SCHEMA")
    require(
        set(authority) == {
            "schema_version", "status", "candidate", "components",
            "cross_repository_authority", "acceptance", "protected_path_roots",
            "excluded_components", "control_files", "notes",
        },
        "BLOCKED_V2_SCHEMA",
    )
    require(authority["schema_version"] == 2, "BLOCKED_V2_SCHEMA_VERSION")
    require(authority["status"] == "CANDIDATE_FOR_CI_VERIFICATION", "BLOCKED_V2_STATUS")
    candidate = authority["candidate"]
    require(
        type(candidate) is dict
        and set(candidate) == {"repository", "commit", "git_tree", "blob_count"}
        and candidate["repository"] == "Kome-Lab/Autostream-ControlPanel"
        and re.fullmatch(r"[0-9a-f]{40}", candidate["commit"])
        and re.fullmatch(r"[0-9a-f]{40}", candidate["git_tree"])
        and type(candidate["blob_count"]) is int,
        "BLOCKED_V2_CANDIDATE",
    )
    components = authority["components"]
    require(type(components) is dict and set(components) == EXPECTED_COMPONENTS, "BLOCKED_V2_COMPONENT_SET")
    seen = set()
    total = 0
    for name, component in components.items():
        require(
            type(component) is dict
            and set(component) == {"tree_sha1", "file_count", "files"}
            and re.fullmatch(r"[0-9a-f]{40}", component["tree_sha1"])
            and type(component["file_count"]) is int
            and type(component["files"]) is list
            and component["file_count"] == len(component["files"]),
            "BLOCKED_V2_COMPONENT_SCHEMA",
        )
        paths = []
        for row in component["files"]:
            require(
                type(row) is dict
                and set(row) == {"path", "mode", "oid"}
                and type(row["path"]) is str
                and row["mode"] in {"100644", "100755"}
                and re.fullmatch(r"[0-9a-f]{40}", row["oid"]),
                "BLOCKED_V2_FILE_ROW",
            )
            path = PurePosixPath(row["path"])
            require(not path.is_absolute() and all(part not in ("", ".", "..") for part in path.parts), "BLOCKED_V2_PATH")
            require(row["path"] not in seen, "BLOCKED_V2_COMPONENT_OVERLAP")
            seen.add(row["path"])
            paths.append(row["path"])
        require(paths == sorted(paths), "BLOCKED_V2_PATH_ORDER")
        require(tree_hash(component["files"]) == component["tree_sha1"], "BLOCKED_V2_COMPONENT_TREE")
        total += len(paths)
    require(total == candidate["blob_count"] == len(seen), "BLOCKED_V2_BLOB_COUNT")
    require(authority["acceptance"] == EXPECTED_ACCEPTANCE, "BLOCKED_V2_ACCEPTANCE_RELAXED")
    require(authority["excluded_components"] == ["diagnostic-excluded"], "BLOCKED_V2_EXCLUSION_POLICY")
    require(
        authority["protected_path_roots"] == ["cmd/", "internal/", "web/src/", "web/public/", "web/tests/"],
        "BLOCKED_V2_PROTECTED_ROOTS",
    )
    require(authority["control_files"] == [
        "scripts/ci/ui188-full-e2e/source-authority-v2.json",
        "scripts/ci/ui188-full-e2e/source_v2.py",
        "scripts/ci/ui188-full-e2e/test_source_authority_v2.py",
    ], "BLOCKED_V2_CONTROL_FILES")
    require(not (set(authority["control_files"]) & seen), "BLOCKED_V2_CONTROL_OVERLAP")
    return components


def git_output(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


def verify(root, component_name="all", authority_path=None):
    authority_path = authority_path or AUTHORITY_PATH
    authority = unique_json(authority_path.read_bytes())
    components = validate_authority(authority)
    candidate = authority["candidate"]
    for control_path in authority["control_files"]:
        control_file = root / control_path
        require(not control_file.is_symlink() and control_file.is_file(), "BLOCKED_V2_CONTROL_FILE_MISSING")
    candidate_tree = git_output(root, "rev-parse", candidate["commit"] + "^{tree}")
    require(candidate_tree == candidate["git_tree"], "BLOCKED_V2_CANDIDATE_TREE")

    selected = list(components) if component_name == "all" else [component_name]
    require(all(name in components for name in selected), "BLOCKED_V2_UNKNOWN_COMPONENT")
    expected_protected = set()
    verified = {}
    for name, component in components.items():
        if name in PROTECTED_COMPONENTS:
            expected_protected.update(
                row["path"] for row in component["files"]
                if any(row["path"].startswith(prefix) for prefix in authority["protected_path_roots"])
            )

    actual_protected = set()
    tracked = subprocess.check_output(["git", "-C", str(root), "ls-files", "-z"])
    for raw_path in tracked.split(b"\\0"):
        if not raw_path:
            continue
        path = raw_path.decode("utf-8")
        if any(path.startswith(prefix) for prefix in authority["protected_path_roots"]):
            actual_protected.add(path)
    require(actual_protected == expected_protected, "BLOCKED_V2_UNLISTED_PROTECTED_PATH")

    for name in selected:
        rows = components[name]["files"]
        for row in rows:
            path = row["path"]
            destination = root / path
            require(not destination.is_symlink(), "BLOCKED_V2_SYMLINK")
            require(destination.is_file(), "BLOCKED_V2_MISSING")
            index = subprocess.check_output(
                ["git", "-C", str(root), "ls-files", "--stage", "-z", "--", path]
            )
            records = [record for record in index.split(b"\\0") if record]
            require(len(records) == 1, "BLOCKED_V2_INDEX_ENTRY")
            metadata, indexed_path = records[0].split(b"\\t", 1)
            mode, oid, stage = metadata.decode("ascii").split()
            require(indexed_path.decode("utf-8") == path and stage == "0", "BLOCKED_V2_INDEX_PATH")
            require(mode == row["mode"] and oid == row["oid"], "BLOCKED_V2_GIT_BLOB")
            actual_oid = subprocess.check_output(
                ["git", "-C", str(root), "hash-object", "--no-filters", "--", path], text=True
            ).strip()
            require(actual_oid == row["oid"], "BLOCKED_V2_WORKTREE_BYTES")
            if os.name == "posix":
                executable = bool(destination.stat().st_mode & 0o111)
                require(executable == (row["mode"] == "100755"), "BLOCKED_V2_WORKTREE_MODE")
        verified[name] = len(rows)

    gomod = (root / "go.mod").read_text()
    require(re.search(r"^go 1\.26\.5$", gomod, re.M), "BLOCKED_V2_GO_VERSION")
    require(authority["cross_repository_authority"]["cp_contracts_version"] in gomod, "BLOCKED_V2_CONTRACTS_PIN")
    return verified


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--component", default="all")
    parser.add_argument("--authority", type=Path, default=AUTHORITY_PATH)
    args = parser.parse_args()
    try:
        result = verify(args.root.resolve(), args.component, args.authority)
        print("SOURCE_V2_COMPONENTS_VERIFIED=" + json.dumps(result, sort_keys=True))
    except Blocked as error:
        raise SystemExit("BLOCKED_SOURCE_AUTHORITY_V2:" + str(error)) from None
    except (OSError, ValueError, subprocess.SubprocessError, KeyError, TypeError) as error:
        raise SystemExit("BLOCKED_SOURCE_AUTHORITY_V2:" + type(error).__name__) from None


if __name__ == "__main__":
    main()
