import type { ActionEvaluation } from "../src/lib/foundation/actions/contracts.ts";
import type {
  ActionConstraint,
  ActionPendingState,
  EvaluateActionPermissionInput,
  PermissionDecision,
  PermissionDisclosure,
  PermissionSnapshot,
} from "../src/lib/foundation/permissions/evaluator.ts";
import type {
  ActionAvailabilityBoundaryProps,
  ActionControlRenderProps,
} from "../src/components/foundation/permissions/action-availability-boundary.ts";

export const validSnapshots = [
  { kind: "ready", permissions: ["streams.start"] },
  { kind: "refreshing" },
  { kind: "unavailable" },
] as const satisfies readonly PermissionSnapshot[];

export const validConstraints = [
  { kind: "ready" },
  { kind: "unknown", reasonKey: "actionPermissionUnknown" },
  { kind: "blocked", reasonKey: "actionStateBlocked" },
  { kind: "not-applicable", reasonKey: "actionStateBlocked" },
] as const satisfies readonly ActionConstraint[];

export const validPendingStates = [
  { kind: "idle" },
  { kind: "pending", reasonKey: "actionAlreadyPending" },
] as const satisfies readonly ActionPendingState[];

export const validDecisions = [
  { kind: "allowed" },
  { kind: "denied" },
  { kind: "unknown", reason: "refreshing" },
  { kind: "unknown", reason: "unavailable" },
  { kind: "unknown", reason: "malformed-requirement" },
  { kind: "unknown", reason: "malformed-snapshot" },
] as const satisfies readonly PermissionDecision[];

export const validEvaluation: ActionEvaluation = {
  visibility: { kind: "visible" },
  availability: { kind: "allowed" },
};

export const validInput: EvaluateActionPermissionInput = {
  requirement: { kind: "all", permissions: ["streams.start"] },
  snapshot: validSnapshots[0],
  disclosure: "visible-denied",
};

export const validBoundaryProps: ActionAvailabilityBoundaryProps = {
  evaluation: validEvaluation,
  translate: () => "translated",
  reasonPresentation: "inline",
  children: (props) => props.disabled ? "disabled" : "enabled",
};

// @ts-expect-error -- ready snapshots require a permission array
export const invalidReadySnapshot: PermissionSnapshot = { kind: "ready" };
// @ts-expect-error -- refreshing snapshots cannot carry stale permissions
export const invalidRefreshingPermissions: PermissionSnapshot = { kind: "refreshing", permissions: ["streams.start"] };
// @ts-expect-error -- disclosure policy is a closed union
export const invalidDisclosure: PermissionDisclosure = "visible-hidden";
// @ts-expect-error -- blocked constraints require a localized reason key
export const invalidBlockedConstraint: ActionConstraint = { kind: "blocked" };
// @ts-expect-error -- not-applicable constraints require a localized reason key
export const invalidNotApplicableConstraint: ActionConstraint = { kind: "not-applicable" };
// @ts-expect-error -- pending state requires a localized reason key
export const invalidPendingState: ActionPendingState = { kind: "pending" };
// @ts-expect-error -- unknown decisions require a bounded reason
export const invalidUnknownDecision: PermissionDecision = { kind: "unknown" };
// @ts-expect-error -- unknown decision reasons are closed
export const invalidDecisionReason: PermissionDecision = { kind: "unknown", reason: "network" };
// @ts-expect-error -- tooltip-only presentation is not an availability reason mode
export const invalidReasonPresentation: ActionAvailabilityBoundaryProps = { evaluation: validEvaluation, translate: () => "translated", reasonPresentation: "tooltip", children: () => null };
// @ts-expect-error -- boundary control props never expose raw permission names
export const invalidRawPermissionChildProps: ActionControlRenderProps = { disabled: true, permissionName: "streams.start" };

