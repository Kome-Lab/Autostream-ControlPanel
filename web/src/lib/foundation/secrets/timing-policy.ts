import type {
  OneTimeSecretTimingInput,
  OneTimeSecretTimingPlan,
} from "@/lib/foundation/secrets/contracts";

export const ONE_TIME_SECRET_HARD_MAX_MS = 10 * 60 * 1000;
export const ONE_TIME_SECRET_WARNING_LEAD_MS = 60 * 1000;

export function planOneTimeSecretTiming<const BackendExpiry extends number = number>(
  input: OneTimeSecretTimingInput<BackendExpiry>,
): OneTimeSecretTimingPlan | undefined {
  let adoptedAtEpochMs: unknown;
  let adoptedAtMonotonicMs: unknown;
  let backendExpiresAtEpochMs: unknown;
  try {
    adoptedAtEpochMs = input.adoptedAtEpochMs;
    adoptedAtMonotonicMs = input.adoptedAtMonotonicMs;
    backendExpiresAtEpochMs = input.backendExpiresAtEpochMs;
  } catch {
    return undefined;
  }

  if (!isNonNegativeSafeInteger(adoptedAtEpochMs) || !isNonNegativeSafeInteger(adoptedAtMonotonicMs)) {
    return undefined;
  }
  if (backendExpiresAtEpochMs !== undefined && !isNonNegativeSafeInteger(backendExpiresAtEpochMs)) {
    return undefined;
  }

  let effectiveLifetimeMs = ONE_TIME_SECRET_HARD_MAX_MS;
  if (backendExpiresAtEpochMs !== undefined) {
    if (backendExpiresAtEpochMs <= adoptedAtEpochMs) return undefined;
    const backendLifetimeMs = backendExpiresAtEpochMs - adoptedAtEpochMs;
    if (!Number.isSafeInteger(backendLifetimeMs) || backendLifetimeMs <= 0) return undefined;
    effectiveLifetimeMs = Math.min(backendLifetimeMs, ONE_TIME_SECRET_HARD_MAX_MS);
  }

  const expiresAtMonotonicMs = adoptedAtMonotonicMs + effectiveLifetimeMs;
  if (!Number.isSafeInteger(expiresAtMonotonicMs)) return undefined;
  const warningAtMonotonicMs = Math.max(
    adoptedAtMonotonicMs,
    expiresAtMonotonicMs - ONE_TIME_SECRET_WARNING_LEAD_MS,
  );
  if (!Number.isSafeInteger(warningAtMonotonicMs)) return undefined;

  return Object.freeze({
    effectiveLifetimeMs,
    warningAtMonotonicMs,
    expiresAtMonotonicMs,
  });
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}
