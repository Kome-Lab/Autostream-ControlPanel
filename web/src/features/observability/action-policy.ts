import type { ActionDescriptor } from "@/lib/foundation/actions/contracts";
import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";

export type ObservabilityActionID = "OBS-01" | "OBS-02" | "OBS-03" | "OBS-04" | "OBS-05";

export type ObservabilityActionPlan = Readonly<{
  id: ObservabilityActionID;
  sourcePath: string;
  sourceKey: string;
  key: string;
  label: string;
  path: string;
  permission: string;
  risk: "guarded" | "high" | "critical";
  confirmation: "consequence" | "typed-target";
  targetLabel?: string;
  authorityFingerprint: string;
  auditAction: string;
  emphasis?: boolean;
}>;

export type ObservabilityActionSnapshot = Readonly<{
  plan: ObservabilityActionPlan;
  evaluation: "allowed" | "denied" | "unknown";
  freshness: "fresh" | "refreshing" | "stale" | "unavailable";
}>;

export type ObservabilityActionExecutionResult =
  | Readonly<{ kind: "succeeded"; value: unknown }>
  | Readonly<{
      kind: "blocked";
      reason:
        | "confirmation-required"
        | "typed-target-mismatch"
        | "duplicate"
        | "outcome-unknown"
        | "not-allowed"
        | "freshness-unavailable"
        | "authority-changed";
    }>
  | Readonly<{ kind: "conflict"; error: AdaptedAPIError }>
  | Readonly<{ kind: "failed"; error: AdaptedAPIError }>
  | Readonly<{ kind: "outcome_unknown" }>;

export type ObservabilityActionController = Readonly<{
  execute: (
    plan: ObservabilityActionPlan,
    confirmation: Readonly<{ confirmed: boolean; typedValue?: string }>,
  ) => Promise<ObservabilityActionExecutionResult>;
  reconcile: (plan: ObservabilityActionPlan) => void;
}>;

export function observabilityActionPlans(
  resourcePath: string,
  row: Readonly<Record<string, unknown>>,
): readonly ObservabilityActionPlan[] {
  const status = rowString(row, ["status"]).trim().toLowerCase();
  const fingerprint = authorityFingerprint(row, status);
  const sourceKey = observabilityRowKey(row);
  if (!sourceKey) return Object.freeze([]);
  if (resourcePath === "/observability/incidents") {
    const id = rowString(row, ["id"]);
    if (!id || ["resolved", "closed", "ignored"].includes(status)) return Object.freeze([]);
    const actions: ObservabilityActionPlan[] = [];
    if (status !== "acknowledged") {
      actions.push(plan({
        id: "OBS-01",
        sourcePath: resourcePath,
        sourceKey,
        key: "acknowledge",
        label: "確認済みにする",
        path: `/observability/incidents/${encodeURIComponent(id)}/acknowledge`,
        permission: "incidents.acknowledge",
        risk: "guarded",
        confirmation: "consequence",
        authorityFingerprint: fingerprint,
        auditAction: "incidents.acknowledge",
      }));
    }
    actions.push(plan({
      id: "OBS-02",
      sourcePath: resourcePath,
      sourceKey,
      key: "resolve",
      label: "解決済みにする",
      path: `/observability/incidents/${encodeURIComponent(id)}/resolve`,
      permission: "incidents.resolve",
      risk: "high",
      confirmation: "consequence",
      authorityFingerprint: fingerprint,
      auditAction: "incidents.resolve",
      emphasis: true,
    }));
    return Object.freeze(actions);
  }

  if (resourcePath === "/observability/diagnostics") {
    const incidentID = rowString(row, ["incident_id"]);
    if (!incidentID) return Object.freeze([]);
    return Object.freeze([diagnosticsPlan(resourcePath, sourceKey, incidentID, fingerprint)]);
  }

  if (resourcePath !== "/observability/remediation-actions") return Object.freeze([]);
  const id = rowString(row, ["id"]);
  const incidentID = rowString(row, ["incident_id"]);
  const actionName = rowString(row, ["action"]).trim().toLowerCase();
  if (actionName === "rerun_diagnostics") {
    return incidentID ? Object.freeze([diagnosticsPlan(resourcePath, sourceKey, incidentID, fingerprint)]) : Object.freeze([]);
  }
  const mode = rowString(row, ["mode"]).trim().toLowerCase();
  if (
    !id
    || mode === "suggest_only"
    || mode === "disabled"
    || ["executed", "skipped", "blocked", "failed", "cancelled", "disabled"].includes(status)
  ) {
    return Object.freeze([]);
  }
  const requiresApproval = rowBoolean(row, ["requires_approval"], false);
  const safeAuto = rowBoolean(row, ["safe_auto"], false);
  if (status === "pending_approval" || (status === "suggested" && requiresApproval)) {
    return Object.freeze([plan({
      id: "OBS-04",
      sourcePath: resourcePath,
      sourceKey,
      key: "approve",
      label: "承認",
      path: `/observability/remediation-actions/${encodeURIComponent(id)}/approve`,
      permission: "remediation.approve",
      risk: "high",
      confirmation: "consequence",
      authorityFingerprint: fingerprint,
      auditAction: "remediation.approve",
      emphasis: true,
    })]);
  }
  if (status === "approved" || (status === "suggested" && safeAuto)) {
    const targetLabel = approvedRemediationLabel(row);
    if (!targetLabel) return Object.freeze([]);
    return Object.freeze([plan({
      id: "OBS-05",
      sourcePath: resourcePath,
      sourceKey,
      key: "execute",
      label: "実行",
      path: `/observability/remediation-actions/${encodeURIComponent(id)}/execute`,
      permission: "remediation.execute",
      risk: "critical",
      confirmation: "typed-target",
      targetLabel,
      authorityFingerprint: fingerprint,
      auditAction: "remediation.execute",
      emphasis: true,
    })]);
  }
  return Object.freeze([]);
}

export function createObservabilityActionController(dependencies: Readonly<{
  refresh: (plan: ObservabilityActionPlan) => Promise<ObservabilityActionSnapshot>;
  mutate: (plan: ObservabilityActionPlan) => Promise<unknown>;
}>): ObservabilityActionController {
  const pending = new Set<string>();
  const unresolved = new Set<string>();
  return Object.freeze({
    async execute(plan, confirmation) {
      const scope = actionScope(plan);
      if (unresolved.has(scope)) return Object.freeze({ kind: "blocked", reason: "outcome-unknown" });
      if (pending.has(scope)) return Object.freeze({ kind: "blocked", reason: "duplicate" });
      if (!confirmation.confirmed) return Object.freeze({ kind: "blocked", reason: "confirmation-required" });
      if (plan.confirmation === "typed-target" && confirmation.typedValue !== plan.targetLabel) {
        return Object.freeze({ kind: "blocked", reason: "typed-target-mismatch" });
      }
      pending.add(scope);
      try {
        let refreshed: ObservabilityActionSnapshot;
        try {
          refreshed = await dependencies.refresh(plan);
        } catch {
          return Object.freeze({ kind: "blocked", reason: "freshness-unavailable" });
        }
        if (refreshed.evaluation !== "allowed") return Object.freeze({ kind: "blocked", reason: "not-allowed" });
        if (refreshed.freshness !== "fresh") return Object.freeze({ kind: "blocked", reason: "freshness-unavailable" });
        if (!sameAuthority(plan, refreshed.plan)) return Object.freeze({ kind: "blocked", reason: "authority-changed" });
        try {
          return Object.freeze({ kind: "succeeded", value: await dependencies.mutate(plan) });
        } catch (error) {
          const adapted = adaptAPIError(error);
          if (["network", "timeout", "unavailable", "protocol", "unknown"].includes(adapted.kind)) {
            unresolved.add(scope);
            return Object.freeze({ kind: "outcome_unknown" });
          }
          if (adapted.kind === "conflict") return Object.freeze({ kind: "conflict", error: adapted });
          return Object.freeze({ kind: "failed", error: adapted });
        }
      } finally {
        pending.delete(scope);
      }
    },
    reconcile(plan) {
      unresolved.delete(actionScope(plan));
    },
  });
}

export function buildObservabilityActionDescriptor(action: ObservabilityActionPlan): ActionDescriptor {
  const typed = action.confirmation === "typed-target";
  return Object.freeze({
    id: action.id,
    labelKey: "action",
    risk: action.risk,
    target: Object.freeze({
      resourceType: "observability",
      resourceId: action.sourceKey,
      ...(typed && action.targetLabel ? { publicLabel: action.targetLabel } : {}),
    }),
    applicability: Object.freeze({ ruleIds: Object.freeze([`observability-${action.id.toLowerCase()}-state`]), requiredSections: Object.freeze([action.sourcePath]) }),
    confirmation: typed
      ? Object.freeze({ mode: "typed-target" as const, consequenceKey: "dangerousNotice", typedToken: Object.freeze({ kind: "target-label" as const }), requireSubmitRevalidation: true as const })
      : Object.freeze({ mode: "consequence" as const, consequenceKey: "dangerousNotice", requireSubmitRevalidation: true as const }),
    duplicate: Object.freeze({ scope: "resource-action" as const, whilePending: "block" as const }),
    retry: Object.freeze({ kind: "never" as const }),
    audit: Object.freeze({ action: action.auditAction, labelKey: "action", safeReferenceFieldIds: Object.freeze(typed ? ["publicLabel"] : []) }),
    stateIndependent: false,
    revalidation: Object.freeze({ kind: "revision" as const }),
  });
}

export function findRefreshedObservabilityPlan(data: unknown, original: ObservabilityActionPlan) {
  const row = observabilityRows(data).find((candidate) => observabilityRowKey(candidate) === original.sourceKey);
  return row ? observabilityActionPlans(original.sourcePath, row).find((candidate) => candidate.id === original.id) : undefined;
}

export function observabilityRows(data: unknown): readonly Readonly<Record<string, unknown>>[] {
  if (Array.isArray(data)) return Object.freeze(data.filter(plainRecord));
  if (!plainRecord(data)) return Object.freeze([]);
  for (const key of ["items", "data", "results"]) {
    const value = data[key];
    if (Array.isArray(value)) return Object.freeze(value.filter(plainRecord));
  }
  return Object.freeze([]);
}

function diagnosticsPlan(sourcePath: string, sourceKey: string, incidentID: string, authorityFingerprint: string) {
  return plan({
    id: "OBS-03",
    sourcePath,
    sourceKey,
    key: "diagnostic-rerun",
    label: "診断を再評価",
    path: `/observability/incidents/${encodeURIComponent(incidentID)}/diagnostics/rerun`,
    permission: "diagnostics.run",
    risk: "guarded",
    confirmation: "consequence",
    authorityFingerprint,
    auditAction: "diagnostics.run",
    emphasis: true,
  });
}

function plan(value: ObservabilityActionPlan): ObservabilityActionPlan {
  return Object.freeze({ ...value });
}

function sameAuthority(left: ObservabilityActionPlan, right: ObservabilityActionPlan) {
  return left.id === right.id
    && left.path === right.path
    && left.permission === right.permission
    && left.authorityFingerprint === right.authorityFingerprint;
}

function actionScope(action: ObservabilityActionPlan) {
  return `${action.id}:${action.path}`;
}

function observabilityRowKey(row: Readonly<Record<string, unknown>>) {
  return rowString(row, ["id", "incident_id"]);
}

function plainRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function authorityFingerprint(row: Readonly<Record<string, unknown>>, status: string) {
  return JSON.stringify([
    status,
    scalar(row.revision),
    scalar(row.updated_at),
    scalar(row.mode),
    scalar(row.requires_approval),
    scalar(row.safe_auto),
  ]);
}

function scalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : null;
}

function firstSafePublicLabel(row: Readonly<Record<string, unknown>>, keys: readonly string[]) {
  for (const key of keys) {
    const value = rowString(row, [key]);
    if (isSafePublicLabel(value)) return value;
  }
  return "";
}

function approvedRemediationLabel(row: Readonly<Record<string, unknown>>) {
  const explicit = firstSafePublicLabel(row, ["target_label", "name"]);
  if (explicit) return explicit;
  const action = rowString(row, ["action"]);
  const normalized = action.trim().toLowerCase().replace(/[\s-]+/g, "_");
  return new Set([
    "restart_encoder",
    "restart_encoder_recorder",
    "restart_worker",
    "rerun_diagnostics",
    "refresh_service_status",
    "retry_package_remux",
    "retry_gdrive_upload",
    "switch_worker",
    "clear_stale_warning",
  ]).has(normalized) && isSafePublicLabel(action) ? action : "";
}

function isSafePublicLabel(value: string) {
  return value.length > 0
    && value.length <= 128
    && value.trim() === value
    && !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(value)
    && !/^[^\s@]+@[^\s@]+$/.test(value)
    && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(value);
}

function rowString(row: Readonly<Record<string, unknown>>, keys: readonly string[]) {
  for (const key of keys) {
    const value = row[key];
    if (typeof value === "string") return value;
    if (typeof value === "number" && Number.isFinite(value)) return String(value);
  }
  return "";
}

function rowBoolean(row: Readonly<Record<string, unknown>>, keys: readonly string[], fallback: boolean) {
  for (const key of keys) {
    const value = row[key];
    if (typeof value === "boolean") return value;
    if (typeof value === "number") return value !== 0;
    if (typeof value === "string") {
      const normalized = value.trim().toLowerCase();
      if (["1", "true", "yes", "on"].includes(normalized)) return true;
      if (["0", "false", "no", "off"].includes(normalized)) return false;
    }
  }
  return fallback;
}
