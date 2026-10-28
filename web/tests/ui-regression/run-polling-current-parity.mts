import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { runExactParityFile, runFixedLegacyParity } from "./polling-fixed-parity.mts";
import { pollingOuterTestNames } from "./polling-current-source-test-names.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
try {
  const current = await runExactParityFile(resolve(root, "web"), "tests/ui-regression/polling-current-source-delta.test.mts", pollingOuterTestNames);
  process.stdout.write(JSON.stringify({ phase: "current-polling-outer-projection", ...current }) + "\n");
  // No platform/HEAD environment disguise and no silent exclusion of the old
  // imported suites: real Linux executes their exact fixed source and 59 cases.
  const historical = await runFixedLegacyParity(root);
  process.stdout.write(JSON.stringify(historical) + "\n");
} catch (error) {
  process.stderr.write(String(error instanceof Error ? error.stack || error.message : error) + "\n");
  process.exitCode = 1;
}
