"use client";

import { type ReactNode, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { apiPut } from "@/lib/api/client";
import { defaultTimeZone, timeZoneOptions } from "@/lib/timezone";
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
          <CardDescription>サイドバー、ログイン、初期作成画面の表示名と、画面上の時刻表示に使うタイムゾーンです。</CardDescription>
        </CardHeader>
        <CardContent className="max-w-xl space-y-4">
          {appSettings.isLoading ? (
            <Skeleton className="h-10 w-full" />
          ) : (
            <AppSettingsForm
              key={`${appSettings.data?.app_name || "default"}-${appSettings.data?.timezone || defaultTimeZone}-${appSettings.data?.smtp_enabled ? "smtp-on" : "smtp-off"}`}
              initialSettings={appSettings.data}
            />
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function AppSettingsForm({ initialSettings }: { initialSettings?: AppSettings }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [appName, setAppName] = useState(initialSettings?.app_name || t("appName"));
  const [timezone, setTimezone] = useState(initialSettings?.timezone || defaultTimeZone);
  const [smtpEnabled, setSMTPEnabled] = useState(Boolean(initialSettings?.smtp_enabled));
  const [smtpHost, setSMTPHost] = useState(initialSettings?.smtp_host || "");
  const [smtpPort, setSMTPPort] = useState(String(initialSettings?.smtp_port || 587));
  const [smtpStartTLS, setSMTPStartTLS] = useState(initialSettings?.smtp_starttls ?? true);
  const [smtpFrom, setSMTPFrom] = useState(initialSettings?.smtp_from || "");
  const [smtpUsername, setSMTPUsername] = useState(initialSettings?.smtp_username || "");
  const [smtpPassword, setSMTPPassword] = useState("");
  const [message, setMessage] = useState("");
  const options = timeZoneOptions.some((option) => option.value === timezone) ? timeZoneOptions : [{ value: timezone, label: timezone }, ...timeZoneOptions];
  const saveAppSettings = useMutation({
    mutationFn: () =>
      apiPut<AppSettings>("/settings/app", {
        app_name: appName,
        timezone,
        smtp_enabled: smtpEnabled,
        smtp_host: smtpHost,
        smtp_port: Number.parseInt(smtpPort, 10),
        smtp_starttls: smtpStartTLS,
        smtp_from: smtpFrom,
        smtp_username: smtpUsername,
        smtp_password: smtpPassword,
      }),
    onSuccess: async () => {
      setSMTPPassword("");
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
      <div className="space-y-2">
        <label className="text-sm font-medium" htmlFor="app-timezone">
          タイムゾーン
        </label>
        <Select value={timezone} onValueChange={setTimezone}>
          <SelectTrigger id="app-timezone">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-xs text-muted-foreground">Dashboard、Streams、Audit Logs、Accountの時刻表示に反映されます。</p>
      </div>
      <div className="space-y-3 rounded-md border bg-muted/20 p-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-sm font-medium">メールサーバー</div>
            <p className="text-xs text-muted-foreground">ユーザー登録完了などControl Panelから送るメールに使います。</p>
          </div>
          <Switch checked={smtpEnabled} onCheckedChange={setSMTPEnabled} />
        </div>
        {smtpEnabled ? (
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="SMTP Host">
              <Input value={smtpHost} onChange={(event) => setSMTPHost(event.target.value)} placeholder="smtp.example.jp" />
            </Field>
            <Field label="SMTP Port">
              <Input inputMode="numeric" value={smtpPort} onChange={(event) => setSMTPPort(event.target.value)} />
            </Field>
            <Field label="From">
              <Input type="email" value={smtpFrom} onChange={(event) => setSMTPFrom(event.target.value)} placeholder="autostream@example.jp" />
            </Field>
            <Field label="SMTP Username">
              <Input value={smtpUsername} onChange={(event) => setSMTPUsername(event.target.value)} />
            </Field>
            <Field label="SMTP Password">
              <Input type="password" value={smtpPassword} onChange={(event) => setSMTPPassword(event.target.value)} placeholder={initialSettings?.smtp_password_configured ? "設定済み" : ""} />
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={smtpStartTLS} onCheckedChange={setSMTPStartTLS} />
              STARTTLSを使用する
            </label>
          </div>
        ) : null}
      </div>
      {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
      <Button onClick={() => saveAppSettings.mutate()} disabled={saveAppSettings.isPending || !appName.trim() || (smtpEnabled && (!smtpHost.trim() || !smtpFrom.trim()))}>
        <Save className="size-4" />
        {t("save")}
      </Button>
    </>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="space-y-2 text-sm font-medium">
      <span>{label}</span>
      {children}
    </label>
  );
}
