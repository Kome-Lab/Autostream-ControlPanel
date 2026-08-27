import type { OneTimeSecretRuntime } from "../../src/lib/foundation/secrets/contracts.ts";

type ScheduledTimer = Readonly<{
  callback: () => void;
  dueAtMonotonicMs: number;
  order: number;
}>;

export class OneTimeSecretFakeClock implements OneTimeSecretRuntime {
  #epochNowMs: number;
  #monotonicNowMs: number;
  #nextHandle = 1;
  #nextOrder = 1;
  #timers = new Map<number, ScheduledTimer>();

  constructor(epochNowMs = 1_000_000, monotonicNowMs = 10_000) {
    this.#epochNowMs = epochNowMs;
    this.#monotonicNowMs = monotonicNowMs;
  }

  readonly epochNowMs = () => this.#epochNowMs;

  readonly monotonicNowMs = () => this.#monotonicNowMs;

  readonly schedule = (callback: () => void, delayMs: number): unknown => {
    if (!Number.isSafeInteger(delayMs) || delayMs < 0) {
      throw new RangeError("fake clock delay must be a non-negative safe integer");
    }
    const dueAtMonotonicMs = this.#monotonicNowMs + delayMs;
    if (!Number.isSafeInteger(dueAtMonotonicMs)) {
      throw new RangeError("fake clock deadline overflow");
    }
    const handle = this.#nextHandle;
    this.#nextHandle += 1;
    this.#timers.set(handle, Object.freeze({
      callback,
      dueAtMonotonicMs,
      order: this.#nextOrder,
    }));
    this.#nextOrder += 1;
    return handle;
  };

  readonly cancel = (handle: unknown): void => {
    if (typeof handle === "number") this.#timers.delete(handle);
  };

  advanceEpochBy(milliseconds: number) {
    assertAdvance(milliseconds);
    const target = this.#epochNowMs + milliseconds;
    if (!Number.isSafeInteger(target)) throw new RangeError("fake epoch overflow");
    this.#epochNowMs = target;
  }

  advanceMonotonicBy(milliseconds: number) {
    assertAdvance(milliseconds);
    const target = this.#monotonicNowMs + milliseconds;
    if (!Number.isSafeInteger(target)) throw new RangeError("fake monotonic overflow");

    while (true) {
      const next = this.#nextTimerAtOrBefore(target);
      if (!next) break;
      this.#monotonicNowMs = next.timer.dueAtMonotonicMs;
      this.#timers.delete(next.handle);
      next.timer.callback();
    }
    this.#monotonicNowMs = target;
  }

  pendingTimerCount() {
    return this.#timers.size;
  }

  pendingDeadlines() {
    return [...this.#timers.values()]
      .sort(compareTimers)
      .map((timer) => timer.dueAtMonotonicMs);
  }

  #nextTimerAtOrBefore(target: number) {
    let selected: Readonly<{ handle: number; timer: ScheduledTimer }> | undefined;
    for (const [handle, timer] of this.#timers) {
      if (timer.dueAtMonotonicMs > target) continue;
      if (!selected || compareTimers(timer, selected.timer) < 0) {
        selected = Object.freeze({ handle, timer });
      }
    }
    return selected;
  }
}

function assertAdvance(milliseconds: number) {
  if (!Number.isSafeInteger(milliseconds) || milliseconds < 0) {
    throw new RangeError("fake clock advance must be a non-negative safe integer");
  }
}

function compareTimers(left: ScheduledTimer, right: ScheduledTimer) {
  return left.dueAtMonotonicMs - right.dueAtMonotonicMs || left.order - right.order;
}
