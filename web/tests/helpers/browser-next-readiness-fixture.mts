import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, resolve } from "node:path";
import ts from "typescript";
import { FetchRequestLifecycle, RejectableEventWaiters } from "./browser-request-lifecycle.mts";
import { browserHarnessPath } from "./browser-lifecycle-source-paths.mts";


type NextReadyServer = { baseUrl: string; close: () => Promise<void> };
type NextProbePlan = { status?: number; error?: Error; afterMs?: number; ignoreAbort?: boolean };
type NextProbe = {
  startedAt: number;
  signal: AbortSignal;
  abortCount: number;
  pending: boolean;
  respond: (status: number) => void;
};
type NextReadinessOptions = {
  probe?: (index: number) => NextProbePlan;
  preflightStatus?: number;
  platform?: "linux" | "win32";
  missingNext?: boolean;
  killError?: Error;
  restoreError?: Error;
  diagnosticError?: Error;
  blockTermination?: boolean;
};
type NextTimerOwner = "harness" | "response" | "preflight";
type NextTimerHandle = { id: number; unref: () => NextTimerHandle };

class NextReadinessClock {
  now = 0;
  private nextTimerId = 0;
  private readonly timers = new Map<number, { due: number; owner: NextTimerOwner; callback: () => void }>();

  setTimeout(callback: () => void, milliseconds: number, owner: NextTimerOwner = "harness"): NextTimerHandle {
    assert.ok(Number.isFinite(milliseconds) && milliseconds >= 0, "readiness timer must be finite and nonnegative");
    const id = ++this.nextTimerId;
    this.timers.set(id, { due: this.now + milliseconds, owner, callback });
    return { id, unref() { return this; } };
  }

  clearTimeout(handle: NextTimerHandle | undefined) {
    if (handle) this.timers.delete(handle.id);
  }

  get harnessTimerCount() {
    return [...this.timers.values()].filter((timer) => timer.owner === "harness").length;
  }

  get pendingCount() {
    return this.timers.size;
  }

  async flush() {
    await new Promise<void>((resolveTurn) => setImmediate(resolveTurn));
  }

  elapseWithoutCallbacks(milliseconds: number) {
    this.now += milliseconds;
  }

  async advance(milliseconds: number) {
    await this.flush();
    const target = this.now + milliseconds;
    let callbacks = 0;
    while (true) {
      const next = [...this.timers.entries()].filter(([, timer]) => timer.due <= target)
        .sort(([leftId, left], [rightId, right]) => left.due - right.due || leftId - rightId)[0];
      if (!next) break;
      assert.ok(++callbacks <= 2_000, "controlled clock detected an unbounded timer loop");
      this.now = Math.max(this.now, next[1].due);
      this.timers.delete(next[0]);
      next[1].callback();
      await this.flush();
    }
    this.now = Math.max(this.now, target);
    await this.flush();
  }
}

class NextReadinessChild extends EventEmitter {
  readonly pid = 9001;
  exitCode: number | null = null;
  signalCode: NodeJS.Signals | null = null;
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
  readonly kills: NodeJS.Signals[] = [];
  private readonly options: NextReadinessOptions;

  constructor(options: NextReadinessOptions) {
    super();
    this.options = options;
  }

  kill(signal: NodeJS.Signals) {
    this.kills.push(signal);
    if (this.options.killError) throw this.options.killError;
    if (!this.options.blockTermination) queueMicrotask(() => this.finish(null, signal));
    return true;
  }

  finish(code: number | null, signal: NodeJS.Signals | null) {
    this.exitCode = code;
    this.signalCode = signal;
    this.emit("exit", code, signal);
  }
}

let nextReadinessHarnessJavascript: Map<string, string> | undefined;

export function createNextReadinessFixture(options: NextReadinessOptions = {}) {
  // Execute the complete checked-in harness, including ensureWebServer's real calls.
  // Only OS, HTTP and clock boundaries are controlled; there is no alternate readiness algorithm.
  nextReadinessHarnessJavascript ??= new Map([
    "browser-harness.mts", "browser-next-server.mts", "browser-process-timing.mts",
  ].map((name) => ["./" + name, ts.transpileModule(readFileSync(resolve(dirname(browserHarnessPath), name), "utf8"), {
    fileName: name.replace(/\.mts$/, ".ts"),
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText]));
  const clock = new NextReadinessClock();
  const child = new NextReadinessChild(options);
  const webRoot = resolve("controlled-next-readiness-web");
  const baseUrl = "http://127.0.0.1:3002";
  const nextBin = resolve(webRoot, "node_modules", "next", "dist", "bin", "next");
  const files = new Map<string, Buffer>([
    [resolve(webRoot, "next-env.d.ts"), Buffer.from([0xef, 0xbb, 0xbf, 0x61, 0x0d, 0x0a])],
    [resolve(webRoot, "AGENTS.md"), Buffer.from("controlled pre-existing file\r\n")],
  ]);
  if (!options.missingNext) files.set(nextBin, Buffer.from("controlled existing Next binary"));
  const fingerprint = () => [...files].map(([path, bytes]) => [path, bytes.toString("hex")]).sort(([left], [right]) => left.localeCompare(right));
  const originalFiles = fingerprint();
  const spawns: Array<{ command: string; args: string[]; options: unknown }> = [];
  const taskkills: Array<{ command: string; args: string[]; options: unknown }> = [];
  const probes: NextProbe[] = [];
  const preflightTimeouts: number[] = [];
  const lines: string[] = [];
  let activeProbes = 0;
  let maximumConcurrentProbes = 0;
  const mockFetch = (url: string, request: { signal: AbortSignal }) => {
    assert.equal(url, baseUrl, "the actual helper changed the requested readiness URL");
    const spawned = spawns.length !== 0;
    const plan = spawned ? options.probe?.(probes.length) || {} : options.preflightStatus === undefined
      ? { error: new Error("controlled preflight refusal") } : { status: options.preflightStatus };
    let resolveResponse!: (value: { status: number }) => void;
    let rejectResponse!: (error: unknown) => void;
    let responseTimer: NextTimerHandle | undefined;
    const promise = new Promise<{ status: number }>((resolveFetch, rejectFetch) => {
      resolveResponse = resolveFetch;
      rejectResponse = rejectFetch;
    });
    const probe: NextProbe = {
      startedAt: clock.now, signal: request.signal, abortCount: 0, pending: true,
      respond: (status) => complete(undefined, status),
    };
    const complete = (error: unknown, status?: number) => {
      if (!probe.pending) return;
      probe.pending = false;
      clock.clearTimeout(responseTimer);
      request.signal.removeEventListener("abort", aborted);
      if (spawned) activeProbes -= 1;
      if (status === undefined) rejectResponse(error);
      else resolveResponse({ status });
    };
    const aborted = () => {
      probe.abortCount += 1;
      if (!plan.ignoreAbort) complete(request.signal.reason);
    };
    if (spawned) {
      probes.push(probe);
      activeProbes += 1;
      maximumConcurrentProbes = Math.max(maximumConcurrentProbes, activeProbes);
    }
    request.signal.addEventListener("abort", aborted);
    if (request.signal.aborted) aborted();
    else if (plan.error || plan.status !== undefined) {
      const respond = () => complete(plan.error, plan.status);
      if (plan.afterMs) responseTimer = clock.setTimeout(respond, plan.afterMs, "response");
      else queueMicrotask(respond);
    }
    return promise;
  };
  const dependencies: Record<string, unknown> = {
    "node:child_process": {
      spawn: (command: string, args: string[], spawnOptions: unknown) => {
        spawns.push({ command, args, options: spawnOptions });
        for (const name of ["next-env.d.ts", "AGENTS.md", "CLAUDE.md"]) files.set(resolve(webRoot, name), Buffer.from("controlled generated content\n"));
        return child;
      },
      spawnSync: (command: string, args: string[], spawnOptions: unknown) => {
        taskkills.push({ command, args, options: spawnOptions });
        if (!options.blockTermination) queueMicrotask(() => child.finish(null, "SIGKILL"));
        return { status: 0, signal: null };
      },
    },
    "node:fs": {
      existsSync: (path: string) => files.has(path),
      readFileSync: (path: string) => {
        const content = files.get(path);
        assert.ok(content, "unexpected controlled file read");
        return Buffer.from(content);
      },
      writeFileSync: (path: string, content: Buffer) => {
        if (options.restoreError) throw options.restoreError;
        files.set(path, Buffer.from(content));
      },
      rmSync: (path: string) => { files.delete(path); },
    },
    "node:os": { tmpdir },
    "node:path": { basename, dirname, resolve },
    "./browser-request-lifecycle.mts": { FetchRequestLifecycle, RejectableEventWaiters },
    "./browser-process-attempt.mts": {},
    "./browser-launch-profile.mts": {},
  };
  const evaluated = new Map<string, Record<string, unknown>>();
  const loadActualModule = (name: string): Record<string, unknown> => {
    const cached = evaluated.get(name);
    if (cached) return cached;
    const javascript = nextReadinessHarnessJavascript!.get(name);
    assert.ok(javascript, `unexpected harness source: ${name}`);
    const exports: Record<string, unknown> = {};
    evaluated.set(name, exports);
    new Function("require", "exports", "process", "Date", "fetch", "AbortSignal", "setTimeout", "clearTimeout", "console", javascript)(
      (dependency: string) => {
        if (nextReadinessHarnessJavascript!.has(dependency)) return loadActualModule(dependency);
        assert.ok(Object.hasOwn(dependencies, dependency), `unexpected harness dependency: ${dependency}`);
        return dependencies[dependency];
      },
      exports,
    { execPath: "controlled-node", platform: options.platform || "linux", env: { PATH: "CONTROLLED_PATH" } },
    { now: () => clock.now },
    mockFetch,
    { timeout: (milliseconds: number) => {
      preflightTimeouts.push(milliseconds);
      const controller = new AbortController();
      clock.setTimeout(() => controller.abort(new Error("controlled preflight timeout")), milliseconds, "preflight");
      return controller.signal;
    } },
    (callback: () => void, milliseconds: number) => clock.setTimeout(callback, milliseconds),
    (handle: NextTimerHandle | undefined) => clock.clearTimeout(handle),
    { ...console, error: (line: string) => {
      lines.push(line);
      if (options.diagnosticError) throw options.diagnosticError;
    } },
    );
    return exports;
  };
  const exported = loadActualModule("./browser-harness.mts") as { ensureWebServer?: (root: string, url: string) => Promise<NextReadyServer> };
  assert.equal(typeof exported.ensureWebServer, "function");
  return {
    clock, child, webRoot, baseUrl, spawns, taskkills, probes, preflightTimeouts, lines,
    fingerprint, originalFiles,
    get maximumConcurrentProbes() { return maximumConcurrentProbes; },
    start: () => exported.ensureWebServer!(webRoot, baseUrl),
  };
}

type NextReadinessFixture = ReturnType<typeof createNextReadinessFixture>;

export function observeNextStartup<T>(promise: Promise<T>) {
  const outcome: { state: "pending" | "fulfilled" | "rejected"; value?: T; error?: unknown; settlements: number } = { state: "pending", settlements: 0 };
  void promise.then((value) => {
    outcome.state = "fulfilled";
    outcome.value = value;
    outcome.settlements += 1;
  }, (error: unknown) => {
    outcome.state = "rejected";
    outcome.error = error;
    outcome.settlements += 1;
  });
  return outcome;
}

export function assertNextListenersRemoved(fixture: NextReadinessFixture) {
  for (const name of ["error", "exit"]) assert.equal(fixture.child.listenerCount(name), 0, `retained child ${name} listener`);
  assert.equal(fixture.child.stdout.listenerCount("data"), 0);
  assert.equal(fixture.child.stderr.listenerCount("data"), 0);
}

export function assertNextRestored(fixture: NextReadinessFixture) {
  assert.deepEqual(fixture.fingerprint(), fixture.originalFiles, "generated-file bytes were not restored");
  assert.equal(fixture.clock.harnessTimerCount, 0, "startup or cleanup retained a timer");
  assertNextListenersRemoved(fixture);
}

export function assertNextReady(fixture: NextReadinessFixture, outcome: ReturnType<typeof observeNextStartup<NextReadyServer>>) {
  assert.equal(outcome.state, "fulfilled", String(outcome.error || "readiness is still pending"));
  assert.ok(outcome.value);
  assert.equal(outcome.value.baseUrl, fixture.baseUrl);
  assert.equal(outcome.settlements, 1);
  assert.equal(fixture.spawns.length, 1);
  assert.equal(fixture.clock.harnessTimerCount, 0);
  assertNextListenersRemoved(fixture);
  assert.equal(fixture.lines.length, 0);
  return outcome.value;
}

export async function assertNextCloseRestores(fixture: NextReadinessFixture, server: NextReadyServer) {
  const first = server.close();
  assert.equal(server.close(), first, "close must share the same cleanup promise");
  const outcome = observeNextStartup(first);
  await fixture.clock.flush();
  assert.equal(outcome.state, "fulfilled", String(outcome.error || "cleanup is still pending"));
  assert.equal(server.close(), first);
  assert.equal(fixture.child.kills.length, 1);
  assertNextRestored(fixture);
}

export function assertNextFailureDiagnostic(fixture: NextReadinessFixture, reason: string) {
  assert.equal(fixture.lines.length, 1, "startup failure diagnostics must be emitted once");
  const line = fixture.lines[0];
  assert.ok(Buffer.byteLength(line) <= 2_048);
  assert.match(line, /^NEXT_SERVER_READINESS_FAILURE /);
  assert.doesNotMatch(line, /CONTROLLED_|127\.0\.0\.1|http:|controlled-next|Next server|Ready|BROWSER_FETCH_FAILURE/);
  const data = JSON.parse(line.slice("NEXT_SERVER_READINESS_FAILURE ".length));
  assert.deepEqual(Object.keys(data).sort(), ["child_exit_seen", "child_signal_seen", "deadline_expired", "http_response_seen", "last_status_class", "phase", "probe_count", "reason_class"]);
  assert.equal(data.phase, "spawned");
  assert.equal(data.reason_class, reason);
  assert.ok(Number.isInteger(data.probe_count) && data.probe_count >= 0 && data.probe_count <= 65_535);
  assert.equal(data.probe_count, Math.min(fixture.probes.length, 65_535));
  for (const key of ["http_response_seen", "deadline_expired", "child_exit_seen", "child_signal_seen"]) assert.equal(typeof data[key], "boolean");
  assert.ok(["none", "1xx", "2xx", "3xx", "4xx", "5xx", "other"].includes(data.last_status_class));
  assert.equal(data.deadline_expired, reason === "deadline");
  assert.equal(data.child_exit_seen, reason === "child_exit" || reason === "child_signal");
  assert.equal(data.child_signal_seen, reason === "child_signal");
}

export function assertNextFailed(fixture: NextReadinessFixture, outcome: ReturnType<typeof observeNextStartup<NextReadyServer>>, reason: string) {
  assert.equal(outcome.state, "rejected", "readiness did not reject within its fixed deadline or child event");
  assert.ok(outcome.error instanceof Error);
  assert.equal(outcome.settlements, 1);
  assertNextRestored(fixture);
  assertNextFailureDiagnostic(fixture, reason);
  return outcome.error;
}
