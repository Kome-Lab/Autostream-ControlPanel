import type { ActionDescriptor } from "@/lib/foundation/actions/contracts";
import type { WorkerNode } from "@/types/domain";

export type WorkerRestartDescriptor = Omit<
  ActionDescriptor,
  "id" | "risk" | "permissions" | "confirmation" | "duplicate" | "retry" | "revalidation"
> & Readonly<{
  id: "WKR-01";
  risk: "high";
  permissions: Readonly<{
    kind: "all";
    permissions: readonly ["workers.restart"];
  }>;
  confirmation: Readonly<{
    mode: "consequence";
    consequenceKey: "workerRestartConsequence";
    requireSubmitRevalidation: true;
  }>;
  duplicate: Readonly<{
    scope: "resource-action";
    whilePending: "block";
  }>;
  retry: Readonly<{ kind: "never" }>;
  revalidation: Readonly<{
    kind: "safe-fingerprint";
    fieldIds: readonly ["canonicalWorkerId", "serviceType"];
  }>;
}>;

export function buildWorkerRestartDescriptor(worker: WorkerNode): WorkerRestartDescriptor {
  const resourceId = canonicalWorkerID(worker);
  const publicLabel = safeWorkerLabel(worker);
  return Object.freeze({
    id: "WKR-01",
    labelKey: "workerRestartAction",
    risk: "high",
    target: Object.freeze({
      resourceType: "worker",
      resourceId,
      publicLabel,
      publicStableId: resourceId,
    }),
    permissions: Object.freeze({
      kind: "all",
      permissions: Object.freeze(["workers.restart"] as const),
    }),
    applicability: Object.freeze({
      ruleIds: Object.freeze(["worker-restart-target"]),
      requiredSections: Object.freeze(["worker"]),
    }),
    confirmation: Object.freeze({
      mode: "consequence",
      consequenceKey: "workerRestartConsequence",
      requireSubmitRevalidation: true,
    }),
    duplicate: Object.freeze({
      scope: "resource-action",
      whilePending: "block",
    }),
    retry: Object.freeze({ kind: "never" }),
    audit: Object.freeze({
      action: "workers.restart",
      labelKey: "workerRestartAudit",
      safeReferenceFieldIds: Object.freeze(["resourceId"]),
    }),
    stateIndependent: false,
    revalidation: Object.freeze({
      kind: "safe-fingerprint",
      fieldIds: Object.freeze(["canonicalWorkerId", "serviceType"] as const),
    }),
  });
}

export function workerRestartDuplicateKey(worker: WorkerNode): string {
  return `worker:${canonicalWorkerID(worker)}:restart`;
}

export function workerRestartFingerprint(worker: WorkerNode): string {
  return JSON.stringify([canonicalWorkerID(worker), worker.service_type]);
}

export function canonicalWorkerID(worker: WorkerNode): string {
  if (typeof worker.service_id === "string" && worker.service_id.length > 0) return worker.service_id;
  return typeof worker.id === "string" ? worker.id : "";
}

function safeWorkerLabel(worker: WorkerNode): string {
  return typeof worker.service_name === "string" && worker.service_name.length > 0
    ? worker.service_name
    : "Worker";
}
