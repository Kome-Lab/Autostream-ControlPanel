import type { TranslationKey } from "@/lib/i18n";
import { mediaAssetBelongsToDraft, type MediaAsset, type MediaAssetUsage, type MediaAssetVariant, type MediaUploadSession } from "./media-asset-status";

export type DraftMediaAsset = MediaAsset & { client_operation_id?: string; deleted_at?: string };
export type DraftMediaVariant = MediaAssetVariant & { target_width?: number; target_height?: number; crop_mode?: string };
export type DraftUploadSession = Omit<MediaUploadSession, "owner_type"> & { owner_type: string; claimed_stream_id?: string };
export type DraftUploadLookup = { session: DraftUploadSession; operation_id: string; assets: { asset: DraftMediaAsset; variants: DraftMediaVariant[] }[] };
export type MediaUploadPhase = "preparing" | "uploading" | "converting" | "checking" | "cleaning" | "ready" | "failed" | "outcome_unknown";
export type MediaUploadIssue = "file_name" | "unknown" | "processing" | "expired" | "claimed" | "integrity" | "cleanup_unknown" | "rejected" | "too_large" | "dimensions" | "type_mismatch" | "animated" | "unsupported" | "conversion_failed";
export type MediaUploadProgress = Readonly<{
  phase: MediaUploadPhase;
  operationID: string;
  usage: MediaAssetUsage;
  session?: DraftUploadSession;
  asset?: DraftMediaAsset;
  variant?: MediaAssetVariant;
  issue?: MediaUploadIssue;
  errorKey?: TranslationKey;
  retryable?: boolean;
  cleanupConfirmed?: boolean;
}>;
export type MediaUploadOptions = Readonly<{
  operationID?: string;
  signal?: AbortSignal;
  onProgress?: (progress: MediaUploadProgress) => void;
}>;
export type ReadyDraftMedia = { session: DraftUploadSession; asset: DraftMediaAsset; variant: MediaAssetVariant };
export type DraftMediaCheck = Readonly<{
  kind: "missing" | "deleted" | "expired" | "claimed" | "uploaded" | "processing" | "failed" | "ready";
  session: DraftUploadSession;
  asset?: DraftMediaAsset;
  variant?: MediaAssetVariant;
}>;
export type MediaUploadSummary = Readonly<{
  pendingCount: number;
  failedCount: number;
  unresolvedCount: number;
  coverReservations: number;
  blocking: boolean;
  statusID: string;
}>;

export function requireMediaID(value: unknown): string {
  if (typeof value !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(value)) throw new Error("media_upload_integrity");
  return value;
}

export function requireMediaOperationID(value: unknown): string {
  if (typeof value !== "string" || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value)) throw new Error("media_upload_integrity");
  return value;
}

export function requireDraftSession(session: DraftUploadSession): DraftUploadSession {
  requireMediaID(session?.id);
  if (session.owner_type !== "upload_draft" || session.claimed_stream_id) throw new Error("media_upload_integrity");
  if (!Number.isFinite(Date.parse(session.expires_at))) throw new Error("media_upload_integrity");
  return session;
}

export function draftSessionState(session: DraftUploadSession, now = Date.now()): "usable" | "expired" | "claimed" {
  requireMediaID(session?.id);
  if (session.owner_type !== "upload_draft" || session.claimed_stream_id) return "claimed";
  const expires = Date.parse(session.expires_at);
  if (!Number.isFinite(expires)) throw new Error("media_upload_integrity");
  return expires <= now ? "expired" : "usable";
}

export function requireDraftMediaAsset(asset: DraftMediaAsset, sessionID: string, usage: MediaAssetUsage, operationID: string): DraftMediaAsset {
  requireMediaID(asset?.id); requireMediaID(sessionID); requireMediaOperationID(operationID);
  if (asset.deleted_at || !mediaAssetBelongsToDraft(asset, sessionID) || asset.usage_type !== usage || asset.client_operation_id !== operationID) throw new Error("media_upload_integrity");
  return asset;
}

export function requireVariantIdentity(asset: DraftMediaAsset, variant: DraftMediaVariant): MediaAssetVariant {
  requireMediaID(variant?.id);
  if (variant.asset_id !== asset.id || !mediaVariantMatchesSpec(variant)
    || !["queued", "processing", "ready", "failed"].includes(variant.status)) throw new Error("media_upload_integrity");
  return variant;
}

export function mediaVariantMatchesSpec(variant: DraftMediaVariant) {
  // Non-ready rows have no processed width/height/opacity yet. Their immutable
  // target specification, rather than those zero-valued result fields, owns it.
  return variant.status === "ready"
    ? variant.width === 1920 && variant.height === 1080 && variant.opaque === true
    : variant.target_width === 1920 && variant.target_height === 1080 && variant.crop_mode === "center_crop_opaque";
}

export function mediaVariantTargetsSpec(variant: DraftMediaVariant) {
  return variant.target_width !== undefined || variant.target_height !== undefined || variant.crop_mode !== undefined
    ? variant.target_width === 1920 && variant.target_height === 1080 && variant.crop_mode === "center_crop_opaque"
    : variant.status === "ready" && mediaVariantMatchesSpec(variant);
}

export function draftMediaDeletePath(asset: MediaAsset, sessionID: string) {
  requireMediaID(asset?.id); requireMediaID(sessionID);
  if (!mediaAssetBelongsToDraft(asset, sessionID)) throw new Error("media_upload_integrity");
  return `/media-assets/${encodeURIComponent(asset.id)}?expected_upload_session_id=${encodeURIComponent(sessionID)}`;
}

// Presentation observers cannot change a completed transport or trigger cleanup.
export function notifyMediaUpload(options: MediaUploadOptions, progress: MediaUploadProgress) {
  try { options.onProgress?.(Object.freeze(progress)); } catch { /* Observer failures do not mutate upload ownership. */ }
}

export function mediaFilenameReady(file: Pick<File, "name">) {
  const length = [...file.name.trim()].length;
  return length > 0 && length <= 255;
}

export function mediaUploadIsPending(phase: MediaUploadPhase) {
  return phase === "preparing" || phase === "uploading" || phase === "converting" || phase === "checking" || phase === "cleaning";
}

export function mediaUploadIssue(error: unknown): MediaUploadIssue {
  const code = error && typeof error === "object" && "code" in error ? error.code : error instanceof Error ? error.message : "";
  return code === "upload_too_large" ? "too_large" : code === "invalid_image_dimensions" ? "dimensions"
    : code === "content_type_mismatch" ? "type_mismatch" : code === "animated_image_unsupported" ? "animated"
      : code === "unsupported_image" || code === "invalid_image" ? "unsupported"
        : code === "image_processing_failed" || code === "media_variant_failed" ? "conversion_failed"
          : code === "media_upload_integrity" || code === "media_variant_integrity" || code === "media_asset_integrity" ? "integrity" : "rejected";
}
