import { createElement } from "react";

import { OneTimeSecretReveal } from "../src/components/foundation/secrets/one-time-secret-reveal";
import type {
  OneTimeSecretBundle,
  OneTimeSecretClearReason,
  OneTimeSecretLifecycleOwner,
  OneTimeSecretPhase,
  OneTimeSecretRuntime,
  OneTimeSecretSnapshot,
} from "../src/lib/foundation/secrets/contracts.ts";
import { planOneTimeSecretTiming } from "../src/lib/foundation/secrets/timing-policy";

export const validPhase: OneTimeSecretPhase = "concealed";
export const validReason: OneTimeSecretClearReason = "expired";
export const validSnapshot: OneTimeSecretSnapshot = {
  generation: 1,
  phase: "revealed",
  warningActive: false,
  copyStatus: "idle",
};
export const validRuntime: OneTimeSecretRuntime = {
  epochNowMs: () => 1_000,
  monotonicNowMs: () => 2_000,
  schedule: () => 1,
  cancel: () => {},
};
export const validBundle: OneTimeSecretBundle<string> = {
  value: "opaque source",
  backendExpiresAtEpochMs: 2_000,
  initialVisibility: "concealed",
};
export const validPlan = planOneTimeSecretTiming({
  adoptedAtEpochMs: 1_000,
  adoptedAtMonotonicMs: 2_000,
  backendExpiresAtEpochMs: 2_000,
});

declare const owner: OneTimeSecretLifecycleOwner<string>;
owner.replace(validBundle);
owner.copyWith((value) => { void value; });

const validComponentProps = {
  snapshot: validSnapshot,
  translate: (key: string) => key,
  renderRevealedContent: () => createElement("code", null, "opaque source"),
  canCopy: true,
  onRevealIntent: () => {},
  onConcealIntent: () => {},
  onCopyIntent: () => {},
  onAcknowledgeIntent: () => {},
  onDismissIntent: () => {},
  onUnmountIntent: () => {},
} as const;
export const validComponent = createElement(OneTimeSecretReveal, validComponentProps);

// @ts-expect-error -- lifecycle phases are a closed union
export const invalidPhase: OneTimeSecretPhase = "gone";
// @ts-expect-error -- clear reasons are a closed union
export const invalidReason: OneTimeSecretClearReason = "closed";
// @ts-expect-error -- safe snapshots cannot carry a source value
export const snapshotWithSecret: OneTimeSecretSnapshot = { ...validSnapshot, secret: "opaque source" };
// @ts-expect-error -- safe snapshots cannot carry a generic value
export const snapshotWithValue: OneTimeSecretSnapshot = { ...validSnapshot, value: "opaque source" };
// @ts-expect-error -- safe snapshots cannot carry a token
export const snapshotWithToken: OneTimeSecretSnapshot = { ...validSnapshot, token: "opaque source" };
// @ts-expect-error -- safe snapshots cannot carry a URL
export const snapshotWithURL: OneTimeSecretSnapshot = { ...validSnapshot, url: "opaque source" };
// @ts-expect-error -- every snapshot, including copied, requires explicit copyStatus
export const copiedWithoutStatus: OneTimeSecretSnapshot = { generation: 1, phase: "copied", warningActive: false };

// @ts-expect-error -- a negative literal backend expiry is rejected at the public owner boundary
owner.replace({ value: "opaque source", backendExpiresAtEpochMs: -1 });
// @ts-expect-error -- backend expiry is numeric
owner.replace({ value: "opaque source", backendExpiresAtEpochMs: "soon" });
// @ts-expect-error -- initial visibility is a closed union
owner.replace({ value: "opaque source", initialVisibility: "visible" });
// @ts-expect-error -- timing policy rejects negative literal backend expiry at compile time
planOneTimeSecretTiming({ adoptedAtEpochMs: 1_000, adoptedAtMonotonicMs: 2_000, backendExpiresAtEpochMs: -1 });
// @ts-expect-error -- timing policy backend expiry is numeric
planOneTimeSecretTiming({ adoptedAtEpochMs: 1_000, adoptedAtMonotonicMs: 2_000, backendExpiresAtEpochMs: "soon" });

// @ts-expect-error -- runtime requires a scheduler
export const runtimeWithoutSchedule: OneTimeSecretRuntime = {
  epochNowMs: () => 1_000,
  monotonicNowMs: () => 2_000,
  cancel: () => {},
};
// @ts-expect-error -- runtime requires timer cancellation
export const runtimeWithoutCancel: OneTimeSecretRuntime = {
  epochNowMs: () => 1_000,
  monotonicNowMs: () => 2_000,
  schedule: () => 1,
};

// @ts-expect-error -- component never accepts a raw secret prop
createElement(OneTimeSecretReveal, { ...validComponentProps, secret: "opaque source" });
// @ts-expect-error -- component never accepts a token prop
createElement(OneTimeSecretReveal, { ...validComponentProps, token: "opaque source" });
// @ts-expect-error -- component never accepts a URL prop
createElement(OneTimeSecretReveal, { ...validComponentProps, url: "opaque source" });
// @ts-expect-error -- component never accepts recovery-code material
createElement(OneTimeSecretReveal, { ...validComponentProps, recoveryCodes: ["opaque source"] });
// @ts-expect-error -- the selected lazy boundary requires an element, not a raw string return
createElement(OneTimeSecretReveal, { ...validComponentProps, renderRevealedContent: () => "opaque source" });
// @ts-expect-error -- copy intent never receives a raw error or metadata argument
createElement(OneTimeSecretReveal, { ...validComponentProps, onCopyIntent: (_error: Error) => { void _error; } });
