import type { ActionDescriptor, ActionRisk, RetryPolicy } from "@/lib/foundation/actions/contracts";
import type { PermissionRequirement } from "@/lib/foundation/permissions/contracts";
import type { Stream } from "@/types/domain";

export type StreamActionID =
  | "STR-01" | "STR-02" | "STR-03" | "STR-04" | "STR-05" | "STR-06"
  | "STR-07" | "STR-08" | "STR-09" | "STR-10" | "STR-11";

export type StreamActionIntent = Readonly<{
  id: StreamActionID;
  stream?: Stream;
  payload?: Readonly<Record<string, unknown>>;
  publicLabel?: string;
  staticRelayRecoveryAvailable?: boolean;
}>;

export type StreamActionRequest = Readonly<{
  id: StreamActionID;
  method: "POST" | "PUT" | "DELETE";
  path: string;
  body?: unknown;
}>;

export type StreamActionDescriptorTemplate = Readonly<{
  id: StreamActionID;
  risk: ActionRisk;
  basePermission: string;
  method: StreamActionRequest["method"];
  auditAction: string;
  retry: RetryPolicy;
}>;

const neverRetry = Object.freeze({ kind: "never" as const });

export const streamActionDescriptors: readonly StreamActionDescriptorTemplate[] = Object.freeze([
  template("STR-01", "guarded", "streams.create", "POST", "streams.create", neverRetry),
  template("STR-02", "guarded", "streams.update", "PUT", "streams.update_settings", neverRetry),
  template("STR-03", "high", "streams.update", "PUT", "streams.update_runtime_settings", neverRetry),
  template("STR-04", "high", "streams.start", "POST", "streams.start", neverRetry),
  template("STR-05", "high", "streams.stop", "POST", "streams.stop", neverRetry),
  template("STR-06", "critical", "streams.stop", "POST", "streams.force_stop", neverRetry),
  template("STR-07", "critical", "streams.stop", "POST", "streams.youtube_relay_static_recovery.resolve", neverRetry),
  template("STR-08", "guarded", "streams.start", "POST", "none", Object.freeze({ kind: "manual-after-refresh" as const })),
  template("STR-09", "guarded", "streams.update", "POST", "streams.worker_event_test", neverRetry),
  template("STR-10", "critical", "streams.delete", "DELETE", "streams.delete", neverRetry),
  template("STR-11", "routine", "streams.read", "POST", "streams.preview_link.create", neverRetry),
]);

const templates = new Map(streamActionDescriptors.map((value) => [value.id, value]));

export function streamActionTemplate(id: StreamActionID) {
  return templates.get(id);
}

export function buildStreamActionPermissionRequirement(intent: StreamActionIntent): PermissionRequirement {
  const base = streamActionTemplate(intent.id)?.basePermission;
  if (!base) return Object.freeze({ kind: "all", permissions: Object.freeze(["__invalid_stream_action__"] as const) });
  const permissions = [base];
  if (intent.id === "STR-02") {
    const payload = intent.payload;
    if (hasOwn(payload, "encoder_service_id")) permissions.push("services.assign");
    if (hasOwn(payload, "worker_service_id")) permissions.push("workers.assign");
  }
  return Object.freeze({
    kind: "all" as const,
    permissions: Object.freeze(permissions) as readonly [string, ...string[]],
  });
}

export function buildStreamActionDescriptor(intent: StreamActionIntent): ActionDescriptor | undefined {
  const templateValue = streamActionTemplate(intent.id);
  const target = streamActionTarget(intent);
  if (!templateValue || !target || !streamActionRequest(intent)) return undefined;
  const typed = templateValue.risk === "critical";
  const none = intent.id === "STR-11";
  return Object.freeze({
    id: intent.id,
    labelKey: labelKey(intent.id),
    risk: templateValue.risk,
    target: Object.freeze(target),
    permissions: buildStreamActionPermissionRequirement(intent),
    applicability: Object.freeze({
      ruleIds: Object.freeze([`stream-${intent.id.toLowerCase()}-lifecycle`]),
      requiredSections: Object.freeze(["auth", "streams"]),
    }),
    confirmation: none
      ? Object.freeze({ mode: "none" as const, requireSubmitRevalidation: true })
      : typed
        ? Object.freeze({
            mode: "typed-target" as const,
            consequenceKey: "dangerousNotice" as const,
            typedToken: Object.freeze({ kind: "target-label" as const }),
            requireSubmitRevalidation: true as const,
          })
        : Object.freeze({
            mode: "consequence" as const,
            consequenceKey: "dangerousNotice" as const,
            requireSubmitRevalidation: true,
          }),
    duplicate: Object.freeze({
      scope: intent.id === "STR-01" ? "resource-action" as const : "resource-target" as const,
      whilePending: "block" as const,
    }),
    retry: templateValue.retry,
    audit: Object.freeze({
      action: templateValue.auditAction,
      labelKey: "actions" as const,
      safeReferenceFieldIds: Object.freeze([intent.id === "STR-01" ? "publicLabel" : "publicLabel"]),
    }),
    stateIndependent: false,
    revalidation: Object.freeze({
      kind: "safe-fingerprint" as const,
      fieldIds: Object.freeze(["id", "status", "updatedAt"]),
    }),
  });
}

export function streamActionRequest(intent: StreamActionIntent): StreamActionRequest | undefined {
  const id = intent.stream?.id;
  const encoded = id ? encodeURIComponent(id) : "";
  switch (intent.id) {
    case "STR-01":
      return intent.payload ? request(intent.id, "POST", "/streams", intent.payload) : undefined;
    case "STR-02":
      return encoded && intent.payload ? request(intent.id, "PUT", `/streams/${encoded}/settings`, intent.payload) : undefined;
    case "STR-03":
      return encoded && intent.payload ? request(intent.id, "PUT", `/streams/${encoded}/runtime-settings`, intent.payload) : undefined;
    case "STR-04": return encoded ? request(intent.id, "POST", `/streams/${encoded}/start`) : undefined;
    case "STR-05": return encoded ? request(intent.id, "POST", `/streams/${encoded}/stop`) : undefined;
    case "STR-06": return encoded ? request(intent.id, "POST", `/streams/${encoded}/force-stop`) : undefined;
    case "STR-07": return encoded ? request(intent.id, "POST", `/streams/${encoded}/youtube/relay-static/recovery/resolve`, { confirm_external_cleanup: true }) : undefined;
    case "STR-08": return encoded ? request(intent.id, "POST", `/streams/${encoded}/start-readiness`) : undefined;
    case "STR-09": return encoded ? request(intent.id, "POST", `/streams/${encoded}/worker-events/test`, { event_type: "current_time" }) : undefined;
    case "STR-10": return encoded ? request(intent.id, "DELETE", `/streams/${encoded}`) : undefined;
    case "STR-11": return encoded ? request(intent.id, "POST", `/streams/${encoded}/preview-links`) : undefined;
  }
}

export function streamActionScope(intent: StreamActionIntent): string | undefined {
  const requestValue = streamActionRequest(intent);
  const target = streamActionTarget(intent);
  return requestValue && target ? `stream:${target.resourceId}:${intent.id}` : undefined;
}

export function streamActionApplicable(intent: StreamActionIntent): boolean {
  if (intent.id === "STR-01") return Boolean(intent.payload && safePublicLabel(streamActionLabel(intent)));
  const status = String(intent.stream?.status || "").trim().toLowerCase();
  if (!intent.stream?.id || !safePublicLabel(intent.stream.name)) return false;
  switch (intent.id) {
    case "STR-02": return !["starting", "live", "stopping"].includes(status);
    case "STR-03": return status === "live";
    case "STR-04": return ["created", "draft", "scheduled", "ready", "failed"].includes(status);
    case "STR-05": return ["starting", "live", "failed"].includes(status);
    case "STR-06": return ["starting", "live", "stopping", "failed"].includes(status);
    case "STR-07": return intent.staticRelayRecoveryAvailable === true && ["failed", "completed"].includes(status);
    case "STR-08":
    case "STR-09": return true;
    case "STR-10": return !["starting", "live", "stopping"].includes(status);
    case "STR-11": return ["starting", "live", "stopping"].includes(status);
  }
}

function streamActionTarget(intent: StreamActionIntent) {
  const resourceId = intent.id === "STR-01" ? "new-stream" : intent.stream?.id || "";
  const publicLabel = streamActionLabel(intent);
  if (!resourceId || !safePublicLabel(publicLabel)) return undefined;
  return { resourceType: "stream", resourceId, publicLabel };
}

function streamActionLabel(intent: StreamActionIntent) {
  if (intent.id !== "STR-01") return intent.stream?.name?.trim() || "";
  const payloadName = intent.payload?.name;
  return (intent.publicLabel || (typeof payloadName === "string" ? payloadName : "")).trim();
}

function labelKey(id: StreamActionID): "streams" | "save" | "start" | "stop" | "actions" {
  if (id === "STR-01") return "streams";
  if (id === "STR-02" || id === "STR-03") return "save";
  if (id === "STR-04") return "start";
  if (id === "STR-05" || id === "STR-06") return "stop";
  return "actions";
}

function template(
  id: StreamActionID,
  risk: ActionRisk,
  basePermission: string,
  method: StreamActionRequest["method"],
  auditAction: string,
  retry: RetryPolicy,
): StreamActionDescriptorTemplate {
  return Object.freeze({ id, risk, basePermission, method, auditAction, retry });
}

function request(id: StreamActionID, method: StreamActionRequest["method"], path: string, body?: unknown): StreamActionRequest {
  return Object.freeze({ id, method, path, ...(body === undefined ? {} : { body }) });
}

function hasOwn(value: unknown, key: string) {
  return typeof value === "object" && value !== null && Object.prototype.hasOwnProperty.call(value, key);
}

function safePublicLabel(value: string) {
  return value.length > 0
    && value.length <= 128
    && value.trim() === value
    && !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value)
    && !/^[^\s@]+@[^\s@]+$/.test(value)
    && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value);
}
