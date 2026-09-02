import type {
  OneTimeSecretLifecycleOwner,
} from "@/lib/foundation/secrets/contracts";
import type { WorkerNode } from "@/types/domain";

export type NodeOneTimeSecretField = Readonly<{
  kind:
    | "registration-token"
    | "configure-token"
    | "runtime-token"
    | "configuration"
    | "configure-command";
  value: string;
}>;

export type NodeOneTimeSecretValue = Readonly<{
  fields: readonly NodeOneTimeSecretField[];
}>;

export type NodeOneTimePublicResult = Readonly<{
  node?: WorkerNode;
  nodeApiUrl?: string;
  configureTokenExpiresAt?: string;
  manualConfigurationRequired?: boolean;
  configurationPath?: string;
}>;

export type NodeOneTimeAdoptionResult = Readonly<{
  adopted: boolean;
  publicResult: NodeOneTimePublicResult;
}>;

const secretFields = Object.freeze([
  Object.freeze({ wire: "token", kind: "registration-token" as const }),
  Object.freeze({ wire: "configure_token", kind: "configure-token" as const }),
  Object.freeze({ wire: "runtime_token", kind: "runtime-token" as const }),
  Object.freeze({ wire: "configuration_yaml", kind: "configuration" as const }),
  Object.freeze({ wire: "configure_command", kind: "configure-command" as const }),
]);

export function adoptNodeOneTimeResponse(
  owner: OneTimeSecretLifecycleOwner<NodeOneTimeSecretValue>,
  response: unknown,
): NodeOneTimeAdoptionResult {
  // Replacement is terminal for the previous generation even when the new
  // response is malformed or contains no one-time output.
  owner.dismiss();
  if (!plainRecord(response)) return emptyResult;

  const publicResult = copyPublicResult(response);
  const fields: NodeOneTimeSecretField[] = [];
  for (const descriptor of secretFields) {
    const candidate = safeOwn(response, descriptor.wire);
    if (candidate === undefined) continue;
    if (typeof candidate !== "string" || candidate.length === 0 || candidate.length > 2_000_000) {
      return Object.freeze({ adopted: false, publicResult });
    }
    fields.push(Object.freeze({ kind: descriptor.kind, value: candidate }));
  }
  if (fields.length === 0) return Object.freeze({ adopted: false, publicResult });

  const expiryValue = safeOwn(response, "configure_token_expires_at");
  let backendExpiresAtEpochMs: number | undefined;
  if (expiryValue !== undefined) {
    if (typeof expiryValue !== "string") return Object.freeze({ adopted: false, publicResult });
    const parsed = Date.parse(expiryValue);
    if (!Number.isSafeInteger(parsed) || parsed < 0) return Object.freeze({ adopted: false, publicResult });
    backendExpiresAtEpochMs = parsed;
  }
  const adopted = owner.replace({
    value: Object.freeze({ fields: Object.freeze(fields) }),
    ...(backendExpiresAtEpochMs === undefined ? {} : { backendExpiresAtEpochMs }),
    initialVisibility: "concealed",
  });
  return Object.freeze({ adopted, publicResult });
}

const emptyResult = Object.freeze({
  adopted: false,
  publicResult: Object.freeze({}),
} as const satisfies NodeOneTimeAdoptionResult);

function copyPublicResult(response: Record<string, unknown>): NodeOneTimePublicResult {
  const node = copyPublicNode(safeOwn(response, "node"));
  const nodeApiUrl = safeBoundedString(safeOwn(response, "node_api_url"), 2_048);
  const configureTokenExpiresAt = safeBoundedString(safeOwn(response, "configure_token_expires_at"), 128);
  const configurationPath = safeBoundedString(safeOwn(response, "configuration_path"), 1_024);
  const manual = safeOwn(response, "manual_configuration_required");
  return Object.freeze({
    ...(node ? { node } : {}),
    ...(nodeApiUrl ? { nodeApiUrl } : {}),
    ...(configureTokenExpiresAt ? { configureTokenExpiresAt } : {}),
    ...(typeof manual === "boolean" ? { manualConfigurationRequired: manual } : {}),
    ...(configurationPath ? { configurationPath } : {}),
  });
}

function copyPublicNode(value: unknown): WorkerNode | undefined {
  if (!plainRecord(value)) return undefined;
  const id = safeBoundedString(safeOwn(value, "id"), 128) ?? "";
  const serviceID = safeBoundedString(safeOwn(value, "service_id"), 128);
  const serviceType = safeBoundedString(safeOwn(value, "service_type"), 64);
  const serviceName = safeBoundedString(safeOwn(value, "service_name"), 128);
  const status = safeBoundedString(safeOwn(value, "status"), 64);
  if ((!id && !serviceID) || !serviceType || !serviceName || !status) return undefined;
  return Object.freeze({
    id: id || serviceID || "",
    ...(serviceID ? { service_id: serviceID } : {}),
    service_type: serviceType,
    service_name: serviceName,
    status,
  });
}

function safeOwn(value: Record<string, unknown>, key: string) {
  try {
    return Object.prototype.hasOwnProperty.call(value, key) ? Reflect.get(value, key) : undefined;
  } catch {
    return undefined;
  }
}

function safeBoundedString(value: unknown, maximum: number) {
  return typeof value === "string" && value.length > 0 && value.length <= maximum ? value : undefined;
}

function plainRecord(value: unknown): value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}
