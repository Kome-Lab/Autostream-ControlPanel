import type { ActionDescriptor, ActionEvaluation, ActionRisk } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import { evaluateActionPermission, type PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";

export type AppSettingsActionID = "APP-01" | "APP-02";
export type AppSettingsActionIntent = Readonly<{
  id: AppSettingsActionID;
  payload: Readonly<Record<string, unknown>>;
}>;
export type AppSettingsActionRequest = Readonly<{
  id: AppSettingsActionID;
  method: "PUT" | "POST";
  path: string;
  body: Readonly<Record<string, unknown>>;
}>;
export type AppSettingsActionDefinition = Readonly<{
  id: AppSettingsActionID;
  method: AppSettingsActionRequest["method"];
  path: string;
  permission: "system_settings.update";
  risk: ActionRisk;
  auditAction: string;
}>;

export const appSettingsActionDefinitions: readonly AppSettingsActionDefinition[] = Object.freeze([
  Object.freeze({ id: "APP-01", method: "PUT", path: "/settings/app", permission: "system_settings.update", risk: "high", auditAction: "app.settings.update" }),
  Object.freeze({ id: "APP-02", method: "POST", path: "/settings/app/test-email", permission: "system_settings.update", risk: "guarded", auditAction: "app.settings.test_email" }),
]);

const definitions = new Map(appSettingsActionDefinitions.map((value) => [value.id, value]));

export function buildAppSettingsActionDescriptor(intent: AppSettingsActionIntent): ActionDescriptor | undefined {
  const definition = definitions.get(intent.id);
  if (!definition || !validPayload(intent)) return undefined;
  const publicLabel = intent.id === "APP-01" ? "Application settings" : "Test email";
  return Object.freeze({
    id: intent.id,
    labelKey: "appSettings" as const,
    risk: definition.risk,
    target: Object.freeze({ resourceType: "application-settings", resourceId: "application-settings", publicLabel }),
    permissions: Object.freeze({ kind: "all" as const, permissions: Object.freeze([definition.permission] as const) }),
    applicability: Object.freeze({ ruleIds: Object.freeze([`app-settings-${intent.id.toLowerCase()}`]), requiredSections: Object.freeze(["auth", "settings"]) }),
    confirmation: Object.freeze({ mode: "consequence" as const, consequenceKey: "dangerousNotice" as const, requireSubmitRevalidation: true }),
    duplicate: Object.freeze({ scope: "resource-action" as const, whilePending: "block" as const }),
    retry: Object.freeze({ kind: "never" as const }),
    audit: Object.freeze({ action: definition.auditAction, labelKey: "appSettings" as const, safeReferenceFieldIds: Object.freeze(["publicLabel"]) }),
    stateIndependent: false,
    revalidation: Object.freeze({ kind: "safe-fingerprint" as const, fieldIds: Object.freeze(["updatedAt", "configuration"]) }),
  });
}

export function appSettingsActionRequest(intent: AppSettingsActionIntent): AppSettingsActionRequest | undefined {
  const definition = definitions.get(intent.id);
  return definition && validPayload(intent)
    ? Object.freeze({ id: intent.id, method: definition.method, path: definition.path, body: intent.payload })
    : undefined;
}

export function appSettingsActionScope(intent: AppSettingsActionIntent) {
  return appSettingsActionRequest(intent) ? `app-settings:${intent.id}` : undefined;
}

export type AppSettingsActionState = Readonly<{
  kind: "ready" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  fingerprint?: string;
}>;
export type AppSettingsActionAuthority = Readonly<{
  permissions: PermissionSnapshot;
  state: AppSettingsActionState;
}>;
export type AppSettingsActionBlockedReason = "invalid-intent" | "permission-denied" | "permission-unknown" | "state-unavailable" | "duplicate" | "confirmation-required" | "authority-changed" | "reconciliation-required" | "cancelled";
export type AppSettingsActionOpenResult =
  | Readonly<{ kind: "allowed"; intent: AppSettingsActionIntent; descriptor: ActionDescriptor; authority: string }>
  | Readonly<{ kind: "blocked"; reason: AppSettingsActionBlockedReason }>;
export type AllowedAppSettingsActionOpen = Extract<AppSettingsActionOpenResult, { kind: "allowed" }>;
export type AppSettingsActionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown"; nextAction: "inspect-audit" }>
  | Readonly<{ kind: "blocked"; reason: AppSettingsActionBlockedReason }>;

export type AppSettingsActionController = ReturnType<typeof createAppSettingsActionController>;

export function createAppSettingsActionController(dependencies: Readonly<{
  getPermissions: () => PermissionSnapshot;
  getState: () => AppSettingsActionState;
  refresh: () => Promise<AppSettingsActionAuthority>;
  mutate: (request: AppSettingsActionRequest & Readonly<{ signal: AbortSignal }>) => Promise<unknown>;
}>) {
  const active = new Map<string, AbortController>();
  const unresolved = new Set<string>();

  const evaluateBase = (intent: AppSettingsActionIntent, authority?: AppSettingsActionAuthority): ActionEvaluation => {
    const descriptor = buildAppSettingsActionDescriptor(intent);
    if (!descriptor) return notApplicable();
    const state = authority?.state ?? dependencies.getState();
    const constraint = state.kind === "ready" && state.freshness === "fresh" && state.fingerprint
      ? { kind: "ready" as const }
      : { kind: "unknown" as const, reasonKey: "actionStateBlocked" as const };
    return evaluateActionPermission({
      requirement: descriptor.permissions ?? { kind: "none" },
      snapshot: authority?.permissions ?? dependencies.getPermissions(),
      disclosure: "visible-denied",
      constraint,
    });
  };

  const evaluate = (intent: AppSettingsActionIntent) => {
    const value = evaluateBase(intent);
    const scope = appSettingsActionScope(intent);
    if (!scope || value.availability.kind !== "allowed") return value;
    if (active.has(scope)) return withAvailability(value, "pending", "actionAlreadyPending");
    if (unresolved.has(scope)) return withAvailability(value, "blocked", "confirmationRefreshRequired");
    return value;
  };

  const open = async (intent: AppSettingsActionIntent): Promise<AppSettingsActionOpenResult> => {
    const scope = appSettingsActionScope(intent);
    if (!scope) return blocked("invalid-intent");
    if (active.has(scope)) return blocked("duplicate");
    if (unresolved.has(scope)) return blocked("reconciliation-required");
    let authority: AppSettingsActionAuthority;
    try {
      authority = await dependencies.refresh();
    } catch {
      return blocked("state-unavailable");
    }
    const reason = evaluationReason(evaluateBase(intent, authority));
    if (reason) return blocked(reason);
    const descriptor = buildAppSettingsActionDescriptor(intent);
    if (!descriptor || authority.state.kind !== "ready" || authority.state.freshness !== "fresh" || !authority.state.fingerprint) return blocked("state-unavailable");
    return Object.freeze({ kind: "allowed", intent, descriptor, authority: authority.state.fingerprint });
  };

  const submit = async (
    opened: AllowedAppSettingsActionOpen,
    confirmation: Readonly<{ confirmed: boolean }>,
    lifecycle?: Readonly<{ onDispatch?: () => void }>,
  ): Promise<AppSettingsActionResult> => {
    const scope = appSettingsActionScope(opened.intent);
    if (!scope) return blocked("invalid-intent");
    if (unresolved.has(scope)) return blocked("reconciliation-required");
    if (!confirmation.confirmed) return blocked("confirmation-required");
    if (active.has(scope)) return blocked("duplicate");
    const abort = new AbortController();
    active.set(scope, abort);
    try {
      let authority: AppSettingsActionAuthority;
      try {
        authority = await dependencies.refresh();
      } catch {
        return blocked("state-unavailable");
      }
      const reason = evaluationReason(evaluateBase(opened.intent, authority));
      if (reason) return blocked(reason);
      if (!authority.state.fingerprint || authority.state.fingerprint !== opened.authority) return blocked("authority-changed");
      const request = appSettingsActionRequest(opened.intent);
      if (!request || abort.signal.aborted || active.get(scope) !== abort) return blocked("cancelled");
      try {
        const execution = dependencies.mutate(Object.freeze({ ...request, signal: abort.signal }));
        try {
          lifecycle?.onDispatch?.();
        } catch {
          // A UI cleanup callback must not alter the mutation outcome after dispatch.
        }
        const value = await execution;
        if (abort.signal.aborted || active.get(scope) !== abort) return blocked("cancelled");
        return Object.freeze({ kind: "succeeded", value });
      } catch (error) {
        if (abort.signal.aborted || active.get(scope) !== abort) return blocked("cancelled");
        unresolved.add(scope);
        const adapted = adaptAPIError(error);
        return ["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)
          ? Object.freeze({ kind: "outcome_unknown", nextAction: "inspect-audit" as const })
          : Object.freeze({ kind: "failed", error: adapted });
      }
    } finally {
      if (active.get(scope) === abort) active.delete(scope);
    }
  };

  return Object.freeze({
    evaluate,
    open,
    submit,
    cancel: (intent?: AppSettingsActionIntent) => {
      const scopes = intent ? [appSettingsActionScope(intent)].filter((value): value is string => Boolean(value)) : [...active.keys()];
      for (const scope of scopes) {
        active.get(scope)?.abort();
        active.delete(scope);
      }
    },
    reconcile: (intent?: AppSettingsActionIntent) => {
      if (!intent) unresolved.clear();
      else {
        const scope = appSettingsActionScope(intent);
        if (scope) unresolved.delete(scope);
      }
    },
  });
}

function validPayload(intent: AppSettingsActionIntent) {
  if (!intent.payload || typeof intent.payload !== "object" || Array.isArray(intent.payload)) return false;
  if (intent.id === "APP-01") return typeof intent.payload.app_name === "string";
  return typeof intent.payload.to === "string" && intent.payload.to.trim().length > 0;
}

function evaluationReason(value: ActionEvaluation): AppSettingsActionBlockedReason | undefined {
  if (value.visibility.kind === "hidden") return "invalid-intent";
  if (value.availability.kind === "allowed") return undefined;
  if (value.availability.kind === "denied") return "permission-denied";
  if (value.availability.kind === "unknown") return value.availability.reasonKey === "actionPermissionUnknown" ? "permission-unknown" : "state-unavailable";
  if (value.availability.kind === "pending") return "duplicate";
  return "state-unavailable";
}

function notApplicable(): ActionEvaluation {
  return Object.freeze({ visibility: Object.freeze({ kind: "hidden" as const, reason: "not-applicable" as const }), availability: Object.freeze({ kind: "blocked" as const, reasonKey: "actionStateBlocked" as const }) });
}

function withAvailability(value: ActionEvaluation, kind: "pending" | "blocked", reasonKey: "actionAlreadyPending" | "confirmationRefreshRequired"): ActionEvaluation {
  return Object.freeze({ visibility: value.visibility, availability: Object.freeze({ kind, reasonKey }) });
}

function blocked(reason: AppSettingsActionBlockedReason): AppSettingsActionResult & AppSettingsActionOpenResult {
  return Object.freeze({ kind: "blocked", reason });
}
