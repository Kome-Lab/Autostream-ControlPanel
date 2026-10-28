import { APIError, apiDelete, apiPost, apiPostForm } from "@/lib/api/client";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import { requireReadyMediaVariant, type MediaAssetUsage, type MediaUploadSession } from "./media-asset-status";
import { checkDraftMediaUpload } from "./media-upload-reconcile";
import {
  draftMediaDeletePath, draftSessionState, mediaFilenameReady, mediaUploadIssue, notifyMediaUpload,
  requireDraftMediaAsset, requireDraftSession, requireMediaID, requireMediaOperationID, requireVariantIdentity,
  type DraftMediaAsset, type DraftMediaVariant, type DraftUploadSession, type MediaUploadOptions, type MediaUploadProgress, type ReadyDraftMedia,
} from "./media-upload-protocol";

export { requireReadyMediaVariant } from "./media-asset-status";
export type { MediaAsset, MediaAssetUsage, MediaAssetVariant, MediaUploadSession } from "./media-asset-status";

const rejectedOriginalCodes = new Set([
  "upload_too_large", "invalid_image_dimensions", "content_type_mismatch", "animated_image_unsupported",
  "unsupported_image", "invalid_image", "cover_upload_limit", "forbidden", "unauthorized", "csrf_invalid",
]);

function definitelyRejectedOriginal(error: unknown) {
  return error instanceof APIError && error.status >= 400 && error.status < 500 && rejectedOriginalCodes.has(error.code || "");
}

function failureProgress(progress: MediaUploadProgress, error: unknown, definite: boolean): MediaUploadProgress {
  const issue = mediaUploadIssue(error);
  return { ...progress, phase: definite ? "failed" : "outcome_unknown", issue: definite || issue === "integrity" ? issue : "unknown",
    errorKey: adaptAPIError(error).messageKey, retryable: definite, cleanupConfirmed: definite };
}

export async function deleteDraftMediaAsset(asset: DraftMediaAsset, sessionID: string): Promise<void> {
  // Cleanup has its own bounded request. An aborted upload signal is never proof
  // that the server stopped, nor may it cancel the exact-session cleanup CAS.
  await apiDelete(draftMediaDeletePath(asset, sessionID), AbortSignal.timeout(15_000));
}

export async function convertDraftMediaAsset(
  session: DraftUploadSession, asset: DraftMediaAsset, usage: MediaAssetUsage, options: MediaUploadOptions = {},
): Promise<ReadyDraftMedia> {
  const operationID = requireMediaOperationID(options.operationID || asset.client_operation_id);
  requireDraftSession(session);
  if (draftSessionState(session) !== "usable") throw new Error("media_upload_integrity");
  requireDraftMediaAsset(asset, session.id, usage, operationID);
  let progress: MediaUploadProgress = { phase: "converting", operationID, usage, session, asset };
  notifyMediaUpload(options, progress);
  let variant: DraftMediaVariant;
  try {
    variant = await apiPost<DraftMediaVariant>(`/media-assets/${encodeURIComponent(asset.id)}/variants`, { width: 1920, height: 1080, opaque: true }, options.signal);
    requireVariantIdentity(asset, variant);
  } catch (error) {
    // A missing/invalid receipt or server error can follow a committed variant.
    // Keep the original for read-only reconciliation; do not delete or resend it.
    notifyMediaUpload(options, failureProgress(progress, error, false));
    throw error;
  }
  progress = { ...progress, variant };
  if (variant.status === "queued" || variant.status === "processing") {
    notifyMediaUpload(options, { ...progress, phase: "outcome_unknown", issue: "processing", retryable: false });
    throw new Error("media_variant_not_ready");
  }
  try {
    requireReadyMediaVariant(asset, variant);
  } catch (error) {
    if (variant.status !== "failed") {
      notifyMediaUpload(options, failureProgress(progress, error, false));
      throw error;
    }
    notifyMediaUpload(options, { ...progress, phase: "cleaning", issue: mediaUploadIssue(error) });
    try {
      await deleteDraftMediaAsset(asset, session.id);
      notifyMediaUpload(options, failureProgress(progress, error, true));
    } catch {
      notifyMediaUpload(options, { ...progress, phase: "outcome_unknown", issue: "cleanup_unknown", retryable: false });
    }
    throw error;
  }
  notifyMediaUpload(options, { ...progress, phase: "ready" });
  return { session, asset, variant };
}

export async function uploadDraftMediaAsset(
  file: File,
  usage: MediaAssetUsage,
  existingSession?: Pick<MediaUploadSession, "id">,
  options: MediaUploadOptions = {},
): Promise<ReadyDraftMedia> {
  const operationID = requireMediaOperationID(options.operationID || crypto.randomUUID());
  let progress: MediaUploadProgress = { phase: "preparing", operationID, usage };
  notifyMediaUpload(options, progress);
  if (!mediaFilenameReady(file)) {
    notifyMediaUpload(options, { ...progress, phase: "failed", issue: "file_name", retryable: false, cleanupConfirmed: true });
    throw new Error("media_upload_file_name");
  }
  let session: DraftUploadSession;
  try {
    if (existingSession) {
      requireMediaID(existingSession.id);
      const checked = await checkDraftMediaUpload(existingSession.id, operationID, usage, options.signal);
      session = checked.session;
      progress = { ...progress, session, asset: checked.asset, variant: checked.variant };
      if (checked.kind !== "missing") {
        // Re-entering an operation never writes its original again. A separate
        // read-only check can adopt a ready result or offer explicit conversion.
        notifyMediaUpload(options, { ...progress, phase: "outcome_unknown", issue: checked.kind === "claimed" || checked.kind === "expired" ? checked.kind : "unknown", retryable: false });
        throw new Error("media_upload_existing_operation");
      }
    } else {
      session = requireDraftSession(await apiPost<DraftUploadSession>("/media-assets/upload-sessions", undefined, options.signal));
      if (draftSessionState(session) !== "usable") throw new Error("media_upload_integrity");
    }
  } catch (error) {
    if (error instanceof Error && error.message === "media_upload_existing_operation") throw error;
    // A new session receipt can only leave an empty expiring session. For an
    // existing operation, even a failed GET cannot prove an earlier POST absent.
    notifyMediaUpload(options, failureProgress(progress, error, !existingSession));
    throw error;
  }
  progress = { ...progress, phase: "uploading", session };
  notifyMediaUpload(options, progress);
  const form = new FormData();
  // The server's streaming parser requires bounded metadata before image bytes.
  form.append("session_id", session.id);
  form.append("usage_type", usage);
  form.append("client_operation_id", operationID);
  form.append("file", file, file.name);
  let asset: DraftMediaAsset;
  try {
    asset = await apiPostForm<DraftMediaAsset>("/media-assets", form, options.signal);
    requireDraftMediaAsset(asset, session.id, usage, operationID);
  } catch (error) {
    // Generic bad_request, abort, JSON failure and 5xx are ambiguous: the
    // streaming handler might already have committed the uploaded original.
    notifyMediaUpload(options, failureProgress(progress, error, definitelyRejectedOriginal(error)));
    throw error;
  }
  return convertDraftMediaAsset(session, asset, usage, { ...options, operationID });
}
