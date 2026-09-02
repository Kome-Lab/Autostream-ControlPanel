import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const resourceSource = readFileSync(new URL("../src/features/resources/resource-page.tsx", import.meta.url), "utf8");
const policySource = readFileSync(new URL("../src/features/observability/action-policy.ts", import.meta.url), "utf8");

function sourceSection(source: string, startMarker: string, endMarker: string) {
  const start = source.indexOf(startMarker);
  assert.notEqual(start, -1, `missing ${startMarker}`);
  const end = source.indexOf(endMarker, start);
  assert.notEqual(end, -1, `missing ${endMarker} after ${startMarker}`);
  return source.slice(start, end);
}

test("rerun_diagnostics uses the incident diagnostics endpoint and never falls through to remediation execute", () => {
  const section = sourceSection(policySource, 'if (actionName === "rerun_diagnostics")', "  const mode");
  const diagnosticsPlan = sourceSection(policySource, "function diagnosticsPlan", "function plan");

  assert.match(section, /incidentID \? Object\.freeze\(\[diagnosticsPlan\(/);
  assert.match(diagnosticsPlan, /\/observability\/incidents\/\$\{encodeURIComponent\(incidentID\)\}\/diagnostics\/rerun/);
  assert.doesNotMatch(section, /remediation-actions\/\$\{encodeURIComponent\(id\)\}\/execute/);
});

test("remediation execute conflicts present a state message and refresh observability resources", () => {
  const controller = sourceSection(policySource, "export function createObservabilityActionController", "export function buildObservabilityActionDescriptor");
  const conflictHandler = sourceSection(resourceSource, 'if (result.kind === "conflict")', 'if (result.kind === "failed")');

  assert.match(controller, /const adapted = adaptAPIError\(error\)/);
  assert.match(controller, /adapted\.kind === "conflict"/);
  assert.match(controller, /kind: "conflict", error: adapted/);
  assert.match(conflictHandler, /状態が更新されたため操作を再送しませんでした/);
  assert.match(conflictHandler, /"\/observability\/remediation-actions", "\/observability\/incidents", "\/observability\/diagnostics"/);
  assert.match(conflictHandler, /queryClient\.invalidateQueries/);
});
