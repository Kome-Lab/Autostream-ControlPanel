import type { ActionDescriptor } from "../src/lib/foundation/actions/contracts.ts";
import type { WorkerRestartControllerDependencies } from "../src/features/workers/workers-action-controller.ts";
import type { WorkerConfigurationDescriptor } from "../src/features/workers/workers-configuration-descriptor.ts";
import type { CanonicalWorkerNode } from "../src/features/workers/workers-wire-normalizer.ts";
import type { WorkerNode } from "../src/types/domain.ts";

declare const descriptor: ActionDescriptor;
export const validWorkerDescriptor = descriptor;

// @ts-expect-error Worker descriptor requires the restart permission tuple
export const missingRestartPermission: ReturnType<typeof import("../src/features/workers/workers-action-descriptors.ts").buildWorkerRestartDescriptor> = { ...descriptor, permissions: { kind: "none" } };
// @ts-expect-error Worker restart retry is fixed to never
export const automaticRestartRetry: ReturnType<typeof import("../src/features/workers/workers-action-descriptors.ts").buildWorkerRestartDescriptor> = { ...descriptor, retry: { kind: "server-idempotent", maxAttempts: 2 } };
// @ts-expect-error endpoint overrides are not controller dependencies
export const endpointOverride: WorkerRestartControllerDependencies = { endpoint: "/different" };

declare const unnormalizedWorkerWire: Readonly<{
  service_id: "worker-1";
  service_type: "worker";
  service_name: "Worker";
  status: "online";
}>;
// @ts-expect-error raw service_id-only wire values are not canonical WorkerNode values
export const rawWireAsCanonical: WorkerNode = unnormalizedWorkerWire;

export const mismatchedCanonicalIdentity: CanonicalWorkerNode<"worker-1"> = {
  // @ts-expect-error canonical identity requires id to equal service_id
  id: "worker-2",
  service_id: "worker-1",
  service_type: "worker",
  service_name: "Worker",
  status: "online",
};

export const configurationAny: WorkerConfigurationDescriptor = {
  labelKey: "workerConfigurationAction",
  permissions: { kind: "any", permissions: ["service_health.read", "api_tokens.create"] },
  disclosure: "visible-denied",
};
// @ts-expect-error Configuration authority is ANY, never ALL
export const configurationAll: WorkerConfigurationDescriptor = { ...configurationAny, permissions: { kind: "all", permissions: ["service_health.read", "api_tokens.create"] } };
