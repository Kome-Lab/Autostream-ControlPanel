import type {
  ActionDescriptor,
  ActionRisk,
  ConfirmationMode,
  MutationOutcome,
} from "@/lib/foundation/actions/contracts";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";

export type ConfirmationRiskDefault = Readonly<{
  minimumMode: ConfirmationMode;
  submitRevalidation:
    | "normally-not-required"
    | "required-when-state-dependent"
    | "required";
  ambiguousRetry:
    | "server-idempotent-only"
    | "manual-after-refresh"
    | "never";
}>;

export const confirmationRiskDefaults: Readonly<Record<ActionRisk, ConfirmationRiskDefault>> = Object.freeze({
  routine: Object.freeze({
    minimumMode: "none",
    submitRevalidation: "normally-not-required",
    ambiguousRetry: "server-idempotent-only",
  }),
  guarded: Object.freeze({
    minimumMode: "consequence",
    submitRevalidation: "required-when-state-dependent",
    ambiguousRetry: "manual-after-refresh",
  }),
  high: Object.freeze({
    minimumMode: "consequence",
    submitRevalidation: "required",
    ambiguousRetry: "never",
  }),
  critical: Object.freeze({
    minimumMode: "typed-target",
    submitRevalidation: "required",
    ambiguousRetry: "server-idempotent-only",
  }),
});

export type ResolvedConfirmationPlan =
  | Readonly<{
      kind: "consequence";
      requireSubmitRevalidation: boolean;
    }>
  | Readonly<{
      kind: "typed-target";
      requireSubmitRevalidation: true;
      token: string;
      tokenSource: "target-label" | "public-stable-id" | "fixed-ascii";
    }>
  | Readonly<{
      kind: "invalid";
      reason:
        | "malformed-descriptor"
        | "risk-policy-mismatch"
        | "missing-revalidation-policy"
        | "unsafe-token-source";
    }>;

export type ResolvedTypedConfirmationToken =
  | Readonly<{
      kind: "resolved";
      value: string;
      source: "target-label" | "public-stable-id" | "fixed-ascii";
    }>
  | Readonly<{
      kind: "invalid";
      reason: string;
    }>;

export type ConfirmationOutcomePresentation =
  | Readonly<{ kind: "succeeded" }>
  | Readonly<{ kind: "conflict"; error: AdaptedAPIError; refreshRequired: true }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{
      kind: "outcome-unknown";
      nextAction:
        | "refresh-resource"
        | "inspect-audit"
        | "contact-operator";
    }>;

type DescriptorCore = Readonly<{
  id: string;
  labelKey: string;
  risk: ActionRisk;
  target: Readonly<{
    resourceType: string;
    resourceId: string;
    publicLabel?: string;
    publicStableId?: string;
  }>;
  requiredSections: readonly string[];
  confirmation: Readonly<{
    mode: ConfirmationMode;
    consequenceKey?: string;
    requireSubmitRevalidation: boolean;
    typedToken?: Readonly<{
      kind: "target-label" | "public-stable-id" | "fixed-ascii";
      value?: string;
    }>;
  }>;
  retry: Readonly<{
    kind: "never" | "manual-after-refresh" | "server-idempotent" | "lookup-only";
    maxAttempts?: number;
  }>;
  revalidation: Readonly<{
    kind: "none" | "revision" | "safe-fingerprint";
    fieldIds?: readonly string[];
  }> | undefined;
  stateIndependent: boolean | undefined;
}>;

const modeStrength: Readonly<Record<ConfirmationMode, number>> = Object.freeze({
  none: 0,
  consequence: 1,
  "typed-target": 2,
});

const malformedPlan = Object.freeze({
  kind: "invalid",
  reason: "malformed-descriptor",
} as const satisfies ResolvedConfirmationPlan);

const protocolError = Object.freeze({
  kind: "protocol",
  messageKey: "apiErrorProtocol",
} as const satisfies AdaptedAPIError);

export function confirmationRiskDefault(
  risk: ActionRisk,
): ConfirmationRiskDefault | undefined {
  switch (risk) {
    case "routine":
      return confirmationRiskDefaults.routine;
    case "guarded":
      return confirmationRiskDefaults.guarded;
    case "high":
      return confirmationRiskDefaults.high;
    case "critical":
      return confirmationRiskDefaults.critical;
    default:
      return undefined;
  }
}

export function resolveHighRiskConfirmationPlan(
  descriptor: ActionDescriptor,
): ResolvedConfirmationPlan {
  const core = copyDescriptorCore(descriptor);
  if (!core) return malformedPlan;

  const defaults = confirmationRiskDefault(core.risk);
  if (!defaults || core.confirmation.mode === "none") {
    return invalidPlan("risk-policy-mismatch");
  }
  if (modeStrength[core.confirmation.mode] < modeStrength[defaults.minimumMode]) {
    return invalidPlan("risk-policy-mismatch");
  }
  if (!retryMatchesRisk(core.risk, core.retry)) {
    return invalidPlan("risk-policy-mismatch");
  }

  const stateDependent = core.stateIndependent !== true
    || core.requiredSections.length > 0
    || core.revalidation?.kind === "revision"
    || core.revalidation?.kind === "safe-fingerprint";
  const requiresSubmitRevalidation = core.risk === "high"
    || core.risk === "critical"
    || (core.risk === "guarded" && stateDependent)
    || core.confirmation.requireSubmitRevalidation;

  if (requiresSubmitRevalidation && core.confirmation.requireSubmitRevalidation !== true) {
    return invalidPlan("risk-policy-mismatch");
  }
  if (requiresSubmitRevalidation && !isRevalidationAuthority(core.revalidation)) {
    return invalidPlan("missing-revalidation-policy");
  }
  if (!requiresSubmitRevalidation && isRevalidationAuthority(core.revalidation)) {
    return invalidPlan("risk-policy-mismatch");
  }

  if (core.confirmation.mode === "consequence") {
    return Object.freeze({
      kind: "consequence",
      requireSubmitRevalidation: requiresSubmitRevalidation,
    });
  }

  const token = resolveTypedTokenFromCore(core);
  if (token.kind === "invalid") return invalidPlan("unsafe-token-source");
  return Object.freeze({
    kind: "typed-target",
    requireSubmitRevalidation: true,
    token: token.value,
    tokenSource: token.source,
  });
}

export function resolveTypedConfirmationToken(
  descriptor: ActionDescriptor,
): ResolvedTypedConfirmationToken {
  const core = copyDescriptorCore(descriptor);
  if (!core) return invalidToken("malformed-descriptor");
  return resolveTypedTokenFromCore(core);
}

export function confirmationPublicTargetValues(
  descriptor: ActionDescriptor,
): readonly string[] {
  const core = copyDescriptorCore(descriptor);
  if (!core) return Object.freeze([]);
  const values: string[] = [];
  if (core.target.publicLabel !== undefined && isSafePublicLiteral(core.target.publicLabel)) {
    values.push(core.target.publicLabel);
  }
  if (
    core.target.publicStableId !== undefined
    && isSafePublicLiteral(core.target.publicStableId)
    && !values.includes(core.target.publicStableId)
  ) {
    values.push(core.target.publicStableId);
  }
  return Object.freeze(values);
}

export function typedConfirmationMatches(
  input: string,
  requiredToken: string,
): boolean {
  return input === requiredToken;
}

export function confirmationOutcomePresentation<T>(
  outcome: MutationOutcome<T>,
): ConfirmationOutcomePresentation {
  try {
    if (!isObjectLike(outcome)) return failedProtocolPresentation();
    const kind = Reflect.get(outcome, "kind");
    if (kind === "succeeded") return Object.freeze({ kind: "succeeded" });
    if (kind === "outcome_unknown") {
      const nextAction = Reflect.get(outcome, "nextAction");
      if (
        nextAction !== "refresh-resource"
        && nextAction !== "inspect-audit"
        && nextAction !== "contact-operator"
      ) {
        return failedProtocolPresentation();
      }
      return Object.freeze({ kind: "outcome-unknown", nextAction });
    }
    if (kind !== "failed") return failedProtocolPresentation();
    const error = Reflect.get(outcome, "error");
    if (!isAdaptedError(error)) return failedProtocolPresentation();
    if (error.kind === "conflict" || error.kind === "protected_state") {
      return Object.freeze({ kind: "conflict", error, refreshRequired: true });
    }
    return Object.freeze({ kind: "failed", error });
  } catch {
    return failedProtocolPresentation();
  }
}

function copyDescriptorCore(value: unknown): DescriptorCore | undefined {
  if (!isObjectLike(value)) return undefined;
  try {
    const id = copyIdentity(Reflect.get(value, "id"));
    const labelKey = copyIdentity(Reflect.get(value, "labelKey"));
    const risk = copyRisk(Reflect.get(value, "risk"));
    const target = copyTarget(Reflect.get(value, "target"));
    const applicability = Reflect.get(value, "applicability");
    const confirmation = copyConfirmation(Reflect.get(value, "confirmation"));
    const duplicate = Reflect.get(value, "duplicate");
    const retry = copyRetry(Reflect.get(value, "retry"));
    const audit = Reflect.get(value, "audit");
    const revalidation = copyRevalidation(Reflect.get(value, "revalidation"));
    const stateIndependent = Reflect.get(value, "stateIndependent");
    if (
      !id
      || !labelKey
      || !risk
      || !target
      || !isObjectLike(applicability)
      || !confirmation
      || !isValidDuplicate(duplicate)
      || !retry
      || !isValidAudit(audit)
      || revalidation === null
      || (stateIndependent !== undefined && typeof stateIndependent !== "boolean")
    ) {
      return undefined;
    }
    const ruleIds = copySafeStringArray(Reflect.get(applicability, "ruleIds"), false);
    const requiredSections = copySafeStringArray(Reflect.get(applicability, "requiredSections"), false);
    if (!ruleIds || !requiredSections) return undefined;
    return Object.freeze({
      id,
      labelKey,
      risk,
      target,
      requiredSections,
      confirmation,
      retry,
      revalidation,
      stateIndependent,
    });
  } catch {
    return undefined;
  }
}

function copyTarget(value: unknown): DescriptorCore["target"] | undefined {
  if (!isObjectLike(value)) return undefined;
  const resourceType = copyIdentity(Reflect.get(value, "resourceType"));
  const resourceId = copyIdentity(Reflect.get(value, "resourceId"));
  const publicLabel = Reflect.get(value, "publicLabel");
  const publicStableId = Reflect.get(value, "publicStableId");
  if (
    !resourceType
    || !resourceId
    || (publicLabel !== undefined && typeof publicLabel !== "string")
    || (publicStableId !== undefined && typeof publicStableId !== "string")
  ) {
    return undefined;
  }
  return Object.freeze({
    resourceType,
    resourceId,
    ...(publicLabel === undefined ? {} : { publicLabel }),
    ...(publicStableId === undefined ? {} : { publicStableId }),
  });
}

function copyConfirmation(value: unknown): DescriptorCore["confirmation"] | undefined {
  if (!isObjectLike(value)) return undefined;
  const mode = Reflect.get(value, "mode");
  const requireSubmitRevalidation = Reflect.get(value, "requireSubmitRevalidation");
  const consequenceKey = Reflect.get(value, "consequenceKey");
  const typedToken = Reflect.get(value, "typedToken");
  if (
    (mode !== "none" && mode !== "consequence" && mode !== "typed-target")
    || typeof requireSubmitRevalidation !== "boolean"
  ) {
    return undefined;
  }
  if (mode === "none") {
    return consequenceKey === undefined && typedToken === undefined
      ? Object.freeze({ mode, requireSubmitRevalidation })
      : undefined;
  }
  if (!copyIdentity(consequenceKey)) return undefined;
  if (mode === "consequence") {
    return typedToken === undefined
      ? Object.freeze({ mode, consequenceKey, requireSubmitRevalidation })
      : undefined;
  }
  if (requireSubmitRevalidation !== true || !isObjectLike(typedToken)) return undefined;
  const tokenKind = Reflect.get(typedToken, "kind");
  const tokenValue = Reflect.get(typedToken, "value");
  if (tokenKind !== "target-label" && tokenKind !== "public-stable-id" && tokenKind !== "fixed-ascii") {
    return undefined;
  }
  if (tokenKind === "fixed-ascii") {
    if (typeof tokenValue !== "string") return undefined;
  } else if (tokenValue !== undefined) {
    return undefined;
  }
  return Object.freeze({
    mode,
    consequenceKey,
    requireSubmitRevalidation: true,
    typedToken: Object.freeze({ kind: tokenKind, ...(tokenValue === undefined ? {} : { value: tokenValue }) }),
  });
}

function copyRetry(value: unknown): DescriptorCore["retry"] | undefined {
  if (!isObjectLike(value)) return undefined;
  const kind = Reflect.get(value, "kind");
  const maxAttempts = Reflect.get(value, "maxAttempts");
  if (kind === "server-idempotent") {
    return typeof maxAttempts === "number"
      && Number.isFinite(maxAttempts)
      && Number.isInteger(maxAttempts)
      && maxAttempts > 0
      ? Object.freeze({ kind, maxAttempts })
      : undefined;
  }
  if (kind !== "never" && kind !== "manual-after-refresh" && kind !== "lookup-only") return undefined;
  return maxAttempts === undefined ? Object.freeze({ kind }) : undefined;
}

function copyRevalidation(value: unknown): DescriptorCore["revalidation"] | null {
  if (value === undefined) return undefined;
  if (!isObjectLike(value)) return null;
  const kind = Reflect.get(value, "kind");
  const fieldIds = Reflect.get(value, "fieldIds");
  if (kind === "none" || kind === "revision") {
    return fieldIds === undefined ? Object.freeze({ kind }) : null;
  }
  if (kind !== "safe-fingerprint") return null;
  const copied = copySafeStringArray(fieldIds, true);
  return copied ? Object.freeze({ kind, fieldIds: copied }) : null;
}

function isValidDuplicate(value: unknown) {
  if (!isObjectLike(value)) return false;
  const scope = Reflect.get(value, "scope");
  return (
    scope === "resource-action"
    || scope === "resource-target"
    || scope === "session-flow"
    || scope === "fleet"
  ) && Reflect.get(value, "whilePending") === "block";
}

function isValidAudit(value: unknown) {
  if (!isObjectLike(value)) return false;
  return copyIdentity(Reflect.get(value, "action")) !== undefined
    && copyIdentity(Reflect.get(value, "labelKey")) !== undefined
    && copySafeStringArray(Reflect.get(value, "safeReferenceFieldIds"), false) !== undefined;
}

function resolveTypedTokenFromCore(core: DescriptorCore): ResolvedTypedConfirmationToken {
  if (core.confirmation.mode !== "typed-target" || !core.confirmation.typedToken) {
    return invalidToken("typed-target-required");
  }
  const source = core.confirmation.typedToken.kind;
  if (source === "fixed-ascii") {
    const value = core.confirmation.typedToken.value;
    return typeof value === "string" && isSafeFixedAscii(value)
      ? Object.freeze({ kind: "resolved", value, source })
      : invalidToken("unsafe-fixed-ascii");
  }
  const value = source === "target-label"
    ? core.target.publicLabel
    : core.target.publicStableId;
  return typeof value === "string" && isSafePublicLiteral(value)
    ? Object.freeze({ kind: "resolved", value, source })
    : invalidToken(`unsafe-${source}`);
}

function retryMatchesRisk(risk: ActionRisk, retry: DescriptorCore["retry"]) {
  if (retry.kind === "lookup-only" || retry.kind === "never") return true;
  if (risk === "routine") return retry.kind === "server-idempotent";
  if (risk === "guarded") return retry.kind === "manual-after-refresh";
  if (risk === "critical") return retry.kind === "server-idempotent";
  return false;
}

function isRevalidationAuthority(value: DescriptorCore["revalidation"]) {
  return value?.kind === "revision" || value?.kind === "safe-fingerprint";
}

function isSafePublicLiteral(value: string) {
  const codePointLength = [...value].length;
  return codePointLength >= 1
    && codePointLength <= 128
    && value.trim() === value
    && !hasControlOrBidi(value)
    && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value)
    && !/^\/\//.test(value)
    && !/^[^\s@]+@[^\s@]+$/.test(value);
}

function isSafeFixedAscii(value: string) {
  return value.length >= 1
    && value.length <= 64
    && value.trim() === value
    && /^[A-Z0-9 ._-]+$/.test(value)
    && !hasControlOrBidi(value);
}

function hasControlOrBidi(value: string) {
  return /[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value);
}

function copyIdentity(value: unknown) {
  return typeof value === "string"
    && value.length > 0
    && value.trim() === value
    && !hasControlOrBidi(value)
    ? value
    : undefined;
}

function copyRisk(value: unknown): ActionRisk | undefined {
  return value === "routine" || value === "guarded" || value === "high" || value === "critical"
    ? value
    : undefined;
}

function copySafeStringArray(value: unknown, nonEmpty: boolean): readonly string[] | undefined {
  if (!Array.isArray(value) || (nonEmpty && value.length === 0)) return undefined;
  const copy: string[] = [];
  for (let index = 0; index < value.length; index += 1) {
    const item = copyIdentity(value[index]);
    if (!item || copy.includes(item)) return undefined;
    copy.push(item);
  }
  return Object.freeze(copy);
}

function isAdaptedError(value: unknown): value is AdaptedAPIError {
  if (!isObjectLike(value)) return false;
  const kind = Reflect.get(value, "kind");
  const messageKey = Reflect.get(value, "messageKey");
  return typeof kind === "string" && copyIdentity(messageKey) !== undefined;
}

function failedProtocolPresentation(): ConfirmationOutcomePresentation {
  return Object.freeze({ kind: "failed", error: protocolError });
}

function invalidPlan(reason: Extract<ResolvedConfirmationPlan, { kind: "invalid" }>["reason"]): ResolvedConfirmationPlan {
  return Object.freeze({ kind: "invalid", reason });
}

function invalidToken(reason: string): ResolvedTypedConfirmationToken {
  return Object.freeze({ kind: "invalid", reason });
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}
