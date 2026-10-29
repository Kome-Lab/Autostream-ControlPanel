import type { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import type { FetchRequestLifecycle, FetchSettlementAttempt, FetchSettlementCommand } from "./browser-request-lifecycle.mts";

type Scope = NonNullable<ReturnType<BrowserNetworkCancelProof["ownedDocument"]>>;
type Current = { generation: number; frameId: string | undefined; closed: boolean };
export type InFlightOAuthContext = {
  owner: object; lifecycle: FetchRequestLifecycle; proof: BrowserNetworkCancelProof | undefined; session: string;
  current: () => Current; assertHealthy: () => void;
};
type Preparation = NonNullable<ReturnType<BrowserNetworkCancelProof["prepareInFlightOAuthCancellation"]>>;
type Entry = {
  context: InFlightOAuthContext; id: string; scope: Scope; origin: string; proof: Preparation;
  lifecycleLive: (attempt: FetchSettlementAttempt) => boolean; attempt?: FetchSettlementAttempt;
  command?: number; pending?: object; sent: boolean; sealed: boolean; eligible: boolean;
  invalid: boolean; disposed: boolean; wireResponses: number; error?: Error;
};
type State = { disabled: boolean; seen: Set<string>; commands: Set<number>; provisional?: Entry; pending: Map<number, Entry> };
const states = new WeakMap<object, State>();
const handles = new WeakMap<object, Entry>();
const rejected = new WeakMap<object, Entry>();
const attempts = new WeakMap<FetchSettlementAttempt, Entry>();
const origins = new WeakMap<Scope, string>();
const maxEntries = 2048;
const path = "/integrations/oauth-accounts";
const invalidID = "Invalid InterceptionId.";
const identifier = (value: unknown): value is string => typeof value === "string" && value.length > 0 && value.length <= 256;
const values = (value: unknown): Record<string, unknown> | undefined => value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;

// This records only the origin witnessed for the already-parsed owned scope.
// No opt-in, claim, clock tick or cancellation permission is created here.
export function observeInFlightOAuthDocument(proof: BrowserNetworkCancelProof, message: { sessionId?: unknown; method?: unknown; params?: unknown }, generation: number) {
  try {
    if (message.method !== "Page.frameNavigated" || !proof.ownsSession(message.sessionId)) return;
    const frame = values(values(message.params)?.frame);
    if (!frame || frame.parentId !== undefined && frame.parentId !== "" || !identifier(frame.loaderId)) return;
    const scope = identifier(frame?.id) ? proof.ownedDocument({ generation, frameId: frame.id }) : undefined;
    if (!scope || scope.loader !== frame.loaderId || scope.generation !== generation || scope.frame !== frame.id
      || typeof frame.url !== "string" || frame.url.length > 8192) return;
    const url = new URL(frame.url);
    if (["http:", "https:"].includes(url.protocol) && !url.username && !url.password) origins.set(scope, url.origin);
  } catch { /* Unknown origins cannot authorize an observation. */ }
}

function state(owner: object) {
  let value = states.get(owner);
  if (!value) { value = { disabled: false, seen: new Set(), commands: new Set(), pending: new Map() }; states.set(owner, value); }
  return value;
}

function live(entry: Entry) {
  entry.context.assertHealthy();
  const current = entry.context.current();
  return !entry.invalid && !entry.disposed && states.get(entry.context.owner)?.disabled === false && !current.closed
    && entry.context.proof?.ownedDocument(current) === entry.scope && origins.get(entry.scope) === entry.origin
    && !!entry.attempt && entry.lifecycleLive(entry.attempt);
}

function discard(entry: Entry) {
  entry.invalid = true;
  entry.disposed = true;
  entry.proof.discard();
  const value = states.get(entry.context.owner);
  if (value?.provisional === entry) value.provisional = undefined;
  if (entry.command !== undefined && value?.pending.get(entry.command) === entry) value.pending.delete(entry.command);
  if (entry.error) rejected.delete(entry.error);
  if (entry.attempt) attempts.delete(entry.attempt);
}

// This handle is a separate family from old Network tickets and declared reads.
// Even a failed opted-in preparation burns its Fetch ID; missing facts cannot be repaired later.
export function prepareInFlightOAuthCancellation(context: InFlightOAuthContext, id: string, command: FetchSettlementCommand, params: Record<string, unknown>, optedIn: boolean) {
  if (optedIn !== true) return;
  context.assertHealthy();
  const value = state(context.owner);
  if (value.disabled || !identifier(id) || value.seen.has(id)) return;
  if (value.seen.size >= maxEntries) { value.disabled = true; return; }
  value.seen.add(id);
  if (value.provisional || command !== "Fetch.fulfillRequest" || params.requestId !== id || !context.proof
    || !context.proof.ownsLifecycle(context.lifecycle, context.session)) return;
  const current = context.current(), scope = context.proof.ownedDocument(current);
  if (current.closed || !scope) return;
  const origin = origins.get(scope);
  if (!origin) return;
  const url = new URL(origin + path);
  if (!["http:", "https:"].includes(url.protocol) || !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)) return;
  const lifecycleLive = context.lifecycle.prepareInFlightOAuthCancellation(id);
  const proof = context.proof.prepareInFlightOAuthCancellation(id, current, url.href);
  if (!lifecycleLive || !proof) return;
  const entry: Entry = { context, id, scope, origin, proof, lifecycleLive, sent: false, sealed: false, eligible: false, invalid: false, disposed: false, wireResponses: 0 };
  const handle = Object.freeze({});
  handles.set(handle, entry);
  return handle;
}

export function armInFlightOAuthCancellation(handle: object | undefined, attempt: FetchSettlementAttempt) {
  if (!handle) return;
  const entry = handles.get(handle);
  if (!entry || entry.attempt || entry.disposed) return;
  entry.attempt = attempt;
  if (!live(entry) || !entry.proof.arm()) { discard(entry); return; }
  attempts.set(attempt, entry);
  state(entry.context.owner).provisional = entry;
}

// Called at actual command allocation, with the real pending-command object.
export function bindInFlightOAuthCommand(owner: object, id: number, pending: object | undefined, method: string, params: Record<string, unknown>, session: string | undefined) {
  const value = states.get(owner), entry = value?.provisional;
  if (!entry || !value) return;
  value.provisional = undefined;
  try {
    if (!live(entry) || !Number.isSafeInteger(id) || id < 1 || !pending || value.commands.has(id)
      || method !== "Fetch.fulfillRequest" || params.requestId !== entry.id || session !== entry.context.session
      || value.commands.size >= maxEntries) { discard(entry); if (value.commands.size >= maxEntries) value.disabled = true; return; }
    value.commands.add(id);
    entry.command = id;
    entry.pending = pending;
    value.pending.set(id, entry);
  } catch { discard(entry); }
}

// A synchronous socket throw or a synchronous response before normal return cannot qualify.
export function markInFlightOAuthSend(owner: object, id: number, pending: object | undefined) {
  const entry = states.get(owner)?.pending.get(id);
  if (!entry) return;
  try {
    if (entry.pending !== pending || entry.sent || entry.sealed || !live(entry) || !entry.proof.sent()) { discard(entry); return; }
    entry.sent = true;
  } catch { discard(entry); }
}

// Run before the Harness pending-map lookup, so a duplicate error received
// after that map's first deletion still invalidates an unconsumed receipt.
export function observeInFlightOAuthWire(owner: object, id: number) {
  const entry = states.get(owner)?.pending.get(id);
  if (!entry) return;
  entry.wireResponses += 1;
  if (entry.wireResponses !== 1) discard(entry);
}

// Must run at the actual matching CDP error response, before pending.reject.
// Returning the same Error preserves the undeclared/default error path.
export function sealInFlightOAuthRejection(owner: object, id: number, pending: object, error: Error, session: unknown) {
  const entry = states.get(owner)?.pending.get(id);
  if (!entry) return error;
  try {
    if (entry.pending !== pending || entry.sealed || !entry.sent || entry.wireResponses !== 1 || session !== entry.context.session || error.message !== invalidID || !live(entry)) { discard(entry); return error; }
    entry.sealed = true;
    entry.error = error;
    entry.eligible = entry.proof.seal();
    if (!entry.eligible) { discard(entry); return error; }
    rejected.set(error, entry);
  } catch { discard(entry); }
  return error;
}

export function discardInFlightOAuthCommand(owner: object, id: number, pending: object) {
  const entry = states.get(owner)?.pending.get(id);
  if (entry && entry.pending === pending) discard(entry);
}

// Taking burns before any owner/attempt checks. Copies and other receipt families have no entry.
export function takeInFlightOAuthCancellation(error: unknown, owner: object, attempt: FetchSettlementAttempt, validEntry: boolean) {
  const byError = error !== null && typeof error === "object" ? rejected.get(error) : undefined;
  const byAttempt = attempts.get(attempt);
  if (byError && byAttempt && byError !== byAttempt) { discard(byError); discard(byAttempt); return false; }
  const entry = byError || byAttempt;
  if (!entry) return false;
  if (entry.error) rejected.delete(entry.error);
  if (entry.attempt) attempts.delete(entry.attempt);
  try {
    if (!validEntry || entry.context.lifecycle !== owner || entry.attempt !== attempt || entry.error !== error
      || !entry.sealed || !entry.eligible || !entry.sent || !live(entry)) return false;
    return entry.proof.consume();
  } finally { discard(entry); }
}

export function disposeInFlightOAuthCancellation(handle: object | undefined) {
  if (!handle) return;
  const entry = handles.get(handle);
  handles.delete(handle);
  if (entry) discard(entry);
}
