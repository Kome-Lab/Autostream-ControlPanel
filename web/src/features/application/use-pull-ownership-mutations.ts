import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiPost } from "@/lib/api/client";
import { normalizePullUpdaterOwnershipActivationResponse, normalizePullUpdaterOwnershipDeactivationResponse } from "@/lib/updater-ownership";
import { systemUpdateErrorMessage } from "@/lib/system-update-presentation";
import type { PullUpdaterOwnershipActivationRequest, PullUpdaterOwnershipActivationResponse, PullUpdaterOwnershipDeactivationResponse } from "@/types/domain";
import { type PullOwnershipDeactivationAttempt } from "./updater-settings-form-model";
import { pullOwnershipMutationErrorIsAmbiguous } from "./updater-settings-authority";



export function usePullOwnershipMutations({
  updaterID,
  queryClient,
  setAmbiguousOwnershipRequest,
  setAmbiguousDeactivationAttempt,
  setOwnershipFeedback,
}: {
  updaterID: string;
  queryClient: ReturnType<typeof useQueryClient>;
  setAmbiguousOwnershipRequest: (value: PullUpdaterOwnershipActivationRequest | null) => void;
  setAmbiguousDeactivationAttempt: (value: PullOwnershipDeactivationAttempt | null) => void;
  setOwnershipFeedback: (value: {tone: "success" | "error"; message: string}) => void
}) {
  const activateOwnership = useMutation<
    PullUpdaterOwnershipActivationResponse,
    Error,
    PullUpdaterOwnershipActivationRequest
  >({
    mutationFn: async (request) => {
      const response = normalizePullUpdaterOwnershipActivationResponse(await apiPost<unknown>(
        `/system-updates/updaters/${encodeURIComponent(updaterID)}/pull-ownership/activate`,
        request,
      ));
      if (
        response.updater_id !== updaterID
        || response.execution_host_id !== request.expected_execution_host_id
        || response.agent_service_id !== updaterID
        || response.ownership_epoch <= request.expected_ownership_epoch
        || response.source_policy_revision !== request.expected_source_policy_revision
        || response.projection_revision !== request.expected_projection_revision
        || response.local_executor_policy_revision !== request.expected_local_executor_policy_revision
        || response.local_executor_policy_sha256 !== request.expected_local_executor_policy_sha256
      ) {
        throw new Error("invalid_pull_ownership_activation_response");
      }
      return response;
    },
    retry: false,
    onSuccess: async (response) => {
      setAmbiguousOwnershipRequest(null);
      setAmbiguousDeactivationAttempt(null);
      setOwnershipFeedback({ tone: "success", message: `実行権限をHost Agentへ切り替えました。Ownership epoch: ${response.ownership_epoch}` });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["system-updates"] }),
        queryClient.invalidateQueries({ queryKey: ["system-updates", "updaters", updaterID, "settings"] }),
      ]);
    },
    onError: (error, request) => {
      if (pullOwnershipMutationErrorIsAmbiguous(error)) {
        setAmbiguousOwnershipRequest(request);
        setOwnershipFeedback({ tone: "error", message: "切替結果を確認できません。安全のため再送せず、Updater状態を再取得してください。" });
        return;
      }
      setOwnershipFeedback({ tone: "error", message: systemUpdateErrorMessage(error, "実行権限を切り替えられませんでした。最新状態を再取得して確認してください。") });
    },
  });
  const deactivateOwnership = useMutation<
    PullUpdaterOwnershipDeactivationResponse,
    Error,
    PullOwnershipDeactivationAttempt
  >({
    mutationFn: async (attempt) => {
      const response = normalizePullUpdaterOwnershipDeactivationResponse(await apiPost<unknown>(
        `/system-updates/updaters/${encodeURIComponent(updaterID)}/pull-ownership/deactivate`,
        attempt.request,
      ));
      if (
        response.updater_id !== updaterID
        || response.execution_host_id !== attempt.request.expected_execution_host_id
        || response.agent_service_id !== updaterID
        || response.ownership_epoch <= attempt.request.expected_ownership_epoch
        || response.agent_ownership_epoch !== 0
        || response.source_policy_revision !== attempt.request.expected_source_policy_revision
        || response.projection_revision !== attempt.request.expected_projection_revision
        || response.local_executor_policy_revision !== attempt.request.expected_local_executor_policy_revision
        || response.local_executor_policy_sha256 !== attempt.request.expected_local_executor_policy_sha256
      ) {
        throw new Error("invalid_pull_ownership_deactivation_response");
      }
      return response;
    },
    retry: false,
    onSuccess: async (response) => {
      setAmbiguousDeactivationAttempt(null);
      setAmbiguousOwnershipRequest(null);
      setOwnershipFeedback({
        tone: "success",
        message: `Host Agentの更新実行権限を解除しました。Ownership epoch: ${response.ownership_epoch} / Agent epoch: ${response.agent_ownership_epoch}`,
      });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["system-updates"] }),
        queryClient.invalidateQueries({ queryKey: ["system-updates", "updaters", updaterID, "settings"] }),
      ]);
    },
    onError: (error, attempt) => {
      if (pullOwnershipMutationErrorIsAmbiguous(error)) {
        setAmbiguousDeactivationAttempt(attempt);
        setOwnershipFeedback({
          tone: "error",
          message: "実行権限解除の結果を確認できません。安全のため再送せず、Updater状態を再取得してください。",
        });
        return;
      }
      setOwnershipFeedback({
        tone: "error",
        message: systemUpdateErrorMessage(error, "実行権限を解除できませんでした。最新状態を再取得してください。"),
      });
    },
  });
  return { activateOwnership, deactivateOwnership };
}
