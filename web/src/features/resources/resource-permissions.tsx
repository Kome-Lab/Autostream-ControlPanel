"use client";

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

export function requiredPermissionText(permission?: string) {
  return permission ? `この操作には「${permissionLabel(permission)}」権限が必要です。管理者に権限付与を依頼してください。` : "この操作を実行する権限がありません。管理者に権限付与を依頼してください。";
}

export function permissionOptionFromRow(row: ResourceRow): SelectOption {
	const value = rowString(row, ["id", "name", "value"]);
	return {
		value,
		label: permissionLabel(value),
		description: rowString(row, ["description"]) || permissionDescription(value),
		group: permissionGroupForValue(value),
	};
}

const permissionGroupLabels: Record<string, string> = {
	all: "管理者",
	users: "ユーザー管理",
	roles: "ロール管理",
	streams: "配信運用",
	encoder_profiles: "エンコーダー設定",
	archive_profiles: "録画/アーカイブ設定",
	caption_profiles: "字幕/STT設定",
	overlay_profiles: "ウォーターマーク設定",
	discord_configs: "Discord設定",
	youtube_outputs: "YouTube出力",
	services: "サービス割り当て",
	workers: "Worker管理",
	archives: "録画ファイル",
	logs: "ログ",
	audit_logs: "監査ログ",
	secrets: "シークレット",
	api_tokens: "Node/API token",
	system_settings: "システム設定",
	system_updates: "システム更新",
	incidents: "インシデント",
	diagnostics: "診断",
	remediation: "復旧操作",
	notification_channels: "通知設定",
	integrations: "外部連携",
	metrics: "メトリクス",
	service_health: "Nodeヘルス",
	other: "その他",
};

export function permissionGroupForValue(value: string) {
	if (value === "*") return permissionGroupLabels.all;
	const group = value.split(".")[0] || "other";
	return permissionGroupLabel(group);
}

export function permissionGroupLabel(group: string) {
	return permissionGroupLabels[group] || humanizePermissionText(group);
}

export function permissionLabel(value: string) {
	if (value === "*") return "すべての操作を許可";
	if (value === "system_updates.execute") return "システム更新を実行";
	const dot = value.lastIndexOf(".");
	if (dot < 0) return humanizePermissionText(value);
	const groupKey = value.slice(0, dot);
	const action = value.slice(dot + 1);
	const subject = permissionGroupLabel(groupKey);
	switch (action) {
		case "read":
			return `${subject}を見る`;
		case "create":
			return `${subject}を作成`;
		case "update":
			return `${subject}を編集`;
		case "delete":
			return `${subject}を削除`;
		case "disable":
			return `${subject}を無効化`;
		case "assign":
			return `${subject}を割り当て`;
		case "unassign":
			return `${subject}の割り当て解除`;
		case "restart":
			return `${subject}を再起動`;
		case "start":
			return `${subject}を開始`;
		case "stop":
			return `${subject}を停止`;
		case "retry_upload":
			return "録画アップロードを再試行";
		case "download":
			return `${subject}をダウンロード`;
		case "export":
			return `${subject}を書き出し`;
		case "revoke":
			return `${subject}を失効`;
		case "read_status":
			return `${subject}の状態を見る`;
		case "reset_password":
			return "ユーザーのパスワードを再設定";
		case "force_password_change":
			return "ユーザーにパスワード変更を要求";
		case "manage_mfa":
			return "ユーザーのMFAを管理";
		case "acknowledge":
			return "インシデントを確認済みにする";
		case "resolve":
			return "インシデントを解決済みにする";
		case "run":
			return "診断を実行";
		case "approve":
			return "復旧操作を承認";
		case "execute":
			return "復旧操作を実行";
		case "test":
			return "通知テストを送信";
		default:
			return `${subject}: ${humanizePermissionText(action)}`;
	}
}

export function permissionDescription(value: string) {
	if (value === "*") return "全画面と全操作を許可します。管理者ロールだけに付与します。";
	const group = permissionGroupForValue(value);
	return `${group}に関する操作権限です。`;
}

function humanizePermissionText(value: string) {
	return value
		.split(/[_\-.]+/)
		.filter(Boolean)
		.map((part) => part.charAt(0).toUpperCase() + part.slice(1))
		.join(" ");
}
