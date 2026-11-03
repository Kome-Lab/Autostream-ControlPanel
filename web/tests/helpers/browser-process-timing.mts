import { spawnSync, type ChildProcessWithoutNullStreams } from "node:child_process";


export function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error));
}

export function delay(milliseconds: number) {
  return new Promise<void>((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}

export async function withTimeout<T>(promise: Promise<T>, milliseconds: number, message: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, rejectTimeout) => {
        timer = setTimeout(() => rejectTimeout(new Error(message)), milliseconds);
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

export async function terminateChildProcess(child: ChildProcessWithoutNullStreams) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  if (process.platform === "win32" && child.pid) {
    spawnSync("taskkill.exe", ["/PID", String(child.pid), "/T", "/F"], {
      stdio: "ignore",
      windowsHide: true,
    });
    await Promise.race([waitForChildExit(child), delay(3_000)]);
    if (child.exitCode !== null || child.signalCode !== null) return;
  }
  child.kill("SIGTERM");
  await Promise.race([waitForChildExit(child), delay(3_000)]);
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGKILL");
  await Promise.race([waitForChildExit(child), delay(2_000)]);
}

function waitForChildExit(process: ChildProcessWithoutNullStreams) {
  if (process.exitCode !== null || process.signalCode !== null) return Promise.resolve();
  return new Promise<void>((resolveExit) => process.once("exit", () => resolveExit()));
}

