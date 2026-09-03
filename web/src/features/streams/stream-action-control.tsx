"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";

import { HighRiskConfirmation, type ConfirmationDialogState, type HighRiskConfirmationContext } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/admin/i18n-provider";
import { buildStreamActionDescriptor, streamActionScope, type StreamActionIntent } from "@/features/streams/stream-action-descriptors";
import type { AllowedStreamActionOpen, StreamActionController, StreamActionExecutionResult } from "@/features/streams/stream-action-controller";

export type StreamActionControlHandle = Readonly<{ open: () => void }>;

type Props = Readonly<{
  controller: StreamActionController;
  intent: StreamActionIntent;
  label: string;
  children: ReactNode;
  context?: HighRiskConfirmationContext;
  buttonProps?: Omit<ComponentProps<typeof Button>, "children" | "onClick">;
  disabled?: boolean;
  onResult: (result: StreamActionExecutionResult, intent: StreamActionIntent) => boolean | void | Promise<boolean | void>;
}>;

export const StreamActionControl = forwardRef<StreamActionControlHandle, Props>(function StreamActionControl({
  controller,
  intent,
  label,
  children,
  context,
  buttonProps,
  disabled = false,
  onResult,
}, ref) {
  const { t } = useI18n();
  const [opened, setOpened] = useState<AllowedStreamActionOpen | null>(null);
  const [dialogState, setDialogState] = useState<ConfirmationDialogState>({ kind: "ready" });
  const opening = useRef(false);
  const scope = streamActionScope(intent) || "invalid";
  const descriptor = opened?.descriptor ?? buildStreamActionDescriptor(intent);
  const evaluation = controller.evaluate(opened?.intent ?? intent);

  useEffect(() => {
    setOpened(null);
    setDialogState({ kind: "ready" });
    opening.current = false;
  }, [scope]);

  const submit = useCallback(async (allowed: AllowedStreamActionOpen) => {
    setDialogState({ kind: "revalidating" });
    await Promise.resolve();
    setDialogState({ kind: "submitting" });
    const result = await controller.submit(allowed, {
      confirmed: true,
      ...(allowed.typedToken === undefined ? {} : { typedValue: allowed.typedToken }),
    });
    const authorityRefreshed = await onResult(result, allowed.intent);
    if (result.kind === "succeeded") {
      setOpened(null);
      setDialogState({ kind: "ready" });
      return;
    }
    if (result.kind === "failed" && allowed.descriptor.retry.kind === "manual-after-refresh" && authorityRefreshed === true) {
      // Deterministic failures may return to the trigger only after its owner
      // proves a fresh authority snapshot. The next attempt remains deliberate;
      // this path never resends automatically and excludes ambiguous outcomes.
      setOpened(null);
      setDialogState({ kind: "ready" });
      return;
    }
    setDialogState(dialogStateForResult(result));
  }, [controller, onResult]);

  const open = useCallback(() => {
    if (disabled || opening.current) return;
    opening.current = true;
    void controller.open(intent).then((result) => {
      if (result.kind === "allowed") {
        if (result.descriptor.confirmation.mode === "none") {
          setDialogState({ kind: "submitting" });
          void submit(result);
        } else {
          setOpened(result);
          setDialogState({ kind: "ready" });
        }
        return;
      }
      onResult(result, intent);
    }).finally(() => {
      opening.current = false;
    });
  }, [controller, disabled, intent, onResult, submit]);

  useImperativeHandle(ref, () => ({ open }), [open]);

  if (!descriptor) return null;
  return (
    <ActionAvailabilityBoundary evaluation={evaluation} translate={t} reasonPresentation="sr-only">
      {(availabilityProps) => descriptor.confirmation.mode === "none" ? (
        <Button
          type="button"
          aria-label={label}
          title={label}
          {...buttonProps}
          {...availabilityProps}
          disabled={disabled || availabilityProps.disabled || buttonProps?.disabled}
          onClick={open}
        >
          {children}
        </Button>
      ) : (
        <HighRiskConfirmation
          descriptor={descriptor}
          open={opened !== null}
          evaluation={evaluation}
          state={dialogState}
          context={context}
          translate={t}
          trigger={(confirmationProps) => (
            <Button
              type={buttonProps?.type ?? "button"}
              aria-label={label}
              title={label}
              {...buttonProps}
              {...availabilityProps}
              disabled={disabled || availabilityProps.disabled || confirmationProps.disabled || buttonProps?.disabled}
            >
              {children}
            </Button>
          )}
          onOpenIntent={open}
          onCloseIntent={() => {
            controller.cancel(opened?.intent ?? intent);
            setOpened(null);
            setDialogState({ kind: "ready" });
          }}
          onConfirmIntent={() => {
            if (opened) void submit(opened);
          }}
        />
      )}
    </ActionAvailabilityBoundary>
  );
});

function dialogStateForResult(result: StreamActionExecutionResult): ConfirmationDialogState {
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
