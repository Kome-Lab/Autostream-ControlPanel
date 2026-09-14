"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { Badge } from "@/components/ui/badge";
import { NodeStateDetails } from "./node-state-details";
import { useI18n } from "@/components/admin/i18n-provider";
import type { WorkerNode } from "@/types/domain";
import { nodeTypes } from "./node-registration-model";
import { nodeReportedPlatform, NodeMetricsSummary } from "./node-metrics-summary";

function nodeTypeLabel(type?: string) {
  if (type === "update_agent") return "Updater（Host Agent）";
  return nodeTypes.find((item) => item.value === type)?.label || type || "-";
}

export function NodeTypeSummary({ node }: { node: WorkerNode }) {
  const uiText = useUICopy();
  const updater = node.service_type === "update_agent";
  return (
    <div className="min-w-0 max-w-64 space-y-1 text-sm">
      <Badge variant={updater ? "secondary" : "outline"}>{nodeTypeLabel(node.service_type)}</Badge>
      <div className="break-words text-xs text-muted-foreground">
        {updater ? uiText("ホスト単位の更新専用。通常のNode通信とは別の管理経路です。") : node.description || uiText("Nodeサービス")}
      </div>
    </div>
  );
}

export function NodeStatusSummary({ node }: { node: WorkerNode }) {
  const uiText = useUICopy();
  const { locale } = useI18n();
  return (
    <div className="min-w-0 max-w-48 space-y-1.5 text-sm">
      <NodeStateDetails node={node} />
      <div className="text-xs font-medium">{locale === "ja" ? "Heartbeat報告: " : "Heartbeat received: "}{node.last_heartbeat_at || (locale === "ja" ? "未報告" : "Not reported")}</div>
      <div className="text-xs text-muted-foreground">{node.configure_token_used_at ? uiText("Configure済み") : uiText("Configure未実行")}</div>
    </div>
  );
}

export function NodeReportSummary({ node }: { node: WorkerNode }) {
  const uiText = useUICopy();
  if (node.service_type === "update_agent") {
    return (
      <div className="min-w-0 max-w-64 space-y-1 text-sm">
        <div className="font-medium">{uiText("Host Agent報告")}</div>
        <div className="text-xs text-muted-foreground">{node.reported_version || node.version || uiText("未報告")}</div>
        <div className="text-xs text-muted-foreground">{uiText("更新実行状態はシステム情報で確認")}</div>
      </div>
    );
  }
  return (
    <div className="min-w-0 max-w-56 space-y-1 text-sm">
      <div>Version {node.reported_version || node.version || uiText("未報告")}</div>
      <div className="text-xs text-muted-foreground">{nodeReportedPlatform(node, uiText)}</div>
      <NodeMetricsSummary node={node} />
    </div>
  );
}
