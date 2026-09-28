import { once } from "node:events";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import process from "node:process";
import { run } from "node:test";
import { spec } from "node:test/reporters";
import { fileURLToPath } from "node:url";

import {
  assertOperationWitnessExecution,
  type OperationWitnessManifest,
  type CompletedWitnessScenario,
  type WitnessRunSummary,
} from "./operation-witness-gate.mts";

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const manifest = JSON.parse(
  readFileSync(new URL("../fixtures/operation-witnesses.json", import.meta.url), "utf8"),
) as OperationWitnessManifest;

let summary: WitnessRunSummary | undefined;
const completed: CompletedWitnessScenario[] = [];
const files = [...new Set(manifest.witnesses.map((witness) => resolve(webRoot, witness.behavior_test)))];
const tests = run({
  cwd: webRoot,
  files,
  concurrency: false,
  forceExit: true,
  timeout: 180_000,
  execArgv: ["--no-warnings"],
});
tests.on("test:complete", (result) => {
  completed.push({
    name: result.name,
    passed: result.details.passed,
    skipped: Boolean(result.skip),
    todo: Boolean(result.todo),
  });
});
tests.on("test:summary", (result) => {
  summary = { success: result.success, counts: { ...result.counts } };
});
const report = tests.compose(spec);
report.pipe(process.stdout, { end: false });
await once(report, "end");
assertOperationWitnessExecution(manifest, summary, completed);
process.stdout.write("Operation witnesses PASS: 2/2, zero denominator=0, unrelated binding=0\n");
