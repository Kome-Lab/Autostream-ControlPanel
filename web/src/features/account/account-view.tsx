"use client";

import { useEffect, useRef, useState } from "react";
import Image from "next/image";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Camera, CheckCircle2, Copy, KeyRound, Link2, Mail, Plus, QrCode, RefreshCcw, Save, ShieldCheck, ShieldOff, Trash2, Upload, UserCog, X } from "lucide-react";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { AccountAvatar } from "@/components/ui/account-avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { APIError, apiDelete, apiGet, apiPost, apiPut, apiPutBinary } from "@/lib/api/client";
import { qrCodeDataURL } from "@/lib/qr-code";
import { useAppSettings, useCurrentUser } from "@/features/queries";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { passkeyRegistrationCredentialToJSON, passkeysSupported, publicKeyCreationOptionsFromJSON } from "@/lib/passkeys";
import type { MFAEnrollResponse, MFAStatus, OAuthLinkStartResponse, OAuthLoginProvider, OAuthUserLink, PasskeyCredential, PasskeyRegistrationStart } from "@/types/domain";

type AccountNotice = { tone: "success" | "error"; text: string } | null;

type AvatarResponse = {
  avatar_url: string;
  content_type: string;
  size_bytes: number;
  updated_at: string;
};

const maxAvatarBytes = 768 * 1024;
const minAvatarDimension = 32;
const maxAvatarDimension = 2048;

export function AccountView() {
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const mfaStatus = useQuery({ queryKey: ["auth", "mfa", "status"], queryFn: () => apiGet<MFAStatus>("/auth/mfa/status") });
  const passkeys = useQuery({ queryKey: ["auth", "passkeys"], queryFn: () => apiGet<PasskeyCredential[]>("/auth/passkeys") });
  const oauthLinks = useQuery({ queryKey: ["auth", "oauth-links"], queryFn: () => apiGet<OAuthUserLink[]>("/auth/oauth-links") });
  const oauthProviders = useQuery({ queryKey: ["auth", "oauth", "providers"], queryFn: () => apiGet<OAuthLoginProvider[]>("/auth/oauth/providers") });
  const [notice, setNotice] = useState<AccountNotice>(null);
  const user = currentUser.data?.user;
  const username = user?.username || "-";
  const roles = user?.roles || [];

  const showError = (error: unknown, fallback: string) => {
    const code = error instanceof APIError ? error.code : "";
    setNotice({ tone: "error", text: code ? `${fallback} (${code})` : fallback });
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-normal">アカウント設定</h1>
          <p className="mt-1 text-sm text-muted-foreground">個人情報とログイン時のセキュリティを管理します。</p>
        </div>
        <Badge variant="outline" className="gap-2"><UserCog />個人アカウント</Badge>
      </div>

      {notice ? (
        <div role="status" aria-live="polite" className={notice.tone === "success" ? "rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-200" : "rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200"}>
          {notice.text}
        </div>
      ) : null}

      <Card>
        <CardContent className="grid gap-5 md:grid-cols-[minmax(0,1fr)_minmax(320px,0.7fr)] md:items-center">
          <div className="flex min-w-0 items-center gap-4">
            <AccountAvatar name={username} src={user?.avatar_url} alt={`${username}のアカウントアイコン`} className="size-20" sizes="80px" />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <div className="truncate text-xl font-semibold">{username}</div>
                <Badge className={user?.status === "active" ? "border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300" : ""} variant="outline">
                  <CheckCircle2 />{accountStatusLabel(user?.status)}
                </Badge>
              </div>
              <div className="mt-1 truncate text-sm text-muted-foreground">{user?.email || "メールアドレス未設定"}</div>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {roles.length ? roles.map((role) => <Badge key={role} variant="secondary">{roleLabel(role)}</Badge>) : <Badge variant="secondary">ロール未設定</Badge>}
              </div>
            </div>
          </div>
          <div className="grid grid-cols-3 divide-x rounded-md border bg-muted/20">
            <AccountSummaryMetric label="MFA" value={mfaStatus.isLoading ? "確認中" : mfaStatus.data?.enabled ? "有効" : "無効"} />
            <AccountSummaryMetric label="Passkey" value={`${passkeys.data?.length || 0}件`} />
            <AccountSummaryMetric label="外部ログイン" value={`${oauthLinks.data?.length || 0}件`} />
          </div>
        </CardContent>
      </Card>

      <Tabs defaultValue="profile" className="gap-4">
        <TabsList variant="line" className="h-auto w-full justify-start border-b pb-1">
          <TabsTrigger value="profile" className="min-w-32 flex-none"><UserCog />プロフィール</TabsTrigger>
          <TabsTrigger value="security" className="min-w-32 flex-none"><ShieldCheck />セキュリティ</TabsTrigger>
        </TabsList>
        <TabsContent value="profile">
          <div className="grid gap-4 xl:grid-cols-[minmax(300px,0.75fr)_minmax(0,1.25fr)]">
            <AvatarPanel
              username={username}
              currentAvatarURL={user?.avatar_url}
              setNotice={setNotice}
              onError={showError}
              refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "me"] })}
            />
            <EmailPanel
              key={user?.email || ""}
              currentEmail={user?.email || ""}
              links={oauthLinks.data || []}
              providers={oauthProviders.data || []}
              loading={oauthLinks.isLoading || oauthProviders.isLoading}
              setNotice={setNotice}
              onUpdated={() => queryClient.invalidateQueries({ queryKey: ["auth", "me"] })}
              onDeleted={() => queryClient.invalidateQueries({ queryKey: ["auth", "oauth-links"] })}
              onError={showError}
            />
          </div>
        </TabsContent>
        <TabsContent value="security">
          <div className="grid items-start gap-4 xl:grid-cols-2 2xl:grid-cols-3">
            <PasswordPanel setNotice={setNotice} onError={showError} />
            <MFAPanel status={mfaStatus.data} loading={mfaStatus.isLoading} setNotice={setNotice} onError={showError} refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "mfa", "status"] })} />
            <PasskeyPanel
              passkeys={passkeys.data || []}
              loading={passkeys.isLoading}
              username={username}
              timezone={appSettings.data?.timezone}
              setNotice={setNotice}
              onError={showError}
              refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "passkeys"] })}
            />
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function AccountSummaryMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 px-2 py-3 text-center sm:px-3">
      <div className="truncate text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 truncate text-sm font-semibold">{value}</div>
    </div>
  );
}

function AvatarPanel({
  username,
  currentAvatarURL,
  setNotice,
  onError,
  refresh,
}: {
  username: string;
  currentAvatarURL?: string;
  setNotice: (notice: AccountNotice) => void;
  onError: (error: unknown, fallback: string) => void;
  refresh: () => void;
}) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [previewURL, setPreviewURL] = useState("");
  const [dimensions, setDimensions] = useState<{ width: number; height: number } | null>(null);

  useEffect(() => () => {
    if (previewURL) URL.revokeObjectURL(previewURL);
  }, [previewURL]);

  const clearSelection = () => {
    setSelectedFile(null);
    setDimensions(null);
    setPreviewURL("");
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const upload = useMutation({
    mutationFn: (file: File) => apiPutBinary<AvatarResponse>("/auth/avatar", file),
    onSuccess: async () => {
      clearSelection();
      setNotice({ tone: "success", text: "アカウントアイコンを更新しました。" });
      await refresh();
    },
    onError: (error) => onError(error, "アカウントアイコンを更新できませんでした"),
  });
  const remove = useMutation({
    mutationFn: () => apiDelete<void>("/auth/avatar"),
    onSuccess: async () => {
      clearSelection();
      setNotice({ tone: "success", text: "アカウントアイコンを削除しました。" });
      await refresh();
    },
    onError: (error) => onError(error, "アカウントアイコンを削除できませんでした"),
  });

  const selectFile = async (file?: File) => {
    if (!file) return;
    clearSelection();
    if (!(["image/jpeg", "image/png"] as string[]).includes(file.type)) {
      setNotice({ tone: "error", text: "JPEGまたはPNG画像を選択してください。" });
      return;
    }
    if (file.size > maxAvatarBytes) {
      setNotice({ tone: "error", text: "画像は768 KB以下にしてください。" });
      return;
    }
    try {
      const nextDimensions = await readImageDimensions(file);
      if (nextDimensions.width < minAvatarDimension || nextDimensions.height < minAvatarDimension || nextDimensions.width > maxAvatarDimension || nextDimensions.height > maxAvatarDimension) {
        setNotice({ tone: "error", text: "画像の縦横は32〜2048 pxにしてください。" });
        return;
      }
      const nextURL = URL.createObjectURL(file);
      setPreviewURL(nextURL);
      setSelectedFile(file);
      setDimensions(nextDimensions);
      setNotice(null);
    } catch {
      setNotice({ tone: "error", text: "画像を読み込めませんでした。別の画像を選択してください。" });
    }
  };

  return (
    <Card className="h-fit">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg"><Camera className="size-5" />アカウントアイコン</CardTitle>
        <CardDescription>ヘッダーとアカウントメニューに表示する画像です。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-col items-center gap-4 rounded-md border bg-muted/20 p-4 text-center sm:flex-row sm:text-left">
          <AccountAvatar name={username} src={previewURL || currentAvatarURL} alt={previewURL ? "選択したアカウントアイコンのプレビュー" : `${username}のアカウントアイコン`} className="size-24" sizes="96px" />
          <div className="min-w-0 flex-1 space-y-2">
            <div>
              <div className="text-sm font-medium">{previewURL ? "変更後のプレビュー" : currentAvatarURL ? "現在のアイコン" : "アイコン未設定"}</div>
              <div className="mt-1 text-xs text-muted-foreground">JPEG / PNG、768 KB以下、32〜2048 px</div>
            </div>
            {selectedFile ? (
              <div className="text-xs text-muted-foreground">
                <div className="truncate font-medium text-foreground">{selectedFile.name}</div>
                <div>{formatFileSize(selectedFile.size)}{dimensions ? ` / ${dimensions.width}×${dimensions.height} px` : ""}</div>
              </div>
            ) : null}
          </div>
        </div>
        <input
          ref={fileInputRef}
          type="file"
          accept="image/png,image/jpeg"
          className="sr-only"
          aria-label="アカウントアイコン画像を選択"
          onChange={(event) => void selectFile(event.target.files?.[0])}
        />
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => fileInputRef.current?.click()} disabled={upload.isPending || remove.isPending}>
            <Upload />画像を選択
          </Button>
          {selectedFile ? (
            <>
              <Button type="button" onClick={() => upload.mutate(selectedFile)} disabled={upload.isPending}>
                <Save />{upload.isPending ? "保存中" : "この画像を保存"}
              </Button>
              <Button type="button" variant="ghost" size="icon" aria-label="画像の選択を取り消す" onClick={clearSelection} disabled={upload.isPending}>
                <X />
              </Button>
            </>
          ) : null}
          {!selectedFile && currentAvatarURL ? (
            <DangerConfirm title="アカウントアイコンを削除" description="画像を削除すると、ユーザー名の先頭文字が表示されます。" onConfirm={() => remove.mutate()} actionLabel="削除">
              <Button type="button" variant="outline" disabled={remove.isPending}><Trash2 />削除</Button>
            </DangerConfirm>
          ) : null}
        </div>
      </CardContent>
    </Card>
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
  loading,
  setNotice,
  onUpdated,
  onDeleted,
  onError,
}: {
  currentEmail: string;
  links: OAuthUserLink[];
  providers: OAuthLoginProvider[];
  loading: boolean;
  setNotice: (notice: AccountNotice) => void;
  onUpdated: () => void;
  onDeleted: () => void;
  onError: (error: unknown, fallback: string) => void;
}) {
  const [email, setEmail] = useState(currentEmail);
  const updateEmail = useMutation({
    mutationFn: () => apiPut<{ status: string; target?: string }>("/auth/email", { email: email.trim() }),
    onSuccess: (response) => {
      if (response.status === "unchanged") {
        setNotice({ tone: "success", text: "メールアドレスは変更されていません。" });
      } else {
        setNotice({ tone: "success", text: response.target ? `確認メールを送信しました。宛先: ${response.target}` : "確認メールを送信しました。" });
      }
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
            <Button onClick={() => updateEmail.mutate()} disabled={updateEmail.isPending || email.trim() === currentEmail.trim() || !email.trim()}>
              確認メール送信
            </Button>
          </div>
          <div className="text-xs text-muted-foreground">変更すると新しい宛先へ確認メールを送信します。メール内のワンタイムURLを開くまで変更は完了しません。</div>
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
          <div className="text-sm font-medium">連携済みログイン</div>
          {links.length === 0 ? <div className="text-sm text-muted-foreground">{loading ? "読み込み中" : "連携済みログインはありません。"}</div> : null}
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
  const [copiedRecoveryCodes, setCopiedRecoveryCodes] = useState(false);
  const policyMode = status?.policy_mode || "";
  const totpEnrollmentAvailable = Boolean(status?.available && policyMode !== "passkey");
  const recoveryCodes = enrollment?.recovery_codes || [];
  const qrImage = enrollment?.provisioning_uri ? qrCodeDataURL(enrollment.provisioning_uri) : "";
  const registrationInProgress = Boolean(enrollment?.secret);
  const recoveryOnlyResult = Boolean(enrollment && !enrollment.secret && recoveryCodes.length > 0);
  const enroll = useMutation({
    mutationFn: () => apiPost<MFAEnrollResponse>("/auth/mfa/enroll", status?.enabled ? { code: currentCode } : {}),
    onSuccess: (data) => {
      setEnrollment(data);
      setCopiedRecoveryCodes(false);
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
      setCopiedRecoveryCodes(false);
      setRecoveryCode("");
      setNotice({ tone: "success", text: "リカバリーコードを再発行しました。" });
      refresh();
    },
    onError: (error) => onError(error, "リカバリーコードを再発行できませんでした"),
  });
  const canStartEnrollment = totpEnrollmentAvailable && !loading && !enroll.isPending && (!status?.enabled || currentCode.length >= 6);
  const copyRecoveryCodes = async () => {
    if (recoveryCodes.length === 0 || typeof navigator === "undefined") return;
    await navigator.clipboard.writeText(recoveryCodes.join("\n"));
    setCopiedRecoveryCodes(true);
  };

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
          <span className="text-sm text-muted-foreground">認証方式 {mfaPolicyLabel(policyMode)}</span>
          {status?.required ? <Badge variant="outline">必須</Badge> : null}
          {status?.pending_enrollment ? <Badge variant="outline">確認待ち</Badge> : null}
          {status?.recovery_code_count !== undefined && status.enabled ? <Badge variant="secondary">リカバリーコード残り {status.recovery_code_count}</Badge> : null}
        </div>
        {!loading && !totpEnrollmentAvailable ? <div className="rounded-md border bg-muted/35 px-3 py-2 text-sm text-muted-foreground">{mfaUnavailableMessage(status)}</div> : null}
        {totpEnrollmentAvailable && status?.enabled ? (
          <div className="space-y-2 rounded-md border p-3">
            <label className="text-sm font-medium">TOTPを再登録する場合の本人確認コード</label>
            <Input inputMode="numeric" placeholder="現在のMFAコード" value={currentCode} onChange={(event) => setCurrentCode(event.target.value)} />
            <p className="text-xs text-muted-foreground">再登録すると新しいQRコードとリカバリーコードを発行します。現在のMFAコードが必要です。</p>
          </div>
        ) : null}
        {totpEnrollmentAvailable ? (
          <Button variant="outline" className="w-full" onClick={() => enroll.mutate()} disabled={!canStartEnrollment}>
            <QrCode className="size-4" />
            {status?.enabled ? "TOTPを再登録" : status?.pending_enrollment ? "TOTP登録をやり直す" : "TOTP登録を開始"}
          </Button>
        ) : null}
        {enrollment ? (
          <div className="space-y-4 rounded-md border p-3">
            {registrationInProgress ? (
              <div className="grid gap-3 md:grid-cols-[180px_1fr]">
                <div className="flex min-h-44 items-center justify-center rounded-md border bg-white p-3">
                  {qrImage ? <Image src={qrImage} alt="TOTP登録用QRコード" width={160} height={160} unoptimized /> : <div className="text-center text-sm text-muted-foreground">QRコードを生成できませんでした。手動入力キーを使ってください。</div>}
                </div>
                <div className="space-y-3">
                  <div>
                    <div className="text-sm font-medium">1. 認証アプリでQRコードを読み取る</div>
                    <p className="mt-1 text-xs text-muted-foreground">Google Authenticator、1Password、Microsoft AuthenticatorなどのTOTP対応アプリで読み取ります。</p>
                  </div>
                  {enrollment.secret ? (
                    <div className="space-y-1">
                      <label className="text-xs font-medium text-muted-foreground">手動入力キー</label>
                      <Input readOnly value={enrollment.secret} aria-label="TOTP secret" className="font-mono" />
                    </div>
                  ) : null}
                  {enrollment.provisioning_uri ? <Textarea readOnly value={enrollment.provisioning_uri} rows={2} aria-label="Provisioning URI" className="font-mono text-xs" /> : null}
                </div>
              </div>
            ) : null}
            {recoveryCodes.length ? <RecoveryCodesBlock codes={recoveryCodes} copied={copiedRecoveryCodes} onCopy={copyRecoveryCodes} recoveryOnly={recoveryOnlyResult} /> : null}
            {registrationInProgress ? (
              <div className="space-y-2 rounded-md border bg-muted/20 p-3">
                <label className="text-sm font-medium">2. アプリに表示された6桁コードで有効化</label>
                <div className="flex flex-col gap-2 sm:flex-row">
                  <Input inputMode="numeric" placeholder="確認コード" value={verifyCode} onChange={(event) => setVerifyCode(event.target.value)} />
                  <Button onClick={() => verify.mutate()} disabled={verifyCode.length < 6 || verify.isPending}>
                    有効化
                  </Button>
                </div>
              </div>
            ) : null}
          </div>
        ) : null}
        {status?.enabled ? (
          <div className="grid gap-3 md:grid-cols-2">
            <div className="space-y-2 rounded-md border p-3">
              <div className="flex items-center gap-2 text-sm font-medium">
                <RefreshCcw className="size-4" />
                リカバリーコード再発行
              </div>
              <p className="text-xs text-muted-foreground">新しいリカバリーコードを発行します。発行後、古いリカバリーコードは使えません。</p>
              <Input inputMode="numeric" placeholder="現在のMFAコード" value={recoveryCode} onChange={(event) => setRecoveryCode(event.target.value)} />
              <Button variant="outline" className="w-full" onClick={() => regenerate.mutate()} disabled={recoveryCode.length < 6 || regenerate.isPending}>
                リカバリーコードを再発行
              </Button>
            </div>
            <div className="space-y-2 rounded-md border border-red-200 bg-red-50/50 p-3">
              <div className="flex items-center gap-2 text-sm font-medium text-red-700">
                <ShieldOff className="size-4" />
                MFAを無効化
              </div>
              <p className="text-xs text-red-700/80">無効化すると次回ログイン時のTOTP確認が不要になります。現在のMFAコードで確認してください。</p>
              <Input inputMode="numeric" placeholder="現在のMFAコード" value={disableCode} onChange={(event) => setDisableCode(event.target.value)} />
              <DangerConfirm title="MFAを無効化しますか" description="このアカウントの多要素認証を無効化します。必要な場合は後で再登録してください。" onConfirm={() => disable.mutate()} actionLabel="MFAを無効化">
                <Button variant="destructive" className="w-full" disabled={disableCode.length < 6 || disable.isPending}>
                  MFAを無効化
                </Button>
              </DangerConfirm>
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function RecoveryCodesBlock({ codes, copied, onCopy, recoveryOnly }: { codes: string[]; copied: boolean; onCopy: () => void; recoveryOnly: boolean }) {
  return (
    <div className="space-y-3 rounded-md border border-amber-200 bg-amber-50 p-3 text-amber-950">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <div className="text-sm font-semibold">{recoveryOnly ? "再発行されたリカバリーコード" : "リカバリーコード"}</div>
          <p className="mt-1 text-xs text-amber-800">ここに表示されているコードが保存対象です。MFAアプリを使えない時のログインに使います。表示は今回だけです。</p>
        </div>
        <Button type="button" variant="outline" size="sm" className="bg-white" onClick={onCopy}>
          <Copy className="size-4" />
          {copied ? "コピー済み" : "まとめてコピー"}
        </Button>
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        {codes.map((code) => (
          <code key={code} className="rounded-md border bg-white px-3 py-2 text-sm font-semibold tracking-wide text-foreground">
            {code}
          </code>
        ))}
      </div>
    </div>
  );
}

function mfaPolicyLabel(mode: string) {
  switch (mode) {
    case "totp":
      return "TOTP";
    case "passkey":
      return "Passkey";
    case "disabled":
      return "無効";
    default:
      return mode || "-";
  }
}

function mfaUnavailableMessage(status?: MFAStatus) {
  if (!status?.available) {
    return "MFAストアが構成されていないため、TOTP登録は利用できません。";
  }
  if (status.policy_mode === "passkey") {
    return "現在のMFA方式はPasskeyです。Passkey欄から端末やセキュリティキーを登録してください。";
  }
  if (status.policy_mode === "disabled") return "このアカウントでは任意でTOTPを登録できます。登録後のログインでは2FAが必要になります。";
  return "現在のMFAポリシーではTOTP登録を利用できません。";
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
      if (!passkeysSupported()) {
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
        credential: passkeyRegistrationCredentialToJSON(credential),
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
                <div className="text-xs text-muted-foreground">最終使用 {passkey.last_used_at ? formatDateTime(passkey.last_used_at, timezone) : "-"}</div>
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

function providerLabel(value: string) {
  if (!value) return "OAuth";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function formatDateTime(value: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { dateStyle: "short", timeStyle: "short" });
}

function accountStatusLabel(status?: string) {
  switch (status) {
    case "active":
      return "有効";
    case "locked":
      return "ロック中";
    case "disabled":
      return "無効";
    case "pending_password_change":
      return "初回設定待ち";
    default:
      return status || "確認中";
  }
}

function roleLabel(role: string) {
  switch (role) {
    case "super_admin":
      return "システム管理者";
    case "admin":
      return "管理者";
    case "operator":
      return "配信担当者";
    case "viewer":
      return "閲覧者";
    default:
      return role.replaceAll("_", " ");
  }
}

function readImageDimensions(file: File) {
  return new Promise<{ width: number; height: number }>((resolve, reject) => {
    const objectURL = URL.createObjectURL(file);
    const image = new window.Image();
    image.onload = () => {
      URL.revokeObjectURL(objectURL);
      resolve({ width: image.naturalWidth, height: image.naturalHeight });
    };
    image.onerror = () => {
      URL.revokeObjectURL(objectURL);
      reject(new Error("invalid image"));
    };
    image.src = objectURL;
  });
}

function formatFileSize(size: number) {
  if (size < 1024) return `${size} B`;
  return `${Math.round(size / 1024)} KB`;
}
