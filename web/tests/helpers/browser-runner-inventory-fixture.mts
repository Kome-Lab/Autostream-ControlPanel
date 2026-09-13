

export const independentRequiredBrowserScenarioNames = Object.freeze([
  "UI Foundation runtime behavior",
  "query states distinguish loading, empty, unhealthy, error, stale, recovery, and update variants",
  "logout clears protected UI and sends exactly one mutation",
  "session expiry keeps a validated same-origin return URL without a redirect loop",
  "/auth/me 401 expiry preserves the exact query and hash return URL",
  "stale refresh completion does not replace a newer authenticated session",
  "session guard ignores setup completion after unmount",
  "login rejects external return URL variants",
  "Streams start-readiness follows streams.start at render and confirm time",
  "handler guard structural oracle rejects in-memory regressions",
  "start only",
  "update only",
  "both",
  "neither",
  "wildcard",
  "permission changes before confirm",
  "backend 403 retains the existing action error mapping",
  "pending mutation keeps duplicate start-readiness blocked",
  "Worker restart uses fresh canonical action policy and one POST per worker",
  "Workers Configuration uses the server ANY permission and safe remote state",
  "desktop/mobile navigation parity, active route, and permission visibility are runtime-enforced",
  "same-route and cross-route mobile create release the navigation focus owner",
  "reduced motion removes Sheet animation while preserving close and focus",
  "locale and theme controls preserve route/session and expose translated accessible names",
  "Account appearance persists 12 themes and 3 modes with DB fallback and save rollback",
  "Stream detail presents visual snapshots and cover actions preserve request-count and applied-state boundaries",
  "Bundle 7 affected surfaces are responsive at every canonical width",
  "status focus remains visible in normal and forced-colors modes",
  "false-positive guards reject invalid observable outcomes",
  "high-risk and compatibility confirmations preserve browser focus, literal input, state, and intent boundaries",
  "conflict renders safe error and hides diagnostic metadata",
  "failed renders safe error without retry",
  "revalidation unavailable blocks confirmation and returns focus",
  "failure-state browser oracle rejects in-memory false-positive variants",
  "one-time secret browser boundary preserves controlled reveal, focus, cleanup, and leakage invariants",
] as const);

export function passingScenario(name: string) {
  return { name, passed: true, skipped: false, todo: false };
}

export function browserSummary(overrides: Partial<{
  cancelled: number;
  passed: number;
  skipped: number;
  suites: number;
  tests: number;
  todo: number;
  topLevel: number;
}> = {}) {
  return {
    success: true,
    counts: {
      cancelled: 0,
      passed: 35,
      skipped: 0,
      suites: 3,
      tests: 35,
      todo: 0,
      topLevel: 3,
      ...overrides,
    },
  };
}

export function passingBrowserInventoryFixture() {
  return {
    summary: browserSummary(),
    completed: [
      ...independentRequiredBrowserScenarioNames.map(passingScenario),
    ],
  };
}
