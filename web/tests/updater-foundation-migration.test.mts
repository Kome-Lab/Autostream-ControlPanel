import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
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
  buildUpdaterActionDescriptor,
  createUpdaterActionController,
  updaterActionDefinitions,
  updaterAuthorityFingerprint,
} = await import("../src/features/application/updater-action-policy.ts");

const fresh = (authorityFingerprint: string) => ({
  permission: "allowed" as const,
  freshness: "fresh" as const,
  applicability: "applicable" as const,
  authorityFingerprint,
});

test("UPD denominator is exact 10/10 with frozen routes, risk, confirmation, and audit", () => {
  assert.deepEqual(updaterActionDefinitions.map((entry) => entry.id), Array.from({ length: 10 }, (_, index) => `UPD-${String(index + 1).padStart(2, "0")}`));
  assert.deepEqual(updaterActionDefinitions.map((entry) => [entry.method, entry.route]), [
    ["POST", "/system-updates"],
    ["POST", "/system-updates"],
    ["SEQUENTIAL_POST", "/system-updates"],
    ["POST", "/system-updates/{id}/cancel"],
    ["POST", "/system-updates"],
    ["POST", "/system-updates/updaters/{id}/pull-ownership/activate"],
    ["POST", "/system-updates/updaters/{id}/pull-ownership/deactivate"],
    ["PUT", "/system-updates/updaters/{id}/settings"],
    ["POST", "/system-updates/updaters/{id}/bootstrap-jobs"],
    ["POST", "/system-updates/updaters/{id}/bootstrap-jobs"],
  ]);
  assert.deepEqual(updaterActionDefinitions.map((entry) => entry.risk), [
    "high", "critical", "critical", "high", "critical", "critical", "critical", "critical", "critical", "critical",
  ]);
  assert.deepEqual(updaterActionDefinitions.map((entry) => entry.confirmation), [
    "consequence", "typed-target", "fixed-phrase", "consequence", "typed-target", "typed-target", "fixed-phrase", "typed-target", "typed-target", "fixed-phrase",
  ]);
  assert.deepEqual(updaterActionDefinitions.filter((entry) => entry.fixedPhrase).map((entry) => [entry.id, entry.fixedPhrase]), [
    ["UPD-03", "UPDATE BATCH"],
    ["UPD-07", "BRIDGE ROLLBACK"],
    ["UPD-10", "BOOTSTRAP HOSTS"],
  ]);
  assert.deepEqual(updaterActionDefinitions.map((entry) => entry.auditAction), [
    "system_updates.create",
    "system_updates.create",
    "system_updates.create",
    "system_updates.cancel",
    "system_updates.create",
    "system_updates.pull_ownership.activate",
    "system_updates.pull_ownership.deactivate",
    "system_updates.updater_policy.save",
    "system_updates.bootstrap.create",
    "system_updates.bootstrap.create",
  ]);
});

test("Updater descriptors encode exact typed targets, fixed phrases, permission, and lookup-only retry", () => {
  const cases = [
    { id: "UPD-01", resourceId: "worker-main", authorityFingerprint: "fp-1" },
    { id: "UPD-02", resourceId: "control-panel", publicLabel: "Control Panel", authorityFingerprint: "fp-2" },
    { id: "UPD-03", resourceId: "fleet", authorityFingerprint: "fp-3" },
    { id: "UPD-04", resourceId: "job-1", authorityFingerprint: "fp-4" },
    { id: "UPD-05", resourceId: "worker-main", publicLabel: "worker-main", authorityFingerprint: "fp-5" },
    { id: "UPD-06", resourceId: "updater-main", publicLabel: "Updater Main", authorityFingerprint: "fp-6" },
    { id: "UPD-07", resourceId: "updater-main", authorityFingerprint: "fp-7" },
    { id: "UPD-08", resourceId: "updater-main", publicLabel: "Updater Main", authorityFingerprint: "fp-8" },
    { id: "UPD-09", resourceId: "host-main", publicLabel: "host-main", authorityFingerprint: "fp-9" },
    { id: "UPD-10", resourceId: "updater-main", authorityFingerprint: "fp-10" },
  ] as const;
  for (const intent of cases) {
    const descriptor = buildUpdaterActionDescriptor(intent);
    assert.ok(descriptor, intent.id);
    assert.deepEqual(descriptor.permissions, { kind: "all", permissions: ["system_updates.execute"] }, intent.id);
    assert.deepEqual(descriptor.retry, { kind: "lookup-only" }, intent.id);
    assert.deepEqual(descriptor.revalidation, { kind: "safe-fingerprint", fieldIds: ["authorityFingerprint"] }, intent.id);
    assert.equal(descriptor.confirmation.requireSubmitRevalidation, true, intent.id);
  }
  assert.equal(buildUpdaterActionDescriptor({ id: "UPD-02", resourceId: "control-panel", publicLabel: "https://unsafe.invalid", authorityFingerprint: "fp" }), undefined);
  assert.equal(buildUpdaterActionDescriptor({ id: "UPD-09", resourceId: "host-main", publicLabel: "operator@example.invalid", authorityFingerprint: "fp" }), undefined);
});

test("Updater controller rejects stale authority and typed mismatch before mutation", async () => {
  const controller = createUpdaterActionController();
  const intent = { id: "UPD-08" as const, resourceId: "updater-main", publicLabel: "Updater Main", authorityFingerprint: "revision-7" };
  let refreshes = 0;
  let mutations = 0;
  assert.deepEqual(await controller.execute(intent, { confirmed: true, typedValue: "wrong" }, async () => {
    refreshes += 1;
    return fresh("revision-7");
  }, async () => { mutations += 1; }), { kind: "blocked", reason: "typed-target-mismatch" });
  assert.equal(refreshes, 0);
  assert.equal(mutations, 0);

  assert.deepEqual(await controller.execute(intent, { confirmed: true, typedValue: "Updater Main" }, async () => {
    refreshes += 1;
    return fresh("revision-8");
  }, async () => { mutations += 1; }), { kind: "blocked", reason: "authority-changed" });
  assert.equal(refreshes, 1);
  assert.equal(mutations, 0);
});

test("Updater per-target and fleet locks latch before refresh and block duplicates", async () => {
  const controller = createUpdaterActionController();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let mutations = 0;
  const targetIntent = { id: "UPD-01" as const, resourceId: "worker-main", authorityFingerprint: "target-fp" };
  const first = controller.execute(targetIntent, { confirmed: true }, async () => {
    await gate;
    return fresh("target-fp");
  }, async () => { mutations += 1; return "accepted"; });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(await controller.execute(targetIntent, { confirmed: true }, async () => fresh("target-fp"), async () => { mutations += 1; }), { kind: "blocked", reason: "duplicate" });

  const batchIntent = { id: "UPD-03" as const, resourceId: "fleet", authorityFingerprint: "fleet-fp" };
  let releaseFleet!: () => void;
  const fleetGate = new Promise<void>((resolve) => { releaseFleet = resolve; });
  const fleet = controller.execute(batchIntent, { confirmed: true, typedValue: "UPDATE BATCH" }, async () => {
    await fleetGate;
    return fresh("fleet-fp");
  }, async () => "accepted");
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(await controller.execute(batchIntent, { confirmed: true, typedValue: "UPDATE BATCH" }, async () => fresh("fleet-fp"), async () => "duplicate"), { kind: "blocked", reason: "duplicate" });
  release();
  releaseFleet();
  assert.equal((await first).kind, "succeeded");
  assert.equal((await fleet).kind, "succeeded");
  assert.equal(mutations, 1);
});

test("Updater response loss requires lookup reconciliation and never blindly resends", async () => {
  const controller = createUpdaterActionController();
  const intent = { id: "UPD-09" as const, resourceId: "host-main", publicLabel: "host-main", authorityFingerprint: "bootstrap-r9" };
  let posts = 0;
  const first = await controller.execute(intent, { confirmed: true, typedValue: "host-main" }, async () => fresh("bootstrap-r9"), async () => {
    posts += 1;
    throw new TypeError("RAW_BOOTSTRAP_RESPONSE_LOST");
  });
  assert.deepEqual(first, { kind: "outcome_unknown", nextAction: "lookup-operation" });
  assert.deepEqual(await controller.execute(intent, { confirmed: true, typedValue: "host-main" }, async () => fresh("bootstrap-r9"), async () => { posts += 1; }), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(posts, 1);
  controller.reconcile(intent);
  assert.equal((await controller.execute(intent, { confirmed: true, typedValue: "host-main" }, async () => fresh("bootstrap-r9"), async () => { posts += 1; return "accepted"; })).kind, "succeeded");
  assert.equal(posts, 2);
});

test("Updater authority fingerprint is stable, bounded, and changes with revision", () => {
  const left = updaterAuthorityFingerprint(["UPD-08", "updater-main", 7, true]);
  const same = updaterAuthorityFingerprint(["UPD-08", "updater-main", 7, true]);
  const changed = updaterAuthorityFingerprint(["UPD-08", "updater-main", 8, true]);
  assert.equal(left, same);
  assert.notEqual(left, changed);
  assert.match(left, /^uaf1-[0-9a-f]{16}-\d+$/);
  assert.ok(left.length < 64);
});

test("Updater UI wraps Bundle 5 controllers without embedded fallback or raw response rendering", () => {
  const application = readFileSync(new URL("../src/features/application/application-info-view.tsx", import.meta.url), "utf8");
  const settings = readFileSync(new URL("../src/features/application/updater-settings-panel.tsx", import.meta.url), "utf8");
  const bootstrap = readFileSync(new URL("../src/features/application/updater-host-bootstrap-panel.tsx", import.meta.url), "utf8");
  const source = `${application}\n${settings}\n${bootstrap}`;

  for (const id of Array.from({ length: 10 }, (_, index) => `UPD-${String(index + 1).padStart(2, "0")}`)) {
    assert.match(source, new RegExp(id.replace("-", "\\-")), id);
  }
  assert.match(application, /requestSystemUpdateWithRecovery/);
  assert.match(application, /requestSystemUpdatePortReconfigureWithRecovery/);
  assert.match(settings, /pullUpdaterOwnershipActivationRequest/);
  assert.match(settings, /pullUpdaterOwnershipDeactivationRequest/);
  assert.match(settings, /expected_revision/);
  assert.match(bootstrap, /encryptBootstrapCredentials/);
  assert.match(bootstrap, /clearBootstrapRequestEnvelope/);
  assert.match(bootstrap, /requestUpdaterHostBootstrapWithRecovery/);
  assert.doesNotMatch(application, /lines\.push\(`code:|String\(job\.message/);
  assert.doesNotMatch(bootstrap, /\{result\.message\}/);
  assert.doesNotMatch(source, /embedded[_ -]executor|direct[_ -]host[_ -](?:shell|fallback)/i);
});
