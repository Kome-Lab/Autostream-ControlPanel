// Default CDP response ownership, separate from the opt-in G3/dialog policy.
export type CDPResponse = {
  id?: number;
  method?: string;
  params?: Record<string, unknown>;
  result?: Record<string, unknown>;
  error?: { message?: string };
  sessionId?: string;
};

export type PendingCommand = {
  resolve: (value: Record<string, unknown>) => void;
  reject: (error: Error) => void;
};

// Reuse the existing polling/settlement 10s, navigation 20s and close 3s
// contracts. These are response bounds, not a new case-performance SLA.
export function commandTimeoutMs(method: string) {
  if (method === "Browser.close") return 3_000;
  if (method === "Page.navigate" || method === "Page.reload") return 20_000;
  return 10_000;
}

export class BrowserCommandDeadlineError extends Error {
  readonly command: number;
  readonly method: string;
  readonly timeoutMs: number;
  constructor(command: number, method: string, timeoutMs: number) {
    super(`CDP_COMMAND_DEADLINE method=${method} command=${command} timeoutMs=${timeoutMs}`);
    this.name = "BrowserCommandDeadlineError";
    this.command = command;
    this.method = method;
    this.timeoutMs = timeoutMs;
  }
}

export class CommandDeadlines {
  private readonly timers = new Map<number, ReturnType<typeof setTimeout>>();
  private firstFailure: BrowserCommandDeadlineError | undefined;
  private readonly failStop: (error: Error) => void;
  constructor(failStop: (error: Error) => void) { this.failStop = failStop; }

  get failure() { return this.firstFailure; }

  arm(command: number, method: string, timeoutMs: number) {
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > commandTimeoutMs(method)) {
      throw new Error("Invalid CDP response deadline");
    }
    const timer = setTimeout(() => {
      this.timers.delete(command);
      this.firstFailure ??= new BrowserCommandDeadlineError(command, method, timeoutMs);
      this.failStop(this.firstFailure);
    }, timeoutMs);
    this.timers.set(command, timer);
    return () => { clearTimeout(timer); this.timers.delete(command); };
  }
}

// Initial target attachment retains its existing launch-owner contract. The
// default harness deadline starts after attachment; this is not launch proof.
export function createCommandSender(socket: WebSocket) {
  let commandId = 0;
  const pending = new Map<number, PendingCommand>();
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data)) as CDPResponse;
    if (!message.id) return;
    const command = pending.get(message.id);
    if (!command) return;
    pending.delete(message.id);
    if (message.error) command.reject(new Error(message.error.message || "CDP command failed"));
    else command.resolve(message.result || {});
  });
  return (method: string, params: Record<string, unknown>) => {
    const id = ++commandId;
    return new Promise<Record<string, unknown>>((resolveCommand, rejectCommand) => {
      pending.set(id, { resolve: resolveCommand, reject: rejectCommand });
      socket.send(JSON.stringify({ id, method, params }));
    });
  };
}
