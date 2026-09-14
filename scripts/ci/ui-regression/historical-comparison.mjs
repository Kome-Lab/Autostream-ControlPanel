import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { exportWeb, buildExport } from "./source-export.mjs";
import { staticExportServer } from "../../../web/tests/ui-regression/static-server.mts";
import { ownedOutput } from "../../../web/tests/ui-regression/run-browser.mts";

assert.equal(process.env.CI, "true");
assert.equal(process.platform, "linux");
assert.equal(execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim(), process.env.GITHUB_SHA, "actual outer workflow commit");
const fixedAfter = "b266aabb896f0df9880a0354c1135130d6938e8d";
const root = resolve(process.argv[2]);
const output = resolve(process.argv[3]);
const git = (...args) => execFileSync("git", ["-C", root, ...args], { encoding: "utf8" }).trim();
assert.equal(git("rev-parse", "HEAD"), fixedAfter, "actual historical checkout, never an env disguise");
ownedOutput(process.env.RUNNER_TEMP || "", output);
mkdirSync(output);
const contract = await import(pathToFileURL(resolve(root, "web/tests/helpers/bundle9-browser-contract.mts")));
const { fontInventory } = await import(pathToFileURL(resolve(root, "web/tests/helpers/run-bundle9-ui-comparison.mts")));
const { captureBundle9Source } = await import(pathToFileURL(resolve(root, "web/tests/helpers/bundle9-browser-scenarios.mts")));
const { BUNDLE9_BROWSER_BEFORE: before, BUNDLE9_BROWSER_CLOCK: clock, assertSameObservation, assertPixelIdentity, bundle9ExpectedCaptureNames } = contract;
const write = (name, data) => writeFileSync(resolve(output, name), JSON.stringify(data, null, 2) + "\n", { flag: "wx" });
write("sources.json", { workflowCommit: process.env.GITHUB_SHA, before, after: fixedAfter, actualHistoricalHEAD: git("rev-parse", "HEAD"), comparison: "historical-only", pixelThreshold: 0, mask: false });
const expectedLock = readFileSync(resolve(root, "web/package-lock.json"));
assert.deepEqual(Buffer.from(execFileSync("git", ["-C", root, "show", before + ":web/package-lock.json"])), expectedLock);
const digest = bytes => createHash("sha256").update(bytes).digest("hex");
const helperPaths = git("ls-tree", "-r", "--name-only", fixedAfter, "web/tests/helpers").split("\n").filter(Boolean);
const helpers = helperPaths.map(path => {
  const raw = execFileSync("git", ["-C", root, "show", fixedAfter + ":" + path]);
  assert.deepEqual(readFileSync(resolve(root, path)), raw, "historical helper must match fixed raw source");
  return { path, sha256: digest(raw) };
});
const beforeTree = git("rev-parse", before + ":web/src"), afterTree = git("rev-parse", fixedAfter + ":web/src");
assert.notEqual(beforeTree, afterTree, "different fixed UI source trees required");
const fonts = fontInventory();
write("source-and-conditions.json", {
  workflowCommit: process.env.GITHUB_SHA, before: { commit: before, webSourceTree: beforeTree },
  after: { commit: fixedAfter, webSourceTree: afterTree }, helpers, fonts, clock,
  node: process.version, platform: process.platform, arch: process.arch, lockSHA256: digest(expectedLock),
  expected: bundle9ExpectedCaptureNames, visualAcceptance: "historical identity only",
});
const modules = resolve(root, "web/node_modules");
const sources = {};
for (const [side, revision] of [["before", before], ["after", fixedAfter]]) {
  sources[side] = exportWeb(root, revision, resolve(output, side + "-source"), modules);
  await buildExport(sources[side], resolve(output, side + "-build.log"), { SOURCE_DATE_EPOCH: String(Date.parse(clock) / 1000) });
}
const sharp = createRequire(resolve(root, "web/package.json"))("sharp");
const decode = async file => { const value = await sharp(file).ensureAlpha().raw().toBuffer({ resolveWithObject: true }); return { ...value.info, data: value.data }; };
const captures = {};
for (const side of ["before", "after"]) {
  const server = await staticExportServer(resolve(sources[side], "out"));
  try { captures[side] = await captureBundle9Source(server.baseURL, resolve(output, side)); }
  finally { await server.close(); }
}
assertSameObservation(captures.before.browserVersion, captures.after.browserVersion);
const results = [];
for (const name of bundle9ExpectedCaptureNames) {
  const left = captures.before.captures.find(item => item.name === name);
  const right = captures.after.captures.find(item => item.name === name);
  assert.ok(left && right, "missing historical capture");
  assertSameObservation(left.observation, right.observation);
  results.push({ name, ...assertPixelIdentity(await decode(resolve(output, "before", name + ".png")), await decode(resolve(output, "after", name + ".png"))) });
}
assert.equal(results.length, 41);
write("result.json", { historical: true, before, after: fixedAfter, expected: 41, comparisons: results, status: "PASS" });
