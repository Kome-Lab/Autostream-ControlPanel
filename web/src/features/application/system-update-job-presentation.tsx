"use client";

import { isSystemUpdateJobActive } from "@/lib/system-update-target-policy";
import { systemUpdateErrorMessage, systemUpdateJobStatusLabel } from "@/lib/system-update-presentation";
import { systemUpdatePortJobResultLabel } from "@/lib/system-update-port-requests";
import type { SystemUpdateJob, SystemUpdateTarget } from "@/types/domain";
import { type Feedback } from "./application-operation-types";

export function targetDisplayName(job: SystemUpdateJob, targets: SystemUpdateTarget[]) { return targets.find((target) => target.target_id === job.target_id)?.name || job.target_id; }

export function systemUpdateJobMessage(job: SystemUpdateJob) {
  const fallback = systemUpdateJobStatusLabel(job.status);
  const summary = job.code ? systemUpdateErrorMessage({ code: job.code }, fallback) : fallback;
  const result = systemUpdatePortJobResultLabel(job);
  return result ? `${summary} · ${result}` : summary;
}

export function systemUpdateJobDisplayStatus(job: SystemUpdateJob) {
  const status = job.status === "queued" && job.strategy === "when_idle" ? "配信終了待ち" : systemUpdateJobStatusLabel(job.status);
  const result = systemUpdatePortJobResultLabel(job);
  return result ? `${status} · ${result}` : status;
}

export function systemUpdateJobOperationLabel(job: SystemUpdateJob) { return job.operation === "port_reconfigure" ? "ポート変更" : "ソフトウェア更新"; }

export function systemUpdateJobChangeSummary(job: SystemUpdateJob) {
  if (job.operation === "port_reconfigure") {
    if (job.port_reconfigure?.port_contract_version === 2) {
      const { before, target } = job.port_reconfigure;
      return `local ${before?.local_listen_port ?? "-"} → ${target?.local_listen_port ?? "-"} / 広告 ${before?.advertised_port ?? "-"} → ${target?.advertised_port ?? "-"}`;
    }
    const docker = job.port_reconfigure?.docker;
    if (docker) {
      return `広告 ${job.port_reconfigure?.old_port ?? "-"} → ${job.port_reconfigure?.new_port ?? "-"} / published ${docker.old_published_port} → ${docker.new_published_port} / container ${docker.old_container_port} → ${docker.new_container_port}`;
    }
    return `port ${job.port_reconfigure?.old_port ?? "-"} → ${job.port_reconfigure?.new_port ?? "-"}`;
  }
  return `${job.current_version || "-"} → ${job.target_version || "-"}`;
}

export function systemUpdateSucceeded(status?: string) { return ["succeeded", "success", "completed"].includes(String(status || "").toLowerCase()); }

export function selfUpdateTerminalFeedback(job?: SystemUpdateJob): Feedback | null { if (!job || isSystemUpdateJobActive(job.status)) return null; if (systemUpdateSucceeded(job.status)) return { tone: "success", message: "Control Panelの更新が完了しました。新しい管理画面へ再読み込みします。" }; if (["failed", "rolled_back", "cancelled", "canceled"].includes(String(job.status || "").toLowerCase())) return { tone: "error", message: `Control Panelの更新は完了しませんでした。${systemUpdateJobMessage(job)}` }; return null; }
