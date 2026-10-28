import { delay } from "./browser-process-timing.mts";

export async function waitForMapCount(
  counts: Map<string, number>,
  pathname: string,
  expected: number,
  description: string,
  timeoutMs: number,
  assertHealthy: () => void,
) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    assertHealthy();
    if ((counts.get(pathname) || 0) === expected) return;
    await delay(20);
  }
  assertHealthy();
  throw new Error(`${description} did not become ${expected}; last count: ${counts.get(pathname) || 0}`);
}

// Initial-read readiness only. Exact one-shot/cycle checks use waitForMapCount.
export async function waitForMinimumMapCount(counts: Map<string, number>, pathname: string, minimum: number, description: string, timeoutMs: number, assertHealthy: () => void) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    assertHealthy();
    if ((counts.get(pathname) || 0) >= minimum) return;
    await delay(20);
  }
  assertHealthy();
  throw new Error(`${description} did not reach ${minimum}; last count: ${counts.get(pathname) || 0}`);
}
