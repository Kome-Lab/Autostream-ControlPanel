
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";
import { isUpdaterHostBootstrapJobActive, updaterHostBootstrapEligibility } from "@/lib/updater-bootstrap";


export type Feedback = {
  tone: "success" | "pending" | "error";
  message: string;
};

export function bootstrapResultSafeMessage(status?: string, uiText: UICopy = japaneseCopy) {
  const messages: Record<string, string> = {
    succeeded: uiText("検証済みhelperの導入と動作確認が完了しました。"),
    failed: uiText("セットアップは完了しませんでした。状態を再取得し、安全な手順で確認してください。"),
    partial_failed: uiText("一部の手順が完了しませんでした。状態を再取得して確認してください。"),
    credential_expired: uiText("一時認証情報の有効期間が終了しました。再実行時は新しい認証情報を入力してください。"),
    running: uiText("暗号化された要求を独立Updaterが処理しています。"),
    queued: uiText("独立Updaterでの処理開始を待っています。"),
  };
  return messages[String(status || "").toLowerCase()] || uiText("セットアップ状態を独立Updaterから取得しました。");
}

export function eligibilityStatus(reason: ReturnType<typeof updaterHostBootstrapEligibility>["reason"] | undefined, statusKnown: boolean) {
  if (!statusKnown) return "checking";
  const statuses: Record<string, string> = {
    updater_offline: "updater_offline",
    policy_pending: "policy_pending",
    release_token_pending: "release_token_pending",
    host_unsaved: "host_unsaved",
    host_key_pending: "host_key_pending",
    client_key_pending: "client_key_pending",
    encryption_key_pending: "encryption_key_pending",
    unsupported_profile: "unsupported_profile",
    bootstrap_active: "running",
    already_configured: "succeeded",
  };
  return reason ? statuses[reason] || "blocked" : "";
}

export function bootstrapBadgeTone(status?: string): "default" | "secondary" | "destructive" | "outline" {
  if (status === "succeeded") return "default";
  if (status === "failed" || status === "partial_failed" || status === "credential_expired" || status === "updater_offline") return "destructive";
  if (isUpdaterHostBootstrapJobActive(status)) return "secondary";
  return "outline";
}
