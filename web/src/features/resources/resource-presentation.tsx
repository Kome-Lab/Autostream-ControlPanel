"use client";
import { oauthAccountName, fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { type ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { auditActionLabel } from "@/lib/audit-action";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { notificationChannelTypeLabel, notificationDeliveryPresentation } from "@/lib/notification-channel";
import { oauthAccountConfiguredName, oauthAccountPurposeLabel, oauthProviderTypeLabel as providerTypeLabel } from "@/lib/oauth-account";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { type ResourceRow } from "./resource-form-types";
import { rowString, rowValue, isRecord } from "./resource-values";
import { permissionGroupForValue, permissionDescription, permissionLabel } from "./resource-permissions";

export function enrichResourceRow(resource: ResourceDefinition, row: ResourceRow, uiText: UICopy = japaneseCopy): ResourceRow {
  if (resource.path === "/permissions") {
    const value = rowString(row, ["id", "name", "value"]);
    return {
      ...row,
      id: value,
      name: value,
      group: rowString(row, ["group"]) || permissionGroupForValue(value, uiText),
      description: rowString(row, ["description"]) || permissionDescription(value, uiText),
    };
  }
  if (resource.path.startsWith("/profiles/")) {
    return { ...row, profile_summary: profileSummary(resource.path, row, uiText) };
  }
  if (resource.path === "/discord/configs") {
    return { ...row, bot_summary: compactList([uiText("音声転送: 常時有効"), uiText("自動再接続: 常時有効"), rowString(row, ["reconnect_max_attempts", "config.reconnect_max_attempts"]) ? uiText("再接続 {0}回", rowString(row, ["reconnect_max_attempts", "config.reconnect_max_attempts"])) : ""]) };
  }
  if (resource.path === "/youtube/outputs") {
    return { ...row, output_summary: compactList([labelValue(uiText("方式"), rowString(row, ["mode", "config.mode"])), labelValue(uiText("公開"), rowString(row, ["privacy_status", "config.privacy_status"])), enabledLabel(uiText("自動開始"), rowValue(row, ["enable_auto_start", "config.enable_auto_start"]), uiText)]) };
  }
  if (resource.path === "/archive/destinations") {
    return { ...row, destination_summary: compactList([rowValue(row, ["shared_drive"]) === true ? uiText("共有ドライブ") : uiText("マイドライブ"), rowValue(row, ["folder_id_configured"]) === true ? uiText("Folder設定済み") : uiText("Folder未設定")]) };
  }
  if (resource.path === "/integrations/oauth-accounts") {
    return {
      ...row,
      oauth_account_display_name: oauthAccountName(row, uiText),
      account_usage: fixedPresentationText(oauthAccountPurposeLabel(row), uiText),
        account_summary: compactList([
          labelValue(uiText("プロバイダ"), formatScalarValue("provider_type", rowString(row, ["provider_type"]), undefined, uiText)),
          oauthAccountConfiguredName(row) ? uiText("表示名設定済み") : uiText("表示名未設定"),
          rowString(row, ["email"]) ? uiText("メール取得済み") : uiText("メール未取得"),
          rowValue(row, ["refresh_token_configured"]) === true ? uiText("OAuth tokenあり") : uiText("OAuth token未接続"),
        ]),
        oauth_refresh_status: {
          attempted_at: rowValue(row, ["access_token_refresh_attempted_at"]),
          failed_at: rowValue(row, ["access_token_refresh_failed_at"]),
          failure_code: rowValue(row, ["access_token_refresh_failure_code"]),
          relink_required: rowValue(row, ["access_token_refresh_relink_required"]),
        },
      };
  }
  if (resource.path === "/observability/diagnostics") {
    return {
      ...row,
      report: rowValue(row, ["diagnostic_report", "report"]),
    };
  }
  if (resource.path === "/secrets/status") {
    const summary = secretStatusSummary(row, uiText);
    return {
      ...row,
      secret_label: summary.label,
      secret_scope: summary.scope,
      secret_status: rowValue(row, ["configured"]) === true ? "configured" : "missing",
      secret_hint: summary.hint,
      secret_reference: rowString(row, ["name"]),
    };
  }
  if (resource.path === "/observability/notification-deliveries") {
    const presentation = notificationDeliveryPresentation(row);
    return {
      ...row,
      event_name: presentation.eventKey,
      ...(presentation.detail ? { event_detail: presentation.detail } : {}),
      ...(presentation.sentAt ? { sent_at: presentation.sentAt } : {}),
    };
  }
  return row;
}

function profileSummary(path: string, row: ResourceRow, uiText: UICopy = japaneseCopy) {
  if (path === "/profiles/encoder") {
    const width = rowString(row, ["width", "config.width"]);
    const height = rowString(row, ["height", "config.height"]);
    return compactList([width && height ? `${width}x${height}` : "", rowString(row, ["fps", "config.fps"]) ? `${rowString(row, ["fps", "config.fps"])}fps` : "", rowString(row, ["video_bitrate_kbps", "bitrate_kbps", "config.video_bitrate_kbps"]) ? `${rowString(row, ["video_bitrate_kbps", "bitrate_kbps", "config.video_bitrate_kbps"])}kbps` : "", rowString(row, ["audio_bitrate_kbps", "config.audio_bitrate_kbps"]) ? uiText("音声 {0}kbps", rowString(row, ["audio_bitrate_kbps", "config.audio_bitrate_kbps"])) : ""]);
  }
  if (path === "/profiles/caption") {
    return compactList([labelValue(uiText("言語"), rowString(row, ["language", "config.language"])), labelValue(uiText("方式"), rowString(row, ["provider", "config.provider"])), rowString(row, ["delay_ms", "config.delay_ms"]) ? uiText("遅延 {0}ms", rowString(row, ["delay_ms", "config.delay_ms"])) : ""]);
  }
  if (path === "/profiles/overlay") {
    return compactList([
      "1920x1080",
      uiText("自動フィット"),
      rowString(row, ["watermark_image_name", "config.watermark_image_name", "watermark_image_url", "config.watermark_image_url"]) ? uiText("画像あり") : "",
    ]);
  }
  if (path === "/profiles/archive") {
    return compactList([labelValue(uiText("形式"), rowString(row, ["format", "config.format"])), rowString(row, ["retention_days", "config.retention_days"]) ? uiText("{0}日保持", rowString(row, ["retention_days", "config.retention_days"])) : "", enabledLabel("Upload", rowValue(row, ["upload_enabled", "config.upload_enabled"]), uiText), rowString(row, ["drive_destination_id", "config.drive_destination_id"]) ? uiText("Drive保存先あり") : ""]);
  }
  return [];
}

export function visibleColumns(rows: Record<string, unknown>[], resource: ResourceDefinition) {
  const resourcePreferred = resourcePreferredColumns(resource);
  if (resourcePreferred.length > 0) {
    return resourcePreferred.filter((column) => rows.some((row) => row[column] !== undefined));
  }
  const preferred = ["name", "username", "service_name", "service_type", "type", "status", "health_status", "title", "action", "target", "updated_at", "created_at"];
  const seen = new Set<string>();
  for (const key of preferred) {
    if (isInternalReferenceColumn(key)) continue;
    if (rows.some((row) => row[key] !== undefined)) seen.add(key);
  }
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      if (seen.size >= 8) break;
      if (isInternalReferenceColumn(key)) continue;
      seen.add(key);
    }
    if (seen.size >= 8) break;
  }
  return [...seen];
}

function resourcePreferredColumns(resource: ResourceDefinition) {
  if (resource.path.startsWith("/profiles/")) return ["name", "profile_summary", "updated_at", "created_at"];
  if (resource.path === "/discord/configs") return ["name", "bot_summary", "updated_at"];
  if (resource.path === "/integrations/oauth-providers") return ["name", "provider_type", "enabled", "client_secret_configured", "updated_at"];
  if (resource.path === "/integrations/oauth-accounts") return ["oauth_account_display_name", "account_usage", "account_summary", "access_token_refreshed_at", "oauth_refresh_status", "refresh_token_updated_at"];
  if (resource.path === "/youtube/outputs") return ["name", "output_summary", "updated_at"];
  if (resource.path === "/archive/destinations") return ["name", "destination_summary", "updated_at"];
  if (resource.path === "/secrets/status") return ["secret_label", "secret_scope", "secret_status", "secret_hint", "updated_at"];
  if (resource.path === "/users") return ["username", "email", "status", "roles", "last_login_at"];
  if (resource.path === "/roles") return ["name", "permissions", "updated_at"];
  if (resource.path === "/permissions") return ["name", "group", "description"];
  if (resource.path === "/streams") return ["name", "status", "auto_start_trigger", "discord_config_id", "updated_at"];
  if (resource.path === "/stream-logs") return ["stream_name", "level", "message", "created_at", "stream_deleted_at"];
  if (resource.path === "/audit-logs") return ["timestamp", "actor_username", "action", "result", "resource_type"];
  if (resource.path === "/service-health") return ["service_name", "service_type", "status", "health_status", "last_heartbeat_at"];
  if (resource.path === "/observability/incidents") return ["title", "severity", "status", "updated_at"];
  if (resource.path === "/observability/diagnostics") return ["rule", "severity", "status", "service_id", "stream_id", "report", "updated_at"];
  if (resource.path === "/observability/remediation-actions") return ["action", "mode", "status", "result", "created_at", "updated_at"];
  if (resource.path === "/observability/notification-deliveries") return ["event_name", "event_detail", "channel", "status", "sent_at", "error"];
  if (resource.path === "/observability/notification-channels") return ["name", "type", "enabled", "severity_filter", "event_type_filter"];
  if (resource.path === "/observability/metrics") return ["name", "service_type", "status", "value", "updated_at"];
  return [];
}

function isInternalReferenceColumn(key: string) {
  const normalized = key.toLowerCase();
  if (normalized === "id") return true;
  if (normalized.endsWith("_id")) return true;
  if (normalized.endsWith("_ids")) return true;
  return false;
}

export function formatResourceCell(resource: ResourceDefinition, value: unknown, key = "", timezone?: string, uiText: UICopy = japaneseCopy): ReactNode {
  if (key === "status" && (resource.path === "/observability/incidents" || resource.path === "/observability/diagnostics" || resource.path === "/observability/remediation-actions") && typeof value === "string") {
    return observabilityStatusLabel(value, uiText);
  }
  if (key === "action" && resource.path === "/observability/remediation-actions" && typeof value === "string") {
    return observabilityActionLabel(value, uiText);
  }
  if (key === "mode" && resource.path === "/observability/remediation-actions" && typeof value === "string") {
    return observabilityModeLabel(value, uiText);
  }
  if (resource.path === "/integrations/oauth-accounts" && key === "refresh_token_updated_at" && (value === undefined || value === null || value === "")) {
    return <span className="text-muted-foreground">{uiText("未記録（既存連携では不明）")}</span>;
  }
  if (resource.path === "/integrations/oauth-accounts" && key === "access_token_refreshed_at" && (value === undefined || value === null || value === "")) {
    return <span className="text-muted-foreground">{uiText("未実行")}</span>;
  }
  if (resource.path === "/integrations/oauth-accounts" && key === "oauth_refresh_status" && isRecord(value)) {
    return <OAuthRefreshStatus value={value} timezone={timezone} />;
  }
  if (resource.path === "/observability/notification-channels" && key === "type" && typeof value === "string") {
    return notificationChannelTypeLabel(value);
  }
  if (resource.path === "/observability/notification-deliveries" && key === "event_detail" && typeof value === "string") {
    return <span className="whitespace-pre-line text-sm leading-relaxed">{value}</span>;
  }
  return formatCell(value, key, timezone, uiText);
}

function observabilityStatusLabel(value: string, uiText: UICopy = japaneseCopy) {
  const labels: Record<string, string> = {
    open: uiText("未対応"),
    acknowledged: uiText("確認済み"),
    resolved: uiText("解決済み"),
    closed: uiText("終了"),
    ignored: uiText("対象外"),
    suggested: uiText("提案"),
    pending_approval: uiText("承認待ち"),
    approved: uiText("承認済み"),
    executed: uiText("実行済み"),
    blocked: uiText("保留"),
    failed: uiText("失敗"),
    skipped: uiText("スキップ"),
  };
  return labels[value.trim().toLowerCase()] || value;
}

function observabilityActionLabel(value: string, uiText: UICopy = japaneseCopy) {
  const normalized = value.trim().toLowerCase().replace(/[\s-]+/g, "_");
  const labels: Record<string, string> = {
    restart_encoder: uiText("Encoder / Recorderを再起動"),
    restart_encoder_recorder: uiText("Encoder / Recorderを再起動"),
    restart_worker: uiText("Workerを再起動"),
    rerun_diagnostics: uiText("診断を再評価"),
    refresh_service_status: uiText("サービス状態を更新"),
    retry_package_remux: uiText("アーカイブ変換を再試行"),
    retry_gdrive_upload: uiText("Driveアップロードを再試行"),
    switch_worker: uiText("Workerを切り替え"),
    clear_stale_warning: uiText("古い警告を解除"),
  };
  return labels[normalized] || value;
}

function observabilityModeLabel(value: string, uiText: UICopy = japaneseCopy) {
  const labels: Record<string, string> = {
    disabled: uiText("無効"),
    suggest_only: uiText("提案のみ"),
    safe_auto: uiText("安全な自動実行"),
    manual_approval: uiText("手動承認"),
  };
  return labels[value.trim().toLowerCase()] || value;
}

function formatCell(value: unknown, key = "", timezone?: string, uiText: UICopy = japaneseCopy): ReactNode {
  if (value === null || value === undefined || value === "") return "-";
  if (typeof value === "boolean") return <Badge variant={value ? "default" : "secondary"}>{value ? uiText("有効") : uiText("無効")}</Badge>;
  if (typeof value === "string" || typeof value === "number") return formatScalarValue(key, value, timezone, uiText);
  if (Array.isArray(value)) {
    if (value.length === 0) return "-";
    return (
      <div className="flex flex-wrap gap-1">
        {value.slice(0, 6).map((item, index) => (
          <Badge key={index} variant="secondary" className="max-w-full text-xs">
            {formatNestedValue(key, item, uiText)}
          </Badge>
        ))}
        {value.length > 6 ? <Badge variant="outline">+{value.length - 6}</Badge> : null}
      </div>
    );
  }
  if (isRecord(value)) {
    const entries = Object.entries(value).filter(([, entryValue]) => entryValue !== "" && entryValue !== undefined && entryValue !== null);
    if (entries.length === 0) return "-";
    return (
      <div className="space-y-1 text-xs">
        {entries.slice(0, 5).map(([key, entryValue]) => (
          <div key={key} className="grid grid-cols-[minmax(72px,0.45fr)_minmax(0,1fr)] gap-2">
            <span className="text-muted-foreground">{columnLabel(key, uiText)}</span>
            <span className="min-w-0 truncate">{formatNestedValue(key, entryValue, uiText)}</span>
          </div>
        ))}
        {entries.length > 5 ? <div className="text-muted-foreground">{uiText("ほか")}{entries.length - 5} {uiText("件")}</div> : null}
      </div>
    );
  }
  return String(value);
}

function formatNestedValue(key: string, value: unknown, uiText: UICopy = japaneseCopy): string {
  if (isSensitiveKey(key)) return value ? uiText("設定済み") : "-";
  if (value === null || value === undefined || value === "") return "-";
  if (typeof value === "boolean") return value ? uiText("有効") : uiText("無効");
  if (typeof value === "string" || typeof value === "number") return formatScalarValue(key, value, undefined, uiText);
  if (Array.isArray(value)) return value.length === 0 ? "-" : value.map((item) => formatNestedValue("", item, uiText)).join(", ");
  if (isRecord(value)) return uiText("設定あり");
  return String(value);
}

function formatScalarValue(key: string, value: string | number, timezone?: string, uiText: UICopy = japaneseCopy) {
  const raw = String(value);
  if (key === "action") return fixedPresentationText(auditActionLabel(raw), uiText);
  if (key === "event_name") return valueLabels(uiText)[raw] || valueLabels(uiText)[raw.toLowerCase()] || fixedPresentationText(auditActionLabel(raw), uiText);
  if (key === "permissions") return permissionLabel(raw, uiText);
  if (key === "account_purpose") return fixedPresentationText(oauthAccountPurposeLabel({ account_purpose: raw }), uiText);
  const enumField = ["status", "health_status", "service_type", "provider_type", "type", "severity", "severity_filter", "event_type", "event_type_filter", "level", "result", "auto_start_trigger", "mode", "mfa_mode", "mfa_supported_methods", "position", "visibility", "secret_status"].includes(key);
  const status = enumField ? valueLabels(uiText)[raw] || valueLabels(uiText)[raw.toLowerCase()] : undefined;
  if (status) return status;
  if ((key.endsWith("_at") || key === "timestamp") && !Number.isNaN(Date.parse(raw))) return formatDateTimeInTimeZone(raw, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  return raw;
}

function labelValue(label: string, value: string) {
  return value ? `${label}: ${value}` : "";
}

export function oauthAccountOptionDescription(row: ResourceRow, uiText: UICopy = japaneseCopy) {
  return compactList([
    providerTypeLabel(rowString(row, ["provider_type"])),
    fixedPresentationText(oauthAccountPurposeLabel(row), uiText),
    rowValue(row, ["refresh_token_configured"]) === true ? uiText("接続済み") : uiText("未接続"),
  ]).join(" / ");
}

function OAuthRefreshStatus({ value, timezone }: { value: Record<string, unknown>; timezone?: string }) {
  const uiText = useUICopy();
  const attemptedAt = typeof value.attempted_at === "string" ? value.attempted_at : "";
  const failedAt = typeof value.failed_at === "string" ? value.failed_at : "";
  const failureCode = typeof value.failure_code === "string" ? value.failure_code : "";
  const relinkRequired = value.relink_required === true;
  return (
    <div className="space-y-1 text-sm leading-relaxed">
      <div><span className="text-muted-foreground">{uiText("最終試行:")}</span>{attemptedAt ? formatScalarValue("access_token_refresh_attempted_at", attemptedAt, timezone, uiText) : uiText("未実行")}</div>
      {failedAt ? <div><span className="text-muted-foreground">{uiText("最終失敗:")}</span>{formatScalarValue("access_token_refresh_failed_at", failedAt, timezone, uiText)}</div> : <div><span className="text-muted-foreground">{uiText("失敗状態:")}</span>{uiText("なし")}</div>}
      {failureCode ? <div><span className="text-muted-foreground">{uiText("失敗分類:")}</span>{oauthRefreshFailureLabel(failureCode, uiText)}</div> : null}
      <div><span className="text-muted-foreground">{uiText("再連携:")}</span>{relinkRequired ? <span className="font-medium text-destructive">{uiText("必要")}</span> : uiText("不要")}</div>
    </div>
  );
}

function oauthRefreshFailureLabel(value: string, uiText: UICopy = japaneseCopy) {
  const labels: Record<string, string> = {
    unknown: uiText("原因を安全に分類できませんでした"),
    credentials_unavailable: uiText("接続情報を利用できません"),
    provider_not_ready: uiText("プロバイダ設定を利用できません"),
    provider_unavailable: uiText("プロバイダに一時的に接続できません"),
    provider_credentials_invalid: uiText("プロバイダのクライアント設定が無効です"),
    reauthorization_required: uiText("認可のやり直しが必要です"),
    timeout: uiText("プロバイダ応答がタイムアウトしました"),
    invalid_response: uiText("プロバイダ応答を利用できません"),
  };
  return labels[value.trim().toLowerCase()] || uiText("原因を安全に分類できませんでした");
}

function enabledLabel(label: string, value: unknown, uiText: UICopy = japaneseCopy) {
  if (value === undefined || value === null || value === "") return "";
  return `${label}: ${value === true ? uiText("有効") : value === false ? uiText("無効") : String(value)}`;
}

export function compactList(values: string[]) {
  return values.map((value) => value.trim()).filter(Boolean);
}

export function isSensitiveKey(key: string) {
  return /(secret|token|password|credential|private|key)/i.test(key);
}

function humanizeKey(key: string, uiText: UICopy = japaneseCopy) {
  const known = columnLabels(uiText)[key];
  if (known) return known;
  return key
    .replace(/_/g, " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function valueLabels(uiText: UICopy = japaneseCopy): Record<string, string> { return {
  discord_bot: "Discord Bot",
  encoder_recorder: "Encoder/Recorder",
  observability: "Observability",
  worker: "Worker",
  google: "Google",
  github: "GitHub",
  discord: "Discord",
  online: uiText("オンライン"),
  offline: uiText("オフライン"),
  healthy: uiText("正常"),
  degraded: uiText("注意"),
  unhealthy: uiText("異常"),
  created: uiText("待機中"),
  scheduled: uiText("待機中"),
  ready: uiText("待機中"),
  draft: uiText("下書き"),
  starting: uiText("開始中"),
  live: uiText("配信中"),
  stopping: uiText("停止中"),
  stopped: uiText("停止"),
  completed: uiText("完了"),
  failed: uiText("失敗"),
  error: uiText("エラー"),
  public: uiText("公開"),
  unlisted: uiText("限定公開"),
  private: uiText("非公開"),
  discord_voice_join: uiText("VC参加で自動開始"),
  manual: uiText("手動開始"),
  stream_key: uiText("ストリームキー（直接送信／従来Relay）"),
  live_api: uiText("YouTube Live API（本番・通常）"),
  live_api_dry_run: uiText("YouTube Live API（検証）"),
  live_api_relay_static: uiText("YouTube Live API（固定Relay・既存互換）"),
  top_left: uiText("左上"),
  top_right: uiText("右上"),
  bottom_left: uiText("左下"),
  bottom_right: uiText("右下"),
  disabled: uiText("無効"),
  totp: "TOTP",
  passkey: "Passkey",
  critical: uiText("重大"),
  warning: uiText("警告"),
  info: uiText("情報"),
  acknowledged: uiText("確認済み"),
  resolved: uiText("解決済み"),
  retrying: uiText("再試行中"),
  success: uiText("成功"),
  open: uiText("未対応"),
  "incident.opened": uiText("インシデント発生"),
  "incident.updated": uiText("インシデント更新"),
  "incident.resolved": uiText("インシデント解決"),
  "diagnostic.created": uiText("診断作成"),
  "remediation.pending_approval": uiText("復旧承認待ち"),
  "remediation.executed": uiText("復旧実行"),
  "admin.audit": uiText("管理操作"),
  configured: uiText("登録済み"),
  missing: uiText("未登録"),
  "Password minimum length": uiText("最小パスワード長"),
  "MFA mode": uiText("MFAポリシー"),
  "Session idle timeout": uiText("アイドルタイムアウト"),
  "Session absolute lifetime": uiText("絶対セッション期限"),
  "Login lockout threshold": uiText("ロックまでの失敗回数"),
  "Remember me enabled": "Remember me",
  "Passkey status": uiText("Passkey状態"),
}; }

function columnLabels(uiText: UICopy = japaneseCopy): Record<string, string> { return {
  id: "ID",
  name: uiText("名前"),
  username: uiText("ユーザー名"),
  email: uiText("メール"),
  description: uiText("説明"),
  display_name: uiText("表示名"),
  service_id: "Node ID",
  service_name: uiText("Node名"),
  service_type: uiText("種別"),
  node_id: "Node ID",
  node_name: uiText("Node名"),
  account_label: uiText("表示名"),
  oauth_account_display_name: uiText("表示名"),
  account_purpose: uiText("利用可能な用途"),
  account_usage: uiText("利用可能な用途"),
  refresh_token_updated_at: uiText("Refresh Token登録/更新"),
  access_token_refreshed_at: uiText("Access Token最終自動更新"),
  oauth_refresh_status: uiText("自動更新の状態"),
  provider_type: uiText("プロバイダ"),
  type: uiText("種別"),
  status: uiText("状態"),
  health_status: uiText("ヘルス"),
  severity: uiText("重要度"),
  severity_filter: uiText("通知する重要度"),
  event_type_filter: uiText("通知するイベント"),
  title: uiText("タイトル"),
  check: uiText("チェック"),
  rule: uiText("検知ルール"),
  stream_id: uiText("配信枠ID"),
  stream_name: uiText("配信枠"),
  stream_deleted_at: uiText("配信枠削除日時"),
  level: uiText("レベル"),
  message: uiText("内容"),
  report: uiText("診断内容"),
  diagnostic_report: uiText("診断内容"),
  mode: uiText("復旧モード"),
  result: uiText("実行結果"),
  channel: uiText("通知先"),
  event_type: uiText("イベント"),
  event_name: uiText("イベント"),
  event_detail: uiText("内容"),
  sent_at: uiText("送信日時"),
  error: uiText("エラー"),
  timestamp: uiText("日時"),
  actor_username: uiText("実行者"),
  resource_type: uiText("対象"),
  auto_start_trigger: uiText("開始条件"),
  discord_voice_channel_id: "VC Channel ID",
  action: uiText("操作"),
  target: uiText("対象"),
  updated_at: uiText("更新日時"),
  created_at: uiText("作成日時"),
  last_heartbeat_at: uiText("最終Heartbeat"),
  last_login_at: uiText("最終ログイン"),
  permissions: uiText("権限"),
  roles: uiText("ロール"),
  profile_summary: uiText("設定内容"),
  bot_summary: uiText("BOT設定"),
  output_summary: uiText("出力設定"),
  destination_summary: uiText("保存先"),
  account_summary: uiText("接続設定"),
  secret_label: uiText("用途"),
  secret_scope: uiText("分類"),
  secret_status: uiText("状態"),
  secret_hint: uiText("確認先"),
  secret_reference: uiText("参照名"),
  enabled: uiText("有効"),
  configured: uiText("設定済み"),
  client_secret_configured: "Client Secret",
  password_min_length: uiText("最小パスワード長"),
  password_hash: uiText("パスワードハッシュ"),
  login_lockout_threshold: uiText("ロックまでの失敗回数"),
  session_idle_timeout_min: uiText("アイドル期限(分)"),
  session_absolute_lifetime_h: uiText("絶対期限(時間)"),
  remember_me_enabled: "Remember me",
  mfa_mode: uiText("MFAポリシー"),
  mfa_required_roles: uiText("MFA対象ロール"),
  mfa_supported_methods: uiText("対応MFA"),
  passkey_status: uiText("Passkey状態"),
  fingerprint: uiText("指紋"),
  group: uiText("分類"),
  value: uiText("値"),
}; }

export function columnLabel(column: string, uiText: UICopy = japaneseCopy) {
  return columnLabels(uiText)[column] || humanizeKey(column, uiText);
}

function secretStatusSummary(row: ResourceRow, uiText: UICopy = japaneseCopy): { label: string; scope: string; hint: string } {
  const name = rowString(row, ["name"]);
  const fixed: Record<string, { label: string; scope: string; hint: string }> = {
    app_smtp_password: { label: uiText("メールサーバー SMTPパスワード"), scope: uiText("システム通知"), hint: uiText("設定 > メールサーバー") },
    app_turnstile_secret: { label: "Cloudflare Turnstile Secret", scope: uiText("ログイン保護"), hint: uiText("設定 > Turnstile") },
    deepgram_api_key: { label: "Deepgram API Key", scope: uiText("字幕生成"), hint: uiText("字幕プロファイル") },
    discord_bot_token: { label: "Discord BOT Token", scope: "Discord", hint: uiText("Discord BOT設定") },
    google_drive_folder_id: { label: "Google Drive Folder ID", scope: uiText("録画アーカイブ"), hint: uiText("Drive保存先") },
    observability_token: { label: "Observability Token", scope: uiText("監視"), hint: uiText("Observability連携") },
    youtube_stream_key: { label: "YouTube Stream Key", scope: "YouTube", hint: uiText("YouTube出力") },
  };
  if (fixed[name]) return fixed[name];
  for (const item of dynamicSecretPrefixes(uiText)) {
    if (name.startsWith(item.prefix)) {
      return { label: item.label, scope: item.scope, hint: item.hint };
    }
  }
  return { label: name || uiText("未分類シークレット"), scope: uiText("その他"), hint: uiText("関連する設定画面") };
}

function dynamicSecretPrefixes(uiText: UICopy = japaneseCopy) { return [
  { prefix: "youtube_stream_key_", label: "YouTube Stream Key", scope: uiText("YouTube出力"), hint: uiText("YouTube出力") },
  { prefix: "discord_bot_token_", label: "Discord BOT Token", scope: uiText("Discord BOT設定"), hint: uiText("Discord BOT設定") },
  { prefix: "encoder_runtime_secret_", label: "Encoder Runtime Secret", scope: "Encoder/Recorder", hint: uiText("Node設定") },
  { prefix: "google_oauth_refresh_token_", label: "Google OAuth Refresh Token", scope: uiText("OAuth接続アカウント"), hint: uiText("連携 > OAuth接続アカウント") },
  { prefix: "google_drive_folder_id_", label: "Google Drive Folder ID", scope: uiText("Drive保存先"), hint: uiText("Drive保存先") },
  { prefix: "webhook_url_", label: "Webhook URL", scope: uiText("通知先"), hint: uiText("通知先") },
  { prefix: "smtp_password_", label: "SMTP Password", scope: uiText("通知先メール"), hint: uiText("通知先メール") },
]; }
