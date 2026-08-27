import { createElement, type ReactNode } from "react";

import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { Freshness } from "@/lib/foundation/remote-state/contracts";
import type { TranslationKey, TranslationValues } from "@/lib/i18n";

type RemoteStateNoticeCommonProps = Readonly<{
  noticeId: string;
  translate: (
    key: TranslationKey,
    values?: TranslationValues,
  ) => string;
  formatTimestamp: (timestamp: number) => string;
  onRetry?: () => void;
  retryPending?: boolean;
}>;

export type RemoteStateNoticeProps = RemoteStateNoticeCommonProps & (
  | Readonly<{
      kind: "content";
      freshness: Freshness;
      partialMissingCount?: number;
    }>
  | Readonly<{
      kind: "blocking";
      error: AdaptedAPIError;
    }>
);

export function RemoteStateNotice(props: RemoteStateNoticeProps): ReactNode {
  if (props.kind === "blocking") {
    return createElement(
      "div",
      { "data-remote-notice": "blocking" },
      createElement(
        "div",
        { id: props.noticeId, role: "alert" },
        createElement("h2", null, props.translate("remoteStateBlockingError")),
        createElement("p", null, props.translate(props.error.messageKey)),
      ),
      createRetryButton(props),
    );
  }

  const partialMissingCount = validMissingCount(props.partialMissingCount);
  const messages: ReactNode[] = [];
  if (partialMissingCount !== undefined) {
    messages.push(createElement("p", { key: "partial" }, props.translate("remoteStatePartial")));
  }
  if (props.freshness.kind === "refreshing") {
    messages.push(createElement("p", { key: "refreshing" }, props.translate("remoteStateRefreshing")));
  } else if (props.freshness.kind === "stale") {
    messages.push(createElement("p", { key: "stale" }, props.translate("remoteStateStale")));
    const formattedTimestamp = formatTimestampSafely(
      props.formatTimestamp,
      props.freshness.lastSuccessAt,
    );
    if (formattedTimestamp !== undefined) {
      messages.push(createElement(
        "p",
        { key: "last-success" },
        props.translate("remoteStateLastSuccessfulAt", { time: formattedTimestamp }),
      ));
    }
    messages.push(createElement(
      "p",
      { key: "stale-error" },
      props.translate(props.freshness.error.messageKey),
    ));
  }

  const retryButton = createRetryButton(props);
  if (messages.length === 0 && retryButton === null) return null;
  return createElement(
    "div",
    { "data-remote-notice": props.freshness.kind },
    createElement(
      "div",
      {
        id: props.noticeId,
        ...(partialMissingCount === undefined
          ? {}
          : { "data-remote-missing-count": partialMissingCount }),
      },
      ...messages,
    ),
    retryButton,
  );
}

function createRetryButton(
  props: Pick<RemoteStateNoticeCommonProps, "noticeId" | "onRetry" | "retryPending" | "translate">,
): ReactNode {
  if (!props.onRetry) return null;
  const pending = props.retryPending === true;
  return createElement(
    "button",
    {
      type: "button",
      "aria-describedby": props.noticeId,
      ...(pending
        ? {
            disabled: true,
            "aria-disabled": true,
            "aria-busy": true,
          }
        : { onClick: props.onRetry }),
    },
    props.translate("remoteStateRetry"),
  );
}

function formatTimestampSafely(
  formatter: (timestamp: number) => string,
  timestamp: number,
): string | undefined {
  try {
    const formatted: unknown = formatter(timestamp);
    return typeof formatted === "string" ? formatted : undefined;
  } catch {
    return undefined;
  }
}

function validMissingCount(value: number | undefined): number | undefined {
  return typeof value === "number"
    && Number.isFinite(value)
    && Number.isInteger(value)
    && value >= 0
    ? value
    : undefined;
}
