import type { BrowserHarness } from "../helpers/browser-harness.mts";
import type { Condition } from "./matrix.mts";
import { observationExpression, type UIObservation } from "./observation.mts";

type Writer = (name: string, value: unknown) => void;
type Trace = readonly { method: string; path: string; status: number; body: unknown }[];
export async function captureObservation(browser: BrowserHarness, condition: Condition, trace: Trace, write: Writer, writePNG: (name: string, bytes: Buffer) => void) {
  browser.setFetchDiagnosticContext?.({ phase: "capture" });
  const observation = await browser.evaluate<UIObservation>(observationExpression);
  // Detach once, before any screenshot await, and use the same snapshot in evidence and assertions.
  const evidence = structuredClone({
    condition, observation, api: trace, statuses: [...browser.responseStatuses],
    requests: [...browser.requests], responses: [...browser.responses], consoleErrors: browser.consoleErrorCount,
    visual: "NOT_REVIEWED",
  });
  write(condition.id + ".json", evidence);
  const png = await browser.captureScreenshot();
  writePNG(condition.id + ".png", png);
  return evidence;
}
export function preserveFetchDiagnostic(browser: BrowserHarness, write: Writer) {
  try {
    const diagnostic = browser.fetchFailureDiagnosticJSON;
    if (diagnostic) write("fetch-failure-diagnostic.json", JSON.parse(diagnostic));
  } catch {
    // The caller must still throw its original failure. Existing evidence is never overwritten.
  }
}
