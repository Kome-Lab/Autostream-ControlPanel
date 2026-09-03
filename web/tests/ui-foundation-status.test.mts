import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";
import ts from "typescript";

import { assertStatusFoundationBoundaries, statusProductionConsumers } from "./helpers/ui-foundation-status-imports.mts";
import {
  compareStatusMappingInventory,
  deriveStatusAuthorityDomains,
  readStatusAuthority,
  verifyStatusAuthoritySources,
} from "./helpers/ui-foundation-status-authority.mts";

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

const { summarizeStatusCoverage } = await import("../src/lib/foundation/status/aggregate.ts");
const { copyCanonicalDomainStatusPresentation } = await import("../src/lib/foundation/status/presenter-core.ts");
const {
  presentArchiveProcessingStatus,
  presentArchiveShareStatus,
  presentRecordingLifecycleStatus,
  presentStreamLifecycleStatus,
} = await import("../src/lib/foundation/status/lifecycle-presenters.ts");
const {
  presentNodeConnectivityStatus,
  presentNodeHealthStatus,
  presentNodeOwnershipStatus,
  summarizePositiveNodeHealth,
} = await import("../src/lib/foundation/status/node-presenters.ts");
const {
  presentSystemUpdateJobStatus,
  presentSystemUpdatePolicyStatus,
  presentSystemUpdateTargetStatus,
} = await import("../src/lib/foundation/status/system-update-presenters.ts");
const {
  presentAuditResultStatus,
  presentDiagnosticStatus,
  presentIncidentStatus,
  presentRemediationStatus,
} = await import("../src/lib/foundation/status/observability-presenters.ts");
const { DomainStatusBadge } = await import("../src/components/foundation/status/domain-status-badge.ts");
const { translate } = await import("../src/lib/i18n.ts");

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const fixture = JSON.parse(readFileSync(join(webRoot, "tests", "fixtures", "ui-foundation-status-mappings.json"), "utf8"));
const authority = readStatusAuthority(webRoot);
const presenters = Object.freeze({
  "stream-lifecycle": presentStreamLifecycleStatus,
  "recording-lifecycle": presentRecordingLifecycleStatus,
  "node-connectivity": presentNodeConnectivityStatus,
  "node-health": presentNodeHealthStatus,
  "node-ownership": presentNodeOwnershipStatus,
  "system-update-target": presentSystemUpdateTargetStatus,
  "system-update-job": presentSystemUpdateJobStatus,
  "system-update-policy": presentSystemUpdatePolicyStatus,
  incident: presentIncidentStatus,
  diagnostic: presentDiagnosticStatus,
  remediation: presentRemediationStatus,
  "archive-processing": presentArchiveProcessingStatus,
  "archive-share": presentArchiveShareStatus,
  "audit-result": presentAuditResultStatus,
});

test("source-derived authority exactly owns every mapping value and every mapping executes production", () => {
  assert.equal(fixture.authorityHead, "4c98b1ed611d69c6a77bf4e74e5aca18a9b1ae3b");
  assert.deepEqual(compareStatusMappingInventory(authority, fixture), []);
  const sourceEvidence = verifyStatusAuthoritySources(webRoot, authority);
  assert.equal(sourceEvidence.verifiedSources + sourceEvidence.unavailableSources.length, authority.authorities.length);
  assert.equal(sourceEvidence.verifiedControlPanelSources >= 4, true);
  assert.equal(sourceEvidence.excerptMismatches, 0);
  assert.deepEqual(
    authority.authorities.map((entry) => [entry.repository, entry.head]),
    [
      ["Autostream-Contracts", "46567bf35acff1c252293b931add90e1b646056e"],
      ["Autostream-Observability", "2cbaccd05854a9ff2db9a8f5af6904be6be45494"],
      ["Autostream-ControlPanel", "b246e65508b552399f918e39b3948cf453cc1e32"],
      ["Autostream-ControlPanel", "b246e65508b552399f918e39b3948cf453cc1e32"],
      ["Autostream-ControlPanel", "4c98b1ed611d69c6a77bf4e74e5aca18a9b1ae3b"],
      ["Autostream-ControlPanel", "b246e65508b552399f918e39b3948cf453cc1e32"],
      ["Autostream-ControlPanel", "b246e65508b552399f918e39b3948cf453cc1e32"],
    ],
  );
  assert.deepEqual(Object.keys(presenters), fixture.domains);
  for (const mapping of fixture.mappings) {
    const presentation = presenters[mapping.domain](mapping.wireValue);
    assert.deepEqual(presentation, {
      known: true,
      tone: mapping.tone,
      labelKey: mapping.labelKey,
      ...(mapping.detailKey ? { detailKey: mapping.detailKey } : {}),
      icon: mapping.icon,
    }, `${mapping.domain}:${mapping.wireValue}`);
    assert.equal(Object.isFrozen(presentation), true);
    const canonicalCopy = copyCanonicalDomainStatusPresentation(presentation);
    assert.deepEqual(canonicalCopy, presentation, `${mapping.domain}:${mapping.wireValue} canonical copy`);
    assert.notEqual(canonicalCopy, presentation, `${mapping.domain}:${mapping.wireValue} must be detached`);
    assert.equal(translate("ja", presentation.labelKey).length > 0, true);
    assert.equal(translate("en", presentation.labelKey).length > 0, true);
  }
});

test("mechanical authority oracle rejects omissions, inventions, duplicates, wrong domains and provenance drift", () => {
  const mutations = [
    ["remove created", (candidate) => { candidate.mappings = candidate.mappings.filter((row) => !(row.domain === "stream-lifecycle" && row.wireValue === "created")); }],
    ["invent stream error", (candidate) => { candidate.mappings.push({ domain: "stream-lifecycle", wireValue: "error", labelKey: "statusFailed", tone: "critical", icon: "circle-alert" }); }],
    ["remove investigating", (candidate) => { candidate.mappings = candidate.mappings.filter((row) => !(row.domain === "incident" && row.wireValue === "investigating")); }],
    ["invent incident closed", (candidate) => { candidate.mappings.push({ domain: "incident", wireValue: "closed", labelKey: "statusIncidentResolved", tone: "neutral", icon: "circle-check" }); }],
    ["replace evaluated with success", (candidate) => { candidate.mappings.find((row) => row.domain === "diagnostic" && row.wireValue === "evaluated").wireValue = "success"; }],
    ["remove remediation disabled", (candidate) => { candidate.mappings = candidate.mappings.filter((row) => !(row.domain === "remediation" && row.wireValue === "disabled")); }],
    ["invent remediation skipped", (candidate) => { candidate.mappings.push({ domain: "remediation", wireValue: "skipped", labelKey: "statusRemediationBlocked", tone: "neutral", icon: "circle-slash" }); }],
    ["duplicate canonical value", (candidate) => { candidate.mappings.push({ ...candidate.mappings.find((row) => row.domain === "stream-lifecycle" && row.wireValue === "created") }); }],
    ["move value to wrong domain", (candidate) => { candidate.mappings.find((row) => row.domain === "stream-lifecycle" && row.wireValue === "created").domain = "incident"; }],
  ];
  for (const [name, mutate] of mutations) {
    const candidate = structuredClone(fixture);
    mutate(candidate);
    assert.notEqual(compareStatusMappingInventory(authority, candidate).length, 0, name);
  }

  const changedSourceDigest = structuredClone(authority);
  changedSourceDigest.authorities.find(({ id }) => id === "control-panel-domain").sourceSha256 = "0".repeat(64);
  assert.throws(() => verifyStatusAuthoritySources(webRoot, changedSourceDigest), /source digest/);
  const changedSnapshot = structuredClone(authority);
  changedSnapshot.vocabularies[0].excerpt += "\n// invented";
  assert.throws(() => deriveStatusAuthorityDomains(changedSnapshot), /excerpt digest/);

  const presentation = presentStreamLifecycleStatus("created");
  for (const mutant of [
    { ...presentation, tone: "critical" },
    { ...presentation, icon: "circle-alert" },
    { ...presentation, labelKey: "statusFailed" },
  ]) {
    assert.notDeepEqual(mutant, presentation, "frozen presentation fields must remain mutation-sensitive");
  }
});

test("unknown, future, case, whitespace, and hostile inputs fail closed without retaining raw values", () => {
  const throwingGetter = Object.defineProperty({}, "status", { get() { throw new Error("getter marker"); } });
  const revoked = Proxy.revocable({ status: "healthy" }, {});
  revoked.revoke();
  const toStringTrap = { toString() { throw new Error("toString marker"); } };
  const hostile = [null, undefined, 12, [], () => "healthy", throwingGetter, revoked.proxy, toStringTrap];

  for (const [domain, presenter] of Object.entries(presenters)) {
    for (const input of ["future_v9", " HEALTHY ", "HEALTHY", ...hostile]) {
      const presentation = presenter(input);
      assert.deepEqual(presentation, {
        known: false,
        tone: "unknown",
        labelKey: "statusUnknown",
        detailKey: "statusUnknownDetail",
        icon: "circle-help",
      }, `${domain}:${typeof input}`);
      assert.equal(Object.isFrozen(presentation), true);
      assert.equal(JSON.stringify(presentation).includes("future_v9"), false);
    }
  }
  assert.equal(presentRecordingLifecycleStatus("recording_future").known, false, "future recording status must not become waiting");
  assert.equal(presentNodeHealthStatus("assigned").known, false, "assigned is ownership, not health");
  for (const removed of ["draft", "scheduled", "ready", "stopped", "error"]) {
    assert.equal(presentStreamLifecycleStatus(removed).known, false, `stream ${removed} is not in Contracts`);
  }
  assert.equal(presentIncidentStatus("closed").known, false);
  for (const removed of ["acknowledged", "resolved", "pass", "ok", "success", "failed"]) {
    assert.equal(presentDiagnosticStatus(removed).known, false, `diagnostic ${removed} is not a diagnostic outcome`);
  }
  for (const removed of ["failed", "skipped"]) {
    assert.equal(presentRemediationStatus(removed).known, false, `remediation ${removed} is not in the SQL enum`);
  }
});

test("diagnostic code is copied only from the explicit stable allowlist", () => {
  assert.deepEqual(presentSystemUpdateTargetStatus({ status: "unreachable", diagnosticCode: "target_unreachable" }), {
    known: true,
    tone: "critical",
    labelKey: "statusUpdateTargetUnreachable",
    detailKey: "statusUpdateTargetUnreachableDetail",
    icon: "network-x",
    diagnosticCode: "target_unreachable",
  });
  assert.equal("diagnosticCode" in presentSystemUpdateTargetStatus({ status: "unreachable", diagnosticCode: "raw host https://secret.invalid" }), false);
  assert.equal("diagnosticCode" in presentSystemUpdateTargetStatus({ status: "reachable", diagnosticCode: "target_unreachable" }), false);
});

test("coverage and node-positive summaries exclude unknown and keep ownership separate", () => {
  const source = [presentNodeHealthStatus("healthy"), presentNodeHealthStatus("future"), presentNodeOwnershipStatus("assigned")];
  const coverage = summarizeStatusCoverage(source);
  assert.deepEqual(coverage, { total: 3, known: 2, unknown: 1 });
  assert.equal(Object.isFrozen(coverage), true);
  assert.deepEqual(summarizeStatusCoverage({ length: 3 }), { total: 0, known: 0, unknown: 0 });
  assert.deepEqual(summarizePositiveNodeHealth(source), { total: 3, positive: 1 });
  assert.equal(source[2].labelKey, "statusNodeAssigned");
});

test("canonical presentation copy rejects malformed and hostile runtime values without leakage", () => {
  let accessorReads = 0;
  const accessorLabel = Object.defineProperty({
    known: true,
    tone: "success",
    icon: "heart-pulse",
  }, "labelKey", {
    enumerable: true,
    get() {
      accessorReads += 1;
      return "statusNodeHealthy";
    },
  });
  const throwingGetter = Object.defineProperty({
    known: true,
    tone: "success",
    icon: "heart-pulse",
  }, "labelKey", {
    enumerable: true,
    get() { throw new Error("F4-GETTER-MARKER"); },
  });
  const symbolKey = { known: true, labelKey: "statusNodeHealthy", tone: "success", icon: "heart-pulse" };
  Object.defineProperty(symbolKey, Symbol("F4-SYMBOL-MARKER"), { enumerable: true, value: "F4-SYMBOL-MARKER" });
  const hiddenField = { known: true, labelKey: "statusNodeHealthy", tone: "success", icon: "heart-pulse" };
  Object.defineProperty(hiddenField, "hidden", { enumerable: false, value: "F4-HIDDEN-MARKER" });
  const throwingProxy = new Proxy({}, { ownKeys() { throw new Error("F4-PROXY-MARKER"); } });
  const revoked = Proxy.revocable({ known: true, labelKey: "statusNodeHealthy", tone: "success", icon: "heart-pulse" }, {});
  revoked.revoke();
  class PresentationClass {
    known = true;
    labelKey = "statusNodeHealthy";
    tone = "success";
    icon = "heart-pulse";
  }
  const malformed = [
    { known: true },
    { known: true, labelKey: "statusNodeHealthy" },
    { known: true, labelKey: "statusNodeHealthy", tone: "success" },
    { known: false, labelKey: "statusUnknown", tone: "success", icon: "unknown" },
    { known: true, labelKey: "statusNodeHealthy", tone: "success" },
    { known: true, labelKey: "statusNodeHealthy", tone: "success", icon: "heart-pulse", token: "F4-TOKEN-MARKER" },
    accessorLabel,
    symbolKey,
    hiddenField,
    throwingGetter,
    throwingProxy,
    revoked.proxy,
    new Map([["known", true]]),
    new Set(["statusNodeHealthy"]),
    new Date(0),
    new PresentationClass(),
    [],
    () => undefined,
  ];

  for (const value of malformed) {
    assert.doesNotThrow(() => copyCanonicalDomainStatusPresentation(value));
    assert.equal(copyCanonicalDomainStatusPresentation(value), undefined);
    const coverage = summarizeStatusCoverage([value]);
    const positive = summarizePositiveNodeHealth([value]);
    assert.deepEqual(coverage, { total: 1, known: 0, unknown: 1 });
    assert.deepEqual(positive, { total: 1, positive: 0 });
    assert.equal(coverage.known + coverage.unknown, coverage.total);
    assert.equal(Object.isFrozen(coverage), true);
    assert.equal(Object.isFrozen(positive), true);
    assert.doesNotMatch(JSON.stringify({ coverage, positive }), /F4-(?:TOKEN|GETTER|SYMBOL|HIDDEN|PROXY)-MARKER/);
  }
  assert.equal(accessorReads, 0, "descriptor validation must not execute accessors");

  const source = { known: true, labelKey: "statusNodeHealthy", tone: "success", icon: "heart-pulse" };
  const canonical = copyCanonicalDomainStatusPresentation(source);
  assert.deepEqual(canonical, source);
  assert.notEqual(canonical, source);
  assert.equal(Object.isFrozen(canonical), true);
  source.labelKey = "F4-SOURCE-MUTATION-MARKER";
  assert.equal(canonical?.labelKey, "statusNodeHealthy");
  assert.deepEqual(summarizeStatusCoverage([canonical]), { total: 1, known: 1, unknown: 0 });
  assert.deepEqual(summarizePositiveNodeHealth([canonical]), { total: 1, positive: 1 });
});

test("shared badge renders localized text, icon and semantic tone without raw or action capability", () => {
  const unknown = presentNodeConnectivityStatus("RAW_FUTURE_MARKER");
  const markup = renderToStaticMarkup(createElement(DomainStatusBadge, {
    presentation: unknown,
    translate: (key) => translate("en", key),
    showDetail: true,
  }));
  assert.match(markup, /Unknown status/);
  assert.match(markup, /data-status-tone="unknown"/);
  assert.match(markup, /data-status-icon="circle-help"/);
  assert.match(markup, /forced-color-adjust-auto/);
  assert.match(markup, /motion-reduce:transition-none/);
  assert.doesNotMatch(markup, /RAW_FUTURE_MARKER/);
  assert.equal("safeToAct" in unknown, false);
  assert.equal("onClick" in unknown, false);
});

test("AST boundaries reject action authority, arbitrary colors, mutable registries, barrels and cycles", () => {
  assert.deepEqual(assertStatusFoundationBoundaries(webRoot), { pureFileCount: 6, componentFileCount: 1 });
  assert.deepEqual(statusProductionConsumers(webRoot), [
    "src/features/workers/workers-status-presenter.ts",
    "src/features/workers/workers-view.tsx",
  ], "B07 must have no production consumer outside the Worker pilot");
});

test("status type negative matrix reports TS2578 when malformed input becomes valid", () => {
  const configPath = join(webRoot, "tsconfig.json");
  const typeTestPath = resolve(webRoot, "tests", "ui-foundation-status.type-test.ts");
  const configRead = ts.readConfigFile(configPath, ts.sys.readFile);
  assert.equal(configRead.error, undefined);
  const config = ts.parseJsonConfigFileContent(configRead.config, ts.sys, webRoot);
  const original = readFileSync(typeTestPath, "utf8");
  const mutant = original.replace(
    "export const invalidMalformedPresentation: DomainStatusPresentation = { known: true };",
    "export const invalidMalformedPresentation: DomainStatusPresentation = { known: true, tone: \"success\", labelKey: \"statusNodeHealthy\", icon: \"heart-pulse\" };",
  );
  assert.notEqual(mutant, original);
  const canonicalTarget = resolve(typeTestPath);
  const host = ts.createCompilerHost(config.options);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  host.getSourceFile = (fileName, languageVersion, onError, shouldCreateNewSourceFile) =>
    resolve(fileName) === canonicalTarget
      ? ts.createSourceFile(fileName, mutant, languageVersion, true, ts.ScriptKind.TS)
      : originalGetSourceFile(fileName, languageVersion, onError, shouldCreateNewSourceFile);
  const program = ts.createProgram({ rootNames: config.fileNames, options: config.options, host });
  const diagnostics = ts.getPreEmitDiagnostics(program);
  assert.equal(
    diagnostics.some((diagnostic) => diagnostic.code === 2578 && resolve(diagnostic.file?.fileName ?? "") === canonicalTarget),
    true,
    "a valid presentation conversion must leave an unused @ts-expect-error diagnostic",
  );
});
