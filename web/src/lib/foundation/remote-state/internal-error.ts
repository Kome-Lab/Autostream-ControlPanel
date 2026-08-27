import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { RemoteState } from "@/lib/foundation/remote-state/contracts";

export const safeProtocolError: AdaptedAPIError = Object.freeze({
  kind: "protocol",
  messageKey: "apiErrorProtocol",
});

export function protocolBlockingError<T>(): RemoteState<T> {
  return Object.freeze({
    kind: "blocking-error",
    error: safeProtocolError,
  });
}

export function copyCanonicalRemoteStateError(
  value: unknown,
): AdaptedAPIError | undefined {
  const entries = readPlainDataRecord(value);
  if (!entries) return undefined;

  let kind: unknown;
  let messageKey: unknown;
  let kindSeen = false;
  let messageKeySeen = false;
  for (const [key, entryValue] of entries) {
    switch (key) {
      case "kind":
        if (kindSeen) return undefined;
        kindSeen = true;
        kind = entryValue;
        break;
      case "messageKey":
        if (messageKeySeen) return undefined;
        messageKeySeen = true;
        messageKey = entryValue;
        break;
      case "fieldErrors":
      case "retryAfterSeconds":
      case "diagnosticCode":
        break;
      default:
        return undefined;
    }
  }
  if (!kindSeen || !messageKeySeen) return undefined;
  return copyCanonicalPair(kind, messageKey);
}

export function readPlainDataRecord(
  value: unknown,
): ReadonlyArray<readonly [string, unknown]> | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  try {
    if (Array.isArray(value)) return undefined;
    const prototype = Object.getPrototypeOf(value);
    if (prototype !== Object.prototype && prototype !== null) return undefined;

    const sourceKeys = Reflect.ownKeys(value);
    const descriptors = Object.getOwnPropertyDescriptors(value);
    const descriptorKeys = Reflect.ownKeys(descriptors);
    if (sourceKeys.length !== descriptorKeys.length) return undefined;

    const entries: Array<readonly [string, unknown]> = [];
    for (const key of sourceKeys) {
      if (typeof key !== "string" || !Object.hasOwn(descriptors, key)) return undefined;
      const descriptor = Reflect.get(descriptors, key);
      if (!descriptor
        || descriptor.enumerable !== true
        || !Object.hasOwn(descriptor, "value")) {
        return undefined;
      }
      entries.push(Object.freeze([key, descriptor.value] as const));
    }
    for (const key of descriptorKeys) {
      if (typeof key !== "string" || !sourceKeys.includes(key)) return undefined;
    }
    return Object.freeze(entries);
  } catch {
    return undefined;
  }
}

function copyCanonicalPair(kind: unknown, messageKey: unknown): AdaptedAPIError | undefined {
  switch (kind) {
    case "unauthenticated":
      return messageKey === "apiErrorUnauthenticated"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "forbidden":
      return messageKey === "apiErrorForbidden"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "validation":
      return messageKey === "apiErrorValidation"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "not_found":
      return messageKey === "apiErrorNotFound"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "conflict":
      return messageKey === "apiErrorConflict"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "protected_state":
      return messageKey === "apiErrorProtectedState"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "rate_limited":
      return messageKey === "apiErrorRateLimited"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "timeout":
      return messageKey === "apiErrorTimeout"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "unavailable":
      return messageKey === "apiErrorUnavailable"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "network":
      return messageKey === "apiErrorNetwork"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "protocol":
      return messageKey === "apiErrorProtocol"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    case "unknown":
      return messageKey === "apiErrorUnknown"
        ? Object.freeze({ kind, messageKey })
        : undefined;
    default:
      return undefined;
  }
}
