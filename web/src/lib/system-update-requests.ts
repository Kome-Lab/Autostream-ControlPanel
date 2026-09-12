import type { SystemUpdatePortReconfigureCreateRequest, SystemUpdateJob, SystemUpdateTarget } from "@/types/domain";
import { systemUpdateStrategyForTarget } from "./system-update-target-policy";
import { systemUpdateJobFromResponse } from "./system-updates";

export function systemUpdateRequest(target: Pick<SystemUpdateTarget, "target_id" | "busy" | "current_stream_id">, idempotencyKey: string) {
  return {
    target_id: target.target_id,
    strategy: systemUpdateStrategyForTarget(target),
    idempotency_key: idempotencyKey,
  };
}

export class SystemUpdateRequestAmbiguousError extends Error {
  request: SystemUpdatePortReconfigureCreateRequest;

  constructor(request: SystemUpdatePortReconfigureCreateRequest, cause?: unknown) {
    super("system_update_request_ambiguous", { cause });
    this.name = "SystemUpdateRequestAmbiguousError";
    this.request = request;
  }
}

export async function requestSystemUpdateWithRecovery(
  target: SystemUpdateTarget,
  idempotencyKey: string,
  send: (request: ReturnType<typeof systemUpdateRequest>) => Promise<unknown>,
  refreshJobs: () => Promise<SystemUpdateJob[]>,
) {
  try {
    return systemUpdateJobFromResponse(await send(systemUpdateRequest(target, idempotencyKey)));
  } catch (originalError) {
    try {
      const jobs = await refreshJobs();
      const recovered = jobs.find((job) => job.idempotency_key === idempotencyKey);
      if (recovered) return recovered;
    } catch {
      // Preserve the original request failure; React Query may retry this same operation/key.
    }
    throw originalError;
  }
}

export async function runSystemUpdatesSequentially<T>(
  targets: SystemUpdateTarget[],
  run: (target: SystemUpdateTarget, index: number) => Promise<T>,
) {
  const results: T[] = [];
  for (let index = 0; index < targets.length; index += 1) {
    results.push(await run(targets[index], index));
  }
  return results;
}

export function ambiguousSystemUpdateCreateError(error: unknown) {
  if (!error || typeof error !== "object" || !("status" in error)) return true;
  const status = Number((error as { status?: unknown }).status);
  return !Number.isInteger(status) || status < 400 || status >= 500;
}
