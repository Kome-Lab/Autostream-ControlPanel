"use client";

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@/components/admin/i18n-provider";
import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";
import { Button } from "@/components/ui/button";
import { useCurrentUser } from "@/features/queries";
import { apiGet, apiPost, getCSRFToken } from "@/lib/api/client";
import type { CurrentUser, Stream } from "@/types/domain";
import {
  canInspectManualHold, canResumeAutoStart, createAutoStartResumeController, isAutoStartHoldSnapshot, projectHoldAttempt,
  type AutoStartHoldSnapshot, type HoldAttemptProjection, type ResumeOpened, type ResumeResult,
} from "./stream-auto-start-hold";

const reasonCopy: Readonly<Record<string, readonly [string, string]>> = {
  manual_stop: ["手動停止", "Manual stop"], MANUAL_STOP: ["手動停止", "Manual stop"], "manual-stop": ["手動停止", "Manual stop"],
  manual_force_stop: ["手動の強制停止", "Manual force stop"], force_stop: ["手動の強制停止", "Manual force stop"], "manual-force-stop": ["手動の強制停止", "Manual force stop"],
  explicit_resume: ["自動開始を再開", "Explicit resume"], "explicit-resume": ["自動開始を再開", "Explicit resume"],
  stream_created: ["配信枠の作成", "Stream created"], created: ["配信枠の作成", "Stream created"],
};
const boundedSignal = (signal: AbortSignal) => AbortSignal.any([signal, AbortSignal.timeout(10000)]);

export function StreamAutoStartHoldPanel({ stream }: { stream: Stream }) {
  const currentUser = useCurrentUser();
  return <StreamAutoStartHoldContent key={`${stream.id}:${currentUser.data?.user.id || "unauthenticated"}`} stream={stream} />;
}

function StreamAutoStartHoldContent({ stream }: { stream: Stream }) {
  const { locale } = useI18n(), ja = locale === "ja";
  const queryClient = useQueryClient(), currentUser = useCurrentUser();
  const [opened, setOpened] = useState<ResumeOpened | null>(null);
  const [busy, setBusy] = useState(false), [opening, setOpening] = useState(false);
  const [result, setResult] = useState<ResumeResult | null>(null);
  const [pendingID, setPendingID] = useState<string | null>(null);
  const [serverInspection, setServerInspection] = useState<Readonly<{ requestID: string; projection?: HoldAttemptProjection; denied?: boolean }> | null>(null);
  const actorID = currentUser.data?.user.id || "";
  const readPath = `/streams/${encodeURIComponent(stream.id)}/auto-start-hold`;
  const queryKey = ["streams", stream.id, "auto-start-hold", stream.updated_at || "", stream.settings_revision || "", stream.archive_run_id || "", stream.status] as const;
  const storageKey = actorID ? `autostream.resume-attempt:${encodeURIComponent(actorID)}:${encodeURIComponent(stream.id)}` : "";
  const snapshot = useQuery({ queryKey, retry: false, queryFn: async ({ signal }) => {
    const value = await apiGet<unknown>(readPath, boundedSignal(signal));
    if (!isAutoStartHoldSnapshot(value, stream.id)) throw new Error("hold_snapshot_unavailable");
    return value;
  } });
  const controller = useMemo(() => createAutoStartResumeController({
    streamID: stream.id,
    readSnapshot: signal => apiGet<unknown>(`/streams/${encodeURIComponent(stream.id)}/auto-start-hold`, boundedSignal(signal)),
    readIdentity: async signal => {
      const value = await apiGet<CurrentUser>("/auth/me", boundedSignal(signal));
      return { actorID: value?.user?.id || "", permissions: value?.permissions || [] };
    },
    currentIdentity: () => {
      const value = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
      return value?.user.id ? { actorID: value.user.id, sessionMarker: getCSRFToken() } : undefined;
    },
    newRequestID: () => globalThis.crypto.randomUUID(),
    send: (request, signal) => apiPost(`/streams/${encodeURIComponent(stream.id)}/auto-start/resume`, request, boundedSignal(signal)),
    inspect: (id, signal) => apiGet(`/streams/${encodeURIComponent(stream.id)}/auto-start/resume-attempts/${encodeURIComponent(id)}`, boundedSignal(signal)),
    onAttempt: id => {
      if (queryClient.getQueryData<CurrentUser>(["auth", "me"])?.user.id !== actorID) return;
      setPendingID(id);
      // Only the original non-secret request ID is mirrored for read-only
      // recovery after a page reload. The server remains the attempt owner.
      try { if (storageKey) { if (id) window.sessionStorage.setItem(storageKey, id); else window.sessionStorage.removeItem(storageKey); } } catch { /* server pending ID can still be inspected */ }
    },
  }), [stream.id, actorID, storageKey, queryClient]);
  const ownerRef = useRef<typeof controller | null>(null);
  const genericInspection = useRef<AbortController | null>(null);
  useLayoutEffect(() => {
    ownerRef.current = controller;
    return () => { ownerRef.current = null; controller.cancel(); genericInspection.current?.abort(); genericInspection.current = null; };
  }, [controller]);
  useEffect(() => {
    if (storageKey) {
      try { const id = window.sessionStorage.getItem(storageKey); if (id) controller.recover(id); } catch { /* no mutation fallback */ }
    }
  }, [controller, storageKey]);
  const usable = snapshot.isSuccess && !snapshot.isFetching && !snapshot.isError;
  const hold = usable ? snapshot.data.hold : undefined;
  const permissions = currentUser.isSuccess && !currentUser.isError && !currentUser.isFetching
    ? { actorID, permissions: currentUser.data?.permissions || [] } : undefined;
  const allowed = canResumeAutoStart(permissions);
  const readAllowed = canInspectManualHold(permissions);
  const serverPendingID = hold?.pending_attempt_id || "";
  const resumeAvailable = usable && Boolean(hold && (!hold.known || hold.held)) && allowed && !pendingID && !hold?.pending_attempt_id;
  const owned = (owner: typeof controller) => ownerRef.current === owner;
  const acceptResult = async (owner: typeof controller, value: ResumeResult) => {
    if (!owned(owner)) return;
    setResult(value);
    if (value.kind === "observed") {
      setOpened(null);
      await Promise.all([queryClient.invalidateQueries({ queryKey: ["streams"], exact: true }), queryClient.invalidateQueries({ queryKey, exact: true })]);
    } else if (value.kind === "blocked" && value.reason === "changed") setOpened(null);
    else if (value.kind === "blocked" && value.reason === "pending") await queryClient.invalidateQueries({ queryKey, exact: true });
  };
  const openIntent = async () => {
    if (busy || opening || !resumeAvailable) return;
    const owner = controller; setOpening(true); setResult(null);
    try {
      const value = await owner.open();
      if (!owned(owner)) return;
      if (value.kind === "opened") { setOpened(value.opened); queryClient.setQueryData<AutoStartHoldSnapshot>(queryKey, value.opened.snapshot); }
      else await acceptResult(owner, value);
    } finally { if (owned(owner)) setOpening(false); }
  };
  const confirm = async () => {
    if (!opened || busy) return;
    const owner = controller, intent = opened; setBusy(true); setResult(null);
    try { await acceptResult(owner, await owner.confirm(intent)); }
    finally { if (owned(owner)) setBusy(false); }
  };
  const inspect = async () => {
    if (busy || !controller.pendingRequestID) return;
    const owner = controller; setBusy(true);
    try { await acceptResult(owner, await owner.inspect()); }
    finally { if (owned(owner)) setBusy(false); }
  };
  const inspectServerHold = async () => {
    if (busy || genericInspection.current || pendingID || !readAllowed || !/^[A-Za-z0-9_.:-]{1,80}$/.test(serverPendingID)) return;
    const owner = controller, id = serverPendingID, marker = getCSRFToken(), operation = new AbortController();
    genericInspection.current = operation; setBusy(true); setServerInspection({ requestID: id });
    const sameOwner = () => owned(owner) && queryClient.getQueryData<CurrentUser>(["auth", "me"])?.user.id === actorID && getCSRFToken() === marker;
    try {
      const identity = await apiGet<CurrentUser>("/auth/me", boundedSignal(operation.signal));
      if (!sameOwner()) return;
      if (!canInspectManualHold({ actorID: identity?.user?.id || "", permissions: identity?.permissions || [] }) || identity.user.id !== actorID) { setServerInspection({ requestID: id, denied: true }); return; }
      const value = await apiGet<unknown>(`/streams/${encodeURIComponent(stream.id)}/auto-start/hold-attempts/${encodeURIComponent(id)}`, boundedSignal(operation.signal));
      if (!sameOwner()) return;
      const projection = projectHoldAttempt(value, stream.id, id);
      setServerInspection({ requestID: id, ...(projection ? { projection } : {}) });
    } catch { if (sameOwner()) setServerInspection({ requestID: id }); }
    finally {
      if (genericInspection.current === operation) genericInspection.current = null;
      if (owned(owner)) { if (!sameOwner()) setServerInspection(null); setBusy(false); }
    }
  };
  const message = resultMessage(result, ja);
  const presentation = result?.kind === "observed" ? result.presentation : undefined;
  const reason = hold?.reason ? reasonCopy[hold.reason]?.[ja ? 0 : 1] : undefined;
  const serverView = serverInspection?.requestID === serverPendingID ? serverInspection : undefined;
  return <section aria-label={ja ? "自動開始の手動保留" : "Manual hold of automatic start"} className="space-y-3 rounded-lg border p-4" data-manual-auto-start-hold>
    <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold">{ja ? "自動開始の手動保留" : "Automatic start hold"}</h3>
      <Button type="button" size="sm" variant="outline" disabled={snapshot.isFetching || busy} onClick={() => void snapshot.refetch()}>{ja ? "保留状態を更新" : "Refresh hold"}</Button>
    </div>
    <dl className="grid gap-2 text-sm sm:grid-cols-2">
      <div><dt className="text-muted-foreground">{ja ? "Manual Hold" : "Manual hold"}</dt><dd data-hold-state={!hold?.known ? "unknown" : hold.held ? "held" : "clear"}>{!hold?.known ? "UNKNOWN" : hold.held ? (ja ? "保留中" : "Held") : (ja ? "解除済み" : "Not held")}</dd></div>
      <div><dt className="text-muted-foreground">{ja ? "保留理由" : "Reason"}</dt><dd>{reason || (ja ? "未確認" : "Unconfirmed")}</dd></div>
      <div><dt className="text-muted-foreground">Hold Revision</dt><dd>{hold?.revision ?? "—"}</dd></div>
      <div><dt className="text-muted-foreground">{ja ? "現在のRun" : "Current run"}</dt><dd className="[overflow-wrap:anywhere]">{!usable ? "UNKNOWN" : snapshot.data.stream.archive_run_id || (ja ? "未報告" : "Not reported")}</dd></div>
    </dl>
    <p className="text-xs text-muted-foreground">{ja ? "手動で配信を開始しても、この保留は解除されません。" : "Starting manually does not clear this hold."}</p>
    {snapshot.isError ? <p role="alert">{ja ? "保留状態を取得できません。未保留として開始しません。" : "Hold state is unavailable. It is not treated as clear."}</p> : null}
    <ConfirmationDialogFrame open={opened !== null} onOpenChange={open => {
      if (open) void openIntent();
      else if (!busy) { controller.cancel(); setOpened(null); }
    }} trigger={<Button type="button" variant="outline" disabled={!resumeAvailable || busy || opening}>{opening ? (ja ? "確認中" : "Checking") : (ja ? "自動開始を再開" : "Resume automatic start")}</Button>}
      title={ja ? "自動開始を再開" : "Resume automatic start"}
      description={ja ? "開始条件が成立している場合、配信が開始される" : "The stream may start if the current start conditions are met."}
      cancelLabel={ja ? "キャンセル" : "Cancel"} actionLabel={ja ? "確認して自動開始を再開" : "Confirm and resume automatic start"}
      onConfirm={() => confirm()} actionDisabled={!opened || busy || !allowed || Boolean(pendingID)} cancelDisabled={busy} busy={busy} showAction={!pendingID}>
      {opened ? <p className="text-sm">{opened.snapshot.stream.name} · Hold Revision {opened.snapshot.hold.revision}</p> : null}
    </ConfirmationDialogFrame>
    {!allowed ? <p className="text-xs text-muted-foreground">{ja ? "現在の開始権限を確認できるユーザーだけが再開できます。" : "Resume requires current permission to start this stream."}</p> : null}
    {pendingID ? <Button type="button" variant="outline" disabled={busy || !allowed} onClick={() => void inspect()}>{busy ? (ja ? "照会中" : "Inspecting") : (ja ? "同じ要求の結果を照会" : "Inspect the original request")}</Button> : null}
    {serverPendingID && !pendingID ? <Button type="button" variant="outline" data-manual-hold-attempt-inspect disabled={busy || !readAllowed} onClick={() => void inspectServerHold()}>{busy ? (ja ? "照会中" : "Inspecting") : (ja ? "保留元の要求を照会" : "Inspect the original hold request")}</Button> : null}
    {serverView ? <div role="status" aria-live="polite" className="space-y-2 text-sm" data-manual-hold-attempt-result>
      {serverView.projection ? <><p>{serverView.projection.operation === "explicit_resume" ? (ja ? "この照会は保留記録だけを示します。実際の開始結果はUNKNOWNです。" : "This lookup shows the hold record only. The actual start result is UNKNOWN.") : (ja ? "この照会は保留記録だけを示します。実際の停止結果はUNKNOWNです。" : "This lookup shows the hold record only. The actual stop result is UNKNOWN.")}</p>
        <dl className="grid gap-2 sm:grid-cols-2"><div><dt>{ja ? "元の要求の保留記録" : "Original hold record"}</dt><dd>{serverView.projection.holdCommit === "known" ? (ja ? "確定を確認" : "Confirmed") : "UNKNOWN"}</dd></div><div><dt>Hold Revision</dt><dd>{serverView.projection.historicalHold.revision}</dd></div><div><dt>{ja ? "この要求のGate" : "This request's gate"}</dt><dd>{serverView.projection.gate}</dd></div><div><dt>{ja ? "実際の停止・開始" : "Actual stop / start"}</dt><dd>UNKNOWN</dd></div></dl>
      </> : <p>{serverView.denied ? (ja ? "現在の閲覧権限を確認できません。" : "Current read permission could not be confirmed.") : busy ? (ja ? "同じ保留記録を照会中です。" : "Inspecting the same hold record.") : (ja ? "保留記録の結果はUNKNOWNです。要求を再実行しません。" : "The hold record result is UNKNOWN. The request is not resent.")}</p>}
    </div> : null}
    {message ? <p role={result?.kind === "blocked" ? "alert" : "status"} aria-live="polite">{message}</p> : null}
    {presentation ? <dl className="grid gap-2 text-sm sm:grid-cols-2" data-resume-boundaries>
      <div><dt>{ja ? "保留解除の確定" : "Hold release commit"}</dt><dd>{presentation.holdCommit === "known" ? (ja ? "確定を確認" : "Confirmed") : "UNKNOWN"}</dd></div>
      <div><dt>{ja ? "開始条件" : "Start condition"}</dt><dd>{presentation.condition === "met" ? (ja ? "成立" : "Met") : presentation.condition === "not-met" ? (ja ? "不成立" : "Not met") : "UNKNOWN"}</dd></div>
      <div><dt>{ja ? "開始処理の結果" : "Start boundary result"}</dt><dd>{presentation.start === "accepted" ? (ja ? "開始の受理を確認" : "Start accepted") : presentation.start === "not-started" ? (ja ? "開始していません" : "No start effect") : presentation.start === "failed" ? (ja ? "開始失敗を確認" : "Start failure confirmed") : presentation.start === "pending" ? (ja ? "未完了" : "Pending") : "UNKNOWN"}</dd></div>
      <div><dt>{ja ? "この要求のGate" : "This request's gate"}</dt><dd>{presentation.gate}</dd></div>
    </dl> : null}
  </section>;
}

function resultMessage(result: ResumeResult | null, ja: boolean): string {
  if (!result || result.kind === "opened") return "";
  if (result.kind === "observed" && result.presentation.gate === "HELD") return ja ? "この要求のGateはHELDです。要求を再実行せず、同じ要求の結果を照会してください。" : "This request's gate is HELD. Inspect the same request without resending.";
  if (result.kind === "unknown" || result.kind === "observed" && result.unresolved) return ja ? "結果はUNKNOWNまたは未完了です。再実行せず、同じ要求の結果を照会してください。" : "The outcome is unknown or pending. Inspect the original request without resending.";
  if (result.kind === "observed") return ja ? "この要求の境界結果を確認しました。現在の保留状態と開始結果は別々に表示します。" : "The request boundary results are observed. Hold state and the start result are shown separately.";
  if (result.reason === "changed") return ja ? "対象・Run・設定またはHold Revisionが変わりました。新しい状態を確認してください。" : "The target, run, configuration or hold revision changed. Review a new confirmation.";
  if (result.reason === "permission") return ja ? "現在の権限またはSessionを確認できません。再開要求を追加送信していません。" : "Current permission or session could not be confirmed. No additional resume request was sent.";
  if (result.reason === "pending") return ja ? "元の要求が未確定です。同じ要求の結果だけを照会してください。" : "The original request is unresolved. Inspect that same request only.";
  return ja ? "最新状態を確認できません。再開要求は送信していません。" : "Current state could not be confirmed. No resume request was sent.";
}
