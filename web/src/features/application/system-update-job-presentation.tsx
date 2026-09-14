"use client";
import { fixedPresentationText } from "@/lib/i18n/ui-v2/presentation-copy";

import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import { isSystemUpdateJobActive } from "@/lib/system-update-target-policy";
import { systemUpdateErrorMessage, systemUpdateJobStatusLabel } from "@/lib/system-update-presentation";
import { systemUpdatePortJobResultLabel } from "@/lib/system-update-port-requests";
import type { SystemUpdateJob, SystemUpdateTarget } from "@/types/domain";
import { type Feedback } from "./application-operation-types";

export function targetDisplayName(job: SystemUpdateJob, targets: SystemUpdateTarget[]) { return targets.find((target) => target.target_id === job.target_id)?.name || job.target_id; }

export function systemUpdateJobMessage(job: SystemUpdateJob, uiText: UICopy = japaneseCopy) {
  const fallback = fixedPresentationText(systemUpdateJobStatusLabel(job.status), uiText);
  const summary = job.code ? fixedPresentationText(systemUpdateErrorMessage({ code: job.code }, fallback), uiText) : fallback;
  const result = systemUpdatePortJobResultLabel(job);
  return result ? `${summary} · ${result}` : summary;
}

export function systemUpdateJobDisplayStatus(job: SystemUpdateJob, uiText: UICopy = japaneseCopy) {
  const status = job.status === "queued" && job.strategy === "when_idle" ? uiText("配信終了待ち") : fixedPresentationText(systemUpdateJobStatusLabel(job.status), uiText);
  const result = fixedPresentationText(systemUpdatePortJobResultLabel(job), uiText);
  return result ? `${status} · ${result}` : status;
}

export function systemUpdateJobOperationLabel(job: SystemUpdateJob, uiText: UICopy = japaneseCopy) { return job.operation === "port_reconfigure" ? uiText("ポート変更") : uiText("ソフトウェア更新"); }

export function systemUpdateJobChangeSummary(job: SystemUpdateJob, uiText: UICopy = japaneseCopy) {
  if (job.operation === "port_reconfigure") {
    if (job.port_reconfigure?.port_contract_version === 2) {
      const { before, target } = job.port_reconfigure;
      return uiText("local {0} → {1} / 広告 {2} → {3}", before?.local_listen_port ?? "-", target?.local_listen_port ?? "-", before?.advertised_port ?? "-", target?.advertised_port ?? "-");
    }
    const docker = job.port_reconfigure?.docker;
    if (docker) {
      return uiText("広告 {0} → {1} / published {2} → {3} / container {4} → {5}", job.port_reconfigure?.old_port ?? "-", job.port_reconfigure?.new_port ?? "-", docker.old_published_port, docker.new_published_port, docker.old_container_port, docker.new_container_port);
    }
    return `port ${job.port_reconfigure?.old_port ?? "-"} → ${job.port_reconfigure?.new_port ?? "-"}`;
  }
  return `${job.current_version || "-"} → ${job.target_version || "-"}`;
}

export function systemUpdateSucceeded(status?: string) { return ["succeeded", "success", "completed"].includes(String(status || "").toLowerCase()); }

export function selfUpdateTerminalFeedback(job?: SystemUpdateJob, uiText: UICopy = japaneseCopy): Feedback | null { if (!job || isSystemUpdateJobActive(job.status)) return null; if (systemUpdateSucceeded(job.status)) return { tone: "success", message: uiText("Control Panelの更新が完了しました。新しい管理画面へ再読み込みします。") }; if (["failed", "rolled_back", "cancelled", "canceled"].includes(String(job.status || "").toLowerCase())) return { tone: "error", message: uiText("Control Panelの更新は完了しませんでした。{0}", systemUpdateJobMessage(job, uiText)) }; return null; }
