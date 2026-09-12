"use client";

import { RefreshCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { requiredPermissionText } from "./resource-permissions";

export function PermissionNotice({ resource, action, permission }: { resource: ResourceDefinition; action: string; permission?: string }) {
  return (
    <Card>
      <CardHeader className="border-b bg-muted/20 py-3">
        <CardTitle className="text-base">{resource.title}</CardTitle>
        <CardDescription>{resource.description}</CardDescription>
      </CardHeader>
      <CardContent className="py-5">
        <p className="text-sm">この項目を{action}する権限がありません。</p>
        <p className="mt-1 text-sm text-muted-foreground">{requiredPermissionText(permission)}</p>
      </CardContent>
    </Card>
  );
}

export function QueryErrorNotice({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950">
      <div>
        <p className="font-medium">データを取得できませんでした。</p>
        <p className="mt-1">通信状態とログイン状態を確認して、もう一度お試しください。</p>
      </div>
      <Button variant="outline" size="sm" onClick={onRetry}>
        <RefreshCcw className="size-4" />
        更新を再試行
      </Button>
    </div>
  );
}
