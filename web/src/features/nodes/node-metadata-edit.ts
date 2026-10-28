import type { QueryClient } from "@tanstack/react-query";
import { APIError, apiGet } from "../../lib/api/client";
import { hasPermission } from "../../lib/auth/permissions";
import { withBoundedActionRevalidation } from "../../lib/foundation/actions/bounded-revalidation";
import type { CurrentUser, WorkerNode } from "../../types/domain";

export function hasNodeMetadataRevision(node: WorkerNode) {
  return /^[a-f0-9]{64}$/.test(node.metadata_revision ?? "");
}

// The edit-start stamp belongs to the draft. A polling result may prove that
// stamp is still current, but must never replace it with a newer stamp.
export async function revalidateNodeMetadataEdit(client: QueryClient, nodeID: string, baseline: WorkerNode) {
  if ((baseline.service_id || baseline.id) !== nodeID || !hasNodeMetadataRevision(baseline)) {
    throw new APIError("Node edit revision is unavailable", 409, "node_metadata_revision_required");
  }
  await withBoundedActionRevalidation(async (signal) => {
    await Promise.all([
      client.cancelQueries({ queryKey: ["auth", "me"] }),
      client.cancelQueries({ queryKey: ["nodes"] }),
    ]);
    const [user, nodes] = await Promise.all([
      apiGet<CurrentUser>("/auth/me", signal),
      apiGet<WorkerNode[]>("/nodes", signal),
    ]);
    if (!hasPermission(user, "api_tokens.create")) throw new APIError("Node edit permission was revoked", 403, "permission_denied");
    const current = nodes.find((node) => (node.service_id || node.id) === nodeID);
    if (!current) throw new APIError("Node no longer exists", 404, "not_found");
    if (!hasNodeMetadataRevision(current) || current.metadata_revision !== baseline.metadata_revision) {
      throw new APIError("Node metadata changed", 409, "node_metadata_conflict");
    }
  });
  return baseline.metadata_revision!;
}

export function nodeMetadataWriteOutcomeUnknown(error: unknown) {
  // These route rejections happen before UpdateServiceMetadata. A lost response
  // or server failure must retain the unresolved intent instead of retrying it.
  if (!(error instanceof APIError)) return true;
  if (error.status === 401 || error.status === 403) return false;
  const safeRejections: Record<number, readonly string[]> = {
    400: ["bad_request", "invalid_metadata_revision", "invalid_node_endpoint", "node_endpoint_blocked", "invalid_node_registration"],
    404: ["not_found"],
    409: ["node_metadata_conflict", "node_endpoint_managed_by_updater"],
    503: ["node_endpoint_ownership_unavailable"],
  };
  return !safeRejections[error.status]?.includes(error.code ?? "");
}
