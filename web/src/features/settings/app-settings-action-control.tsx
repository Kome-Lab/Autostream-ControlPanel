"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { HighRiskConfirmation, type ConfirmationDialogState } from "@/components/foundation/confirmation/high-risk-confirmation";
import type {
  AllowedAppSettingsActionOpen,
  AppSettingsActionController,
  AppSettingsActionIntent,
  AppSettingsActionResult,
} from "@/features/settings/app-settings-action-policy";
import { buildAppSettingsActionDescriptor } from "@/features/settings/app-settings-action-policy";

export function AppSettingsActionConfirmationHost({
  controller,
  intent,
  onResult,
  onDispatch,
  onCancel,
}: Readonly<{
  controller: AppSettingsActionController;
  intent: AppSettingsActionIntent;
  onResult: (result: AppSettingsActionResult, intent: AppSettingsActionIntent) => void;
  onDispatch?: () => void;
  onCancel: () => void;
}>) {
  const { t } = useI18n();
  const [opened, setOpened] = useState<AllowedAppSettingsActionOpen | null>(null);
  const [dialogState, setDialogState] = useState<ConfirmationDialogState>({ kind: "ready" });
  const opening = useRef(false);

  const openIntent = useCallback(() => {
    if (opening.current) return;
    opening.current = true;
    void controller.open(intent).then((result) => {
      if (result.kind === "allowed") {
        setOpened(result);
        setDialogState({ kind: "ready" });
      } else {
        onResult(result, intent);
      }
    }).finally(() => {
      opening.current = false;
    });
  }, [controller, intent, onResult]);

  useEffect(() => {
    openIntent();
  }, [openIntent]);

  const submit = useCallback(async () => {
    if (!opened) return;
    setDialogState({ kind: "revalidating" });
    await Promise.resolve();
    setDialogState({ kind: "submitting" });
    const result = await controller.submit(opened, { confirmed: true }, { onDispatch });
    onResult(result, opened.intent);
    if (result.kind === "succeeded") {
      setOpened(null);
      setDialogState({ kind: "ready" });
      return;
    }
    setDialogState(dialogStateForResult(result));
  }, [controller, onDispatch, onResult, opened]);

  const descriptor = opened?.descriptor ?? buildAppSettingsActionDescriptor(intent);
  const evaluation = controller.evaluate(opened?.intent ?? intent);
  if (!descriptor) return null;
  return (
    <HighRiskConfirmation
      descriptor={descriptor}
      open={opened !== null}
      evaluation={evaluation}
      state={dialogState}
      translate={t}
      trigger={() => <button type="button" className="hidden" aria-hidden="true" tabIndex={-1} />}
      onOpenIntent={() => undefined}
      onCloseIntent={() => {
        controller.cancel(opened?.intent ?? intent);
        setOpened(null);
        setDialogState({ kind: "ready" });
        onCancel();
      }}
      onConfirmIntent={() => void submit()}
    />
  );
}

function dialogStateForResult(result: AppSettingsActionResult): ConfirmationDialogState {
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
