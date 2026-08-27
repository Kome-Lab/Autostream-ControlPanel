type NonNegativeIntegerLiteral<Value extends number> = number extends Value
  ? number
  : `${Value}` extends `-${string}`
    ? never
    : `${Value}` extends `${bigint}`
      ? Value
      : never;

export type OneTimeSecretPhase =
  | "concealed"
  | "revealed"
  | "copied"
  | "acknowledged"
  | "cleared";

export type OneTimeSecretClearReason =
  | "acknowledged"
  | "dismissed"
  | "expired"
  | "navigation"
  | "unmounted"
  | "session-lost"
  | "replaced"
  | "invalid";

export type OneTimeSecretCopyStatus = "idle" | "copied" | "failed";

export type OneTimeSecretSnapshot = Readonly<{
  generation: number;
  phase: OneTimeSecretPhase;
  warningActive: boolean;
  copyStatus: OneTimeSecretCopyStatus;
  clearReason?: OneTimeSecretClearReason;
}>;

export type OneTimeSecretTimingInput<BackendExpiry extends number = number> = Readonly<{
  adoptedAtEpochMs: number;
  adoptedAtMonotonicMs: number;
  backendExpiresAtEpochMs?: NonNegativeIntegerLiteral<BackendExpiry>;
}>;

export type OneTimeSecretTimingPlan = Readonly<{
  effectiveLifetimeMs: number;
  warningAtMonotonicMs: number;
  expiresAtMonotonicMs: number;
}>;

export type OneTimeSecretRuntime = Readonly<{
  epochNowMs: () => number;
  monotonicNowMs: () => number;
  schedule: (callback: () => void, delayMs: number) => unknown;
  cancel: (handle: unknown) => void;
}>;

export type OneTimeSecretBundle<T, BackendExpiry extends number = number> = Readonly<{
  value: T;
  backendExpiresAtEpochMs?: NonNegativeIntegerLiteral<BackendExpiry>;
  initialVisibility?: "concealed" | "revealed";
}>;

export type OneTimeSecretCopyResult =
  | "copied"
  | "failed"
  | "unavailable"
  | "stale-generation";

export type OneTimeSecretLifecycleOwner<T> = Readonly<{
  getSnapshot: () => OneTimeSecretSnapshot;
  readRevealedValue: () => T | undefined;
  subscribe: (listener: () => void) => () => void;
  replace: <const BackendExpiry extends number = number>(
    bundle: OneTimeSecretBundle<T, BackendExpiry>,
  ) => boolean;
  reveal: () => boolean;
  conceal: () => boolean;
  copyWith: (
    writer: (value: T) => void | Promise<void>,
  ) => Promise<OneTimeSecretCopyResult>;
  acknowledge: () => boolean;
  dismiss: () => boolean;
  clearForNavigation: () => boolean;
  clearForSessionLoss: () => boolean;
  dispose: () => boolean;
}>;

// The owner can remove only its own opaque reference. JavaScript cannot promise
// physical zeroization, and callers or explicit copy writers may retain theirs.
