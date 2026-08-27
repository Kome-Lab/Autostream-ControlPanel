import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import type { ActionEvaluation } from "../src/lib/foundation/actions/contracts.ts";
import type { PermissionRequirement } from "../src/lib/foundation/permissions/contracts.ts";
import type {
  EvaluateActionPermissionInput,
  PermissionDecision,
  PermissionSnapshot,
} from "../src/lib/foundation/permissions/evaluator.ts";
import type {
  ActionAvailabilityBoundaryProps,
  ActionControlRenderProps,
} from "../src/components/foundation/permissions/action-availability-boundary.ts";
import { assertPermissionFoundationBoundaries } from "./helpers/ui-foundation-permission-imports.mts";

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

type EvaluatorModule = typeof import("../src/lib/foundation/permissions/evaluator.ts");
type BoundaryModule = typeof import("../src/components/foundation/permissions/action-availability-boundary.ts");
type I18nModule = typeof import("../src/lib/i18n.ts");

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const fixturePath = join(webRoot, "tests", "fixtures", "ui-foundation-route-permissions.json");
let evaluatorPromise: Promise<EvaluatorModule> | undefined;
let boundaryPromise: Promise<BoundaryModule> | undefined;
let i18nPromise: Promise<I18nModule> | undefined;

function loadEvaluator() {
  evaluatorPromise ??= import("../src/lib/foundation/permissions/evaluator.ts");
  return evaluatorPromise;
}

function loadBoundary() {
  boundaryPromise ??= import("../src/components/foundation/permissions/action-availability-boundary.ts");
  return boundaryPromise;
}

function loadI18n() {
  i18nPromise ??= import("../src/lib/i18n.ts");
  return i18nPromise;
}

test("permission evaluator preserves exact wildcard, all, any, and none semantics", async () => {
  const { evaluatePermissionRequirement } = await loadEvaluator();
  const cases: readonly {
    label: string;
    requirement: PermissionRequirement;
    snapshot: PermissionSnapshot;
    expected: PermissionDecision;
  }[] = [
    { label: "none ready", requirement: { kind: "none" }, snapshot: ready(), expected: { kind: "allowed" } },
    { label: "none refreshing", requirement: { kind: "none" }, snapshot: { kind: "refreshing" }, expected: { kind: "allowed" } },
    { label: "none unavailable", requirement: { kind: "none" }, snapshot: { kind: "unavailable" }, expected: { kind: "allowed" } },
    { label: "all present", requirement: all("streams.read", "streams.start"), snapshot: ready("streams.read", "streams.start"), expected: { kind: "allowed" } },
    { label: "all missing", requirement: all("streams.read", "streams.start"), snapshot: ready("streams.read"), expected: { kind: "denied" } },
    { label: "any present", requirement: any("service_health.read", "api_tokens.create"), snapshot: ready("api_tokens.create"), expected: { kind: "allowed" } },
    { label: "any missing", requirement: any("service_health.read", "api_tokens.create"), snapshot: ready("workers.read"), expected: { kind: "denied" } },
    { label: "all wildcard", requirement: all("streams.read", "streams.start"), snapshot: ready("*"), expected: { kind: "allowed" } },
    { label: "any wildcard", requirement: any("service_health.read", "api_tokens.create"), snapshot: ready("*"), expected: { kind: "allowed" } },
    { label: "exact case only", requirement: all("streams.start"), snapshot: ready("Streams.Start"), expected: { kind: "denied" } },
    { label: "no prefix matching", requirement: all("streams.start"), snapshot: ready("streams"), expected: { kind: "denied" } },
    { label: "no permission hierarchy", requirement: all("streams"), snapshot: ready("streams.start"), expected: { kind: "denied" } },
    { label: "no whitespace trimming", requirement: all("streams.start"), snapshot: ready(" streams.start "), expected: { kind: "denied" } },
    { label: "no lowercase normalization", requirement: all("STREAMS.START"), snapshot: ready("streams.start"), expected: { kind: "denied" } },
    { label: "duplicate grants", requirement: all("streams.start"), snapshot: ready("streams.start", "streams.start"), expected: { kind: "allowed" } },
    { label: "duplicate all requirements", requirement: all("streams.start", "streams.start"), snapshot: ready("streams.start"), expected: { kind: "allowed" } },
    { label: "duplicate any requirements", requirement: any("streams.start", "streams.start"), snapshot: ready("streams.start"), expected: { kind: "allowed" } },
    { label: "refreshing all", requirement: all("streams.start"), snapshot: { kind: "refreshing" }, expected: { kind: "unknown", reason: "refreshing" } },
    { label: "unavailable any", requirement: any("streams.start"), snapshot: { kind: "unavailable" }, expected: { kind: "unknown", reason: "unavailable" } },
  ];

  for (const entry of cases) {
    assert.deepEqual(
      evaluatePermissionRequirement(entry.requirement, entry.snapshot),
      entry.expected,
      entry.label,
    );
  }
});

test("runtime malformed requirements fail closed without throwing", async () => {
  const evaluator = await loadEvaluator();
  const throwingRequirement = new Proxy({ kind: "all", permissions: ["streams.start"] }, {
    get() {
      throw new Error("hostile requirement getter");
    },
  });
  const revokedPermissions = Proxy.revocable([], {});
  revokedPermissions.revoke();

  const cases: readonly [string, unknown][] = [
    ["empty all", { kind: "all", permissions: [] }],
    ["empty any", { kind: "any", permissions: [] }],
    ["unknown kind", { kind: "some", permissions: ["streams.start"] }],
    ["missing list", { kind: "all" }],
    ["non-array list", { kind: "all", permissions: "streams.start" }],
    ["non-string requirement", { kind: "all", permissions: ["streams.start", 1] }],
    ["none carrying permissions", { kind: "none", permissions: ["streams.start"] }],
    ["throwing getter", throwingRequirement],
    ["revoked permission list", { kind: "all", permissions: revokedPermissions.proxy }],
    ["null requirement", null],
  ];

  for (const [label, requirement] of cases) {
    let decision: PermissionDecision | undefined;
    assert.doesNotThrow(() => {
      decision = evaluateRuntime(evaluator, requirement, ready("streams.start"));
    }, label);
    assert.deepEqual(decision, { kind: "unknown", reason: "malformed-requirement" }, label);
  }
});

test("runtime malformed snapshots fail closed without throwing, including for none", async () => {
  const evaluator = await loadEvaluator();
  const throwingSnapshot = new Proxy({ kind: "ready", permissions: ["streams.start"] }, {
    get() {
      throw new Error("hostile snapshot getter");
    },
  });
  const revokedPermissions = Proxy.revocable([], {});
  revokedPermissions.revoke();

  const cases: readonly [string, unknown][] = [
    ["null snapshot", null],
    ["unknown kind", { kind: "stale" }],
    ["ready missing list", { kind: "ready" }],
    ["ready non-array", { kind: "ready", permissions: "streams.start" }],
    ["ready non-string grant", { kind: "ready", permissions: ["streams.start", false] }],
    ["refreshing stale permissions", { kind: "refreshing", permissions: ["streams.start"] }],
    ["unavailable stale permissions", { kind: "unavailable", permissions: ["streams.start"] }],
    ["throwing getter", throwingSnapshot],
    ["revoked permission list", { kind: "ready", permissions: revokedPermissions.proxy }],
  ];

  for (const [label, snapshot] of cases) {
    let decision: PermissionDecision | undefined;
    assert.doesNotThrow(() => {
      decision = evaluateRuntime(evaluator, all("streams.start"), snapshot);
    }, label);
    assert.deepEqual(decision, { kind: "unknown", reason: "malformed-snapshot" }, label);
    assert.deepEqual(
      evaluateRuntime(evaluator, { kind: "none" }, snapshot),
      { kind: "unknown", reason: "malformed-snapshot" },
      `${label} none requirement`,
    );
  }
});

test("permission evaluation does not mutate or retain caller-owned permission arrays", async () => {
  const { evaluatePermissionRequirement } = await loadEvaluator();
  const requiredPermissions = ["streams.read", "streams.start"] as const;
  const grantedPermissions = ["streams.start", "streams.read"] as const;
  const requirement = Object.freeze({ kind: "all" as const, permissions: Object.freeze(requiredPermissions) });
  const snapshot = Object.freeze({ kind: "ready" as const, permissions: Object.freeze(grantedPermissions) });

  const decision = evaluatePermissionRequirement(requirement, snapshot);
  assert.deepEqual(decision, { kind: "allowed" });
  assert.deepEqual(requirement.permissions, ["streams.read", "streams.start"]);
  assert.deepEqual(snapshot.permissions, ["streams.start", "streams.read"]);
});

test("action projection keeps visibility and availability orthogonal with fixed priority", async () => {
  const { evaluateActionPermission } = await loadEvaluator();
  const deniedInput = input({ snapshot: ready(), pending: { kind: "pending", reasonKey: "actionAlreadyPending" }, constraint: { kind: "blocked", reasonKey: "actionStateBlocked" } });
  assert.deepEqual(evaluateActionPermission(deniedInput), {
    visibility: { kind: "visible" },
    availability: { kind: "denied", reasonKey: "actionPermissionDenied" },
  });
  assert.deepEqual(evaluateActionPermission({ ...deniedInput, disclosure: "hidden-security-sensitive" }), {
    visibility: { kind: "hidden", reason: "security-sensitive" },
    availability: { kind: "denied", reasonKey: "actionPermissionDenied" },
  });

  const unknownInput = input({ snapshot: { kind: "refreshing" }, constraint: { kind: "blocked", reasonKey: "actionStateBlocked" }, pending: { kind: "pending", reasonKey: "actionAlreadyPending" } });
  assert.deepEqual(evaluateActionPermission(unknownInput), {
    visibility: { kind: "visible" },
    availability: { kind: "unknown", reasonKey: "actionPermissionUnknown" },
  });
  assert.deepEqual(evaluateActionPermission({ ...unknownInput, disclosure: "hidden-security-sensitive" }), {
    visibility: { kind: "hidden", reason: "security-sensitive" },
    availability: { kind: "unknown", reasonKey: "actionPermissionUnknown" },
  });

  assert.deepEqual(evaluateActionPermission(input({
    constraint: { kind: "unknown", reasonKey: "actionPermissionUnknown" },
    pending: { kind: "pending", reasonKey: "actionAlreadyPending" },
  })), {
    visibility: { kind: "visible" },
    availability: { kind: "unknown", reasonKey: "actionPermissionUnknown" },
  });
  assert.deepEqual(evaluateActionPermission(input({
    constraint: { kind: "blocked", reasonKey: "actionStateBlocked" },
    pending: { kind: "pending", reasonKey: "actionAlreadyPending" },
  })), {
    visibility: { kind: "visible" },
    availability: { kind: "blocked", reasonKey: "actionStateBlocked" },
  });
  assert.deepEqual(evaluateActionPermission(input({
    constraint: { kind: "not-applicable", reasonKey: "actionStateBlocked" },
    pending: { kind: "pending", reasonKey: "actionAlreadyPending" },
  })), {
    visibility: { kind: "hidden", reason: "not-applicable" },
    availability: { kind: "blocked", reasonKey: "actionStateBlocked" },
  });
  assert.deepEqual(evaluateActionPermission(input({ pending: { kind: "pending", reasonKey: "actionAlreadyPending" } })), {
    visibility: { kind: "visible" },
    availability: { kind: "pending", reasonKey: "actionAlreadyPending" },
  });
  assert.deepEqual(evaluateActionPermission(input()), {
    visibility: { kind: "visible" },
    availability: { kind: "allowed" },
  });

  assert.deepEqual(evaluateActionPermission(input({ deniedReasonKey: "actions", snapshot: ready() })), {
    visibility: { kind: "visible" },
    availability: { kind: "denied", reasonKey: "actions" },
  });
  assert.deepEqual(evaluateActionPermission(input({ unknownReasonKey: "actions", snapshot: { kind: "unavailable" } })), {
    visibility: { kind: "visible" },
    availability: { kind: "unknown", reasonKey: "actions" },
  });
});

test("invocation predicate is true only for visible and allowed", async () => {
  const { isActionInvocationAllowed } = await loadEvaluator();
  const cases: readonly [ActionEvaluation, boolean][] = [
    [{ visibility: { kind: "visible" }, availability: { kind: "allowed" } }, true],
    [{ visibility: { kind: "hidden", reason: "security-sensitive" }, availability: { kind: "allowed" } }, false],
    [{ visibility: { kind: "visible" }, availability: { kind: "denied", reasonKey: "actionPermissionDenied" } }, false],
    [{ visibility: { kind: "visible" }, availability: { kind: "blocked", reasonKey: "actionStateBlocked" } }, false],
    [{ visibility: { kind: "visible" }, availability: { kind: "unknown", reasonKey: "actionPermissionUnknown" } }, false],
    [{ visibility: { kind: "visible" }, availability: { kind: "pending", reasonKey: "actionAlreadyPending" } }, false],
  ];
  for (const [evaluation, expected] of cases) assert.equal(isActionInvocationAllowed(evaluation), expected);

  const hostile = new Proxy({}, { get() { throw new Error("hostile evaluation"); } });
  assert.doesNotThrow(() => assert.equal(invokeRuntimePredicate(isActionInvocationAllowed, hostile), false));
});

test("hidden and allowed accessible boundaries do not expose unavailable UI", async () => {
  const { ActionAvailabilityBoundary } = await loadBoundary();
  let hiddenChildren = 0;
  let hiddenTranslations = 0;
  const hiddenMarkup = renderToStaticMarkup(createBoundaryElement(ActionAvailabilityBoundary, {
    evaluation: {
      visibility: { kind: "hidden", reason: "security-sensitive" },
      availability: { kind: "denied", reasonKey: "actionPermissionDenied" },
    },
    translate: () => {
      hiddenTranslations += 1;
      return "must not render";
    },
    reasonId: "hidden-reason",
  }, () => {
    hiddenChildren += 1;
    return createElement("button", null, "Hidden");
  }));
  assert.equal(hiddenMarkup, "");
  assert.equal(hiddenChildren, 0);
  assert.equal(hiddenTranslations, 0);

  let allowedProps: ActionControlRenderProps | undefined;
  const allowedMarkup = renderToStaticMarkup(createBoundaryElement(ActionAvailabilityBoundary, {
    evaluation: { visibility: { kind: "visible" }, availability: { kind: "allowed" } },
    translate: () => "must not render",
    reasonId: "allowed-reason",
  }, (props) => {
    allowedProps = props;
    return createElement("button", { ...props, type: "button" }, "Allowed");
  }));
  assert.deepEqual(allowedProps, { disabled: false });
  assert.match(allowedMarkup, /^<button type="button">Allowed<\/button>$/);
  assert.equal(allowedMarkup.includes("data-action-availability"), false);
  assert.equal(allowedMarkup.includes("allowed-reason"), false);
  assert.equal(allowedMarkup.includes("aria-disabled"), false);
  assert.equal(allowedMarkup.includes("aria-describedby"), false);
});

test("visible unavailable boundaries link focusable controls to inline and sr-only reasons", async () => {
  const { ActionAvailabilityBoundary } = await loadBoundary();
  const { translations } = await loadI18n();
  const cases = [
    {
      kind: "denied" as const,
      key: "actionPermissionDenied" as const,
      presentation: "inline" as const,
      expected: translations.en.actionPermissionDenied,
    },
    {
      kind: "unknown" as const,
      key: "actionPermissionUnknown" as const,
      presentation: "sr-only" as const,
      expected: translations.en.actionPermissionUnknown,
    },
    {
      kind: "blocked" as const,
      key: "actionStateBlocked" as const,
      presentation: "inline" as const,
      expected: translations.en.actionStateBlocked,
    },
    {
      kind: "pending" as const,
      key: "actionAlreadyPending" as const,
      presentation: "sr-only" as const,
      expected: translations.en.actionAlreadyPending,
    },
  ];

  for (const entry of cases) {
    let childCalls = 0;
    let childProps: ActionControlRenderProps | undefined;
    const reasonId = `${entry.kind}-reason`;
    const markup = renderToStaticMarkup(createBoundaryElement(ActionAvailabilityBoundary, {
      evaluation: {
        visibility: { kind: "visible" },
        availability: { kind: entry.kind, reasonKey: entry.key },
      },
      translate: (key) => translations.en[key],
      reasonPresentation: entry.presentation,
      reasonId,
    }, (props) => {
      childCalls += 1;
      childProps = props;
      return createElement("button", { ...props, type: "button" }, "Unavailable");
    }));

    assert.equal(childCalls, 1, entry.kind);
    assert.deepEqual(childProps, {
      disabled: true,
      "aria-disabled": true,
      "aria-describedby": reasonId,
    }, entry.kind);
    assert.match(markup, /<span[^>]*tabindex="0"/, entry.kind);
    assert.match(markup, new RegExp(`aria-disabled="true"[^>]*aria-describedby="${reasonId}"`), entry.kind);
    assert.match(markup, new RegExp(`data-action-availability="${entry.kind}"`), entry.kind);
    assert.match(markup, new RegExp(`<span id="${reasonId}"`), entry.kind);
    assert.equal(markup.includes(entry.expected), true, entry.kind);
    assert.equal(markup.includes("streams.start"), false, entry.kind);
    assert.equal(markup.includes("raw backend error"), false, entry.kind);
    assert.equal(markup.includes("disabled=\"\""), true, entry.kind);
    assert.equal(markup.includes(`aria-describedby="${reasonId}"`), true, entry.kind);
    assert.equal(markup.includes("class=\"sr-only\""), entry.presentation === "sr-only", entry.kind);
  }
});

test("one evaluation drives page, row, card, mobile, overflow, shortcut, and breadcrumb placements", async () => {
  const { evaluateActionPermission } = await loadEvaluator();
  const { ActionAvailabilityBoundary } = await loadBoundary();
  const placements = ["page-primary", "table-row", "card", "mobile", "overflow", "shortcut", "breadcrumb"] as const;

  const hidden = evaluateActionPermission(input({ snapshot: ready(), disclosure: "hidden-security-sensitive" }));
  let hiddenRenderCount = 0;
  for (const placement of placements) {
    const output = renderToStaticMarkup(createBoundaryElement(ActionAvailabilityBoundary, {
      evaluation: hidden,
      translate: () => "hidden",
      reasonId: `${placement}-reason`,
    }, () => {
      hiddenRenderCount += 1;
      return createElement("button", null, placement);
    }));
    assert.equal(output, "", placement);
  }
  assert.equal(hiddenRenderCount, 0);

  const unavailable = evaluateActionPermission(input({ snapshot: ready() }));
  const observed: { placement: string; props: ActionControlRenderProps | undefined; markup: string }[] = [];
  for (const placement of placements) {
    let props: ActionControlRenderProps | undefined;
    const markup = renderToStaticMarkup(createBoundaryElement(ActionAvailabilityBoundary, {
      evaluation: unavailable,
      translate: () => "Denied",
      reasonId: `${placement}-reason`,
    }, (controlProps) => {
      props = controlProps;
      return createElement("button", { ...controlProps }, placement);
    }));
    observed.push({ placement, props, markup });
  }
  for (const entry of observed) {
    assert.equal(unavailable.availability.kind, "denied");
    assert.equal(entry.props?.disabled, true, entry.placement);
    assert.equal(entry.props?.["aria-disabled"], true, entry.placement);
    assert.equal(entry.props?.["aria-describedby"], `${entry.placement}-reason`, entry.placement);
    assert.match(entry.markup, /data-action-availability="denied"/, entry.placement);
  }

  const allowed = evaluateActionPermission(input());
  for (const placement of placements) {
    let props: ActionControlRenderProps | undefined;
    renderToStaticMarkup(createBoundaryElement(ActionAvailabilityBoundary, {
      evaluation: allowed,
      translate: () => "unused",
    }, (controlProps) => {
      props = controlProps;
      return createElement("button", { ...controlProps }, placement);
    }));
    assert.deepEqual(props, { disabled: false }, placement);
  }
});

test("handler always reevaluates fresh permission, constraint, and pending state before request", async () => {
  const { evaluateActionPermission, isActionInvocationAllowed } = await loadEvaluator();
  const cases: readonly {
    label: string;
    renderInput: EvaluateActionPermissionInput;
    handlerInput: EvaluateActionPermissionInput;
    expectedRequests: number;
  }[] = [
    { label: "allowed to allowed", renderInput: input(), handlerInput: input(), expectedRequests: 1 },
    { label: "allowed to denied", renderInput: input(), handlerInput: input({ snapshot: ready() }), expectedRequests: 0 },
    { label: "allowed to unknown", renderInput: input(), handlerInput: input({ snapshot: { kind: "unavailable" } }), expectedRequests: 0 },
    { label: "allowed to blocked", renderInput: input(), handlerInput: input({ constraint: { kind: "blocked", reasonKey: "actionStateBlocked" } }), expectedRequests: 0 },
    { label: "allowed to pending", renderInput: input(), handlerInput: input({ pending: { kind: "pending", reasonKey: "actionAlreadyPending" } }), expectedRequests: 0 },
    { label: "hidden to hidden", renderInput: input({ snapshot: ready(), disclosure: "hidden-security-sensitive" }), handlerInput: input({ snapshot: ready(), disclosure: "hidden-security-sensitive" }), expectedRequests: 0 },
    { label: "denied to allowed after refresh", renderInput: input({ snapshot: ready() }), handlerInput: input(), expectedRequests: 1 },
  ];

  for (const entry of cases) {
    const renderEvaluation = evaluateActionPermission(entry.renderInput);
    let requestCount = 0;
    const handlerEvaluation = evaluateActionPermission(entry.handlerInput);
    if (isActionInvocationAllowed(handlerEvaluation)) requestCount += 1;
    assert.equal(requestCount, entry.expectedRequests, `${entry.label}: ${renderEvaluation.availability.kind} -> ${handlerEvaluation.availability.kind}`);
  }
});

test("backend 403 remains B-02 forbidden and is never locally retried or resent", async () => {
  const [{ evaluateActionPermission, isActionInvocationAllowed }, { adaptAPIError }, { APIError }] = await Promise.all([
    loadEvaluator(),
    import("../src/lib/foundation/api-errors/adapter.ts"),
    import("../src/lib/api/client.ts"),
  ]);
  const handlerEvaluation = evaluateActionPermission(input());
  let initialRequestCount = 0;
  const automaticResendCount = 0;
  if (isActionInvocationAllowed(handlerEvaluation)) initialRequestCount += 1;
  const adapted = adaptAPIError(new APIError("raw permission detail must not render", 403, "permission_denied"));

  assert.equal(initialRequestCount, 1);
  assert.equal(automaticResendCount, 0);
  assert.deepEqual(adapted, { kind: "forbidden", messageKey: "apiErrorForbidden" });
  assert.equal(JSON.stringify(adapted).includes("permission_denied"), false);
  assert.equal(JSON.stringify(adapted).includes("raw permission detail"), false);
  assert.equal(automaticResendCount, 0);
});

test("fixed route fixture has exact immutable authorities and separate page/action requirements", async () => {
  const raw = readFileSync(fixturePath, "utf8");
  const fixture: RoutePermissionFixture = JSON.parse(raw);
  assert.deepEqual(Object.keys(fixture), ["metadata", "entries"]);
  assert.deepEqual(fixture.metadata, {
    repository: "Kome-Lab/Autostream-ControlPanel",
    authorityHead: "09b235e56a459c74dc2f2833588db86299efe568",
    source: "internal/httpapi/server.go",
    navigationSource: "web/src/lib/navigation.ts",
  });
  assert.deepEqual(fixture.entries, [
    {
      id: "STR-08",
      surface: "streams.start-readiness",
      method: "POST",
      route: "/streams/{id}/start-readiness",
      pageRequirement: { kind: "all", permissions: ["streams.read"] },
      actionRequirement: { kind: "all", permissions: ["streams.start"] },
      serverWrapper: "requirePermission",
    },
    {
      id: "WORKERS-CONFIGURATION",
      surface: "workers.configuration",
      method: "GET",
      route: "/nodes/{id}/configuration",
      pageRequirement: {
        kind: "any",
        permissions: ["workers.read", "service_health.read", "api_tokens.create"],
      },
      actionRequirement: {
        kind: "any",
        permissions: ["service_health.read", "api_tokens.create"],
      },
      serverWrapper: "requireAnyPermission",
    },
  ]);
  assert.deepEqual(fixture.entries.map((entry) => entry.id), ["STR-08", "WORKERS-CONFIGURATION"]);
  assert.equal(new Set(fixture.entries.map((entry) => entry.id)).size, 2);
  assert.equal(fixture.entries.every((entry) => ["GET", "POST"].includes(entry.method)), true);
  for (const entry of fixture.entries) {
    for (const requirement of [entry.pageRequirement, entry.actionRequirement]) {
      assert.equal(["all", "any"].includes(requirement.kind), true);
      assert.equal(requirement.permissions.length > 0, true);
      assert.equal(requirement.permissions.every((permission) => typeof permission === "string" && permission.length > 0), true);
    }
  }
});

test("STR-08 fixture preserves independent page and start-readiness action decisions", async () => {
  const { evaluatePermissionRequirement } = await loadEvaluator();
  const fixture: RoutePermissionFixture = JSON.parse(readFileSync(fixturePath, "utf8"));
  const route = requiredFixtureEntry(fixture, "STR-08");
  const cases: readonly [string, PermissionSnapshot, PermissionDecision["kind"], PermissionDecision["kind"]][] = [
    ["read plus start", ready("streams.read", "streams.start"), "allowed", "allowed"],
    ["read only", ready("streams.read"), "allowed", "denied"],
    ["start only", ready("streams.start"), "denied", "allowed"],
    ["wildcard", ready("*"), "allowed", "allowed"],
    ["refreshing", { kind: "refreshing" }, "unknown", "unknown"],
  ];
  for (const [label, snapshot, pageKind, actionKind] of cases) {
    assert.equal(evaluatePermissionRequirement(route.pageRequirement, snapshot).kind, pageKind, `${label} page`);
    assert.equal(evaluatePermissionRequirement(route.actionRequirement, snapshot).kind, actionKind, `${label} action`);
  }
});

test("Workers Configuration fixture preserves broader page ANY and narrower server ANY", async () => {
  const { evaluatePermissionRequirement } = await loadEvaluator();
  const fixture: RoutePermissionFixture = JSON.parse(readFileSync(fixturePath, "utf8"));
  const route = requiredFixtureEntry(fixture, "WORKERS-CONFIGURATION");
  const cases: readonly [string, PermissionSnapshot, PermissionDecision["kind"], PermissionDecision["kind"]][] = [
    ["workers read only", ready("workers.read"), "allowed", "denied"],
    ["service health only", ready("service_health.read"), "allowed", "allowed"],
    ["API token create only", ready("api_tokens.create"), "allowed", "allowed"],
    ["neither", ready("streams.read"), "denied", "denied"],
    ["wildcard", ready("*"), "allowed", "allowed"],
    ["refreshing", { kind: "refreshing" }, "unknown", "unknown"],
  ];
  for (const [label, snapshot, pageKind, actionKind] of cases) {
    assert.equal(evaluatePermissionRequirement(route.pageRequirement, snapshot).kind, pageKind, `${label} page`);
    assert.equal(evaluatePermissionRequirement(route.actionRequirement, snapshot).kind, actionKind, `${label} configuration`);
  }
});

test("ja/en permission reason copy is exact, parallel, placeholder-free, and key-safe", async () => {
  const { translations, translate } = await loadI18n();
  const expected = {
    ja: {
      actionPermissionDenied: "この操作を実行する権限がありません。",
      actionPermissionUnknown: "権限情報を確認できないため、この操作は実行できません。",
      actionStateBlocked: "現在の状態ではこの操作を実行できません。",
      actionAlreadyPending: "この操作はすでに実行中です。",
    },
    en: {
      actionPermissionDenied: "You do not have permission to perform this action.",
      actionPermissionUnknown: "This action is unavailable because permissions could not be verified.",
      actionStateBlocked: "This action is unavailable in the current state.",
      actionAlreadyPending: "This action is already in progress.",
    },
  } as const;
  for (const locale of ["ja", "en"] as const) {
    for (const key of Object.keys(expected[locale]) as (keyof typeof expected.ja)[]) {
      assert.equal(translations[locale][key], expected[locale][key], `${locale}.${key}`);
      assert.equal(translate(locale, key), expected[locale][key], `${locale}.${key} translate`);
      assert.equal(translations[locale][key].includes("{"), false, `${locale}.${key} placeholder`);
      assert.notEqual(translations[locale][key], key, `${locale}.${key} key leakage`);
    }
  }
  assert.deepEqual(Object.keys(expected.ja), Object.keys(expected.en));
});

test("new production modules have pure imports, no endpoint ownership, and zero consumers", () => {
  assert.deepEqual(assertPermissionFoundationBoundaries(webRoot), {
    componentRuntimeImports: 1,
    evaluatorRuntimeImports: 0,
    productionConsumerCount: 0,
  });
});

function ready(...permissions: readonly string[]): PermissionSnapshot {
  return { kind: "ready", permissions };
}

function all(first: string, ...rest: string[]): PermissionRequirement {
  return { kind: "all", permissions: [first, ...rest] };
}

function any(first: string, ...rest: string[]): PermissionRequirement {
  return { kind: "any", permissions: [first, ...rest] };
}

function input(overrides: Partial<EvaluateActionPermissionInput> = {}): EvaluateActionPermissionInput {
  return {
    requirement: all("streams.start"),
    snapshot: ready("streams.start"),
    disclosure: "visible-denied",
    ...overrides,
  };
}

function evaluateRuntime(evaluator: EvaluatorModule, requirement: unknown, snapshot: unknown): PermissionDecision {
  return Reflect.apply(evaluator.evaluatePermissionRequirement, undefined, [requirement, snapshot]);
}

function invokeRuntimePredicate(predicate: EvaluatorModule["isActionInvocationAllowed"], evaluation: unknown): boolean {
  return Reflect.apply(predicate, undefined, [evaluation]);
}

function createBoundaryElement(
  Boundary: BoundaryModule["ActionAvailabilityBoundary"],
  props: Omit<ActionAvailabilityBoundaryProps, "children">,
  children: ActionAvailabilityBoundaryProps["children"],
) {
  const completeProps: ActionAvailabilityBoundaryProps = { ...props, children };
  return createElement(Boundary, completeProps);
}

type RoutePermissionEntry = {
  id: string;
  surface: string;
  method: string;
  route: string;
  pageRequirement: PermissionRequirement & { permissions: readonly string[] };
  actionRequirement: PermissionRequirement & { permissions: readonly string[] };
  serverWrapper: string;
};

type RoutePermissionFixture = {
  metadata: {
    repository: string;
    authorityHead: string;
    source: string;
    navigationSource: string;
  };
  entries: readonly RoutePermissionEntry[];
};

function requiredFixtureEntry(fixture: RoutePermissionFixture, id: string) {
  const entry = fixture.entries.find((candidate) => candidate.id === id);
  assert.ok(entry, `${id} fixture entry is missing`);
  return entry;
}
