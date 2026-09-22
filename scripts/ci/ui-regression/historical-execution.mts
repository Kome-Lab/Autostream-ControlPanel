import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { historicalScenarioPlan } from "../../../web/tests/ui-regression/history-scenario-owner.mts";

export const historicalAfter = "b266aabb896f0df9880a0354c1135130d6938e8d";
export const historicalScenarioPath = "web/tests/helpers/bundle9-browser-scenarios.mts";
export const historicalScenarioSHA256 = "4e28633097e4ccb40e3a7bffe721b2ffb59524b755aa879437c302451d5ba6f6";
const ownerURL = new URL("../../../web/tests/ui-regression/history-scenario-owner.mts", import.meta.url);
const digest = (bytes: string | Uint8Array) => createHash("sha256").update(bytes).digest("hex");

// Exact raw edits, independently reversible to the fixed Git blob. Nothing in
// a scenario's operations, fixture, required-response assertions, or capture
// implementation is replaced. There is no general historical hash exemption.
export function historicalOwnershipEdits() {
  return [
    { id: "owner-import", from: 'import assert from "node:assert/strict";', to: `import { HistoricalScenarioOwners, assertHistoricalFreshDocument, closeHistoricalOwner, nextHistoricalFixture } from ${JSON.stringify(ownerURL.href)};\nimport assert from "node:assert/strict";` },
    { id: "fresh-target-instead-of-discard-navigation", from: '  browser.setFetchDiagnosticContext?.({ phase: "to-blank" });\n  await browser.navigate("about:blank");', to: '  await assertHistoricalFreshDocument(browser);' },
    { id: "active-owner-reference", from: '  const browser = await BrowserHarness.launch();', to: '  let browser = await BrowserHarness.launch();\n  let owners: HistoricalScenarioOwners<BrowserHarness> | undefined;\n  let primaryFailure: { error: unknown } | undefined;' },
    { id: "fixture-owner-reference", from: '  const fixture = createBundle9Fixture(baseURL);', to: '  let fixture = createBundle9Fixture(baseURL);' },
    { id: "configure-each-owner", from: '    writeJSON(resolve(output, "browser-version.json"), browserVersion);', to: `    writeJSON(resolve(output, "browser-version.json"), browserVersion);
    owners = new HistoricalScenarioOwners(browser, () => BrowserHarness.launch(), async (next) => {
      browser = next;
      fixture = nextHistoricalFixture(fixture, () => createBundle9Fixture(baseURL));
      next.setFetchDiagnosticContext?.({ side: basename(resolve(output)) });
      next.setRouteResolver(fixture.resolver);
      await next.configureDeterministicDocument({ timezone: "Asia/Tokyo", locale: "ja-JP", source: initialDocumentScript });
      assert.deepEqual(await next.browserVersion(), browserVersion, "same historical browser across owners");
    });
    const executionOwners = owners;` },
    { id: "capture-owner-inventory", from: '      captures.push({ name, observation: evidence, pngSHA256: sha256(png) });', to: '      captures.push({ name, observation: evidence, pngSHA256: sha256(png) });\n      executionOwners.recordCapture(name);' },
    { id: "scenario-owner-begin", from: '    const scenario = async (name: string, execute: () => Promise<void>) => {\n      browser.setFetchDiagnosticContext', to: '    const scenario = async (name: string, execute: () => Promise<void>) => executionOwners.run(name, async (next) => {\n      browser = next;\n      browser.setFetchDiagnosticContext' },
    { id: "scenario-owner-end", from: '        browser.assertNoFatalError();\n      }\n    };', to: '        browser.assertNoFatalError();\n      }\n    });' },
    { id: "surface-viewport-owner", from: 'await browser.setViewport(viewport.width, viewport.height);', to: 'await executionOwners.setViewport(viewport.width, viewport.height);' },
    { id: "desktop-viewport-owner", from: 'await browser.setViewport(1440, 900);', to: 'await executionOwners.setViewport(1440, 900);' },
    { id: "mobile-viewport-owner", from: 'await browser.setViewport(390, 844);', to: 'await executionOwners.setViewport(390, 844);' },
    { id: "cleanup-before-pass", from: '    assertCaptureInventory(captures.map((capture) => capture.name));', to: '    assertCaptureInventory(captures.map((capture) => capture.name));\n    writeJSON(resolve(output, "execution-ownership.json"), executionOwners.finish());' },
    { id: "preserve-primary-during-final-cleanup", from: '  } catch (error) {\n    try {\n      const diagnostic', to: '  } catch (error) {\n    primaryFailure = { error };\n    try {\n      const diagnostic' },
    { id: "owner-final-cleanup", from: '  } finally { await browser.close(); }', to: '  } finally { await closeHistoricalOwner(owners, browser, primaryFailure); }' },
  ];
}

function replaceExactlyOnce(source: string, from: string, to: string, label: string) {
  assert.equal(source.split(from).length - 1, 1, `${label}: exact transform site`);
  return source.replace(from, to);
}

export function transformHistoricalScenario(original: Buffer) {
  assert.equal(digest(original), historicalScenarioSHA256, "fixed historical scenario raw hash");
  let source = original.toString("utf8");
  for (const edit of historicalOwnershipEdits()) source = replaceExactlyOnce(source, edit.from, edit.to, edit.id);
  verifyHistoricalDerivative(original, Buffer.from(source));
  return Buffer.from(source);
}

export function verifyHistoricalDerivative(original: Buffer, derivative: Buffer) {
  assert.equal(digest(original), historicalScenarioSHA256, "fixed historical scenario raw hash");
  let reverse = derivative.toString("utf8");
  for (const edit of historicalOwnershipEdits().toReversed()) reverse = replaceExactlyOnce(reverse, edit.to, edit.from, edit.id);
  assert.deepEqual(Buffer.from(reverse), original, "all bytes outside ownership transform must match original");
  return true;
}

export function prepareHistoricalExecution(root: string, executionRoot: string) {
  const git = (...args: string[]) => execFileSync("git", ["-c", `safe.directory=${root.replaceAll("\\", "/")}`, "-C", root, ...args]);
  assert.equal(git("rev-parse", "HEAD").toString("utf8").trim(), historicalAfter, "actual fixed historical checkout");
  const paths = git("ls-tree", "-r", "--name-only", historicalAfter, "web/tests/helpers").toString("utf8").trim().split("\n");
  const originals = paths.map(path => {
    const raw = git("cat-file", "blob", `${historicalAfter}:${path}`);
    assert.deepEqual(readFileSync(resolve(root, path)), raw, `${path}: fixed original working bytes`);
    return { path, raw };
  });
  const scenario = originals.find(row => row.path === historicalScenarioPath);
  assert.ok(scenario);
  const transformed = transformHistoricalScenario(scenario.raw);
  mkdirSync(executionRoot, { recursive: false });
  const files = originals.map(({ path, raw }) => {
    const bytes = path === historicalScenarioPath ? transformed : raw;
    const target = resolve(executionRoot, path);
    mkdirSync(dirname(target), { recursive: true });
    writeFileSync(target, bytes, { flag: "wx" });
    assert.deepEqual(readFileSync(target), bytes);
    return { path, originalSHA256: digest(raw), executionSHA256: digest(bytes), changed: path === historicalScenarioPath };
  });
  const proof = {
    fixedCommit: historicalAfter, originalScenarioSHA256: historicalScenarioSHA256,
    executionScenarioSHA256: digest(transformed), adapterSHA256: digest(readFileSync(fileURLToPath(import.meta.url))),
    ownerSHA256: digest(readFileSync(ownerURL)), files,
    transforms: historicalOwnershipEdits().map(({ id, from, to }) => ({ id, originalSHA256: digest(from), derivativeSHA256: digest(to) })),
    reverseRawEquality: true, unchangedFixtureAssertionsAndOperations: true,
    ownerPlan: historicalScenarioPlan, copiesOriginalSources: true, modifiesHistoricalCheckout: false,
  };
  return { moduleURL: pathToFileURL(resolve(executionRoot, historicalScenarioPath)), proof };
}

// Both sides must retain the original order, identity and inventory, in addition
// to the unchanged contract's API/DOM comparison and exact zero-pixel threshold.
type Pixel = Readonly<{ width: number; height: number; data: Uint8Array }>;
type Captures = Readonly<{ browserVersion: unknown; captures: readonly Readonly<{ name: string; observation: unknown }>[] }>;
type HistoricalContract = Readonly<{
  bundle9ExpectedCaptureNames: readonly string[];
  assertSameObservation: (before: unknown, after: unknown) => unknown;
  assertPixelIdentity: (before: Pixel, after: Pixel) => { width: number; height: number; differentPixels: number };
}>;

export async function compareHistoricalCaptures(before: Captures, after: Captures, contract: HistoricalContract, decode: (side: "before" | "after", name: string) => Promise<Pixel>) {
  const expected = contract.bundle9ExpectedCaptureNames;
  assert.deepEqual(historicalScenarioPlan.flatMap(row => [...row.captures]), expected, "owner plan preserves fixed 41 capture order");
  for (const side of [before, after]) assert.deepEqual(side.captures.map(item => item.name), expected, "exact historical capture inventory/order");
  contract.assertSameObservation(before.browserVersion, after.browserVersion);
  const results = [];
  for (let index = 0; index < expected.length; index += 1) {
    const name = expected[index];
    contract.assertSameObservation(before.captures[index].observation, after.captures[index].observation);
    results.push({ name, ...contract.assertPixelIdentity(await decode("before", name), await decode("after", name)) });
  }
  assert.equal(results.length, 41);
  return results;
}
