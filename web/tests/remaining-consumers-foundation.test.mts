import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import test from "node:test";
import { readMovedSource } from "./helpers/moved-source.mts";

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
  aggregateRemainingQueries,
  remainingConsumerManifest,
  remainingStatePresentation,
} = await import("../src/features/remote-state/remaining-remote-state.ts");

type Consumer = keyof typeof remainingConsumerManifest;
type Snapshot = Readonly<{
  status: "pending" | "success" | "error";
  isFetching: boolean;
  data?: readonly unknown[];
  error?: unknown;
  dataUpdatedAt: number;
}>;

const consumers = Object.keys(remainingConsumerManifest) as Consumer[];

test("Wave 3C remote-state manifest is finite and covers the five authorized consumers", () => {
  assert.deepEqual(remainingConsumerManifest, {
    audit: ["audit-logs"],
    workers: ["workers", "registered-nodes", "service-health"],
    nodes: ["registered-nodes"],
    archive: ["streams", "processing-streams"],
    application: ["settings"],
  });
  assert.deepEqual(consumers, ["audit", "workers", "nodes", "archive", "application"]);
});

test("every missing and failed section is fail-safe instead of being coerced to zero", () => {
  for (const consumer of consumers) {
    const sections = remainingConsumerManifest[consumer];
    for (const target of sections) {
      const missingQueries = Object.fromEntries(sections.filter((id) => id !== target).map((id) => [id, success([id])]));
      const missing = aggregateRemainingQueries(consumer, missingQueries);
      if (sections.length === 1) {
        assert.equal(missing.kind, "initial-loading", `${consumer}/${target} missing`);
      } else {
        assert.equal(missing.kind, "partial", `${consumer}/${target} missing`);
        assert.deepEqual(missing.kind === "partial" ? missing.missingSections : [], [target]);
      }

      const failedQueries = Object.fromEntries(sections.map((id) => [id, id === target ? failed() : success([id])]));
      const failedState = aggregateRemainingQueries(consumer, failedQueries);
      if (sections.length === 1) {
        assert.equal(failedState.kind, "blocking-error", `${consumer}/${target} failed`);
      } else {
        assert.equal(failedState.kind, "partial", `${consumer}/${target} failed`);
        assert.deepEqual(failedState.kind === "partial" ? failedState.missingSections : [], [target]);
      }
    }
  }
});

test("cached refresh failures remain visible and partial plus stale is explicitly representable", () => {
  for (const consumer of consumers) {
    const sections = remainingConsumerManifest[consumer];
    for (const target of sections) {
      const queries = Object.fromEntries(sections.map((id) => [id, id === target ? stale([`${id}-cached`]) : success([id])]));
      const state = aggregateRemainingQueries(consumer, queries);
      assert.ok(state.kind === "ready" || state.kind === "partial", `${consumer}/${target} retains data`);
      if (state.kind === "ready" || state.kind === "partial") assert.equal(state.freshness.kind, "stale", `${consumer}/${target} stale`);
      assert.match(JSON.stringify(state), /cached/);
      assert.doesNotMatch(JSON.stringify(state), /HOSTILE_REMOTE_ERROR_MARKER/);
    }
  }

  const partialStale = aggregateRemainingQueries("archive", {
    streams: stale(["cached-stream"]),
    "processing-streams": failed(),
  });
  assert.equal(partialStale.kind, "partial");
  if (partialStale.kind === "partial") {
    assert.equal(partialStale.freshness.kind, "stale");
    assert.deepEqual(partialStale.missingSections, ["processing-streams"]);
    assert.deepEqual(partialStale.data.streams, ["cached-stream"]);
  }
});

test("refreshing known data remains visible and is never reported as a positive zero", () => {
  const state = aggregateRemainingQueries("workers", {
    workers: success(["worker-a"], true),
    "registered-nodes": success([], false),
    "service-health": success([], false),
  });
  assert.equal(state.kind, "ready");
  if (state.kind === "ready") {
    assert.equal(state.freshness.kind, "refreshing");
    assert.deepEqual(state.data.workers, ["worker-a"]);
  }
});

test("remaining-state notices disclose missing and stale state in ja and en without raw errors", () => {
  const state = aggregateRemainingQueries("archive", {
    streams: stale(["cached-stream"]),
    "processing-streams": failed(),
  });
  const ja = remainingStatePresentation(state, "archive", "ja");
  const en = remainingStatePresentation(state, "archive", "en");
  assert.equal(ja.tone, "warning");
  assert.equal(en.tone, "warning");
  assert.match(ja.text, /処理中アーカイブ/);
  assert.match(ja.text, /古いデータ/);
  assert.match(en.text, /processing archives/);
  assert.match(en.text, /stale data/);
  assert.doesNotMatch(`${ja.text}${en.text}`, /HOSTILE_REMOTE_ERROR_MARKER/);
});

test("unknown legacy status is conservative and never echoes the wire value", () => {
  const marker = "WIRE_SAYS_HEALTHYISH_91";
  const badgeSource = readFileSync(new URL("../src/components/admin/status-badge.tsx", import.meta.url), "utf8");
  const fallback = badgeSource.match(/statusMap\[normalized\]\s*\?\?\s*\{(?<body>[\s\S]*?)\n\s*\};/u)?.groups?.body || "";
  assert.match(fallback, /label:\s*"状態不明"/);
  assert.match(fallback, /detail:\s*"既知の状態として判定できません"/);
  assert.doesNotMatch(fallback, new RegExp(marker));
});

test("all remaining consumer views use the shared state projection and preserve cached rows", () => {
  const sources = {
    audit: source("audit/audit-logs-view.tsx"),
    workers: source("workers/workers-view.tsx"),
    nodes: source("nodes/node-registration-view.tsx"),
    archive: source("archive/archive-view.tsx"),
    application: source("settings/settings-view.tsx"),
  };
  assert.match(sources.audit, /aggregateRemainingQueries\("audit"/);
  assert.match(sources.audit, /auditLogs\.data \|\| \[\]/);
  assert.match(sources.workers, /aggregateRemainingQueries\("workers"/);
  assert.match(sources.workers, /workers\.data \|\| \[\]/);
  assert.match(sources.nodes, /aggregateRemainingQueries\("nodes"/);
  assert.match(sources.nodes, /registeredNodes\.data \|\| \[\]/);
  assert.match(sources.archive, /aggregateRemainingQueries\("archive"/);
  assert.match(sources.archive, /streams\.isError && streamRows\.length === 0/);
  assert.match(sources.archive, /query\.isError && artifacts\.length === 0/);
  assert.match(sources.application, /managedAppSettingsRemoteState/);
  assert.match(sources.application, /RemoteStateBoundary/);
});

test("remaining consumer surfaces contain no raw error or unknown-positive status display", () => {
  const nodeSource = source("nodes/node-registration-view.tsx");
  const archiveSource = source("archive/archive-view.tsx");
  const badgeSource = readFileSync(new URL("../src/components/admin/status-badge.tsx", import.meta.url), "utf8");
  for (const value of [
    source("audit/audit-logs-view.tsx"),
    source("workers/workers-view.tsx"),
    nodeSource,
    archiveSource,
    source("settings/settings-view.tsx"),
  ]) {
    assert.doesNotMatch(value, /error\.message(?!Key)/);
  }
  assert.match(nodeSource, /statusDescriptor\(configuration\.node\?\.status\)\.label/);
  assert.match(archiveSource, /archiveProcessingStateLabel\(stream\.status, uiText\)/);
  assert.match(archiveSource, /return uiText\("状態不明"\)/);
  assert.doesNotMatch(badgeSource, /label:\s*status\s*\|\|/);
});

test("notice semantics are screen-reader and forced-colors safe and expose no action surface", () => {
  const notice = readFileSync(new URL("../src/features/remote-state/remaining-state-notice.tsx", import.meta.url), "utf8");
  assert.match(notice, /role=\{role\}/);
  assert.match(notice, /aria-live=/);
  assert.match(notice, /forced-color-adjust-auto/);
  assert.match(notice, /data-remote-state/);
  assert.doesNotMatch(notice, /<Button|onClick|availability|ActionAvailability/);
});


function source(relativePath: string) {
  return readMovedSource(new URL(`../src/features/${relativePath}`, import.meta.url));
}

function success(data: readonly unknown[], isFetching = false): Snapshot {
  return { status: "success", isFetching, data, dataUpdatedAt: 11 };
}

function failed(): Snapshot {
  return { status: "error", isFetching: false, error: new TypeError("HOSTILE_REMOTE_ERROR_MARKER"), dataUpdatedAt: 0 };
}

function stale(data: readonly unknown[]): Snapshot {
  return { status: "error", isFetching: false, data, error: new TypeError("HOSTILE_REMOTE_ERROR_MARKER"), dataUpdatedAt: 7 };
}
