"use client";
import { useI18n } from "@/components/admin/i18n-provider";
import { resourceCopy } from "@/lib/i18n/ui-v2/resource-copy";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { RefreshCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { requiredPermissionText } from "./resource-permissions";

export function PermissionNotice({ resource, action, permission }: { resource: ResourceDefinition; action: string; permission?: string }) {
  const uiText = useUICopy();
  const { locale } = useI18n();
  const copy = resourceCopy(resource, locale);
  return (
    <Card>
      <CardHeader className="border-b bg-muted/20 py-3">
        <CardTitle className="text-base">{copy.title}</CardTitle>
        <CardDescription>{copy.description}</CardDescription>
      </CardHeader>
      <CardContent className="py-5">
        <p className="text-sm">{uiText("この項目を")}{action}{uiText("する権限がありません。")}</p>
        <p className="mt-1 text-sm text-muted-foreground">{requiredPermissionText(permission, uiText)}</p>
      </CardContent>
    </Card>
  );
}

export function QueryErrorNotice({ onRetry }: { onRetry: () => void }) {
  const uiText = useUICopy();
  return (
    <div role="alert" className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950">
      <div>
        <p className="font-medium">{uiText("データを取得できませんでした。")}</p>
        <p className="mt-1">{uiText("通信状態とログイン状態を確認して、もう一度お試しください。")}</p>
      </div>
      <Button variant="outline" size="sm" onClick={onRetry}>
        <RefreshCcw className="size-4" />
        {uiText("更新を再試行")}</Button>
    </div>
  );
}
