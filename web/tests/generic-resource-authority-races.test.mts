import assert from "node:assert/strict";
import test from "node:test";
import type { ResourceActionIntent } from "../src/features/resources/resource-action-descriptors.ts";
import { createIntent, oldRow, refreshResourceAction, resourceSecurityMetadata, runtimeFixture } from "./helpers/resource-polling-fixture.mts";

const security = () => ({
  password_min_length: 12, login_lockout_threshold: 5, session_idle_timeout_min: 30,
  session_absolute_lifetime_h: 12, remember_me_enabled: false, mfa_mode: "totp", mfa_required_roles: ["admin", "super_admin"],
});
const securityUpdatedAt = "2026-10-28T00:00:00.123457Z";

test("P1-RESOURCE-POLL-010: a real security setting change before the first Save retains the original baseline and draft without a PUT", async (t) => {
  const baseline = { ...resourceSecurityMetadata(security()), updated_at: securityUpdatedAt };
  const fixture = runtimeFixture(t, "/security/settings", { ...security(), updated_at: securityUpdatedAt });
  const intent: ResourceActionIntent = { id: "RES-40", row: baseline, payload: { ...security(), password_min_length: 16 }, publicLabel: "Security policy" };
  fixture.setData({ ...security(), session_idle_timeout_min: 15, updated_at: securityUpdatedAt });
  const opened = await fixture.controller.open(intent);
  const result = opened.kind === "allowed" ? await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken }) : opened;
  assert.deepEqual(result, { kind: "blocked", reason: "authority-changed" });
  assert.deepEqual(intent.row, baseline);
  assert.equal(intent.payload?.password_min_length, 16);
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-POLL-011: security metadata ignores observation timestamps and role ordering while the explicit Save remains one PUT", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", { ...security(), updated_at: securityUpdatedAt });
  const baseline = resourceSecurityMetadata({ ...security(), observed_at: "before", private_value: "never copied" });
  assert.deepEqual(Object.keys(baseline ?? {}).sort(), Object.keys(security()).sort());
  const intent: ResourceActionIntent = { id: "RES-40", row: { ...baseline, updated_at: securityUpdatedAt }, payload: { ...security(), password_min_length: 16 }, publicLabel: "Security policy" };
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.setData({ ...security(), mfa_required_roles: ["super_admin", "admin"], updated_at: securityUpdatedAt, observed_at: "after" });
  assert.equal((await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken })).kind, "succeeded");
  assert.deepEqual(fixture.writes, [{ method: "PUT", path: "/security/settings", body: intent.payload }]);
});

test("P1-RESOURCE-POLL-012: an older direct GET cannot restore a row after another intent deletes it and a newer read settles", { timeout: 15_000 }, async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  const hold = fixture.holdNextRead();
  const oldOpen = fixture.controller.open(intent);
  try {
    await Promise.race([hold.started, oldOpen.then(() => assert.fail("the controlled resource GET must start before open completes"))]);
    const deletion = await fixture.controller.open({ id: "RES-06", row: oldRow(), publicLabel: "Existing BOT" });
    assert.equal(deletion.kind, "allowed");
    if (deletion.kind !== "allowed") return;
    assert.equal((await fixture.controller.submit(deletion, { confirmed: true, typedValue: deletion.typedToken })).kind, "succeeded");
    await refreshResourceAction(fixture.client, intent);
  } finally { hold.release(); }
  await oldOpen;
  assert.deepEqual(fixture.client.getQueryData(["resource", fixture.path]), { items: [] });
  assert.deepEqual(fixture.writes, [{ method: "DELETE", path: `${fixture.path}/${oldRow().id}`, body: undefined }]);
});

test("P1-RESOURCE-POLL-013: cancelling fresh submit validation aborts its GET signal and dispatches no mutation even when the reader ignores abort", { timeout: 15_000 }, async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  const hold = fixture.holdNextRead();
  const submission = fixture.controller.submit(opened, { confirmed: true });
  try {
    await Promise.race([hold.started, submission.then(() => assert.fail("fresh submit validation must start its controlled GET"))]);
    fixture.controller.cancel(intent);
    assert.deepEqual(await submission, { kind: "blocked", reason: "cancelled" });
    assert.equal(hold.signal?.aborted, true);
  } finally { hold.release(); }
  assert.equal(fixture.writes.length, 0);
  assert.equal((await fixture.controller.open(intent)).kind, "allowed", "a cancelled read never dispatched and needs no unknown-write latch");
});

test("P1-RESOURCE-POLL-014: cancellation after dispatch retains the unknown-write latch despite a later successful response", { timeout: 15_000 }, async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  const hold = fixture.holdNextWriteResponse();
  const submission = fixture.controller.submit(opened, { confirmed: true });
  try {
    await Promise.race([hold.started, submission.then(() => assert.fail("the mutation must dispatch before it is cancelled"))]);
    fixture.controller.cancel(intent);
  } finally { hold.release(); }
  assert.deepEqual(await submission, { kind: "blocked", reason: "cancelled" });
  assert.equal(hold.signal?.aborted, true);
  await refreshResourceAction(fixture.client, intent);
  fixture.controller.reconcile();
  assert.deepEqual(await fixture.controller.open(intent), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(fixture.writes.length, 1);
});

test("P1-RESOURCE-POLL-017: mutation completion fences an overlapping direct GET even when only a managed query supplies the newer cache", { timeout: 15_000 }, async (t) => {
  const fixture = runtimeFixture(t), intent = createIntent();
  const deletion = await fixture.controller.open({ id: "RES-06", row: oldRow(), publicLabel: "Existing BOT" });
  assert.equal(deletion.kind, "allowed");
  if (deletion.kind !== "allowed") return;
  const write = fixture.holdNextWriteResponse();
  const submission = fixture.controller.submit(deletion, { confirmed: true, typedValue: deletion.typedToken });
  let staleRead: ReturnType<typeof refreshResourceAction> | undefined;
  let read: ReturnType<typeof fixture.holdNextRead> | undefined;
  try {
    await Promise.race([write.started, submission.then(() => assert.fail("the mutation must dispatch before the overlapping GET"))]);
    read = fixture.holdNextRead();
    staleRead = refreshResourceAction(fixture.client, intent);
    await Promise.race([read.started, staleRead.then(() => assert.fail("the overlapping direct GET must be held"))]);
    write.release();
    assert.equal((await submission).kind, "succeeded");
    await fixture.readManagedResource();
    assert.deepEqual(fixture.client.getQueryData(["resource", fixture.path]), { items: [] });
  } finally { write.release(); read?.release(); }
  await staleRead;
  assert.deepEqual(fixture.client.getQueryData(["resource", fixture.path]), { items: [] });
  assert.deepEqual(fixture.writes, [{ method: "DELETE", path: `${fixture.path}/${oldRow().id}`, body: undefined }]);
});
