"use client";

import type { ReactNode } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";

type DeferredNodeConfirmationProps = {
  title: string;
  description?: string;
  children: ReactNode;
  onConfirm: () => void | Promise<void>;
  actionLabel?: string;
};

// Node A3 owns replacement of this intentionally isolated direct-confirmation
// boundary at V2_RC. Bundle 8B must not change the node workflow semantics.
export function DeferredNodeConfirmation({ title, description, children, onConfirm, actionLabel }: DeferredNodeConfirmationProps) {
  const { t } = useI18n();
  return (
    <ConfirmationDialogFrame
      trigger={children}
      title={title}
      description={description || t("dangerousNotice")}
      cancelLabel={t("cancel")}
      actionLabel={actionLabel || t("execute")}
      actionClosesDialog
      onConfirm={() => void onConfirm()}
    />
  );
}
