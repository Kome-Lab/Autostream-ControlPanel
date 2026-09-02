import type { ActionDescriptor, ActionRisk } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";

export type AccountActionID =
  | "AUTH-01" | "AUTH-02" | "AUTH-03" | "AUTH-04" | "AUTH-05" | "AUTH-06" | "AUTH-07"
  | "AUTH-08" | "AUTH-09" | "AUTH-10" | "AUTH-11" | "AUTH-12" | "AUTH-13" | "AUTH-14"
  | "AUTH-15" | "AUTH-16" | "AUTH-17" | "AUTH-18" | "AUTH-19" | "AUTH-20" | "AUTH-21";

export type AccountActionDefinition = Readonly<{
  id: AccountActionID;
  method: "POST" | "PUT" | "DELETE" | "PUT_BINARY";
  route: string;
  risk: ActionRisk;
  confirmation: "none" | "consequence" | "typed-target" | "flow-continuation";
  authority: "setup" | "anonymous" | "challenge" | "one-time-token" | "authenticated";
  auditAction: string;
  oneTimeOutput?: boolean;
  inputSecret?: boolean;
  redirectCapability?: boolean;
  webAuthnCapability?: boolean;
  flowContinuationOf?: AccountActionID;
}>;

const definition = (value: AccountActionDefinition) => Object.freeze(value);
export const accountActionDefinitions: readonly AccountActionDefinition[] = Object.freeze([
  definition({ id: "AUTH-01", method: "POST", route: "/setup/first-admin", risk: "critical", confirmation: "typed-target", authority: "setup", auditAction: "setup.first_admin", inputSecret: true }),
  definition({ id: "AUTH-02", method: "POST", route: "/auth/login", risk: "routine", confirmation: "none", authority: "anonymous", auditAction: "auth.login", inputSecret: true }),
  definition({ id: "AUTH-03", method: "POST", route: "/auth/mfa/verify", risk: "routine", confirmation: "flow-continuation", authority: "challenge", auditAction: "mfa.verify", inputSecret: true, flowContinuationOf: "AUTH-02" }),
  definition({ id: "AUTH-04", method: "POST", route: "/auth/oauth/{provider}/start", risk: "routine", confirmation: "none", authority: "anonymous", auditAction: "auth.oauth.start", redirectCapability: true }),
  definition({ id: "AUTH-05", method: "POST", route: "/auth/passkeys/login/start", risk: "routine", confirmation: "none", authority: "anonymous", auditAction: "auth.passkey.login.start", webAuthnCapability: true }),
  definition({ id: "AUTH-06", method: "POST", route: "/auth/passkeys/login/finish", risk: "routine", confirmation: "flow-continuation", authority: "challenge", auditAction: "auth.passkey.login.finish", inputSecret: true, flowContinuationOf: "AUTH-05" }),
  definition({ id: "AUTH-07", method: "POST", route: "/auth/email/confirm", risk: "high", confirmation: "consequence", authority: "one-time-token", auditAction: "auth.email.confirm", inputSecret: true }),
  definition({ id: "AUTH-08", method: "POST", route: "/auth/logout", risk: "routine", confirmation: "none", authority: "authenticated", auditAction: "auth.logout" }),
  definition({ id: "AUTH-09", method: "PUT_BINARY", route: "/auth/avatar", risk: "routine", confirmation: "none", authority: "authenticated", auditAction: "auth.avatar.update", inputSecret: true }),
  definition({ id: "AUTH-10", method: "DELETE", route: "/auth/avatar", risk: "routine", confirmation: "none", authority: "authenticated", auditAction: "auth.avatar.delete" }),
  definition({ id: "AUTH-11", method: "POST", route: "/auth/change-password", risk: "high", confirmation: "consequence", authority: "authenticated", auditAction: "auth.change_password", inputSecret: true }),
  definition({ id: "AUTH-12", method: "PUT", route: "/auth/email", risk: "high", confirmation: "consequence", authority: "authenticated", auditAction: "auth.email.change_request" }),
  definition({ id: "AUTH-13", method: "POST", route: "/auth/oauth-links/{provider}/start", risk: "high", confirmation: "consequence", authority: "authenticated", auditAction: "auth.oauth_link.create", redirectCapability: true }),
  definition({ id: "AUTH-14", method: "DELETE", route: "/auth/oauth-links/{link}", risk: "high", confirmation: "consequence", authority: "authenticated", auditAction: "auth.oauth_link.delete" }),
  definition({ id: "AUTH-15", method: "POST", route: "/auth/mfa/enroll", risk: "critical", confirmation: "typed-target", authority: "authenticated", auditAction: "mfa.enroll", oneTimeOutput: true }),
  definition({ id: "AUTH-16", method: "POST", route: "/auth/mfa/verify", risk: "high", confirmation: "flow-continuation", authority: "authenticated", auditAction: "mfa.verify", inputSecret: true, flowContinuationOf: "AUTH-15" }),
  definition({ id: "AUTH-17", method: "POST", route: "/auth/mfa/disable", risk: "critical", confirmation: "typed-target", authority: "authenticated", auditAction: "mfa.disable", inputSecret: true }),
  definition({ id: "AUTH-18", method: "POST", route: "/auth/recovery-codes/regenerate", risk: "critical", confirmation: "typed-target", authority: "authenticated", auditAction: "mfa.recovery_codes.regenerate", inputSecret: true, oneTimeOutput: true }),
  definition({ id: "AUTH-19", method: "POST", route: "/auth/passkeys/register/start", risk: "guarded", confirmation: "consequence", authority: "authenticated", auditAction: "passkeys.registration.start", webAuthnCapability: true }),
  definition({ id: "AUTH-20", method: "POST", route: "/auth/passkeys/register/finish", risk: "high", confirmation: "flow-continuation", authority: "authenticated", auditAction: "passkeys.registration.finish", inputSecret: true, flowContinuationOf: "AUTH-19" }),
  definition({ id: "AUTH-21", method: "DELETE", route: "/auth/passkeys/{id}", risk: "high", confirmation: "consequence", authority: "authenticated", auditAction: "passkeys.delete" }),
]);

const definitions = new Map(accountActionDefinitions.map((entry) => [entry.id, entry]));

export type AccountActionIntent = Readonly<{
  id: AccountActionID;
  resourceId: string;
  publicUsername?: string;
  authorityRevision?: string;
}>;

export type AccountAuthoritySnapshot = Readonly<{
  session: "setup" | "anonymous" | "challenge" | "one-time-token" | "authenticated" | "unavailable";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  revision: string;
}>;

export type AccountActionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown"; nextAction: "inspect-audit" }>
  | Readonly<{ kind: "blocked"; reason: "invalid-intent" | "confirmation-required" | "typed-target-mismatch" | "duplicate" | "session-unavailable" | "authority-changed" | "reconciliation-required" }>;

export function buildAccountActionDescriptor(intent: AccountActionIntent): ActionDescriptor | undefined {
  const entry = definitions.get(intent.id);
  if (!entry || !safeResourceID(intent.resourceId)) return undefined;
  const typed = entry.confirmation === "typed-target";
  if (typed && !safePublicUsername(intent.publicUsername)) return undefined;
  const confirmation = typed
    ? Object.freeze({
        mode: "typed-target" as const,
        consequenceKey: "dangerousNotice",
        typedToken: Object.freeze({ kind: "target-label" as const }),
        requireSubmitRevalidation: true as const,
      })
    : entry.confirmation === "consequence"
      ? Object.freeze({ mode: "consequence" as const, consequenceKey: "dangerousNotice", requireSubmitRevalidation: entry.risk !== "routine" })
      : Object.freeze({ mode: "none" as const, requireSubmitRevalidation: false });
  const revalidation = entry.risk === "routine"
    ? Object.freeze({ kind: "none" as const })
    : Object.freeze({ kind: "revision" as const });
  return Object.freeze({
    id: entry.id,
    labelKey: "account",
    risk: entry.risk,
    target: Object.freeze({
      resourceType: "account",
      resourceId: intent.resourceId,
      ...(typed ? { publicLabel: intent.publicUsername } : {}),
    }),
    applicability: Object.freeze({ ruleIds: Object.freeze([`account-${entry.id.toLowerCase()}-flow`]), requiredSections: Object.freeze(["session"]) }),
    confirmation,
    duplicate: Object.freeze({ scope: entry.id <= "AUTH-08" ? "session-flow" as const : "resource-action" as const, whilePending: "block" as const }),
    retry: Object.freeze({ kind: "never" as const }),
    audit: Object.freeze({ action: entry.auditAction, labelKey: "action", safeReferenceFieldIds: Object.freeze(typed ? ["publicLabel"] : []) }),
    stateIndependent: false,
    revalidation,
  });
}

export function createAccountActionController(dependencies: Readonly<{
  readAuthority: (intent: AccountActionIntent) => AccountAuthoritySnapshot;
}>) {
  const pending = new Set<string>();
  const unresolved = new Set<string>();
  return Object.freeze({
    async execute(
      intent: AccountActionIntent,
      confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
      handler: () => Promise<unknown>,
    ): Promise<AccountActionResult> {
      const entry = definitions.get(intent.id);
      const descriptor = buildAccountActionDescriptor(intent);
      if (!entry || !descriptor || typeof handler !== "function") return blocked("invalid-intent");
      const confirmationReason = validateConfirmation(entry, intent, confirmation);
      if (confirmationReason) return blocked(confirmationReason);
      const scope = accountActionScope(intent, entry);
      if (unresolved.has(scope)) return blocked("reconciliation-required");
      if (pending.has(scope)) return blocked("duplicate");
      pending.add(scope);
      try {
        const authority = dependencies.readAuthority(intent);
        if (!authorityMatches(entry, authority)) return blocked("session-unavailable");
        if (intent.authorityRevision !== undefined && intent.authorityRevision !== authority.revision) return blocked("authority-changed");
        try {
          return Object.freeze({ kind: "succeeded", value: await handler() });
        } catch (error) {
          const adapted = adaptAPIError(error);
          if (["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)) {
            unresolved.add(scope);
            return Object.freeze({ kind: "outcome_unknown", nextAction: "inspect-audit" });
          }
          return Object.freeze({ kind: "failed", error: adapted });
        }
      } finally {
        pending.delete(scope);
      }
    },
    reconcile(intent: AccountActionIntent) {
      const entry = definitions.get(intent.id);
      if (entry) unresolved.delete(accountActionScope(intent, entry));
    },
  });
}

export type AccountActionController = ReturnType<typeof createAccountActionController>;

function validateConfirmation(entry: AccountActionDefinition, intent: AccountActionIntent, confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>) {
  if (entry.confirmation === "none" || entry.confirmation === "flow-continuation") return undefined;
  if (!confirmation.confirmed) return "confirmation-required" as const;
  if (entry.confirmation === "typed-target" && confirmation.typedValue !== intent.publicUsername) return "typed-target-mismatch" as const;
  return undefined;
}

function authorityMatches(entry: AccountActionDefinition, authority: AccountAuthoritySnapshot) {
  return authority.freshness === "fresh" && authority.session === entry.authority;
}

function accountActionScope(intent: AccountActionIntent, entry: AccountActionDefinition) {
  const owner = entry.flowContinuationOf ?? entry.id;
  return entry.id <= "AUTH-08" ? `account:session:${owner}` : `account:${intent.resourceId}:${owner}`;
}

function blocked<const Reason extends Extract<AccountActionResult, { kind: "blocked" }>["reason"]>(reason: Reason) {
  return Object.freeze({ kind: "blocked", reason } as const);
}

function safeResourceID(value: string) {
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);
}

function safePublicUsername(value: string | undefined): value is string {
  return typeof value === "string"
    && value.length > 0
    && value.length <= 128
    && /^[\p{L}\p{N}._ -]+$/u.test(value)
    && !value.includes("@")
    && value.trim() === value;
}
