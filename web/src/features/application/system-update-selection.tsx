"use client";

import { isControlPanelUpdateTarget, isSystemUpdateJobActive, systemUpdateStrategyForTarget, systemUpdateSoftwareOperationEligibility } from "@/lib/system-update-target-policy";
import { systemUpdateConnectivity } from "@/lib/system-update-presentation";
import type { SystemUpdateAgentStatus, SystemUpdateHostStatus, SystemUpdateJob, SystemUpdateTarget, SystemUpdatesResponse } from "@/types/domain";

export function compareUpdateJobs(a: SystemUpdateJob, b: SystemUpdateJob) { return Date.parse(b.created_at || b.updated_at || "") - Date.parse(a.created_at || a.updated_at || ""); }

export function latestJobsByTarget(jobs: SystemUpdateJob[]) { const result = new Map<string, SystemUpdateJob>(); for (const job of jobs) if (!result.has(job.target_id)) result.set(job.target_id, job); return result; }

export function updateCanStart(target: SystemUpdateTarget, latestJob: SystemUpdateJob | undefined, updaters: SystemUpdateAgentStatus[], hosts: SystemUpdateHostStatus[]) {
  const operationEligibility = systemUpdateSoftwareOperationEligibility(target);
  const eligibleForStrategy = target.eligible || (systemUpdateStrategyForTarget(target) === "when_idle" && target.blocked_reason === "stream_active");
  return operationEligibility.ready
    && target.update_available
    && eligibleForStrategy
    && systemUpdateConnectivity(target, updaters, hosts).ready
    && !(latestJob && isSystemUpdateJobActive(latestJob.status));
}

export function orderBatchTargets(targets: SystemUpdateTarget[]) { return [...targets].sort((a, b) => Number(isControlPanelUpdateTarget(a)) - Number(isControlPanelUpdateTarget(b))); }

export function availableSystemUpdateTargets(response: SystemUpdatesResponse) {
  const jobsByTarget = latestJobsByTarget([...response.jobs].sort(compareUpdateJobs));
  return orderBatchTargets(response.targets.filter((target) => updateCanStart(target, jobsByTarget.get(target.target_id), response.updaters, response.hosts)));
}
