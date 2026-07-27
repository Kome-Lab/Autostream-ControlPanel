"use client";

import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRound, LoaderCircle, ServerCog, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useUpdaterHostBootstrapJobs } from "@/features/queries";
import {
  canonicalBootstrapHostIDs,
  encryptBootstrapCredentials,
} from "@/lib/bootstrap-envelope";
import { apiGet, apiPost } from "@/lib/api/client";
import {
  activeUpdaterHostBootstrapStatus,
  isUpdaterHostBootstrapJobActive,
  isUpdaterHostBootstrapBulkCandidate,
  normalizeUpdaterHostBootstrapJobsResponse,
  normalizeSystemUpdatesResponse,
  recoverUpdaterHostBootstrapRequest,
  requestUpdaterHostBootstrapWithRecovery,
  systemUpdateErrorMessage,
  systemUpdateHostBootstrapStatusLabel,
  updaterHostBootstrapConfirmationContext,
  updaterHostBootstrapEligibility,
  updaterHostBootstrapEligibilityMessage,
  updaterHostBootstrapRequestIdentity,
  type UpdaterHostBootstrapRequestIdentity,
  UpdaterHostBootstrapRequestAmbiguousError,
} from "@/lib/system-updates";
import type {
  SystemUpdateAgentStatus,
  UpdaterHostBootstrapHostResult,
  UpdaterHostBootstrapJobsResponse,
  UpdaterHostBootstrapRequest,
  UpdaterSettingsHost,
  UpdaterSettingsTarget,
} from "@/types/domain";

type UpdaterHostBootstrapPanelProps = {
  updater: SystemUpdateAgentStatus;
  expectedRevision: number;
  savedHosts: UpdaterSettingsHost[];
  currentHosts: UpdaterSettingsHost[];
  savedTargets: UpdaterSettingsTarget[];
  currentTargets: UpdaterSettingsTarget[];
  releaseTokenConfigured: boolean;
  canEdit: boolean;
  onActiveChange: (active: boolean) => void;
  onCloseBlockedChange: (blocked: boolean) => void;
};

type Feedback = {
  tone: "success" | "pending" | "error";
  message: string;
};

export function UpdaterHostBootstrapPanel({
  updater,
  expectedRevision,
  savedHosts,
  currentHosts,
  savedTargets,
  currentTargets,
  releaseTokenConfigured,
  canEdit,
  onActiveChange,
  onCloseBlockedChange,
}: UpdaterHostBootstrapPanelProps) {
  const formID = useId();
  const queryClient = useQueryClient();
  const bootstrapJobs = useUpdaterHostBootstrapJobs(updater.updater_id);
  const [selectedHostIDs, setSelectedHostIDs] = useState<string[]>([]);
  const [selectionMode, setSelectionMode] = useState<"single" | "bulk" | null>(null);
  const [administratorUser, setAdministratorUser] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [confirmedContext, setConfirmedContext] = useState("");
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [preparingEnvelope, setPreparingEnvelope] = useState(false);
  const [ambiguousRequest, setAmbiguousRequest] = useState<UpdaterHostBootstrapRequestIdentity | null>(null);
  const mountedRef = useRef(true);
  const activeBootstrapRequestRef = useRef<UpdaterHostBootstrapRequest | null>(null);
  const submitGenerationRef = useRef(0);
  const queryKey = ["system-updates", "updaters", updater.updater_id, "bootstrap-jobs"] as const;

  const latestResults = useMemo(
    () => latestBootstrapResults(bootstrapJobs.data?.jobs || [], expectedRevision),
    [bootstrapJobs.data?.jobs, expectedRevision],
  );
  const activeBootstrapStatus = useMemo(
    () => activeUpdaterHostBootstrapStatus(bootstrapJobs.data?.jobs || []),
    [bootstrapJobs.data?.jobs],
  );
  const savedHostsByID = useMemo(
    () => new Map(savedHosts.map((host) => [host.host_id, host])),
    [savedHosts],
  );
  const eligibilityByHostID = useMemo(() => new Map(currentHosts.map((host) => {
    const bootstrapStatus = activeBootstrapStatus || latestResults.get(host.host_id)?.status;
    return [host.host_id, updaterHostBootstrapEligibility({
      updater,
      expectedRevision,
      savedHost: savedHostsByID.get(host.host_id),
      currentHost: host,
      savedTargets,
      currentTargets,
      releaseTokenConfigured,
      bootstrapStatus,
    })] as const;
  })), [
    activeBootstrapStatus,
    currentHosts,
    currentTargets,
    expectedRevision,
    latestResults,
    savedHostsByID,
    savedTargets,
    releaseTokenConfigured,
    updater,
  ]);
  const bootstrapStatusReady = bootstrapJobs.isSuccess;
  const bulkHostIDs = currentHosts
    .filter((host) => {
      const eligibility = eligibilityByHostID.get(host.host_id);
      return bootstrapStatusReady && Boolean(eligibility && isUpdaterHostBootstrapBulkCandidate(eligibility));
    })
    .map((host) => host.host_id);
  const selectedHosts = selectedHostIDs
    .map((hostID) => savedHostsByID.get(hostID))
    .filter((host): host is UpdaterSettingsHost => Boolean(host));
  const confirmationContext = updaterHostBootstrapConfirmationContext(
    updater,
    expectedRevision,
    selectedHostIDs,
    selectedHosts,
  );
  const hostKeysConfirmed = Boolean(confirmedContext) && confirmedContext === confirmationContext;
  const selectedOperationHostIDs = new Set(selectedHostIDs);
  const operationContext = JSON.stringify({
    confirmation_context: confirmationContext,
    confirmed_context: confirmedContext,
    updater_state: {
      updater_id: updater.updater_id,
      online: updater.online,
      desired_revision: updater.desired_revision,
      applied_revision: updater.applied_revision,
      policy_status: updater.policy_status,
    },
    expected_revision: expectedRevision,
    saved_hosts: savedHosts.filter((host) => selectedOperationHostIDs.has(host.host_id)),
    current_hosts: currentHosts.filter((host) => selectedOperationHostIDs.has(host.host_id)),
    saved_targets: savedTargets.filter((target) => selectedOperationHostIDs.has(target.host_id)),
    current_targets: currentTargets.filter((target) => selectedOperationHostIDs.has(target.host_id)),
    selected_host_ids: selectedHostIDs,
    selection_mode: selectionMode,
    release_token_configured: releaseTokenConfigured,
    can_edit: canEdit,
  });
  const operationContextRef = useRef(operationContext);
  useLayoutEffect(() => {
    operationContextRef.current = operationContext;
  }, [operationContext]);
  useLayoutEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      submitGenerationRef.current += 1;
      if (activeBootstrapRequestRef.current) {
        clearBootstrapRequestEnvelope(activeBootstrapRequestRef.current);
        activeBootstrapRequestRef.current = null;
      }
    };
  }, []);
  const selectedHostsStillReady = canEdit && selectedHostIDs.length > 0 && selectedHostIDs.every(
    (hostID) => {
      const eligibility = eligibilityByHostID.get(hostID);
      if (!bootstrapStatusReady || !eligibility) return false;
      return selectionMode === "bulk"
        ? isUpdaterHostBootstrapBulkCandidate(eligibility)
        : eligibility.ready;
    },
  );

  const clearPlaintext = () => {
    setAdministratorUser("");
    setPrivateKey("");
    setPassphrase("");
    setConfirmedContext("");
  };

  const recordBootstrapAcceptance = async (
    created: UpdaterHostBootstrapJobsResponse,
    request: UpdaterHostBootstrapRequestIdentity,
  ) => {
    setAmbiguousRequest(null);
    queryClient.setQueryData<UpdaterHostBootstrapJobsResponse>(queryKey, (current) => ({
      jobs: mergeBootstrapJobs(created.jobs, current?.jobs || []),
    }));
    const hostCount = created.jobs.reduce((count, job) => count + job.host_ids.length, 0) || request.host_ids.length;
    setFeedback({ tone: "success", message: `${hostCount}台のホストセットアップを受け付けました。進捗はこの画面で自動更新します。` });
    setSelectedHostIDs([]);
    setSelectionMode(null);
    await queryClient.invalidateQueries({ queryKey });
  };

  const startBootstrap = useMutation<UpdaterHostBootstrapJobsResponse, Error, UpdaterHostBootstrapRequest>({
    mutationFn: (request) => {
      activeBootstrapRequestRef.current = request;
      return requestUpdaterHostBootstrapWithRecovery(
        request,
        async (stableRequest) => normalizeUpdaterHostBootstrapJobsResponse(
          await apiPost<unknown>(
            `/system-updates/updaters/${encodeURIComponent(updater.updater_id)}/bootstrap-jobs`,
            stableRequest,
          ),
          updater.updater_id,
        ),
        async () => normalizeUpdaterHostBootstrapJobsResponse(
          await apiGet<unknown>(
            `/system-updates/updaters/${encodeURIComponent(updater.updater_id)}/bootstrap-jobs`,
          ),
          updater.updater_id,
        ).jobs,
      );
    },
    retry: false,
    onSuccess: async (created, request) => {
      clearBootstrapRequestEnvelope(request);
      if (activeBootstrapRequestRef.current === request) activeBootstrapRequestRef.current = null;
      await recordBootstrapAcceptance(created, request);
    },
    onError: (error, request) => {
      if (error instanceof UpdaterHostBootstrapRequestAmbiguousError) {
        const identity = updaterHostBootstrapRequestIdentity(request);
        clearBootstrapRequestEnvelope(request);
        if (activeBootstrapRequestRef.current === request) activeBootstrapRequestRef.current = null;
        setAmbiguousRequest(identity);
        setFeedback({
          tone: "pending",
          message: "セットアップ要求の受付結果を確認中です。要求識別子だけで状態を自動確認し、POSTの再送や新しいセットアップは開始しません。",
        });
        return;
      }
      setAmbiguousRequest(null);
      setFeedback({
        tone: "error",
        message: systemUpdateErrorMessage(error, "ホストのセットアップを開始できませんでした。認証情報と状態を確認してください。"),
      });
    },
    onSettled: (_created, _error, request) => {
      if (activeBootstrapRequestRef.current === request) {
        clearBootstrapRequestEnvelope(request);
        activeBootstrapRequestRef.current = null;
      }
      clearPlaintext();
    },
  });
  const recoverAmbiguousBootstrap = useMutation<
    UpdaterHostBootstrapJobsResponse | undefined,
    Error,
    UpdaterHostBootstrapRequestIdentity
  >({
    mutationFn: (request) => recoverUpdaterHostBootstrapRequest(
      request,
      async () => {
        const response = normalizeUpdaterHostBootstrapJobsResponse(
          await apiGet<unknown>(
            `/system-updates/updaters/${encodeURIComponent(updater.updater_id)}/bootstrap-jobs`,
          ),
          updater.updater_id,
        );
        queryClient.setQueryData(queryKey, response);
        return response.jobs;
      },
    ),
    retry: false,
    onSuccess: async (recovered, request) => {
      if (recovered) await recordBootstrapAcceptance(recovered, request);
    },
  });
  const pollAmbiguousBootstrap = recoverAmbiguousBootstrap.mutate;
  const bootstrapMutationPending = startBootstrap.isPending;
  const ambiguousRecoveryPending = recoverAmbiguousBootstrap.isPending;
  useEffect(() => {
    if (!ambiguousRequest || ambiguousRecoveryPending) return;
    const retryTimer = window.setTimeout(() => {
      if (mountedRef.current) pollAmbiguousBootstrap(ambiguousRequest);
    }, 5_000);
    return () => window.clearTimeout(retryTimer);
  }, [ambiguousRecoveryPending, ambiguousRequest, pollAmbiguousBootstrap]);
  const busy = preparingEnvelope
    || bootstrapMutationPending
    || ambiguousRecoveryPending
    || Boolean(ambiguousRequest);
  useLayoutEffect(() => {
    onActiveChange(Boolean(activeBootstrapStatus) || busy);
  }, [activeBootstrapStatus, busy, onActiveChange]);
  useLayoutEffect(() => {
    onCloseBlockedChange(busy);
  }, [busy, onCloseBlockedChange]);
  useLayoutEffect(() => () => {
    onActiveChange(false);
    onCloseBlockedChange(false);
  }, [onActiveChange, onCloseBlockedChange]);

  const submitBootstrap = async () => {
    const generation = submitGenerationRef.current + 1;
    submitGenerationRef.current = generation;
    const initialOperationContext = operationContext;
    const operationStillCurrent = () => {
      if (!mountedRef.current || submitGenerationRef.current !== generation) return false;
      if (operationContextRef.current !== initialOperationContext) {
        throw new Error("updater_host_bootstrap_context_changed");
      }
      return true;
    };
    setPreparingEnvelope(true);
    setFeedback(null);
    try {
      if (typeof globalThis.crypto?.randomUUID !== "function") throw new Error("bootstrap_webcrypto_unavailable");
      if (!canEdit || !selectedHostsStillReady) throw new Error("updater_host_bootstrap_not_ready");
      if (!hostKeysConfirmed) throw new Error("bootstrap_host_keys_unconfirmed");

      const [refreshedSystemUpdatesRaw, refreshedBootstrapJobs] = await Promise.all([
        apiGet<unknown>("/system-updates"),
        bootstrapJobs.refetch(),
      ]);
      if (!operationStillCurrent()) return;
      const refreshedSystemUpdates = normalizeSystemUpdatesResponse(refreshedSystemUpdatesRaw);
      queryClient.setQueryData(["system-updates"], refreshedSystemUpdates);
      const refreshedUpdater = refreshedSystemUpdates.updaters.find((candidate) => candidate.updater_id === updater.updater_id);
      if (!refreshedUpdater || refreshedBootstrapJobs.isError || !refreshedBootstrapJobs.data) {
        throw new Error("updater_host_bootstrap_status_unavailable");
      }
      if (
        confirmedContext !== updaterHostBootstrapConfirmationContext(
          refreshedUpdater,
          expectedRevision,
          selectedHostIDs,
          selectedHosts,
        )
      ) {
        throw new Error("bootstrap_host_keys_unconfirmed");
      }
      const refreshedLatestResults = latestBootstrapResults(refreshedBootstrapJobs.data.jobs, expectedRevision);
      const refreshedActiveStatus = activeUpdaterHostBootstrapStatus(refreshedBootstrapJobs.data.jobs);
      const refreshedSelectionReady = selectedHostIDs.every((hostID) => {
        const currentHost = currentHosts.find((host) => host.host_id === hostID);
        const eligibility = updaterHostBootstrapEligibility({
          updater: refreshedUpdater,
          expectedRevision,
          savedHost: savedHostsByID.get(hostID),
          currentHost,
          savedTargets,
          currentTargets,
          releaseTokenConfigured,
          bootstrapStatus: refreshedActiveStatus || refreshedLatestResults.get(hostID)?.status,
        });
        return selectionMode === "bulk"
          ? isUpdaterHostBootstrapBulkCandidate(eligibility)
          : eligibility.ready;
      });
      if (!refreshedSelectionReady) throw new Error("updater_host_bootstrap_not_ready");

      const normalizedUser = administratorUser.trim();
      if (!/^[a-z_][a-z0-9_-]{0,31}$/.test(normalizedUser) || normalizedUser === "root") {
        throw new Error("bootstrap_administrator_user_invalid");
      }
      const normalizedPrivateKey = privateKey.trim();
      if (
        !normalizedPrivateKey
        || new TextEncoder().encode(normalizedPrivateKey).byteLength > 64 * 1024
        || !/-----BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY-----/.test(normalizedPrivateKey)
      ) {
        throw new Error("bootstrap_private_key_invalid");
      }
      if (new TextEncoder().encode(passphrase).byteLength > 8 * 1024) throw new Error("bootstrap_passphrase_too_long");

      const hostIDs = canonicalBootstrapHostIDs(selectedHostIDs);
      const jobID = globalThis.crypto.randomUUID();
      const envelope = await encryptBootstrapCredentials(
        refreshedUpdater.bootstrap_encryption_public_key || "",
        {
          updaterID: refreshedUpdater.updater_id,
          expectedRevision,
          jobID,
          hostIDs,
        },
        {
          administrator_user: normalizedUser,
          private_key: normalizedPrivateKey,
          passphrase,
        },
      );
      if (!operationStillCurrent()) return;
      const request: UpdaterHostBootstrapRequest = {
        job_id: jobID,
        idempotency_key: globalThis.crypto.randomUUID(),
        expected_revision: expectedRevision,
        host_ids: hostIDs,
        recipient_key_fingerprint: refreshedUpdater.bootstrap_encryption_key_fingerprint || "",
        envelope,
      };

      if (!operationStillCurrent()) return;
      // Commit the cleared form before the mutation receives its envelope-only request.
      flushSync(clearPlaintext);
      startBootstrap.mutate(request);
    } catch (error) {
      if (!mountedRef.current || submitGenerationRef.current !== generation) return;
      clearPlaintext();
      setFeedback({
        tone: "error",
        message: systemUpdateErrorMessage(error, "ホストのセットアップを開始できませんでした。認証情報と状態を確認してください。"),
      });
    } finally {
      if (mountedRef.current && submitGenerationRef.current === generation) {
        setPreparingEnvelope(false);
      }
    }
  };

  const openCredentialForm = (hostIDs: string[], mode: "single" | "bulk") => {
    if (!canEdit) return;
    submitGenerationRef.current += 1;
    clearPlaintext();
    setFeedback(null);
    setSelectedHostIDs(hostIDs);
    setSelectionMode(mode);
  };

  const closeCredentialForm = () => {
    submitGenerationRef.current += 1;
    clearPlaintext();
    setSelectedHostIDs([]);
    setSelectionMode(null);
  };

  return (
    <div className="space-y-4 rounded-md border border-blue-200 bg-blue-50/40 p-4 dark:border-blue-900 dark:bg-blue-950/20">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 font-medium"><ServerCog className="size-4" />helper自動セットアップ</div>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-muted-foreground">
            管理者SSH認証を今回だけ使用し、中央Updaterから制限付きhelperを導入して動作確認します。対象ホストで個別にインストールコマンドを実行する必要はありません。
            helperは更新時だけSSH経由で起動し、対象ホストに常駐service・listener・helper専用port・helper用env・Node Runtime Tokenは作成しません。
            v1の自動セットアップは標準systemdサービス（Control Panel 8080、Encoder 8081、Observability 8082、Discord Bot 8083、Worker 8084）だけに対応し、実際のhealth・version応答まで確認します。Docker、非標準ポート、非標準パス、カスタム構成は手動導入になります。
          </p>
        </div>
        {canEdit ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!bootstrapStatusReady || bulkHostIDs.length === 0 || busy}
            onClick={() => openCredentialForm(bulkHostIDs, "bulk")}
          >
            <ServerCog className="size-4" />
            未セットアップを一括セットアップ{bulkHostIDs.length ? ` (${bulkHostIDs.length})` : ""}
          </Button>
        ) : null}
      </div>

      {bootstrapJobs.isError ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-xs text-destructive" role="alert">
          セットアップ状態を取得できないため、新しいセットアップを開始できません。通信状態を確認して再度開いてください。
        </div>
      ) : null}

      <div className="space-y-2">
        {currentHosts.map((host) => {
          const result = latestResults.get(host.host_id);
          const eligibility = eligibilityByHostID.get(host.host_id);
          const displayStatus = result?.status || eligibilityStatus(eligibility?.reason, bootstrapStatusReady);
          const buttonLabel = result?.status === "succeeded"
            ? "再セットアップ"
            : result?.status === "failed" || result?.status === "credential_expired"
              ? "再試行"
              : "セットアップ";
          const disabled = !canEdit || !bootstrapStatusReady || !eligibility?.ready || busy;
          return (
            <div key={host.host_id} className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-background/80 p-3 text-sm">
              <div className="min-w-0">
                <div className="truncate font-medium">{host.name || host.host_id}</div>
                <div className="mt-0.5 text-xs text-muted-foreground">{host.address || "接続先未入力"}:{host.port || 22}</div>
                {result?.message ? <div className="mt-1 break-words text-xs text-muted-foreground">{result.message}</div> : null}
              </div>
              <div className="flex items-center gap-2">
                <Badge variant={bootstrapBadgeTone(displayStatus)}>{systemUpdateHostBootstrapStatusLabel(displayStatus)}</Badge>
                {typeof result?.progress === "number" && isUpdaterHostBootstrapJobActive(result.status) ? (
                  <span className="text-xs text-muted-foreground">{Math.max(0, Math.min(100, Math.round(result.progress)))}%</span>
                ) : null}
                {canEdit ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={disabled}
                    title={updaterHostBootstrapEligibilityMessage(eligibility?.reason, bootstrapStatusReady)}
                    onClick={() => openCredentialForm([host.host_id], "single")}
                  >
                    {buttonLabel}
                  </Button>
                ) : null}
              </div>
            </div>
          );
        })}
      </div>

      {selectedHostIDs.length > 0 && canEdit ? (
        <div className="space-y-4 rounded-md border bg-background p-4">
          <div>
            <div className="font-medium">{selectedHostIDs.length === 1 ? "ホストをセットアップ" : `${selectedHostIDs.length}台を一括セットアップ`}</div>
            <p className="mt-1 text-xs text-muted-foreground">
              秘密鍵とパスフレーズは今回のセットアップだけに使用し、保存・再表示しません。選択した全ホストで同じ認証情報を使用します。
            </p>
          </div>

          <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs leading-5 text-amber-950 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-100">
            <div className="flex items-center gap-2 font-medium"><ShieldCheck className="size-4" />接続鍵の確認</div>
            <div className="mt-1 break-all">
              Updater暗号鍵: {updater.bootstrap_encryption_key_fingerprint || "Fingerprint未報告"}
            </div>
            {selectedHosts.map((host) => (
              <div key={host.host_id} className="mt-1 break-all">
                {host.name || host.host_id}: {host.host_key_fingerprint || host.host_public_key_fingerprint || "Fingerprint未報告"}
              </div>
            ))}
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <label className="space-y-1.5 text-sm" htmlFor={`${formID}-administrator-user`}>
              <span className="font-medium">一時管理者SSHユーザー</span>
              <Input
                id={`${formID}-administrator-user`}
                value={administratorUser}
                onChange={(event) => setAdministratorUser(event.target.value)}
                autoComplete="off"
                placeholder="deploy"
                disabled={busy || !canEdit}
              />
              <span className="block text-xs text-muted-foreground">rootではなく、パスワード入力なしでsudoを実行できる既存ユーザーを指定します。</span>
            </label>
            <label className="space-y-1.5 text-sm" htmlFor={`${formID}-passphrase`}>
              <span className="font-medium">秘密鍵パスフレーズ（任意）</span>
              <Input
                id={`${formID}-passphrase`}
                type="password"
                value={passphrase}
                onChange={(event) => setPassphrase(event.target.value)}
                autoComplete="off"
                disabled={busy || !canEdit}
              />
            </label>
          </div>

          <label className="space-y-1.5 text-sm" htmlFor={`${formID}-private-key`}>
            <span className="flex items-center gap-2 font-medium"><KeyRound className="size-4" />一時SSH秘密鍵</span>
            <Textarea
              id={`${formID}-private-key`}
              value={privateKey}
              onChange={(event) => setPrivateKey(event.target.value)}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              rows={6}
              className="font-mono text-xs"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              disabled={busy || !canEdit}
            />
          </label>

          <label className="flex items-start gap-2 rounded-md border p-3 text-sm">
            <input
              type="checkbox"
              checked={hostKeysConfirmed}
              onChange={(event) => setConfirmedContext(event.target.checked ? confirmationContext : "")}
              disabled={busy || !canEdit}
              className="mt-0.5"
            />
            <span>表示されたUpdater暗号鍵と各ホストのSSHホスト鍵Fingerprintを、独立した安全な経路で確認しました。</span>
          </label>

          <div className="flex flex-wrap justify-end gap-2">
            <Button type="button" variant="outline" onClick={closeCredentialForm} disabled={busy}>キャンセル</Button>
            <Button
              type="button"
              onClick={() => void submitBootstrap()}
              disabled={
                busy
                || !canEdit
                || !selectedHostsStillReady
                || !hostKeysConfirmed
                || !administratorUser.trim()
                || !privateKey.trim()
              }
            >
              {busy ? <LoaderCircle className="size-4 animate-spin" /> : <ServerCog className="size-4" />}
              セットアップを開始
            </Button>
          </div>
        </div>
      ) : null}

      {feedback ? (
        <div
          className={feedback.tone === "success"
            ? "rounded-md border border-emerald-300 bg-emerald-50 p-3 text-sm text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-100"
            : feedback.tone === "pending"
              ? "rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100"
              : "rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"}
          role={feedback.tone === "error" ? "alert" : "status"}
        >
          {feedback.message}
        </div>
      ) : null}
    </div>
  );
}

function latestBootstrapResults(
  jobs: UpdaterHostBootstrapJobsResponse["jobs"],
  expectedRevision: number,
) {
  const latest = new Map<string, UpdaterHostBootstrapHostResult>();
  const ordered = jobs.filter((job) => job.expected_revision === expectedRevision).sort((left, right) => {
    const leftTime = Date.parse(left.updated_at || left.created_at || "") || 0;
    const rightTime = Date.parse(right.updated_at || right.created_at || "") || 0;
    return rightTime - leftTime;
  });
  for (const job of ordered) {
    const results = job.hosts.length > 0
      ? job.hosts
      : job.host_ids.map((hostID) => ({ host_id: hostID, status: job.status }));
    for (const result of results) {
      if (!latest.has(result.host_id)) latest.set(result.host_id, result);
    }
  }
  return latest;
}

function mergeBootstrapJobs(
  incoming: UpdaterHostBootstrapJobsResponse["jobs"],
  current: UpdaterHostBootstrapJobsResponse["jobs"],
) {
  const jobs = new Map(current.map((job) => [job.id, job]));
  for (const job of incoming) jobs.set(job.id, job);
  return Array.from(jobs.values());
}

function clearBootstrapRequestEnvelope(request: UpdaterHostBootstrapRequest) {
  request.envelope.ephemeral_public_key = "";
  request.envelope.nonce = "";
  request.envelope.ciphertext = "";
}

function eligibilityStatus(reason: ReturnType<typeof updaterHostBootstrapEligibility>["reason"] | undefined, statusKnown: boolean) {
  if (!statusKnown) return "checking";
  const statuses: Record<string, string> = {
    updater_offline: "updater_offline",
    policy_pending: "policy_pending",
    release_token_pending: "release_token_pending",
    host_unsaved: "host_unsaved",
    host_key_pending: "host_key_pending",
    client_key_pending: "client_key_pending",
    encryption_key_pending: "encryption_key_pending",
    unsupported_profile: "unsupported_profile",
    bootstrap_active: "running",
    already_configured: "succeeded",
  };
  return reason ? statuses[reason] || "blocked" : "";
}

function bootstrapBadgeTone(status?: string): "default" | "secondary" | "destructive" | "outline" {
  if (status === "succeeded") return "default";
  if (status === "failed" || status === "partial_failed" || status === "credential_expired" || status === "updater_offline") return "destructive";
  if (isUpdaterHostBootstrapJobActive(status)) return "secondary";
  return "outline";
}
