import type { Stream } from "../../types/domain";

// Product metadata owned by ControlPanel. Revision is a record CAS value, not a
// replacement for a Run, Lifecycle Admission, Boot or Generation.
export type AutoStartHold = Readonly<{
  stream_id: string;
  known: boolean;
  held: boolean;
  revision: number;
  reason: string;
  actor_user_id: string;
  original_attempt_id: string;
  pending_attempt_id: string;
  created_at: string;
  updated_at: string;
}>;

export type AutoStartHoldSnapshot = Readonly<{ stream: Stream; hold: AutoStartHold }>;
export type AutoStartResumeRequest = Readonly<{
  request_id: string;
  expected_hold_revision: number;
  expected_stream_revision: string;
  expected_settings_revision: string;
  expected_run_id: string;
}>;

export type ResumePresentation = Readonly<{
  holdCommit: "known" | "unknown";
  condition: "met" | "not-met" | "unknown";
  start: "accepted" | "failed" | "not-started" | "pending" | "unknown";
  gate: "HELD" | "NOT_QUALIFIED" | "VERIFIED_FOR_PROFILE";
}>;
export type AutoStartResumeResponse = Readonly<{
  stream?: Stream;
  hold: AutoStartHold;
  attempt: Readonly<{
    request: AutoStartResumeRequest & Readonly<{ stream_id: string; operation?: string }>;
    hold: AutoStartHold;
    condition: unknown;
    hold_commit: unknown;
    start_result: unknown;
    gate: "HELD" | "NOT_QUALIFIED" | "VERIFIED_FOR_PROFILE";
  }>;
}>;

export type ResumeIdentity = Readonly<{ actorID: string; permissions: readonly string[] }>;
export type HoldAttemptProjection = Readonly<{
  requestID: string;
  operation: "manual_stop" | "manual_force_stop" | "explicit_resume";
  holdCommit: "known" | "unknown";
  historicalHold: AutoStartHold;
  gate: "HELD" | "NOT_QUALIFIED" | "VERIFIED_FOR_PROFILE";
  physicalResult: "unknown";
}>;
export type ResumeOpened = Readonly<{
  snapshot: AutoStartHoldSnapshot;
  actorID: string;
  fingerprint: string;
}>;
export type ResumeResult =
  | Readonly<{ kind: "opened"; opened: ResumeOpened }>
  | Readonly<{ kind: "observed"; requestID: string; response: AutoStartResumeResponse; presentation: ResumePresentation; unresolved: boolean }>
  | Readonly<{ kind: "blocked"; reason: "permission" | "unavailable" | "changed" | "pending" | "cancelled" | "request-id-unavailable" }>
  | Readonly<{ kind: "unknown"; requestID: string }>;

export type AutoStartResumeDependencies = Readonly<{
  streamID: string;
  readSnapshot: (signal: AbortSignal) => Promise<unknown>;
  readIdentity: (signal: AbortSignal) => Promise<ResumeIdentity>;
  // Private browser session marker; it is compared only in memory and is never
  // included in a request, journal, fingerprint, log or display projection.
  currentIdentity: () => Readonly<{ actorID: string; sessionMarker: string }> | undefined;
  newRequestID: () => string;
  send: (request: AutoStartResumeRequest, signal: AbortSignal) => Promise<unknown>;
  inspect: (requestID: string, signal: AbortSignal) => Promise<unknown>;
  onAttempt?: (requestID: string | null) => void;
}>;

const object = (value: unknown): Record<string, unknown> | undefined => value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
const text = (value: unknown, max = 4096): value is string => typeof value === "string" && value.length <= max && !/[\u0000-\u001f\u007f]/u.test(value);
const requestID = (value: unknown): value is string => text(value, 80) && value.length > 0 && /^[A-Za-z0-9_.:-]+$/.test(value);
const revision = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) > 0;
const optionalText = (value: unknown) => value === undefined || value === null || text(value);

export function isAutoStartHold(value: unknown, streamID: string): value is AutoStartHold {
  const row = object(value);
  return Boolean(row && row.stream_id === streamID && typeof row.known === "boolean" && typeof row.held === "boolean" && revision(row.revision)
    && ["reason", "actor_user_id", "original_attempt_id", "pending_attempt_id", "created_at", "updated_at"].every(key => optionalText(row[key])));
}

export function isAutoStartHoldSnapshot(value: unknown, streamID: string): value is AutoStartHoldSnapshot {
  const row = object(value), stream = object(row?.stream);
  return Boolean(row && stream && stream.id === streamID && text(stream.status) && text(stream.updated_at) && stream.updated_at.length > 0
    && text(stream.settings_revision) && stream.settings_revision.length > 0 && optionalText(stream.archive_run_id)
    && isAutoStartHold(row.hold, streamID));
}

export function autoStartHoldFingerprint(snapshot: AutoStartHoldSnapshot): string {
  const { stream, hold } = snapshot;
  return JSON.stringify([stream.id, stream.status, stream.updated_at, stream.settings_revision, stream.archive_run_id || "", stream.auto_start_trigger || "",
    stream.selected_encoder_id || "", stream.selected_worker_id || "", stream.selected_discord_id || "", stream.assigned_encoder_id || "", stream.assigned_worker_id || "",
    hold.known, hold.held, hold.revision, hold.pending_attempt_id || ""]);
}

export function canResumeAutoStart(identity: ResumeIdentity | undefined): boolean {
  return Boolean(identity && text(identity.actorID, 256) && identity.actorID.length > 0 && Array.isArray(identity.permissions)
    && identity.permissions.every(p => text(p, 256)) && (identity.permissions.includes("*") || identity.permissions.includes("streams.start")));
}

export function canInspectManualHold(identity: ResumeIdentity | undefined): boolean {
  return Boolean(identity && text(identity.actorID, 256) && identity.actorID.length > 0 && Array.isArray(identity.permissions)
    && identity.permissions.every(p => text(p, 256)) && (identity.permissions.includes("*") || identity.permissions.includes("streams.read")));
}

function detached(snapshot: AutoStartHoldSnapshot): AutoStartHoldSnapshot {
  return Object.freeze({ stream: Object.freeze({ ...snapshot.stream }), hold: Object.freeze({ ...snapshot.hold }) });
}

function boundary(value: unknown, streamID: string, kind: "CP_LIFECYCLE" | "PROJECTION", subBoundary: "RECEIPT_SAVE" | "CP_OUTCOME"): string | undefined {
  const row = object(value), address = object(row?.Address), resource = object(address?.Resource), fact = object(row?.Outcome), observation = object(fact?.Observation);
  if (!row || row.Knowledge !== "KNOWN" || row.Presence !== "PRESENT" || !address || address.Kind !== kind || address.SubBoundary !== subBoundary
    || !text(address.OwnerID) || !address.OwnerID || !resource || resource.Kind !== "STREAM" || resource.OwnerID !== address.OwnerID || resource.ResourceID !== streamID
    || fact?.State !== "OBSERVED" || !text(fact.Value) || !observation || observation.OwnerID !== address.OwnerID || observation.Scope !== streamID
    || !text(observation.FrameRef) || !observation.FrameRef || !revision(observation.SourceRevision)) return undefined;
  return ["SUCCESS_AT_BOUNDARY", "FAILURE_AT_BOUNDARY", "NO_EFFECT_PROVEN", "PENDING"].includes(fact.Value) ? fact.Value : undefined;
}

// This generic read proves only the original Hold record boundary. It cannot
// settle a physical Stop/Start or manufacture a new operation from its ID.
export function projectHoldAttempt(value: unknown, streamID: string, id: string): HoldAttemptProjection | undefined {
  const row = object(value), attempt = object(row?.attempt), request = object(attempt?.request);
  if (!requestID(id) || !row || !attempt || !request || request.request_id !== id || request.stream_id !== streamID
    || !isAutoStartHold(row.hold, streamID) || !isAutoStartHold(attempt.hold, streamID)
    || typeof request.operation !== "string" || !["manual_stop", "manual_force_stop", "explicit_resume"].includes(request.operation)
    || typeof attempt.gate !== "string" || !["HELD", "NOT_QUALIFIED", "VERIFIED_FOR_PROFILE"].includes(attempt.gate)
    || row.stream !== undefined && object(row.stream)?.id !== streamID) return undefined;
  const heldAsRequested = request.operation === "explicit_resume" ? !attempt.hold.held : attempt.hold.held;
  return Object.freeze({ requestID: id, operation: request.operation as HoldAttemptProjection["operation"],
    holdCommit: boundary(attempt.hold_commit, streamID, "CP_LIFECYCLE", "RECEIPT_SAVE") === "SUCCESS_AT_BOUNDARY" && attempt.hold.known && heldAsRequested ? "known" : "unknown",
    historicalHold: Object.freeze({ ...attempt.hold }), gate: attempt.gate as HoldAttemptProjection["gate"], physicalResult: "unknown" });
}

export function resumeResultPresentation(value: AutoStartResumeResponse, streamID: string): ResumePresentation {
  const condition = object(value.attempt.condition), observed = object(condition?.Observation);
  const conditionValue = condition?.State === "OBSERVED" && typeof condition.Value === "boolean" && observed?.Scope === streamID && revision(observed.SourceRevision)
    ? condition.Value : undefined;
  const committed = boundary(value.attempt.hold_commit, streamID, "CP_LIFECYCLE", "RECEIPT_SAVE"), started = boundary(value.attempt.start_result, streamID, "PROJECTION", "CP_OUTCOME");
  return Object.freeze({
    holdCommit: committed === "SUCCESS_AT_BOUNDARY" && value.attempt.hold.known && !value.attempt.hold.held ? "known" : "unknown",
    condition: conditionValue === undefined ? "unknown" : conditionValue ? "met" : "not-met",
    start: started === "SUCCESS_AT_BOUNDARY" ? "accepted" : started === "FAILURE_AT_BOUNDARY" ? "failed" : started === "NO_EFFECT_PROVEN" ? "not-started" : started === "PENDING" ? "pending" : "unknown",
    gate: value.attempt.gate,
  });
}

function validResponse(value: unknown, streamID: string, id: string, original?: AutoStartResumeRequest): value is AutoStartResumeResponse {
  const row = object(value), attempt = object(row?.attempt), request = object(attempt?.request), hold = row?.hold;
  if (!row || !attempt || !request || request.stream_id !== streamID || request.request_id !== id || !isAutoStartHold(hold, streamID)
    || !isAutoStartHold(attempt.hold, streamID) || typeof attempt.gate !== "string" || !["HELD", "NOT_QUALIFIED", "VERIFIED_FOR_PROFILE"].includes(attempt.gate)) return false;
  if (row.stream !== undefined && object(row.stream)?.id !== streamID) return false;
  if (original && Object.entries(original).some(([key, expected]) => request[key] !== expected)) return false;
  return true;
}

function resolved(presentation: ResumePresentation): boolean {
  return presentation.gate !== "HELD" && presentation.holdCommit === "known" && presentation.condition !== "unknown"
    && (presentation.start === "accepted" || presentation.start === "failed" || presentation.start === "not-started");
}

// This controller owns UI intent/transport correlation, not a Lifecycle state
// machine. Mutation, inspection and result knowledge remain server-owned.
export function createAutoStartResumeController(deps: AutoStartResumeDependencies) {
  let epoch = 0, disposed = false, active: AbortController | undefined;
  let pending: AutoStartResumeRequest | undefined, recovered: string | undefined;
  const openedObjects = new WeakMap<object, Readonly<{ marker: string; epoch: number }>>();
  const blocked = (reason: Extract<ResumeResult, { kind: "blocked" }>["reason"]): ResumeResult => Object.freeze({ kind: "blocked", reason });
  const current = (generation: number, operation: AbortController) => !disposed && epoch === generation && active === operation && !operation.signal.aborted;
  const acquire = () => { if (disposed || active) return undefined; const operation = new AbortController(); active = operation; return { operation, generation: epoch }; };
  const finish = (operation: AbortController) => { if (active === operation) active = undefined; };
  const identityMatches = (actorID: string, marker: string) => { const now = deps.currentIdentity(); return now?.actorID === actorID && now.sessionMarker === marker; };
  const observe = (value: unknown, id: string): ResumeResult => {
    if (!validResponse(value, deps.streamID, id, pending)) return Object.freeze({ kind: "unknown", requestID: id });
    const presentation = resumeResultPresentation(value, deps.streamID), unresolved = !resolved(presentation);
    if (!unresolved) { pending = undefined; recovered = undefined; deps.onAttempt?.(null); }
    return Object.freeze({ kind: "observed", requestID: id, response: value, presentation, unresolved });
  };
  return {
    get pendingRequestID() { return pending?.request_id || recovered; },
    async open(): Promise<ResumeResult> {
      if (pending || recovered) return blocked("pending");
      const owned = acquire(); if (!owned) return blocked("pending");
      const { operation, generation } = owned, marker = deps.currentIdentity();
      if (!marker) { finish(operation); return blocked("permission"); }
      try {
        const [value, identity] = await Promise.all([deps.readSnapshot(operation.signal), deps.readIdentity(operation.signal)]);
        if (!current(generation, operation)) return blocked("cancelled");
        if (!identityMatches(identity.actorID, marker.sessionMarker) || !canResumeAutoStart(identity)) return blocked("permission");
        if (!isAutoStartHoldSnapshot(value, deps.streamID)) return blocked("unavailable");
        if (value.hold.pending_attempt_id) return blocked("pending");
        if (value.hold.known && !value.hold.held) return blocked("changed");
        const snapshot = detached(value), opened = Object.freeze({ snapshot, actorID: identity.actorID, fingerprint: autoStartHoldFingerprint(snapshot) });
        openedObjects.set(opened, Object.freeze({ marker: marker.sessionMarker, epoch }));
        return Object.freeze({ kind: "opened", opened });
      } catch { return blocked(current(generation, operation) ? "unavailable" : "cancelled"); }
      finally { finish(operation); }
    },
    async confirm(opened: ResumeOpened): Promise<ResumeResult> {
      if (pending || recovered) return blocked("pending");
      const origin = openedObjects.get(opened), marker = origin?.marker;
      if (!origin || origin.epoch !== epoch || marker === undefined || !identityMatches(opened.actorID, marker)) return blocked("changed");
      const owned = acquire(); if (!owned) return blocked("pending");
      const { operation, generation } = owned;
      try {
        const [value, identity] = await Promise.all([deps.readSnapshot(operation.signal), deps.readIdentity(operation.signal)]);
        if (!current(generation, operation)) return blocked("cancelled");
        if (!canResumeAutoStart(identity) || identity.actorID !== opened.actorID || !identityMatches(opened.actorID, marker)) return blocked("permission");
        if (!isAutoStartHoldSnapshot(value, deps.streamID)) return blocked("unavailable");
        if (autoStartHoldFingerprint(value) !== opened.fingerprint) return blocked("changed");
        const id = deps.newRequestID();
        if (!requestID(id)) return blocked("request-id-unavailable");
        pending = Object.freeze({ request_id: id, expected_hold_revision: opened.snapshot.hold.revision,
          expected_stream_revision: opened.snapshot.stream.updated_at!, expected_settings_revision: opened.snapshot.stream.settings_revision!, expected_run_id: opened.snapshot.stream.archive_run_id || "" });
        deps.onAttempt?.(id);
        openedObjects.delete(opened);
        let valueAfterSend: unknown;
        try { valueAfterSend = await deps.send(pending, operation.signal); }
        catch { return Object.freeze({ kind: "unknown", requestID: id }); }
        if (!current(generation, operation) || !identityMatches(opened.actorID, marker)) return Object.freeze({ kind: "unknown", requestID: id });
        return observe(valueAfterSend, id);
      } catch { return blocked(current(generation, operation) ? "unavailable" : "cancelled"); }
      finally { finish(operation); }
    },
    // Recovery attaches only a server/client-persisted original ID. It cannot
    // manufacture a mutation request or convert a generic Stream GET to a receipt.
    recover(id: string) { if (!pending && requestID(id)) { recovered = id; deps.onAttempt?.(id); } },
    async inspect(): Promise<ResumeResult> {
      const id = pending?.request_id || recovered;
      if (!id) return blocked("unavailable");
      const owned = acquire(); if (!owned) return blocked("pending");
      const { operation, generation } = owned, marker = deps.currentIdentity();
      if (!marker) { finish(operation); return blocked("permission"); }
      try {
        const identity = await deps.readIdentity(operation.signal);
        if (!current(generation, operation)) return blocked("cancelled");
        if (!canResumeAutoStart(identity) || !identityMatches(identity.actorID, marker.sessionMarker)) return blocked("permission");
        const value = await deps.inspect(id, operation.signal);
        if (!current(generation, operation) || !identityMatches(identity.actorID, marker.sessionMarker)) return blocked("cancelled");
        return observe(value, id);
      } catch { return Object.freeze({ kind: "unknown", requestID: id }); }
      finally { finish(operation); }
    },
    cancel() { epoch++; active?.abort(); active = undefined; },
    dispose() { disposed = true; epoch++; active?.abort(); active = undefined; },
  };
}
