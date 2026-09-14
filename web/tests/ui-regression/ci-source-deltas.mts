import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// This supplement leaves the 733-record authority and the two 004 exceptions
// intact. New lock entries are the exact registry records, including peer edges.
const approval = {
  "schemaVersion": 1,
  "protected_fixture_sha256": "cd9a25bad28720244b8c9b9ec8c5ed5c08df1914800fd6b7831f5efcd3459ae8",
  "base_commit": "28a9ff71ea9925722000a2933c59c79b8cc8b9b2",
  "protectedDeltas": [
    {
      "path": "internal/security/workflow_test.go",
      "original_sha256": "b781e8ff795c2bd7dd5d325ddae363ccb69ed73439bbcae0ada2dcd391573f81",
      "transform": "yaml-job-step-source-policy"
    },
    {
      "path": "web/package-lock.json",
      "original_sha256": "bd6632f4a912935758cfc8a56678ad9d01f6ea4f3be6ea22eb72e18690ae8217",
      "transform": "two-type-dependencies-and-peer-closure"
    }
  ],
  "devDependencies": {
    "@svta/cml-cmcd": "2.4.0",
    "eventemitter3": "5.0.4"
  },
  "addedPackages": {
    "node_modules/@svta/cml-cmcd": {
      "version": "2.4.0",
      "resolved": "https://registry.npmjs.org/@svta/cml-cmcd/-/cml-cmcd-2.4.0.tgz",
      "integrity": "sha512-LEVH5t5igv1fN18huFK+9S+qQOej2jFU16Z6E1phlaL615uP/+G3JiVZrw5n/GPAME4XjWmcgzbd7rP8dNwzvQ==",
      "dev": true,
      "license": "Apache-2.0",
      "engines": {
        "node": ">=20"
      },
      "peerDependencies": {
        "@svta/cml-structured-field-values": "1.1.3",
        "@svta/cml-utils": "1.5.0"
      }
    },
    "node_modules/@svta/cml-structured-field-values": {
      "version": "1.1.3",
      "resolved": "https://registry.npmjs.org/@svta/cml-structured-field-values/-/cml-structured-field-values-1.1.3.tgz",
      "integrity": "sha512-XqLzQOTznz6kh/nsSh8dy1kV7GQjL4csG+2U5EvLGhAqNuNUczbVg5EAsDobKDVg8xhdthdXx2UuwM+ZHQCxSg==",
      "dev": true,
      "license": "Apache-2.0",
      "peer": true,
      "engines": {
        "node": ">=20"
      },
      "peerDependencies": {
        "@svta/cml-utils": "1.5.0"
      }
    },
    "node_modules/@svta/cml-utils": {
      "version": "1.5.0",
      "resolved": "https://registry.npmjs.org/@svta/cml-utils/-/cml-utils-1.5.0.tgz",
      "integrity": "sha512-JMqclD7Akd+GSJiuaYNUHOP2wNtf/nauKeszlYeivSHfi0Lp3pmSW5PXDvJ2dO4aPmmSOQbF0ztTsW9Vcs2Whw==",
      "dev": true,
      "license": "Apache-2.0",
      "peer": true,
      "engines": {
        "node": ">=20"
      }
    },
    "node_modules/eventemitter3": {
      "version": "5.0.4",
      "resolved": "https://registry.npmjs.org/eventemitter3/-/eventemitter3-5.0.4.tgz",
      "integrity": "sha512-mlsTRyGaPBjPedk6Bvw+aqbsXDtoAyAzm5MO7JgU+yVRyMQ5O8bD4Kcci7BS85f93veegeCPkL8R4GLClnjLFw==",
      "dev": true,
      "license": "MIT"
    }
  }
} as const;
export const ciProtectedPaths = approval.protectedDeltas.map(row => row.path);
export const addedDirectTests = ["condition-lifecycle.test.mts", "render-state.test.mts", "dependency-integrity.test.mts"] as const;
export function assertCIManifest(value: unknown): asserts value is typeof approval {
  assert.deepEqual(value, approval, "missing or changed CI source supplement");
}
type Package = { devDependencies?: Record<string, string>; scripts?: Record<string, string>; [key: string]: unknown };
type Lock = { packages: Record<string, Record<string, unknown>>; [key: string]: unknown };
function hash(value: Buffer) { return createHash("sha256").update(value).digest("hex"); }
function normalize(value: Buffer) { return value.toString("utf8").replace(/\r\n/g, "\n"); }
export function assertTypeDependencies(before: Package, after: Package) {
  for (const key of ["dependencies", "optionalDependencies", "overrides"]) assert.deepEqual(after[key], before[key], "runtime dependency or override changed");
  const expected = { ...before.devDependencies, ...approval.devDependencies };
  for (const name of Object.keys(approval.devDependencies)) assert.ok(!before.devDependencies?.[name], "type dependency already present in fixed source");
  assert.deepEqual(after.devDependencies, expected, "only two exact type devDependencies are authorized");
}
export function assertLockDelta(before: Buffer, after: Buffer) {
  assert.equal(hash(before), approval.protectedDeltas[1].original_sha256, "lock fixed original hash mismatch");
  const old = JSON.parse(before.toString("utf8")) as Lock, current = JSON.parse(after.toString("utf8")) as Lock;
  assert.deepEqual(Object.keys(current.packages).filter(key => !(key in old.packages)).sort(), Object.keys(approval.addedPackages).sort(), "exact type dependency closure required");
  for (const [key, record] of Object.entries(approval.addedPackages)) {
    assert.deepEqual(current.packages[key], record, "new dependency version, registry, integrity or peer closure changed");
    delete current.packages[key];
  }
  const root = current.packages[""], oldRoot = old.packages[""];
  assert.deepEqual(root.devDependencies, { ...(oldRoot.devDependencies as Record<string,string>), ...approval.devDependencies }, "root lock type dependencies disagree");
  root.devDependencies = oldRoot.devDependencies;
  assert.deepEqual(current, old, "existing lock packages and metadata must remain unchanged");
}
const workflowDriver = "package security\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestGitHubWorkflowActionsArePinnedToCommitSHA(t *testing.T) {\n\troot := filepath.Join(\"..\", \"..\")\n\tworkflowPaths, err := filepath.Glob(filepath.Join(root, \".github\", \"workflows\", \"*.yml\"))\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif len(workflowPaths) == 0 {\n\t\tt.Fatal(\"expected GitHub workflow files\")\n\t}\n\tyamlPaths, err := filepath.Glob(filepath.Join(root, \".github\", \"workflows\", \"*.yaml\"))\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tworkflowPaths = append(workflowPaths, yamlPaths...)\n\tfor _, workflowPath := range workflowPaths {\n\t\tdata, err := os.ReadFile(workflowPath)\n\t\tif err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n\t\tif err := checkWorkflowSources(root, data); err != nil {\n\t\t\tt.Fatalf(\"%s: %v\", workflowPath, err)\n\t\t}\n\t}\n}\n";
export function assertCISourceDelta(path: string, before: Buffer, after: Buffer, manifest: unknown) {
  assertCIManifest(manifest);
  const record = approval.protectedDeltas.find(row => row.path === path);
  assert.ok(record, "unknown CI protected delta path");
  assert.equal(hash(before), record.original_sha256, "CI delta fixed original hash mismatch");
  if (record.transform === "two-type-dependencies-and-peer-closure") assertLockDelta(before, after);
  else assert.equal(normalize(after), workflowDriver, "workflow traversal must call the structural policy for every actual YAML file");
  return { path, transform: record.transform, originalHash: "MATCH", limitedStructure: "MATCH" };
}
export function assertPackageRegistration(before: Package, after: Package) {
  assertTypeDependencies(before, after);
  const copy = structuredClone(after);
  copy.devDependencies = before.devDependencies;
  const scripts = { ...copy.scripts }, original = before.scripts?.["test:ui-regression:browser-contracts"];
  assert.equal(scripts["test:ui-regression:browser-contracts"], original + addedDirectTests.map(name => " tests/ui-regression/" + name).join(""), "direct tests must be registered once in the existing current suite");
  scripts["test:ui-regression:browser-contracts"] = original!;
  copy.scripts = scripts;
  assert.deepEqual(copy, before, "unrelated package metadata or test registration changed");
}
