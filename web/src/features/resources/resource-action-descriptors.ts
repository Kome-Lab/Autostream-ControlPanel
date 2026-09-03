import type { ActionDescriptor, ActionRisk } from "@/lib/foundation/actions/contracts";
import type { PermissionRequirement } from "@/lib/foundation/permissions/contracts";

export type ResourceActionID =
  | "RES-01" | "RES-02" | "RES-03" | "RES-04" | "RES-05" | "RES-06"
  | "RES-07" | "RES-08" | "RES-09" | "RES-10" | "RES-11" | "RES-12"
  | "RES-13" | "RES-14" | "RES-15" | "RES-16" | "RES-17" | "RES-18"
  | "RES-19" | "RES-20" | "RES-21" | "RES-22" | "RES-23" | "RES-24"
  | "RES-25" | "RES-26" | "RES-27" | "RES-28" | "RES-29" | "RES-30"
  | "RES-31" | "RES-32" | "RES-33" | "RES-34" | "RES-35" | "RES-36"
  | "RES-37" | "RES-38" | "RES-39" | "RES-40";

export type ResourceActionWave = "3A" | "3B";
export type ResourceActionOperation = "create" | "update" | "delete" | "secret" | "connect" | "relink" | "test";

export type ResourceActionRow = Readonly<Record<string, unknown>>;

export type ResourceActionIntent = Readonly<{
  id: ResourceActionID;
  payload?: Readonly<Record<string, unknown>>;
  row?: ResourceActionRow;
  publicLabel?: string;
}>;

export type ResourceActionRequest = Readonly<{
  id: ResourceActionID;
  method: "POST" | "PUT" | "DELETE";
  path: string;
  body?: unknown;
}>;

export type ResourceActionTemplate = Readonly<{
  id: ResourceActionID;
  wave: ResourceActionWave;
  operation: ResourceActionOperation;
  route: string;
  sourcePath: string;
  method: ResourceActionRequest["method"];
  basePermission: string;
  risk: ActionRisk;
  confirmation: "consequence" | "typed-label" | "typed-fixed";
  fixedToken?: string;
  duplicateScope: "resource-action" | "resource-target" | "session-flow";
  auditAction: string;
  secretInput: boolean;
}>;

const row = (
  id: ResourceActionID,
  wave: ResourceActionWave,
  operation: ResourceActionOperation,
  route: string,
  sourcePath: string,
  method: ResourceActionRequest["method"],
  basePermission: string,
  risk: ActionRisk,
  confirmation: ResourceActionTemplate["confirmation"],
  duplicateScope: ResourceActionTemplate["duplicateScope"],
  auditAction: string,
  secretInput = false,
  fixedToken?: string,
): ResourceActionTemplate => Object.freeze({
  id, wave, operation, route, sourcePath, method, basePermission, risk,
  confirmation, duplicateScope, auditAction, secretInput, ...(fixedToken ? { fixedToken } : {}),
});

export const resourceActionDescriptors: readonly ResourceActionTemplate[] = Object.freeze([
  row("RES-01", "3A", "create", "/profiles/encoder", "/profiles/encoder", "POST", "encoder_profiles.create", "guarded", "consequence", "resource-action", "encoder_profiles.create"),
  row("RES-02", "3A", "update", "/profiles/encoder/{id}", "/profiles/encoder", "PUT", "encoder_profiles.update", "high", "consequence", "resource-target", "encoder_profiles.update"),
  row("RES-03", "3A", "delete", "/profiles/encoder/{id}", "/profiles/encoder", "DELETE", "encoder_profiles.delete", "high", "consequence", "resource-target", "encoder_profiles.delete"),
  row("RES-04", "3A", "create", "/discord/configs", "/discord/configs", "POST", "discord_configs.create", "high", "consequence", "resource-action", "discord_configs.create", true),
  row("RES-05", "3A", "update", "/discord/configs/{id}", "/discord/configs", "PUT", "discord_configs.update", "high", "consequence", "resource-target", "discord_configs.update", true),
  row("RES-06", "3A", "delete", "/discord/configs/{id}", "/discord/configs", "DELETE", "discord_configs.delete", "high", "consequence", "resource-target", "discord_configs.delete"),
  row("RES-07", "3A", "create", "/youtube/outputs", "/youtube/outputs", "POST", "youtube_outputs.create", "high", "consequence", "resource-action", "youtube_outputs.create", true),
  row("RES-08", "3A", "update", "/youtube/outputs/{id}", "/youtube/outputs", "PUT", "youtube_outputs.update", "high", "consequence", "resource-target", "youtube_outputs.update", true),
  row("RES-09", "3A", "delete", "/youtube/outputs/{id}", "/youtube/outputs", "DELETE", "youtube_outputs.delete", "high", "consequence", "resource-target", "youtube_outputs.delete"),
  row("RES-10", "3A", "create", "/profiles/caption", "/profiles/caption", "POST", "caption_profiles.create", "guarded", "consequence", "resource-action", "caption_profiles.create"),
  row("RES-11", "3A", "update", "/profiles/caption/{id}", "/profiles/caption", "PUT", "caption_profiles.update", "high", "consequence", "resource-target", "caption_profiles.update"),
  row("RES-12", "3A", "delete", "/profiles/caption/{id}", "/profiles/caption", "DELETE", "caption_profiles.delete", "high", "consequence", "resource-target", "caption_profiles.delete"),
  row("RES-13", "3A", "secret", "/secrets/deepgram_api_key", "/secrets/status", "PUT", "secrets.update", "critical", "typed-fixed", "resource-action", "secrets.update", true, "deepgram_api_key"),
  row("RES-14", "3A", "create", "/profiles/overlay", "/profiles/overlay", "POST", "overlay_profiles.create", "guarded", "consequence", "resource-action", "overlay_profiles.create"),
  row("RES-15", "3A", "update", "/profiles/overlay/{id}", "/profiles/overlay", "PUT", "overlay_profiles.update", "high", "consequence", "resource-target", "overlay_profiles.update", true),
  row("RES-16", "3A", "delete", "/profiles/overlay/{id}", "/profiles/overlay", "DELETE", "overlay_profiles.delete", "high", "consequence", "resource-target", "overlay_profiles.delete"),
  row("RES-17", "3A", "create", "/profiles/archive", "/profiles/archive", "POST", "archive_profiles.create", "guarded", "consequence", "resource-action", "archive_profiles.create"),
  row("RES-18", "3A", "update", "/profiles/archive/{id}", "/profiles/archive", "PUT", "archive_profiles.update", "high", "consequence", "resource-target", "archive_profiles.update"),
  row("RES-19", "3A", "delete", "/profiles/archive/{id}", "/profiles/archive", "DELETE", "archive_profiles.delete", "high", "consequence", "resource-target", "archive_profiles.delete"),
  row("RES-20", "3A", "create", "/archive/destinations", "/archive/destinations", "POST", "integrations.create", "high", "consequence", "resource-action", "integrations.drive_destination.create", true),
  row("RES-21", "3A", "update", "/archive/destinations/{id}", "/archive/destinations", "PUT", "integrations.update", "high", "consequence", "resource-target", "integrations.drive_destination.update", true),
  row("RES-22", "3A", "delete", "/archive/destinations/{id}", "/archive/destinations", "DELETE", "integrations.delete", "high", "consequence", "resource-target", "integrations.drive_destination.delete"),
  row("RES-23", "3B", "create", "/integrations/oauth-providers", "/integrations/oauth-providers", "POST", "integrations.create", "high", "consequence", "resource-action", "integrations.oauth_provider.create", true),
  row("RES-24", "3B", "update", "/integrations/oauth-providers/{id}", "/integrations/oauth-providers", "PUT", "integrations.update", "critical", "typed-label", "resource-target", "integrations.oauth_provider.update", true),
  row("RES-25", "3B", "delete", "/integrations/oauth-providers/{id}", "/integrations/oauth-providers", "DELETE", "integrations.delete", "critical", "typed-label", "resource-target", "integrations.oauth_provider.delete"),
  row("RES-26", "3B", "connect", "/integrations/oauth-accounts/start", "/integrations/oauth-accounts", "POST", "integrations.create", "high", "consequence", "session-flow", "integrations.oauth_account.connect", true),
  row("RES-27", "3B", "relink", "/integrations/oauth-accounts/start", "/integrations/oauth-accounts", "POST", "integrations.update", "high", "consequence", "resource-target", "integrations.oauth_account.relink", true),
  row("RES-28", "3B", "update", "/integrations/oauth-accounts/{id}", "/integrations/oauth-accounts", "PUT", "integrations.update", "guarded", "consequence", "resource-target", "integrations.oauth_account.update"),
  row("RES-29", "3B", "delete", "/integrations/oauth-accounts/{id}", "/integrations/oauth-accounts", "DELETE", "integrations.delete", "high", "consequence", "resource-target", "integrations.oauth_account.delete"),
  row("RES-30", "3A", "create", "/users", "/users", "POST", "users.create", "high", "consequence", "resource-action", "users.create", true),
  row("RES-31", "3A", "update", "/users/{id}", "/users", "PUT", "users.update", "high", "consequence", "resource-target", "users.update", true),
  row("RES-32", "3A", "delete", "/users/{id}", "/users", "DELETE", "users.delete", "critical", "typed-label", "resource-target", "users.delete"),
  row("RES-33", "3A", "create", "/roles", "/roles", "POST", "roles.create", "high", "consequence", "resource-action", "roles.create"),
  row("RES-34", "3A", "update", "/roles/{id}", "/roles", "PUT", "roles.update", "critical", "typed-label", "resource-target", "roles.update"),
  row("RES-35", "3A", "delete", "/roles/{id}", "/roles", "DELETE", "roles.delete", "critical", "typed-label", "resource-target", "roles.delete"),
  row("RES-36", "3A", "create", "/observability/notification-channels", "/observability/notification-channels", "POST", "notification_channels.create", "guarded", "consequence", "resource-action", "notification_channels.create", true),
  row("RES-37", "3A", "update", "/observability/notification-channels/{id}", "/observability/notification-channels", "PUT", "notification_channels.update", "high", "consequence", "resource-target", "notification_channels.update", true),
  row("RES-38", "3A", "delete", "/observability/notification-channels/{id}", "/observability/notification-channels", "DELETE", "notification_channels.delete", "high", "consequence", "resource-target", "notification_channels.delete"),
  row("RES-39", "3A", "test", "/observability/notification-channels/{id}/test", "/observability/notification-channels", "POST", "notification_channels.test", "guarded", "consequence", "resource-target", "notification_channels.test"),
  row("RES-40", "3B", "update", "/security/settings", "/security/settings", "PUT", "system_settings.update", "critical", "typed-fixed", "resource-action", "security.settings.update", false, "SECURITY POLICY"),
]);

const templates = new Map(resourceActionDescriptors.map((value) => [value.id, value]));

export function resourceActionTemplate(id: ResourceActionID) {
  return templates.get(id);
}

export function resourceActionID(path: string, operation: ResourceActionOperation): ResourceActionID | undefined {
  return resourceActionDescriptors.find((value) => value.sourcePath === path && value.operation === operation)?.id;
}

export function buildResourceActionPermissionRequirement(intent: ResourceActionIntent): PermissionRequirement {
  const base = resourceActionTemplate(intent.id)?.basePermission;
  if (!base) return Object.freeze({ kind: "all", permissions: Object.freeze(["__invalid_resource_action__"] as const) });
  const permissions = [base];
  if ((intent.id === "RES-23" || intent.id === "RES-24") && nonEmptyArray(intent.payload?.default_role_ids)) {
    permissions.push("roles.assign");
  }
  if (intent.id === "RES-30" && nonEmptyArray(intent.payload?.role_ids)) permissions.push("roles.assign");
  if (intent.id === "RES-31" && hasOwn(intent.payload, "role_ids")) permissions.push("roles.assign");
  return Object.freeze({
    kind: "all" as const,
    permissions: Object.freeze(permissions) as readonly [string, ...string[]],
  });
}

export function buildResourceActionDescriptor(intent: ResourceActionIntent): ActionDescriptor | undefined {
  const template = resourceActionTemplate(intent.id);
  const target = resourceActionTarget(intent);
  if (!template || !target || !resourceActionRequest(intent)) return undefined;
  return Object.freeze({
    id: intent.id,
    labelKey: "actions" as const,
    risk: template.risk,
    target: Object.freeze(target),
    permissions: buildResourceActionPermissionRequirement(intent),
    applicability: Object.freeze({
      ruleIds: Object.freeze([`resource-${intent.id.toLowerCase()}-state`]),
      requiredSections: Object.freeze(["auth", template.sourcePath]),
    }),
    confirmation: template.confirmation === "typed-fixed"
      ? Object.freeze({
          mode: "typed-target" as const,
          consequenceKey: "dangerousNotice" as const,
          typedToken: Object.freeze({ kind: "fixed-ascii" as const, value: template.fixedToken || "" }),
          requireSubmitRevalidation: true as const,
        })
      : template.confirmation === "typed-label"
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
    duplicate: Object.freeze({ scope: template.duplicateScope, whilePending: "block" as const }),
    retry: Object.freeze({ kind: "never" as const }),
    audit: Object.freeze({
      action: template.auditAction,
      labelKey: "actions" as const,
      safeReferenceFieldIds: Object.freeze(["publicLabel"]),
    }),
    stateIndependent: false,
    revalidation: Object.freeze({
      kind: "safe-fingerprint" as const,
      fieldIds: Object.freeze(["id", "revision", "updatedAt", "status"]),
    }),
  });
}

export function resourceActionRequest(intent: ResourceActionIntent): ResourceActionRequest | undefined {
  const template = resourceActionTemplate(intent.id);
  if (!template) return undefined;
  const id = resourceRowID(intent.row);
  if (template.route.includes("{id}") && !id) return undefined;
  if (template.method !== "DELETE" && template.operation !== "test" && !intent.payload) return undefined;
  const path = template.route.replace("{id}", encodeURIComponent(id));
  return Object.freeze({
    id: intent.id,
    method: template.method,
    path,
    ...(template.method === "DELETE" || template.operation === "test" ? {} : { body: intent.payload }),
  });
}

export function resourceActionScope(intent: ResourceActionIntent): string | undefined {
  const template = resourceActionTemplate(intent.id);
  const target = resourceActionTarget(intent);
  if (!template || !target || !resourceActionRequest(intent)) return undefined;
  if (template.duplicateScope === "session-flow") return "resource:oauth-connect:session";
  if (template.duplicateScope === "resource-action") return `resource:${template.sourcePath}:${intent.id}`;
  return `resource:${template.sourcePath}:${target.resourceId}`;
}

export function resourceActionApplicable(intent: ResourceActionIntent) {
  const template = resourceActionTemplate(intent.id);
  if (!template || !resourceActionRequest(intent)) return false;
  if (!resourceActionTarget(intent)) return false;
  if ((intent.id === "RES-20" || intent.id === "RES-21") && !nonEmptyString(intent.payload?.oauth_account_id)) return false;
  if ((intent.id === "RES-26" || intent.id === "RES-27") && !nonEmptyString(intent.payload?.provider_id)) return false;
  if (template.operation === "create" || template.operation === "update") return Boolean(intent.payload);
  return true;
}

export function resourceActionSourcePath(intent: ResourceActionIntent) {
  return resourceActionTemplate(intent.id)?.sourcePath;
}

export function resourceActionPublicLabel(intent: ResourceActionIntent) {
  if (intent.id === "RES-13") return "Deepgram API key";
  if (intent.id === "RES-40") return "Security policy";
  const direct = intent.publicLabel?.trim();
  if (direct) return direct;
  const source = intent.row || intent.payload || {};
  if (intent.id === "RES-32" || intent.id === "RES-30" || intent.id === "RES-31") return stringValue(source.username);
  return firstString(source.name, source.display_name, source.account_label, source.provider_type, source.id);
}

export function resourceActionTarget(intent: ResourceActionIntent) {
  const template = resourceActionTemplate(intent.id);
  if (!template) return undefined;
  const publicLabel = resourceActionPublicLabel(intent);
  if (!safePublicLabel(publicLabel)) return undefined;
  const resourceId = resourceRowID(intent.row) || (template.duplicateScope === "session-flow" ? "oauth-connect" : `new:${template.sourcePath}`);
  return { resourceType: "generic-resource", resourceId, publicLabel };
}

export function resourceRowID(value: ResourceActionRow | undefined) {
  return firstString(value?.id, value?.service_id);
}

function nonEmptyArray(value: unknown) {
  return Array.isArray(value) && value.length > 0;
}

function nonEmptyString(value: unknown) {
  return typeof value === "string" && value.trim().length > 0;
}

function hasOwn(value: unknown, key: string) {
  return typeof value === "object" && value !== null && Object.prototype.hasOwnProperty.call(value, key);
}

function firstString(...values: unknown[]) {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
    if (typeof value === "number" && Number.isFinite(value)) return String(value);
  }
  return "";
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function safePublicLabel(value: string) {
  return value.length > 0
    && value.length <= 128
    && value.trim() === value
    && !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value)
    && !/^[^\s@]+@[^\s@]+$/.test(value)
    && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value);
}
