import type { DomainStatusPresentation } from "@/lib/foundation/status/contracts";
import {
  freezeStatusMapping,
  presentStatus,
} from "@/lib/foundation/status/presenter-core";

const incidentMapping = freezeStatusMapping({
  open: { labelKey: "statusIncidentOpen", tone: "critical", icon: "siren" },
  acknowledged: { labelKey: "statusIncidentAcknowledged", tone: "warning", icon: "eye" },
  investigating: { labelKey: "statusIncidentInvestigating", tone: "info", icon: "scan-search" },
  mitigated: { labelKey: "statusIncidentMitigated", tone: "warning", icon: "shield-check" },
  resolved: { labelKey: "statusIncidentResolved", tone: "success", icon: "circle-check" },
  ignored: { labelKey: "statusIncidentIgnored", tone: "neutral", icon: "eye" },
});

const diagnosticMapping = freezeStatusMapping({
  evaluated: { labelKey: "statusDiagnosticEvaluated", tone: "info", icon: "scan-search" },
  inconclusive: { labelKey: "statusDiagnosticInconclusive", tone: "warning", icon: "circle-help" },
});

const remediationMapping = freezeStatusMapping({
  disabled: { labelKey: "statusRemediationDisabled", tone: "neutral", icon: "circle-slash" },
  suggested: { labelKey: "statusRemediationSuggested", tone: "info", icon: "lightbulb" },
  pending_approval: { labelKey: "statusRemediationPendingApproval", tone: "warning", icon: "clock-3" },
  approved: { labelKey: "statusRemediationApproved", tone: "info", icon: "badge-check" },
  executed: { labelKey: "statusRemediationExecuted", tone: "success", icon: "circle-check" },
  blocked: { labelKey: "statusRemediationBlocked", tone: "warning", icon: "circle-slash" },
});

const auditResultMapping = freezeStatusMapping({
  success: { labelKey: "statusAuditSuccess", tone: "success", icon: "circle-check" },
  failure: { labelKey: "statusAuditFailure", tone: "critical", icon: "circle-alert" },
});

export function presentIncidentStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, incidentMapping);
}

export function presentDiagnosticStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, diagnosticMapping);
}

export function presentRemediationStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, remediationMapping);
}

export function presentAuditResultStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, auditResultMapping);
}
