import assert from "node:assert/strict";
import test from "node:test";
import type { ResourceActionIntent } from "../src/features/resources/resource-action-descriptors.ts";
import { createIntent, deferred, oldRow, refreshResourceAction, resourceActionStateSnapshot, resourcePermissionSnapshot, runtimeFixture } from "./helpers/resource-polling-fixture.mts";

test("P1-RESOURCE-POLL-001: three same-content background fetch cycles retain cached action availability", async (t) => {
  const fixture = runtimeFixture(t);
  const intent = createIntent();
  const observations: unknown[] = [];
  for (let cycle = 0; cycle < 3; cycle++) {
    const auth = deferred<unknown>(), resource = deferred<unknown>();
    const authFetch = fixture.client.fetchQuery({ queryKey: ["auth", "me"], queryFn: () => auth.promise });
    const resourceFetch = fixture.client.fetchQuery({ queryKey: ["resource", fixture.path], queryFn: () => resource.promise });
    try {
      observations.push({
        permissions: resourcePermissionSnapshot(fixture.client).kind,
        freshness: resourceActionStateSnapshot(fixture.client, intent).freshness,
        availability: fixture.controller.evaluate(intent).availability.kind,
      });
    } finally {
      auth.resolve({ permissions: ["*"] });
      resource.resolve({ items: [oldRow()] });
      await Promise.all([authFetch, resourceFetch]);
    }
  }
  assert.deepEqual(observations, Array.from({ length: 3 }, () => ({
    permissions: "ready", freshness: "fresh", availability: "allowed",
  })));
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-POLL-002: an unrelated record changing during create confirmation does not block the one save intent", async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.setRows([{ ...oldRow(), name: "Other operator changed this existing BOT", updated_at: "2026-10-28T00:01:00Z" }]);
  const result = await fixture.controller.submit(opened, { confirmed: true });
  assert.equal(result.kind, "succeeded");
  assert.deepEqual(fixture.writes, [{ method: "POST", path: fixture.path, body: intent.payload }]);
});

test("P1-RESOURCE-POLL-003: a route-proven early validation rejection permits an explicit corrected submission", async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  fixture.rejectNext(400, "invalid_discord_config");
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(opened, { confirmed: true })).kind, "failed");
  assert.equal(fixture.writes.length, 1, "no automatic retry after rejection");
  const corrected: ResourceActionIntent = { ...intent, payload: { name: "Corrected BOT", service_id: "bot-one" }, publicLabel: "Corrected BOT" };
  const retry = await fixture.controller.open(corrected);
  assert.equal(retry.kind, "allowed");
  if (retry.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(retry, { confirmed: true })).kind, "succeeded");
  assert.equal(fixture.writes.length, 2, "only the second explicit save dispatches again");
});

test("P1-RESOURCE-POLL-004: a successful list read alone cannot release an unknown create outcome", async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  fixture.loseNextResponse();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(opened, { confirmed: true })).kind, "outcome_unknown");
  await refreshResourceAction(fixture.client, intent);
  fixture.controller.reconcile();
  assert.deepEqual(await fixture.controller.open(intent), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(fixture.writes.length, 1, "read-only refresh is not proof that a lost create was never applied");
});

test("P1-RESOURCE-POLL-005: an edit begun before a real target change is never silently rebased on the fresh row", async (t) => {
  const fixture = runtimeFixture(t);
  const baseline = oldRow();
  const intent: ResourceActionIntent = { id: "RES-05", row: baseline, payload: { name: "My retained draft", service_id: "bot-one" }, publicLabel: "Existing BOT" };
  fixture.setRows([{ ...baseline, name: "Another operator's setting", updated_at: "2026-10-28T00:01:00Z" }]);
  const opened = await fixture.controller.open(intent);
  const result = opened.kind === "allowed" ? await fixture.controller.submit(opened, { confirmed: true }) : opened;
  assert.deepEqual(result, { kind: "blocked", reason: "authority-changed" });
  assert.deepEqual(intent.row, baseline);
  assert.equal(intent.payload?.name, "My retained draft");
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-POLL-006: a real permission revocation still stops dispatch after confirmation", async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.setPermissions([]);
  assert.deepEqual(await fixture.controller.submit(opened, { confirmed: true }), { kind: "blocked", reason: "permission-denied" });
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-POLL-007: a preset's original edit revision remains authoritative after a real revision advance", async (t) => {
  const fixture = runtimeFixture(t, "/discord/target-presets");
  fixture.setRows([{ id: "preset-one", name: "Main target", revision: 8 }]);
  const intent: ResourceActionIntent = {
    id: "RES-42", row: { id: "preset-one", name: "Main target", revision: 7 },
    payload: { name: "My retained target", guild_id: "123", text_channel_id: "456", voice_channel_id: "789", expected_revision: 7 },
    publicLabel: "Main target",
  };
  assert.equal((await fixture.controller.open(intent)).kind, "blocked");
  assert.equal(intent.payload?.expected_revision, 7);
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-POLL-008: new objects and different row or object key order retain identical meaning", async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  const two = { id: "profile-two", name: "Second BOT", updated_at: "2026-10-28T00:00:00Z" };
  fixture.setRows([oldRow(), two]);
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.setRows([two, oldRow()].map(({ id, name, updated_at }) => ({ updated_at, name, id })));
  assert.equal((await fixture.controller.submit(opened, { confirmed: true })).kind, "succeeded");
  assert.equal(fixture.writes.length, 1);
});

test("P1-RESOURCE-POLL-009: a later 400 that can follow a secret write is not made retryable by a successful list GET", async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  fixture.rejectNext(400, "store_discord_bot_token_failed");
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(opened, { confirmed: true })).kind, "failed");
  await refreshResourceAction(fixture.client, intent);
  fixture.controller.reconcile();
  assert.deepEqual(await fixture.controller.open(intent), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(fixture.writes.length, 1);
});

test("P1-RESOURCE-POLL-015: a definitive atomic CAS conflict retains the old draft and permits only an explicitly reopened fresh edit", async (t) => {
  const fixture = runtimeFixture(t), baseline = oldRow();
  const intent: ResourceActionIntent = { id: "RES-05", row: baseline, payload: { name: "My retained draft", expected_updated_at: baseline.updated_at }, publicLabel: "Existing BOT" };
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.rejectNext(409, "revision_conflict");
  assert.equal((await fixture.controller.submit(opened, { confirmed: true })).kind, "failed");
  assert.equal(fixture.writes.length, 1, "no automatic resend after a conflict");
  assert.equal(intent.payload?.expected_updated_at, baseline.updated_at, "the original edit is never rebased");
  const latest = { ...baseline, updated_at: "2026-10-28T00:01:00.000001Z" };
  fixture.setRows([latest]);
  const explicitNewEdit: ResourceActionIntent = { ...intent, row: latest, payload: { ...intent.payload, expected_updated_at: latest.updated_at } };
  const reopened = await fixture.controller.open(explicitNewEdit);
  assert.equal(reopened.kind, "allowed");
  if (reopened.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(reopened, { confirmed: true })).kind, "succeeded");
  assert.equal(fixture.writes.length, 2);
  assert.equal((fixture.writes[1].body as Record<string, unknown>).expected_updated_at, latest.updated_at);
});

test("P1-RESOURCE-POLL-016: a matching conflict code on DELETE is not treated as an atomic edit CAS rejection", async (t) => {
  const fixture = runtimeFixture(t);
  const intent: ResourceActionIntent = { id: "RES-06", row: oldRow(), publicLabel: "Existing BOT" };
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.rejectNext(409, "revision_conflict");
  assert.equal((await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken })).kind, "failed");
  await refreshResourceAction(fixture.client, intent);
  fixture.controller.reconcile();
  assert.deepEqual(await fixture.controller.open(intent), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(fixture.writes.length, 1);
});
