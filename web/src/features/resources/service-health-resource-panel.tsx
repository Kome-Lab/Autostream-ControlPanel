"use client";
import { useI18n } from "@/components/admin/i18n-provider";
import { resourceCopy } from "@/lib/i18n/ui-v2/resource-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useMemo } from "react";
import { RefreshCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useAppSettings, useCurrentUser, useNodes, useServiceHealth } from "@/features/queries";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { hasPermission } from "@/lib/auth/permissions";
import { type ResourceAccess, type ResourceRow } from "./resource-form-types";
import { mergeServiceHealthRows } from "./service-health-resource-model";
import { enrichResourceRow, visibleColumns } from "./resource-presentation";
import { PermissionNotice, QueryErrorNotice } from "./resource-notices";
import { ResourceTable } from "./resource-table";

export function ServiceHealthResourcePanel({ resource, access }: { resource: ResourceDefinition; access: ResourceAccess }) {
  const uiText = useUICopy();
  const { locale } = useI18n();
  const copy = resourceCopy(resource, locale);
  const appSettings = useAppSettings();
  const currentUser = useCurrentUser();
  const canReadRegisteredNodes = hasPermission(currentUser.data, "api_tokens.create");
  const registeredNodes = useNodes(access.read && canReadRegisteredNodes);
  const serviceHealth = useServiceHealth(access.read);
  const timezone = appSettings.data?.timezone;
  const rows = useMemo(
    () => mergeServiceHealthRows(registeredNodes.data || [], serviceHealth.data || []).map((row) => enrichResourceRow(resource, row as unknown as ResourceRow, uiText)),
    [registeredNodes.data, resource, serviceHealth.data, uiText],
  );
  const columns = useMemo(() => visibleColumns(rows, resource), [rows, resource]);
  const received = serviceHealth.data !== undefined || (canReadRegisteredNodes && registeredNodes.data !== undefined);
  const loading = !received && (serviceHealth.isLoading || (canReadRegisteredNodes && registeredNodes.isLoading));
  const fetching = serviceHealth.isFetching || (canReadRegisteredNodes && registeredNodes.isFetching);

  if (!access.read) return <PermissionNotice resource={resource} action={uiText("参照")} permission={resource.permissions?.read} />;
  const queryError = serviceHealth.isError || (canReadRegisteredNodes && registeredNodes.isError);
  const partial = queryError && received && (serviceHealth.data === undefined || (canReadRegisteredNodes && registeredNodes.data === undefined));
  return (
    <Card>
      <CardHeader className="gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <CardTitle>{copy.title}</CardTitle>
          <CardDescription>{copy.description}</CardDescription>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={fetching}
          onClick={() => {
            void registeredNodes.refetch();
            void serviceHealth.refetch();
          }}
        >
          <RefreshCcw className="size-4" />
          {locale === "ja" ? "更新" : "Refresh"}</Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {queryError ? <div data-remote-state={partial ? "partial" : received ? "ready" : "error"} data-remote-freshness={received ? "stale" : undefined}>
          <QueryErrorNotice onRetry={() => { void registeredNodes.refetch(); void serviceHealth.refetch(); }} />
          {received ? <p role="status">{partial ? uiText("一部の状態を取得できません。取得済みのデータを表示しています。") : uiText("更新に失敗しました。取得済みのデータを表示しています。")}</p> : null}
        </div> : null}
        {loading ? <div role="status" data-remote-state="loading"><p>{uiText("サービスの状態を読み込み中です。")}</p><Skeleton className="h-48 w-full" /></div> : null}
        {fetching && received ? <p role="status" data-remote-freshness="refreshing">{uiText("取得済みデータを表示しながら更新中です。")}</p> : null}
        {received ? <ResourceTable rows={rows} columns={columns} resource={resource} timezone={timezone} canEdit={false} canDelete={false} canTest={false} currentUser={currentUser.data} /> : null}
      </CardContent>
    </Card>
  );
}
