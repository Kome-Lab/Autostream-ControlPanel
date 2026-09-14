import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

export type StateObservationPolicy = { controls: "status-only"; role: "status" | "alert"; heading: Record<string, string>; copy: Record<string, string> };
export type Surface = { id: string; route: string; primary: string; source: string; stage: string; fixture?: string; states: Record<string, { applicable: boolean; contract: string; source: string; route?: string; observation?: StateObservationPolicy }> };
type Inventory = { widths: number[]; modes: string[]; locales: string[]; themes: string[]; surfaces: Surface[] };
export const inventory: Inventory = JSON.parse(readFileSync(new URL("../fixtures/ui-regression/surfaces.json", import.meta.url), "utf8"));
export type Condition = { id: string; family: string; route: string; kind: string; state: string; width: number; mode: string; locale: string; theme: string; exercise?: string };
export const conditions: Condition[] = [];
function add(surface: Surface, kind: string, state: string, widths: number[], themes: string[], exercise?: string) {
  for (const width of widths) for (const theme of themes) for (const mode of inventory.modes) for (const locale of inventory.locales) {
    const id = [kind, surface.id, state, theme, mode, locale, width, exercise || "capture"].join("--");
    conditions.push({ id, family: surface.id, route: surface.states[state]?.route || surface.route, kind, state, width, theme, mode, locale, ...(exercise ? { exercise } : {}) });
  }
}
for (const surface of inventory.surfaces) {
  add(surface, "ready", "ready", inventory.widths, ["autostream"]);
  for (const [state, contract] of Object.entries(surface.states) as [string, { applicable: boolean }][]) {
    if (contract.applicable) add(surface, "state", state, [390, 1440], ["autostream"]);
  }
  if (surface.id === "dashboard") add(surface, "parity", "ready", [390, 1440], ["autostream"], "denied-read");
  if (surface.fixture) add(surface, "shared", "ready", [390, 1440], inventory.themes, surface.fixture);
  for (const exercise of ["keyboard", "css-magnification-200", "forced-colors", "reduced-motion", "long-text", "long-id", "system-mode"]) {
    add(surface, "accessibility", "ready", [390, 1920], ["autostream"], exercise);
  }
}
export function selectedConditions(family: string) {
  assert.ok(inventory.surfaces.some((item: { id: string }) => item.id === family), "unknown shard");
  return conditions.filter((item) => item.family === family);
}
export function assertExecution(expected: readonly Condition[], results: readonly { id: string; status: string }[]) {
  assert.ok(expected.length > 0, "zero expected cases");
  assert.equal(new Set(results.map((row) => row.id)).size, results.length, "duplicate execution");
  assert.deepEqual(results.map((row) => row.id).sort(), expected.map((row) => row.id).sort(), "missing or extra cases");
  assert.ok(results.every((row) => row.status === "PASS"), "FAIL/SKIP/CANCELLED/NOT_REACHED is not success");
}
assert.equal(conditions.length, 4144, "current plan denominator");
assert.equal(inventory.surfaces.length, 28);
assert.equal(conditions.filter((row) => row.kind === "ready").length, 784);
assert.equal(conditions.filter((row) => row.kind === "shared").length, 480);
assert.equal(new Set(conditions.map((row) => row.id)).size, conditions.length);

export const nativeZoomEvidence = Object.freeze({ required: true, status: "NATIVE_ZOOM_EVIDENCE_PENDING", evidence: null, reason: "No approved native zoom operation for the protected Chrome launch profile." });
