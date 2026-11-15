#!/usr/bin/env python3
"""Apply exact reviewed B1 edits to the full accepted B0 files; no broad exemption."""
import json
import pathlib
import subprocess

BASE = "2446b5b451cc3749a3245be640b5b43cf086183e"
def git(*args):
    return subprocess.check_output(["git", *args]).decode("utf-8")
subprocess.run(["git", "merge-base", "--is-ancestor", BASE, "HEAD"], check=True)
manifest = json.loads(pathlib.Path("scripts/ci/b1-source-patches.json").read_text())
new = set(manifest["new"])
patches = manifest["patches"]
changed = set(git("diff", "--name-only", BASE, "HEAD").splitlines())
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
    assert pathlib.Path(path).read_text() == expected, "B1 source changed beyond exact reviewed patch: " + path
# These trees remain completely immutable, including existing migrations,
# delegated Role handlers, last-admin, identity writers and pure codecs.
protected = ["go.mod", "go.sum", "internal/database",
             "internal/store/admin.go", "internal/store/auth_sessions_mariadb.go",
             "internal/store/operational_last_admin_mariadb.go",
             "internal/httpapi", "internal/v3authorization/canonical_resolver.go",
             "internal/v3authorization/codec.go", "internal/v3authorization/authority_migrations",
             "internal/v3authorization/management_migrations", "internal/v3authorization/migrations",
             "internal/lifecyclefoundation/migrations", "internal/lifecyclefoundation/migration.go"]
subprocess.run(["git", "diff", "--exit-code", BASE, "HEAD", "--", *protected], check=True)
print(git("log", "-1", "--format=B1 tested commit: %H%nB1 tested tree: %T").strip())
print("B1 exact source boundary: PASS")
