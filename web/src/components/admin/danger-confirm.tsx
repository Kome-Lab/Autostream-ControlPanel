"use client";

import { ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { useI18n } from "@/components/admin/i18n-provider";

type DangerConfirmProps = {
  title: string;
  description?: string;
  children: ReactNode;
  onConfirm: () => void | Promise<void>;
  actionLabel?: string;
};

export function DangerConfirm({ title, description, children, onConfirm, actionLabel }: DangerConfirmProps) {
  const { t } = useI18n();
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>{children}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogMedia className="bg-red-50 text-red-600">
            <AlertTriangle />
          </AlertDialogMedia>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description || t("dangerousNotice")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => void onConfirm()}>
            {actionLabel || t("execute")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
