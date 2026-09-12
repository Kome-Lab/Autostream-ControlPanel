"use client";

import { observabilityActionPlans, type ObservabilityActionPlan } from "@/features/observability/action-policy";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { hasPermission } from "@/lib/auth/permissions";
import { type ResourceRow } from "./resource-form-types";
import { requiredPermissionText } from "./resource-permissions";
import { isRecord } from "./resource-values";

type ObservabilityAction = {
  plan: ObservabilityActionPlan;
  allowed: boolean;
  permissionText?: string;
};

export function observabilityActionButtons(resource: ResourceDefinition, row: ResourceRow, currentUser: Parameters<typeof hasPermission>[0]): ObservabilityAction[] {
  const allowed = (permission: string) => hasPermission(currentUser, permission);
  return observabilityActionPlans(resource.path, row).map((plan) => ({
    plan,
    allowed: allowed(plan.permission),
    permissionText: requiredPermissionText(plan.permission),
  }));
}

export function observabilityActionSuccessMessage(action: { path: string; label: string }, response: unknown) {
  if (action.path.includes("/diagnostics/rerun")) {
    const outcome = isRecord(response) && typeof response.outcome === "string" ? response.outcome.trim().toLowerCase() : "";
    if (outcome === "inconclusive") return "診断を再評価しましたが、保存済みのシグナルでは結論を更新できませんでした。";
    return "診断を再評価しました。";
  }
  return `${action.label}を実行しました。`;
}
