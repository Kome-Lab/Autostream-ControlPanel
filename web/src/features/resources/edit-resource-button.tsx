"use client";
import { resourceCopy } from "@/lib/i18n/ui-v2/resource-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { DraftExitContext, useDraftExit } from "@/components/forms/draft-exit";

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

const timestampGuardedPaths = new Set(["/profiles/encoder", "/profiles/archive", "/profiles/caption", "/profiles/overlay", "/discord/configs", "/youtube/outputs", "/archive/destinations"]);

export function EditResourceButton({ resource, row, disabled, controller }: { resource: ResourceDefinition; row: ResourceRow; disabled: boolean; controller: ResourceActionController }) {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState<PendingResourceSubmission | null>(null);
  const [dispatching, setDispatching] = useState(false);
  const [baseline, setBaseline] = useState<ResourceRow | null>(null);
  const editRow = baseline ?? row;
  const id = resourceRowID(editRow);
  const copy = resourceCopy(resource, locale);
  const legacyMutation = useMutation<unknown, Error, Record<string, unknown>>({
    mutationFn: async (payload) => apiPut(`${resource.path}/${encodeURIComponent(id)}`, payload),
    onSuccess: async () => {
      draftExit.saved();
      setMessage(uiText("更新しました。"));
      await queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
    },
    onError: async (error) => {
      setMessage(resourceWriteErrorMessage(resource, error, "更新", uiText));
      if (error instanceof APIError && error.code === "caption_profile_saved_runtime_apply_failed") {
        await queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
      }
    },
  });
  const draftExit = useDraftExit({ enabled: open && !disabled, pending: legacyMutation.isPending || Boolean(pending) || dispatching });
  const submit: SubmitResource = (payload, options) => {
    setMessage("");
    const expectedUpdatedAt = editRow.updated_at;
    if (timestampGuardedPaths.has(resource.path) && (typeof expectedUpdatedAt !== "string" || !expectedUpdatedAt)) {
      setMessage(locale === "ja" ? "編集開始時の更新日時を確認できません。入力を保持しています。最新状態を確認してから再編集してください。" : "The edit's original update timestamp is unavailable. Your input is retained. Check the latest state, then edit again.");
      return;
    }
    const guardedPayload = timestampGuardedPaths.has(resource.path) ? { ...payload, expected_updated_at: expectedUpdatedAt } : payload;
    const actionID = resourceActionID(resource.path, "update");
    if (!actionID) {
      legacyMutation.mutate(guardedPayload);
      options?.onSensitiveDispatched?.();
      return;
    }
    const intents: ResourceActionIntent[] = [];
    if (options?.secretValue) {
      intents.push(Object.freeze({ id: "RES-13", payload: Object.freeze({ value: options.secretValue }), publicLabel: "Deepgram API key" }));
    }
    intents.push(Object.freeze({ id: actionID, row: Object.freeze({ ...editRow }), payload: Object.freeze({ ...guardedPayload }), publicLabel: resourceRowLabel(editRow, uiText) }));
    setPending({
      path: `${resource.path}/${encodeURIComponent(id)}`,
      invalidatePath: resource.path,
      successMessage: uiText("更新しました。"),
      intents: Object.freeze(intents),
      index: 0,
      onSensitiveDispatched: options?.onSensitiveDispatched,
    });
  };

  const handleResult = (submission: PendingResourceSubmission, result: ResourceActionExecutionResult, intent: ResourceActionIntent) => {
    setDispatching(false);
    setMessage(resourceActionResultMessage(result, t, uiText("更新しました。")));
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
    draftExit.saved();
    setOpen(false);
  };

  return (
    <DraftExitContext.Provider value={draftExit}><Dialog open={open} onOpenChange={(value) => {
      if (value) { setBaseline(Object.freeze({ ...row })); setOpen(true); }
      else draftExit.request(() => { setOpen(false); setBaseline(null); });
    }}>
      <DialogTrigger asChild>
          <Button variant="outline" size="icon-sm" disabled={!id || disabled} title={disabled ? requiredPermissionText(resource.permissions?.update, uiText) : undefined} aria-label={uiText("{0} を編集", resourceRowLabel(row, uiText))}>
          <Pencil className="size-4" />
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{locale === "ja" ? copy.title + uiText("を編集") : "Edit " + copy.title}</DialogTitle>
          <DialogDescription>{resource.form === "user" ? uiText("ユーザー名、メールアドレス、割り当てロールを更新します。") : uiText("作成済みの設定をフォームで更新します。秘密情報は空欄のまま更新すると既存値を保持する項目があります。")}</DialogDescription>
        </DialogHeader>
        <ResourceFormFields resource={resource} disabled={legacyMutation.isPending || Boolean(pending) || dispatching} submit={submit} initial={editRow} submitLabel={locale === "ja" ? uiText("更新") : "Update"} />
        {baseline && row.updated_at !== baseline.updated_at ? <p role="alert" className="text-sm text-destructive">{locale === "ja"
          ? "この設定は別の操作で更新されています。入力は保持しています。最新状態を確認し、必要なら閉じて再編集してください。"
          : "This setting changed in another operation. Your input is retained. Check the latest state, then close and edit again if needed."}</p> : null}
        <Button type="button" variant="outline" disabled={legacyMutation.isPending || Boolean(pending) || dispatching} onClick={() => {
          void queryClient.refetchQueries({ queryKey: ["resource", resource.path], exact: true });
        }}>{locale === "ja" ? "最新状態を確認" : "Check latest state"}</Button>
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
            <Button variant="outline">{uiText("閉じる")}</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog></DraftExitContext.Provider>
  );
}
