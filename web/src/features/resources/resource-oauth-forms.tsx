"use client";

import { useMemo, useState } from "react";
import { Textarea } from "@/components/ui/textarea";
import { oauthAccountConfiguredName, oauthAccountPurposeLabel } from "@/lib/oauth-account";
import { type SubmitResource, type ResourceRow, noneValue } from "./resource-form-types";
import { useResourceOptions, useResourceRows } from "./resource-form-queries";
import { rowString, rowValue, stringListSetting, splitList, firstNonEmpty } from "./resource-values";
import { SelectField, TextField, Field, SwitchField, CheckboxList, FormActions } from "./resource-input-fields";

export function OAuthProviderForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const roles = useResourceOptions("/roles", ["id"], ["name", "id"], ["permissions"]);
  const defaultRedirectURI = typeof window === "undefined" ? "https://control.example.jp/auth/oauth/callback" : `${window.location.origin}/auth/oauth/callback`;
  const [providerType, setProviderType] = useState(() => rowString(initial || {}, ["provider_type"]) || "google");
  const [name, setName] = useState(() => rowString(initial || {}, ["name"]) || "Google Workspace");
  const [enabled, setEnabled] = useState(() => rowValue(initial || {}, ["enabled"]) !== false);
  const [clientID, setClientID] = useState(() => rowString(initial || {}, ["client_id"]));
  const [clientSecret, setClientSecret] = useState("");
  const [redirectURI, setRedirectURI] = useState(() => rowString(initial || {}, ["redirect_uri"]) || defaultRedirectURI);
  const [allowedDomains, setAllowedDomains] = useState(() => stringListSetting(rowValue(initial || {}, ["allowed_domains"])).join("\n"));
  const [autoProvision, setAutoProvision] = useState(() => rowValue(initial || {}, ["auto_provision"]) === true);
  const [defaultRoleIDs, setDefaultRoleIDs] = useState<string[]>(() => stringListSetting(rowValue(initial || {}, ["default_role_ids"])));
  const editing = Boolean(initial);
  const secretConfigured = rowValue(initial || {}, ["client_secret_configured"]) === true;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          {
            provider_type: providerType,
            name,
            enabled,
            client_id: clientID,
            client_secret: clientSecret,
            redirect_uri: redirectURI,
            allowed_domains: splitList(allowedDomains),
            auto_provision: autoProvision,
            default_role_ids: defaultRoleIDs,
          },
          clientSecret ? { onSensitiveDispatched: () => setClientSecret("") } : undefined,
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <SelectField
          label="プロバイダ"
          value={providerType}
          onChange={setProviderType}
          options={[
            { value: "google", label: "Google" },
            { value: "github", label: "GitHub" },
            { value: "discord", label: "Discord" },
          ]}
        />
        <TextField label="表示名" value={name} onChange={setName} required />
        <TextField label="Client ID" value={clientID} onChange={setClientID} required />
        <TextField label="Client Secret" value={clientSecret} onChange={setClientSecret} type="password" description={editing ? (secretConfigured ? "空欄のまま更新すると現在のClient Secretを保持します。差し替える時だけ入力してください。" : "保存済みClient Secretはありません。") : "入力値は保存後に再表示しません。"} />
        <TextField label="Redirect URI" value={redirectURI} onChange={setRedirectURI} required />
      </div>
      <div className="rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
        <div className="font-medium">Google Cloud Consoleに登録するリダイレクトURI</div>
        <p className="mt-1 text-xs text-amber-800">ログイン、OAuthアカウント連携、YouTube/Drive接続は保存済みのRedirect URIを共用します。Google OAuthクライアントの「承認済みのリダイレクトURI」に下の値を登録してください。</p>
        <div className="mt-2 space-y-1 font-mono text-xs">
          <div className="rounded bg-white px-2 py-1">{redirectURI}</div>
        </div>
        <p className="mt-2 text-xs text-amber-800">`redirect_uri_mismatch` が出る場合は、Google側の値とここに保存した値がスキーム、ホスト、パスまで完全一致しているか確認してください。</p>
      </div>
      <Field label="許可ドメイン" description="複数ある場合は改行またはカンマで区切ります。空なら制限しません。">
        <Textarea value={allowedDomains} onChange={(event) => setAllowedDomains(event.target.value)} className="min-h-20" placeholder="example.jp" />
      </Field>
      <div className="grid gap-3 md:grid-cols-2">
        <SwitchField label="有効化" checked={enabled} onCheckedChange={setEnabled} />
        <SwitchField label="初回ログイン時に自動ユーザー作成" checked={autoProvision} onCheckedChange={setAutoProvision} />
      </div>
      {autoProvision ? <CheckboxList label="自動作成ユーザーのロール" values={defaultRoleIDs} onChange={setDefaultRoleIDs} items={roles} emptyText="ロールがありません。" /> : null}
      <FormActions label={submitLabel} disabled={disabled || (autoProvision && defaultRoleIDs.length === 0)} />
    </form>
  );
}

export function OAuthAccountConnectForm({ disabled, submit }: { disabled: boolean; submit: SubmitResource }) {
  const providerRows = useResourceRows("/integrations/oauth-providers");
  const providerOptions = useMemo(
    () =>
      providerRows
        .filter((row) => rowString(row, ["provider_type"]) === "google")
        .map((row) => ({
          value: rowString(row, ["id"]),
          label: firstNonEmpty(rowString(row, ["name"]), rowString(row, ["id"])),
          description: rowString(row, ["redirect_uri"]),
        }))
        .filter((option) => option.value),
    [providerRows],
  );
  const [providerID, setProviderID] = useState(noneValue);
  const [accountLabel, setAccountLabel] = useState("配信・アーカイブ用Google");
  const [accountPurpose, setAccountPurpose] = useState("drive_youtube");
  const effectiveProviderID = providerID === noneValue && providerOptions[0]?.value ? providerOptions[0].value : providerID;

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          {
            provider_id: effectiveProviderID === noneValue ? "" : effectiveProviderID,
            account_label: accountLabel,
            account_purpose: accountPurpose,
            redirect_after: "/admin/integrations/",
          },
          {
            path: "/integrations/oauth-accounts/start",
            invalidatePath: "/integrations/oauth-accounts",
            successMessage: "OAuth認可画面へ移動します。",
            redirectToAuthorizationURL: true,
          },
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <SelectField label="Google OAuthプロバイダ" value={effectiveProviderID} onChange={setProviderID} options={[{ value: noneValue, label: "未選択" }, ...providerOptions]} />
        <TextField label="アカウント表示名" value={accountLabel} onChange={setAccountLabel} required />
        <SelectField
          label="接続用途"
          value={accountPurpose}
          onChange={setAccountPurpose}
          options={[
            { value: "drive_youtube", label: "YouTube Live・Drive保存" },
            { value: "youtube", label: "YouTube Liveのみ" },
            { value: "drive", label: "Drive保存のみ" },
          ]}
        />
      </div>
      {providerOptions.length === 0 ? <p className="text-sm text-muted-foreground">先にOAuthログインプロバイダでGoogleプロバイダを登録し、有効化してください。</p> : null}
      <FormActions label="OAuth接続を開始" disabled={disabled || effectiveProviderID === noneValue} />
    </form>
  );
}

export function OAuthAccountRenameForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial: ResourceRow; submitLabel?: string }) {
  const [accountLabel, setAccountLabel] = useState(() => oauthAccountConfiguredName(initial));
  const providerType = rowString(initial, ["provider_type"]);
  const email = rowString(initial, ["email"]);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit({ account_label: accountLabel.trim() });
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="アカウント表示名" value={accountLabel} onChange={setAccountLabel} placeholder="例: 広報 YouTube" description="配信枠や保存先の選択肢に表示する、識別しやすい名前です。" required />
        <div className="rounded-md border bg-muted/20 px-3 py-2 text-sm">
          <div className="text-muted-foreground">接続情報</div>
          <div className="mt-1 space-y-1">
            <div>{providerType || "プロバイダ未設定"}</div>
            <div>{oauthAccountPurposeLabel(initial)}</div>
            <div className="truncate text-muted-foreground">{email || "メール未取得"}</div>
          </div>
        </div>
      </div>
      <FormActions label={submitLabel} disabled={disabled || accountLabel.trim() === ""} />
    </form>
  );
}
