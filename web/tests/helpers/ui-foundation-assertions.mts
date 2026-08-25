import assert from "node:assert/strict";

export const authMeExpiryScenarioName = "/auth/me 401 expiry preserves the exact query and hash return URL";

export type AuthMeExpiryOutcome = {
  accountMenuPresent: boolean;
  authMeRequestCount: number;
  authMeResponseStatuses: number[];
  csrfTokenPresent: boolean;
  expectedOrigin: string;
  expectedReturnURL: string;
  href: string;
  navigationCount: number;
  navigationSucceeded: boolean;
  pathname: string;
  protectedAdminLandmarkPresent: boolean;
  reason: string | null;
  refreshResponseStatuses: number[];
  returnParameterValues: string[];
};

export type LogoutOutcome = {
  logoutRequestCount: number;
  pathname: string;
  protectedAdminLandmarkPresent: boolean;
  accountMenuPresent: boolean;
  csrfTokenPresent: boolean;
};

export type NavigationSnapshot = {
  hrefs: string[];
  active: string;
  activeHrefs: string[];
};

export type NavigationBoundaryOutcome = {
  requestedPath: string;
  expectedActive: string;
  desktop: NavigationSnapshot;
  mobile: NavigationSnapshot;
};

export type LoginNavigationOutcome = {
  href: string;
  expectedOrigin: string;
  expectedPathname: string;
};

export type BrowserSuiteSummary = {
  success: boolean;
  counts: {
    cancelled: number;
    passed: number;
    skipped: number;
    suites: number;
    tests: number;
    todo: number;
    topLevel: number;
  };
};

export type CompletedBrowserScenario = {
  name: string;
  passed: boolean;
  skipped: boolean;
  todo: boolean;
};

export function assertLogoutOutcome(outcome: LogoutOutcome) {
  assert.equal(outcome.logoutRequestCount, 1, "logout mutation request count");
  assert.equal(outcome.pathname, "/login/", "logout final pathname");
  assert.equal(outcome.protectedAdminLandmarkPresent, false, "protected admin landmark must be removed");
  assert.equal(outcome.accountMenuPresent, false, "account menu must be removed");
  assert.equal(outcome.csrfTokenPresent, false, "CSRF session state must be cleared");
}

export function assertAuthMeExpiryOutcome(outcome: AuthMeExpiryOutcome) {
  assert.equal(outcome.navigationSucceeded, true, "login navigation must complete successfully");
  assert.equal(outcome.authMeRequestCount, 1, "/auth/me request count after expiry");
  assert.deepEqual(outcome.authMeResponseStatuses, [401], "/auth/me must produce exactly one observed 401 response");
  assert.deepEqual(outcome.refreshResponseStatuses, [200], "/auth/session/refresh must remain successful in the auth-me expiry scenario");

  const loginURL = new URL(outcome.href);
  assert.equal(loginURL.origin, outcome.expectedOrigin, "session-expiry login navigation must remain same-origin");
  assert.equal(outcome.pathname, "/login/", "session-expiry final pathname");
  assert.equal(outcome.reason, "session_expired", "session-expiry login reason");
  assert.deepEqual(
    outcome.returnParameterValues,
    [outcome.expectedReturnURL],
    "session-expiry return parameter must be present exactly once without double encoding",
  );

  const actualReturnURL = new URL(outcome.returnParameterValues[0], outcome.expectedOrigin);
  const expectedReturnURL = new URL(outcome.expectedReturnURL, outcome.expectedOrigin);
  assert.equal(actualReturnURL.origin, outcome.expectedOrigin, "session-expiry return URL must remain same-origin");
  assert.equal(actualReturnURL.pathname, expectedReturnURL.pathname, "session-expiry return pathname");
  assert.equal(actualReturnURL.search, expectedReturnURL.search, "session-expiry return query and ordering");
  assert.equal(actualReturnURL.hash, expectedReturnURL.hash, "session-expiry return hash");
  assert.equal(outcome.navigationCount, 1, "session-expiry login navigation count");
  assert.equal(outcome.protectedAdminLandmarkPresent, false, "protected admin landmark must be removed after /auth/me 401");
  assert.equal(outcome.accountMenuPresent, false, "account menu must be removed after /auth/me 401");
  assert.equal(outcome.csrfTokenPresent, false, "CSRF session state must be cleared after /auth/me 401");
}

export function assertNavigationBoundaryOutcome(outcome: NavigationBoundaryOutcome) {
  const expectedActiveHrefs = outcome.expectedActive ? [outcome.expectedActive] : [];
  assert.equal(outcome.desktop.active, outcome.expectedActive, `${outcome.requestedPath} desktop active route`);
  assert.equal(outcome.mobile.active, outcome.expectedActive, `${outcome.requestedPath} mobile active route`);
  assert.deepEqual(outcome.desktop.activeHrefs, expectedActiveHrefs, `${outcome.requestedPath} desktop active route count`);
  assert.deepEqual(outcome.mobile.activeHrefs, expectedActiveHrefs, `${outcome.requestedPath} mobile active route count`);
  assert.deepEqual(outcome.mobile.hrefs, outcome.desktop.hrefs, `${outcome.requestedPath} desktop/mobile navigation authority`);
}

export function assertLoginNavigationOutcome(outcome: LoginNavigationOutcome) {
  const actual = new URL(outcome.href);
  assert.equal(actual.origin, outcome.expectedOrigin, "login navigation must remain same-origin");
  assert.equal(actual.pathname, outcome.expectedPathname, "login navigation pathname");
}

export function assertNoBrowserConsoleErrors(errorCount: number) {
  assert.equal(errorCount, 0, "browser console or uncaught error count");
}

export function assertBrowserSuiteExecution(
  summary: BrowserSuiteSummary | undefined,
  completed: CompletedBrowserScenario[],
  requiredScenarioNames: readonly string[],
) {
  assert.ok(summary, "browser test runner did not report a summary");
  assert.equal(summary.success, true, "browser test runner reported a failure");
  assert.ok(summary.counts.tests > 0, "browser suite must execute at least one test");
  assert.equal(summary.counts.cancelled, 0, "browser suite must not cancel tests");
  assert.equal(summary.counts.skipped, 0, "browser suite must not skip scenarios");
  assert.equal(summary.counts.todo, 0, "browser suite must not leave TODO scenarios");

  for (const requiredName of new Set(requiredScenarioNames)) {
    const scenario = completed.find((candidate) => candidate.name === requiredName);
    assert.ok(scenario, `required browser scenario did not run: ${requiredName}`);
    assert.equal(scenario.passed, true, `required browser scenario did not pass: ${requiredName}`);
    assert.equal(scenario.skipped, false, `required browser scenario was skipped: ${requiredName}`);
    assert.equal(scenario.todo, false, `required browser scenario was TODO: ${requiredName}`);
  }
}
