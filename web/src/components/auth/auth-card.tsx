"use client";

import type { FormEvent, ReactNode } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { KeyRound, Moon, RadioTower, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { TurnstileWidget } from "@/components/auth/turnstile-widget";
import { APIError, apiGet, apiPost, setCSRFToken } from "@/lib/api/client";
import { passkeyAssertionCredentialToJSON, passkeysSupported, publicKeyRequestOptionsFromJSON } from "@/lib/passkeys";
import { useI18n } from "@/components/admin/i18n-provider";
import { useTheme } from "@/components/admin/theme-provider";
import { useAppSettings, useSetupStatus } from "@/features/queries";
import type { OAuthLinkStartResponse, OAuthLoginProvider, PasskeyLoginStart } from "@/types/domain";

type LoginResponse = {
  csrf_token?: string;
  mfa_required?: boolean;
  challenge_token?: string;
};

export function LoginCard() {
  const { t } = useI18n();
  const router = useRouter();
  const setupStatus = useSetupStatus();
  const appSettings = useAppSettings();
  const oauthProviders = useQuery({ queryKey: ["auth", "oauth", "providers", "login"], queryFn: () => apiGet<OAuthLoginProvider[]>("/auth/oauth/providers") });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMFACode] = useState("");
  const [mfaChallengeToken, setMFAChallengeToken] = useState(() => oauthMFAChallengeFromHash());
  const [turnstileToken, setTurnstileToken] = useState("");
  const [turnstileResetKey, setTurnstileResetKey] = useState(0);
  const [message, setMessage] = useState(() => (oauthMFAChallengeFromHash() ? "OAuthログインのMFA確認を完了してください。" : ""));
  const [busy, setBusy] = useState(false);
  const [passkeyUnavailable, setPasskeyUnavailable] = useState(false);
  const turnstileEnabled = Boolean(appSettings.data?.turnstile_enabled && appSettings.data?.turnstile_site_key);
  const turnstileSiteKey = appSettings.data?.turnstile_site_key || "";
  const loginSecurityPending = appSettings.isLoading || (turnstileEnabled && !turnstileToken);

  const resetTurnstile = () => {
    setTurnstileToken("");
    setTurnstileResetKey((value) => value + 1);
  };

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (oauthMFAChallengeFromHash()) {
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
    }
  }, []);

  const login = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const body = await apiPost<LoginResponse>("/auth/login", { username, password, turnstile_token: turnstileToken });
      if (body.mfa_required && body.challenge_token) {
        setMFAChallengeToken(body.challenge_token);
        setMessage("2FAコードを入力してください。");
        return;
      }
      setCSRFToken(body.csrf_token);
      router.push("/admin/");
    } catch (error) {
      resetTurnstile();
      setMessage(authErrorMessage(error, "ログインできませんでした。ユーザー名とパスワードを確認してください。"));
    } finally {
      setBusy(false);
    }
  };

  const verifyMFA = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const body = await apiPost<{ csrf_token?: string }>("/auth/mfa/verify", { challenge_token: mfaChallengeToken, code: mfaCode });
      setCSRFToken(body.csrf_token);
      router.push("/admin/");
    } catch (error) {
      setMessage(authErrorMessage(error, "2FAコードを確認してください。"));
    } finally {
      setBusy(false);
    }
  };

  const startOAuthLogin = async (providerID: string) => {
    setBusy(true);
    setMessage("");
    try {
      const body = await apiPost<OAuthLinkStartResponse>(`/auth/oauth/${encodeURIComponent(providerID)}/start`, { turnstile_token: turnstileToken });
      window.location.assign(body.authorization_url);
    } catch (error) {
      resetTurnstile();
      setMessage(authErrorMessage(error, "OAuthログインを開始できませんでした。"));
      setBusy(false);
    }
  };

  const loginWithPasskey = async () => {
    if (!passkeysSupported()) {
      setPasskeyUnavailable(true);
      setMessage("このブラウザではPasskeyログインを利用できません。");
      return;
    }
    setPasskeyUnavailable(false);
    setBusy(true);
    setMessage("");
    try {
      const body = { username: username.trim() || undefined, turnstile_token: turnstileToken };
      const start = await apiPost<PasskeyLoginStart>("/auth/passkeys/login/start", body);
      const credential = await navigator.credentials.get({ publicKey: publicKeyRequestOptionsFromJSON(start.public_key) });
      if (!credential || !(credential instanceof PublicKeyCredential)) {
        throw new Error("passkey authentication cancelled");
      }
      const result = await apiPost<LoginResponse>("/auth/passkeys/login/finish", {
        challenge_token: start.challenge_token,
        credential: passkeyAssertionCredentialToJSON(credential),
      });
      if (result.mfa_required && result.challenge_token) {
        setMFAChallengeToken(result.challenge_token);
        setMessage("2FAコードを入力してください。");
        return;
      }
      setCSRFToken(result.csrf_token);
      router.push("/admin/");
    } catch (error) {
      resetTurnstile();
      setMessage(authErrorMessage(error, "Passkeyでログインできませんでした。"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthFrame title={t("login")} description="Control Panelにログインします。">
      <form className="space-y-3" onSubmit={mfaChallengeToken ? verifyMFA : login}>
        {setupStatus.data?.setup_required ? (
          <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
            初回管理者が未作成です。先に{" "}
            <Link href="/setup" className="font-medium text-primary underline-offset-4 hover:underline">
              初期作成
            </Link>
            を完了してください。
          </div>
        ) : null}
        {mfaChallengeToken ? (
          <Input value={mfaCode} onChange={(event) => setMFACode(event.target.value)} placeholder="2FAコード" inputMode="numeric" autoComplete="one-time-code" />
        ) : (
          <>
            <Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder={t("username")} autoComplete="username" />
            <Input value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t("password")} type="password" autoComplete="current-password" />
            {turnstileEnabled ? <TurnstileWidget siteKey={turnstileSiteKey} action="login" resetKey={turnstileResetKey} onToken={setTurnstileToken} /> : null}
          </>
        )}
        {message ? <p className="text-sm text-destructive">{message}</p> : null}
        <Button className="w-full" type="submit" disabled={busy || (!mfaChallengeToken && loginSecurityPending) || (Boolean(mfaChallengeToken) && mfaCode.trim().length < 6)}>
          {mfaChallengeToken ? "2FA確認" : t("login")}
        </Button>
      </form>
      {!mfaChallengeToken ? (
        <div className="space-y-2">
          <Button type="button" variant="outline" className="w-full justify-start" disabled={busy || loginSecurityPending} onClick={loginWithPasskey}>
            <KeyRound className="size-4" />
            Passkeyでログイン
          </Button>
          {passkeyUnavailable ? <p className="text-xs text-muted-foreground">このブラウザではPasskeyを利用できません。</p> : null}
        </div>
      ) : null}
      {!mfaChallengeToken && oauthProviders.data?.length ? (
        <div className="space-y-2">
          <div className="text-xs text-muted-foreground">OAuthログイン</div>
          {oauthProviders.data.map((provider) => (
            <Button key={provider.id} type="button" variant="outline" className="w-full justify-start" disabled={busy || loginSecurityPending} onClick={() => startOAuthLogin(provider.id)}>
              {provider.name || provider.provider_type}
            </Button>
          ))}
        </div>
      ) : null}
    </AuthFrame>
  );
}

export function EmailConfirmCard({ token }: { token?: string }) {
  const appSettings = useAppSettings();
  const searchParams = useSearchParams();
  const [turnstileToken, setTurnstileToken] = useState("");
  const [turnstileResetKey, setTurnstileResetKey] = useState(0);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const turnstileEnabled = Boolean(appSettings.data?.turnstile_enabled && appSettings.data?.turnstile_site_key);
  const turnstileSiteKey = appSettings.data?.turnstile_site_key || "";
  const tokenFromURL = searchParams.get("token") || searchParams.get("t") || searchParams.get("confirmation_token") || "";
  const trimmedToken = (token || tokenFromURL).trim();

  const confirm = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const body = await apiPost<{ status: string; target?: string }>("/auth/email/confirm", { token: trimmedToken, turnstile_token: turnstileToken });
      setMessage(body.target ? `メールアドレスを変更しました。宛先: ${body.target}` : "メールアドレスを変更しました。");
    } catch (error) {
      setTurnstileToken("");
      setTurnstileResetKey((value) => value + 1);
      setMessage(authErrorMessage(error, "メールアドレス変更を確認できませんでした。"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthFrame title="メールアドレス変更確認" description="ワンタイムURLの確認を完了します。">
      <form className="space-y-3" onSubmit={confirm}>
        {!trimmedToken ? <p className="text-sm text-destructive">確認トークンがありません。</p> : null}
        {turnstileEnabled ? <TurnstileWidget siteKey={turnstileSiteKey} action="email_confirm" resetKey={turnstileResetKey} onToken={setTurnstileToken} /> : null}
        {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
        <Button className="w-full" type="submit" disabled={busy || !trimmedToken || (turnstileEnabled && !turnstileToken)}>
          確認する
        </Button>
      </form>
      <Button asChild variant="outline" className="w-full">
        <Link href="/login">ログインへ戻る</Link>
      </Button>
    </AuthFrame>
  );
}

function authErrorMessage(error: unknown, fallback: string) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      invalid_credentials: "ユーザー名またはパスワードを確認してください。",
      mfa_enrollment_required: "このアカウントは2FA登録が必要です。管理者に確認してください。",
      invalid_mfa_code: "2FAコードを確認してください。",
      invalid_mfa_challenge: "2FA確認の有効期限が切れています。もう一度ログインしてください。",
      passkey_required: "このアカウントはPasskeyログインが必要です。",
      passkey_enrollment_required: "このアカウントはPasskey登録が必要です。管理者に確認してください。",
      passkeys_not_configured: "Passkeyログインはまだ構成されていません。",
      passkey_runtime_unavailable: "Passkeyログイン設定を確認してください。",
      passkey_login_challenge_failed: "Passkeyログインを開始できませんでした。",
      invalid_passkey_login_challenge: "Passkeyログインの有効期限が切れています。もう一度お試しください。",
      passkey_login_response_required: "Passkey認証の応答がありません。",
      oauth_provider_not_usable_for_login: "このOAuthプロバイダはログインに利用できません。",
      turnstile_token_required: "BOT確認を完了してください。",
      turnstile_failed: "BOT確認に失敗しました。もう一度お試しください。",
      turnstile_unavailable: "BOT確認を利用できません。時間をおいて再試行してください。",
      turnstile_not_configured: "BOT確認設定が未完了です。管理者に確認してください。",
      invalid_email_change_token: "メールアドレス変更URLの有効期限が切れています。",
    };
    return messages[error.code || ""] || `${fallback} (${error.code || error.status})`;
  }
  return fallback;
}

function oauthMFAChallengeFromHash() {
  if (typeof window === "undefined") return "";
  const hash = window.location.hash.startsWith("#") ? window.location.hash.slice(1) : "";
  if (!hash) return "";
  return new URLSearchParams(hash).get("oauth_mfa_challenge")?.trim() || "";
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
