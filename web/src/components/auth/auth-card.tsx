"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { apiPost, setCSRFToken } from "@/lib/api/client";
import { useI18n } from "@/components/admin/i18n-provider";

export function LoginCard() {
  const { t } = useI18n();
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const login = async () => {
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
      <Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder={t("username")} autoComplete="username" />
      <Input value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t("password")} type="password" autoComplete="current-password" />
      {message ? <p className="text-sm text-red-600">{message}</p> : null}
      <Button className="w-full" onClick={login} disabled={busy}>
        {t("login")}
      </Button>
    </AuthFrame>
  );
}

export function SetupCard() {
  const { t } = useI18n();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [message, setMessage] = useState("");

  const create = async () => {
    try {
      await apiPost("/setup/first-admin", { username, password, setup_token: setupToken });
      setMessage("初期管理者を作成しました。ログインページへ進んでください。");
    } catch {
      setMessage("初期作成に失敗しました。セットアップトークンとパスワード条件を確認してください。");
    }
  };

  return (
    <AuthFrame title={t("setup")} description="初回だけ管理者ユーザーを作成します。">
      <Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder={t("username")} autoComplete="username" />
      <Input value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t("password")} type="password" autoComplete="new-password" />
      <Input value={setupToken} onChange={(event) => setSetupToken(event.target.value)} placeholder="Setup token" type="password" />
      {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
      <Button className="w-full" onClick={create}>
        {t("createFirstAdmin")}
      </Button>
    </AuthFrame>
  );
}

function AuthFrame({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">{children}</CardContent>
      </Card>
    </main>
  );
}
