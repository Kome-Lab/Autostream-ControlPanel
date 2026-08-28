import type { WorkerNode } from "@/types/domain";

export type CanonicalWorkerNode<Identity extends string = string> = Readonly<
  Omit<WorkerNode, "id" | "service_id"> & Readonly<{
    id: Identity;
    service_id: Identity;
  }>
>;

type CopiedJSONPrimitive =
  | null
  | boolean
  | number
  | string;

interface CopiedJSONArray {
  readonly [index: number]: CopiedJSONValue;
  readonly length: number;
}

interface CopiedJSONObject {
  readonly [key: string]: CopiedJSONValue;
}

type CopiedJSONValue = CopiedJSONPrimitive | CopiedJSONArray | CopiedJSONObject;

const invalidJSONValue = Symbol("invalid-worker-json-value");

export function copyCanonicalWorkerWireValue(value: unknown): CanonicalWorkerNode | undefined {
  try {
    const properties = plainDataProperties(value);
    if (!properties) return undefined;

    const serviceId = requiredString(properties, "service_id", true);
    const serviceType = requiredString(properties, "service_type", true);
    const serviceName = requiredString(properties, "service_name", false);
    const status = requiredString(properties, "status", false);
    if (serviceId === undefined || serviceType === undefined || serviceName === undefined || status === undefined) {
      return undefined;
    }

    const compatibilityId = optionalString(properties, "id");
    if (compatibilityId.kind === "invalid"
      || (compatibilityId.kind === "value" && compatibilityId.value !== serviceId)) {
      return undefined;
    }

    const healthStatus = optionalString(properties, "health_status");
    const assignmentRole = optionalString(properties, "assignment_role");
    const currentStreamId = optionalString(properties, "current_stream_id");
    const reportedVersion = optionalString(properties, "reported_version");
    const reportedOS = optionalString(properties, "reported_os");
    const reportedArch = optionalString(properties, "reported_arch");
    const lastHeartbeatAt = optionalString(properties, "last_heartbeat_at");
    if ([healthStatus, assignmentRole, currentStreamId, reportedVersion, reportedOS, reportedArch, lastHeartbeatAt]
      .some((entry) => entry.kind === "invalid")) {
      return undefined;
    }

    const heartbeatAge = optionalFiniteNumber(properties, "heartbeat_age_sec");
    if (heartbeatAge.kind === "invalid") return undefined;

    const capabilities = optionalJSONRecord(properties, "capabilities");
    const reportedCapabilities = optionalJSONRecord(properties, "reported_capabilities");
    const metrics = optionalMetrics(properties, "metrics");
    if (capabilities.kind === "invalid" || reportedCapabilities.kind === "invalid" || metrics.kind === "invalid") {
      return undefined;
    }

    return Object.freeze({
      id: serviceId,
      service_id: serviceId,
      service_type: serviceType,
      service_name: serviceName,
      status,
      ...(healthStatus.kind === "value" ? { health_status: healthStatus.value } : {}),
      ...(assignmentRole.kind === "value" ? { assignment_role: assignmentRole.value } : {}),
      ...(currentStreamId.kind === "value" ? { current_stream_id: currentStreamId.value } : {}),
      ...(reportedVersion.kind === "value" ? { reported_version: reportedVersion.value } : {}),
      ...(reportedOS.kind === "value" ? { reported_os: reportedOS.value } : {}),
      ...(reportedArch.kind === "value" ? { reported_arch: reportedArch.value } : {}),
      ...(lastHeartbeatAt.kind === "value" ? { last_heartbeat_at: lastHeartbeatAt.value } : {}),
      ...(heartbeatAge.kind === "value" ? { heartbeat_age_sec: heartbeatAge.value } : {}),
      ...(capabilities.kind === "value" ? { capabilities: capabilities.value } : {}),
      ...(reportedCapabilities.kind === "value" ? { reported_capabilities: reportedCapabilities.value } : {}),
      ...(metrics.kind === "value" ? { metrics: metrics.value } : {}),
    });
  } catch {
    return undefined;
  }
}

export function copyCanonicalWorkerWireList(value: unknown): readonly CanonicalWorkerNode[] | undefined {
  try {
    if (!Array.isArray(value)) return undefined;
    const copy: CanonicalWorkerNode[] = [];
    for (const entry of value) {
      const worker = copyCanonicalWorkerWireValue(entry);
      if (!worker) return undefined;
      copy.push(worker);
    }
    return Object.freeze(copy);
  } catch {
    return undefined;
  }
}

type OptionalValue<T> =
  | Readonly<{ kind: "absent" }>
  | Readonly<{ kind: "value"; value: T }>
  | Readonly<{ kind: "invalid" }>;

function plainDataProperties(value: unknown): PropertyDescriptorMap | undefined {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return undefined;
  const prototype = Object.getPrototypeOf(value);
  if (prototype !== Object.prototype && prototype !== null) return undefined;
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key === "symbol")) return undefined;
  const properties = Object.getOwnPropertyDescriptors(value);
  for (const key of keys) {
    if (typeof key !== "string") return undefined;
    const descriptor = properties[key];
    if (!descriptor || !descriptor.enumerable || !("value" in descriptor)) return undefined;
  }
  return properties;
}

function requiredString(
  properties: PropertyDescriptorMap,
  key: string,
  requireNonEmpty: boolean,
): string | undefined {
  const descriptor = properties[key];
  if (!descriptor || !("value" in descriptor) || typeof descriptor.value !== "string") return undefined;
  if (requireNonEmpty && descriptor.value.length === 0) return undefined;
  return descriptor.value;
}

function optionalString(properties: PropertyDescriptorMap, key: string): OptionalValue<string> {
  const descriptor = properties[key];
  if (!descriptor) return Object.freeze({ kind: "absent" });
  return "value" in descriptor && typeof descriptor.value === "string"
    ? Object.freeze({ kind: "value", value: descriptor.value })
    : Object.freeze({ kind: "invalid" });
}

function optionalFiniteNumber(properties: PropertyDescriptorMap, key: string): OptionalValue<number> {
  const descriptor = properties[key];
  if (!descriptor) return Object.freeze({ kind: "absent" });
  return "value" in descriptor && typeof descriptor.value === "number" && Number.isFinite(descriptor.value)
    ? Object.freeze({ kind: "value", value: descriptor.value })
    : Object.freeze({ kind: "invalid" });
}

function optionalJSONRecord(
  properties: PropertyDescriptorMap,
  key: string,
): OptionalValue<Readonly<Record<string, CopiedJSONValue>>> {
  const descriptor = properties[key];
  if (!descriptor) return Object.freeze({ kind: "absent" });
  if (!("value" in descriptor)) return Object.freeze({ kind: "invalid" });
  const copied = copyJSONRecord(descriptor.value, new WeakSet<object>());
  return copied
    ? Object.freeze({ kind: "value", value: copied })
    : Object.freeze({ kind: "invalid" });
}

function optionalMetrics(
  properties: PropertyDescriptorMap,
  key: string,
): OptionalValue<Readonly<Record<string, number | string>>> {
  const descriptor = properties[key];
  if (!descriptor) return Object.freeze({ kind: "absent" });
  if (!("value" in descriptor)) return Object.freeze({ kind: "invalid" });
  const metricProperties = plainDataProperties(descriptor.value);
  if (!metricProperties) return Object.freeze({ kind: "invalid" });
  const copy: Record<string, number | string> = Object.create(null);
  for (const metricKey of Object.keys(metricProperties)) {
    const metric = metricProperties[metricKey];
    if (!("value" in metric)
      || (typeof metric.value !== "string"
        && !(typeof metric.value === "number" && Number.isFinite(metric.value)))) {
      return Object.freeze({ kind: "invalid" });
    }
    copy[metricKey] = metric.value;
  }
  return Object.freeze({ kind: "value", value: Object.freeze(copy) });
}

function copyJSONRecord(
  value: unknown,
  ancestors: WeakSet<object>,
): Readonly<Record<string, CopiedJSONValue>> | undefined {
  const properties = plainDataProperties(value);
  if (!properties || value === null || typeof value !== "object" || ancestors.has(value)) return undefined;
  ancestors.add(value);
  try {
    const copy: Record<string, CopiedJSONValue> = Object.create(null);
    for (const key of Object.keys(properties)) {
      const descriptor = properties[key];
      if (!("value" in descriptor)) return undefined;
      const copied = copyJSONValue(descriptor.value, ancestors);
      if (copied === invalidJSONValue) return undefined;
      copy[key] = copied;
    }
    return Object.freeze(copy);
  } finally {
    ancestors.delete(value);
  }
}

function copyJSONValue(
  value: unknown,
  ancestors: WeakSet<object>,
): CopiedJSONValue | typeof invalidJSONValue {
  if (value === null || typeof value === "string" || typeof value === "boolean") return value;
  if (typeof value === "number") return Number.isFinite(value) ? value : invalidJSONValue;
  if (Array.isArray(value)) return copyJSONArray(value, ancestors);
  if (typeof value !== "object") return invalidJSONValue;
  return copyJSONRecord(value, ancestors) ?? invalidJSONValue;
}

function copyJSONArray(
  value: unknown[],
  ancestors: WeakSet<object>,
): readonly CopiedJSONValue[] | typeof invalidJSONValue {
  if (Object.getPrototypeOf(value) !== Array.prototype || ancestors.has(value)) return invalidJSONValue;
  const properties = Object.getOwnPropertyDescriptors(value);
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key === "symbol")) return invalidJSONValue;
  const lengthDescriptor = Object.getOwnPropertyDescriptor(value, "length");
  if (!lengthDescriptor || !("value" in lengthDescriptor) || lengthDescriptor.value !== value.length) {
    return invalidJSONValue;
  }
  ancestors.add(value);
  try {
    const copy: CopiedJSONValue[] = [];
    for (let index = 0; index < value.length; index += 1) {
      const descriptor = properties[String(index)];
      if (!descriptor || !descriptor.enumerable || !("value" in descriptor)) return invalidJSONValue;
      const copied = copyJSONValue(descriptor.value, ancestors);
      if (copied === invalidJSONValue) return invalidJSONValue;
      copy.push(copied);
    }
    if (keys.some((key) => typeof key === "string" && key !== "length" && !/^(0|[1-9]\d*)$/.test(key))) {
      return invalidJSONValue;
    }
    return Object.freeze(copy);
  } finally {
    ancestors.delete(value);
  }
}
