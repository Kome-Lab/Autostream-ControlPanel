"use client";

import { useState, type ReactNode } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { HighRiskConfirmation, type ConfirmationDialogState } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { Button } from "@/components/ui/button";
import {
  buildAccountActionDescriptor,
  type AccountActionController,
  type AccountActionIntent,
  type AccountActionResult,
  type AccountAuthoritySnapshot,
} from "@/features/account/account-action-policy";
import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";

export function AccountActionConfirmation({
  controller,
  intent,
  authority,
  refreshAuthority,
  label,
  icon,
  variant = "outline",
  className,
  disabled = false,
  handler,
  onSucceeded,
  onOutcomeUnknown,
}: Readonly<{
  controller: AccountActionController;
  intent: AccountActionIntent;
  authority: AccountAuthoritySnapshot;
  refreshAuthority: () => Promise<AccountAuthoritySnapshot>;
  label: string;
  icon?: ReactNode;
  variant?: "default" | "outline" | "destructive";
  className?: string;
  disabled?: boolean;
  handler: () => Promise<unknown>;
  onSucceeded?: (value: unknown) => void | Promise<void>;
  onOutcomeUnknown?: () => void;
}>) {
  const { t } = useI18n();
  const evaluation = accountEvaluation(authority);
  const [opened, setOpened] = useState<AccountActionIntent>();
  const [state, setState] = useState<ConfirmationDialogState>({ kind: "ready" });
  const descriptor = buildAccountActionDescriptor(intent);
  if (!descriptor) return null;

  const open = () => {
    if (authority.session === "unavailable" || authority.freshness !== "fresh") {
      setState({ kind: "revalidation-unavailable" });
      return;
    }
    setOpened(Object.freeze({ ...intent, authorityRevision: authority.revision }));
    setState({ kind: "ready" });
  };

  const submit = async () => {
    if (!opened) return;
    setState({ kind: "revalidating" });
    const refreshed = await refreshAuthority();
    if (refreshed.session === "unavailable" || refreshed.freshness !== "fresh") {
      setState({ kind: "revalidation-unavailable" });
      return;
    }
    if (refreshed.revision !== opened.authorityRevision) {
      setState({ kind: "stale-blocked" });
      return;
    }
    setState({ kind: "submitting" });
    const result = await controller.execute(
      opened,
      {
        confirmed: true,
        typedValue: descriptor.confirmation.mode === "typed-target" ? opened.publicUsername : undefined,
      },
      handler,
    );
    await handleAccountResult(result, onSucceeded, onOutcomeUnknown, setOpened, setState);
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
            <Button
              type="button"
              variant={variant}
              className={className}
              {...availabilityProps}
              disabled={disabled || availabilityProps.disabled || confirmationProps.disabled}
            >
              {icon}{label}
            </Button>
          )}
          onOpenIntent={open}
          onCloseIntent={() => {
            setOpened(undefined);
            setState({ kind: "ready" });
          }}
          onConfirmIntent={() => { void submit(); }}
        />
      )}
    </ActionAvailabilityBoundary>
  );
}

async function handleAccountResult(
  result: AccountActionResult,
  onSucceeded: ((value: unknown) => void | Promise<void>) | undefined,
  onOutcomeUnknown: (() => void) | undefined,
  setOpened: (value: AccountActionIntent | undefined) => void,
  setState: (value: ConfirmationDialogState) => void,
) {
  if (result.kind === "succeeded") {
    await onSucceeded?.(result.value);
    setOpened(undefined);
    setState({ kind: "ready" });
    return;
  }
  if (result.kind === "outcome_unknown") {
    onOutcomeUnknown?.();
    setState({ kind: "outcome-unknown", nextAction: result.nextAction });
    return;
  }
  if (result.kind === "failed") {
    setState(result.error.kind === "conflict"
      ? { kind: "conflict", error: result.error }
      : { kind: "failed", error: result.error });
    return;
  }
  setState(result.reason === "authority-changed"
    ? { kind: "stale-blocked" }
    : { kind: "revalidation-unavailable" });
}

function accountEvaluation(authority: AccountAuthoritySnapshot): ActionEvaluation {
  if (authority.session !== "unavailable" && authority.freshness === "fresh") {
    return Object.freeze({
      visibility: Object.freeze({ kind: "visible" as const }),
      availability: Object.freeze({ kind: "allowed" as const }),
    });
  }
  return Object.freeze({
    visibility: Object.freeze({ kind: "visible" as const }),
    availability: Object.freeze({ kind: "unknown" as const, reasonKey: "actionPermissionUnknown" as const }),
  });
}
