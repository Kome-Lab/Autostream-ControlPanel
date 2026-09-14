import assert from "node:assert/strict";
import type { Condition } from "./matrix.mts";
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
const failureCopy = /取得でき|取得失敗|更新失敗|送信.*失敗|Could not|couldn.t|unavailable|failed to|refresh failed|一部|stale|表示できません/i;
function statuses(evidence: RequestEvidence, path: string) { return evidence.responseStatuses.get(path) || []; }
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
    assert.equal(evidence.responses.get(primary) || 0, 0);
    assert.ok(requests > 0);
    assert.match(text, /取得|読込|読み込|Loading|loading|pending|未取得/);
  } else if (condition.state === "permission-denied") {
    const expected = deniedPrimaryRequests[condition.family] || 0;
    assert.equal(requests, expected, "existing per-surface denied query contract");
    assert.deepEqual(received, Array(expected).fill(403), "every denied response must be 403");
    assert.match(text, /権限|permission|Permission|利用でき|アクセス|forbidden|Forbidden|取得でき|unavailable|表示できません/);
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
