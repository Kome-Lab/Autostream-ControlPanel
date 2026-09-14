"use client";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { type ResourceDefinition } from "@/features/resources/resource-config";
import { hasPermission } from "@/lib/auth/permissions";
import { type ResourceAccess, type ResourceRow, type SelectOption } from "./resource-form-types";
import { rowString } from "./resource-values";

export function resourceAccess(resource: ResourceDefinition, currentUser: Parameters<typeof hasPermission>[0]): ResourceAccess {
  const allowed = (permission?: string) => permission ? hasPermission(currentUser, permission) : false;
  return {
    read: allowed(resource.permissions?.read),
    create: allowed(resource.permissions?.create),
    update: allowed(resource.permissions?.update),
    delete: allowed(resource.permissions?.delete),
    test: allowed(resource.permissions?.test),
  };
}

export function requiredPermissionText(permission?: string, uiText: UICopy = japaneseCopy) {
  return permission ? uiText("この操作には「{0}」権限が必要です。管理者に権限付与を依頼してください。", permissionLabel(permission, uiText)) : uiText("この操作を実行する権限がありません。管理者に権限付与を依頼してください。");
}

export function permissionOptionFromRow(row: ResourceRow, uiText: UICopy = japaneseCopy): SelectOption {
	const value = rowString(row, ["id", "name", "value"]);
	return {
		value,
		label: permissionLabel(value, uiText),
		description: rowString(row, ["description"]) || permissionDescription(value, uiText),
		group: permissionGroupForValue(value, uiText),
	};
}

function permissionGroupLabels(uiText: UICopy = japaneseCopy): Record<string, string> { return {
	all: uiText("管理者"),
	users: uiText("ユーザー管理"),
	roles: uiText("ロール管理"),
	streams: uiText("配信運用"),
	encoder_profiles: uiText("エンコーダー設定"),
	archive_profiles: uiText("録画/アーカイブ設定"),
	caption_profiles: uiText("字幕/STT設定"),
	overlay_profiles: uiText("ウォーターマーク設定"),
	discord_configs: uiText("Discord設定"),
	youtube_outputs: uiText("YouTube出力"),
	services: uiText("サービス割り当て"),
	workers: uiText("Worker管理"),
	archives: uiText("録画ファイル"),
	logs: uiText("ログ"),
	audit_logs: uiText("監査ログ"),
	secrets: uiText("シークレット"),
	api_tokens: "Node/API token",
	system_settings: uiText("システム設定"),
	system_updates: uiText("システム更新"),
	incidents: uiText("インシデント"),
	diagnostics: uiText("診断"),
	remediation: uiText("復旧操作"),
	notification_channels: uiText("通知設定"),
	integrations: uiText("外部連携"),
	metrics: uiText("メトリクス"),
	service_health: uiText("Nodeヘルス"),
	other: uiText("その他"),
}; }

export function permissionGroupForValue(value: string, uiText: UICopy = japaneseCopy) {
	if (value === "*") return permissionGroupLabels(uiText).all;
	const group = value.split(".")[0] || "other";
	return permissionGroupLabel(group, uiText);
}

export function permissionGroupLabel(group: string, uiText: UICopy = japaneseCopy) {
	return permissionGroupLabels(uiText)[group] || humanizePermissionText(group);
}

export function permissionLabel(value: string, uiText: UICopy = japaneseCopy) {
	if (value === "*") return uiText("すべての操作を許可");
	if (value === "system_updates.execute") return uiText("システム更新を実行");
	const dot = value.lastIndexOf(".");
	if (dot < 0) return humanizePermissionText(value);
	const groupKey = value.slice(0, dot);
	const action = value.slice(dot + 1);
	const subject = permissionGroupLabel(groupKey, uiText);
	switch (action) {
		case "read":
			return uiText("{0}を見る", subject);
		case "create":
			return uiText("{0}を作成", subject);
		case "update":
			return uiText("{0}を編集", subject);
		case "delete":
			return uiText("{0}を削除", subject);
		case "disable":
			return uiText("{0}を無効化", subject);
		case "assign":
			return uiText("{0}を割り当て", subject);
		case "unassign":
			return uiText("{0}の割り当て解除", subject);
		case "restart":
			return uiText("{0}を再起動", subject);
		case "start":
			return uiText("{0}を開始", subject);
		case "stop":
			return uiText("{0}を停止", subject);
		case "retry_upload":
			return uiText("録画アップロードを再試行");
		case "download":
			return uiText("{0}をダウンロード", subject);
		case "export":
			return uiText("{0}を書き出し", subject);
		case "revoke":
			return uiText("{0}を失効", subject);
		case "read_status":
			return uiText("{0}の状態を見る", subject);
		case "reset_password":
			return uiText("ユーザーのパスワードを再設定");
		case "force_password_change":
			return uiText("ユーザーにパスワード変更を要求");
		case "manage_mfa":
			return uiText("ユーザーのMFAを管理");
		case "acknowledge":
			return uiText("インシデントを確認済みにする");
		case "resolve":
			return uiText("インシデントを解決済みにする");
		case "run":
			return uiText("診断を実行");
		case "approve":
			return uiText("復旧操作を承認");
		case "execute":
			return uiText("復旧操作を実行");
		case "test":
			return uiText("通知テストを送信");
		default:
			return `${subject}: ${humanizePermissionText(action)}`;
	}
}

export function permissionDescription(value: string, uiText: UICopy = japaneseCopy) {
	if (value === "*") return uiText("全画面と全操作を許可します。管理者ロールだけに付与します。");
	const group = permissionGroupForValue(value, uiText);
	return uiText("{0}に関する操作権限です。", group);
}

function humanizePermissionText(value: string) {
	return value
		.split(/[_\-.]+/)
		.filter(Boolean)
		.map((part) => part.charAt(0).toUpperCase() + part.slice(1))
		.join(" ");
}
