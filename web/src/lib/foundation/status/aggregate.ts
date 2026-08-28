import { copyCanonicalDomainStatusPresentation } from "@/lib/foundation/status/presenter-core";

export type StatusCoverageSummary = Readonly<{
  total: number;
  known: number;
  unknown: number;
}>;

export function summarizeStatusCoverage(
  presentations: readonly unknown[],
): StatusCoverageSummary {
  try {
    if (!Array.isArray(presentations)) return emptySummary();
    let known = 0;
    for (const presentation of presentations) {
      if (copyCanonicalDomainStatusPresentation(presentation)?.known === true) known += 1;
    }
    return Object.freeze({
      total: presentations.length,
      known,
      unknown: presentations.length - known,
    });
  } catch {
    return emptySummary();
  }
}

function emptySummary(): StatusCoverageSummary {
  return Object.freeze({ total: 0, known: 0, unknown: 0 });
}
