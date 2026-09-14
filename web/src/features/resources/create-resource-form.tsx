"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { DraftExitContext, useDraftExit } from "@/components/forms/draft-exit";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { apiPost } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";
import { resourceCopy } from "@/lib/i18n/ui-v2/resource-copy";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { ResourceActionConfirmationHost } from "@/features/resources/resource-action-control";
import { type ResourceActionController, type ResourceActionExecutionResult } from "@/features/resources/resource-action-controller";
import { resourceActionID, type ResourceActionIntent, type ResourceActionOperation } from "@/features/resources/resource-action-descriptors";
import { type PendingResourceSubmission, type Submission, type SubmitResource } from "./resource-form-types";
import { isSensitiveKey } from "./resource-presentation";
import { isRecord } from "./resource-values";
import { resourceWriteErrorMessage, resourcePayloadLabel, resourceActionResultMessage } from "./resource-action-feedback";
import { requiredPermissionText } from "./resource-permissions";
import { ResourceFormFields } from "./resource-form-fields";

export function CreateResourceForm({ resource, allowed, permission, controller }: { resource: ResourceDefinition; allowed: boolean; permission?: string; controller: ResourceActionController }) {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const copy = resourceCopy(resource, locale);
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState<PendingResourceSubmission | null>(null);
  const [dispatching, setDispatching] = useState(false);
  const hasSensitiveFields = Object.keys(resource.createTemplate || {}).some(isSensitiveKey);
  const legacyMutation = useMutation<unknown, Error, Submission>({
    mutationFn: async (submission) => apiPost(submission.path, submission.payload),
    onSuccess: async (data, submission) => {
      draftExit.saved();
      setMessage(submission.successMessage);
      await queryClient.invalidateQueries({ queryKey: ["resource", submission.invalidatePath] });
      if (submission.redirectToAuthorizationURL) {
        const authorizationURL = isRecord(data) && typeof data.authorization_url === "string" ? data.authorization_url : "";
        if (authorizationURL && typeof window !== "undefined") {
          window.location.assign(authorizationURL);
        } else {
          setMessage(uiText("OAuth認可URLを取得できませんでした。プロバイダ設定を確認してください。"));
        }
      }
    },
    onError: (error) => setMessage(resourceWriteErrorMessage(resource, error, "作成", uiText)),
  });

  const draftExit = useDraftExit({ enabled: open && allowed, pending: legacyMutation.isPending || Boolean(pending) || dispatching });
  const submit: SubmitResource = (payload, options) => {
    setMessage("");
    const submission: Submission = {
      path: options?.path || resource.path,
      payload,
      invalidatePath: options?.invalidatePath || resource.path,
      successMessage: options?.successMessage || uiText("作成しました。"),
      redirectToAuthorizationURL: options?.redirectToAuthorizationURL,
    };
    const operation: ResourceActionOperation = submission.path === "/integrations/oauth-accounts/start" ? "connect" : "create";
    const actionID = resourceActionID(resource.path, operation);
    if (!actionID) {
      legacyMutation.mutate(submission);
      options?.onSensitiveDispatched?.();
      return;
    }
    const intents: ResourceActionIntent[] = [];
    if (options?.secretValue) {
      intents.push(Object.freeze({ id: "RES-13", payload: Object.freeze({ value: options.secretValue }), publicLabel: "Deepgram API key" }));
    }
    intents.push(Object.freeze({ id: actionID, payload: Object.freeze({ ...payload }), publicLabel: resourcePayloadLabel(payload) }));
    setPending({
      path: submission.path,
      invalidatePath: submission.invalidatePath,
      successMessage: submission.successMessage,
      redirectToAuthorizationURL: submission.redirectToAuthorizationURL,
      intents: Object.freeze(intents),
      index: 0,
      onSensitiveDispatched: options?.onSensitiveDispatched,
    });
  };

  const handleResult = (submission: PendingResourceSubmission, result: ResourceActionExecutionResult, intent: ResourceActionIntent) => {
    setDispatching(false);
    setMessage(resourceActionResultMessage(result, t, submission.successMessage));
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
    void queryClient.invalidateQueries({ queryKey: ["resource", submission.invalidatePath] });
    if (submission.redirectToAuthorizationURL) {
      const authorizationURL = isRecord(result.value) && typeof result.value.authorization_url === "string" ? result.value.authorization_url : "";
      if (authorizationURL && typeof window !== "undefined") {
        window.location.assign(authorizationURL);
        return;
      }
      setMessage(uiText("OAuth認可URLを取得できませんでした。プロバイダ設定を確認してください。"));
      return;
    }
    draftExit.saved();
    setOpen(false);
  };

  return (
    <DraftExitContext.Provider value={draftExit}><div className="flex flex-wrap items-center justify-between gap-3">
      <div>
        <div className="font-medium">{locale === "ja" ? "登録済み設定" : "Configured records"}</div>
        <p className="text-sm text-muted-foreground">{locale === "ja" ? "新しい設定はポップアップで作成します。" : "Create a record in the dialog. Existing records keep their individual actions."}</p>
      </div>
      <Dialog open={open} onOpenChange={(value) => {
        if (!value && !draftExit.request(() => setOpen(false))) return;
        if (value) setOpen(true);
        if (!value && !legacyMutation.isPending && !pending && !dispatching) setMessage("");
      }}>
        <DialogTrigger asChild>
          <Button size="sm" disabled={!allowed} title={allowed ? undefined : requiredPermissionText(permission, uiText)}>
            <Plus className="size-4" />
            {locale === "ja" ? "新規作成" : "Create"}
          </Button>
        </DialogTrigger>
        <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{locale === "ja" ? copy.title + "を作成" : "Create " + copy.title}</DialogTitle>
            <DialogDescription>
              {hasSensitiveFields
                ? uiText("必要項目を入力します。秘密情報はAPI側のシークレットストアに保存されます。")
                : uiText("必要項目を入力します。作成後も一覧から確認・編集できます。")}
            </DialogDescription>
          </DialogHeader>
          <ResourceFormFields resource={resource} disabled={legacyMutation.isPending || Boolean(pending) || dispatching || !allowed} submit={submit} />
          {message ? <p role="status" className="text-sm text-muted-foreground">{message}</p> : null}
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
        </DialogContent>
      </Dialog>
      {!allowed ? <p className="w-full text-xs text-muted-foreground">{requiredPermissionText(permission, uiText)}</p> : null}
    </div></DraftExitContext.Provider>
  );
}
