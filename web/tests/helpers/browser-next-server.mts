import { spawn, spawnSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import { existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { asError } from "./browser-process-timing.mts";


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
  let closing: Promise<void> | undefined;
  const close = () => closing ??= (async () => {
    const errors: unknown[] = [];
    try { await terminateNextServer(server); } catch (error) { errors.push(error); }
    try { restoreNextGeneratedFiles(generatedFiles); } catch (error) { errors.push(error); }
    if (errors.length === 1) throw errors[0];
    if (errors.length > 1) throw new AggregateError(errors, "Next server cleanup failed");
  })();
  let output = "";
  const captureOutput = (chunk: Buffer) => { output = `${output}${chunk}`.slice(-8_000); };
  server.stdout.on("data", captureOutput);
  server.stderr.on("data", captureOutput);
  try {
    await waitForNextServerReady(server, requestedBaseUrl, () => output);
  } catch (error) {
    try { await close(); } catch (cleanupError) {
      throw new AggregateError([error, cleanupError], "Next server startup and cleanup failed", { cause: error });
    }
    throw error;
  } finally {
    server.stdout.off("data", captureOutput);
    server.stderr.off("data", captureOutput);
  }
  return {
    baseUrl: requestedBaseUrl,
    close,
  };
}

async function waitForNextServerReady(server: ChildProcessWithoutNullStreams, url: string, readOutput: () => string) {
  const deadline = Date.now() + 30_000;
  const controller = new AbortController();
  let finished = false;
  let probeCount = 0;
  let httpResponseSeen = false;
  let lastStatusClass = "none";
  let childExitSeen = false;
  let childSignalSeen = false;
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let resumePoll: (() => void) | undefined;
  let rejectFailure!: (error: Error) => void;
  const failed = new Promise<never>((_, reject) => { rejectFailure = reject; });
  const fail = (error: Error, reasonClass: "deadline" | "child_error" | "child_exit" | "child_signal") => {
    if (finished) return;
    finished = true;
    try {
      console.error(`NEXT_SERVER_READINESS_FAILURE ${JSON.stringify({
        phase: "spawned", reason_class: reasonClass, probe_count: probeCount,
        http_response_seen: httpResponseSeen, last_status_class: lastStatusClass,
        deadline_expired: Date.now() >= deadline, child_exit_seen: childExitSeen, child_signal_seen: childSignalSeen,
      })}`);
    } catch { /* Diagnostics must not replace the startup failure. */ }
    controller.abort(error);
    rejectFailure(error);
  };
  const deadlineFailure = () => fail(new Error(`Next server did not become ready:\n${readOutput()}`), "deadline");
  const onError = (error: Error) => fail(error, "child_error");
  const onExit = (_code: number | null, signal: NodeJS.Signals | null) => {
    childExitSeen = true;
    childSignalSeen = signal !== null;
    fail(new Error(`Next server exited early:\n${readOutput()}`), signal !== null ? "child_signal" : "child_exit");
  };
  server.once("error", onError);
  server.once("exit", onExit);
  const deadlineTimer = setTimeout(deadlineFailure, Math.max(0, deadline - Date.now()));
  if (server.exitCode !== null || server.signalCode !== null) onExit(server.exitCode, server.signalCode);
  const poll = async () => {
    while (!finished) {
      if (Date.now() >= deadline) { deadlineFailure(); return; }
      probeCount = Math.min(65_535, probeCount + 1);
      let ready = false;
      try {
        const response = await fetch(url, { signal: controller.signal });
        if (finished) return;
        httpResponseSeen = true;
        const statusClass = Math.floor(response.status / 100);
        lastStatusClass = statusClass >= 1 && statusClass <= 5 ? `${statusClass}xx` : "other";
        ready = response.status < 500;
      } catch { /* A refused connection can be observed again within the same deadline. */ }
      if (finished) return;
      if (Date.now() >= deadline) { deadlineFailure(); return; }
      if (ready) { finished = true; return; }
      await new Promise<void>((resolvePoll) => {
        resumePoll = resolvePoll;
        pollTimer = setTimeout(() => {
          pollTimer = undefined;
          resumePoll = undefined;
          resolvePoll();
        }, Math.min(100, deadline - Date.now()));
      });
    }
  };
  try {
    await Promise.race([failed, poll()]);
  } finally {
    finished = true;
    clearTimeout(deadlineTimer);
    if (pollTimer !== undefined) clearTimeout(pollTimer);
    resumePoll?.();
    controller.abort();
    server.off("error", onError);
    server.off("exit", onExit);
  }
}

async function terminateNextServer(server: ChildProcessWithoutNullStreams) {
  const exited = () => server.exitCode !== null || server.signalCode !== null;
  if (exited() || !server.pid) return;
  if (process.platform === "win32") {
    await waitForNextServerExit(server, 3_000, () => {
      spawnSync("taskkill.exe", ["/PID", String(server.pid), "/T", "/F"], {
        stdio: "ignore", windowsHide: true, timeout: 3_000,
      });
    });
    if (exited()) return;
  }
  await waitForNextServerExit(server, 3_000, () => { server.kill("SIGTERM"); });
  if (exited()) return;
  await waitForNextServerExit(server, 2_000, () => { server.kill("SIGKILL"); });
  if (!exited()) throw new Error("Next server did not exit during cleanup");
}

function waitForNextServerExit(server: ChildProcessWithoutNullStreams, milliseconds: number, terminate: () => void) {
  if (server.exitCode !== null || server.signalCode !== null) return Promise.resolve();
  return new Promise<void>((resolveExit, rejectExit) => {
    let settled = false;
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      server.off("exit", onExit);
      server.off("error", onError);
      if (error) rejectExit(error); else resolveExit();
    };
    const onExit = () => finish();
    const onError = (error: Error) => finish(error);
    const timer = setTimeout(() => finish(), milliseconds);
    server.once("exit", onExit);
    server.once("error", onError);
    try { terminate(); } catch (error) { finish(asError(error)); }
  });
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

async function serverResponds(url: string) {
  try {
    const response = await fetch(url, { signal: AbortSignal.timeout(1_000) });
    return response.status < 500;
  } catch {
    return false;
  }
}

function nextServerEnvironment() {
  const environment: NodeJS.ProcessEnv = {
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
