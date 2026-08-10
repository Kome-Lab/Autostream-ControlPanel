import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("../src/features/resources/resource-page.tsx", import.meta.url), "utf8");

function sourceSection(startMarker: string, endMarker: string) {
  const start = source.indexOf(startMarker);
  assert.notEqual(start, -1, `missing ${startMarker}`);
  const end = source.indexOf(endMarker, start);
  assert.notEqual(end, -1, `missing ${endMarker} after ${startMarker}`);
  return source.slice(start, end);
}

test("rerun_diagnostics uses the incident diagnostics endpoint and never falls through to remediation execute", () => {
  const section = sourceSection('if (actionName === "rerun_diagnostics")', "    const mode");

  assert.match(section, /if \(!incidentID\) return \[\];/);
  assert.match(section, /\/observability\/incidents\/\$\{encodeURIComponent\(incidentID\)\}\/diagnostics\/rerun/);
  assert.doesNotMatch(section, /remediation-actions\/\$\{encodeURIComponent\(id\)\}\/execute/);
});

test("remediation execute conflicts present a state message and refresh observability resources", () => {
  const errorHandler = sourceSection("onError: async (error, action) =>", "  const deleteMutation");
  const conflictMessage = sourceSection("function observabilityRemediationExecutionConflictMessage", "function EditResourceButton");

  assert.match(errorHandler, /observabilityRemediationExecutionConflictMessage\(action\.path, error\)/);
  assert.match(errorHandler, /"\/observability\/remediation-actions", "\/observability\/incidents", "\/observability\/diagnostics"/);
  assert.match(errorHandler, /queryClient\.invalidateQueries/);
  assert.match(conflictMessage, /error\.status !== 409/);
  assert.match(conflictMessage, /remediation_action_terminal/);
  assert.match(conflictMessage, /remediation_action_not_executable/);
  assert.match(conflictMessage, /再実行せず/);
});
