"use client";

import { useI18n } from "@/components/admin/i18n-provider";
import { remainingStatePresentation, type RemainingConsumer } from "@/features/remote-state/remaining-remote-state";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";

export function RemainingStateNotice({ state, consumer }: { state: RemoteState<unknown>; consumer: RemainingConsumer }) {
  const { locale } = useI18n();
  const presentation = remainingStatePresentation(state, consumer, locale);
  const role = presentation.tone === "error" ? "alert" : "status";
  const freshness = state.kind === "initial-loading" || state.kind === "blocking-error" ? "unavailable" : state.freshness.kind;
  return (
    <div
      role={role}
      aria-live={role === "status" ? "polite" : undefined}
      data-remote-consumer={consumer}
      data-remote-state={state.kind}
      data-remote-freshness={freshness}
      className="forced-color-adjust-auto motion-reduce:transition-none rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"
    >
      {presentation.text}
    </div>
  );
}
