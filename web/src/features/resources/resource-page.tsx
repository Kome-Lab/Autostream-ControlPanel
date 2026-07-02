"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCcw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { apiPost } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";
import { useResourceData } from "@/features/queries";
import { resourcePages, type ResourceDefinition, type ResourcePageId } from "@/features/resources/resource-config";

export function ResourcePage({ pageId }: { pageId: ResourcePageId }) {
  const { t } = useI18n();
  const page = resourcePages[pageId];
  const defaultTab = page.resources[0]?.path || "";

  return (
    <div className="space-y-6">
      <section>
        <h1 className="text-2xl font-semibold tracking-normal">{t(page.titleKey)}</h1>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">{page.description}</p>
      </section>

      {page.resources.length === 1 ? (
        <ResourcePanel resource={page.resources[0]} />
      ) : (
        <Tabs defaultValue={defaultTab} className="space-y-4">
          <TabsList className="max-w-full flex-wrap justify-start">
            {page.resources.map((resource) => (
              <TabsTrigger key={resource.path} value={resource.path}>
                {resource.title}
              </TabsTrigger>
            ))}
          </TabsList>
          {page.resources.map((resource) => (
            <TabsContent key={resource.path} value={resource.path}>
              <ResourcePanel resource={resource} />
            </TabsContent>
          ))}
        </Tabs>
      )}
    </div>
  );
}

function ResourcePanel({ resource }: { resource: ResourceDefinition }) {
  const query = useResourceData<unknown>(resource.path);
  const rows = useMemo(() => normalizeRows(query.data), [query.data]);
  const columns = useMemo(() => visibleColumns(rows), [rows]);

  return (
    <Card>
      <CardHeader className="gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <CardTitle>{resource.title}</CardTitle>
          <CardDescription>{resource.description}</CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant="outline">{resource.path}</Badge>
          <Button variant="outline" size="sm" onClick={() => query.refetch()}>
            <RefreshCcw className="size-4" />
            更新
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {resource.createTemplate ? <CreateResourceForm resource={resource} /> : null}
        {query.isLoading ? <Skeleton className="h-48 w-full" /> : <ResourceTable rows={rows} columns={columns} />}
      </CardContent>
    </Card>
  );
}

function CreateResourceForm({ resource }: { resource: ResourceDefinition }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState(() => JSON.stringify(resource.createTemplate, null, 2));
  const [message, setMessage] = useState("");
  const mutation = useMutation({
    mutationFn: async () => apiPost(resource.path, JSON.parse(body)),
    onSuccess: async () => {
      setMessage("作成しました。");
      await queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
    },
    onError: () => setMessage("作成に失敗しました。入力内容と権限を確認してください。"),
  });

  return (
    <div className="rounded-md border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="font-medium">新規作成</div>
          <p className="text-sm text-muted-foreground">既存APIに送るJSONを確認して作成します。</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setOpen((value) => !value)}>
          <Plus className="size-4" />
          {open ? "閉じる" : "開く"}
        </Button>
      </div>
      {open ? (
        <div className="mt-3 space-y-3">
          <Textarea value={body} onChange={(event) => setBody(event.target.value)} className="min-h-40 font-mono text-xs" spellCheck={false} />
          {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
          <Button size="sm" onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            作成
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function ResourceTable({ rows, columns }: { rows: Record<string, unknown>[]; columns: string[] }) {
  if (rows.length === 0) {
    return <div className="rounded-md border border-dashed p-6 text-sm text-muted-foreground">データがありません。</div>;
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          {columns.map((column) => (
            <TableHead key={column}>{column}</TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row, index) => (
          <TableRow key={String(row.id || row.name || index)}>
            {columns.map((column) => (
              <TableCell key={column} className="max-w-[280px] overflow-hidden text-ellipsis">
                {formatCell(row[column])}
              </TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function normalizeRows(data: unknown): Record<string, unknown>[] {
  if (!data) return [];
  if (Array.isArray(data)) return data.map((item) => normalizeRow(item));
  if (isRecord(data)) {
    for (const key of ["items", "data", "results"]) {
      const value = data[key];
      if (Array.isArray(value)) return value.map((item) => normalizeRow(item));
    }
    return Object.entries(data).map(([key, value]) => ({ name: key, value }));
  }
  return [{ value: data }];
}

function normalizeRow(item: unknown): Record<string, unknown> {
  if (isRecord(item)) {
    const row: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(item)) row[key] = value;
    return row;
  }
  return { value: item };
}

function visibleColumns(rows: Record<string, unknown>[]) {
  const preferred = ["id", "name", "username", "service_id", "service_name", "service_type", "type", "status", "health_status", "title", "action", "target", "updated_at", "created_at"];
  const seen = new Set<string>();
  for (const key of preferred) {
    if (rows.some((row) => row[key] !== undefined)) seen.add(key);
  }
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      if (seen.size >= 8) break;
      seen.add(key);
    }
    if (seen.size >= 8) break;
  }
  return [...seen];
}

function formatCell(value: unknown) {
  if (value === null || value === undefined || value === "") return "-";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "string" || typeof value === "number") return String(value);
  return JSON.stringify(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
