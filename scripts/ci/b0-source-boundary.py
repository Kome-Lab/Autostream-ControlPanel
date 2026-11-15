#!/usr/bin/env python3
"""Exact reviewed B0 extensions; all existing product bytes remain frozen."""
import pathlib
import subprocess

BASE = "ee26856bbf7e808424a658345dd5f362d5500bb6"
EXTRAS = [
    "docs/lifecycle-foundation/CONNECTION_INVENTORY.md",
    "docs/lifecycle-foundation/S12_B0_IMPLEMENTATION.md",
    "internal/lifecyclefoundation/foundation_test.go",
    "internal/lifecyclefoundation/migration.go",
    "internal/lifecyclefoundation/migration_test.go",
    "internal/lifecyclefoundation/migrations/001_lifecycle.sql",
    "internal/lifecyclefoundation/process_test.go",
    "internal/lifecyclefoundation/service.go",
    "internal/lifecyclefoundation/store.go",
    "internal/lifecyclefoundation/synthetic_test.go",
    "internal/lifecyclefoundation/types.go",
    "scripts/ci/b0-checks.sh",
    "scripts/ci/b0-required-tests.txt",
    "scripts/ci/b0-source-boundary.py"
]
WORKFLOWS = [".github/workflows/v3-authorization-ci.yml", ".github/workflows/v3-operational-authorization-ci.yml"]
S13 = "scripts/ci/s13-source-boundary.sh"
STEP = "      - name: S12-B0 required Lifecycle Foundation\n        run: bash scripts/ci/b0-checks.sh\n"
INSERTION = "    docs/lifecycle-foundation/CONNECTION_INVENTORY.md|docs/lifecycle-foundation/S12_B0_IMPLEMENTATION.md|internal/lifecyclefoundation/foundation_test.go|internal/lifecyclefoundation/migration.go|internal/lifecyclefoundation/migration_test.go|internal/lifecyclefoundation/migrations/001_lifecycle.sql|internal/lifecyclefoundation/process_test.go|internal/lifecyclefoundation/service.go|internal/lifecyclefoundation/store.go|internal/lifecyclefoundation/synthetic_test.go|internal/lifecyclefoundation/types.go|scripts/ci/b0-checks.sh|scripts/ci/b0-required-tests.txt|scripts/ci/b0-source-boundary.py) ;;\n    .github/workflows/v3-authorization-ci.yml) python3 scripts/ci/b0-source-boundary.py ;;\n"

def git(*args):
    return subprocess.check_output(["git", *args]).decode("utf-8")

subprocess.run(["git", "merge-base", "--is-ancestor", BASE, "HEAD"], check=True)
changed = git("diff", "--name-only", BASE, "HEAD").splitlines()
allowed = set(EXTRAS + WORKFLOWS + [S13])
assert set(changed) <= allowed, "B0 changed protected or unrelated source"
for path in EXTRAS:
    assert subprocess.run(["git", "cat-file", "-e", BASE + ":" + path],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0, "B0 attempted to exempt an existing file"
for path in WORKFLOWS:
    original = git("show", BASE + ":" + path)
    if path == WORKFLOWS[0]:
        original = original.replace("        with:\n          persist-credentials: false",
                                    "        with:\n          fetch-depth: 0\n          persist-credentials: false", 1)
    anchor = "      - name: Toolchain and pinned evidence dependency\n" if path == WORKFLOWS[0] else "      - name: Protected source boundaries and formatting\n"
    assert original.count(anchor) == 1, "CI extension anchor drifted"
    expected_workflow = original.replace(anchor, STEP + anchor, 1)
    assert pathlib.Path(path).read_text() == expected_workflow, "existing CI changed beyond the exact B0 extension"
original = git("show", BASE + ":" + S13)
expected = original.replace('  case "$file" in\n', '  case "$file" in\n' + INSERTION, 1)
expected = expected.replace(" .github/workflows/v3-authorization-ci.yml ':(exclude)", " ':(exclude)", 1)
expected = expected.replace("printf 'S13 protected source boundary: PASS",
                            "python3 scripts/ci/b0-source-boundary.py\nprintf 'S13 protected source boundary: PASS", 1)
assert pathlib.Path(S13).read_text() == expected, "S13 guard changed beyond its exact reviewed extension"
protected = ["go.mod", "go.sum", "cmd", "internal/database", "internal/operationalauthorization",
             "internal/v3authorization", "internal/httpapi", "internal/store"]
subprocess.run(["git", "diff", "--exit-code", BASE, "HEAD", "--", *protected], check=True)
print(git("log", "-1", "--format=B0 tested commit: %H%nB0 tested tree: %T").strip())
print("B0 exact source boundary: PASS")
