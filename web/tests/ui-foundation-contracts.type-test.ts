import type { TranslationKey } from "../src/lib/i18n.ts";
import type {
  ActionAvailability,
  ActionDescriptor,
  ActionEvaluation,
  ActionId,
  ActionRisk,
  ActionTarget,
  ActionVisibility,
  ApplicabilityDescriptor,
  AuditPresentationDescriptor,
  ConfirmationPolicy,
  DuplicateSubmissionPolicy,
  MutationOutcome,
  RetryPolicy,
  RevalidationPolicy,
  TypedTokenSource,
} from "../src/lib/foundation/actions/contracts.ts";
import type {
  AdaptedAPIError,
  AdaptedAPIErrorKind,
  SafeFieldError,
} from "../src/lib/foundation/api-errors/contracts.ts";
import type {
  NonEmptyReadonlyArray,
  PermissionRequirement,
} from "../src/lib/foundation/permissions/contracts.ts";
import type { Freshness, RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import type {
  DomainStatusPresentation,
  KnownStatusTone,
  StatusIcon,
} from "../src/lib/foundation/status/contracts.ts";

export const labelKey: TranslationKey = "actions";
export const actionId: ActionId = "WKR-01";
export const statusIcon: StatusIcon = "worker";

export const validVisibility = [
  { kind: "visible" },
  { kind: "hidden", reason: "not-applicable" },
  { kind: "hidden", reason: "security-sensitive" },
] as const satisfies readonly ActionVisibility[];

export const validAvailability = [
  { kind: "allowed" },
  { kind: "denied", reasonKey: labelKey },
  { kind: "blocked", reasonKey: labelKey },
  { kind: "unknown", reasonKey: labelKey },
  { kind: "pending", reasonKey: labelKey },
] as const satisfies readonly ActionAvailability[];

export const nonEmptyPermissions: NonEmptyReadonlyArray<string> = ["workers.restart"];
export const validPermissionRequirements = [
  { kind: "all", permissions: nonEmptyPermissions },
  { kind: "any", permissions: ["workers.restart", "system_updates.execute"] },
  { kind: "none" },
] as const satisfies readonly PermissionRequirement[];

export const validTypedTokenSources = [
  { kind: "target-label" },
  { kind: "public-stable-id" },
  { kind: "fixed-ascii", value: "CONFIRM" },
] as const satisfies readonly TypedTokenSource[];

export const validConfirmationPolicies = [
  { mode: "none", requireSubmitRevalidation: false },
  { mode: "consequence", consequenceKey: labelKey, requireSubmitRevalidation: true },
  {
    mode: "typed-target",
    consequenceKey: labelKey,
    typedToken: validTypedTokenSources[0],
    requireSubmitRevalidation: true,
  },
] as const satisfies readonly ConfirmationPolicy[];

export const validRetryPolicies = [
  { kind: "never" },
  { kind: "manual-after-refresh" },
  { kind: "server-idempotent", maxAttempts: 2 },
  { kind: "lookup-only" },
] as const satisfies readonly RetryPolicy[];

export const validRevalidationPolicies = [
  { kind: "none" },
  { kind: "revision" },
  { kind: "safe-fingerprint", fieldIds: ["status", "revision"] },
] as const satisfies readonly RevalidationPolicy[];

export const actionTarget: ActionTarget = {
  resourceType: "worker",
  resourceId: "worker-public-id",
  publicLabel: "Worker A",
  publicStableId: "worker-public-id",
};
export const applicability: ApplicabilityDescriptor = {
  ruleIds: ["worker-restartable"],
  requiredSections: ["worker", "health"],
};
export const duplicate: DuplicateSubmissionPolicy = {
  scope: "resource-action",
  whilePending: "block",
};
export const audit: AuditPresentationDescriptor = {
  action: "workers.restart",
  labelKey,
  safeReferenceFieldIds: ["resourceId"],
};
export const validActionDescriptor: ActionDescriptor = {
  id: actionId,
  labelKey,
  risk: "high",
  target: actionTarget,
  permissions: validPermissionRequirements[0],
  applicability,
  confirmation: validConfirmationPolicies[1],
  duplicate,
  retry: validRetryPolicies[0],
  audit,
  stateIndependent: false,
  revalidation: validRevalidationPolicies[1],
};
export const validActionEvaluation: ActionEvaluation = {
  visibility: validVisibility[0],
  availability: validAvailability[0],
};
export const validRisks: readonly ActionRisk[] = ["routine", "guarded", "high", "critical"];

export const fieldError: SafeFieldError = { field: "name", messageKey: labelKey };
export const adaptedError: AdaptedAPIError = {
  kind: "validation",
  messageKey: labelKey,
  fieldErrors: [fieldError],
  retryAfterSeconds: 10,
  diagnosticCode: "validation_failed",
};
export const validErrorKinds: readonly AdaptedAPIErrorKind[] = [
  "unauthenticated",
  "forbidden",
  "validation",
  "not_found",
  "conflict",
  "protected_state",
  "rate_limited",
  "timeout",
  "unavailable",
  "network",
  "protocol",
  "unknown",
];

export const validMutationOutcomes = [
  { kind: "succeeded", value: "ok" },
  { kind: "failed", error: adaptedError },
  { kind: "outcome_unknown", safeReference: "public-ref", nextAction: "inspect-audit" },
] as const satisfies readonly MutationOutcome<string>[];

export const validFreshness = [
  { kind: "fresh", lastSuccessAt: 1 },
  { kind: "refreshing", lastSuccessAt: 1 },
  { kind: "stale", lastSuccessAt: 1, error: adaptedError },
] as const satisfies readonly Freshness[];

export const validRemoteStates = [
  { kind: "initial-loading" },
  { kind: "empty", freshness: validFreshness[2] },
  { kind: "ready", data: ["worker"], freshness: validFreshness[2] },
  {
    kind: "partial",
    data: ["worker"],
    missingSections: ["health"],
    sectionErrors: { health: adaptedError },
    freshness: validFreshness[2],
  },
  { kind: "blocking-error", error: adaptedError },
] as const satisfies readonly RemoteState<readonly string[]>[];

export const validStatusPresentations = [
  { known: true, tone: "success", labelKey, icon: statusIcon },
  { known: false, tone: "unknown", labelKey, icon: statusIcon },
] as const satisfies readonly DomainStatusPresentation[];

// @ts-expect-error -- hidden visibility requires a reason
export const invalidHiddenWithoutReason: ActionVisibility = { kind: "hidden" };
// @ts-expect-error -- visible visibility cannot carry a hidden reason
export const invalidVisibleWithReason: ActionVisibility = { kind: "visible", reason: "not-applicable" };
// @ts-expect-error -- hidden reasons are closed
export const invalidHiddenReason: ActionVisibility = { kind: "hidden", reason: "unknown" };

// @ts-expect-error -- allowed availability cannot carry a reason key
export const invalidAllowedReason: ActionAvailability = { kind: "allowed", reasonKey: labelKey };
// @ts-expect-error -- denied availability requires a reason key
export const invalidDeniedWithoutReason: ActionAvailability = { kind: "denied" };
// @ts-expect-error -- blocked availability requires a reason key
export const invalidBlockedWithoutReason: ActionAvailability = { kind: "blocked" };
// @ts-expect-error -- unknown availability requires a reason key
export const invalidUnknownWithoutReason: ActionAvailability = { kind: "unknown" };
// @ts-expect-error -- pending availability requires a reason key
export const invalidPendingWithoutReason: ActionAvailability = { kind: "pending" };

// @ts-expect-error -- all permission requirements must be non-empty
export const invalidAllEmpty: PermissionRequirement = { kind: "all", permissions: [] };
// @ts-expect-error -- any permission requirements must be non-empty
export const invalidAnyEmpty: PermissionRequirement = { kind: "any", permissions: [] };
// @ts-expect-error -- none permission requirements cannot carry permissions
export const invalidNonePermissions: PermissionRequirement = { kind: "none", permissions: ["workers.restart"] };
// @ts-expect-error -- permission requirement kinds are closed
export const invalidPermissionKind: PermissionRequirement = { kind: "some", permissions: ["workers.restart"] };

// @ts-expect-error -- none confirmation cannot carry a consequence key
export const invalidNoneConsequence: ConfirmationPolicy = { mode: "none", consequenceKey: labelKey, requireSubmitRevalidation: false };
// @ts-expect-error -- none confirmation cannot carry a typed token
export const invalidNoneToken: ConfirmationPolicy = { mode: "none", typedToken: { kind: "target-label" }, requireSubmitRevalidation: false };
// @ts-expect-error -- consequence confirmation requires a consequence key
export const invalidConsequenceMissingKey: ConfirmationPolicy = { mode: "consequence", requireSubmitRevalidation: true };
// @ts-expect-error -- consequence confirmation cannot carry a typed token
export const invalidConsequenceToken: ConfirmationPolicy = { mode: "consequence", consequenceKey: labelKey, typedToken: { kind: "target-label" }, requireSubmitRevalidation: true };
// @ts-expect-error -- typed-target confirmation requires a consequence key
export const invalidTypedMissingKey: ConfirmationPolicy = { mode: "typed-target", typedToken: { kind: "target-label" }, requireSubmitRevalidation: true };
// @ts-expect-error -- typed-target confirmation requires a typed token
export const invalidTypedMissingToken: ConfirmationPolicy = { mode: "typed-target", consequenceKey: labelKey, requireSubmitRevalidation: true };
// @ts-expect-error -- typed-target confirmation always revalidates on submit
export const invalidTypedNoRevalidation: ConfirmationPolicy = { mode: "typed-target", consequenceKey: labelKey, typedToken: { kind: "target-label" }, requireSubmitRevalidation: false };

// @ts-expect-error -- server-idempotent retry requires a maximum attempt count
export const invalidIdempotentMissingAttempts: RetryPolicy = { kind: "server-idempotent" };
// @ts-expect-error -- lookup-only retry cannot carry a maximum attempt count
export const invalidLookupAttempts: RetryPolicy = { kind: "lookup-only", maxAttempts: 2 };
// @ts-expect-error -- retry kinds are closed
export const invalidRetryKind: RetryPolicy = { kind: "automatic" };

// @ts-expect-error -- succeeded outcomes cannot carry an error
export const invalidSucceededError: MutationOutcome<string> = { kind: "succeeded", value: "ok", error: adaptedError };
// @ts-expect-error -- failed outcomes cannot carry a value
export const invalidFailedValue: MutationOutcome<string> = { kind: "failed", error: adaptedError, value: "ok" };
// @ts-expect-error -- unknown outcomes cannot carry a value
export const invalidUnknownValue: MutationOutcome<string> = { kind: "outcome_unknown", value: "ok", nextAction: "refresh-resource" };
// @ts-expect-error -- unknown outcomes cannot carry an error
export const invalidUnknownError: MutationOutcome<string> = { kind: "outcome_unknown", error: adaptedError, nextAction: "refresh-resource" };
// @ts-expect-error -- unknown outcome next actions are closed
export const invalidUnknownNextAction: MutationOutcome<string> = { kind: "outcome_unknown", nextAction: "retry" };

// @ts-expect-error -- initial loading cannot carry data
export const invalidInitialData: RemoteState<string> = { kind: "initial-loading", data: "worker" };
// @ts-expect-error -- empty state requires freshness
export const invalidEmptyFreshness: RemoteState<string> = { kind: "empty" };
// @ts-expect-error -- ready state requires data
export const invalidReadyData: RemoteState<string> = { kind: "ready", freshness: validFreshness[0] };
// @ts-expect-error -- ready state requires freshness
export const invalidReadyFreshness: RemoteState<string> = { kind: "ready", data: "worker" };
// @ts-expect-error -- partial state requires missing sections
export const invalidPartialMissingSections: RemoteState<string> = { kind: "partial", data: "worker", sectionErrors: {}, freshness: validFreshness[0] };
// @ts-expect-error -- partial state requires section errors
export const invalidPartialSectionErrors: RemoteState<string> = { kind: "partial", data: "worker", missingSections: [], freshness: validFreshness[0] };
// @ts-expect-error -- partial state requires freshness
export const invalidPartialFreshness: RemoteState<string> = { kind: "partial", data: "worker", missingSections: [], sectionErrors: {} };
// @ts-expect-error -- blocking errors cannot carry data
export const invalidBlockingData: RemoteState<string> = { kind: "blocking-error", error: adaptedError, data: "worker" };

// @ts-expect-error -- known statuses cannot use the unknown tone
export const invalidKnownUnknownTone: DomainStatusPresentation = { known: true, tone: "unknown", labelKey, icon: statusIcon };
// @ts-expect-error -- unknown statuses cannot use a positive tone
export const invalidUnknownSuccessTone: DomainStatusPresentation = { known: false, tone: "success", labelKey, icon: statusIcon };
// @ts-expect-error -- every status presentation requires an icon
export const invalidMissingIcon: DomainStatusPresentation = { known: true, tone: "neutral", labelKey };
// @ts-expect-error -- status presentation cannot decide action safety
export const invalidSafeToAct: DomainStatusPresentation = { known: true, tone: "success", labelKey, icon: statusIcon, safeToAct: true };
// @ts-expect-error -- unknown is not part of the known tone set
export const invalidKnownToneMember: KnownStatusTone = "unknown";

// @ts-expect-error -- API error kinds are closed
export const invalidErrorKind: AdaptedAPIError = { kind: "server", messageKey: labelKey };
// @ts-expect-error -- raw messages are forbidden
export const invalidRawMessage: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, rawMessage: "secret" };
// @ts-expect-error -- transport messages are forbidden
export const invalidMessage: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, message: "secret" };
// @ts-expect-error -- raw bodies are forbidden
export const invalidRawBody: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, body: { secret: true } };
// @ts-expect-error -- request URLs are forbidden
export const invalidURL: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, url: "https://example.invalid" };
// @ts-expect-error -- stacks are forbidden
export const invalidStack: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, stack: "stack" };
// @ts-expect-error -- headers are forbidden
export const invalidHeaders: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, headers: {} };
// @ts-expect-error -- tokens are forbidden
export const invalidToken: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, token: "secret" };
// @ts-expect-error -- ciphertext is forbidden
export const invalidCiphertext: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, ciphertext: "secret" };
// @ts-expect-error -- nonces are forbidden
export const invalidNonce: AdaptedAPIError = { kind: "unknown", messageKey: labelKey, nonce: "secret" };
