import assert from "node:assert/strict";
import { conditions, inventory, type Condition } from "./matrix.mts";
import type { UIObservation } from "./observation.mts";

// Current query contracts: retry=1 in QueryProvider; public share uses retry=false.
// These expectations do not change the product's permission or query owners.
export const deniedPrimaryRequests: Record<string, number> = {
  "streams-list": 2, metrics: 2, "audit-logs": 2, monitoring: 2, nodes: 2,
  "public-archive-share": 1,
};
export const sectionPaths: Record<string, string[]> = {
  dashboard: ["/streams", "/service-health"],
  workers: ["/workers", "/nodes", "/service-health"],
  "service-health": ["/service-health", "/nodes"],
  archive: ["/archive/streams", "/archive/processing-streams"],
  monitoring: ["/streams", "/service-health", "/observability/incidents", "/observability/diagnostics"],
  metrics: ["/observability/metrics", "/service-health"],
  "system-updates": ["/system-updates", "/nodes", "/service-health"],
  roles: ["/roles", "/permissions"],
  "security-settings": ["/security/settings", "/roles"],
  account: ["/auth/mfa/status", "/auth/passkeys", "/auth/oauth-links", "/auth/oauth/providers"],
};
export function statePaths(condition: Condition, primary: string) {
  const all = sectionPaths[condition.family] || [primary];
  if (condition.state === "partial") {
    assert.ok(all.length > 1, "partial needs independently observable sections");
    return { success: condition.family === "monitoring" ? [primary, "/service-health"] : [primary], failure: condition.family === "monitoring" ? ["/observability/incidents", "/observability/diagnostics"] : all.slice(1) };
  }
  return { success: [] as string[], failure: condition.state === "blocking-error" ? all : [primary] };
}

// Only owners participating in the visible initial aggregate; role/settings
// secondary form queries are mounted later and are not initial prerequisites.
export function loadingPaths(condition: Condition, primary: string) {
  return ["dashboard", "workers", "service-health", "archive", "monitoring", "metrics", "system-updates", "account"].includes(condition.family)
    ? sectionPaths[condition.family] : [primary];
}

export const visibleSeed: Record<string, string> = {
  dashboard: "B9 Browser Stream", "streams-list": "B9 Browser Stream", "stream-detail": "B9 Browser Stream",
  "service-health": "Worker One", workers: "Worker One", nodes: "Worker One",
  "encoder-profiles": "B9 Encoder Profile", discord: "UI Discord configuration", youtube: "UI YouTube output",
  captions: "UI caption profile", watermark: "UI watermark", archive: "UI recorded stream",
  monitoring: "Worker One", incidents: "B9 Incident", diagnostics: "B9 Diagnostic", metrics: "worker-one",
  remediation: "B9 Remediation", notifications: "B9 Notification", "audit-logs": "ui-operator",
  "system-updates": "B9 Host Agent", integrations: "UI regression record", users: "ui-user", roles: "viewer",
  "security-settings": "12", account: "Enabled", login: "UI Identity", "public-archive-share": "UI recording.mp4",
};
export type RequestEvidence = { requests: Map<string, number>; responses: Map<string, number>; responseStatuses: Map<string, number[]> };
export const failureCopy = /取得でき|取得失敗|更新(?:に)?失敗|送信.*失敗|Could not|couldn.t|unavailable|failed to|refresh failed|一部|stale|表示できません/i;
export const failureTextExpression = "[...document.querySelectorAll('main,[role=dialog],[role=alertdialog]')].map(e=>e.textContent).join(' ')";
export const hasFailureCopy = (text: unknown) => typeof text === "string" && failureCopy.test(text);
function statuses(evidence: RequestEvidence, path: string) { return evidence.responseStatuses.get(path) || []; }
export function assertStatusOnly(condition: Condition, value: UIObservation, evidence: RequestEvidence, primary: string) {
  const registered = conditions.find(row => row.id === condition.id);
  assert.deepEqual(condition, registered, "status-only requires the exact registered condition");
  const surface = inventory.surfaces.find(row => row.id === condition.family);
  assert.ok(surface, "status-only family missing");
  const policy = surface?.states[condition.state]?.observation;
  assert.ok(policy?.controls === "status-only", "zero controls outside the five status-only policies");
  assert.equal(primary, surface.primary, "status-only primary owner mismatch");
  const page = value.statusPage;
  assert.ok(page, "missing actual status-only DOM evidence");
  assert.equal(page.url, condition.route, "status-only route mismatch");
  assert.equal(page.mainCount, 1); assert.equal(page.painted, true, "status-only main is not painted");
  assert.equal(page.overlays, 0, "status-only cannot hide behind an overlay");
  assert.equal(page.controlCandidates, 0, "hidden controls are not a status-only branch");
  assert.equal(page.headings.length, 1, "status-only needs a visible heading");
  assert.equal(page.headings[0].text, policy.heading[condition.locale], "status-only family heading mismatch");
  assert.equal(page.messages.length, 1, "status-only needs one visible status or alert");
  assert.equal(page.messages[0].role, policy.role);
  assert.ok(page.messages[0].text.includes(policy.copy[condition.locale]), "status-only copy is not the expected current state");
  for (const rect of [page.rect, page.headings[0].rect, page.messages[0].rect]) {
    assert.ok(rect.length === 4 && rect.every(Number.isFinite) && rect[2] > 0 && rect[3] > 0, "status-only geometry must be real and nonempty");
  }
  // This invokes the existing real state assertion, not a parallel state model.
  assertState(condition, value, evidence, primary);
  if (condition.state === "initial-loading") {
    for (const path of loadingPaths(condition, primary)) assert.deepEqual(statuses(evidence, path), [], "initial owner cannot have received HTTP evidence");
  } else {
    assert.equal(condition.family, "public-archive-share");
    assert.equal(evidence.requests.get(primary), 1); assert.equal(evidence.responses.get(primary), 1);
    assert.deepEqual(statuses(evidence, primary), [condition.state === "permission-denied" ? 403 : 503], "status-only requires the exact public share response");
  }
}
function assertRetained(value: UIObservation, condition: Condition) {
  const marker = condition.family === "metrics" && condition.state !== "partial" ? "Worker One" : visibleSeed[condition.family];
  assert.ok(marker, "a displayed success witness is required");
  const haystack = value.text + " " + value.controls.map(control => control.name + " " + (control.value || "")).join(" ");
  if (condition.family === "account") assert.match(haystack, /有効|Enabled/, "retained MFA state in the Security tab");
  else assert.ok(haystack.includes(marker), "successful section data must remain visible: " + marker);
}
export function assertState(condition: Condition, value: UIObservation, evidence: RequestEvidence, primary: string) {
  const text = value.text + " " + value.notices.map(notice => notice.kind + " " + notice.freshness + " " + notice.text).join(" ");
  const requests = evidence.requests.get(primary) || 0;
  const received = statuses(evidence, primary);
  if (condition.state === "initial-loading") {
    for (const path of loadingPaths(condition, primary)) {
      assert.equal(evidence.responses.get(path) || 0, 0, "initial owner cannot have a settled response: " + path);
      assert.ok((evidence.requests.get(path) || 0) > 0, "initial owner must issue its real GET: " + path);
    }
    assert.ok(value.notices.every(notice => notice.kind !== "partial" && notice.freshness !== "stale"), "partial/stale is not initial loading");
    assert.match(text, /取得|読込|読み込|Loading|loading|pending|未取得|not been received/);
  } else if (condition.state === "permission-denied") {
    const expected = deniedPrimaryRequests[condition.family] || 0;
    assert.equal(requests, expected, "existing per-surface denied query contract");
    assert.deepEqual(received, Array(expected).fill(403), "every denied response must be 403");
    assert.match(text, /権限|permission|Permission|利用でき|アクセス|forbidden|Forbidden|取得でき|unavailable|Could not load|表示できません/);
    assert.deepEqual(value.controls.filter(control => !control.disabled && /^(Start|Stop|Restart worker|Delete|開始|停止|削除|Worker を再起動)$/.test(control.name)), [], "denied actor cannot perform high-risk actions");
  } else if (condition.state === "partial") {
    const paths = statePaths(condition, primary);
    for (const path of paths.success) assert.equal(statuses(evidence, path).at(-1), 200, "partial has a successful section");
    for (const path of paths.failure) assert.equal(statuses(evidence, path).at(-1), 503, "partial has an independently failed section");
    assert.match(text, failureCopy);
    assertRetained(value, condition);
  } else if (condition.state === "blocking-error" || condition.state === "stale") {
    assert.equal(received.at(-1), 503);
    assert.match(text, failureCopy);
    if (condition.state === "stale") { assert.equal(received[0], 200); assertRetained(value, condition); }
  } else if (condition.state === "empty") {
    assert.equal(received.at(-1), 200);
    assert.match(text, /ありません|なし|0|empty|No /i);
  } else if (condition.state === "unknown") {
    assert.equal(received.at(-1), 200);
    assert.match(text, /不明|未確認|unknown/i, "future input must reach the actual unknown presenter");
    assert.deepEqual(value.controls.filter(control => !control.disabled && /^(Start|開始)$/.test(control.name)), [], "unknown lifecycle cannot start");
  } else if (condition.state === "background-refresh") {
    assert.equal(requests, 2); assert.equal(evidence.responses.get(primary), 1);
    assert.match(text, /更新中|Refreshing|refreshing/);
    assertRetained(value, condition);
  } else assert.equal(received.at(-1), 200);
}
