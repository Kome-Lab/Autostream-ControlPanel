"use client";

import { useI18n } from "@/components/admin/i18n-provider";
import { DefinitionList } from "@/components/data-display/definition-list";
import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { presentNodeConnectivityStatus, presentNodeHealthStatus } from "@/lib/foundation/status/node-presenters";
import type { WorkerNode } from "@/types/domain";

export function NodeStateDetails({ node }: { node: WorkerNode }) {
  const { t, locale } = useI18n();
  const ja = locale === "ja";
  return <DefinitionList className="gap-y-1.5 sm:grid-cols-1 [&>div]:flex [&>div]:flex-wrap [&>div]:items-start [&>div]:gap-x-2 [&>div>dt]:shrink-0 [&>div>dd]:mt-0 [&>div>dd]:min-w-0 [&>div>dd]:max-w-full" items={[
    { label: ja ? "接続" : "Connection", value: <DomainStatusBadge presentation={presentNodeConnectivityStatus(node.status)} translate={t} /> },
    { label: ja ? "プロセス稼働" : "Process health", value: <DomainStatusBadge presentation={presentNodeHealthStatus(node.health_status)} translate={t} /> },
    { label: ja ? "担当配信" : "Current stream", value: node.current_stream_id || (ja ? "担当なし" : "Unassigned") },
    { label: ja ? "担当区分" : "Assignment role", value: node.assignment_role || (ja ? "未報告" : "Not reported") },
    { label: ja ? "稼働ジョブ" : "Active jobs", value: node.metrics?.active_jobs ?? node.metrics?.runningJobs ?? (ja ? "未報告" : "Not reported") },
  ]} />;
}
