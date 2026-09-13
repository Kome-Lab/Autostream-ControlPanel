import { Download, LoaderCircle, XCircle } from "lucide-react";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { isControlPanelUpdateTarget, systemUpdateStrategyForTarget } from "@/lib/system-update-target-policy";
import type { SystemUpdateJob, SystemUpdateTarget, SystemUpdatesResponse } from "@/types/domain";
import { softwareUpdateAuthoritySnapshot, cancelUpdateAuthoritySnapshot } from "./port-reconfigure-authority";



export function createApplicationActionRenderers({
  updaterActionController,
  currentUpdaterAuthority,
  updates,
  refreshTargetAuthority,
  executeTarget,
  creating,
  canExecuteSystemUpdates,
  refreshCancelAuthority,
  executeCancel,
  cancelling,
  cancellingJobID,
}: {
  updaterActionController: ReturnType<typeof createUpdaterActionController>;
  currentUpdaterAuthority: (applicable: boolean, fingerprint: string) => UpdaterActionAuthority;
  updates: SystemUpdatesResponse | undefined;
  refreshTargetAuthority: (actionID: "UPD-01" | "UPD-02", targetID: string) => Promise<UpdaterActionAuthority>;
  executeTarget: (target: SystemUpdateTarget) => Promise<SystemUpdateJob>;
  creating: boolean;
  canExecuteSystemUpdates: boolean;
  refreshCancelAuthority: (jobID: string) => Promise<UpdaterActionAuthority>;
  executeCancel: (job: SystemUpdateJob) => Promise<SystemUpdateJob>;
  cancelling: boolean;
  cancellingJobID: string | undefined
}) {
  const renderTargetAction = (target: SystemUpdateTarget, disabled: boolean) => {
    const actionID = isControlPanelUpdateTarget(target) ? "UPD-02" as const : "UPD-01" as const;
    const snapshot = softwareUpdateAuthoritySnapshot(actionID, target, updates);
    const intent: UpdaterActionIntent = Object.freeze({
      id: actionID,
      resourceId: target.target_id,
      ...(actionID === "UPD-02" ? { publicLabel: target.name || target.target_id } : {}),
      authorityFingerprint: snapshot.fingerprint,
    });
    const strategy = systemUpdateStrategyForTarget(target);
    return (
      <UpdaterActionConfirmation
        controller={updaterActionController}
        intent={intent}
        authority={currentUpdaterAuthority(snapshot.applicable, snapshot.fingerprint)}
        refreshAuthority={() => refreshTargetAuthority(actionID, target.target_id)}
        handler={() => executeTarget(target)}
        label={strategy === "when_idle" ? "空き次第更新" : "更新"}
        icon={creating ? <LoaderCircle className="size-4 animate-spin" /> : <Download className="size-4" />}
        className="mt-3 w-full"
        disabled={disabled}
        title={!canExecuteSystemUpdates ? "system_updates.execute 権限が必要です。" : undefined}
      />
    );
  };
  const renderCancelAction = (job: SystemUpdateJob) => {
    const snapshot = cancelUpdateAuthoritySnapshot(job.id, job);
    const intent: UpdaterActionIntent = Object.freeze({
      id: "UPD-04",
      resourceId: job.id,
      authorityFingerprint: snapshot.fingerprint,
    });
    return (
      <UpdaterActionConfirmation
        controller={updaterActionController}
        intent={intent}
        authority={currentUpdaterAuthority(snapshot.applicable, snapshot.fingerprint)}
        refreshAuthority={() => refreshCancelAuthority(job.id)}
        handler={() => executeCancel(job)}
        label="キャンセル"
        icon={cancelling && cancellingJobID === job.id ? <LoaderCircle className="size-4 animate-spin" /> : <XCircle className="size-4" />}
        variant="outline"
        disabled={cancelling && cancellingJobID === job.id}
        title={!canExecuteSystemUpdates ? "system_updates.execute 権限が必要です。" : undefined}
      />
    );
  };
  return { renderTargetAction, renderCancelAction };
}
