import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";

const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");
register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

const {
  accountActionDefinitions,
  buildAccountActionDescriptor,
  createAccountActionController,
} = await import("../src/features/account/account-action-policy.ts");
const {
  archiveActionDefinitions,
  buildArchiveActionDescriptor,
  createArchiveActionController,
} = await import("../src/features/archive/archive-action-policy.ts");
const { adoptAccountOneTimeOutput } = await import("../src/features/account/account-one-time-secret.ts");
const { adoptArchiveShareCapability } = await import("../src/features/archive/archive-share-capability.ts");
const { createOneTimeSecretLifecycleOwner } = await import("../src/lib/foundation/secrets/lifecycle-owner.ts");

test("AUTH denominator is exact 21/21 with preserved routes and risk split", () => {
  assert.deepEqual(accountActionDefinitions.map((entry) => entry.id), Array.from({ length: 21 }, (_, index) => `AUTH-${String(index + 1).padStart(2, "0")}`));
  assert.deepEqual(accountActionDefinitions.map((entry) => `${entry.method} ${entry.route}`), [
    "POST /setup/first-admin",
    "POST /auth/login",
    "POST /auth/mfa/verify",
    "POST /auth/oauth/{provider}/start",
    "POST /auth/passkeys/login/start",
    "POST /auth/passkeys/login/finish",
    "POST /auth/email/confirm",
    "POST /auth/logout",
    "PUT_BINARY /auth/avatar",
    "DELETE /auth/avatar",
    "POST /auth/change-password",
    "PUT /auth/email",
    "POST /auth/oauth-links/{provider}/start",
    "DELETE /auth/oauth-links/{link}",
    "POST /auth/mfa/enroll",
    "POST /auth/mfa/verify",
    "POST /auth/mfa/disable",
    "POST /auth/recovery-codes/regenerate",
    "POST /auth/passkeys/register/start",
    "POST /auth/passkeys/register/finish",
    "DELETE /auth/passkeys/{id}",
  ]);
  assert.deepEqual(accountActionDefinitions.filter((entry) => entry.risk === "critical").map((entry) => entry.id), ["AUTH-01", "AUTH-15", "AUTH-17", "AUTH-18"]);
  assert.deepEqual(accountActionDefinitions.filter((entry) => entry.oneTimeOutput).map((entry) => entry.id), ["AUTH-15", "AUTH-18"]);
  assert.deepEqual(accountActionDefinitions.filter((entry) => entry.flowContinuationOf).map((entry) => [entry.id, entry.flowContinuationOf]), [
    ["AUTH-03", "AUTH-02"],
    ["AUTH-06", "AUTH-05"],
    ["AUTH-16", "AUTH-15"],
    ["AUTH-20", "AUTH-19"],
  ]);
});

test("critical Account descriptors use only a safe public username", () => {
  for (const id of ["AUTH-01", "AUTH-15", "AUTH-17", "AUTH-18"]) {
    const descriptor = buildAccountActionDescriptor({ id, resourceId: "account-1", publicUsername: "operator" });
    assert.equal(descriptor?.confirmation.mode, "typed-target", id);
    assert.equal(descriptor?.target.publicLabel, "operator", id);
  }
  assert.equal(buildAccountActionDescriptor({ id: "AUTH-17", resourceId: "account-1", publicUsername: "operator@example.invalid" }), undefined);
  assert.equal(buildAccountActionDescriptor({ id: "AUTH-18", resourceId: "account-1", publicUsername: "otpauth://secret" }), undefined);
});

test("Account coordinator latches before handler, revalidates session, and never resends ambiguity", async () => {
  let session: "authenticated" | "anonymous" = "authenticated";
  let calls = 0;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const controller = createAccountActionController({ readAuthority: () => ({ session, freshness: "fresh", revision: "session-1" }) });
  const intent = { id: "AUTH-17", resourceId: "account-1", publicUsername: "operator" };
  const first = controller.execute(intent, { confirmed: true, typedValue: "operator" }, async () => {
    calls += 1;
    await gate;
    throw new TypeError("RAW response lost marker");
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(await controller.execute(intent, { confirmed: true, typedValue: "operator" }, async () => { calls += 1; }), { kind: "blocked", reason: "duplicate" });
  assert.equal(calls, 1);
  release();
  assert.deepEqual(await first, { kind: "outcome_unknown", nextAction: "inspect-audit" });
  assert.deepEqual(await controller.execute(intent, { confirmed: true, typedValue: "operator" }, async () => { calls += 1; }), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(calls, 1);
  controller.reconcile(intent);
  session = "anonymous";
  assert.deepEqual(await controller.execute(intent, { confirmed: true, typedValue: "operator" }, async () => { calls += 1; }), { kind: "blocked", reason: "session-unavailable" });
  assert.equal(calls, 1);
});

test("ARC denominator is exact 5/5 including authenticated download handoff", () => {
  assert.deepEqual(archiveActionDefinitions.map((entry) => entry.id), ["ARC-01", "ARC-02", "ARC-03", "ARC-04", "ARC-05"]);
  assert.deepEqual(archiveActionDefinitions.map((entry) => [entry.method, entry.route, entry.risk, entry.confirmation]), [
    ["PUT", "/streams/{sid}/artifacts/{aid}", "guarded", "consequence"],
    ["DELETE", "/streams/{sid}/artifacts/{aid}", "critical", "typed-target"],
    ["POST", "/streams/{sid}/artifacts/{aid}/shares", "high", "consequence"],
    ["DELETE", "/streams/{sid}/artifacts/{aid}/shares/{share}", "high", "consequence"],
    ["GET_HANDOFF", "/streams/{sid}/artifacts/{aid}/download", "routine", "none"],
  ]);
  assert.equal(buildArchiveActionDescriptor({ id: "ARC-02", streamId: "stream-1", artifactId: "artifact-1", artifactLabel: "Recording A" })?.target.publicLabel, "Recording A");
});

test("Archive coordinator blocks stale/duplicate intent and describes download as handoff only", async () => {
  let revision = "artifact-r1";
  let calls = 0;
  const controller = createArchiveActionController({
    readAuthority: () => ({ permission: "allowed", freshness: "fresh", applicability: "applicable", revision }),
  });
  const intent = { id: "ARC-02", streamId: "stream-1", artifactId: "artifact-1", artifactLabel: "Recording A" };
  const opened = controller.open(intent);
  assert.equal(opened.kind, "allowed");
  revision = "artifact-r2";
  if (opened.kind === "allowed") {
    assert.deepEqual(await controller.submit(opened, { confirmed: true, typedValue: "Recording A" }, async () => { calls += 1; }), { kind: "blocked", reason: "authority-changed" });
  }
  assert.equal(calls, 0);
  assert.deepEqual(controller.downloadHandoff({ ...intent, id: "ARC-05" }), {
    kind: "handoff-ready",
    message: "browser-download-handoff-started",
  });
});

test("MFA secret, provisioning URI and recovery codes enter only the concealed lifecycle owner", () => {
  const owner = createOneTimeSecretLifecycleOwner(runtime());
  const result = adoptAccountOneTimeOutput(owner, {
    method: "totp",
    secret: "MFA-SECRET-MARKER",
    provisioning_uri: "otpauth://totp/MFA-PROVISIONING-MARKER",
    recovery_codes: ["RECOVERY-MARKER-1", "RECOVERY-MARKER-2"],
  });
  assert.equal(result.adopted, true);
  assert.equal(owner.getSnapshot().phase, "concealed");
  assert.equal(JSON.stringify(result.publicResult).includes("MARKER"), false);
  assert.equal(owner.readRevealedValue(), undefined);
  owner.reveal();
  assert.match(JSON.stringify(owner.readRevealedValue()), /MFA-SECRET-MARKER/);
  owner.clearForSessionLoss();
  assert.equal(owner.readRevealedValue(), undefined);
});

test("Archive share URL/token capability is absent from public state and obeys server expiry", () => {
  const clock = runtime();
  const owner = createOneTimeSecretLifecycleOwner(clock);
  const result = adoptArchiveShareCapability(owner, {
    id: "share-1",
    url: "https://control.example.invalid/archive/share/?token=SHARE-CAPABILITY-MARKER",
    expires_at: new Date(clock.now() + 90_000).toISOString(),
    allow_download: true,
  }, "https://control.example.invalid");
  assert.equal(result.adopted, true);
  assert.equal(owner.getSnapshot().phase, "concealed");
  assert.equal(JSON.stringify(result.publicResult).includes("MARKER"), false);
  clock.advance(30_000);
  assert.equal(owner.getSnapshot().warningActive, true);
  clock.advance(60_000);
  assert.equal(owner.getSnapshot().clearReason, "expired");
  assert.equal(owner.readRevealedValue(), undefined);
});

function runtime() {
  let now = 1_000;
  let nextTimer = 0;
  const timers = new Map<number, { callback: () => void; due: number }>();
  return {
    epochNowMs: () => now,
    monotonicNowMs: () => now,
    schedule: (callback: () => void, delayMs: number) => {
      nextTimer += 1;
      timers.set(nextTimer, { callback, due: now + delayMs });
      return nextTimer;
    },
    cancel: (handle: unknown) => { timers.delete(handle as number); },
    now: () => now,
    advance: (milliseconds: number) => {
      now += milliseconds;
      for (const [id, timer] of [...timers]) {
        if (timer.due <= now) {
          timers.delete(id);
          timer.callback();
        }
      }
    },
  };
}
