"use client";

import { type ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { nodeEndpointState } from "@/lib/node-registration";
import type { WorkerNode } from "@/types/domain";

export function InfoItem({ label, value, monospace = false }: { label: string; value: ReactNode; monospace?: boolean }) {
  return <div className="rounded-md border bg-muted/20 px-3 py-2"><div className="text-xs text-muted-foreground">{label}</div><div className={monospace ? "font-mono text-sm" : "text-sm"}>{value}</div></div>;
}

export function SectionLabel({ title, description }: { title: string; description: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <div className="text-sm font-medium">{title}</div>
      <div className="mt-0.5 text-xs text-muted-foreground">{description}</div>
    </div>
  );
}

export function ServiceEndpointSummary({ node }: { node: WorkerNode }) {
  const state = nodeEndpointState(node);
  if (node.service_type === "update_agent") {
    return <UpdaterTransportSummary node={node} state={state} />;
  }
  if (state.kind === "pull_v2") {
    return (
      <div className="space-y-1 text-xs">
        <div className="font-medium">Host Agent（受信ポートなし）</div>
        <div className="break-words text-muted-foreground">実行ホスト: {state.executionHostID || "未割り当て"} · Ownership epoch: {state.ownershipEpoch ?? 0}</div>
      </div>
    );
  }
  return (
    <div className="space-y-1.5 text-xs" aria-label={`${node.service_name || node.service_id || node.id} のendpoint状態`}>
      <EndpointStateLine label="希望endpoint（未適用を含む）" value={state.desired.url || "未設定"} />
      <EndpointStateLine label="現在適用中のendpoint" value={state.applied.url || "未報告"} effective />
      <EndpointStateLine label="Node報告endpoint" value={state.reported.url || "未報告"} />
      <div className="flex flex-wrap items-center gap-2 pt-1">
        <Badge variant={state.status.tone}>{state.status.label}</Badge>
        <span className="text-muted-foreground">Endpoint revision: {state.revision ?? "未報告"}</span>
      </div>
      <p className="text-muted-foreground">{state.status.detail}</p>
    </div>
  );
}

function UpdaterTransportSummary({
  node,
  state,
}: {
  node: WorkerNode;
  state: ReturnType<typeof nodeEndpointState>;
}) {
  const transportMode = state.transportMode || "未報告";
  const isPull = transportMode === "pull_v2";
  return (
    <div className="space-y-1.5 text-xs" aria-label={`${node.service_name || node.service_id || node.id} のUpdater transport状態`}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">Updater / Host Agent</Badge>
        <span className="font-medium">{isPull ? "受信ポートなし（Outbound HTTPS）" : "非対応transport"}</span>
      </div>
      <div className="text-muted-foreground">transport_mode: {transportMode}</div>
      <div className="break-words text-muted-foreground">実行ホスト: {isPull ? state.executionHostID || "未割り当て" : "使用不可"}</div>
      <div className="text-muted-foreground">Ownership epoch: {isPull ? state.ownershipEpoch ?? "未報告" : "使用不可"}</div>
      <div className="text-muted-foreground">通常のNode endpointとは別の更新管理経路</div>
    </div>
  );
}

function EndpointStateLine({ label, value, effective = false }: { label: string; value: string; effective?: boolean }) {
  return (
    <div>
      <div className="text-muted-foreground">{label}</div>
      <div className={effective ? "break-all font-medium text-foreground" : "break-all text-muted-foreground"}>{value}</div>
    </div>
  );
}
