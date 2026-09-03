import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const modules = [
  "streams-view.tsx",
  "stream-slot-form.tsx",
  "stream-details-dialog.tsx",
  "stream-summary.tsx",
  "stream-view-options.ts",
  "stream-lifecycle.ts",
  "stream-action-feedback.ts",
] as const;

const source = (name: string) => readFileSync(
  new URL(`../src/features/streams/${name}`, import.meta.url),
  "utf8",
);

test("Streams orchestration stays bounded after the behavior-preserving split", () => {
  assert.ok(source("streams-view.tsx").split(/\r?\n/).length <= 500);
  for (const filename of modules.slice(1)) {
    assert.ok(source(filename).split(/\r?\n/).length <= 350, filename);
  }
});

test("Streams view delegates forms, details, summaries, and lifecycle rules", () => {
  const orchestrator = source("streams-view.tsx");
  for (const delegatedModule of [
    "stream-slot-form",
    "stream-details-dialog",
    "stream-summary",
    "stream-lifecycle",
  ]) {
    assert.match(orchestrator, new RegExp(`@/features/streams/${delegatedModule}`));
  }
  assert.doesNotMatch(orchestrator, /function\s+(StreamSlotForm|StreamDetailsDialog|StreamSummary)\b/);
});

test("presentation modules do not own transport dispatch", () => {
  for (const filename of [
    "streams-view.tsx",
    "stream-slot-form.tsx",
    "stream-details-dialog.tsx",
    "stream-summary.tsx",
  ]) {
    assert.doesNotMatch(source(filename), /\b(?:apiGet|apiPost|apiPut|apiDelete|fetch)\s*\(/, filename);
  }
});

test("the split stream modules have no dependency cycle", () => {
  const moduleNames = new Set(modules.map((name) => name.replace(/\.(?:ts|tsx)$/, "")));
  const graph = new Map<string, string[]>();
  for (const file of modules) {
    const name = file.replace(/\.(?:ts|tsx)$/, "");
    const dependencies = [...source(file).matchAll(/from\s+["']@\/features\/streams\/([^"']+)["']/g)]
      .map((match) => match[1])
      .filter((dependency) => moduleNames.has(dependency));
    graph.set(name, dependencies);
  }

  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (name: string) => {
    assert.equal(visiting.has(name), false, `dependency cycle at ${name}`);
    if (visited.has(name)) return;
    visiting.add(name);
    for (const dependency of graph.get(name) ?? []) visit(dependency);
    visiting.delete(name);
    visited.add(name);
  };
  for (const name of moduleNames) visit(name);
});
