"use client";

import { useState } from "react";
import { Check, Copy, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useI18n } from "@/components/admin/i18n-provider";
import { type ObservabilityActionController, type ObservabilityActionExecutionResult, type ObservabilityActionPlan } from "@/features/observability/action-policy";
import { ObservabilityActionControl } from "@/features/observability/observability-action-control";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { ResourceActionControl } from "@/features/resources/resource-action-control";
import { type ResourceActionController, type ResourceActionExecutionResult } from "@/features/resources/resource-action-controller";
import { type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";
import { hasPermission } from "@/lib/auth/permissions";
import { notificationChannelTestFeedback, type NotificationChannelTestFeedback } from "@/lib/notification-channel";
import { type ResourceRow } from "./resource-form-types";
import { resourceCanEdit, resourceActionResultMessage } from "./resource-action-feedback";
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
  const { t } = useI18n();
  const [copiedID, setCopiedID] = useState("");
  const [testNotice, setTestNotice] = useState<(NotificationChannelTestFeedback & { id: string; pending?: boolean }) | null>(null);
  if (rows.length === 0) {
    return <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">データがありません。</div>;
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
            intent={Object.freeze({ id: "RES-39", row: Object.freeze({ ...row }), publicLabel: resourceRowLabel(row) })}
            label={`${resourceRowLabel(row)} へテスト送信`}
            disabled={!resourceRowID(row) || !canTest}
            buttonProps={{ variant: "outline", size: "sm" }}
            onResult={(result, intent) => {
              const id = resourceRowID(intent.row as ResourceRow);
              if (result.kind === "succeeded") {
                setTestNotice({ id, ...notificationChannelTestFeedback(result.value) });
                return;
              }
              setTestNotice({ id, ok: false, message: resourceActionResultMessage(result, t, "テスト送信しました。") });
            }}
          >
            <Send className="size-4" />
            テスト送信
          </ResourceActionControl>
        ) : null}
        {observabilityController && onObservabilityResult ? observabilityActionButtons(resource, row, currentUser).map((action) => (
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

  return (
    <div className="space-y-3">
      <div className="hidden overflow-hidden rounded-md border xl:block">
      <Table className="w-full table-fixed">
        <TableHeader>
          <TableRow>
            {columns.map((column) => (
              <TableHead key={column} className="whitespace-normal">{columnLabel(column)}</TableHead>
            ))}
            {showActions ? <TableHead className="w-56 whitespace-normal text-right">操作</TableHead> : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, index) => (
            <TableRow key={String(row.id || row.name || index)}>
              {columns.map((column) => (
                <TableCell key={column} className="whitespace-normal break-words align-top">
                  {formatResourceCell(resource, row[column], column, timezone)}
                </TableCell>
              ))}
              {showActions ? (
                <TableCell className="w-56 text-right">
                  {rowActions(row)}
                </TableCell>
              ) : null}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      </div>
      <div className="grid gap-3 xl:hidden">
        {rows.map((row, index) => (
          <article key={String(row.id || row.name || index)} className="rounded-md border bg-card p-4 shadow-sm">
            <div className="min-w-0">
              <h3 className="break-words text-sm font-semibold">{resourceRowLabel(row)}</h3>
              {resourceRowID(row) ? <p className="mt-1 break-all font-mono text-xs text-muted-foreground">{resourceRowID(row)}</p> : null}
            </div>
            <dl className="mt-4 grid grid-cols-1 gap-x-5 gap-y-3 sm:grid-cols-2">
              {columns.map((column) => (
                <div key={column} className="min-w-0 space-y-1">
                  <dt className="text-xs font-medium text-muted-foreground">{columnLabel(column)}</dt>
                  <dd className="min-w-0 break-words text-sm">{formatResourceCell(resource, row[column], column, timezone)}</dd>
                </div>
              ))}
            </dl>
            {showActions ? <div className="mt-4 border-t pt-3">{rowActions(row)}</div> : null}
          </article>
        ))}
      </div>
    </div>
  );
}

function CopyResourceIDButton({ id, copied, onCopy }: { id: string; copied: boolean; onCopy: (id: string) => Promise<void> }) {
  return (
    <Button variant="outline" size="icon-sm" disabled={!id} aria-label="IDをコピー" onClick={() => void onCopy(id)}>
      {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
    </Button>
  );
}
