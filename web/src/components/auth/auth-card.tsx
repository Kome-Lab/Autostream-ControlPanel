"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


import type { FormEvent, ReactNode } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { KeyRound, Moon, RadioTower, Sun } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader } from "@/components/ui/card";
import { Field } from "@/components/forms/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { TurnstileWidget } from "@/components/auth/turnstile-widget";
import { APIError, apiGet, apiPost, clearCSRFToken, setCSRFToken } from "@/lib/api/client";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import { safePostLoginPath } from "@/lib/auth/post-login-redirect";
import { passkeyAssertionCredentialToJSON, passkeysSupported, publicKeyRequestOptionsFromJSON } from "@/lib/passkeys";
import { useI18n } from "@/components/admin/i18n-provider";
import { useTheme } from "@/components/admin/theme-provider";
import { useAppSettings, useSetupStatus } from "@/features/queries";
import type { OAuthLinkStartResponse, OAuthLoginProvider, PasskeyLoginStart, SetupStatus } from "@/types/domain";
import { AccountActionConfirmation } from "@/features/account/account-action-confirmation";
import { createAccountActionController, type AccountAuthoritySnapshot } from "@/features/account/account-action-policy";
import { validatedOAuthRedirect } from "@/features/account/oauth-redirect-handoff";
import type { TranslationKey, TranslationValues } from "@/lib/i18n";

type LoginResponse = {
  csrf_token?: string;
  mfa_required?: boolean;
  challenge_token?: string;
};

export function LoginCard() {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const router = useRouter();
  const searchParams = useSearchParams();
  const setupStatus = useSetupStatus();
  const appSettings = useAppSettings();
  const oauthProviders = useQuery({ queryKey: ["auth", "oauth", "providers", "login"], queryFn: () => apiGet<OAuthLoginProvider[]>("/auth/oauth/providers") });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMFACode] = useState("");
  const [mfaChallengeToken, setMFAChallengeToken] = useState(() => oauthMFAChallengeFromHash());
  const [turnstileToken, setTurnstileToken] = useState("");
  const [turnstileResetKey, setTurnstileResetKey] = useState(0);
  const [message, setMessage] = useState(() => (oauthMFAChallengeFromHash() ? uiText("OAuthログインのMFA確認を完了してください。") : ""));
  const [busy, setBusy] = useState(false);
  const [passkeyUnavailable, setPasskeyUnavailable] = useState(false);
  const turnstileEnabled = Boolean(appSettings.data?.turnstile_enabled && appSettings.data?.turnstile_site_key);
  const turnstileSiteKey = appSettings.data?.turnstile_site_key || "";
  const loginSecurityPending = appSettings.isLoading || (turnstileEnabled && !turnstileToken);
  const sessionExpired = searchParams.get("reason") === "session_expired";
  const postLoginPath = safePostLoginPath(searchParams.get("redirect_after"));

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
        clearCSRFToken();
        setMFAChallengeToken(body.challenge_token);
        setMessage(uiText("2FAコードを入力してください。"));
        return;
      }
      setCSRFToken(body.csrf_token);
      router.replace(postLoginPath);
    } catch (error) {
      resetTurnstile();
      setMessage(authErrorMessage(error, uiText("ログインできませんでした。ユーザー名とパスワードを確認してください。"), t, uiText));
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
      router.replace(postLoginPath);
    } catch (error) {
      setMessage(authErrorMessage(error, uiText("2FAコードを確認してください。"), t, uiText));
    } finally {
      setBusy(false);
    }
  };

  const startOAuthLogin = async (providerID: string) => {
    setBusy(true);
    setMessage("");
    try {
      const body = await apiPost<OAuthLinkStartResponse>(`/auth/oauth/${encodeURIComponent(providerID)}/start`, {
        redirect_after: postLoginPath,
        turnstile_token: turnstileToken,
      });
      window.location.assign(validatedOAuthRedirect(body.authorization_url));
    } catch (error) {
      resetTurnstile();
      setMessage(authErrorMessage(error, uiText("OAuthログインを開始できませんでした。"), t, uiText));
      setBusy(false);
    }
  };

  const loginWithPasskey = async () => {
    if (!passkeysSupported()) {
      setPasskeyUnavailable(true);
      setMessage(uiText("このブラウザではPasskeyログインを利用できません。"));
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
        clearCSRFToken();
        setMFAChallengeToken(result.challenge_token);
        setMessage(uiText("2FAコードを入力してください。"));
        return;
      }
      setCSRFToken(result.csrf_token);
      router.replace(postLoginPath);
    } catch (error) {
      resetTurnstile();
      setMessage(authErrorMessage(error, uiText("Passkeyでログインできませんでした。"), t, uiText));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthFrame title={t("login")} description={locale === "ja" ? "Control Panelにログインします。" : "Sign in to the Control Panel."}>
      {sessionExpired ? <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">{uiText("セッションの有効期限が切れました。もう一度ログインしてください。")}</div> : null}
      <form className="space-y-3" onSubmit={mfaChallengeToken ? verifyMFA : login}>
        {setupStatus.data?.setup_required ? (
          <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
            {uiText("初回管理者が未作成です。先に")}{" "}
            <Link href="/setup" className="font-medium text-primary underline-offset-4 hover:underline">
              {uiText("初期作成")}</Link>
            {uiText("を完了してください。")}</div>
        ) : null}
        {mfaChallengeToken ? (
          <Field label="2FA / MFA"><Input value={mfaCode} onChange={(event) => setMFACode(event.target.value)} placeholder={uiText("2FAコード")} inputMode="numeric" autoComplete="one-time-code" /></Field>
        ) : (
          <>
            <Field label={t("username")}><Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder={t("username")} autoComplete="username" /></Field>
            <Field label={t("password")}><Input value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t("password")} type="password" autoComplete="current-password" /></Field>
            {turnstileEnabled ? <TurnstileWidget siteKey={turnstileSiteKey} action="login" resetKey={turnstileResetKey} onToken={setTurnstileToken} /> : null}
          </>
        )}
        {message ? <p role="status" className="text-sm text-destructive">{message}</p> : null}
        <Button className="w-full" type="submit" disabled={busy || (!mfaChallengeToken && loginSecurityPending) || (Boolean(mfaChallengeToken) && mfaCode.trim().length < 6)}>
          {mfaChallengeToken ? uiText("2FA確認") : t("login")}
        </Button>
      </form>
      {!mfaChallengeToken ? (
        <div className="space-y-2">
          <Button type="button" variant="outline" className="w-full justify-start" disabled={busy || loginSecurityPending} onClick={loginWithPasskey}>
            <KeyRound className="size-4" />
            {uiText("Passkeyでログイン")}</Button>
          {passkeyUnavailable ? <p className="text-xs text-muted-foreground">{uiText("このブラウザではPasskeyを利用できません。")}</p> : null}
        </div>
      ) : null}
      {!mfaChallengeToken && oauthProviders.data?.length ? (
        <div className="space-y-2">
          <div className="text-xs text-muted-foreground">{uiText("OAuthログイン")}</div>
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
  const uiText = useUICopy();
  const appSettings = useAppSettings();
  const searchParams = useSearchParams();
  const [turnstileToken, setTurnstileToken] = useState("");
  const [turnstileResetKey, setTurnstileResetKey] = useState(0);
  const [message, setMessage] = useState("");
  const turnstileEnabled = Boolean(appSettings.data?.turnstile_enabled && appSettings.data?.turnstile_site_key);
  const turnstileSiteKey = appSettings.data?.turnstile_site_key || "";
  const tokenFromURL = searchParams.get("token") || searchParams.get("t") || searchParams.get("confirmation_token") || "";
  const trimmedToken = (token || tokenFromURL).trim();
  const authority = emailConfirmationAuthority(trimmedToken);
  const actionController = useMemo(() => createAccountActionController({
    readAuthority: () => emailConfirmationAuthority(trimmedToken),
  }), [trimmedToken]);

  const confirm = async () => {
    try {
      return await apiPost<{ status: string; target?: string }>("/auth/email/confirm", { token: trimmedToken, turnstile_token: turnstileToken });
    } catch (error) {
      setTurnstileToken("");
      setTurnstileResetKey((value) => value + 1);
      throw error;
    }
  };

  return (
    <AuthFrame title={uiText("メールアドレス変更確認")} description={uiText("ワンタイムURLの確認を完了します。")}>
      <form className="space-y-3" onSubmit={(event) => event.preventDefault()}>
        {!trimmedToken ? <p className="text-sm text-destructive">{uiText("確認トークンがありません。")}</p> : null}
        {turnstileEnabled ? <TurnstileWidget siteKey={turnstileSiteKey} action="email_confirm" resetKey={turnstileResetKey} onToken={setTurnstileToken} /> : null}
        {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
        <AccountActionConfirmation
          controller={actionController}
          intent={{ id: "AUTH-07", resourceId: "email-change" }}
          authority={authority}
          refreshAuthority={async () => emailConfirmationAuthority(trimmedToken)}
          label={uiText("確認する")}
          variant="default"
          className="w-full"
          disabled={!trimmedToken || (turnstileEnabled && !turnstileToken)}
          handler={confirm}
          onSucceeded={() => setMessage(uiText("メールアドレスを変更しました。"))}
          onOutcomeUnknown={() => setMessage(uiText("変更結果を確認できません。再送せず、ログイン後のアカウント状態を確認してください。"))}
        />
      </form>
      <Button asChild variant="outline" className="w-full">
        <Link href="/login">{uiText("ログインへ戻る")}</Link>
      </Button>
    </AuthFrame>
  );
}

function authErrorMessage(
  error: unknown,
  fallback: string,
  translate: (key: TranslationKey, values?: TranslationValues) => string,
  uiText: UICopy = japaneseCopy
) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      invalid_credentials: uiText("ユーザー名またはパスワードを確認してください。"),
      mfa_enrollment_required: uiText("このアカウントは2FA登録が必要です。管理者に確認してください。"),
      invalid_mfa_code: uiText("2FAコードを確認してください。"),
      invalid_mfa_challenge: uiText("2FA確認の有効期限が切れています。もう一度ログインしてください。"),
      passkey_required: uiText("このアカウントはPasskeyログインが必要です。"),
      passkey_enrollment_required: uiText("このアカウントはPasskey登録が必要です。管理者に確認してください。"),
      passkeys_not_configured: uiText("Passkeyログインはまだ構成されていません。"),
      passkey_runtime_unavailable: uiText("Passkeyログイン設定を確認してください。"),
      passkey_login_challenge_failed: uiText("Passkeyログインを開始できませんでした。"),
      invalid_passkey_login_challenge: uiText("Passkeyログインの有効期限が切れています。もう一度お試しください。"),
      passkey_login_response_required: uiText("Passkey認証の応答がありません。"),
      oauth_provider_not_usable_for_login: uiText("このOAuthプロバイダはログインに利用できません。"),
      turnstile_token_required: uiText("BOT確認を完了してください。"),
      turnstile_failed: uiText("BOT確認に失敗しました。もう一度お試しください。"),
      turnstile_unavailable: uiText("BOT確認を利用できません。時間をおいて再試行してください。"),
      turnstile_not_configured: uiText("BOT確認設定が未完了です。管理者に確認してください。"),
      invalid_email_change_token: uiText("メールアドレス変更URLの有効期限が切れています。"),
    };
    if (messages[error.code || ""]) return messages[error.code || ""];
  }
  const adapted = adaptAPIError(error);
  return adapted.kind === "unknown" ? fallback : translate(adapted.messageKey);
}

function emailConfirmationAuthority(token: string): AccountAuthoritySnapshot {
  return token
    ? Object.freeze({ session: "one-time-token", freshness: "fresh", revision: "email-confirmation-present" })
    : Object.freeze({ session: "unavailable", freshness: "unavailable", revision: "email-confirmation-missing" });
}

function setupAuthority(status: SetupStatus | undefined, fetching: boolean): AccountAuthoritySnapshot {
  if (fetching) return Object.freeze({ session: "setup", freshness: "refreshing", revision: "setup-refreshing" });
  if (!status?.setup_enabled || !status.setup_required) {
    return Object.freeze({ session: "unavailable", freshness: "fresh", revision: "setup-unavailable" });
  }
  return Object.freeze({ session: "setup", freshness: "fresh", revision: "setup-enabled-required" });
}

function oauthMFAChallengeFromHash() {
  if (typeof window === "undefined") return "";
  const hash = window.location.hash.startsWith("#") ? window.location.hash.slice(1) : "";
  if (!hash) return "";
  return new URLSearchParams(hash).get("oauth_mfa_challenge")?.trim() || "";
}

export function SetupCard() {
  const uiText = useUICopy();
  const { t } = useI18n();
  const router = useRouter();
  const setupStatus = useSetupStatus();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [message, setMessage] = useState("");
  const authority = setupAuthority(setupStatus.data, setupStatus.isFetching);
  const actionController = useMemo(() => createAccountActionController({
    readAuthority: () => setupAuthority(setupStatus.data, setupStatus.isFetching),
  }), [setupStatus.data, setupStatus.isFetching]);
  const refreshAuthority = async () => {
    const result = await setupStatus.refetch();
    return setupAuthority(result.data, false);
  };

  const disabled = setupStatus.data ? !setupStatus.data.setup_required : false;

  return (
    <AuthFrame title={t("setup")} description={uiText("初回だけ管理者ユーザーを作成します。")}>
      {setupStatus.isLoading ? <Skeleton className="h-10 w-full" /> : null}
      {setupStatus.data && !setupStatus.data.setup_enabled ? (
        <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">{uiText("初期作成は無効です。`AUTOSTREAM_SETUP_TOKEN` を設定して再起動してください。")}</div>
      ) : null}
      {setupStatus.data?.setup_enabled && !setupStatus.data.setup_required ? (
        <div className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
          {uiText("初期管理者は作成済みです。")}{" "}
          <Link href="/login" className="font-medium text-primary underline-offset-4 hover:underline">
            {uiText("ログインページ")}</Link>
          {uiText("へ進んでください。")}</div>
      ) : null}
      <form className="space-y-3" onSubmit={(event) => event.preventDefault()}>
        <Field label={t("username")}><Input value={username} onChange={(event) => setUsername(event.target.value)} placeholder={t("username")} autoComplete="username" disabled={disabled} /></Field>
        <Field label={t("password")}><Input value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t("password")} type="password" autoComplete="new-password" disabled={disabled} /></Field>
        <Field label="Setup token"><Input value={setupToken} onChange={(event) => setSetupToken(event.target.value)} placeholder="Setup token" type="password" disabled={disabled} /></Field>
        {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
        <AccountActionConfirmation
          controller={actionController}
          intent={{ id: "AUTH-01", resourceId: "first-admin", publicUsername: username }}
          authority={authority}
          refreshAuthority={refreshAuthority}
          label={t("createFirstAdmin")}
          variant="default"
          className="w-full"
          disabled={disabled || !username.trim() || !password || !setupToken}
          handler={() => apiPost("/setup/first-admin", { username, password, setup_token: setupToken })}
          onSucceeded={() => {
            setPassword("");
            setSetupToken("");
            setMessage(uiText("初期管理者を作成しました。ログインページへ進みます。"));
            setTimeout(() => router.push("/login"), 600);
          }}
          onOutcomeUnknown={() => setMessage(uiText("初期作成の結果を確認できません。再送せず、ログイン可能か確認してください。"))}
        />
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
    <main className="flex min-h-dvh items-center justify-center bg-background px-4 py-20 sm:px-6" data-screen-family="authentication">
      <div className="absolute right-4 top-4">
        <Button variant="outline" size="icon-sm" onClick={toggleTheme} aria-label={t("theme")}>
          {dark ? <Moon /> : <Sun />}
        </Button>
      </div>
      <Card className="w-full max-w-md shadow-none">
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
          <h1 className="text-2xl font-semibold leading-tight">{title}</h1>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">{children}</CardContent>
      </Card>
    </main>
  );
}
