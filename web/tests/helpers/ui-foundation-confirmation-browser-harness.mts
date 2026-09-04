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

export type ConfirmationBrowserFixture = Readonly<{
  baseUrl: string;
  browser: BrowserHarness;
}>;

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const fixturePrefix = "autostream-confirmation-browser-";
const fixtureBaseUrl = "http://127.0.0.1:3002";

export async function withConfirmationBrowserFixture(
  run: (fixture: ConfirmationBrowserFixture) => Promise<void>,
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
        ? new AggregateError([cleanupError, asError(error)], "Confirmation browser cleanup failed")
        : asError(error);
    }
    try {
      await removeFixtureRoot(fixtureRoot);
    } catch (error) {
      cleanupError = cleanupError
        ? new AggregateError([cleanupError, asError(error)], "Confirmation browser cleanup failed")
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
    button, input { font: inherit; }
    :focus-visible { outline: 3px solid #2563eb; outline-offset: 3px; }
    @media (prefers-reduced-motion: reduce) {
      *, *::before, *::after { animation-duration: 0.01ms !important; transition-duration: 0.01ms !important; }
    }
    @media (forced-colors: active) {
      :focus-visible { outline: 2px solid CanvasText; }
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
    import { ConfirmationFixture } from "./confirmation-fixture";
    export default async function Page({ searchParams }: {
      searchParams: Promise<Record<string, string | string[] | undefined>>;
    }) {
      const params = await searchParams;
      const scenario = typeof params.scenario === "string" ? params.scenario : "consequence";
      const locale = params.locale === "ja" ? "ja" : "en";
      return <ConfirmationFixture scenario={scenario} locale={locale} />;
    }
  `);
  writeFileSync(join(appRoot, "confirmation-fixture.tsx"), fixtureComponentSource());
}

function fixtureComponentSource() {
  return `
    "use client";

    import { useState } from "react";
    import {
      HighRiskConfirmation,
      type ConfirmationDialogState,
    } from "@/components/foundation/confirmation/high-risk-confirmation";
    import type { ActionDescriptor } from "@/lib/foundation/actions/contracts";
    import { translate, type TranslationKey, type TranslationValues } from "@/lib/i18n";
    import type { Locale } from "@/types/domain";

    const base = {
      id: "WKR-01",
      labelKey: "restart",
      target: {
        resourceType: "worker",
        resourceId: "private-worker-id",
        publicLabel: "Worker Alpha",
        publicStableId: "worker-alpha",
      },
      permissions: { kind: "all", permissions: ["workers.restart"] },
      applicability: { ruleIds: ["worker-restartable"], requiredSections: ["health"] },
      duplicate: { scope: "resource-action", whilePending: "block" },
      retry: { kind: "never" },
      audit: { action: "workers.restart", labelKey: "auditLogs", safeReferenceFieldIds: [] },
      stateIndependent: false,
      revalidation: { kind: "revision" },
    } as const;

    const consequenceDescriptor: ActionDescriptor = {
      ...base,
      risk: "high",
      confirmation: {
        mode: "consequence",
        consequenceKey: "dangerousNotice",
        requireSubmitRevalidation: true,
      },
    };
    const typedDescriptor: ActionDescriptor = {
      ...base,
      risk: "critical",
      confirmation: {
        mode: "typed-target",
        consequenceKey: "dangerousNotice",
        typedToken: { kind: "target-label" },
        requireSubmitRevalidation: true,
      },
    };
    const invalidDescriptor: ActionDescriptor = {
      ...base,
      risk: "critical",
      confirmation: {
        mode: "consequence",
        consequenceKey: "dangerousNotice",
        requireSubmitRevalidation: true,
      },
    };
    const allowed = {
      visibility: { kind: "visible" },
      availability: { kind: "allowed" },
    } as const;
    const conflict = {
      kind: "conflict",
      messageKey: "apiErrorConflict",
      diagnosticCode: "must-not-render",
    } as const;

    export function ConfirmationFixture({ scenario, locale }: { scenario: string; locale: Locale }) {
      const [open, setOpen] = useState(false);
      const [intentCount, setIntentCount] = useState(0);
      const [runtimeState, setRuntimeState] = useState<ConfirmationDialogState>(() => stateFor(scenario));
      const descriptor = scenario === "typed" ? typedDescriptor
        : scenario === "invalid" ? invalidDescriptor
        : consequenceDescriptor;
      const t = (key: TranslationKey, values?: TranslationValues) => translate(locale, key, values);

      return (
        <main>
          <output data-testid="intent-count">{intentCount}</output>
          <HighRiskConfirmation
            descriptor={descriptor}
            open={open}
            evaluation={allowed}
            state={runtimeState}
            context={{
              currentState: { key: "status" },
              impact: { key: "dangerousNotice" },
              rollback: { key: "confirmationRefreshRequired" },
            }}
            translate={t}
            trigger={(props) => (
              <button id={scenario + "-trigger"} type="button" {...props}>Open confirmation</button>
            )}
            onOpenIntent={() => setOpen(true)}
            onCloseIntent={() => setOpen(false)}
            onConfirmIntent={() => {
              setIntentCount((count) => count + 1);
              if (scenario === "controller-close") {
                setOpen(false);
                return;
              }
              setRuntimeState({ kind: "submitting" });
            }}
          />
        </main>
      );
    }

    function stateFor(scenario: string): ConfirmationDialogState {
      if (scenario === "revalidating") return { kind: "revalidating" };
      if (scenario === "submitting") return { kind: "submitting" };
      if (scenario === "stale") return { kind: "stale-blocked" };
      if (scenario === "unavailable") return { kind: "revalidation-unavailable" };
      if (scenario === "conflict") return { kind: "conflict", error: conflict };
      if (scenario === "failed") return { kind: "failed", error: { kind: "network", messageKey: "apiErrorNetwork" } };
      if (scenario === "unknown") return { kind: "outcome-unknown", nextAction: "inspect-audit" };
      return { kind: "ready" };
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
    if (server.exitCode !== null) throw new Error(`Confirmation fixture server exited early:\n${output}`);
    if (/Module not found|Can(?:not|'t) resolve/.test(output)) {
      throw new Error(`Confirmation fixture compile failed:\n${output}`);
    }
    if (await serverResponds(url)) return;
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 100));
  }
  throw new Error(`Confirmation fixture server did not become ready:\n${output}`);
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
  throw lastError ?? new Error("Confirmation fixture cleanup failed");
}

function assertOwnedFixtureRoot(path: string) {
  const resolved = resolve(path);
  if (dirname(resolved) !== resolve(tmpdir()) || !basename(resolved).startsWith(fixturePrefix)) {
    throw new Error(`Refusing unexpected confirmation fixture path: ${resolved}`);
  }
}

function asError(value: unknown) {
  return value instanceof Error ? value : new Error(String(value));
}
