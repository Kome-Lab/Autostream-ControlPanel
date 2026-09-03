"use client";

import { type ReactNode, useCallback, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Save, Send } from "lucide-react";
import { RemoteStateBoundary } from "@/components/foundation/remote-state/remote-state-boundary";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { defaultTimeZone, formatDateTimeInTimeZone, isValidTimeZone, normalizeTimeZone, timeZoneLabel, timeZoneOptions } from "@/lib/timezone";
import { useI18n } from "@/components/admin/i18n-provider";
import { useCurrentUser, useManagedAppSettings } from "@/features/queries";
import { AppSettingsActionConfirmationHost } from "@/features/settings/app-settings-action-control";
import { createAppSettingsActionController, type AppSettingsActionIntent, type AppSettingsActionResult } from "@/features/settings/app-settings-action-policy";
import { appSettingsIntent, appSettingsPermissionSnapshot, appSettingsStateSnapshot, managedAppSettingsRemoteState, mutateAppSettingsAction, refreshAppSettingsAction } from "@/features/settings/app-settings-action-runtime";
import { hasPermission } from "@/lib/auth/permissions";
import type { ManagedAppSettings } from "@/types/domain";

const customTimeZoneValue = "__custom_timezone__";

export function SettingsView() {
  const { t } = useI18n();
  const appSettings = useManagedAppSettings();
  const state = managedAppSettingsRemoteState(appSettings);

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
        <CardContent className="space-y-4">
          <RemoteStateBoundary
            state={state}
            noticeId="app-settings-remote-state"
            translate={t}
            formatTimestamp={(timestamp) => formatDateTimeInTimeZone(new Date(timestamp).toISOString(), appSettings.data?.timezone || defaultTimeZone, { dateStyle: "medium", timeStyle: "medium" })}
            renderLoading={() => <Skeleton className="h-10 w-full" />}
            renderData={(settings) => (
              <AppSettingsForm
                key={`${settings.app_name || "default"}-${settings.timezone || defaultTimeZone}-${settings.smtp_enabled ? "smtp-on" : "smtp-off"}-${settings.turnstile_enabled ? "turnstile-on" : "turnstile-off"}-${settings.google_analytics_enabled ? "analytics-on" : "analytics-off"}`}
                initialSettings={settings}
              />
            )}
            onRetry={() => void appSettings.refetch()}
            retryPending={appSettings.isFetching}
          />
        </CardContent>
      </Card>
    </div>
  );
}

function AppSettingsForm({ initialSettings }: { initialSettings?: ManagedAppSettings }) {
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
  const [googleAnalyticsEnabled, setGoogleAnalyticsEnabled] = useState(Boolean(initialSettings?.google_analytics_enabled));
  const [googleAnalyticsMeasurementID, setGoogleAnalyticsMeasurementID] = useState(initialSettings?.google_analytics_measurement_id || "");
  const [testEmailOverride, setTestEmailOverride] = useState<string | null>(null);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState<AppSettingsActionIntent | null>(null);
  const [dispatching, setDispatching] = useState(false);
  const trimmedTimezone = timezone.trim();
  const effectiveTimezone = trimmedTimezone || defaultTimeZone;
  const timezoneValid = trimmedTimezone === "" || isValidTimeZone(trimmedTimezone);
  const normalizedTimezone = normalizeTimeZone(timezone);
  const options = timeZoneOptions.some((option) => option.value === normalizedTimezone) ? timeZoneOptions : [{ value: normalizedTimezone, label: timeZoneLabel(normalizedTimezone) }, ...timeZoneOptions];
  const timezoneSelectValue = options.some((option) => option.value === effectiveTimezone) ? effectiveTimezone : customTimeZoneValue;
  const testEmailTo = testEmailOverride ?? currentUserEmail;
  const actionController = useMemo(() => createAppSettingsActionController({
    getPermissions: () => appSettingsPermissionSnapshot(queryClient),
    getState: () => appSettingsStateSnapshot(queryClient),
    refresh: () => refreshAppSettingsAction(queryClient),
    mutate: mutateAppSettingsAction,
  }), [queryClient]);
  const savePayload = {
    app_name: appName,
    timezone: normalizedTimezone,
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
    google_analytics_enabled: googleAnalyticsEnabled,
    google_analytics_measurement_id: googleAnalyticsMeasurementID,
  };
  const saveIntent = appSettingsIntent("APP-01", savePayload);
  const testEmailIntent = appSettingsIntent("APP-02", { to: testEmailTo.trim() });
  const saveEvaluation = actionController.evaluate(saveIntent);
  const testEvaluation = actionController.evaluate(testEmailIntent);
  const canUpdate = hasPermission(currentUser.data, "system_settings.update");
  const handleActionResult = useCallback((result: AppSettingsActionResult, intent: AppSettingsActionIntent) => {
    setDispatching(false);
    if (result.kind === "succeeded") {
      if (intent.id === "APP-01") {
        setMessage("保存しました。");
        void queryClient.invalidateQueries({ queryKey: ["settings", "app"] });
        void queryClient.invalidateQueries({ queryKey: ["settings", "app", "manage"] });
      } else {
        setMessage("テストメールを送信しました。");
      }
      setPending(null);
      return;
    }
    setMessage(appSettingsActionResultMessage(result, t));
    if (result.kind === "blocked") setPending(null);
  }, [queryClient, t]);
  const smtpRequiredMissing = smtpEnabled && (!smtpHost.trim() || !smtpFrom.trim());
  const turnstileRequiredMissing = turnstileEnabled && (!turnstileSiteKey.trim() || (!turnstileSecret.trim() && !initialSettings?.turnstile_configured));
  const googleAnalyticsIDValid = !googleAnalyticsEnabled || /^G-[A-Z0-9]{4,22}$/.test(googleAnalyticsMeasurementID.trim().toUpperCase());

  return (
    <div className="space-y-4">
      <div className="grid gap-4 xl:grid-cols-2">
        <SettingsSection title="基本設定" description="管理画面の名前と、システム内の時刻表示に使う基準タイムゾーンです。">
          <div className="grid gap-3 lg:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium" htmlFor="app-name">
                {t("appNameLabel")}
              </label>
              <Input id="app-name" value={appName} onChange={(event) => setAppName(event.target.value)} maxLength={80} />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium" htmlFor="app-timezone-input">
                タイムゾーンID
              </label>
              <Input id="app-timezone-input" value={timezone} onChange={(event) => setTimezone(event.target.value)} placeholder="Asia/Tokyo" spellCheck={false} />
              <p className={timezoneValid ? "text-xs text-muted-foreground" : "text-xs text-destructive"}>{timezoneValid ? "IANA time zone nameを直接入力できます。" : "有効なIANA time zone nameを入力してください。"}</p>
            </div>
            <div className="space-y-2 lg:col-span-2">
              <label className="text-sm font-medium" htmlFor="app-timezone">
                候補から選択
              </label>
              <Select
                value={timezoneSelectValue}
                onValueChange={(value) => {
                  if (value !== customTimeZoneValue) setTimezone(value);
                }}
              >
                <SelectTrigger id="app-timezone">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className="max-h-80">
                  {timezoneSelectValue === customTimeZoneValue ? (
                    <SelectItem value={customTimeZoneValue}>{trimmedTimezone ? `手入力: ${trimmedTimezone}` : "手入力"}</SelectItem>
                  ) : null}
                  {options.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
        </SettingsSection>
        <SettingsSection title="表示プレビュー" description="保存前の表示名とタイムゾーン変換結果を確認できます。">
          <dl className="grid grid-cols-[112px_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
            <dt className="text-muted-foreground">アプリ名</dt>
            <dd className="min-w-0 truncate">{appName || "-"}</dd>
            <dt className="text-muted-foreground">タイムゾーン</dt>
            <dd className="min-w-0 truncate">{timezoneValid ? normalizedTimezone : "未確認"}</dd>
            <dt className="text-muted-foreground">現在時刻</dt>
            <dd className="min-w-0 truncate">{timezoneValid ? formatDateTimeInTimeZone(new Date().toISOString(), normalizedTimezone, { dateStyle: "medium", timeStyle: "medium" }) : "-"}</dd>
          </dl>
        </SettingsSection>
        <SettingsSection
          title="メールサーバー"
          description="ユーザー登録完了、メール変更確認、運用通知に使います。"
          action={<Switch checked={smtpEnabled} onCheckedChange={setSMTPEnabled} />}
          className={smtpEnabled ? "xl:col-span-2" : ""}
        >
          {smtpEnabled ? (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              <Field label="SMTP Host">
                <Input value={smtpHost} onChange={(event) => setSMTPHost(event.target.value)} placeholder="smtp.example.jp" />
              </Field>
              <Field label="SMTP Port">
                <Input inputMode="numeric" value={smtpPort} onChange={(event) => setSMTPPort(event.target.value)} />
              </Field>
              <Field label="From">
                <Input value={smtpFrom} onChange={(event) => setSMTPFrom(event.target.value)} placeholder="AutoStream <no-reply@example.jp>" />
              </Field>
              <Field label="SMTP Username">
                <Input value={smtpUsername} onChange={(event) => setSMTPUsername(event.target.value)} />
              </Field>
              <Field label="SMTP Password">
                <Input type="password" value={smtpPassword} onChange={(event) => setSMTPPassword(event.target.value)} placeholder={initialSettings?.smtp_password_configured ? "設定済み" : ""} />
              </Field>
              <label className="flex min-h-10 items-center gap-2 self-end text-sm">
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
                  onClick={() => {
                    setMessage("");
                    setPending(testEmailIntent);
                  }}
                  disabled={Boolean(pending) || dispatching || !canUpdate || testEvaluation.availability.kind !== "allowed" || !smtpEnabled || smtpRequiredMissing || !testEmailTo.trim()}
                >
                  <Send className="size-4" />
                  テスト送信
                </Button>
              </div>
            </div>
          ) : (
            <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">メール送信を使う場合は有効化してSMTP情報を保存してください。</div>
          )}
        </SettingsSection>
        <SettingsSection title="Cloudflare Turnstile" description="ログインとメール変更確認のBOT確認に使います。" action={<Switch checked={turnstileEnabled} onCheckedChange={setTurnstileEnabled} />}>
          {turnstileEnabled ? (
            <div className="grid gap-3 md:grid-cols-2">
              <Field label="Site key">
                <Input value={turnstileSiteKey} onChange={(event) => setTurnstileSiteKey(event.target.value)} placeholder="0x4AAAA..." />
              </Field>
              <Field label="Secret key">
                <Input type="password" value={turnstileSecret} onChange={(event) => setTurnstileSecret(event.target.value)} placeholder={initialSettings?.turnstile_configured ? "設定済み" : ""} />
              </Field>
            </div>
          ) : (
            <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">Turnstileを使う場合は有効化してSite keyとSecret keyを保存してください。</div>
          )}
        </SettingsSection>
        <SettingsSection title="Google Analytics" description="ログイン画面と管理画面のページ閲覧だけをGA4へ送信します。AutoStreamが送るイベントには検索条件、ユーザー情報、配信内容を含めません。" action={<Switch checked={googleAnalyticsEnabled} onCheckedChange={setGoogleAnalyticsEnabled} />}>
          {googleAnalyticsEnabled ? (
            <>
              <Field label="GA4 Measurement ID">
                <Input
                  value={googleAnalyticsMeasurementID}
                  onChange={(event) => setGoogleAnalyticsMeasurementID(event.target.value.toUpperCase())}
                  placeholder="G-XXXXXXXXXX"
                  maxLength={24}
                  spellCheck={false}
                  aria-invalid={!googleAnalyticsIDValid}
                />
                {!googleAnalyticsIDValid ? <span className="text-xs font-normal text-destructive">G-から始まるMeasurement IDを入力してください。</span> : null}
              </Field>
              <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
                GA4データストリームの「拡張計測機能」はOFFにしてください。ONのままでは、履歴変更のpage_viewやサイト内検索などがGoogle側から別途自動送信されます。
              </div>
            </>
          ) : (
            <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">有効化するまでGoogleのスクリプトや計測通信は読み込まれません。</div>
          )}
        </SettingsSection>
      </div>
      {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
      <Button onClick={() => {
        setMessage("");
        setPending(saveIntent);
      }} disabled={Boolean(pending) || dispatching || !canUpdate || saveEvaluation.availability.kind !== "allowed" || !appName.trim() || !timezoneValid || smtpRequiredMissing || turnstileRequiredMissing || !googleAnalyticsIDValid}>
        <Save className="size-4" />
        {t("save")}
      </Button>
      {pending ? (
        <AppSettingsActionConfirmationHost
          key={pending.id}
          controller={actionController}
          intent={pending}
          onDispatch={() => {
            if (pending.id === "APP-01") {
              setSMTPPassword("");
              setTurnstileSecret("");
            }
            setPending(null);
            setDispatching(true);
          }}
          onResult={handleActionResult}
          onCancel={() => setPending(null)}
        />
      ) : null}
    </div>
  );
}

function SettingsSection({ title, description, action, className = "", children }: { title: string; description: string; action?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <section className={`space-y-3 rounded-md border bg-muted/20 p-3 ${className}`}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium">{title}</div>
          <p className="text-xs text-muted-foreground">{description}</p>
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </div>
      {children}
    </section>
  );
}

function appSettingsActionResultMessage(result: AppSettingsActionResult, translate: ReturnType<typeof useI18n>["t"]) {
  if (result.kind === "failed") return translate(result.error.messageKey);
  if (result.kind === "outcome_unknown") return translate("confirmationOutcomeUnknown");
  if (result.kind === "succeeded") return "";
  if (result.reason === "permission-denied") return translate("actionPermissionDenied");
  if (result.reason === "permission-unknown") return translate("actionPermissionUnknown");
  if (result.reason === "duplicate") return translate("actionAlreadyPending");
  if (result.reason === "authority-changed") return translate("confirmationStaleBlocked");
  if (result.reason === "reconciliation-required") return translate("confirmationRefreshRequired");
  return translate("confirmationRevalidationUnavailable");
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="space-y-2 text-sm font-medium">
      <span>{label}</span>
      {children}
    </label>
  );
}
