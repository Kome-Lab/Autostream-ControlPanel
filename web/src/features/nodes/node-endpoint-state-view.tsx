"use client";

import { Check, Link } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { nodeEndpointState, type NodeEndpointSnapshot } from "@/lib/node-registration";
import type { WorkerNode } from "@/types/domain";
import { nodeIdentity } from "./node-registration-model";

export function NodeEndpointStateView({
  node,
  compact = false,
  copied,
  onCopy,
}: {
  node: WorkerNode;
  compact?: boolean;
  copied: string;
  onCopy: (key: string, value?: string) => Promise<void>;
}) {
  const state = nodeEndpointState(node);
  if (node.service_type === "update_agent") {
    return <UpdaterTransportStateView node={node} state={state} compact={compact} />;
  }
  if (state.kind === "pull_v2") {
    return (
      <div
        className={compact
          ? "grid min-w-0 gap-1 text-xs"
          : "grid min-w-0 gap-2 rounded-md border bg-muted/40 p-3 text-sm"}
        role="group"
        aria-label="Host Pull Agent transport情報"
      >
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="secondary">transport_mode: {state.transportMode}</Badge>
          <span className="font-medium">受信ポートなし（Outbound HTTPS）</span>
        </div>
        <div className="break-all text-muted-foreground">
          execution_host_id: {state.executionHostID || "未報告"}
        </div>
        <div className="text-muted-foreground">
          ownership_epoch: {state.ownershipEpoch ?? "未報告"}
        </div>
      </div>
    );
  }

  const copyKey = `endpoint-${nodeIdentity(node)}-applied`;
  return (
    <div
      className={compact
        ? "grid min-w-0 max-w-96 gap-1.5 text-xs"
        : "grid min-w-0 gap-2 rounded-md border bg-muted/40 p-3 text-sm"}
      role="group"
      aria-label="Node endpoint migration状態"
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge
          variant={state.status.tone}
          title={state.status.detail}
          aria-label={`Endpoint状態: ${state.status.label}。${state.status.detail}`}
        >
          {state.status.label}
        </Badge>
        <span className="text-muted-foreground">
          Revision {state.revision ?? "未報告"}
        </span>
      </div>
      {!compact ? <div className="text-xs text-muted-foreground">{state.status.detail}</div> : null}
      <NodeEndpointSnapshotRow label="希望値" snapshot={state.desired} />
      <NodeEndpointSnapshotRow
        label={state.applied.source === "legacy" ? "反映済み (legacy)" : "反映済み"}
        snapshot={state.applied}
        copied={copied === copyKey}
        onCopy={state.applied.url ? () => onCopy(copyKey, state.applied.url) : undefined}
      />
      <NodeEndpointSnapshotRow label="Node報告" snapshot={state.reported} />
    </div>
  );
}

function UpdaterTransportStateView({
  node,
  state,
  compact = false,
}: {
  node: WorkerNode;
  state: ReturnType<typeof nodeEndpointState>;
  compact?: boolean;
}) {
  const transportMode = state.transportMode || "未報告";
  const isPull = transportMode === "pull_v2";
  const managementEndpoint = state.applied.url || state.desired.url || state.reported.url;
  return (
    <div
      className={compact
        ? "grid min-w-0 gap-1 text-xs"
        : "grid min-w-0 gap-2 rounded-md border bg-muted/40 p-3 text-sm"}
      role="group"
      aria-label={`${node.service_name || node.service_id || node.id} のUpdater transport情報`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">Updater / Host Agent</Badge>
        <span className="font-medium">
          {isPull ? "受信ポートなし（Outbound HTTPS）" : "非対応transport"}
        </span>
      </div>
      <div className="text-muted-foreground">transport_mode: {transportMode}</div>
      {isPull ? (
        <>
          <div className="break-all text-muted-foreground">execution_host_id: {state.executionHostID || "未報告"}</div>
          <div className="text-muted-foreground">ownership_epoch: {state.ownershipEpoch ?? "未報告"}</div>
        </>
      ) : (
        <div className="break-all text-muted-foreground">
          管理endpoint: {managementEndpoint || "未報告"}
        </div>
      )}
      {!compact ? <div className="text-xs text-muted-foreground">通常のNode endpointではなく、ホスト単位の更新経路です。</div> : null}
    </div>
  );
}

function NodeEndpointSnapshotRow({
  label,
  snapshot,
  copied = false,
  onCopy,
}: {
  label: string;
  snapshot: NodeEndpointSnapshot;
  copied?: boolean;
  onCopy?: () => Promise<void>;
}) {
  return (
    <div className="grid min-w-0 gap-0.5 sm:grid-cols-[7rem_minmax(0,1fr)_auto] sm:items-start sm:gap-2">
      <span className="font-medium">{label}</span>
      <span className={snapshot.url ? "break-all" : "text-muted-foreground"}>
        {snapshot.url || "未報告"}
      </span>
      {onCopy ? (
        <Button
          type="button"
          variant="outline"
          size="icon-sm"
          className="justify-self-start"
          aria-label={`${label} endpointをコピー`}
          onClick={() => void onCopy()}
        >
          {copied ? <Check className="size-4" /> : <Link className="size-4" />}
        </Button>
      ) : null}
    </div>
  );
}
