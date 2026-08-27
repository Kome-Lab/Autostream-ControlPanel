import { resolveHighRiskConfirmationPlan } from "@/lib/foundation/actions/confirmation-policy";
import { isActionInvocationAllowed } from "@/lib/foundation/permissions/evaluator";
import type {
  ActionDescriptor,
  ActionEvaluation,
} from "@/lib/foundation/actions/contracts";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";

export type ConfirmationFreshness =
  | Readonly<{ kind: "fresh" }>
  | Readonly<{ kind: "refreshing" }>
  | Readonly<{ kind: "stale" }>
  | Readonly<{
      kind: "unknown";
      reason:
        | "initial"
        | "blocking-error"
        | "required-section-missing"
        | "malformed";
    }>;

export type ConfirmationAuthorityEvidence =
  | Readonly<{
      kind: "none";
    }>
  | Readonly<{
      kind: "revision";
      value: string | number;
    }>
  | Readonly<{
      kind: "safe-fingerprint";
      fieldIds: readonly [string, ...string[]];
      value: string;
    }>;

export type ConfirmationAuthoritySnapshot = Readonly<{
  actionId: string;
  targetResourceType: string;
  targetResourceId: string;
  evaluation: ActionEvaluation;
  freshness: ConfirmationFreshness;
  evidence: ConfirmationAuthorityEvidence;
}>;

export type ConfirmationGateResult =
  | Readonly<{
      kind: "allowed";
      snapshot: ConfirmationAuthoritySnapshot;
    }>
  | Readonly<{
      kind: "blocked";
      reason:
        | "invalid-plan"
        | "not-allowed"
        | "freshness-unavailable"
        | "authority-unavailable"
        | "target-changed"
        | "authority-changed";
    }>;

const freshResult = Object.freeze({ kind: "fresh" } as const satisfies ConfirmationFreshness);
const refreshingResult = Object.freeze({ kind: "refreshing" } as const satisfies ConfirmationFreshness);
const staleResult = Object.freeze({ kind: "stale" } as const satisfies ConfirmationFreshness);
const malformedFreshness = Object.freeze({
  kind: "unknown",
  reason: "malformed",
} as const satisfies ConfirmationFreshness);

export function confirmationFreshnessFromRemoteState(
  descriptor: ActionDescriptor,
  state: RemoteState<unknown>,
): ConfirmationFreshness {
  try {
    const requiredSections = copyRequiredSections(descriptor);
    if (!requiredSections || !isObjectLike(state)) return malformedFreshness;
    const kind = Reflect.get(state, "kind");
    if (kind === "initial-loading") {
      if (hasAnyProperty(state, ["data", "freshness", "error"])) return malformedFreshness;
      return Object.freeze({ kind: "unknown", reason: "initial" });
    }
    if (kind === "blocking-error") {
      if (
        hasAnyProperty(state, ["data", "freshness"])
        || !isAdaptedErrorRecord(Reflect.get(state, "error"))
      ) {
        return malformedFreshness;
      }
      return Object.freeze({ kind: "unknown", reason: "blocking-error" });
    }
    if (kind !== "empty" && kind !== "ready" && kind !== "partial") {
      return malformedFreshness;
    }
    if ((kind === "ready" || kind === "partial") && !Reflect.has(state, "data")) {
      return malformedFreshness;
    }
    if (Reflect.has(state, "error") || (kind === "empty" && Reflect.has(state, "data"))) {
      return malformedFreshness;
    }
    if (kind === "partial") {
      const missingSections = copyStringArray(Reflect.get(state, "missingSections"), false);
      const sectionErrors = Reflect.get(state, "sectionErrors");
      if (!missingSections || !isSectionErrorRecord(sectionErrors)) return malformedFreshness;
      if (requiredSections.some((section) => missingSections.includes(section))) {
        return Object.freeze({ kind: "unknown", reason: "required-section-missing" });
      }
    }
    return copyRemoteFreshness(Reflect.get(state, "freshness")) ?? malformedFreshness;
  } catch {
    return malformedFreshness;
  }
}

export function copyConfirmationAuthoritySnapshot(
  value: ConfirmationAuthoritySnapshot,
): ConfirmationAuthoritySnapshot | undefined {
  if (!isObjectLike(value)) return undefined;
  try {
    const actionId = copyIdentity(Reflect.get(value, "actionId"));
    const targetResourceType = copyIdentity(Reflect.get(value, "targetResourceType"));
    const targetResourceId = copyIdentity(Reflect.get(value, "targetResourceId"));
    const evaluation = copyEvaluation(Reflect.get(value, "evaluation"));
    const freshness = copyConfirmationFreshness(Reflect.get(value, "freshness"));
    const evidence = copyEvidence(Reflect.get(value, "evidence"));
    if (!actionId || !targetResourceType || !targetResourceId || !evaluation || !freshness || !evidence) {
      return undefined;
    }
    return Object.freeze({
      actionId,
      targetResourceType,
      targetResourceId,
      evaluation,
      freshness,
      evidence,
    });
  } catch {
    return undefined;
  }
}

export function evaluateConfirmationOpen(
  descriptor: ActionDescriptor,
  state: RemoteState<unknown>,
  evaluation: ActionEvaluation,
  evidence: ConfirmationAuthorityEvidence,
): ConfirmationGateResult {
  try {
    const plan = resolveHighRiskConfirmationPlan(descriptor);
    if (plan.kind === "invalid") return blocked("invalid-plan");
    const copiedEvaluation = copyEvaluation(evaluation);
    if (!copiedEvaluation || !isActionInvocationAllowed(copiedEvaluation)) return blocked("not-allowed");
    const freshness = confirmationFreshnessFromRemoteState(descriptor, state);
    if (!freshnessAcceptable(plan.requireSubmitRevalidation, freshness)) {
      return blocked("freshness-unavailable");
    }
    const copiedEvidence = evidenceForDescriptor(descriptor, plan.requireSubmitRevalidation, evidence);
    if (!copiedEvidence) return blocked("authority-unavailable");
    const identity = copyDescriptorIdentity(descriptor);
    if (!identity) return blocked("authority-unavailable");
    const snapshot = copyConfirmationAuthoritySnapshot({
      ...identity,
      evaluation: copiedEvaluation,
      freshness,
      evidence: copiedEvidence,
    });
    return snapshot
      ? Object.freeze({ kind: "allowed", snapshot })
      : blocked("authority-unavailable");
  } catch {
    return blocked("authority-unavailable");
  }
}

export function evaluateConfirmationSubmit(
  descriptor: ActionDescriptor,
  opened: ConfirmationAuthoritySnapshot,
  currentState: RemoteState<unknown>,
  currentEvaluation: ActionEvaluation,
  currentEvidence: ConfirmationAuthorityEvidence,
): ConfirmationGateResult {
  try {
    const plan = resolveHighRiskConfirmationPlan(descriptor);
    if (plan.kind === "invalid") return blocked("invalid-plan");
    const openedCopy = copyConfirmationAuthoritySnapshot(opened);
    if (!openedCopy || !isActionInvocationAllowed(openedCopy.evaluation)) {
      return blocked("authority-unavailable");
    }
    if (!freshnessAcceptable(plan.requireSubmitRevalidation, openedCopy.freshness)) {
      return blocked("freshness-unavailable");
    }
    const openedEvidence = evidenceForDescriptor(
      descriptor,
      plan.requireSubmitRevalidation,
      openedCopy.evidence,
    );
    if (!openedEvidence) return blocked("authority-unavailable");
    const identity = copyDescriptorIdentity(descriptor);
    if (!identity) return blocked("authority-unavailable");
    if (
      openedCopy.actionId !== identity.actionId
      || openedCopy.targetResourceType !== identity.targetResourceType
      || openedCopy.targetResourceId !== identity.targetResourceId
    ) {
      return blocked("target-changed");
    }

    const copiedEvaluation = copyEvaluation(currentEvaluation);
    if (!copiedEvaluation || !isActionInvocationAllowed(copiedEvaluation)) return blocked("not-allowed");
    const freshness = confirmationFreshnessFromRemoteState(descriptor, currentState);
    if (!freshnessAcceptable(plan.requireSubmitRevalidation, freshness)) {
      return blocked("freshness-unavailable");
    }
    const copiedEvidence = evidenceForDescriptor(
      descriptor,
      plan.requireSubmitRevalidation,
      currentEvidence,
    );
    if (!copiedEvidence) return blocked("authority-unavailable");
    if (!evidenceMatches(openedEvidence, copiedEvidence)) return blocked("authority-changed");

    const snapshot = copyConfirmationAuthoritySnapshot({
      ...identity,
      evaluation: copiedEvaluation,
      freshness,
      evidence: copiedEvidence,
    });
    return snapshot
      ? Object.freeze({ kind: "allowed", snapshot })
      : blocked("authority-unavailable");
  } catch {
    return blocked("authority-unavailable");
  }
}

function copyDescriptorIdentity(descriptor: unknown) {
  if (!isObjectLike(descriptor)) return undefined;
  const actionId = copyIdentity(Reflect.get(descriptor, "id"));
  const target = Reflect.get(descriptor, "target");
  if (!actionId || !isObjectLike(target)) return undefined;
  const targetResourceType = copyIdentity(Reflect.get(target, "resourceType"));
  const targetResourceId = copyIdentity(Reflect.get(target, "resourceId"));
  return targetResourceType && targetResourceId
    ? Object.freeze({ actionId, targetResourceType, targetResourceId })
    : undefined;
}

function copyRequiredSections(descriptor: unknown) {
  if (!isObjectLike(descriptor)) return undefined;
  const applicability = Reflect.get(descriptor, "applicability");
  if (!isObjectLike(applicability)) return undefined;
  return copyStringArray(Reflect.get(applicability, "requiredSections"), false);
}

function copyRemoteFreshness(value: unknown): ConfirmationFreshness | undefined {
  if (!isObjectLike(value)) return undefined;
  const kind = Reflect.get(value, "kind");
  const lastSuccessAt = Reflect.get(value, "lastSuccessAt");
  if (
    typeof lastSuccessAt !== "number"
    || !Number.isFinite(lastSuccessAt)
    || lastSuccessAt < 0
  ) {
    return undefined;
  }
  if (kind === "fresh" || kind === "refreshing") {
    if (Reflect.has(value, "error")) return undefined;
    return kind === "fresh" ? freshResult : refreshingResult;
  }
  if (kind !== "stale" || !isAdaptedErrorRecord(Reflect.get(value, "error"))) return undefined;
  return staleResult;
}

function copyConfirmationFreshness(value: unknown): ConfirmationFreshness | undefined {
  if (!isObjectLike(value)) return undefined;
  const kind = Reflect.get(value, "kind");
  if (kind === "fresh") return freshResult;
  if (kind === "refreshing") return refreshingResult;
  if (kind === "stale") return staleResult;
  if (kind !== "unknown") return undefined;
  const reason = Reflect.get(value, "reason");
  return reason === "initial"
    || reason === "blocking-error"
    || reason === "required-section-missing"
    || reason === "malformed"
    ? Object.freeze({ kind, reason })
    : undefined;
}

function copyEvaluation(value: unknown): ActionEvaluation | undefined {
  if (!isObjectLike(value)) return undefined;
  const visibility = Reflect.get(value, "visibility");
  const availability = Reflect.get(value, "availability");
  if (!isObjectLike(visibility) || !isObjectLike(availability)) return undefined;
  const visibilityKind = Reflect.get(visibility, "kind");
  const visibilityReason = Reflect.get(visibility, "reason");
  let copiedVisibility: ActionEvaluation["visibility"];
  if (visibilityKind === "visible" && visibilityReason === undefined) {
    copiedVisibility = Object.freeze({ kind: "visible" });
  } else if (
    visibilityKind === "hidden"
    && (visibilityReason === "not-applicable" || visibilityReason === "security-sensitive")
  ) {
    copiedVisibility = Object.freeze({ kind: "hidden", reason: visibilityReason });
  } else {
    return undefined;
  }

  const availabilityKind = Reflect.get(availability, "kind");
  const reasonKey = Reflect.get(availability, "reasonKey");
  let copiedAvailability: ActionEvaluation["availability"];
  if (availabilityKind === "allowed" && reasonKey === undefined) {
    copiedAvailability = Object.freeze({ kind: "allowed" });
  } else if (
    (availabilityKind === "denied"
      || availabilityKind === "blocked"
      || availabilityKind === "unknown"
      || availabilityKind === "pending")
    && copyIdentity(reasonKey)
  ) {
    copiedAvailability = Object.freeze({ kind: availabilityKind, reasonKey });
  } else {
    return undefined;
  }
  return Object.freeze({ visibility: copiedVisibility, availability: copiedAvailability });
}

function copyEvidence(value: unknown): ConfirmationAuthorityEvidence | undefined {
  if (!isObjectLike(value)) return undefined;
  const kind = Reflect.get(value, "kind");
  if (kind === "none") {
    return Reflect.get(value, "value") === undefined && Reflect.get(value, "fieldIds") === undefined
      ? Object.freeze({ kind })
      : undefined;
  }
  if (kind === "revision") {
    const revision = Reflect.get(value, "value");
    if (typeof revision === "number") {
      return Number.isFinite(revision) ? Object.freeze({ kind, value: revision }) : undefined;
    }
    const copied = copyIdentity(revision);
    return copied ? Object.freeze({ kind, value: copied }) : undefined;
  }
  if (kind !== "safe-fingerprint") return undefined;
  const fieldIds = copyNonEmptyStringArray(Reflect.get(value, "fieldIds"));
  const fingerprint = copyIdentity(Reflect.get(value, "value"));
  if (!fieldIds || !fingerprint) return undefined;
  return Object.freeze({
    kind,
    fieldIds,
    value: fingerprint,
  });
}

function evidenceForDescriptor(
  descriptor: unknown,
  requireSubmitRevalidation: boolean,
  evidence: unknown,
): ConfirmationAuthorityEvidence | undefined {
  const copiedEvidence = copyEvidence(evidence);
  if (!copiedEvidence || !isObjectLike(descriptor)) return undefined;
  const revalidation = Reflect.get(descriptor, "revalidation");
  if (!requireSubmitRevalidation) {
    if (copiedEvidence.kind !== "none") return undefined;
    if (revalidation === undefined) return copiedEvidence;
    return isObjectLike(revalidation) && Reflect.get(revalidation, "kind") === "none"
      ? copiedEvidence
      : undefined;
  }
  if (!isObjectLike(revalidation)) return undefined;
  const kind = Reflect.get(revalidation, "kind");
  if (kind === "revision") return copiedEvidence.kind === "revision" ? copiedEvidence : undefined;
  if (kind !== "safe-fingerprint" || copiedEvidence.kind !== "safe-fingerprint") return undefined;
  const fieldIds = copyStringArray(Reflect.get(revalidation, "fieldIds"), true);
  return fieldIds && stringArraysEqual(fieldIds, copiedEvidence.fieldIds)
    ? copiedEvidence
    : undefined;
}

function evidenceMatches(
  opened: ConfirmationAuthorityEvidence,
  current: ConfirmationAuthorityEvidence,
) {
  if (opened.kind !== current.kind) return false;
  if (opened.kind === "none" && current.kind === "none") return true;
  if (opened.kind === "revision" && current.kind === "revision") return opened.value === current.value;
  return opened.kind === "safe-fingerprint"
    && current.kind === "safe-fingerprint"
    && stringArraysEqual(opened.fieldIds, current.fieldIds)
    && opened.value === current.value;
}

function freshnessAcceptable(
  requireSubmitRevalidation: boolean,
  freshness: ConfirmationFreshness,
) {
  if (freshness.kind === "unknown") return false;
  return !requireSubmitRevalidation || freshness.kind === "fresh";
}

function copyStringArray(value: unknown, nonEmpty: boolean): readonly string[] | undefined {
  if (!Array.isArray(value) || (nonEmpty && value.length === 0)) return undefined;
  const copy: string[] = [];
  for (let index = 0; index < value.length; index += 1) {
    const item = copyIdentity(value[index]);
    if (!item || copy.includes(item)) return undefined;
    copy.push(item);
  }
  return Object.freeze(copy);
}

function copyNonEmptyStringArray(value: unknown): readonly [string, ...string[]] | undefined {
  if (!Array.isArray(value) || value.length === 0) return undefined;
  const first = copyIdentity(value[0]);
  if (!first) return undefined;
  const copy: [string, ...string[]] = [first];
  for (let index = 1; index < value.length; index += 1) {
    const item = copyIdentity(value[index]);
    if (!item || copy.includes(item)) return undefined;
    copy.push(item);
  }
  return Object.freeze(copy);
}

function hasAnyProperty(value: object, keys: readonly string[]) {
  return keys.some((key) => Reflect.has(value, key));
}

function isPlainRecord(value: unknown): value is Readonly<Record<PropertyKey, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const prototype = Reflect.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function isSectionErrorRecord(value: unknown) {
  if (!isPlainRecord(value)) return false;
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== "string" || !copyIdentity(key) || !isAdaptedErrorRecord(Reflect.get(value, key))) {
      return false;
    }
  }
  return true;
}

function isAdaptedErrorRecord(value: unknown) {
  if (!isPlainRecord(value)) return false;
  const kind = Reflect.get(value, "kind");
  const supportedKind = kind === "unauthenticated"
    || kind === "forbidden"
    || kind === "validation"
    || kind === "not_found"
    || kind === "conflict"
    || kind === "protected_state"
    || kind === "rate_limited"
    || kind === "timeout"
    || kind === "unavailable"
    || kind === "network"
    || kind === "protocol"
    || kind === "unknown";
  return supportedKind && copyIdentity(Reflect.get(value, "messageKey")) !== undefined;
}

function stringArraysEqual(left: readonly string[], right: readonly string[]) {
  if (left.length !== right.length) return false;
  for (let index = 0; index < left.length; index += 1) {
    if (left[index] !== right[index]) return false;
  }
  return true;
}

function copyIdentity(value: unknown) {
  return typeof value === "string"
    && value.length > 0
    && value.trim() === value
    && !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value)
    ? value
    : undefined;
}

function blocked(
  reason: Extract<ConfirmationGateResult, { kind: "blocked" }>["reason"],
): ConfirmationGateResult {
  return Object.freeze({ kind: "blocked", reason });
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}
