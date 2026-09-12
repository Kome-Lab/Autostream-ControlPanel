"use client";

import { systemUpdateTargetBlockedReason } from "@/lib/system-update-presentation";

export function validPortInput(value: string, minimum: number) {
  const parsed = Number(value);
  return /^[0-9]+$/.test(value.trim()) && Number.isSafeInteger(parsed) && parsed >= minimum && parsed <= 65535;
}

export function formatPort(port?: number) {
  return Number.isSafeInteger(port) && Number(port) > 0 ? String(port) : "未報告";
}

export function dockerPortMappingLabel(state?: string) {
  if (state === "applied") return "mapping反映済み";
  if (state === "drifted") return "mapping差分あり";
  return "mapping未確認";
}

export function dockerPortMappingTone(state?: string): "default" | "secondary" | "destructive" | "outline" {
  if (state === "applied") return "default";
  if (state === "drifted") return "destructive";
  return "secondary";
}

export function portReconfigureReasonMessage(reason: string) {
  const messages: Record<string, string> = {
    permission_denied: "ポート変更には system_updates.execute 権限が必要です。",
    invalid_service_port: "1024〜65535の整数を入力してください。",
    invalid_advertised_port: "公開endpointには1〜65535の整数を入力してください。1024未満は既存reverse proxyの公開portとしてのみ使用してください。",
    invalid_published_port: "localhost publishedポートには1024〜65535の整数を入力してください。",
    invalid_container_port: "container待受ポートには1024〜65535の整数を入力してください。",
    port_unchanged: "現在適用中のポートとは異なる値を入力してください。",
    advanced_mode_required: "Dockerの3つのポートを区別して確認するため、詳細設定を開いてください。",
    unsupported_deployment: "この配備方式のポート変更は利用できません。",
    docker_mapping_drifted: "Docker mappingに差分があります。安全な現在値を確認できるまでポート変更を開始できません。",
    docker_mapping_unavailable: "Host Agentから検証済みDocker mappingが報告されていません。Agent・Executor・Compose設定を更新して再取得してください。",
    request_pending: "ポート変更要求を送信中です。",
    request_ambiguous: "前回要求の結果が不明です。履歴で確認できるまで再送しません。",
    target_busy: "サービスが使用中のためポートを変更できません。",
    active_job: "このサービスでは別の更新ジョブが進行中です。",
    recovery_required: "以前の更新結果を復旧・確認中です。",
    endpoint_recovery: "Endpointをロールバック中です。",
    endpoint_not_applied: "Endpointが反映済みになるまで変更できません。",
    updater_not_ready: "Host Agentまたは設定が反映済みになるまで変更できません。",
    operation_eligibility_unavailable: "Control Panelからポート変更の適格性が報告されていません。APIとHost Agentを更新してから再取得してください。",
    updater_missing: "このサービスを管理するHost Agentが割り当てられていません。",
    updater_offline: "Host Agentがオフラインです。Heartbeatを確認してください。",
    target_unreachable: "Host Agentが対象ホストの到達状態を確認できません。",
    target_reachability_unknown: "Host Agentによる対象ホストの到達確認を待っています。",
    updater_policy_pending: "Host Agentが保存済み設定を反映するまで待っています。",
    updater_policy_failed: "Host Agentが保存済み設定を反映できませんでした。設定画面のエラーを確認してください。",
    updater_policy_mismatch: "Host Agentの設定revisionが一致していません。反映完了を待ってください。",
    updater_policy_target_type_mismatch: "サービス種別がHost Agentの対象設定と一致していません。",
    system_update_target_busy: "サービスが配信処理で使用中のため、ポートを変更できません。",
    system_update_port_reconfigure_not_ready: "このサービスは現在ポートを変更できません。EndpointとHost Agentの状態を確認してください。",
    service_port_reserved: "同じホストで指定したポートが既に使用または予約されています。",
    system_update_endpoint_revision_conflict: "Endpoint revisionが変わりました。Node情報を再取得してください。",
  };
  return messages[reason] || (reason ? systemUpdateTargetBlockedReason(reason) : "");
}
