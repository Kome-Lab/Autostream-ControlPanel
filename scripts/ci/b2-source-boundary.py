#!/usr/bin/env python3
"""Reconstruct complete current files from exact reviewed B2 edits to B1."""
import json
import pathlib
import subprocess
import sys

BASE = "60137bb7f6bbcf830485ead3f5850809b767c5a4"


def git(*args):
    return subprocess.check_output(["git", *args]).decode("utf-8")


manifest = json.loads(pathlib.Path("scripts/ci/b2-source-patches.json").read_text())
assert manifest["baseline"] == BASE, "B2 manifest baseline changed"
new = set(manifest["new"])
patches = manifest["patches"]
assert len(new) == len(manifest["new"]) and not new.intersection(patches), "ambiguous B2 paths"
if len(sys.argv) == 3 and sys.argv[1] == "--owns":
    # S13 calls this only after the mandatory complete reconstruction below has
    # passed. Membership delegates to that exact guard, never exempts a file.
    sys.exit(0 if sys.argv[2] in new or sys.argv[2] in patches else 1)
assert len(sys.argv) == 1, "unsupported B2 boundary arguments"
subprocess.run(["git", "merge-base", "--is-ancestor", BASE, "HEAD"], check=True)
changed = set(git("diff", "--name-only", BASE, "HEAD").splitlines())
assert changed <= new | set(patches), "B2 changed unrelated or unreviewed source"
for path in new:
    assert pathlib.Path(path).is_file(), "B2 new path missing: " + path
    assert subprocess.run(["git", "cat-file", "-e", BASE + ":" + path],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0, "existing source reclassified as new"
for path, edits in patches.items():
    expected = git("show", BASE + ":" + path)
    for edit in edits:
        assert edit["old"] and expected.count(edit["old"]) == 1, "B2 exact edit anchor drift: " + path
        expected = expected.replace(edit["old"], edit["new"], 1)
    if path.endswith(".go"):
        expected = subprocess.check_output(["gofmt"], input=expected.encode()).decode()
    assert pathlib.Path(path).read_text() == expected, "B2 source changed beyond exact reviewed patch: " + path
for line in git("diff", "--name-status", BASE, "HEAD").splitlines():
    status, path = line.split("\t", 1)
    assert status in {"A", "M"}, "B2 removed or renamed preserved source: " + path
    assert not (path.endswith(".sql") and status != "A"), "existing Migration changed: " + path
protected = ["go.mod", "go.sum", "internal/v3authorization/canonical_resolver.go",
             "internal/v3authorization/codec.go", "internal/v3authorization/authority_migrations",
             "internal/v3authorization/management_migrations", "internal/v3authorization/migrations",
             "internal/lifecyclefoundation/migrations/001_lifecycle.sql", "internal/lifecyclefoundation/migration.go"]
subprocess.run(["git", "diff", "--exit-code", BASE, "HEAD", "--", *protected], check=True)
print(git("log", "-1", "--format=B2 tested commit: %H%nB2 tested tree: %T").strip())
print("B2 exact source boundary: PASS")
