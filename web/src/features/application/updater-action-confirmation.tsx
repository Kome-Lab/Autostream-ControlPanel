"use client";

import { useState, type ReactNode } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { HighRiskConfirmation, type ConfirmationDialogState } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { Button } from "@/components/ui/button";
import {
  buildUpdaterActionDescriptor,
  updaterActionTypedValue,
  type UpdaterActionAuthority,
  type UpdaterActionController,
  type UpdaterActionIntent,
  type UpdaterActionResult,
} from "@/features/application/updater-action-policy";
import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";

export function UpdaterActionConfirmation({
  controller,
  intent,
  authority,
  refreshAuthority,
  handler,
  label,
  icon,
  variant = "default",
  size = "sm",
  className,
  disabled = false,
  "aria-busy": ariaBusy,
  title,
  onResult,
}: Readonly<{
  controller: UpdaterActionController;
  intent: UpdaterActionIntent;
  authority: UpdaterActionAuthority;
  refreshAuthority: () => Promise<UpdaterActionAuthority>;
  handler: () => Promise<unknown>;
  label: string;
  icon?: ReactNode;
  variant?: "default" | "outline" | "destructive";
  size?: "sm" | "default";
  className?: string;
  disabled?: boolean;
  "aria-busy"?: boolean;
  title?: string;
  onResult?: (result: UpdaterActionResult) => void | Promise<void>;
}>) {
  const { t } = useI18n();
  const [opened, setOpened] = useState<Readonly<{
    intent: UpdaterActionIntent;
    refreshAuthority: () => Promise<UpdaterActionAuthority>;
    handler: () => Promise<unknown>;
  }>>();
  const [state, setState] = useState<ConfirmationDialogState>({ kind: "ready" });
  const triggerDescriptor = buildUpdaterActionDescriptor(intent);
  const descriptor = opened
    ? buildUpdaterActionDescriptor(opened.intent)
    : triggerDescriptor;
  if (!triggerDescriptor || !descriptor) return null;
  const evaluation = updaterEvaluation(authority);

  const submit = async () => {
    if (!opened) return;
    setState({ kind: "revalidating" });
    const result = await controller.execute(
      opened.intent,
      { confirmed: true, typedValue: updaterActionTypedValue(opened.intent) },
      opened.refreshAuthority,
      opened.handler,
    );
    await onResult?.(result);
    if (result.kind === "succeeded") {
      setOpened(undefined);
      setState({ kind: "ready" });
    } else if (result.kind === "outcome_unknown") {
      setState({ kind: "outcome-unknown", nextAction: "inspect-audit" });
    } else if (result.kind === "conflict") {
      setState({ kind: "conflict", error: result.error });
    } else if (result.kind === "failed") {
      setState({ kind: "failed", error: result.error });
    } else {
      setState(result.reason === "authority-changed" ? { kind: "stale-blocked" } : { kind: "revalidation-unavailable" });
    }
  };

  return (
    <ActionAvailabilityBoundary evaluation={evaluation} translate={t} reasonPresentation="sr-only">
      {(availabilityProps) => (
        <HighRiskConfirmation
          descriptor={descriptor}
          open={opened !== undefined}
          evaluation={evaluation}
          state={state}
          translate={t}
          trigger={(confirmationProps) => (
            <Button type="button" variant={variant} size={size} className={className} title={title} aria-busy={ariaBusy} {...availabilityProps} disabled={disabled || availabilityProps.disabled || confirmationProps.disabled}>
              {icon}{label}
            </Button>
          )}
          onOpenIntent={() => {
            if (authority.permission !== "allowed" || authority.freshness !== "fresh" || authority.applicability !== "applicable") {
              setState({ kind: "revalidation-unavailable" });
              return;
            }
            setState({ kind: "ready" });
            setOpened(Object.freeze({
              intent: Object.freeze({ ...intent }),
              refreshAuthority,
              handler,
            }));
          }}
          onCloseIntent={() => { setOpened(undefined); setState({ kind: "ready" }); }}
          onConfirmIntent={() => { void submit(); }}
        />
      )}
    </ActionAvailabilityBoundary>
  );
}

function updaterEvaluation(authority: UpdaterActionAuthority): ActionEvaluation {
  if (authority.applicability === "not-applicable") {
    return Object.freeze({ visibility: Object.freeze({ kind: "hidden" as const, reason: "not-applicable" as const }), availability: Object.freeze({ kind: "blocked" as const, reasonKey: "actionStateBlocked" as const }) });
  }
  if (authority.permission === "denied") {
    return Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "denied" as const, reasonKey: "actionPermissionDenied" as const }) });
  }
  if (authority.permission === "allowed" && authority.freshness === "fresh" && authority.applicability === "applicable") {
    return Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "allowed" as const }) });
  }
  return Object.freeze({ visibility: Object.freeze({ kind: "visible" as const }), availability: Object.freeze({ kind: "unknown" as const, reasonKey: "actionPermissionUnknown" as const }) });
}
