"use client";
import { StreamTableContext, streamRowID, streamTableColumns } from "./stream-table-cells";
import { cancelStreamCreateFocus, takeStreamCreateFocus, type StreamCreateFocus } from "@/lib/ui-v2/stream-create-focus-handoff";
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";


import { DraftExitContext, useDraftExit } from "@/components/forms/draft-exit";
import { subscribeDraftSessionExit } from "@/lib/ui-v2/draft-navigation-lifecycle";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Plus, RadioTower, RotateCw } from "lucide-react";

import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { streamStatusAllowsDelete, streamStatusAllowsEdit, streamStatusAllowsForceStop, streamStatusAllowsStart, streamStatusAllowsStop } from "@/features/streams/stream-lifecycle";
import { presentStreamLifecycleStatus } from "@/lib/foundation/status/lifecycle-presenters";
import { DetailSection } from "@/components/layout/detail-section";
import { PageHeader } from "@/components/shell/page-header";
import { PageActions } from "@/components/shell/page-actions";
import { useI18n } from "@/components/admin/i18n-provider";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { DataTable } from "@/components/tables/data-table";
import { useAppSettings, useCurrentUser, useResourceData, useStreams } from "@/features/queries";
import { createStreamActionController, type StreamActionExecutionResult } from "@/features/streams/stream-action-controller";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";
import { streamActionBlockedMessage, streamActionLabel } from "@/features/streams/stream-action-feedback";
import { mutateStreamAction, refreshStreamActionAuthority, streamActionStateSnapshot, streamPermissionSnapshot } from "@/features/streams/stream-action-runtime";
import { StreamDetailsDialog } from "@/features/streams/stream-details-dialog";
import { readinessResult, type StreamOperationNotice } from "./stream-detail-operations";
import { StreamSlotForm } from "@/features/streams/stream-slot-form";
import { StreamSummary } from "@/features/streams/stream-summary";
import { normalizeRows, rowString, useOAuthAccountOptions, useOptionLabelMap, useResourceOptions } from "@/features/streams/stream-view-options";
import { hasPermission } from "@/lib/auth/permissions";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { cn } from "@/lib/utils";
import type { Stream } from "@/types/domain";

const streamLifecycle = { streamStatusAllowsDelete, streamStatusAllowsEdit, streamStatusAllowsForceStop, streamStatusAllowsStart, streamStatusAllowsStop };
const streamTableURL = { key: "streams", sorts: ["name", "status", "updated"], filters: { status: ["draft", "created", "ready", "scheduled", "starting", "live", "stopping", "stopped", "completed", "failed", "error"] } } as const;

export function StreamsView() {
  const uiText = useUICopy();
  const { t, locale } = useI18n();
  const ja = locale === "ja";
  const streams = useStreams();
  const currentUser = useCurrentUser();
  const appSettings = useAppSettings();
  const timezone = appSettings.data?.timezone;
  const queryClient = useQueryClient();
  const [createdStreams, setCreatedStreams] = useState<Stream[]>([]);
  const [copiedStreamID, setCopiedStreamID] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [editingStream, setEditingStream] = useState<Stream | null>(null);
  const [selectedStream, setSelectedStream] = useState<Stream | null>(null);
  const detailTrigger = useRef<HTMLButtonElement | null>(null);
  const createTrigger = useRef<HTMLButtonElement | null>(null);
  const createFocus = useRef<StreamCreateFocus | null>(null);
  const createGeneration = useRef(0);
  const createCloseIntent = useRef<{ generation: number; document: Document; pathname: string; search: string } | null>(null);
  const createCloseSession = useRef<ReturnType<typeof subscribeDraftSessionExit> | null>(null);
  const editTrigger = useRef<HTMLButtonElement | null>(null);
  const [actionNotice, setActionNotice] = useState<StreamOperationNotice | null>(null);
  const [, setActionAuthorityRevision] = useState(0);



  const actionController = useMemo(() => createStreamActionController({
    getPermissions: () => streamPermissionSnapshot(queryClient),
    getState: (intent) => streamActionStateSnapshot(queryClient, intent),
    mutate: mutateStreamAction,
  }), [queryClient]);
  const can = (permission: string) => hasPermission(currentUser.data, permission);
  const canCreate = can("streams.create");
  const canUpdate = can("streams.update");
  const createDraftExit = useDraftExit({ enabled: createOpen && canCreate, pending: false });
  const editDraftExit = useDraftExit({ enabled: editingStream !== null && canUpdate, pending: false });
  const requestCreateOpen = useCallback(() => {
    createGeneration.current++;
    createCloseIntent.current = null;
    setCreateOpen(true);
  }, []);
  const requestCreateClose = useCallback(() => {
    const generation = createGeneration.current;
    return createDraftExit.request(() => {
      createCloseIntent.current = window.location.hash === "#create-stream"
        ? { generation, document, pathname: window.location.pathname, search: window.location.search } : null;
      setCreateOpen(false);
    });
  }, [createDraftExit]);
  useLayoutEffect(() => {
    const session = subscribeDraftSessionExit(window);
    createCloseSession.current = session;
    return () => { createCloseIntent.current = null; createCloseSession.current = null; session.dispose(); };
  }, []);
  // The hook above updates this guard's enabled option before this layout runs.
  // Consume even an invalid intent on every commit; never retry a cancelled replace.
  useLayoutEffect(() => {
    const intent = createCloseIntent.current;
    createCloseIntent.current = null;
    if (!intent || createOpen || !canCreate || createCloseSession.current?.active() !== false) return;
    if (intent.generation !== createGeneration.current || intent.document !== document) return;
    if (intent.pathname !== window.location.pathname || intent.search !== window.location.search || window.location.hash !== "#create-stream") return;
    window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);
  });
  useEffect(() => {
    let connectedFocus: StreamCreateFocus | null = null;
    let releaseFocus: (() => void) | undefined;
    const syncFromHash = () => {
      createCloseIntent.current = null;
      if (window.location.hash === "#create-stream") {
        if (canCreate && !createFocus.current) { createFocus.current = takeStreamCreateFocus(); if (createFocus.current) createTrigger.current = null; }
        if (canCreate && createFocus.current && createFocus.current !== connectedFocus) {
          releaseFocus?.(); connectedFocus = createFocus.current;
          releaseFocus = connectedFocus.connect({ document, pathname: window.location.pathname, search: window.location.search });
        }
        requestCreateOpen();
      } else createDraftExit.request(() => setCreateOpen(false));
    };
    const navigation = (window as unknown as { navigation?: EventTarget }).navigation;
    const invalidateClose = () => { createCloseIntent.current = null; };
    syncFromHash();
    window.addEventListener("hashchange", syncFromHash);
    navigation?.addEventListener("navigate", invalidateClose);
    return () => { window.removeEventListener("hashchange", syncFromHash); navigation?.removeEventListener("navigate", invalidateClose); releaseFocus?.(); };
  }, [canCreate, createDraftExit, requestCreateOpen]);
  useEffect(() => { if (!canCreate) { createFocus.current?.cancel(); createFocus.current = null; createTrigger.current = null; } }, [canCreate]);
  const handleStreamActionResult = useCallback((result: StreamActionExecutionResult, intent: StreamActionIntent) => {
    if (result.kind === "succeeded") {

      if (intent.id === "STR-10" && intent.stream) {
        setCreatedStreams((current) => current.filter((stream) => stream.id !== intent.stream?.id));
      }
      void queryClient.invalidateQueries({ queryKey: ["streams"] });
      if (intent.id === "STR-08" && intent.stream) {
        setActionNotice({ tone: "success", streamID: intent.stream.id, readiness: readinessResult(result.value, intent.stream), message: ja ? "開始準備の確認結果を受信しました。" : "Readiness check received." });
        return;
      }
      setActionNotice({ tone: "success", streamID: intent.stream?.id, message: uiText("{0}の{1}を受け付けました。最新状態を確認してください。", intent.stream?.name || intent.publicLabel || uiText("配信枠"), streamActionLabel(intent.id, uiText)) });
      return;
    }
    if (result.kind === "outcome_unknown") {
      setActionNotice({ tone: "error", streamID: intent.stream?.id, message: uiText("操作結果を確認できません。再送せず、配信枠の最新状態または監査ログを確認してください。") });
    } else if (result.kind === "failed") {
      setActionNotice({ tone: "error", streamID: intent.stream?.id, message: uiText("{0} 再送せず、最新状態を確認してください。", t(result.error.messageKey)) });
    } else {
      setActionNotice({ tone: "error", streamID: intent.stream?.id, message: streamActionBlockedMessage(result.reason, uiText) });
    }
    if (result.kind === "failed" && intent.id === "STR-08") {
      // The control clears its latch only when both permission and resource
      // authorities have completed a safe read-only refresh.
      return refreshStreamActionAuthority(queryClient).then((refreshed) => {
        if (refreshed) {
          actionController.reconcile(intent);
          // Query data replacement can remount a row control. Re-render the
          // feature owner so the current control evaluates the cleared latch.
          setActionAuthorityRevision((revision) => revision + 1);
        }
        return refreshed;
      });
    }
    // Reconciliation fetches are safe and never resend the mutation.
    void queryClient.invalidateQueries({ queryKey: ["auth", "me"] });
    void queryClient.invalidateQueries({ queryKey: ["streams"] });
  }, [actionController, ja, queryClient, t, uiText]);
  const streamRows = useMemo(
    () => [...createdStreams, ...(streams.data || []).filter((stream) => !createdStreams.some((created) => created.id === stream.id))],
    [createdStreams, streams.data],
  );
  const currentSelectedStream = selectedStream ? streamRows.find((stream) => stream.id === selectedStream.id) || selectedStream : null;
  const discordLabels = useOptionLabelMap(useResourceOptions("/discord/configs", ["name", "service_id", "id"]));
  const youtubeOutputLabels = useOptionLabelMap(useResourceOptions("/youtube/outputs", ["name", "id"]));
  const youtubeOutputs = useResourceData<unknown>("/youtube/outputs");
  const staticRelayOutputIDs = useMemo(
    () => new Set(normalizeRows(youtubeOutputs.data).filter((row) => rowString(row, ["mode"]) === "live_api_relay_static").map((row) => rowString(row, ["id"])).filter(Boolean)),
    [youtubeOutputs.data],
  );
  const archiveAccountLabels = useOptionLabelMap(useOAuthAccountOptions("drive"));
  const archiveDestinationLabels = useOptionLabelMap(useResourceOptions("/archive/destinations", ["name", "id"]));
  const archiveProfileLabels = useOptionLabelMap(useResourceOptions("/profiles/archive", ["name", "id"]));
  const overlayProfileLabels = useOptionLabelMap(useResourceOptions("/profiles/overlay", ["name", "id"]));
  const copyStreamID = useCallback(async (id: string) => {
    if (!id || typeof navigator === "undefined" || !navigator.clipboard) return;
    await navigator.clipboard.writeText(id);
    setCopiedStreamID(id);
    window.setTimeout(() => setCopiedStreamID((current) => (current === id ? "" : current)), 1200);
  }, []);
  const onDetails = useCallback((stream: Stream, trigger: HTMLButtonElement) => { detailTrigger.current = trigger; setSelectedStream(stream); }, []);
  const onEdit = useCallback((stream: Stream, trigger: HTMLButtonElement) => { editDraftExit.request(() => { editTrigger.current = trigger; setEditingStream(stream); }); }, [editDraftExit]);

  const renderStreamStatus = useCallback((stream: Stream) => <DomainStatusBadge presentation={presentStreamLifecycleStatus(stream.status)} translate={t} showDetail />, [t]);
  const tablePresentation = { renderStreamStatus, lifecycle: streamLifecycle, uiText, t, ja, copiedStreamID, copyStreamID, onDetails, onEdit, canUpdate, actionController, handleStreamActionResult, staticRelayOutputIDs, youtubeOutputLabels, archiveDestinationLabels, archiveProfileLabels, discordLabels, timezone };
  const columns = streamTableColumns({ t, ja, uiText, discordLabels });

  return (
    <div className="space-y-5" data-screen-family="streams">
      <PageHeader title={ja ? "配信枠" : "Streams"}
        description={ja ? "作成後に編集画面で担当Nodeを明示的に割り当て、Readinessを確認して開始します。" : "After creating a slot, edit it to assign nodes explicitly, check Readiness, then start."}
        breadcrumbs={[{ label: ja ? "管理" : "Admin", href: "/admin/" }, { label: ja ? "配信枠" : "Streams" }]}
        eyebrow={<><RadioTower aria-hidden="true" className="size-4" />Discord VC</>}
        actions={<PageActions primary={canCreate ? <Button onClick={(event) => { cancelStreamCreateFocus(); createFocus.current = null; createTrigger.current = event.currentTarget; requestCreateOpen(); }}><Plus aria-hidden="true" />{ja ? "配信枠を作成" : "Create stream"}</Button> : null}
          secondary={<Button variant="outline" disabled={streams.isFetching} onClick={() => void streams.refetch()}><RotateCw aria-hidden="true" />{ja ? "更新" : "Refresh"}</Button>} />} />
      {streams.dataUpdatedAt ? <p className="text-xs text-muted-foreground">{ja ? "最終取得: " : "Last received: "}<time dateTime={new Date(streams.dataUpdatedAt).toISOString()}>{formatDateTime(new Date(streams.dataUpdatedAt).toISOString(), timezone)}</time></p> : null}
      {streams.data !== undefined || createdStreams.length ? <StreamSummary rows={streamRows} /> : <p role="status">{ja ? "配信状態は未取得です。" : "Stream state has not been received."}</p>}
      {streams.isError ? <div className="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between"><div className="flex gap-3"><AlertCircle className="mt-0.5 size-5 shrink-0" /><div><div className="text-sm font-semibold">{uiText("配信枠を取得できませんでした")}</div><p className="mt-0.5 text-xs">{uiText("通信状態を確認して再試行してください。新しい操作は一覧が更新されてから行ってください。")}</p></div></div><Button variant="outline" size="sm" onClick={() => streams.refetch()}><RotateCw className="size-4" />{uiText("再試行")}</Button></div> : null}
      {actionNotice ? <div className={cn("rounded-lg border p-3 text-sm", actionNotice.tone === "success" ? "border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-200" : "border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200")}>{actionNotice.message}</div> : null}
      {streams.isFetching && streams.data !== undefined ? <p role="status">{ja ? "取得済みデータを表示しながら更新中です。" : "Refreshing; showing previously received data."}</p> : null}
      <DetailSection title={ja ? "配信一覧" : "Stream list"} description={ja ? "状態・開始準備・担当・出力を確認してから操作してください。" : "Review state, readiness, assignments and outputs before an action."}>
        <StreamTableContext.Provider value={tablePresentation}><DataTable columns={columns} data={streamRows} density="compact" urlPolicy={streamTableURL} dataReady={streams.data !== undefined || createdStreams.length > 0}
          filters={[{ id: "status", label: t("status"), options: streamTableURL.filters.status.map((value) => ({ value, label: t(presentStreamLifecycleStatus(value).labelKey) })) }]}
          filterPlaceholder={ja ? "取得済みの配信枠を検索" : "Search loaded streams"} getRowId={streamRowID} responsive /></StreamTableContext.Provider>
      </DetailSection>
      <DraftExitContext.Provider value={createDraftExit}><Sheet open={createOpen} onOpenChange={(open) => { if (open) requestCreateOpen(); else requestCreateClose(); }}>
        <SheetContent onCloseAutoFocus={(event) => { event.preventDefault(); const handoff = createFocus.current; createFocus.current = null; if (handoff) handoff.restoreAfterClose(); else createTrigger.current?.focus(); }} side="right" className="w-full overflow-y-auto p-0 sm:max-w-3xl"><SheetHeader className="sr-only"><SheetTitle>{uiText("配信枠を作成")}</SheetTitle><SheetDescription>{uiText("Discord VCの開始条件、入力、出力、録画を設定します。")}</SheetDescription></SheetHeader><StreamSlotForm className="min-h-full rounded-none border-0 shadow-none" actionController={actionController} onActionResult={handleStreamActionResult} canCreate={canCreate} canUpdate={canUpdate} canAssignEncoder={can("services.assign")} canAssignWorker={can("workers.assign")} onSaved={(stream) => { setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)]); setActionNotice({ tone: "success", message: uiText("{0} を作成しました。稼働中の配信を保護するためNode割り当ては変更していません。開始前にこの配信枠を編集し、担当Nodeを明示的に割り当ててください。", stream.name) }); requestCreateClose(); }} /></SheetContent>
      </Sheet></DraftExitContext.Provider>
      <DraftExitContext.Provider value={editDraftExit}><Sheet open={editingStream !== null} onOpenChange={(open) => { if (!open) editDraftExit.request(() => setEditingStream(null)); }}>
        <SheetContent onCloseAutoFocus={(event) => { event.preventDefault(); editTrigger.current?.focus(); }} side="right" className="w-full overflow-y-auto p-0 sm:max-w-3xl"><SheetHeader className="sr-only"><SheetTitle>{uiText("配信枠を編集")}</SheetTitle><SheetDescription>{uiText("待機中または終了済みの配信枠の設定を変更します。")}</SheetDescription></SheetHeader>{editingStream ? <StreamSlotForm key={editingStream.id} stream={editingStream} className="min-h-full rounded-none border-0 shadow-none" actionController={actionController} onActionResult={handleStreamActionResult} canCreate={canCreate} canUpdate={canUpdate} canAssignEncoder={can("services.assign")} canAssignWorker={can("workers.assign")} onSaved={(stream) => { setCreatedStreams((current) => [stream, ...current.filter((item) => item.id !== stream.id)]); setActionNotice({ tone: "success", message: uiText("{0} の設定を更新しました。開始前に担当Nodeと出力先を確認してください。", stream.name) }); editDraftExit.request(() => setEditingStream(null)); }} /> : null}</SheetContent>
      </Sheet></DraftExitContext.Provider>
      <StreamDetailsDialog returnFocus={() => detailTrigger.current?.focus()} stream={currentSelectedStream} actionController={actionController} onActionResult={handleStreamActionResult} actionNotice={actionNotice} onOpenChange={(open) => { if (!open) setSelectedStream(null); }} discordLabels={discordLabels} youtubeOutputLabels={youtubeOutputLabels} archiveAccountLabels={archiveAccountLabels} archiveDestinationLabels={archiveDestinationLabels} archiveProfileLabels={archiveProfileLabels} overlayProfileLabels={overlayProfileLabels} />
    </div>
  );
}

function formatDateTime(value?: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
