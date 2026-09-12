"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Pencil } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { APIError, apiPut } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { ResourceActionConfirmationHost } from "@/features/resources/resource-action-control";
import { type ResourceActionController, type ResourceActionExecutionResult } from "@/features/resources/resource-action-controller";
import { resourceActionID, type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";
import { type ResourceRow, type PendingResourceSubmission, type SubmitResource } from "./resource-form-types";
import { resourceRowID, resourceRowLabel } from "./resource-values";
import { resourceWriteErrorMessage, resourceActionResultMessage } from "./resource-action-feedback";
import { requiredPermissionText } from "./resource-permissions";
import { ResourceFormFields } from "./resource-form-fields";

export function EditResourceButton({ resource, row, disabled, controller }: { resource: ResourceDefinition; row: ResourceRow; disabled: boolean; controller: ResourceActionController }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState<PendingResourceSubmission | null>(null);
  const [dispatching, setDispatching] = useState(false);
  const id = resourceRowID(row);
  const legacyMutation = useMutation<unknown, Error, Record<string, unknown>>({
    mutationFn: async (payload) => apiPut(`${resource.path}/${encodeURIComponent(id)}`, payload),
    onSuccess: async () => {
      setMessage("更新しました。");
      await queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
    },
    onError: async (error) => {
      setMessage(resourceWriteErrorMessage(resource, error, "更新"));
      if (error instanceof APIError && error.code === "caption_profile_saved_runtime_apply_failed") {
        await queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
      }
    },
  });
  const submit: SubmitResource = (payload, options) => {
    setMessage("");
    const actionID = resourceActionID(resource.path, "update");
    if (!actionID) {
      legacyMutation.mutate(payload);
      options?.onSensitiveDispatched?.();
      return;
    }
    const intents: ResourceActionIntent[] = [];
    if (options?.secretValue) {
      intents.push(Object.freeze({ id: "RES-13", payload: Object.freeze({ value: options.secretValue }), publicLabel: "Deepgram API key" }));
    }
    intents.push(Object.freeze({ id: actionID, row: Object.freeze({ ...row }), payload: Object.freeze({ ...payload }), publicLabel: resourceRowLabel(row) }));
    setPending({
      path: `${resource.path}/${encodeURIComponent(id)}`,
      invalidatePath: resource.path,
      successMessage: "更新しました。",
      intents: Object.freeze(intents),
      index: 0,
      onSensitiveDispatched: options?.onSensitiveDispatched,
    });
  };

  const handleResult = (submission: PendingResourceSubmission, result: ResourceActionExecutionResult, intent: ResourceActionIntent) => {
    setDispatching(false);
    setMessage(resourceActionResultMessage(result, t, "更新しました。"));
    if (result.kind !== "succeeded") {
      if (result.kind === "blocked") setPending(null);
      return;
    }
    if (intent.id === "RES-13") {
      void queryClient.invalidateQueries({ queryKey: ["resource", "/secrets/status"] });
    }
    const remainingIntents = submission.intents.slice(submission.index + 1);
    if (remainingIntents.length > 0) {
      setPending({
        path: submission.path,
        invalidatePath: submission.invalidatePath,
        successMessage: submission.successMessage,
        redirectToAuthorizationURL: submission.redirectToAuthorizationURL,
        intents: Object.freeze(remainingIntents),
        index: 0,
      });
      return;
    }
    setPending(null);
    void queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
    setOpen(false);
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
          <Button variant="outline" size="icon-sm" disabled={!id || disabled} title={disabled ? requiredPermissionText(resource.permissions?.update) : undefined} aria-label={`${resourceRowLabel(row)} を編集`}>
          <Pencil className="size-4" />
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{resource.title}を編集</DialogTitle>
          <DialogDescription>{resource.form === "user" ? "ユーザー名、メールアドレス、割り当てロールを更新します。" : "作成済みの設定をフォームで更新します。秘密情報は空欄のまま更新すると既存値を保持する項目があります。"}</DialogDescription>
        </DialogHeader>
        <ResourceFormFields resource={resource} disabled={legacyMutation.isPending || Boolean(pending) || dispatching} submit={submit} initial={row} submitLabel="更新" />
        {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
        {pending ? (
          <ResourceActionConfirmationHost
            key={`${pending.intents[pending.index].id}:${pending.index}`}
            controller={controller}
            intent={pending.intents[pending.index]}
            onDispatch={() => {
              pending.onSensitiveDispatched?.();
              setPending(null);
              setDispatching(true);
            }}
            onResult={(result, intent) => handleResult(pending, result, intent)}
            onCancel={() => setPending(null)}
          />
        ) : null}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">閉じる</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
