import assert from "node:assert/strict";
import { Children, createElement, isValidElement, type ReactElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { register } from "node:module";
import test from "node:test";
import { fileURLToPath } from "node:url";

import type { AdaptedAPIError } from "../src/lib/foundation/api-errors/contracts.ts";
import type { Freshness, RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import type {
  RemoteSectionState,
} from "../src/lib/foundation/remote-state/aggregate.ts";
import type {
  ProjectRemoteStateOptions,
  QueryProjectionSnapshot,
} from "../src/lib/foundation/remote-state/projector.ts";
import type { RemoteStateBoundaryProps } from "../src/components/foundation/remote-state/remote-state-boundary.ts";
import type { RemoteStateNoticeProps } from "../src/components/foundation/remote-state/remote-state-notice.ts";
import type { TranslationKey, TranslationValues } from "../src/lib/i18n.ts";
import { assertRemoteStateFoundationBoundaries } from "./helpers/ui-foundation-remote-state-imports.mts";

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

type ProjectorModule = typeof import("../src/lib/foundation/remote-state/projector.ts");
type AggregateModule = typeof import("../src/lib/foundation/remote-state/aggregate.ts");
type BoundaryModule = typeof import("../src/components/foundation/remote-state/remote-state-boundary.ts");
type NoticeModule = typeof import("../src/components/foundation/remote-state/remote-state-notice.ts");
type I18nModule = typeof import("../src/lib/i18n.ts");

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const protocolError = Object.freeze({ kind: "protocol", messageKey: "apiErrorProtocol" } as const satisfies AdaptedAPIError);
const networkError = Object.freeze({ kind: "network", messageKey: "apiErrorNetwork" } as const satisfies AdaptedAPIError);
const unavailableError = Object.freeze({ kind: "unavailable", messageKey: "apiErrorUnavailable" } as const satisfies AdaptedAPIError);

let projectorPromise: Promise<ProjectorModule> | undefined;
let aggregatePromise: Promise<AggregateModule> | undefined;
let boundaryPromise: Promise<BoundaryModule> | undefined;
let noticePromise: Promise<NoticeModule> | undefined;
let i18nPromise: Promise<I18nModule> | undefined;

function loadProjector() {
  projectorPromise ??= import("../src/lib/foundation/remote-state/projector.ts");
  return projectorPromise;
}

function loadAggregate() {
  aggregatePromise ??= import("../src/lib/foundation/remote-state/aggregate.ts");
  return aggregatePromise;
}

function loadBoundary() {
  boundaryPromise ??= import("../src/components/foundation/remote-state/remote-state-boundary.ts");
  return boundaryPromise;
}

function loadNotice() {
  noticePromise ??= import("../src/components/foundation/remote-state/remote-state-notice.ts");
  return noticePromise;
}

function loadI18n() {
  i18nPromise ??= import("../src/lib/i18n.ts");
  return i18nPromise;
}

test("single-query projector implements the canonical truth table", async () => {
  const { projectRemoteState } = await loadProjector();
  const cases: readonly {
    label: string;
    snapshot: QueryProjectionSnapshot<readonly string[]>;
    expected: RemoteState<readonly string[]>;
  }[] = [
    {
      label: "pending is initial",
      snapshot: { status: "pending", fetching: true },
      expected: { kind: "initial-loading" },
    },
    {
      label: "success empty idle",
      snapshot: { status: "success", fetching: false, data: [], dataUpdatedAt: 10 },
      expected: { kind: "empty", freshness: { kind: "fresh", lastSuccessAt: 10 } },
    },
    {
      label: "success empty fetching",
      snapshot: { status: "success", fetching: true, data: [], dataUpdatedAt: 11 },
      expected: { kind: "empty", freshness: { kind: "refreshing", lastSuccessAt: 11 } },
    },
    {
      label: "success ready idle",
      snapshot: { status: "success", fetching: false, data: ["worker"], dataUpdatedAt: 12 },
      expected: { kind: "ready", data: ["worker"], freshness: { kind: "fresh", lastSuccessAt: 12 } },
    },
    {
      label: "success ready fetching",
      snapshot: { status: "success", fetching: true, data: ["worker"], dataUpdatedAt: 13 },
      expected: { kind: "ready", data: ["worker"], freshness: { kind: "refreshing", lastSuccessAt: 13 } },
    },
    {
      label: "error no data blocks",
      snapshot: { status: "error", fetching: false, error: new Error("raw") },
      expected: { kind: "blocking-error", error: networkError },
    },
    {
      label: "cached empty is stale",
      snapshot: { status: "error", fetching: false, error: new Error("raw"), data: [], dataUpdatedAt: 14 },
      expected: { kind: "empty", freshness: { kind: "stale", lastSuccessAt: 14, error: networkError } },
    },
    {
      label: "cached ready is stale",
      snapshot: { status: "error", fetching: false, error: new Error("raw"), data: ["worker"], dataUpdatedAt: 15 },
      expected: { kind: "ready", data: ["worker"], freshness: { kind: "stale", lastSuccessAt: 15, error: networkError } },
    },
    {
      label: "cached error wins over fetching",
      snapshot: { status: "error", fetching: true, error: new Error("raw"), data: ["worker"], dataUpdatedAt: 16 },
      expected: { kind: "ready", data: ["worker"], freshness: { kind: "stale", lastSuccessAt: 16, error: networkError } },
    },
  ];

  const options: ProjectRemoteStateOptions<readonly string[]> = {
    classifyData: (data) => data.length === 0 ? "empty" : "ready",
    adaptError: () => networkError,
  };
  for (const entry of cases) {
    const result = projectRemoteState(entry.snapshot, options);
    assert.deepEqual(result, entry.expected, entry.label);
    assert.equal(Object.isFrozen(result), true, `${entry.label} wrapper`);
    if (result.kind === "empty" || result.kind === "ready" || result.kind === "partial") {
      assert.equal(Object.isFrozen(result.freshness), true, `${entry.label} freshness`);
    }
    if (entry.snapshot.status !== "pending" && entry.snapshot.data !== undefined && result.kind === "ready") {
      assert.equal(result.data, entry.snapshot.data, `${entry.label} cached identity`);
    }
  }
});

test("single-query projector is total, immutable, and fail-closed for hostile runtime values", async () => {
  const projector = await loadProjector();
  const data = Object.freeze(["cached"]);
  const snapshot = Object.freeze({
    status: "error" as const,
    fetching: true,
    error: Object.freeze({ message: "RAW_MESSAGE", body: "RAW_BODY", URL: "RAW_URL", stack: "RAW_STACK", token: "RAW_TOKEN" }),
    data,
    dataUpdatedAt: 41,
  });
  const options = Object.freeze({ classifyData: () => "ready" as const });
  const result = projector.projectRemoteState(snapshot, options);
  assert.equal(result.kind, "ready");
  if (result.kind !== "ready") return;
  assert.equal(result.data, data);
  assert.deepEqual(result.freshness, {
    kind: "stale",
    lastSuccessAt: 41,
    error: { kind: "unknown", messageKey: "apiErrorUnknown" },
  });
  assert.equal(JSON.stringify(result).includes("RAW_"), false);
  assert.deepEqual(snapshot.data, ["cached"]);

  const throwingGetter = new Proxy({ status: "pending", fetching: true }, {
    get() {
      throw new Error("hostile getter");
    },
  });
  const revoked = Proxy.revocable({ status: "pending", fetching: true }, {});
  revoked.revoke();
  const malformed: readonly [string, unknown, unknown][] = [
    ["null snapshot", null, options],
    ["undefined snapshot", undefined, options],
    ["unknown status", { status: "idle", fetching: false }, options],
    ["missing success data", { status: "success", fetching: false, dataUpdatedAt: 1 }, options],
    ["undefined success data", { status: "success", fetching: false, data: undefined, dataUpdatedAt: 1 }, options],
    ["success raw error", { status: "success", fetching: false, data: ["x"], dataUpdatedAt: 1, error: new Error("raw") }, options],
    ["error without error", { status: "error", fetching: false }, options],
    ["non boolean fetching", { status: "pending", fetching: 1 }, options],
    ["negative timestamp", { status: "success", fetching: false, data: ["x"], dataUpdatedAt: -1 }, options],
    ["fractional timestamp", { status: "success", fetching: false, data: ["x"], dataUpdatedAt: 1.5 }, options],
    ["infinite timestamp", { status: "success", fetching: false, data: ["x"], dataUpdatedAt: Number.POSITIVE_INFINITY }, options],
    ["NaN timestamp", { status: "success", fetching: false, data: ["x"], dataUpdatedAt: Number.NaN }, options],
    ["classifier throws", { status: "success", fetching: false, data: ["x"], dataUpdatedAt: 1 }, { classifyData: () => { throw new Error("classifier"); } }],
    ["classifier unknown", { status: "success", fetching: false, data: ["x"], dataUpdatedAt: 1 }, { classifyData: () => "unknown" }],
    ["adapter throws", { status: "error", fetching: false, error: new Error("raw") }, { classifyData: () => "ready", adaptError: () => { throw new Error("adapter"); } }],
    ["throwing getter", throwingGetter, options],
    ["revoked proxy", revoked.proxy, options],
  ];
  for (const [label, runtimeSnapshot, runtimeOptions] of malformed) {
    let projected: unknown;
    assert.doesNotThrow(() => {
      projected = projectRuntime(projector, runtimeSnapshot, runtimeOptions);
    }, label);
    assertProtocolBlocking(projected, label);
  }
});

test("custom adapter output is canonicalized before Remote State rendering", async () => {
  const [projector, { RemoteStateBoundary }, i18n] = await Promise.all([
    loadProjector(),
    loadBoundary(),
    loadI18n(),
  ]);
  const classifyData = () => "ready" as const;
  const projectWith = (adapterOutput: unknown) => {
    let result: unknown;
    assert.doesNotThrow(() => {
      result = projectRuntime(
        projector,
        { status: "error", fetching: false, error: new Error("adapter input") },
        { classifyData, adaptError: () => adapterOutput },
      );
    });
    return result;
  };
  const errorOf = (state: unknown) => isObjectLike(state) && Reflect.get(state, "kind") === "blocking-error"
    ? Reflect.get(state, "error")
    : undefined;
  const errorSummary = (state: unknown, source: unknown) => {
    const error = errorOf(state);
    return {
      sourceIdentityRetained: error === source,
      frozen: isObjectLike(error) && Object.isFrozen(error),
      ownKeys: isObjectLike(error) ? Reflect.ownKeys(error).map(String).sort() : [],
      kind: isObjectLike(error) ? Reflect.get(error, "kind") : undefined,
      messageKey: isObjectLike(error) ? Reflect.get(error, "messageKey") : undefined,
    };
  };
  const isProtocolFallback = (state: unknown) => {
    const summary = errorSummary(state, undefined);
    return isObjectLike(state)
      && Reflect.get(state, "kind") === "blocking-error"
      && summary.kind === "protocol"
      && summary.messageKey === "apiErrorProtocol"
      && summary.frozen
      && JSON.stringify(summary.ownKeys) === JSON.stringify(["kind", "messageKey"]);
  };

  const source = Object.freeze({
    kind: "protocol",
    messageKey: "RAW_REMOTE_ERROR_MARKER",
    token: "TOKEN_MARKER",
    url: "https://internal.invalid/",
    stack: "STACK_MARKER",
  });
  const result = projectWith(source);
  const renderedNode: ReactNode = Reflect.apply(RemoteStateBoundary, undefined, [{
    state: result,
    noticeId: "hostile-adapter-notice",
    translate: english(i18n),
    formatTimestamp: String,
    renderData: () => "UNEXPECTED_DATA",
  }]);
  const markup = renderToStaticMarkup(createElement("div", null, renderedNode));
  const serialized = JSON.stringify(result);
  const hostileMarkers = [source.messageKey, source.token, source.url, source.stack];
  assert.deepEqual({
    ...errorSummary(result, source),
    serializedLeakCount: hostileMarkers.filter((marker) => serialized.includes(marker)).length,
    renderedLeakCount: hostileMarkers.filter((marker) => markup.includes(marker)).length,
    genericProtocolCopyRendered: markup.includes("The service returned an unexpected response."),
  }, {
    sourceIdentityRetained: false,
    frozen: true,
    ownKeys: ["kind", "messageKey"],
    kind: "protocol",
    messageKey: "apiErrorProtocol",
    serializedLeakCount: 0,
    renderedLeakCount: 0,
    genericProtocolCopyRendered: true,
  });

  const symbolSource = { kind: "network", messageKey: "apiErrorNetwork" };
  Object.defineProperty(symbolSource, Symbol("hidden"), { value: "SYMBOL_MARKER", enumerable: true });
  const nonEnumerableSource = { kind: "network", messageKey: "apiErrorNetwork" };
  Object.defineProperty(nonEnumerableSource, "token", { value: "TOKEN_MARKER", enumerable: false });
  const accessorSource = { kind: "network" };
  Object.defineProperty(accessorSource, "messageKey", {
    enumerable: true,
    get: () => "apiErrorNetwork",
  });
  class AdapterErrorClass {
    readonly kind = "network";
    readonly messageKey = "apiErrorNetwork";
  }
  const revoked = Proxy.revocable({ kind: "network", messageKey: "apiErrorNetwork" }, {});
  revoked.revoke();
  const throwingDescriptorProxy = new Proxy({ kind: "network", messageKey: "apiErrorNetwork" }, {
    getOwnPropertyDescriptor() {
      throw new Error("adapter descriptor failure");
    },
  });
  const invalidOutputs = {
    arbitraryMessageKey: { kind: "protocol", messageKey: "RAW_DYNAMIC_KEY" },
    extraToken: { kind: "protocol", messageKey: "apiErrorProtocol", token: "TOKEN_MARKER" },
    extraURL: { kind: "protocol", messageKey: "apiErrorProtocol", url: "https://internal.invalid/" },
    extraStack: { kind: "protocol", messageKey: "apiErrorProtocol", stack: "STACK_MARKER" },
    symbolProperty: symbolSource,
    nonEnumerableProperty: nonEnumerableSource,
    accessorProperty: accessorSource,
    map: new Map([["kind", "network"], ["messageKey", "apiErrorNetwork"]]),
    classInstance: new AdapterErrorClass(),
    functionValue: () => networkError,
    throwingDescriptorProxy,
    revokedProxy: revoked.proxy,
  };
  assert.deepEqual(Object.fromEntries(Object.entries(invalidOutputs).map(([name, value]) => [
    name,
    isProtocolFallback(projectWith(value)),
  ])), Object.fromEntries(Object.keys(invalidOutputs).map((name) => [name, true])));

  let throwingAdapterResult: unknown;
  assert.doesNotThrow(() => {
    throwingAdapterResult = projectRuntime(
      projector,
      { status: "error", fetching: false, error: new Error("adapter input") },
      { classifyData, adaptError: () => { throw new Error("adapter failure"); } },
    );
  });
  assert.equal(isProtocolFallback(throwingAdapterResult), true);

  const nullPrototypeSource: { kind?: unknown; messageKey?: unknown } = Object.create(null);
  nullPrototypeSource.kind = "network";
  nullPrototypeSource.messageKey = "apiErrorNetwork";
  const nullPrototypeResult = projectWith(nullPrototypeSource);
  assert.deepEqual(errorSummary(nullPrototypeResult, nullPrototypeSource), {
    sourceIdentityRetained: false,
    frozen: true,
    ownKeys: ["kind", "messageKey"],
    kind: "network",
    messageKey: "apiErrorNetwork",
  });

  const optionalSource = {
    kind: "validation",
    messageKey: "apiErrorValidation",
    fieldErrors: [{ field: "name", messageKey: "apiErrorValidation" }],
    retryAfterSeconds: 1,
    diagnosticCode: "SAFE_BUT_UNUSED",
  };
  assert.deepEqual(errorSummary(projectWith(optionalSource), optionalSource), {
    sourceIdentityRetained: false,
    frozen: true,
    ownKeys: ["kind", "messageKey"],
    kind: "validation",
    messageKey: "apiErrorValidation",
  });

  const mutableSource = { kind: "network", messageKey: "apiErrorNetwork" };
  const copiedResult = projectWith(mutableSource);
  mutableSource.kind = "unknown";
  mutableSource.messageKey = "apiErrorUnknown";
  assert.deepEqual(errorSummary(copiedResult, mutableSource), {
    sourceIdentityRetained: false,
    frozen: true,
    ownKeys: ["kind", "messageKey"],
    kind: "network",
    messageKey: "apiErrorNetwork",
  });

  const assertHostileProjectionSanitized = (state: unknown) => {
    const summary = errorSummary(state, source);
    assert.equal(summary.sourceIdentityRetained, false);
    assert.equal(summary.frozen, true);
    assert.deepEqual(summary.ownKeys, ["kind", "messageKey"]);
    assert.equal(summary.kind, "protocol");
    assert.equal(summary.messageKey, "apiErrorProtocol");
    assertNoMarkers(JSON.stringify(state), hostileMarkers);
  };
  assertOracleRejects(
    "return custom adapter result directly",
    assertHostileProjectionSanitized,
    result,
    { kind: "blocking-error", error: source },
  );
  assertOracleRejects(
    "copy arbitrary messageKey",
    assertHostileProjectionSanitized,
    result,
    { kind: "blocking-error", error: Object.freeze({ kind: "protocol", messageKey: source.messageKey }) },
  );
  assertOracleRejects(
    "copy token URL and stack fields",
    assertHostileProjectionSanitized,
    result,
    {
      kind: "blocking-error",
      error: Object.freeze({
        kind: "protocol",
        messageKey: "apiErrorProtocol",
        token: source.token,
        url: source.url,
        stack: source.stack,
      }),
    },
  );
});

test("aggregate projects complete, loading, blocking, partial, and nested states deterministically", async () => {
  const { aggregateRemoteState } = await loadAggregate();
  const combined = Object.freeze({ workers: ["w1"] });
  const classify = (value: { workers: readonly string[] }) => value.workers.length === 0 ? "empty" as const : "ready" as const;

  const readyResult = aggregateRemoteState({
    data: combined,
    sections: [
      section("workers", ready("w1", fresh(30))),
      section("health", ready("healthy", fresh(20))),
    ],
    classifyData: classify,
  });
  assert.deepEqual(readyResult, { kind: "ready", data: combined, freshness: { kind: "fresh", lastSuccessAt: 20 } });
  assert.equal(readyResult.kind === "ready" && readyResult.data === combined, true);

  const emptyData = Object.freeze({ workers: [] as readonly string[] });
  assert.deepEqual(aggregateRemoteState({
    data: emptyData,
    sections: [section("workers", empty(fresh(21))), section("health", empty(fresh(22)))],
    classifyData: classify,
  }), { kind: "empty", freshness: { kind: "fresh", lastSuccessAt: 21 } });

  assert.deepEqual(aggregateRemoteState({
    data: undefined,
    sections: [section("health", { kind: "initial-loading" }), section("workers", { kind: "initial-loading" })],
    classifyData: classify,
  }), { kind: "initial-loading" });

  assert.deepEqual(aggregateRemoteState({
    data: undefined,
    sections: [
      section("zeta", { kind: "blocking-error", error: unavailableError }),
      section("alpha", { kind: "blocking-error", error: networkError }),
      section("middle", { kind: "initial-loading" }),
    ],
    classifyData: classify,
  }), { kind: "blocking-error", error: networkError });

  const initialPartial = aggregateRemoteState({
    data: combined,
    sections: [section("workers", ready("w1", fresh(30))), section("health", { kind: "initial-loading" })],
    classifyData: classify,
  });
  assert.deepEqual(initialPartial, {
    kind: "partial",
    data: combined,
    missingSections: ["health"],
    sectionErrors: {},
    freshness: { kind: "fresh", lastSuccessAt: 30 },
  });

  const blockedPartial = aggregateRemoteState({
    data: combined,
    sections: [
      section("workers", ready("w1", fresh(30))),
      section("health", { kind: "blocking-error", error: unavailableError }),
    ],
    classifyData: classify,
  });
  assert.deepEqual(blockedPartial, {
    kind: "partial",
    data: combined,
    missingSections: ["health"],
    sectionErrors: { health: unavailableError },
    freshness: { kind: "fresh", lastSuccessAt: 30 },
  });

  const nested = aggregateRemoteState({
    data: combined,
    sections: [
      section("workers", {
        kind: "partial",
        data: "w1",
        missingSections: Object.freeze(["metrics", "version"]),
        sectionErrors: Object.freeze({ metrics: networkError }),
        freshness: fresh(18),
      }),
      section("health", ready("healthy", fresh(20))),
    ],
    classifyData: classify,
  });
  assert.deepEqual(nested, {
    kind: "partial",
    data: combined,
    missingSections: ["workers.metrics", "workers.version"],
    sectionErrors: { "workers.metrics": networkError },
    freshness: { kind: "fresh", lastSuccessAt: 18 },
  });
  if (nested.kind === "partial") {
    assert.equal(Object.isFrozen(nested), true);
    assert.equal(Object.isFrozen(nested.missingSections), true);
    assert.equal(Object.isFrozen(nested.sectionErrors), true);
    assert.equal(Object.isFrozen(nested.freshness), true);
  }

  const inheritedName = aggregateRemoteState({
    data: combined,
    sections: [
      section("workers", {
        kind: "partial",
        data: "w1",
        missingSections: Object.freeze(["constructor"]),
        sectionErrors: Object.freeze({}),
        freshness: fresh(18),
      }),
      section("health", ready("healthy", fresh(20))),
    ],
    classifyData: classify,
  });
  assert.equal(inheritedName.kind, "partial");
  if (inheritedName.kind === "partial") {
    assert.deepEqual(inheritedName.missingSections, ["workers.constructor"]);
    assert.deepEqual(inheritedName.sectionErrors, {});
    assert.equal(Object.hasOwn(inheritedName.sectionErrors, "workers.constructor"), false);
  }

  const prototypeName = aggregateRemoteState({
    data: combined,
    sections: [
      section("workers", ready("w1", fresh(30))),
      section("__proto__", { kind: "blocking-error", error: unavailableError }),
    ],
    classifyData: classify,
  });
  assert.equal(prototypeName.kind, "partial");
  if (prototypeName.kind === "partial") {
    assert.deepEqual(prototypeName.missingSections, ["__proto__"]);
    assert.equal(Object.hasOwn(prototypeName.sectionErrors, "__proto__"), true);
    assert.deepEqual(prototypeName.sectionErrors.__proto__, unavailableError);
    assert.notEqual(prototypeName.sectionErrors.__proto__, unavailableError);
    assert.equal(Object.isFrozen(prototypeName.sectionErrors.__proto__), true);
  }
});

test("aggregate freshness uses minimum time and stale then refreshing priority", async () => {
  const { aggregateRemoteState } = await loadAggregate();
  const data = Object.freeze({ value: 1 });
  const classify = () => "ready" as const;
  const staleResult = aggregateRemoteState({
    data,
    sections: [
      section("zeta", ready("z", stale(40, unavailableError))),
      section("middle", ready("m", refreshing(10))),
      section("alpha", ready("a", stale(30, networkError))),
      section("fresh", ready("f", fresh(20))),
    ],
    classifyData: classify,
  });
  assert.deepEqual(staleResult, {
    kind: "ready",
    data,
    freshness: { kind: "stale", lastSuccessAt: 10, error: networkError },
  });

  const refreshingResult = aggregateRemoteState({
    data,
    sections: [section("fresh", ready("f", fresh(20))), section("refresh", ready("r", refreshing(12)))],
    classifyData: classify,
  });
  assert.deepEqual(refreshingResult, {
    kind: "ready",
    data,
    freshness: { kind: "refreshing", lastSuccessAt: 12 },
  });
});

test("aggregate is total and fails closed for malformed IDs, metadata, and inconsistent data", async () => {
  const aggregate = await loadAggregate();
  const classify = () => "ready" as const;
  const malformedPartial = {
    kind: "partial",
    data: "cached",
    missingSections: ["metrics", "metrics"],
    sectionErrors: {},
    freshness: fresh(1),
  };
  const throwingSection = new Proxy({ id: "workers", state: ready("w", fresh(1)) }, {
    get() {
      throw new Error("hostile section");
    },
  });
  const cases: readonly [string, unknown][] = [
    ["empty section list", { data: {}, sections: [], classifyData: classify }],
    ["duplicate ID", { data: {}, sections: [section("workers", ready("w", fresh(1))), section("workers", ready("w", fresh(2)))], classifyData: classify }],
    ["uppercase ID", { data: {}, sections: [section("Workers", ready("w", fresh(1)))], classifyData: classify }],
    ["oversized ID", { data: {}, sections: [section("x".repeat(65), ready("w", fresh(1)))], classifyData: classify }],
    ["malformed partial metadata", { data: {}, sections: [section("workers", malformedPartial)], classifyData: classify }],
    ["orphan nested error", { data: {}, sections: [section("workers", { ...malformedPartial, missingSections: ["metrics"], sectionErrors: { version: networkError } })], classifyData: classify }],
    ["combined data without freshness", { data: {}, sections: [section("workers", { kind: "initial-loading" })], classifyData: classify }],
    ["undefined combined data is not empty", { data: undefined, sections: [section("workers", ready("w", fresh(1)))], classifyData: classify }],
    ["undefined ready section data", { data: {}, sections: [section("workers", { kind: "ready", data: undefined, freshness: fresh(1) })], classifyData: classify }],
    ["undefined partial section data", { data: {}, sections: [section("workers", { kind: "partial", data: undefined, missingSections: ["health"], sectionErrors: {}, freshness: fresh(1) })], classifyData: classify }],
    ["classifier throws", { data: {}, sections: [section("workers", ready("w", fresh(1)))], classifyData: () => { throw new Error("classifier"); } }],
    ["throwing section", { data: {}, sections: [throwingSection], classifyData: classify }],
  ];
  for (const [label, input] of cases) {
    let result: unknown;
    assert.doesNotThrow(() => {
      result = aggregateRuntime(aggregate, input);
    }, label);
    assertProtocolBlocking(result, label);
  }
});

test("aggregate rejects non-plain section error metadata without silently dropping entries", async () => {
  const aggregate = await loadAggregate();
  const combined = Object.freeze({ workers: ["w1"] });
  const aggregateWith = (sectionErrors: unknown) => {
    let result: unknown;
    assert.doesNotThrow(() => {
      result = aggregateRuntime(aggregate, {
        data: combined,
        sections: [{
          id: "workers",
          state: {
            kind: "partial",
            data: "w1",
            missingSections: ["section-a"],
            sectionErrors,
            freshness: fresh(10),
          },
        }],
        classifyData: () => "ready",
      });
    });
    return result;
  };
  const isProtocolFallback = (state: unknown) => {
    if (!isObjectLike(state) || Reflect.get(state, "kind") !== "blocking-error") return false;
    const error = Reflect.get(state, "error");
    return isObjectLike(error)
      && Reflect.get(error, "kind") === "protocol"
      && Reflect.get(error, "messageKey") === "apiErrorProtocol";
  };
  const sectionErrorOf = (state: unknown, key: string) => {
    if (!isObjectLike(state) || Reflect.get(state, "kind") !== "partial") return undefined;
    const errors = Reflect.get(state, "sectionErrors");
    return isObjectLike(errors) && Object.hasOwn(errors, key) ? Reflect.get(errors, key) : undefined;
  };

  const mapMetadata = new Map([["section-a", networkError]]);
  const mapResult = aggregateWith(mapMetadata);
  const mapErrors = isObjectLike(mapResult) && Reflect.get(mapResult, "kind") === "partial"
    ? Reflect.get(mapResult, "sectionErrors")
    : undefined;
  assert.deepEqual({
    kind: isObjectLike(mapResult) ? Reflect.get(mapResult, "kind") : undefined,
    errorKind: isObjectLike(mapResult) && isObjectLike(Reflect.get(mapResult, "error"))
      ? Reflect.get(Reflect.get(mapResult, "error"), "kind")
      : undefined,
    coverage: {
      complete: isObjectLike(mapResult) && ["empty", "ready"].includes(String(Reflect.get(mapResult, "kind"))),
      knownSectionIds: isObjectLike(mapErrors) ? Reflect.ownKeys(mapErrors).filter((key) => typeof key === "string").sort() : [],
    },
    mapEntrySilentlyDropped: isObjectLike(mapResult)
      && Reflect.get(mapResult, "kind") === "partial"
      && isObjectLike(mapErrors)
      && Reflect.ownKeys(mapErrors).length === 0,
  }, {
    kind: "blocking-error",
    errorKind: "protocol",
    coverage: { complete: false, knownSectionIds: [] },
    mapEntrySilentlyDropped: false,
  });

  class SectionErrorRecord {
    readonly ["section-a"] = networkError;
  }
  const symbolRecord = { "section-a": networkError };
  Object.defineProperty(symbolRecord, Symbol("hidden"), { value: networkError, enumerable: true });
  const accessorRecord = {};
  Object.defineProperty(accessorRecord, "section-a", {
    enumerable: true,
    get: () => networkError,
  });
  const throwingProxy = new Proxy({ "section-a": networkError }, {
    ownKeys() {
      throw new Error("metadata ownKeys failure");
    },
  });
  const revoked = Proxy.revocable({ "section-a": networkError }, {});
  revoked.revoke();
  const invalidRecords = {
    map: mapMetadata,
    set: new Set([networkError]),
    date: new Date(0),
    regexp: /section-a/,
    typedArray: new Uint8Array(0),
    urlSearchParams: new URLSearchParams([["section-a", "network"]]),
    classInstance: new SectionErrorRecord(),
    array: [networkError],
    functionValue: () => networkError,
    symbolProperty: symbolRecord,
    accessorProperty: accessorRecord,
    throwingProxy,
    revokedProxy: revoked.proxy,
  };
  assert.deepEqual(Object.fromEntries(Object.entries(invalidRecords).map(([name, value]) => [
    name,
    isProtocolFallback(aggregateWith(value)),
  ])), Object.fromEntries(Object.keys(invalidRecords).map((name) => [name, true])));

  const nullPrototypeRecord: Record<string, unknown> = Object.create(null);
  Object.defineProperty(nullPrototypeRecord, "section-a", {
    value: networkError,
    enumerable: true,
    configurable: true,
    writable: true,
  });
  const nullPrototypeResult = aggregateWith(nullPrototypeRecord);
  const copiedError = sectionErrorOf(nullPrototypeResult, "workers.section-a");
  assert.deepEqual({
    kind: isObjectLike(nullPrototypeResult) ? Reflect.get(nullPrototypeResult, "kind") : undefined,
    sourceIdentityRetained: copiedError === networkError,
    errorFrozen: isObjectLike(copiedError) && Object.isFrozen(copiedError),
    errorOwnKeys: isObjectLike(copiedError) ? Reflect.ownKeys(copiedError).map(String).sort() : [],
    errorKind: isObjectLike(copiedError) ? Reflect.get(copiedError, "kind") : undefined,
    errorMessageKey: isObjectLike(copiedError) ? Reflect.get(copiedError, "messageKey") : undefined,
  }, {
    kind: "partial",
    sourceIdentityRetained: false,
    errorFrozen: true,
    errorOwnKeys: ["kind", "messageKey"],
    errorKind: "network",
    errorMessageKey: "apiErrorNetwork",
  });

  const silentlyDroppedMapMutation = {
    kind: "partial",
    data: combined,
    missingSections: ["workers.section-a"],
    sectionErrors: {},
    freshness: fresh(10),
  };
  const assertMalformedMetadataRejected = (state: unknown) => {
    assert.equal(isProtocolFallback(state), true);
  };
  assertOracleRejects(
    "accept any object as sectionErrors",
    assertMalformedMetadataRejected,
    aggregateWith(new SectionErrorRecord()),
    silentlyDroppedMapMutation,
  );
  assertOracleRejects(
    "accept Map through Object.entries",
    assertMalformedMetadataRejected,
    mapResult,
    silentlyDroppedMapMutation,
  );
  assertOracleRejects(
    "silently drop malformed Map and continue partial",
    assertMalformedMetadataRejected,
    mapResult,
    silentlyDroppedMapMutation,
  );
});

test("coverage excludes unknown and malformed contributions from known and positive counts", async () => {
  const aggregate = await loadAggregate();
  const hostile = new Proxy({ kind: "known", positive: true }, {
    get() {
      throw new Error("hostile coverage");
    },
  });
  const hostileArray = new Proxy([{ kind: "unknown" }], {
    get(target, key, receiver) {
      if (key === "0") throw new Error("hostile contribution slot");
      return Reflect.get(target, key, receiver);
    },
  });
  const cases: readonly [string, readonly unknown[], Readonly<Record<string, number>>][] = [
    ["known positive", [{ kind: "known", positive: true }], counts(1, 1, 1, 0, 0)],
    ["known negative", [{ kind: "known", positive: false }], counts(1, 1, 0, 1, 0)],
    ["unknown", [{ kind: "unknown" }], counts(1, 0, 0, 0, 1)],
    ["mixed", [{ kind: "known", positive: true }, { kind: "known", positive: false }, { kind: "unknown" }], counts(3, 2, 1, 1, 1)],
    ["all unknown", [{ kind: "unknown" }, { kind: "unknown" }], counts(2, 0, 0, 0, 2)],
    ["malformed", [{ kind: "known" }, { kind: "known", positive: "yes" }, null, hostile], counts(4, 0, 0, 0, 4)],
    ["hostile array slot", hostileArray, counts(1, 0, 0, 0, 1)],
  ];
  for (const [label, contributions, expected] of cases) {
    let summary: unknown;
    assert.doesNotThrow(() => {
      summary = coverageRuntime(aggregate, contributions);
    }, label);
    assert.deepEqual(summary, expected, label);
    assert.equal(Object.isFrozen(summary), true, label);
    assert.deepEqual(Object.keys(expected), ["totalCount", "knownCount", "positiveCount", "negativeCount", "unknownCount"]);
  }
});

test("boundary renders initial, empty, and all ready/partial freshness combinations without replacing data", async () => {
  const [{ RemoteStateBoundary }, i18n] = await Promise.all([loadBoundary(), loadI18n()]);
  let loadingDataCalls = 0;
  const initialMarkup = renderBoundary(RemoteStateBoundary, {
    state: { kind: "initial-loading" },
    ...boundaryCallbacks(i18n, () => {
      loadingDataCalls += 1;
      return createElement("strong", null, "unexpected data");
    }),
  });
  assert.match(initialMarkup, /data-remote-state="initial-loading"/);
  assert.match(initialMarkup, /aria-busy="true"/);
  assert.equal(initialMarkup.includes("Loading data."), true);
  assert.equal(loadingDataCalls, 0);

  const freshnessCases = [fresh(10), refreshing(11), stale(12, networkError)] as const;
  for (const stateKind of ["empty", "ready", "partial"] as const) {
    for (const freshness of freshnessCases) {
      let dataCalls = 0;
      let emptyCalls = 0;
      const state: RemoteState<string> = stateKind === "empty"
        ? { kind: "empty", freshness }
        : stateKind === "ready"
          ? { kind: "ready", data: "CACHED_CONTENT", freshness }
          : {
              kind: "partial",
              data: "CACHED_CONTENT",
              missingSections: Object.freeze(["private.section"]),
              sectionErrors: Object.freeze({ "private.section": networkError }),
              freshness,
            };
      const markup = renderBoundary(RemoteStateBoundary, {
        state,
        noticeId: `${stateKind}-${freshness.kind}-notice`,
        translate: english(i18n),
        formatTimestamp: (timestamp) => `T${timestamp}`,
        renderData: (data, context) => {
          dataCalls += 1;
          assert.equal(context.stateKind, stateKind);
          assert.equal(context.freshness, freshness);
          assert.equal(context.missingSections.length, stateKind === "partial" ? 1 : 0);
          assert.equal(context.noticeId, stateKind === "partial" || freshness.kind !== "fresh" ? `${stateKind}-${freshness.kind}-notice` : undefined);
          return createElement("strong", null, data);
        },
        renderEmpty: () => {
          emptyCalls += 1;
          return createElement("em", null, "EMPTY_CONTENT");
        },
      });
      assert.match(markup, new RegExp(`data-remote-state="${stateKind}"`));
      assert.match(markup, new RegExp(`data-remote-freshness="${freshness.kind}"`));
      assert.equal(markup.includes(stateKind === "empty" ? "EMPTY_CONTENT" : "CACHED_CONTENT"), true);
      assert.equal(dataCalls, stateKind === "empty" ? 0 : 1);
      assert.equal(emptyCalls, stateKind === "empty" ? 1 : 0);
      assert.equal(markup.includes("private.section"), false);
      if (stateKind === "partial") {
        assert.equal(markup.includes("Some information could not be verified."), true);
        assert.match(markup, /data-remote-missing-count="1"/);
      } else {
        assert.equal(markup.includes("Some information could not be verified."), false);
      }
      if (freshness.kind === "refreshing") {
        assert.equal(markup.includes("Refreshing data."), true);
        assert.match(markup, new RegExp(`aria-describedby="${stateKind}-${freshness.kind}-notice"`));
      } else if (freshness.kind === "stale") {
        assert.equal(markup.includes("Showing the last successful data"), true);
        assert.equal(markup.includes(`Last successful update: T${freshness.lastSuccessAt}`), true);
        assert.equal(markup.includes("Could not connect to the service."), true);
        assert.match(markup, new RegExp(`aria-describedby="${stateKind}-${freshness.kind}-notice"`));
      }
    }
  }
});

test("blocking and stale rendering disclose only translated safe errors", async () => {
  const [{ projectRemoteState }, { RemoteStateBoundary }, i18n] = await Promise.all([
    loadProjector(),
    loadBoundary(),
    loadI18n(),
  ]);
  const hostile = Object.freeze({
    message: "HOSTILE_MESSAGE",
    body: "HOSTILE_BODY",
    URL: "HOSTILE_URL",
    stack: "HOSTILE_STACK",
    token: "HOSTILE_TOKEN",
  });
  const options = { classifyData: (data: string) => data === "" ? "empty" as const : "ready" as const };
  const blocking = projectRemoteState({ status: "error", fetching: false, error: hostile }, options);
  let blockingDataCalls = 0;
  const blockingMarkup = renderBoundary(RemoteStateBoundary, {
    state: blocking,
    noticeId: "blocking-notice",
    translate: english(i18n),
    formatTimestamp: String,
    renderData: () => {
      blockingDataCalls += 1;
      return "hidden";
    },
  });
  assert.match(blockingMarkup, /data-remote-state="blocking-error"/);
  assert.match(blockingMarkup, /role="alert"/);
  assert.equal(blockingMarkup.includes("The data could not be loaded."), true);
  assert.equal(blockingMarkup.includes("The operation could not be completed."), true);
  assert.equal(blockingDataCalls, 0);
  assertNoMarkers(blockingMarkup, Object.values(hostile));

  const cached = projectRemoteState({
    status: "error",
    fetching: true,
    error: hostile,
    data: "CACHED_SAFE_DATA",
    dataUpdatedAt: 44,
  }, options);
  const staleMarkup = renderBoundary(RemoteStateBoundary, {
    state: cached,
    noticeId: "stale-notice",
    translate: english(i18n),
    formatTimestamp: (timestamp) => `safe-${timestamp}`,
    renderData: (data) => createElement("strong", null, data),
  });
  assert.equal(staleMarkup.includes("CACHED_SAFE_DATA"), true);
  assert.equal(staleMarkup.includes("Showing the last successful data"), true);
  assertNoMarkers(staleMarkup, Object.values(hostile));
});

test("timestamp formatter failure omits only the timestamp and keeps stale content and reason", async () => {
  const [{ RemoteStateBoundary }, i18n] = await Promise.all([loadBoundary(), loadI18n()]);
  const state: RemoteState<string> = { kind: "ready", data: "CACHED", freshness: stale(55, networkError) };
  const throwingMarkup = renderBoundary(RemoteStateBoundary, {
    state,
    noticeId: "throwing-time",
    translate: english(i18n),
    formatTimestamp: () => { throw new Error("formatter failed"); },
    renderData: (data) => data,
  });
  assert.equal(throwingMarkup.includes("CACHED"), true);
  assert.equal(throwingMarkup.includes("Showing the last successful data"), true);
  assert.equal(throwingMarkup.includes("Last successful update"), false);

  const nonStringFormatter = new Proxy((timestamp: number) => String(timestamp), {
    apply: () => 42,
  });
  const nonStringMarkup = renderBoundary(RemoteStateBoundary, {
    state,
    noticeId: "non-string-time",
    translate: english(i18n),
    formatTimestamp: nonStringFormatter,
    renderData: (data) => data,
  });
  assert.equal(nonStringMarkup.includes("CACHED"), true);
  assert.equal(nonStringMarkup.includes("Showing the last successful data"), true);
  assert.equal(nonStringMarkup.includes("Last successful update"), false);
});

test("manual retry is inert on render, single-shot per click, and disabled while pending", async () => {
  const [{ RemoteStateNotice }, i18n] = await Promise.all([loadNotice(), loadI18n()]);
  let retryCalls = 0;
  const props: RemoteStateNoticeProps = {
    kind: "content",
    noticeId: "retry-notice",
    freshness: stale(60, networkError),
    translate: english(i18n),
    formatTimestamp: String,
    onRetry: () => {
      retryCalls += 1;
    },
  };
  const notice = RemoteStateNotice(props);
  assert.equal(retryCalls, 0);
  const button = requireButton(notice);
  assert.equal(button.props.disabled, undefined);
  assert.equal(button.props["aria-describedby"], "retry-notice");
  button.props.onClick?.();
  assert.equal(retryCalls, 1);

  const pendingNotice = RemoteStateNotice({ ...props, retryPending: true });
  assert.equal(retryCalls, 1);
  const pendingButton = requireButton(pendingNotice);
  assert.equal(pendingButton.props.disabled, true);
  assert.equal(pendingButton.props["aria-disabled"], true);
  assert.equal(pendingButton.props["aria-busy"], true);
  pendingButton.props.onClick?.();
  assert.equal(retryCalls, 1);
});

test("retry display state and unrelated mutation pending never erase cached query data", async () => {
  const [{ RemoteStateBoundary }, i18n] = await Promise.all([loadBoundary(), loadI18n()]);
  const queryState = Object.freeze({
    kind: "ready" as const,
    data: Object.freeze({ value: "QUERY_DATA" }),
    freshness: stale(70, unavailableError),
  });
  const unrelatedMutation = Object.freeze({ kind: "pending" as const });
  const markup = renderBoundary(RemoteStateBoundary, {
    state: queryState,
    noticeId: "mutation-separated",
    translate: english(i18n),
    formatTimestamp: String,
    renderData: (data) => data.value,
    onRetry: () => undefined,
    retryPending: true,
  });
  assert.equal(unrelatedMutation.kind, "pending");
  assert.equal(markup.includes("QUERY_DATA"), true);
  assert.match(markup, /data-remote-state="ready"/);
  assert.deepEqual(queryState, {
    kind: "ready",
    data: { value: "QUERY_DATA" },
    freshness: { kind: "stale", lastSuccessAt: 70, error: unavailableError },
  });
});

test("stale and partial reasons have one DOM target and no assertive polling live region", async () => {
  const [{ RemoteStateBoundary }, i18n] = await Promise.all([loadBoundary(), loadI18n()]);
  const state: RemoteState<string> = {
    kind: "partial",
    data: "VISIBLE_DATA",
    missingSections: ["secret.section"],
    sectionErrors: { "secret.section": networkError },
    freshness: stale(80, unavailableError),
  };
  for (let iteration = 0; iteration < 3; iteration += 1) {
    const markup = renderBoundary(RemoteStateBoundary, {
      state,
      noticeId: "combined-notice",
      translate: english(i18n),
      formatTimestamp: String,
      renderData: (data) => data,
    });
    assert.equal(countOccurrences(markup, 'id="combined-notice"'), 1);
    assert.equal(markup.includes('aria-describedby="combined-notice"'), true);
    assert.equal(markup.includes('aria-live="assertive"'), false);
    assert.equal(markup.includes('role="alert"'), false);
    assert.equal(markup.includes("autofocus"), false);
    assert.equal(markup.includes("secret.section"), false);
  }
});

test("ja/en remote-state copy is exact, parallel, and placeholder-safe", async () => {
  const { translations, translate } = await loadI18n();
  const expected = {
    ja: {
      remoteStateLoading: "情報を読み込んでいます。",
      remoteStateEmpty: "表示するデータがありません。",
      remoteStateRefreshing: "情報を更新しています。",
      remoteStateStale: "最新情報を取得できないため、最後に取得したデータを表示しています。",
      remoteStatePartial: "一部の情報を確認できません。",
      remoteStateBlockingError: "情報を取得できませんでした。",
      remoteStateRetry: "再試行",
      remoteStateLastSuccessfulAt: "最終取得: {time}",
    },
    en: {
      remoteStateLoading: "Loading data.",
      remoteStateEmpty: "There is no data to display.",
      remoteStateRefreshing: "Refreshing data.",
      remoteStateStale: "Showing the last successful data because the latest refresh failed.",
      remoteStatePartial: "Some information could not be verified.",
      remoteStateBlockingError: "The data could not be loaded.",
      remoteStateRetry: "Retry",
      remoteStateLastSuccessfulAt: "Last successful update: {time}",
    },
  } as const;
  assert.deepEqual(Object.keys(expected.ja), Object.keys(expected.en));
  for (const locale of ["ja", "en"] as const) {
    for (const key of Object.keys(expected[locale]) as (keyof typeof expected.ja)[]) {
      assert.equal(translations[locale][key], expected[locale][key], `${locale}.${key}`);
      const values = key === "remoteStateLastSuccessfulAt" ? { time: "T1" } : undefined;
      assert.equal(translate(locale, key, values), values ? expected[locale][key].replace("{time}", "T1") : expected[locale][key]);
      assert.equal(new Set(expected[locale][key].match(/\{[a-zA-Z0-9_]+\}/g) || []).size, key === "remoteStateLastSuccessfulAt" ? 1 : 0);
    }
  }
});

test("mutation-sensitive oracles reject every required incorrect alternate", async () => {
  const [projector, aggregate, { RemoteStateBoundary }, { RemoteStateNotice }, i18n] = await Promise.all([
    loadProjector(),
    loadAggregate(),
    loadBoundary(),
    loadNotice(),
    loadI18n(),
  ]);
  const options = { classifyData: (data: readonly string[]) => data.length === 0 ? "empty" as const : "ready" as const, adaptError: () => networkError };

  const initial = projector.projectRemoteState({ status: "pending", fetching: true }, options);
  assertOracleRejects("pending -> empty", assertInitial, initial, { kind: "empty", freshness: fresh(0) });

  const cached = Object.freeze(["cached"]);
  const cachedState = projector.projectRemoteState({ status: "error", fetching: false, error: new Error("raw"), data: cached, dataUpdatedAt: 1 }, options);
  assertOracleRejects("cached error -> blocking", (value) => assertCachedVisible(value, cached), cachedState, { kind: "blocking-error", error: networkError });

  const partial = aggregate.aggregateRemoteState({
    data: cached,
    sections: [section("data", ready(cached, fresh(1))), section("missing", { kind: "initial-loading" })],
    classifyData: () => "ready",
  });
  assertOracleRejects("partial -> ready", assertPartial, partial, { kind: "ready", data: cached, freshness: fresh(1) });

  const staleState = projector.projectRemoteState({ status: "error", fetching: true, error: new Error("raw"), data: cached, dataUpdatedAt: 1 }, options);
  assertOracleRejects(
    "refreshing overrides stale",
    assertStale,
    staleState,
    { kind: "ready", data: cached, freshness: refreshing(1) },
  );

  assertOracleRejects(
    "missing section omitted",
    (value) => assertMissing(value, "missing"),
    partial,
    { kind: "partial", data: cached, missingSections: [], sectionErrors: {}, freshness: fresh(1) },
  );

  const unknownSummary = aggregate.summarizeRemoteCoverage([{ kind: "unknown" }]);
  assertOracleRejects(
    "unknown coverage counted as known",
    assertUnknownExcluded,
    unknownSummary,
    { totalCount: 1, knownCount: 1, positiveCount: 1, negativeCount: 0, unknownCount: 0 },
  );

  const retryMarkup = renderBoundary(RemoteStateBoundary, {
    state: staleState,
    noticeId: "oracle-notice",
    translate: english(i18n),
    formatTimestamp: String,
    renderData: () => "ORACLE_CACHED_DATA",
    onRetry: () => undefined,
    retryPending: true,
  });
  assertOracleRejects(
    "data hidden when retryPending",
    (value) => assertMarkupContains(value, "ORACLE_CACHED_DATA"),
    retryMarkup,
    retryMarkup.replace("ORACLE_CACHED_DATA", ""),
  );
  assertOracleRejects(
    "aria-describedby removed",
    (value) => assertMarkupContains(value, 'aria-describedby="oracle-notice"'),
    retryMarkup,
    retryMarkup.replaceAll('aria-describedby="oracle-notice"', ""),
  );
  assertOracleRejects(
    "raw hostile error rendered",
    (value) => assertMarkupOmits(value, "RAW_HOSTILE_MESSAGE"),
    retryMarkup,
    `${retryMarkup}RAW_HOSTILE_MESSAGE`,
  );

  const noticeProps: RemoteStateNoticeProps = {
    kind: "content",
    noticeId: "oracle-retry",
    freshness: stale(1, networkError),
    translate: english(i18n),
    formatTimestamp: String,
  };
  assert.doesNotThrow(() => assertRetryNotInvokedDuringRender((onRetry) => {
    RemoteStateNotice({ ...noticeProps, onRetry });
  }));
  assert.throws(() => assertRetryNotInvokedDuringRender((onRetry) => {
    onRetry();
    RemoteStateNotice({ ...noticeProps, onRetry });
  }), undefined, "retry invoked during render mutation must be detected");
});

test("new modules preserve strict import, ownership, consumer, and dependency boundaries", () => {
  assert.deepEqual(assertRemoteStateFoundationBoundaries(webRoot), {
    componentRuntimeImportCount: 3,
    productionConsumerCount: 1,
    productionFileCount: 5,
    pureReactImportCount: 0,
  });
});

function fresh(lastSuccessAt: number): Freshness {
  return { kind: "fresh", lastSuccessAt };
}

function refreshing(lastSuccessAt: number): Freshness {
  return { kind: "refreshing", lastSuccessAt };
}

function stale(lastSuccessAt: number, error: AdaptedAPIError): Freshness {
  return { kind: "stale", lastSuccessAt, error };
}

function ready<T>(data: T, freshness: Freshness): RemoteState<T> {
  return { kind: "ready", data, freshness };
}

function empty(freshness: Freshness): RemoteState<unknown> {
  return { kind: "empty", freshness };
}

function section(id: string, state: RemoteState<unknown>): RemoteSectionState {
  return { id, state };
}

function counts(totalCount: number, knownCount: number, positiveCount: number, negativeCount: number, unknownCount: number) {
  return { totalCount, knownCount, positiveCount, negativeCount, unknownCount };
}

function projectRuntime(module: ProjectorModule, snapshot: unknown, options: unknown): unknown {
  return Reflect.apply(module.projectRemoteState, undefined, [snapshot, options]);
}

function aggregateRuntime(module: AggregateModule, input: unknown): unknown {
  return Reflect.apply(module.aggregateRemoteState, undefined, [input]);
}

function coverageRuntime(module: AggregateModule, contributions: readonly unknown[]): unknown {
  return Reflect.apply(module.summarizeRemoteCoverage, undefined, [contributions]);
}

function assertProtocolBlocking(value: unknown, label: string) {
  assert.deepEqual(value, { kind: "blocking-error", error: protocolError }, label);
  assert.equal(Object.isFrozen(value), true, `${label} wrapper`);
}

function boundaryCallbacks<T>(i18n: I18nModule, renderData: RemoteStateBoundaryProps<T>["renderData"]): Omit<RemoteStateBoundaryProps<T>, "state"> {
  return {
    noticeId: "remote-state-notice",
    translate: english(i18n),
    formatTimestamp: String,
    renderData,
  };
}

function english(i18n: I18nModule) {
  return (key: TranslationKey, values?: TranslationValues) => i18n.translate("en", key, values);
}

function renderBoundary<T>(
  Boundary: BoundaryModule["RemoteStateBoundary"],
  props: RemoteStateBoundaryProps<T>,
) {
  return renderToStaticMarkup(createElement(Boundary, props));
}

type ButtonProps = Readonly<{
  children?: ReactNode;
  disabled?: boolean;
  onClick?: () => void;
  "aria-busy"?: boolean;
  "aria-describedby"?: string;
  "aria-disabled"?: boolean;
}>;

function requireButton(node: ReactNode): ReactElement<ButtonProps> {
  const button = findButton(node);
  assert.ok(button, "retry button is missing");
  return button;
}

function findButton(node: ReactNode): ReactElement<ButtonProps> | undefined {
  for (const child of Children.toArray(node)) {
    if (!isValidElement<ButtonProps>(child)) continue;
    if (child.type === "button") return child;
    const nested = findButton(child.props.children);
    if (nested) return nested;
  }
  return undefined;
}

function assertNoMarkers(markup: string, markers: readonly string[]) {
  for (const marker of markers) assert.equal(markup.includes(marker), false, marker);
}

function countOccurrences(value: string, needle: string) {
  return value.split(needle).length - 1;
}

function assertOracleRejects(
  label: string,
  oracle: (value: unknown) => void,
  real: unknown,
  mutated: unknown,
) {
  assert.doesNotThrow(() => oracle(real), `${label} real implementation`);
  assert.throws(() => oracle(mutated), undefined, `${label} mutation must be detected`);
}

function assertInitial(value: unknown) {
  assert.deepEqual(value, { kind: "initial-loading" });
}

function assertCachedVisible(value: unknown, expectedData: unknown) {
  assert.equal(isObjectLike(value) && Reflect.get(value, "kind"), "ready");
  assert.equal(isObjectLike(value) && Reflect.get(value, "data"), expectedData);
}

function assertPartial(value: unknown) {
  assert.equal(isObjectLike(value) && Reflect.get(value, "kind"), "partial");
}

function assertStale(value: unknown) {
  assert.equal(isObjectLike(value) && isObjectLike(Reflect.get(value, "freshness")) && Reflect.get(Reflect.get(value, "freshness"), "kind"), "stale");
}

function assertMissing(value: unknown, expected: string) {
  assert.equal(isObjectLike(value), true);
  if (!isObjectLike(value)) return;
  const missing = Reflect.get(value, "missingSections");
  assert.equal(Array.isArray(missing) && missing.includes(expected), true);
}

function assertUnknownExcluded(value: unknown) {
  assert.equal(isObjectLike(value), true);
  if (!isObjectLike(value)) return;
  assert.equal(Reflect.get(value, "knownCount"), 0);
  assert.equal(Reflect.get(value, "positiveCount"), 0);
  assert.equal(Reflect.get(value, "unknownCount"), 1);
}

function assertMarkupContains(value: unknown, expected: string) {
  assert.equal(typeof value === "string" && value.includes(expected), true);
}

function assertMarkupOmits(value: unknown, marker: string) {
  assert.equal(typeof value === "string" && value.includes(marker), false);
}

function assertRetryNotInvokedDuringRender(render: (onRetry: () => void) => void) {
  let calls = 0;
  render(() => {
    calls += 1;
  });
  assert.equal(calls, 0);
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}
