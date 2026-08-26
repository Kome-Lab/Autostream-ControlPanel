import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import test from "node:test";

import type { BrowserLaunchFacts } from "./browser-launch-profile.mts";
import {
  BrowserLaunchError,
  BrowserProcessAttempt,
  createOwnedBrowserAttemptDirectories,
  isRetryableBrowserStartupFailure,
  launchBrowserProcessWithRetry,
  removeOwnedBrowserAttemptDirectory,
  type BrowserProcessAttemptOwner,
} from "./browser-process-attempt.mts";
import {
  BrowserStartupError,
  type BrowserStartupFailureDetails,
} from "./browser-startup.mts";

const launchFacts: BrowserLaunchFacts = {
  platform: "linux",
  browserExecutableBasename: "google-chrome",
  browserExecutableRealpath: "/opt/google/chrome/google-chrome",
  browserVersion: "Google Chrome 151.0.7922.173",
  xdgRuntimeDirectory: "absent",
  dbusSessionBusAddress: "invalid",
  devShm: { available: true, totalBytes: 67_108_864, availableBytes: 50_331_648 },
  dbusRunSessionAvailable: true,
};

test("retry classification requires the exact live endpoint-timeout state", async (t) => {
  assert.equal(isRetryableBrowserStartupFailure(startupError()), true);

  const fixtures: Array<{ name: string; overrides: Partial<BrowserStartupFailureDetails> }> = [
    { name: "timeout plus process exit code", overrides: { processCode: 7 } },
    { name: "timeout plus process signal", overrides: { processSignal: "SIGTERM" } },
    { name: "timeout plus active-port file", overrides: { activePortFile: "yes" } },
    { name: "timeout plus malformed active-port", overrides: { activePortFile: "yes", activePortParse: "invalid_port" } },
    { name: "timeout plus stderr endpoint", overrides: { stderrParse: "valid", endpointSource: "stderr" } },
    { name: "timeout plus unsafe endpoint", overrides: { stderrParse: "external_host" } },
    { name: "timeout after WebSocket activity", overrides: { webSocketAttempts: 1 } },
    { name: "spawn error", overrides: { reason: "process_error" } },
    { name: "process exit", overrides: { reason: "process_exit", processCode: 7 } },
    { name: "caller abort", overrides: { reason: "aborted" } },
    { name: "malformed active-port", overrides: { reason: "malformed_active_port", activePortFile: "yes", activePortParse: "invalid_port" } },
    { name: "unsafe stderr endpoint", overrides: { reason: "unsafe_stderr_endpoint", stderrParse: "external_host" } },
    { name: "CDP connection timeout", overrides: { reason: "websocket_timeout", activePortFile: "yes", activePortParse: "valid", endpointSource: "active-port", webSocketAttempts: 2 } },
  ];

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      assert.equal(isRetryableBrowserStartupFailure(startupError(fixture.overrides)), false);
    });
  }
});

test("first exact endpoint timeout is cleaned before one fresh retry", async () => {
  const events: string[] = [];
  const reports: string[] = [];
  const owners: FakeAttemptOwner[] = [];
  const session = await launchBrowserProcessWithRetry({
    browserPath: "/configured/google-chrome",
    platform: "linux",
    parentEnvironment: { PATH: "/usr/bin" },
    launchFacts,
    report: (message) => reports.push(message),
    createAttempt(context) {
      assert.equal(context.attemptNumber, owners.length + 1);
      if (context.attemptNumber === 2) {
        assert.equal(owners[0]?.closed, true, "attempt two was created before attempt one cleanup");
        assert.equal(owners[0]?.processAlive, false, "attempt one process survived into retry");
        assert.equal(owners[0]?.profileExists, false, "attempt one profile survived into retry");
        assert.equal(owners[0]?.runtimeExists, false, "attempt one XDG runtime survived into retry");
        assert.equal(owners[0]?.watcherCount, 0, "attempt one watcher survived into retry");
        assert.equal(owners[0]?.listenerCount, 0, "attempt one listener survived into retry");
        assert.equal(owners[0]?.failedSocketOpen, false, "attempt one failed socket survived into retry");
      }
      const owner = new FakeAttemptOwner(
        context.attemptNumber,
        events,
        context.attemptNumber === 1 ? startupError() : null,
      );
      owners.push(owner);
      return owner;
    },
  });

  assert.equal(session.startupAttemptCount, 2);
  assert.equal(session.userDataDirectory, "/owned/attempt-2/profile");
  assert.deepEqual(owners.map((owner) => owner.timeoutMs), [45_000, 45_000]);
  assert.deepEqual(events, ["create:1", "launch:1", "close:1", "create:2", "launch:2"]);
  assert.deepEqual(reports, ["browser_startup_attempts=2"]);
  assert.equal(owners[1]?.closed, false);
  assert.match(session.failureDiagnostics(), /browser_startup_attempts=2/);
  assert.match(session.failureDiagnostics(), /attempt1=\{reason=endpoint_timeout/);
  assert.match(session.failureDiagnostics(), /attempt2=\{reason=connected/);
  assert.match(session.failureDiagnostics(), /platform=linux/);

  await session.close();
  assert.equal(owners[1]?.closed, true);
  assert.equal(owners[1]?.processAlive, false);
  assert.equal(owners[1]?.profileExists, false);
  assert.equal(owners[1]?.runtimeExists, false);
});

test("the 90-second total budget reduces attempt two after attempt-one cleanup", async () => {
  let now = 0;
  const timeouts: number[] = [];
  const session = await launchBrowserProcessWithRetry({
    browserPath: "/configured/google-chrome",
    platform: "linux",
    parentEnvironment: {},
    launchFacts,
    now: () => now,
    report: () => {},
    createAttempt(context) {
      return {
        async launch({ timeoutMs }) {
          timeouts.push(timeoutMs);
          if (context.attemptNumber === 1) {
            now = 45_000;
            throw startupError();
          }
          return fakeConnection(context.attemptNumber);
        },
        async close() {
          if (context.attemptNumber === 1) now = 50_000;
        },
      };
    },
  });

  assert.deepEqual(timeouts, [45_000, 40_000]);
  await session.close();
});

test("a second endpoint timeout fails with both bounded attempt summaries", async () => {
  const rawSecret = "must-not-appear-in-aggregate-launch-error";
  const owners: FakeAttemptOwner[] = [];
  await assert.rejects(
    launchBrowserProcessWithRetry({
      browserPath: "/configured/google-chrome",
      platform: "linux",
      parentEnvironment: {},
      launchFacts,
      report: () => {},
      createAttempt(context) {
        const owner = new FakeAttemptOwner(context.attemptNumber, [], startupError({
          stderrTail: `Bearer ${rawSecret} ${"x".repeat(3_000)}`,
        }));
        owners.push(owner);
        return owner;
      },
    }),
    (error: unknown) => {
      assert.ok(error instanceof BrowserLaunchError);
      assert.equal(error.attempts.length, 2);
      assert.match(error.message, /attemptCount=2/);
      assert.match(error.message, /attempt1=\{reason=endpoint_timeout/);
      assert.match(error.message, /attempt2=\{reason=endpoint_timeout/);
      assert.match(error.message, /platform=linux/);
      assert.doesNotMatch(error.message, new RegExp(rawSecret));
      assert.ok(error.message.length < 8_000, "aggregate launch diagnostics must remain bounded");
      return true;
    },
  );
  assert.deepEqual(owners.map((owner) => owner.closed), [true, true]);
});

test("non-retryable failures each create only one attempt", async (t) => {
  const fixtures: Array<{ name: string; failure: Error }> = [
    { name: "timeout plus process exit", failure: startupError({ processCode: 7 }) },
    { name: "timeout plus malformed endpoint", failure: startupError({ activePortFile: "yes", activePortParse: "invalid_port" }) },
    { name: "timeout plus unsafe endpoint", failure: startupError({ stderrParse: "external_host" }) },
    { name: "spawn error", failure: startupError({ reason: "process_error" }) },
    { name: "process exit", failure: startupError({ reason: "process_exit", processCode: 7 }) },
    { name: "malformed active-port", failure: startupError({ reason: "malformed_active_port", activePortFile: "yes", activePortParse: "invalid_port" }) },
    { name: "unsafe endpoint", failure: startupError({ reason: "unsafe_stderr_endpoint", stderrParse: "external_host" }) },
    { name: "caller abort", failure: startupError({ reason: "aborted" }) },
    { name: "CDP protocol error after connection", failure: new Error("CDP protocol rejected Target.createTarget") },
  ];

  for (const fixture of fixtures) {
    await t.test(fixture.name, async () => {
      let attempts = 0;
      let closes = 0;
      await assert.rejects(launchBrowserProcessWithRetry({
        browserPath: "/configured/google-chrome",
        platform: "linux",
        parentEnvironment: {},
        launchFacts,
        report: () => {},
        createAttempt() {
          attempts += 1;
          return {
            async launch() {
              throw fixture.failure;
            },
            async close() {
              closes += 1;
            },
          };
        },
      }), /attemptCount=1/);
      assert.equal(attempts, 1);
      assert.equal(closes, 1);
    });
  }
});

test("an already-aborted caller starts no browser and never retries", async () => {
  const controller = new AbortController();
  controller.abort();
  let attempts = 0;
  await assert.rejects(launchBrowserProcessWithRetry({
    browserPath: "/configured/google-chrome",
    platform: "linux",
    parentEnvironment: {},
    launchFacts,
    signal: controller.signal,
    report: () => {},
    createAttempt() {
      attempts += 1;
      return new FakeAttemptOwner(1, [], null);
    },
  }), /reason=aborted/);
  assert.equal(attempts, 0);
});

test("unexpected startup errors are classified without retaining a raw public cause", async () => {
  const rawSecret = "raw-startup-cause-must-not-survive";
  await assert.rejects(launchBrowserProcessWithRetry({
    browserPath: "/configured/google-chrome",
    platform: "linux",
    parentEnvironment: {},
    launchFacts,
    report: () => {},
    createAttempt() {
      return {
        async launch() {
          throw new Error(`spawn wrapper detail ${rawSecret}`);
        },
        async close() {},
      };
    },
  }), (error: unknown) => {
    assert.ok(error instanceof BrowserLaunchError);
    assert.match(error.message, /reason=unexpected_startup_error/);
    assert.doesNotMatch(error.message, new RegExp(rawSecret));
    assert.equal(error.cause, undefined);
    return true;
  });
});

test("first success uses one attempt and a later scenario failure cannot relaunch it", async () => {
  let attempts = 0;
  const owner = new FakeAttemptOwner(1, [], null);
  const session = await launchBrowserProcessWithRetry({
    browserPath: "/configured/google-chrome",
    platform: "linux",
    parentEnvironment: {},
    launchFacts,
    report: () => {},
    createAttempt() {
      attempts += 1;
      return owner;
    },
  });

  await assert.rejects(async () => {
    throw new Error("UI scenario assertion failed");
  }, /UI scenario assertion failed/);
  assert.equal(session.startupAttemptCount, 1);
  assert.equal(attempts, 1, "scenario failure triggered a browser relaunch");
  await session.close();
});

test("BrowserProcessAttempt owns launch environment, socket, process, and directory cleanup", async () => {
  const events: string[] = [];
  const browserProcess = fakeBrowserProcess();
  const socket = { close: () => events.push("socket-close") } as unknown as WebSocket;
  const directories = {
    temporaryDirectory: "/owned",
    attemptRoot: "/owned/autostream-ui-browser-attempt-fixture",
    userDataDirectory: "/owned/autostream-ui-browser-attempt-fixture/profile",
    xdgRuntimeDirectory: "/owned/autostream-ui-browser-attempt-fixture/xdg-runtime",
  };
  const owner = new BrowserProcessAttempt({
    browserPath: "/configured/google-chrome",
    platform: "linux",
    attemptNumber: 2,
    parentEnvironment: {
      PATH: "/usr/bin",
      DBUS_SESSION_BUS_ADDRESS: "malformed secret value",
    },
    dependencies: {
      createDirectories: () => {
        events.push("directories-create");
        return directories;
      },
      spawnBrowser: (path, args, options) => {
        events.push("process-spawn");
        assert.equal(path, "/configured/google-chrome");
        assert.ok(args.includes("--enable-logging=stderr"));
        assert.ok(args.includes("--v=1"));
        assert.ok(args.includes(`--user-data-dir=${directories.userDataDirectory}`));
        assert.equal(options.env?.XDG_RUNTIME_DIR, directories.xdgRuntimeDirectory);
        assert.equal(options.env?.DBUS_SESSION_BUS_ADDRESS, undefined);
        assert.equal(options.detached, true);
        return browserProcess;
      },
      connectToDevTools: async (_process, profile, options) => {
        events.push("devtools-connect");
        assert.equal(profile, directories.userDataDirectory);
        assert.equal(options.timeoutMs, 45_000);
        options.recordConnectedDiagnostics?.(connectedStartupDetails());
        browserProcess.stderr.write("Cookie: session=owned-attempt-secret\nlate runtime error\n");
        return socket;
      },
      terminateProcessTree: async (process) => {
        events.push("process-terminate");
        assert.equal(process, browserProcess);
      },
      removeDirectories: async (owned) => {
        events.push("directories-remove");
        assert.equal(owned, directories);
      },
    },
  });

  const connection = await owner.launch({ timeoutMs: 45_000 });
  assert.equal(connection.browserProcess, browserProcess);
  assert.equal(connection.userDataDirectory, directories.userDataDirectory);
  assert.equal(connection.socket, socket);
  assert.equal(browserProcess.stderr.listenerCount("data"), 1);
  const diagnostics = owner.diagnosticDetails();
  assert.equal(diagnostics.reason, "connected");
  assert.equal(diagnostics.endpointSource, "active-port");
  assert.doesNotMatch(diagnostics.stderrTail, /owned-attempt-secret/);
  assert.match(diagnostics.stderrTail, /late runtime error/);
  await owner.close();
  await owner.close();
  assert.equal(browserProcess.stdout.listenerCount("data"), 0);
  assert.equal(browserProcess.stderr.listenerCount("data"), 0);
  assert.deepEqual(events, [
    "directories-create",
    "process-spawn",
    "devtools-connect",
    "socket-close",
    "process-terminate",
    "directories-remove",
  ]);
});

test("Linux attempt directories request mode 0700, are unique, and reject unowned cleanup", async () => {
  const fakeCalls: Array<{ operation: string; path: string; mode?: number }> = [];
  const fakeModes = new Map<string, number>();
  const fakeTemporaryDirectory = join(tmpdir(), "fake-browser-attempt-parent");
  const fakeRoot = join(fakeTemporaryDirectory, "autostream-ui-browser-attempt-fixture");
  const fakeFileSystem = {
    makeTemporaryDirectory(prefix: string) {
      fakeCalls.push({ operation: "mkdtemp", path: prefix, mode: 0o700 });
      fakeModes.set(fakeRoot, 0o700);
      return fakeRoot;
    },
    makeDirectory(path: string, mode: number) {
      fakeCalls.push({ operation: "mkdir", path, mode });
      fakeModes.set(path, mode);
    },
    setMode(path: string, mode: number) {
      fakeCalls.push({ operation: "chmod", path, mode });
      fakeModes.set(path, mode);
    },
    inspect(path: string) {
      return { exists: fakeModes.has(path), directory: true, symbolicLink: false, mode: fakeModes.get(path) ?? 0 };
    },
    remove(path: string) {
      fakeCalls.push({ operation: "remove", path });
    },
  };
  const fakeDirectories = createOwnedBrowserAttemptDirectories({
    platform: "linux",
    temporaryDirectory: fakeTemporaryDirectory,
    fileSystem: fakeFileSystem,
  });
  assert.equal(fakeDirectories.xdgRuntimeDirectory, join(fakeRoot, "xdg-runtime"));
  assert.equal(fakeModes.get(fakeDirectories.attemptRoot), 0o700);
  assert.equal(fakeModes.get(fakeDirectories.userDataDirectory), 0o700);
  assert.equal(fakeModes.get(fakeDirectories.xdgRuntimeDirectory || ""), 0o700);
  assert.ok(fakeCalls.some((call) => call.operation === "chmod" && call.path === fakeDirectories.xdgRuntimeDirectory && call.mode === 0o700));

  const actualParent = mkdtempSync(join(tmpdir(), "autostream-attempt-directory-test-"));
  try {
    const first = createOwnedBrowserAttemptDirectories({ platform: "linux", temporaryDirectory: actualParent });
    const second = createOwnedBrowserAttemptDirectories({ platform: "linux", temporaryDirectory: actualParent });
    assert.notEqual(first.attemptRoot, second.attemptRoot);
    assert.notEqual(first.userDataDirectory, second.userDataDirectory);
    assert.notEqual(first.xdgRuntimeDirectory, second.xdgRuntimeDirectory);
    assert.equal(existsSync(first.userDataDirectory), true);
    assert.equal(existsSync(first.xdgRuntimeDirectory || ""), true);
    await removeOwnedBrowserAttemptDirectory(first);
    await removeOwnedBrowserAttemptDirectory(second);
    assert.equal(existsSync(first.attemptRoot), false);
    assert.equal(existsSync(second.attemptRoot), false);

    await assert.rejects(removeOwnedBrowserAttemptDirectory({
      temporaryDirectory: actualParent,
      attemptRoot: actualParent,
      userDataDirectory: join(actualParent, "profile"),
      xdgRuntimeDirectory: join(actualParent, "xdg-runtime"),
    }), /Refusing to remove unexpected browser attempt directory/);
  } finally {
    rmSync(actualParent, { recursive: true, force: true });
  }
});

test("partial attempt-directory creation removes the owned root before failing", () => {
  const temporaryDirectory = join(tmpdir(), "partial-browser-attempt-parent");
  const attemptRoot = join(temporaryDirectory, "autostream-ui-browser-attempt-partial");
  const removed: string[] = [];
  let modeCalls = 0;
  const fileSystem = {
    makeTemporaryDirectory() {
      return attemptRoot;
    },
    makeDirectory() {},
    setMode() {
      modeCalls += 1;
      if (modeCalls === 2) throw new Error("simulated profile mode failure");
    },
    inspect() {
      return { exists: true, directory: true, symbolicLink: false, mode: 0o700 };
    },
    remove(path: string) {
      removed.push(path);
    },
  };

  assert.throws(() => createOwnedBrowserAttemptDirectories({
    platform: "linux",
    temporaryDirectory,
    fileSystem,
  }), /simulated profile mode failure/);
  assert.deepEqual(removed, [attemptRoot]);
});

class FakeAttemptOwner implements BrowserProcessAttemptOwner {
  readonly browserProcess = fakeBrowserProcess();
  readonly userDataDirectory: string;
  readonly socket = { close() {} } as unknown as WebSocket;
  readonly attemptNumber: number;
  timeoutMs = 0;
  closed = false;
  processAlive = true;
  profileExists = true;
  runtimeExists = true;
  watcherCount = 1;
  listenerCount = 2;
  failedSocketOpen = true;
  private readonly events: string[];
  private readonly failure: Error | null;

  constructor(
    attemptNumber: number,
    events: string[],
    failure: Error | null,
  ) {
    this.attemptNumber = attemptNumber;
    this.events = events;
    this.failure = failure;
    this.userDataDirectory = `/owned/attempt-${attemptNumber}/profile`;
    this.events.push(`create:${attemptNumber}`);
  }

  async launch({ timeoutMs }: { timeoutMs: number; signal?: AbortSignal }) {
    this.timeoutMs = timeoutMs;
    this.events.push(`launch:${this.attemptNumber}`);
    if (this.failure) throw this.failure;
    return {
      browserProcess: this.browserProcess,
      userDataDirectory: this.userDataDirectory,
      socket: this.socket,
    };
  }

  async close() {
    if (this.closed) return;
    this.closed = true;
    this.processAlive = false;
    this.profileExists = false;
    this.runtimeExists = false;
    this.watcherCount = 0;
    this.listenerCount = 0;
    this.failedSocketOpen = false;
    this.events.push(`close:${this.attemptNumber}`);
  }

  diagnosticDetails() {
    return connectedStartupDetails();
  }
}

function startupError(overrides: Partial<BrowserStartupFailureDetails> = {}) {
  return new BrowserStartupError({
    reason: "endpoint_timeout",
    processCode: null,
    processSignal: null,
    activePortFile: "no",
    activePortParse: "not_present",
    stderrParse: "not_reported",
    endpointSource: "none",
    webSocketAttempts: 0,
    stdoutTail: "",
    stderrTail: "",
    ...overrides,
  });
}

function connectedStartupDetails(): BrowserStartupFailureDetails {
  return {
    reason: "connected",
    processCode: null,
    processSignal: null,
    activePortFile: "yes",
    activePortParse: "valid",
    stderrParse: "not_reported",
    endpointSource: "active-port",
    webSocketAttempts: 1,
    stdoutTail: "",
    stderrTail: "",
  };
}

function fakeConnection(attemptNumber: number) {
  return {
    browserProcess: fakeBrowserProcess(),
    userDataDirectory: `/owned/attempt-${attemptNumber}/profile`,
    socket: { close() {} } as unknown as WebSocket,
  };
}

function fakeBrowserProcess() {
  const browserProcess = new EventEmitter() as EventEmitter & {
    stdout: PassThrough;
    stderr: PassThrough;
    exitCode: number | null;
    signalCode: NodeJS.Signals | null;
  };
  browserProcess.stdout = new PassThrough();
  browserProcess.stderr = new PassThrough();
  browserProcess.exitCode = null;
  browserProcess.signalCode = null;
  return browserProcess as unknown as import("node:child_process").ChildProcessWithoutNullStreams;
}
