import { apiPost, apiPostForm } from "@/lib/api/client";
import { requireReadyMediaVariant, type MediaAsset, type MediaAssetUsage, type MediaAssetVariant, type MediaUploadSession } from "./media-asset-status";

export { requireReadyMediaVariant } from "./media-asset-status";
export type { MediaAsset, MediaAssetUsage, MediaAssetVariant, MediaUploadSession } from "./media-asset-status";

export async function uploadDraftMediaAsset(
  file: File,
  usage: MediaAssetUsage,
  existingSession?: Pick<MediaUploadSession, "id">,
) {
  const session = existingSession ?? await apiPost<MediaUploadSession>("/media-assets/upload-sessions");
  const form = new FormData();
  // The server's streaming parser requires bounded metadata before image bytes.
  form.append("session_id", session.id);
  form.append("usage_type", usage);
  form.append("file", file, file.name);
  const asset = await apiPostForm<MediaAsset>("/media-assets", form);
  const variant = await apiPost<MediaAssetVariant>(`/media-assets/${encodeURIComponent(asset.id)}/variants`, { width: 1920, height: 1080, opaque: true });
  return { session, asset, variant: requireReadyMediaVariant(asset, variant) };
}
