import { spawn, spawnSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import { existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, resolve } from "node:path";

import {
  FetchRequestLifecycle,
  RejectableEventWaiters,
  type FetchSettlementCommand,
  type RequestHandlerIdleOptions,
} from "./browser-request-lifecycle.mts";
import {
  BrowserLaunchError,
  launchBrowserProcessWithRetry,
  type BrowserLaunchSession,
} from "./browser-process-attempt.mts";
import { collectBrowserLaunchFacts } from "./browser-launch-profile.mts";

export type StubResponse = {
  status?: number;
  body?: unknown;
  delayMs?: number;
  waitUntil?: Promise<void>;
  requiredResponse?: boolean;
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
  private readonly browserLaunchSession: BrowserLaunchSession | undefined;
  private nextCommandId = 0;
  private routeResolver: RouteResolver = () => null;
  private readonly pendingCommands = new Map<number, PendingCommand>();
  private readonly eventWaiters = new RejectableEventWaiters();
  private readonly requestLifecycle = new FetchRequestLifecycle();
  private consoleErrorEvents = 0;
  private topLevelNavigationEvents = 0;
  private topLevelNavigationPending = false;
  private mainFrameId: string | undefined;
  private fatalError: Error | undefined;
  private closed = false;

  private constructor(
    browserProcess: ChildProcessWithoutNullStreams,
    userDataDirectory: string,
    socket: WebSocket,
    sessionId: string,
    browserLaunchSession?: BrowserLaunchSession,
  ) {
    this.browserProcess = browserProcess;
    this.userDataDirectory = userDataDirectory;
    this.socket = socket;
    this.sessionId = sessionId;
    this.browserLaunchSession = browserLaunchSession;
    this.socket.addEventListener("message", (event) => this.receive(String(event.data)));
    this.socket.addEventListener("close", () => this.handleSocketClose());
  }

  static async launch() {
    let browserPath: string;
    try {
      browserPath = resolveBrowserPath();
    } catch {
      const configuredPath = process.env.AUTOSTREAM_BROWSER_PATH || "unavailable";
      throw new BrowserLaunchError(
        [],
        collectBrowserLaunchFacts(configuredPath),
        "executable_unavailable",
      );
    }
    const browserLaunchSession = await launchBrowserProcessWithRetry({ browserPath });
    const { browserProcess, userDataDirectory, socket } = browserLaunchSession;

    try {
      const command = createCommandSender(socket);
      const target = await command("Target.createTarget", { url: "about:blank" });
      const attached = await command("Target.attachToTarget", { targetId: target.targetId, flatten: true });
      const harness = new BrowserHarness(
        browserProcess,
        userDataDirectory,
        socket,
        String(attached.sessionId),
        browserLaunchSession,
      );
      await harness.send("Page.enable");
      await harness.send("Runtime.enable");
      await harness.send("Fetch.enable", { patterns: [{ urlPattern: "*" }] });
      return harness;
    } catch (error) {
      try {
        await browserLaunchSession.close();
      } catch (cleanupError) {
        throw new AggregateError(
          [asError(error), asError(cleanupError)],
          "Browser launch failed and its owned attempt could not be cleaned",
          { cause: error },
        );
      }
      throw error;
    }
  }

  setRouteResolver(resolver: RouteResolver) {
    this.routeResolver = resolver;
  }

  clearRequestCounts(pathname?: string) {
    if (pathname) {
      this.requests.delete(pathname);
      this.responses.delete(pathname);
      this.responseStatuses.delete(pathname);
      return;
    }
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

  get safeFetchCancellationCount() {
    return this.requestLifecycle.safeCancellationCount;
  }

  assertNoFatalError() {
    if (this.fatalError) throw this.fatalError;
    this.requestLifecycle.assertHealthy();
  }

  async waitForRequestHandlersIdle(options: RequestHandlerIdleOptions = {}) {
    await this.requestLifecycle.waitForIdle(options);
  }

  async waitForRequestCount(pathname: string, count: number, timeoutMs = 10_000) {
    await waitForMapCount(this.requests, pathname, count, `request count for ${pathname}`, timeoutMs, () => this.assertNoFatalError());
  }

  async waitForResponseCount(pathname: string, count: number, timeoutMs = 10_000) {
    await waitForMapCount(this.responses, pathname, count, `response count for ${pathname}`, timeoutMs, () => this.assertNoFatalError());
  }

  async navigate(url: string) {
    this.requestLifecycle.beginNavigation("navigate");
    this.topLevelNavigationPending = true;
    const loaded = this.eventWaiters.wait("Page.loadEventFired", 20_000, `Timed out loading ${url}`);
    try {
      const result = await this.send("Page.navigate", { url });
      if (result.errorText) throw new Error(`Navigation failed: ${result.errorText}`);
      await loaded.promise;
    } catch (error) {
      this.topLevelNavigationPending = false;
      void loaded.promise.catch(() => {});
      loaded.cancel(asError(error));
      throw error;
    }
  }

  async reload() {
    this.requestLifecycle.beginNavigation("reload");
    this.topLevelNavigationPending = true;
    const loaded = this.eventWaiters.wait("Page.loadEventFired", 20_000, "Timed out reloading the page");
    try {
      await this.send("Page.reload", { ignoreCache: true });
      await loaded.promise;
    } catch (error) {
      this.topLevelNavigationPending = false;
      void loaded.promise.catch(() => {});
      loaded.cancel(asError(error));
      throw error;
    }
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
    this.requestLifecycle.beginNavigation("close");
    const closeError = new Error("Browser harness closed");
    this.eventWaiters.rejectAll(closeError);
    try {
      await withTimeout(this.sendBrowser("Browser.close"), 3_000, "Timed out closing the browser through CDP");
    } catch {
      // The browser commonly closes the CDP socket before acknowledging Browser.close.
    }
    try {
      if (this.browserLaunchSession) {
        await this.browserLaunchSession.close();
      } else {
        await terminateChildProcess(this.browserProcess);
        this.socket.close();
        await removeOwnedBrowserDirectory(this.userDataDirectory);
      }
    } finally {
      this.requestLifecycle.close(closeError);
    }
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
    if (message.method === "Page.frameRequestedNavigation") {
      const frameId = String(message.params?.frameId || "");
      if ((!this.mainFrameId || frameId === this.mainFrameId) && !this.topLevelNavigationPending) {
        this.requestLifecycle.beginNavigation("top-level-navigation");
        this.topLevelNavigationPending = true;
      }
    }
    if (message.method === "Page.frameNavigated") {
      const frame = message.params?.frame as { id?: string; parentId?: string } | undefined;
      if (frame && !frame.parentId) {
        this.mainFrameId = frame.id ? String(frame.id) : this.mainFrameId;
        this.topLevelNavigationPending = false;
        this.topLevelNavigationEvents += 1;
      }
    }
    if (message.method === "Fetch.requestPaused") {
      void this.handleRequest(message.params || {}).catch((error) => this.recordFatalError(error));
    }
    this.eventWaiters.resolve(message.method, message.params || {});
  }

  private async handleRequest(params: Record<string, unknown>) {
    const requestId = typeof params.requestId === "string" ? params.requestId : "";
    const request = params.request as { method: string; url: string };
    const pathname = new URL(request.url).pathname.replace(/\/$/, "") || "/";
    this.requestLifecycle.register({
      requestId,
      method: request.method,
      pathname,
      requiredResponse: false,
    });
    this.requests.set(pathname, (this.requests.get(pathname) || 0) + 1);
    const response = this.routeResolver({ method: request.method, url: request.url });
    this.requestLifecycle.setRequiredResponse(requestId, response !== null && response.requiredResponse !== false);
    if (!response) {
      const settled = await this.settleFetchRequest(requestId, "Fetch.continueRequest", { requestId });
      if (!settled) return;
      this.responses.set(pathname, (this.responses.get(pathname) || 0) + 1);
      return;
    }
    if (response.delayMs) await delay(response.delayMs);
    if (response.waitUntil) await response.waitUntil;
    const body = response.body === undefined ? "" : JSON.stringify(response.body);
    const responseStatus = response.status ?? 200;
    const settled = await this.settleFetchRequest(requestId, "Fetch.fulfillRequest", {
      requestId,
      responseCode: responseStatus,
      responseHeaders: [{ name: "content-type", value: "application/json; charset=utf-8" }],
      body: Buffer.from(body).toString("base64"),
    });
    if (!settled) return;
    this.responses.set(pathname, (this.responses.get(pathname) || 0) + 1);
    const statuses = this.responseStatuses.get(pathname) || [];
    statuses.push(responseStatus);
    this.responseStatuses.set(pathname, statuses);
  }

  private async settleFetchRequest(
    requestId: string,
    command: FetchSettlementCommand,
    params: Record<string, unknown>,
  ) {
    const attempt = this.requestLifecycle.beginSettlement(requestId, command);
    try {
      await this.send(command, params);
    } catch (error) {
      this.requestLifecycle.handleSettlementError(attempt, error);
      return false;
    }
    this.requestLifecycle.completeSettlement(attempt);
    return true;
  }

  private recordFatalError(error: unknown) {
    if (this.closed || this.fatalError) return;
    this.fatalError = asError(error);
    this.requestLifecycle.fail(this.fatalError);
    this.eventWaiters.rejectAll(this.fatalError);
    for (const command of this.pendingCommands.values()) command.reject(this.fatalError);
    this.pendingCommands.clear();
  }

  private handleSocketClose() {
    const launchDiagnostics = this.browserLaunchSession?.failureDiagnostics();
    const error = new Error(this.closed
      ? "Browser harness closed"
      : `Browser CDP connection closed${launchDiagnostics ? `: ${launchDiagnostics}` : ""}`);
    if (!this.closed) {
      this.recordFatalError(error);
      return;
    }
    for (const command of this.pendingCommands.values()) command.reject(error);
    this.pendingCommands.clear();
    this.eventWaiters.rejectAll(error);
    this.requestLifecycle.close(error);
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

export function resolveBrowserPath() {
  const configuredPath = process.env.AUTOSTREAM_BROWSER_PATH;
  if (configuredPath) {
    if (!existsSync(configuredPath)) throw new Error("Configured Chrome/Chromium executable was not found");
    return configuredPath;
  }
  const browserPath = defaultBrowserCandidates.find((candidate) => existsSync(candidate));
  if (!browserPath) throw new Error("Chrome/Chromium was not found; set AUTOSTREAM_BROWSER_PATH");
  return browserPath;
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
  assertHealthy: () => void,
) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    assertHealthy();
    if ((counts.get(pathname) || 0) === expected) return;
    await delay(20);
  }
  assertHealthy();
  throw new Error(`${description} did not become ${expected}; last count: ${counts.get(pathname) || 0}`);
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
