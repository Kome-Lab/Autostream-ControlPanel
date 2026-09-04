"use client";

import type { ReactNode } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";

type DeferredUpdaterHostConfirmationProps = {
  title: string;
  description?: string;
  children: ReactNode;
  onConfirm: () => void | Promise<void>;
  actionLabel?: string;
};

// UI-FOUNDATION-001B-B09E-UPDATER-UI owns migration of this preserved operator
// workflow to the independent Updater adapter. Bundle 8B only isolates it from
// the removed global compatibility wrapper.
export function DeferredUpdaterHostConfirmation({ title, description, children, onConfirm, actionLabel }: DeferredUpdaterHostConfirmationProps) {
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
