import { spawnSync } from "node:child_process";
import { accessSync, constants, realpathSync, statfsSync, statSync } from "node:fs";
import { basename, delimiter, join } from "node:path";

export const browserAttemptTimeoutMs = 45_000;
export const browserLaunchBudgetMs = 90_000;

const diagnosticTextLimit = 2_000;
const browserVersionLimit = 160;

const commonBrowserArguments = [
  "--headless",
  "--disable-background-networking",
  "--disable-component-update",
  "--disable-breakpad",
  "--disable-crash-reporter",
  "--disable-default-apps",
  "--disable-gpu",
  "--no-default-browser-check",
  "--no-first-run",
] as const;

const linuxBrowserArguments = [
  "--password-store=basic",
  "--disable-dev-shm-usage",
  "--disable-hang-monitor",
  "--disable-popup-blocking",
  "--disable-prompt-on-repost",
  "--disable-sync",
  "--metrics-recording-only",
  "--mute-audio",
  "--window-size=1280,1024",
] as const;

const secondAttemptDiagnosticArguments = ["--enable-logging=stderr", "--v=1"] as const;

export type XdgRuntimeDirectoryClassification = "absent" | "valid-directory" | "invalid";
export type DbusSessionBusAddressClassification = "absent" | "valid-unix" | "valid-tcp" | "invalid";

export type BrowserLaunchFacts = Readonly<{
  platform: NodeJS.Platform;
  browserExecutableBasename: string;
  browserExecutableRealpath: string;
  browserVersion: string;
  xdgRuntimeDirectory: XdgRuntimeDirectoryClassification;
  dbusSessionBusAddress: DbusSessionBusAddressClassification;
  devShm: Readonly<{
    available: boolean;
    totalBytes: number | null;
    availableBytes: number | null;
  }>;
  dbusRunSessionAvailable: boolean;
}>;

export type BrowserLaunchProfile = Readonly<{
  args: readonly string[];
  environment: NodeJS.ProcessEnv;
}>;

export type BrowserLaunchProfileOptions = {
  platform: NodeJS.Platform;
  attemptNumber: 1 | 2;
  userDataDirectory: string;
  xdgRuntimeDirectory?: string;
  parentEnvironment: Readonly<NodeJS.ProcessEnv>;
};

export type BrowserLaunchFactOptions = {
  platform?: NodeJS.Platform;
  environment?: Readonly<NodeJS.ProcessEnv>;
  inspectDirectory?: (path: string) => boolean;
  resolveExecutableRealpath?: (path: string) => string;
  readBrowserVersion?: (
    path: string,
    platform: NodeJS.Platform,
    environment: Readonly<NodeJS.ProcessEnv>,
  ) => string | null;
  readDevShm?: () => { totalBytes: number; availableBytes: number } | null;
  executableAvailable?: (name: string, environment: Readonly<NodeJS.ProcessEnv>, platform: NodeJS.Platform) => boolean;
};

export function buildBrowserLaunchProfile(options: BrowserLaunchProfileOptions): BrowserLaunchProfile {
  if (options.attemptNumber !== 1 && options.attemptNumber !== 2) {
    throw new Error("Browser startup attempt must be 1 or 2");
  }
  if (!options.userDataDirectory) throw new Error("Browser user-data directory is required");
  if (options.platform === "linux" && !options.xdgRuntimeDirectory) {
    throw new Error("Linux browser startup requires an owned XDG runtime directory");
  }

  const args = [
    ...commonBrowserArguments,
    ...(options.platform === "linux" ? linuxBrowserArguments : []),
    ...(options.attemptNumber === 2 ? secondAttemptDiagnosticArguments : []),
    "--remote-debugging-address=127.0.0.1",
    "--remote-debugging-port=0",
    `--user-data-dir=${options.userDataDirectory}`,
    "about:blank",
  ];
  assertUniqueArguments(args);
  if (args.includes("--no-sandbox") || args.includes("--disable-setuid-sandbox")) {
    throw new Error("Sandbox-disabling browser flags are forbidden");
  }

  return {
    args,
    environment: buildBrowserChildEnvironment(options),
  };
}

export function classifyDbusSessionBusAddress(value: string | undefined): DbusSessionBusAddressClassification {
  if (value === undefined) return "absent";
  if (value.length === 0 || value.length > 2_048 || /[\s\u0000-\u001f\u007f;]/.test(value)) return "invalid";

  const separator = value.indexOf(":");
  if (separator < 1 || separator === value.length - 1) return "invalid";
  const transport = value.slice(0, separator);
  if (transport !== "unix" && transport !== "tcp") return "invalid";
  const fields = parseDbusFields(value.slice(separator + 1));
  if (!fields) return "invalid";

  if (transport === "unix") {
    const unixKeys = ["path", "abstract", "dir", "tmpdir", "runtime"].filter((key) => fields.has(key));
    if (unixKeys.length !== 1) return "invalid";
    if (fields.has("runtime") && !/^(?:yes|no)$/i.test(fields.get("runtime") || "")) return "invalid";
    return "valid-unix";
  }

  const host = fields.get("host");
  const portText = fields.get("port");
  if (!host || !portText || !/^\d+$/.test(portText)) return "invalid";
  const port = Number(portText);
  if (!Number.isSafeInteger(port) || port < 1 || port > 65_535) return "invalid";
  const family = fields.get("family");
  if (family && family !== "ipv4" && family !== "ipv6") return "invalid";
  return "valid-tcp";
}

export function classifyXdgRuntimeDirectory(
  value: string | undefined,
  inspectDirectory: (path: string) => boolean = defaultInspectDirectory,
): XdgRuntimeDirectoryClassification {
  if (value === undefined) return "absent";
  if (value.length === 0 || /[\u0000\r\n]/.test(value)) return "invalid";
  try {
    return inspectDirectory(value) ? "valid-directory" : "invalid";
  } catch {
    return "invalid";
  }
}

export function collectBrowserLaunchFacts(
  browserPath: string,
  options: BrowserLaunchFactOptions = {},
): BrowserLaunchFacts {
  const platform = options.platform ?? process.platform;
  const environment = options.environment ?? process.env;
  const inspectDirectory = options.inspectDirectory ?? defaultInspectDirectory;
  const resolveExecutableRealpath = options.resolveExecutableRealpath ?? defaultResolveExecutableRealpath;
  const readBrowserVersion = options.readBrowserVersion ?? defaultReadBrowserVersion;
  const readDevShm = options.readDevShm ?? defaultReadDevShm;
  const executableAvailable = options.executableAvailable ?? defaultExecutableAvailable;

  let executableRealpath = "unavailable";
  try {
    executableRealpath = boundedSingleLine(resolveExecutableRealpath(browserPath), 512, true) || "unavailable";
  } catch {
    // Resolution failure remains a bounded classification instead of exposing an arbitrary filesystem error.
  }

  let versionOutput: string | null = null;
  try {
    versionOutput = readBrowserVersion(browserPath, platform, environment);
  } catch {
    // Version probing is diagnostic-only and must not make a valid configured browser unusable.
  }
  const browserVersion = boundedSingleLine(versionOutput || "unavailable", browserVersionLimit) || "unavailable";

  let devShm: BrowserLaunchFacts["devShm"] = { available: false, totalBytes: null, availableBytes: null };
  if (platform === "linux") {
    try {
      const sizes = readDevShm();
      if (sizes && validByteCount(sizes.totalBytes) && validByteCount(sizes.availableBytes)) {
        devShm = {
          available: true,
          totalBytes: sizes.totalBytes,
          availableBytes: Math.min(sizes.availableBytes, sizes.totalBytes),
        };
      }
    } catch {
      // /dev/shm availability is represented explicitly below.
    }
  }

  let dbusRunSessionAvailable = false;
  if (platform === "linux") {
    try {
      dbusRunSessionAvailable = executableAvailable("dbus-run-session", environment, platform);
    } catch {
      // Availability is a boolean fact; discovery errors are not emitted.
    }
  }

  return {
    platform,
    browserExecutableBasename: boundedSingleLine(basename(browserPath), 160, true) || "unavailable",
    browserExecutableRealpath: executableRealpath,
    browserVersion,
    xdgRuntimeDirectory: classifyXdgRuntimeDirectory(environment.XDG_RUNTIME_DIR, inspectDirectory),
    dbusSessionBusAddress: classifyDbusSessionBusAddress(environment.DBUS_SESSION_BUS_ADDRESS),
    devShm,
    dbusRunSessionAvailable,
  };
}

export function formatBrowserLaunchFacts(facts: BrowserLaunchFacts) {
  return [
    `platform=${facts.platform}`,
    `browserExecutable=${boundedSingleLine(facts.browserExecutableBasename, 160, true) || "unavailable"}`,
    `browserRealpath=${JSON.stringify(boundedSingleLine(facts.browserExecutableRealpath, 512, true) || "unavailable")}`,
    `browserVersion=${JSON.stringify(boundedSingleLine(facts.browserVersion, browserVersionLimit) || "unavailable")}`,
    `xdgRuntime=${facts.xdgRuntimeDirectory}`,
    `dbusSession=${facts.dbusSessionBusAddress}`,
    `devShmAvailable=${facts.devShm.available ? "yes" : "no"}`,
    `devShmTotalBytes=${facts.devShm.totalBytes ?? "null"}`,
    `devShmAvailableBytes=${facts.devShm.availableBytes ?? "null"}`,
    `dbusRunSessionAvailable=${facts.dbusRunSessionAvailable ? "yes" : "no"}`,
  ].join("; ");
}

export function sanitizeBrowserDiagnosticText(value: string, limit = diagnosticTextLimit, preserveFilesystemPaths = false) {
  const boundedLimit = Number.isSafeInteger(limit) && limit > 0 ? Math.min(limit, diagnosticTextLimit) : diagnosticTextLimit;
  let sanitized = value
    .replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/(\bAuthorization\s*:\s*)[^\r\n]+/gi, "$1<redacted>")
    .replace(/(\b(?:Cookie|Set-Cookie)\s*:\s*)[^\r\n]+/gi, "$1<redacted>")
    .replace(/(\bDBUS_SESSION_BUS_ADDRESS\s*=\s*)[^\s\r\n]+/gi, "$1<redacted>")
    .replace(/\b(?:unix|tcp):[^\s"']+/gi, "<dbus-address-redacted>")
    .replace(/\bBearer\s+\S+/gi, "Bearer <redacted>")
    .replace(/([?&](?:access_token|api_key|key|password|secret|token)=)[^&\s]+/gi, "$1<redacted>")
    .replace(/\b((?:access[_-]?token|api[_-]?key|password|secret|token)\s*=\s*)[^\s;,]+/gi, "$1<redacted>")
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, "�");
  if (!preserveFilesystemPaths) {
    sanitized = sanitized
      .replace(/(["'])(?:[A-Za-z]:\\|\/)[^"'\r\n]*\1/g, "$1<path-redacted>$1")
      .replace(/\b[A-Za-z]:\\[^\r\n"']+/g, "<path-redacted>")
      .replace(/(^|[\s=(])\/(?!\/)[^\s"']+/gm, "$1<path-redacted>");
  }
  return sanitized.slice(-boundedLimit);
}

function buildBrowserChildEnvironment(options: BrowserLaunchProfileOptions): NodeJS.ProcessEnv {
  const environment = { ...options.parentEnvironment };
  if (options.platform === "linux") environment.XDG_RUNTIME_DIR = options.xdgRuntimeDirectory;

  const dbusClassification = classifyDbusSessionBusAddress(options.parentEnvironment.DBUS_SESSION_BUS_ADDRESS);
  if (dbusClassification !== "valid-unix" && dbusClassification !== "valid-tcp") {
    delete environment.DBUS_SESSION_BUS_ADDRESS;
  }
  return environment;
}

function parseDbusFields(value: string) {
  const fields = new Map<string, string>();
  for (const entry of value.split(",")) {
    const equals = entry.indexOf("=");
    if (equals < 1 || equals === entry.length - 1) return null;
    const key = entry.slice(0, equals);
    const fieldValue = entry.slice(equals + 1);
    if (!/^[A-Za-z][A-Za-z0-9_-]*$/.test(key) || !isValidDbusEscapedValue(fieldValue) || fields.has(key)) return null;
    fields.set(key, fieldValue);
  }
  return fields.size > 0 ? fields : null;
}

export function isValidDbusEscapedValue(encoded: string): boolean {
  if (encoded.length === 0) return false;
  for (let index = 0; index < encoded.length; index += 1) {
    const byte = encoded.charCodeAt(index);
    if (byte === 0x25) {
      if (
        index + 2 >= encoded.length
        || !isAsciiHexDigit(encoded.charCodeAt(index + 1))
        || !isAsciiHexDigit(encoded.charCodeAt(index + 2))
      ) return false;
      index += 2;
      continue;
    }
    if (
      byte === 0x2a
      || byte === 0x2d
      || byte === 0x2e
      || byte === 0x2f
      || (byte >= 0x30 && byte <= 0x39)
      || (byte >= 0x41 && byte <= 0x5a)
      || byte === 0x5f
      || (byte >= 0x61 && byte <= 0x7a)
    ) continue;
    return false;
  }
  return true;
}

function isAsciiHexDigit(byte: number) {
  return (
    (byte >= 0x30 && byte <= 0x39)
    || (byte >= 0x41 && byte <= 0x46)
    || (byte >= 0x61 && byte <= 0x66)
  );
}

function assertUniqueArguments(args: readonly string[]) {
  if (new Set(args).size !== args.length) throw new Error("Duplicate browser launch arguments are forbidden");
}

function defaultInspectDirectory(path: string) {
  return statSync(path).isDirectory();
}

function defaultResolveExecutableRealpath(path: string) {
  return realpathSync.native(path);
}

function defaultReadBrowserVersion(
  path: string,
  platform: NodeJS.Platform,
  environment: Readonly<NodeJS.ProcessEnv>,
) {
  if (platform === "win32") return null;
  const probeEnvironment = { ...environment };
  const dbusClassification = classifyDbusSessionBusAddress(probeEnvironment.DBUS_SESSION_BUS_ADDRESS);
  if (dbusClassification !== "valid-unix" && dbusClassification !== "valid-tcp") {
    delete probeEnvironment.DBUS_SESSION_BUS_ADDRESS;
  }
  if (classifyXdgRuntimeDirectory(probeEnvironment.XDG_RUNTIME_DIR) === "invalid") {
    delete probeEnvironment.XDG_RUNTIME_DIR;
  }
  const result = spawnSync(path, ["--version"], {
    encoding: "utf8",
    env: probeEnvironment,
    killSignal: "SIGKILL",
    maxBuffer: 4_096,
    stdio: ["ignore", "pipe", "pipe"],
    timeout: 3_000,
    windowsHide: true,
  });
  if (result.error || result.status !== 0) return null;
  return result.stdout || result.stderr || null;
}

function defaultReadDevShm() {
  const stats = statfsSync("/dev/shm", { bigint: true });
  return {
    totalBytes: boundedBigIntBytes(stats.bsize * stats.blocks),
    availableBytes: boundedBigIntBytes(stats.bsize * stats.bavail),
  };
}

function defaultExecutableAvailable(name: string, environment: Readonly<NodeJS.ProcessEnv>, platform: NodeJS.Platform) {
  const pathValue = environment.PATH ?? environment.Path;
  if (!pathValue) return false;
  const pathDelimiter = platform === "win32" ? ";" : delimiter;
  for (const directory of pathValue.split(pathDelimiter)) {
    if (!directory) continue;
    const candidate = join(directory, name);
    try {
      accessSync(candidate, constants.X_OK);
      return true;
    } catch {
      // Continue through the bounded PATH inventory without logging entries.
    }
  }
  return false;
}

function boundedBigIntBytes(value: bigint) {
  if (value < 0n) return 0;
  const maximum = BigInt(Number.MAX_SAFE_INTEGER);
  return Number(value > maximum ? maximum : value);
}

function validByteCount(value: number) {
  return Number.isSafeInteger(value) && value >= 0;
}

function boundedSingleLine(value: string, limit: number, preserveFilesystemPaths = false) {
  return sanitizeBrowserDiagnosticText(value, Math.min(limit, diagnosticTextLimit), preserveFilesystemPaths)
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, limit);
}
