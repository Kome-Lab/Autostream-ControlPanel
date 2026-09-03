"use client";

import { useCallback, useEffect, useRef, useState, type ComponentProps, type ReactNode } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { HighRiskConfirmation, type ConfirmationDialogState, type HighRiskConfirmationContext } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { Button } from "@/components/ui/button";
import { buildResourceActionDescriptor, resourceActionScope, type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";
import type {
  AllowedResourceActionOpen,
  ResourceActionController,
  ResourceActionExecutionResult,
} from "@/features/resources/resource-action-controller";

type SharedProps = Readonly<{
  controller: ResourceActionController;
  intent: ResourceActionIntent;
  context?: HighRiskConfirmationContext;
  onResult: (result: ResourceActionExecutionResult, intent: ResourceActionIntent) => void;
  onDispatch?: () => void;
}>;

type ControlProps = SharedProps & Readonly<{
  label: string;
  children: ReactNode;
  buttonProps?: Omit<ComponentProps<typeof Button>, "children" | "onClick">;
  disabled?: boolean;
}>;

export function ResourceActionControl({
  controller,
  intent,
  label,
  children,
  context,
  buttonProps,
  disabled = false,
  onResult,
  onDispatch,
}: ControlProps) {
  const { t } = useI18n();
  const confirmation = useResourceConfirmation(controller, intent, onResult, undefined, onDispatch);
  const descriptor = confirmation.opened?.descriptor ?? buildResourceActionDescriptor(intent);
  const evaluation = controller.evaluate(confirmation.opened?.intent ?? intent);
  if (!descriptor) return null;
  return (
    <ActionAvailabilityBoundary evaluation={evaluation} translate={t} reasonPresentation="sr-only">
      {(availabilityProps) => (
        <HighRiskConfirmation
          descriptor={descriptor}
          open={confirmation.opened !== null}
          evaluation={evaluation}
          state={confirmation.dialogState}
          context={context}
          translate={t}
          trigger={(confirmationProps) => (
            <Button
              type="button"
              aria-label={label}
              title={label}
              {...buttonProps}
              {...availabilityProps}
              disabled={disabled || availabilityProps.disabled || confirmationProps.disabled || buttonProps?.disabled}
            >
              {children}
            </Button>
          )}
          onOpenIntent={confirmation.openIntent}
          onCloseIntent={confirmation.closeIntent}
          onConfirmIntent={confirmation.confirmIntent}
        />
      )}
    </ActionAvailabilityBoundary>
  );
}

type HostProps = SharedProps & Readonly<{ onCancel: () => void }>;

export function ResourceActionConfirmationHost({ controller, intent, context, onResult, onDispatch, onCancel }: HostProps) {
  const { t } = useI18n();
  const confirmation = useResourceConfirmation(controller, intent, onResult, onCancel, onDispatch);
  const scope = resourceActionScope(intent) || "invalid";

  useEffect(() => {
    confirmation.openIntent();
  }, [scope]); // eslint-disable-line react-hooks/exhaustive-deps

  const descriptor = confirmation.opened?.descriptor ?? buildResourceActionDescriptor(intent);
  const evaluation = controller.evaluate(confirmation.opened?.intent ?? intent);
  if (!descriptor) return null;
  return (
    <HighRiskConfirmation
      descriptor={descriptor}
      open={confirmation.opened !== null}
      evaluation={evaluation}
      state={confirmation.dialogState}
      context={context}
      translate={t}
      trigger={() => <button type="button" className="hidden" aria-hidden="true" tabIndex={-1} />}
      onOpenIntent={() => undefined}
      onCloseIntent={confirmation.closeIntent}
      onConfirmIntent={confirmation.confirmIntent}
    />
  );
}

function useResourceConfirmation(
  controller: ResourceActionController,
  intent: ResourceActionIntent,
  onResult: SharedProps["onResult"],
  onCancel?: () => void,
  onDispatch?: () => void,
) {
  const [opened, setOpened] = useState<AllowedResourceActionOpen | null>(null);
  const [dialogState, setDialogState] = useState<ConfirmationDialogState>({ kind: "ready" });
  const opening = useRef(false);

  const submit = useCallback(async (allowed: AllowedResourceActionOpen) => {
    setDialogState({ kind: "revalidating" });
    await Promise.resolve();
    setDialogState({ kind: "submitting" });
    const result = await controller.submit(allowed, {
      confirmed: true,
      ...(allowed.typedToken === undefined ? {} : { typedValue: allowed.typedToken }),
    }, { onDispatch });
    onResult(result, allowed.intent);
    if (result.kind === "succeeded") {
      setOpened(null);
      setDialogState({ kind: "ready" });
      return;
    }
    setDialogState(dialogStateForResult(result));
  }, [controller, onDispatch, onResult]);

  const openIntent = useCallback(() => {
    if (opening.current) return;
    opening.current = true;
    void controller.open(intent).then((result) => {
      if (result.kind === "allowed") {
        setOpened(result);
        setDialogState({ kind: "ready" });
        return;
      }
      onResult(result, intent);
    }).finally(() => {
      opening.current = false;
    });
  }, [controller, intent, onResult]);

  const closeIntent = useCallback(() => {
    controller.cancel(opened?.intent ?? intent);
    setOpened(null);
    setDialogState({ kind: "ready" });
    onCancel?.();
  }, [controller, intent, onCancel, opened]);

  const confirmIntent = useCallback(() => {
    if (opened) void submit(opened);
  }, [opened, submit]);

  return { opened, dialogState, openIntent, closeIntent, confirmIntent } as const;
}

function dialogStateForResult(result: ResourceActionExecutionResult): ConfirmationDialogState {
  if (result.kind === "outcome_unknown") return { kind: "outcome-unknown", nextAction: result.nextAction };
  if (result.kind === "failed") {
    return result.error.kind === "conflict" || result.error.kind === "protected_state"
      ? { kind: "conflict", error: result.error }
      : { kind: "failed", error: result.error };
  }
  if (result.kind === "blocked") {
    if (result.reason === "authority-changed") return { kind: "stale-blocked" };
    if (result.reason === "reconciliation-required") return { kind: "outcome-unknown", nextAction: "inspect-audit" };
    return { kind: "revalidation-unavailable" };
  }
  return { kind: "ready" };
}
