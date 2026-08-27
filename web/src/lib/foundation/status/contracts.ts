import type { TranslationKey } from "@/lib/i18n";

export type StatusIcon = string;

export type KnownStatusTone =
  | "neutral"
  | "info"
  | "success"
  | "warning"
  | "critical";

type DomainStatusBase = {
  labelKey: TranslationKey;
  detailKey?: TranslationKey;
  icon: StatusIcon;
  operationalCautionKey?: TranslationKey;
  diagnosticCode?: string;
};

export type DomainStatusPresentation =
  | (DomainStatusBase & {
      known: true;
      tone: KnownStatusTone;
    })
  | (DomainStatusBase & {
      known: false;
      tone: "unknown";
    });
