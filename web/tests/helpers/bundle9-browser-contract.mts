import assert from "node:assert/strict";
import { createHash } from "node:crypto";

export const BUNDLE9_BROWSER_BEFORE = "792c6c56506c26ffd81b18c1793211e8e78be44d";
export const BUNDLE9_BROWSER_CLOCK = "2026-09-12T00:00:00.000Z";
export const bundle9Viewports = Object.freeze([
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
] as const);

// The existing 35-case browser runner owns its full interaction/responsive
// inventory. These additional observations bind changed C01 surfaces to the
// fixed C00 source and compare actual production exports from both sources.
export const bundle9BrowserSurfaces = Object.freeze([
  { id: "streams", cluster: "Streams", route: "/admin/streams/?view=active#preview", required: ["/streams", "/youtube/outputs"], text: "B9 Browser Stream" },
  { id: "resources", cluster: "Generic Resources", route: "/admin/encoder/?view=profiles#details", required: ["/profiles/encoder"], text: "B9 Encoder Profile" },
  { id: "application", cluster: "Application-Updater", route: "/admin/application/?view=updates#updater", required: ["/system-updates", "/nodes"], text: "B9 Host Agent" },
  { id: "account", cluster: "Account-Archive", route: "/admin/account/?view=profile#security", required: ["/auth/mfa/status", "/auth/passkeys", "/auth/oauth-links", "/auth/oauth/providers"], text: "ui-foundation-admin" },
  { id: "archive", cluster: "Account-Archive", route: "/admin/archive/?view=recordings#local", required: ["/archive/streams", "/archive/processing-streams", "/profiles/archive"], text: "B9 Recording Profile" },
  { id: "monitoring", cluster: "Observability-Monitoring", route: "/admin/monitoring/?view=active#incidents", required: ["/streams", "/observability/incidents", "/observability/diagnostics"], text: "監視情報は正常に取得済み" },
  { id: "incidents", cluster: "Observability-Monitoring", route: "/admin/incidents/?view=open#details", required: ["/observability/incidents"], text: "B9 Incident" },
  { id: "diagnostics", cluster: "Observability-Monitoring", route: "/admin/diagnostics/?view=recent#details", required: ["/observability/diagnostics"], text: "B9 Diagnostic" },
  { id: "remediation", cluster: "Observability-Monitoring", route: "/admin/remediation/?view=recent#details", required: ["/observability/remediation-actions"], text: "B9 Remediation" },
  { id: "notifications", cluster: "Observability-Monitoring", route: "/admin/notifications/?view=recent#details", required: ["/observability/notification-deliveries"], text: "B9 Notification" },
] as const);

export const bundle9InteractionCaptures = Object.freeze([
  "streams-confirmation", "streams-cancel-focus", "streams-one-mutation",
  "streams-error", "streams-permission-denied", "streams-detail-preview",
  "streams-detail-closed", "resources-editor",
  "resources-cancel-focus", "resources-permission-denied", "updater-settings",
  "updater-cancel-focus", "account-security", "account-secret-concealed",
  "account-secret-disposed", "archive-local", "archive-permission-denied",
  "monitoring-error", "monitoring-recovered", "mobile-navigation",
  "mobile-navigation-closed",
] as const);

export const bundle9ExpectedCaptureNames = Object.freeze([
  ...bundle9BrowserSurfaces.flatMap((surface) => bundle9Viewports.map((viewport) => `${surface.id}-${viewport.width}`)),
  ...bundle9InteractionCaptures,
]);

export type APIObservation = Readonly<{
  method: string;
  path: string;
  body: unknown;
  status: number;
}>;

export function sha256(bytes: Uint8Array | string) {
  return createHash("sha256").update(bytes).digest("hex");
}

export function canonicalJSON(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(",")}]`;
  if (value !== null && typeof value === "object") {
    return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b, "en")).map(([key, item]) => `${JSON.stringify(key)}:${canonicalJSON(item)}`).join(",")}}`;
  }
  assert.notEqual(value, undefined, "observable values must not silently disappear");
  return JSON.stringify(value);
}

export function apiObservationSummary(trace: readonly APIObservation[]) {
  // Independent GETs may settle in a different network order. Preserve every
  // path/query/body/status and exact multiplicity, plus ordered mutation calls.
  const counts = new Map<string, number>();
  for (const request of trace) {
    const key = canonicalJSON(request);
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  return {
    calls: [...counts].sort(([a], [b]) => a.localeCompare(b, "en")).map(([request, count]) => ({ request: JSON.parse(request) as APIObservation, count })),
    mutations: trace.filter((request) => request.method !== "GET" && request.method !== "HEAD"),
    total: trace.length,
  };
}

export function assertCaptureInventory(names: readonly string[]) {
  assert.equal(new Set(names).size, names.length, "duplicate browser capture");
  assert.deepEqual([...names].sort(), [...bundle9ExpectedCaptureNames].sort(), "all five clusters and their interactions must execute");
  assert.equal(new Set(bundle9BrowserSurfaces.map((surface) => surface.cluster)).size, 5, "five-cluster denominator");
  assert.ok(names.length > 0, "zero browser capture denominator");
}

export function assertSameObservation(before: unknown, after: unknown) {
  assert.equal(canonicalJSON(after), canonicalJSON(before), "browser/API behavior must have zero differences");
}

export function assertPixelIdentity(
  before: Readonly<{ width: number; height: number; channels: number; data: Uint8Array }>,
  after: Readonly<{ width: number; height: number; channels: number; data: Uint8Array }>,
) {
  assert.ok(before.width > 0 && before.height > 0 && before.channels === 4, "nonempty RGBA screenshot required");
  assert.deepEqual([after.width, after.height, after.channels], [before.width, before.height, before.channels], "screenshot dimensions/channels");
  assert.equal(before.data.length, before.width * before.height * before.channels, "complete before pixels");
  assert.equal(after.data.length, before.data.length, "complete after pixels");
  let differentPixels = 0;
  for (let offset = 0; offset < before.data.length; offset += 4) {
    if (before.data[offset] !== after.data[offset]
      || before.data[offset + 1] !== after.data[offset + 1]
      || before.data[offset + 2] !== after.data[offset + 2]
      || before.data[offset + 3] !== after.data[offset + 3]) differentPixels += 1;
  }
  assert.equal(differentPixels, 0, `strict screenshot pixel difference: ${differentPixels}`);
  return { pixels: before.width * before.height, differentPixels };
}

export function assertSourcePair(before: string, after: string, checkedOut: string) {
  assert.equal(before, BUNDLE9_BROWSER_BEFORE, "immutable C00 before source");
  assert.match(after, /^[a-f0-9]{40}$/, "full after source SHA");
  assert.equal(after, checkedOut, "after must be the actual checked-out CI source");
  assert.notEqual(after, before, "before must not be reused as after");
}
