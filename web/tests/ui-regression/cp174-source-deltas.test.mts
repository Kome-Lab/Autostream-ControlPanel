import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { assertCP174Original, assertCP174SourceDelta, cp174Base, cp174Paths, cp174ProtectedPaths, inverseCP174Source } from "./cp174-source-deltas.mts";
import { createNormalizedReader } from "./source-normalization.mts";
import { releaseAssemblyBase, releaseAssemblySource } from "./release-assembly-deltas.mts";
import { cp176Base, cp176Paths, inverseCP176DependencyRepair } from "./cp176-dependency-deltas.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const raw = (path: string) => readFileSync(resolve(root, path));
const object = (commit: string, path: string) => execFileSync("git", ["show", commit + ":" + path], { cwd: root, maxBuffer: 32 * 1024 * 1024 });
const io = { raw, object, exists: (path: string) => existsSync(resolve(root, path)) };
const expectedPaths = ["internal/httpapi/server_streams.go", "web/package.json", ".github/workflows/ci.yml"];
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const beforeDependencyRepair = (path: string) => cp176Paths.includes(path)
  ? inverseCP176DependencyRepair(path, object(cp176Base, path), raw(path)) : raw(path);

test("UI-CP174-001: exactly three finite deltas restore the actual fixed CP174 base and historical authority", () => {
  assert.equal(cp174Base, "0315845e3af01eff6b97c6164db3ddc3109b55af");
  assert.deepEqual(cp174Paths, expectedPaths);
  assert.deepEqual(cp174ProtectedPaths, ["internal/httpapi/server_streams.go"]);
  const reader = createNormalizedReader(root);
  for (const path of cp174Paths) {
    const before = object(cp174Base, path), current = beforeDependencyRepair(path);
    assert.notDeepEqual(current, before);
    assertCP174Original(path, before);
    assert.deepEqual(assertCP174SourceDelta(path, before, current), before);
    assert.deepEqual(inverseCP174Source(path, current), before);
    const mapping = reader.manifest.currentMappings.find(row => row.newPath === path);
    assert.deepEqual(reader.read(mapping?.oldPath ?? path), mapping ? object(reader.manifest.acceptedCommit, mapping.oldPath) : before);
  }
  assert.deepEqual(inverseCP174Source(".github/workflows/ci.yml", raw(".github/workflows/ci.yml")), object(releaseAssemblySource, ".github/workflows/ci.yml"));
  for (const name of ["protected.json", "source-normalization.json", "approved-source-deltas.json", "ci-source-deltas.json"]) {
    const path = "web/tests/fixtures/ui-regression/" + name;
    assert.deepEqual(raw(path), object(cp174Base, path));
    assert.deepEqual(raw(path), object(releaseAssemblyBase, path));
  }
  assert.deepEqual(raw("web/tests/ui-regression/release-assembly-deltas.mts"), object(cp174Base, "web/tests/ui-regression/release-assembly-deltas.mts"), "all eleven prior assembly hash pairs stay immutable");
});

test("UI-CP174-002: missing, reverted, truncated, extra and wrong-original bytes cannot use a historical fallback", () => {
  for (const path of cp174Paths) {
    const before = object(cp174Base, path), current = beforeDependencyRepair(path);
    for (const mutant of [Buffer.alloc(0), before, current.subarray(0, current.length - 1), Buffer.concat([current, Buffer.from("drift")])]) {
      assert.throws(() => inverseCP174Source(path, mutant), /exact specified name edits/);
      assert.throws(() => assertCP174SourceDelta(path, before, mutant), /exact specified name edits/);
    }
    assert.throws(() => assertCP174SourceDelta(path, Buffer.concat([before, Buffer.from("drift")]), current), /fixed original hash/);
    assert.throws(() => inverseCP174Source("unknown", current), /unknown CP174 source path/);
    const missing = new Error("CP174 current source missing");
    const absent = createNormalizedReader(root, { ...io, raw: p => { if (p === path) throw missing; return raw(p); } });
    assert.throws(() => absent.read(path), error => error === missing);
    const changed = createNormalizedReader(root, { ...io, raw: p => p === path ? Buffer.concat([raw(p), Buffer.from("drift")]) : raw(p) });
    assert.throws(() => changed.read(path), /exact specified name edits/);
    const wrongBase = createNormalizedReader(root, { ...io, object: (commit, p) => commit === cp174Base && p === path ? Buffer.alloc(0) : object(commit, p) });
    assert.throws(() => wrongBase.read(path), /fixed original hash/);
  }
  const unlisted = "web/src/lib/navigation.ts", mutant = Buffer.concat([raw(unlisted), Buffer.from("drift")]);
  assert.deepEqual(createNormalizedReader(root, { ...io, raw: p => p === unlisted ? mutant : raw(p) }).read(unlisted), mutant, "unlisted application bytes are not substituted");
});

type Job = { [key: string]: unknown; steps: Record<string, unknown>[]; needs?: string[] };
type Workflow = { jobs: Record<string, Job> };
type Package = { scripts: Record<string, string> };
const yaml = createRequire(import.meta.url)("js-yaml") as { load: (text: string) => Workflow };
const requiredJobs = ["service-installer", "go", "release-rehearsal", "observability_proxy_race_pre_fix", "observability_proxy_race_current", "web", "release-ui", "ui-regression"];
const resultKeys = ["SERVICE_INSTALLER_RESULT", "GO_RESULT", "RELEASE_REHEARSAL_RESULT", "OBSERVABILITY_PROXY_RACE_PRE_FIX_RESULT", "OBSERVABILITY_PROXY_RACE_CURRENT_RESULT", "WEB_RESULT", "RELEASE_UI_RESULT", "UI_REGRESSION_RESULT"];
const expectedScripts = {
  "test:release-ui": "node --no-warnings --test --test-concurrency=1 " + ["tests/preset-resource-actions.test.mts", "tests/streams-visual-integration.test.mts", "tests/ui-regression/preset-components.test.mts", "tests/ui-regression/polling-state.test.mts", "tests/ui-regression/node-layout-tabs.test.mts", "tests/ui-regression/refresh-layout.test.mts"].join(" "),
  "test:release-ui-browser": "node --no-warnings --test --test-concurrency=1 tests/ui-refresh-layout-browser.test.mts tests/ui-release-layout-browser.test.mts",
};
function assertRequiredReleaseUI(pkg: Package, workflow: Workflow) {
  for (const [name, command] of Object.entries(expectedScripts)) assert.equal(pkg.scripts[name], command, "CP174 exact required test command");
  assert.equal(pkg.scripts["test:ui-regression:parity"], "node --no-warnings --test tests/ui-regression/parity.test.mts", "CP174 contract remains in the required parity suite");
  const job = workflow.jobs["release-ui"];
  assert.ok(job, "CP174 required job exists");
  assert.equal(job.if, undefined, "CP174 required job cannot be conditionally skipped");
  assert.equal(job["continue-on-error"], undefined, "CP174 required job cannot continue on error");
  assert.equal(job["runs-on"], "ubuntu-24.04");
  assert.equal(job["timeout-minutes"], 15);
  assert.deepEqual(job.defaults, { run: { "working-directory": "web" } });
  for (const step of job.steps) {
    assert.equal(step["continue-on-error"], undefined, "CP174 required steps fail closed");
    if (step.run) assert.equal(step.if, undefined, "CP174 test and setup steps cannot be skipped");
  }
  for (const command of ["npm ci", "npm run typecheck", "npm run test:release-ui", "npm run test:release-ui-browser"]) {
    assert.equal(job.steps.filter(step => step.run === command).length, 1, "CP174 required command appears exactly once");
  }
  const browser = job.steps.find(step => step.run === "npm run test:release-ui-browser")!;
  assert.equal(browser["timeout-minutes"], 6);
  assert.deepEqual(browser.env, { AUTOSTREAM_BROWSER_PATH: "/usr/bin/google-chrome", AUTOSTREAM_UI174_EVIDENCE_DIR: "${{ runner.temp }}/release-ui-evidence" });
  const overall = workflow.jobs.overall;
  assert.equal(overall.if, "always()", "overall must check cancelled and skipped jobs");
  assert.deepEqual(overall.needs, requiredJobs);
  assert.equal(overall["continue-on-error"], undefined);
  assert.equal(overall.steps.length, 1);
  const gate = overall.steps[0];
  assert.equal(gate.if, undefined); assert.equal(gate["continue-on-error"], undefined);
  assert.deepEqual(gate.env, Object.fromEntries(requiredJobs.map((job, index) => [resultKeys[index], "${{ needs." + job + ".result }}"])));
  assert.equal(gate.run, "set -euo pipefail\n" + resultKeys.map(key => '[[ "${' + key + '}" == success ]]\n').join(""), "every required result must be success");
  assert.ok(workflow.jobs.web.steps.some(step => step.run === "npm run test:ui-regression:parity"), "independent CP174 contract is executed by required web CI");
}

test("UI-CP174-003: current raw package and workflow independently require both suites and every successful job result", () => {
  const pkg = JSON.parse(raw("web/package.json").toString("utf8")) as Package;
  const workflow = yaml.load(raw(".github/workflows/ci.yml").toString("utf8"));
  assertRequiredReleaseUI(pkg, workflow);
  const command = workflow.jobs.overall.steps[0].run as string;
  const env = Object.fromEntries(resultKeys.map(key => [key, "success"]));
  const run = (values: Record<string, string>) => spawnSync("bash", ["-c", command], { env: { ...process.env, ...values }, encoding: "utf8", timeout: 5_000 });
  assert.equal(run(env).status, 0);
  for (const key of resultKeys) for (const result of ["failure", "cancelled", "skipped", ""]) {
    const failed = run({ ...env, [key]: result });
    assert.equal(failed.error, undefined); assert.equal(failed.signal, null);
    assert.notEqual(failed.status, 0, `${key}=${result} cannot satisfy overall`);
  }
  for (const name of Object.keys(expectedScripts)) {
    for (const command of ["", "true", pkg.scripts[name] + " --test-skip-pattern=.*", pkg.scripts[name] + " || true"]) {
      const copy = structuredClone(pkg); copy.scripts[name] = command;
      assert.throws(() => assertRequiredReleaseUI(copy, workflow));
    }
  }
  const mutations: ((value: Workflow) => void)[] = [
    value => { delete value.jobs["release-ui"]; },
    value => { value.jobs["release-ui"].if = false; },
    value => { value.jobs["release-ui"]["continue-on-error"] = true; },
    value => { value.jobs["release-ui"].steps = value.jobs["release-ui"].steps.filter(step => step.run !== "npm run test:release-ui-browser"); },
    value => { value.jobs["release-ui"].steps.find(step => step.run === "npm run test:release-ui-browser")!.if = "false"; },
    value => { value.jobs["release-ui"].steps.find(step => step.run === "npm run test:release-ui")!["continue-on-error"] = true; },
    value => { value.jobs.overall.if = "success()"; },
    value => { value.jobs.overall.needs = requiredJobs.filter(job => job !== "release-ui"); },
    value => { value.jobs.overall.steps[0].run = String(value.jobs.overall.steps[0].run).replace('[[ "${RELEASE_UI_RESULT}" == success ]]\n', ""); },
    value => { value.jobs.overall.steps[0].run = String(value.jobs.overall.steps[0].run) + "\ntrue"; },
    value => { (value.jobs.overall.steps[0].env as Record<string, string>).RELEASE_UI_RESULT = "success"; },
    value => { value.jobs.web.steps = value.jobs.web.steps.filter(step => step.run !== "npm run test:ui-regression:parity"); },
  ];
  for (const mutate of mutations) {
    const copy = structuredClone(workflow); mutate(copy);
    assert.throws(() => assertRequiredReleaseUI(pkg, copy));
  }
});

// These two newly required browser witnesses are additional source
// bindings, not replacements or new entries in the original 733-file authority.
// Candidate hashes stay recorded. Final hashes also bind the added monitoring,
// permission/conflict cases, screenshots, and the API-valid MFA fixture.
// The pre-label hash retains the witness delivered in 4ffd232. The final
// witness also checks editor wording, native timers with a fixed clock, and
// CI-discovered notification and magnified-layout defects using original oracles.
const browserSources = [
  { path: "web/tests/ui-refresh-layout-browser.test.mts", candidateSha256: "7f5325776cb0f76ac2c6d32af747610f50e25c8d7a6884f8401a113a4c67b8d9", preLabelCorrectionSha256: "55b6cc80990583d1c7d2e9d07b5bcee7044b7fc2d95cb8362eb48478019a11ef", labelCorrectionSha256: "aa0e10f2f27005d663669a02163b4a5a90d901690342f2620f6926eccae51842", preCaptureCorrectionSha256: "e1169b077e1be4fdf688a1b862dfaf1f238affc7bda54b504a475fc4d54b8ced", sha256: "e50b93b846a35cb340487c069827a13bc4e3f9e9267d58654e30f7a3400de4aa", guard: 'assert.deepEqual(requestMethods.filter(request => request.method !== "GET" && request.path !== "/auth/session/refresh"), [], "no application or token mutation may be sent");', additions: ['test("Monitoring and Dashboard retain geometry during real scheduled GETs at desktop and mobile widths"', 'test("v2 preset screens enforce permissions, distinguish failed and empty reads, and retain drafts on revision conflict"', 'expected_revision: 4 } }], "one fixture-only mutation', '"Edit Video cover presets", "the editor title must have a readable action and resource name"', '"Update", "the submit button describes an action, not a successful outcome"', '"Update", "a revision conflict must not label the edit as successful"', 'static now(){return uiNow;}', 'performance.now()") - elapsedBefore >= 1_000', 'test("idle notification regions retain the required forced-colors observation contract"', 'test("magnified stream visual controls remain reachable with the original layout contract"', 'assertObservation(observation, condition);', 'assertLayout(layout);'] },
  { path: "web/tests/ui-release-layout-browser.test.mts", candidateSha256: "92a3927b02953903981b66ad62b5f3138a37a79f0450a83721a66765052c7c4e", sha256: "07103c9338c00a1c0dabba209ef80a717a12f0d3ecf3f3186e74561bfa4493b0", guard: 'assert.deepEqual(unexpectedWrites, [], "layout checks cannot issue application mutations");', additions: ['"/security/settings": { password_min_length: 12, mfa_mode: "totp" }'] },
] as const;
function assertBrowserWitness(path: string, bytes: Buffer) {
  const row = browserSources.find(source => source.path === path);
  assert.ok(row, "unknown CP174 browser witness");
  assert.equal(hash(bytes), row.sha256, "CP174 required browser witness bytes changed");
}
test("UI-CP174-004: required browser witnesses cannot be omitted, skipped, returned early or weakened", () => {
  for (const { path, guard, candidateSha256, sha256, additions } of browserSources) {
    const current = raw(path), source = current.toString("utf8");
    assertBrowserWitness(path, current);
    assert.notEqual(sha256, candidateSha256, "final browser proof strengthens the recorded candidate");
    assert.ok(source.includes(guard));
    for (const addition of additions) assert.ok(source.includes(addition), "required browser correction: " + addition);
    const mutants = ["", source.replace("test(\"CP", "test.skip(\"CP"), source.replace("async t => {", "async t => { return;"), source.replace(guard, "void 0;"), ...additions.map(addition => source.replace(addition, "omitted correction"))];
    for (const mutant of mutants) {
      assert.notEqual(mutant, source, "negative control must change the actual witness");
      assert.throws(() => assertBrowserWitness(path, Buffer.from(mutant)), /browser witness bytes changed/);
    }
  }
  assert.match(raw("web/tests/ui-regression/parity.test.mts").toString("utf8"), /^import "\.\/cp174-source-deltas\.test\.mts";$/m, "CP174 independent tests are imported unconditionally by the existing required suite");
});
