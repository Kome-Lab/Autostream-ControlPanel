import type { SystemUpdateAgentStatus, SystemUpdateHostStatus, SystemUpdateJob, SystemUpdateReachability, SystemUpdateTarget } from "@/types/domain";
import { normalize, optionalNumberValue } from "./system-update-values";

export function systemUpdateConnectivity(
  target: Pick<SystemUpdateTarget, "host_id" | "updater_id">,
  updaters: SystemUpdateAgentStatus[],
  hosts: SystemUpdateHostStatus[],
) {
  const updater = target.updater_id ? updaters.find((item) => item.updater_id === target.updater_id) : undefined;
  const hostCandidate = target.host_id ? hosts.find((item) => item.host_id === target.host_id) : undefined;
  const host = updater && hostCandidate?.updater_id === updater.updater_id ? hostCandidate : undefined;
  const reachability: SystemUpdateReachability = host?.reachability || "unknown";
  const agentOnline = updater?.online === true;
  const policyReady = updater ? systemUpdateUpdaterPolicyState(updater).ready : false;
  return { updater, host, agentOnline, reachability, ready: agentOnline && policyReady && reachability === "reachable" };
}

export function systemUpdateUpdaterPolicyState(updater: SystemUpdateAgentStatus): {
  label: "未設定" | "反映待ち" | "反映済み" | "反映失敗" | "オフライン";
  tone: "default" | "secondary" | "destructive" | "outline";
  ready: boolean;
} {
  if (!updater.online) return { label: "オフライン", tone: "destructive", ready: false };
  const status = normalize(updater.policy_status);
  const desiredRevision = optionalNumberValue(updater.desired_revision);
  const appliedRevision = optionalNumberValue(updater.applied_revision);
  if (["failed", "error", "rejected", "invalid"].includes(status)) {
    return { label: "反映失敗", tone: "destructive", ready: false };
  }
  if (status === "unconfigured" || desiredRevision === undefined || desiredRevision <= 0) {
    return { label: "未設定", tone: "outline", ready: false };
  }
  if (
    ["pending", "applying", "validating", "waiting"].includes(status)
    || appliedRevision === undefined
    || appliedRevision !== desiredRevision
  ) {
    return { label: "反映待ち", tone: "secondary", ready: false };
  }
  return { label: "反映済み", tone: "default", ready: true };
}

export function systemUpdatePolicyErrorMessage(code?: string) {
  const messages: Record<string, string> = {
    policy_fetch_failed: "Control Panelから新しい設定を取得できませんでした。接続を確認すると自動で再試行します。",
    policy_invalid: "保存された設定をUpdaterが検証できませんでした。入力内容を確認してください。",
    ssh_identity_failed: "対象ホストへ接続するSSH鍵を準備できませんでした。Updaterのデータ領域と権限を確認してください。",
    ssh_connectivity_failed: "対象ホストへSSH接続できません。接続先、SSHポート、ユーザー、公開鍵を確認してください。",
    policy_snapshot_failed: "新しい設定の安全な保存処理に問題が発生しました。更新操作を停止して自動再試行しています。",
    coordinator_start_failed: "新しい設定でUpdaterを開始できませんでした。旧設定を維持しています。",
    active_job_pending: "更新処理中のため反映を待っています。処理完了後に自動で反映します。",
  };
  const normalized = normalize(code);
  return messages[normalized] || String(code || "").trim();
}

export function systemUpdateHostReachabilityLabel(reachability?: SystemUpdateReachability) {
  if (reachability === "reachable") return "到達可";
  if (reachability === "unreachable") return "接続不可";
  return "未確認";
}

export function systemUpdateHostReachabilityMessage(code?: string) {
  const messages: Record<string, string> = {
    ssh_timeout: "SSH接続がタイムアウトしました。",
    ssh_connection_refused: "対象ホストがSSH接続を拒否しました。",
    ssh_auth_failed: "対象ホストへのSSH認証に失敗しました。",
    ssh_host_key_mismatch: "SSHホスト鍵が一致しません。管理者による確認が必要です。",
    remote_helper_unavailable: "対象ホストの更新helperを利用できません。",
    remote_config_invalid: "対象ホストの更新設定を確認できません。",
  };
  return messages[normalize(code)] || "";
}

export function systemUpdateTargetBlockedReason(reason?: string) {
  const code = normalize(reason);
  const messages: Record<string, string> = {
    target_not_configured: "更新対象が更新エージェントに登録されていません。",
    update_agent_unavailable: "Host Agentが設定されていません。",
    updater_not_configured: "Host Agentが設定されていません。",
    updater_missing: "Host Agentが設定されていません。",
    update_agent_offline: "更新エージェントがオフラインです。接続状態を確認してください。",
    updater_offline: "更新エージェントがオフラインです。接続状態を確認してください。",
    updater_unavailable: "更新エージェントに接続できません。",
    target_unreachable: "更新エージェントから対象ホストへ接続できません。",
    target_reachability_unknown: "対象ホストへの接続状態をまだ確認できません。",
    updater_policy_pending: "保存したUpdater設定の反映を待っています。",
    updater_policy_failed: "保存したUpdater設定を反映できませんでした。Updater設定画面を確認してください。",
    updater_policy_mismatch: "更新エージェントの設定反映が完了していません。",
    updater_policy_target_type_mismatch: "更新対象のサービス種別がUpdater設定と一致していません。",
    updater_release_token_not_configured: "GitHub Release Tokenが未設定です。Updater設定画面で保存してください。",
    updater_version_incompatible: "minimum_agent_versionを満たすように更新エージェントを更新してください。",
    current_version_unknown: "現在のバージョンが未報告です。",
    latest_version_unknown: "最新バージョンを確認できません。",
    update_not_available: "適用できる更新はありません。",
    no_update_available: "適用できる更新はありません。",
    stream_active: "配信中です。空き次第の更新を選択してください。",
    target_busy: "この対象では別の更新処理が進行中です。",
    job_in_progress: "この対象では別の更新処理が進行中です。",
    operation_eligibility_unavailable: "更新操作の適格性が未報告です。Control Panelと更新エージェントを更新してから再取得してください。",
    system_update_software_update_not_ready: "この対象ではソフトウェア更新を開始できません。",
    system_update_port_reconfigure_not_ready: "この対象ではサービスのポートを変更できません。",
    unsupported_deployment_mode: "この配備方式は自動更新に対応していません。",
    deployment_mode_unsupported: "この配備方式は自動更新に対応していません。",
    release_manifest_unavailable: "更新用リリース情報を取得できません。",
    docker_release_manifest_unavailable: "Docker Bundleの更新情報を取得できません。",
    release_manifest_missing: "更新用リリース情報が公開されていないため、適用できません。",
    release_manifest_invalid: "更新用リリース情報を検証できないため、適用できません。",
    manifest_unverified: "最新バージョンは確認できましたが、更新用リリース情報を検証できないため自動適用できません。",
    release_version_invalid: "公開された更新バージョンが不正なため、適用できません。",
  };
  if (!code) return "更新条件を満たしていません。";
  return messages[code] || reason || "更新条件を満たしていません。";
}

export function systemUpdateErrorMessage(error: unknown, fallback = "更新処理を開始できませんでした。") {
  const record = error && typeof error === "object" ? error as { code?: string; message?: string; status?: number } : undefined;
  const code = normalize(record?.code || record?.message);
  const messages: Record<string, string> = {
    permission_denied: "システム更新を実行する権限がありません。",
    forbidden: "システム更新を実行する権限がありません。",
    target_not_found: "更新対象が見つかりません。一覧を再取得してください。",
    system_update_target_not_found: "更新対象が見つかりません。一覧を再取得してください。",
    target_not_configured: "更新対象が更新エージェントに登録されていません。",
    update_agent_unavailable: "Host Agentが設定されていません。",
    updater_not_configured: "Host Agentが設定されていません。",
    updater_missing: "Host Agentが設定されていません。",
    update_agent_offline: "更新エージェントがオフラインです。接続状態を確認してください。",
    updater_offline: "更新エージェントがオフラインです。接続状態を確認してください。",
    updater_unavailable: "更新エージェントに接続できません。",
    target_unreachable: "更新エージェントから対象ホストへ接続できません。",
    target_reachability_unknown: "対象ホストへの接続状態をまだ確認できません。",
    updater_version_incompatible: "minimum_agent_versionを満たすように更新エージェントを更新してください。",
    update_not_available: "適用できる更新はありません。",
    no_update_available: "適用できる更新はありません。",
    already_up_to_date: "このサービスはすでに最新です。",
    version_not_found: "適用するバージョンが見つかりません。",
    release_not_found: "更新用リリースを取得できません。",
    release_version_invalid: "公開された更新バージョンが不正なため、適用できません。",
    release_manifest_unavailable: "更新用リリース情報を取得できません。",
    docker_release_manifest_unavailable: "Docker Bundleの更新情報を取得できません。",
    release_manifest_missing: "更新用リリース情報が公開されていないため、適用できません。",
    release_manifest_invalid: "更新用リリース情報を検証できないため、適用できません。",
    manifest_unverified: "更新用リリース情報を検証できないため、自動適用できません。",
    invalid_target: "更新対象の指定が正しくありません。",
    invalid_system_update_request: "更新要求の内容が正しくありません。一覧を再取得してから再試行してください。",
    invalid_system_update_response: "更新サービスから正しい応答を受け取れませんでした。一覧を再取得してください。",
    invalid_port_reconfigure_request: "ポート変更要求が現在のendpoint状態と一致しません。Node情報を再取得してください。",
    invalid_system_update_port_mode: "変更する範囲とポートの入力を確認してください。",
    system_update_port_contract_required: "Host AgentとLocal Executorのポート変更機能が揃うまで利用できません。",
    system_update_port_policy_snapshot_unavailable: "現在の待受と設定を確認できていません。Host Agentの接続と設定反映を確認してください。",
    system_update_port_snapshot_stale: "確認後に設定が変わりました。一覧を再取得して変更内容を確認してください。",
    system_update_port_idempotency_conflict: "同じ要求IDで異なる変更が記録されています。既存ジョブを確認してください。",
    system_update_advertised_only_unsupported: "広告endpointだけの変更はできません。local listenerの変更も指定してください。",
    system_update_port_result_mismatch: "復旧結果と保存済み設定が一致していません。同じジョブの再照合を待ってください。",
    system_update_port_recovery_required: "復旧が未完了です。同じジョブでの再照合が完了するまで新しい変更は開始できません。",
    system_update_host_busy: "同じホストで変更または復旧処理が進行中です。",
    invalid_service_port: "ポートは1024〜65535の整数で指定してください。",
    service_port_reserved: "同じホストで指定したポートが既に使用または予約されています。",
    system_update_endpoint_revision_conflict: "Endpoint revisionが変わりました。Node情報を再取得してからやり直してください。",
    system_update_port_reconfigure_not_ready: "このサービスは現在ポートを変更できません。EndpointとHost Agentの状態を確認してください。",
    invalid_pull_ownership_activation_request: "更新実行権限の切替条件が現在状態と一致しません。Updater状態を再取得してください。",
    invalid_pull_activation_request: "更新実行権限の切替条件が現在状態と一致しません。Updater状態を再取得してください。",
    invalid_pull_deactivation_request: "実行権限解除の条件が現在状態と一致しません。Updater状態を再取得してください。",
    pull_ownership_not_ready: "Host Agent、Local Executor、または対象サービスの準備が完了していません。",
    host_agent_not_ready: "Host Agent、Local Executor、または対象サービスの準備が完了していません。最新状態を再取得してください。",
    system_update_agent_inactive: "Host Agentがオンラインになるまで更新実行権限を切り替えられません。",
    update_agent_inactive: "Host Agentのtokenが有効ではありません。登録状態を確認してください。",
    system_update_agent_binding_mismatch: "Host Agentの実行ホスト割り当てが変わりました。Updater状態を再取得してください。",
    system_update_execution_host_busy: "対象ホストで更新ジョブまたはrecoveryが進行中です。",
    host_lifecycle_busy: "対象ホストで更新、self-update、token rotation、またはmutation grantが進行中です。",
    system_update_ownership_conflict: "更新実行権限のOwnerまたはepochが変わりました。Updater状態を再取得してください。",
    invalid_updater_policy: "Updater設定に不正な項目があります。入力内容を確認してください。",
    invalid_updater_database_name: "Control PanelまたはObservabilityが実際に使用しているMariaDBデータベース名を確認してください。",
    invalid_updater_local_listen_port: "systemdサービスが127.0.0.1で実際に待ち受ける1024〜65535のポートを確認してください。公開HTTPSポート443は指定しません。",
    invalid_updater_host_public_key: "SSHホスト公開鍵を確認できません。対象ホストで確認したssh-ed25519公開鍵の全文を入力してください。",
    invalid_updater_host_bootstrap_request: "ホストセットアップ要求の内容が正しくありません。設定を再取得してから再試行してください。",
    updater_host_not_found: "セットアップ対象ホストが保存済み設定に見つかりません。",
    updater_host_bootstrap_not_ready: "対象ホストは現在セットアップを開始できる状態ではありません。",
    updater_host_bootstrap_status_unavailable: "セットアップ状態を再確認できませんでした。通信状態を確認して再試行してください。",
    updater_host_bootstrap_context_changed: "確認後にUpdaterまたはホスト設定が変わりました。認証情報とFingerprintを再確認してください。",
    updater_host_bootstrap_in_progress: "ホストの自動セットアップ中はUpdater設定を変更できません。完了後に再試行してください。",
    bootstrap_webcrypto_unavailable: "このブラウザでは認証情報を安全に暗号化できません。HTTPS接続と対応ブラウザを確認してください。",
    bootstrap_encryption_public_key_invalid: "Updaterが報告したbootstrap暗号鍵を検証できません。Updaterを更新して再接続してください。",
    bootstrap_administrator_user_invalid: "一時管理者SSHユーザーにはroot以外のLinuxユーザー名を入力してください。",
    bootstrap_private_key_invalid: "OpenSSHまたはPEM形式の一時SSH秘密鍵を入力してください。",
    bootstrap_passphrase_too_long: "秘密鍵パスフレーズが長すぎます。",
    bootstrap_host_keys_unconfirmed: "Updater暗号鍵と各ホストのSSHホスト鍵Fingerprintを確認してください。",
    bootstrap_host_ids_invalid: "セットアップ対象ホストを選択してください。",
    bootstrap_host_ids_duplicate: "セットアップ対象ホストが重複しています。画面を再取得してください。",
    bootstrap_envelope_context_invalid: "セットアップ対象と設定Revisionの組み合わせが不正です。画面を再取得してください。",
    bootstrap_credentials_invalid: "一時管理者SSHユーザーと秘密鍵を確認してください。",
    bootstrap_envelope_too_large: "一時SSH秘密鍵が大きすぎるため安全に送信できません。",
    invalid_bootstrap_envelope: "暗号化した一時認証情報をControl Panelが検証できませんでした。画面を再取得してください。",
    invalid_bootstrap_host_selection: "セットアップ対象ホストの選択が保存済み設定と一致しません。",
    invalid_bootstrap_job_request: "ホストセットアップ要求の内容が正しくありません。",
    bootstrap_job_not_found: "ホストセットアップ処理が見つかりません。状態を再取得してください。",
    bootstrap_job_conflict: "別のホストセットアップが進行中、または同じ要求の状態が変わっています。",
    bootstrap_job_operation_failed: "ホストセットアップ処理を保存または更新できませんでした。",
    bootstrap_policy_revision_mismatch: "Updater設定が変わりました。設定と状態を再取得してから再試行してください。",
    updater_policy_not_applied: "保存したUpdater設定の反映が完了するまでお待ちください。",
    bootstrap_encryption_key_unavailable: "Updaterのbootstrap暗号鍵を取得できません。Updaterの接続状態を確認してください。",
    bootstrap_recipient_key_changed: "Updaterのbootstrap暗号鍵が変わりました。状態を再取得し、Fingerprintを確認してから再試行してください。",
    unsupported_bootstrap_profile: "選択したホスト構成は標準の自動セットアップに対応していません。手動導入を使用してください。",
    secure_transport_required: "一時認証情報を扱うためHTTPS接続が必要です。",
    bootstrap_broker_unavailable: "一時認証情報をUpdaterへ安全に引き渡せませんでした。時間を置いて再試行してください。",
    bootstrap_claim_timeout: "Updaterが有効時間内に一時認証情報を受け取れませんでした。状態を再取得してから再試行してください。",
    credential_expired: "一時認証情報の有効期限が切れました。新しい認証情報で再試行してください。",
    updater_policy_revision_conflict: "Updater設定が別の操作で更新されました。設定画面を開き直してから再度保存してください。",
    updater_policy_pending: "保存したUpdater設定の反映を待っています。反映完了後にもう一度お試しください。",
    updater_policy_failed: "保存したUpdater設定を反映できませんでした。Updater設定画面の状態を確認してください。",
    updater_policy_mismatch: "Control Panelと更新エージェントの設定が一致していません。設定の反映完了を待ってください。",
    updater_policy_target_type_mismatch: "更新対象のサービス種別がUpdater設定と一致していません。対象設定を確認してください。",
    updater_release_token_not_configured: "GitHub Release Tokenが未設定です。Updater設定画面で保存してください。",
    ssh_connectivity_failed: "対象ホストへSSH接続できません。接続先、SSHポート、ユーザー、公開鍵を確認してください。",
    policy_snapshot_failed: "Updater設定の安全な保存に失敗しました。更新エージェントのログとデータディレクトリを確認してください。",
    update_updater_release_token_failed: "GitHub Release Tokenを安全に保存できませんでした。Control Panelの暗号化設定を確認してください。",
    save_updater_policy_failed: "Updater設定を保存できませんでした。Control Panelのログを確認してください。",
    invalid_strategy: "更新方法の指定が正しくありません。",
    stream_active: "配信中のため、今すぐ更新できません。空き次第の更新を選択してください。",
    target_busy: "この対象では別の更新処理が進行中です。",
    system_update_target_busy: "配信中のため、今すぐ更新できません。空き次第の更新を選択してください。",
    system_update_target_unavailable: "現在の状態ではこの対象を更新できません。",
    system_update_target_active: "この対象では別の更新処理が進行中です。",
    job_in_progress: "この対象では別の更新処理が進行中です。",
    update_in_progress: "この対象では別の更新処理が進行中です。",
    conflict: "更新対象の状態が変わりました。一覧を再取得してください。",
    idempotency_conflict: "同じ更新要求が異なる内容で送信されています。一覧を再取得してください。",
    idempotency_key_conflict: "同じ更新要求が異なる内容で送信されています。一覧を再取得してください。",
    checksum_missing: "更新ファイルのチェックサムが公開されていません。更新を中止しました。",
    checksum_mismatch: "更新ファイルの検証に失敗したため、適用しませんでした。",
    signature_invalid: "更新ファイルの署名を確認できないため、適用しませんでした。",
    download_failed: "更新ファイルのダウンロードに失敗しました。",
    install_failed: "更新ファイルを適用できませんでした。ロールバック結果を確認してください。",
    restart_failed: "更新後のサービス再起動に失敗しました。",
    health_check_failed: "更新後のヘルスチェックに失敗しました。ロールバック結果を確認してください。",
    rollback_failed: "更新のロールバックに失敗しました。ホストを直接確認してください。",
    cancel_not_allowed: "この段階の更新はキャンセルできません。",
    system_update_not_cancellable: "この段階の更新はキャンセルできません。",
    job_not_found: "更新ジョブが見つかりません。一覧を再取得してください。",
    system_update_job_not_found: "更新ジョブが見つかりません。一覧を再取得してください。",
    create_system_update_failed: "更新ジョブを作成できませんでした。Control Panelのログを確認してください。",
    cancel_system_update_failed: "更新ジョブをキャンセルできませんでした。Control Panelのログを確認してください。",
    list_system_update_targets_failed: "更新対象を取得できませんでした。Control Panelのログを確認してください。",
    list_system_update_jobs_failed: "更新履歴を取得できませんでした。Control Panelのログを確認してください。",
    stale_report: "更新エージェントの状態報告が古いため、更新を開始できません。",
  };
  const detail = safeErrorDetail(record?.message, code);
  const withDetail = (summary: string) => detail ? `${summary} 詳細: ${detail}` : summary;
  if (messages[code]) return withDetail(messages[code]);
  if (record?.status === 403) return withDetail(messages.permission_denied);
  if (record?.status === 404) return withDetail(messages.target_not_found);
  if (record?.status === 409) return withDetail(messages.conflict);
  if (record?.status && record.status >= 500) return withDetail("更新サービスでエラーが発生しました。更新エージェントとControl Panelのログを確認してください。");
  return withDetail(code ? `${fallback} (${code})` : fallback);
}

export function systemUpdateJobStatusLabel(status?: string) {
  const labels: Record<string, string> = {
    accepted: "受付済み",
    pending: "待機中",
    queued: "待機中",
    claimed: "Updater受付済み",
    reconciling: "適用状態を確認中",
    waiting: "待機中",
    waiting_for_idle: "配信終了待ち",
    downloading: "ダウンロード中",
    verifying: "検証中",
    preparing: "更新準備中",
    staging: "展開準備中",
    staged: "展開済み",
    stopping: "サービス停止中",
    installing: "適用中",
    applying: "適用中",
    starting: "サービス起動中",
    restarting: "再起動中",
    health_checking: "動作確認中",
    rolling_back: "ロールバック中",
    running: "処理中",
    succeeded: "完了",
    success: "完了",
    completed: "完了",
    failed: "失敗",
    cancelled: "キャンセル済み",
    canceled: "キャンセル済み",
    rolled_back: "ロールバック済み",
  };
  return labels[normalize(status)] || status || "不明";
}

export function systemUpdateJobTone(status?: string): "default" | "secondary" | "destructive" | "outline" {
  const value = normalize(status);
  if (["failed", "rollback_failed"].includes(value)) return "destructive";
  if (["succeeded", "success", "completed"].includes(value)) return "default";
  if (["cancelled", "canceled", "rolled_back"].includes(value)) return "outline";
  return "secondary";
}

export function systemUpdateDeploymentLabel(mode?: string) {
  const labels: Record<string, string> = {
    docker: "Docker（Bundle管理）",
    docker_compose: "Docker Compose（Bundle管理）",
    systemd: "systemd",
    binary: "バイナリ",
  };
  return labels[normalize(mode)] || mode || "未設定";
}

export function systemUpdateProgress(job: Pick<SystemUpdateJob, "progress">) {
  const progress = Number(job.progress || 0);
  if (!Number.isFinite(progress)) return 0;
  return Math.min(100, Math.max(0, Math.round(progress)));
}

export function safeErrorDetail(value?: string, code?: string) {
  const detail = String(value || "").replace(/[\u0000-\u001f\u007f]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 500);
  if (!detail || normalize(detail) === normalize(code)) return "";
  return detail;
}
