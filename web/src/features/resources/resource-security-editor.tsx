"use client";

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useI18n } from "@/components/admin/i18n-provider";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { ResourceActionConfirmationHost } from "@/features/resources/resource-action-control";
import { type ResourceActionController } from "@/features/resources/resource-action-controller";
import { type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";
import { useResourceOptions } from "./resource-form-queries";
import { isRecord, numberSetting, stringSetting, numberValue } from "./resource-values";
import { NumberField, SelectField, Field, CheckboxList } from "./resource-input-fields";
import { requiredPermissionText } from "./resource-permissions";
import { resourceActionResultMessage } from "./resource-action-feedback";

type SecuritySettingsPayload = {
  password_min_length: number;
  password_hash: "argon2id";
  login_lockout_threshold: number;
  session_idle_timeout_min: number;
  session_absolute_lifetime_h: number;
  remember_me_enabled: false;
  mfa_mode: string;
  mfa_required_roles: string[];
};

export function SecuritySettingsEditor({ resource, data, loading, disabled, controller }: { resource: ResourceDefinition; data: unknown; loading: boolean; disabled: boolean; controller: ResourceActionController }) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const roles = useResourceOptions("/roles", ["name"], ["name"], ["permissions"]);
  const [passwordMinLength, setPasswordMinLength] = useState("12");
  const [loginLockoutThreshold, setLoginLockoutThreshold] = useState("5");
  const [sessionIdleTimeout, setSessionIdleTimeout] = useState("30");
  const [sessionAbsoluteLifetime, setSessionAbsoluteLifetime] = useState("12");
  const [mfaMode, setMFAMode] = useState("disabled");
  const [mfaRequiredRoles, setMFARequiredRoles] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState<ResourceActionIntent | null>(null);

  useEffect(() => {
    if (!isRecord(data)) return;
    const handle = window.setTimeout(() => {
      setPasswordMinLength(String(numberSetting(data.password_min_length, 12)));
      setLoginLockoutThreshold(String(numberSetting(data.login_lockout_threshold, 5)));
      setSessionIdleTimeout(String(numberSetting(data.session_idle_timeout_min, 30)));
      setSessionAbsoluteLifetime(String(numberSetting(data.session_absolute_lifetime_h, 12)));
      setMFAMode(stringSetting(data.mfa_mode, "disabled"));
      setMFARequiredRoles(Array.isArray(data.mfa_required_roles) ? data.mfa_required_roles.map(String) : []);
    }, 0);
    return () => window.clearTimeout(handle);
  }, [data]);

  const roleItems = roles.length > 0 ? roles : [{ value: "super_admin", label: "super_admin" }, { value: "admin", label: "admin" }];
  const passwordLength = numberValue(passwordMinLength, 12);
  const lockoutThreshold = numberValue(loginLockoutThreshold, 5);
  const idleTimeout = numberValue(sessionIdleTimeout, 30);
  const absoluteLifetime = numberValue(sessionAbsoluteLifetime, 12);
  const mfaScope = mfaRequiredRoles.length > 0 ? mfaRequiredRoles.join(", ") : "全ユーザー";

  return (
    <div className="rounded-md border bg-muted/20 p-3">
      <div className="mb-3">
        <div className="font-medium">設定を変更</div>
        <p className="text-sm text-muted-foreground">ログイン保護、セッション期限、MFA適用範囲を保存できます。パスワードハッシュはArgon2id固定です。</p>
      </div>
      {!loading ? (
        <div className="mb-4 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <SecurityPolicyCard label="パスワード" value={`${passwordLength}文字以上`} detail="Argon2idで保存します。Remember meは無効です。" />
          <SecurityPolicyCard label="ロックアウト" value={`${lockoutThreshold}回失敗でロック`} detail="連続ログイン失敗時の保護です。" />
          <SecurityPolicyCard label="セッション" value={`無操作${idleTimeout}分 / 最大${absoluteLifetime}時間`} detail="保存後に作成されるログインセッションへ適用します。" />
          <SecurityPolicyCard label="MFA" value={securityMFAModeLabel(mfaMode)} detail={mfaMode === "disabled" ? "現在は要求しません。" : `対象: ${mfaScope}`} />
        </div>
      ) : null}
      {loading ? (
        <Skeleton className="h-36 w-full" />
      ) : (
        <form
          className="space-y-3"
          onSubmit={(event) => {
            event.preventDefault();
            const payload: SecuritySettingsPayload = {
              password_min_length: numberValue(passwordMinLength, 12),
              password_hash: "argon2id",
              login_lockout_threshold: numberValue(loginLockoutThreshold, 5),
              session_idle_timeout_min: numberValue(sessionIdleTimeout, 30),
              session_absolute_lifetime_h: numberValue(sessionAbsoluteLifetime, 12),
              remember_me_enabled: false,
              mfa_mode: mfaMode,
              mfa_required_roles: mfaRequiredRoles,
            };
            setMessage("");
            setPending(Object.freeze({ id: "RES-40", payload: Object.freeze(payload), publicLabel: "Security policy" }));
          }}
        >
          <fieldset disabled={disabled} className="space-y-3">
          <div className="grid gap-3 md:grid-cols-2">
            <NumberField label="最小パスワード長" value={passwordMinLength} onChange={setPasswordMinLength} min={8} required />
            <NumberField label="ロックまでの失敗回数" value={loginLockoutThreshold} onChange={setLoginLockoutThreshold} min={3} required />
            <NumberField label="アイドルタイムアウト (分)" value={sessionIdleTimeout} onChange={setSessionIdleTimeout} min={5} required />
            <NumberField label="絶対セッション期限 (時間)" value={sessionAbsoluteLifetime} onChange={setSessionAbsoluteLifetime} min={1} required />
            <SelectField
              label="MFAポリシー"
              value={mfaMode}
              onChange={setMFAMode}
              options={[
                { value: "disabled", label: "無効" },
                { value: "totp", label: "TOTPを要求" },
                { value: "passkey", label: "Passkeyを要求" },
              ]}
            />
            <Field label="固定ポリシー">
              <div className="rounded-md border bg-background px-3 py-2 text-sm text-muted-foreground">Argon2id / Remember me無効 / Passkey利用可能</div>
            </Field>
          </div>
          <CheckboxList
            label="MFAを要求するロール"
            values={mfaRequiredRoles}
            onChange={setMFARequiredRoles}
            items={roleItems}
            emptyText="ロールがまだ登録されていません。空のまま保存すると全ユーザーにMFAを要求します。"
          />
          <p className="text-xs text-muted-foreground">MFAポリシー有効時にロールを未選択で保存すると、全ユーザーが対象になります。</p>
          </fieldset>
          <Button type="submit" disabled={Boolean(pending) || disabled} title={disabled ? requiredPermissionText(resource.permissions?.update) : undefined}>
            保存
          </Button>
          {disabled ? <p className="text-xs text-muted-foreground">{requiredPermissionText(resource.permissions?.update)}</p> : null}
          {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
          {pending ? (
            <ResourceActionConfirmationHost
              controller={controller}
              intent={pending}
              onResult={(result) => {
                setMessage(resourceActionResultMessage(result, t, "セキュリティ設定を保存しました。"));
                if (result.kind === "succeeded") {
                  setPending(null);
                  void queryClient.invalidateQueries({ queryKey: ["resource", resource.path] });
                  void queryClient.invalidateQueries({ queryKey: ["auth", "mfa", "status"] });
                } else if (result.kind === "blocked") {
                  setPending(null);
                }
              }}
              onCancel={() => setPending(null)}
            />
          ) : null}
        </form>
      )}
    </div>
  );
}

function SecurityPolicyCard({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="rounded-md border bg-background p-3 text-sm">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="mt-1 font-semibold">{value}</div>
      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{detail}</p>
    </div>
  );
}

function securityMFAModeLabel(mode: string) {
  switch (mode) {
    case "totp":
      return "TOTPを要求";
    case "passkey":
      return "Passkeyを要求";
    default:
      return "無効";
  }
}
