import { readFileSync, statSync } from "node:fs";
import { isAbsolute } from "node:path";
import { isDeepStrictEqual } from "node:util";
import { fileURLToPath } from "node:url";

import { normalizeSystemUpdatesResponse } from "../../src/lib/system-updates.ts";
import type { SystemUpdateJob } from "../../src/types/domain.ts";
import { BrowserHarness, ensureWebServer } from "./browser-harness.mts";

export const stPortAcceptedGetCases = Object.freeze([
  { scenario_id: "B1_local_only", result: "applied", label: "適用済み" },
  { scenario_id: "B2_unchanged", result: "unchanged", label: "変更不要" },
  { scenario_id: "B3_rolled_back", result: "rolled_back", label: "復旧済み" },
] as const);

export const stPortGetBrowserParentName = "ST-PORT actual CP GET results render in the production application browser";
export const stPortGetBrowserScenarioNames = Object.freeze([
  stPortGetBrowserParentName,
  ...stPortAcceptedGetCases.map((entry) => `ST-PORT actual CP GET ${entry.scenario_id} renders ${entry.result}`),
]);

const sourceSetKeys = ["control_panel_sha", "updater_sha", "contracts_sha", "worker_sha"] as const;
type SourceSet = Readonly<Record<(typeof sourceSetKeys)[number], string>>;
type AcceptedCase = (typeof stPortAcceptedGetCases)[number];

export type StPortGetBrowserCase = Readonly<{
  scenario_id: AcceptedCase["scenario_id"];
  result: AcceptedCase["result"];
  job_id: string;
  system_updates: Record<string, unknown>;
  selectedJob: SystemUpdateJob;
}>;

export type StPortGetBrowserArtifact = Readonly<{
  schema_version: 1;
  source_set: SourceSet;
  cases: readonly StPortGetBrowserCase[];
}>;

const secretField = /(?:^|_)(?:token|secret|password|passphrase|nonce|credential|cookie|headers?|grant)(?:_|$)/i;
// Public authorization_id, lease_id and session_id are correlation fields.
// Credential values (including lease_token) are rejected by the producer before
// export; this secondary guard rejects credential fields, not public IDs.
const secretExactFields = new Set(["authorization"]);

function requireCondition(condition: unknown, code: string): asserts condition {
  // Never attach the input object: it contains full policy/config digests.
  if (!condition) throw new Error(code);
}

function record(value: unknown, code: string): Record<string, unknown> {
  requireCondition(typeof value === "object" && value !== null && !Array.isArray(value), code);
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, keys: readonly string[], code: string) {
  requireCondition(Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key)), code);
}

function parseJSON(value: string, code: string): unknown {
  try { return JSON.parse(value); } catch { throw new Error(code); }
}

function sourceSet(value: unknown): SourceSet {
  const candidate = record(value, "st_port_get_source_set_invalid");
  exactKeys(candidate, sourceSetKeys, "st_port_get_source_set_keys_invalid");
  for (const key of sourceSetKeys) {
    requireCondition(typeof candidate[key] === "string" && /^[a-f0-9]{40}$/.test(candidate[key]), "st_port_get_source_sha_invalid");
  }
  return candidate as SourceSet;
}

function assertNoCredentialFields(value: unknown, depth = 0) {
  requireCondition(depth <= 32, "st_port_get_artifact_depth_exceeded");
  if (Array.isArray(value)) {
    for (const item of value) assertNoCredentialFields(item, depth + 1);
  } else if (typeof value === "object" && value !== null) {
    for (const [key, entry] of Object.entries(value)) {
      requireCondition(!secretField.test(key) && !secretExactFields.has(key.toLowerCase()), "st_port_get_artifact_contains_credential_field");
      assertNoCredentialFields(entry, depth + 1);
    }
  }
}

export function loadStPortGetBrowserArtifact(): StPortGetBrowserArtifact {
  const path = process.env.AUTOSTREAM_ST_PORT_GET_ARTIFACT;
  const expectedSourceJSON = process.env.AUTOSTREAM_ST_PORT_EXPECTED_SOURCE_SET;
  requireCondition(path && isAbsolute(path), "st_port_get_artifact_required");
  requireCondition(expectedSourceJSON, "st_port_get_expected_source_set_required");
  let body: string;
  try {
    const info = statSync(path);
    requireCondition(info.isFile() && info.size > 0 && info.size <= 8 * 1024 * 1024, "st_port_get_artifact_size_invalid");
    body = readFileSync(path, "utf8");
  } catch {
    throw new Error("st_port_get_artifact_unavailable");
  }
  const artifact = record(parseJSON(body, "st_port_get_artifact_json_invalid"), "st_port_get_artifact_invalid");
  exactKeys(artifact, ["schema_version", "source_set", "cases"], "st_port_get_artifact_keys_invalid");
  requireCondition(artifact.schema_version === 1, "st_port_get_artifact_version_invalid");
  const actualSources = sourceSet(artifact.source_set);
  const expectedSources = sourceSet(parseJSON(expectedSourceJSON, "st_port_get_expected_source_set_invalid"));
  requireCondition(isDeepStrictEqual(actualSources, expectedSources), "st_port_get_source_set_mismatch");
  requireCondition(Array.isArray(artifact.cases) && artifact.cases.length === stPortAcceptedGetCases.length, "st_port_get_accepted_case_inventory_invalid");
  assertNoCredentialFields(artifact);
  const seenJobIDs = new Set<string>();
  const cases = stPortAcceptedGetCases.map((expected) => {
    const matching = (artifact.cases as unknown[]).filter((item) => record(item, "st_port_get_case_invalid").scenario_id === expected.scenario_id);
    requireCondition(matching.length === 1, "st_port_get_case_missing_or_duplicate");
    const entry = record(matching[0], "st_port_get_case_invalid");
    exactKeys(entry, ["scenario_id", "job_id", "result", "system_updates"], "st_port_get_case_keys_invalid");
    requireCondition(entry.result === expected.result, "st_port_get_case_result_mismatch");
    requireCondition(typeof entry.job_id === "string" && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(entry.job_id), "st_port_get_job_identity_invalid");
    requireCondition(!seenJobIDs.has(entry.job_id), "st_port_get_job_identity_duplicate");
    seenJobIDs.add(entry.job_id);
    const wire = record(entry.system_updates, "st_port_get_response_invalid");
    for (const field of ["updaters", "hosts", "targets", "jobs"]) {
      requireCondition(Array.isArray(wire[field]), "st_port_get_response_collection_missing");
    }
    const selected = (wire.jobs as unknown[]).filter((value) => record(value, "st_port_get_job_invalid").id === entry.job_id);
    requireCondition(selected.length === 1, "st_port_get_selected_job_missing_or_duplicate");
    const rawJob = record(selected[0], "st_port_get_job_invalid");
    const normalized = normalizeSystemUpdatesResponse(wire);
    const normalizedJobs = normalized.jobs.filter((job) => job.id === entry.job_id);
    requireCondition(normalizedJobs.length === 1, "st_port_get_normalized_job_missing");
    const selectedJob = normalizedJobs[0];
    requireCondition(selectedJob.operation === "port_reconfigure" && selectedJob.port_reconfigure?.port_contract_version === 2, "st_port_get_versioned_plan_required");
    requireCondition(selectedJob.port_result?.result === expected.result, "st_port_get_accepted_result_required");
    requireCondition(selectedJob.status === (expected.result === "rolled_back" ? "rolled_back" : "succeeded"), "st_port_get_terminal_status_mismatch");
    requireCondition(!selectedJob.recovery_required && !selectedJob.last_recovery_observation, "st_port_get_accepted_result_has_recovery_hold");
    requireCondition(isDeepStrictEqual(JSON.parse(JSON.stringify(selectedJob.port_result)), rawJob.port_result), "st_port_get_typed_result_was_changed");
    return {
      scenario_id: expected.scenario_id,
      result: expected.result,
      job_id: entry.job_id,
      system_updates: wire,
      selectedJob,
    };
  });
  return { schema_version: 1, source_set: actualSources, cases };
}

export async function withStPortGetBrowser(
  run: (browser: BrowserHarness, baseUrl: string) => Promise<void>,
  registerCleanup: (cleanup: () => Promise<void>) => void,
) {
  const webRoot = fileURLToPath(new URL("../..", import.meta.url));
  const baseUrl = "http://127.0.0.1:3004";
  let alreadyListening = false;
  try {
    await fetch(baseUrl, { signal: AbortSignal.timeout(1_000) });
    alreadyListening = true;
  } catch { /* A new owned server is required. */ }
  requireCondition(!alreadyListening, "st_port_browser_port_already_in_use");
  const server = await ensureWebServer(webRoot, baseUrl);
  const browserPromise = BrowserHarness.launch();
  let cleanupPromise: Promise<void> | undefined;
  const cleanup = () => {
    cleanupPromise ??= (async () => {
      const browser = await browserPromise.catch(() => undefined);
      try { await browser?.close(); } finally { await server.close(); }
    })();
    return cleanupPromise;
  };
  // The test hook also owns cleanup when Node cancels a timed-out scenario.
  // Share one promise so the normal finally and cancellation hook cannot race.
  registerCleanup(cleanup);
  try {
    const browser = await browserPromise;
    await run(browser, server.baseUrl);
    browser.assertNoFatalError();
  } finally {
    await cleanup();
  }
}

export function installStPortGetRoutes(browser: BrowserHarness, entry: StPortGetBrowserCase) {
  let portMutations = 0;
  browser.setRouteResolver(({ method, url }) => {
    const path = new URL(url).pathname.replace(/\/+$/, "") || "/";
    if (path === "/system-updates") {
      if (method !== "GET") {
        portMutations += 1;
        return { status: 405, body: { code: "method_not_allowed" } };
      }
      // Exact body collected by the authenticated CP helper. No generated jobs,
      // fixture fallback, or replacement of a missing/invalid accepted result.
      return { status: 200, body: entry.system_updates, requiredResponse: true };
    }
    if (path.startsWith("/system-updates/") && method !== "GET") {
      portMutations += 1;
      return { status: 405, body: { code: "method_not_allowed" } };
    }
    if (method === "GET" && path === "/auth/me") return { body: {
      user: { id: "st-port-ui-reader", username: "st-port-ui-reader", email: "reader@example.test", roles: [] },
      permissions: ["system_updates.read"],
    } };
    if (method === "GET" && path === "/setup/status") return { body: { setup_enabled: true, setup_required: false } };
    if (method === "GET" && path === "/settings/app") return { body: { app_name: "AutoStream", timezone: "Asia/Tokyo" } };
    if (method === "GET" && path === "/account/preferences/ui") return { body: { theme_id: "autostream", color_mode: "light", revision: 1 } };
    if (method === "GET" && path === "/version") return { body: {
      service: "control-panel", version: "2.0.0", commit: "st-port-browser-shell", build_date: "2026-09-06T00:00:00Z",
      update_available: false, latest_version: "v2.0.0", update_check_source: "disabled", service_updates: {},
    } };
    if (method === "GET" && (path === "/service-health" || path === "/nodes")) return { body: [] };
    return null;
  });
  return { portMutationCount: () => portMutations };
}

export function acceptedJobDOMExpression(entry: StPortGetBrowserCase) {
  const labels = stPortAcceptedGetCases.map((value) => value.label);
  const expectedLabel = stPortAcceptedGetCases.find((value) => value.result === entry.result)!.label;
  const { before, target } = entry.selectedJob.port_reconfigure!;
  const expectedChange = `local ${before!.local_listen_port} → ${target!.local_listen_port} / 広告 ${before!.advertised_port} → ${target!.advertised_port}`;
  return `(() => {
    const rows = [...document.querySelectorAll('[data-system-update-job-id]')].filter(row => row.getAttribute('data-system-update-job-id') === ${JSON.stringify(entry.job_id)});
    const row = rows[0];
    const cells = row ? [...row.querySelectorAll('td')] : [];
    const status = cells[2]?.innerText || '';
    const message = cells[4]?.innerText || '';
    const rect = row?.getBoundingClientRect();
    return {
      rowCount: rows.length,
      applicationHeading: [...document.querySelectorAll('h1')].some(node => node.textContent === 'アプリケーション情報'),
      visible: Boolean(row && rect && rect.width > 0 && rect.height > 0 && getComputedStyle(row).display !== 'none'),
      statusMatches: status.includes(${JSON.stringify(expectedLabel)}),
      messageMatches: message.includes(${JSON.stringify(expectedLabel)}),
      otherAcceptedLabel: ${JSON.stringify(labels)}.some(label => label !== ${JSON.stringify(expectedLabel)} && status.includes(label)),
      unresolvedLabel: status.includes('確認中') || status.includes('復旧未完了'),
      changeMatches: (cells[1]?.innerText || '') === ${JSON.stringify(expectedChange)},
    };
  })()`;
}

export type AcceptedJobDOM = Readonly<{
  rowCount: number;
  applicationHeading: boolean;
  visible: boolean;
  statusMatches: boolean;
  messageMatches: boolean;
  otherAcceptedLabel: boolean;
  unresolvedLabel: boolean;
  changeMatches: boolean;
}>;

export function acceptedJobDOMMatches(value: AcceptedJobDOM) {
  return value.rowCount === 1 && value.applicationHeading && value.visible && value.statusMatches && value.messageMatches
    && !value.otherAcceptedLabel && !value.unresolvedLabel && value.changeMatches;
}
