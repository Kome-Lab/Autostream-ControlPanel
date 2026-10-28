import { apiGet } from "@/lib/api/client";
import { requireReadyMediaVariant, type MediaAssetUsage } from "./media-asset-status";
import {
  draftSessionState, mediaVariantTargetsSpec, requireDraftMediaAsset, requireMediaID, requireMediaOperationID, requireVariantIdentity,
  type DraftMediaCheck, type DraftUploadLookup,
} from "./media-upload-protocol";

// A missing receipt is not permission to resend. This function performs one
// read-only query; a separate explicit action resumes a missing conversion.
export async function checkDraftMediaUpload(sessionID: string, operationID: string, usage: MediaAssetUsage, signal?: AbortSignal): Promise<DraftMediaCheck> {
  requireMediaID(sessionID); requireMediaOperationID(operationID);
  const result = await apiGet<DraftUploadLookup>(`/media-assets/upload-sessions/${encodeURIComponent(sessionID)}/assets?operation_id=${encodeURIComponent(operationID)}`, signal);
  if (result?.session?.id !== sessionID || result.operation_id !== operationID || !Array.isArray(result.assets) || result.assets.length > 1) throw new Error("media_upload_integrity");
  const session = result.session;
  const state = draftSessionState(session);
  if (state !== "usable") return { kind: state, session };
  const record = result.assets[0];
  if (!record) return { kind: "missing", session };
  const asset = record.asset;
  if (asset?.owner_type === "stream" || asset?.owner_type === "preset") {
    requireMediaID(asset.id); requireMediaID(asset.owner_id);
    if (asset.client_operation_id !== operationID || asset.usage_type !== usage || asset.owner_id === sessionID
      || asset.upload_session_id !== undefined && typeof asset.upload_session_id !== "string"
      || asset.upload_session_id && asset.upload_session_id !== sessionID
      || asset.owner_type === "preset" && asset.upload_session_id) throw new Error("media_upload_integrity");
    // The exact operation snapshot proves the immutable origin session. A
    // claimed asset is observable, but is never a draft conversion/cleanup input.
    return { kind: "claimed", session, asset };
  }
  requireDraftMediaAsset({ ...asset, deleted_at: undefined }, sessionID, usage, operationID);
  if (asset.deleted_at) return { kind: "deleted", session, asset };
  if (!Array.isArray(record.variants)) throw new Error("media_upload_integrity");
  const variants = record.variants.filter(mediaVariantTargetsSpec);
  if (variants.length > 1) throw new Error("media_upload_integrity");
  const selected = variants[0];
  if (!selected) return { kind: "uploaded", session, asset };
  const variant = requireVariantIdentity(asset, selected);
  return variant.status === "ready" ? { kind: "ready", session, asset, variant: requireReadyMediaVariant(asset, variant) }
    : { kind: variant.status === "failed" ? "failed" : "processing", session, asset, variant };
}
