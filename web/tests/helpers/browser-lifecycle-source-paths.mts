import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";


export const actionPath = "/streams/fixture/start-readiness";
export const helperRoot = dirname(fileURLToPath(import.meta.url));
export const browserHarnessPath = join(helperRoot, "browser-harness.mts");
export const uiBrowserTestPath = join(helperRoot, "..", "ui-foundation-browser.test.mts");
export const browserRunnerPath = join(helperRoot, "run-ui-foundation-browser.mts");

export const accountScenarioName = "Account appearance persists 12 themes and 3 modes with DB fallback and save rollback";
export const preferencePath = "/account/preferences/ui";
