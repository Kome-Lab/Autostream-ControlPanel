import { spawnSync } from "node:child_process";
import { captureNextGeneratedFiles, restoreNextGeneratedFiles } from "../../web/tests/helpers/browser-harness.mts";
const generated = captureNextGeneratedFiles(process.cwd());
try {
  const result = spawnSync("npm", ["run", "build"], { stdio: "inherit" });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
} finally {
  restoreNextGeneratedFiles(generated);
}
