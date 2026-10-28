import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { actualJSXCallback } from "./ui-regression/source-callback.mts";
import type { ResourceActionIntent } from "../src/features/resources/resource-action-descriptors.ts";
import { resourceSecurityMetadata, runtimeFixture } from "./helpers/resource-polling-fixture.mts";

const source = () => readFileSync(new URL("../src/features/resources/resource-security-editor.tsx", import.meta.url), "utf8");
const stamp = "2026-10-28T00:00:00.123457Z";
const baseline = { password_min_length: 12, mfa_mode: "totp", mfa_required_roles: ["admin"] };

function formFixture(updatedAt: string | undefined = stamp) {
  let pending: ResourceActionIntent | null = null, prevented = 0;
  const submittedSnapshots = { current: new WeakMap<ResourceActionIntent, string>() };
  const bindings = {
    passwordMinLength: "16", loginLockoutThreshold: "5", sessionIdleTimeout: "30", sessionAbsoluteLifetime: "12",
    mfaMode: "totp", mfaRequiredRoles: ["admin"], numberValue: Number, baseline: baseline as Record<string, unknown> | undefined, baselineUpdatedAt: updatedAt,
    setMessage() {}, setPending(value: ResourceActionIntent | null) { pending = value; }, submittedSnapshots, draftSnapshot: "submitted draft",
  };
  return {
    bindings, submittedSnapshots,
    submit: () => actualJSXCallback(source(), "form", "onSubmit", bindings)({ preventDefault() { prevented++; } }),
    get pending() { return pending; }, get prevented() { return prevented; },
  };
}

test("P1-RESOURCE-SECURITY-DRAFT-001: the actual submit callback binds the original microsecond timestamp and submitted nonsecret draft to its own intent", () => {
  const fixture = formFixture();
  fixture.submit();
  assert.equal(fixture.prevented, 1);
  const intent = fixture.pending;
  assert.ok(intent);
  assert.equal(intent.row?.updated_at, stamp);
  assert.equal(intent.payload?.expected_updated_at, stamp);
  assert.equal(fixture.submittedSnapshots.current.get(intent), "submitted draft");
  assert.equal(Object.isFrozen(intent), true);
  assert.equal(Object.isFrozen(intent.payload), true);
});

test("P1-RESOURCE-SECURITY-DRAFT-002: a missing original GET timestamp retains inputs and never opens a save intent", () => {
  const fixture = formFixture("");
  fixture.submit();
  assert.equal(fixture.pending, null);
  assert.equal(fixture.bindings.passwordMinLength, "16");
  assert.deepEqual(fixture.bindings.mfaRequiredRoles, ["admin"]);
});

function resultFixture(text = source(), submission?: ReturnType<typeof formFixture>) {
  let saved = "before", pending: unknown = {}, invalidations = 0;
  const intent: ResourceActionIntent = submission?.pending ?? { id: "RES-40", payload: { expected_updated_at: stamp } };
  const submittedSnapshots = submission?.submittedSnapshots ?? { current: new WeakMap<ResourceActionIntent, string>([[intent, "submitted draft"]]) };
  const bindings = {
    setMessage() {}, resourceActionResultMessage: () => "safe", t: (key: string) => key, uiText: (key: string) => key,
    setSavedSnapshot(value: string) { saved = value; }, draftSnapshot: "later edit", submittedSnapshots,
    setPending(value: unknown) { pending = value; }, resource: { path: "/security/settings" },
    queryClient: { invalidateQueries() { invalidations++; } },
    isRecord: (value: unknown) => typeof value === "object" && value !== null && !Array.isArray(value), resourceSecurityMetadata,
    setBaseline(value: Record<string, unknown> | undefined) { if (submission) submission.bindings.baseline = value; },
    setBaselineUpdatedAt(value: string) { if (submission) submission.bindings.baselineUpdatedAt = value; },
  };
  return {
    intent, result: actualJSXCallback(text, "ResourceActionConfirmationHost", "onResult", bindings),
    get saved() { return saved; }, get pending() { return pending; }, get invalidations() { return invalidations; },
  };
}

test("P1-RESOURCE-SECURITY-DRAFT-003: actual delayed success acknowledges only the submitted snapshot and leaves later input dirty; the old current-snapshot mutation is rejected", () => {
  const current = source(), fixture = resultFixture(current);
  fixture.result({ kind: "succeeded" }, fixture.intent);
  assert.equal(fixture.saved, "submitted draft");
  assert.notEqual(fixture.saved, "later edit", "the post-submit edit remains dirty");
  assert.equal(fixture.pending, null);
  assert.equal(fixture.invalidations, 2);
  const mutant = current.replace("setSavedSnapshot(submittedSnapshot);", "setSavedSnapshot(draftSnapshot);");
  assert.notEqual(mutant, current, "the mutation must replace the actual success acknowledgement");
  const mutated = resultFixture(mutant);
  mutated.result({ kind: "succeeded" }, mutated.intent);
  assert.throws(() => assert.equal(mutated.saved, "submitted draft"), /later edit/);
});

test("P1-RESOURCE-SECURITY-DRAFT-004: failure, unknown, mismatched and duplicate receipts cannot acknowledge another Security draft", () => {
  const fixture = resultFixture();
  for (const kind of ["blocked", "failed", "outcome_unknown"]) {
    fixture.result({ kind }, fixture.intent);
    assert.equal(fixture.saved, "before");
    assert.equal(fixture.invalidations, 0);
  }
  fixture.result({ kind: "succeeded" }, { ...fixture.intent });
  assert.equal(fixture.saved, "before");
  assert.equal(fixture.invalidations, 0);
  fixture.result({ kind: "succeeded" }, fixture.intent);
  assert.equal(fixture.saved, "submitted draft");
  assert.equal(fixture.invalidations, 2);
  fixture.result({ kind: "succeeded" }, fixture.intent);
  assert.equal(fixture.invalidations, 2);
});

test("P1-RESOURCE-SECURITY-DRAFT-005: the authoritative successful receipt advances only the saved baseline so later input can be explicitly saved with the new timestamp", async (t) => {
  const submission = formFixture();
  submission.submit();
  const first = submission.pending;
  assert.ok(first);
  const result = resultFixture(source(), submission);
  submission.bindings.passwordMinLength = "20";
  submission.bindings.draftSnapshot = "later edit";
  const savedStamp = "2026-10-28T00:00:00.123458Z";
  const receipt = { ...resourceSecurityMetadata(first.payload), updated_at: savedStamp, observed_at: "not baseline metadata" };
  result.result({ kind: "succeeded", value: receipt }, first);
  assert.equal(result.saved, "submitted draft");
  assert.notEqual(result.saved, submission.bindings.draftSnapshot);
  assert.equal(submission.bindings.passwordMinLength, "20", "the success handler never resets the later field value");
  assert.equal(submission.bindings.baseline?.password_min_length, 16, "the new comparison baseline comes from the applied submission, not the later draft");
  assert.equal(submission.bindings.baseline?.observed_at, undefined);
  submission.submit();
  const second = submission.pending;
  assert.ok(second);
  assert.notEqual(second, first);
  assert.equal(second.payload?.password_min_length, 20);
  assert.equal(second.payload?.expected_updated_at, savedStamp);
  assert.equal(second.row?.updated_at, savedStamp);
  assert.equal(second.row?.password_min_length, 16);
  assert.equal(submission.submittedSnapshots.current.get(second), "later edit");
  const fixture = runtimeFixture(t, "/security/settings", receipt);
  const opened = await fixture.controller.open(second);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await fixture.controller.submit(opened, { confirmed: true, typedValue: opened.typedToken })).kind, "succeeded");
  assert.deepEqual(fixture.writes, [{ method: "PUT", path: "/security/settings", body: second.payload }]);
});
