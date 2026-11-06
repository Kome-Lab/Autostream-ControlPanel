"""Materialize and verify the immutable UI188 CP fixture outside the product tree."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys


def materialize(root: Path, destination: Path) -> dict:
    root = root.resolve()
    destination = destination.resolve()
    if destination == root or destination.is_relative_to(root):
        raise ValueError("Historical fixture must be outside the current product tree")
    if destination.exists():
        raise ValueError("Historical fixture destination already exists")
    harness = root / "scripts/ci/ui188-full-e2e"
    sys.path.insert(0, str(harness))
    from source import HISTORICAL_MANIFEST_SHA256, cp_test_rows, unique_json, verify

    manifest_raw = (harness / "product-files.json").read_bytes()
    if hashlib.sha256(manifest_raw).hexdigest() != HISTORICAL_MANIFEST_SHA256:
        raise ValueError("Immutable historical manifest hash changed")
    manifest = unique_json(manifest_raw)
    lock = unique_json((harness / "source-lock.json").read_bytes())
    rows = cp_test_rows(manifest, lock, (harness / "cp-test-overlay.json").read_bytes())
    destination.mkdir(mode=0o700)
    for row in rows:
        if row["mode"] not in ("100644", "100755"):
            raise ValueError("Unsupported historical CP source mode")
        target = destination / row["path"]
        if not target.resolve().is_relative_to(destination):
            raise ValueError("Historical source path escapes its fixture")
        data = subprocess.check_output(["git", "-C", str(root), "cat-file", "blob", row["oid"]])
        if hashlib.sha256(data).hexdigest() != row["sha256"]:
            raise ValueError("Historical source blob hash mismatch")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        target.chmod(0o755 if row["mode"] == "100755" else 0o644)

    # The harness is separate from the immutable product. Refuse any overlap
    # rather than replacing a historical product row with current source.
    historical_harness = destination / "scripts/ci/ui188-full-e2e"
    if historical_harness.exists():
        raise ValueError("Historical product overlaps the preflight harness")
    shutil.copytree(harness, historical_harness, ignore=shutil.ignore_patterns("__pycache__"))
    verified = verify(destination, "cp")
    if verified != len(rows):
        raise ValueError("Historical source verification denominator changed")
    return {
        "historical_paths_verified": verified,
        "historical_product_tree": lock["components"]["cp"]["verified_tree"],
        "current_product_modified": False,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    args = parser.parse_args()
    print(json.dumps(materialize(args.root, args.destination), sort_keys=True))


if __name__ == "__main__":
    main()
