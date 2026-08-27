import type { TranslationKey } from "@/lib/i18n";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { PermissionRequirement } from "@/lib/foundation/permissions/contracts";

export type ActionId = string;

export type ActionRisk =
  | "routine"
  | "guarded"
  | "high"
  | "critical";

export type ActionVisibility =
  | {
      kind: "visible";
      reason?: never;
    }
  | {
      kind: "hidden";
      reason: "not-applicable" | "security-sensitive";
    };

export type ActionAvailability =
  | {
      kind: "allowed";
      reasonKey?: never;
    }
  | {
      kind: "denied";
      reasonKey: TranslationKey;
    }
  | {
      kind: "blocked";
      reasonKey: TranslationKey;
    }
  | {
      kind: "unknown";
      reasonKey: TranslationKey;
    }
  | {
      kind: "pending";
      reasonKey: TranslationKey;
    };

export type ActionTarget = {
  resourceType: string;
  resourceId: string;
  publicLabel?: string;
  publicStableId?: string;
};

export type ApplicabilityDescriptor = {
  ruleIds: readonly string[];
  requiredSections: readonly string[];
};

export type DuplicateSubmissionPolicy = {
  scope:
    | "resource-action"
    | "resource-target"
    | "session-flow"
    | "fleet";
  whilePending: "block";
};

export type RetryPolicy =
  | {
      kind: "never";
      maxAttempts?: never;
    }
  | {
      kind: "manual-after-refresh";
      maxAttempts?: never;
    }
  | {
      kind: "server-idempotent";
      maxAttempts: number;
    }
  | {
      kind: "lookup-only";
      maxAttempts?: never;
    };

export type RevalidationPolicy =
  | {
      kind: "none";
      fieldIds?: never;
    }
  | {
      kind: "revision";
      fieldIds?: never;
    }
  | {
      kind: "safe-fingerprint";
      fieldIds: readonly string[];
    };

export type AuditPresentationDescriptor = {
  action: string;
  labelKey: TranslationKey;
  safeReferenceFieldIds: readonly string[];
};

export type ConfirmationMode =
  | "none"
  | "consequence"
  | "typed-target";

export type TypedTokenSource =
  | {
      kind: "target-label";
      value?: never;
    }
  | {
      kind: "public-stable-id";
      value?: never;
    }
  | {
      kind: "fixed-ascii";
      value: string;
    };

export type ConfirmationPolicy =
  | {
      mode: "none";
      consequenceKey?: never;
      typedToken?: never;
      requireSubmitRevalidation: boolean;
    }
  | {
      mode: "consequence";
      consequenceKey: TranslationKey;
      typedToken?: never;
      requireSubmitRevalidation: boolean;
    }
  | {
      mode: "typed-target";
      consequenceKey: TranslationKey;
      typedToken: TypedTokenSource;
      requireSubmitRevalidation: true;
    };

export type ActionDescriptor = {
  id: ActionId;
  labelKey: TranslationKey;
  risk: ActionRisk;
  target: ActionTarget;
  permissions?: PermissionRequirement;
  applicability: ApplicabilityDescriptor;
  confirmation: ConfirmationPolicy;
  duplicate: DuplicateSubmissionPolicy;
  retry: RetryPolicy;
  audit: AuditPresentationDescriptor;
  stateIndependent?: boolean;
  revalidation?: RevalidationPolicy;
};

export type ActionEvaluation = {
  visibility: ActionVisibility;
  availability: ActionAvailability;
};

export type MutationOutcome<T> =
  | {
      kind: "succeeded";
      value: T;
      error?: never;
    }
  | {
      kind: "failed";
      error: AdaptedAPIError;
      value?: never;
    }
  | {
      kind: "outcome_unknown";
      safeReference?: string;
      nextAction:
        | "refresh-resource"
        | "inspect-audit"
        | "contact-operator";
      value?: never;
      error?: never;
    };
