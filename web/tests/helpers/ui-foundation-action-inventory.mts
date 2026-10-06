import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import type { ActionRisk } from "../../src/lib/foundation/actions/contracts.ts";
import type { ResourceActionTemplate } from "../../src/features/resources/resource-action-descriptors.ts";

export const actionInventoryKeys = Object.freeze([
  "id", "feature", "action", "uiSource", "route", "method", "userTriggered", "permission", "risk",
  "visibility", "availabilityInputs", "confirmation", "duplicateScope", "retry", "errorMapping", "audit",
  "secretCapability", "migrationWave",
] as const);

export type ActionInventoryRow = Readonly<Record<typeof actionInventoryKeys[number], string> & {
  risk: ActionRisk;
  method: "DELETE" | "GET" | "POST" | "PUT" | "PUT binary";
}>;

function readInventory(name: string): readonly ActionInventoryRow[] {
  const raw = readFileSync(new URL(`../fixtures/${name}`, import.meta.url), "utf8");
  assert.equal(raw.includes("\r"), false, `${name}: LF only`);
  assert.ok(raw.endsWith("\n") && !raw.endsWith("\n\n"), `${name}: exactly one terminal LF`);
  const lines = raw.slice(0, -1).split("\n");
  const rows = lines.map((line, index) => {
    const row = JSON.parse(line) as Record<string, unknown>;
    assert.deepEqual(Object.keys(row), actionInventoryKeys, `${name}:${index + 1}: exact ordered schema`);
    assert.equal(JSON.stringify(row), line, `${name}:${index + 1}: compact JSON`);
    assert.ok(Object.values(row).every((value) => typeof value === "string" && value.length > 0), `${name}:${index + 1}: string fields`);
    assert.ok(["routine", "guarded", "high", "critical"].includes(String(row.risk)), `${name}:${index + 1}: canonical risk`);
    assert.ok(["DELETE", "GET", "POST", "PUT", "PUT binary"].includes(String(row.method)), `${name}:${index + 1}: HTTP method`);
    return Object.freeze(row as ActionInventoryRow);
  });
  const ids = rows.map(({ id }) => id);
  assert.equal(new Set(ids).size, ids.length, `${name}: duplicate action ID`);
  assert.deepEqual(ids, [...ids].sort(), `${name}: lexical ID order`);
  return Object.freeze(rows);
}

// Keep historical provenance separate; every current gate uses the full union.
export const frozenActionInventory = readInventory("ui-foundation-action-inventory.jsonl");
export const v2ActionInventory = readInventory("ui-foundation-action-inventory-v2.jsonl");
export const currentActionInventory = Object.freeze(
  [...frozenActionInventory, ...v2ActionInventory].sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
);
assert.equal(new Set(currentActionInventory.map(({ id }) => id)).size, currentActionInventory.length, "current inventory: duplicate action ID");

export function assertResourceActionInventory(
  rows: readonly ActionInventoryRow[],
  descriptors: readonly ResourceActionTemplate[],
) {
  const resources = rows.filter(({ id }) => id.startsWith("RES-"));
  const ids = resources.map(({ id }) => id);
  assert.equal(new Set(ids).size, ids.length, "Resource inventory IDs must be unique");
  assert.equal(new Set(descriptors.map(({ id }) => id)).size, descriptors.length, "Resource descriptor IDs must be unique");
  assert.deepEqual([...ids].sort(), descriptors.map(({ id }) => id).sort(), "Resource implementation IDs must equal current inventory IDs");
  for (const entry of resources) {
    const descriptor = descriptors.find(({ id }) => id === entry.id);
    assert.ok(descriptor, entry.id);
    assert.equal(descriptor.route, entry.route, `${entry.id} route`);
    assert.equal(descriptor.method, entry.method, `${entry.id} method`);
    assert.equal(descriptor.risk, entry.risk, `${entry.id} risk`);
    assert.equal(descriptor.auditAction, entry.audit, `${entry.id} audit`);
    assert.equal(descriptor.wave, entry.migrationWave, `${entry.id} wave`);
    const duplicateScope = entry.duplicateScope === "RA" ? "resource-action" : entry.duplicateScope === "RT" ? "resource-target" : entry.duplicateScope === "SESS" ? "session-flow" : undefined;
    assert.ok(duplicateScope, `${entry.id} known duplicate scope`);
    assert.equal(descriptor.duplicateScope, duplicateScope, `${entry.id} duplicate scope`);
    const confirmation = entry.confirmation === "C" ? "consequence" : entry.confirmation === "T(deepgram_api_key)" || entry.confirmation === "T(SECURITY POLICY)" ? "typed-fixed" : entry.confirmation.startsWith("T(") ? "typed-label" : undefined;
    assert.ok(confirmation, `${entry.id} known confirmation`);
    assert.equal(descriptor.confirmation, confirmation, `${entry.id} confirmation`);
    assert.equal(entry.retry, "0", `${entry.id} no automatic retry`);
    if (entry.migrationWave === "v2") {
      assert.equal(entry.permission, descriptor.basePermission, `${entry.id} permission`);
      assert.equal(entry.feature, "generic-resources", `${entry.id} feature owner`);
      const mutationOwners = {
        create: "create-resource-form.tsx/CreateResourceForm",
        update: "edit-resource-button.tsx/EditResourceButton",
        delete: "delete-resource-button.tsx/DeleteResourceButton",
      } as const;
      assert.ok(descriptor.operation in mutationOwners, `${entry.id} preset CRUD operation`);
      assert.equal(entry.uiSource, mutationOwners[descriptor.operation as keyof typeof mutationOwners], `${entry.id} UI mutation owner`);
      assert.equal(entry.availabilityInputs, descriptor.operation === "create" ? "P,F,D,X" : "P,F,D,X,R", `${entry.id} revision availability`);
      assert.equal(entry.userTriggered, "yes", `${entry.id} user intent`);
      assert.equal(entry.visibility, "V/NA", `${entry.id} visibility`);
      assert.equal(entry.errorMapping, "A,P,U", `${entry.id} error mapping`);
      assert.equal(entry.secretCapability, "-", `${entry.id} secret capability`);
      assert.equal(descriptor.secretInput, false, `${entry.id} no secret input`);
    }
  }
}
