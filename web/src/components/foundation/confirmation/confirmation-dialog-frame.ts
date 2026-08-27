"use client";

import {
  createElement,
  type ComponentProps,
  type MouseEvent,
  type ReactNode,
} from "react";
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
import { Button } from "@/components/ui/button";

export type ConfirmationDialogFrameStatus = Readonly<{
  role: "status" | "alert";
  content: ReactNode;
}>;

export type ConfirmationDialogFrameProps = Readonly<{
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  trigger: ReactNode;
  title: ReactNode;
  description: ReactNode;
  children?: ReactNode;
  cancelLabel: ReactNode;
  actionLabel: ReactNode;
  onConfirm: (event: MouseEvent<HTMLButtonElement>) => void | Promise<void>;
  actionDisabled?: boolean;
  cancelDisabled?: boolean;
  actionClosesDialog?: boolean;
  showAction?: boolean;
  busy?: boolean;
  status?: ConfirmationDialogFrameStatus;
  actionResetKey?: string;
}>;

export function ConfirmationDialogFrame({
  open,
  onOpenChange,
  trigger,
  title,
  description,
  children,
  cancelLabel,
  actionLabel,
  onConfirm,
  actionDisabled = false,
  cancelDisabled = false,
  actionClosesDialog = false,
  showAction = true,
  busy = false,
  status,
  actionResetKey,
}: ConfirmationDialogFrameProps): ReactNode {
  const rootProps = open === undefined
    ? { ...(onOpenChange ? { onOpenChange } : {}) }
    : { open, ...(onOpenChange ? { onOpenChange } : {}) };
  const legacyActionProps: ComponentProps<typeof AlertDialogAction> &
    Readonly<{ "data-confirm-action": string }> = {
    variant: "destructive",
    disabled: actionDisabled,
    "data-confirm-action": "",
    onClick: onConfirm,
  };
  const controlledActionProps: ComponentProps<typeof Button> &
    Readonly<{ "data-confirm-action": string; key?: string }> = {
    ...(actionResetKey ? { key: actionResetKey } : {}),
    type: "button",
    variant: "destructive",
    disabled: actionDisabled,
    "data-confirm-action": "",
    onClick: onConfirm,
  };
  const action = showAction
    ? actionClosesDialog
      ? createElement(
          AlertDialogAction,
          legacyActionProps,
          actionLabel,
        )
      : createElement(
          Button,
          controlledActionProps,
          actionLabel,
        )
    : null;

  return createElement(
    AlertDialog,
    rootProps,
    createElement(AlertDialogTrigger, { asChild: true }, trigger),
    createElement(
      AlertDialogContent,
      {
        ...(busy ? { "aria-busy": true } : {}),
        onEscapeKeyDown: (event) => {
          if (cancelDisabled) event.preventDefault();
        },
      },
      createElement(
        AlertDialogHeader,
        null,
        createElement(
          AlertDialogMedia,
          { className: "bg-red-50 text-red-600" },
          createElement(AlertTriangle),
        ),
        createElement(AlertDialogTitle, null, title),
        createElement(AlertDialogDescription, null, description),
      ),
      children,
      status
        ? createElement(
            "p",
            status.role === "status"
              ? { role: "status", "aria-live": "polite" }
              : { role: "alert" },
            status.content,
          )
        : null,
      createElement(
        AlertDialogFooter,
        null,
        createElement(
          AlertDialogCancel,
          { disabled: cancelDisabled },
          cancelLabel,
        ),
        action,
      ),
    ),
  );
}
