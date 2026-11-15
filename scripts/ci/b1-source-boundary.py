#!/usr/bin/env python3
"""Apply exact reviewed B1 edits to the full accepted B0 files; no broad exemption."""
import json
import pathlib
import subprocess

BASE = "2446b5b451cc3749a3245be640b5b43cf086183e"
B1 = "60137bb7f6bbcf830485ead3f5850809b767c5a4"
def git(*args):
    return subprocess.check_output(["git", *args]).decode("utf-8")
subprocess.run(["git", "merge-base", "--is-ancestor", BASE, "HEAD"], check=True)
manifest = json.loads(git("show", B1 + ":scripts/ci/b1-source-patches.json"))
new = set(manifest["new"])
patches = manifest["patches"]
changed = set(git("diff", "--name-only", BASE, B1).splitlines())
assert changed <= new | set(patches), "B1 changed unrelated/protected source"
for path in new:
    assert subprocess.run(["git", "cat-file", "-e", BASE + ":" + path],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0, "existing source reclassified as new"
for path, edits in patches.items():
    expected = git("show", BASE + ":" + path)
    for edit in edits:
        assert expected.count(edit["old"]) == 1, "B1 exact edit anchor drift: " + path
        expected = expected.replace(edit["old"], edit["new"], 1)
    if path.endswith(".go"):
        expected = subprocess.check_output(["gofmt"], input=expected.encode()).decode()
    assert git("show", B1 + ":" + path) == expected, "B1 historical source changed beyond exact reviewed patch: " + path
# These trees remain completely immutable, including existing migrations,
# delegated Role handlers, last-admin, identity writers and pure codecs.
protected = ["go.mod", "go.sum", "internal/database",
             "internal/store/admin.go", "internal/store/auth_sessions_mariadb.go",
             "internal/store/operational_last_admin_mariadb.go",
             "internal/httpapi", "internal/v3authorization/canonical_resolver.go",
             "internal/v3authorization/codec.go", "internal/v3authorization/authority_migrations",
             "internal/v3authorization/management_migrations", "internal/v3authorization/migrations",
             "internal/lifecyclefoundation/migrations", "internal/lifecyclefoundation/migration.go"]
subprocess.run(["git", "diff", "--exit-code", BASE, B1, "--", *protected], check=True)
print(git("show", "-s", "--format=B1 accepted commit: %H%nB1 accepted tree: %T", B1).strip())
print("B1 historical exact source boundary: PASS")
subprocess.run(["python3", "scripts/ci/b2-source-boundary.py"], check=True)
