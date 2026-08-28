import type { DomainStatusPresentation } from "@/lib/foundation/status/contracts";
import {
  copyCanonicalDomainStatusPresentation,
  freezeStatusMapping,
  presentStatus,
} from "@/lib/foundation/status/presenter-core";

export type PositiveNodeHealthSummary = Readonly<{
  total: number;
  positive: number;
}>;

const nodeConnectivityMapping = freezeStatusMapping({
  online: { labelKey: "statusNodeOnline", tone: "success", icon: "wifi" },
  degraded: { labelKey: "statusNodeDegraded", tone: "warning", icon: "triangle-alert" },
  offline: { labelKey: "statusNodeOffline", tone: "critical", icon: "wifi-off" },
  updating: { labelKey: "statusNodeUpdating", tone: "info", icon: "loader-circle" },
});

const nodeHealthMapping = freezeStatusMapping({
  offline: { labelKey: "statusNodeOffline", tone: "critical", icon: "wifi-off" },
  unconfigured: { labelKey: "statusNodeUnconfigured", tone: "neutral", icon: "circle-help" },
  warning: { labelKey: "statusNodeWarning", tone: "warning", icon: "triangle-alert" },
  healthy: { labelKey: "statusNodeHealthy", tone: "success", icon: "heart-pulse" },
});

const nodeOwnershipMapping = freezeStatusMapping({
  pending: { labelKey: "statusNodeRegistrationPending", tone: "info", icon: "clock-3" },
  registered: { labelKey: "statusNodeRegistered", tone: "neutral", icon: "badge-check" },
  assigned: {
    labelKey: "statusNodeAssigned",
    detailKey: "statusNodeAssignedDetail",
    tone: "info",
    icon: "link",
  },
  restart_requested: { labelKey: "statusNodeRestartRequested", tone: "warning", icon: "rotate-cw" },
});

const positiveNodeHealthPresentations = Object.freeze([
  Object.freeze({
    known: true,
    tone: "success",
    labelKey: "statusNodeHealthy",
    icon: "heart-pulse",
  } as const),
]);

export function presentNodeConnectivityStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, nodeConnectivityMapping);
}

export function presentNodeHealthStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, nodeHealthMapping);
}

export function presentNodeOwnershipStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, nodeOwnershipMapping);
}

export function summarizePositiveNodeHealth(
  presentations: readonly unknown[],
): PositiveNodeHealthSummary {
  try {
    if (!Array.isArray(presentations)) return Object.freeze({ total: 0, positive: 0 });
    let positive = 0;
    for (const presentation of presentations) {
      if (isKnownPositiveNodeHealth(presentation)) positive += 1;
    }
    return Object.freeze({ total: presentations.length, positive });
  } catch {
    return Object.freeze({ total: 0, positive: 0 });
  }
}

function isKnownPositiveNodeHealth(value: unknown): boolean {
  const canonical = copyCanonicalDomainStatusPresentation(value);
  if (!canonical || Reflect.ownKeys(canonical).length !== 4) return false;
  return positiveNodeHealthPresentations.some((positive) =>
    canonical.known === positive.known
      && canonical.tone === positive.tone
      && canonical.labelKey === positive.labelKey
      && canonical.icon === positive.icon);
}
