import { planOneTimeSecretTiming } from "@/lib/foundation/secrets/timing-policy";
import type {
  OneTimeSecretBundle,
  OneTimeSecretClearReason,
  OneTimeSecretCopyResult,
  OneTimeSecretLifecycleOwner,
  OneTimeSecretPhase,
  OneTimeSecretRuntime,
  OneTimeSecretSnapshot,
  OneTimeSecretTimingInput,
  OneTimeSecretTimingPlan,
} from "@/lib/foundation/secrets/contracts";

type ActiveSource<T> = Readonly<{
  generation: number;
  value: T;
}>;

type TimerSlot = Readonly<{
  generation: number;
  handle: unknown;
}>;

type RuntimeFunctions = Readonly<{
  epochNowMs: () => number;
  monotonicNowMs: () => number;
  schedule: (callback: () => void, delayMs: number) => unknown;
  cancel: (handle: unknown) => void;
}>;

const initialSnapshot: OneTimeSecretSnapshot = Object.freeze({
  generation: 0,
  phase: "cleared",
  warningActive: false,
  copyStatus: "idle",
});

export function createOneTimeSecretLifecycleOwner<T>(
  runtime: OneTimeSecretRuntime,
): OneTimeSecretLifecycleOwner<T> {
  const runtimeFunctions = readRuntimeFunctions(runtime);
  const listeners = new Set<() => void>();
  let active: ActiveSource<T> | undefined;
  let snapshot = initialSnapshot;
  let warningTimer: TimerSlot | undefined;
  let expiryTimer: TimerSlot | undefined;
  let disposed = false;
  let notifying = false;

  const emit = () => {
    notifying = true;
    try {
      for (const listener of [...listeners]) {
        try {
          listener();
        } catch {
          // A listener cannot prevent source cleanup or other safe notifications.
        }
      }
    } finally {
      notifying = false;
    }
  };

  const cancelTimer = (slot: TimerSlot | undefined) => {
    if (!slot || !runtimeFunctions) return;
    try {
      runtimeFunctions.cancel(slot.handle);
    } catch {
      // Cleanup remains fail-closed even when a scheduler rejects cancellation.
    }
  };

  const cancelAllTimers = () => {
    const warning = warningTimer;
    const expiry = expiryTimer;
    warningTimer = undefined;
    expiryTimer = undefined;
    cancelTimer(warning);
    cancelTimer(expiry);
  };

  const clearActive = (
    clearReason: OneTimeSecretClearReason,
    phase: "acknowledged" | "cleared" = "cleared",
  ) => {
    if (!active) return false;
    const generation = active.generation;
    cancelAllTimers();
    active = undefined;
    snapshot = terminalSnapshot(generation, phase, clearReason);
    emit();
    return true;
  };

  const markInvalid = () => {
    if (active) {
      clearActive("invalid");
      return false;
    }
    cancelAllTimers();
    if (snapshot.phase === "cleared" && snapshot.clearReason === "invalid") return false;
    snapshot = terminalSnapshot(snapshot.generation, "cleared", "invalid");
    emit();
    return false;
  };

  const scheduleLifecycle = (generation: number, plan: OneTimeSecretTimingPlan) => {
    if (!runtimeFunctions) return false;
    try {
      if (plan.warningAtMonotonicMs > plan.expiresAtMonotonicMs - plan.effectiveLifetimeMs) {
        warningTimer = Object.freeze({
          generation,
          handle: runtimeFunctions.schedule(() => {
            const scheduled = warningTimer;
            if (!scheduled || scheduled.generation !== generation) return;
            warningTimer = undefined;
            if (active?.generation !== generation) return;
            snapshot = Object.freeze({ ...snapshot, warningActive: true });
            emit();
          }, plan.warningAtMonotonicMs - (plan.expiresAtMonotonicMs - plan.effectiveLifetimeMs)),
        });
      }
      expiryTimer = Object.freeze({
        generation,
        handle: runtimeFunctions.schedule(() => {
          const scheduled = expiryTimer;
          if (!scheduled || scheduled.generation !== generation) return;
          expiryTimer = undefined;
          if (active?.generation !== generation) return;
          clearActive("expired");
        }, plan.effectiveLifetimeMs),
      });
      return true;
    } catch {
      cancelAllTimers();
      return false;
    }
  };

  const replace = <const BackendExpiry extends number = number>(
    bundle: OneTimeSecretBundle<T, BackendExpiry>,
  ) => {
    if (disposed || notifying) return false;

    if (active) clearActive("replaced");

    let value: T;
    let backendExpiresAtEpochMs: number | undefined;
    let initialVisibility: "concealed" | "revealed";
    let adoptedAtEpochMs: number;
    let adoptedAtMonotonicMs: number;
    try {
      value = bundle.value;
      backendExpiresAtEpochMs = bundle.backendExpiresAtEpochMs;
      initialVisibility = bundle.initialVisibility ?? "concealed";
      if (initialVisibility !== "concealed" && initialVisibility !== "revealed") return markInvalid();
      if (!runtimeFunctions) return markInvalid();
      adoptedAtEpochMs = runtimeFunctions.epochNowMs();
      adoptedAtMonotonicMs = runtimeFunctions.monotonicNowMs();
    } catch {
      return markInvalid();
    }

    const plan = runtimeTimingPlan({
      adoptedAtEpochMs,
      adoptedAtMonotonicMs,
      ...(backendExpiresAtEpochMs === undefined ? {} : { backendExpiresAtEpochMs }),
    });
    if (!plan) return markInvalid();
    const generation = snapshot.generation + 1;
    if (!Number.isSafeInteger(generation)) return markInvalid();

    active = Object.freeze({ generation, value });
    snapshot = Object.freeze({
      generation,
      phase: initialVisibility,
      warningActive: plan.warningAtMonotonicMs === adoptedAtMonotonicMs,
      copyStatus: "idle",
    });
    if (!scheduleLifecycle(generation, plan)) {
      clearActive("invalid");
      return false;
    }
    emit();
    return true;
  };

  const reveal = () => {
    if (notifying || !active || snapshot.phase !== "concealed") return false;
    snapshot = activeSnapshot(snapshot, "revealed", "idle");
    emit();
    return true;
  };

  const conceal = () => {
    if (notifying || !active || (snapshot.phase !== "revealed" && snapshot.phase !== "copied")) return false;
    snapshot = activeSnapshot(snapshot, "concealed", "idle");
    emit();
    return true;
  };

  const finishCopy = (generation: number, succeeded: boolean): OneTimeSecretCopyResult => {
    if (
      !active
      || active.generation !== generation
      || (snapshot.phase !== "revealed" && snapshot.phase !== "copied")
    ) {
      return "stale-generation";
    }
    snapshot = activeSnapshot(
      snapshot,
      succeeded ? "copied" : "revealed",
      succeeded ? "copied" : "failed",
    );
    emit();
    return succeeded ? "copied" : "failed";
  };

  const copyWith = (
    writer: (value: T) => void | Promise<void>,
  ): Promise<OneTimeSecretCopyResult> => {
    if (
      notifying
      || !active
      || (snapshot.phase !== "revealed" && snapshot.phase !== "copied")
      || typeof writer !== "function"
    ) {
      return Promise.resolve("unavailable");
    }
    const generation = active.generation;
    let completion: void | Promise<void>;
    try {
      completion = writer(active.value);
    } catch {
      return Promise.resolve(finishCopy(generation, false));
    }
    try {
      return Promise.resolve(completion).then(
        () => finishCopy(generation, true),
        () => finishCopy(generation, false),
      );
    } catch {
      return Promise.resolve(finishCopy(generation, false));
    }
  };

  const terminal = (
    reason: OneTimeSecretClearReason,
    phase: "acknowledged" | "cleared" = "cleared",
  ) => {
    if (notifying) return false;
    return clearActive(reason, phase);
  };

  return Object.freeze({
    getSnapshot: () => snapshot,
    readRevealedValue: () => active && (snapshot.phase === "revealed" || snapshot.phase === "copied")
      ? active.value
      : undefined,
    subscribe: (listener: () => void) => {
      if (disposed || typeof listener !== "function") return () => {};
      listeners.add(listener);
      let subscribed = true;
      return () => {
        if (!subscribed) return;
        subscribed = false;
        listeners.delete(listener);
      };
    },
    replace,
    reveal,
    conceal,
    copyWith,
    acknowledge: () => terminal("acknowledged", "acknowledged"),
    dismiss: () => terminal("dismissed"),
    clearForNavigation: () => terminal("navigation"),
    clearForSessionLoss: () => terminal("session-lost"),
    dispose: () => {
      if (disposed || notifying) return false;
      disposed = true;
      const cleared = clearActive("unmounted");
      listeners.clear();
      return cleared;
    },
  });
}

function readRuntimeFunctions(runtime: OneTimeSecretRuntime): RuntimeFunctions | undefined {
  try {
    const epochNowMs = runtime.epochNowMs;
    const monotonicNowMs = runtime.monotonicNowMs;
    const schedule = runtime.schedule;
    const cancel = runtime.cancel;
    if (
      typeof epochNowMs !== "function"
      || typeof monotonicNowMs !== "function"
      || typeof schedule !== "function"
      || typeof cancel !== "function"
    ) {
      return undefined;
    }
    return Object.freeze({
      epochNowMs: () => epochNowMs.call(runtime),
      monotonicNowMs: () => monotonicNowMs.call(runtime),
      schedule: (callback: () => void, delayMs: number) => schedule.call(runtime, callback, delayMs),
      cancel: (handle: unknown) => cancel.call(runtime, handle),
    });
  } catch {
    return undefined;
  }
}

function activeSnapshot(
  snapshot: OneTimeSecretSnapshot,
  phase: Extract<OneTimeSecretPhase, "concealed" | "revealed" | "copied">,
  copyStatus: OneTimeSecretSnapshot["copyStatus"],
): OneTimeSecretSnapshot {
  return Object.freeze({
    generation: snapshot.generation,
    phase,
    warningActive: snapshot.warningActive,
    copyStatus,
  });
}

function terminalSnapshot(
  generation: number,
  phase: "acknowledged" | "cleared",
  clearReason: OneTimeSecretClearReason,
): OneTimeSecretSnapshot {
  return Object.freeze({
    generation,
    phase,
    warningActive: false,
    copyStatus: "idle",
    clearReason,
  });
}

function runtimeTimingPlan(
  input: OneTimeSecretTimingInput,
): OneTimeSecretTimingPlan | undefined {
  try {
    return planOneTimeSecretTiming(input);
  } catch {
    return undefined;
  }
}
