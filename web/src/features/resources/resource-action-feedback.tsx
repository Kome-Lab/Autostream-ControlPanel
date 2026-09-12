"use client";

import { APIError } from "@/lib/api/client";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { type ResourceActionExecutionResult } from "@/features/resources/resource-action-controller";
import { type NotificationChannelTestFeedback } from "@/lib/notification-channel";
import type { TranslationKey } from "@/lib/i18n";
import { firstNonEmpty } from "./resource-values";

export function resourceHistoryConfig(path: string) {
  if (path === "/stream-logs") return { timestampField: "created_at", initialLimit: 500, pageSize: 200 } as const;
  if (path === "/observability/incidents") return { timestampField: "updated_at", initialLimit: 200, pageSize: 200 } as const;
  return null;
}

export function resourcePayloadLabel(payload: Readonly<Record<string, unknown>>) {
  return firstNonEmpty(
    typeof payload.name === "string" ? payload.name : "",
    typeof payload.username === "string" ? payload.username : "",
    typeof payload.provider_type === "string" ? payload.provider_type : "",
    "OAuth connection",
  );
}

export function resourceActionResultMessage(
  result: ResourceActionExecutionResult,
  translate: (key: TranslationKey) => string,
  successMessage: string,
) {
  if (result.kind === "succeeded") return successMessage;
  if (result.kind === "failed") return translate(result.error.messageKey);
  if (result.kind === "outcome_unknown") return translate("confirmationOutcomeUnknown");
  const key: TranslationKey = result.reason === "permission-denied"
    ? "actionPermissionDenied"
    : result.reason === "permission-unknown"
      ? "actionPermissionUnknown"
      : result.reason === "duplicate"
        ? "actionAlreadyPending"
        : result.reason === "authority-changed"
          ? "confirmationStaleBlocked"
          : result.reason === "reconciliation-required"
            ? "confirmationRefreshRequired"
            : "confirmationRevalidationUnavailable";
  return translate(key);
}

export function resourceCanEdit(resource: ResourceDefinition) {
  return Boolean(resource.form && resource.form !== "security-settings");
}

export function resourceWriteErrorMessage(resource: ResourceDefinition, error: Error, action: "作成" | "更新") {
  const fallback = `${action}できませんでした。入力内容、権限、ログイン状態を確認して再実行してください。`;
  if (!(error instanceof APIError)) return fallback;
  const common: Record<string, string> = {
    bad_request: "送信内容を確認できませんでした。入力内容を見直してください。",
    csrf_failed: "ログイン状態が古くなっています。画面を再読み込みしてから再実行してください。",
    forbidden: "この設定を変更する権限がありません。",
    permission_denied: "ロールを変更する権限がありません。",
  };
  if (common[error.code || ""]) return common[error.code || ""];
  if (resource.path === "/profiles/caption" && error.code === "caption_profile_saved_runtime_apply_failed") {
    return "字幕設定は保存されましたが、配信中のWorkerへの即時反映に失敗しました。Workerのバージョン、割り当て、接続状態を確認してから設定を再保存してください。";
  }
  if (resource.path === "/users") {
    const messages: Record<string, string> = {
      cannot_update_own_roles: "ログイン中のユーザー自身のロールは変更できません。ユーザー名またはメールアドレスだけを更新してください。",
      last_super_admin: "最後の有効なsuper_adminからロールを外すことはできません。",
      cannot_assign_super_admin: "super_adminロールを付与できるのはsuper_adminだけです。",
      permission_escalation: "自分が持っていない権限を含むロールは付与できません。",
      invalid_role_assignment: "選択したロールを割り当てられません。ロール一覧を更新して再選択してください。",
      invalid_permissions: "選択したロールに利用できない権限が含まれています。",
      create_user_failed: "ユーザーを作成できませんでした。ユーザー名やメールアドレスの重複を確認してください。",
      update_user_failed: "ユーザーを更新できませんでした。ユーザー名やメールアドレスの形式・重複を確認してください。",
    };
    if (messages[error.code || ""]) return messages[error.code || ""];
  }
  if (resource.path === "/observability/notification-channels") {
    const messages: Record<string, string> = {
      invalid_notification_channel: "通知先の必須項目が不足しています。通知方式ごとの入力内容を確認してください。",
      invalid_webhook_url: "Webhook URLを利用できません。通知方式に対応した正規HTTPS URLを指定してください。",
      invalid_smtp_channel: "共通SMTP設定を利用できません。送信先と「設定 > メールサーバー」の構成を確認してください。",
      secret_encryption_key_required: "Observabilityの秘密情報暗号化キーが未設定です。AUTOSTREAM_SECRET_ENCRYPTION_KEYを設定してObservabilityを再起動してください。",
      observability_auth_failed: "Control PanelとObservabilityのNode Runtime Tokenが一致していません。Observabilityのconfig.ymlを再発行して反映してください。",
      observability_not_configured: "登録済みのObservability Nodeが見つかりません。Node登録、公開URL、config.yml、Service Healthを確認してください。",
      observability_unavailable: "Observabilityが一時的に利用できません。Node状態とObservabilityのログを確認してください。",
      observability_rate_limited: "Observabilityへのリクエストが集中しています。少し待ってから再実行してください。",
      observability_request_rejected: "Observabilityが通知先設定を受け付けませんでした。入力内容を確認してください。",
      observability_request_failed: "Observabilityへ接続できません。Nodeの公開URL、通信経路、サービスログを確認してください。",
    };
    if (messages[error.code || ""]) return messages[error.code || ""];
  }
  return fallback;
}

export function oauthAccountRelinkErrorMessage(error: Error) {
  if (!(error instanceof APIError)) return "再連携を開始できませんでした。通信状態を確認して再試行してください。";
  const messages: Record<string, string> = {
    csrf_failed: "ログイン状態が古くなっています。画面を再読み込みしてから再試行してください。",
    oauth_account_not_found: "OAuthアカウントが見つかりません。画面を更新してください。",
    oauth_account_provider_mismatch: "対象アカウントとOAuthプロバイダが一致しません。画面を更新してください。",
    oauth_account_identity_mismatch: "別のGoogleアカウントが選択されました。元のアカウントを選んで再試行してください。",
    oauth_refresh_token_missing: "Googleから更新トークンを取得できませんでした。認可画面でアカウントを選び直してください。",
    oauth_provider_unavailable: "OAuthプロバイダが無効または未設定です。プロバイダ設定を確認してください。",
    oauth_connected_account_scope_required: "接続用途に必要な権限が許可されませんでした。認可画面で必要な権限を許可してください。",
    oauth_connected_account_redirect_uri_unavailable: "OAuthコールバックURLが設定されていません。プロバイダ設定を確認してください。",
    secret_encryption_key_required: "Control Panelのシークレット暗号化キーが未設定です。設定を確認してください。",
    forbidden: "OAuthアカウントを更新する権限がありません。",
  };
  return messages[error.code || ""] || "再連携を開始できませんでした。OAuthプロバイダ設定とログイン状態を確認してください。";
}

export function notificationChannelTestRequestError(error: Error): NotificationChannelTestFeedback {
  if (!(error instanceof APIError)) {
    return { ok: false, message: "通知テストを送信できませんでした。通信状態を確認してください。" };
  }
  const messages: Record<string, string> = {
    csrf_failed: "ログイン状態が古くなっています。画面を再読み込みしてから再実行してください。",
    forbidden: "通知テストを送信する権限がありません。",
    not_found: "通知先が見つかりません。画面を更新してください。",
    observability_auth_failed: "Control PanelとObservabilityの認証設定を確認してください。",
    observability_not_configured: "登録済みのObservability Nodeが見つかりません。",
    observability_rate_limited: "通知テストが集中しています。少し待ってから再実行してください。",
    observability_request_failed: "Observabilityへ接続できませんでした。Node状態を確認してください。",
    observability_unavailable: "Observabilityが一時的に利用できません。",
    rate_limited: "通知が集中しています。少し待ってから再実行してください。",
    smtp_not_configured: "設定 > メールサーバーで有効なSMTP設定を保存してください。",
    smtp_requires_tls: "メールサーバーのTLS設定を有効にしてください。",
    smtp_connect_failed: "メールサーバーへ接続できませんでした。設定と稼働状態を確認してください。",
    smtp_auth_failed: "メールサーバーの認証に失敗しました。認証設定を確認してください。",
    smtp_send_failed: "テストメールを送信できませんでした。メールサーバー設定とログを確認してください。",
    send_failed: "テスト通知を送信できませんでした。通知先設定とログを確認してください。",
    missing_service_scope: "Observability NodeのRuntime Tokenにメール送信権限がありません。Control PanelでRuntime Tokenを再生成し、Observabilityのconfig.ymlへ反映してサービスを再起動してください。",
    missing_service_token: "ObservabilityからControl Panelへ接続するRuntime Tokenが設定されていません。Nodeのconfig.ymlを再発行して反映してください。",
    invalid_service_token: "Control PanelとObservabilityのRuntime Tokenが一致していません。Nodeのconfig.ymlを再発行して反映してください。",
    service_token_not_registered: "Observability NodeのRuntime TokenがControl Panelに登録されていません。Node登録とRuntime Tokenを確認してください。",
    service_type_not_allowed: "Observability Nodeに別のサービス種別のRuntime Tokenが設定されています。Nodeのconfig.ymlを再発行して反映してください。",
    service_registry_not_configured: "Control PanelのNodeサービス登録を利用できません。Control Panelの設定とログを確認してください。",
    list_services_failed: "登録済みNode情報を取得できませんでした。Control Panelのログを確認してください。",
    app_settings_failed: "共通SMTP設定を読み込めませんでした。Control Panelの設定とログを確認してください。",
    secret_encryption_key_required: "Control Panelのシークレット暗号化キーが未設定です。Control Panelの設定を確認してください。",
  };
  const code = error.code || "";
  if (/^smtp_[a-z0-9_]*_failed$/.test(code)) {
    return { ok: false, message: "メール送信に失敗しました。メールサーバー設定とログを確認してください。" };
  }
  return { ok: false, message: messages[code] || "通知テストを送信できませんでした。" };
}
