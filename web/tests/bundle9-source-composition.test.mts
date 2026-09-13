import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import ts from "typescript";
import { declarations } from "./helpers/bundle9-source-characterization.mts";
import { composeResponsibilitySource } from "./helpers/bundle9-responsibility-composition.mts";

function hasClientPrologue(source: string) {
  const first = ts.createSourceFile("entry.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX).statements[0];
  return Boolean(first && ts.isExpressionStatement(first) && ts.isStringLiteral(first.expression)
    && first.expression.text === "use client");
}

test("Bundle 9 extracted client entries retain their Next client boundary", () => {
  for (const path of [
    "application/application-info-view.tsx",
    "application/updater-host-bootstrap-panel.tsx",
    "application/updater-settings-form.tsx",
    "application/updater-settings-panel.tsx",
    "nodes/node-registration-view.tsx",
    "streams/stream-preview.tsx",
  ]) {
    const source = readFileSync(new URL(`../src/features/${path}`, import.meta.url), "utf8");
    assert.equal(hasClientPrologue(source), true, path);
    assert.equal(hasClientPrologue(source.replace('"use client";', '')), false, `${path}: removed directive`);
    assert.equal(hasClientPrologue('import { useState } from "react";\n' + source), false, `${path}: displaced directive`);
  }
});

test("Bundle 9 composed UI declarations retain before hashes and reject owner, binding, permission, and effect mutants", () => {
  const webRoot = fileURLToPath(new URL("../", import.meta.url));
  const fixture = JSON.parse(readFileSync(new URL("./fixtures/bundle9-ui-characterization.json", import.meta.url), "utf8"));
  const entries = [
    ["src/features/application/application-info-view.tsx", "ApplicationInfoView"],
    ["src/features/application/updater-settings-panel.tsx", "UpdaterSettingsPanel"],
    ["src/features/application/updater-settings-form.tsx", "UpdaterSettingsForm"],
    ["src/features/nodes/node-registration-view.tsx", "LegacyNodeRegistrationView"],
  ];
  const expected = new Map<string, string>();
  for (const [file, name] of entries) {
    const before = fixture.clusters.flatMap((cluster: { sources: { declarations: { name: string; sha256: string }[] }[] }) => cluster.sources)
      .flatMap((source: { declarations: { name: string; sha256: string }[] }) => source.declarations)
      .find((declaration: { name: string }) => declaration.name === name);
    assert.ok(before, `${name}: original before authority`);
    expected.set(name, before.sha256);
    assert.equal(declarations(composeResponsibilitySource(resolve(webRoot, file)), file).find((entry) => entry.name === name)?.sha256, before.sha256);
  }
  const mutations = [
    ["ApplicationInfoView", "application/application-authority-readers.ts", '"system_updates.execute"', '"system_updates.read"'],
    ["ApplicationInfoView", "application/application-authority-readers.ts", "systemUpdates.refetch(), currentUser.refetch()", "currentUser.refetch(), currentUser.refetch()"],
    ["ApplicationInfoView", "application/application-action-renderers.tsx", '"更新"', '"変更"'],
    ["ApplicationInfoView", "application/application-info-view.tsx", "updates: systemUpdates.data", "updates: undefined"],
    ["ApplicationInfoView", "application/application-authority-readers.ts", "return { refreshTargetAuthority, refreshBatchAuthority, refreshCancelAuthority }", "return { refreshTargetAuthority, refreshBatchAuthority }"],
    ["UpdaterSettingsForm", "application/updater-settings-form.tsx", "transportMode={settings.transport_mode}", 'transportMode={"pull_v2"}'],
    ["UpdaterSettingsForm", "application/updater-runtime-settings-section.tsx", "  return (", "  useEffect(() => {});\n  return ("],
    ["LegacyNodeRegistrationView", "nodes/registered-node-columns.tsx", "deleteNode.mutate(nodeID)", 'deleteNode.mutate("")'],
    ["LegacyNodeRegistrationView", "nodes/node-edit-dialog.tsx", "disabled={!allowed || !editFormValid || updatePending}", "disabled={false}"],
  ];
  for (const [name, relative, original, replacement] of mutations) {
    const mutantPath = resolve(webRoot, "src/features", relative);
    const originalSource = readFileSync(mutantPath, "utf8");
    assert.ok(originalSource.includes(original), `${relative}: actual mutation site`);
    const entry = resolve(webRoot, entries.find(([, symbol]) => symbol === name)![0]);
    assert.throws(() => {
      const composed = composeResponsibilitySource(entry, (file) => file === mutantPath
        ? originalSource.replace(original, replacement) : readFileSync(file, "utf8"));
      assert.equal(declarations(composed, entry).find((declaration) => declaration.name === name)?.sha256, expected.get(name));
    }, undefined, `${name}: ${relative} mutation escaped the fixed before oracle`);
  }
});
