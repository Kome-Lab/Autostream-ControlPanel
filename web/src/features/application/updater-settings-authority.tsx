"use client";

import { updaterAuthorityFingerprint, type UpdaterActionAuthority } from "@/features/application/updater-action-policy";
import type { SystemUpdateAgentStatus, SystemUpdateJob, UpdaterSettings } from "@/types/domain";
import { type UpdaterSettingsFormState } from "./updater-settings-form-model";

export function ownershipActionAuthoritySnapshot(
  actionID: "UPD-06" | "UPD-07",
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings | undefined,
  jobs: SystemUpdateJob[],
  applicable: boolean,
) {
  const ownership = settings?.execution_host_ownership;
  const activation = settings?.pull_activation;
  const relevantJobs = jobs
    .filter((job) => job.updater_id === updater.updater_id || settings?.targets.some((target) => target.target_id === job.target_id))
    .sort((left, right) => left.id.localeCompare(right.id));
  return {
    applicable,
    fingerprint: updaterAuthorityFingerprint([
      actionID,
      updater.updater_id,
      updater.transport_mode,
      updater.execution_host_id,
      updater.ownership_epoch,
      updater.online,
      updater.last_heartbeat_at,
      updater.desired_revision,
      updater.applied_revision,
      updater.policy_status,
      settings?.revision,
      settings?.projection_revision,
      settings?.local_executor_policy_revision,
      settings?.local_executor_policy_sha256,
      settings?.execution_host_id,
      ownership?.transport_mode,
      ownership?.agent_service_id,
      ownership?.ownership_epoch,
      ownership?.policy_revision,
      activation?.ready,
      activation?.status,
      activation?.last_heartbeat_at,
      activation?.observe_only,
      activation?.update_executor,
      activation?.mutation_enabled,
      activation?.recovery_pending,
      activation?.reported_ownership_epoch,
      activation?.reported_projection_revision,
      ...relevantJobs.flatMap((job) => [job.id, job.status, job.updated_at, job.recovery_required]),
    ]),
  };
}

export function updaterSettingsFormFingerprint(
  baseRevision: number,
  form: UpdaterSettingsFormState,
  deleteGitHubToken: boolean,
  githubTokenPresent: boolean,
) {
  return updaterAuthorityFingerprint([
    "UPD-08-form",
    baseRevision,
    form.pollInterval,
    form.heartbeatInterval,
    form.localExecutorPolicySHA256,
    deleteGitHubToken,
    githubTokenPresent,
    ...form.hosts.flatMap((host) => [
      host.host_id,
      host.name,
      host.address,
      host.port,
      host.user,
      host.arch,
      host.host_public_key,
    ]),
    ...form.targets.flatMap((target) => [
      target.target_id,
      target.service_id,
      target.host_id,
      target.service_type,
      target.deployment_mode,
      target.database_name,
      target.local_listen_port,
    ]),
  ]);
}

export function updaterSettingsActionAuthoritySnapshot(
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings | undefined,
  formFingerprint: string,
  applicable: boolean,
) {
  return {
    applicable,
    fingerprint: updaterAuthorityFingerprint([
      "UPD-08",
      updater.updater_id,
      updater.transport_mode,
      updater.execution_host_id,
      updater.ownership_epoch,
      settings?.revision,
      settings?.projection_revision,
      settings?.local_executor_policy_revision,
      settings?.local_executor_policy_sha256,
      settings?.github_token_configured,
      settings?.transport_mode,
      settings?.execution_host_id,
      formFingerprint,
    ]),
  };
}

export function unavailableUpdaterAuthority(authorityFingerprint: string): UpdaterActionAuthority {
  return Object.freeze({
    permission: "unknown",
    freshness: "unavailable",
    applicability: "unknown",
    authorityFingerprint,
  });
}

export function ownershipEligibilityMessage(reason: string) {
  const messages: Record<string, string> = {
    unsupported_transport: "pull_v2ではありません",
    pull_ownership_contract_unavailable: "切替用revisionまたは現在Ownerが未報告です",
    pull_rollback_contract_unavailable: "現在のpull owner fenceが未報告です",
    request_pending: "切替要求を送信中です",
    request_ambiguous: "切替結果を確認中です",
    observer_offline: "Host Agentがオフラインです",
    recovery_required: "Host Agentがrecovery中です",
    active_job: "対象ホストで更新ジョブが進行中です",
    observer_not_ready: "ObserverまたはLocal Executorの準備が完了していません",
  };
  return messages[reason] || "";
}

export function pullOwnershipMutationErrorIsAmbiguous(error: unknown) {
  if (!error || typeof error !== "object" || !("status" in error)) return true;
  const status = Number((error as { status?: unknown }).status);
  return !Number.isInteger(status) || status < 400 || status >= 500;
}
