"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useState } from "react";
import { RefreshCcw } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { ResourceActionControl } from "@/features/resources/resource-action-control";
import { type ResourceActionController } from "@/features/resources/resource-action-controller";
import { type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";
import { type ResourceRow } from "./resource-form-types";
import { resourceRowID, rowString, resourceRowLabel, isRecord } from "./resource-values";
import { resourceActionResultMessage } from "./resource-action-feedback";

export function OAuthAccountRelinkButton({ row, disabled, controller }: { row: ResourceRow; disabled: boolean; controller: ResourceActionController }) {
  const uiText = useUICopy();
  const { t } = useI18n();
  const [message, setMessage] = useState("");
  const accountID = resourceRowID(row);
  const providerID = rowString(row, ["provider_id"]);
  const accountPurpose = rowString(row, ["account_purpose"]) || "drive_youtube";
  const intent: ResourceActionIntent = Object.freeze({
    id: "RES-27",
    row: Object.freeze({ ...row }),
    publicLabel: resourceRowLabel(row, uiText),
    payload: Object.freeze({
      provider_id: providerID,
      oauth_account_id: accountID,
      account_purpose: accountPurpose,
      redirect_after: "/admin/integrations/",
    }),
  });

  return (
    <div className="flex flex-col items-end gap-1">
      <ResourceActionControl
        controller={controller}
        intent={intent}
        label={uiText("{0} を再連携", resourceRowLabel(row, uiText))}
        disabled={!accountID || !providerID || disabled}
        buttonProps={{ variant: "outline", size: "sm" }}
        onResult={(result) => {
          setMessage(resourceActionResultMessage(result, t, uiText("OAuth再連携を開始しました。")));
          if (result.kind !== "succeeded") return;
          const authorizationURL = isRecord(result.value) && typeof result.value.authorization_url === "string" ? result.value.authorization_url : "";
          if (authorizationURL && typeof window !== "undefined") {
            window.location.assign(authorizationURL);
            return;
          }
          setMessage(uiText("OAuth認可URLを取得できませんでした。プロバイダ設定を確認してください。"));
        }}
      >
        <RefreshCcw className="size-4" />
        {uiText("再連携")}</ResourceActionControl>
      {message ? <span className="max-w-48 text-left text-xs text-destructive">{message}</span> : null}
    </div>
  );
}
