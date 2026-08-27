import type {
  AdaptedAPIError,
  AdaptedAPIErrorKind,
} from "@/lib/foundation/api-errors/contracts";
import {
  defaultAPIErrorRegistry,
  filterAPIErrorFieldErrors,
  isAPIErrorRegistry,
  lookupAPIErrorRegistry,
} from "@/lib/foundation/api-errors/registry";
import type { APIErrorRegistry, APIErrorRegistryMatch } from "@/lib/foundation/api-errors/registry";
import type { TranslationKey } from "@/lib/i18n";

export type AdaptAPIErrorOptions = Readonly<{
  registry?: APIErrorRegistry;
  timeout?: boolean;
  retryAfterSeconds?: unknown;
  fieldErrors?: unknown;
}>;

type APIErrorDetails = Readonly<{
  status: number;
  code?: string;
  detailCode?: string;
}>;

const unreadable = Symbol("unreadable");

export function adaptAPIError(
  error: unknown,
  options?: AdaptAPIErrorOptions,
): AdaptedAPIError {
  try {
    return adaptAPIErrorSafely(error, options);
  } catch {
    return finishAdaptedError("unknown", undefined, undefined, defaultAPIErrorRegistry, undefined);
  }
}

function adaptAPIErrorSafely(
  error: unknown,
  options: AdaptAPIErrorOptions | undefined,
): AdaptedAPIError {
  const registryCandidate = safeProperty(options, "registry");
  const registry = isAPIErrorRegistry(registryCandidate)
    ? registryCandidate
    : defaultAPIErrorRegistry;
  const explicitTimeout = safeProperty(options, "timeout") === true;
  const errorName = safeProperty(error, "name");
  if (errorName === unreadable) {
    return finishAdaptedError("unknown", undefined, options, registry, undefined);
  }
  if (errorName === "APIError") {
    const apiError = extractAPIError(error);
    return apiError
      ? adaptCurrentAPIError(apiError, options, registry, explicitTimeout)
      : finishAdaptedError("unknown", undefined, options, registry, undefined);
  }

  if (errorName === "TimeoutError") {
    return finishAdaptedError("timeout", undefined, options, registry, undefined);
  }
  if (errorName === "AbortError") {
    return finishAdaptedError(
      explicitTimeout ? "timeout" : "unknown",
      undefined,
      options,
      registry,
      undefined,
    );
  }
  if (isTypeError(error)) {
    return finishAdaptedError(
      explicitTimeout ? "timeout" : "network",
      undefined,
      options,
      registry,
      undefined,
    );
  }
  return finishAdaptedError("unknown", undefined, options, registry, undefined);
}

function adaptCurrentAPIError(
  error: APIErrorDetails,
  options: AdaptAPIErrorOptions | undefined,
  registry: APIErrorRegistry,
  explicitTimeout: boolean,
): AdaptedAPIError {
  if (error.status === 401 || error.status === 403) {
    const authorityKind: AdaptedAPIErrorKind = error.status === 401
      ? "unauthenticated"
      : "forbidden";
    const match = lookupAPIErrorRegistry(
      registry,
      error.status,
      error.code,
      error.detailCode,
    );
    return match?.kind === authorityKind
      ? finishRegistryMatch(match, options, registry)
      : finishAdaptedError(authorityKind, undefined, options, registry, undefined);
  }

  if (error.code === "non_json_response" || error.code === "invalid_json_response") {
    const match = lookupAPIErrorRegistry(
      defaultAPIErrorRegistry,
      error.status,
      error.code,
      undefined,
    );
    return finishAdaptedError(
      "protocol",
      match?.messageKey,
      options,
      registry,
      match?.diagnosticCode,
    );
  }

  const match = lookupAPIErrorRegistry(
    registry,
    error.status,
    error.code,
    error.detailCode,
  );
  if (match) return finishRegistryMatch(match, options, registry);

  if (error.status === 504 && explicitTimeout) {
    return finishAdaptedError("timeout", undefined, options, registry, undefined);
  }
  return finishAdaptedError(
    statusFallbackKind(error.status),
    undefined,
    options,
    registry,
    undefined,
  );
}

function finishRegistryMatch(
  match: APIErrorRegistryMatch,
  options: AdaptAPIErrorOptions | undefined,
  registry: APIErrorRegistry,
): AdaptedAPIError {
  return finishAdaptedError(
    match.kind,
    match.messageKey,
    options,
    registry,
    match.diagnosticCode,
  );
}

function finishAdaptedError(
  kind: AdaptedAPIErrorKind,
  messageKey: TranslationKey | undefined,
  options: AdaptAPIErrorOptions | undefined,
  registry: APIErrorRegistry,
  diagnosticCode: string | undefined,
): AdaptedAPIError {
  const result: AdaptedAPIError = {
    kind,
    messageKey: messageKey ?? defaultMessageKey(kind),
  };

  if (kind === "validation") {
    const fieldErrors = filterAPIErrorFieldErrors(
      registry,
      safeProperty(options, "fieldErrors"),
    );
    if (fieldErrors) result.fieldErrors = fieldErrors;
  }
  if (kind === "rate_limited") {
    const retryAfterSeconds = filterRetryAfterSeconds(
      safeProperty(options, "retryAfterSeconds"),
    );
    if (retryAfterSeconds !== undefined) result.retryAfterSeconds = retryAfterSeconds;
  }
  if (diagnosticCode !== undefined) result.diagnosticCode = diagnosticCode;
  return Object.freeze(result);
}

function extractAPIError(value: unknown): APIErrorDetails | undefined {
  if (!isObjectLike(value)) return undefined;
  const status = safeProperty(value, "status");
  const code = safeProperty(value, "code");
  const detailCode = safeProperty(value, "detailCode");
  const responseContentType = safeProperty(value, "responseContentType");

  if (!Number.isInteger(status) || (status as number) < 100 || (status as number) > 599) {
    return undefined;
  }
  if (!isOptionalString(code) || !isOptionalString(detailCode) || !isOptionalString(responseContentType)) {
    return undefined;
  }
  return {
    status: status as number,
    ...(typeof code === "string" ? { code } : {}),
    ...(typeof detailCode === "string" ? { detailCode } : {}),
  };
}

function statusFallbackKind(status: number): AdaptedAPIErrorKind {
  switch (status) {
    case 400:
    case 422:
      return "validation";
    case 401:
      return "unauthenticated";
    case 403:
      return "forbidden";
    case 404:
      return "not_found";
    case 408:
      return "timeout";
    case 409:
      return "conflict";
    case 429:
      return "rate_limited";
    default:
      return status >= 500 && status <= 599 ? "unavailable" : "unknown";
  }
}

function defaultMessageKey(kind: AdaptedAPIErrorKind): TranslationKey {
  switch (kind) {
    case "unauthenticated":
      return "apiErrorUnauthenticated";
    case "forbidden":
      return "apiErrorForbidden";
    case "validation":
      return "apiErrorValidation";
    case "not_found":
      return "apiErrorNotFound";
    case "conflict":
      return "apiErrorConflict";
    case "protected_state":
      return "apiErrorProtectedState";
    case "rate_limited":
      return "apiErrorRateLimited";
    case "timeout":
      return "apiErrorTimeout";
    case "unavailable":
      return "apiErrorUnavailable";
    case "network":
      return "apiErrorNetwork";
    case "protocol":
      return "apiErrorProtocol";
    case "unknown":
      return "apiErrorUnknown";
  }
}

function filterRetryAfterSeconds(value: unknown): number | undefined {
  return typeof value === "number"
    && Number.isFinite(value)
    && Number.isInteger(value)
    && value >= 0
    && value <= 86_400
    ? value
    : undefined;
}

function safeProperty(value: unknown, key: string): unknown | typeof unreadable {
  if (!isObjectLike(value)) return undefined;
  try {
    return Reflect.get(value, key);
  } catch {
    return unreadable;
  }
}

function isOptionalString(value: unknown): value is string | undefined {
  return value === undefined || typeof value === "string";
}

function isTypeError(value: unknown): value is TypeError {
  try {
    return value instanceof TypeError;
  } catch {
    return false;
  }
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}
