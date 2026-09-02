import type { ActionDescriptor, ActionRisk } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";

export type UpdaterActionID = "UPD-01" | "UPD-02" | "UPD-03" | "UPD-04" | "UPD-05" | "UPD-06" | "UPD-07" | "UPD-08" | "UPD-09" | "UPD-10";

export type UpdaterActionDefinition = Readonly<{
  id: UpdaterActionID;
  method: "POST" | "PUT" | "SEQUENTIAL_POST";
  route: string;
  risk: ActionRisk;
  confirmation: "consequence" | "typed-target" | "fixed-phrase";
  auditAction: string;
  fixedPhrase?: string;
}>;

const define = (value: UpdaterActionDefinition) => Object.freeze(value);
export const updaterActionDefinitions: readonly UpdaterActionDefinition[] = Object.freeze([
  define({ id: "UPD-01", method: "POST", route: "/system-updates", risk: "high", confirmation: "consequence", auditAction: "system_updates.create" }),
  define({ id: "UPD-02", method: "POST", route: "/system-updates", risk: "critical", confirmation: "typed-target", auditAction: "system_updates.create" }),
  define({ id: "UPD-03", method: "SEQUENTIAL_POST", route: "/system-updates", risk: "critical", confirmation: "fixed-phrase", fixedPhrase: "UPDATE BATCH", auditAction: "system_updates.create" }),
  define({ id: "UPD-04", method: "POST", route: "/system-updates/{id}/cancel", risk: "high", confirmation: "consequence", auditAction: "system_updates.cancel" }),
  define({ id: "UPD-05", method: "POST", route: "/system-updates", risk: "critical", confirmation: "typed-target", auditAction: "system_updates.create" }),
  define({ id: "UPD-06", method: "POST", route: "/system-updates/updaters/{id}/pull-ownership/activate", risk: "critical", confirmation: "typed-target", auditAction: "system_updates.pull_ownership.activate" }),
  define({ id: "UPD-07", method: "POST", route: "/system-updates/updaters/{id}/pull-ownership/deactivate", risk: "critical", confirmation: "fixed-phrase", fixedPhrase: "BRIDGE ROLLBACK", auditAction: "system_updates.pull_ownership.deactivate" }),
  define({ id: "UPD-08", method: "PUT", route: "/system-updates/updaters/{id}/settings", risk: "critical", confirmation: "typed-target", auditAction: "system_updates.updater_policy.save" }),
  define({ id: "UPD-09", method: "POST", route: "/system-updates/updaters/{id}/bootstrap-jobs", risk: "critical", confirmation: "typed-target", auditAction: "system_updates.bootstrap.create" }),
  define({ id: "UPD-10", method: "POST", route: "/system-updates/updaters/{id}/bootstrap-jobs", risk: "critical", confirmation: "fixed-phrase", fixedPhrase: "BOOTSTRAP HOSTS", auditAction: "system_updates.bootstrap.create" }),
]);

const definitions = new Map(updaterActionDefinitions.map((entry) => [entry.id, entry]));

export type UpdaterActionIntent = Readonly<{
  id: UpdaterActionID;
  resourceId: string;
  publicLabel?: string;
  authorityFingerprint: string;
}>;

export type UpdaterActionAuthority = Readonly<{
  permission: "allowed" | "denied" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  applicability: "applicable" | "not-applicable" | "unknown";
  authorityFingerprint: string;
}>;

export type UpdaterActionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "conflict"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown"; nextAction: "lookup-operation" }>
  | Readonly<{ kind: "blocked"; reason: "invalid-intent" | "confirmation-required" | "typed-target-mismatch" | "duplicate" | "reconciliation-required" | "not-allowed" | "freshness-unavailable" | "not-applicable" | "authority-changed" }>;

export function buildUpdaterActionDescriptor(intent: UpdaterActionIntent): ActionDescriptor | undefined {
  const entry = definitions.get(intent.id);
  if (!entry || !safeIdentity(intent.resourceId) || !safeFingerprint(intent.authorityFingerprint)) return undefined;
  const fixed = entry.confirmation === "fixed-phrase";
  const typed = entry.confirmation === "typed-target";
  if (typed && !safePublicLabel(intent.publicLabel)) return undefined;
  return Object.freeze({
    id: entry.id,
    labelKey: "action",
    risk: entry.risk,
    target: Object.freeze({
      resourceType: "system-update",
      resourceId: intent.resourceId,
      ...(typed ? { publicLabel: intent.publicLabel } : {}),
    }),
    permissions: Object.freeze({
      kind: "all" as const,
      permissions: Object.freeze(["system_updates.execute"] as const),
    }),
    applicability: Object.freeze({ ruleIds: Object.freeze([`updater-${entry.id.toLowerCase()}-authority`]), requiredSections: Object.freeze(["system-updates", "updater-status"]) }),
    confirmation: fixed
      ? Object.freeze({ mode: "typed-target" as const, consequenceKey: "dangerousNotice", typedToken: Object.freeze({ kind: "fixed-ascii" as const, value: entry.fixedPhrase! }), requireSubmitRevalidation: true as const })
      : typed
        ? Object.freeze({ mode: "typed-target" as const, consequenceKey: "dangerousNotice", typedToken: Object.freeze({ kind: "target-label" as const }), requireSubmitRevalidation: true as const })
        : Object.freeze({ mode: "consequence" as const, consequenceKey: "dangerousNotice", requireSubmitRevalidation: true as const }),
    duplicate: Object.freeze({ scope: intent.id === "UPD-03" || intent.id === "UPD-10" ? "fleet" as const : "resource-action" as const, whilePending: "block" as const }),
    retry: Object.freeze({ kind: "lookup-only" as const }),
    audit: Object.freeze({ action: entry.auditAction, labelKey: "action", safeReferenceFieldIds: Object.freeze(typed ? ["publicLabel"] : []) }),
    stateIndependent: false,
    revalidation: Object.freeze({ kind: "safe-fingerprint" as const, fieldIds: Object.freeze(["authorityFingerprint"]) }),
  });
}

export function createUpdaterActionController() {
  const pending = new Set<string>();
  const unresolved = new Set<string>();
  return Object.freeze({
    async execute(
      intent: UpdaterActionIntent,
      confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
      refresh: () => Promise<UpdaterActionAuthority>,
      handler: () => Promise<unknown>,
    ): Promise<UpdaterActionResult> {
      const entry = definitions.get(intent.id);
      const descriptor = buildUpdaterActionDescriptor(intent);
      if (!entry || !descriptor || typeof refresh !== "function" || typeof handler !== "function") return blocked("invalid-intent");
      if (!confirmation.confirmed) return blocked("confirmation-required");
      const required = entry.confirmation === "fixed-phrase" ? entry.fixedPhrase : entry.confirmation === "typed-target" ? intent.publicLabel : undefined;
      if (required !== undefined && confirmation.typedValue !== required) return blocked("typed-target-mismatch");
      const scope = updaterActionScope(intent);
      if (unresolved.has(scope)) return blocked("reconciliation-required");
      if (pending.has(scope)) return blocked("duplicate");
      pending.add(scope);
      try {
        let authority: UpdaterActionAuthority;
        try {
          authority = await refresh();
        } catch {
          return blocked("freshness-unavailable");
        }
        if (authority.permission !== "allowed") return blocked("not-allowed");
        if (authority.freshness !== "fresh") return blocked("freshness-unavailable");
        if (authority.applicability !== "applicable") return blocked("not-applicable");
        if (authority.authorityFingerprint !== intent.authorityFingerprint) return blocked("authority-changed");
        try {
          return Object.freeze({ kind: "succeeded", value: await handler() });
        } catch (error) {
          const adapted = adaptAPIError(error);
          if (["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)) {
            unresolved.add(scope);
            return Object.freeze({ kind: "outcome_unknown", nextAction: "lookup-operation" });
          }
          if (adapted.kind === "conflict") return Object.freeze({ kind: "conflict", error: adapted });
          return Object.freeze({ kind: "failed", error: adapted });
        }
      } finally {
        pending.delete(scope);
      }
    },
    reconcile(intent: UpdaterActionIntent) {
      unresolved.delete(updaterActionScope(intent));
    },
  });
}

export type UpdaterActionController = ReturnType<typeof createUpdaterActionController>;

export function updaterActionTypedValue(intent: UpdaterActionIntent) {
  const entry = definitions.get(intent.id);
  return entry?.confirmation === "fixed-phrase" ? entry.fixedPhrase : entry?.confirmation === "typed-target" ? intent.publicLabel : undefined;
}

export type UpdaterAuthorityFingerprintPart = string | number | boolean | null | undefined;

export function updaterAuthorityFingerprint(parts: readonly UpdaterAuthorityFingerprintPart[]) {
  let primary = 0x811c9dc5;
  let secondary = 0x9e3779b9;
  const input = parts.map((part) => part === undefined ? "u:" : part === null ? "n:" : `${typeof part}:${String(part)}`).join("\u001f");
  for (let index = 0; index < input.length; index += 1) {
    const code = input.charCodeAt(index);
    primary = Math.imul(primary ^ code, 0x01000193) >>> 0;
    secondary = Math.imul(secondary ^ (code + index), 0x85ebca6b) >>> 0;
  }
  return `uaf1-${primary.toString(16).padStart(8, "0")}${secondary.toString(16).padStart(8, "0")}-${input.length}`;
}

function updaterActionScope(intent: UpdaterActionIntent) {
  return intent.id === "UPD-03" || intent.id === "UPD-10" ? `updater:fleet:${intent.id}` : `updater:${intent.resourceId}:${intent.id}`;
}

function blocked<const Reason extends Extract<UpdaterActionResult, { kind: "blocked" }>["reason"]>(reason: Reason) {
  return Object.freeze({ kind: "blocked", reason } as const);
}

function safeIdentity(value: string) {
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,191}$/.test(value);
}

function safeFingerprint(value: string) {
  return typeof value === "string" && value.length > 0 && value.length <= 16_384 && !/[\u0000\r\n]/u.test(value);
}

function safePublicLabel(value: string | undefined): value is string {
  return typeof value === "string"
    && value.length > 0
    && [...value].length <= 128
    && value.trim() === value
    && !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value)
    && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value)
    && !/^[^\s@]+@[^\s@]+$/.test(value);
}
