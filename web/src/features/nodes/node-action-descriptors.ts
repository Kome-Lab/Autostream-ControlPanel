import type { ActionDescriptor, ActionRisk } from "@/lib/foundation/actions/contracts";
import type { WorkerNode } from "@/types/domain";

export type NodeActionID = "NOD-01" | "NOD-02" | "NOD-03" | "NOD-04" | "NOD-05";
export type NodeProjectionAction =
  | "registration_token"
  | "configure_token_regenerate"
  | "runtime_token_rotate";

export type NodeRegistrationAuthorityInput = Readonly<{
  nodeType: string;
  nodeId: string;
  allowRuntimeSecrets: boolean;
  allowRemediation: boolean;
}>;

export type NodeActionIntent = Readonly<{
  id: NodeActionID;
  node?: WorkerNode;
  registration?: NodeRegistrationAuthorityInput;
  body?: unknown;
}>;

export type NodeActionDescriptorTemplate = Readonly<{
  id: NodeActionID;
  risk: ActionRisk;
  basePermission: "api_tokens.create" | "services.disable";
  projectionAction?: NodeProjectionAction;
  method: "POST" | "PUT" | "DELETE";
  auditAction: string;
}>;

// A2 is intentionally source-only. A3 and release-owner approval own the only
// change that may enable this artifact in a production route.
export const NODE_FOUNDATION_SOURCE_ENABLED = false as const;

export const nodeActionDescriptors: readonly NodeActionDescriptorTemplate[] = Object.freeze([
  Object.freeze({
    id: "NOD-01",
    risk: "critical",
    basePermission: "api_tokens.create",
    projectionAction: "registration_token",
    method: "POST",
    auditAction: "nodes.registration_token.create",
  }),
  Object.freeze({
    id: "NOD-02",
    risk: "critical",
    basePermission: "api_tokens.create",
    projectionAction: "configure_token_regenerate",
    method: "POST",
    auditAction: "nodes.configure_token.rotate",
  }),
  Object.freeze({
    id: "NOD-03",
    risk: "critical",
    basePermission: "api_tokens.create",
    projectionAction: "runtime_token_rotate",
    method: "POST",
    auditAction: "nodes.runtime_token.rotate",
  }),
  Object.freeze({
    id: "NOD-04",
    risk: "high",
    basePermission: "api_tokens.create",
    method: "PUT",
    auditAction: "nodes.update",
  }),
  Object.freeze({
    id: "NOD-05",
    risk: "critical",
    basePermission: "services.disable",
    method: "DELETE",
    auditAction: "services.delete",
  }),
]);

const templateByID = new Map(nodeActionDescriptors.map((descriptor) => [descriptor.id, descriptor]));

export function buildNodeActionDescriptor(intent: NodeActionIntent): ActionDescriptor | undefined {
  const template = templateByID.get(intent.id);
  const target = nodeActionTarget(intent);
  if (!template || !target) return undefined;
  const critical = template.risk === "critical";
  const revalidation = template.projectionAction
    ? Object.freeze({ kind: "revision" as const })
    : Object.freeze({
        kind: "safe-fingerprint" as const,
        fieldIds: Object.freeze(["canonicalNodeId", "serviceType", "status"]),
      });
  return Object.freeze({
    id: template.id,
    labelKey: intent.id === "NOD-01" ? "createToken" : "actions",
    risk: template.risk,
    target: Object.freeze(target),
    permissions: Object.freeze({
      kind: "all" as const,
      permissions: Object.freeze([template.basePermission] as const),
    }),
    applicability: Object.freeze({
      ruleIds: Object.freeze([`node-${intent.id.toLowerCase()}-target`]),
      requiredSections: Object.freeze(["nodes"]),
    }),
    confirmation: critical
      ? Object.freeze({
          mode: "typed-target" as const,
          consequenceKey: "dangerousNotice",
          typedToken: Object.freeze({
            kind: intent.id === "NOD-01" ? "public-stable-id" as const : "target-label" as const,
          }),
          requireSubmitRevalidation: true as const,
        })
      : Object.freeze({
          mode: "consequence" as const,
          consequenceKey: "dangerousNotice",
          requireSubmitRevalidation: true,
        }),
    duplicate: Object.freeze({ scope: "resource-action" as const, whilePending: "block" as const }),
    retry: Object.freeze({ kind: "never" as const }),
    audit: Object.freeze({
      action: template.auditAction,
      labelKey: "action",
      safeReferenceFieldIds: Object.freeze([intent.id === "NOD-01" ? "publicStableId" : "publicLabel"]),
    }),
    stateIndependent: false,
    revalidation,
  });
}

export function nodeActionTemplate(id: NodeActionID): NodeActionDescriptorTemplate | undefined {
  return templateByID.get(id);
}

export function nodeActionScope(intent: NodeActionIntent): string | undefined {
  const template = templateByID.get(intent.id);
  const target = nodeActionTarget(intent);
  if (!template || !target) return undefined;
  const action = template.projectionAction ?? (intent.id === "NOD-04" ? "update" : "delete");
  return `node:${target.resourceId}:${action}`;
}

export function nodeActionMutationPath(intent: NodeActionIntent): string | undefined {
  const nodeID = canonicalNodeID(intent.node);
  switch (intent.id) {
    case "NOD-01":
      return intent.registration ? "/nodes/registration-tokens" : undefined;
    case "NOD-02":
      return nodeID ? `/nodes/${encodeURIComponent(nodeID)}/configure-token` : undefined;
    case "NOD-03":
      return nodeID ? `/nodes/${encodeURIComponent(nodeID)}/rotate-token` : undefined;
    case "NOD-04":
      return nodeID ? `/nodes/${encodeURIComponent(nodeID)}` : undefined;
    case "NOD-05":
      return nodeID ? `/services/${encodeURIComponent(nodeID)}` : undefined;
  }
}

export function nodeActionProjectionInput(intent: NodeActionIntent) {
  const template = templateByID.get(intent.id);
  if (!template?.projectionAction) return undefined;
  if (template.projectionAction === "registration_token") {
    const registration = intent.registration;
    return registration
      ? Object.freeze({
          action: template.projectionAction,
          nodeType: registration.nodeType,
          allowRuntimeSecrets: registration.allowRuntimeSecrets,
          allowRemediation: registration.allowRemediation,
        })
      : undefined;
  }
  const nodeID = canonicalNodeID(intent.node);
  return nodeID
    ? Object.freeze({ action: template.projectionAction, nodeId: nodeID })
    : undefined;
}

export function nodeActionFingerprint(intent: NodeActionIntent): string | undefined {
  if (intent.id === "NOD-01") {
    const registration = intent.registration;
    return registration
      ? JSON.stringify([
          registration.nodeId,
          registration.nodeType,
          registration.allowRuntimeSecrets,
          registration.allowRemediation,
        ])
      : undefined;
  }
  const node = intent.node;
  const id = canonicalNodeID(node);
  return node && id ? JSON.stringify([id, node.service_type, node.status]) : undefined;
}

export function canonicalNodeID(node: WorkerNode | undefined): string {
  if (!node) return "";
  if (typeof node.service_id === "string" && node.service_id.length > 0) return node.service_id;
  return typeof node.id === "string" ? node.id : "";
}

function nodeActionTarget(intent: NodeActionIntent) {
  if (intent.id === "NOD-01") {
    const registration = intent.registration;
    if (!registration || !safePublicIdentifier(registration.nodeId)) return undefined;
    return {
      resourceType: "node",
      resourceId: registration.nodeId,
      publicLabel: registration.nodeId,
      publicStableId: registration.nodeId,
    };
  }
  const node = intent.node;
  const resourceId = canonicalNodeID(node);
  const publicLabel = typeof node?.service_name === "string" ? node.service_name : "";
  if (!resourceId || !safePublicLabel(publicLabel)) return undefined;
  return {
    resourceType: "node",
    resourceId,
    publicLabel,
  };
}

function safePublicIdentifier(value: string) {
  return /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);
}

function safePublicLabel(value: string) {
  return value.length > 0
    && value.length <= 128
    && value.trim() === value
    && !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value)
    && !/^[^\s@]+@[^\s@]+$/.test(value)
    && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value);
}
