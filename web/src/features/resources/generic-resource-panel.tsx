"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, RefreshCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { apiGet, apiPost } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";
import { useAppSettings, useResourceData } from "@/features/queries";
import { createObservabilityActionController, findRefreshedObservabilityPlan, type ObservabilityActionExecutionResult, type ObservabilityActionPlan } from "@/features/observability/action-policy";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { createResourceActionController } from "@/features/resources/resource-action-controller";
import { mutateResourceAction, refreshResourceAction, resourceActionStateSnapshot, resourcePermissionSnapshot } from "@/features/resources/resource-action-runtime";
import { hasPermission } from "@/lib/auth/permissions";
import type { CurrentUser } from "@/types/domain";
import { type ResourceAccess, type ResourceRow } from "./resource-form-types";
import { resourceHistoryConfig, resourceActionResultMessage } from "./resource-action-feedback";
import { normalizeRows } from "./resource-values";
import { enrichResourceRow, visibleColumns } from "./resource-presentation";
import { observabilityActionSuccessMessage } from "./resource-observability-actions";
import { PermissionNotice, QueryErrorNotice } from "./resource-notices";
import { SecuritySettingsEditor } from "./resource-security-editor";
import { CreateResourceForm } from "./create-resource-form";
import { ResourceTable } from "./resource-table";

export function GenericResourcePanel({ resource, access, currentUser }: { resource: ResourceDefinition; access: ResourceAccess; currentUser: Parameters<typeof hasPermission>[0] }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const query = useResourceData<unknown>(resource.path, access.read);
  const appSettings = useAppSettings();
  const timezone = appSettings.data?.timezone;
  const historyConfig = useMemo(() => resourceHistoryConfig(resource.path), [resource.path]);
  const [historyState, setHistoryState] = useState<{ path: string; rows: ResourceRow[]; exhausted: boolean }>({ path: resource.path, rows: [], exhausted: false });
  const historyExhausted = historyState.path === resource.path && historyState.exhausted;
  const baseRows = useMemo(() => normalizeRows(query.data), [query.data]);
  const historyHasMore = Boolean(historyConfig) && !historyExhausted && baseRows.length >= (historyConfig?.initialLimit || 0);
  const rows = useMemo(() => {
    const olderHistoryRows = historyState.path === resource.path ? historyState.rows : [];
    const unique = new Map<string, ResourceRow>();
    for (const row of [...baseRows, ...olderHistoryRows]) {
      const id = typeof row.id === "string" ? row.id : JSON.stringify(row);
      if (!unique.has(id)) unique.set(id, row);
    }
    return [...unique.values()].map((row) => enrichResourceRow(resource, row));
  }, [baseRows, historyState.path, historyState.rows, resource]);
  const columns = useMemo(() => visibleColumns(rows, resource), [rows, resource]);
  const showTable = resource.form !== "security-settings";
  const [deleteMessage, setDeleteMessage] = useState("");
  const [actionMessage, setActionMessage] = useState("");
  const resourceActionController = useMemo(() => createResourceActionController({
    getPermissions: () => resourcePermissionSnapshot(queryClient),
    getState: (intent) => resourceActionStateSnapshot(queryClient, intent),
    refresh: (intent) => refreshResourceAction(queryClient, intent),
    mutate: mutateResourceAction,
  }), [queryClient]);
  const historyMutation = useMutation<ResourceRow[], Error, void>({
    mutationFn: async () => {
      if (!historyConfig || rows.length === 0) return [];
      const oldest = rows[rows.length - 1];
      const rawBefore = oldest[historyConfig.timestampField];
      const before = typeof rawBefore === "string" ? rawBefore : "";
      const beforeID = typeof oldest.id === "string" ? oldest.id : "";
      if (!before || !beforeID) throw new Error("history cursor is missing");
      const params = new URLSearchParams({ limit: String(historyConfig.pageSize), before, before_id: beforeID });
      const response = await apiGet<unknown>(`${resource.path}?${params.toString()}`);
      return normalizeRows(response);
    },
    onSuccess: (page) => {
      setHistoryState((current) => ({
        path: resource.path,
        rows: [...(current.path === resource.path ? current.rows : []), ...page],
        exhausted: page.length < (historyConfig?.pageSize || 200),
      }));
    },
    onError: () => setActionMessage("過去の履歴を取得できませんでした。通信状態を確認して再試行してください。"),
  });
  const observabilityController = useMemo(() => createObservabilityActionController({
    refresh: async (plan) => {
      const [data, freshUser] = await Promise.all([
        apiGet<unknown>(plan.sourcePath),
        apiGet<CurrentUser>("/auth/me"),
      ]);
      const freshPlan = findRefreshedObservabilityPlan(data, plan);
      if (!freshPlan) return { plan, evaluation: "unknown" as const, freshness: "fresh" as const };
      queryClient.setQueryData(["resource", plan.sourcePath], data);
      queryClient.setQueryData(["auth", "me"], freshUser);
      const allowed = hasPermission(freshUser, freshPlan.permission);
      return { plan: freshPlan, evaluation: allowed ? "allowed" as const : "denied" as const, freshness: "fresh" as const };
    },
    mutate: (plan) => apiPost(plan.path),
  }), [queryClient]);
  const handleObservabilityResult = async (plan: ObservabilityActionPlan, result: ObservabilityActionExecutionResult) => {
    const affectedResources = plan.path.includes("/diagnostics/rerun")
      ? [resource.path, "/observability/incidents", "/observability/diagnostics"]
      : [resource.path];
    if (result.kind === "succeeded") {
      setActionMessage(observabilityActionSuccessMessage(plan, result.value));
      await Promise.all([...new Set(affectedResources)].map((path) => queryClient.invalidateQueries({ queryKey: ["resource", path] })));
      return;
    }
    if (result.kind === "outcome_unknown") {
      setActionMessage("操作結果を確認できません。再送せず、最新状態または監査ログを確認してください。");
      await Promise.all([...new Set(affectedResources)].map((path) => queryClient.invalidateQueries({ queryKey: ["resource", path] })));
      return;
    }
    if (result.kind === "conflict") {
      setActionMessage("状態が更新されたため操作を再送しませんでした。最新状態を確認してください。");
      await Promise.all(["/observability/remediation-actions", "/observability/incidents", "/observability/diagnostics"].map((path) => queryClient.invalidateQueries({ queryKey: ["resource", path] })));
      return;
    }
    if (result.kind === "failed") {
      setActionMessage(t(result.error.messageKey));
      return;
    }
    setActionMessage("最新の権限または状態を確認できないため、操作を送信しませんでした。");
  };
  if (!access.read) return <PermissionNotice resource={resource} action="参照" permission={resource.permissions?.read} />;

  return (
    <Card>
      <CardHeader className="gap-2 border-b bg-muted/20 py-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <CardTitle>{resource.title}</CardTitle>
          <CardDescription>{resource.description}</CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" disabled={query.isFetching} onClick={() => {
            void query.refetch().then((result) => {
              if (result.isSuccess) resourceActionController.reconcile();
            });
          }}>
            <RefreshCcw className="size-4" />
            更新
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4 py-4">
        {query.isError ? <QueryErrorNotice onRetry={() => {
          void query.refetch().then((result) => {
            if (result.isSuccess) resourceActionController.reconcile();
          });
        }} /> : null}
        {resource.form === "security-settings" ? <SecuritySettingsEditor resource={resource} data={query.data} loading={query.isLoading} disabled={!access.update} controller={resourceActionController} /> : null}
        {resource.form && resource.form !== "security-settings" ? <CreateResourceForm resource={resource} allowed={access.create} permission={resource.permissions?.create} controller={resourceActionController} /> : null}
        {deleteMessage ? <p className="text-sm text-muted-foreground">{deleteMessage}</p> : null}
        {actionMessage ? <p className="text-sm text-muted-foreground">{actionMessage}</p> : null}
        {showTable ? (
          query.isLoading && rows.length === 0 ? (
            <Skeleton className="h-48 w-full" />
          ) : rows.length === 0 && query.isError ? null : (
            <ResourceTable
              rows={rows}
              columns={columns}
              resource={resource}
              timezone={timezone}
              canEdit={access.update}
              canDelete={access.delete}
              canTest={access.test}
              currentUser={currentUser}
              resourceActionController={resourceActionController}
              observabilityController={observabilityController}
              onObservabilityResult={(plan, result) => {
                setActionMessage("");
                return handleObservabilityResult(plan, result);
              }}
              onDeleteResult={(result) => {
                setDeleteMessage(resourceActionResultMessage(result, t, "削除しました。"));
                if (result.kind === "succeeded") {
                  void queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
                }
              }}
            />
          )
        ) : null}
        {showTable && historyConfig && rows.length > 0 && historyHasMore ? (
          <div className="flex justify-center border-t pt-4">
            <Button variant="outline" size="sm" disabled={historyMutation.isPending} onClick={() => historyMutation.mutate()}>
              {historyMutation.isPending ? <LoaderCircle className="size-4 animate-spin" /> : null}
              さらに過去の履歴を読み込む
            </Button>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
