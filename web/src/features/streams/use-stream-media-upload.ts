"use client";

import { useId, useLayoutEffect, useMemo, useState, useSyncExternalStore } from "react";
import { convertDraftMediaAsset, deleteDraftMediaAsset, uploadDraftMediaAsset } from "./media-asset-client";
import { checkDraftMediaUpload } from "./media-upload-reconcile";
import { createMediaUploadOwner, type MediaUploadOwnerOptions } from "./media-upload-owner";
import { mediaUploadIsPending, type MediaUploadSummary } from "./media-upload-protocol";

const noServerUpload = () => undefined;

export function useStreamMediaUpload(options: MediaUploadOwnerOptions) {
  const statusID = `stream-image-status-${useId()}`;
  const [owner] = useState(() => createMediaUploadOwner(options, {
    upload: uploadDraftMediaAsset,
    check: checkDraftMediaUpload,
    convert: convertDraftMediaAsset,
    discard: deleteDraftMediaAsset,
    createURL: file => URL.createObjectURL(file),
    revokeURL: url => URL.revokeObjectURL(url),
    operationID: () => crypto.randomUUID(),
  }));
  useLayoutEffect(() => { owner.configure(options); }, [options, owner]);
  useLayoutEffect(() => { owner.activate(); return () => owner.dispose(); }, [owner]);
  const operation = useSyncExternalStore(owner.subscribe, owner.getSnapshot, noServerUpload);
  const pending = Boolean(operation && (mediaUploadIsPending(operation.phase) || operation.phase === "ready"));
  const summary = useMemo<MediaUploadSummary>(() => Object.freeze({
    pendingCount: pending ? 1 : 0,
    failedCount: operation?.phase === "failed" ? 1 : 0,
    unresolvedCount: operation?.phase === "outcome_unknown" ? 1 : 0,
    coverReservations: operation?.kind === "cover" && !operation.cleanupConfirmed ? 1 : 0,
    blocking: Boolean(operation),
    statusID,
  }), [operation, pending, statusID]);
  return {
    operation, summary,
    pendingKind: pending ? operation?.kind || "" : "",
    isBlocked: owner.isBlocked,
    start: owner.start, retry: owner.retry, dismiss: owner.dismiss,
    check: owner.check, resume: owner.resume, reset: owner.reset,
  };
}
