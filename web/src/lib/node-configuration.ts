type NodeConfigurationPermissions = {
  serviceType: string;
  canCreateTokens: boolean;
  canRevokeTokens?: boolean;
  canResolveManagedSecret: boolean;
  requiresManagedSecret: boolean;
  canExecuteSystemUpdates: boolean;
};

export function canIssueNodeConfiguration(permissions: NodeConfigurationPermissions) {
  const requiresSecretUpdate = permissions.requiresManagedSecret
    || permissions.serviceType === "update_agent";
  return permissions.canCreateTokens
    && (!requiresSecretUpdate || permissions.canResolveManagedSecret)
    && (permissions.serviceType !== "update_agent" || permissions.canExecuteSystemUpdates);
}

export function canRotateNodeRuntimeToken(permissions: NodeConfigurationPermissions) {
  return permissions.canRevokeTokens === true && canIssueNodeConfiguration(permissions);
}

export function canRegenerateNodeConfigureToken(
  permissions: NodeConfigurationPermissions,
) {
  return canRotateNodeRuntimeToken(permissions);
}
