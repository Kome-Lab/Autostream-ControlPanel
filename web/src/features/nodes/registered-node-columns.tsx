
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";
import type { ColumnDef } from "@tanstack/react-table";
import { Check, Copy, FileCode2, KeyRound, Pencil, RotateCw, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DeferredNodeConfirmation as DangerConfirm } from "@/features/nodes/deferred-node-confirmation";
import { RoleGuard, guardedButtonProps } from "@/components/admin/role-guard";
import { useI18n } from "@/components/admin/i18n-provider";
import { canRegenerateNodeConfigureToken, canRotateNodeRuntimeToken } from "@/lib/node-configuration";
import type { WorkerNode } from "@/types/domain";
import { nodeIdentity, nodeDisplayName } from "./node-registration-model";
import { NodeTypeSummary, NodeStatusSummary, NodeReportSummary } from "./node-status-summary";
import { NodeEndpointStateView } from "./node-endpoint-state-view";
import { formatHeartbeat } from "./node-metrics-summary";
import { useNodeRegistrationMutations } from "./use-node-registration-mutations";



export function createRegisteredNodeColumns({
  t,
  copyValue,
  copied,
  timezone,
  allowed,
  canRevokeRuntimeToken,
  canResolveRuntimeSecrets,
  canExecuteSystemUpdates,
  canDeleteNode,
  actions: { loadConfiguration, regenerateConfigureToken, rotateRuntimeToken, deleteNode },
  openEditNode,
}: {
  t: ReturnType<typeof useI18n>["t"];
  copyValue: (key: string, value?: string) => Promise<void>;
  copied: string;
  timezone: string | undefined;
  allowed: boolean;
  canRevokeRuntimeToken: boolean;
  canResolveRuntimeSecrets: boolean;
  canExecuteSystemUpdates: boolean;
  canDeleteNode: boolean;
  actions: Pick<ReturnType<typeof useNodeRegistrationMutations>, "loadConfiguration" | "regenerateConfigureToken" | "rotateRuntimeToken" | "deleteNode">;
  openEditNode: (node: WorkerNode) => void
}, uiText: UICopy = japaneseCopy) {

  const registeredColumns: ColumnDef<WorkerNode>[] = [
    {
      accessorKey: "service_name",
      meta: { required: true, priority: 0 },
      header: t("name"),
      cell: ({ row }) => {
        const nodeID = nodeIdentity(row.original);
        return (
          <div className="min-w-0">
            <div className="flex items-start gap-2">
              <div className="min-w-0">
                <div className="break-words font-medium">{nodeDisplayName(row.original)}</div>
                <div className="mt-1 break-all font-mono text-xs text-muted-foreground">{nodeID}</div>
              </div>
              <Button variant="outline" size="icon-sm" aria-label={uiText("Node IDをコピー")} title={uiText("Node IDをコピー")} onClick={() => copyValue(`node-id-${nodeID}`, nodeID)}>
                {copied === `node-id-${nodeID}` ? <Check className="size-4" /> : <Copy className="size-4" />}
              </Button>
            </div>
          </div>
        );
      },
    },
    {
      accessorKey: "service_type",
      header: t("nodeType"),
      cell: ({ row }) => <NodeTypeSummary node={row.original} />,
    },
    {
      id: "endpoint",
      meta: { priority: 2 },
      header: "Endpoint",
      cell: ({ row }) => (
        <NodeEndpointStateView
          node={row.original}
          compact
          copied={copied}
          onCopy={copyValue}
        />
      ),
    },
    {
      id: "status",
      header: uiText("状態 / 登録"),
      cell: ({ row }) => <NodeStatusSummary node={row.original} />,
    },
    {
      id: "reported",
      meta: { priority: 2 },
      header: uiText("報告 / 負荷"),
      cell: ({ row }) => <NodeReportSummary node={row.original} />,
    },
    {
      id: "heartbeat",
      header: uiText("最終Heartbeat"),
      cell: ({ row }) => <span className="whitespace-nowrap text-sm">{formatHeartbeat(row.original, timezone)}</span>,
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => {
        const node = row.original;
        const nodeID = nodeIdentity(node);
        const nodeConfigurationIncludesSigningKey = node.service_type === "worker" || node.service_type === "encoder_recorder";
        const tokenPermissions = {
          serviceType: node.service_type,
          canCreateTokens: allowed,
          canRevokeTokens: canRevokeRuntimeToken,
          canResolveManagedSecret: canResolveRuntimeSecrets,
          requiresManagedSecret: nodeConfigurationIncludesSigningKey,
          canExecuteSystemUpdates,
        };
        const canManageNodeTokens = canRotateNodeRuntimeToken(tokenPermissions);
        const canRegenerateConfigureToken = canRegenerateNodeConfigureToken(tokenPermissions);
        const configureTokenPermissionMessage = !allowed
          ? uiText("Configure Token再生成には api_tokens.create 権限が必要です。")
          : !canRevokeRuntimeToken
            ? uiText("Configure Token再生成には api_tokens.revoke 権限が必要です。")
            : nodeConfigurationIncludesSigningKey && !canResolveRuntimeSecrets
              ? uiText("Worker / EncoderのConfigure Token再生成には secrets.update 権限が必要です。")
              : node.service_type === "update_agent" && !canResolveRuntimeSecrets
                ? uiText("UpdaterのConfigure Token再生成には secrets.update 権限が必要です。")
                : node.service_type === "update_agent" && !canExecuteSystemUpdates
                  ? uiText("UpdaterのConfigure Token再生成には system_updates.execute 権限が必要です。")
                  : uiText("Configure Tokenを再生成する権限がありません。");
        const runtimeTokenPermissionMessage = node.service_type === "update_agent" && !canResolveRuntimeSecrets
          ? uiText("UpdaterのRuntime Token再生成には secrets.update 権限が必要です。")
          : node.service_type === "update_agent" && !canExecuteSystemUpdates
            ? uiText("UpdaterのRuntime Token再生成には system_updates.execute 権限が必要です。")
            : uiText("Runtime Token再生成には api_tokens.create と api_tokens.revoke 権限が必要です。");
        return (
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <Button variant="outline" size="sm" className="px-2" aria-label={uiText("Configurationを表示")} title={uiText("Configurationを表示")} onClick={() => loadConfiguration.mutate(nodeID)} disabled={loadConfiguration.isPending}>
              <FileCode2 />
              <span>{uiText("設定")}</span>
            </Button>
            <RoleGuard allowed={canRegenerateConfigureToken} message={configureTokenPermissionMessage}>
              <Button variant="outline" size="sm" className="px-2" aria-label={uiText("Configure Tokenを再生成")} title={uiText("Configure Tokenを再生成")} onClick={() => regenerateConfigureToken.mutate(nodeID)} {...guardedButtonProps(canRegenerateConfigureToken)} disabled={!canRegenerateConfigureToken || regenerateConfigureToken.isPending}>
                <KeyRound />
                <span>{uiText("初期化")}</span>
              </Button>
            </RoleGuard>
            <RoleGuard allowed={canManageNodeTokens} message={runtimeTokenPermissionMessage}>
              <DangerConfirm
                title={uiText("{0} のRuntime Tokenを再生成しますか", node.service_name)}
                description={node.service_type === "update_agent"
                  ? uiText("既存のRuntime Tokenは無効になります。通常はこの操作ではなくConfigure Tokenを再生成し、表示された手順をこのHost Agentを稼働させる対象ホストで実行してください。")
                  : uiText("既存のRuntime Tokenは無効になります。Node Agentへ新しいconfig.ymlまたはTokenを反映してください。")}
                onConfirm={() => rotateRuntimeToken.mutate(nodeID)}
                actionLabel={uiText("再生成")}
              >
                <Button variant="outline" size="sm" className="px-2" aria-label={uiText("Runtime Tokenを再生成")} title={uiText("Runtime Tokenを再生成")} {...guardedButtonProps(canManageNodeTokens)} disabled={!canManageNodeTokens || rotateRuntimeToken.isPending}>
                  <RotateCw />
                  <span>Token</span>
                </Button>
              </DangerConfirm>
            </RoleGuard>
            <Button variant="outline" size="sm" className="px-2" aria-label={uiText("Nodeを編集")} title={uiText("Nodeを編集")} onClick={() => openEditNode(node)} disabled={!allowed}>
              <Pencil />
              <span>{uiText("編集")}</span>
            </Button>
            <RoleGuard allowed={canDeleteNode}>
              <DangerConfirm title={uiText("{0} を削除しますか", node.service_name)} description={uiText("Node登録、割り当て、Runtime Tokenを無効化します。この操作は取り消せません。")} onConfirm={() => deleteNode.mutate(nodeID)} actionLabel={uiText("削除")}>
                <Button variant="destructive" size="sm" className="px-2" aria-label={uiText("Nodeを削除")} title={uiText("Nodeを削除")} {...guardedButtonProps(canDeleteNode)} disabled={!canDeleteNode || deleteNode.isPending}>
                  <Trash2 />
                  <span>{uiText("削除")}</span>
                </Button>
              </DangerConfirm>
            </RoleGuard>
          </div>
        );
      },
    },
  ];
  return registeredColumns;
}
