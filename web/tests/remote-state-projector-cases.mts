import assert from "node:assert/strict";
import { createElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import type { RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import type { ProjectRemoteStateOptions, QueryProjectionSnapshot } from "../src/lib/foundation/remote-state/projector.ts";
import { loadProjector, networkError, projectRuntime, assertProtocolBlocking, loadBoundary, loadI18n, isObjectLike, english, assertNoMarkers, assertOracleRejects } from "./remote-state-fixture.mts";


export function registerRemoteProjectorCases() {


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
}
