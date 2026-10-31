import test from "node:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { BrowserHarness, ensureWebServer } from "./helpers/browser-harness.mts";
import { webRoot, requestedBaseUrl, createBrowserRouteFixture } from "./ui-browser-fixture.mts";
import { runQueryStatesScenario, runLogoutScenario, runSessionExpiryScenario, runAuthMeExpiryScenario } from "./ui-browser-query-auth-scenarios.mts";

type CI017HarnessAccess = {
  socket: WebSocket;
  send(method: string, params?: Record<string, unknown>): Promise<Record<string, unknown>>;
  requests: Map<string, number>;
  responses: Map<string, number>;
  responseStatuses: Map<string, number[]>;
  navigationCount: number;
  consoleErrorCount: number;
};

function createCI017Recorder(browser: BrowserHarness) {
  const internals = browser as unknown as CI017HarnessAccess;
  const events: Array<Record<string, unknown>> = [];
  const networkRequestIds = new Set<string>();
  const startedAt = performance.now();
  const record = (event: string, detail: Record<string, unknown> = {}) => {
    if (events.length >= 160) return;
    events.push({ at_ms: Math.round((performance.now() - startedAt) * 100) / 100, event, ...detail });
  };
  const listener = (message: MessageEvent) => {
    let packet: { method?: string; params?: Record<string, unknown> };
    try { packet = JSON.parse(String(message.data)); } catch { return; }
    const params = packet.params || {};
    if (packet.method === "Network.requestWillBeSent" && params.request && typeof params.request === "object") {
      const request = params.request as { url?: string; method?: string };
      try {
        if (new URL(String(request.url || "")).pathname === "/setup/status" && request.method === "GET") {
          const id = String(params.requestId || "");
          networkRequestIds.add(id);
          const initiator = params.initiator as { type?: string; stack?: { callFrames?: Array<{ functionName?: string; url?: string; lineNumber?: number; columnNumber?: number }> } } | undefined;
          const callFrames = (initiator?.stack?.callFrames || []).slice(0, 8).map(frame => ({
            function_name: String(frame.functionName || "").slice(0, 100),
            url: String(frame.url || "").slice(0, 180),
            line: Number(frame.lineNumber) || 0,
            column: Number(frame.columnNumber) || 0
          }));
          record("network_request", { request_id: id, loader_id_present: typeof params.loaderId === "string",
            frame_id_present: typeof params.frameId === "string", resource_type: String(params.type || "unknown"),
            initiator_type: String(initiator?.type || "unknown"), initiator_frames: callFrames });
        }
      } catch {}
    }
    if (packet.method === "Network.responseReceived" && networkRequestIds.has(String(params.requestId || ""))) {
      const response = params.response as { status?: number } | undefined;
      record("network_response", { request_id: String(params.requestId || ""), status: Number(response?.status) || 0 });
    }
    if (packet.method === "Network.loadingFinished" && networkRequestIds.has(String(params.requestId || "")))
      record("network_finished", { request_id: String(params.requestId || "") });
    if (packet.method === "Network.loadingFailed" && networkRequestIds.has(String(params.requestId || "")))
      record("network_failed", { request_id: String(params.requestId || ""), canceled: params.canceled === true,
        error_text: typeof params.errorText === "string" ? params.errorText.slice(0, 80) : "unknown" });
    if (packet.method === "Fetch.requestPaused" && params.request && typeof params.request === "object") {
      const request = params.request as { url?: string; method?: string };
      try {
        if (new URL(String(request.url || "")).pathname === "/setup/status") record("fetch_paused", {
          fetch_request_id: String(params.requestId || ""), network_id: typeof params.networkId === "string" ? params.networkId : null,
          method: request.method || "unknown", resource_type: typeof params.resourceType === "string" ? params.resourceType : "unknown",
          frame_id_present: typeof params.frameId === "string"
        });
      } catch {}
    }
    if (packet.method === "Page.frameRequestedNavigation") {
      record("frame_requested_navigation", { reason: String(params.reason || "unknown"), disposition: String(params.disposition || "unknown"), frame_id_present: typeof params.frameId === "string" });
    }
    if (packet.method === "Page.frameNavigated" && params.frame && typeof params.frame === "object") {
      const frame = params.frame as { url?: string; parentId?: string };
      if (!frame.parentId) {
        try { record("main_frame_navigated", { pathname: new URL(String(frame.url || "about:blank")).pathname }); } catch {}
      }
    }
  };
  internals.socket.addEventListener("message", listener);
  return {
    record,
    events,
    async installFetchProbe() {
      const source = [
        "(() => {",
        "const traceKey = '__ci017SetupTraceV3';",
        "const initKey = '__ci017SetupTraceV3Initialized';",
        "try { if (!sessionStorage.getItem(initKey)) { sessionStorage.setItem(initKey, '1'); sessionStorage.setItem(traceKey, '[]'); } } catch {}",
        "let trace = []; try { trace = JSON.parse(sessionStorage.getItem(traceKey) || '[]'); if (!Array.isArray(trace)) trace = []; } catch { trace = []; }",
        "globalThis.__ci017SetupFetchTrace = trace;",
        "const original = window.fetch;",
        "const stack = () => String(new Error().stack || '').split('\\n').slice(2, 12).map(line => line.slice(0, 240));",
        "for (const method of ['pushState', 'replaceState']) { const originalMethod = history[method]; history[method] = function(state, title, url) { let target = null; try { const parsed = new URL(String(url || location.href), location.href); target = parsed.pathname + parsed.search + parsed.hash; } catch {} record({ event: 'history_' + method, target, stack: stack() }); return originalMethod.apply(this, arguments); }; }",
        "window.addEventListener('popstate', () => record({ event: 'popstate', target: location.pathname + location.search + location.hash, stack: stack() }));",
        "window.addEventListener('hashchange', () => record({ event: 'hashchange', target: location.pathname + location.search + location.hash, stack: stack() }));",
        "window.addEventListener('beforeunload', () => record({ event: 'beforeunload', target: location.pathname + location.search + location.hash, stack: stack() }));",
        "window.addEventListener('pagehide', event => record({ event: 'pagehide', target: location.pathname + location.search + location.hash, persisted: event.persisted, stack: stack() }));",
        "window.addEventListener('pageshow', event => record({ event: 'pageshow', target: location.pathname + location.search + location.hash, persisted: event.persisted }));",
        "window.addEventListener('visibilitychange', () => record({ event: 'visibilitychange', visibility: document.visibilityState, target: location.pathname + location.search + location.hash }));",
        "const formObserver = new MutationObserver(records => { for (const mutation of records) { for (const node of mutation.removedNodes) if (node instanceof Element && (node.matches('form') || node.querySelector('form'))) record({ event: 'login_form_removed' }); for (const node of mutation.addedNodes) if (node instanceof Element && (node.matches('form') || node.querySelector('form'))) record({ event: 'login_form_added' }); } });",
        "formObserver.observe(document.documentElement, { childList: true, subtree: true });",
        "const record = entry => { if (trace.length < 180) { trace.push({ at_ms: Math.round(performance.now() * 100) / 100, pathname: location.pathname, ...entry }); try { sessionStorage.setItem(traceKey, JSON.stringify(trace)); } catch {} } };",
        "window.fetch = function(input, init) {",
        "let pathname = ''; try { pathname = new URL(typeof input === 'string' ? input : input.url, location.href).pathname; } catch {}",
        "if (pathname !== '/setup/status') return original.apply(this, arguments);",
        "const signal = init?.signal || (typeof Request !== 'undefined' && input instanceof Request ? input.signal : undefined);",
        "record({ event: 'fetch_start', signal_present: !!signal, signal_aborted: signal?.aborted === true, stack: stack() });",
        "if (signal) signal.addEventListener('abort', () => record({ event: 'fetch_abort', signal_aborted: signal.aborted === true }), { once: true });",
        "let pending; try { pending = original.apply(this, arguments); } catch (error) { record({ event: 'fetch_throw', error_name: error instanceof Error ? error.name : 'unknown' }); throw error; }",
        "return pending.then(response => { record({ event: 'fetch_response', status: response.status, signal_aborted: signal?.aborted === true }); return response; }, error => { record({ event: 'fetch_reject', error_name: error instanceof Error ? error.name : 'unknown', signal_aborted: signal?.aborted === true }); throw error; });",
        "};",
        "})();"
      ].join("\n");
      await internals.send("Page.addScriptToEvaluateOnNewDocument", { source });
      record("fetch_probe_installed");
    },
    async snapshot() {
      let page: Record<string, unknown> = { observation_error: true };
      let fetchTrace: unknown[] = [];
      try {
        page = await browser.evaluate<Record<string, unknown>>("(() => ({ pathname: location.pathname, reason: new URL(location.href).searchParams.get('reason'), document_ready_state: document.readyState, login_fallback_present: [...document.querySelectorAll('main p')].some(node => node.textContent?.includes('ログイン情報を読み込んでいます。')), username_input_present: !!document.querySelector('input[autocomplete=\"username\"]'), password_input_present: !!document.querySelector('input[autocomplete=\"current-password\"]'), login_form_present: !!document.querySelector('form'), main_heading_present: !!document.querySelector('main h1'), protected_navigation_present: !!document.querySelector('nav[aria-label=\"Admin navigation\"]'), account_menu_present: !!document.querySelector('button[aria-label=\"Account menu\"]') }))()");
        fetchTrace = await browser.evaluate<unknown[]>("globalThis.__ci017SetupFetchTrace || []");
      } catch (error) {
        page = { observation_error: true, error_name: error instanceof Error ? error.name : "unknown" };
      }
      return {
        schema_version: 2,
        page,
        fetch_trace: fetchTrace.slice(-180),
        cdp_events: [...events],
        harness: {
          setup_requests: internals.requests.get("/setup/status") || 0,
          setup_responses: internals.responses.get("/setup/status") || 0,
          setup_response_statuses: [...(internals.responseStatuses.get("/setup/status") || [])],
          auth_me_requests: internals.requests.get("/auth/me") || 0,
          auth_me_responses: internals.responses.get("/auth/me") || 0,
          refresh_requests: internals.requests.get("/auth/session/refresh") || 0,
          refresh_responses: internals.responses.get("/auth/session/refresh") || 0,
          navigation_count: internals.navigationCount,
          console_error_count: internals.consoleErrorCount
        }
      };
    },
    close() { internals.socket.removeEventListener("message", listener); }
  };
}

test("CI017 auth/me expiry setup bootstrap diagnostic", { timeout: 420_000 }, async (t) => {
  if (process.env.CI !== "true" || process.platform !== "linux") throw new Error("CI017 diagnostic requires the blocking Linux CI runtime");
  if (process.env.AUTOSTREAM_CI017_DIAGNOSTIC !== "1") throw new Error("CI017 diagnostic mode must be explicitly enabled");
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  const browserPromise = BrowserHarness.launch();
  t.after(async () => { const browser = await browserPromise.catch(() => undefined); await browser?.close(); await server.close(); });
  const browser = await browserPromise;
  const recorder = createCI017Recorder(browser);
  let phase = "query-states";
  let scenarioError: unknown;
  let observation: Record<string, unknown> = { observation_error: true };
  try {
    await recorder.installFetchProbe();
    const fixture = createBrowserRouteFixture(browser);
    await runQueryStatesScenario(t, browser, server, fixture);
    phase = "logout";
    await runLogoutScenario(t, browser, server, fixture);
    phase = "session-expiry";
    await runSessionExpiryScenario(t, browser, server, fixture);
    phase = "auth-me-401-expiry";
    await runAuthMeExpiryScenario(t, browser, server, fixture);
  } catch (error) {
    scenarioError = error;
  } finally {
    try { observation = await recorder.snapshot(); }
    catch (error) { observation = { observation_error: true, error_name: error instanceof Error ? error.name : "unknown" }; }
    const diagnostic = {
      schema_version: 2,
      code: "CI017_AUTH_EXPIRY_SETUP",
      source_commit: process.env.GITHUB_SHA || "unknown",
      scenario_phase: phase,
      scenario_status: scenarioError === undefined ? "PASS" : "FAIL",
      failure_name: scenarioError instanceof Error ? scenarioError.name : scenarioError === undefined ? null : "unknown",
      failure_message: scenarioError instanceof Error ? scenarioError.message.slice(0, 240) : null,
      observation
    };
    const output = process.env.AUTOSTREAM_CI017_OUTPUT;
    if (output) {
      mkdirSync(output, { recursive: true });
      writeFileSync(resolve(output, "ci017-auth-expiry-diagnostic.json"), JSON.stringify(diagnostic, null, 2) + "\n", { flag: "wx" });
    }
    console.error("CI017_AUTH_EXPIRY_DIAGNOSTIC " + JSON.stringify(diagnostic));
    recorder.close();
  }
  if (scenarioError !== undefined) throw scenarioError;
});
