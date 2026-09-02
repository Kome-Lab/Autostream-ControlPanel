import type { ActionDescriptor, ActionRisk } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";

export type ArchiveActionID = "ARC-01" | "ARC-02" | "ARC-03" | "ARC-04" | "ARC-05";
export type ArchiveActionDefinition = Readonly<{
  id: ArchiveActionID;
  method: "PUT" | "DELETE" | "POST" | "GET_HANDOFF";
  route: string;
  permission: "archives.delete" | "archives.download";
  risk: ActionRisk;
  confirmation: "none" | "consequence" | "typed-target";
  auditAction: string;
  oneTimeOutput?: boolean;
}>;

export const archiveActionDefinitions: readonly ArchiveActionDefinition[] = Object.freeze([
  Object.freeze({ id: "ARC-01", method: "PUT", route: "/streams/{sid}/artifacts/{aid}", permission: "archives.delete", risk: "guarded", confirmation: "consequence", auditAction: "archive.artifact.rename" }),
  Object.freeze({ id: "ARC-02", method: "DELETE", route: "/streams/{sid}/artifacts/{aid}", permission: "archives.delete", risk: "critical", confirmation: "typed-target", auditAction: "archive.artifact.delete" }),
  Object.freeze({ id: "ARC-03", method: "POST", route: "/streams/{sid}/artifacts/{aid}/shares", permission: "archives.download", risk: "high", confirmation: "consequence", auditAction: "archive.artifact.share.create", oneTimeOutput: true }),
  Object.freeze({ id: "ARC-04", method: "DELETE", route: "/streams/{sid}/artifacts/{aid}/shares/{share}", permission: "archives.delete", risk: "high", confirmation: "consequence", auditAction: "archive.artifact.share.revoke" }),
  Object.freeze({ id: "ARC-05", method: "GET_HANDOFF", route: "/streams/{sid}/artifacts/{aid}/download", permission: "archives.download", risk: "routine", confirmation: "none", auditAction: "archive.artifact.download" }),
]);
const definitions = new Map(archiveActionDefinitions.map((entry) => [entry.id, entry]));

export type ArchiveActionIntent = Readonly<{
  id: ArchiveActionID;
  streamId: string;
  artifactId: string;
  artifactLabel: string;
  shareId?: string;
}>;

export type ArchiveAuthoritySnapshot = Readonly<{
  permission: "allowed" | "denied" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  applicability: "applicable" | "not-applicable" | "unknown";
  revision: string;
}>;

export type ArchiveActionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown"; nextAction: "refresh-resource" | "inspect-audit" }>
  | Readonly<{ kind: "blocked"; reason: "invalid-intent" | "permission-denied" | "authority-unavailable" | "not-applicable" | "confirmation-required" | "typed-target-mismatch" | "duplicate" | "authority-changed" | "reconciliation-required" }>;

export type ArchiveOpenResult =
  | Readonly<{ kind: "allowed"; intent: ArchiveActionIntent; authorityRevision: string; descriptor: ActionDescriptor }>
  | Extract<ArchiveActionResult, { kind: "blocked" }>;

export function buildArchiveActionDescriptor(intent: ArchiveActionIntent): ActionDescriptor | undefined {
  const entry = definitions.get(intent.id);
  if (!entry || !safeID(intent.streamId) || !safeID(intent.artifactId) || !safeLabel(intent.artifactLabel)) return undefined;
  if (intent.id === "ARC-04" && !safeID(intent.shareId ?? "")) return undefined;
  const confirmation = entry.confirmation === "typed-target"
    ? Object.freeze({ mode: "typed-target" as const, consequenceKey: "dangerousNotice", typedToken: Object.freeze({ kind: "target-label" as const }), requireSubmitRevalidation: true as const })
    : entry.confirmation === "consequence"
      ? Object.freeze({ mode: "consequence" as const, consequenceKey: "dangerousNotice", requireSubmitRevalidation: true })
      : Object.freeze({ mode: "none" as const, requireSubmitRevalidation: false });
  return Object.freeze({
    id: entry.id,
    labelKey: "archive",
    risk: entry.risk,
    target: Object.freeze({ resourceType: "archive-artifact", resourceId: intent.artifactId, publicLabel: intent.artifactLabel }),
    permissions: Object.freeze({ kind: "all" as const, permissions: Object.freeze([entry.permission] as const) }),
    applicability: Object.freeze({ ruleIds: Object.freeze(["archive-recording-artifact"]), requiredSections: Object.freeze(["artifact"]) }),
    confirmation,
    duplicate: Object.freeze({ scope: "resource-action" as const, whilePending: "block" as const }),
    retry: Object.freeze({ kind: entry.id === "ARC-05" ? "lookup-only" as const : "never" as const }),
    audit: Object.freeze({ action: entry.auditAction, labelKey: "action", safeReferenceFieldIds: Object.freeze(["publicLabel"]) }),
    stateIndependent: entry.id === "ARC-05",
    revalidation: entry.id === "ARC-05" ? Object.freeze({ kind: "none" as const }) : Object.freeze({ kind: "revision" as const }),
  });
}

export function createArchiveActionController(dependencies: Readonly<{
  readAuthority: (intent: ArchiveActionIntent) => ArchiveAuthoritySnapshot;
}>) {
  const pending = new Set<string>();
  const unresolved = new Set<string>();
  const open = (intent: ArchiveActionIntent): ArchiveOpenResult => {
    const descriptor = buildArchiveActionDescriptor(intent);
    if (!descriptor) return blocked("invalid-intent");
    const authority = dependencies.readAuthority(intent);
    const reason = authorityReason(authority);
    return reason
      ? blocked(reason)
      : Object.freeze({ kind: "allowed", intent, authorityRevision: authority.revision, descriptor });
  };
  return Object.freeze({
    open,
    async submit(
      opened: Extract<ArchiveOpenResult, { kind: "allowed" }>,
      confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
      handler: () => Promise<unknown>,
    ): Promise<ArchiveActionResult> {
      const entry = definitions.get(opened.intent.id);
      if (!entry || typeof handler !== "function") return blocked("invalid-intent");
      if (entry.confirmation !== "none" && !confirmation.confirmed) return blocked("confirmation-required");
      if (entry.confirmation === "typed-target" && confirmation.typedValue !== opened.intent.artifactLabel) return blocked("typed-target-mismatch");
      const scope = archiveScope(opened.intent);
      if (unresolved.has(scope)) return blocked("reconciliation-required");
      if (pending.has(scope)) return blocked("duplicate");
      pending.add(scope);
      try {
        const authority = dependencies.readAuthority(opened.intent);
        const reason = authorityReason(authority);
        if (reason) return blocked(reason);
        if (authority.revision !== opened.authorityRevision) return blocked("authority-changed");
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
    downloadHandoff(intent: ArchiveActionIntent) {
      if (intent.id !== "ARC-05") return blocked("invalid-intent");
      const opened = open(intent);
      return opened.kind === "allowed"
        ? Object.freeze({ kind: "handoff-ready", message: "browser-download-handoff-started" as const })
        : opened;
    },
    reconcile(intent: ArchiveActionIntent) {
      unresolved.delete(archiveScope(intent));
    },
  });
}

function authorityReason(authority: ArchiveAuthoritySnapshot) {
  if (authority.permission === "denied") return "permission-denied" as const;
  if (authority.applicability === "not-applicable") return "not-applicable" as const;
  if (authority.permission !== "allowed" || authority.applicability !== "applicable" || authority.freshness !== "fresh" || !safeRevision(authority.revision)) return "authority-unavailable" as const;
  return undefined;
}

function archiveScope(intent: ArchiveActionIntent) {
  return `archive:${intent.streamId}:${intent.artifactId}:${intent.id}${intent.shareId ? `:${intent.shareId}` : ""}`;
}

function blocked<const Reason extends Extract<ArchiveActionResult, { kind: "blocked" }>["reason"]>(reason: Reason) {
  return Object.freeze({ kind: "blocked", reason } as const);
}

function safeID(value: string) {
  return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);
}

function safeRevision(value: string) {
  return typeof value === "string" && value.length > 0 && value.length <= 128;
}

function safeLabel(value: string) {
  return typeof value === "string" && value.length > 0 && value.length <= 128 && value.trim() === value && !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value);
}
