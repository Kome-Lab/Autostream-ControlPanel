export const invalidInterceptionIdMessage = "Invalid InterceptionId.";

export type FetchSettlementCommand = "Fetch.continueRequest" | "Fetch.fulfillRequest";
export type NavigationCancellationReason = "navigate" | "reload" | "top-level-navigation" | "teardown" | "close";

export type FetchRequestRegistration = {
  requestId: string;
  method: string;
  pathname: string;
  requiredResponse: boolean;
};

export type RequestHandlerFilter = {
  pathname?: string;
  method?: string;
};

export type RequestHandlerIdleOptions = RequestHandlerFilter & {
  timeoutMs?: number;
};

export type FetchSettlementAttempt = Readonly<{
  requestId: string;
  command: FetchSettlementCommand;
  attempt: number;
}>;

type ActiveFetchRequest = FetchRequestRegistration & {
  generation: number;
  settlementAttemptCount: number;
  settlementCommand?: FetchSettlementCommand;
  cancellationContext?: {
    generation: number;
    reason: NavigationCancellationReason;
  };
  terminalState: "active" | "settling";
};

type TerminalFetchRequest = Omit<ActiveFetchRequest, "terminalState"> & {
  terminalState: "continued" | "fulfilled" | "cancelled";
};

type IdleWaiter = {
  filter: RequestHandlerFilter;
  resolve: () => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};

export class FetchRequestLifecycle {
  private readonly activeRequests = new Map<string, ActiveFetchRequest>();
  private readonly seenRequestIds = new Set<string>();
  private readonly terminalRequests: TerminalFetchRequest[] = [];
  private readonly idleWaiters = new Set<IdleWaiter>();
  private navigationGeneration = 0;
  private cancellationCount = 0;
  private fatalError: Error | undefined;

  get activeCount() {
    return this.activeRequests.size;
  }

  get safeCancellationCount() {
    return this.cancellationCount;
  }

  beginNavigation(reason: NavigationCancellationReason) {
    this.navigationGeneration += 1;
    for (const request of this.activeRequests.values()) {
      request.cancellationContext = { generation: this.navigationGeneration, reason };
    }
    return this.navigationGeneration;
  }

  register(registration: FetchRequestRegistration) {
    if (!registration.requestId || this.seenRequestIds.has(registration.requestId)) {
      throw new Error(`Duplicate Fetch request ID: ${registration.requestId || "<empty>"}`);
    }
    const request = {
      ...registration,
      method: registration.method.toUpperCase(),
      generation: this.navigationGeneration,
      settlementAttemptCount: 0,
      terminalState: "active" as const,
    };
    this.seenRequestIds.add(request.requestId);
    this.activeRequests.set(request.requestId, request);
    return request.generation;
  }

  setRequiredResponse(requestId: string, requiredResponse: boolean) {
    const request = this.activeRequests.get(requestId);
    if (!request) throw new Error(`Unknown Fetch request ID: ${requestId}`);
    if (request.settlementAttemptCount !== 0) {
      throw new Error(`Cannot change required response after settlement began: ${requestId}`);
    }
    request.requiredResponse = requiredResponse;
  }

  beginSettlement(requestId: string, command: FetchSettlementCommand): FetchSettlementAttempt {
    const request = this.activeRequests.get(requestId);
    if (!request) throw new Error(`Unknown Fetch request ID: ${requestId}`);
    if (request.settlementAttemptCount !== 0) {
      throw new Error(`Duplicate Fetch settlement attempt: ${requestId}`);
    }
    request.settlementAttemptCount += 1;
    request.settlementCommand = command;
    request.terminalState = "settling";
    return { requestId, command, attempt: request.settlementAttemptCount };
  }

  completeSettlement(attempt: FetchSettlementAttempt) {
    const request = this.requireMatchingAttempt(attempt);
    this.recordTerminal(request, attempt.command === "Fetch.fulfillRequest" ? "fulfilled" : "continued");
    this.activeRequests.delete(request.requestId);
    this.flushIdleWaiters();
  }

  handleSettlementError(attempt: FetchSettlementAttempt, error: unknown) {
    const request = this.requireMatchingAttempt(attempt);
    const settlementError = asError(error);
    const recognizedCancellation = settlementError.message === invalidInterceptionIdMessage
      && attempt.attempt === 1
      && request.settlementAttemptCount === 1
      && (attempt.command === "Fetch.continueRequest" || attempt.command === "Fetch.fulfillRequest")
      && request.generation < this.navigationGeneration
      && request.cancellationContext !== undefined
      && !request.requiredResponse;
    if (!recognizedCancellation) throw settlementError;

    this.recordTerminal(request, "cancelled");
    this.activeRequests.delete(request.requestId);
    this.cancellationCount += 1;
    this.flushIdleWaiters();
    return { cancelled: true } as const;
  }

  fail(error: unknown) {
    if (this.fatalError) return this.fatalError;
    this.fatalError = asError(error);
    for (const waiter of this.idleWaiters) {
      clearTimeout(waiter.timer);
      waiter.reject(this.fatalError);
    }
    this.idleWaiters.clear();
    return this.fatalError;
  }

  assertHealthy() {
    if (this.fatalError) throw this.fatalError;
  }

  waitForIdle(options: RequestHandlerIdleOptions = {}) {
    this.assertHealthy();
    const filter = normalizeFilter(options);
    if (!this.hasActiveRequest(filter)) return Promise.resolve();
    const timeoutMs = options.timeoutMs ?? 10_000;
    return new Promise<void>((resolveIdle, rejectIdle) => {
      const waiter = {
        filter,
        resolve: resolveIdle,
        reject: rejectIdle,
        timer: setTimeout(() => {
          this.idleWaiters.delete(waiter);
          rejectIdle(new Error(`Timed out waiting for request handlers to settle; ${this.diagnostics(filter)}`));
        }, timeoutMs),
      } satisfies IdleWaiter;
      this.idleWaiters.add(waiter);
    });
  }

  diagnostics(filter: RequestHandlerFilter = {}) {
    const active = [...this.activeRequests.values()].filter((request) => matchesFilter(request, normalizeFilter(filter)));
    const summaries = active.slice(0, 5).map((request) => ({
      requestId: request.requestId,
      method: request.method,
      pathname: request.pathname,
      generation: request.generation,
      settlementCommand: request.settlementCommand || null,
      settlementAttemptCount: request.settlementAttemptCount,
      requiredResponse: request.requiredResponse,
      cancellationContext: request.cancellationContext || null,
      terminalState: request.terminalState,
    }));
    return JSON.stringify({ activeCount: active.length, active: summaries, omitted: Math.max(0, active.length - summaries.length) });
  }

  close(error: unknown = new Error("Browser request lifecycle closed")) {
    const closeError = asError(error);
    for (const waiter of this.idleWaiters) {
      clearTimeout(waiter.timer);
      waiter.reject(closeError);
    }
    this.idleWaiters.clear();
    this.activeRequests.clear();
    this.seenRequestIds.clear();
    this.terminalRequests.length = 0;
  }

  private requireMatchingAttempt(attempt: FetchSettlementAttempt) {
    const request = this.activeRequests.get(attempt.requestId);
    if (!request) throw new Error(`Unknown Fetch request ID: ${attempt.requestId}`);
    if (request.settlementAttemptCount !== attempt.attempt || request.settlementCommand !== attempt.command) {
      throw new Error(`Fetch settlement attempt mismatch: ${attempt.requestId}`);
    }
    return request;
  }

  private hasActiveRequest(filter: RequestHandlerFilter) {
    return [...this.activeRequests.values()].some((request) => matchesFilter(request, filter));
  }

  private recordTerminal(request: ActiveFetchRequest, terminalState: TerminalFetchRequest["terminalState"]) {
    this.terminalRequests.push({ ...request, terminalState });
    if (this.terminalRequests.length > 100) this.terminalRequests.shift();
  }

  private flushIdleWaiters() {
    for (const waiter of this.idleWaiters) {
      if (this.hasActiveRequest(waiter.filter)) continue;
      clearTimeout(waiter.timer);
      this.idleWaiters.delete(waiter);
      waiter.resolve();
    }
  }
}

export type EventWaiter = {
  promise: Promise<Record<string, unknown>>;
  cancel: (error?: Error) => void;
};

type PendingEventWaiter = {
  resolve: (params: Record<string, unknown>) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};

export class RejectableEventWaiters {
  private readonly waiters = new Map<string, Set<PendingEventWaiter>>();

  get pendingCount() {
    let count = 0;
    for (const waiters of this.waiters.values()) count += waiters.size;
    return count;
  }

  wait(method: string, timeoutMs: number, timeoutMessage: string): EventWaiter {
    let waiter: PendingEventWaiter;
    const promise = new Promise<Record<string, unknown>>((resolveEvent, rejectEvent) => {
      waiter = {
        resolve: resolveEvent,
        reject: rejectEvent,
        timer: setTimeout(() => {
          this.remove(method, waiter);
          rejectEvent(new Error(timeoutMessage));
        }, timeoutMs),
      };
      const methodWaiters = this.waiters.get(method) || new Set<PendingEventWaiter>();
      methodWaiters.add(waiter);
      this.waiters.set(method, methodWaiters);
    });
    return {
      promise,
      cancel: (error = new Error(`Cancelled event waiter: ${method}`)) => {
        if (!this.remove(method, waiter)) return;
        waiter.reject(error);
      },
    };
  }

  resolve(method: string, params: Record<string, unknown>) {
    const methodWaiters = [...(this.waiters.get(method) || [])];
    for (const waiter of methodWaiters) {
      if (!this.remove(method, waiter)) continue;
      waiter.resolve(params);
    }
  }

  rejectAll(error: unknown) {
    const rejection = asError(error);
    for (const [method, methodWaiters] of this.waiters) {
      for (const waiter of [...methodWaiters]) {
        if (!this.remove(method, waiter)) continue;
        waiter.reject(rejection);
      }
    }
  }

  private remove(method: string, waiter: PendingEventWaiter) {
    const methodWaiters = this.waiters.get(method);
    if (!methodWaiters?.delete(waiter)) return false;
    clearTimeout(waiter.timer);
    if (methodWaiters.size === 0) this.waiters.delete(method);
    return true;
  }
}

function normalizeFilter(filter: RequestHandlerFilter) {
  return {
    pathname: filter.pathname,
    method: filter.method?.toUpperCase(),
  };
}

function matchesFilter(request: ActiveFetchRequest, filter: RequestHandlerFilter) {
  return (!filter.pathname || request.pathname === filter.pathname)
    && (!filter.method || request.method === filter.method);
}

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error));
}
