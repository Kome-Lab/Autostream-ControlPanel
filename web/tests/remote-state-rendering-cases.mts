import assert from "node:assert/strict";
import { createElement } from "react";
import test from "node:test";
import type { RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import type { RemoteStateNoticeProps } from "../src/components/foundation/remote-state/remote-state-notice.ts";
import { assertRemoteStateFoundationBoundaries } from "./helpers/ui-foundation-remote-state-imports.mts";
import { loadBoundary, loadI18n, renderBoundary, boundaryCallbacks, fresh, refreshing, stale, networkError, english, loadProjector, assertNoMarkers, loadNotice, requireButton, unavailableError, countOccurrences, loadAggregate, assertOracleRejects, assertInitial, assertCachedVisible, section, ready, assertPartial, assertStale, assertMissing, assertUnknownExcluded, assertMarkupContains, assertMarkupOmits, assertRetryNotInvokedDuringRender, webRoot } from "./remote-state-fixture.mts";


export function registerRemoteRenderingCases() {


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
    productionConsumerCount: 5,
    productionFileCount: 5,
    pureReactImportCount: 0,
  });
});
}
