import { buildVisualUpdate, type StreamVisualSettings } from "@/features/streams/control-platform";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import { APIError } from "@/lib/api/client";
import { withBoundedActionRevalidation } from "@/lib/foundation/actions/bounded-revalidation";

export type VisualActionPermissionSnapshot = Readonly<{
  kind: "ready" | "refreshing" | "unavailable";
  permissions: readonly string[];
}>;

export type VisualActionStateSnapshot = Readonly<{
  kind: "ready" | "missing" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
  revision?: number;
  fingerprint?: string;
  streamID?: string;
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
  mutate: (request: Record<string, unknown>, signal?: AbortSignal) => Promise<StreamVisualSettings>;
  revalidate?: (signal: AbortSignal) => Promise<Readonly<{ permission: VisualActionPermissionSnapshot; state: VisualActionStateSnapshot }>>;
}>) {
  let pending = false;
  let unresolved = false;
  let uncertain: { revision: number; streamID?: string; fields: Record<string, unknown> } | undefined;
  let active: { abort: AbortController; sent: boolean } | undefined;

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
    async issue(fields: Record<string, unknown>, expectedRevision: number, uploadSessionID?: string): Promise<VisualActionResult> {
      if (!Number.isSafeInteger(expectedRevision) || expectedRevision < 0) return blocked("state-unavailable");
      const unavailable = evaluate();
      if (unavailable) return unavailable;
      const opened = dependencies.getState();
      if (opened.kind !== "ready" || opened.revision === undefined || !opened.fingerprint) return blocked("state-unavailable");
      if (opened.revision !== expectedRevision) return blocked("authority-changed");
      pending = true;
      const operation = { abort: new AbortController(), sent: false };
      active = operation;
      try {
        let proven: Readonly<{ permission: VisualActionPermissionSnapshot; state: VisualActionStateSnapshot }>;
        try {
          proven = dependencies.revalidate
            ? await withBoundedActionRevalidation(dependencies.revalidate, operation.abort.signal)
            : { permission: dependencies.getPermission(), state: dependencies.getState() };
        } catch { return blocked("state-unavailable"); }
        const permission = proven.permission;
        if (permission.kind !== "ready") return blocked("permission-unknown");
        if (!permission.permissions.includes("*") && !permission.permissions.includes("streams.update")) return blocked("permission-denied");
        const current = proven.state;
        if (current.kind !== "ready" || current.freshness !== "fresh" || current.revision === undefined || !current.fingerprint) {
          return blocked("state-unavailable");
        }
        if (current.revision !== expectedRevision || current.fingerprint !== opened.fingerprint) return blocked("authority-changed");
        try {
          if (operation.abort.signal.aborted) return blocked("state-unavailable");
          operation.sent = true;
          const value = await dependencies.mutate({ ...buildVisualUpdate(expectedRevision, fields, uploadSessionID), expected_revision: expectedRevision }, operation.abort.signal);
          return Object.freeze({ kind: "succeeded" as const, value });
        } catch (error) {
          const adapted = adaptAPIError(error);
          unresolved = !definitiveVisualRejection(error);
          if (unresolved) {
            try { uncertain = { revision: expectedRevision, streamID: opened.streamID, fields: JSON.parse(JSON.stringify(fields)) }; }
            catch { uncertain = undefined; }
          }
          return ["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)
            ? Object.freeze({ kind: "outcome_unknown" as const })
            : Object.freeze({ kind: "failed" as const, error: adapted });
        }
      } finally {
        pending = false;
        if (active === operation) active = undefined;
      }
    },
    cancel() { if (active && !active.sent) active.abort.abort(); },
    // A successful GET alone cannot release an uncertain write. Only observing
    // its requested state at a newer revision releases this latch; this is a
    // read-only desired-state observation, never a second PUT or success receipt.
    reconcile(settings?: StreamVisualSettings) {
      if (!unresolved) return true;
      if (!settings || !uncertain?.streamID || settings.stream_id !== uncertain.streamID || !Number.isSafeInteger(settings.revision) || settings.revision <= uncertain.revision || !visualRequestObserved(settings, uncertain.fields)) return false;
      unresolved = false;
      uncertain = undefined;
      return true;
    },
  });
}

function definitiveVisualRejection(error: unknown) {
  if (!(error instanceof APIError)) return false;
  return error.status === 400 && ["bad_request", "invalid_visual_settings"].includes(error.code || "")
    || error.status === 409 && ["revision_conflict", "cover_upload_limit", "asset_claim_failed"].includes(error.code || "");
}

function visualRequestObserved(settings: StreamVisualSettings, fields: Record<string, unknown>) {
  const direct = new Set(["background_mode", "background_asset_id", "background_variant_id", "header_title_mode", "header_title_value", "cover_source", "cover_preset_id", "cover_preset_revision", "cover_asset_id", "cover_variant_id", "cover_start_active"]);
  const nullableText = new Set(["background_asset_id", "background_variant_id", "header_title_value", "cover_preset_id", "cover_asset_id", "cover_variant_id"]);
  if (!Object.keys(fields).length) return false;
  for (const [key, expected] of Object.entries(fields)) {
    if (direct.has(key)) {
      const actual = settings[key as keyof StreamVisualSettings];
      if (nullableText.has(key)) {
        if ((actual ?? "") !== (expected ?? "")) return false;
        continue;
      }
      if ((actual ?? (typeof expected === "number" ? 0 : "")) !== expected) return false;
    } else if (key === "discord_target") {
      if (!expected || typeof expected !== "object" || Array.isArray(expected)) return false;
      const target = expected as Record<string, unknown>;
      if (settings.discord_target_mode !== target.mode) return false;
      if (target.mode === "preset" && (settings.discord_target_preset_id !== target.preset_id || settings.discord_target_preset_revision !== target.preset_revision)) return false;
      if (target.mode === "manual" && (settings.discord_guild_id !== target.guild_id || settings.discord_text_channel_id !== target.text_channel_id || settings.discord_voice_channel_id !== target.voice_channel_id)) return false;
      if (!["inherit", "preset", "manual"].includes(String(target.mode))) return false;
    } else if (key === "cover_candidates") {
      if (!Array.isArray(expected) || !settings.cover_candidates) return false;
      const normalize = (rows: Record<string, unknown>[]) => rows.map((row) => [String(row.name || "").trim(), row.source, row.asset_id, row.variant_id, row.preset_id || "", row.preset_revision || 0]);
      if (expected.some((row) => !row || typeof row !== "object" || Array.isArray(row))) return false;
      if (JSON.stringify(normalize(expected)) !== JSON.stringify(normalize(settings.cover_candidates))) return false;
    } else return false;
  }
  return true;
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
