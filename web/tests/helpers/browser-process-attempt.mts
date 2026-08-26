import {
  spawn,
  spawnSync,
  type ChildProcessWithoutNullStreams,
} from "node:child_process";
import { chmodSync, lstatSync, mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";

import {
  browserAttemptTimeoutMs,
  browserLaunchBudgetMs,
  buildBrowserLaunchProfile,
  collectBrowserLaunchFacts,
  formatBrowserLaunchFacts,
  sanitizeBrowserDiagnosticText,
  type BrowserLaunchFacts,
} from "./browser-launch-profile.mts";
import {
  BrowserStartupError,
  connectToBrowserDevTools,
  formatBrowserStartupFailureDetails,
  type BrowserStartupFailureDetails,
  type BrowserStartupOptions,
} from "./browser-startup.mts";

const attemptDirectoryPrefix = "autostream-ui-browser-attempt-";
const ownedDirectoryMode = 0o700;

export type BrowserAttemptDirectories = Readonly<{
  temporaryDirectory: string;
  attemptRoot: string;
  userDataDirectory: string;
  xdgRuntimeDirectory?: string;
}>;

export type BrowserAttemptDirectoryFileSystem = {
  makeTemporaryDirectory: (prefix: string) => string;
  makeDirectory: (path: string, mode: number) => void;
  setMode: (path: string, mode: number) => void;
  inspect: (path: string) => {
    exists: boolean;
    directory: boolean;
    symbolicLink: boolean;
    mode: number;
  };
  remove: (path: string) => void;
};

export type BrowserAttemptConnection = Readonly<{
  browserProcess: ChildProcessWithoutNullStreams;
  userDataDirectory: string;
  socket: WebSocket;
}>;

export type BrowserProcessAttemptOwner = {
  launch: (options: { timeoutMs: number; signal?: AbortSignal }) => Promise<BrowserAttemptConnection>;
  close: () => Promise<void>;
  diagnosticDetails?: () => BrowserStartupFailureDetails;
};

export type BrowserProcessAttemptContext = Readonly<{
  browserPath: string;
  platform: NodeJS.Platform;
  attemptNumber: 1 | 2;
  parentEnvironment: Readonly<NodeJS.ProcessEnv>;
}>;

type BrowserSpawnOptions = {
  cwd: string;
  env: NodeJS.ProcessEnv;
  stdio: "pipe";
  windowsHide: boolean;
  detached: boolean;
};

export type BrowserProcessAttemptDependencies = {
  createDirectories: (options: {
    platform: NodeJS.Platform;
    temporaryDirectory?: string;
  }) => BrowserAttemptDirectories;
  spawnBrowser: (
    browserPath: string,
    args: readonly string[],
    options: BrowserSpawnOptions,
  ) => ChildProcessWithoutNullStreams;
  connectToDevTools: (
    browserProcess: ChildProcessWithoutNullStreams,
    userDataDirectory: string,
    options: BrowserStartupOptions,
  ) => Promise<WebSocket>;
  terminateProcessTree: (browserProcess: ChildProcessWithoutNullStreams, platform: NodeJS.Platform) => Promise<void>;
  removeDirectories: (directories: BrowserAttemptDirectories) => Promise<void>;
};

export type BrowserProcessAttemptOptions = BrowserProcessAttemptContext & {
  dependencies?: Partial<BrowserProcessAttemptDependencies>;
};

export type BrowserLaunchAttemptSummary = Readonly<{
  attemptNumber: number;
  details: BrowserStartupFailureDetails;
}>;

export type BrowserLaunchSession = BrowserAttemptConnection & Readonly<{
  startupAttemptCount: number;
  launchFacts: BrowserLaunchFacts;
  close: () => Promise<void>;
  failureDiagnostics: () => string;
}>;

export type BrowserLaunchRetryOptions = {
  browserPath: string;
  platform?: NodeJS.Platform;
  parentEnvironment?: Readonly<NodeJS.ProcessEnv>;
  launchFacts?: BrowserLaunchFacts;
  signal?: AbortSignal;
  report?: (message: string) => void;
  now?: () => number;
  createAttempt?: (context: BrowserProcessAttemptContext) => BrowserProcessAttemptOwner;
};

export class BrowserLaunchError extends Error {
  readonly attempts: readonly BrowserLaunchAttemptSummary[];
  readonly launchFacts: BrowserLaunchFacts;

  constructor(
    attempts: readonly BrowserLaunchAttemptSummary[],
    launchFacts: BrowserLaunchFacts,
    terminalReason?: string,
  ) {
    const attemptFields = attempts.map((attempt) => (
      `attempt${attempt.attemptNumber}={${formatBrowserStartupFailureDetails(attempt.details)}}`
    ));
    const fields = [
      `attemptCount=${attempts.length}`,
      ...attemptFields,
      ...(terminalReason ? [`reason=${terminalReason}`] : []),
      `launchFacts={${formatBrowserLaunchFacts(launchFacts)}}`,
    ];
    super(`Browser launch failed: ${fields.join("; ")}`);
    this.name = "BrowserLaunchError";
    this.attempts = Object.freeze([...attempts]);
    this.launchFacts = launchFacts;
  }
}

export class BrowserProcessAttempt implements BrowserProcessAttemptOwner {
  private readonly browserPath: string;
  private readonly platform: NodeJS.Platform;
  private readonly attemptNumber: 1 | 2;
  private readonly parentEnvironment: Readonly<NodeJS.ProcessEnv>;
  private readonly dependencies: BrowserProcessAttemptDependencies;
  private directories: BrowserAttemptDirectories | undefined;
  private browserProcess: ChildProcessWithoutNullStreams | undefined;
  private socket: WebSocket | undefined;
  private startupDiagnostics: BrowserStartupFailureDetails | undefined;
  private stdoutTail = "";
  private stderrTail = "";
  private readonly onStdout = (chunk: Buffer) => {
    this.stdoutTail = appendRuntimeTail(this.stdoutTail, chunk);
  };
  private readonly onStderr = (chunk: Buffer) => {
    this.stderrTail = appendRuntimeTail(this.stderrTail, chunk);
  };
  private launchStarted = false;
  private closed = false;

  constructor(options: BrowserProcessAttemptOptions) {
    this.browserPath = options.browserPath;
    this.platform = options.platform;
    this.attemptNumber = options.attemptNumber;
    this.parentEnvironment = options.parentEnvironment;
    this.dependencies = { ...defaultAttemptDependencies, ...options.dependencies };
  }

  async launch(options: { timeoutMs: number; signal?: AbortSignal }) {
    if (this.launchStarted) throw new Error("Browser process attempt cannot be launched twice");
    if (this.closed) throw new Error("Closed browser process attempt cannot be launched");
    this.launchStarted = true;
    this.directories = this.dependencies.createDirectories({ platform: this.platform });
    const profile = buildBrowserLaunchProfile({
      platform: this.platform,
      attemptNumber: this.attemptNumber,
      userDataDirectory: this.directories.userDataDirectory,
      xdgRuntimeDirectory: this.directories.xdgRuntimeDirectory,
      parentEnvironment: this.parentEnvironment,
    });
    this.browserProcess = this.dependencies.spawnBrowser(this.browserPath, profile.args, {
      cwd: this.directories.attemptRoot,
      env: profile.environment,
      stdio: "pipe",
      windowsHide: true,
      detached: this.platform !== "win32",
    });
    this.browserProcess.stdout.on("data", this.onStdout);
    this.browserProcess.stderr.on("data", this.onStderr);
    this.socket = await this.dependencies.connectToDevTools(
      this.browserProcess,
      this.directories.userDataDirectory,
      {
        timeoutMs: options.timeoutMs,
        signal: options.signal,
        recordConnectedDiagnostics: (details) => {
          this.startupDiagnostics = details;
        },
      },
    );
    return {
      browserProcess: this.browserProcess,
      userDataDirectory: this.directories.userDataDirectory,
      socket: this.socket,
    };
  }

  async close() {
    if (this.closed) return;
    this.closed = true;
    const cleanupFailures: string[] = [];
    if (this.socket) {
      try {
        this.socket.close();
      } catch {
        cleanupFailures.push("socket_close_failed");
      }
    }
    if (this.browserProcess) {
      try {
        await this.dependencies.terminateProcessTree(this.browserProcess, this.platform);
      } catch {
        cleanupFailures.push("process_termination_failed");
      }
      this.browserProcess.stdout.off("data", this.onStdout);
      this.browserProcess.stderr.off("data", this.onStderr);
    }
    if (this.directories) {
      try {
        await this.dependencies.removeDirectories(this.directories);
      } catch {
        cleanupFailures.push("directory_cleanup_failed");
      }
    }
    if (cleanupFailures.length > 0) {
      throw new Error(`Browser attempt cleanup failed: ${cleanupFailures.join(",")}`);
    }
  }

  diagnosticDetails(): BrowserStartupFailureDetails {
    const startup = this.startupDiagnostics ?? connectedDiagnosticsFallback();
    return Object.freeze({
      ...startup,
      processCode: this.browserProcess?.exitCode ?? startup.processCode,
      processSignal: this.browserProcess?.signalCode ?? startup.processSignal,
      stdoutTail: sanitizeBrowserDiagnosticText(this.stdoutTail),
      stderrTail: sanitizeBrowserDiagnosticText(this.stderrTail),
    });
  }
}

export async function launchBrowserProcessWithRetry(options: BrowserLaunchRetryOptions): Promise<BrowserLaunchSession> {
  const platform = options.platform ?? process.platform;
  const parentEnvironment = options.parentEnvironment ?? process.env;
  const now = options.now ?? (() => Date.now());
  const launchDeadline = now() + browserLaunchBudgetMs;
  const facts = options.launchFacts ?? collectBrowserLaunchFacts(options.browserPath, {
    platform,
    environment: parentEnvironment,
    readBrowserVersion: options.signal?.aborted ? () => null : undefined,
  });
  const report = options.report ?? ((message: string) => process.stdout.write(`${message}\n`));
  const createAttempt = options.createAttempt ?? ((context: BrowserProcessAttemptContext) => new BrowserProcessAttempt(context));
  const summaries: BrowserLaunchAttemptSummary[] = [];

  if (options.signal?.aborted) {
    throw new BrowserLaunchError(summaries, facts, "aborted");
  }

  for (const attemptNumber of [1, 2] as const) {
    const remainingBudget = launchDeadline - now();
    if (remainingBudget <= 0) {
      throw new BrowserLaunchError(summaries, facts, "total_launch_timeout");
    }
    const owner = createAttempt({
      browserPath: options.browserPath,
      platform,
      attemptNumber,
      parentEnvironment,
    });
    try {
      const connection = await owner.launch({
        timeoutMs: Math.min(browserAttemptTimeoutMs, remainingBudget),
        signal: options.signal,
      });
      if (attemptNumber === 2) report("browser_startup_attempts=2");
      return {
        ...connection,
        startupAttemptCount: attemptNumber,
        launchFacts: facts,
        close: () => owner.close(),
        failureDiagnostics: () => formatActiveLaunchDiagnostics(
          attemptNumber,
          summaries,
          owner,
          facts,
        ),
      };
    } catch (error) {
      const summary = { attemptNumber, details: failureDetails(error, options.signal) } satisfies BrowserLaunchAttemptSummary;
      summaries.push(summary);
      try {
        await owner.close();
      } catch {
        throw new BrowserLaunchError(summaries, facts, "cleanup_failed");
      }

      const canRetry = attemptNumber === 1
        && isRetryableBrowserStartupFailure(error)
        && !options.signal?.aborted
        && now() < launchDeadline;
      if (canRetry) continue;
      throw new BrowserLaunchError(
        summaries,
        facts,
        options.signal?.aborted && summary.details.reason !== "aborted" ? "aborted" : undefined,
      );
    }
  }

  throw new BrowserLaunchError(summaries, facts, "attempt_limit_reached");
}

export function isRetryableBrowserStartupFailure(error: unknown) {
  if (!(error instanceof BrowserStartupError)) return false;
  const details = error.details;
  return details.reason === "endpoint_timeout"
    && details.processCode === null
    && details.processSignal === null
    && details.activePortFile === "no"
    && details.activePortParse === "not_present"
    && details.stderrParse === "not_reported"
    && details.endpointSource === "none"
    && details.webSocketAttempts === 0;
}

export function createOwnedBrowserAttemptDirectories(options: {
  platform: NodeJS.Platform;
  temporaryDirectory?: string;
  fileSystem?: BrowserAttemptDirectoryFileSystem;
}): BrowserAttemptDirectories {
  const temporaryDirectory = resolve(options.temporaryDirectory ?? tmpdir());
  const fileSystem = options.fileSystem ?? defaultDirectoryFileSystem;
  const attemptRoot = resolve(fileSystem.makeTemporaryDirectory(join(temporaryDirectory, attemptDirectoryPrefix)));
  if (dirname(attemptRoot) !== temporaryDirectory || !basename(attemptRoot).startsWith(attemptDirectoryPrefix)) {
    throw new Error("Browser attempt directory provider returned an unowned path");
  }
  try {
    fileSystem.setMode(attemptRoot, ownedDirectoryMode);
    const userDataDirectory = join(attemptRoot, "profile");
    fileSystem.makeDirectory(userDataDirectory, ownedDirectoryMode);
    fileSystem.setMode(userDataDirectory, ownedDirectoryMode);
    const xdgRuntimeDirectory = options.platform === "linux" ? join(attemptRoot, "xdg-runtime") : undefined;
    if (xdgRuntimeDirectory) {
      fileSystem.makeDirectory(xdgRuntimeDirectory, ownedDirectoryMode);
      fileSystem.setMode(xdgRuntimeDirectory, ownedDirectoryMode);
    }

    for (const path of [attemptRoot, userDataDirectory, ...(xdgRuntimeDirectory ? [xdgRuntimeDirectory] : [])]) {
      const inspection = fileSystem.inspect(path);
      if (!inspection.exists || !inspection.directory || inspection.symbolicLink) {
        throw new Error("Browser attempt directory ownership verification failed");
      }
      if (options.platform === "linux" && process.platform !== "win32" && inspection.mode !== ownedDirectoryMode) {
        throw new Error("Browser attempt directory mode verification failed");
      }
    }

    return { temporaryDirectory, attemptRoot, userDataDirectory, xdgRuntimeDirectory };
  } catch (error) {
    try {
      fileSystem.remove(attemptRoot);
    } catch (cleanupError) {
      throw new Error("Browser attempt directory initialization and cleanup failed", {
        cause: new AggregateError([error, cleanupError]),
      });
    }
    throw error;
  }
}

export async function removeOwnedBrowserAttemptDirectory(
  directories: BrowserAttemptDirectories,
  fileSystem: BrowserAttemptDirectoryFileSystem = defaultDirectoryFileSystem,
) {
  const temporaryDirectory = resolve(directories.temporaryDirectory);
  const attemptRoot = resolve(directories.attemptRoot);
  const expectedUserDataDirectory = join(attemptRoot, "profile");
  const expectedXdgRuntimeDirectory = join(attemptRoot, "xdg-runtime");
  if (
    dirname(attemptRoot) !== temporaryDirectory
    || !basename(attemptRoot).startsWith(attemptDirectoryPrefix)
    || resolve(directories.userDataDirectory) !== expectedUserDataDirectory
    || (directories.xdgRuntimeDirectory !== undefined && resolve(directories.xdgRuntimeDirectory) !== expectedXdgRuntimeDirectory)
  ) {
    throw new Error("Refusing to remove unexpected browser attempt directory");
  }
  const inspection = fileSystem.inspect(attemptRoot);
  if (!inspection.exists) return;
  if (!inspection.directory || inspection.symbolicLink) {
    throw new Error("Refusing to remove unexpected browser attempt directory");
  }

  for (let attempt = 0; attempt < 8; attempt += 1) {
    try {
      fileSystem.remove(attemptRoot);
      return;
    } catch (error) {
      if (attempt === 7) throw error;
      await delay(150 * (attempt + 1));
    }
  }
}

export async function terminateBrowserProcessTree(
  child: ChildProcessWithoutNullStreams,
  platform: NodeJS.Platform = process.platform,
) {
  if (platform !== "win32" && child.pid) {
    const processGroupId = child.pid;
    if (!signalProcessGroup(processGroupId, "SIGTERM")) return;
    if (await waitForProcessGroupExit(processGroupId, 3_000)) return;
    if (!signalProcessGroup(processGroupId, "SIGKILL")) return;
    if (await waitForProcessGroupExit(processGroupId, 2_000)) return;
    throw new Error("browser_process_group_termination_timeout");
  }
  if (processEnded(child)) return;
  if (platform === "win32" && child.pid) {
    spawnSync("taskkill.exe", ["/PID", String(child.pid), "/T", "/F"], {
      stdio: "ignore",
      windowsHide: true,
    });
    if (await waitForProcessExit(child, 3_000)) return;
  }

  if (!processEnded(child)) {
    try {
      child.kill("SIGKILL");
    } catch {
      // The bounded verification below decides whether cleanup succeeded.
    }
    if (!(await waitForProcessExit(child, 2_000))) throw new Error("browser_process_termination_timeout");
  }
}

const defaultAttemptDependencies: BrowserProcessAttemptDependencies = {
  createDirectories: (options) => createOwnedBrowserAttemptDirectories(options),
  spawnBrowser: (browserPath, args, options) => spawn(browserPath, [...args], options),
  connectToDevTools: (browserProcess, userDataDirectory, options) => (
    connectToBrowserDevTools(browserProcess, userDataDirectory, options)
  ),
  terminateProcessTree: (browserProcess, platform) => terminateBrowserProcessTree(browserProcess, platform),
  removeDirectories: (directories) => removeOwnedBrowserAttemptDirectory(directories),
};

const defaultDirectoryFileSystem: BrowserAttemptDirectoryFileSystem = {
  makeTemporaryDirectory(prefix) {
    return mkdtempSync(prefix);
  },
  makeDirectory(path, mode) {
    mkdirSync(path, { mode });
  },
  setMode(path, mode) {
    chmodSync(path, mode);
  },
  inspect(path) {
    try {
      const stats = lstatSync(path);
      return {
        exists: true,
        directory: stats.isDirectory(),
        symbolicLink: stats.isSymbolicLink(),
        mode: stats.mode & 0o777,
      };
    } catch (error) {
      if (isNodeError(error) && error.code === "ENOENT") {
        return { exists: false, directory: false, symbolicLink: false, mode: 0 };
      }
      throw error;
    }
  },
  remove(path) {
    rmSync(path, { recursive: true, force: true, maxRetries: 2, retryDelay: 100 });
  },
};

function failureDetails(error: unknown, signal: AbortSignal | undefined): BrowserStartupFailureDetails {
  if (error instanceof BrowserStartupError) return error.details;
  return {
    reason: signal?.aborted ? "aborted" : "unexpected_startup_error",
    processCode: null,
    processSignal: null,
    activePortFile: "unknown",
    activePortParse: "not_read",
    stderrParse: "not_reported",
    endpointSource: "none",
    webSocketAttempts: 0,
    stdoutTail: "",
    stderrTail: "",
  };
}

function connectedDiagnosticsFallback(): BrowserStartupFailureDetails {
  return {
    reason: "connected",
    processCode: null,
    processSignal: null,
    activePortFile: "unknown",
    activePortParse: "not_recorded",
    stderrParse: "not_recorded",
    endpointSource: "none",
    webSocketAttempts: 0,
    stdoutTail: "",
    stderrTail: "",
  };
}

function formatActiveLaunchDiagnostics(
  attemptNumber: number,
  priorSummaries: readonly BrowserLaunchAttemptSummary[],
  owner: BrowserProcessAttemptOwner,
  facts: BrowserLaunchFacts,
) {
  let activeDetails = connectedDiagnosticsFallback();
  try {
    activeDetails = owner.diagnosticDetails?.() ?? activeDetails;
  } catch {
    // A diagnostic callback is never allowed to hide the original browser failure.
  }
  const summaries = [
    ...priorSummaries,
    { attemptNumber, details: activeDetails },
  ];
  return [
    `browser_startup_attempts=${attemptNumber}`,
    ...summaries.map((summary) => (
      `attempt${summary.attemptNumber}={${formatBrowserStartupFailureDetails(summary.details)}}`
    )),
    `launchFacts={${formatBrowserLaunchFacts(facts)}}`,
  ].join("; ");
}

function appendRuntimeTail(previous: string, chunk: Uint8Array) {
  return `${previous}${Buffer.from(chunk).toString("utf8")}`.slice(-2_000);
}

function signalProcessGroup(processGroupId: number, signal: NodeJS.Signals) {
  try {
    process.kill(-processGroupId, signal);
    return true;
  } catch (error) {
    if (isNodeError(error) && error.code === "ESRCH") return false;
    throw error;
  }
}

async function waitForProcessGroupExit(processGroupId: number, timeoutMs: number) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (!processGroupExists(processGroupId)) return true;
    await delay(Math.min(25, Math.max(1, deadline - Date.now())));
  }
  return !processGroupExists(processGroupId);
}

function processGroupExists(processGroupId: number) {
  try {
    process.kill(-processGroupId, 0);
    return true;
  } catch (error) {
    if (isNodeError(error) && error.code === "ESRCH") return false;
    if (isNodeError(error) && error.code === "EPERM") return true;
    throw error;
  }
}

function processEnded(child: ChildProcessWithoutNullStreams) {
  return child.exitCode !== null || child.signalCode !== null;
}

function waitForProcessExit(child: ChildProcessWithoutNullStreams, timeoutMs: number) {
  if (processEnded(child)) return Promise.resolve(true);
  return new Promise<boolean>((resolveExit) => {
    let settled = false;
    const finish = (exited: boolean) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      child.off("exit", onExit);
      resolveExit(exited);
    };
    const onExit = () => finish(true);
    const timer = setTimeout(() => finish(processEnded(child)), timeoutMs);
    child.once("exit", onExit);
  });
}

function delay(milliseconds: number) {
  return new Promise<void>((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}

function isNodeError(error: unknown): error is NodeJS.ErrnoException {
  return error instanceof Error && "code" in error;
}
