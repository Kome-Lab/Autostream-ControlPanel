import type { NodeProjectionAction } from "@/features/nodes/node-action-descriptors";

export const NODE_PROJECTION_CONTROL_API_MAJOR = "2";
export const NODE_PROJECTION_CONTRACT_VERSION = 1;

export type NodeProjectionRequestInput = Readonly<{
  action: NodeProjectionAction;
  nodeType?: string;
  nodeId?: string;
  allowRuntimeSecrets?: boolean;
  allowRemediation?: boolean;
}>;

export type NodeActionProjection = Readonly<{
  contractVersion: 1;
  projectionRevision: string;
  evaluatedAt: string;
  action: NodeProjectionAction;
  availability: "allowed" | "denied" | "unknown" | "not_applicable";
  reasonCode:
    | "allowed"
    | "additional_permission_required"
    | "invalid_service_type"
    | "invalid_service_scope"
    | "projection_unavailable"
    | "staged_runtime_token_rotation_required";
  requiredPermissions: readonly NodeProjectedPermission[];
  missingPermissions: readonly NodeProjectedPermission[];
}>;

export type NodeProjectedPermission =
  | "api_tokens.create"
  | "api_tokens.revoke"
  | "secrets.update"
  | "system_updates.execute"
  | "streams.start"
  | "streams.stop"
  | "remediation.execute";

const exactWireKeys = Object.freeze([
  "action",
  "availability",
  "contract_version",
  "evaluated_at",
  "missing_permissions",
  "projection_revision",
  "reason_code",
  "required_permissions",
]);
const actions = new Set<NodeProjectionAction>([
  "registration_token",
  "configure_token_regenerate",
  "runtime_token_rotate",
]);
const availabilityValues = new Set(["allowed", "denied", "unknown", "not_applicable"]);
const permissions = new Set<NodeProjectedPermission>([
  "api_tokens.create",
  "api_tokens.revoke",
  "secrets.update",
  "system_updates.execute",
  "streams.start",
  "streams.stop",
  "remediation.execute",
]);

export function nodeProjectionRequest(input: NodeProjectionRequestInput) {
  if (!validProjectionRequest(input)) return undefined;
  const query = new URLSearchParams();
  query.set("action", input.action);
  if (input.nodeType !== undefined) query.set("node_type", input.nodeType);
  if (input.nodeId !== undefined) query.set("node_id", input.nodeId);
  if (input.allowRuntimeSecrets !== undefined) query.set("allow_runtime_secrets", String(input.allowRuntimeSecrets));
  if (input.allowRemediation !== undefined) query.set("allow_remediation", String(input.allowRemediation));
  return Object.freeze({
    path: `/nodes/action-permissions?${query.toString()}`,
    headers: Object.freeze({
      Accept: "application/json",
      "X-AutoStream-Contract-Major": NODE_PROJECTION_CONTROL_API_MAJOR,
    }),
  });
}

export function parseNodeActionProjectionResponse(input: Readonly<{
  status: number;
  headers: Headers;
  body: unknown;
}>): NodeActionProjection | undefined {
  try {
    if (input.status !== 200 || !validHeaders(input.headers) || !plainRecord(input.body)) return undefined;
    const body = input.body;
    if (!exactKeys(body, exactWireKeys)) return undefined;
    const action = body.action;
    const availability = body.availability;
    const reasonCode = body.reason_code;
    const requiredPermissions = permissionSet(body.required_permissions);
    const missingPermissions = permissionSet(body.missing_permissions);
    if (
      body.contract_version !== NODE_PROJECTION_CONTRACT_VERSION
      || typeof body.projection_revision !== "string"
      || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(body.projection_revision)
      || typeof body.evaluated_at !== "string"
      || !validDateTime(body.evaluated_at)
      || typeof action !== "string"
      || !actions.has(action as NodeProjectionAction)
      || typeof availability !== "string"
      || !availabilityValues.has(availability)
      || typeof reasonCode !== "string"
      || !requiredPermissions
      || !missingPermissions
      || !validDisposition(availability, reasonCode, missingPermissions.length)
      || missingPermissions.some((permission) => !requiredPermissions.includes(permission))
    ) return undefined;
    return Object.freeze({
      contractVersion: 1,
      projectionRevision: body.projection_revision,
      evaluatedAt: body.evaluated_at,
      action: action as NodeProjectionAction,
      availability: availability as NodeActionProjection["availability"],
      reasonCode: reasonCode as NodeActionProjection["reasonCode"],
      requiredPermissions,
      missingPermissions,
    });
  } catch {
    return undefined;
  }
}

export async function fetchNodeActionProjection(
  input: NodeProjectionRequestInput,
  signal?: AbortSignal,
): Promise<NodeActionProjection | undefined> {
  const request = nodeProjectionRequest(input);
  if (!request) return undefined;
  let response: Response;
  try {
    response = await fetch(request.path, {
      method: "GET",
      credentials: "same-origin",
      headers: request.headers,
      signal,
    });
  } catch {
    return undefined;
  }
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return undefined;
  }
  return parseNodeActionProjectionResponse({ status: response.status, headers: response.headers, body });
}

function validProjectionRequest(input: NodeProjectionRequestInput) {
  if (!actions.has(input.action)) return false;
  if (input.action === "registration_token") {
    return typeof input.nodeType === "string"
      && /^[a-z][a-z0-9_]{0,63}$/.test(input.nodeType)
      && input.nodeId === undefined
      && (input.allowRuntimeSecrets === undefined || typeof input.allowRuntimeSecrets === "boolean")
      && (input.allowRemediation === undefined || typeof input.allowRemediation === "boolean");
  }
  return typeof input.nodeId === "string"
    && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(input.nodeId)
    && input.nodeType === undefined
    && input.allowRuntimeSecrets === undefined
    && input.allowRemediation === undefined;
}

function validHeaders(headers: Headers) {
  const contentType = headers.get("content-type")?.toLowerCase() ?? "";
  const cacheControl = headers.get("cache-control")?.toLowerCase().split(",").map((part) => part.trim()) ?? [];
  return /^application\/json(?:\s*;|$)/.test(contentType)
    && cacheControl.includes("no-store")
    && cacheControl.includes("no-cache")
    && headers.get("pragma")?.trim().toLowerCase() === "no-cache"
    && headers.get("referrer-policy")?.trim().toLowerCase() === "no-referrer"
    && headers.get("x-autostream-contract-major")?.trim() === NODE_PROJECTION_CONTROL_API_MAJOR;
}

function validDisposition(availability: string, reason: string, missingCount: number) {
  if (availability === "allowed") return reason === "allowed" && missingCount === 0;
  if (availability === "denied") return reason === "additional_permission_required" && missingCount > 0;
  if (availability === "unknown") return ["invalid_service_scope", "projection_unavailable"].includes(reason) && missingCount === 0;
  if (availability === "not_applicable") return ["invalid_service_type", "staged_runtime_token_rotation_required"].includes(reason) && missingCount === 0;
  return false;
}

function permissionSet(value: unknown): readonly NodeProjectedPermission[] | undefined {
  if (!Array.isArray(value) || value.length > 8) return undefined;
  const copy: NodeProjectedPermission[] = [];
  for (const entry of value) {
    if (typeof entry !== "string" || !permissions.has(entry as NodeProjectedPermission) || copy.includes(entry as NodeProjectedPermission)) return undefined;
    copy.push(entry as NodeProjectedPermission);
  }
  return Object.freeze(copy);
}

function plainRecord(value: unknown): value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function exactKeys(value: Record<string, unknown>, expected: readonly string[]) {
  const actual = Object.keys(value).sort();
  return actual.length === expected.length && actual.every((key, index) => key === expected[index]);
}

function validDateTime(value: string) {
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value)
    && Number.isFinite(Date.parse(value));
}
