"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import Image from "next/image";
import { usePathname } from "next/navigation";
import { QrCode, RefreshCcw, ShieldCheck, ShieldOff } from "lucide-react";
import { useI18n } from "@/components/admin/i18n-provider";
import { OneTimeSecretReveal } from "@/components/foundation/secrets/one-time-secret-reveal";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { APIError, apiPost } from "@/lib/api/client";
import { createOneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/lifecycle-owner";
import { qrCodeDataURL } from "@/lib/qr-code";
import type { MFAEnrollResponse, MFAStatus } from "@/types/domain";
import { AccountActionConfirmation } from "@/features/account/account-action-confirmation";
import { type AccountActionController, type AccountActionIntent, type AccountAuthoritySnapshot } from "@/features/account/account-action-policy";
import { adoptAccountOneTimeOutput, type AccountOneTimeSecretValue } from "@/features/account/account-one-time-secret";
import { type AccountNotice } from "./account-authority";

export function MFAPanel({
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
