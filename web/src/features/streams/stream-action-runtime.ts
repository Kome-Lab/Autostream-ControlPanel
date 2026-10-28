import type { QueryClient } from "@tanstack/react-query";

import { APIError, apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import type { PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";
import type { CurrentUser, Stream } from "@/types/domain";
import type { StreamActionStateSnapshot, StreamChangedField } from "@/features/streams/stream-action-controller";
import type { StreamActionIntent, StreamActionRequest } from "@/features/streams/stream-action-descriptors";
import { streamActionResponseStream, isConfirmedStreamStopOutcome } from "@/features/streams/stream-action-response";
import { withBoundedActionRevalidation } from "@/lib/foundation/actions/bounded-revalidation";

const mutationVersions = new WeakMap<QueryClient, { all: number; targets: Map<string, number> }>();
function versions(queryClient: QueryClient) {
  let value = mutationVersions.get(queryClient);
  if (!value) { value = { all: 0, targets: new Map() }; mutationVersions.set(queryClient, value); }
  return value;
}
function markMutationResponse(queryClient: QueryClient, ids: readonly string[]) {
  const value = versions(queryClient);
  value.all++;
  for (const id of ids) value.targets.set(id, (value.targets.get(id) || 0) + 1);
}

export async function cacheStreamActionResponse(queryClient: QueryClient, intent: StreamActionIntent, value: unknown) {
  if (intent.id === "STR-10" && intent.stream) {
    markMutationResponse(queryClient, [intent.stream.id]);
    await queryClient.cancelQueries({ queryKey: ["streams"], exact: true });
    queryClient.setQueryData<Stream[]>(["streams"], (rows) => rows?.filter((row) => row.id !== intent.stream?.id));
    return;
  }
  const stream = streamActionResponseStream(value, intent.stream?.id);
  if (!stream) return;
  const waiting = value && typeof value === "object" && "waiting_stream" in value
    ? streamActionResponseStream(value.waiting_stream) : undefined;
  markMutationResponse(queryClient, [stream.id, ...(waiting ? [waiting.id] : [])]);
  await queryClient.cancelQueries({ queryKey: ["streams"], exact: true });
  queryClient.setQueryData<Stream[]>(["streams"], (rows) => {
    const next = (rows || []).filter((row) => row.id !== stream.id && row.id !== waiting?.id);
    return [...next, ...(waiting?.id === stream.id ? [waiting] : [stream, ...(waiting ? [waiting] : [])])];
  });
}

export async function readStreamStopOutcome(queryClient: QueryClient, intent: StreamActionIntent) {
  if (intent.id !== "STR-05" || !intent.stream?.id || !intent.stream.updated_at) return undefined;
  for (let attempt = 0; attempt < 3; attempt++) {
    if (attempt) await new Promise((resolve) => setTimeout(resolve, attempt * 250));
    const value = await apiGet<unknown>(`/streams/${encodeURIComponent(intent.stream.id)}/stop-outcome?expected_updated_at=${encodeURIComponent(intent.stream.updated_at)}`, AbortSignal.timeout(5000));
    const stream = streamActionResponseStream(value, intent.stream.id);
    if (isConfirmedStreamStopOutcome(value, intent)) {
      await cacheStreamActionResponse(queryClient, intent, value);
      return value;
    }
    if (!stream || stream.status !== "stopping") return undefined;
  }
  return undefined;
}

export function streamPermissionSnapshot(queryClient: QueryClient): PermissionSnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const current = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.status !== "success" || !current || !Array.isArray(current.permissions)) {
    return Object.freeze({ kind: "unavailable" });
  }
  return Object.freeze({ kind: "ready", permissions: Object.freeze([...current.permissions]) });
}

export function streamActionStateSnapshot(queryClient: QueryClient, intent: StreamActionIntent): StreamActionStateSnapshot {
  if (intent.id === "STR-01") {
    const selected = selectedStreamPrerequisites(queryClient, intent);
    return selected ? Object.freeze({ kind: "ready", freshness: "fresh", fingerprint: JSON.stringify(selected) })
      : Object.freeze({ kind: "unknown", freshness: "unavailable" });
  }
  const state = queryClient.getQueryState<Stream[]>(["streams"]);
  const rows = queryClient.getQueryData<Stream[]>(["streams"]);
  const freshness = state?.status === "error" && Array.isArray(rows)
      ? "stale" as const
      : state?.status === "success" && Array.isArray(rows)
        ? "fresh" as const
        : "unavailable" as const;
  if (!Array.isArray(rows)) return Object.freeze({ kind: "unknown", freshness });
  const row = rows.find((value) => value.id === intent.stream?.id);
  if (!row) return Object.freeze({ kind: "missing", freshness, authorityChange: "missing" });
  const settings = intent.id === "STR-02" || intent.id === "STR-03";
  const changes = streamAuthorityChanges(intent.stream!, row);
  return Object.freeze({ kind: "ready", freshness,
    fingerprint: JSON.stringify(settings ? streamSettingsAuthority(row) : streamFingerprintFields(row)),
    ...(settings ? { baselineChanged: Boolean(changes.authorityChange) || JSON.stringify(streamSettingsAuthority(intent.stream!)) !== JSON.stringify(streamSettingsAuthority(row)) } : {}),
    ...changes,
  });
}

// Read into a local proof first. Cancelling observers only prevents older GETs
// overwriting these values; a later poll never revokes this one save intent.
export async function revalidateStreamActionAuthority(queryClient: QueryClient, intent: StreamActionIntent, signal: AbortSignal) {
  const targetID = intent.stream?.id;
  const before = targetID ? versions(queryClient).targets.get(targetID) || 0 : 0;
  const requests = streamPrerequisiteRequests(intent);
  const [currentUser, currentStream, ...selected] = await Promise.all([
    apiGet<CurrentUser>("/auth/me", signal),
    intent.id === "STR-01" ? Promise.resolve(undefined) : apiGet<Stream>(`/streams/${encodeURIComponent(intent.stream?.id || "")}`, signal).catch((error) => {
      if (error instanceof APIError && error.status === 404) return undefined;
      throw error;
    }),
    ...requests.map((row) => apiGet<unknown>(row.path, signal)),
  ]);
  if (signal.aborted) throw signal.reason;
  if (!Array.isArray(currentUser?.permissions) || intent.id !== "STR-01" && currentStream && !streamActionResponseStream(currentStream, intent.stream?.id)) throw new Error("Authority response invalid");
  const local = new Map<string, unknown>([[JSON.stringify(["auth", "me"]), currentUser], [JSON.stringify(["streams"]), currentStream ? [currentStream] : []]]);
  requests.forEach((row, index) => local.set(JSON.stringify(row.key), selected[index]));
  const proven = {
    getQueryState: (key: readonly unknown[]) => local.has(JSON.stringify(key)) ? { status: "success", fetchStatus: "idle" } : undefined,
    getQueryData: (key: readonly unknown[]) => local.get(JSON.stringify(key)),
  } as QueryClient;
  const authority = { permissions: streamPermissionSnapshot(proven), state: streamActionStateSnapshot(proven, intent) };
  if (targetID && (versions(queryClient).targets.get(targetID) || 0) !== before) {
    return { permissions: authority.permissions, state: { kind: "unknown" as const, freshness: "stale" as const } };
  }
  await Promise.all([["auth", "me"], ["streams"], ...requests.map((row) => row.key)].map((key) => queryClient.cancelQueries({ queryKey: key, exact: true })));
  if (signal.aborted) throw signal.reason;
  if (targetID && (versions(queryClient).targets.get(targetID) || 0) !== before) {
    return { permissions: authority.permissions, state: { kind: "unknown" as const, freshness: "stale" as const } };
  }
  queryClient.setQueryData(["auth", "me"], currentUser);
  if (currentStream) queryClient.setQueryData<Stream[]>(["streams"], (rows) => {
    const currentRows = rows || [];
    const index = currentRows.findIndex((row) => row.id === currentStream.id);
    const next = currentRows.filter((row) => row.id !== currentStream.id);
    next.splice(index < 0 ? next.length : index, 0, currentStream);
    return next;
  });
  requests.forEach((row, index) => queryClient.setQueryData(row.key, selected[index]));
  return authority;
}

export async function refreshStreamActionAuthority(queryClient: QueryClient) {
  const before = versions(queryClient).all;
  try {
    const [currentUser, streams] = await withBoundedActionRevalidation((signal) => Promise.all([
      apiGet<CurrentUser>("/auth/me", signal),
      apiGet<Stream[]>("/streams", signal),
    ]));
    if (!Array.isArray(currentUser.permissions) || !Array.isArray(streams)) return false;
    if (versions(queryClient).all !== before) return false;
    // A periodic observer refetch can otherwise replace a just-proven authority
    // with a transient refreshing snapshot before the control renders again.
    await Promise.all([
      queryClient.cancelQueries({ queryKey: ["auth", "me"], exact: true }),
      queryClient.cancelQueries({ queryKey: ["streams"], exact: true }),
    ]);
    if (versions(queryClient).all !== before) return false;
    queryClient.setQueryData(["auth", "me"], currentUser);
    queryClient.setQueryData(["streams"], streams);
  } catch {
    return false;
  }
  // The two validated GET results are the fresh authority. After the latch is
  // cleared, the normal evaluator still blocks removed permissions, a missing
  // target, an inapplicable lifecycle, or any later refreshing/stale state.
  return true;
}

export async function mutateStreamAction(request: StreamActionRequest & Readonly<{ signal: AbortSignal }>) {
  if (request.signal.aborted) throw new DOMException("Cancelled", "AbortError");
  if (request.method === "DELETE") return apiDelete<unknown>(request.path, request.signal);
  if (request.method === "PUT") return apiPut<unknown>(request.path, request.body, request.signal);
  return apiPost<unknown>(request.path, request.body, request.signal);
}

function streamFingerprintFields(stream: Stream) {
  return [stream.id, stream.status, stream.updated_at || stream.created_at || ""] as const;
}

function streamSettingsAuthority(stream: Stream) {
  return [stream.id, stream.status, stream.settings_revision || "unconfigured", stream.assigned_encoder_id || "", stream.assigned_worker_id || ""];
}

function streamAuthorityChanges(baseline: Stream, current: Stream): Pick<StreamActionStateSnapshot, "authorityChange" | "changedFields"> {
  const groups: readonly [StreamChangedField, readonly (keyof Stream)[]][] = [
    ["name", ["name"]], ["schedule", ["scheduled_start_at", "scheduled_end_at"]],
    ["discord", ["discord_config_id", "auto_start_trigger"]], ["youtube", ["youtube_output_id"]],
    ["encoding", ["encoder_profile_id"]], ["caption", ["caption_profile_id"]],
    ["overlay", ["overlay_profile_id"]], ["archive", ["archive_profile_id"]],
    ["audio", ["encoder_audio_gain_db"]], ["nodes", ["assigned_encoder_id", "assigned_worker_id"]],
  ];
  const changedFields = groups.filter(([, keys]) => keys.some((key) => (baseline[key] ?? "") !== (current[key] ?? ""))).map(([field]) => field);
  const authorityChange = changedFields.includes("nodes") ? "ownership" as const
    : baseline.status !== current.status ? "lifecycle" as const
    : (baseline.settings_revision || "unconfigured") !== (current.settings_revision || "unconfigured") || changedFields.length ? "settings" as const : undefined;
  return { ...(authorityChange ? { authorityChange } : {}), ...(changedFields.length ? { changedFields } : {}) };
}

const streamProfileReferences = [
  ["discord_config_id", "/discord/configs"], ["youtube_output_id", "/youtube/outputs"],
  ["encoder_profile_id", "/profiles/encoder"], ["caption_profile_id", "/profiles/caption"],
  ["overlay_profile_id", "/profiles/overlay"], ["archive_profile_id", "/profiles/archive"],
] as const;

function streamPrerequisiteRequests(intent: StreamActionIntent) {
  if (intent.id !== "STR-01") return [] as { key: readonly string[]; path: string }[];
  const requests: { key: readonly string[]; path: string }[] = streamProfileReferences.filter(([field]) => typeof intent.payload?.[field] === "string" && intent.payload[field] !== "")
    .map(([, path]) => ({ key: ["resource", path] as readonly string[], path }));
  if (["encoder_service_id", "worker_service_id", "discord_config_id"].some((field) => Boolean(intent.payload?.[field]))) requests.push({ key: ["service-health"], path: "/service-health" });
  return requests;
}

function selectedStreamPrerequisites(queryClient: QueryClient, intent: StreamActionIntent) {
  const profiles: unknown[] = [];
  const services = new Set<string>();
  for (const field of ["encoder_service_id", "worker_service_id"]) {
    const id = intent.payload?.[field]; if (typeof id === "string" && id) services.add(id);
  }
  for (const [field, path] of streamProfileReferences) {
    const id = intent.payload?.[field];
    if (typeof id !== "string" || !id) continue;
    const state = queryClient.getQueryState(["resource", path]);
    const row = authorityRows(queryClient.getQueryData(["resource", path])).find((item) => item.id === id);
    if (state?.status !== "success" || !row) return undefined;
    profiles.push([path, id, row.revision ?? "", row.updated_at ?? "", row.service_id ?? "", row.enabled ?? true]);
    if (field === "discord_config_id" && typeof row.service_id === "string") services.add(row.service_id);
  }
  const nodes: unknown[] = [];
  if (services.size) {
    if (queryClient.getQueryState(["service-health"])?.status !== "success") return undefined;
    const rows = authorityRows(queryClient.getQueryData(["service-health"]));
    for (const id of [...services].sort()) {
      const row = rows.find((item) => (item.service_id || item.id) === id);
      if (!row) return undefined;
      nodes.push([id, row.service_type, row.current_stream_id || "", row.assignment_role || "", row.ownership_epoch ?? 0, row.execution_host_id || "", row.transport_mode || ""]);
    }
  }
  return ["new-stream", profiles, nodes];
}

function authorityRows(value: unknown): Record<string, unknown>[] {
  const rows = Array.isArray(value) ? value : value && typeof value === "object" ? ["items", "data", "results"].map((key) => (value as Record<string, unknown>)[key]).find(Array.isArray) : undefined;
  return Array.isArray(rows) ? rows.filter((row): row is Record<string, unknown> => Boolean(row && typeof row === "object" && !Array.isArray(row))) : [];
}
