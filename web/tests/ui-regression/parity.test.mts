import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readdirSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { resourceActionDescriptors } from "../../src/features/resources/resource-action-descriptors.ts";
import { assertResourceActionInventory, currentActionInventory, frozenActionInventory, v2ActionInventory } from "../helpers/ui-foundation-action-inventory.mts";
import "./release-assembly-deltas.test.mts";
import "./ci-closure-deltas.test.mts";
import "./cp174-source-deltas.test.mts";
import { cp174ProtectedPaths } from "./cp174-source-deltas.mts";
import ts from "typescript";
import { createNormalizedReader, assertNormalizationManifest, inverseNormalization } from "./source-normalization.mts";
import { approvedProtectedPaths, assertApprovedManifest, assertProtectedFixture, assertApprovedSourceDelta, assertRunnerTypeDelta, assertBrowserOperationSource } from "./approved-source-delta.mts";
import { ciProtectedPaths, assertCISourceDelta, assertTypeDependencies } from "./ci-source-deltas.mts";
const root = fileURLToPath(new URL("../../..", import.meta.url));
const normalizedSource = createNormalizedReader(root);
const read = normalizedSource.read;
const fixture = (name: string) => JSON.parse(read("web/tests/fixtures/ui-regression/" + name + ".json").toString("utf8"));
const sha = (value: Buffer) => createHash("sha256").update(value).digest("hex");
const base = fixture("protected").base_commit;
const rawBase = (path: string) => execFileSync("git", ["show", base + ":" + path], { cwd: root, maxBuffer: 32 * 1024 * 1024 });

test("UI-PARITY-001: all 59 actual static entries and redirects retain fixed source", () => {
  const entries = fixture("entries").routes;
  assert.equal(entries.length, 59);
  const walk = (path: string): string[] => readdirSync(resolve(root, path), { withFileTypes: true }).flatMap(item => item.isDirectory() ? walk(path + "/" + item.name) : item.name === "page.tsx" ? [path + "/" + item.name] : []);
  assert.deepEqual(walk("web/src/app").sort(), entries.map((row: { path: string }) => row.path).sort());
  for (const row of entries) {
    assert.ok(row.families.length > 0);
    assert.equal(sha(read(row.path)), row.sha256, row.path);
  }
});
test("UI-PARITY-002: 100 actions keep original permission, payload, duplicate and secret contracts", () => {
  const record = fixture("actions");
  const raw = read("web/tests/fixtures/ui-foundation-action-inventory.jsonl");
  assert.equal(sha(raw), record.original_inventory_sha256);
  const current = raw.toString("utf8").trim().split("\n").map(line => JSON.parse(line));
  assert.equal(current.length, 100);
  assert.equal(new Set(current.map(row => row.id)).size, 100);
  assert.deepEqual(record.actions.map((row: { original: unknown }) => row.original), current);
  for (const row of record.actions) {
    assert.ok(row.current_owner_paths.length);
    for (const path of row.current_owner_paths) assert.ok(existsSync(resolve(root, path)), row.id + ": missing owner");
  }
});
test("UI-PARITY-002-V2: current parity includes all 106 actions and the six preset mutation owners", () => {
  const original = fixture("actions").actions.map((row: { original: unknown }) => row.original);
  assert.deepEqual(frozenActionInventory, original, "the 100-action authority remains unchanged");
  assert.equal(currentActionInventory.length, 106);
  assert.equal(new Set(currentActionInventory.map(({ id }) => id)).size, 106);
  assert.deepEqual(currentActionInventory.filter(({ migrationWave }) => migrationWave !== "v2"), original);
  assert.deepEqual(currentActionInventory.filter(({ migrationWave }) => migrationWave === "v2"), v2ActionInventory);
  assert.deepEqual(v2ActionInventory.map(({ id }) => id), ["RES-41", "RES-42", "RES-43", "RES-44", "RES-45", "RES-46"]);
  assertResourceActionInventory(currentActionInventory, resourceActionDescriptors);
  for (const row of v2ActionInventory) {
    const ownerPath = "web/src/features/resources/" + row.uiSource.split("/")[0];
    assert.ok(existsSync(resolve(root, ownerPath)), row.id + ": missing current mutation owner");
  }
});
test("UI-PARITY-003: original 733 records retain 728 current owners and five fixed-history-only sources with prior bounded deltas and one CP174 backend delta", () => {
  const records = fixture("protected").protected;
  assert.ok(records.length > 100);
  const manifest = fixture("approved-source-deltas");
  assertProtectedFixture(read("web/tests/fixtures/ui-regression/protected.json"), manifest);
  assertBrowserOperationSource(read(manifest.g3OperationDelta.newSource.path), manifest);
  assert.equal(records.length, 733);
  let rawMatches = 0, normalizedMatches = 0, historicalMatches = 0, deltas = 0, ciDeltas = 0, cp174Deltas = 0;
  for (const row of records) {
    if (cp174ProtectedPaths.includes(row.path)) {
      assert.equal(sha(rawBase(row.path)), row.sha256, row.path);
      assert.deepEqual(read(row.path), rawBase(row.path), "CP174 finite inverse restores the original protected backend bytes");
      cp174Deltas++;
    } else if (approvedProtectedPaths.some(path => path === row.path)) {
      assert.equal(sha(rawBase(row.path)), row.sha256, row.path);
      assertApprovedSourceDelta(row.path, rawBase(row.path), read(row.path), manifest); deltas++;
    } else if (ciProtectedPaths.some(path => path === row.path)) {
      assert.equal(sha(rawBase(row.path)), row.sha256, row.path);
      assertCISourceDelta(row.path, rawBase(row.path), read(row.path), fixture("ci-source-deltas")); ciDeltas++;
    } else {
      assert.equal(sha(read(row.path)), row.sha256, row.path);
      if (normalizedSource.manifest.historyOnly.some(item => item.path === row.path)) historicalMatches++;
      else if (normalizedSource.manifest.currentMappings.some(item => item.oldPath === row.path)) normalizedMatches++;
      else rawMatches++;
    }
  }
  assert.equal(historicalMatches, 5); assert.equal(rawMatches + normalizedMatches + deltas + ciDeltas + cp174Deltas, 728);
  assert.equal(rawMatches + normalizedMatches, 721); assert.equal(deltas, 4); assert.equal(ciDeltas, 2); assert.equal(cp174Deltas, 1);
});
function navigationBindings(source: string) {
  const bindings: { href: string; permissions: string[]; key: string }[] = [];
  const file = ts.createSourceFile("navigation.ts", source, ts.ScriptTarget.Latest, true);
  function visit(node: ts.Node) {
    if (ts.isCallExpression(node) && node.expression.getText(file) === "navItem") {
      const [href, key, , permissions] = node.arguments;
      assert.ok(ts.isStringLiteral(href) && ts.isStringLiteral(key) && ts.isArrayLiteralExpression(permissions));
      const values = permissions.elements.map(item => { assert.ok(ts.isStringLiteral(item)); return item.text; });
      bindings.push({ href: href.text, key: key.text, permissions: values });
    }
    ts.forEachChild(node, visit);
  }
  visit(file);
  return bindings.sort((a, b) => a.href.localeCompare(b.href));
}
test("UI-PARITY-004: navigation groups retain every exact route and permission binding", () => {
  const before = navigationBindings(rawBase("web/src/lib/navigation.ts").toString("utf8"));
  assert.equal(before.length, 26);
  // Keep the immutable base authoritative for every original binding. The
  // current UI adds only these two independent preset-read alternatives.
  const expected = structuredClone(before);
  for (const [href, original, addition] of [
    ["/admin/discord/", "discord_configs.read", "discord_target_presets.read"],
    ["/admin/overlay/", "overlay_profiles.read", "video_cover_presets.read"],
  ]) {
    const binding = expected.find(row => row.href === href);
    assert.ok(binding, href + ": missing original route");
    assert.deepEqual(binding.permissions, [original], href + ": original permission binding changed");
    binding.permissions.push(addition);
  }
  const currentSource = read("web/src/lib/navigation.ts").toString("utf8");
  const current = navigationBindings(currentSource);
  const assertCurrent = (bindings: ReturnType<typeof navigationBindings>) => {
    assert.deepEqual(bindings, expected, "current navigation permits only the two approved preset read additions");
  };
  assertCurrent(current);
  assert.deepEqual([...currentSource.matchAll(/\bkey:\s*["']([^"']+)["']/g)].map(match => match[1]),
    ["navOperations", "navMonitoring", "navProfiles", "navAdministration"]);

  for (const [href, permissions] of [
    ["/admin/discord/", ["discord_configs.read"]],
    ["/admin/overlay/", ["overlay_profiles.read"]],
    ["/admin/discord/", ["discord_target_presets.read"]],
    ["/admin/overlay/", ["video_cover_presets.read"]],
    ["/admin/discord/", ["discord_configs.read", "discord_target_presets.read", "discord_target_presets.read"]],
    ["/admin/overlay/", ["overlay_profiles.read", "video_cover_presets.create"]],
    ["/admin/streams/", ["streams.read", "discord_target_presets.read"]],
  ] satisfies [string, string[]][]) {
    const mutant = structuredClone(current);
    mutant.find(row => row.href === href)!.permissions = permissions;
    assert.throws(() => assertCurrent(mutant), /only the two approved preset read additions/);
  }
  const changedKey = structuredClone(current);
  changedKey[0].key = "unapproved";
  for (const mutant of [current.slice(1), [...current, structuredClone(current[0])], changedKey]) {
    assert.throws(() => assertCurrent(mutant), /only the two approved preset read additions/);
  }
});
test("UI-PARITY-005: runtime dependencies and original browser registration stay fixed with the exact type-only closure", () => {
  const before = JSON.parse(rawBase("web/package.json").toString("utf8"));
  const after = JSON.parse(read("web/package.json").toString("utf8"));
  assertTypeDependencies(before, after);
  assert.equal(after.scripts["test:ui-foundation-browser"], before.scripts["test:ui-foundation-browser"]);
  assertRunnerTypeDelta(rawBase("web/tests/helpers/run-ui-foundation-browser.mts"), read("web/tests/helpers/run-ui-foundation-browser.mts"), fixture("approved-source-deltas"));
  for (const path of ["web/tests/ui-foundation-browser.test.mts", "web/tests/ui-foundation-confirmation-browser.test.mts", "web/tests/ui-foundation-secrets-browser.test.mts"]) assert.deepEqual(read(path), rawBase(path), path);
});
test("UI-PARITY-006: only role names register the new suites and the CI job blocks overall", () => {
  const scripts = JSON.parse(read("web/package.json").toString("utf8")).scripts;
  const names = ["components", "parity", "browser-contracts", "browser"].map(name => "test:ui-regression:" + name);
  for (const name of names) assert.ok(scripts[name]);
  assert.equal(Object.keys(scripts).some(name => name.startsWith("test:bundle10")), false);
  assert.equal(new Set(names.map(name => scripts[name])).size, 4);
  const ci = read(".github/workflows/ci.yml").toString("utf8");
  for (const name of names.slice(0, 3)) assert.ok(ci.includes("npm run " + name));
  assert.match(ci, /uses: \.\/\.github\/workflows\/ui-regression.yml/);
  assert.match(ci, /needs: \[service-installer, go, release-rehearsal, observability_proxy_race_pre_fix, observability_proxy_race_current, web, ui-regression\]/);
  assert.match(ci, /UI_REGRESSION_RESULT.*== success/);
  for (const path of ["web/tests/bundle10", "web/tests/fixtures/bundle10", "scripts/ci/bundle10", ".github/workflows/bundle10-ui.yml"]) assert.equal(existsSync(resolve(root, path)), false);
});

test("UI-PARITY-007: exact supplement rejects missing, unknown, wrong original, added API and changed old behavior", () => {
  const manifest = fixture("approved-source-deltas"); assertApprovedManifest(manifest);
  const operation = read(manifest.g3OperationDelta.newSource.path);
  assertBrowserOperationSource(operation, manifest);
  for (const [from, to] of [["10_000", "20_000"], ["this.dialogs === 0", "true"], ["this.port.abort(error)", "void error"]]) {
    const changed = operation.toString("utf8").replace(from, to); assert.notEqual(changed, operation.toString("utf8"));
    assert.throws(() => assertBrowserOperationSource(Buffer.from(changed), manifest), /exact contract/);
  }
  assert.throws(() => assertApprovedManifest(null), /supplement/);
  const unknown = { ...manifest, protectedDeltas: [...manifest.protectedDeltas, { path: "unknown" }] }; assert.throws(() => assertApprovedManifest(unknown), /supplement/);
  for (const path of approvedProtectedPaths) {
    const before = rawBase(path), after = read(path); assertApprovedSourceDelta(path, before, after, manifest);
    assert.throws(() => assertApprovedSourceDelta("unknown", before, after, manifest), /unknown/);
    assert.throws(() => assertApprovedSourceDelta(path, Buffer.concat([before, Buffer.from("extra")]), after, manifest), /original hash/);
    assert.throws(() => assertApprovedSourceDelta(path, before, Buffer.concat([after, Buffer.from("\nexport function arbitraryAPI() {}\n")]), manifest), /original AST/);
  }
  const harness = "web/tests/helpers/browser-harness.mts", guard = "web/src/components/shell/use-shell-session-guard.ts";
  for (const [path, from, to] of [
    [harness, 'modifiers: direction === "backward" ? 8 : 0', 'modifiers: 0'],
    [harness, 'async pressKey(key: string, code = key)', 'async pressKey(key: string, code = "changed")'],
    [guard, 'if (sessionExpired) { notifyDraftSessionExit();', 'if (true) { notifyDraftSessionExit();'],
    [guard, 'if (active) { notifyDraftSessionExit();', 'notifyDraftSessionExit(); if (active) {'],
  ]) {
    const current = read(path).toString("utf8"), mutant = current.replace(from, to); assert.notEqual(mutant, current);
    assert.throws(() => assertApprovedSourceDelta(path, rawBase(path), Buffer.from(mutant), manifest), /approved insertion|original AST/);
  }
  const runner = "web/tests/helpers/run-ui-foundation-browser.mts";
  assert.throws(() => assertRunnerTypeDelta(rawBase(runner), Buffer.from(read(runner).toString("utf8").replace('result.nesting !== 0', 'result.nesting !== 1')), manifest), /runtime and registration AST/);
});

test("UI-PARITY-008: finite name inverse binds accepted Git bytes and rejects source, registration and historical authority drift", () => {
  const { manifest, raw } = normalizedSource;
  const original = (path: string) => execFileSync("git", ["show", manifest.acceptedCommit + ":" + path], { cwd: root, maxBuffer: 32 * 1024 * 1024 });
  for (const row of manifest.currentMappings) {
    const before = original(row.oldPath), current = raw(row.newPath);
    assert.deepEqual(inverseNormalization(row, before, current), before);
    assert.throws(() => inverseNormalization(row, Buffer.concat([before, Buffer.from("drift")]), current), /accepted Git raw/);
    assert.throws(() => inverseNormalization(row, before, Buffer.concat([current, Buffer.from("\n")])) , /exact specified name edits/);
  }
  const input = raw("web/tests/fixtures/ui-regression/source-normalization.json");
  const encode = (value: typeof manifest) => Buffer.from(JSON.stringify(value, null, 2) + "\n");
  assert.deepEqual(encode(JSON.parse(input.toString("utf8"))), input);
  const mutations = [
    (copy: typeof manifest) => { copy.currentMappings.pop(); },
    (copy: typeof manifest) => { copy.currentMappings[1].newPath = copy.currentMappings[0].newPath; },
    (copy: typeof manifest) => { copy.currentMappings[1].newPath = copy.currentMappings[0].newPath.toUpperCase(); },
    (copy: typeof manifest) => { copy.currentMappings[0].newPath = "unknown.ts"; },
    (copy: typeof manifest) => { copy.currentMappings[0].acceptedSha256 = "0".repeat(64); },
    (copy: typeof manifest) => { copy.historyOnly[0].fixedCommit = manifest.acceptedCommit; },
    (copy: typeof manifest) => { copy.historyOnly[0].fixedCommit = "8fb0fb0b2e9f0e3ce5edc36358479c329d96515a"; },
    (copy: typeof manifest) => { copy.historyOnly.pop(); },
    (copy: typeof manifest) => { copy.packageScripts.pop(); },
  ];
  for (const mutate of mutations) {
    const copy = JSON.parse(input.toString("utf8")) as typeof manifest; mutate(copy);
    assert.throws(() => assertNormalizationManifest(encode(copy)), /fixed table-derived contract/);
  }
  const row = manifest.currentMappings.find(item => item.oldPath === "web/package.json")!;
  const current = JSON.parse(raw(row.newPath).toString("utf8"));
  for (const mutate of [
    (pkg: typeof current) => { delete pkg.scripts["test:ui-regression:browser-contracts"]; },
    (pkg: typeof current) => { pkg.scripts["test:ui-regression:browser-contracts"] += " tests/ui-regression/render-state.test.mts"; },
    (pkg: typeof current) => { pkg.dependencies.unapproved = "1"; },
    (pkg: typeof current) => { pkg.scripts["test:operation-witnesses"] = "node --test unrelated.mts"; },
  ]) {
    const pkg = structuredClone(current); mutate(pkg);
    assert.throws(() => inverseNormalization(row, original(row.oldPath), Buffer.from(JSON.stringify(pkg))), /exact specified name edits/);
  }
  for (const path of ["web/tests/fixtures/ui-regression/protected.json", "web/tests/fixtures/ui-regression/approved-source-deltas.json", "web/tests/fixtures/ui-regression/ci-source-deltas.json", "web/package-lock.json", "web/tsconfig.ui-regression.json"]) assert.deepEqual(raw(path), original(path));
  for (const row of manifest.historyOnly) assert.equal(sha(read(row.path)), row.originalSha256);
  const missing = new Error("controlled immutable object unavailable");
  const old = manifest.historyOnly[0].path;
  const io = { raw, exists: (path: string) => existsSync(resolve(root, path)), object: () => { throw missing; } };
  assert.throws(() => createNormalizedReader(root, io).read(old), error => error === missing, "no current/private fallback for missing Git history");
  assert.throws(() => createNormalizedReader(root, { ...io, exists: path => path === old || io.exists(path) }).read(old), /must be absent/);
  const renamed = manifest.currentMappings.find(item => item.oldPath !== item.newPath)!;
  assert.throws(() => createNormalizedReader(root, { ...io, exists: path => path === renamed.oldPath || io.exists(path) }).read(renamed.oldPath), /alias must not remain/);
  const absent = new Error("controlled current source absent");
  assert.throws(() => createNormalizedReader(root, { ...io, raw: path => { if (path === renamed.newPath) throw absent; return raw(path); }, object: (_commit, path) => original(path) }).read(renamed.oldPath), error => error === absent);
  const originalSuite = "web/tests/ui-foundation-browser.test.mts";
  const suiteBytes = raw(originalSuite), renamedSuite = Buffer.from(suiteBytes.toString("utf8").replace(/(test\(\s*["'])/, "$1renamed-"));
  assert.notDeepEqual(renamedSuite, suiteBytes);
  const changedSuite = createNormalizedReader(root, { ...io, raw: path => path === originalSuite ? renamedSuite : raw(path) }).read(originalSuite);
  assert.throws(() => assert.deepEqual(changedSuite, original(originalSuite)), "the unchanged original suite identity remains protected after name resolution");
});
