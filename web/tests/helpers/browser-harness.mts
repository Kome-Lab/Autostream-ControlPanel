import type { BrowserOperationBoundary, BrowserOperationOptions, BrowserOperationRecord } from "./browser-operation-boundary.mts";
import { CommandDeadlines, commandTimeoutMs, createCommandSender, type CDPResponse, type PendingCommand } from "./browser-command-deadline.mts";
import { NativeFocusObserver } from "./browser-ua-focus.mts";
import { spawnSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import { rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, resolve } from "node:path";
import { FetchRequestLifecycle, RejectableEventWaiters, type FetchDiagnosticContext, type FetchSettlementCommand, type RequestHandlerIdleOptions } from "./browser-request-lifecycle.mts";
import { BrowserLaunchError, launchBrowserProcessWithRetry, type BrowserLaunchSession } from "./browser-process-attempt.mts";
import { collectBrowserLaunchFacts } from "./browser-launch-profile.mts";
import { BrowserNetworkCancelProof } from "./browser-network-cancel-proof.mts";
import { declareLoginReturnAuthPhase, retireDeclaredAuth } from "./browser-declared-auth-retirement.mts";
import { optionalBootstrapCancellationEvent, waitForOptionalBootstrapCancellation } from "./browser-optional-app-cancellation.mts";
import { resolveBrowserPath } from "./browser-executable.mts";
import { waitForMapCount } from "./browser-response-count.mts";
import { asError, delay, withTimeout } from "./browser-process-timing.mts";


export type StubResponse = {
  status?: number;
  body?: unknown;
  bodyBase64?: string;
  contentType?: string;
  delayMs?: number;
  waitUntil?: Promise<void>;
  requiredResponse?: boolean;
  observeBootstrapCancellation?: true;
};

export type StubRequest = {
  method: string;
  url: string;
  postData?: string;
};

export type RouteResolver = (request: StubRequest) => StubResponse | null;

export type BrowserNativeKey = "Enter" | "Space" | "Escape";

export type BrowserOwnedLaunchFacts = Readonly<{
  pid: number;
  executable: string;
  profile: string;
  endpoint: string;
}>;

type BrowserNativeKeyInput = Readonly<{
  key: string;
  code: string;
  windowsVirtualKeyCode: number;
  nativeVirtualKeyCode: number;
  text?: string;
}>;

const browserNativeKeyInputs = {
  Enter: {
    key: "Enter",
    code: "Enter",
    windowsVirtualKeyCode: 13,
    nativeVirtualKeyCode: 13,
    text: "\r",
  },
  Space: {
    key: " ",
    code: "Space",
    windowsVirtualKeyCode: 32,
    nativeVirtualKeyCode: 32,
    text: " ",
  },
  Escape: {
    key: "Escape",
    code: "Escape",
    windowsVirtualKeyCode: 27,
    nativeVirtualKeyCode: 27,
  },
} satisfies Readonly<Record<BrowserNativeKey, BrowserNativeKeyInput>>;

export class BrowserHarness {
  readonly requests = new Map<string, number>();
  readonly responses = new Map<string, number>();
  readonly responseStatuses = new Map<string, number[]>();

  private readonly browserProcess: ChildProcessWithoutNullStreams;
  private readonly userDataDirectory: string;
  private readonly socket: WebSocket;
  private readonly sessionId: string;
  private readonly browserLaunchSession: BrowserLaunchSession | undefined;
  private operationBoundary: BrowserOperationBoundary | undefined;
  private manualHeaded = false;
  private nextCommandId = 0;
  private diagnosticNavigateId: number | undefined;
  private diagnosticNavigateResponded = false;
  private generationFailureReported = false;
  private generationFailureJSON: string | undefined;
  private routeResolver: RouteResolver = () => null;
  private readonly pendingCommands = new Map<number, PendingCommand>();
  private readonly commandDeadlines = new CommandDeadlines(error => {
    this.recordFatalError(error);
    this.rejectPendingCommands(error); // Also fence a deadline racing owned close.
  });
  private closePromise: Promise<void> | undefined;
  private readonly eventWaiters = new RejectableEventWaiters();
  private readonly requestLifecycle = new FetchRequestLifecycle();
  private readonly networkCancellation?: BrowserNetworkCancelProof;
  private consoleErrorEvents = 0;
  private topLevelNavigationEvents = 0;
  private topLevelNavigationPending = false;
  private mainFrameId: string | undefined;
  private fatalError: Error | undefined;
  private fetchFailureReported = false;
  private closed = false;
  private readonly nativeFocusObserver = new NativeFocusObserver((method, params) => this.send(method, params));

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
    this.networkCancellation = new BrowserNetworkCancelProof(this.requestLifecycle, sessionId);
    this.browserLaunchSession = browserLaunchSession;
    this.socket.addEventListener("message", (event) => this.receive(String(event.data)));
    this.socket.addEventListener("close", () => this.handleSocketClose());
  }

  static async launch(options: Readonly<{ manualZoom?: "headed" }> = {}) {
    if (options.manualZoom !== undefined && options.manualZoom !== "headed") throw new Error("Unsupported manual zoom launch mode");
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
    const browserLaunchSession = await launchBrowserProcessWithRetry({ browserPath, ...(options.manualZoom ? { manualZoom: options.manualZoom } : {}) });
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
      harness.manualHeaded = options.manualZoom === "headed";
      await harness.send("Page.enable");
      await harness.send("Runtime.enable");
      await harness.send("Network.enable");
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

  // Explicit G3 opt-in. Default suite transport and dialog policy stay unchanged.
  async runBoundedOperation<T>(options: BrowserOperationOptions, write: (record: BrowserOperationRecord) => void, action: () => Promise<T>): Promise<T> {
    const { BrowserOperationBoundary } = await import("./browser-operation-boundary.mts");
    if (!this.manualHeaded || this.operationBoundary || this.closed || this.fatalError) throw new Error("G3 operation requires an idle owned headed target");
    const scope = new BrowserOperationBoundary(options, {
      write,
      abort: (error) => this.recordFatalError(error),
      acceptDiscard: () => this.send("Page.handleJavaScriptDialog", { accept: true }),
    });
    this.operationBoundary = scope;
    try { return await scope.run(action); }
    finally { this.operationBoundary = undefined; }
  }

  async configureDeterministicDocument(options: Readonly<{ source: string; timezone: string; locale: string }>) {
    await this.send("Emulation.setTimezoneOverride", { timezoneId: options.timezone });
    await this.send("Emulation.setLocaleOverride", { locale: options.locale });
    await this.send("Page.addScriptToEvaluateOnNewDocument", { source: options.source });
  }

  async observeNativeFocus() {
    this.assertNoFatalError();
    if (this.closed) throw new Error("Browser harness closed");
    return this.nativeFocusObserver.observe();
  }

  async settleNativePublicMedia(source: string) {
    this.assertNoFatalError();
    if (this.closed) throw new Error("Browser harness closed");
    const { settleNativeMediaLoading, nativePublicMediaReadyExpression } = await import("./browser-native-media-settlement.mts");
    await this.waitFor(nativePublicMediaReadyExpression(source), value => value === true, "public media fixture error state before native baseline");
    return settleNativeMediaLoading((method, params) => this.send(method, params), source);
  }

  ownedLaunchFacts(): BrowserOwnedLaunchFacts {
    this.assertNoFatalError();
    if (this.closed) throw new Error("Browser harness closed");
    const pid = this.browserProcess.pid;
    if (pid === undefined || !Number.isSafeInteger(pid) || pid <= 0) throw new Error("Browser process identity unavailable");
    return Object.freeze({ pid, executable: this.browserProcess.spawnfile, profile: this.userDataDirectory, endpoint: this.socket.url });
  }

  // A scoped test input/observer is removed by its original CDP registration ID.
  async installNewDocumentScript(source: string): Promise<() => Promise<void>> {
    const result = await this.send("Page.addScriptToEvaluateOnNewDocument", { source });
    if (typeof result.identifier !== "string") throw new Error("new-document registration ID missing");
    let removed = false;
    return async () => {
      if (removed) return;
      await this.send("Page.removeScriptToEvaluateOnNewDocument", { identifier: result.identifier });
      removed = true;
    };
  }

  async browserVersion() {
    return this.sendBrowser("Browser.getVersion");
  }

  async movePointerToCaptureMargin(point: Readonly<{ x: number; y: number }>): Promise<void> {
    this.assertNoFatalError();
    if (this.closed) throw new Error("Browser harness closed");
    if (![point.x, point.y].every(value => Number.isFinite(value) && value > 0 && value < 30_000)) throw new Error("Invalid bounded pointer margin");
    const safe = await this.evaluate<boolean>(`(() => {
      const x=${point.x},y=${point.y},hit=document.elementFromPoint(x,y);
      return x<Math.min(innerWidth,document.documentElement.clientWidth)-2 && y<Math.min(innerHeight,document.documentElement.clientHeight)-2
        && !!hit && !hit.closest('button,a,input,select,textarea,[tabindex],[role=button],[role=link],[role=dialog],[role=alertdialog]');
    })()`);
    if (safe !== true) throw new Error("Pointer target is not a verified noninteractive viewport margin");
    await this.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: point.x, y: point.y, button: "none", buttons: 0, modifiers: 0 });
  }

  async captureViewportEvidence() {
    this.assertNoFatalError();
    if (this.closed) throw new Error("Browser harness closed");
    const layout = await this.send("Page.getLayoutMetrics");
    const view = layout.cssVisualViewport as { clientWidth?: number; clientHeight?: number; pageX?: number; pageY?: number; scale?: number } | undefined;
    const width = Math.ceil(view?.clientWidth ?? 0), height = Math.ceil(view?.clientHeight ?? 0);
    const pageX = view?.pageX, pageY = view?.pageY, scale = view?.scale;
    if (![width, height, pageX, pageY, scale].every(value => typeof value === "number" && Number.isFinite(value)) || width <= 0 || height <= 0 || width > 30_000 || height > 30_000 || Math.abs(pageX!) > 30_000 || Math.abs(pageY!) > 30_000 || scale !== 1) throw new Error("Invalid bounded viewport evidence geometry");
    const screenshot = await this.send("Page.captureScreenshot", {
      format: "png", fromSurface: true, captureBeyondViewport: false,
      clip: { x: pageX, y: pageY, width, height, scale: 1 },
    });
    if (typeof screenshot.data !== "string" || !screenshot.data) throw new Error("Viewport evidence was empty");
    const png = Buffer.from(screenshot.data, "base64");
    if (png.length < 24 || !png.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])) || png.readUInt32BE(16) !== width || png.readUInt32BE(20) !== height) throw new Error("Viewport evidence dimensions mismatch");
    this.assertNoFatalError();
    if (this.closed) throw new Error("Browser harness closed during viewport capture");
    return { png, viewport: { width, height, pageX, pageY, scale } };
  }

  async captureScreenshot() {
    const layout = await this.send("Page.getLayoutMetrics");
    const size = layout.cssContentSize as { width?: number; height?: number } | undefined;
    const width = Math.ceil(size?.width ?? 0);
    const height = Math.ceil(size?.height ?? 0);
    if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0 || width > 30_000 || height > 30_000) {
      throw new Error("Invalid or unbounded product screenshot dimensions");
    }
    const screenshot = await this.send("Page.captureScreenshot", {
      format: "png", fromSurface: true, captureBeyondViewport: true,
      clip: { x: 0, y: 0, width, height, scale: 1 },
    });
    if (typeof screenshot.data !== "string" || screenshot.data.length === 0) throw new Error("Product screenshot was empty");
    return Buffer.from(screenshot.data, "base64");
  }

  declareLoginReturnAuthPhase(url: string) { return declareLoginReturnAuthPhase(this.networkCancellation, { lifecycle: this.requestLifecycle, session: this.sessionId, current: () => ({ generation: this.requestLifecycle.generation, frameId: this.mainFrameId, closed: this.closed }), assertHealthy: () => this.assertNoFatalError() }, url); }
  get declaredAuthRetirementCount() { return this.requestLifecycle.declaredAuthRetirementCount; }
  setRouteResolver(resolver: RouteResolver) {
    this.routeResolver = resolver;
  }

  setFetchDiagnosticContext(context: FetchDiagnosticContext) {
    try { this.requestLifecycle.setDiagnosticContext(context); }
    catch { /* Diagnostic labels cannot interrupt the scenario. */ }
  }

  get fetchFailureDiagnosticJSON() {
    return this.generationFailureJSON;
  }

  private fetchNavigationObservation() {
    return {
      page_navigate_sent: this.diagnosticNavigateId !== undefined,
      page_navigate_response_received: this.diagnosticNavigateResponded,
      top_level_navigation_pending: this.topLevelNavigationPending,
    };
  }

  private reportFetchGenerationFailure(requestId?: string, legacy?: ReturnType<FetchRequestLifecycle["settlementFailureDiagnostic"]>) {
    if (this.closed || this.fatalError || this.generationFailureReported) return;
    this.generationFailureReported = true;
    try {
      let generation;
      try { generation = this.requestLifecycle.generationFailureDiagnostic(requestId, this.fetchNavigationObservation()); }
      catch { /* Preserve the established diagnostic if the extra collection fails. */ }
      const base = { ...legacy, generation_diagnostic: generation };
      let json = JSON.stringify(base);
      try {
        const network = requestId && this.networkCancellation?.failureDiagnostic(requestId, this.requestLifecycle, this.sessionId,
          { generation: this.requestLifecycle.generation, frameId: this.mainFrameId });
        if (network) {
          const enriched = JSON.stringify({ ...base, network_cancellation_diagnostic: network });
          const prefix = legacy ? "BROWSER_FETCH_FAILURE " : "BROWSER_FETCH_GENERATION_DIAGNOSTIC ";
          if (Buffer.byteLength(prefix + enriched + "\n", "utf8") <= 2048) json = enriched;
          else {
            const bounded = JSON.stringify({ ...base, network_cancellation_diagnostic_unavailable: "oversized" });
            if (Buffer.byteLength(prefix + bounded + "\n", "utf8") <= 2048) json = bounded;
          }
        }
      } catch { /* Keep the established diagnostic and original settlement failure. */ }
      const line = `${legacy ? "BROWSER_FETCH_FAILURE" : "BROWSER_FETCH_GENERATION_DIAGNOSTIC"} ${json}`;
      if (Buffer.byteLength(line + "\n", "utf8") > 4096) return;
      this.generationFailureJSON = json;
      console.error(line);
    } catch { /* Diagnostics must never replace the original Fetch failure. */ }
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
  get preSettlementFetchCancellationCount() { return this.requestLifecycle.preSettlementCancellationCount; }

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
    void loaded.promise.catch(() => {}); // Observed even while Page.navigate awaits its response.
    try {
      const result = await this.send("Page.navigate", { url });
      if (result.errorText) throw new Error(`Navigation failed: ${result.errorText}`);
      await loaded.promise;
    } catch (error) {
      this.topLevelNavigationPending = false;
      loaded.cancel(asError(error));
      throw error;
    }
  }

  async reload() {
    this.requestLifecycle.beginNavigation("reload");
    this.topLevelNavigationPending = true;
    const loaded = this.eventWaiters.wait("Page.loadEventFired", 20_000, "Timed out reloading the page");
    void loaded.promise.catch(() => {});
    try {
      await this.send("Page.reload", { ignoreCache: true });
      await loaded.promise;
    } catch (error) {
      this.topLevelNavigationPending = false;
      loaded.cancel(asError(error));
      throw error;
    }
  }

  async evaluate<T>(expression: string, timeoutMs = commandTimeoutMs("Runtime.evaluate")): Promise<T> {
    const response = await this.send("Runtime.evaluate", {
      expression,
      awaitPromise: true,
      returnByValue: true,
      userGesture: true,
    }, timeoutMs);
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
      lastValue = await this.evaluate<T>(expression, Math.min(commandTimeoutMs("Runtime.evaluate"), Math.max(1, deadline - Date.now())));
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

  async pressNativeKey(key: BrowserNativeKey): Promise<void> {
    if (this.closed) throw new Error("Browser harness closed");
    this.assertNoFatalError();
    const input: BrowserNativeKeyInput = browserNativeKeyInputs[key];
    const base = {
      key: input.key,
      code: input.code,
      windowsVirtualKeyCode: input.windowsVirtualKeyCode,
      nativeVirtualKeyCode: input.nativeVirtualKeyCode,
    };
    const text = input.text === undefined
      ? {}
      : { text: input.text, unmodifiedText: input.text };
    await this.send("Input.dispatchKeyEvent", { ...base, ...text, type: "keyDown" });
    await this.send("Input.dispatchKeyEvent", { ...base, type: "keyUp" });
  }

  async pressTab(direction: "forward" | "backward"): Promise<void> {
    if (direction !== "forward" && direction !== "backward") throw new Error("Unsupported Tab direction");
    if (this.closed) throw new Error("Browser harness closed");
    this.assertNoFatalError();
    const tabInput = { key: "Tab", code: "Tab", windowsVirtualKeyCode: 9, nativeVirtualKeyCode: 9, modifiers: direction === "backward" ? 8 : 0 };
    await this.send("Input.dispatchKeyEvent", { ...tabInput, type: "keyDown" });
    await this.send("Input.dispatchKeyEvent", { ...tabInput, type: "keyUp" });
  }

  async pressKey(key: string, code = key) {
    await this.send("Input.dispatchKeyEvent", { type: "keyDown", key, code });
    await this.send("Input.dispatchKeyEvent", { type: "keyUp", key, code });
  }

  get commandDeadlineFailure() { return this.commandDeadlines.failure; }

  close() {
    this.closePromise ??= this.closeOwned();
    return this.closePromise;
  }

  private async closeOwned() {
    this.closed = true;
    this.nativeFocusObserver.clear();
    this.requestLifecycle.beginNavigation("close");
    const closeError = new Error("Browser harness closed");
    this.eventWaiters.rejectAll(closeError);
    this.rejectPendingCommands(closeError);
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
      this.rejectPendingCommands(closeError);
      this.requestLifecycle.close(closeError);
    }
  }

  private async send(method: string, params: Record<string, unknown> = {}, timeoutMs = commandTimeoutMs(method)) {
    return this.sendCommand(method, params, this.sessionId, timeoutMs);
  }

  private async sendBrowser(method: string, params: Record<string, unknown> = {}) {
    return this.sendCommand(method, params);
  }

  private sendCommand(method: string, params: Record<string, unknown>, sessionId?: string, timeoutMs = commandTimeoutMs(method)) {
    if (this.fatalError) return Promise.reject(this.fatalError);
    if (this.closed && method !== "Browser.close") return Promise.reject(new Error("Browser harness closed"));
    const scope = this.operationBoundary;
    const observed = scope?.beforeCommand(method, params);
    const id = ++this.nextCommandId;
    const payload = sessionId ? { id, method, params, sessionId } : { id, method, params };
    const result = new Promise<Record<string, unknown>>((resolveCommand, rejectCommand) => {
      // G3 owns its operation bound; Browser.close owns its existing ACK/fallback bound.
      const cancel = scope || method === "Browser.close" ? () => {} : this.commandDeadlines.arm(id, method, timeoutMs);
      this.pendingCommands.set(id, {
        resolve: value => { cancel(); resolveCommand(value); },
        reject: error => { cancel(); rejectCommand(error); },
      });
      if (method === "Page.navigate") {
        this.diagnosticNavigateId = id;
        this.diagnosticNavigateResponded = false;
      }
      try { this.socket.send(JSON.stringify(payload)); }
      catch (error) { this.recordFatalError(error); this.rejectPendingCommands(asError(error)); }
    });
    if (!scope || observed === undefined) return result;
    return result.then((value) => { scope.afterCommand(observed, true); return value; },
      (error) => { scope.afterCommand(observed, false, asError(error)); throw error; });
  }

  private receive(rawMessage: string) {
    const message = JSON.parse(rawMessage) as CDPResponse;
    if (message.id) {
      const command = this.pendingCommands.get(message.id);
      if (!command) return;
      if (message.id === this.diagnosticNavigateId) this.diagnosticNavigateResponded = true;
      this.pendingCommands.delete(message.id);
      if (message.error) command.reject(new Error(message.error.message || "CDP command failed"));
      else command.resolve(message.result || {});
      return;
    }
    if (!message.method || (message.sessionId && message.sessionId !== this.sessionId)) return;
    this.networkCancellation?.observe(message, this.requestLifecycle.generation);
    const cancellationEvent = optionalBootstrapCancellationEvent(message.params?.requestId);
    if (message.sessionId === this.sessionId && message.method === "Network.loadingFailed" && message.params?.canceled === true && message.params?.errorText === "net::ERR_ABORTED" && cancellationEvent) this.eventWaiters.resolve(cancellationEvent, message.params);
    if (!this.operationBoundary && message.method === "Page.javascriptDialogOpening") {
      this.recordFatalError(new Error("UNEXPECTED_JAVASCRIPT_DIALOG"));
    }
    if (this.operationBoundary && message.method === "Page.javascriptDialogOpening") {
      this.operationBoundary.dialogOpening(message.params || {}, message.sessionId === this.sessionId && (message.params?.frameId === undefined || message.params.frameId === this.mainFrameId));
    }
    if (this.operationBoundary && message.method === "Page.javascriptDialogClosed") {
      this.operationBoundary.dialogClosed(message.params || {}, message.sessionId === this.sessionId && (message.params?.frameId === undefined || message.params.frameId === this.mainFrameId));
    }
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
      void this.handleRequest(message.params || {}).catch((error) => {
        if (!this.fetchFailureReported) this.reportFetchGenerationFailure();
        this.recordFatalError(error);
      });
    }
    this.eventWaiters.resolve(message.method, message.params || {});
  }

  private async handleRequest(params: Record<string, unknown>) {
    const requestId = typeof params.requestId === "string" ? params.requestId : "";
		const request = params.request as { method: string; url: string; postData?: string };
    const pathname = new URL(request.url).pathname.replace(/\/$/, "") || "/";
    const generation = this.requestLifecycle.register({
      requestId,
      method: request.method,
      pathname,
      requiredResponse: false,
    });
    this.requestLifecycle.observeRegistration(requestId, params, this.mainFrameId, this.fetchNavigationObservation());
    this.requests.set(pathname, (this.requests.get(pathname) || 0) + 1);
		const response = this.routeResolver({ method: request.method, url: request.url, postData: request.postData });
    this.requestLifecycle.setRequiredResponse(requestId, response !== null && response.requiredResponse !== false);
    this.networkCancellation?.registerContract(requestId, { generation, pathname, optionalFixture: response !== null && response.requiredResponse === false });
    if (retireDeclaredAuth(this.networkCancellation, requestId, response)) return;
    if (!response) {
      const settled = await this.settleFetchRequest(requestId, "Fetch.continueRequest", { requestId });
      if (!settled) return;
      this.responses.set(pathname, (this.responses.get(pathname) || 0) + 1);
      return;
    }
    if (response.observeBootstrapCancellation) { this.assertNoFatalError(); await waitForOptionalBootstrapCancellation(this.eventWaiters, request, response, params.networkId); }
    if (response.delayMs) await delay(response.delayMs);
    if (response.waitUntil) await response.waitUntil;
    const body = response.body === undefined ? "" : JSON.stringify(response.body);
    const responseStatus = response.status ?? 200;
    const settled = await this.settleFetchRequest(requestId, "Fetch.fulfillRequest", {
      requestId,
      responseCode: responseStatus,
      responseHeaders: [{ name: "content-type", value: response.contentType ?? "application/json; charset=utf-8" }],
      body: response.bodyBase64 ?? Buffer.from(body).toString("base64"),
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
    if (!this.closed && !this.fatalError && this.networkCancellation) {
      this.assertNoFatalError();
      const ticket = this.networkCancellation.claimBeforeSettlement(requestId, command, { generation: this.requestLifecycle.generation, frameId: this.mainFrameId });
      if (ticket) {
        this.requestLifecycle.completePreSettlementCancellation(requestId, command, ticket);
        return false;
      }
    }
    const attempt = this.requestLifecycle.beginSettlement(requestId, command);
    this.requestLifecycle.observeSettlement(requestId, this.fetchNavigationObservation());
    try {
      await this.send(command, params);
    } catch (error) {
      try {
        this.requestLifecycle.handleSettlementError(attempt, error);
      } catch (fatalError) {
        if (!this.closed && !this.fatalError && !this.fetchFailureReported) {
          this.fetchFailureReported = true;
          try {
            this.reportFetchGenerationFailure(attempt.requestId, this.requestLifecycle.settlementFailureDiagnostic(attempt, fatalError));
          } catch {
            // Diagnostics must never replace the original settlement failure.
          }
        }
        throw fatalError;
      }
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
    this.rejectPendingCommands(this.fatalError);
  }

  private rejectPendingCommands(error: Error) {
    for (const command of this.pendingCommands.values()) command.reject(error);
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
    this.rejectPendingCommands(error);
    this.eventWaiters.rejectAll(error);
    this.requestLifecycle.close(error);
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

export { resolveBrowserPath };
export { ensureWebServer, captureNextGeneratedFiles, restoreNextGeneratedFiles, nextGeneratedFilesMatch, type NextGeneratedFilesSnapshot } from "./browser-next-server.mts";
