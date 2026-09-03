import type { QueryClient } from "@tanstack/react-query";

import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api/client";
import type { PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";
import type { CurrentUser, Stream } from "@/types/domain";
import type { StreamActionStateSnapshot } from "@/features/streams/stream-action-controller";
import type { StreamActionIntent, StreamActionRequest } from "@/features/streams/stream-action-descriptors";

export function streamPermissionSnapshot(queryClient: QueryClient): PermissionSnapshot {
  const state = queryClient.getQueryState<CurrentUser>(["auth", "me"]);
  const current = queryClient.getQueryData<CurrentUser>(["auth", "me"]);
  if (state?.fetchStatus === "fetching") return Object.freeze({ kind: "refreshing" });
  if (state?.status !== "success" || !current || !Array.isArray(current.permissions)) {
    return Object.freeze({ kind: "unavailable" });
  }
  return Object.freeze({ kind: "ready", permissions: Object.freeze([...current.permissions]) });
}

export function streamActionStateSnapshot(queryClient: QueryClient, intent: StreamActionIntent): StreamActionStateSnapshot {
  const state = queryClient.getQueryState<Stream[]>(["streams"]);
  const rows = queryClient.getQueryData<Stream[]>(["streams"]);
  const freshness = state?.fetchStatus === "fetching"
    ? "refreshing" as const
    : state?.status === "error" && Array.isArray(rows)
      ? "stale" as const
      : state?.status === "success" && Array.isArray(rows)
        ? "fresh" as const
        : "unavailable" as const;
  if (!Array.isArray(rows)) return Object.freeze({ kind: "unknown", freshness });
  if (intent.id === "STR-01") {
    return Object.freeze({
      kind: "ready",
      freshness,
      fingerprint: JSON.stringify(rows.map(streamFingerprintFields)),
    });
  }
  const row = rows.find((value) => value.id === intent.stream?.id);
  if (!row) return Object.freeze({ kind: "missing", freshness });
  return Object.freeze({ kind: "ready", freshness, fingerprint: JSON.stringify(streamFingerprintFields(row)) });
}

export async function refreshStreamActionAuthority(queryClient: QueryClient) {
  try {
    const [currentUser, streams] = await Promise.all([
      apiGet<CurrentUser>("/auth/me"),
      apiGet<Stream[]>("/streams"),
    ]);
    if (!Array.isArray(currentUser.permissions) || !Array.isArray(streams)) return false;
    // A periodic observer refetch can otherwise replace a just-proven authority
    // with a transient refreshing snapshot before the control renders again.
    await Promise.all([
      queryClient.cancelQueries({ queryKey: ["auth", "me"], exact: true }),
      queryClient.cancelQueries({ queryKey: ["streams"], exact: true }),
    ]);
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
  if (request.method === "DELETE") return apiDelete<unknown>(request.path);
  if (request.method === "PUT") return apiPut<unknown>(request.path, request.body);
  return apiPost<unknown>(request.path, request.body);
}

function streamFingerprintFields(stream: Stream) {
  return [stream.id, stream.status, stream.updated_at || stream.created_at || ""] as const;
}
