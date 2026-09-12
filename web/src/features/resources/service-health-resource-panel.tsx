"use client";

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
  const appSettings = useAppSettings();
  const currentUser = useCurrentUser();
  const canReadRegisteredNodes = hasPermission(currentUser.data, "api_tokens.create");
  const registeredNodes = useNodes(access.read && canReadRegisteredNodes);
  const serviceHealth = useServiceHealth(access.read);
  const timezone = appSettings.data?.timezone;
  const rows = useMemo(
    () => mergeServiceHealthRows(registeredNodes.data || [], serviceHealth.data || []).map((row) => enrichResourceRow(resource, row as unknown as ResourceRow)),
    [registeredNodes.data, resource, serviceHealth.data],
  );
  const columns = useMemo(() => visibleColumns(rows, resource), [rows, resource]);
  const loading = rows.length === 0 && (serviceHealth.isLoading || (canReadRegisteredNodes && registeredNodes.isLoading));
  const fetching = serviceHealth.isFetching || (canReadRegisteredNodes && registeredNodes.isFetching);

  if (!access.read) return <PermissionNotice resource={resource} action="参照" permission={resource.permissions?.read} />;
  const queryError = serviceHealth.isError || (canReadRegisteredNodes && registeredNodes.isError);
  return (
    <Card>
      <CardHeader className="gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <CardTitle>{resource.title}</CardTitle>
          <CardDescription>{resource.description}</CardDescription>
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
          更新
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {queryError ? <QueryErrorNotice onRetry={() => { void registeredNodes.refetch(); void serviceHealth.refetch(); }} /> : loading ? <Skeleton className="h-48 w-full" /> : <ResourceTable rows={rows} columns={columns} resource={resource} timezone={timezone} canEdit={false} canDelete={false} canTest={false} currentUser={currentUser.data} />}
      </CardContent>
    </Card>
  );
}
