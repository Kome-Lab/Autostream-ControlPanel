import assert from "node:assert/strict";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { EventEmitter } from "node:events";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import test from "node:test";

import {
  connectToBrowserDevTools,
  createDevToolsStderrParser,
  parseDevToolsActivePort,
  parseDevToolsStderrLine,
  type ActivePortAccess,
  type BrowserStartupOptions,
} from "./browser-startup.mts";
import { resolveBrowserPath } from "./browser-harness.mts";

const validPath = "/devtools/browser/01234567-89ab-cdef-0123-456789abcdef";
const validUrl = `ws://127.0.0.1:9222${validPath}`;

test("DevToolsActivePort accepts only the strict Chrome two-line format", async (t) => {
  for (const fixture of [
    { name: "LF", content: `9222\n${validPath}` },
    { name: "CRLF", content: `9222\r\n${validPath}` },
    { name: "terminal newline", content: `9222\n${validPath}\n` },
    { name: "one trailing blank line", content: `9222\n${validPath}\n\n` },
    { name: "minimum port", content: `1\n${validPath}` },
    { name: "maximum port", content: `65535\n${validPath}` },
  ]) {
    await t.test(fixture.name, () => {
      const port = fixture.name === "minimum port" ? 1 : fixture.name === "maximum port" ? 65_535 : 9_222;
      assert.deepEqual(
        parseDevToolsActivePort(Buffer.from(fixture.content)),
        { ok: true, url: `ws://127.0.0.1:${port}${validPath}` },
      );
    });
  }
});

test("DevToolsActivePort rejects partial, malformed, unsafe, and overbroad fixtures", async (t) => {
  const fixtures: Array<{ name: string; content: Uint8Array; category: string; retryable?: boolean }> = [
    { name: "partial first line", content: Buffer.from("9222"), category: "missing_path", retryable: true },
    { name: "partial second line", content: Buffer.from("9222\n"), category: "missing_path", retryable: true },
    { name: "invalid port text", content: Buffer.from(`nine\n${validPath}`), category: "invalid_port" },
    { name: "zero port", content: Buffer.from(`0\n${validPath}`), category: "port_out_of_range" },
    { name: "port above range", content: Buffer.from(`65536\n${validPath}`), category: "port_out_of_range" },
    { name: "negative port", content: Buffer.from(`-1\n${validPath}`), category: "invalid_port" },
    { name: "missing path", content: Buffer.from("9222\n"), category: "missing_path", retryable: true },
    { name: "wrong path prefix", content: Buffer.from("9222\n/json/version"), category: "wrong_path_prefix" },
    {
      name: "full external URL as path",
      content: Buffer.from("9222\nws://example.test:9222/devtools/browser/external"),
      category: "full_url_not_allowed",
    },
    { name: "nested browser path", content: Buffer.from(`9222\n${validPath}/extra`), category: "unexpected_path" },
    { name: "extra nonblank line", content: Buffer.from(`9222\n${validPath}\nunexpected`), category: "extra_nonblank_line" },
    { name: "more than one trailing blank", content: Buffer.from(`9222\n${validPath}\n\n\n`), category: "extra_blank_line" },
    { name: "malformed UTF-8", content: Buffer.from([0xff, 0xfe, 0xfd]), category: "invalid_utf8" },
  ];

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      const parsed = parseDevToolsActivePort(fixture.content);
      assert.equal(parsed.ok, false);
      if (parsed.ok) return;
      assert.equal(parsed.category, fixture.category);
      assert.equal(parsed.retryable, fixture.retryable ?? false);
    });
  }
});

test("stderr parser accepts split loopback output, ignores unrelated lines, and uses the first valid endpoint", () => {
  const results: ReturnType<typeof parseDevToolsActivePort>[] = [];
  const parser = createDevToolsStderrParser((result) => results.push(result));
  parser.push(Buffer.from("unrelated Chrome output\r\nDevTools listening on ws://127.0.0.1:9222/devtools/brow"));
  parser.push(Buffer.from("ser/01234567-89ab-cdef-0123-456789abcdef\r\n"));
  parser.push(Buffer.from("DevTools listening on ws://127.0.0.1:9333/devtools/browser/later\n"));
  parser.end();

  assert.deepEqual(results, [{ ok: true, url: validUrl }]);
});

test("stderr parser rejects non-loopback, non-ws, and unexpected-path endpoints", async (t) => {
  const fixtures = [
    { name: "all-interface bind", url: `ws://0.0.0.0:9222${validPath}`, category: "external_host" },
    { name: "external hostname", url: `ws://example.test:9222${validPath}`, category: "external_host" },
    { name: "localhost alias", url: `ws://localhost:9222${validPath}`, category: "external_host" },
    { name: "secure websocket", url: `wss://127.0.0.1:9222${validPath}`, category: "unsupported_protocol" },
    { name: "wrong path", url: "ws://127.0.0.1:9222/devtools/page/id", category: "wrong_path_prefix" },
    { name: "missing explicit port", url: `ws://127.0.0.1${validPath}`, category: "missing_port" },
    { name: "query suffix", url: `${validUrl}?token=not-allowed`, category: "query_or_fragment_not_allowed" },
  ];

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      const parsed = parseDevToolsStderrLine(`DevTools listening on ${fixture.url}`);
      assert.ok(parsed);
      assert.equal(parsed.ok, false);
      if (parsed.ok) return;
      assert.equal(parsed.category, fixture.category);
    });
  }
});

test("delayed DevToolsActivePort creation opens the validated loopback endpoint", async () => {
  const browserProcess = new FakeBrowserProcess();
  const profile = mkdtempSync(join(tmpdir(), "autostream-browser-startup-test-"));
  const tracked = trackedTimers();
  let openedUrl = "";
  const expectedSocket = fakeSocket();

  try {
    const connected = connectToBrowserDevTools(asChildProcess(browserProcess), profile, {
      timeoutMs: 500,
      activePortProbeIntervalMs: 5,
      timers: tracked.options,
      openWebSocket: async (url) => {
        openedUrl = url;
        return expectedSocket;
      },
    });
    writeFileSync(join(profile, "DevToolsActivePort"), `9222\n${validPath}`);

    assert.equal(await connected, expectedSocket);
    assert.equal(openedUrl, validUrl);
    assertStartupResourcesReleased(browserProcess, tracked);
  } finally {
    rmSync(profile, { recursive: true, force: true });
  }
});

test("validated stderr remains a fallback when the active-port file is absent", async () => {
  const browserProcess = new FakeBrowserProcess();
  const activePort = new FakeActivePort();
  const tracked = trackedTimers();
  const connected = connectToBrowserDevTools(asChildProcess(browserProcess), "unused", {
    ...startupOptions(activePort, tracked),
    openWebSocket: async () => fakeSocket(),
  });
  await activePort.watching;
  browserProcess.stderr.write(`unrelated\nDevTools listening on ${validUrl}\n`);

  await connected;
  assert.equal(activePort.closeCount, 1);
  assertStartupResourcesReleased(browserProcess, tracked);
});

test("browser process error and exit fail immediately with bounded categories", async (t) => {
  await t.test("process error", async () => {
    const browserProcess = new FakeBrowserProcess();
    const activePort = new FakeActivePort();
    const tracked = trackedTimers();
    const connected = connectToBrowserDevTools(asChildProcess(browserProcess), "unused", startupOptions(activePort, tracked));
    await activePort.watching;
    browserProcess.emit("error", new Error("do not expose this process message"));
    await assert.rejects(connected, /reason=process_error/);
    assertStartupResourcesReleased(browserProcess, tracked);
  });

  await t.test("process exit", async () => {
    const browserProcess = new FakeBrowserProcess();
    const activePort = new FakeActivePort();
    const tracked = trackedTimers();
    const connected = connectToBrowserDevTools(asChildProcess(browserProcess), "unused", startupOptions(activePort, tracked));
    await activePort.watching;
    browserProcess.exit(7, null);
    await assert.rejects(connected, /reason=process_exit; code=7; signal=null/);
    assertStartupResourcesReleased(browserProcess, tracked);
  });
});

test("malformed active-port and external stderr endpoints never become startup success", async (t) => {
  await t.test("malformed active-port", async () => {
    const browserProcess = new FakeBrowserProcess();
    const activePort = new FakeActivePort();
    const tracked = trackedTimers();
    const connected = connectToBrowserDevTools(asChildProcess(browserProcess), "unused", startupOptions(activePort, tracked));
    await activePort.watching;
    activePort.set(Buffer.from(`invalid\n${validPath}`));
    await assert.rejects(connected, /reason=malformed_active_port.*activePortParse=invalid_port/);
    assertStartupResourcesReleased(browserProcess, tracked);
  });

  await t.test("external stderr endpoint", async () => {
    const browserProcess = new FakeBrowserProcess();
    const activePort = new FakeActivePort();
    const tracked = trackedTimers();
    const connected = connectToBrowserDevTools(asChildProcess(browserProcess), "unused", startupOptions(activePort, tracked));
    await activePort.watching;
    browserProcess.stderr.write(`DevTools listening on ws://0.0.0.0:9222${validPath}\n`);
    await assert.rejects(connected, /reason=unsafe_stderr_endpoint.*stderrParse=external_host/);
    assertStartupResourcesReleased(browserProcess, tracked);
  });
});

test("endpoint absence times out and never invokes WebSocket connection", async () => {
  const browserProcess = new FakeBrowserProcess();
  const activePort = new FakeActivePort();
  const tracked = trackedTimers();
  let openAttempts = 0;
  await assert.rejects(
    connectToBrowserDevTools(asChildProcess(browserProcess), "unused", {
      ...startupOptions(activePort, tracked),
      timeoutMs: 25,
      openWebSocket: async () => {
        openAttempts += 1;
        return fakeSocket();
      },
    }),
    /reason=endpoint_timeout.*activePortFile=no/,
  );
  assert.equal(openAttempts, 0);
  assertStartupResourcesReleased(browserProcess, tracked);
});

test("caller abort stops discovery immediately and releases startup resources", async () => {
  const browserProcess = new FakeBrowserProcess();
  const activePort = new FakeActivePort();
  const tracked = trackedTimers();
  const controller = new AbortController();
  const connected = connectToBrowserDevTools(asChildProcess(browserProcess), "unused", {
    ...startupOptions(activePort, tracked),
    signal: controller.signal,
  });
  await activePort.watching;
  controller.abort();

  await assert.rejects(connected, /reason=aborted/);
  assert.equal(activePort.closeCount, 1);
  assertStartupResourcesReleased(browserProcess, tracked);
});

test("startup diagnostics are bounded and redact credential-shaped output", async () => {
  const browserProcess = new FakeBrowserProcess();
  const activePort = new FakeActivePort();
  const tracked = trackedTimers();
  const rawToken = "must-not-appear-in-diagnostics";
  const connected = connectToBrowserDevTools(asChildProcess(browserProcess), "unused", {
    ...startupOptions(activePort, tracked),
    timeoutMs: 25,
  });
  await activePort.watching;
  browserProcess.stdout.write(`Bearer ${rawToken}\n`);
  browserProcess.stderr.write(`${"x".repeat(2_500)}?token=${rawToken}\n`);

  await assert.rejects(connected, (error: unknown) => {
    assert.ok(error instanceof Error);
    assert.doesNotMatch(error.message, new RegExp(rawToken));
    assert.match(error.message, /<redacted>/);
    assert.ok(error.message.length < 4_500, "diagnostic output must remain bounded");
    return true;
  });
  assertStartupResourcesReleased(browserProcess, tracked);
});

test("transient WebSocket failures retry only within the original deadline", async () => {
  const browserProcess = new FakeBrowserProcess();
  const activePort = new FakeActivePort(Buffer.from(`9222\n${validPath}`));
  const tracked = trackedTimers();
  let attempts = 0;
  const socket = fakeSocket();
  const connected = await connectToBrowserDevTools(asChildProcess(browserProcess), "unused", {
    ...startupOptions(activePort, tracked),
    timeoutMs: 250,
    webSocketRetryIntervalMs: 1,
    openWebSocket: async () => {
      attempts += 1;
      if (attempts < 3) throw new Error("transient connection refusal");
      return socket;
    },
  });

  assert.equal(connected, socket);
  assert.equal(attempts, 3);
  assertStartupResourcesReleased(browserProcess, tracked);
});

test("permanent and never-settling WebSocket failures remain nonzero and release resources", async (t) => {
  await t.test("permanent refusal", async () => {
    const browserProcess = new FakeBrowserProcess();
    const activePort = new FakeActivePort(Buffer.from(`9222\n${validPath}`));
    const tracked = trackedTimers();
    let attempts = 0;
    await assert.rejects(
      connectToBrowserDevTools(asChildProcess(browserProcess), "unused", {
        ...startupOptions(activePort, tracked),
        timeoutMs: 30,
        webSocketRetryIntervalMs: 2,
        openWebSocket: async () => {
          attempts += 1;
          throw new Error("connection refused");
        },
      }),
      /reason=websocket_timeout/,
    );
    assert.ok(attempts > 1);
    assertStartupResourcesReleased(browserProcess, tracked);
  });

  await t.test("never settles", async () => {
    const browserProcess = new FakeBrowserProcess();
    const activePort = new FakeActivePort(Buffer.from(`9222\n${validPath}`));
    const tracked = trackedTimers();
    let aborted = 0;
    await assert.rejects(
      connectToBrowserDevTools(asChildProcess(browserProcess), "unused", {
        ...startupOptions(activePort, tracked),
        timeoutMs: 25,
        openWebSocket: (_url, signal) => new Promise<WebSocket>((_resolve, reject) => {
          signal.addEventListener("abort", () => {
            aborted += 1;
            reject(new Error("aborted"));
          }, { once: true });
        }),
      }),
      /reason=websocket_timeout/,
    );
    assert.equal(aborted, 1);
    assertStartupResourcesReleased(browserProcess, tracked);
  });
});

test("a nonexistent executable and an immediate child exit fail closed", async (t) => {
  await t.test("configured nonexistent executable", () => {
    const previous = process.env.AUTOSTREAM_BROWSER_PATH;
    process.env.AUTOSTREAM_BROWSER_PATH = join(tmpdir(), `autostream-browser-does-not-exist-${process.pid}`);
    try {
      assert.throws(() => resolveBrowserPath(), /Configured Chrome\/Chromium executable was not found/);
    } finally {
      if (previous === undefined) delete process.env.AUTOSTREAM_BROWSER_PATH;
      else process.env.AUTOSTREAM_BROWSER_PATH = previous;
    }
  });

  await t.test("nonexistent executable", async () => {
    const child = spawn(join(tmpdir(), `autostream-browser-does-not-exist-${process.pid}`), [], {
      stdio: "pipe",
      windowsHide: true,
    }) as ChildProcessWithoutNullStreams;
    const activePort = new FakeActivePort();
    await assert.rejects(
      connectToBrowserDevTools(child, "unused", { ...startupOptions(activePort), timeoutMs: 500 }),
      /reason=process_error/,
    );
  });

  await t.test("immediate exit", async () => {
    const child = spawn(process.execPath, ["-e", "process.exit(7)"], {
      stdio: "pipe",
      windowsHide: true,
    });
    const activePort = new FakeActivePort();
    await assert.rejects(
      connectToBrowserDevTools(child, "unused", { ...startupOptions(activePort), timeoutMs: 1_000 }),
      /reason=process_exit; code=7; signal=null/,
    );
  });
});

class FakeBrowserProcess extends EventEmitter {
  readonly stdout = new PassThrough();
  readonly stderr = new PassThrough();
  exitCode: number | null = null;
  signalCode: NodeJS.Signals | null = null;

  exit(code: number | null, signal: NodeJS.Signals | null) {
    this.exitCode = code;
    this.signalCode = signal;
    this.emit("exit", code, signal);
  }
}

class FakeActivePort implements ActivePortAccess {
  private content: Uint8Array | null;
  private onChange: (() => void) | undefined;
  private readonly resolveWatching: () => void;
  readonly watching: Promise<void>;
  closeCount = 0;

  constructor(content: Uint8Array | null = null) {
    this.content = content;
    let resolveWatching!: () => void;
    this.watching = new Promise<void>((resolve) => {
      resolveWatching = resolve;
    });
    this.resolveWatching = resolveWatching;
  }

  async read() {
    return this.content;
  }

  watch(onChange: () => void) {
    this.onChange = onChange;
    this.resolveWatching();
    return {
      close: () => {
        this.closeCount += 1;
        this.onChange = undefined;
      },
    };
  }

  set(content: Uint8Array) {
    this.content = content;
    this.onChange?.();
  }
}

function startupOptions(activePort: ActivePortAccess, tracked = trackedTimers()): BrowserStartupOptions {
  return {
    activePortAccess: activePort,
    activePortProbeIntervalMs: 5,
    timeoutMs: 200,
    timers: tracked.options,
    webSocketRetryIntervalMs: 2,
  };
}

function trackedTimers() {
  const active = new Set<ReturnType<typeof setTimeout>>();
  const options: NonNullable<BrowserStartupOptions["timers"]> = {
    now: () => Date.now(),
    set(callback, milliseconds) {
      const timer = setTimeout(() => {
        active.delete(timer);
        callback();
      }, milliseconds);
      active.add(timer);
      return timer;
    },
    clear(timer) {
      clearTimeout(timer);
      active.delete(timer);
    },
  };
  return { active, options };
}

function assertStartupResourcesReleased(browserProcess: FakeBrowserProcess, tracked: ReturnType<typeof trackedTimers>) {
  assert.equal(browserProcess.listenerCount("error"), 0, "process error listeners");
  assert.equal(browserProcess.listenerCount("exit"), 0, "process exit listeners");
  assert.equal(browserProcess.stdout.listenerCount("data"), 0, "stdout data listeners");
  assert.equal(browserProcess.stderr.listenerCount("data"), 0, "stderr data listeners");
  assert.equal(tracked.active.size, 0, "startup timers");
}

function asChildProcess(browserProcess: FakeBrowserProcess) {
  return browserProcess as unknown as ChildProcessWithoutNullStreams;
}

function fakeSocket() {
  return { close() {} } as unknown as WebSocket;
}
