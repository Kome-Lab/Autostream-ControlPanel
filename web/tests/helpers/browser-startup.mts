import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { watch, type FSWatcher } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { StringDecoder } from "node:string_decoder";
import { TextDecoder } from "node:util";

const devToolsBrowserPathPrefix = "/devtools/browser/";
const devToolsStderrMarker = "DevTools listening on ";
const diagnosticTailLimit = 2_000;

export const browserStartupTimeoutMs = 30_000;

export type DevToolsEndpointParseFailure = {
  ok: false;
  category: string;
  retryable: boolean;
};

export type DevToolsEndpointParseResult =
  | { ok: true; url: string }
  | DevToolsEndpointParseFailure;

export type ActivePortAccess = {
  read: () => Promise<Uint8Array | null>;
  watch: (onChange: () => void, onError: () => void) => { close: () => void };
};

type StartupTimers = {
  now: () => number;
  set: (callback: () => void, milliseconds: number) => ReturnType<typeof setTimeout>;
  clear: (timer: ReturnType<typeof setTimeout>) => void;
};

export type BrowserStartupOptions = {
  timeoutMs?: number;
  signal?: AbortSignal;
  activePortAccess?: ActivePortAccess;
  openWebSocket?: (url: string, signal: AbortSignal) => Promise<WebSocket>;
  activePortProbeIntervalMs?: number;
  webSocketRetryIntervalMs?: number;
  timers?: StartupTimers;
};

type StartupDiagnostics = {
  activePortFile: "unknown" | "no" | "yes";
  activePortParse: string;
  endpointSource: "none" | "active-port" | "stderr";
  processCode: number | null;
  processSignal: NodeJS.Signals | null;
  stderrParse: string;
  stderrTail: string;
  stdoutTail: string;
  webSocketAttempts: number;
};

type EndpointDiscovery =
  | { kind: "endpoint"; source: "active-port" | "stderr"; url: string }
  | { kind: "failure"; reason: string };

type ProcessTerminal =
  | { kind: "process-error" }
  | { kind: "process-exit"; code: number | null; signal: NodeJS.Signals | null };

type Deadline = { kind: "deadline" };
type StartupAbort = { kind: "abort" };

const defaultTimers: StartupTimers = {
  now: () => Date.now(),
  set: (callback, milliseconds) => setTimeout(callback, milliseconds),
  clear: (timer) => clearTimeout(timer),
};

export function parseDevToolsActivePort(content: Uint8Array): DevToolsEndpointParseResult {
  let value: string;
  try {
    value = new TextDecoder("utf-8", { fatal: true }).decode(content);
  } catch {
    return parseFailure("invalid_utf8");
  }

  if (value.includes("\r") && value.replaceAll("\r\n", "").includes("\r")) {
    return parseFailure("invalid_line_endings");
  }

  const lines = value.split(/\r?\n/);
  if (lines.length > 2) {
    const trailingLines = lines.slice(2);
    if (trailingLines.some((line) => line !== "")) return parseFailure("extra_nonblank_line");
    if (trailingLines.length > 2) return parseFailure("extra_blank_line");
  }

  const portText = lines[0] || "";
  if (portText === "") return parseFailure("missing_port", true);
  if (!/^\d+$/.test(portText)) return parseFailure("invalid_port");
  const port = Number(portText);
  if (!Number.isSafeInteger(port) || port < 1 || port > 65_535) return parseFailure("port_out_of_range");

  if (lines.length < 2 || lines[1] === "") return parseFailure("missing_path", true);
  const path = lines[1];
  if (/^[a-z][a-z\d+.-]*:\/\//i.test(path)) return parseFailure("full_url_not_allowed");
  if (!path.startsWith(devToolsBrowserPathPrefix)) return parseFailure("wrong_path_prefix");
  if (!validDevToolsBrowserPath(path)) return parseFailure("unexpected_path");

  return { ok: true, url: `ws://127.0.0.1:${port}${path}` };
}

export function parseDevToolsStderrLine(line: string): DevToolsEndpointParseResult | null {
  const markerIndex = line.indexOf(devToolsStderrMarker);
  if (markerIndex < 0) return null;
  const rawUrl = line.slice(markerIndex + devToolsStderrMarker.length);
  return validateDevToolsWebSocketUrl(rawUrl);
}

export function createDevToolsStderrParser(
  receive: (result: DevToolsEndpointParseResult) => void,
) {
  const decoder = new StringDecoder("utf8");
  let buffered = "";
  let settled = false;

  const inspect = (line: string) => {
    if (settled) return;
    const normalized = line.endsWith("\r") ? line.slice(0, -1) : line;
    const result = parseDevToolsStderrLine(normalized);
    if (!result) return;
    settled = true;
    receive(result);
  };

  const drainCompleteLines = () => {
    let newlineIndex = buffered.indexOf("\n");
    while (newlineIndex >= 0) {
      inspect(buffered.slice(0, newlineIndex));
      buffered = buffered.slice(newlineIndex + 1);
      newlineIndex = buffered.indexOf("\n");
    }
    buffered = buffered.slice(-diagnosticTailLimit);
  };

  return {
    push(chunk: Uint8Array) {
      if (settled) return;
      buffered += decoder.write(Buffer.from(chunk));
      drainCompleteLines();
    },
    end() {
      if (settled) return;
      buffered += decoder.end();
      inspect(buffered);
      buffered = "";
    },
  };
}

export async function connectToBrowserDevTools(
  browserProcess: ChildProcessWithoutNullStreams,
  userDataDirectory: string,
  options: BrowserStartupOptions = {},
) {
  const timeoutMs = options.timeoutMs ?? browserStartupTimeoutMs;
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) throw new Error("Browser startup timeout must be positive");
  const activePortProbeIntervalMs = options.activePortProbeIntervalMs ?? 50;
  if (!Number.isFinite(activePortProbeIntervalMs) || activePortProbeIntervalMs <= 0) {
    throw new Error("Active-port probe interval must be positive");
  }
  const webSocketRetryIntervalMs = options.webSocketRetryIntervalMs ?? 100;
  if (!Number.isFinite(webSocketRetryIntervalMs) || webSocketRetryIntervalMs <= 0) {
    throw new Error("WebSocket retry interval must be positive");
  }

  const timers = options.timers ?? defaultTimers;
  const deadlineAt = timers.now() + timeoutMs;
  const diagnostics: StartupDiagnostics = {
    activePortFile: "unknown",
    activePortParse: "not_read",
    endpointSource: "none",
    processCode: browserProcess.exitCode,
    processSignal: browserProcess.signalCode,
    stderrParse: "not_reported",
    stderrTail: "",
    stdoutTail: "",
    webSocketAttempts: 0,
  };

  const onStdout = (chunk: Buffer) => {
    diagnostics.stdoutTail = appendDiagnosticTail(diagnostics.stdoutTail, chunk);
  };
  const onStderr = (chunk: Buffer) => {
    diagnostics.stderrTail = appendDiagnosticTail(diagnostics.stderrTail, chunk);
  };
  browserProcess.stdout.on("data", onStdout);
  browserProcess.stderr.on("data", onStderr);

  const terminal = monitorProcess(browserProcess, diagnostics);
  const deadline = createDeadline(deadlineAt, timers);
  const abort = monitorAbort(options.signal);
  const activePortAccess = options.activePortAccess ?? fileActivePortAccess(userDataDirectory);
  const discovery = discoverEndpoint(
    browserProcess,
    activePortAccess,
    diagnostics,
    deadlineAt,
    timers,
    activePortProbeIntervalMs,
  );

  try {
    const first = await Promise.race([
      discovery.promise,
      terminal.promise,
      deadline.promise,
      abort.promise,
    ]);

    if (first.kind === "deadline") throw startupFailure("endpoint_timeout", diagnostics);
    if (first.kind === "abort") throw startupFailure("aborted", diagnostics);
    if (first.kind === "process-error") throw startupFailure("process_error", diagnostics);
    if (first.kind === "process-exit") throw startupFailure("process_exit", diagnostics);
    if (first.kind === "failure") throw startupFailure(first.reason, diagnostics);

    diagnostics.endpointSource = first.source;
    const endpoint = validateDevToolsWebSocketUrl(first.url);
    if (!endpoint.ok) {
      diagnostics.stderrParse = endpoint.category;
      throw startupFailure("unsafe_endpoint", diagnostics);
    }

    const openWebSocket = options.openWebSocket ?? openValidatedWebSocket;
    while (timers.now() < deadlineAt) {
      diagnostics.webSocketAttempts += 1;
      const attemptController = new AbortController();
      const attempt = Promise.resolve()
        .then(() => openWebSocket(endpoint.url, attemptController.signal))
        .then(
          (socket) => {
            if (attemptController.signal.aborted) {
              closeWebSocket(socket);
              return { kind: "connection-failure" as const };
            }
            return { kind: "socket" as const, socket };
          },
          () => ({ kind: "connection-failure" as const }),
        );
      const connected = await Promise.race([attempt, terminal.promise, deadline.promise, abort.promise]);

      if (connected.kind === "socket") {
        if (browserProcess.exitCode !== null || browserProcess.signalCode !== null) {
          closeWebSocket(connected.socket);
          throw startupFailure("process_exit", diagnostics);
        }
        return connected.socket;
      }

      attemptController.abort();
      if (connected.kind === "deadline") throw startupFailure("websocket_timeout", diagnostics);
      if (connected.kind === "abort") throw startupFailure("aborted", diagnostics);
      if (connected.kind === "process-error") throw startupFailure("process_error", diagnostics);
      if (connected.kind === "process-exit") throw startupFailure("process_exit", diagnostics);

      const retry = createDelay(Math.min(webSocketRetryIntervalMs, Math.max(0, deadlineAt - timers.now())), timers);
      try {
        const retryOutcome = await Promise.race([retry.promise, terminal.promise, deadline.promise, abort.promise]);
        if (retryOutcome.kind === "deadline") throw startupFailure("websocket_timeout", diagnostics);
        if (retryOutcome.kind === "abort") throw startupFailure("aborted", diagnostics);
        if (retryOutcome.kind === "process-error") throw startupFailure("process_error", diagnostics);
        if (retryOutcome.kind === "process-exit") throw startupFailure("process_exit", diagnostics);
      } finally {
        retry.close();
      }
    }

    throw startupFailure("websocket_timeout", diagnostics);
  } finally {
    discovery.close();
    terminal.close();
    deadline.close();
    abort.close();
    browserProcess.stdout.off("data", onStdout);
    browserProcess.stderr.off("data", onStderr);
  }
}

function validateDevToolsWebSocketUrl(value: string): DevToolsEndpointParseResult {
  if (value !== value.trim() || /\s/.test(value)) return parseFailure("invalid_url");
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return parseFailure("invalid_url");
  }

  if (url.protocol !== "ws:") return parseFailure("unsupported_protocol");
  if (url.username || url.password) return parseFailure("credentials_not_allowed");
  if (url.hostname !== "127.0.0.1") return parseFailure("external_host");
  const explicitPort = value.match(/^ws:\/\/127\.0\.0\.1:(\d+)\//)?.[1];
  if (!explicitPort) return parseFailure("missing_port");
  const port = Number(explicitPort);
  if (!Number.isSafeInteger(port) || port < 1 || port > 65_535) return parseFailure("port_out_of_range");
  if (!url.pathname.startsWith(devToolsBrowserPathPrefix)) return parseFailure("wrong_path_prefix");
  if (!validDevToolsBrowserPath(url.pathname)) return parseFailure("unexpected_path");
  if (url.search || url.hash) return parseFailure("query_or_fragment_not_allowed");

  return { ok: true, url: `ws://127.0.0.1:${port}${url.pathname}` };
}

function validDevToolsBrowserPath(path: string) {
  const opaqueId = path.slice(devToolsBrowserPathPrefix.length);
  return opaqueId.length > 0 && /^[A-Za-z0-9._~-]+$/.test(opaqueId);
}

function parseFailure(category: string, retryable = false): DevToolsEndpointParseFailure {
  return { ok: false, category, retryable };
}

function fileActivePortAccess(userDataDirectory: string): ActivePortAccess {
  const activePortPath = join(userDataDirectory, "DevToolsActivePort");
  return {
    async read() {
      try {
        return await readFile(activePortPath);
      } catch (error) {
        if (isNodeError(error) && error.code === "ENOENT") return null;
        throw new Error("active_port_read_error");
      }
    },
    watch(onChange, onError) {
      let watcher: FSWatcher | undefined;
      try {
        watcher = watch(userDataDirectory, { persistent: false }, (_event, filename) => {
          if (filename === null || String(filename) === "DevToolsActivePort") onChange();
        });
        watcher.on("error", onError);
      } catch {
        queueMicrotask(onError);
      }
      return {
        close() {
          watcher?.off("error", onError);
          watcher?.close();
        },
      };
    },
  };
}

function discoverEndpoint(
  browserProcess: ChildProcessWithoutNullStreams,
  activePortAccess: ActivePortAccess,
  diagnostics: StartupDiagnostics,
  deadlineAt: number,
  timers: StartupTimers,
  probeIntervalMs: number,
) {
  let resolveDiscovery!: (result: EndpointDiscovery) => void;
  const promise = new Promise<EndpointDiscovery>((resolve) => {
    resolveDiscovery = resolve;
  });
  let closed = false;
  let settled = false;
  let reading = false;
  let reread = false;
  let probeTimer: ReturnType<typeof setTimeout> | undefined;

  const finish = (result: EndpointDiscovery) => {
    if (settled || closed) return;
    settled = true;
    resolveDiscovery(result);
  };

  const scheduleProbe = () => {
    if (settled || closed || probeTimer || timers.now() >= deadlineAt) return;
    const interval = Math.max(1, Math.min(probeIntervalMs, deadlineAt - timers.now()));
    probeTimer = timers.set(() => {
      probeTimer = undefined;
      void checkActivePort();
    }, interval);
  };

  const checkActivePort = async () => {
    if (settled || closed) return;
    if (reading) {
      reread = true;
      return;
    }
    reading = true;
    try {
      do {
        reread = false;
        let content: Uint8Array | null;
        try {
          content = await activePortAccess.read();
        } catch {
          diagnostics.activePortParse = "read_error";
          finish({ kind: "failure", reason: "active_port_read_error" });
          return;
        }
        if (content === null) {
          diagnostics.activePortFile = "no";
          diagnostics.activePortParse = "not_present";
        } else {
          diagnostics.activePortFile = "yes";
          const parsed = parseDevToolsActivePort(content);
          diagnostics.activePortParse = parsed.ok ? "valid" : parsed.category;
          if (parsed.ok) {
            finish({ kind: "endpoint", source: "active-port", url: parsed.url });
            return;
          }
          if (!parsed.retryable) {
            finish({ kind: "failure", reason: "malformed_active_port" });
            return;
          }
        }
      } while (reread && !settled && !closed);
    } finally {
      reading = false;
      scheduleProbe();
    }
  };

  const stderrParser = createDevToolsStderrParser((parsed) => {
    diagnostics.stderrParse = parsed.ok ? "valid" : parsed.category;
    if (parsed.ok) finish({ kind: "endpoint", source: "stderr", url: parsed.url });
    else finish({ kind: "failure", reason: "unsafe_stderr_endpoint" });
  });
  const receiveStderr = (chunk: Buffer) => stderrParser.push(chunk);
  browserProcess.stderr.on("data", receiveStderr);

  const watcher = activePortAccess.watch(
    () => void checkActivePort(),
    () => {
      diagnostics.activePortParse = "watch_error";
      scheduleProbe();
    },
  );
  void checkActivePort();

  return {
    promise,
    close() {
      if (closed) return;
      closed = true;
      if (probeTimer) timers.clear(probeTimer);
      browserProcess.stderr.off("data", receiveStderr);
      stderrParser.end();
      watcher.close();
    },
  };
}

function monitorProcess(browserProcess: ChildProcessWithoutNullStreams, diagnostics: StartupDiagnostics) {
  let resolveTerminal!: (result: ProcessTerminal) => void;
  let settled = false;
  const promise = new Promise<ProcessTerminal>((resolve) => {
    resolveTerminal = resolve;
  });
  const onError = () => {
    if (settled) return;
    settled = true;
    resolveTerminal({ kind: "process-error" });
  };
  const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
    diagnostics.processCode = code;
    diagnostics.processSignal = signal;
    if (settled) return;
    settled = true;
    resolveTerminal({ kind: "process-exit", code, signal });
  };
  browserProcess.once("error", onError);
  browserProcess.once("exit", onExit);
  if (browserProcess.exitCode !== null || browserProcess.signalCode !== null) {
    onExit(browserProcess.exitCode, browserProcess.signalCode);
  }
  return {
    promise,
    close() {
      browserProcess.off("error", onError);
      browserProcess.off("exit", onExit);
    },
  };
}

function createDeadline(deadlineAt: number, timers: StartupTimers) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const promise = new Promise<Deadline>((resolve) => {
    timer = timers.set(() => resolve({ kind: "deadline" }), Math.max(0, deadlineAt - timers.now()));
  });
  return {
    promise,
    close() {
      if (timer) timers.clear(timer);
      timer = undefined;
    },
  };
}

function monitorAbort(signal: AbortSignal | undefined) {
  let resolveAbort!: (result: StartupAbort) => void;
  const promise = new Promise<StartupAbort>((resolve) => {
    resolveAbort = resolve;
  });
  const onAbort = () => resolveAbort({ kind: "abort" });
  signal?.addEventListener("abort", onAbort, { once: true });
  if (signal?.aborted) onAbort();
  return {
    promise,
    close() {
      signal?.removeEventListener("abort", onAbort);
    },
  };
}

function createDelay(milliseconds: number, timers: StartupTimers) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const promise = new Promise<{ kind: "retry" }>((resolve) => {
    timer = timers.set(() => resolve({ kind: "retry" }), milliseconds);
  });
  return {
    promise,
    close() {
      if (timer) timers.clear(timer);
      timer = undefined;
    },
  };
}

function openValidatedWebSocket(url: string, signal: AbortSignal) {
  return new Promise<WebSocket>((resolve, reject) => {
    let socket: WebSocket;
    try {
      socket = new WebSocket(url);
    } catch {
      reject(new Error("websocket_constructor_failed"));
      return;
    }

    let settled = false;
    const cleanup = () => {
      socket.removeEventListener("open", onOpen);
      socket.removeEventListener("error", onFailure);
      socket.removeEventListener("close", onFailure);
      signal.removeEventListener("abort", onAbort);
    };
    const onOpen = () => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve(socket);
    };
    const onFailure = () => {
      if (settled) return;
      settled = true;
      cleanup();
      closeWebSocket(socket);
      reject(new Error("websocket_connection_failed"));
    };
    const onAbort = () => onFailure();
    socket.addEventListener("open", onOpen, { once: true });
    socket.addEventListener("error", onFailure, { once: true });
    socket.addEventListener("close", onFailure, { once: true });
    signal.addEventListener("abort", onAbort, { once: true });
    if (signal.aborted) onAbort();
  });
}

function startupFailure(reason: string, diagnostics: StartupDiagnostics) {
  const fields = [
    `reason=${reason}`,
    `code=${diagnostics.processCode ?? "null"}`,
    `signal=${diagnostics.processSignal ?? "null"}`,
    `activePortFile=${diagnostics.activePortFile}`,
    `activePortParse=${diagnostics.activePortParse}`,
    `stderrParse=${diagnostics.stderrParse}`,
    `endpointSource=${diagnostics.endpointSource}`,
    `webSocketAttempts=${diagnostics.webSocketAttempts}`,
  ];
  if (diagnostics.stdoutTail) fields.push(`stdoutTail=${JSON.stringify(sanitizeDiagnosticTail(diagnostics.stdoutTail))}`);
  if (diagnostics.stderrTail) fields.push(`stderrTail=${JSON.stringify(sanitizeDiagnosticTail(diagnostics.stderrTail))}`);
  return new Error(`Chrome DevTools startup failed: ${fields.join("; ")}`);
}

function appendDiagnosticTail(previous: string, chunk: Uint8Array) {
  return `${previous}${Buffer.from(chunk).toString("utf8")}`.slice(-diagnosticTailLimit);
}

function sanitizeDiagnosticTail(value: string) {
  return value
    .replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/\bBearer\s+\S+/gi, "Bearer <redacted>")
    .replace(/([?&](?:access_token|api_key|key|password|secret|token)=)[^&\s]+/gi, "$1<redacted>")
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, "�")
    .slice(-diagnosticTailLimit);
}

function closeWebSocket(socket: WebSocket) {
  try {
    socket.close();
  } catch {
    // A failed startup socket may already be closed or not fully constructed.
  }
}

function isNodeError(error: unknown): error is NodeJS.ErrnoException {
  return error instanceof Error && "code" in error;
}
