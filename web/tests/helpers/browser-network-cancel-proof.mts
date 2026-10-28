type ObjectValue = Record<string, unknown>;
type Owner = object & { assertHealthy(): void };
type Command = "Fetch.continueRequest" | "Fetch.fulfillRequest";
type DocumentScope = Readonly<{ frame: string; loader: string; generation: number; order: number }>;
type Network = {
  starts: number; invalid: boolean; pauseIDs: Set<string>; used: boolean;
  order?: number; generation?: number; frame?: string; loader?: string;
  url?: string; method?: string; type?: string; response: boolean; finished: boolean;
  failure?: Readonly<{ order: number; generation: number; canceled: boolean; aborted: boolean }>;
};
type Pause = {
  invalid: boolean; order: number; generation: number; networkID?: string;
  frame?: string; url?: string; path?: string; method?: string; type?: string;
  scope?: DocumentScope; contract?: Readonly<{ order: number; generation: number; path: string; optionalFixture: boolean }>;
  firstClaimDiagnostic?: NetworkBoundaryDiagnostic;
};
type Tri = boolean | null;
type DiagnosticBit = 0 | 1;
type DiagnosticTri = DiagnosticBit | null;
type NetworkBoundaryDiagnostic = Readonly<{
  at:number;
  present:readonly [pause:DiagnosticBit,network:DiagnosticBit,scope:DiagnosticBit,contract:DiagnosticBit,cancel:DiagnosticBit];
  counts:readonly [starts:number,links:number];
  orders:readonly [scope:number|null,start:number|null,pause:number|null,contract:number|null,cancel:number|null];
  same:readonly [link:DiagnosticTri,scope:DiagnosticTri,frame:DiagnosticTri,loader:DiagnosticTri,generation:DiagnosticTri,
    failure_generation:DiagnosticTri,url:DiagnosticTri,method:DiagnosticTri,types:DiagnosticTri,contract_path:DiagnosticTri];
  flags:readonly [disabled:DiagnosticBit,pause_invalid:DiagnosticTri,network_invalid:DiagnosticTri,used:DiagnosticTri,
    optional:DiagnosticTri,canceled:DiagnosticTri,aborted:DiagnosticTri,response:DiagnosticTri,finished:DiagnosticTri];
}>;
// v2 tuple slots are named above; 1/0/null mean true/false/unobserved.
const diagnosticTuple = <const T extends readonly unknown[]>(...values:T):Readonly<T> => Object.freeze(values);
const diagnosticBit = (value:boolean):DiagnosticBit => value?1:0;
const diagnosticTri = (value:Tri):DiagnosticTri => value===null?null:diagnosticBit(value);
const diagnosticReadRoutes = [
  ["/auth/me","AUTH_ME"],["/setup/status","SETUP_STATUS"],["/settings/app","APP_SETTINGS"],
  ["/auth/oauth/providers","AUTH_OAUTH_PROVIDERS"],["/auth/mfa/status","AUTH_MFA_STATUS"],
  ["/auth/passkeys","AUTH_PASSKEYS"],["/auth/oauth-links","AUTH_OAUTH_LINKS"],
  ["/integrations/oauth-accounts","OAUTH_ACCOUNTS"],["/archive/destinations","ARCHIVE_DESTINATIONS"],
  ["/discord/configs","DISCORD_CONFIGS"],["/youtube/outputs","YOUTUBE_OUTPUTS"],
  ["/profiles/archive","ARCHIVE_PROFILES"],["/profiles/overlay","OVERLAY_PROFILES"],
  ["/account/preferences/ui","UI_PREFERENCES"],["/streams","STREAMS"],["/workers","WORKERS"],
  ["/nodes","NODES"],["/observability/incidents","INCIDENTS"],["/service-health","SERVICE_HEALTH"],["/version","VERSION"],
] as const;
type DiagnosticRoute = typeof diagnosticReadRoutes[number][1] | "NEXT_STATIC" | "ADMIN_TEXT" | "ADMIN_JSON" | "ADMIN_ROUTE" | "FAVICON" | "OTHER";
const diagnosticRoutes = new Map<string,DiagnosticRoute>(diagnosticReadRoutes);
const diagnosticRoute = (path:string|undefined):DiagnosticRoute => {
  const known=path&&diagnosticRoutes.get(path);if(known)return known;
  if(path?.startsWith("/_next/static/"))return "NEXT_STATIC";
  if(path?.startsWith("/admin/")&&path.endsWith(".txt"))return "ADMIN_TEXT";
  if(path?.startsWith("/admin/")&&path.endsWith(".json"))return "ADMIN_JSON";
  if(path==="/admin"||path?.startsWith("/admin/"))return "ADMIN_ROUTE";
  return path==="/favicon.ico"?"FAVICON":"OTHER";
};
const diagnosticMethod = (method:string|undefined) => method==="GET"||method==="HEAD"?method:["POST","PUT","PATCH","DELETE"].includes(method||"")?"WRITE":"OTHER";
type NetworkFailureDiagnostic = Readonly<{
  schema:"network-cancel-boundary-v2";route:DiagnosticRoute;method:ReturnType<typeof diagnosticMethod>;
  matches:readonly [owner:DiagnosticBit,session:DiagnosticBit,allowed_path:DiagnosticBit];
  first:NetworkBoundaryDiagnostic|null;failure:NetworkBoundaryDiagnostic;
}>;
const diagnosticEqual = (...values:unknown[]):Tri => values.some(value=>value===undefined)?null:values.every(value=>value===values[0]);
const diagnosticData = (value:object,key:string):unknown => {
  const descriptor=Object.getOwnPropertyDescriptor(value,key);return descriptor&&"value"in descriptor?descriptor.value:undefined;
};
declare const ticketBrand: unique symbol;
export type NetworkCancellationTicket = Readonly<{ [ticketBrand]: true }>;
type TicketData = Readonly<{ owner: object; id: string; generation: number; path: string; validate: () => boolean }>;
const tickets = new WeakMap<object, TicketData>();
const optionalFixturePaths = new Set(["/integrations/oauth-accounts", "/integrations/oauth-providers", "/roles", "/nodes", "/workers", "/auth/me"]);
const object = (value: unknown): ObjectValue | undefined => value !== null && typeof value === "object" && !Array.isArray(value) ? value as ObjectValue : undefined;
const identifier = (value: unknown) => typeof value === "string" && value.length > 0 && value.length <= 256 ? value : undefined;
const generationNumber = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) >= 0;
function requestURL(value: unknown) {
  if (typeof value !== "string" || value.length > 4096) return;
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:" ? { raw: value, path: url.pathname.replace(/\/$/, "") || "/" } : undefined;
  } catch { return; }
}

// Tickets contain no enumerable IDs/URLs. Taking one burns it, even on a wrong
// owner/context, so a failed terminal attempt cannot reuse the witness.
export function takeNetworkCancellationTicket(value: unknown, owner: object, id: string, generation: number, command: Command, path: string | undefined) {
  if (value === null || typeof value !== "object") return false;
  const ticket = tickets.get(value);
  tickets.delete(value);
  if (!ticket || command !== "Fetch.fulfillRequest" || ticket.owner !== owner || ticket.id !== id || ticket.generation !== generation || ticket.path !== path) return false;
  try { return ticket.validate(); } catch { return false; }
}

// This proves an already-terminal browser cancellation BEFORE the first send.
// It never accepts settlement errors or changes the navigation-error rule.
export class BrowserNetworkCancelProof {
  private readonly networks = new Map<string, Network>();
  private readonly pauses = new Map<string, Pause>();
  private readonly attempted = new Set<string>();
  private scope: DocumentScope | undefined;
  private order = 0;
  private disabled = false;
  private readonly owner: Owner;
  private readonly session: string;
  private readonly limit: number;

  constructor(owner: Owner, session: string, limit = 2048) {
    if (!identifier(session) || !Number.isSafeInteger(limit) || limit < 1 || limit > 2048) throw new TypeError("Invalid bounded Network cancellation proof owner");
    this.owner = owner; this.session = session; this.limit = limit;
  }
  get overflowed() { return this.disabled; }
  private diagnosticSnapshot(id:string,current:{generation:number;frameId:string|undefined},at:number):NetworkBoundaryDiagnostic {
    const pause=this.pauses.get(id),scope=this.scope,network=pause?.networkID?this.networks.get(pause.networkID):undefined;
    const contract=pause?.contract,cancel=network?.failure;
    const generation=diagnosticData(current,"generation"),frame=diagnosticData(current,"frameId");
    return Object.freeze({at,
      present:diagnosticTuple(diagnosticBit(!!pause),diagnosticBit(!!network),diagnosticBit(!!scope),diagnosticBit(!!contract),diagnosticBit(!!cancel)),
      counts:diagnosticTuple(network?.starts||0,network?.pauseIDs.size||0),
      orders:diagnosticTuple(scope?.order??null,network?.order??null,pause?.order??null,contract?.order??null,cancel?.order??null),
      same:diagnosticTuple(diagnosticTri(network&&pause?network.pauseIDs.size===1&&network.pauseIDs.has(id):null),
        diagnosticTri(pause?.scope&&scope?pause.scope===scope:null),
        diagnosticTri(diagnosticEqual(pause?.frame,network?.frame,scope?.frame,frame)),
        diagnosticTri(diagnosticEqual(network?.loader,pause?.scope?.loader,scope?.loader)),
        diagnosticTri(diagnosticEqual(pause?.generation,network?.generation,scope?.generation,contract?.generation,generation)),
        diagnosticTri(diagnosticEqual(cancel?.generation,generation)),diagnosticTri(diagnosticEqual(pause?.url,network?.url)),diagnosticTri(diagnosticEqual(pause?.method,network?.method)),
        diagnosticTri(pause&&network?pause.type==="XHR"&&network.type==="Fetch":null),diagnosticTri(diagnosticEqual(contract?.path,pause?.path))),
      flags:diagnosticTuple(diagnosticBit(this.disabled),diagnosticTri(pause?.invalid??null),diagnosticTri(network?.invalid??null),diagnosticTri(network?.used??null),
        diagnosticTri(contract?.optionalFixture??null),diagnosticTri(cancel?.canceled??null),diagnosticTri(cancel?.aborted??null),diagnosticTri(network?.response??null),diagnosticTri(network?.finished??null))});
  }

  // Read-only metadata. No clock tick, health probe, ticket use or acceptance.
  failureDiagnostic(id:string,owner:object,session:string,current:{generation:number;frameId:string|undefined}):NetworkFailureDiagnostic|undefined {
    try {
      const pause=this.pauses.get(id);
      return Object.freeze({schema:"network-cancel-boundary-v2",route:diagnosticRoute(pause?.path),method:diagnosticMethod(pause?.method),
        matches:diagnosticTuple(diagnosticBit(this.owner===owner),diagnosticBit(this.session===session),diagnosticBit(!!pause?.path&&optionalFixturePaths.has(pause.path))),
        first:pause?.firstClaimDiagnostic??null,failure:this.diagnosticSnapshot(id,current,this.order)});
    } catch { return; }
  }
  private next() {
    if (this.order >= Number.MAX_SAFE_INTEGER) { this.disabled = true; return this.order; }
    return ++this.order;
  }
  private network(id: string) {
    let value = this.networks.get(id);
    if (!value) {
      if (this.networks.size >= this.limit) { this.disabled = true; return; }
      value = { starts: 0, invalid: false, pauseIDs: new Set(), used: false, response: false, finished: false };
      this.networks.set(id, value);
    }
    return value;
  }

  observe(message: { sessionId?: unknown; method?: unknown; params?: unknown }, generation: number) {
    try {
      if (message.sessionId !== this.session) return;
      const params = object(message.params);
      if (!params || !generationNumber(generation)) { this.disabled = true; return; }
      if (message.method === "Page.frameDetached") {
        const frame = identifier(params.frameId);
        if (!frame || frame === this.scope?.frame) { this.next(); this.scope = undefined; }
        return;
      }
      if (message.method === "Page.frameNavigated") {
        const frame = object(params.frame);
        if (!frame || frame.parentId !== undefined && typeof frame.parentId !== "string") {
          this.next(); this.scope = undefined; return;
        }
        if (typeof frame.parentId === "string" && frame.parentId !== "") {
          const id = identifier(frame.id), parent = identifier(frame.parentId);
          if (id && parent && id !== parent && id !== this.scope?.frame) return;
          this.next(); this.scope = undefined; return;
        }
        const id = identifier(frame.id), loader = identifier(frame.loaderId), order = this.next();
        this.scope = id && loader ? Object.freeze({ frame: id, loader, generation, order }) : undefined;
        return;
      }
      if (message.method === "Fetch.requestPaused") {
        const id = identifier(params.requestId), request = object(params.request), url = requestURL(request?.url), order = this.next();
        if (!id) { this.disabled = true; return; }
        const prior = this.pauses.get(id);
        if (prior) { prior.invalid = true; return; }
        if (this.pauses.size >= this.limit) { this.disabled = true; return; }
        const networkID = identifier(params.networkId), frame = identifier(params.frameId);
        const pause: Pause = { order, generation, networkID, frame, url: url?.raw, path: url?.path, method: typeof request?.method === "string" ? request.method : undefined,
          type: typeof params.resourceType === "string" ? params.resourceType : undefined, scope: this.scope,
          invalid: !request || !url || !networkID || !frame || !this.scope || params.responseStatusCode !== undefined || params.responseErrorReason !== undefined };
        this.pauses.set(id, pause);
        if (networkID) {
          const network = this.network(networkID);
          if (network) { network.pauseIDs.add(id); if (network.pauseIDs.size !== 1) network.invalid = true; }
        }
        return;
      }
      if (!["Network.requestWillBeSent", "Network.loadingFailed", "Network.loadingFinished", "Network.responseReceived"].includes(String(message.method))) return;
      const id = identifier(params.requestId), order = this.next();
      if (!id) { this.disabled = true; return; }
      const value = this.network(id);
      if (!value) return;
      if (message.method === "Network.requestWillBeSent") {
        value.starts += 1;
        if (value.starts !== 1 || value.failure || value.response || value.finished || params.redirectResponse !== undefined) { value.invalid = true; return; }
        const request = object(params.request), url = requestURL(request?.url);
        value.order = order; value.generation = generation;
        value.frame = identifier(params.frameId); value.loader = identifier(params.loaderId);
        value.url = url?.raw; value.method = typeof request?.method === "string" ? request.method : undefined;
        value.type = typeof params.type === "string" ? params.type : undefined;
        if (!url || !value.frame || !value.loader) value.invalid = true;
      } else if (message.method === "Network.loadingFailed") {
        if (value.failure) value.invalid = true;
        value.failure = Object.freeze({ order, generation, canceled: params.canceled === true, aborted: params.errorText === "net::ERR_ABORTED" });
      } else if (message.method === "Network.responseReceived") {
        value.response = true;
      } else {
        value.finished = true;
      }
    } catch { this.disabled = true; }
  }

  registerContract(id: string, input: { generation: number; pathname: string; optionalFixture: boolean }) {
    try {
      const order = this.next(), pause = this.pauses.get(id);
      if (!pause) return;
      if (pause.contract || !generationNumber(input.generation)) { pause.invalid = true; return; }
      pause.contract = Object.freeze({ order, generation: input.generation, path: input.pathname, optionalFixture: input.optionalFixture === true });
    } catch { this.disabled = true; }
  }

  claimBeforeSettlement(id: string, command: Command, current: { generation: number; frameId: string | undefined }): NetworkCancellationTicket | undefined {
    try {
      this.owner.assertHealthy();
      if (this.attempted.has(id)) return;
      if (this.attempted.size >= this.limit) { this.disabled = true; return; }
      this.attempted.add(id);
      const boundary = this.next(), pause = this.pauses.get(id), scope = this.scope;
      const network = pause?.networkID ? this.networks.get(pause.networkID) : undefined;
      const contract = pause?.contract, failure = network?.failure;
      try { if(pause&&!pause.firstClaimDiagnostic)pause.firstClaimDiagnostic=this.diagnosticSnapshot(id,current,boundary); }
      catch { /* Metadata collection cannot change the original claim or send. */ }
      if (this.disabled || command !== "Fetch.fulfillRequest" || !pause || !network || !scope || !contract || !failure
        || pause.invalid || network.invalid || network.used || network.starts !== 1 || network.pauseIDs.size !== 1 || !network.pauseIDs.has(id)
        || !contract.optionalFixture || !pause.path || !optionalFixturePaths.has(pause.path) || contract.path !== pause.path
        || pause.method !== "GET" || network.method !== "GET" || pause.type !== "XHR" || network.type !== "Fetch"
        || !pause.url || pause.url !== network.url || pause.scope !== scope || pause.frame !== scope.frame || network.frame !== scope.frame
        || current.frameId !== scope.frame || network.loader !== scope.loader
        || pause.generation !== current.generation || network.generation !== current.generation || failure.generation !== current.generation
        || scope.generation !== current.generation || contract.generation !== current.generation
        || !failure.canceled || !failure.aborted || network.response || network.finished
        || network.order === undefined || !(scope.order < pause.order && scope.order < network.order && network.order < failure.order
          && pause.order < failure.order && contract.order < failure.order && failure.order < boundary)) return;
      network.used = true;
      const ticket = Object.freeze({}) as NetworkCancellationTicket;
      const data: TicketData = Object.freeze({ owner: this.owner, id, generation: current.generation, path: pause.path,
        validate: () => !this.disabled && this.order === boundary && this.scope === scope && !pause.invalid && !network.invalid
          && network.used && !network.response && !network.finished && network.pauseIDs.size === 1 && network.pauseIDs.has(id) });
      tickets.set(ticket, data);
      return ticket;
    } catch { return; }
  }
}
