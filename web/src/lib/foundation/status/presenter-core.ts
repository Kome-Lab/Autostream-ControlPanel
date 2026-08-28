import type { TranslationKey } from "@/lib/i18n";
import type {
  DomainStatusPresentation,
  KnownStatusTone,
  StatusIcon,
} from "@/lib/foundation/status/contracts";

export type KnownStatusDefinition = Readonly<{
  tone: KnownStatusTone;
  labelKey: TranslationKey;
  detailKey?: TranslationKey;
  icon: StatusIcon;
  operationalCautionKey?: TranslationKey;
  safeDiagnosticCodes?: readonly string[];
}>;

export type StatusMappingDefinition = Readonly<Record<string, KnownStatusDefinition>>;

const diagnosticCodePattern = /^[a-z][a-z0-9_]{0,63}$/;

const canonicalKnownLabelKeys = Object.freeze([
  "statusArchiveAwaitingReport",
  "statusArchiveShareActive",
  "statusArchiveShareExpired",
  "statusArchiveShareRevoked",
  "statusArchiveStopping",
  "statusAuditFailure",
  "statusAuditSuccess",
  "statusDiagnosticEvaluated",
  "statusDiagnosticInconclusive",
  "statusFailed",
  "statusIncidentAcknowledged",
  "statusIncidentIgnored",
  "statusIncidentInvestigating",
  "statusIncidentMitigated",
  "statusIncidentOpen",
  "statusIncidentResolved",
  "statusNodeAssigned",
  "statusNodeDegraded",
  "statusNodeHealthy",
  "statusNodeOffline",
  "statusNodeOnline",
  "statusNodeRegistered",
  "statusNodeRegistrationPending",
  "statusNodeRestartRequested",
  "statusNodeUnconfigured",
  "statusNodeUpdating",
  "statusNodeWarning",
  "statusPolicyApplied",
  "statusPolicyFailed",
  "statusPolicyPending",
  "statusRecordingActive",
  "statusRecordingAttention",
  "statusRecordingComplete",
  "statusRecordingFinalizing",
  "statusRecordingWaiting",
  "statusRemediationApproved",
  "statusRemediationBlocked",
  "statusRemediationDisabled",
  "statusRemediationExecuted",
  "statusRemediationPendingApproval",
  "statusRemediationSuggested",
  "statusStreamCompleted",
  "statusStreamCreated",
  "statusStreamLive",
  "statusStreamStarting",
  "statusStreamStopping",
  "statusUpdateCanceled",
  "statusUpdateClaimed",
  "statusUpdateDownloading",
  "statusUpdateFailed",
  "statusUpdateHealthChecking",
  "statusUpdateInstalling",
  "statusUpdateQueued",
  "statusUpdateReconciling",
  "statusUpdateRolledBack",
  "statusUpdateRollingBack",
  "statusUpdateStaging",
  "statusUpdateStarting",
  "statusUpdateStopping",
  "statusUpdateSucceeded",
  "statusUpdateTargetReachable",
  "statusUpdateTargetUnreachable",
  "statusUpdateVerifying",
] as const satisfies readonly TranslationKey[]);

const canonicalStatusIcons = Object.freeze([
  "badge-check",
  "circle-alert",
  "circle-check",
  "circle-dot",
  "circle-help",
  "circle-slash",
  "circle-stop",
  "circle-x",
  "clock-3",
  "clock-alert",
  "download",
  "eye",
  "file-pen-line",
  "hand",
  "heart-pulse",
  "lightbulb",
  "link",
  "link-2-off",
  "loader-circle",
  "network",
  "network-x",
  "package-check",
  "package-open",
  "play",
  "radio",
  "refresh-cw",
  "rotate-cw",
  "scan-search",
  "share-2",
  "shield-alert",
  "shield-check",
  "siren",
  "stethoscope",
  "triangle-alert",
  "undo-2",
  "wifi",
  "wifi-off",
] as const);

const canonicalKnownTones = Object.freeze([
  "neutral",
  "info",
  "success",
  "warning",
  "critical",
] as const satisfies readonly KnownStatusTone[]);

const presentationRequiredKeys = Object.freeze(["known", "labelKey", "tone", "icon"] as const);
const presentationOptionalKeys = Object.freeze([
  "detailKey",
  "operationalCautionKey",
  "diagnosticCode",
] as const);

export function freezeStatusMapping(definition: StatusMappingDefinition): StatusMappingDefinition {
  const copy: Record<string, KnownStatusDefinition> = Object.create(null);
  for (const wireValue of Object.keys(definition)) {
    const value = definition[wireValue];
    copy[wireValue] = Object.freeze({
      tone: value.tone,
      labelKey: value.labelKey,
      ...(value.detailKey ? { detailKey: value.detailKey } : {}),
      icon: value.icon,
      ...(value.operationalCautionKey ? { operationalCautionKey: value.operationalCautionKey } : {}),
      ...(value.safeDiagnosticCodes ? { safeDiagnosticCodes: Object.freeze([...value.safeDiagnosticCodes]) } : {}),
    });
  }
  return Object.freeze(copy);
}

export function presentStatus(
  raw: unknown,
  mapping: StatusMappingDefinition,
): DomainStatusPresentation {
  const input = readStatusInput(raw);
  if (!input || !Object.prototype.hasOwnProperty.call(mapping, input.status)) {
    return unknownStatus();
  }
  const definition = mapping[input.status];
  const diagnosticCode = allowedDiagnosticCode(input.diagnosticCode, definition.safeDiagnosticCodes);
  return Object.freeze({
    known: true,
    tone: definition.tone,
    labelKey: definition.labelKey,
    ...(definition.detailKey ? { detailKey: definition.detailKey } : {}),
    icon: definition.icon,
    ...(definition.operationalCautionKey ? { operationalCautionKey: definition.operationalCautionKey } : {}),
    ...(diagnosticCode ? { diagnosticCode } : {}),
  });
}

export function copyCanonicalDomainStatusPresentation(
  value: unknown,
): DomainStatusPresentation | undefined {
  try {
    const properties = canonicalPresentationProperties(value);
    if (!properties) return undefined;
    const known = properties.known.value;
    const tone = properties.tone.value;
    const labelKey = properties.labelKey.value;
    const icon = properties.icon.value;
    const detailKey = properties.detailKey?.value;
    const operationalCautionKey = properties.operationalCautionKey?.value;
    const diagnosticCode = properties.diagnosticCode?.value;

    if (known === false) {
      if (tone !== "unknown"
        || labelKey !== "statusUnknown"
        || icon !== "circle-help"
        || detailKey !== "statusUnknownDetail"
        || operationalCautionKey !== undefined
        || diagnosticCode !== undefined) {
        return undefined;
      }
      return Object.freeze({
        known: false,
        tone: "unknown",
        labelKey: "statusUnknown",
        detailKey: "statusUnknownDetail",
        icon: "circle-help",
      });
    }

    if (known !== true
      || !isCanonicalKnownTone(tone)
      || !isCanonicalKnownLabelKey(labelKey)
      || !isCanonicalStatusIcon(icon)
      || !isCanonicalKnownDetail(labelKey, detailKey)
      || operationalCautionKey !== undefined
      || !isCanonicalDiagnosticCode(labelKey, diagnosticCode)) {
      return undefined;
    }

    return Object.freeze({
      known: true,
      tone,
      labelKey,
      ...(detailKey ? { detailKey } : {}),
      icon,
      ...(diagnosticCode ? { diagnosticCode } : {}),
    });
  } catch {
    return undefined;
  }
}

function unknownStatus(): DomainStatusPresentation {
  return Object.freeze({
    known: false,
    tone: "unknown",
    labelKey: "statusUnknown",
    detailKey: "statusUnknownDetail",
    icon: "circle-help",
  });
}

function readStatusInput(raw: unknown): Readonly<{ status: string; diagnosticCode?: string }> | undefined {
  if (typeof raw === "string") return Object.freeze({ status: raw });
  if (raw === null || typeof raw !== "object") return undefined;
  try {
    if (Array.isArray(raw)) return undefined;
    const status = Reflect.get(raw, "status");
    if (typeof status !== "string") return undefined;
    const diagnosticCode = Reflect.get(raw, "diagnosticCode");
    return Object.freeze({
      status,
      ...(typeof diagnosticCode === "string" ? { diagnosticCode } : {}),
    });
  } catch {
    return undefined;
  }
}

function allowedDiagnosticCode(
  candidate: string | undefined,
  allowlist: readonly string[] | undefined,
): string | undefined {
  if (!candidate || !allowlist || !diagnosticCodePattern.test(candidate)) return undefined;
  return allowlist.includes(candidate) ? candidate : undefined;
}

function canonicalPresentationProperties(value: unknown): PropertyDescriptorMap | undefined {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return undefined;
  const prototype = Object.getPrototypeOf(value);
  if (prototype !== Object.prototype && prototype !== null) return undefined;
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string")) return undefined;
  const properties = Object.getOwnPropertyDescriptors(value);
  const allowedKeys: readonly string[] = [...presentationRequiredKeys, ...presentationOptionalKeys];
  for (const required of presentationRequiredKeys) {
    if (!keys.includes(required)) return undefined;
  }
  for (const key of keys) {
    if (typeof key !== "string" || !allowedKeys.includes(key)) return undefined;
    const descriptor = properties[key];
    if (!descriptor || !descriptor.enumerable || !("value" in descriptor)) return undefined;
  }
  return properties;
}

function isCanonicalKnownTone(value: unknown): value is KnownStatusTone {
  return typeof value === "string"
    && canonicalKnownTones.some((candidate) => candidate === value);
}

function isCanonicalKnownLabelKey(value: unknown): value is TranslationKey {
  return typeof value === "string"
    && canonicalKnownLabelKeys.some((candidate) => candidate === value);
}

function isCanonicalStatusIcon(value: unknown): value is StatusIcon {
  return typeof value === "string"
    && canonicalStatusIcons.some((candidate) => candidate === value);
}

function isCanonicalKnownDetail(
  labelKey: TranslationKey,
  detailKey: unknown,
): detailKey is TranslationKey | undefined {
  if (detailKey === undefined) return true;
  return (labelKey === "statusNodeAssigned" && detailKey === "statusNodeAssignedDetail")
    || (labelKey === "statusUpdateTargetUnreachable" && detailKey === "statusUpdateTargetUnreachableDetail");
}

function isCanonicalDiagnosticCode(labelKey: TranslationKey, value: unknown): value is string | undefined {
  if (value === undefined) return true;
  return labelKey === "statusUpdateTargetUnreachable"
    && typeof value === "string"
    && diagnosticCodePattern.test(value)
    && value === "target_unreachable";
}
