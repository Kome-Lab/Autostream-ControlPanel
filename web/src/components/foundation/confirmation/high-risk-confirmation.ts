"use client";

import {
  createElement,
  useId,
  useState,
  type ComponentProps,
  type MouseEvent,
  type ReactNode,
} from "react";

import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";
import { Input } from "@/components/ui/input";
import {
  confirmationPublicTargetValues,
  resolveHighRiskConfirmationPlan,
  typedConfirmationMatches,
} from "@/lib/foundation/actions/confirmation-policy";
import { isActionInvocationAllowed } from "@/lib/foundation/permissions/evaluator";
import type {
  ActionDescriptor,
  ActionEvaluation,
} from "@/lib/foundation/actions/contracts";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { TranslationKey, TranslationValues } from "@/lib/i18n";

export type ConfirmationMessage = Readonly<{
  key: TranslationKey;
  values?: TranslationValues;
}>;

export type HighRiskConfirmationContext = Readonly<{
  currentState?: ConfirmationMessage;
  impact?: ConfirmationMessage;
  rollback?: ConfirmationMessage;
  credentialEffect?: ConfirmationMessage;
}>;

export type ConfirmationDialogState =
  | Readonly<{ kind: "ready" }>
  | Readonly<{ kind: "revalidating" }>
  | Readonly<{ kind: "submitting" }>
  | Readonly<{ kind: "stale-blocked" }>
  | Readonly<{ kind: "revalidation-unavailable" }>
  | Readonly<{ kind: "conflict"; error: AdaptedAPIError }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{
      kind: "outcome-unknown";
      nextAction:
        | "refresh-resource"
        | "inspect-audit"
        | "contact-operator";
    }>;

export type HighRiskConfirmationProps = Readonly<{
  descriptor: ActionDescriptor;
  open: boolean;
  evaluation: ActionEvaluation;
  state: ConfirmationDialogState;
  context?: HighRiskConfirmationContext;
  translate: (
    key: TranslationKey,
    values?: TranslationValues,
  ) => string;
  trigger: (
    props: Readonly<{
      disabled: boolean;
      "aria-disabled"?: true;
    }>,
  ) => ReactNode;
  onOpenIntent: () => void;
  onCloseIntent: () => void;
  onConfirmIntent: () => void;
}>;

export type HighRiskConfirmationBodyProps = Readonly<{
  descriptor: ActionDescriptor;
  state: ConfirmationDialogState;
  context?: HighRiskConfirmationContext;
  translate: (
    key: TranslationKey,
    values?: TranslationValues,
  ) => string;
  typedInput: string;
  onTypedInputChange: (value: string) => void;
}>;

type SafePresentation = Readonly<{
  labelKey: TranslationKey;
  consequenceKey: TranslationKey;
  auditLabelKey: TranslationKey;
}>;

export function HighRiskConfirmation({
  descriptor,
  open,
  evaluation,
  state,
  context,
  translate,
  trigger,
  onOpenIntent,
  onCloseIntent,
  onConfirmIntent,
}: HighRiskConfirmationProps): ReactNode {
  const plan = resolveHighRiskConfirmationPlan(descriptor);
  const presentation = copySafePresentation(descriptor);
  const invocationAllowed = isActionInvocationAllowed(evaluation);
  const unavailable = plan.kind === "invalid" || !presentation || !invocationAllowed;
  const [typedInput, setTypedInput] = useState("");
  const requiredToken = plan.kind === "typed-target" ? plan.token : undefined;
  const typedMatch = requiredToken === undefined
    || typedConfirmationMatches(typedInput, requiredToken);
  const confirmEnabled = !unavailable && state.kind === "ready" && typedMatch;
  const submitting = state.kind === "submitting";
  const busy = state.kind === "revalidating" || submitting;

  const triggerProps: Readonly<{ disabled: boolean; "aria-disabled"?: true }> = unavailable
    ? { disabled: true, "aria-disabled": true }
    : { disabled: false };
  const renderedTrigger = trigger(triggerProps);
  if (!presentation) {
    return createElement(
      ConfirmationDialogFrame,
      {
        open: false,
        trigger: renderedTrigger,
        title: "",
        description: "",
        cancelLabel: "",
        actionLabel: "",
        onConfirm: () => {},
        actionDisabled: true,
      },
    );
  }

  return createElement(
    ConfirmationDialogFrame,
    {
      open: open && !unavailable,
      onOpenChange: (nextOpen) => {
        if (nextOpen) {
          setTypedInput("");
          if (!unavailable) onOpenIntent();
          return;
        }
        if (!submitting) {
          setTypedInput("");
          onCloseIntent();
        }
      },
      trigger: renderedTrigger,
      title: translate(presentation.labelKey),
      description: translate(presentation.consequenceKey),
      cancelLabel: translate("cancel"),
      actionLabel: translate("execute"),
      actionDisabled: !confirmEnabled,
      cancelDisabled: submitting,
      showAction: state.kind !== "outcome-unknown",
      busy,
      status: statusForState(state, translate),
      actionResetKey: state.kind,
      onConfirm: (event: MouseEvent<HTMLButtonElement>) => {
        if (!confirmEnabled) return;
        if (event.currentTarget.dataset.confirmationIntentLatched === "true") return;
        event.currentTarget.dataset.confirmationIntentLatched = "true";
        event.currentTarget.disabled = true;
        onConfirmIntent();
      },
    },
    createElement(HighRiskConfirmationBody, {
      descriptor,
      state,
      context,
      translate,
      typedInput,
      onTypedInputChange: setTypedInput,
    }),
  );
}

export function HighRiskConfirmationBody({
  descriptor,
  context,
  translate,
  typedInput,
  onTypedInputChange,
}: HighRiskConfirmationBodyProps): ReactNode {
  const plan = resolveHighRiskConfirmationPlan(descriptor);
  const presentation = copySafePresentation(descriptor);
  const inputId = useId();
  const mismatchId = useId();
  if (plan.kind === "invalid" || !presentation) return null;

  const targetValues = confirmationPublicTargetValues(descriptor);
  const sections: ReactNode[] = [];
  if (targetValues.length > 0) {
    sections.push(createSection(
      "target",
      translate("resource"),
      createElement(
        "ul",
        null,
        ...targetValues.map((value) => createElement("li", { key: value }, value)),
      ),
    ));
  }
  if (context?.currentState) {
    sections.push(createSection(
      "current-state",
      translate("status"),
      createElement("p", null, translateMessage(context.currentState, translate)),
    ));
  }
  if (context?.impact) {
    sections.push(createSection(
      "impact",
      translate("confirmationImpactHeading"),
      createElement("p", null, translateMessage(context.impact, translate)),
    ));
  }
  if (context?.rollback) {
    sections.push(createSection(
      "rollback",
      translate("confirmationRollbackHeading"),
      createElement("p", null, translateMessage(context.rollback, translate)),
    ));
  }
  if (context?.credentialEffect) {
    sections.push(createSection(
      "credential-effect",
      translate("confirmationCredentialEffectHeading"),
      createElement("p", null, translateMessage(context.credentialEffect, translate)),
    ));
  }
  sections.push(createSection(
    "audit",
    translate("confirmationAuditHeading"),
    createElement("p", null, translate(presentation.auditLabelKey)),
  ));

  if (plan.kind === "typed-target") {
    const mismatch = typedInput.length > 0
      && !typedConfirmationMatches(typedInput, plan.token);
    const inputProps: ComponentProps<typeof Input> &
      Readonly<{ "data-confirmation-token-input": string }> = {
      id: inputId,
      value: typedInput,
      autoFocus: true,
      autoComplete: "off",
      spellCheck: false,
      "data-confirmation-token-input": "",
      ...(mismatch
        ? { "aria-invalid": true, "aria-describedby": mismatchId }
        : {}),
      onChange: (event) => onTypedInputChange(event.currentTarget.value),
    };
    sections.push(createElement(
      "div",
      { key: "typed-target", "data-confirmation-typed-target": "" },
      createElement(
        "p",
        null,
        translate("confirmationTypeTokenInstruction", { token: plan.token }),
      ),
      createElement("label", { htmlFor: inputId }, translate("confirmationTokenInputLabel")),
      createElement(Input, inputProps),
      mismatch
        ? createElement(
            "p",
            { id: mismatchId, role: "alert" },
            translate("confirmationTypedTokenMismatch"),
          )
        : null,
    ));
  }

  return createElement("div", { "data-confirmation-body": "" }, ...sections);
}

function createSection(key: string, heading: string, content: ReactNode) {
  return createElement(
    "section",
    { key, "data-confirmation-section": key },
    createElement("h3", null, heading),
    content,
  );
}

function translateMessage(
  message: ConfirmationMessage,
  translate: HighRiskConfirmationProps["translate"],
) {
  return translate(message.key, message.values);
}

function copySafePresentation(descriptor: ActionDescriptor): SafePresentation | undefined {
  try {
    const labelKey: TranslationKey = descriptor.labelKey;
    const confirmation = descriptor.confirmation;
    const auditLabelKey: TranslationKey = descriptor.audit.labelKey;
    if (
      typeof labelKey !== "string"
      || typeof auditLabelKey !== "string"
      || confirmation.mode === "none"
      || typeof confirmation.consequenceKey !== "string"
    ) {
      return undefined;
    }
    const consequenceKey: TranslationKey = confirmation.consequenceKey;
    return Object.freeze({ labelKey, consequenceKey, auditLabelKey });
  } catch {
    return undefined;
  }
}

function statusForState(
  state: ConfirmationDialogState,
  translate: HighRiskConfirmationProps["translate"],
) {
  if (state.kind === "ready") return undefined;
  if (state.kind === "revalidating") {
    return Object.freeze({ role: "status" as const, content: translate("confirmationRevalidating") });
  }
  if (state.kind === "submitting") {
    return Object.freeze({ role: "status" as const, content: translate("confirmationSubmitting") });
  }
  if (state.kind === "stale-blocked") {
    return Object.freeze({ role: "alert" as const, content: translate("confirmationStaleBlocked") });
  }
  if (state.kind === "revalidation-unavailable") {
    return Object.freeze({ role: "alert" as const, content: translate("confirmationRevalidationUnavailable") });
  }
  if (state.kind === "outcome-unknown") {
    return Object.freeze({ role: "alert" as const, content: translate("confirmationOutcomeUnknown") });
  }
  if (state.kind === "conflict") {
    return Object.freeze({
      role: "alert" as const,
      content: createElement(
        "span",
        null,
        translate(state.error.messageKey),
        " ",
        translate("confirmationRefreshRequired"),
      ),
    });
  }
  return Object.freeze({ role: "alert" as const, content: translate(state.error.messageKey) });
}
