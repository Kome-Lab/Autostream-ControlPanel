import type { ReactNode } from "react";

import type { ActionDescriptor } from "../src/lib/foundation/actions/contracts";
import {
  confirmationRiskDefault,
  type ResolvedConfirmationPlan,
} from "../src/lib/foundation/actions/confirmation-policy";
import type {
  ConfirmationAuthorityEvidence,
  ConfirmationAuthoritySnapshot,
} from "../src/lib/foundation/actions/confirmation-revalidation";
import type {
  ConfirmationDialogState,
  HighRiskConfirmationContext,
  HighRiskConfirmationProps,
} from "../src/components/foundation/confirmation/high-risk-confirmation";

const descriptor = {} as ActionDescriptor;
const trigger: HighRiskConfirmationProps["trigger"] = (props) => {
  void props;
  return null as ReactNode;
};
const context: HighRiskConfirmationContext = {
  impact: { key: "dangerousNotice" },
};
const revisionEvidence: ConfirmationAuthorityEvidence = { kind: "revision", value: 1 };
const fingerprintEvidence: ConfirmationAuthorityEvidence = {
  kind: "safe-fingerprint",
  fieldIds: ["status"],
  value: "opaque",
};
const readyState: ConfirmationDialogState = { kind: "ready" };
void descriptor;
void trigger;
void context;
void revisionEvidence;
void fingerprintEvidence;
void readyState;

// @ts-expect-error -- risk defaults accept only the canonical four risks
export const invalidRiskDefault = confirmationRiskDefault("severe");

// @ts-expect-error -- invalid plan reasons are a closed fail-closed union
export const invalidPlanReason: ResolvedConfirmationPlan = { kind: "invalid", reason: "unsafe" };

// @ts-expect-error -- a resolved typed-target plan (including critical) requires a literal token
export const criticalPlanWithoutToken: ResolvedConfirmationPlan = {
  kind: "typed-target",
  requireSubmitRevalidation: true,
  tokenSource: "target-label",
};

// @ts-expect-error -- revision evidence requires an exact revision value
export const revisionEvidenceWithoutValue: ConfirmationAuthorityEvidence = { kind: "revision" };

export const emptyFingerprintFields: ConfirmationAuthorityEvidence = {
  kind: "safe-fingerprint",
  // @ts-expect-error -- a safe fingerprint must declare a non-empty field tuple
  fieldIds: [],
  value: "opaque",
};

// @ts-expect-error -- an authority snapshot always includes the current evaluation
export const snapshotWithoutEvaluation: ConfirmationAuthoritySnapshot = {
  actionId: "WKR-01",
  targetResourceType: "worker",
  targetResourceId: "worker-a",
  freshness: { kind: "fresh" },
  evidence: { kind: "revision", value: 1 },
};

// @ts-expect-error -- dialog render states are closed and controller-owned
export const invalidDialogState: ConfirmationDialogState = { kind: "retrying" };

export const invalidNextAction: ConfirmationDialogState = {
  kind: "outcome-unknown",
  // @ts-expect-error -- ambiguous outcome next actions are closed
  nextAction: "resend",
};

// @ts-expect-error -- trigger render props cannot carry endpoint authority
export const endpointTrigger: HighRiskConfirmationProps["trigger"] = (props: {
  disabled: boolean;
  endpoint: string;
}) => {
  void props;
  return null as ReactNode;
};

// @ts-expect-error -- trigger render props cannot carry payload authority
export const payloadTrigger: HighRiskConfirmationProps["trigger"] = (props: {
  disabled: boolean;
  payload: object;
}) => {
  void props;
  return null as ReactNode;
};

// @ts-expect-error -- context messages require TranslationKey, not raw copy
export const rawContext: HighRiskConfirmationContext = { impact: { key: "Delete everything now" } };

// @ts-expect-error -- the renderer emits a zero-argument intent and never endpoint/payload arguments
export const endpointConfirm: HighRiskConfirmationProps["onConfirmIntent"] = (
  endpoint: string,
  payload: object,
) => {
  void endpoint;
  void payload;
};
