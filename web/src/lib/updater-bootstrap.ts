import type { SystemUpdateAgentStatus, UpdaterHostBootstrapHostResult, UpdaterHostBootstrapJob, UpdaterHostBootstrapJobsResponse, UpdaterHostBootstrapRequest, UpdaterSettingsHost } from "@/types/domain";
import { normalize, recordValue, stringValue, nonNegativeIntegerValue, optionalNumberValue } from "./system-update-values";
import { systemUpdateUpdaterPolicyState, safeErrorDetail } from "./system-update-presentation";
import { sameUpdaterSettingsHost, sameStringSet } from "./updater-settings-model";

const activeBootstrapStatuses = new Set([
  "awaiting_credentials",
  "queued",
  "claimed",
  "connecting",
  "uploading",
  "verifying",
  "installing",
  "probing",
  "running",
]);

const terminalBootstrapStatuses = new Set([
  "succeeded",
  "failed",
  "partial_failed",
  "credential_expired",
  "canceled",
]);

const terminalBootstrapHostStatuses = new Set([
  "succeeded",
  "failed",
]);

export function isUpdaterHostBootstrapJobActive(status?: string) {
  return activeBootstrapStatuses.has(normalize(status));
}

export function activeUpdaterHostBootstrapStatus(jobs: UpdaterHostBootstrapJob[]) {
  for (const job of jobs) {
    if (isUpdaterHostBootstrapJobActive(job.status)) return job.status;
    if (terminalBootstrapStatuses.has(normalize(job.status))) continue;
    const activeHost = job.hosts.find((host) => isUpdaterHostBootstrapJobActive(host.status));
    if (activeHost) return activeHost.status;
  }
  return "";
}

export function systemUpdateHostBootstrapStatusLabel(status?: string) {
  const labels: Record<string, string> = {
    awaiting_credentials: "認証情報待ち",
    queued: "待機中",
    claimed: "Updater受付済み",
    connecting: "SSH接続中",
    uploading: "helper転送中",
    verifying: "検証中",
    installing: "導入中",
    probing: "動作確認中",
    running: "処理中",
    succeeded: "利用可能",
    failed: "失敗",
    partial_failed: "一部失敗",
    credential_expired: "認証情報期限切れ",
    canceled: "キャンセル済み",
    checking: "状態確認中",
    updater_offline: "Updaterオフライン",
    policy_pending: "設定反映待ち",
    release_token_pending: "Release Token未設定",
    host_unsaved: "設定保存待ち",
    host_key_pending: "ホスト鍵確認待ち",
    client_key_pending: "公開鍵生成待ち",
    encryption_key_pending: "暗号鍵待ち",
    unsupported_profile: "手動導入対象",
    blocked: "開始不可",
  };
  return labels[normalize(status)] || status || "未セットアップ";
}

export type UpdaterHostBootstrapEligibilityReason =
  | ""
  | "updater_offline"
  | "policy_pending"
  | "release_token_pending"
  | "host_unsaved"
  | "host_key_pending"
  | "client_key_pending"
  | "encryption_key_pending"
  | "unsupported_profile"
  | "bootstrap_active"
  | "already_configured";

export function updaterHostBootstrapConfirmationContext(
  updater: SystemUpdateAgentStatus,
  expectedRevision: number,
  selectedHostIDs: string[],
  selectedHosts: UpdaterSettingsHost[],
) {
  if (selectedHostIDs.length === 0) return "";
  return JSON.stringify({
    version: 1,
    updater_id: updater.updater_id,
    expected_revision: expectedRevision,
    encryption_public_key: updater.bootstrap_encryption_public_key || "",
    encryption_key_fingerprint: updater.bootstrap_encryption_key_fingerprint || "",
    host_ids: [...selectedHostIDs].sort(),
    hosts: [...selectedHosts]
      .sort((left, right) => left.host_id.localeCompare(right.host_id))
      .map((host) => ({
        host_id: host.host_id,
        host_public_key: host.host_public_key,
        host_key_fingerprint: host.host_key_fingerprint || host.host_public_key_fingerprint || "",
        ssh_client_public_key: updater.ssh_client_public_keys?.[host.host_id] || "",
        ssh_client_key_fingerprint: updater.ssh_client_key_fingerprints?.[host.host_id] || "",
      })),
  });
}

export function updaterHostBootstrapEligibility({
  updater,
  expectedAppliedRevision,
  savedHost,
  currentHost,
  releaseTokenConfigured,
  bootstrapStatus,
}: {
  updater: SystemUpdateAgentStatus;
  expectedAppliedRevision: number;
  savedHost?: UpdaterSettingsHost;
  currentHost?: UpdaterSettingsHost;
  releaseTokenConfigured: boolean;
  bootstrapStatus?: string;
}): { ready: boolean; reason: UpdaterHostBootstrapEligibilityReason } {
  if (!updater.online) return { ready: false, reason: "updater_offline" };
  const policyState = systemUpdateUpdaterPolicyState(updater);
  if (
    !policyState.ready
    || expectedAppliedRevision <= 0
    || updater.desired_revision !== expectedAppliedRevision
    || updater.applied_revision !== expectedAppliedRevision
  ) {
    return { ready: false, reason: "policy_pending" };
  }
  if (!releaseTokenConfigured) return { ready: false, reason: "release_token_pending" };
  if (!savedHost || !currentHost || !sameUpdaterSettingsHost(savedHost, currentHost)) {
    return { ready: false, reason: "host_unsaved" };
  }
  if (savedHost.user !== "autostream-update-host") {
    return { ready: false, reason: "unsupported_profile" };
  }
  if (!(savedHost.host_key_fingerprint || savedHost.host_public_key_fingerprint)) {
    return { ready: false, reason: "host_key_pending" };
  }
  const clientPublicKey = updater.ssh_client_public_keys?.[savedHost.host_id] || "";
  const clientKeyFingerprint = updater.ssh_client_key_fingerprints?.[savedHost.host_id] || "";
  if (!clientPublicKey || !clientKeyFingerprint) return { ready: false, reason: "client_key_pending" };
  if (!updater.bootstrap_encryption_public_key || !updater.bootstrap_encryption_key_fingerprint) {
    return { ready: false, reason: "encryption_key_pending" };
  }
  if (isUpdaterHostBootstrapJobActive(bootstrapStatus)) return { ready: false, reason: "bootstrap_active" };
  if (normalize(bootstrapStatus) === "succeeded") return { ready: true, reason: "already_configured" };
  return { ready: true, reason: "" };
}

export function isUpdaterHostBootstrapBulkCandidate(
  eligibility: ReturnType<typeof updaterHostBootstrapEligibility>,
) {
  return eligibility.ready && eligibility.reason !== "already_configured";
}

export function updaterHostBootstrapEligibilityMessage(
  reason: UpdaterHostBootstrapEligibilityReason | undefined,
  statusKnown: boolean,
) {
  if (!statusKnown) return "セットアップ状態を確認中です。";
  const messages: Record<UpdaterHostBootstrapEligibilityReason, string> = {
    "": "ホストをセットアップします。",
    updater_offline: "独立Updaterがオフラインです。",
    policy_pending: "保存した設定projectionが独立Updaterへ反映されるまでお待ちください。",
    release_token_pending: "GitHub Release Tokenを保存してからホストセットアップを開始してください。",
    host_unsaved: "このホストの変更を先に保存してください。",
    host_key_pending: "保存済みSSHホスト鍵のFingerprintを確認できるまでお待ちください。",
    client_key_pending: "対象ホスト用のUpdater公開鍵が生成されるまでお待ちください。",
    encryption_key_pending: "Updaterのbootstrap暗号鍵が報告されるまでお待ちください。",
    unsupported_profile: "自動bootstrapは検証済みの標準Host Agent profileだけに対応しています。カスタム構成は手動導入してください。",
    bootstrap_active: "このホストのセットアップが進行中です。",
    already_configured: "このホストはセットアップ済みです。必要な場合は再セットアップできます。",
  };
  return messages[reason || ""];
}

export async function requestUpdaterHostBootstrapWithRecovery(
  request: UpdaterHostBootstrapRequest,
  send: (request: UpdaterHostBootstrapRequest) => Promise<UpdaterHostBootstrapJobsResponse>,
  refreshJobs: () => Promise<UpdaterHostBootstrapJob[]>,
): Promise<UpdaterHostBootstrapJobsResponse> {
  let originalError: unknown;
  try {
    return bootstrapResponseForRequest(await send(request), request);
  } catch (error) {
    originalError = error;
  }

  const recovered = await recoverUpdaterHostBootstrapJob(request, refreshJobs);
  if (recovered) return { jobs: [recovered] };
  if (!ambiguousUpdaterHostBootstrapCreateError(originalError)) throw originalError;

  try {
    // A retry is safe only because the exact same job ID, idempotency key, host
    // set, and encrypted envelope are reused. The broker returns the existing
    // job when the first POST committed but its response was lost.
    return bootstrapResponseForRequest(await send(request), request);
  } catch (retryError) {
    const recoveredAfterRetry = await recoverUpdaterHostBootstrapJob(request, refreshJobs);
    if (recoveredAfterRetry) return { jobs: [recoveredAfterRetry] };
    // Once the first POST is ambiguous, a later 4xx does not prove that the
    // first request was rejected: authentication, policy, or token state may
    // have changed after it committed. Only an exact correlated job or a
    // successful replay resolves the original ambiguity.
    throw new UpdaterHostBootstrapRequestAmbiguousError(retryError);
  }
}

export type UpdaterHostBootstrapRequestIdentity = Pick<
  UpdaterHostBootstrapRequest,
  "job_id" | "idempotency_key" | "expected_revision" | "host_ids"
>;

export function updaterHostBootstrapRequestIdentity(
  request: UpdaterHostBootstrapRequest,
): UpdaterHostBootstrapRequestIdentity {
  return {
    job_id: request.job_id,
    idempotency_key: request.idempotency_key,
    expected_revision: request.expected_revision,
    host_ids: [...request.host_ids],
  };
}

export async function recoverUpdaterHostBootstrapRequest(
  request: UpdaterHostBootstrapRequestIdentity,
  refreshJobs: () => Promise<UpdaterHostBootstrapJob[]>,
): Promise<UpdaterHostBootstrapJobsResponse | undefined> {
  const recovered = await recoverUpdaterHostBootstrapJob(request, refreshJobs);
  return recovered ? { jobs: [recovered] } : undefined;
}

export class UpdaterHostBootstrapRequestAmbiguousError extends Error {
  constructor(cause?: unknown) {
    super("updater_host_bootstrap_request_ambiguous", { cause });
    this.name = "UpdaterHostBootstrapRequestAmbiguousError";
  }
}

export function normalizeUpdaterHostBootstrapJobsResponse(
  value: unknown,
  fallbackUpdaterID = "",
): UpdaterHostBootstrapJobsResponse {
  const response = recordValue(value);
  const jobs = Array.isArray(response.jobs)
    ? response.jobs.map((value) => normalizeUpdaterHostBootstrapJob(value, fallbackUpdaterID)).filter((job) => job.id)
    : [];
  return { jobs };
}

function normalizeUpdaterHostBootstrapJob(value: unknown, fallbackUpdaterID: string): UpdaterHostBootstrapJob {
  const job = recordValue(value);
  const status = stringValue(job.status);
  const normalizedHosts = Array.isArray(job.hosts)
    ? job.hosts.map(normalizeUpdaterHostBootstrapHostResult).filter((host) => host.host_id)
    : [];
  const hosts = terminalBootstrapStatuses.has(normalize(status))
    ? normalizedHosts.map((host) => (
      terminalBootstrapHostStatuses.has(normalize(host.status))
        ? host
        : { ...host, status }
    ))
    : normalizedHosts;
  const rawHostIDs = Array.isArray(job.host_ids) ? job.host_ids.map(stringValue) : hosts.map((host) => host.host_id);
  const hostIDs = [...new Set(rawHostIDs.map((hostID) => hostID.trim()).filter(Boolean))].sort();
  return {
    id: stringValue(job.id || job.job_id),
    idempotency_key: stringValue(job.idempotency_key),
    updater_id: stringValue(job.updater_id) || fallbackUpdaterID,
    expected_revision: nonNegativeIntegerValue(job.expected_revision, 0),
    status,
    host_ids: hostIDs,
    hosts,
    created_at: stringValue(job.created_at),
    updated_at: stringValue(job.updated_at),
    completed_at: stringValue(job.completed_at),
  };
}

function normalizeUpdaterHostBootstrapHostResult(value: unknown): UpdaterHostBootstrapHostResult {
  const host = recordValue(value);
  return {
    host_id: stringValue(host.host_id),
    status: stringValue(host.status),
    progress: optionalNumberValue(host.progress),
    code: stringValue(host.code),
    message: safeErrorDetail(stringValue(host.message)),
    updated_at: stringValue(host.updated_at),
    completed_at: stringValue(host.completed_at),
  };
}

function bootstrapResponseForRequest(
  response: UpdaterHostBootstrapJobsResponse,
  request: UpdaterHostBootstrapRequest,
) {
  const recovered = response.jobs.find((job) => updaterHostBootstrapJobMatchesRequest(job, request));
  if (!recovered) throw new Error("invalid_updater_host_bootstrap_response");
  return { jobs: [recovered] };
}

async function recoverUpdaterHostBootstrapJob(
  request: UpdaterHostBootstrapRequestIdentity,
  refreshJobs: () => Promise<UpdaterHostBootstrapJob[]>,
) {
  try {
    const jobs = await refreshJobs();
    return jobs.find((job) => updaterHostBootstrapJobMatchesRequest(job, request));
  } catch {
    return undefined;
  }
}

function updaterHostBootstrapJobMatchesRequest(
  job: UpdaterHostBootstrapJob,
  request: UpdaterHostBootstrapRequestIdentity,
) {
  return job.id === request.job_id
    && job.idempotency_key === request.idempotency_key
    && job.expected_revision === request.expected_revision
    && sameStringSet(job.host_ids, request.host_ids);
}

function ambiguousUpdaterHostBootstrapCreateError(error: unknown) {
  if (!error || typeof error !== "object" || !("status" in error)) return true;
  const status = Number((error as { status?: unknown }).status);
  return !Number.isInteger(status) || status < 400 || status >= 500;
}
