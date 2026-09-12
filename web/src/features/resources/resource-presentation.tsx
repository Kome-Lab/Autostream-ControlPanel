"use client";

import { type ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { auditActionLabel } from "@/lib/audit-action";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { notificationChannelTypeLabel, notificationDeliveryPresentation } from "@/lib/notification-channel";
import { oauthAccountConfiguredName, oauthAccountDisplayName, oauthAccountPurposeLabel, oauthProviderTypeLabel as providerTypeLabel } from "@/lib/oauth-account";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { type ResourceRow } from "./resource-form-types";
import { rowString, rowValue, isRecord } from "./resource-values";
import { permissionGroupForValue, permissionDescription, permissionLabel } from "./resource-permissions";

export function enrichResourceRow(resource: ResourceDefinition, row: ResourceRow): ResourceRow {
  if (resource.path === "/permissions") {
    const value = rowString(row, ["id", "name", "value"]);
    return {
      ...row,
      id: value,
      name: value,
      group: rowString(row, ["group"]) || permissionGroupForValue(value),
      description: rowString(row, ["description"]) || permissionDescription(value),
    };
  }
  if (resource.path.startsWith("/profiles/")) {
    return { ...row, profile_summary: profileSummary(resource.path, row) };
  }
  if (resource.path === "/discord/configs") {
    return { ...row, bot_summary: compactList(["音声転送: 常時有効", "自動再接続: 常時有効", rowString(row, ["reconnect_max_attempts", "config.reconnect_max_attempts"]) ? `再接続 ${rowString(row, ["reconnect_max_attempts", "config.reconnect_max_attempts"])}回` : ""]) };
  }
  if (resource.path === "/youtube/outputs") {
    return { ...row, output_summary: compactList([labelValue("方式", rowString(row, ["mode", "config.mode"])), labelValue("公開", rowString(row, ["privacy_status", "config.privacy_status"])), enabledLabel("自動開始", rowValue(row, ["enable_auto_start", "config.enable_auto_start"]))]) };
  }
  if (resource.path === "/archive/destinations") {
    return { ...row, destination_summary: compactList([rowValue(row, ["shared_drive"]) === true ? "共有ドライブ" : "マイドライブ", rowValue(row, ["folder_id_configured"]) === true ? "Folder設定済み" : "Folder未設定"]) };
  }
  if (resource.path === "/integrations/oauth-accounts") {
    return {
      ...row,
      oauth_account_display_name: oauthAccountDisplayName(row),
      account_usage: oauthAccountPurposeLabel(row),
        account_summary: compactList([
          labelValue("プロバイダ", formatScalarValue("provider_type", rowString(row, ["provider_type"]))),
          oauthAccountConfiguredName(row) ? "表示名設定済み" : "表示名未設定",
          rowString(row, ["email"]) ? "メール取得済み" : "メール未取得",
          rowValue(row, ["refresh_token_configured"]) === true ? "OAuth tokenあり" : "OAuth token未接続",
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
    const summary = secretStatusSummary(row);
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

function profileSummary(path: string, row: ResourceRow) {
  if (path === "/profiles/encoder") {
    const width = rowString(row, ["width", "config.width"]);
    const height = rowString(row, ["height", "config.height"]);
    return compactList([width && height ? `${width}x${height}` : "", rowString(row, ["fps", "config.fps"]) ? `${rowString(row, ["fps", "config.fps"])}fps` : "", rowString(row, ["video_bitrate_kbps", "bitrate_kbps", "config.video_bitrate_kbps"]) ? `${rowString(row, ["video_bitrate_kbps", "bitrate_kbps", "config.video_bitrate_kbps"])}kbps` : "", rowString(row, ["audio_bitrate_kbps", "config.audio_bitrate_kbps"]) ? `音声 ${rowString(row, ["audio_bitrate_kbps", "config.audio_bitrate_kbps"])}kbps` : ""]);
  }
  if (path === "/profiles/caption") {
    return compactList([labelValue("言語", rowString(row, ["language", "config.language"])), labelValue("方式", rowString(row, ["provider", "config.provider"])), rowString(row, ["delay_ms", "config.delay_ms"]) ? `遅延 ${rowString(row, ["delay_ms", "config.delay_ms"])}ms` : ""]);
  }
  if (path === "/profiles/overlay") {
    return compactList([
      "1920x1080",
      "自動フィット",
      rowString(row, ["watermark_image_name", "config.watermark_image_name", "watermark_image_url", "config.watermark_image_url"]) ? "画像あり" : "",
    ]);
  }
  if (path === "/profiles/archive") {
    return compactList([labelValue("形式", rowString(row, ["format", "config.format"])), rowString(row, ["retention_days", "config.retention_days"]) ? `${rowString(row, ["retention_days", "config.retention_days"])}日保持` : "", enabledLabel("Upload", rowValue(row, ["upload_enabled", "config.upload_enabled"])), rowString(row, ["drive_destination_id", "config.drive_destination_id"]) ? "Drive保存先あり" : ""]);
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

export function formatResourceCell(resource: ResourceDefinition, value: unknown, key = "", timezone?: string): ReactNode {
  if (key === "status" && (resource.path === "/observability/incidents" || resource.path === "/observability/diagnostics" || resource.path === "/observability/remediation-actions") && typeof value === "string") {
    return observabilityStatusLabel(value);
  }
  if (key === "action" && resource.path === "/observability/remediation-actions" && typeof value === "string") {
    return observabilityActionLabel(value);
  }
  if (key === "mode" && resource.path === "/observability/remediation-actions" && typeof value === "string") {
    return observabilityModeLabel(value);
  }
  if (resource.path === "/integrations/oauth-accounts" && key === "refresh_token_updated_at" && (value === undefined || value === null || value === "")) {
    return <span className="text-muted-foreground">未記録（既存連携では不明）</span>;
  }
  if (resource.path === "/integrations/oauth-accounts" && key === "access_token_refreshed_at" && (value === undefined || value === null || value === "")) {
    return <span className="text-muted-foreground">未実行</span>;
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
  return formatCell(value, key, timezone);
}

function observabilityStatusLabel(value: string) {
  const labels: Record<string, string> = {
    open: "未対応",
    acknowledged: "確認済み",
    resolved: "解決済み",
    closed: "終了",
    ignored: "対象外",
    suggested: "提案",
    pending_approval: "承認待ち",
    approved: "承認済み",
    executed: "実行済み",
    blocked: "保留",
    failed: "失敗",
    skipped: "スキップ",
  };
  return labels[value.trim().toLowerCase()] || value;
}

function observabilityActionLabel(value: string) {
  const normalized = value.trim().toLowerCase().replace(/[\s-]+/g, "_");
  const labels: Record<string, string> = {
    restart_encoder: "Encoder / Recorderを再起動",
    restart_encoder_recorder: "Encoder / Recorderを再起動",
    restart_worker: "Workerを再起動",
    rerun_diagnostics: "診断を再評価",
    refresh_service_status: "サービス状態を更新",
    retry_package_remux: "アーカイブ変換を再試行",
    retry_gdrive_upload: "Driveアップロードを再試行",
    switch_worker: "Workerを切り替え",
    clear_stale_warning: "古い警告を解除",
  };
  return labels[normalized] || value;
}

function observabilityModeLabel(value: string) {
  const labels: Record<string, string> = {
    disabled: "無効",
    suggest_only: "提案のみ",
    safe_auto: "安全な自動実行",
    manual_approval: "手動承認",
  };
  return labels[value.trim().toLowerCase()] || value;
}

function formatCell(value: unknown, key = "", timezone?: string): ReactNode {
  if (value === null || value === undefined || value === "") return "-";
  if (typeof value === "boolean") return <Badge variant={value ? "default" : "secondary"}>{value ? "有効" : "無効"}</Badge>;
  if (typeof value === "string" || typeof value === "number") return formatScalarValue(key, value, timezone);
  if (Array.isArray(value)) {
    if (value.length === 0) return "-";
    return (
      <div className="flex flex-wrap gap-1">
        {value.slice(0, 6).map((item, index) => (
          <Badge key={index} variant="secondary" className="max-w-full text-xs">
            {formatNestedValue(key, item)}
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
            <span className="text-muted-foreground">{columnLabel(key)}</span>
            <span className="min-w-0 truncate">{formatNestedValue(key, entryValue)}</span>
          </div>
        ))}
        {entries.length > 5 ? <div className="text-muted-foreground">ほか {entries.length - 5} 件</div> : null}
      </div>
    );
  }
  return String(value);
}

function formatNestedValue(key: string, value: unknown): string {
  if (isSensitiveKey(key)) return value ? "設定済み" : "-";
  if (value === null || value === undefined || value === "") return "-";
  if (typeof value === "boolean") return value ? "有効" : "無効";
  if (typeof value === "string" || typeof value === "number") return formatScalarValue(key, value);
  if (Array.isArray(value)) return value.length === 0 ? "-" : value.map((item) => formatNestedValue("", item)).join(", ");
  if (isRecord(value)) return "設定あり";
  return String(value);
}

function formatScalarValue(key: string, value: string | number, timezone?: string) {
  const raw = String(value);
  if (key === "action") return auditActionLabel(raw);
  if (key === "event_name") return valueLabels[raw] || valueLabels[raw.toLowerCase()] || auditActionLabel(raw);
  if (key === "permissions") return permissionLabel(raw);
  if (key === "account_purpose") return oauthAccountPurposeLabel({ account_purpose: raw });
  if ((key === "id" || key === "name") && columnLabels[raw]) return columnLabels[raw];
  const status = valueLabels[raw] || valueLabels[raw.toLowerCase()];
  if (status) return status;
  if ((key.endsWith("_at") || key === "timestamp") && !Number.isNaN(Date.parse(raw))) return formatDateTimeInTimeZone(raw, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  return raw;
}

function labelValue(label: string, value: string) {
  return value ? `${label}: ${value}` : "";
}

export function oauthAccountOptionDescription(row: ResourceRow) {
  return compactList([
    providerTypeLabel(rowString(row, ["provider_type"])),
    oauthAccountPurposeLabel(row),
    rowValue(row, ["refresh_token_configured"]) === true ? "接続済み" : "未接続",
  ]).join(" / ");
}

function OAuthRefreshStatus({ value, timezone }: { value: Record<string, unknown>; timezone?: string }) {
  const attemptedAt = typeof value.attempted_at === "string" ? value.attempted_at : "";
  const failedAt = typeof value.failed_at === "string" ? value.failed_at : "";
  const failureCode = typeof value.failure_code === "string" ? value.failure_code : "";
  const relinkRequired = value.relink_required === true;
  return (
    <div className="space-y-1 text-sm leading-relaxed">
      <div><span className="text-muted-foreground">最終試行: </span>{attemptedAt ? formatScalarValue("access_token_refresh_attempted_at", attemptedAt, timezone) : "未実行"}</div>
      {failedAt ? <div><span className="text-muted-foreground">最終失敗: </span>{formatScalarValue("access_token_refresh_failed_at", failedAt, timezone)}</div> : <div><span className="text-muted-foreground">失敗状態: </span>なし</div>}
      {failureCode ? <div><span className="text-muted-foreground">失敗分類: </span>{oauthRefreshFailureLabel(failureCode)}</div> : null}
      <div><span className="text-muted-foreground">再連携: </span>{relinkRequired ? <span className="font-medium text-destructive">必要</span> : "不要"}</div>
    </div>
  );
}

function oauthRefreshFailureLabel(value: string) {
  const labels: Record<string, string> = {
    unknown: "原因を安全に分類できませんでした",
    credentials_unavailable: "接続情報を利用できません",
    provider_not_ready: "プロバイダ設定を利用できません",
    provider_unavailable: "プロバイダに一時的に接続できません",
    provider_credentials_invalid: "プロバイダのクライアント設定が無効です",
    reauthorization_required: "認可のやり直しが必要です",
    timeout: "プロバイダ応答がタイムアウトしました",
    invalid_response: "プロバイダ応答を利用できません",
  };
  return labels[value.trim().toLowerCase()] || "原因を安全に分類できませんでした";
}

function enabledLabel(label: string, value: unknown) {
  if (value === undefined || value === null || value === "") return "";
  return `${label}: ${value === true ? "有効" : value === false ? "無効" : String(value)}`;
}

export function compactList(values: string[]) {
  return values.map((value) => value.trim()).filter(Boolean);
}

export function isSensitiveKey(key: string) {
  return /(secret|token|password|credential|private|key)/i.test(key);
}

function humanizeKey(key: string) {
  const known = columnLabels[key];
  if (known) return known;
  return key
    .replace(/_/g, " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

const valueLabels: Record<string, string> = {
  discord_bot: "Discord Bot",
  encoder_recorder: "Encoder/Recorder",
  observability: "Observability",
  worker: "Worker",
  google: "Google",
  github: "GitHub",
  discord: "Discord",
  online: "オンライン",
  offline: "オフライン",
  healthy: "正常",
  degraded: "注意",
  unhealthy: "異常",
  created: "待機中",
  scheduled: "待機中",
  ready: "待機中",
  draft: "下書き",
  starting: "開始中",
  live: "配信中",
  stopping: "停止中",
  stopped: "停止",
  completed: "完了",
  failed: "失敗",
  error: "エラー",
  public: "公開",
  unlisted: "限定公開",
  private: "非公開",
  discord_voice_join: "VC参加で自動開始",
  manual: "手動開始",
  stream_key: "ストリームキー（直接送信／従来Relay）",
  live_api: "YouTube Live API（本番・通常）",
  live_api_dry_run: "YouTube Live API（検証）",
  live_api_relay_static: "YouTube Live API（固定Relay・既存互換）",
  top_left: "左上",
  top_right: "右上",
  bottom_left: "左下",
  bottom_right: "右下",
  disabled: "無効",
  totp: "TOTP",
  passkey: "Passkey",
  critical: "重大",
  warning: "警告",
  info: "情報",
  acknowledged: "確認済み",
  resolved: "解決済み",
  retrying: "再試行中",
  success: "成功",
  open: "未対応",
  "incident.opened": "インシデント発生",
  "incident.updated": "インシデント更新",
  "incident.resolved": "インシデント解決",
  "diagnostic.created": "診断作成",
  "remediation.pending_approval": "復旧承認待ち",
  "remediation.executed": "復旧実行",
  "admin.audit": "管理操作",
  configured: "登録済み",
  missing: "未登録",
  "Password minimum length": "最小パスワード長",
  "MFA mode": "MFAポリシー",
  "Session idle timeout": "アイドルタイムアウト",
  "Session absolute lifetime": "絶対セッション期限",
  "Login lockout threshold": "ロックまでの失敗回数",
  "Remember me enabled": "Remember me",
  "Passkey status": "Passkey状態",
};

const columnLabels: Record<string, string> = {
  id: "ID",
  name: "名前",
  username: "ユーザー名",
  email: "メール",
  description: "説明",
  display_name: "表示名",
  service_id: "Node ID",
  service_name: "Node名",
  service_type: "種別",
  node_id: "Node ID",
  node_name: "Node名",
  account_label: "表示名",
  oauth_account_display_name: "表示名",
  account_purpose: "利用可能な用途",
  account_usage: "利用可能な用途",
  refresh_token_updated_at: "Refresh Token登録/更新",
  access_token_refreshed_at: "Access Token最終自動更新",
  oauth_refresh_status: "自動更新の状態",
  provider_type: "プロバイダ",
  type: "種別",
  status: "状態",
  health_status: "ヘルス",
  severity: "重要度",
  severity_filter: "通知する重要度",
  event_type_filter: "通知するイベント",
  title: "タイトル",
  check: "チェック",
  rule: "検知ルール",
  stream_id: "配信枠ID",
  stream_name: "配信枠",
  stream_deleted_at: "配信枠削除日時",
  level: "レベル",
  message: "内容",
  report: "診断内容",
  diagnostic_report: "診断内容",
  mode: "復旧モード",
  result: "実行結果",
  channel: "通知先",
  event_type: "イベント",
  event_name: "イベント",
  event_detail: "内容",
  sent_at: "送信日時",
  error: "エラー",
  timestamp: "日時",
  actor_username: "実行者",
  resource_type: "対象",
  auto_start_trigger: "開始条件",
  discord_voice_channel_id: "VC Channel ID",
  action: "操作",
  target: "対象",
  updated_at: "更新日時",
  created_at: "作成日時",
  last_heartbeat_at: "最終Heartbeat",
  last_login_at: "最終ログイン",
  permissions: "権限",
  roles: "ロール",
  profile_summary: "設定内容",
  bot_summary: "BOT設定",
  output_summary: "出力設定",
  destination_summary: "保存先",
  account_summary: "接続設定",
  secret_label: "用途",
  secret_scope: "分類",
  secret_status: "状態",
  secret_hint: "確認先",
  secret_reference: "参照名",
  enabled: "有効",
  configured: "設定済み",
  client_secret_configured: "Client Secret",
  password_min_length: "最小パスワード長",
  password_hash: "パスワードハッシュ",
  login_lockout_threshold: "ロックまでの失敗回数",
  session_idle_timeout_min: "アイドル期限(分)",
  session_absolute_lifetime_h: "絶対期限(時間)",
  remember_me_enabled: "Remember me",
  mfa_mode: "MFAポリシー",
  mfa_required_roles: "MFA対象ロール",
  mfa_supported_methods: "対応MFA",
  passkey_status: "Passkey状態",
  fingerprint: "指紋",
  group: "分類",
  value: "値",
};

export function columnLabel(column: string) {
  return columnLabels[column] || humanizeKey(column);
}

function secretStatusSummary(row: ResourceRow): { label: string; scope: string; hint: string } {
  const name = rowString(row, ["name"]);
  const fixed: Record<string, { label: string; scope: string; hint: string }> = {
    app_smtp_password: { label: "メールサーバー SMTPパスワード", scope: "システム通知", hint: "設定 > メールサーバー" },
    app_turnstile_secret: { label: "Cloudflare Turnstile Secret", scope: "ログイン保護", hint: "設定 > Turnstile" },
    deepgram_api_key: { label: "Deepgram API Key", scope: "字幕生成", hint: "字幕プロファイル" },
    discord_bot_token: { label: "Discord BOT Token", scope: "Discord", hint: "Discord BOT設定" },
    google_drive_folder_id: { label: "Google Drive Folder ID", scope: "録画アーカイブ", hint: "Drive保存先" },
    observability_token: { label: "Observability Token", scope: "監視", hint: "Observability連携" },
    youtube_stream_key: { label: "YouTube Stream Key", scope: "YouTube", hint: "YouTube出力" },
  };
  if (fixed[name]) return fixed[name];
  for (const item of dynamicSecretPrefixes) {
    if (name.startsWith(item.prefix)) {
      return { label: item.label, scope: item.scope, hint: item.hint };
    }
  }
  return { label: name || "未分類シークレット", scope: "その他", hint: "関連する設定画面" };
}

const dynamicSecretPrefixes = [
  { prefix: "youtube_stream_key_", label: "YouTube Stream Key", scope: "YouTube出力", hint: "YouTube出力" },
  { prefix: "discord_bot_token_", label: "Discord BOT Token", scope: "Discord BOT設定", hint: "Discord BOT設定" },
  { prefix: "encoder_runtime_secret_", label: "Encoder Runtime Secret", scope: "Encoder/Recorder", hint: "Node設定" },
  { prefix: "google_oauth_refresh_token_", label: "Google OAuth Refresh Token", scope: "OAuth接続アカウント", hint: "連携 > OAuth接続アカウント" },
  { prefix: "google_drive_folder_id_", label: "Google Drive Folder ID", scope: "Drive保存先", hint: "Drive保存先" },
  { prefix: "webhook_url_", label: "Webhook URL", scope: "通知先", hint: "通知先" },
  { prefix: "smtp_password_", label: "SMTP Password", scope: "通知先メール", hint: "通知先メール" },
];
