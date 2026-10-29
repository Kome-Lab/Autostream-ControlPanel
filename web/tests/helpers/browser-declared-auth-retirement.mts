import type { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import type { FetchRequestLifecycle } from "./browser-request-lifecycle.mts";
import type { StubResponse } from "./browser-harness.mts";

type Scope = NonNullable<ReturnType<BrowserNetworkCancelProof["ownedDocument"]>>;
type Current = { generation: number; frameId: string | undefined; closed: boolean };
type Input = { lifecycle: FetchRequestLifecycle; session: string; current: () => Current; assertHealthy: () => void };
type Message = { sessionId?: unknown; method?: unknown; params?: unknown };
type Values = Record<string, unknown>;
type Bound = {
  id: string; start: number; invalid: boolean; url: string; frame: string; loader: string;
  generation: number; starts: number; response: boolean; finished: boolean;
  cancel?: { order: number; exact: boolean; generation: number };
  pause?: { id: string; order: number; generation: number; frame: string; url: string; valid: boolean };
};
type Phase = { scope: Scope; url: string; origin: string; declaration: number; active: boolean; consumed: boolean; bound?: Bound };
type ReceiptData = { owner: object; id: string; generation: number; validate: () => boolean };
const receipts = new WeakMap<object, ReceiptData>();
const managers = new WeakMap<BrowserNetworkCancelProof, DeclaredAuthPhase>();
const documentOrigins = new WeakMap<Scope, string>();
const authStartHistory = new WeakMap<BrowserNetworkCancelProof, { loaders: Set<string>; invalid: boolean }>();
const values = (value: unknown): Values | undefined => value !== null && typeof value === "object" && !Array.isArray(value) ? value as Values : undefined;
const identifier = (value: unknown): value is string => typeof value === "string" && value.length > 0 && value.length <= 256;

function exactAuthURL(value: string) {
  const url = new URL(value);
  if (!["http:", "https:"].includes(url.protocol) || !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)
    || url.pathname !== "/auth/me" || url.search || url.hash || url.username || url.password || url.href !== value) {
    throw new TypeError("A canonical loopback AUTH_ME URL is required");
  }
  return url.href;
}

// A separate receipt family: it cannot be used as an existing Network ticket.
export function takeDeclaredAuthRetirement(value: unknown, owner: object, id: string, generation: number) {
  if (value === null || typeof value !== "object") return false;
  const receipt = receipts.get(value);
  receipts.delete(value);
  return !!receipt && receipt.owner === owner && receipt.id === id && receipt.generation === generation && receipt.validate();
}

class DeclaredAuthPhase {
  private readonly proof: BrowserNetworkCancelProof;
  readonly input: Input;
  private readonly seenScopes = new WeakSet<object>();
  private readonly seenLoaders = new Set<string>();
  private readonly networkIDs = new Set<string>();
  private readonly fetchIDs = new Set<string>();
  private readonly earlyTerminalIDs = new Set<string>();
  private phase: Phase | undefined;
  private order = 0;
  private declarations = 0;
  private disabled = false;

  constructor(proof: BrowserNetworkCancelProof, input: Input) { this.proof = proof; this.input = input; }

  private next() {
    if (this.order >= Number.MAX_SAFE_INTEGER) this.disabled = true;
    return ++this.order;
  }

  private live(phase: Phase) {
    this.input.assertHealthy();
    const current = this.input.current();
    return !this.disabled && phase.active && !current.closed && !this.proof.overflowed && !authStartHistory.get(this.proof)?.invalid
      && this.proof.ownedDocument(current) === phase.scope && documentOrigins.get(phase.scope) === phase.origin;
  }

  declare(url: string) {
    this.input.assertHealthy();
    const current = this.input.current(), scope = this.proof.ownedDocument(current), origin = new URL(url).origin;
    if (current.closed || !scope || this.disabled || this.phase?.active || this.seenScopes.has(scope) || this.seenLoaders.has(scope.loader)
      || authStartHistory.get(this.proof)?.invalid || authStartHistory.get(this.proof)?.loaders.has(scope.loader)
      || documentOrigins.get(scope) !== origin || !this.proof.canDeclareAuthBeforeStart(scope, url)) throw new Error("LoginReturn AUTH phase requires a fresh same-origin owned document scope before AUTH start");
    if (++this.declarations > 2048) { this.disabled = true; throw new Error("Declared AUTH phase capacity exceeded"); }
    this.seenScopes.add(scope);
    this.seenLoaders.add(scope.loader);
    const phase: Phase = { scope, url, origin, declaration: this.next(), active: true, consumed: false };
    this.phase = phase;
    this.earlyTerminalIDs.clear();
    return Object.freeze({ dispose: () => { phase.active = false; } });
  }

  observe(message: Message, generation: number) {
    const phase = this.phase;
    if (!phase?.active) return;
    try {
      if (!this.live(phase)) { phase.active = false; return; }
      if (message.sessionId !== this.input.session) return;
      const params = values(message.params);
      if (!params || !Number.isSafeInteger(generation) || generation !== phase.scope.generation) { phase.active = false; return; }
      if (!["Network.requestWillBeSent", "Network.loadingFailed", "Network.loadingFinished", "Network.responseReceived", "Fetch.requestPaused"].includes(String(message.method))) return;
      const order = this.next(), bound = phase.bound;
      if (message.method === "Network.requestWillBeSent") {
        const request = values(params.request);
        if (bound && params.requestId === bound.id) { bound.starts += 1; bound.invalid = true; return; }
        const id = params.requestId;
        if (!identifier(id)) { phase.active = false; return; }
        const reused = this.networkIDs.has(id);
        if (!reused && this.networkIDs.size >= 2048) { this.disabled = true; return; }
        this.networkIDs.add(id);
        // A distinct second start never replaces or invalidates the first ID.
        if (phase.consumed || request?.url !== phase.url) return;
        phase.consumed = true;
        const frame = params.frameId, loader = params.loaderId;
        if (!identifier(frame) || !identifier(loader)) { phase.active = false; return; }
        phase.bound = { id, frame, loader, url: phase.url, generation, start: order, starts: 1, response: false, finished: false,
          invalid: reused || this.earlyTerminalIDs.has(id) || request.method !== "GET" || params.type !== "Fetch"
            || params.redirectResponse !== undefined || frame !== phase.scope.frame || loader !== phase.scope.loader };
        return;
      }
      if (message.method === "Fetch.requestPaused") {
        if (!bound || params.networkId !== bound.id) return;
        const request = values(params.request), id = params.requestId, frame = params.frameId;
        if (bound.pause || !identifier(id) || !identifier(frame)) { bound.invalid = true; return; }
        if (this.fetchIDs.size >= 2048) { this.disabled = true; return; }
        const reused = this.fetchIDs.has(id);
        this.fetchIDs.add(id);
        bound.pause = { id, frame, order, generation, url: typeof request?.url === "string" ? request.url : "",
          valid: !reused && request?.method === "GET" && request.url === bound.url && frame === bound.frame && params.resourceType === "XHR"
            && params.responseStatusCode === undefined && params.responseErrorReason === undefined };
        return;
      }
      const id = params.requestId;
      if (!identifier(id)) { phase.active = false; return; }
      if (!bound || id !== bound.id) {
        if (!bound) {
          if (this.earlyTerminalIDs.size >= 2048) this.disabled = true;
          else this.earlyTerminalIDs.add(id);
        }
        return;
      }
      if (message.method === "Network.loadingFailed") {
        if (bound.cancel) bound.invalid = true;
        bound.cancel = { order, generation, exact: params.canceled === true && params.errorText === "net::ERR_ABORTED" };
      } else if (message.method === "Network.responseReceived") bound.response = true;
      else bound.finished = true;
    } catch { phase.active = false; }
  }

  retire(id: string, response: StubResponse | null) {
    const phase = this.phase, bound = phase?.bound, pause = bound?.pause, cancel = bound?.cancel;
    if (!phase || !bound || !pause || !cancel || pause.id !== id || !this.live(phase)
      || bound.invalid || !pause.valid || bound.starts !== 1 || !cancel.exact || bound.response || bound.finished
      || bound.generation !== phase.scope.generation || pause.generation !== bound.generation || cancel.generation !== bound.generation
      || !(phase.declaration < bound.start && bound.start < cancel.order && cancel.order < pause.order)
      || response === null || response.requiredResponse !== false || response.observeBootstrapCancellation !== true) return false;
    // This uses the original proof's attempted-ID set. A failed old claim cannot
    // be followed by this transition, nor can this transition authorize a retry.
    if (!this.proof.reserveDeclaredRetirementBoundary(id, phase.scope)) return false;
    const receipt = Object.freeze({});
    receipts.set(receipt, { owner: this.input.lifecycle, id, generation: bound.generation,
      validate: () => this.phase === phase && this.live(phase) && !bound.invalid && pause.valid
        && !bound.response && !bound.finished && bound.pause === pause && bound.cancel === cancel });
    this.input.lifecycle.completeDeclaredAuthRetirement(id, receipt);
    phase.active = false;
    return true;
  }
}

export function declareLoginReturnAuthPhase(proof: BrowserNetworkCancelProof | undefined, input: Input, url: string) {
  if (!proof || !proof.ownsLifecycle(input.lifecycle, input.session)) throw new Error("An owned Network proof is required for LoginReturn AUTH");
  const exactURL = exactAuthURL(url);
  let manager = managers.get(proof);
  if (!manager) { manager = new DeclaredAuthPhase(proof, input); managers.set(proof, manager); }
  if (manager.input.lifecycle !== input.lifecycle || manager.input.session !== input.session) throw new Error("Declared AUTH owner mismatch");
  return manager.declare(exactURL);
}

export function observeDeclaredAuthRetirement(proof: BrowserNetworkCancelProof, message: Message, generation: number) {
  // The existing parser has already processed this event. Keep only the origin
  // witnessed for that exact opaque owned scope; unknown URLs have no fallback.
  try {
    if (message.method === "Network.requestWillBeSent" && proof.ownsSession(message.sessionId)) {
      const params = values(message.params), request = values(params?.request);
      if (typeof request?.url === "string" && request.url.length <= 4096 && new URL(request.url).pathname === "/auth/me") {
        let history = authStartHistory.get(proof);
        if (!history) { history = { loaders: new Set(), invalid: false }; authStartHistory.set(proof, history); }
        const loader = params?.loaderId;
        if (!identifier(loader) || !Number.isSafeInteger(generation)) history.invalid = true;
        else if (!history.loaders.has(loader) && history.loaders.size >= 2048) history.invalid = true;
        else history.loaders.add(loader);
      }
    }
    if (message.method === "Page.frameNavigated" && proof.ownsSession(message.sessionId)) {
      const frame = values(values(message.params)?.frame);
      const scope = identifier(frame?.id) ? proof.ownedDocument({ generation, frameId: frame.id }) : undefined;
      if (scope && typeof frame?.url === "string" && frame.url.length <= 8192) {
        const url = new URL(frame.url);
        if (["http:", "https:"].includes(url.protocol)) documentOrigins.set(scope, url.origin);
      }
    }
  } catch { /* An unavailable origin witness cannot authorize a declaration. */ }
  managers.get(proof)?.observe(message, generation);
}

export function retireDeclaredAuth(proof: BrowserNetworkCancelProof | undefined, id: string, response: StubResponse | null) {
  return !!proof && (managers.get(proof)?.retire(id, response) ?? false);
}
