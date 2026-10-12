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

// Historical snapshots remain immutable; current confirmation changes are an
// independent, finite specification, never derived from implementation rows.
export const frozenActionInventory = readInventory("ui-foundation-action-inventory.jsonl");
export const v2ActionInventory = readInventory("ui-foundation-action-inventory-v2.jsonl");
const currentConfirmationChanges: Readonly<Record<string, string>> = Object.freeze({
  "RES-13": "T(deepgram_api_key)",
  "RES-24": "T(provider label)",
  "RES-25": "T(provider label)",
  "RES-32": "T(public username)",
  "RES-34": "T(role label)",
  "RES-35": "T(role label)",
  "RES-40": "T(SECURITY POLICY)",
});

export function buildCurrentActionInventory(
  frozen: readonly ActionInventoryRow[],
  v2: readonly ActionInventoryRow[],
): readonly ActionInventoryRow[] {
  const historical = [...frozen, ...v2];
  for (const [id, confirmation] of Object.entries(currentConfirmationChanges)) {
    const matches = historical.filter((row) => row.id === id);
    assert.equal(matches.length, 1, `${id} current confirmation source must be unique`);
    assert.equal(matches[0].confirmation, confirmation, `${id} frozen confirmation before current change`);
  }
  assert.equal(frozen.length, 100, "frozen inventory: 100 actions");
  assert.equal(v2.length, 6, "v2 inventory: six actions");
  const current = historical.map((row) => Object.hasOwn(currentConfirmationChanges, row.id)
    ? Object.freeze({ ...row, confirmation: "C" }) : row)
    .sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  assert.equal(new Set(current.map(({ id }) => id)).size, current.length, "current inventory: duplicate action ID");
  return Object.freeze(current);
}

export const currentActionInventory = buildCurrentActionInventory(frozenActionInventory, v2ActionInventory);

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
