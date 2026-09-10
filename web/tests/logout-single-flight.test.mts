import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { MutationObserver, QueryClient, type MutationObserverOptions } from "@tanstack/react-query";
import ts from "typescript";

const require = createRequire(import.meta.url);
const shellSource = readFileSync(new URL("../src/components/shell/app-shell.tsx", import.meta.url), "utf8");
const clientSource = readFileSync(new URL("../src/lib/api/client.ts", import.meta.url), "utf8");

type LogoutOptions = MutationObserverOptions<{ status: string }, Error, void>;
type LogoutProps = { onLogout: () => void; logoutPending: boolean };
type Element = { type: unknown; props: { children?: Element | Element[] } & Partial<LogoutProps> };
type Outcome = "success" | "http failure" | "transport rejection";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

// Execute the complete production modules; replace only their external boundaries.
function loadModule(source: string, imports: Record<string, unknown>, globals: Record<string, unknown> = {}) {
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const exports: Record<string, unknown> = {};
  runInNewContext(output, {
    ...globals,
    exports,
    require: (name: string) => {
      if (name === "react/jsx-runtime") return require(name);
      assert.ok(Object.hasOwn(imports, name), `unexpected production import: ${name}`);
      return imports[name];
    },
  });
  return exports;
}

function createShell() {
  const response = deferred<Response>();
  const requestStarted = deferred<void>();
  const requests: Array<{ path: string; method?: string; body?: unknown }> = [];
  const navigation: string[] = [];
  const cleanup: string[] = [];
  const storage = new Map([["autostream.csrf_token", "logout-test-fixture"]]);
  const inFlight: Promise<unknown>[] = [];
  // A retrying client default ensures the production mutation overrides it.
  const client = new QueryClient({ defaultOptions: { mutations: { retry: 3, retryDelay: 0, gcTime: Infinity } } });
  const cells: unknown[] = [];
  let cursor = 0;
  let mutationCalls = 0;
  let observer!: MutationObserver<{ status: string }, Error, void>;
  let onNavigate = () => {};

  function cell<T>(initialize: () => T): T {
    const slot = cursor++;
    if (!(slot in cells)) cells[slot] = initialize();
    return cells[slot] as T;
  }

  const api = loadModule(clientSource, { "@/features/mock-data": {} }, {
    process: { env: { NEXT_PUBLIC_AUTOSTREAM_DEMO: "false" } },
    window: {
      sessionStorage: {
        getItem: (key: string) => storage.get(key) ?? null,
        removeItem: (key: string) => { cleanup.push(key); storage.delete(key); },
      },
    },
    fetch: (path: string, init: RequestInit) => {
      requests.push({ path, method: init.method, body: init.body });
      requestStarted.resolve();
      return response.promise;
    },
  });
  const ready = { data: {}, isLoading: false, isError: false, isFetching: false, isPending: false };
  const TopBar = Symbol("TopBar");
  const shell = loadModule(shellSource, {
    // Hook cells preserve state/ref identity while renders are advanced explicitly.
    react: {
      useRef: <T,>(value: T) => cell(() => ({ current: value })),
      useState: <T,>(value: T | (() => T)) => {
        const state = cell(() => ({ value: typeof value === "function" ? (value as () => T)() : value }));
        return [state.value, (next: T) => { state.value = next; }];
      },
    },
    "next/link": { default: Symbol("Link") },
    "next/navigation": {
      usePathname: () => "/admin/streams/",
      useRouter: () => ({ replace: (path: string) => { navigation.push(path); onNavigate(); } }),
    },
    "@tanstack/react-query": {
      useMutation: (options: LogoutOptions) => {
        observer = cell(() => new MutationObserver(client, options));
        observer.setOptions(options);
        return {
          ...observer.getCurrentResult(),
          mutate: () => {
            mutationCalls++;
            inFlight.push(observer.mutate().catch(() => undefined));
          },
        };
      },
    },
    "@/components/admin/i18n-provider": { useI18n: () => ({ locale: "en", t: (key: string) => key }) },
    "@/components/shell/app-sidebar": { AppSidebar: Symbol("AppSidebar") },
    "@/components/shell/mobile-navigation": { MobileNavigation: Symbol("MobileNavigation") },
    "@/components/shell/top-bar": { TopBar },
    "@/components/shell/use-navigation-sections": { useNavigationSections: () => ({}) },
    "@/components/shell/use-shell-session-guard": { useShellSessionGuard: () => ({ sessionExpired: false }) },
    "@/components/status/update-indicator": { formatVersion: () => "" },
    "@/components/ui/button": { Button: Symbol("Button") },
    "@/components/ui/skeleton": { Skeleton: Symbol("Skeleton") },
    "@/features/queries": {
      useCurrentUser: () => ready,
      useAppSettings: () => ready,
      useVersion: () => ready,
      useServiceHealth: () => ({ ...ready, data: [] }),
    },
    "@/lib/api/client": api,
    "@/lib/auth/permissions": { hasPermission: () => true },
    "@/lib/navigation": {
      activeNavigationItem: () => undefined,
      activeNavigationSectionKey: () => "operations",
      isSuperAdmin: () => true,
    },
  });
  const AppShell = shell.AppShell as (props: { children: null }) => Element;

  function render(): LogoutProps {
    cursor = 0;
    const pending = [AppShell({ children: null })];
    while (pending.length) {
      const element = pending.shift()!;
      if (element.type === TopBar) return element.props as LogoutProps;
      const children = element.props.children;
      if (children) pending.push(...(Array.isArray(children) ? children : [children]));
    }
    throw new Error("production AppShell did not connect logout to TopBar");
  }

  return {
    render, requests, navigation, cleanup, storage, requestStarted: requestStarted.promise,
    // Reuse the loaded module so a module-global logout latch cannot pass this test.
    remount() { cells.length = 0; return render(); },
    get mutationCalls() { return mutationCalls; },
    get result() { return observer.getCurrentResult(); },
    get retry() { return observer.options.retry; },
    onNavigate(callback: () => void) { onNavigate = callback; },
    async settle(outcome: Outcome) {
      if (outcome === "transport rejection") response.reject(new Error("transport unavailable"));
      else response.resolve(Response.json(outcome === "success" ? { status: "ok" } : { code: "logout_failed" }, {
        status: outcome === "success" ? 200 : 503,
      }));
      await Promise.all(inFlight);
    },
    async close() {
      response.resolve(Response.json({ status: "ok" }));
      await Promise.all(inFlight);
      client.clear();
    },
  };
}

for (const outcome of ["success", "http failure", "transport rejection"] as const) {
  test(`AppShell logout stays single-flight through ${outcome} and delayed navigation`, { timeout: 5_000 }, async (t) => {
    const shell = createShell();
    t.after(() => shell.close());
    const initial = shell.render();
    assert.equal(initial.logoutPending, false);

    for (let call = 0; call < 5; call++) initial.onLogout();
    assert.equal(shell.mutationCalls, 1, "same-tick calls must be rejected before entering mutate");
    await shell.requestStarted;
    assert.deepEqual(shell.requests, [{ path: "/auth/logout", method: "POST", body: undefined }]);
    assert.equal(shell.retry, false);

    const pending = shell.render();
    assert.equal(pending.logoutPending, true);
    initial.onLogout();
    pending.onLogout();
    shell.render().onLogout();
    assert.equal(shell.mutationCalls, 1, "deferred response and rerenders must share the guard");
    assert.equal(shell.requests.length, 1);
    assert.deepEqual(shell.cleanup, []);
    assert.deepEqual(shell.navigation, []);

    shell.onNavigate(() => {
      assert.equal(shell.storage.has("autostream.csrf_token"), false, "cleanup precedes redirect");
      initial.onLogout();
      shell.render().onLogout();
    });
    await shell.settle(outcome);
    assert.equal(shell.result.status, outcome === "success" ? "success" : "error");
    assert.deepEqual(shell.cleanup, ["autostream.csrf_token"]);
    assert.deepEqual(shell.navigation, ["/login"]);

    // router.replace has been requested, but the authenticated Shell is still mounted.
    const settled = shell.render();
    assert.equal(shell.result.isPending, false);
    assert.equal(settled.logoutPending, true, "pending UI must outlive network settlement");
    initial.onLogout();
    pending.onLogout();
    settled.onLogout();
    assert.equal(shell.mutationCalls, 1, "settled requests must not reopen the mutation entrance");
    assert.equal(shell.requests.length, 1, "no second POST or automatic retry");
  });
}

test("a new authenticated AppShell can logout once while the old handler remains locked", { timeout: 5_000 }, async (t) => {
  const shell = createShell();
  t.after(() => shell.close());
  const oldHandler = shell.render().onLogout;
  oldHandler();
  await shell.requestStarted;
  await shell.settle("success");
  assert.equal(shell.requests.length, 1);

  shell.storage.set("autostream.csrf_token", "next-session-fixture");
  const newUI = shell.remount();
  assert.equal(newUI.logoutPending, false);
  newUI.onLogout();
  newUI.onLogout();
  oldHandler();
  await shell.settle("success");
  assert.equal(shell.mutationCalls, 2, "one mutation per Shell instance");
  assert.equal(shell.requests.length, 2, "one POST per authenticated session");
  assert.deepEqual(shell.navigation, ["/login", "/login"]);
  assert.deepEqual(shell.cleanup, ["autostream.csrf_token", "autostream.csrf_token"]);
  assert.equal(shell.storage.has("autostream.csrf_token"), false);
});
