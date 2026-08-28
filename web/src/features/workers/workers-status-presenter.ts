import type { DomainStatusPresentation } from "@/lib/foundation/status/contracts";
import {
  presentNodeConnectivityStatus,
  presentNodeHealthStatus,
  presentNodeOwnershipStatus,
} from "@/lib/foundation/status/node-presenters";

export type WorkerOperationalSummary = Readonly<{
  total: number;
  healthy: number;
  attention: number;
}>;

type WorkerStatusFields = Readonly<{
  status?: string;
  healthStatus?: string;
}>;

const unknownWorkerOperationalStatus = presentNodeHealthStatus(undefined);
const emptyWorkerOperationalSummary = Object.freeze({ total: 0, healthy: 0, attention: 0 });

export function presentWorkerOperationalStatus(input: unknown): DomainStatusPresentation {
  const worker = copyWorkerStatusFields(input);
  if (!worker) return unknownWorkerOperationalStatus;
  const health = presentNodeHealthStatus(worker.healthStatus);
  if (health.known) return health;
  const connectivity = presentNodeConnectivityStatus(worker.status);
  if (connectivity.known) return connectivity;
  return presentNodeOwnershipStatus(worker.status);
}

export function summarizeWorkerOperations(workers: unknown): WorkerOperationalSummary {
  try {
    if (!Array.isArray(workers)) return emptyWorkerOperationalSummary;
    let healthy = 0;
    for (const worker of workers) {
      if (isConfirmedHealthyWorker(worker)) healthy += 1;
    }
    return Object.freeze({
      total: workers.length,
      healthy,
      attention: workers.length - healthy,
    });
  } catch {
    return emptyWorkerOperationalSummary;
  }
}

function isConfirmedHealthyWorker(input: unknown): boolean {
  const worker = copyWorkerStatusFields(input);
  if (!worker) return false;
  const connectivity = presentNodeConnectivityStatus(worker.status);
  const health = presentNodeHealthStatus(worker.healthStatus);
  return connectivity.known
    && connectivity.labelKey === "statusNodeOnline"
    && health.known
    && health.labelKey === "statusNodeHealthy";
}

function copyWorkerStatusFields(input: unknown): WorkerStatusFields | undefined {
  if (input === null || typeof input !== "object") return undefined;
  try {
    if (Array.isArray(input)) return undefined;
    const prototype = Object.getPrototypeOf(input);
    if (prototype !== Object.prototype && prototype !== null) return undefined;
    const keys = Reflect.ownKeys(input);
    const properties = Object.getOwnPropertyDescriptors(input);
    for (const key of keys) {
      if (typeof key !== "string") return undefined;
      const descriptor = properties[key];
      if (!descriptor || !descriptor.enumerable || !("value" in descriptor)) return undefined;
    }

    const status = copyStringDataProperty(input, properties, "status");
    const healthStatus = copyStringDataProperty(input, properties, "health_status");
    const serviceType = copyStringDataProperty(input, properties, "service_type");
    if (status === null || healthStatus === null || serviceType === null) return undefined;
    return Object.freeze({
      ...(status === undefined ? {} : { status }),
      ...(healthStatus === undefined ? {} : { healthStatus }),
    });
  } catch {
    return undefined;
  }
}

function copyStringDataProperty(
  input: object,
  properties: PropertyDescriptorMap,
  key: string,
): string | undefined | null {
  const descriptor = properties[key];
  if (!descriptor) return undefined;
  const value = Reflect.get(input, key);
  if (!("value" in descriptor) || value !== descriptor.value) return null;
  return typeof value === "string" ? value : undefined;
}
