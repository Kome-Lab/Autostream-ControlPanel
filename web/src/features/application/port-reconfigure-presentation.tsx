"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { systemUpdateTargetBlockedReason } from "@/lib/system-update-presentation";

export function validPortInput(value: string, minimum: number) {
  const parsed = Number(value);
  return /^[0-9]+$/.test(value.trim()) && Number.isSafeInteger(parsed) && parsed >= minimum && parsed <= 65535;
}

export function formatPort(port?: number, uiText: UICopy = japaneseCopy) {
  return Number.isSafeInteger(port) && Number(port) > 0 ? String(port) : uiText("未報告");
}

export function dockerPortMappingLabel(state?: string, uiText: UICopy = japaneseCopy) {
  if (state === "applied") return uiText("mapping反映済み");
  if (state === "drifted") return uiText("mapping差分あり");
  return uiText("mapping未確認");
}

export function dockerPortMappingTone(state?: string): "default" | "secondary" | "destructive" | "outline" {
  if (state === "applied") return "default";
  if (state === "drifted") return "destructive";
  return "secondary";
}

export function portReconfigureReasonMessage(reason: string, uiText: UICopy = japaneseCopy) {
  const messages: Record<string, string> = {
    permission_denied: uiText("ポート変更には system_updates.execute 権限が必要です。"),
    invalid_service_port: uiText("1024〜65535の整数を入力してください。"),
    invalid_advertised_port: uiText("公開endpointには1〜65535の整数を入力してください。1024未満は既存reverse proxyの公開portとしてのみ使用してください。"),
    invalid_published_port: uiText("localhost publishedポートには1024〜65535の整数を入力してください。"),
    invalid_container_port: uiText("container待受ポートには1024〜65535の整数を入力してください。"),
    port_unchanged: uiText("現在適用中のポートとは異なる値を入力してください。"),
    advanced_mode_required: uiText("Dockerの3つのポートを区別して確認するため、詳細設定を開いてください。"),
    unsupported_deployment: uiText("この配備方式のポート変更は利用できません。"),
    docker_mapping_drifted: uiText("Docker mappingに差分があります。安全な現在値を確認できるまでポート変更を開始できません。"),
    docker_mapping_unavailable: uiText("Host Agentから検証済みDocker mappingが報告されていません。Agent・Executor・Compose設定を更新して再取得してください。"),
    request_pending: uiText("ポート変更要求を送信中です。"),
    request_ambiguous: uiText("前回要求の結果が不明です。履歴で確認できるまで再送しません。"),
    target_busy: uiText("サービスが使用中のためポートを変更できません。"),
    active_job: uiText("このサービスでは別の更新ジョブが進行中です。"),
    recovery_required: uiText("以前の更新結果を復旧・確認中です。"),
    endpoint_recovery: uiText("Endpointをロールバック中です。"),
    endpoint_not_applied: uiText("Endpointが反映済みになるまで変更できません。"),
    updater_not_ready: uiText("Host Agentまたは設定が反映済みになるまで変更できません。"),
    operation_eligibility_unavailable: uiText("Control Panelからポート変更の適格性が報告されていません。APIとHost Agentを更新してから再取得してください。"),
    updater_missing: uiText("このサービスを管理するHost Agentが割り当てられていません。"),
    updater_offline: uiText("Host Agentがオフラインです。Heartbeatを確認してください。"),
    target_unreachable: uiText("Host Agentが対象ホストの到達状態を確認できません。"),
    target_reachability_unknown: uiText("Host Agentによる対象ホストの到達確認を待っています。"),
    updater_policy_pending: uiText("Host Agentが保存済み設定を反映するまで待っています。"),
    updater_policy_failed: uiText("Host Agentが保存済み設定を反映できませんでした。設定画面のエラーを確認してください。"),
    updater_policy_mismatch: uiText("Host Agentの設定revisionが一致していません。反映完了を待ってください。"),
    updater_policy_target_type_mismatch: uiText("サービス種別がHost Agentの対象設定と一致していません。"),
    system_update_target_busy: uiText("サービスが配信処理で使用中のため、ポートを変更できません。"),
    system_update_port_reconfigure_not_ready: uiText("このサービスは現在ポートを変更できません。EndpointとHost Agentの状態を確認してください。"),
    service_port_reserved: uiText("同じホストで指定したポートが既に使用または予約されています。"),
    system_update_endpoint_revision_conflict: uiText("Endpoint revisionが変わりました。Node情報を再取得してください。"),
  };
  return messages[reason] || (reason ? fixedPresentationText(systemUpdateTargetBlockedReason(reason), uiText) : "");
}
