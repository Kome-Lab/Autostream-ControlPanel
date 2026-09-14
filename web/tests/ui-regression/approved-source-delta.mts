import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import ts from "typescript";

// Fixed old identities and explicit source transformations, never expected=current hashes.
const approval = {
  "schemaVersion": 1,
  "protected_fixture_sha256": "cd9a25bad28720244b8c9b9ec8c5ed5c08df1914800fd6b7831f5efcd3459ae8",
  "protectedDeltas": [
    {
      "path": "web/src/components/shell/use-shell-session-guard.ts",
      "original_sha256": "07f5adae4bfc8708d31a3bca980a98050bd1668ada6e66fb3c1ec849f2b93347",
      "transform": "session-redirect-notices-only",
      "reason": "Notify immediately before the four existing redirect calls; retain every predicate and existing statement."
    },
    {
      "path": "web/tests/helpers/browser-harness.mts",
      "original_sha256": "bd45673d96e20aaea5927fa6b57c1c4d1445c36bb515e8e14758554bfa5ab5de",
      "transform": "add-closed-press-tab-only",
      "reason": "One closed Tab direction API; all existing class members and native-key contracts remain fixed."
    }
  ],
  "runnerTypeDelta": {
    "path": "web/tests/helpers/run-ui-foundation-browser.mts",
    "original_sha256": "31eaadac62a427fedb59be8fca91b23b995c7cf5d93da014159b634ad221d3e3",
    "transform": "capture-narrowed-result-file-only"
  }
} as const;
export const approvedProtectedPaths = approval.protectedDeltas.map(record => record.path);
export function assertApprovedManifest(value: unknown): asserts value is typeof approval {
  assert.deepEqual(value, approval, "missing or changed explicit source-delta supplement");
}
function hash(raw: Buffer) { return createHash("sha256").update(raw).digest("hex"); }
function normalized(raw: Buffer) { return raw.toString("utf8").replace(/\r\n/g, "\n"); }
function exactlyOnce(text: string, from: string, to: string) {
  assert.equal(text.split(from).length - 1, 1, "approved insertion must exist exactly once at its original position");
  return text.replace(from, to);
}
function syntax(text: string) {
  return ts.createPrinter({ newLine: ts.NewLineKind.LineFeed }).printFile(ts.createSourceFile("source.ts", text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS));
}
export function assertProtectedFixture(raw: Buffer, manifest: unknown) {
  assertApprovedManifest(manifest);
  assert.equal(hash(raw), manifest.protected_fixture_sha256, "all 733 original protected records/hashes must remain byte-identical");
}
const tabMethod = "  async pressTab(direction: \"forward\" | \"backward\"): Promise<void> {\n    if (direction !== \"forward\" && direction !== \"backward\") throw new Error(\"Unsupported Tab direction\");\n    if (this.closed) throw new Error(\"Browser harness closed\");\n    this.assertNoFatalError();\n    const tabInput = { key: \"Tab\", code: \"Tab\", windowsVirtualKeyCode: 9, nativeVirtualKeyCode: 9, modifiers: direction === \"backward\" ? 8 : 0 };\n    await this.send(\"Input.dispatchKeyEvent\", { ...tabInput, type: \"keyDown\" });\n    await this.send(\"Input.dispatchKeyEvent\", { ...tabInput, type: \"keyUp\" });\n  }\n\n";
export function assertApprovedSourceDelta(path: string, before: Buffer, after: Buffer, manifest: unknown) {
  assertApprovedManifest(manifest);
  const record = manifest.protectedDeltas.find(row => row.path === path);
  assert.ok(record, "unknown protected source-delta path");
  assert.equal(hash(before), record.original_sha256, "source-delta fixed original hash mismatch");
  let stripped = normalized(after);
  if (record.transform === "add-closed-press-tab-only") {
    const parsed = ts.createSourceFile(path, stripped, ts.ScriptTarget.Latest, true);
    const owner = parsed.statements.find((node): node is ts.ClassDeclaration => ts.isClassDeclaration(node) && node.name?.text === "BrowserHarness");
    assert.ok(owner);
    assert.equal(owner.members.filter(member => ts.isMethodDeclaration(member) && member.name.getText(parsed) === "pressTab").length, 1);
    stripped = exactlyOnce(stripped, tabMethod, "");
  } else {
    stripped = exactlyOnce(stripped, 'import { notifyDraftSessionExit } from "@/lib/ui-v2/draft-navigation-lifecycle";\n', "");
    stripped = exactlyOnce(stripped,
      '            notifyDraftSessionExit();\n            window.location.replace(loginPathForLocation(window.location, true));',
      '            window.location.replace(loginPathForLocation(window.location, true));');
    for (const [predicate, redirect] of [
      ["sessionExpired", "window.location.replace(loginPathForLocation(window.location, true));"],
      ["active", 'router.replace(status.setup_required ? "/setup" : loginPathForLocation(window.location));'],
      ["active", "router.replace(loginPathForLocation(window.location));"],
    ]) stripped = exactlyOnce(stripped, `if (${predicate}) { notifyDraftSessionExit(); ${redirect} }`, `if (${predicate}) ${redirect}`);
  }
  assert.equal(syntax(stripped), syntax(normalized(before)), "original AST predicates, APIs, ordering and members must remain identical");
  assert.equal(stripped, normalized(before), "only exact approved additions may differ outside newline normalization");
  return { path, original_sha256: record.original_sha256, transform: record.transform, originalAST: "MATCH", remainingBytes: "MATCH" };
}
export function assertRunnerTypeDelta(before: Buffer, after: Buffer, manifest: unknown) {
  assertApprovedManifest(manifest);
  assert.equal(hash(before), manifest.runnerTypeDelta.original_sha256, "runner fixed original hash mismatch");
  let stripped = exactlyOnce(normalized(after), "  const resultFile = result.file;\n  return requiredBrowserTestFiles.some((candidate) => {", "  return requiredBrowserTestFiles.some((candidate) => {");
  stripped = exactlyOnce(stripped, "return nameMatches && resolve(resultFile) === expectedPath;", "return nameMatches && resolve(result.file) === expectedPath;");
  assert.equal(syntax(stripped), syntax(normalized(before)), "runner runtime and registration AST must remain identical");
  assert.equal(stripped, normalized(before), "only the narrowed local capture is authorized");
}
