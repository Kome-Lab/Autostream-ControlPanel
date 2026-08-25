import { spawn, spawnSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";

export type StubResponse = {
  status?: number;
  body?: unknown;
  delayMs?: number;
  waitUntil?: Promise<void>;
};

export type StubRequest = {
  method: string;
  url: string;
};

export type RouteResolver = (request: StubRequest) => StubResponse | null;

type CDPResponse = {
  id?: number;
  method?: string;
  params?: Record<string, unknown>;
  result?: Record<string, unknown>;
  error?: { message?: string };
  sessionId?: string;
};

type PendingCommand = {
  resolve: (value: Record<string, unknown>) => void;
  reject: (error: Error) => void;
};

const defaultBrowserCandidates = process.platform === "win32"
  ? [
      "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
      "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
      "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
      "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
    ]
  : process.platform === "darwin"
    ? [
        "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
        "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
      ]
    : ["/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser"];

export class BrowserHarness {
  readonly requests = new Map<string, number>();
  readonly responses = new Map<string, number>();
  readonly responseStatuses = new Map<string, number[]>();

  private readonly browserProcess: ChildProcessWithoutNullStreams;
  private readonly userDataDirectory: string;
  private readonly socket: WebSocket;
  private readonly sessionId: string;
  private nextCommandId = 0;
  private routeResolver: RouteResolver = () => null;
  private readonly pendingCommands = new Map<number, PendingCommand>();
  private readonly eventWaiters = new Map<string, Set<(params: Record<string, unknown>) => void>>();
  private consoleErrorEvents = 0;
  private topLevelNavigationEvents = 0;
  private fatalError: Error | undefined;
  private closed = false;

  private constructor(
    browserProcess: ChildProcessWithoutNullStreams,
    userDataDirectory: string,
    socket: WebSocket,
    sessionId: string,
  ) {
    this.browserProcess = browserProcess;
    this.userDataDirectory = userDataDirectory;
    this.socket = socket;
    this.sessionId = sessionId;
    this.socket.addEventListener("message", (event) => this.receive(String(event.data)));
    this.socket.addEventListener("close", () => {
      for (const command of this.pendingCommands.values()) command.reject(new Error("Browser CDP connection closed"));
      this.pendingCommands.clear();
    });
  }

  static async launch() {
    const browserPath = resolveBrowserPath();
    const userDataDirectory = mkdtempSync(join(tmpdir(), "autostream-ui-browser-"));
    const browserProcess = spawn(browserPath, [
      "--headless=new",
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-breakpad",
      "--disable-crash-reporter",
      "--disable-default-apps",
      "--disable-gpu",
      "--no-default-browser-check",
      "--no-first-run",
      "--remote-debugging-port=0",
      `--user-data-dir=${userDataDirectory}`,
      "about:blank",
    ], { stdio: "pipe", windowsHide: true });

    try {
      const websocketUrl = await devToolsWebsocketUrl(browserProcess);
      const socket = await openWebSocket(websocketUrl);
      const command = createCommandSender(socket);
      const target = await command("Target.createTarget", { url: "about:blank" });
      const attached = await command("Target.attachToTarget", { targetId: target.targetId, flatten: true });
      const harness = new BrowserHarness(browserProcess, userDataDirectory, socket, String(attached.sessionId));
      await harness.send("Page.enable");
      await harness.send("Runtime.enable");
      await harness.send("Fetch.enable", { patterns: [{ urlPattern: "*" }] });
      return harness;
    } catch (error) {
      await terminateChildProcess(browserProcess);
      try {
        await removeOwnedBrowserDirectory(userDataDirectory);
      } catch (cleanupError) {
        throw new AggregateError(
          [asError(error), asError(cleanupError)],
          "Browser launch failed and its owned profile could not be removed",
          { cause: error },
        );
      }
      throw error;
    }
  }

  setRouteResolver(resolver: RouteResolver) {
    this.routeResolver = resolver;
  }

  clearRequestCounts() {
    this.requests.clear();
    this.responses.clear();
    this.responseStatuses.clear();
  }

  clearNavigationCount() {
    this.topLevelNavigationEvents = 0;
  }

  clearConsoleErrors() {
    this.consoleErrorEvents = 0;
  }

  get consoleErrorCount() {
    return this.consoleErrorEvents;
  }

  get navigationCount() {
    return this.topLevelNavigationEvents;
  }

  async waitForRequestCount(pathname: string, count: number, timeoutMs = 10_000) {
    await waitForMapCount(this.requests, pathname, count, `request count for ${pathname}`, timeoutMs);
  }

  async waitForResponseCount(pathname: string, count: number, timeoutMs = 10_000) {
    await waitForMapCount(this.responses, pathname, count, `response count for ${pathname}`, timeoutMs);
  }

  async navigate(url: string) {
    const loaded = this.once("Page.loadEventFired");
    const result = await this.send("Page.navigate", { url });
    if (result.errorText) throw new Error(`Navigation failed: ${result.errorText}`);
    await withTimeout(loaded, 20_000, `Timed out loading ${url}`);
  }

  async reload() {
    const loaded = this.once("Page.loadEventFired");
    await this.send("Page.reload", { ignoreCache: true });
    await withTimeout(loaded, 20_000, "Timed out reloading the page");
  }

  async evaluate<T>(expression: string): Promise<T> {
    const response = await this.send("Runtime.evaluate", {
      expression,
      awaitPromise: true,
      returnByValue: true,
      userGesture: true,
    });
    if (response.exceptionDetails) {
      const details = response.exceptionDetails as { text?: string; exception?: { description?: string } };
      throw new Error(details.exception?.description || details.text || "Browser evaluation failed");
    }
    return (response.result as { value?: T } | undefined)?.value as T;
  }

  async waitFor<T>(expression: string, accept: (value: T) => boolean, description: string, timeoutMs = 10_000) {
    const deadline = Date.now() + timeoutMs;
    let lastValue: T | undefined;
    while (Date.now() < deadline) {
      lastValue = await this.evaluate<T>(expression);
      if (accept(lastValue)) return lastValue;
      await delay(50);
    }
    throw new Error(`${description}; last value: ${JSON.stringify(lastValue)}`);
  }

  async setViewport(width: number, height: number) {
    await this.send("Emulation.setDeviceMetricsOverride", {
      width,
      height,
      deviceScaleFactor: 1,
      mobile: width < 768,
      screenWidth: width,
      screenHeight: height,
    });
  }

  async clickAt(x: number, y: number) {
    await this.send("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
    await this.send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
    await this.send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
  }

  async clickSelector(selector: string) {
    const point = await this.evaluate<{ x: number; y: number } | null>(`(() => {
      const element = [...document.querySelectorAll(${JSON.stringify(selector)})]
        .find((candidate) => candidate instanceof HTMLElement && candidate.getClientRects().length > 0);
      if (!(element instanceof HTMLElement)) return null;
      const rect = element.getBoundingClientRect();
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
    })()`);
    if (!point) throw new Error(`Clickable element not found: ${selector}`);
    await this.clickAt(point.x, point.y);
  }

  async clickRoleWithText(role: string, text: string) {
    const point = await this.waitFor<{ x: number; y: number } | null>(`(() => {
      const element = [...document.querySelectorAll('[role=${JSON.stringify(role)}]')]
        .find((candidate) => candidate instanceof HTMLElement
          && candidate.getClientRects().length > 0
          && candidate.textContent?.trim() === ${JSON.stringify(text)});
      if (!(element instanceof HTMLElement)) return null;
      const rect = element.getBoundingClientRect();
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
    })()`, (value) => value !== null, `visible ${role} named ${text} was not found`);
    if (!point) throw new Error(`Visible ${role} named ${text} was not found`);
    await this.clickAt(point.x, point.y);
  }

  async fillSelector(selector: string, value: string) {
    const changed = await this.evaluate<boolean>(`(() => {
      const element = [...document.querySelectorAll(${JSON.stringify(selector)})]
        .find((candidate) => candidate instanceof HTMLInputElement && candidate.getClientRects().length > 0);
      if (!(element instanceof HTMLInputElement)) return false;
      const descriptor = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value');
      descriptor?.set?.call(element, ${JSON.stringify(value)});
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new Event('change', { bubbles: true }));
      return true;
    })()`);
    if (!changed) throw new Error(`Editable input not found: ${selector}`);
  }

  async setMediaFeatures(features: Array<{ name: string; value: string }>) {
    await this.send("Emulation.setEmulatedMedia", { media: "screen", features });
  }

  async pressKey(key: string, code = key) {
    await this.send("Input.dispatchKeyEvent", { type: "keyDown", key, code });
    await this.send("Input.dispatchKeyEvent", { type: "keyUp", key, code });
  }

  async close() {
    if (this.closed) return;
    this.closed = true;
    try {
      await withTimeout(this.sendBrowser("Browser.close"), 3_000, "Timed out closing the browser through CDP");
    } catch {
      // The browser commonly closes the CDP socket before acknowledging Browser.close.
    }
    await terminateChildProcess(this.browserProcess);
    this.socket.close();
    await removeOwnedBrowserDirectory(this.userDataDirectory);
  }

  private async send(method: string, params: Record<string, unknown> = {}) {
    return this.sendCommand(method, params, this.sessionId);
  }

  private async sendBrowser(method: string, params: Record<string, unknown> = {}) {
    return this.sendCommand(method, params);
  }

  private sendCommand(method: string, params: Record<string, unknown>, sessionId?: string) {
    if (this.fatalError) return Promise.reject(this.fatalError);
    const id = ++this.nextCommandId;
    const payload = sessionId ? { id, method, params, sessionId } : { id, method, params };
    return new Promise<Record<string, unknown>>((resolveCommand, rejectCommand) => {
      this.pendingCommands.set(id, { resolve: resolveCommand, reject: rejectCommand });
      this.socket.send(JSON.stringify(payload));
    });
  }

  private once(method: string) {
    return new Promise<Record<string, unknown>>((resolveEvent) => {
      const listener = (params: Record<string, unknown>) => {
        this.eventWaiters.get(method)?.delete(listener);
        resolveEvent(params);
      };
      const listeners = this.eventWaiters.get(method) || new Set();
      listeners.add(listener);
      this.eventWaiters.set(method, listeners);
    });
  }

  private receive(rawMessage: string) {
    const message = JSON.parse(rawMessage) as CDPResponse;
    if (message.id) {
      const command = this.pendingCommands.get(message.id);
      if (!command) return;
      this.pendingCommands.delete(message.id);
      if (message.error) command.reject(new Error(message.error.message || "CDP command failed"));
      else command.resolve(message.result || {});
      return;
    }
    if (!message.method || (message.sessionId && message.sessionId !== this.sessionId)) return;
    if (message.method === "Runtime.consoleAPICalled") {
      const type = String(message.params?.type || "");
      if (type === "error" || type === "assert") this.consoleErrorEvents += 1;
    }
    if (message.method === "Runtime.exceptionThrown") this.consoleErrorEvents += 1;
    if (message.method === "Page.frameNavigated") {
      const frame = message.params?.frame as { parentId?: string } | undefined;
      if (frame && !frame.parentId) this.topLevelNavigationEvents += 1;
    }
    if (message.method === "Fetch.requestPaused") {
      void this.handleRequest(message.params || {}).catch((error) => this.recordFatalError(error));
    }
    for (const listener of this.eventWaiters.get(message.method) || []) listener(message.params || {});
  }

  private async handleRequest(params: Record<string, unknown>) {
    const requestId = String(params.requestId);
    const request = params.request as { method: string; url: string };
    const pathname = new URL(request.url).pathname.replace(/\/$/, "") || "/";
    this.requests.set(pathname, (this.requests.get(pathname) || 0) + 1);
    const response = this.routeResolver({ method: request.method, url: request.url });
    if (!response) {
      await this.send("Fetch.continueRequest", { requestId });
      this.responses.set(pathname, (this.responses.get(pathname) || 0) + 1);
      return;
    }
    if (response.delayMs) await delay(response.delayMs);
    if (response.waitUntil) await response.waitUntil;
    const body = response.body === undefined ? "" : JSON.stringify(response.body);
    const responseStatus = response.status ?? 200;
    await this.send("Fetch.fulfillRequest", {
      requestId,
      responseCode: responseStatus,
      responseHeaders: [{ name: "content-type", value: "application/json; charset=utf-8" }],
      body: Buffer.from(body).toString("base64"),
    });
    this.responses.set(pathname, (this.responses.get(pathname) || 0) + 1);
    const statuses = this.responseStatuses.get(pathname) || [];
    statuses.push(responseStatus);
    this.responseStatuses.set(pathname, statuses);
  }

  private recordFatalError(error: unknown) {
    if (this.closed || this.fatalError) return;
    this.fatalError = asError(error);
    for (const command of this.pendingCommands.values()) command.reject(this.fatalError);
    this.pendingCommands.clear();
  }
}

export async function ensureWebServer(webRoot: string, requestedBaseUrl = "http://127.0.0.1:3002") {
  if (await serverResponds(requestedBaseUrl)) return { baseUrl: requestedBaseUrl, close: async () => {} };

  const url = new URL(requestedBaseUrl);
  const generatedFiles = captureNextGeneratedFiles(webRoot);
  const nextBin = resolve(webRoot, "node_modules", "next", "dist", "bin", "next");
  if (!existsSync(nextBin)) throw new Error(`Next binary is missing: ${nextBin}`);
  const server = spawn(process.execPath, [nextBin, "dev", "--hostname", url.hostname, "--port", url.port || "3000"], {
    cwd: webRoot,
    env: nextServerEnvironment(),
    stdio: "pipe",
    windowsHide: true,
  });
  let closed = false;
  const close = async () => {
    if (closed) return;
    closed = true;
    try {
      await terminateChildProcess(server);
    } finally {
      restoreNextGeneratedFiles(generatedFiles);
    }
  };
  let output = "";
  server.stdout.on("data", (chunk) => { output = `${output}${chunk}`.slice(-8_000); });
  server.stderr.on("data", (chunk) => { output = `${output}${chunk}`.slice(-8_000); });
  try {
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline && !(await serverResponds(requestedBaseUrl))) {
      if (server.exitCode !== null) throw new Error(`Next server exited early:\n${output}`);
      await delay(100);
    }
    if (!(await serverResponds(requestedBaseUrl))) {
      throw new Error(`Next server did not become ready:\n${output}`);
    }
  } catch (error) {
    await close();
    throw error;
  }
  return {
    baseUrl: requestedBaseUrl,
    close,
  };
}

type GeneratedFileSnapshot = {
  path: string;
  existed: boolean;
  content?: Buffer;
};

export type NextGeneratedFilesSnapshot = GeneratedFileSnapshot[];

export function captureNextGeneratedFiles(webRoot: string): NextGeneratedFilesSnapshot {
  const root = resolve(webRoot);
  return ["next-env.d.ts", "AGENTS.md", "CLAUDE.md"].map((name) => {
    const path = resolve(root, name);
    if (dirname(path) !== root) throw new Error(`Refusing unexpected generated file path: ${path}`);
    return existsSync(path)
      ? { path, existed: true, content: readFileSync(path) }
      : { path, existed: false };
  });
}

export function restoreNextGeneratedFiles(snapshot: NextGeneratedFilesSnapshot) {
  for (const file of snapshot) {
    if (file.existed) {
      if (!file.content) throw new Error(`Generated file snapshot is incomplete: ${file.path}`);
      const unchanged = existsSync(file.path) && readFileSync(file.path).equals(file.content);
      if (!unchanged) writeFileSync(file.path, file.content);
      continue;
    }
    rmSync(file.path, { force: true });
  }
}

export function nextGeneratedFilesMatch(snapshot: NextGeneratedFilesSnapshot) {
  return snapshot.every((file) => {
    if (!file.existed) return !existsSync(file.path);
    return Boolean(file.content && existsSync(file.path) && readFileSync(file.path).equals(file.content));
  });
}

function resolveBrowserPath() {
  const candidates = [process.env.AUTOSTREAM_BROWSER_PATH, ...defaultBrowserCandidates].filter((value): value is string => Boolean(value));
  const browserPath = candidates.find((candidate) => existsSync(candidate));
  if (!browserPath) throw new Error("Chrome/Chromium was not found; set AUTOSTREAM_BROWSER_PATH");
  return browserPath;
}

function devToolsWebsocketUrl(browserProcess: ChildProcessWithoutNullStreams) {
  return new Promise<string>((resolveUrl, rejectUrl) => {
    let output = "";
    const timeout = setTimeout(() => rejectUrl(new Error(`Chrome DevTools endpoint was not reported:\n${output}`)), 15_000);
    const receiveOutput = (chunk: Buffer) => {
      output = `${output}${chunk.toString("utf8")}`.slice(-8_000);
      const match = output.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (!match) return;
      clearTimeout(timeout);
      resolveUrl(match[1]);
    };
    browserProcess.stderr.on("data", receiveOutput);
    browserProcess.once("error", (error) => {
      clearTimeout(timeout);
      rejectUrl(error);
    });
    browserProcess.once("exit", (code) => {
      clearTimeout(timeout);
      rejectUrl(new Error(`Chrome exited before DevTools was ready (${code}):\n${output}`));
    });
  });
}

function openWebSocket(url: string) {
  return new Promise<WebSocket>((resolveSocket, rejectSocket) => {
    const socket = new WebSocket(url);
    socket.addEventListener("open", () => resolveSocket(socket), { once: true });
    socket.addEventListener("error", () => rejectSocket(new Error(`Could not connect to ${url}`)), { once: true });
  });
}

function createCommandSender(socket: WebSocket) {
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

async function serverResponds(url: string) {
  try {
    const response = await fetch(url, { signal: AbortSignal.timeout(1_000) });
    return response.status < 500;
  } catch {
    return false;
  }
}

async function removeOwnedBrowserDirectory(path: string) {
  const resolved = resolve(path);
  if (dirname(resolved) !== resolve(tmpdir()) || !basename(resolved).startsWith("autostream-ui-browser-")) {
    throw new Error(`Refusing to remove unexpected browser directory: ${resolved}`);
  }
  for (let attempt = 0; attempt < 8; attempt += 1) {
    try {
      rmSync(resolved, { recursive: true, force: true, maxRetries: 2, retryDelay: 100 });
      return;
    } catch (error) {
      if (attempt === 7) throw error;
      await delay(150 * (attempt + 1));
    }
  }
}

async function terminateChildProcess(child: ChildProcessWithoutNullStreams) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  if (process.platform === "win32" && child.pid) {
    spawnSync("taskkill.exe", ["/PID", String(child.pid), "/T", "/F"], {
      stdio: "ignore",
      windowsHide: true,
    });
    await Promise.race([waitForChildExit(child), delay(3_000)]);
    if (child.exitCode !== null || child.signalCode !== null) return;
  }
  child.kill("SIGTERM");
  await Promise.race([waitForChildExit(child), delay(3_000)]);
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGKILL");
  await Promise.race([waitForChildExit(child), delay(2_000)]);
}

function waitForChildExit(process: ChildProcessWithoutNullStreams) {
  if (process.exitCode !== null || process.signalCode !== null) return Promise.resolve();
  return new Promise<void>((resolveExit) => process.once("exit", () => resolveExit()));
}

function nextServerEnvironment() {
  const environment = {
    ...process.env,
    NEXT_PUBLIC_AUTOSTREAM_DEMO: "false",
    NEXT_TELEMETRY_DISABLED: "1",
  };
  for (const key of [
    "AI_AGENT",
    "CURSOR_TRACE_ID",
    "CURSOR_AGENT",
    "CURSOR_EXTENSION_HOST_ROLE",
    "GEMINI_CLI",
    "CODEX_SANDBOX",
    "CODEX_CI",
    "CODEX_THREAD_ID",
    "ANTIGRAVITY_AGENT",
    "AUGMENT_AGENT",
    "OPENCODE_CLIENT",
    "CLAUDECODE",
    "CLAUDE_CODE",
    "CLAUDE_CODE_IS_COWORK",
    "REPL_ID",
    "COPILOT_MODEL",
    "COPILOT_ALLOW_ALL",
    "COPILOT_GITHUB_TOKEN",
  ]) {
    delete environment[key];
  }
  return environment;
}

async function waitForMapCount(
  counts: Map<string, number>,
  pathname: string,
  expected: number,
  description: string,
  timeoutMs: number,
) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if ((counts.get(pathname) || 0) === expected) return;
    await delay(20);
  }
  throw new Error(`${description} did not become ${expected}`);
}

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error));
}

function delay(milliseconds: number) {
  return new Promise<void>((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}

async function withTimeout<T>(promise: Promise<T>, milliseconds: number, message: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, rejectTimeout) => {
        timer = setTimeout(() => rejectTimeout(new Error(message)), milliseconds);
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}
