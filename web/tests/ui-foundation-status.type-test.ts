import type { DomainStatusPresentation } from "../src/lib/foundation/status/contracts.ts";
import type { DomainStatusBadgeProps } from "../src/components/foundation/status/domain-status-badge.ts";
import type { StatusMappingDefinition } from "../src/lib/foundation/status/presenter-core.ts";
import type { TranslationKey } from "../src/lib/i18n.ts";

const known = { known: true, tone: "success", labelKey: "statusNodeHealthy", icon: "heart-pulse" } as const satisfies DomainStatusPresentation;
const unknown = { known: false, tone: "unknown", labelKey: "statusUnknown", detailKey: "statusUnknownDetail", icon: "circle-help" } as const satisfies DomainStatusPresentation;
export const presentations = [known, unknown] as const;

// @ts-expect-error status presentation has no action authority
export const invalidSafeToAct: DomainStatusPresentation = { ...known, safeToAct: true };
// @ts-expect-error unknown presentation cannot use a positive tone
export const invalidUnknownTone: DomainStatusPresentation = { ...unknown, tone: "success" };
// @ts-expect-error known presentation cannot use unknown tone
export const invalidKnownTone: DomainStatusPresentation = { ...known, tone: "unknown" };
// @ts-expect-error a discriminator alone is not a canonical presentation
export const invalidMalformedPresentation: DomainStatusPresentation = { known: true };
// @ts-expect-error raw strings are not TranslationKey values
export const invalidRawLabel: DomainStatusPresentation = { ...known, labelKey: "raw-future-status" };

export const validBadgeProps = { presentation: known, translate: (key: TranslationKey) => key } satisfies DomainStatusBadgeProps;
// @ts-expect-error badge accepts no raw status prop
export const invalidRawBadge: DomainStatusBadgeProps = { ...validBadgeProps, rawStatus: "healthy" };
// @ts-expect-error badge accepts no action callback
export const invalidActionBadge: DomainStatusBadgeProps = { ...validBadgeProps, onClick: () => undefined };
// @ts-expect-error badge accepts no arbitrary color/class input
export const invalidClassBadge: DomainStatusBadgeProps = { ...validBadgeProps, className: "bg-green-500" };

export const immutableMapping = {
  healthy: Object.freeze({ tone: "success", labelKey: "statusNodeHealthy", icon: "heart-pulse" }),
} as const satisfies StatusMappingDefinition;
// @ts-expect-error mapping entries are readonly
immutableMapping.healthy.tone = "critical";
