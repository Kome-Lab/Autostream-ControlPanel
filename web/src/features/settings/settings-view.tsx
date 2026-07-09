"use client";

import { type ReactNode, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Save, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { APIError, apiPost, apiPut } from "@/lib/api/client";
import { defaultTimeZone, timeZoneOptions } from "@/lib/timezone";
import { useI18n } from "@/components/admin/i18n-provider";
import { useAppSettings, useCurrentUser } from "@/features/queries";
import type { AppSettings } from "@/types/domain";

type TestEmailResponse = {
  status: string;
  target?: string;
};

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
              key={`${appSettings.data?.app_name || "default"}-${appSettings.data?.timezone || defaultTimeZone}-${appSettings.data?.smtp_enabled ? "smtp-on" : "smtp-off"}-${appSettings.data?.turnstile_enabled ? "turnstile-on" : "turnstile-off"}`}
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
  const currentUser = useCurrentUser();
  const currentUserEmail = currentUser.data?.user.email || "";
  const [appName, setAppName] = useState(initialSettings?.app_name || t("appName"));
  const [timezone, setTimezone] = useState(initialSettings?.timezone || defaultTimeZone);
  const [smtpEnabled, setSMTPEnabled] = useState(Boolean(initialSettings?.smtp_enabled));
  const [smtpHost, setSMTPHost] = useState(initialSettings?.smtp_host || "");
  const [smtpPort, setSMTPPort] = useState(String(initialSettings?.smtp_port || 587));
  const [smtpStartTLS, setSMTPStartTLS] = useState(initialSettings?.smtp_starttls ?? true);
  const [smtpFrom, setSMTPFrom] = useState(initialSettings?.smtp_from || "");
  const [smtpUsername, setSMTPUsername] = useState(initialSettings?.smtp_username || "");
  const [smtpPassword, setSMTPPassword] = useState("");
  const [turnstileEnabled, setTurnstileEnabled] = useState(Boolean(initialSettings?.turnstile_enabled));
  const [turnstileSiteKey, setTurnstileSiteKey] = useState(initialSettings?.turnstile_site_key || "");
  const [turnstileSecret, setTurnstileSecret] = useState("");
  const [testEmailOverride, setTestEmailOverride] = useState<string | null>(null);
  const [message, setMessage] = useState("");
  const options = timeZoneOptions.some((option) => option.value === timezone) ? timeZoneOptions : [{ value: timezone, label: timezone }, ...timeZoneOptions];
  const testEmailTo = testEmailOverride ?? currentUserEmail;
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
        turnstile_enabled: turnstileEnabled,
        turnstile_site_key: turnstileSiteKey,
        turnstile_secret: turnstileSecret,
      }),
    onSuccess: async () => {
      setSMTPPassword("");
      setTurnstileSecret("");
      setMessage("保存しました。");
      await queryClient.invalidateQueries({ queryKey: ["settings", "app"] });
    },
    onError: () => setMessage("保存に失敗しました。権限と入力内容を確認してください。"),
  });
  const testEmail = useMutation({
    mutationFn: () => apiPost<TestEmailResponse>("/settings/app/test-email", { to: testEmailTo.trim() }),
    onMutate: () => setMessage("テストメールを送信しています。"),
    onSuccess: (response) => setMessage(response.target ? `テストメールを送信しました。宛先: ${response.target}` : "テストメールを送信しました。"),
    onError: (error) => setMessage(testEmailErrorMessage(error)),
  });
  const smtpRequiredMissing = smtpEnabled && (!smtpHost.trim() || !smtpFrom.trim());
  const turnstileRequiredMissing = turnstileEnabled && (!turnstileSiteKey.trim() || (!turnstileSecret.trim() && !initialSettings?.turnstile_configured));

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
        <p className="text-xs text-muted-foreground">Streams、Audit Logs、Accountの時刻表示に反映されます。</p>
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
            <Field label="テスト送信先">
              <Input
                type="email"
                value={testEmailTo}
                onChange={(event) => {
                  setTestEmailOverride(event.target.value);
                }}
                placeholder="ops@example.jp"
              />
            </Field>
            <div className="flex items-end">
              <Button
                type="button"
                variant="outline"
                onClick={() => testEmail.mutate()}
                disabled={testEmail.isPending || saveAppSettings.isPending || !smtpEnabled || smtpRequiredMissing || !testEmailTo.trim()}
              >
                <Send className="size-4" />
                テスト送信
              </Button>
            </div>
          </div>
        ) : null}
      </div>
      <div className="space-y-3 rounded-md border bg-muted/20 p-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-sm font-medium">Cloudflare Turnstile</div>
            <p className="text-xs text-muted-foreground">ログインとメール変更確認のBOT確認に使います。</p>
          </div>
          <Switch checked={turnstileEnabled} onCheckedChange={setTurnstileEnabled} />
        </div>
        {turnstileEnabled ? (
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="Site key">
              <Input value={turnstileSiteKey} onChange={(event) => setTurnstileSiteKey(event.target.value)} placeholder="0x4AAAA..." />
            </Field>
            <Field label="Secret key">
              <Input type="password" value={turnstileSecret} onChange={(event) => setTurnstileSecret(event.target.value)} placeholder={initialSettings?.turnstile_configured ? "設定済み" : ""} />
            </Field>
          </div>
        ) : null}
      </div>
      {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
      <Button onClick={() => saveAppSettings.mutate()} disabled={saveAppSettings.isPending || !appName.trim() || smtpRequiredMissing || turnstileRequiredMissing}>
        <Save className="size-4" />
        {t("save")}
      </Button>
    </>
  );
}

function testEmailErrorMessage(error: unknown) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      invalid_email_recipient: "テスト送信先のメールアドレスを確認してください。",
      smtp_not_configured: "メールサーバー設定を保存してからテスト送信してください。",
      smtp_requires_tls: "外部SMTPではSTARTTLSを有効にしてください。",
      secret_encryption_key_required: "SMTPパスワードを読み出せません。Secret encryption keyを設定してください。",
      smtp_dial_failed: "SMTPサーバーへ接続できません。ホスト名、ポート、ファイアウォール、DNSを確認してください。",
      smtp_starttls_failed: "STARTTLSに失敗しました。ポート番号、STARTTLS設定、証明書設定を確認してください。",
      smtp_auth_failed: "SMTP認証に失敗しました。ユーザー名、パスワード、SMTP認証方式を確認してください。",
      smtp_from_rejected: "SMTPサーバーに送信元アドレスが拒否されました。Fromアドレスと送信許可設定を確認してください。",
      smtp_recipient_rejected: "SMTPサーバーにテスト送信先が拒否されました。送信先アドレスとリレー許可設定を確認してください。",
      smtp_data_failed: "SMTPサーバーがメール本文の送信開始を拒否しました。サーバーの制限や認証設定を確認してください。",
      smtp_write_failed: "SMTPサーバーへのメール本文送信中に失敗しました。通信経路とサーバーログを確認してください。",
      smtp_close_failed: "SMTPサーバーへのメール送信完了処理に失敗しました。サーバーログを確認してください。",
      send_failed: "テストメール送信に失敗しました。SMTPサーバー設定と到達性を確認してください。",
      unauthorized: "ログイン状態が切れています。再ログインしてからテスト送信してください。",
      csrf_failed: "セキュリティトークンを確認できませんでした。ページを再読み込みしてからテスト送信してください。",
      permission_denied: "メールサーバー設定を更新する権限がありません。",
      password_change_required: "パスワード変更が必要な状態です。パスワード変更後にテスト送信してください。",
    };
    return messages[error.code || ""] || testEmailStatusErrorMessage(error.status, error.code || error.message);
  }
  if (error instanceof Error) return `テストメール送信に失敗しました。${error.message}`;
  return "テストメール送信に失敗しました。";
}

function testEmailStatusErrorMessage(status: number, detail: string) {
  switch (status) {
    case 401:
      return "ログイン状態が切れています。再ログインしてからテスト送信してください。";
    case 403:
      return "テストメール送信が拒否されました。権限を確認し、ページを再読み込みしてから再実行してください。";
    case 404:
      return "テストメール送信APIが見つかりません。デプロイ済みControl Panelが最新か確認してください。";
    case 409:
      return "メールサーバー設定が未完了です。設定を保存してからテスト送信してください。";
    case 502:
    case 503:
    case 504:
      return "テストメール送信に失敗しました。SMTPサーバーへ到達できないか、上流サービスが応答していません。SMTP設定とサーバーログを確認してください。";
    default:
      return `テストメール送信に失敗しました。HTTP ${status}: ${detail}`;
  }
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="space-y-2 text-sm font-medium">
      <span>{label}</span>
      {children}
    </label>
  );
}
