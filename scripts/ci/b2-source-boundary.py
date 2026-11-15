#!/usr/bin/env python3
"""Reconstruct accepted B2 bytes, then require the complete reviewed IU delta."""
import json
import pathlib
import subprocess
import sys

BASE = "60137bb7f6bbcf830485ead3f5850809b767c5a4"
ACCEPTED = "18e4639462957e26fea2a3a45a734af67dc9ef93"


def git(*args):
    return subprocess.check_output(["git", *args]).decode("utf-8")


manifest = json.loads(git("show", ACCEPTED + ":scripts/ci/b2-source-patches.json"))
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
subprocess.run(["git", "merge-base", "--is-ancestor", ACCEPTED, "HEAD"], check=True)
changed = set(git("diff", "--name-only", BASE, ACCEPTED).splitlines())
assert changed <= new | set(patches), "B2 changed unrelated or unreviewed source"
for path in new:
    subprocess.run(["git", "cat-file", "-e", ACCEPTED + ":" + path], check=True)
    assert subprocess.run(["git", "cat-file", "-e", BASE + ":" + path],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0, "existing source reclassified as new"
for path, edits in patches.items():
    expected = git("show", BASE + ":" + path)
    for edit in edits:
        assert edit["old"] and expected.count(edit["old"]) == 1, "B2 exact edit anchor drift: " + path
        expected = expected.replace(edit["old"], edit["new"], 1)
    if path.endswith(".go"):
        expected = subprocess.check_output(["gofmt"], input=expected.encode()).decode()
    assert git("show", ACCEPTED + ":" + path) == expected, "B2 accepted source changed beyond exact reviewed patch: " + path
for line in git("diff", "--name-status", BASE, ACCEPTED).splitlines():
    status, path = line.split("\t", 1)
    assert status in {"A", "M"}, "B2 removed or renamed preserved source: " + path
    assert not (path.endswith(".sql") and status != "A"), "existing Migration changed: " + path
protected = ["go.mod", "go.sum", "internal/v3authorization/canonical_resolver.go",
             "internal/v3authorization/codec.go", "internal/v3authorization/authority_migrations",
             "internal/v3authorization/management_migrations", "internal/v3authorization/migrations",
             "internal/lifecyclefoundation/migrations/001_lifecycle.sql", "internal/lifecyclefoundation/migration.go"]
subprocess.run(["git", "diff", "--exit-code", BASE, ACCEPTED, "--", *protected], check=True)
print(git("show", "-s", "--format=B2 accepted commit: %H%nB2 accepted tree: %T", ACCEPTED).strip())
print("B2 historical exact source boundary: PASS")
subprocess.run([sys.executable, "scripts/ci/iu-source-boundary.py"], check=True)
