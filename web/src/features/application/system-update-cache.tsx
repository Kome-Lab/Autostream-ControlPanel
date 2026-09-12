"use client";

import { useQueryClient } from "@tanstack/react-query";
import type { SystemUpdateJob, SystemUpdatesResponse } from "@/types/domain";

export function newIdempotencyKey(targetID: string) { const random = typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : Math.random().toString(36).slice(2); const safeTargetID = targetID.replace(/[^a-zA-Z0-9_-]/g, "-").slice(0, 48); return `web-${safeTargetID}-${random}`; }

export function mergeSystemUpdateJob(current: SystemUpdatesResponse | undefined, job: SystemUpdateJob, queryClient: ReturnType<typeof useQueryClient>) {
  if (!current) return;
  queryClient.setQueryData<SystemUpdatesResponse>(["system-updates"], { ...current, jobs: [job, ...current.jobs.filter((item) => item.id !== job.id)] });
}
