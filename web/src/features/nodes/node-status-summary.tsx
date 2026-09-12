"use client";

import { Badge } from "@/components/ui/badge";
import { StatusBadge } from "@/components/admin/status-badge";
import type { WorkerNode } from "@/types/domain";
import { nodeTypes } from "./node-registration-model";
import { nodeReportedPlatform, NodeMetricsSummary } from "./node-metrics-summary";

function nodeTypeLabel(type?: string) {
  if (type === "update_agent") return "Updater（Host Agent）";
  return nodeTypes.find((item) => item.value === type)?.label || type || "-";
}

export function NodeTypeSummary({ node }: { node: WorkerNode }) {
  const updater = node.service_type === "update_agent";
  return (
    <div className="min-w-0 max-w-64 space-y-1 text-sm">
      <Badge variant={updater ? "secondary" : "outline"}>{nodeTypeLabel(node.service_type)}</Badge>
      <div className="break-words text-xs text-muted-foreground">
        {updater ? "ホスト単位の更新専用。通常のNode通信とは別の管理経路です。" : node.description || "Nodeサービス"}
      </div>
    </div>
  );
}

export function NodeStatusSummary({ node }: { node: WorkerNode }) {
  return (
    <div className="min-w-0 max-w-48 space-y-1.5 text-sm">
      <StatusBadge status={node.health_status || node.status} showDetail />
      <div className="text-xs font-medium">{node.last_heartbeat_at ? "接続済み" : "接続待ち"}</div>
      <div className="text-xs text-muted-foreground">{node.configure_token_used_at ? "Configure済み" : "Configure未実行"}</div>
    </div>
  );
}

export function NodeReportSummary({ node }: { node: WorkerNode }) {
  if (node.service_type === "update_agent") {
    return (
      <div className="min-w-0 max-w-64 space-y-1 text-sm">
        <div className="font-medium">Host Agent報告</div>
        <div className="text-xs text-muted-foreground">{node.reported_version || node.version || "未報告"}</div>
        <div className="text-xs text-muted-foreground">更新実行状態はシステム情報で確認</div>
      </div>
    );
  }
  return (
    <div className="min-w-0 max-w-56 space-y-1 text-sm">
      <div>Version {node.reported_version || node.version || "未報告"}</div>
      <div className="text-xs text-muted-foreground">{nodeReportedPlatform(node)}</div>
      <NodeMetricsSummary node={node} />
    </div>
  );
}
