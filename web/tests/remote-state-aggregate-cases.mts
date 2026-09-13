import assert from "node:assert/strict";
import test from "node:test";
import { loadAggregate, section, ready, fresh, empty, unavailableError, networkError, stale, refreshing, aggregateRuntime, assertProtocolBlocking, isObjectLike, assertOracleRejects, counts, coverageRuntime } from "./remote-state-fixture.mts";


export function registerRemoteAggregateCases() {


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
}
