import type { ActionEvaluation } from "@/lib/foundation/actions/contracts";
import type { PermissionRequirement } from "@/lib/foundation/permissions/contracts";
import type { TranslationKey } from "@/lib/i18n";

export type PermissionSnapshot =
  | {
      kind: "ready";
      permissions: readonly string[];
    }
  | {
      kind: "refreshing";
    }
  | {
      kind: "unavailable";
    };

export type PermissionDisclosure =
  | "visible-denied"
  | "hidden-security-sensitive";

export type ActionConstraint =
  | {
      kind: "ready";
    }
  | {
      kind: "unknown";
      reasonKey: TranslationKey;
    }
  | {
      kind: "blocked";
      reasonKey: TranslationKey;
    }
  | {
      kind: "not-applicable";
      reasonKey: TranslationKey;
    };

export type ActionPendingState =
  | {
      kind: "idle";
    }
  | {
      kind: "pending";
      reasonKey: TranslationKey;
    };

export type PermissionDecision =
  | {
      kind: "allowed";
    }
  | {
      kind: "denied";
    }
  | {
      kind: "unknown";
      reason:
        | "refreshing"
        | "unavailable"
        | "malformed-requirement"
        | "malformed-snapshot";
    };

export type EvaluateActionPermissionInput = {
  requirement: PermissionRequirement;
  snapshot: PermissionSnapshot;
  disclosure: PermissionDisclosure;
  constraint?: ActionConstraint;
  pending?: ActionPendingState;
  deniedReasonKey?: TranslationKey;
  unknownReasonKey?: TranslationKey;
};

type ValidatedRequirement =
  | {
      kind: "all" | "any";
      permissions: readonly string[];
    }
  | {
      kind: "none";
    };

type ValidatedSnapshot =
  | {
      kind: "ready";
      permissions: readonly string[];
    }
  | {
      kind: "refreshing";
    }
  | {
      kind: "unavailable";
    };

export function evaluatePermissionRequirement(
  requirement: PermissionRequirement,
  snapshot: PermissionSnapshot,
): PermissionDecision {
  const validatedRequirement = validateRequirement(requirement);
  if (!validatedRequirement) {
    return { kind: "unknown", reason: "malformed-requirement" };
  }

  const validatedSnapshot = validateSnapshot(snapshot);
  if (!validatedSnapshot) {
    return { kind: "unknown", reason: "malformed-snapshot" };
  }

  if (validatedRequirement.kind === "none") {
    return { kind: "allowed" };
  }
  if (validatedSnapshot.kind === "refreshing") {
    return { kind: "unknown", reason: "refreshing" };
  }
  if (validatedSnapshot.kind === "unavailable") {
    return { kind: "unknown", reason: "unavailable" };
  }

  if (validatedSnapshot.permissions.includes("*")) {
    return { kind: "allowed" };
  }
  const allowed = validatedRequirement.kind === "all"
    ? validatedRequirement.permissions.every((permission) => validatedSnapshot.permissions.includes(permission))
    : validatedRequirement.permissions.some((permission) => validatedSnapshot.permissions.includes(permission));
  return allowed ? { kind: "allowed" } : { kind: "denied" };
}

export function evaluateActionPermission(
  input: EvaluateActionPermissionInput,
): ActionEvaluation {
  try {
    const decision = evaluatePermissionRequirement(input.requirement, input.snapshot);
    const disclosure = input.disclosure;
    if (disclosure !== "visible-denied" && disclosure !== "hidden-security-sensitive") {
      return failClosedEvaluation();
    }

    if (decision.kind === "denied") {
      return {
        visibility: permissionVisibility(disclosure),
        availability: {
          kind: "denied",
          reasonKey: input.deniedReasonKey ?? "actionPermissionDenied",
        },
      };
    }
    if (decision.kind === "unknown") {
      return {
        visibility: permissionVisibility(disclosure),
        availability: {
          kind: "unknown",
          reasonKey: input.unknownReasonKey ?? "actionPermissionUnknown",
        },
      };
    }

    const constraint = input.constraint ?? { kind: "ready" };
    if (constraint.kind === "unknown") {
      return {
        visibility: { kind: "visible" },
        availability: { kind: "unknown", reasonKey: constraint.reasonKey },
      };
    }
    if (constraint.kind === "blocked") {
      return {
        visibility: { kind: "visible" },
        availability: { kind: "blocked", reasonKey: constraint.reasonKey },
      };
    }
    if (constraint.kind === "not-applicable") {
      return {
        visibility: { kind: "hidden", reason: "not-applicable" },
        availability: { kind: "blocked", reasonKey: constraint.reasonKey },
      };
    }
    if (constraint.kind !== "ready") {
      return failClosedEvaluation();
    }

    const pending = input.pending ?? { kind: "idle" };
    if (pending.kind === "pending") {
      return {
        visibility: { kind: "visible" },
        availability: { kind: "pending", reasonKey: pending.reasonKey },
      };
    }
    if (pending.kind !== "idle") {
      return failClosedEvaluation();
    }

    return {
      visibility: { kind: "visible" },
      availability: { kind: "allowed" },
    };
  } catch {
    return failClosedEvaluation();
  }
}

export function isActionInvocationAllowed(evaluation: ActionEvaluation): boolean {
  try {
    return evaluation.visibility.kind === "visible"
      && evaluation.availability.kind === "allowed";
  } catch {
    return false;
  }
}

function validateRequirement(value: unknown): ValidatedRequirement | undefined {
  if (!isObjectLike(value)) return undefined;
  try {
    const kind = Reflect.get(value, "kind");
    const permissions = Reflect.get(value, "permissions");
    if (kind === "none") {
      return permissions === undefined ? { kind: "none" } : undefined;
    }
    if (kind !== "all" && kind !== "any") return undefined;
    const validatedPermissions = copyStringArray(permissions);
    if (!validatedPermissions || validatedPermissions.length === 0) return undefined;
    return { kind, permissions: validatedPermissions };
  } catch {
    return undefined;
  }
}

function validateSnapshot(value: unknown): ValidatedSnapshot | undefined {
  if (!isObjectLike(value)) return undefined;
  try {
    const kind = Reflect.get(value, "kind");
    const permissions = Reflect.get(value, "permissions");
    if (kind === "refreshing" || kind === "unavailable") {
      return permissions === undefined ? { kind } : undefined;
    }
    if (kind !== "ready") return undefined;
    const validatedPermissions = copyStringArray(permissions);
    return validatedPermissions ? { kind, permissions: validatedPermissions } : undefined;
  } catch {
    return undefined;
  }
}

function copyStringArray(value: unknown): readonly string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const copy: string[] = [];
  for (let index = 0; index < value.length; index += 1) {
    const item = value[index];
    if (typeof item !== "string") return undefined;
    copy.push(item);
  }
  return copy;
}

function permissionVisibility(disclosure: PermissionDisclosure): ActionEvaluation["visibility"] {
  return disclosure === "hidden-security-sensitive"
    ? { kind: "hidden", reason: "security-sensitive" }
    : { kind: "visible" };
}

function failClosedEvaluation(): ActionEvaluation {
  return {
    visibility: { kind: "hidden", reason: "security-sensitive" },
    availability: { kind: "unknown", reasonKey: "actionPermissionUnknown" },
  };
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}
