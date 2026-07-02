"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { apiPut } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";
import { useAppSettings } from "@/features/queries";
import type { AppSettings } from "@/types/domain";

export function SettingsView() {
  const { t } = useI18n();
  const appSettings = useAppSettings();

  return (
    <div className="space-y-6">
      <section>
        <h1 className="text-2xl font-semibold tracking-normal">{t("settings")}</h1>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">管理画面の表示名と運用設定を管理します。</p>
      </section>

      <Card>
        <CardHeader>
          <CardTitle>{t("appSettings")}</CardTitle>
          <CardDescription>サイドバー、ログイン、初期作成画面に表示される名前です。</CardDescription>
        </CardHeader>
        <CardContent className="max-w-xl space-y-4">
          {appSettings.isLoading ? (
            <Skeleton className="h-10 w-full" />
          ) : (
            <AppNameForm key={appSettings.data?.app_name || "default"} initialName={appSettings.data?.app_name || t("appName")} />
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function AppNameForm({ initialName }: { initialName: string }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [appName, setAppName] = useState(initialName);
  const [message, setMessage] = useState("");
  const saveAppSettings = useMutation({
    mutationFn: () => apiPut<AppSettings>("/settings/app", { app_name: appName }),
    onSuccess: async () => {
      setMessage("保存しました。");
      await queryClient.invalidateQueries({ queryKey: ["settings", "app"] });
    },
    onError: () => setMessage("保存に失敗しました。権限と入力内容を確認してください。"),
  });

  return (
    <>
      <div className="space-y-2">
        <label className="text-sm font-medium" htmlFor="app-name">
          {t("appNameLabel")}
        </label>
        <Input id="app-name" value={appName} onChange={(event) => setAppName(event.target.value)} maxLength={80} />
      </div>
      {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
      <Button onClick={() => saveAppSettings.mutate()} disabled={saveAppSettings.isPending || !appName.trim()}>
        <Save className="size-4" />
        {t("save")}
      </Button>
    </>
  );
}
