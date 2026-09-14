"use client";
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";


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

export function observabilityActionButtons(resource: ResourceDefinition, row: ResourceRow, currentUser: Parameters<typeof hasPermission>[0], uiText: UICopy = japaneseCopy): ObservabilityAction[] {
  const allowed = (permission: string) => hasPermission(currentUser, permission);
  return observabilityActionPlans(resource.path, row).map((plan) => ({
    plan,
    allowed: allowed(plan.permission),
    permissionText: requiredPermissionText(plan.permission, uiText),
  }));
}

export function observabilityActionSuccessMessage(action: { path: string; label: string }, response: unknown, uiText: UICopy = japaneseCopy) {
  if (action.path.includes("/diagnostics/rerun")) {
    const outcome = isRecord(response) && typeof response.outcome === "string" ? response.outcome.trim().toLowerCase() : "";
    if (outcome === "inconclusive") return uiText("診断を再評価しましたが、保存済みのシグナルでは結論を更新できませんでした。");
    return uiText("診断を再評価しました。");
  }
  return uiText("{0}を実行しました。", action.label);
}
