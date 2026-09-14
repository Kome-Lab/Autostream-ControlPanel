"use client";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Palette, ShieldCheck, UserCog } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { PageHeader } from "@/components/shell/page-header";
import { DetailSection } from "@/components/layout/detail-section";
import { AccountAvatar } from "@/components/ui/account-avatar";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { apiGet } from "@/lib/api/client";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import { useAppSettings, useCurrentUser } from "@/features/queries";
import type { MFAStatus, OAuthLoginProvider, OAuthUserLink, PasskeyCredential } from "@/types/domain";
import { AppearancePanel } from "@/features/account/appearance-panel";
import { createAccountActionController } from "@/features/account/account-action-policy";
import { type AccountNotice, readAccountAuthority } from "./account-authority";
import { accountStatusLabel, roleLabel } from "./account-presentation";
import { AvatarPanel } from "./account-avatar-panel";
import { EmailPanel, PasswordPanel } from "./account-security-panels";
import { MFAPanel } from "./account-mfa-panel";
import { PasskeyPanel } from "./account-passkey-panel";

export function AccountView() {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const ja = locale === "ja";
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
    <div className="space-y-5" data-screen-family="account">
      <PageHeader title={ja ? "アカウント設定" : "Account settings"}
        description={ja ? "プロフィール、ログイン保護、外観を管理します。資格情報は各操作の確認画面で扱います。" : "Manage your profile, login protection and appearance. Credential actions retain their own confirmation steps."}
        eyebrow={<><UserCog aria-hidden="true" className="size-4" />{ja ? "個人アカウント" : "Personal account"}</>}
        actions={<Button variant="outline" disabled={[currentUser, mfaStatus, passkeys, oauthLinks, oauthProviders].some(query => query.isFetching)} onClick={() => { void Promise.all([currentUser.refetch(), mfaStatus.refetch(), passkeys.refetch(), oauthLinks.refetch(), oauthProviders.refetch()]); }}>{ja ? "更新" : "Refresh"}</Button>} />
      {[mfaStatus, passkeys, oauthLinks, oauthProviders].some(query => query.isLoading) ? <p role="status">{ja ? "アカウント情報を取得中です。" : "Loading account information."}</p> : [mfaStatus, passkeys, oauthLinks, oauthProviders].some(query => query.isFetching) ? <p role="status">{ja ? "表示中の情報を更新中です。" : "Refreshing displayed information."}</p> : null}
      {[currentUser, mfaStatus, passkeys, oauthLinks, oauthProviders].some((query) => query.isError) ? <p role="alert" className="rounded-md border p-3 text-sm text-status-warning">{ja ? "一部の最新状態を取得できません。表示中の情報には前回の結果が含まれる場合があります。" : "Some current state is unavailable. Displayed information may include previous results."}</p> : null}

      {notice ? (
        <div role="status" aria-live="polite" className={notice.tone === "success" ? "rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-200" : "rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200"}>
          {notice.text}
        </div>
      ) : null}

      <Card>
        <CardContent className="grid gap-5 md:grid-cols-[minmax(0,1fr)_minmax(320px,0.7fr)] md:items-center">
          <div className="flex min-w-0 items-center gap-4">
            <AccountAvatar name={username} src={user?.avatar_url} alt={uiText("{0}のアカウントアイコン", username)} className="size-20" sizes="80px" />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <div className="truncate text-xl font-semibold">{username}</div>
                <Badge className={user?.status === "active" ? "border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300" : ""} variant="outline">
                  <CheckCircle2 />{accountStatusLabel(user?.status, uiText)}
                </Badge>
              </div>
              <div className="mt-1 truncate text-sm text-muted-foreground">{user?.email || uiText("メールアドレス未設定")}</div>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {roles.length ? roles.map((role) => <Badge key={role} variant="secondary">{roleLabel(role, uiText)}</Badge>) : <Badge variant="secondary">{uiText("ロール未設定")}</Badge>}
              </div>
            </div>
          </div>
          <div className="grid grid-cols-3 divide-x rounded-md border bg-muted/20">
            <AccountSummaryMetric label="MFA" value={mfaStatus.isError || !mfaStatus.data ? (ja ? "未確認" : "Unknown") : mfaStatus.data.enabled ? (ja ? "有効" : "Enabled") : (ja ? "無効" : "Disabled")} />
            <AccountSummaryMetric label="Passkey" value={passkeys.isError || !passkeys.data ? (ja ? "未確認" : "Unknown") : String(passkeys.data.length)} />
            <AccountSummaryMetric label={ja ? "外部ログイン" : "External login"} value={oauthLinks.isError || !oauthLinks.data ? (ja ? "未確認" : "Unknown") : String(oauthLinks.data.length)} />
          </div>
        </CardContent>
      </Card>

      <Tabs defaultValue="profile" className="gap-4">
        <TabsList variant="line" className="h-auto w-full flex-wrap justify-start border-b pb-1">
          <TabsTrigger value="profile" className="min-w-32 flex-none"><UserCog />{ja ? "プロフィール" : "Profile"}</TabsTrigger>
          <TabsTrigger value="security" className="min-w-32 flex-none"><ShieldCheck />{ja ? "セキュリティ" : "Security"}</TabsTrigger>
          <TabsTrigger value="appearance" className="min-w-32 flex-none"><Palette />{ja ? "外観" : "Appearance"}</TabsTrigger>
        </TabsList>
        <TabsContent value="profile">
          <DetailSection title={ja ? "プロフィールとログイン連携" : "Profile and connected logins"}>
          <div className="grid gap-4 xl:grid-cols-[minmax(300px,0.75fr)_minmax(0,1.25fr)]">
            <AvatarPanel
              username={username}
              currentAvatarURL={user?.avatar_url}
              setNotice={setNotice}
              onError={showError}
              refresh={() => queryClient.invalidateQueries({ queryKey: ["auth", "me"] })}
              actionController={actionController}
              authority={authority}
              refreshAuthority={refreshAuthority}
              accountResourceID={accountResourceID}
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
          </DetailSection>
        </TabsContent>
        <TabsContent value="security">
          <DetailSection title={ja ? "パスワード・MFA・Passkey" : "Password, MFA and passkeys"} description={ja ? "復旧コードや秘密値は必要な操作の中で一度だけ表示します。" : "Recovery codes and secrets are revealed only within their individual actions."}>
          <div className="grid items-start gap-6 xl:grid-cols-2 min-[1800px]:grid-cols-3">
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
          </DetailSection>
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
