"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { KeyRound, Link2, Mail, Plus, Trash2 } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { apiDelete, apiPost, apiPut } from "@/lib/api/client";
import type { OAuthLinkStartResponse, OAuthLoginProvider, OAuthUserLink } from "@/types/domain";
import { AccountActionConfirmation } from "@/features/account/account-action-confirmation";
import { type AccountActionController, type AccountActionIntent, type AccountAuthoritySnapshot } from "@/features/account/account-action-policy";
import { validatedOAuthRedirect } from "@/features/account/oauth-redirect-handoff";
import { type AccountNotice } from "./account-authority";
import { providerLabel } from "./account-presentation";

export function PasswordPanel({
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

export function EmailPanel({
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
