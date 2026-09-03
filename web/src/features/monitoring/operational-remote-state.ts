import { aggregateRemoteState, remoteStateAllowsPositiveSummary, summarizeRemoteCoverage, type RemoteCoverageContribution } from "@/lib/foundation/remote-state/aggregate";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";
import { projectRemoteState, type QueryProjectionSnapshot } from "@/lib/foundation/remote-state/projector";
import type { Stream, WorkerNode } from "@/types/domain";

export { remoteStateAllowsPositiveSummary };

export const operationalConsumerManifest = Object.freeze({
  dashboard: Object.freeze(["streams", "services"]),
  monitoring: Object.freeze(["services", "streams", "incidents", "diagnostics"]),
  metrics: Object.freeze(["metrics", "services"]),
} as const);

export type OperationalConsumer = keyof typeof operationalConsumerManifest;
export type OperationalSectionID = (typeof operationalConsumerManifest)[OperationalConsumer][number];

export type OperationalQuerySnapshot<T = readonly unknown[]> = Readonly<{
  status: "pending" | "success" | "error";
  isFetching: boolean;
  data?: T;
  error?: unknown;
  dataUpdatedAt: number;
}>;

export type OperationalAggregateData = Readonly<Record<string, readonly unknown[]>>;

export function operationalQuerySnapshot<T extends readonly unknown[]>(query: Readonly<{
  status: "pending" | "success" | "error";
  isFetching: boolean;
  data?: T;
  error?: unknown;
  dataUpdatedAt: number;
}>): OperationalQuerySnapshot<T> {
  return {
    status: query.status,
    isFetching: query.isFetching,
    dataUpdatedAt: query.dataUpdatedAt,
    ...(query.data !== undefined ? { data: query.data } : {}),
    ...(query.error !== undefined ? { error: query.error } : {}),
  };
}

export function knownEmptyOperationalQuery(): OperationalQuerySnapshot<readonly unknown[]> {
  return { status: "success", isFetching: false, data: [], dataUpdatedAt: 0 };
}

export function projectOperationalQuery<T extends readonly unknown[]>(query: OperationalQuerySnapshot<T>): RemoteState<T> {
  const snapshot: QueryProjectionSnapshot<T> = query.status === "pending"
    ? { status: "pending", fetching: query.isFetching }
    : query.status === "success"
      ? { status: "success", fetching: query.isFetching, data: query.data as T, dataUpdatedAt: query.dataUpdatedAt }
      : query.data === undefined
        ? { status: "error", fetching: query.isFetching, error: query.error }
        : { status: "error", fetching: query.isFetching, error: query.error, data: query.data, dataUpdatedAt: query.dataUpdatedAt };
  return projectRemoteState(snapshot, { classifyData: (data) => data.length === 0 ? "empty" : "ready" });
}

export function aggregateOperationalQueries(
  consumer: OperationalConsumer,
  queries: Readonly<Partial<Record<OperationalSectionID, OperationalQuerySnapshot>>>,
): RemoteState<OperationalAggregateData> {
  const ids = operationalConsumerManifest[consumer];
  const sections = ids.map((id) => ({ id, state: queries[id] ? projectOperationalQuery(queries[id]) : { kind: "initial-loading" as const } }));
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

export function operationalStatePresentation(
  state: RemoteState<unknown>,
  consumer: OperationalConsumer,
  locale: "ja" | "en",
) {
  const label = consumer === "dashboard" ? (locale === "ja" ? "ダッシュボード" : "Dashboard") : consumer === "monitoring" ? "Monitoring" : "Metrics";
  const sectionLabel = (id: string) => ({
    streams: locale === "ja" ? "配信枠" : "streams",
    services: locale === "ja" ? "Node状態" : "node health",
    incidents: locale === "ja" ? "インシデント" : "incidents",
    diagnostics: locale === "ja" ? "診断" : "diagnostics",
    metrics: locale === "ja" ? "メトリクス" : "metrics",
  })[id] || id;
  if (state.kind === "initial-loading") return { tone: "loading" as const, text: locale === "ja" ? `${label}の初期データを取得中です。` : `Loading initial ${label} data.`, missing: [] as string[] };
  if (state.kind === "blocking-error") return { tone: "error" as const, text: locale === "ja" ? `${label}の表示に必要なデータを取得できません。` : `Required ${label} data is unavailable.`, missing: operationalConsumerManifest[consumer].map(sectionLabel) };
  const stale = state.freshness.kind === "stale";
  const refreshing = state.freshness.kind === "refreshing";
  const missing = state.kind === "partial" ? state.missingSections.map(sectionLabel) : [];
  const text = state.kind === "partial"
    ? (locale === "ja" ? `一部未取得: ${missing.join("、")}。取得済みデータだけを表示します。` : `Unavailable sections: ${missing.join(", ")}. Showing only known data.`)
    : stale
      ? (locale === "ja" ? "更新に失敗したため、取得済みの古いデータを表示しています。" : "Refresh failed; showing previously loaded stale data.")
      : refreshing
        ? (locale === "ja" ? "取得済みデータを表示しながら更新中です。" : "Refreshing while keeping known data visible.")
        : state.kind === "empty"
          ? (locale === "ja" ? "取得は完了しましたが、対象データはありません。" : "Loading completed; there is no data.")
          : (locale === "ja" ? "必要なデータを取得済みです。" : "Required data is available.");
  return { tone: state.kind === "partial" || stale ? "warning" as const : "ready" as const, text, missing };
}

export type StreamOperationalClass = "live" | "waiting" | "attention" | "done" | "unknown";

export function classifyStreamOperationalStatus(status: unknown): StreamOperationalClass {
  const normalized = typeof status === "string" ? status.trim().toLowerCase() : "";
  if (["live", "starting"].includes(normalized)) return "live";
  if (["created", "scheduled", "ready", "draft"].includes(normalized)) return "waiting";
  if (["failed", "error"].includes(normalized)) return "attention";
  if (["stopping", "stopped", "completed"].includes(normalized)) return "done";
  return "unknown";
}

export function countOperationalStreams(streams: readonly Stream[]) {
  return streams.reduce((counts, stream) => {
    counts[classifyStreamOperationalStatus(stream.status)] += 1;
    return counts;
  }, { live: 0, waiting: 0, attention: 0, done: 0, unknown: 0 });
}

export function summarizeServiceAvailability(services: readonly WorkerNode[]) {
  return summarizeRemoteCoverage(services.map(serviceAvailabilityContribution));
}

export function serviceAvailabilityContribution(service: WorkerNode): RemoteCoverageContribution {
  const health = String(service.health_status || "").trim().toLowerCase();
  const status = String(service.status || "").trim().toLowerCase();
  const authority = health || status;
  if (["healthy", "ok", "online"].includes(authority)) return { kind: "known", positive: true };
  if (["offline", "unhealthy", "warning", "degraded", "unconfigured", "failed", "error"].includes(authority)) return { kind: "known", positive: false };
  return { kind: "unknown" };
}

export function summarizeKnownStatuses(
  values: readonly unknown[],
  positiveStatuses: readonly string[],
  negativeStatuses: readonly string[],
) {
  const positive = new Set(positiveStatuses.map((value) => value.toLowerCase()));
  const negative = new Set(negativeStatuses.map((value) => value.toLowerCase()));
  return summarizeRemoteCoverage(values.map((value) => {
    const normalized = typeof value === "string" ? value.trim().toLowerCase() : "";
    if (positive.has(normalized)) return { kind: "known", positive: true } as const;
    if (negative.has(normalized)) return { kind: "known", positive: false } as const;
    return { kind: "unknown" } as const;
  }));
}

export function recordingStateContribution(stream: Stream): RemoteCoverageContribution {
  const configured = Boolean(stream.archive_profile_id || stream.archive_drive_destination_id || stream.archive_oauth_account_id || stream.archive_file_name);
  if (!configured) return { kind: "known", positive: false };
  const status = classifyStreamOperationalStatus(stream.status);
  if (status === "unknown") return { kind: "unknown" };
  return { kind: "known", positive: status === "done" };
}
