import assert from "node:assert/strict";
import test from "node:test";
import type { ResourceActionIntent } from "../src/features/resources/resource-action-descriptors.ts";
import { refreshResourceAction, resourceSecurityMetadata, runtimeFixture } from "./helpers/resource-polling-fixture.mts";

const originalStamp = "2026-10-28T00:00:00.123457Z";
const newerStamp = "2026-10-28T00:00:00.123458Z";
const settings = (updatedAt = originalStamp) => ({
  password_min_length: 12, login_lockout_threshold: 5, session_idle_timeout_min: 30,
  session_absolute_lifetime_h: 12, remember_me_enabled: false, mfa_mode: "totp", mfa_required_roles: ["admin", "super_admin"],
  updated_at: updatedAt,
});
const edit = (updatedAt = originalStamp): ResourceActionIntent => ({
  id: "RES-40", row: { ...resourceSecurityMetadata(settings(updatedAt)), updated_at: updatedAt },
  payload: { ...resourceSecurityMetadata(settings(updatedAt)), password_min_length: 16, expected_updated_at: updatedAt },
  publicLabel: "Security policy",
});

test("P1-RESOURCE-SECURITY-CAS-001: an actual revision advance before the first Save never adopts a newer security baseline", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", settings(newerStamp)), intent = edit();
  const opened = await fixture.controller.open(intent);
  const result = opened.kind === "allowed" ? await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken }) : opened;
  assert.deepEqual(result, { kind: "blocked", reason: "authority-changed" });
  assert.equal(intent.row?.updated_at, originalStamp);
  assert.equal(intent.payload?.expected_updated_at, originalStamp);
  assert.equal(intent.payload?.password_min_length, 16);
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-SECURITY-CAS-002: a revision advance during confirmation stops dispatch even if visible settings compare equal", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", settings()), intent = edit();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.setData(settings(newerStamp));
  assert.deepEqual(await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken }), { kind: "blocked", reason: "authority-changed" });
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-SECURITY-CAS-003: same-revision polls retain the exact microsecond baseline and permit one explicit Save", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", settings()), intent = edit();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  for (let cycle = 0; cycle < 3; cycle++) {
    fixture.setData({ ...settings(), observed_at: cycle, mfa_required_roles: ["super_admin", "admin"] });
    await refreshResourceAction(fixture.client, intent);
  }
  assert.equal((await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken })).kind, "succeeded");
  assert.equal(fixture.writes.length, 1);
  assert.deepEqual(fixture.writes[0], { method: "PUT", path: "/security/settings", body: intent.payload });
  assert.equal((fixture.writes[0].body as Record<string, unknown>).expected_updated_at, originalStamp);
});

test("P1-RESOURCE-SECURITY-CAS-004: an unconfigured security row retains the server's exact creation sentinel", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", settings("unconfigured")), intent = edit("unconfigured");
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken })).kind, "succeeded");
  assert.equal((fixture.writes[0].body as Record<string, unknown>).expected_updated_at, "unconfigured");
});

test("P1-RESOURCE-SECURITY-CAS-005: a current response without the required CAS timestamp cannot authorize the UI edit", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", { ...resourceSecurityMetadata(settings()) });
  assert.deepEqual(await fixture.controller.open(edit()), { kind: "blocked", reason: "state-unavailable" });
  assert.equal(fixture.writes.length, 0);
});

test("P1-RESOURCE-SECURITY-CAS-006: a definitive atomic conflict allows an explicit new edit while the rejected draft keeps its original timestamp", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", settings()), intent = edit();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.rejectNext(409, "revision_conflict");
  assert.equal((await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken })).kind, "failed");
  assert.equal(fixture.writes.length, 1, "the CAS rejection never causes an automatic resend");
  assert.equal(intent.payload?.expected_updated_at, originalStamp);
  fixture.setData(settings(newerStamp));
  const reopened = await fixture.controller.open(edit(newerStamp));
  assert.equal(reopened.kind, "allowed");
  if (reopened.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(reopened, { confirmed: true, typedValue: reopened.typedToken })).kind, "succeeded");
  assert.equal(fixture.writes.length, 2);
  assert.equal((fixture.writes[1].body as Record<string, unknown>).expected_updated_at, newerStamp);
});

test("P1-RESOURCE-SECURITY-CAS-007: an uncertain server failure remains latched after a successful settings GET", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", settings()), intent = edit();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.rejectNext(500, "save_security_settings_failed");
  const result = await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken });
  assert.ok(result.kind === "failed" || result.kind === "outcome_unknown");
  await refreshResourceAction(fixture.client, intent);
  fixture.controller.reconcile();
  assert.deepEqual(await fixture.controller.open(intent), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(fixture.writes.length, 1);
});

test("P1-RESOURCE-SECURITY-CAS-008: the atomic conflict exception does not make every security 400 retryable", async (t) => {
  const fixture = runtimeFixture(t, "/security/settings", settings()), intent = edit();
  const opened = await fixture.controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  fixture.rejectNext(400, "bad_request");
  assert.equal((await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken })).kind, "failed");
  await refreshResourceAction(fixture.client, intent);
  fixture.controller.reconcile();
  assert.deepEqual(await fixture.controller.open(intent), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(fixture.writes.length, 1);
});
