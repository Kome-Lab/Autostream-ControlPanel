import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { assertWorkerFoundationBoundaries, workerRestartTriggerCompositionIssues } from "./helpers/workers-foundation-imports.mts";
import { webRoot, replaceWorkerViewExactlyOnce } from "./workers-pilot-fixture.mts";


export function registerWorkerSourceOracleCases() {


test("AST guards reject DangerConfirm, secret imports, endpoint descriptors, retries and global locks", () => {
  assert.deepEqual(assertWorkerFoundationBoundaries(webRoot), { descriptorFiles: 2, controllerFiles: 2, normalizerFiles: 1 });
});

test("Worker restart AST oracle rejects ref, manual-open, wrapper and interactive trigger mutants", () => {
  const production = readFileSync(join(webRoot, "src", "features", "workers", "workers-view.tsx"), "utf8")
    .replace(/\r\n/g, "\n");
  assert.deepEqual(workerRestartTriggerCompositionIssues(production), []);

  const wrapTrigger = (source: string, wrapperOpen: string, wrapperClose: string) => {
    const opened = replaceWorkerViewExactlyOnce(
      source,
      "              trigger={(confirmationProps) => (\n                <Button\n",
      `              trigger={(confirmationProps) => (\n                ${wrapperOpen}\n                  <Button\n`,
    );
    return replaceWorkerViewExactlyOnce(
      opened,
      "                  <span>{translate(\"restart\")}</span>\n                </Button>\n              )}",
      `                  <span>{translate(\"restart\")}</span>\n                  </Button>\n                ${wrapperClose}\n              )}`,
    );
  };
  const withManualOnClick = (source: string, expression: string) => replaceWorkerViewExactlyOnce(
    source,
    "                  ref={restartTriggerRef}\n",
    `                  ref={restartTriggerRef}\n                  onClick={${expression}}\n`,
  );

  const extractedHelper = replaceWorkerViewExactlyOnce(
    production,
    "  return (\n    <div className=\"flex items-center gap-2\">",
    "  const openRestartFromTrigger = () => openRestart(row.original);\n\n  return (\n    <div className=\"flex items-center gap-2\">",
  );
  const movedAvailability = wrapTrigger(
    replaceWorkerViewExactlyOnce(
      replaceWorkerViewExactlyOnce(
        production,
        "        <ActionAvailabilityBoundary\n          evaluation={restartEvaluation}",
        "        <MovedAvailabilityBoundary\n          evaluation={restartEvaluation}",
      ),
      "        </ActionAvailabilityBoundary>\n      ) : null}",
      "        </MovedAvailabilityBoundary>\n      ) : null}",
    ),
    "<ActionAvailabilityBoundary>",
    "</ActionAvailabilityBoundary>",
  );

  const mutants = [
    {
      name: "restartTriggerRef removed",
      source: replaceWorkerViewExactlyOnce(production, "                  ref={restartTriggerRef}\n", ""),
      expectedIssue: "restart-trigger-ref",
    },
    {
      name: "restartTriggerRef moved to wrapper",
      source: wrapTrigger(
        replaceWorkerViewExactlyOnce(production, "                  ref={restartTriggerRef}\n", ""),
        "<div ref={restartTriggerRef}>",
        "</div>",
      ),
      expectedIssue: "immediate-trigger-button",
    },
    {
      name: "different ref substituted",
      source: replaceWorkerViewExactlyOnce(production, "ref={restartTriggerRef}", "ref={differentTriggerRef}"),
      expectedIssue: "restart-trigger-ref",
    },
    {
      name: "manual open onClick added",
      source: withManualOnClick(production, "() => openRestart(row.original)"),
      expectedIssue: "manual-open-onclick",
    },
    {
      name: "manual open callback extracted to helper",
      source: withManualOnClick(extractedHelper, "openRestartFromTrigger"),
      expectedIssue: "manual-open-onclick",
    },
    {
      name: "wrapper added",
      source: wrapTrigger(production, "<div>", "</div>"),
      expectedIssue: "immediate-trigger-button",
    },
    {
      name: "nested button added",
      source: replaceWorkerViewExactlyOnce(
        production,
        "                  <span>{translate(\"restart\")}</span>\n",
        "                  <span>{translate(\"restart\")}</span>\n                  <button type=\"button\">Nested</button>\n",
      ),
      expectedIssue: "interactive-trigger-descendant",
    },
    {
      name: "nested link added",
      source: replaceWorkerViewExactlyOnce(
        production,
        "                  <span>{translate(\"restart\")}</span>\n",
        "                  <span>{translate(\"restart\")}</span>\n                  <a href=\"#nested\">Nested</a>\n",
      ),
      expectedIssue: "interactive-trigger-descendant",
    },
    {
      name: "two trigger children",
      source: wrapTrigger(production, "<>", "<Button>Second trigger</Button>\n                </>"),
      expectedIssue: "immediate-trigger-button",
    },
    {
      name: "HighRiskConfirmation removed",
      source: replaceWorkerViewExactlyOnce(production, "<HighRiskConfirmation\n", "<RemovedHighRiskConfirmation\n"),
      expectedIssue: "high-risk-confirmation-count",
    },
    {
      name: "availability boundary moved inside trigger",
      source: movedAvailability,
      expectedIssue: "availability-boundary-not-outer",
    },
  ];

  for (const mutant of mutants) {
    const issues = workerRestartTriggerCompositionIssues(mutant.source, `${mutant.name}.tsx`);
    assert.equal(
      issues.includes(mutant.expectedIssue),
      true,
      `${mutant.name} was accepted by the Worker restart AST oracle: ${JSON.stringify(issues)}`,
    );
  }
});
}
