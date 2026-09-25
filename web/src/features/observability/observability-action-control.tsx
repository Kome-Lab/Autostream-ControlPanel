"use client";

import { useState } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { HighRiskConfirmation, type ConfirmationDialogState } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { Button } from "@/components/ui/button";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import {
  buildObservabilityActionDescriptor,
  type ObservabilityActionController,
  type ObservabilityActionExecutionResult,
  type ObservabilityActionPlan,
} from "@/features/observability/action-policy";
import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";

export function ObservabilityActionControl({
  controller,
  plan,
  allowed,
  permissionText,
  onResult,
}: Readonly<{
  controller: ObservabilityActionController;
  plan: ObservabilityActionPlan;
  allowed: boolean;
  permissionText?: string;
  onResult: (plan: ObservabilityActionPlan, result: ObservabilityActionExecutionResult) => void | Promise<void>;
}>) {
  const { t } = useI18n();
  const uiText = useUICopy();
  const label = plan.id === "OBS-01" ? uiText("確認済みにする")
    : plan.id === "OBS-02" ? uiText("解決済みにする")
      : plan.id === "OBS-03" ? uiText("診断を再評価")
        : plan.id === "OBS-04" ? uiText("承認") : plan.id === "OBS-05" ? uiText("実行") : plan.label;
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<ConfirmationDialogState>({ kind: "ready" });
  const descriptor = buildObservabilityActionDescriptor(plan);
  const evaluation = observabilityEvaluation(allowed);

  const submit = async () => {
    setState({ kind: "revalidating" });
    const result = await controller.execute(plan, { confirmed: true, typedValue: plan.targetLabel });
    await onResult(plan, result);
    if (result.kind === "succeeded") {
      setOpen(false);
      setState({ kind: "ready" });
    } else if (result.kind === "outcome_unknown") {
      setState({ kind: "outcome-unknown", nextAction: "inspect-audit" });
    } else if (result.kind === "conflict") {
      setState({ kind: "conflict", error: result.error });
    } else if (result.kind === "failed") {
      setState({ kind: "failed", error: result.error });
    } else {
      setState(result.reason === "authority-changed"
        ? { kind: "stale-blocked" }
        : { kind: "revalidation-unavailable" });
    }
  };

  return (
    <ActionAvailabilityBoundary evaluation={evaluation} translate={t} reasonPresentation="sr-only">
      {(availabilityProps) => (
        <HighRiskConfirmation
          descriptor={descriptor}
          open={open}
          evaluation={evaluation}
          state={state}
          translate={t}
          trigger={(confirmationProps) => (
            <Button
              type="button"
              variant={plan.emphasis ? "default" : "outline"}
              size="sm"
              title={!allowed ? permissionText : undefined}
              {...availabilityProps}
              disabled={availabilityProps.disabled || confirmationProps.disabled}
            >
              {label}
            </Button>
          )}
          onOpenIntent={() => {
            setState({ kind: "ready" });
            setOpen(true);
          }}
          onCloseIntent={() => {
            setOpen(false);
            setState({ kind: "ready" });
          }}
          onConfirmIntent={() => { void submit(); }}
        />
      )}
    </ActionAvailabilityBoundary>
  );
}

function observabilityEvaluation(allowed: boolean): ActionEvaluation {
  return allowed
    ? Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "allowed" as const }) })
    : Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "denied" as const, reasonKey: "actionPermissionDenied" as const }) });
}
