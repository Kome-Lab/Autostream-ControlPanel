import assert from "node:assert/strict";
import type { Condition } from "./matrix.mts";

export const longIdentifier = "UI-LONG-ID-" + "0123456789abcdef-".repeat(24);
export const longText = "UI-LONG-TEXT- 長い日本語の運用表示 Long operational name ".repeat(6);
// Only synthetic display fields. No credentials, error payloads, locale values,
// policy enums, permission strings or capability URLs enter these transforms.
const displayFields = new Set(["name", "service_name", "username", "actor_username", "title", "summary", "subject", "artifact_name", "stream_name"]);
const aliases = new Map(["stream-control-platform", "worker-one", "encoder-one", "host-agent-main", "ui-foundation-user", "ui-record-one", "ui-archive", "ui-run", "ui-artifact", "ui-audit", "incident-one", "diagnostic-one", "remediation-one", "notification-one", ...Array.from({ length: 24 }, (_, i) => "ui-stream-" + (i + 1))].map(id => [id, id + "-" + longIdentifier]));
export function canonicalFixturePath(path: string) {
  for (const [id, alias] of aliases) path = path.replaceAll(encodeURIComponent(alias), encodeURIComponent(id)).replaceAll(alias, id);
  return path;
}
export function contentInput(body: unknown, exercise?: string, path = ""): unknown {
  if (!body || !["long-id", "long-text"].includes(exercise || "")) return body;
  if (Array.isArray(body)) return body.map(value => contentInput(value, exercise, path));
  if (typeof body !== "object") return body;
  return Object.fromEntries(Object.entries(body).map(([key, value]) => [key,
    displayFields.has(key) && !(path === "/observability/metrics" && key === "name") && typeof value === "string" ? (exercise === "long-id" ? longIdentifier + value : longText + value)
      : exercise === "long-id" && typeof value === "string" && (key === "id" || key.endsWith("_id")) && aliases.has(value) ? aliases.get(value)
        : value && typeof value === "object" ? contentInput(value, exercise, path) : value]));
}
export function emptyInput(path: string, body: unknown): unknown {
  // Applicable collection APIs return arrays; singleton surfaces retain their
  // existing not-applicable empty contracts. Never substitute [] for an object.
  assert.ok(Array.isArray(body), "no empty driver for singleton " + path);
  return [];
}
export function unknownInput(path: string, body: unknown): unknown {
  if (Array.isArray(body)) return body.map(row => unknownInput(path, row));
  assert.ok(body && typeof body === "object", "unknown requires the actual response shape");
  const row = body as Record<string, unknown>;
  if (path === "/system-updates") return { ...row, updaters: (row.updaters as unknown[]).map(item => ({ ...(item as object), status: "future_state", online: null })) };
  if (path === "/audit-logs") return { ...row, result: "future_result" };
  if (["/workers", "/nodes", "/service-health"].includes(path)) return { ...row, status: "future_state", health_status: "future_health" };
  return { ...row, status: "future_state" };
}
export function assertContentReached(condition: Condition, text: string, fieldValues: string[]) {
  if (!["long-id", "long-text"].includes(condition.exercise || "")) return;
  const marker = condition.exercise === "long-id" ? longIdentifier : longText;
  assert.ok([text, ...fieldValues].some(value => value.includes(marker)), "long-content driver did not reach a displayed field");
}
