"use client";
import { notificationFeedback } from "@/lib/i18n/ui-v2/presentation-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useState } from "react";
import { Check, Copy, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { ColumnDef } from "@tanstack/react-table";
import { DataTable } from "@/components/tables/data-table";
import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { presentNodeConnectivityStatus, presentNodeHealthStatus } from "@/lib/foundation/status/node-presenters";
import { presentIncidentStatus, presentDiagnosticStatus, presentRemediationStatus } from "@/lib/foundation/status/observability-presenters";
import { useI18n } from "@/components/admin/i18n-provider";
import { type ObservabilityActionController, type ObservabilityActionExecutionResult, type ObservabilityActionPlan } from "@/features/observability/action-policy";
import { ObservabilityActionControl } from "@/features/observability/observability-action-control";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { ResourceActionControl } from "@/features/resources/resource-action-control";
import { type ResourceActionController, type ResourceActionExecutionResult } from "@/features/resources/resource-action-controller";
import { type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";
import { hasPermission } from "@/lib/auth/permissions";
import { type NotificationChannelTestFeedback } from "@/lib/notification-channel";
import { type ResourceRow } from "./resource-form-types";
import { resourceHistoryConfig, resourceCanEdit, resourceActionResultMessage } from "./resource-action-feedback";
import { resourceRowID, resourceRowLabel } from "./resource-values";
import { EditResourceButton } from "./edit-resource-button";
import { OAuthAccountRelinkButton } from "./oauth-account-relink-button";
import { observabilityActionButtons } from "./resource-observability-actions";
import { DeleteResourceButton } from "./delete-resource-button";
import { columnLabel, formatResourceCell } from "./resource-presentation";

export function ResourceTable({
  rows,
  columns,
  resource,
  timezone,
  canEdit,
  canDelete,
  canTest,
  currentUser,
  resourceActionController,
  observabilityController,
  onObservabilityResult,
  onDeleteResult,
}: {
  rows: ResourceRow[];
  columns: string[];
  resource: ResourceDefinition;
  timezone?: string;
  canEdit: boolean;
  canDelete: boolean;
  canTest: boolean;
  currentUser: Parameters<typeof hasPermission>[0];
  resourceActionController?: ResourceActionController;
  observabilityController?: ObservabilityActionController;
  onObservabilityResult?: (plan: ObservabilityActionPlan, result: ObservabilityActionExecutionResult) => void | Promise<void>;
  onDeleteResult?: (result: ResourceActionExecutionResult, intent: ResourceActionIntent) => void;
}) {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const [copiedID, setCopiedID] = useState("");
  const [testNotice, setTestNotice] = useState<(NotificationChannelTestFeedback & { id: string; pending?: boolean }) | null>(null);
  if (rows.length === 0) {
    return <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">{uiText("データがありません。")}</div>;
  }
  const showDelete = Boolean(resource.deletable);
  const showEdit = resourceCanEdit(resource);
  const showTest = Boolean(resource.permissions?.test);
  const observabilityResource = resource.path === "/observability/incidents" || resource.path === "/observability/diagnostics" || resource.path === "/observability/remediation-actions";
  const showIDCopy = rows.some((row) => resourceRowID(row));
  const showActions = showDelete || showEdit || showTest || showIDCopy || observabilityResource;
  const copyRowID = async (id: string) => {
    if (!id || typeof navigator === "undefined" || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(id);
    } catch {
      return;
    }
    setCopiedID(id);
    window.setTimeout(() => setCopiedID((current) => (current === id ? "" : current)), 1500);
  };
  const rowActions = (row: ResourceRow) => (
    <>
      <div className="flex flex-wrap justify-start gap-1 xl:justify-end">
        {showIDCopy ? <CopyResourceIDButton id={resourceRowID(row)} copied={copiedID === resourceRowID(row)} onCopy={copyRowID} /> : null}
        {showEdit && resourceActionController ? <EditResourceButton resource={resource} row={row} disabled={!canEdit} controller={resourceActionController} /> : null}
        {resource.path === "/integrations/oauth-accounts" && resourceActionController ? <OAuthAccountRelinkButton row={row} disabled={!canEdit} controller={resourceActionController} /> : null}
        {showTest && resourceActionController ? (
          <ResourceActionControl
            controller={resourceActionController}
            intent={Object.freeze({ id: "RES-39", row: Object.freeze({ ...row }), publicLabel: resourceRowLabel(row, uiText) })}
            label={uiText("{0} へテスト送信", resourceRowLabel(row, uiText))}
            disabled={!resourceRowID(row) || !canTest}
            buttonProps={{ variant: "outline", size: "sm" }}
            onResult={(result, intent) => {
              const id = resourceRowID(intent.row as ResourceRow);
              if (result.kind === "succeeded") {
                setTestNotice({ id, ...notificationFeedback(result.value, uiText) });
                return;
              }
              setTestNotice({ id, ok: false, message: resourceActionResultMessage(result, t, uiText("テスト送信しました。")) });
            }}
          >
            <Send className="size-4" />
            {uiText("テスト送信")}</ResourceActionControl>
        ) : null}
        {observabilityController && onObservabilityResult ? observabilityActionButtons(resource, row, currentUser, uiText).map((action) => (
          <ObservabilityActionControl
            key={action.plan.key}
            controller={observabilityController}
            plan={action.plan}
            allowed={action.allowed}
            permissionText={action.permissionText}
            onResult={onObservabilityResult}
          />
        )) : null}
        {showDelete && resourceActionController && onDeleteResult ? <DeleteResourceButton resource={resource} row={row} controller={resourceActionController} disabled={!resourceRowID(row) || !canDelete} permission={resource.permissions?.delete} onResult={onDeleteResult} /> : null}
      </div>
      {testNotice?.id === resourceRowID(row) ? (
        <p role={testNotice.pending || testNotice.ok ? "status" : "alert"} className={`mt-2 text-left text-xs ${testNotice.pending ? "text-muted-foreground" : testNotice.ok ? "text-emerald-700 dark:text-emerald-300" : "text-destructive"}`}>
          {testNotice.message}
        </p>
      ) : null}
    </>
  );

  const statusPresenter = resource.path === "/observability/incidents" ? presentIncidentStatus
    : resource.path === "/observability/diagnostics" ? presentDiagnosticStatus
      : resource.path === "/observability/remediation-actions" ? presentRemediationStatus : undefined;
  // These are existing public display fields, not action targets or row IDs.
  const familyIdentity: Readonly<Record<string, string>> = {
    "/users": "username", "/service-health": "service_name",
    "/observability/incidents": "title", "/observability/diagnostics": "rule",
    "/observability/remediation-actions": "action", "/observability/notification-deliveries": "event_name",
    "/integrations/oauth-accounts": "oauth_account_display_name", "/secrets/status": "secret_label",
    "/stream-logs": "stream_name", "/audit-logs": "actor_username",
  };
  const identity = [familyIdentity[resource.path], "name", "username", "service_name", "id"].find((column) => column && columns.includes(column));
  const definitions: ColumnDef<ResourceRow>[] = columns.map((column, index) => ({
    id: column,
    accessorFn: (row) => typeof row[column] === "object" ? "" : row[column],
    header: locale === "ja" ? columnLabel(column, uiText) : column.replaceAll("_", " "),
    meta: {
      required: column === identity || index === 0 || ["name", "id", "status", "severity"].includes(column),
      priority: column === identity || ["name", "id", "status", "severity"].includes(column) ? 0 : ["updated_at", "created_at", "confidence", "evidence"].includes(column) ? 1 : 2,
    },
    cell: ({ row }) => resource.path === "/service-health" && (column === "status" || column === "health_status")
      ? <DomainStatusBadge presentation={(column === "status" ? presentNodeConnectivityStatus : presentNodeHealthStatus)(row.original[column])} translate={t} showDetail />
      : column === "status" && statusPresenter
      ? <DomainStatusBadge presentation={statusPresenter(row.original[column])} translate={t} showDetail />
      : column === identity && (row.original[column] === undefined || row.original[column] === null || row.original[column] === "")
      ? resourceRowLabel(row.original, uiText)
      : formatResourceCell(resource, row.original[column], column, timezone, uiText),
  }));
  if (!identity) definitions.unshift({
    id: "record-identity", header: uiText("名前"), meta: { required: true, priority: 0 },
    cell: ({ row }) => resourceRowLabel(row.original, uiText),
  });
  if (showActions) definitions.push({
    id: "actions", header: t("actions"), meta: { required: true, priority: 0 },
    cell: ({ row }) => rowActions(row.original),
  });
  return <DataTable columns={definitions} data={rows} getRowId={(row, index) => resourceRowID(row) || String(row.name ?? index)}
    mode={resourceHistoryConfig(resource.path) || resource.path.startsWith("/observability/") ? "server" : "client"}
    filterPlaceholder={locale === "ja" ? "取得済みデータを検索" : "Search loaded records"}
    density="compact" minTableWidthClass="min-w-[980px]" />;

}

function CopyResourceIDButton({ id, copied, onCopy }: { id: string; copied: boolean; onCopy: (id: string) => Promise<void> }) {
  const uiText = useUICopy();
  return (
    <Button variant="outline" size="icon-sm" disabled={!id} aria-label={uiText("IDをコピー")} onClick={() => void onCopy(id)}>
      {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
    </Button>
  );
}
