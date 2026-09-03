import { aggregateRemoteState } from "@/lib/foundation/remote-state/aggregate";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import { projectRemoteState, type QueryProjectionSnapshot } from "@/lib/foundation/remote-state/projector";

export const remainingConsumerManifest = Object.freeze({
  audit: Object.freeze(["audit-logs"]),
  workers: Object.freeze(["workers", "registered-nodes", "service-health"]),
  nodes: Object.freeze(["registered-nodes"]),
  archive: Object.freeze(["streams", "processing-streams"]),
  application: Object.freeze(["settings"]),
} as const);

export type RemainingConsumer = keyof typeof remainingConsumerManifest;
export type RemainingSection = (typeof remainingConsumerManifest)[RemainingConsumer][number];
export type RemainingQuerySnapshot<T = readonly unknown[]> = Readonly<{
  status: "pending" | "success" | "error";
  isFetching: boolean;
  data?: T;
  error?: unknown;
  dataUpdatedAt: number;
}>;

export function remainingQuerySnapshot<T extends readonly unknown[]>(query: Readonly<{
  status: "pending" | "success" | "error";
  isFetching: boolean;
  data?: T;
  error?: unknown;
  dataUpdatedAt: number;
}>): RemainingQuerySnapshot<T> {
  return {
    status: query.status,
    isFetching: query.isFetching,
    dataUpdatedAt: query.dataUpdatedAt,
    ...(query.data !== undefined ? { data: query.data } : {}),
    ...(query.error !== undefined ? { error: query.error } : {}),
  };
}

export function knownEmptyRemainingQuery(): RemainingQuerySnapshot {
  return { status: "success", isFetching: false, data: [], dataUpdatedAt: 0 };
}

export function aggregateRemainingQueries(
  consumer: RemainingConsumer,
  queries: Readonly<Partial<Record<RemainingSection, RemainingQuerySnapshot>>>,
): RemoteState<Readonly<Record<string, readonly unknown[]>>> {
  const sections = remainingConsumerManifest[consumer].map((id) => ({ id, state: project(queries[id]) }));
  const data: Record<string, readonly unknown[]> = {};
  let hasContent = false;
  for (const section of sections) {
    const query = queries[section.id];
    if ((section.state.kind === "ready" || section.state.kind === "empty") && query?.data !== undefined) {
      data[section.id] = query.data;
      hasContent = true;
    }
  }
  return aggregateRemoteState({
    data: hasContent ? Object.freeze(data) : undefined,
    sections: sections as [(typeof sections)[number], ...(typeof sections)[number][]],
    classifyData: (value) => Object.values(value).every((rows) => rows.length === 0) ? "empty" : "ready",
  });
}

export function remainingStatePresentation(state: RemoteState<unknown>, consumer: RemainingConsumer, locale: "ja" | "en") {
  const labels: Record<RemainingConsumer, readonly [string, string]> = {
    audit: ["監査ログ", "audit logs"],
    workers: ["Worker情報", "worker data"],
    nodes: ["Node情報", "node data"],
    archive: ["アーカイブ情報", "archive data"],
    application: ["アプリ設定", "application settings"],
  };
  const sectionLabels: Record<string, readonly [string, string]> = {
    "audit-logs": ["監査ログ", "audit logs"],
    workers: ["Worker", "workers"],
    "registered-nodes": ["登録Node", "registered nodes"],
    "service-health": ["Node稼働状態", "node health"],
    streams: ["配信枠", "streams"],
    "processing-streams": ["処理中アーカイブ", "processing archives"],
    settings: ["設定", "settings"],
  };
  const label = labels[consumer][locale === "ja" ? 0 : 1];
  const missing = state.kind === "partial"
    ? state.missingSections.map((id) => sectionLabels[id]?.[locale === "ja" ? 0 : 1] || id)
    : [];
  const stale = state.kind !== "initial-loading" && state.kind !== "blocking-error" && state.freshness.kind === "stale";
  const refreshing = state.kind !== "initial-loading" && state.kind !== "blocking-error" && state.freshness.kind === "refreshing";
  if (state.kind === "initial-loading") return { tone: "loading" as const, text: locale === "ja" ? `${label}を取得中です。` : `Loading ${label}.`, missing };
  if (state.kind === "blocking-error") return { tone: "error" as const, text: locale === "ja" ? `${label}を取得できません。` : `${label} is unavailable.`, missing };
  if (state.kind === "partial") {
    const suffix = stale
      ? (locale === "ja" ? "取得済みの古いデータを表示しています。" : "Showing previously loaded stale data.")
      : (locale === "ja" ? "取得済みデータだけを表示します。" : "Showing only known data.");
    return { tone: "warning" as const, text: locale === "ja" ? `一部未取得: ${missing.join("、")}。${suffix}` : `Unavailable: ${missing.join(", ")}. ${suffix}`, missing };
  }
  if (stale) return { tone: "warning" as const, text: locale === "ja" ? "更新に失敗したため、取得済みの古いデータを表示しています。" : "Refresh failed; showing previously loaded stale data.", missing };
  if (refreshing) return { tone: "loading" as const, text: locale === "ja" ? "取得済みデータを表示しながら更新中です。" : "Refreshing while keeping known data visible.", missing };
  return { tone: "ready" as const, text: locale === "ja" ? `${label}は最新です。` : `${label} is current.`, missing };
}

function project(query: RemainingQuerySnapshot | undefined) {
  if (!query) return { kind: "initial-loading" as const };
  const snapshot: QueryProjectionSnapshot<readonly unknown[]> = query.status === "pending"
    ? { status: "pending", fetching: query.isFetching }
    : query.status === "success"
      ? { status: "success", fetching: query.isFetching, data: query.data || [], dataUpdatedAt: query.dataUpdatedAt }
      : query.data === undefined
        ? { status: "error", fetching: query.isFetching, error: query.error }
        : { status: "error", fetching: query.isFetching, error: query.error, data: query.data, dataUpdatedAt: query.dataUpdatedAt };
  return projectRemoteState(snapshot, { classifyData: (data) => data.length === 0 ? "empty" : "ready" });
}
