import { spawn, spawnSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

import { BrowserHarness } from "./browser-harness.mts";

export type SecretBrowserFixture = Readonly<{
  baseUrl: string;
  browser: BrowserHarness;
}>;

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const fixturePrefix = "autostream-secret-browser-";
const fixtureBaseUrl = "http://127.0.0.1:3002";

export async function withSecretBrowserFixture(
  run: (fixture: SecretBrowserFixture) => Promise<void>,
) {
  if (await serverResponds(fixtureBaseUrl)) {
    throw new Error("Port 3002 is already in use; refusing to adopt or terminate a pre-existing listener");
  }
  const fixtureRoot = mkdtempSync(join(tmpdir(), fixturePrefix));
  assertOwnedFixtureRoot(fixtureRoot);
  createFixtureApp(fixtureRoot);

  const server = startFixtureServer(fixtureRoot);
  let browser: BrowserHarness | undefined;
  try {
    await waitForServer(server, fixtureBaseUrl);
    browser = await BrowserHarness.launch();
    await run({ baseUrl: fixtureBaseUrl, browser });
    browser.assertNoFatalError();
  } finally {
    let cleanupError: Error | undefined;
    if (browser) {
      try {
        await browser.close();
      } catch (error) {
        cleanupError = asError(error);
      }
    }
    try {
      await terminateFixtureServer(server);
    } catch (error) {
      cleanupError = cleanupError
        ? new AggregateError([cleanupError, asError(error)], "Secret browser cleanup failed")
        : asError(error);
    }
    try {
      await removeFixtureRoot(fixtureRoot);
    } catch (error) {
      cleanupError = cleanupError
        ? new AggregateError([cleanupError, asError(error)], "Secret browser cleanup failed")
        : asError(error);
    }
    if (cleanupError) throw cleanupError;
  }
}

function createFixtureApp(fixtureRoot: string) {
  const appRoot = join(fixtureRoot, "app");
  mkdirSync(appRoot, { recursive: true });
  const sourceRoot = resolve(webRoot, "src").replaceAll("\\", "/");
  writeFileSync(join(fixtureRoot, "package.json"), JSON.stringify({
    private: true,
    dependencies: { next: "*", react: "*", "react-dom": "*" },
  }, null, 2));
  writeFileSync(join(fixtureRoot, "tsconfig.json"), JSON.stringify({
    compilerOptions: {
      baseUrl: ".",
      target: "ES2022",
      lib: ["dom", "dom.iterable", "esnext"],
      strict: true,
      noEmit: true,
      module: "esnext",
      moduleResolution: "bundler",
      jsx: "react-jsx",
      paths: { "@/*": [`${sourceRoot}/*`] },
      plugins: [{ name: "next" }],
    },
    include: ["**/*.ts", "**/*.tsx", ".next/types/**/*.ts"],
  }, null, 2));
  writeFileSync(join(fixtureRoot, "next.config.mjs"), `
    export default {
      agentRules: false,
      reactStrictMode: false,
      experimental: { externalDir: true },
      webpack(config) {
        config.resolve.alias = { ...config.resolve.alias, "@": ${JSON.stringify(sourceRoot)} };
        return config;
      },
    };
  `);
  symlinkSync(
    resolve(webRoot, "node_modules"),
    join(fixtureRoot, "node_modules"),
    process.platform === "win32" ? "junction" : "dir",
  );

  writeFileSync(join(appRoot, "globals.css"), `
    * { box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; margin: 2rem; }
    button { font: inherit; margin: 0.25rem; }
    [data-one-time-secret-content]:focus-visible,
    button:focus-visible { outline: 3px solid #2563eb; outline-offset: 3px; }
    @media (prefers-reduced-motion: reduce) {
      *, *::before, *::after { animation-duration: 0.01ms !important; transition-duration: 0.01ms !important; }
    }
    @media (forced-colors: active) {
      [data-one-time-secret-content]:focus-visible,
      button:focus-visible { outline: 2px solid CanvasText; }
    }
  `);
  writeFileSync(join(appRoot, "layout.tsx"), `
    import type { ReactNode } from "react";
    import "./globals.css";
    export default function Layout({ children }: { children: ReactNode }) {
      return <html lang="en"><body>{children}</body></html>;
    }
  `);
  writeFileSync(join(appRoot, "page.tsx"), `
    import { SecretFixture } from "./secret-fixture";
    export default async function Page({ searchParams }: {
      searchParams: Promise<Record<string, string | string[] | undefined>>;
    }) {
      const params = await searchParams;
      const scenario = typeof params.scenario === "string" ? params.scenario : "default";
      const locale = params.locale === "ja" ? "ja" : "en";
      return <SecretFixture scenario={scenario} locale={locale} />;
    }
  `);
  writeFileSync(join(appRoot, "secret-fixture.tsx"), fixtureComponentSource());
}

function fixtureComponentSource() {
  return String.raw`
    "use client";

    import { StrictMode, useEffect, useState } from "react";
    import { OneTimeSecretReveal } from "@/components/foundation/secrets/one-time-secret-reveal";
    import { createOneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/lifecycle-owner";
    import type { OneTimeSecretRuntime } from "@/lib/foundation/secrets/contracts";
    import { translate, type TranslationKey, type TranslationValues } from "@/lib/i18n";
    import type { Locale } from "@/types/domain";

    const primaryMarker = "B06-BROWSER-SECRET-64c5de";
    const replacementMarker = "B06-BROWSER-REPLACEMENT-f5ae13";
    const observedMarkers = [primaryMarker, replacementMarker] as const;

    type Timer = Readonly<{ callback: () => void; due: number; order: number }>;

    class ManualRuntime implements OneTimeSecretRuntime {
      epoch = 1_000_000;
      monotonic = 0;
      nextHandle = 1;
      nextOrder = 1;
      timers = new Map<number, Timer>();

      epochNowMs = () => this.epoch;
      monotonicNowMs = () => this.monotonic;
      schedule = (callback: () => void, delayMs: number) => {
        const handle = this.nextHandle++;
        this.timers.set(handle, { callback, due: this.monotonic + delayMs, order: this.nextOrder++ });
        return handle;
      };
      cancel = (handle: unknown) => {
        if (typeof handle === "number") this.timers.delete(handle);
      };
      advanceBy(milliseconds: number) {
        const target = this.monotonic + milliseconds;
        while (true) {
          const next = [...this.timers.entries()]
            .filter(([, timer]) => timer.due <= target)
            .sort(([, left], [, right]) => left.due - right.due || left.order - right.order)[0];
          if (!next) break;
          const [handle, timer] = next;
          this.monotonic = timer.due;
          this.timers.delete(handle);
          timer.callback();
        }
        this.monotonic = target;
      }
    }

    type LeakState = { hits: string[] };

    export function SecretFixture({ scenario, locale }: { scenario: string; locale: Locale }) {
      const [browserReady, setBrowserReady] = useState(false);
      useEffect(() => { setBrowserReady(true); }, []);
      if (!browserReady) return <main data-secret-fixture-pending="">Preparing isolated fixture.</main>;
      return <BrowserSecretFixture scenario={scenario} locale={locale} />;
    }

    function BrowserSecretFixture({ scenario, locale }: { scenario: string; locale: Locale }) {
      const [{ runtime, owner, leaks }] = useState(() => {
        const leaks = installLeakSpies();
        const runtime = new ManualRuntime();
        const owner = createOneTimeSecretLifecycleOwner<string>(runtime);
        if (!owner.replace({ value: primaryMarker })) throw new Error("fixture adoption failed");
        return { runtime, owner, leaks };
      });
      const [snapshot, setSnapshot] = useState(owner.getSnapshot());
      const [mounted, setMounted] = useState(true);
      const [copyWriterCount, setCopyWriterCount] = useState(0);
      const [acknowledgeCount, setAcknowledgeCount] = useState(0);
      const [dismissCount, setDismissCount] = useState(0);
      const [unmountCount, setUnmountCount] = useState(0);

      useEffect(() => owner.subscribe(() => setSnapshot(owner.getSnapshot())), [owner]);

      const t = (key: TranslationKey, values?: TranslationValues) => translate(locale, key, values);
      const reveal = mounted ? (
        <StrictMode>
          <OneTimeSecretReveal
            snapshot={snapshot}
            translate={t}
            renderRevealedContent={() => (
              <code data-secret-marker="">{owner.readRevealedValue()}</code>
            )}
            canCopy
            onRevealIntent={() => { owner.reveal(); }}
            onConcealIntent={() => { owner.conceal(); }}
            onCopyIntent={() => {
              void owner.copyWith((value) => {
                setCopyWriterCount((count) => count + 1);
                if (scenario === "copy-failure") throw new Error(value);
              });
            }}
            onAcknowledgeIntent={() => {
              setAcknowledgeCount((count) => count + 1);
              owner.acknowledge();
            }}
            onDismissIntent={() => {
              setDismissCount((count) => count + 1);
              owner.dismiss();
            }}
            onUnmountIntent={() => {
              setUnmountCount((count) => count + 1);
              owner.dispose();
            }}
          />
        </StrictMode>
      ) : null;

      return (
        <main>
          <button id="fixture-focus-anchor" type="button">Focus anchor</button>
          <div data-fixture-controls="">
            <button id="advance-warning" type="button" onClick={() => runtime.advanceBy(540_000)}>Advance warning</button>
            <button id="advance-expiry" type="button" onClick={() => runtime.advanceBy(60_000)}>Advance expiry</button>
            <button id="navigation-clear" type="button" onClick={() => owner.clearForNavigation()}>Navigation clear</button>
            <button id="session-clear" type="button" onClick={() => owner.clearForSessionLoss()}>Session clear</button>
            <button id="replace-source" type="button" onClick={() => owner.replace({ value: replacementMarker })}>Replace</button>
            <button id="unmount-reveal" type="button" onClick={() => setMounted(false)}>Unmount</button>
          </div>
          <output data-testid="phase">{snapshot.phase}</output>
          <output data-testid="reason">{snapshot.clearReason || ""}</output>
          <output data-testid="generation">{snapshot.generation}</output>
          <output data-testid="pending-count">{runtime.timers.size}</output>
          <output data-testid="copy-writer-count">{copyWriterCount}</output>
          <output data-testid="acknowledge-count">{acknowledgeCount}</output>
          <output data-testid="dismiss-count">{dismissCount}</output>
          <output data-testid="unmount-count">{unmountCount}</output>
          <output data-testid="leak-hit-count">{leaks.hits.length}</output>
          {reveal}
        </main>
      );
    }

    function installLeakSpies(): LeakState {
      const state: LeakState = { hits: [] };
      const inspect = (surface: string, values: readonly unknown[]) => {
        if (values.some((value) => observedMarkers.some((marker) => deepContains(value, marker)))) {
          state.hits.push(surface);
        }
      };

      const storageSet = Storage.prototype.setItem;
      Storage.prototype.setItem = function(key: string, value: string) {
        inspect("storage", [key, value]);
        return storageSet.call(this, key, value);
      };

      const pushState = history.pushState.bind(history);
      history.pushState = (data: unknown, unused: string, url?: string | URL | null) => {
        inspect("history.pushState", [data, unused, url]);
        return pushState(data, unused, url);
      };
      const replaceState = history.replaceState.bind(history);
      history.replaceState = (data: unknown, unused: string, url?: string | URL | null) => {
        inspect("history.replaceState", [data, unused, url]);
        return replaceState(data, unused, url);
      };

      for (const level of ["log", "info", "warn", "error", "debug"] as const) {
        const original = console[level].bind(console);
        console[level] = (...values: unknown[]) => {
          inspect("console." + level, values);
          original(...values);
        };
      }

      const originalFetch = globalThis.fetch.bind(globalThis);
      globalThis.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
        inspect("fetch", [input, init]);
        return originalFetch(input, init);
      };
      const xhrSend = XMLHttpRequest.prototype.send;
      XMLHttpRequest.prototype.send = function(body?: Document | XMLHttpRequestBodyInit | null) {
        inspect("xhr", [body]);
        return xhrSend.call(this, body);
      };

      return state;
    }

    function deepContains(value: unknown, marker: string, seen = new Set<object>()): boolean {
      if (typeof value === "string") return value.includes(marker);
      if (value instanceof URL) return value.href.includes(marker);
      if (value instanceof Request) return value.url.includes(marker);
      if (typeof value !== "object" || value === null || seen.has(value)) return false;
      seen.add(value);
      if (value instanceof Error) return value.message.includes(marker) || Boolean(value.stack?.includes(marker));
      try {
        return Object.entries(value).some(([key, entry]) => key.includes(marker) || deepContains(entry, marker, seen));
      } catch {
        return false;
      }
    }
  `;
}

function startFixtureServer(fixtureRoot: string) {
  const nextBin = resolve(webRoot, "node_modules", "next", "dist", "bin", "next");
  return spawn(process.execPath, [nextBin, "dev", "--webpack", "--hostname", "127.0.0.1", "--port", "3002"], {
    cwd: fixtureRoot,
    env: {
      ...process.env,
      NEXT_TELEMETRY_DISABLED: "1",
      NEXT_PUBLIC_AUTOSTREAM_DEMO: "false",
    },
    stdio: "pipe",
    windowsHide: true,
  });
}

async function waitForServer(server: ChildProcessWithoutNullStreams, url: string) {
  let output = "";
  server.stdout.on("data", (chunk) => { output = `${output}${chunk}`.slice(-12_000); });
  server.stderr.on("data", (chunk) => { output = `${output}${chunk}`.slice(-12_000); });
  const deadline = Date.now() + 45_000;
  while (Date.now() < deadline) {
    if (server.exitCode !== null) throw new Error(`Secret fixture server exited early:\n${output}`);
    if (/Module not found|Can(?:not|'t) resolve/.test(output)) {
      throw new Error(`Secret fixture compile failed:\n${output}`);
    }
    if (await serverResponds(url)) return;
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 100));
  }
  throw new Error(`Secret fixture server did not become ready:\n${output}`);
}

async function serverResponds(url: string) {
  try {
    const response = await fetch(url, { signal: AbortSignal.timeout(1_000) });
    return response.status < 500;
  } catch {
    return false;
  }
}

async function terminateFixtureServer(server: ChildProcessWithoutNullStreams) {
  try {
    if (server.exitCode === null && server.signalCode === null && process.platform === "win32" && server.pid) {
      spawnSync("taskkill.exe", ["/PID", String(server.pid), "/T", "/F"], {
        stdio: "ignore",
        windowsHide: true,
      });
      await Promise.race([waitForChildExit(server), delay(3_000)]);
    }
    if (server.exitCode === null && server.signalCode === null) {
      server.kill("SIGTERM");
      await Promise.race([waitForChildExit(server), delay(3_000)]);
    }
    if (server.exitCode === null && server.signalCode === null) {
      server.kill("SIGKILL");
      await Promise.race([waitForChildExit(server), delay(2_000)]);
    }
  } finally {
    server.stdin.destroy();
    server.stdout.destroy();
    server.stderr.destroy();
    server.unref();
  }
}

function waitForChildExit(child: ChildProcessWithoutNullStreams) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();
  return new Promise<void>((resolveExit) => child.once("exit", () => resolveExit()));
}

function delay(milliseconds: number) {
  return new Promise<void>((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}

async function removeFixtureRoot(path: string) {
  assertOwnedFixtureRoot(path);
  let lastError: Error | undefined;
  for (let attempt = 0; attempt < 8; attempt += 1) {
    try {
      rmSync(join(path, "node_modules"), { force: true });
      rmSync(path, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
      return;
    } catch (error) {
      lastError = asError(error);
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 150 * (attempt + 1)));
    }
  }
  throw lastError ?? new Error("Secret fixture cleanup failed");
}

function assertOwnedFixtureRoot(path: string) {
  const resolved = resolve(path);
  if (dirname(resolved) !== resolve(tmpdir()) || !basename(resolved).startsWith(fixturePrefix)) {
    throw new Error(`Refusing unexpected secret fixture path: ${resolved}`);
  }
}

function asError(value: unknown) {
  return value instanceof Error ? value : new Error(String(value));
}
