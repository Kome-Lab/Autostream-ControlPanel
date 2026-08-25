import { hasAnyPermission } from "@/lib/auth/permissions";
import type { TranslationKey } from "@/lib/i18n";
import type { CurrentUser, Locale } from "@/types/domain";

export type NavigationIconName =
  | "activity"
  | "alert-triangle"
  | "archive"
  | "bar-chart"
  | "bell"
  | "captions"
  | "clipboard-list"
  | "file-text"
  | "gauge"
  | "hard-drive"
  | "info"
  | "key-round"
  | "layers"
  | "line-chart"
  | "message-circle"
  | "network"
  | "play-square"
  | "plug"
  | "server-cog"
  | "settings"
  | "shield"
  | "users"
  | "video"
  | "wrench";

export type NavigationItem = {
  href: string;
  key: TranslationKey;
  icon: NavigationIconName;
  permissions: string[];
  description: Record<Locale, string>;
};

export type NavigationSection = {
  key: TranslationKey;
  items: NavigationItem[];
};

const activityIcon = "activity";
const alertTriangleIcon = "alert-triangle";
const archiveIcon = "archive";
const barChartIcon = "bar-chart";
const bellIcon = "bell";
const captionsIcon = "captions";
const clipboardListIcon = "clipboard-list";
const fileTextIcon = "file-text";
const gaugeIcon = "gauge";
const hardDriveIcon = "hard-drive";
const infoIcon = "info";
const keyRoundIcon = "key-round";
const layersIcon = "layers";
const lineChartIcon = "line-chart";
const messageCircleIcon = "message-circle";
const networkIcon = "network";
const playSquareIcon = "play-square";
const plugIcon = "plug";
const serverCogIcon = "server-cog";
const settingsIcon = "settings";
const shieldIcon = "shield";
const usersIcon = "users";
const videoIcon = "video";
const wrenchIcon = "wrench";

function navItem(href: string, key: TranslationKey, icon: NavigationIconName, permissions: string[], ja: string, en: string): NavigationItem {
  return { href, key, icon, permissions, description: { ja, en } };
}

export const navigationSections: NavigationSection[] = [
  {
    key: "navOperations",
    items: [
      navItem("/admin/", "dashboard", barChartIcon, [], "待機枠、配信中、要対応、基盤状態をまとめて確認", "Review waiting slots, live streams, action items, and platform health"),
      navItem("/admin/streams/", "streams", playSquareIcon, ["streams.read"], "Discord VC連動の待機枠、開始、停止、録画設定", "Manage Discord VC-triggered slots, start, stop, and recording"),
      navItem("/admin/service-health/", "serviceHealth", activityIcon, ["service_health.read"], "Nodeとサービスの接続状態を確認", "Review node and service availability"),
      navItem("/admin/incidents/", "incidents", alertTriangleIcon, ["incidents.read"], "障害の検知、確認、解決を追跡", "Track detection, acknowledgement, and resolution"),
      navItem("/admin/archive/", "archive", archiveIcon, ["archives.read", "archive_profiles.read", "integrations.read"], "録画成果物の確認、保存、ダウンロード", "Manage recordings, retention, and downloads"),
      navItem("/admin/logs/", "logs", fileTextIcon, ["logs.read"], "配信枠ごとの記録を確認", "Inspect records for each stream slot"),
    ],
  },
  {
    key: "navProfiles",
    items: [
      navItem("/admin/workers/", "workers", serverCogIcon, ["workers.read", "service_health.read", "api_tokens.create"], "Worker・EncoderとNodeサービスの状態・操作", "Operate Workers, Encoders, and Node services"),
      navItem("/admin/encoder/", "encoder", gaugeIcon, ["encoder_profiles.read"], "配信品質の標準設定", "Standardize encoding quality"),
      navItem("/admin/discord/", "discord", messageCircleIcon, ["discord_configs.read"], "配信起動に使うDiscord BOT", "Configure Discord bots used for stream automation"),
      navItem("/admin/youtube/", "youtube", videoIcon, ["youtube_outputs.read"], "YouTube出力と公開設定", "Configure YouTube outputs and visibility"),
      navItem("/admin/caption/", "caption", captionsIcon, ["caption_profiles.read"], "字幕生成の標準設定", "Standardize caption generation"),
      navItem("/admin/overlay/", "overlay", layersIcon, ["overlay_profiles.read"], "案件ごとのウォーターマーク", "Manage stream watermarks"),
    ],
  },
  {
    key: "navMonitoring",
    items: [
      navItem("/admin/monitoring/", "monitoring", lineChartIcon, ["incidents.read", "service_health.read"], "運用状況と障害を横断監視", "Monitor operations and incidents"),
      navItem("/admin/diagnostics/", "diagnostics", wrenchIcon, ["diagnostics.read"], "配信経路とサービスの診断結果", "Review stream-path and service diagnostics"),
      navItem("/admin/remediation/", "remediation", hardDriveIcon, ["remediation.read"], "承認制の復旧操作", "Review and approve recovery actions"),
      navItem("/admin/notifications/", "notifications", bellIcon, ["notification_channels.read"], "通知履歴と連絡先", "Manage delivery history and destinations"),
      navItem("/admin/metrics/", "metrics", barChartIcon, ["metrics.read"], "Nodeと配信基盤の時系列指標", "Inspect time-series platform metrics"),
      navItem("/admin/audit-logs/", "auditLogs", clipboardListIcon, ["audit_logs.read"], "誰が何をしたかを確認", "Review who changed what and when"),
    ],
  },
  {
    key: "navAdministration",
    items: [
      navItem("/admin/users/", "users", usersIcon, ["users.read"], "担当者アカウントと利用状態", "Manage operator accounts and access state"),
      navItem("/admin/roles/", "roles", shieldIcon, ["roles.read"], "役割ごとの操作権限", "Manage role-based permissions"),
      navItem("/admin/integrations/", "integrations", plugIcon, ["integrations.read"], "OAuthと外部サービス接続", "Manage OAuth and external connections"),
      navItem("/admin/security/", "security", keyRoundIcon, ["secrets.read_status", "system_settings.read"], "ログイン・MFA・シークレット設定", "Manage login, MFA, and secret settings"),
      navItem("/admin/nodes/", "nodeRegistration", networkIcon, ["api_tokens.create"], "新しいNodeと登録トークンを発行", "Issue nodes and registration tokens"),
      navItem("/admin/registered-nodes/", "registeredNodes", serverCogIcon, ["api_tokens.create"], "登録済みNodeを編集・削除", "Edit and remove registered nodes"),
      navItem("/admin/application/", "applicationInfo", infoIcon, ["system_settings.read", "system_updates.read"], "各サービスのバージョンと更新状況を確認", "Review service versions and updates"),
      navItem("/admin/settings/", "settings", settingsIcon, ["system_settings.read"], "表示、時刻、メールサーバー設定", "Manage display, time, and mail settings"),
    ],
  },
];

export const navigationSectionKeys = navigationSections.map((section) => section.key);
const navigationItems = navigationSections.flatMap((section) => section.items);

export function visibleNavigationSections(currentUser: CurrentUser) {
  return navigationSections
    .map((section) => ({ ...section, items: section.items.filter((item) => canSeeNavItem(item, currentUser)) }))
    .filter((section) => section.items.length > 0);
}

export function canSeeNavItem(item: NavigationItem, currentUser: CurrentUser) {
  if (item.permissions.length === 0) return true;
  return isSuperAdmin(currentUser) || hasAnyPermission(currentUser, item.permissions);
}

export function isSuperAdmin(currentUser?: CurrentUser) {
  return currentUser?.user.roles?.includes("super_admin") === true;
}

export function activeNavigationItem(pathname: string) {
  return navigationItems.find((item) => isActivePath(pathname, item.href));
}

export function activeNavigationSectionKey(pathname: string) {
  return navigationSections.find((section) => section.items.some((item) => isActivePath(pathname, item.href)))?.key || null;
}

export function isActivePath(pathname: string, href: string) {
  return pathname === href || (href !== "/admin/" && pathname.startsWith(href));
}
