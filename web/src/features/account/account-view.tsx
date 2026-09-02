"use client";

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import Image from "next/image";
import { usePathname, useRouter } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Camera, CheckCircle2, KeyRound, Link2, Mail, Palette, Plus, QrCode, RefreshCcw, Save, ShieldCheck, ShieldOff, Trash2, Upload, UserCog, X } from "lucide-react";
import { DangerConfirm } from "@/components/admin/danger-confirm";
import { useI18n } from "@/components/admin/i18n-provider";
import { OneTimeSecretReveal } from "@/components/foundation/secrets/one-time-secret-reveal";
import { AccountAvatar } from "@/components/ui/account-avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { APIError, apiDelete, apiGet, apiPost, apiPut, apiPutBinary } from "@/lib/api/client";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import { createOneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/lifecycle-owner";
import { qrCodeDataURL } from "@/lib/qr-code";
import { useAppSettings, useCurrentUser } from "@/features/queries";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { passkeyRegistrationCredentialToJSON, passkeysSupported, publicKeyCreationOptionsFromJSON } from "@/lib/passkeys";
import type { CurrentUser, MFAEnrollResponse, MFAStatus, OAuthLinkStartResponse, OAuthLoginProvider, OAuthUserLink, PasskeyCredential, PasskeyRegistrationStart } from "@/types/domain";
import { AppearancePanel } from "@/features/account/appearance-panel";
import { AccountActionConfirmation } from "@/features/account/account-action-confirmation";
import {
  createAccountActionController,
  type AccountActionController,
  type AccountActionIntent,
  type AccountAuthoritySnapshot,
} from "@/features/account/account-action-policy";
import { adoptAccountOneTimeOutput, type AccountOneTimeSecretValue } from "@/features/account/account-one-time-secret";
import { validatedOAuthRedirect } from "@/features/account/oauth-redirect-handoff";

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
  const { t } = useI18n();
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
  const accountResourceID = user?.id || "current-account";
  const authority = readAccountAuthority(queryClient);
  const actionController = useMemo(() => createAccountActionController({
    readAuthority: () => readAccountAuthority(queryClient),
  }), [queryClient]);
  const refreshAuthority = async () => {
    await currentUser.refetch();
    return readAccountAuthority(queryClient);
  };

  const showError = (error: unknown, fallback: string) => {
    const adapted = adaptAPIError(error);
    setNotice({ tone: "error", text: adapted.kind === "unknown" ? fallback : t(adapted.messageKey) });
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
          <TabsTrigger value="appearance" className="min-w-32 flex-none"><Palette />外観</TabsTrigger>
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
              actionController={actionController}
              authority={authority}
              refreshAuthority={refreshAuthority}
              accountResourceID={accountResourceID}
            />
          </div>
        </TabsContent>
        <TabsContent value="security">
          <div className="grid items-start gap-4 xl:grid-cols-2 2xl:grid-cols-3">
            <PasswordPanel setNotice={setNotice} actionController={actionController} authority={authority} refreshAuthority={refreshAuthority} accountResourceID={accountResourceID} />
            <MFAPanel status={mfaStatus.data} loading={mfaStatus.isLoading} username={username} sessionAvailable={Boolean(user)} setNotice={setNotice} refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "mfa", "status"] })} actionController={actionController} authority={authority} refreshAuthority={refreshAuthority} accountResourceID={accountResourceID} />
            <PasskeyPanel
              passkeys={passkeys.data || []}
              loading={passkeys.isLoading}
              username={username}
              timezone={appSettings.data?.timezone}
              setNotice={setNotice}
              refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "passkeys"] })}
              actionController={actionController}
              authority={authority}
              refreshAuthority={refreshAuthority}
              accountResourceID={accountResourceID}
            />
          </div>
        </TabsContent>
        <TabsContent value="appearance">
          <AppearancePanel />
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

function PasswordPanel({
  setNotice,
  actionController,
  authority,
  refreshAuthority,
  accountResourceID,
}: {
  setNotice: (notice: AccountNotice) => void;
  actionController: AccountActionController;
  authority: AccountAuthoritySnapshot;
  refreshAuthority: () => Promise<AccountAuthoritySnapshot>;
  accountResourceID: string;
}) {
  const router = useRouter();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const mismatch = newPassword !== "" && confirmPassword !== "" && newPassword !== confirmPassword;
  const intent: AccountActionIntent = { id: "AUTH-11", resourceId: accountResourceID };

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
        <AccountActionConfirmation
          controller={actionController}
          intent={intent}
          authority={authority}
          refreshAuthority={refreshAuthority}
          label="変更して再ログイン"
          className="w-full"
          disabled={!currentPassword || !newPassword || mismatch}
          handler={() => apiPost<{ status: string }>("/auth/change-password", { current_password: currentPassword, new_password: newPassword })}
          onSucceeded={() => {
            setCurrentPassword("");
            setNewPassword("");
            setConfirmPassword("");
            setNotice({ tone: "success", text: "パスワードを変更しました。再ログインしてください。" });
            window.setTimeout(() => router.push("/login"), 900);
          }}
          onOutcomeUnknown={() => setNotice({ tone: "error", text: "変更結果を確認できません。再送せず、再ログインまたは監査ログで確認してください。" })}
        />
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
  actionController,
  authority,
  refreshAuthority,
  accountResourceID,
}: {
  currentEmail: string;
  links: OAuthUserLink[];
  providers: OAuthLoginProvider[];
  loading: boolean;
  setNotice: (notice: AccountNotice) => void;
  onUpdated: () => void;
  onDeleted: () => void;
  actionController: AccountActionController;
  authority: AccountAuthoritySnapshot;
  refreshAuthority: () => Promise<AccountAuthoritySnapshot>;
  accountResourceID: string;
}) {
  const [email, setEmail] = useState(currentEmail);
  const outcomeUnknown = () => setNotice({ tone: "error", text: "操作結果を確認できません。再送せず、アカウント状態または監査ログを確認してください。" });
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
            <AccountActionConfirmation
              controller={actionController}
              intent={{ id: "AUTH-12", resourceId: accountResourceID }}
              authority={authority}
              refreshAuthority={refreshAuthority}
              label="確認メール送信"
              variant="default"
              disabled={email.trim() === currentEmail.trim() || !email.trim()}
              handler={() => apiPut<{ status: string; target?: string }>("/auth/email", { email: email.trim() })}
              onSucceeded={() => {
                setNotice({ tone: "success", text: "確認メールを送信しました。メール内のワンタイムURLを開くまで変更は完了しません。" });
                onUpdated();
              }}
              onOutcomeUnknown={outcomeUnknown}
            />
          </div>
          <div className="text-xs text-muted-foreground">変更すると新しい宛先へ確認メールを送信します。メール内のワンタイムURLを開くまで変更は完了しません。</div>
        </div>
        <div className="grid gap-2 sm:grid-cols-2">
          {providers.map((provider) => (
            <AccountActionConfirmation
              key={provider.id}
              controller={actionController}
              intent={{ id: "AUTH-13", resourceId: accountResourceID }}
              authority={authority}
              refreshAuthority={refreshAuthority}
              label={`${provider.name || providerLabel(provider.provider_type)}を連携`}
              icon={<Plus className="size-4" />}
              className="justify-start"
              handler={async () => {
                const data = await apiPost<OAuthLinkStartResponse>(`/auth/oauth-links/${encodeURIComponent(provider.id)}/start`, { redirect_after: "/admin/account/" });
                window.location.assign(validatedOAuthRedirect(data.authorization_url));
                return undefined;
              }}
              onOutcomeUnknown={outcomeUnknown}
            />
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
              <AccountActionConfirmation
                controller={actionController}
                intent={{ id: "AUTH-14", resourceId: accountResourceID }}
                authority={authority}
                refreshAuthority={refreshAuthority}
                label="OAuth連携を解除"
                icon={<Trash2 />}
                handler={() => apiDelete<{ status: string }>(`/auth/oauth-links/${encodeURIComponent(link.id)}`)}
                onSucceeded={() => {
                  setNotice({ tone: "success", text: "OAuth連携を解除しました。" });
                  onDeleted();
                }}
                onOutcomeUnknown={outcomeUnknown}
              />
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
  username,
  sessionAvailable,
  setNotice,
  refresh,
  actionController,
  authority,
  refreshAuthority,
  accountResourceID,
}: {
  status?: MFAStatus;
  loading: boolean;
  username: string;
  sessionAvailable: boolean;
  setNotice: (notice: AccountNotice) => void;
  refresh: () => void;
  actionController: AccountActionController;
  authority: AccountAuthoritySnapshot;
  refreshAuthority: () => Promise<AccountAuthoritySnapshot>;
  accountResourceID: string;
}) {
  const { t } = useI18n();
  const pathname = usePathname();
  const previousPathname = useRef(pathname);
  const [currentCode, setCurrentCode] = useState("");
  const [verifyCode, setVerifyCode] = useState("");
  const [disableCode, setDisableCode] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [registrationInProgress, setRegistrationInProgress] = useState(false);
  const [recoveryOnlyResult, setRecoveryOnlyResult] = useState(false);
  const [verifyPending, setVerifyPending] = useState(false);
  const [secretOwner] = useState(() => createOneTimeSecretLifecycleOwner<AccountOneTimeSecretValue>({
    epochNowMs: () => Date.now(),
    monotonicNowMs: () => Math.floor(typeof performance === "undefined" ? Date.now() : performance.now()),
    schedule: (callback, delayMs) => setTimeout(callback, delayMs),
    cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
  }));
  const secretSnapshot = useSyncExternalStore(secretOwner.subscribe, secretOwner.getSnapshot, secretOwner.getSnapshot);
  const policyMode = status?.policy_mode || "";
  const totpEnrollmentAvailable = Boolean(status?.available && policyMode !== "passkey");
  const canStartEnrollment = totpEnrollmentAvailable && !loading && (!status?.enabled || currentCode.length >= 6);
  const typedIntent = (id: "AUTH-15" | "AUTH-17" | "AUTH-18"): AccountActionIntent => ({ id, resourceId: accountResourceID, publicUsername: username });
  const outcomeUnknown = () => setNotice({ tone: "error", text: "MFA操作の結果を確認できません。再送せず、セッション状態または監査ログを確認してください。" });

  useEffect(() => () => { secretOwner.dispose(); }, [secretOwner]);
  useEffect(() => {
    if (!loading && !sessionAvailable) secretOwner.clearForSessionLoss();
  }, [loading, secretOwner, sessionAvailable]);
  useEffect(() => {
    if (previousPathname.current !== pathname) secretOwner.clearForNavigation();
    previousPathname.current = pathname;
  }, [pathname, secretOwner]);

  const adoptOneTimeOutput = (value: unknown) => {
    const adoption = adoptAccountOneTimeOutput(secretOwner, value);
    if (!adoption.adopted) throw new APIError("Invalid MFA one-time response.", 502, "invalid_mfa_one_time_response");
    return adoption.publicResult;
  };

  const verifyEnrollment = async () => {
    if (verifyPending) return;
    setVerifyPending(true);
    try {
      const refreshed = await refreshAuthority();
      const result = await actionController.execute(
        { id: "AUTH-16", resourceId: accountResourceID, authorityRevision: refreshed.revision },
        { confirmed: true },
        () => apiPost<{ status: string }>("/auth/mfa/verify", { code: verifyCode }),
      );
      if (result.kind === "succeeded") {
        setRegistrationInProgress(false);
        setVerifyCode("");
        setNotice({ tone: "success", text: "MFAを有効化しました。発行済みの情報は確認後に破棄してください。" });
        refresh();
      } else if (result.kind === "failed") {
        setNotice({ tone: "error", text: t(result.error.messageKey) });
      } else if (result.kind === "outcome_unknown") {
        outcomeUnknown();
      } else {
        setNotice({ tone: "error", text: "最新のセッションを確認できないため、MFA確認を送信しませんでした。" });
      }
    } finally {
      setVerifyPending(false);
    }
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
          <AccountActionConfirmation
            controller={actionController}
            intent={typedIntent("AUTH-15")}
            authority={authority}
            refreshAuthority={refreshAuthority}
            label={status?.enabled ? "TOTPを再登録" : status?.pending_enrollment ? "TOTP登録をやり直す" : "TOTP登録を開始"}
            icon={<QrCode className="size-4" />}
            className="w-full"
            disabled={!canStartEnrollment}
            handler={async () => adoptOneTimeOutput(await apiPost<MFAEnrollResponse>("/auth/mfa/enroll", status?.enabled ? { code: currentCode } : {}))}
            onSucceeded={(value) => {
              const result = value as { enrollmentPending?: boolean; recoveryCodeCount?: number };
              setRegistrationInProgress(result.enrollmentPending === true);
              setRecoveryOnlyResult(result.enrollmentPending !== true && Number(result.recoveryCodeCount) > 0);
              setNotice({ tone: "success", text: "MFA登録情報を受信しました。明示的に表示して確認してください。" });
              refresh();
            }}
            onOutcomeUnknown={outcomeUnknown}
          />
        ) : null}
        {secretSnapshot.generation > 0 ? (
          <div className="space-y-4 rounded-md border p-3">
            <OneTimeSecretReveal
              snapshot={secretSnapshot}
              translate={t}
              renderRevealedContent={() => {
                const revealed = secretOwner.readRevealedValue();
                const qrImage = revealed?.provisioningURI ? qrCodeDataURL(revealed.provisioningURI) : "";
                return revealed ? (
                  <div className="space-y-4">
                    {registrationInProgress ? (
                      <div className="grid gap-3 md:grid-cols-[180px_1fr]">
                        <div className="flex min-h-44 items-center justify-center rounded-md border bg-white p-3">
                          {qrImage ? <Image src={qrImage} alt="TOTP登録用QRコード" width={160} height={160} unoptimized /> : <div className="text-center text-sm text-muted-foreground">QRコードを生成できませんでした。手動入力キーを使ってください。</div>}
                        </div>
                        <div className="space-y-3">
                          <div>
                            <div className="text-sm font-medium">1. 認証アプリでQRコードを読み取る</div>
                            <p className="mt-1 text-xs text-muted-foreground">TOTP対応アプリで読み取ります。</p>
                          </div>
                          {revealed.mfaSecret ? <Input readOnly value={revealed.mfaSecret} aria-label="TOTP secret" className="font-mono" /> : null}
                          {revealed.provisioningURI ? <Textarea readOnly value={revealed.provisioningURI} rows={2} aria-label="Provisioning URI" className="font-mono text-xs" /> : null}
                        </div>
                      </div>
                    ) : null}
                    {revealed.recoveryCodes?.length ? <RecoveryCodesBlock codes={revealed.recoveryCodes} recoveryOnly={recoveryOnlyResult} /> : null}
                  </div>
                ) : <span />;
              }}
              canCopy
              onRevealIntent={() => { secretOwner.reveal(); }}
              onConcealIntent={() => { secretOwner.conceal(); }}
              onCopyIntent={() => { void secretOwner.copyWith((value) => navigator.clipboard.writeText([
                value.mfaSecret,
                value.provisioningURI,
                ...(value.recoveryCodes ?? []),
              ].filter((entry): entry is string => Boolean(entry)).join("\n"))); }}
              onAcknowledgeIntent={() => { secretOwner.acknowledge(); }}
              onDismissIntent={() => { secretOwner.dismiss(); }}
              onUnmountIntent={() => { secretOwner.dispose(); }}
            />
            {registrationInProgress ? (
              <div className="space-y-2 rounded-md border bg-muted/20 p-3">
                <label className="text-sm font-medium">2. アプリに表示された6桁コードで有効化</label>
                <div className="flex flex-col gap-2 sm:flex-row">
                  <Input inputMode="numeric" placeholder="確認コード" value={verifyCode} onChange={(event) => setVerifyCode(event.target.value)} />
                  <Button onClick={() => { void verifyEnrollment(); }} disabled={verifyCode.length < 6 || verifyPending}>
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
              <AccountActionConfirmation
                controller={actionController}
                intent={typedIntent("AUTH-18")}
                authority={authority}
                refreshAuthority={refreshAuthority}
                label="リカバリーコードを再発行"
                className="w-full"
                disabled={recoveryCode.length < 6}
                handler={async () => adoptOneTimeOutput(await apiPost<{ recovery_codes: string[] }>("/auth/recovery-codes/regenerate", { code: recoveryCode }))}
                onSucceeded={() => {
                  setRegistrationInProgress(false);
                  setRecoveryOnlyResult(true);
                  setRecoveryCode("");
                  setNotice({ tone: "success", text: "新しいリカバリーコードを受信しました。明示的に表示して確認してください。" });
                  refresh();
                }}
                onOutcomeUnknown={outcomeUnknown}
              />
            </div>
            <div className="space-y-2 rounded-md border border-red-200 bg-red-50/50 p-3">
              <div className="flex items-center gap-2 text-sm font-medium text-red-700">
                <ShieldOff className="size-4" />
                MFAを無効化
              </div>
              <p className="text-xs text-red-700/80">無効化すると次回ログイン時のTOTP確認が不要になります。現在のMFAコードで確認してください。</p>
              <Input inputMode="numeric" placeholder="現在のMFAコード" value={disableCode} onChange={(event) => setDisableCode(event.target.value)} />
              <AccountActionConfirmation
                controller={actionController}
                intent={typedIntent("AUTH-17")}
                authority={authority}
                refreshAuthority={refreshAuthority}
                label="MFAを無効化"
                variant="destructive"
                className="w-full"
                disabled={disableCode.length < 6}
                handler={() => apiPost<{ status: string }>("/auth/mfa/disable", { code: disableCode })}
                onSucceeded={() => {
                  secretOwner.dismiss();
                  setDisableCode("");
                  setRegistrationInProgress(false);
                  setRecoveryOnlyResult(false);
                  setNotice({ tone: "success", text: "MFAを無効化しました。" });
                  refresh();
                }}
                onOutcomeUnknown={outcomeUnknown}
              />
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function RecoveryCodesBlock({ codes, recoveryOnly }: { codes: readonly string[]; recoveryOnly: boolean }) {
  return (
    <div className="space-y-3 rounded-md border border-amber-200 bg-amber-50 p-3 text-amber-950">
      <div>
        <div className="text-sm font-semibold">{recoveryOnly ? "再発行されたリカバリーコード" : "リカバリーコード"}</div>
        <p className="mt-1 text-xs text-amber-800">MFAアプリを使えない時のログインに使います。この一時表示を確認後に破棄してください。</p>
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
  refresh,
  actionController,
  authority,
  refreshAuthority,
  accountResourceID,
}: {
  passkeys: PasskeyCredential[];
  loading: boolean;
  username: string;
  timezone?: string;
  setNotice: (notice: AccountNotice) => void;
  refresh: () => void;
  actionController: AccountActionController;
  authority: AccountAuthoritySnapshot;
  refreshAuthority: () => Promise<AccountAuthoritySnapshot>;
  accountResourceID: string;
}) {
  const [name, setName] = useState("メイン端末");
  const outcomeUnknown = () => setNotice({ tone: "error", text: "Passkey操作の結果を確認できません。再送せず、登録一覧または監査ログを確認してください。" });
  const registerPasskey = async () => {
      if (!passkeysSupported()) {
        throw new Error("passkey unsupported");
      }
      const start = await apiPost<PasskeyRegistrationStart>("/auth/passkeys/register/start", { display_name: username || name });
      const credential = await navigator.credentials.create({ publicKey: publicKeyCreationOptionsFromJSON(start.public_key) });
      if (!credential || !(credential instanceof PublicKeyCredential)) {
        throw new Error("passkey creation cancelled");
      }
      await apiPost<PasskeyCredential>("/auth/passkeys/register/finish", {
        registration_token: start.registration_token,
        name: name.trim() || "Passkey",
        credential: passkeyRegistrationCredentialToJSON(credential),
      });
      return undefined;
  };
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
          <AccountActionConfirmation
            controller={actionController}
            intent={{ id: "AUTH-19", resourceId: accountResourceID }}
            authority={authority}
            refreshAuthority={refreshAuthority}
            label="登録"
            variant="default"
            disabled={!name.trim()}
            handler={registerPasskey}
            onSucceeded={() => {
              setNotice({ tone: "success", text: "Passkeyを登録しました。" });
              refresh();
            }}
            onOutcomeUnknown={outcomeUnknown}
          />
        </div>
        <div className="space-y-2">
          {passkeys.length === 0 ? <div className="text-sm text-muted-foreground">{loading ? "読み込み中" : "登録済みPasskeyはありません。"}</div> : null}
          {passkeys.map((passkey) => (
            <div key={passkey.id} className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{passkey.name || "Passkey"}</div>
                <div className="text-xs text-muted-foreground">最終使用 {passkey.last_used_at ? formatDateTime(passkey.last_used_at, timezone) : "-"}</div>
              </div>
              <AccountActionConfirmation
                controller={actionController}
                intent={{ id: "AUTH-21", resourceId: accountResourceID }}
                authority={authority}
                refreshAuthority={refreshAuthority}
                label="Passkeyを削除"
                icon={<Trash2 />}
                handler={() => apiDelete<void>(`/auth/passkeys/${encodeURIComponent(passkey.id)}`)}
                onSucceeded={() => {
                  setNotice({ tone: "success", text: "Passkeyを削除しました。" });
                  refresh();
                }}
                onOutcomeUnknown={outcomeUnknown}
              />
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

function readAccountAuthority(queryClient: ReturnType<typeof useQueryClient>): AccountAuthoritySnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const current = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.fetchStatus === "fetching") {
    return Object.freeze({ session: "authenticated", freshness: "refreshing", revision: accountAuthorityRevision(current) });
  }
  if (state?.status !== "success" || !current?.user?.id || !current.user.username) {
    return Object.freeze({ session: "unavailable", freshness: "unavailable", revision: "unavailable" });
  }
  return Object.freeze({
    session: "authenticated",
    freshness: "fresh",
    revision: accountAuthorityRevision(current),
  });
}

function accountAuthorityRevision(current: CurrentUser | undefined) {
  if (!current?.user) return "unavailable";
  return JSON.stringify([
    current.user.id,
    current.user.username,
    current.user.status ?? "",
    [...(current.user.roles ?? [])].sort(),
    [...(current.permissions ?? [])].sort(),
  ]);
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
