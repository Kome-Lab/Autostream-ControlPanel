import { mediaFilenameReady, type DraftMediaCheck, type DraftMediaAsset, type DraftUploadSession, type MediaUploadOptions, type MediaUploadProgress, type ReadyDraftMedia } from "./media-upload-protocol";
import type { MediaAssetUsage } from "./media-asset-status";

export type MediaUploadKind = "background" | "cover";
export type MediaUploadDisplay = Readonly<MediaUploadProgress & {
  kind: MediaUploadKind; name: string; previewURL: string; canResume: boolean;
}>;
export type MediaUploadOwnerOptions = {
  ownerKey: string; sessionID: string; coverCount: number; candidateCount: number;
  canStart: () => boolean;
  onReady: (kind: MediaUploadKind, result: ReadyDraftMedia, file: File, previewURL: string) => void;
};
export type MediaUploadTransport = {
  upload: (file: File, usage: MediaAssetUsage, session: { id: string } | undefined, options: MediaUploadOptions) => Promise<ReadyDraftMedia>;
  check: (sessionID: string, operationID: string, usage: MediaAssetUsage, signal: AbortSignal) => Promise<DraftMediaCheck>;
  convert: (session: DraftUploadSession, asset: DraftMediaAsset, usage: MediaAssetUsage, options: MediaUploadOptions) => Promise<ReadyDraftMedia>;
  discard: (asset: DraftMediaAsset, sessionID: string) => Promise<void>;
  createURL: (file: File) => string; revokeURL: (url: string) => void; operationID: () => string;
};
type Operation = {
  generation: number; kind: MediaUploadKind; file: File; url: string;
  progress: MediaUploadProgress; abort: AbortController; resume: boolean; deleteRequested: boolean;
};

// One in-flight operation belongs to one draft generation. The ready candidate
// is handed back separately; incomplete images never become save payload data.
export function createMediaUploadOwner(initial: MediaUploadOwnerOptions, transport: MediaUploadTransport) {
  let options = initial, retainedSessionID = initial.sessionID, generation = 0, active = true;
  let operation: Operation | undefined, snapshot: MediaUploadDisplay | undefined;
  const listeners = new Set<() => void>();
  const current = (value: Operation) => active && operation === value && value.generation === generation;
  const usage = (kind: MediaUploadKind): MediaAssetUsage => kind === "cover" ? "video_cover" : "scene_background";
  const publish = () => {
    snapshot = operation ? Object.freeze({ ...operation.progress, kind: operation.kind, name: operation.file.name, previewURL: operation.url, canResume: operation.resume }) : undefined;
    for (const listener of listeners) listener();
  };
  const clear = () => {
    if (operation?.url) transport.revokeURL(operation.url);
    operation = undefined;
    publish();
  };
  const receive = (value: Operation, progress: MediaUploadProgress) => {
    value.progress = progress;
    if (!current(value)) return;
    if (progress.session) retainedSessionID = progress.session.id;
    publish();
  };
  const unknown = (value: Operation) => {
    receive(value, { ...value.progress, phase: "outcome_unknown", issue: "unknown", retryable: false });
  };
  const complete = async (value: Operation, result: ReadyDraftMedia) => {
    if (!current(value)) {
      // Late success has proven identities; the server still verifies exact
      // draft ownership. A claimed asset is protected even after navigation.
      await transport.discard(result.asset, result.session.id).catch(() => undefined);
      return;
    }
    options.onReady(value.kind, result, value.file, value.url);
    value.url = ""; // Preview ownership transfers to the adopted ready candidate.
    operation = undefined;
    publish();
  };
  const run = async (value: Operation, request: () => Promise<ReadyDraftMedia>) => {
    try { await complete(value, await request()); }
    catch {
      if (current(value) && value.progress.phase !== "failed" && value.progress.phase !== "outcome_unknown") unknown(value);
    }
  };
  const start = async (kind: MediaUploadKind, file?: File) => {
    if (!file || !active || operation || !options.canStart() || kind === "cover" && (options.coverCount >= 4 || options.candidateCount >= 32)) return;
    const value: Operation = {
      generation: ++generation, kind, file, url: transport.createURL(file), abort: new AbortController(), resume: false, deleteRequested: false,
      progress: { phase: "preparing", operationID: transport.operationID(), usage: usage(kind) },
    };
    operation = value;
    if (!mediaFilenameReady(file)) {
      value.progress = { ...value.progress, phase: "failed", issue: "file_name", retryable: false, cleanupConfirmed: true };
      publish();
      return;
    }
    publish();
    await run(value, () => transport.upload(file, usage(kind), retainedSessionID ? { id: retainedSessionID } : undefined, {
      operationID: value.progress.operationID, signal: value.abort.signal, onProgress: progress => receive(value, progress),
    }));
  };
  const deleteFailed = async (value: Operation) => {
    if (value.progress.cleanupConfirmed) return true;
    const { asset, session } = value.progress;
    if (!asset || !session) return false;
    value.deleteRequested = true;
    receive(value, { ...value.progress, phase: "cleaning" });
    try {
      await transport.discard(asset, session.id);
      if (!current(value)) return false;
      receive(value, { ...value.progress, phase: "failed", cleanupConfirmed: true, retryable: true });
      return true;
    } catch {
      if (current(value)) receive(value, { ...value.progress, phase: "outcome_unknown", issue: "cleanup_unknown", retryable: false });
      return false;
    }
  };
  const retry = async () => {
    const value = operation;
    if (!value || value.progress.phase !== "failed" || !value.progress.retryable || !options.canStart()) return;
    if (!await deleteFailed(value) || !current(value)) return;
    const { kind, file } = value;
    clear();
    await start(kind, file);
  };
  const dismiss = async () => {
    const value = operation;
    if (!value || value.progress.phase !== "failed" || !options.canStart()) return;
    if (await deleteFailed(value) && current(value)) clear();
  };
  const check = async () => {
    const value = operation;
    const sessionID = value?.progress.session?.id || retainedSessionID;
    if (!value || !sessionID || value.progress.phase !== "outcome_unknown") return;
    value.abort = new AbortController();
    value.resume = false;
    receive(value, { ...value.progress, phase: "checking" });
    try {
      const result = await transport.check(sessionID, value.progress.operationID, usage(value.kind), value.abort.signal);
      if (!current(value)) return;
      if (result.kind === "ready" && result.asset && result.variant) {
        await complete(value, { session: result.session, asset: result.asset, variant: result.variant });
        return;
      }
      if (result.kind === "deleted" || result.kind === "missing" && value.deleteRequested && value.progress.asset) {
        receive(value, { ...value.progress, phase: "failed", issue: "rejected", retryable: true, cleanupConfirmed: true });
      } else if (result.kind === "failed" && result.asset) {
        receive(value, { ...value.progress, phase: "failed", session: result.session, asset: result.asset, variant: result.variant, issue: "rejected", retryable: true, cleanupConfirmed: false });
      } else {
        value.resume = result.kind === "uploaded";
        receive(value, { ...value.progress, phase: "outcome_unknown", session: result.session,
          asset: result.asset, variant: result.variant, issue: result.kind === "expired" || result.kind === "claimed" ? result.kind : result.kind === "missing" ? "unknown" : "processing", retryable: false });
      }
    } catch { if (current(value)) unknown(value); }
  };
  const resume = async () => {
    const value = operation;
    const { asset, session } = value?.progress || {};
    if (!value || !value.resume || !asset || !session || !options.canStart()) return;
    value.resume = false;
    value.abort = new AbortController();
    receive(value, { ...value.progress, phase: "converting" });
    await run(value, () => transport.convert(session, asset, usage(value.kind), {
      operationID: value.progress.operationID, signal: value.abort.signal, onProgress: progress => receive(value, progress),
    }));
  };
  const reset = () => {
    generation++;
    operation?.abort.abort();
    retainedSessionID = "";
    clear();
  };
  return Object.freeze({
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    configure(next: MediaUploadOwnerOptions) {
      if (options.ownerKey !== next.ownerKey) reset();
      options = next;
      if (next.sessionID) retainedSessionID = next.sessionID;
    },
    activate() { active = true; },
    dispose() { active = false; reset(); },
    isBlocked: () => Boolean(operation),
    start, retry, dismiss, check, resume, reset,
  });
}
