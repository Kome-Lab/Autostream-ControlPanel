"use client";

import { Trash2 } from "lucide-react";
import { type ResourceDefinition } from "@/features/resources/resource-config";
import { ResourceActionControl } from "@/features/resources/resource-action-control";
import { type ResourceActionController, type ResourceActionExecutionResult } from "@/features/resources/resource-action-controller";
import { resourceActionID, type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";
import { type ResourceRow } from "./resource-form-types";
import { resourceRowLabel } from "./resource-values";

export function DeleteResourceButton({
  resource,
  row,
  controller,
  disabled,
  onResult,
}: {
  resource: ResourceDefinition;
  row: ResourceRow;
  controller: ResourceActionController;
  disabled: boolean;
  permission?: string;
  onResult: (result: ResourceActionExecutionResult, intent: ResourceActionIntent) => void;
}) {
  const label = resourceRowLabel(row);
  const actionID = resourceActionID(resource.path, "delete");
  if (!actionID) return null;
  const intent: ResourceActionIntent = Object.freeze({ id: actionID, row: Object.freeze({ ...row }), publicLabel: label });
  return (
    <ResourceActionControl
      controller={controller}
      intent={intent}
      label={`${label} を削除`}
      disabled={disabled}
      buttonProps={{ variant: "destructive", size: "icon-sm" }}
      onResult={onResult}
    >
        <Trash2 className="size-4" />
    </ResourceActionControl>
  );
}
