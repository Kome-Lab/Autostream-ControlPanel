import type { TranslationKey } from "@/lib/i18n";

export type AdaptedAPIErrorKind =
  | "unauthenticated"
  | "forbidden"
  | "validation"
  | "not_found"
  | "conflict"
  | "protected_state"
  | "rate_limited"
  | "timeout"
  | "unavailable"
  | "network"
  | "protocol"
  | "unknown";

export type SafeFieldError = {
  field: string;
  messageKey: TranslationKey;
};

export type AdaptedAPIError = {
  kind: AdaptedAPIErrorKind;
  messageKey: TranslationKey;
  fieldErrors?: readonly SafeFieldError[];
  retryAfterSeconds?: number;
  diagnosticCode?: string;
};
