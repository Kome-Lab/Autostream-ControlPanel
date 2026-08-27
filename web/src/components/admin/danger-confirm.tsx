"use client";

import type { ReactNode } from "react";

import { useI18n } from "@/components/admin/i18n-provider";
import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";

type DangerConfirmProps = {
  title: string;
  description?: string;
  children: ReactNode;
  onConfirm: () => void | Promise<void>;
  actionLabel?: string;
};

/**
 * @deprecated New actions must use HighRiskConfirmation with a Feature-owned
 * controller. Existing direct consumers remain temporarily source-compatible.
 */
export function DangerConfirm({ title, description, children, onConfirm, actionLabel }: DangerConfirmProps) {
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
