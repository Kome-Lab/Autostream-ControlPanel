"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Link2, Mail, Plus, ShieldCheck, Trash2, UserCog } from "lucide-react";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { APIError, apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import { useAppSettings, useCurrentUser } from "@/features/queries";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import type { CurrentUser, MFAEnrollResponse, MFAStatus, OAuthLinkStartResponse, OAuthLoginProvider, OAuthUserLink, PasskeyCredential, PasskeyRegistrationStart } from "@/types/domain";

type AccountNotice = { tone: "success" | "error"; text: string } | null;

export function AccountView() {
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const mfaStatus = useQuery({ queryKey: ["auth", "mfa", "status"], queryFn: () => apiGet<MFAStatus>("/auth/mfa/status") });
  const passkeys = useQuery({ queryKey: ["auth", "passkeys"], queryFn: () => apiGet<PasskeyCredential[]>("/auth/passkeys") });
  const oauthLinks = useQuery({ queryKey: ["auth", "oauth-links"], queryFn: () => apiGet<OAuthUserLink[]>("/auth/oauth-links") });
  const oauthProviders = useQuery({ queryKey: ["auth", "oauth", "providers"], queryFn: () => apiGet<OAuthLoginProvider[]>("/auth/oauth/providers") });
  const [notice, setNotice] = useState<AccountNotice>(null);

  const linkedEmails = useMemo(() => {
    const seen = new Set<string>();
    return (oauthLinks.data || [])
      .map((link) => link.email?.trim())
      .filter((email): email is string => {
        if (!email || seen.has(email)) return false;
        seen.add(email);
        return true;
      });
  }, [oauthLinks.data]);

  const showError = (error: unknown, fallback: string) => {
    const code = error instanceof APIError ? error.code : "";
    setNotice({ tone: "error", text: code ? `${fallback} (${code})` : fallback });
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-normal">アカウント</h1>
          <p className="mt-1 text-sm text-muted-foreground">ログイン、本人確認、連携メールを管理します。</p>
        </div>
        <Badge variant="secondary" className="gap-2">
          <UserCog className="size-3.5" />
          {currentUser.data?.user.username || "-"}
        </Badge>
      </div>

      {notice ? (
        <div className={notice.tone === "success" ? "rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800" : "rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800"}>
          {notice.text}
        </div>
      ) : null}

      <div className="grid gap-4 xl:grid-cols-2">
        <PasswordPanel setNotice={setNotice} onError={showError} />
        <EmailPanel
          key={currentUser.data?.user.email || ""}
          currentEmail={currentUser.data?.user.email || ""}
          links={oauthLinks.data || []}
          providers={oauthProviders.data || []}
          linkedEmails={linkedEmails}
          loading={oauthLinks.isLoading || oauthProviders.isLoading}
          setNotice={setNotice}
          onUpdated={() => queryClient.invalidateQueries({ queryKey: ["auth", "me"] })}
          onDeleted={() => queryClient.invalidateQueries({ queryKey: ["auth", "oauth-links"] })}
          onError={showError}
        />
        <MFAPanel status={mfaStatus.data} loading={mfaStatus.isLoading} setNotice={setNotice} onError={showError} refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "mfa", "status"] })} />
        <PasskeyPanel
          passkeys={passkeys.data || []}
          loading={passkeys.isLoading}
          username={currentUser.data?.user.username || ""}
          timezone={appSettings.data?.timezone}
          setNotice={setNotice}
          onError={showError}
          refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "passkeys"] })}
        />
      </div>
    </div>
  );
}

function PasswordPanel({ setNotice, onError }: { setNotice: (notice: AccountNotice) => void; onError: (error: unknown, fallback: string) => void }) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const mismatch = newPassword !== "" && confirmPassword !== "" && newPassword !== confirmPassword;
  const changePassword = useMutation({
    mutationFn: () => apiPost<{ status: string }>("/auth/change-password", { current_password: currentPassword, new_password: newPassword }),
    onSuccess: () => {
      setNotice({ tone: "success", text: "パスワードを変更しました。再ログインしてください。" });
      window.setTimeout(() => window.location.assign("/login"), 900);
    },
    onError: (error) => onError(error, "パスワードを変更できませんでした"),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <KeyRound className="size-5" />
          パスワード
        </CardTitle>
        <CardDescription>変更後は現在のセッションを含めてログアウトします。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <Input type="password" autoComplete="current-password" placeholder="現在のパスワード" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} />
        <Input type="password" autoComplete="new-password" placeholder="新しいパスワード" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} />
        <Input type="password" autoComplete="new-password" placeholder="新しいパスワードを再入力" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} />
        {mismatch ? <div className="text-sm text-red-600">新しいパスワードが一致していません。</div> : null}
        <Button className="w-full" onClick={() => changePassword.mutate()} disabled={!currentPassword || !newPassword || mismatch || changePassword.isPending}>
          変更して再ログイン
        </Button>
      </CardContent>
    </Card>
  );
}

function EmailPanel({
  currentEmail,
  links,
  providers,
  linkedEmails,
  loading,
  setNotice,
  onUpdated,
  onDeleted,
  onError,
}: {
  currentEmail: string;
  links: OAuthUserLink[];
  providers: OAuthLoginProvider[];
  linkedEmails: string[];
  loading: boolean;
  setNotice: (notice: AccountNotice) => void;
  onUpdated: () => void;
  onDeleted: () => void;
  onError: (error: unknown, fallback: string) => void;
}) {
  const [email, setEmail] = useState(currentEmail);
  const updateEmail = useMutation({
    mutationFn: () => apiPut<CurrentUser>("/auth/email", { email: email.trim() }),
    onSuccess: () => {
      setNotice({ tone: "success", text: "メールアドレスを保存しました。" });
      onUpdated();
    },
    onError: (error) => onError(error, "メールアドレスを保存できませんでした"),
  });
  const deleteLink = useMutation({
    mutationFn: (id: string) => apiDelete<{ status: string }>(`/auth/oauth-links/${encodeURIComponent(id)}`),
    onSuccess: onDeleted,
    onError: (error) => onError(error, "OAuth連携を解除できませんでした"),
  });
  const startLink = useMutation({
    mutationFn: (providerID: string) => apiPost<OAuthLinkStartResponse>(`/auth/oauth-links/${encodeURIComponent(providerID)}/start`, { redirect_after: "/admin/account/" }),
    onSuccess: (data) => {
      window.location.assign(data.authorization_url);
    },
    onError: (error) => onError(error, "OAuth連携を開始できませんでした"),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <Mail className="size-5" />
          メール・OAuth連携
        </CardTitle>
        <CardDescription>通知や本人確認に使うメールとログイン連携を管理します。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2 rounded-md border p-3">
          <label className="text-sm font-medium">アカウントメール</label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input type="email" autoComplete="email" placeholder="operator@example.jp" value={email} onChange={(event) => setEmail(event.target.value)} />
            <Button onClick={() => updateEmail.mutate()} disabled={updateEmail.isPending || email.trim() === currentEmail.trim()}>
              保存
            </Button>
          </div>
          <div className="text-xs text-muted-foreground">通知やアカウント登録完了メールの宛先として使います。空で保存すると未設定になります。</div>
        </div>
        <div className="grid gap-2 sm:grid-cols-2">
          {providers.map((provider) => (
            <Button key={provider.id} variant="outline" className="justify-start" onClick={() => startLink.mutate(provider.id)} disabled={startLink.isPending}>
              <Plus className="size-4" />
              {provider.name || providerLabel(provider.provider_type)}を連携
            </Button>
          ))}
          {providers.length === 0 ? <div className="text-sm text-muted-foreground">{loading ? "読み込み中" : "利用可能なOAuthプロバイダはありません。"}</div> : null}
        </div>
        <div className="space-y-2">
          {linkedEmails.length > 0 ? linkedEmails.map((email) => <div key={email} className="rounded-md border px-3 py-2 text-sm">{email}</div>) : <div className="text-sm text-muted-foreground">{loading ? "読み込み中" : "連携メールはありません。"}</div>}
        </div>
        <div className="space-y-2">
          {links.map((link) => (
            <div key={link.id} className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-sm font-medium">
                  <Link2 className="size-4" />
                  {providerLabel(link.provider_type)}
                </div>
                <div className="truncate text-xs text-muted-foreground">{link.email || link.subject}</div>
              </div>
              <DangerConfirm title="OAuth連携を解除" description="このログイン連携をこのアカウントから解除します。" onConfirm={() => deleteLink.mutate(link.id)} actionLabel="解除">
                <Button variant="outline" size="icon-sm" aria-label="OAuth連携を解除">
                  <Trash2 />
                </Button>
              </DangerConfirm>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

function MFAPanel({
  status,
  loading,
  setNotice,
  onError,
  refresh,
}: {
  status?: MFAStatus;
  loading: boolean;
  setNotice: (notice: AccountNotice) => void;
  onError: (error: unknown, fallback: string) => void;
  refresh: () => void;
}) {
  const [currentCode, setCurrentCode] = useState("");
  const [verifyCode, setVerifyCode] = useState("");
  const [disableCode, setDisableCode] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [enrollment, setEnrollment] = useState<MFAEnrollResponse | null>(null);
  const enroll = useMutation({
    mutationFn: () => apiPost<MFAEnrollResponse>("/auth/mfa/enroll", status?.enabled ? { code: currentCode } : {}),
    onSuccess: (data) => {
      setEnrollment(data);
      setNotice({ tone: "success", text: "MFA登録を開始しました。確認コードを入力してください。" });
      refresh();
    },
    onError: (error) => onError(error, "MFA登録を開始できませんでした"),
  });
  const verify = useMutation({
    mutationFn: () => apiPost<{ status: string }>("/auth/mfa/verify", { code: verifyCode }),
    onSuccess: () => {
      setEnrollment(null);
      setVerifyCode("");
      setNotice({ tone: "success", text: "MFAを有効化しました。" });
      refresh();
    },
    onError: (error) => onError(error, "MFAを有効化できませんでした"),
  });
  const disable = useMutation({
    mutationFn: () => apiPost<{ status: string }>("/auth/mfa/disable", { code: disableCode }),
    onSuccess: () => {
      setDisableCode("");
      setNotice({ tone: "success", text: "MFAを無効化しました。" });
      refresh();
    },
    onError: (error) => onError(error, "MFAを無効化できませんでした"),
  });
  const regenerate = useMutation({
    mutationFn: () => apiPost<{ recovery_codes: string[] }>("/auth/recovery-codes/regenerate", { code: recoveryCode }),
    onSuccess: (data) => {
      setEnrollment({ method: "totp", secret: "", provisioning_uri: "", recovery_codes: data.recovery_codes });
      setRecoveryCode("");
      setNotice({ tone: "success", text: "リカバリーコードを再発行しました。" });
      refresh();
    },
    onError: (error) => onError(error, "リカバリーコードを再発行できませんでした"),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <ShieldCheck className="size-5" />
          多要素認証
        </CardTitle>
        <CardDescription>確認コードとリカバリーコードでログインを保護します。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={status?.enabled ? "default" : "secondary"}>{loading ? "確認中" : status?.enabled ? "有効" : "無効"}</Badge>
          <span className="text-sm text-muted-foreground">Policy {status?.policy_mode || "-"}</span>
          {status?.required ? <Badge variant="outline">必須</Badge> : null}
        </div>
        {status?.enabled ? <Input inputMode="numeric" placeholder="現在のMFAコード" value={currentCode} onChange={(event) => setCurrentCode(event.target.value)} /> : null}
        <Button variant="outline" className="w-full" onClick={() => enroll.mutate()} disabled={enroll.isPending || (status?.enabled && currentCode.length < 6)}>
          MFA登録を開始
        </Button>
        {enrollment ? (
          <div className="space-y-3 rounded-md border p-3">
            {enrollment.secret ? <Input readOnly value={enrollment.secret} aria-label="TOTP secret" /> : null}
            {enrollment.provisioning_uri ? <Textarea readOnly value={enrollment.provisioning_uri} rows={3} aria-label="Provisioning URI" /> : null}
            {enrollment.recovery_codes?.length ? <Textarea readOnly value={enrollment.recovery_codes.join("\n")} rows={6} aria-label="Recovery codes" /> : null}
            {enrollment.secret ? (
              <div className="flex gap-2">
                <Input inputMode="numeric" placeholder="確認コード" value={verifyCode} onChange={(event) => setVerifyCode(event.target.value)} />
                <Button onClick={() => verify.mutate()} disabled={verifyCode.length < 6 || verify.isPending}>
                  有効化
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
        {status?.enabled ? (
          <div className="grid gap-2 md:grid-cols-2">
            <div className="flex gap-2">
              <Input inputMode="numeric" placeholder="MFAコード" value={recoveryCode} onChange={(event) => setRecoveryCode(event.target.value)} />
              <Button variant="outline" onClick={() => regenerate.mutate()} disabled={recoveryCode.length < 6 || regenerate.isPending}>
                再発行
              </Button>
            </div>
            <div className="flex gap-2">
              <Input inputMode="numeric" placeholder="MFAコード" value={disableCode} onChange={(event) => setDisableCode(event.target.value)} />
              <Button variant="destructive" onClick={() => disable.mutate()} disabled={disableCode.length < 6 || disable.isPending}>
                無効化
              </Button>
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function PasskeyPanel({
  passkeys,
  loading,
  username,
  timezone,
  setNotice,
  onError,
  refresh,
}: {
  passkeys: PasskeyCredential[];
  loading: boolean;
  username: string;
  timezone?: string;
  setNotice: (notice: AccountNotice) => void;
  onError: (error: unknown, fallback: string) => void;
  refresh: () => void;
}) {
  const [name, setName] = useState("メイン端末");
  const register = useMutation({
    mutationFn: async () => {
      if (typeof window === "undefined" || !("PublicKeyCredential" in window)) {
        throw new Error("passkey unsupported");
      }
      const start = await apiPost<PasskeyRegistrationStart>("/auth/passkeys/register/start", { display_name: username || name });
      const credential = await navigator.credentials.create({ publicKey: publicKeyCreationOptionsFromJSON(start.public_key) });
      if (!credential || !(credential instanceof PublicKeyCredential)) {
        throw new Error("passkey creation cancelled");
      }
      return apiPost<PasskeyCredential>("/auth/passkeys/register/finish", {
        registration_token: start.registration_token,
        name: name.trim() || "Passkey",
        credential: publicKeyCredentialToJSON(credential),
      });
    },
    onSuccess: () => {
      setNotice({ tone: "success", text: "Passkeyを登録しました。" });
      refresh();
    },
    onError: (error) => onError(error, "Passkeyを登録できませんでした"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => apiDelete<void>(`/auth/passkeys/${encodeURIComponent(id)}`),
    onSuccess: () => {
      setNotice({ tone: "success", text: "Passkeyを削除しました。" });
      refresh();
    },
    onError: (error) => onError(error, "Passkeyを削除できませんでした"),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <KeyRound className="size-5" />
          Passkey
        </CardTitle>
        <CardDescription>端末の生体認証やセキュリティキーを登録します。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex gap-2">
          <Input placeholder="Passkey名" value={name} onChange={(event) => setName(event.target.value)} />
          <Button onClick={() => register.mutate()} disabled={register.isPending}>
            登録
          </Button>
        </div>
        <div className="space-y-2">
          {passkeys.length === 0 ? <div className="text-sm text-muted-foreground">{loading ? "読み込み中" : "登録済みPasskeyはありません。"}</div> : null}
          {passkeys.map((passkey) => (
            <div key={passkey.id} className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{passkey.name || "Passkey"}</div>
                <div className="text-xs text-muted-foreground">Last used {passkey.last_used_at ? formatDateTime(passkey.last_used_at, timezone) : "-"}</div>
              </div>
              <DangerConfirm title="Passkeyを削除" description="このPasskeyではログインできなくなります。" onConfirm={() => remove.mutate(passkey.id)} actionLabel="削除">
                <Button variant="outline" size="icon-sm" aria-label="Passkeyを削除">
                  <Trash2 />
                </Button>
              </DangerConfirm>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

type PublicKeyCredentialUserEntityJSON = Omit<PublicKeyCredentialUserEntity, "id"> & { id: string };
type PublicKeyCredentialDescriptorJSON = Omit<PublicKeyCredentialDescriptor, "id"> & { id: string };
type PublicKeyCredentialCreationOptionsJSON = Omit<PublicKeyCredentialCreationOptions, "challenge" | "user" | "excludeCredentials"> & {
  challenge: string;
  user: PublicKeyCredentialUserEntityJSON;
  excludeCredentials?: PublicKeyCredentialDescriptorJSON[];
};

function publicKeyCreationOptionsFromJSON(input: Record<string, unknown>): PublicKeyCredentialCreationOptions {
  const options = input as Partial<PublicKeyCredentialCreationOptionsJSON>;
  const user = options.user || { id: "", name: "", displayName: "" };
  return {
    ...options,
    challenge: base64URLToBuffer(String(options.challenge || "")),
    user: { ...user, id: base64URLToBuffer(String(user.id || "")) },
    excludeCredentials: Array.isArray(options.excludeCredentials)
      ? options.excludeCredentials.map((credential) => ({ ...credential, id: base64URLToBuffer(String(credential.id || "")) }))
      : undefined,
  } as PublicKeyCredentialCreationOptions;
}

function publicKeyCredentialToJSON(credential: PublicKeyCredential) {
  const response = credential.response as AuthenticatorAttestationResponse & { getTransports?: () => string[] };
  return {
    id: credential.id,
    type: credential.type,
    rawId: bufferToBase64URL(credential.rawId),
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      attestationObject: bufferToBase64URL(response.attestationObject),
      transports: response.getTransports ? response.getTransports() : undefined,
    },
  };
}

function base64URLToBuffer(value: string): ArrayBuffer {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
  const binary = window.atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes.buffer;
}

function bufferToBase64URL(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return window.btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function providerLabel(value: string) {
  if (!value) return "OAuth";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function formatDateTime(value: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { dateStyle: "short", timeStyle: "short" });
}
