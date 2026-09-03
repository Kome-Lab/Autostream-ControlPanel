import { buildVisualUpdate, type StreamVisualSettings } from "@/features/streams/control-platform";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";

export type VisualActionPermissionSnapshot = Readonly<{
  kind: "ready" | "refreshing" | "unavailable";
  permissions: readonly string[];
}>;

export type VisualActionStateSnapshot = Readonly<{
  kind: "ready" | "missing" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  revision?: number;
  fingerprint?: string;
}>;

export type VisualActionResult =
  | Readonly<{ kind: "succeeded"; value: StreamVisualSettings }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown" }>
  | Readonly<{ kind: "blocked"; reason: "permission-denied" | "permission-unknown" | "state-unavailable" | "duplicate" | "authority-changed" | "reconciliation-required" }>;

export type StreamVisualActionController = ReturnType<typeof createStreamVisualActionController>;

export function createStreamVisualActionController(dependencies: Readonly<{
  getPermission: () => VisualActionPermissionSnapshot;
  getState: () => VisualActionStateSnapshot;
  mutate: (request: Record<string, unknown>) => Promise<StreamVisualSettings>;
}>) {
  let pending = false;
  let unresolved = false;

  const evaluate = (): Extract<VisualActionResult, { kind: "blocked" }> | undefined => {
    const permission = dependencies.getPermission();
    if (permission.kind !== "ready") return blocked("permission-unknown");
    if (!permission.permissions.includes("*") && !permission.permissions.includes("streams.update")) return blocked("permission-denied");
    const state = dependencies.getState();
    if (state.kind !== "ready" || state.freshness !== "fresh" || state.revision === undefined || !state.fingerprint) {
      return blocked("state-unavailable");
    }
    if (pending) return blocked("duplicate");
    if (unresolved) return blocked("reconciliation-required");
    return undefined;
  };

  return Object.freeze({
    evaluate,
    get pending() { return pending; },
    get unresolved() { return unresolved; },
    async issue(fields: Record<string, unknown>, uploadSessionID?: string): Promise<VisualActionResult> {
      const unavailable = evaluate();
      if (unavailable) return unavailable;
      const opened = dependencies.getState();
      if (opened.kind !== "ready" || opened.revision === undefined || !opened.fingerprint) return blocked("state-unavailable");
      pending = true;
      try {
        const permission = dependencies.getPermission();
        if (permission.kind !== "ready") return blocked("permission-unknown");
        if (!permission.permissions.includes("*") && !permission.permissions.includes("streams.update")) return blocked("permission-denied");
        const current = dependencies.getState();
        if (current.kind !== "ready" || current.freshness !== "fresh" || current.revision === undefined || !current.fingerprint) {
          return blocked("state-unavailable");
        }
        if (current.revision !== opened.revision || current.fingerprint !== opened.fingerprint) return blocked("authority-changed");
        try {
          const value = await dependencies.mutate(buildVisualUpdate(current.revision, fields, uploadSessionID));
          return Object.freeze({ kind: "succeeded" as const, value });
        } catch (error) {
          const adapted = adaptAPIError(error);
          unresolved = true;
          return ["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)
            ? Object.freeze({ kind: "outcome_unknown" as const })
            : Object.freeze({ kind: "failed" as const, error: adapted });
        }
      } finally {
        pending = false;
      }
    },
    reconcile() { unresolved = false; },
  });
}

export function visualSettingsFingerprint(settings: StreamVisualSettings) {
  return JSON.stringify([
    settings.stream_id,
    settings.revision,
    settings.background_mode,
    settings.background_asset_id || "",
    settings.background_variant_id || "",
    settings.header_title_mode,
    settings.header_title_value || "",
    settings.discord_target_mode || "",
    settings.discord_snapshot_revision,
    settings.cover_source,
    settings.cover_preset_revision || 0,
    settings.cover_asset_id || "",
    settings.cover_variant_id || "",
    settings.cover_start_active,
  ]);
}

function blocked(reason: Extract<VisualActionResult, { kind: "blocked" }>['reason']) {
  return Object.freeze({ kind: "blocked" as const, reason });
}
