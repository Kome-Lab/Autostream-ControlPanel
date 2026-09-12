"use client";

import { useState } from "react";
import { KeyRound, Trash2 } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { apiDelete, apiPost } from "@/lib/api/client";
import { passkeyRegistrationCredentialToJSON, passkeysSupported, publicKeyCreationOptionsFromJSON } from "@/lib/passkeys";
import type { PasskeyCredential, PasskeyRegistrationStart } from "@/types/domain";
import { AccountActionConfirmation } from "@/features/account/account-action-confirmation";
import { type AccountActionController, type AccountAuthoritySnapshot } from "@/features/account/account-action-policy";
import { type AccountNotice } from "./account-authority";
import { formatDateTime } from "./account-presentation";

export function PasskeyPanel({
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
