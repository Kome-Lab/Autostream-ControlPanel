import { createElement, type ReactNode } from "react";

import { RemoteStateNotice } from "@/components/foundation/remote-state/remote-state-notice";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { Freshness, RemoteState } from "@/lib/foundation/remote-state/contracts";
import type { TranslationKey, TranslationValues } from "@/lib/i18n";

export type RemoteContentContext = Readonly<{
  stateKind: "ready" | "partial";
  freshness: Freshness;
  noticeId?: string;
  missingSections: readonly string[];
  sectionErrors: Readonly<Record<string, AdaptedAPIError>>;
}>;

export type RemoteStateBoundaryProps<T> = Readonly<{
  state: RemoteState<T>;
  noticeId: string;
  translate: (
    key: TranslationKey,
    values?: TranslationValues,
  ) => string;
  formatTimestamp: (timestamp: number) => string;
  renderData: (
    data: T,
    context: RemoteContentContext,
  ) => ReactNode;
  renderLoading?: () => ReactNode;
  renderEmpty?: (freshness: Freshness) => ReactNode;
  onRetry?: () => void;
  retryPending?: boolean;
}>;

export function RemoteStateBoundary<T>({
  state,
  noticeId,
  translate,
  formatTimestamp,
  renderData,
  renderLoading,
  renderEmpty,
  onRetry,
  retryPending,
}: RemoteStateBoundaryProps<T>): ReactNode {
  if (state.kind === "initial-loading") {
    return createElement(
      "div",
      {
        "data-remote-state": "initial-loading",
        "aria-busy": true,
      },
      renderLoading ? renderLoading() : translate("remoteStateLoading"),
    );
  }

  if (state.kind === "blocking-error") {
    return createElement(
      "div",
      { "data-remote-state": "blocking-error" },
      createElement(RemoteStateNotice, {
        kind: "blocking",
        error: state.error,
        noticeId,
        translate,
        formatTimestamp,
        onRetry,
        retryPending,
      }),
    );
  }

  const hasNotice = state.kind === "partial"
    || state.freshness.kind !== "fresh"
    || onRetry !== undefined;
  let content: ReactNode;
  if (state.kind === "empty") {
    content = renderEmpty ? renderEmpty(state.freshness) : translate("remoteStateEmpty");
  } else if (state.kind === "partial") {
    content = renderData(state.data, Object.freeze({
      stateKind: "partial",
      freshness: state.freshness,
      noticeId,
      missingSections: state.missingSections,
      sectionErrors: state.sectionErrors,
    }));
  } else {
    const missingSections: readonly string[] = Object.freeze([]);
    const sectionErrors: Readonly<Record<string, AdaptedAPIError>> = Object.freeze({});
    content = renderData(state.data, Object.freeze({
      stateKind: "ready",
      freshness: state.freshness,
      ...(hasNotice ? { noticeId } : {}),
      missingSections,
      sectionErrors,
    }));
  }

  return createElement(
    "div",
    {
      "data-remote-state": state.kind,
      "data-remote-freshness": state.freshness.kind,
      ...(state.freshness.kind === "refreshing" ? { "aria-busy": true } : {}),
      ...(hasNotice ? { "aria-describedby": noticeId } : {}),
    },
    content,
    hasNotice
      ? createElement(RemoteStateNotice, {
          kind: "content",
          freshness: state.freshness,
          ...(state.kind === "partial"
            ? { partialMissingCount: state.missingSections.length }
            : {}),
          noticeId,
          translate,
          formatTimestamp,
          onRetry,
          retryPending,
        })
      : null,
  );
}
