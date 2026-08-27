import { createElement, useId, type ReactNode } from "react";

import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";
import type { TranslationKey } from "@/lib/i18n";

export type ActionControlRenderProps = {
  disabled: boolean;
  "aria-disabled"?: true;
  "aria-describedby"?: string;
};

export type ActionAvailabilityBoundaryProps = {
  evaluation: ActionEvaluation;
  translate: (key: TranslationKey) => string;
  reasonPresentation?: "inline" | "sr-only";
  reasonId?: string;
  children: (props: ActionControlRenderProps) => ReactNode;
};

export function ActionAvailabilityBoundary({
  evaluation,
  translate,
  reasonPresentation = "inline",
  reasonId,
  children,
}: ActionAvailabilityBoundaryProps): ReactNode {
  const generatedReasonId = useId();
  if (evaluation.visibility.kind === "hidden") return null;
  if (evaluation.availability.kind === "allowed") {
    return children({ disabled: false });
  }

  const resolvedReasonId = reasonId ?? generatedReasonId;
  const control = children({
    disabled: true,
    "aria-disabled": true,
    "aria-describedby": resolvedReasonId,
  });
  const reason = translate(evaluation.availability.reasonKey);

  return createElement(
    "span",
    {
      tabIndex: 0,
      "aria-disabled": true,
      "aria-describedby": resolvedReasonId,
      "data-action-availability": evaluation.availability.kind,
    },
    control,
    createElement(
      "span",
      {
        id: resolvedReasonId,
        ...(reasonPresentation === "sr-only" ? { className: "sr-only" } : {}),
      },
      reason,
    ),
  );
}
