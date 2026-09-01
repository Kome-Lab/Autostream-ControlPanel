export type MediaAssetUsage = "scene_background" | "video_cover";

export type MediaUploadSession = { id: string; owner_type: "upload_draft"; expires_at: string };
export type MediaAsset = { id: string; usage_type: MediaAssetUsage; media_type: string; width: number; height: number; sha256: string };
export type MediaAssetVariant = { id: string; asset_id: string; status: "queued" | "processing" | "ready" | "failed"; width: number; height: number; opaque: boolean; last_error_code?: string };

const safeVariantErrorCodes = new Set([
  "upload_too_large",
  "invalid_image_dimensions",
  "content_type_mismatch",
  "animated_image_unsupported",
  "unsupported_image",
  "media_asset_integrity",
  "image_processing_failed",
]);

export function requireReadyMediaVariant(asset: MediaAsset, variant: MediaAssetVariant) {
  if (!asset.id || !variant.id || variant.asset_id !== asset.id) throw new Error("media_variant_integrity");
  if (variant.status === "failed") {
    throw new Error(variant.last_error_code && safeVariantErrorCodes.has(variant.last_error_code) ? variant.last_error_code : "media_variant_failed");
  }
  if (variant.status !== "ready") throw new Error("media_variant_not_ready");
  if (variant.width !== 1920 || variant.height !== 1080 || !variant.opaque) throw new Error("media_variant_integrity");
  return variant;
}
