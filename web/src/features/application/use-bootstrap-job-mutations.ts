import { useMutation, useQueryClient } from "@tanstack/react-query";
import { type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { apiGet, apiPost } from "@/lib/api/client";
import { normalizeUpdaterHostBootstrapJobsResponse, recoverUpdaterHostBootstrapRequest, requestUpdaterHostBootstrapWithRecovery, updaterHostBootstrapRequestIdentity, type UpdaterHostBootstrapRequestIdentity, UpdaterHostBootstrapRequestAmbiguousError } from "@/lib/updater-bootstrap";
import { systemUpdateErrorMessage } from "@/lib/system-update-presentation";
import type { UpdaterHostBootstrapJobsResponse, UpdaterHostBootstrapRequest } from "@/types/domain";
import { type Feedback } from "./updater-bootstrap-presentation";
import { clearBootstrapRequestEnvelope } from "./updater-bootstrap-job-state";



export function useBootstrapJobMutations({
  updaterID,
  activeBootstrapRequestRef,
  queryClient,
  queryKey,
  recordBootstrapAcceptance,
  clearPlaintext,
  setAmbiguousRequest,
  setAmbiguousFoundationIntent,
  setFeedback,
  currentFoundationIntent,
}: {
  updaterID: string;
  activeBootstrapRequestRef: {current: UpdaterHostBootstrapRequest | null};
  queryClient: ReturnType<typeof useQueryClient>;
  queryKey: readonly ["system-updates", "updaters", string, "bootstrap-jobs"];
  recordBootstrapAcceptance: (created: UpdaterHostBootstrapJobsResponse, request: UpdaterHostBootstrapRequestIdentity) => Promise<void>;
  clearPlaintext: () => void;
  setAmbiguousRequest: (value: UpdaterHostBootstrapRequestIdentity | null) => void;
  setAmbiguousFoundationIntent: (value: UpdaterActionIntent | null) => void;
  setFeedback: (value: Feedback) => void;
  currentFoundationIntent: () => UpdaterActionIntent
}) {

  const startBootstrap = useMutation<UpdaterHostBootstrapJobsResponse, Error, UpdaterHostBootstrapRequest>({
    mutationFn: (request) => {
      activeBootstrapRequestRef.current = request;
      return requestUpdaterHostBootstrapWithRecovery(
        request,
        async (stableRequest) => normalizeUpdaterHostBootstrapJobsResponse(
          await apiPost<unknown>(
            `/system-updates/updaters/${encodeURIComponent(updaterID)}/bootstrap-jobs`,
            stableRequest,
          ),
          updaterID,
        ),
        async () => normalizeUpdaterHostBootstrapJobsResponse(
          await apiGet<unknown>(
            `/system-updates/updaters/${encodeURIComponent(updaterID)}/bootstrap-jobs`,
          ),
          updaterID,
        ).jobs,
      );
    },
    retry: false,
    onSuccess: async (created, request) => {
      clearBootstrapRequestEnvelope(request);
      if (activeBootstrapRequestRef.current === request) activeBootstrapRequestRef.current = null;
      setAmbiguousFoundationIntent(null);
      await recordBootstrapAcceptance(created, request);
    },
    onError: (error, request) => {
      if (error instanceof UpdaterHostBootstrapRequestAmbiguousError) {
        const identity = updaterHostBootstrapRequestIdentity(request);
        clearBootstrapRequestEnvelope(request);
        if (activeBootstrapRequestRef.current === request) activeBootstrapRequestRef.current = null;
        setAmbiguousRequest(identity);
        setAmbiguousFoundationIntent(currentFoundationIntent());
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
            `/system-updates/updaters/${encodeURIComponent(updaterID)}/bootstrap-jobs`,
          ),
          updaterID,
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
  return { startBootstrap, recoverAmbiguousBootstrap };
}
