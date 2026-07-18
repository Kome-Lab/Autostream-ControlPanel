import type {
  SystemUpdateAgentStatus,
  SystemUpdateHostStatus,
  SystemUpdateJob,
  SystemUpdateReachability,
  SystemUpdateStrategy,
  SystemUpdateTarget,
  SystemUpdatesResponse,
} from "@/types/domain";

const activeStatuses = new Set([
  "accepted",
  "pending",
  "queued",
  "claimed",
  "reconciling",
  "waiting",
  "waiting_for_idle",
  "downloading",
  "verifying",
  "preparing",
  "staging",
  "staged",
  "stopping",
  "installing",
  "applying",
  "starting",
  "restarting",
  "health_checking",
  "rolling_back",
  "running",
]);

const cancellableStatuses = new Set(["queued"]);

export function isControlPanelUpdateTarget(target: Pick<SystemUpdateTarget, "target_id" | "target_type">) {
  return target.target_type === "control_panel" || target.target_id === "control-panel";
}

export function isSystemUpdateJobActive(status?: string) {
  return activeStatuses.has(normalize(status));
}

export function isSystemUpdateJobCancellable(status?: string) {
  return cancellableStatuses.has(normalize(status));
}

export function systemUpdateMayDisconnectPanel(status?: string) {
  return new Set(["stopping", "installing", "applying", "starting", "restarting", "health_checking", "rolling_back", "reconciling"]).has(normalize(status));
}

type SemanticVersion = {
  core: [string, string, string];
  prerelease: string[];
};

export function compareSystemUpdateVersions(left: string, right: string): -1 | 0 | 1 | null {
  const leftVersion = parseSemanticVersion(left);
  const rightVersion = parseSemanticVersion(right);
  if (!leftVersion || !rightVersion) return null;

  for (let index = 0; index < leftVersion.core.length; index += 1) {
    const compared = compareNumericIdentifier(leftVersion.core[index], rightVersion.core[index]);
    if (compared !== 0) return compared;
  }

  if (leftVersion.prerelease.length === 0 && rightVersion.prerelease.length === 0) return 0;
  if (leftVersion.prerelease.length === 0) return 1;
  if (rightVersion.prerelease.length === 0) return -1;

  const length = Math.max(leftVersion.prerelease.length, rightVersion.prerelease.length);
  for (let index = 0; index < length; index += 1) {
    const leftIdentifier = leftVersion.prerelease[index];
    const rightIdentifier = rightVersion.prerelease[index];
    if (leftIdentifier === undefined) return -1;
    if (rightIdentifier === undefined) return 1;
    if (leftIdentifier === rightIdentifier) continue;

    const leftNumeric = /^\d+$/.test(leftIdentifier);
    const rightNumeric = /^\d+$/.test(rightIdentifier);
    if (leftNumeric && rightNumeric) return compareNumericIdentifier(leftIdentifier, rightIdentifier);
    if (leftNumeric) return -1;
    if (rightNumeric) return 1;
    return leftIdentifier < rightIdentifier ? -1 : 1;
  }
  return 0;
}

function parseSemanticVersion(raw: string): SemanticVersion | null {
  const value = raw.trim().replace(/^v/, "");
  const match = value.match(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/);
  if (!match) return null;
  const prerelease = match[4] ? match[4].split(".") : [];
  if (prerelease.some((identifier) => /^\d+$/.test(identifier) && identifier.length > 1 && identifier.startsWith("0"))) return null;
  return { core: [match[1], match[2], match[3]], prerelease };
}

function compareNumericIdentifier(left: string, right: string): -1 | 0 | 1 {
  if (left.length !== right.length) return left.length < right.length ? -1 : 1;
  if (left === right) return 0;
  return left < right ? -1 : 1;
}

export function systemUpdateStrategyForTarget(target: Pick<SystemUpdateTarget, "busy" | "current_stream_id">): SystemUpdateStrategy {
  const busy = typeof target.busy === "boolean" ? target.busy : Boolean(target.current_stream_id);
  return busy ? "when_idle" : "maintenance";
}

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
  return { updater, host, agentOnline, reachability, ready: agentOnline && reachability === "reachable" };
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

export function systemUpdateRequest(target: Pick<SystemUpdateTarget, "target_id" | "busy" | "current_stream_id">, idempotencyKey: string) {
  return {
    target_id: target.target_id,
    strategy: systemUpdateStrategyForTarget(target),
    idempotency_key: idempotencyKey,
  };
}

export async function requestSystemUpdateWithRecovery(
  target: SystemUpdateTarget,
  idempotencyKey: string,
  send: (request: ReturnType<typeof systemUpdateRequest>) => Promise<unknown>,
  refreshJobs: () => Promise<SystemUpdateJob[]>,
) {
  try {
    return systemUpdateJobFromResponse(await send(systemUpdateRequest(target, idempotencyKey)));
  } catch (originalError) {
    try {
      const jobs = await refreshJobs();
      const recovered = jobs.find((job) => job.idempotency_key === idempotencyKey);
      if (recovered) return recovered;
    } catch {
      // Preserve the original request failure; React Query may retry this same operation/key.
    }
    throw originalError;
  }
}

export function normalizeSystemUpdatesResponse(value: unknown): SystemUpdatesResponse {
  const response = recordValue(value);
  const updaters = Array.isArray(response.updaters) ? response.updaters.map(normalizeSystemUpdateAgent).filter((updater) => updater.updater_id) : [];
  const hosts = Array.isArray(response.hosts) ? response.hosts.map(normalizeSystemUpdateHost).filter((host) => host.host_id) : [];
  const targets = Array.isArray(response.targets) ? response.targets.map(normalizeSystemUpdateTarget).filter((target) => target.target_id) : [];
  const jobs = Array.isArray(response.jobs) ? response.jobs.map(normalizeSystemUpdateJob).filter((job) => job.id) : [];
  return { updaters, hosts, targets, jobs };
}

export function systemUpdateJobFromResponse(value: unknown): SystemUpdateJob {
  const response = recordValue(value);
  const nestedJob = recordValue(response.job);
  const job = normalizeSystemUpdateJob(Object.keys(nestedJob).length > 0 ? { ...response, ...nestedJob } : response);
  if (!job.id || !job.target_id) throw new Error("invalid_system_update_response");
  return job;
}

export async function runSystemUpdatesSequentially<T>(
  targets: SystemUpdateTarget[],
  run: (target: SystemUpdateTarget, index: number) => Promise<T>,
) {
  const results: T[] = [];
  for (let index = 0; index < targets.length; index += 1) {
    results.push(await run(targets[index], index));
  }
  return results;
}

export function systemUpdateTargetBlockedReason(reason?: string) {
  const code = normalize(reason);
  const messages: Record<string, string> = {
    target_not_configured: "更新対象が中央Updaterに登録されていません。",
    update_agent_unavailable: "中央Updaterが設定されていません。",
    updater_not_configured: "中央Updaterが設定されていません。",
    updater_missing: "中央Updaterが設定されていません。",
    update_agent_offline: "中央Updaterがオフラインです。接続状態を確認してください。",
    updater_offline: "中央Updaterがオフラインです。接続状態を確認してください。",
    updater_unavailable: "中央Updaterに接続できません。",
    target_unreachable: "中央Updaterから対象ホストへ接続できません。",
    target_reachability_unknown: "対象ホストへの接続状態をまだ確認できません。",
    updater_version_incompatible: "minimum_agent_versionを満たすように中央Updaterを更新してください。",
    current_version_unknown: "現在のバージョンが未報告です。",
    latest_version_unknown: "最新バージョンを確認できません。",
    update_not_available: "適用できる更新はありません。",
    no_update_available: "適用できる更新はありません。",
    stream_active: "配信中です。空き次第の更新を選択してください。",
    target_busy: "この対象では別の更新処理が進行中です。",
    job_in_progress: "この対象では別の更新処理が進行中です。",
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
    target_not_configured: "更新対象が中央Updaterに登録されていません。",
    update_agent_unavailable: "中央Updaterが設定されていません。",
    updater_not_configured: "中央Updaterが設定されていません。",
    updater_missing: "中央Updaterが設定されていません。",
    update_agent_offline: "中央Updaterがオフラインです。接続状態を確認してください。",
    updater_offline: "中央Updaterがオフラインです。接続状態を確認してください。",
    updater_unavailable: "中央Updaterに接続できません。",
    target_unreachable: "中央Updaterから対象ホストへ接続できません。",
    target_reachability_unknown: "対象ホストへの接続状態をまだ確認できません。",
    updater_version_incompatible: "minimum_agent_versionを満たすように中央Updaterを更新してください。",
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
    stale_report: "中央Updaterの状態報告が古いため、更新を開始できません。",
  };
  const detail = safeErrorDetail(record?.message, code);
  const withDetail = (summary: string) => detail ? `${summary} 詳細: ${detail}` : summary;
  if (messages[code]) return withDetail(messages[code]);
  if (record?.status === 403) return withDetail(messages.permission_denied);
  if (record?.status === 404) return withDetail(messages.target_not_found);
  if (record?.status === 409) return withDetail(messages.conflict);
  if (record?.status && record.status >= 500) return withDetail("更新サービスでエラーが発生しました。中央UpdaterとControl Panelのログを確認してください。");
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

function normalize(value?: string) {
  return String(value || "").trim().toLowerCase();
}

function safeErrorDetail(value?: string, code?: string) {
  const detail = String(value || "").replace(/[\u0000-\u001f\u007f]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 500);
  if (!detail || normalize(detail) === normalize(code)) return "";
  return detail;
}

function normalizeSystemUpdateTarget(value: unknown): SystemUpdateTarget {
  const target = recordValue(value);
  const updaterID = stringValue(target.updater_id || target.update_agent_id);
  const blockedReason = stringValue(target.blocked_reason);
  return {
    target_id: stringValue(target.target_id),
    target_type: stringValue(target.target_type || target.service_type),
    name: stringValue(target.name || target.target_id),
    host_id: stringValue(target.host_id),
    current_version: stringValue(target.current_version),
    latest_version: stringValue(target.latest_version),
    update_available: Boolean(target.update_available),
    deployment_mode: stringValue(target.deployment_mode),
    updater_id: updaterID,
    updater_online: target.updater_online === true,
    busy: typeof target.busy === "boolean" ? target.busy : undefined,
    current_stream_id: stringValue(target.current_stream_id),
    eligible: Boolean(target.eligible),
    blocked_reason: blockedReason,
    update_check_source: stringValue(target.update_check_source),
    update_check_error: stringValue(target.update_check_error),
  };
}

function normalizeSystemUpdateAgent(value: unknown): SystemUpdateAgentStatus {
  const updater = recordValue(value);
  const updaterID = stringValue(updater.updater_id);
  return {
    updater_id: updaterID,
    name: stringValue(updater.name) || updaterID,
    status: stringValue(updater.status),
    online: updater.online === true,
    version: stringValue(updater.version),
    last_heartbeat_at: stringValue(updater.last_heartbeat_at),
  };
}

function normalizeSystemUpdateHost(value: unknown): SystemUpdateHostStatus {
  const host = recordValue(value);
  const hostID = stringValue(host.host_id);
  return {
    host_id: hostID,
    name: stringValue(host.name) || hostID,
    updater_id: stringValue(host.updater_id),
    reachability: normalizeSystemUpdateReachability(host.reachability),
    reachability_checked_at: stringValue(host.reachability_checked_at),
    reachability_code: stringValue(host.reachability_code),
  };
}

function normalizeSystemUpdateReachability(value: unknown): SystemUpdateReachability {
  const reachability = normalize(stringValue(value));
  return reachability === "reachable" || reachability === "unreachable" ? reachability : "unknown";
}

function normalizeSystemUpdateJob(value: unknown): SystemUpdateJob {
  const job = recordValue(value);
  return {
    id: stringValue(job.id),
    idempotency_key: stringValue(job.idempotency_key),
    target_id: stringValue(job.target_id),
    target_type: stringValue(job.target_type || job.target_service_type),
    current_version: stringValue(job.current_version),
    target_version: stringValue(job.target_version),
    deployment_mode: stringValue(job.deployment_mode),
    strategy: stringValue(job.strategy) as SystemUpdateStrategy || undefined,
    status: stringValue(job.status),
    progress: numberValue(job.progress),
    code: stringValue(job.code),
    message: stringValue(job.message),
    requested_by: stringValue(job.requested_by || job.requested_by_username),
    created_at: stringValue(job.created_at),
    updated_at: stringValue(job.updated_at),
    completed_at: stringValue(job.completed_at),
    sequence: optionalNumberValue(job.sequence),
    report_sequence: optionalNumberValue(job.report_sequence),
    lease_generation: optionalNumberValue(job.lease_generation),
    recovery_required: typeof job.recovery_required === "boolean" ? job.recovery_required : undefined,
    last_status: stringValue(job.last_status),
  };
}

function recordValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value : "";
}

function numberValue(value: unknown) {
  const number = typeof value === "number" ? value : Number(value || 0);
  return Number.isFinite(number) ? number : 0;
}

function optionalNumberValue(value: unknown) {
  if (value === undefined || value === null || value === "") return undefined;
  const number = typeof value === "number" ? value : Number(value);
  return Number.isFinite(number) ? number : undefined;
}
