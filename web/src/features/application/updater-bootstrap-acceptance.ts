import { useQueryClient } from "@tanstack/react-query";
import { type UpdaterActionController, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { type UpdaterHostBootstrapRequestIdentity } from "@/lib/updater-bootstrap";
import type { UpdaterHostBootstrapJobsResponse } from "@/types/domain";
import { type Feedback } from "./updater-bootstrap-presentation";
import { mergeBootstrapJobs } from "./updater-bootstrap-job-state";



export function createBootstrapAcceptanceRecorder({
  setAmbiguousRequest,
  ambiguousFoundationIntent,
  updaterActionController,
  setAmbiguousFoundationIntent,
  queryClient,
  queryKey,
  setFeedback,
  setSelectedHostIDs,
  setSelectionMode,
}: {
  setAmbiguousRequest: (value: UpdaterHostBootstrapRequestIdentity | null) => void;
  ambiguousFoundationIntent: UpdaterActionIntent | null;
  updaterActionController: UpdaterActionController;
  setAmbiguousFoundationIntent: (value: UpdaterActionIntent | null) => void;
  queryClient: ReturnType<typeof useQueryClient>;
  queryKey: readonly ["system-updates", "updaters", string, "bootstrap-jobs"];
  setFeedback: (value: Feedback) => void;
  setSelectedHostIDs: (value: string[]) => void;
  setSelectionMode: (value: "single" | "bulk" | null) => void
}) {

  const recordBootstrapAcceptance = async (
    created: UpdaterHostBootstrapJobsResponse,
    request: UpdaterHostBootstrapRequestIdentity,
  ) => {
    setAmbiguousRequest(null);
    if (ambiguousFoundationIntent) {
      updaterActionController.reconcile(ambiguousFoundationIntent);
      setAmbiguousFoundationIntent(null);
    }
    queryClient.setQueryData<UpdaterHostBootstrapJobsResponse>(queryKey, (current) => ({
      jobs: mergeBootstrapJobs(created.jobs, current?.jobs || []),
    }));
    const hostCount = created.jobs.reduce((count, job) => count + job.host_ids.length, 0) || request.host_ids.length;
    setFeedback({ tone: "success", message: `${hostCount}台のホストセットアップを受け付けました。進捗はこの画面で自動更新します。` });
    setSelectedHostIDs([]);
    setSelectionMode(null);
    await queryClient.invalidateQueries({ queryKey });
  };
  return recordBootstrapAcceptance;
}
