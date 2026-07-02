"use client";

import type { FormEvent, ReactNode } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Moon, RadioTower, Sun } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { apiPost, setCSRFToken } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";
import { useTheme } from "@/components/admin/theme-provider";
import { useAppSettings, useSetupStatus } from "@/features/queries";

export function LoginCard() {
  const { t } = useI18n();
  const router = useRouter();
  const setupStatus = useSetupStatus();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const login = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const body = await apiPost<{ csrf_token?: string }>("/auth/login", { username, password });
      setCSRFToken(body.csrf_token);
      router.push("/admin/");
    } catch {
      setMessage("ログインできませんでした。ユーザー名とパスワードを確認してください。");
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthFrame title={t("login")} description="Control Panelにログインします。">
      <form className="space-y-3" onSubmit={login}>
        {setupStatus.data?.setup_required ? (
          <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
            初回管理者が未作成です。先に{" "}
            <Link href="/setup" className="font-medium text-primary underline-offset-4 hover:underline">
              初期作成
            </Link>
            を完了してください。
          </div>
        ) : null}
        <Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder={t("username")} autoComplete="username" />
        <Input value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t("password")} type="password" autoComplete="current-password" />
        {message ? <p className="text-sm text-destructive">{message}</p> : null}
        <Button className="w-full" type="submit" disabled={busy}>
          {t("login")}
        </Button>
      </form>
    </AuthFrame>
  );
}

export function SetupCard() {
  const { t } = useI18n();
  const router = useRouter();
  const setupStatus = useSetupStatus();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      await apiPost("/setup/first-admin", { username, password, setup_token: setupToken });
      setMessage("初期管理者を作成しました。ログインページへ進みます。");
      setTimeout(() => router.push("/login"), 600);
    } catch {
      setMessage("初期作成に失敗しました。セットアップトークン、ユーザー名、12文字以上のパスワードを確認してください。");
    } finally {
      setBusy(false);
    }
  };

  const disabled = setupStatus.data ? !setupStatus.data.setup_required : false;

  return (
    <AuthFrame title={t("setup")} description="初回だけ管理者ユーザーを作成します。">
      {setupStatus.isLoading ? <Skeleton className="h-10 w-full" /> : null}
      {setupStatus.data && !setupStatus.data.setup_enabled ? (
        <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">初期作成は無効です。`AUTOSTREAM_SETUP_TOKEN` を設定して再起動してください。</div>
      ) : null}
      {setupStatus.data?.setup_enabled && !setupStatus.data.setup_required ? (
        <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
          初期管理者は作成済みです。{" "}
          <Link href="/login" className="font-medium text-primary underline-offset-4 hover:underline">
            ログインページ
          </Link>
          へ進んでください。
        </div>
      ) : null}
      <form className="space-y-3" onSubmit={create}>
        <Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder={t("username")} autoComplete="username" disabled={disabled || busy} />
        <Input value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t("password")} type="password" autoComplete="new-password" disabled={disabled || busy} />
        <Input value={setupToken} onChange={(event) => setSetupToken(event.target.value)} placeholder="Setup token" type="password" disabled={disabled || busy} />
        {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
        <Button className="w-full" type="submit" disabled={disabled || busy}>
          {t("createFirstAdmin")}
        </Button>
      </form>
    </AuthFrame>
  );
}

function AuthFrame({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  const { t } = useI18n();
  const { dark, toggleTheme } = useTheme();
  const appSettings = useAppSettings();
  const appName = appSettings.data?.app_name || t("appName");

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="absolute right-4 top-4">
        <Button variant="outline" size="icon-sm" onClick={toggleTheme} aria-label={t("theme")}>
          {dark ? <Moon /> : <Sun />}
        </Button>
      </div>
      <Card className="w-full max-w-md">
        <CardHeader>
          <div className="mb-2 flex items-center gap-3">
            <div className="flex size-9 items-center justify-center rounded-md bg-primary text-primary-foreground">
              <RadioTower className="size-5" />
            </div>
            <div>
              <div className="font-semibold">{appName}</div>
              <div className="text-xs text-muted-foreground">Control Panel</div>
            </div>
          </div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">{children}</CardContent>
      </Card>
    </main>
  );
}
