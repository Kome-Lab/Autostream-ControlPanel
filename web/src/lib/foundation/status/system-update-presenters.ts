import type { DomainStatusPresentation } from "@/lib/foundation/status/contracts";
import {
  freezeStatusMapping,
  presentStatus,
} from "@/lib/foundation/status/presenter-core";

const systemUpdateTargetMapping = freezeStatusMapping({
  reachable: { labelKey: "statusUpdateTargetReachable", tone: "success", icon: "network" },
  unreachable: {
    labelKey: "statusUpdateTargetUnreachable",
    detailKey: "statusUpdateTargetUnreachableDetail",
    tone: "critical",
    icon: "network-x",
    safeDiagnosticCodes: ["target_unreachable"],
  },
});

const systemUpdateJobMapping = freezeStatusMapping({
  queued: { labelKey: "statusUpdateQueued", tone: "neutral", icon: "clock-3" },
  claimed: { labelKey: "statusUpdateClaimed", tone: "info", icon: "hand" },
  downloading: { labelKey: "statusUpdateDownloading", tone: "info", icon: "download" },
  verifying: { labelKey: "statusUpdateVerifying", tone: "info", icon: "scan-search" },
  staging: { labelKey: "statusUpdateStaging", tone: "info", icon: "package-open" },
  stopping: { labelKey: "statusUpdateStopping", tone: "warning", icon: "circle-stop" },
  installing: { labelKey: "statusUpdateInstalling", tone: "warning", icon: "package-check" },
  starting: { labelKey: "statusUpdateStarting", tone: "info", icon: "play" },
  health_checking: { labelKey: "statusUpdateHealthChecking", tone: "info", icon: "stethoscope" },
  reconciling: { labelKey: "statusUpdateReconciling", tone: "warning", icon: "refresh-cw" },
  rolling_back: { labelKey: "statusUpdateRollingBack", tone: "warning", icon: "undo-2" },
  succeeded: { labelKey: "statusUpdateSucceeded", tone: "success", icon: "circle-check" },
  rolled_back: { labelKey: "statusUpdateRolledBack", tone: "warning", icon: "undo-2" },
  failed: { labelKey: "statusUpdateFailed", tone: "critical", icon: "circle-alert" },
  canceled: { labelKey: "statusUpdateCanceled", tone: "neutral", icon: "circle-x" },
});

const systemUpdatePolicyMapping = freezeStatusMapping({
  applied: { labelKey: "statusPolicyApplied", tone: "success", icon: "shield-check" },
  pending: { labelKey: "statusPolicyPending", tone: "warning", icon: "clock-3" },
  failed: { labelKey: "statusPolicyFailed", tone: "critical", icon: "shield-alert" },
});

export function presentSystemUpdateTargetStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, systemUpdateTargetMapping);
}

export function presentSystemUpdateJobStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, systemUpdateJobMapping);
}

export function presentSystemUpdatePolicyStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, systemUpdatePolicyMapping);
}
