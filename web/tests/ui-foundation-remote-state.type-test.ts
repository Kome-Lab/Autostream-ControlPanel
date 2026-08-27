import type { ReactNode } from "react";

import type {
  AggregateRemoteStateInput,
  RemoteCoverageContribution,
  RemoteSectionState,
} from "../src/lib/foundation/remote-state/aggregate.ts";
import type {
  ProjectRemoteStateOptions,
  QueryProjectionSnapshot,
} from "../src/lib/foundation/remote-state/projector.ts";
import type {
  RemoteContentContext,
  RemoteStateBoundaryProps,
} from "../src/components/foundation/remote-state/remote-state-boundary.ts";

const classifyText: ProjectRemoteStateOptions<string>["classifyData"] = (data) =>
  data.length === 0 ? "empty" : "ready";

export const validSnapshots = [
  { status: "pending", fetching: true },
  { status: "success", fetching: false, data: "ready", dataUpdatedAt: 1 },
  { status: "error", fetching: false, error: new Error("safe adapter input") },
  { status: "error", fetching: true, error: new Error("safe adapter input"), data: "cached", dataUpdatedAt: 1 },
] as const satisfies readonly QueryProjectionSnapshot<string>[];

export const validProjectOptions: ProjectRemoteStateOptions<string> = {
  classifyData: classifyText,
};

export const validSections = [
  {
    id: "workers",
    state: {
      kind: "ready",
      data: "worker",
      freshness: { kind: "fresh", lastSuccessAt: 1 },
    },
  },
] as const satisfies readonly [RemoteSectionState, ...RemoteSectionState[]];

export const validAggregate: AggregateRemoteStateInput<string> = {
  data: "combined",
  sections: validSections,
  classifyData: classifyText,
};

export const validCoverage = [
  { kind: "known", positive: true },
  { kind: "known", positive: false },
  { kind: "unknown" },
] as const satisfies readonly RemoteCoverageContribution[];

export const validBoundaryProps: RemoteStateBoundaryProps<string> = {
  state: {
    kind: "ready",
    data: "cached",
    freshness: { kind: "stale", lastSuccessAt: 1, error: { kind: "network", messageKey: "apiErrorNetwork" } },
  },
  noticeId: "remote-state-notice",
  translate: () => "translated",
  formatTimestamp: (timestamp) => String(timestamp),
  renderData: (data, context) => `${context.stateKind}:${data}`,
};

// @ts-expect-error -- pending snapshots cannot carry data
export const invalidPendingData: QueryProjectionSnapshot<string> = { status: "pending", fetching: true, data: "cached" };
// @ts-expect-error -- pending snapshots cannot carry an error
export const invalidPendingError: QueryProjectionSnapshot<string> = { status: "pending", fetching: true, error: new Error("invalid") };
// @ts-expect-error -- successful snapshots require data
export const invalidSuccessData: QueryProjectionSnapshot<string> = { status: "success", fetching: false, dataUpdatedAt: 1 };
// @ts-expect-error -- successful snapshots require the exact successful timestamp
export const invalidSuccessTimestamp: QueryProjectionSnapshot<string> = { status: "success", fetching: false, data: "ready" };
// @ts-expect-error -- successful snapshots cannot carry a raw error
export const invalidSuccessError: QueryProjectionSnapshot<string> = { status: "success", fetching: false, data: "ready", dataUpdatedAt: 1, error: new Error("invalid") };
// @ts-expect-error -- error snapshots require an error value
export const invalidErrorValue: QueryProjectionSnapshot<string> = { status: "error", fetching: false };
// @ts-expect-error -- query status is a closed structural union
export const invalidStatus: QueryProjectionSnapshot<string> = { status: "idle", fetching: false };
// @ts-expect-error -- fetching is strictly boolean
export const invalidFetching: QueryProjectionSnapshot<string> = { status: "pending", fetching: "yes" };

// @ts-expect-error -- aggregate sections are a non-empty tuple
export const invalidEmptySections: AggregateRemoteStateInput<string> = { data: "combined", sections: [], classifyData: classifyText };
// @ts-expect-error -- every aggregate section requires a stable ID
export const invalidMissingSectionId: RemoteSectionState = { state: { kind: "initial-loading" } };
// @ts-expect-error -- every aggregate section requires a canonical state
export const invalidMissingSectionState: RemoteSectionState = { id: "workers" };
// @ts-expect-error -- coverage kinds are closed
export const invalidCoverageKind: RemoteCoverageContribution = { kind: "missing" };
// @ts-expect-error -- known coverage must explicitly declare positive or negative
export const invalidKnownCoverage: RemoteCoverageContribution = { kind: "known" };

// @ts-expect-error -- noticeId is required for the accessible reason boundary
export const invalidMissingNoticeId: RemoteStateBoundaryProps<string> = { state: validBoundaryProps.state, translate: validBoundaryProps.translate, formatTimestamp: validBoundaryProps.formatTimestamp, renderData: validBoundaryProps.renderData };
// @ts-expect-error -- translation is caller-owned and required
export const invalidMissingTranslate: RemoteStateBoundaryProps<string> = { state: validBoundaryProps.state, noticeId: "notice", formatTimestamp: validBoundaryProps.formatTimestamp, renderData: validBoundaryProps.renderData };
// @ts-expect-error -- timestamp formatting is caller-owned and required
export const invalidMissingFormatter: RemoteStateBoundaryProps<string> = { state: validBoundaryProps.state, noticeId: "notice", translate: validBoundaryProps.translate, renderData: validBoundaryProps.renderData };
// @ts-expect-error -- data rendering is caller-owned and required
export const invalidMissingRenderData: RemoteStateBoundaryProps<string> = { state: validBoundaryProps.state, noticeId: "notice", translate: validBoundaryProps.translate, formatTimestamp: validBoundaryProps.formatTimestamp };
// @ts-expect-error -- retryPending is strictly boolean
export const invalidRetryPending: RemoteStateBoundaryProps<string> = { ...validBoundaryProps, retryPending: "pending" };
const renderExpectingRawError = (
  data: string,
  context: RemoteContentContext & { rawError: Error },
): ReactNode => `${data}:${context.rawError.name}`;

// @ts-expect-error -- Boundary render context intentionally never exposes a raw query error
export const invalidRawErrorContext: RemoteStateBoundaryProps<string> = { ...validBoundaryProps, renderData: renderExpectingRawError };
