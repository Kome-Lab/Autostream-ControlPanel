export const UPDATER_CONFIGURATION_PATH = "/etc/autostream/updater.json";
export const UPDATER_CONFIGURATION_EXAMPLE = "release/autostream-updater.json.example";

export const UPDATER_MANUAL_CONFIGURATION_STEPS = [
  "panel_url、node_id、この画面のNode Runtime Tokenを設定します。",
  "非公開Releaseを使う場合はfine-grained GitHub読取Tokenを設定します。",
  "API、hosts、targets、SSH鍵をこのホストの構成に合わせます。",
  "設定をroot:autostream-updater・0640で保護し、sudo /usr/local/bin/autostream-updater validate-config --config /etc/autostream/updater.json で検証してからUpdaterを再起動します。",
] as const;

type UpdaterConfigurationMetadata = {
  manual_configuration_required?: boolean;
  configure_command?: string;
  configuration_path?: string;
  configuration_example?: string;
};

export function updaterManualConfiguration(configuration?: UpdaterConfigurationMetadata | null) {
  if (!configuration?.manual_configuration_required || configuration.configure_command?.trim()) return null;
  return {
    path: configuration.configuration_path || UPDATER_CONFIGURATION_PATH,
    example: configuration.configuration_example || UPDATER_CONFIGURATION_EXAMPLE,
    steps: UPDATER_MANUAL_CONFIGURATION_STEPS,
  };
}

type NodeConfigurationPermissions = {
  serviceType: string;
  canCreateTokens: boolean;
  canRevokeTokens?: boolean;
  canResolveManagedSecret: boolean;
  requiresManagedSecret: boolean;
  canExecuteSystemUpdates: boolean;
};

export function canIssueNodeConfiguration(permissions: NodeConfigurationPermissions) {
  return permissions.canCreateTokens
    && (!permissions.requiresManagedSecret || permissions.canResolveManagedSecret)
    && (permissions.serviceType !== "update_agent" || permissions.canExecuteSystemUpdates);
}

export function canRotateNodeRuntimeToken(permissions: NodeConfigurationPermissions) {
  return permissions.canRevokeTokens === true && canIssueNodeConfiguration(permissions);
}

export function canRegenerateNodeConfigureToken(
  permissions: NodeConfigurationPermissions,
  configuration?: UpdaterConfigurationMetadata | null,
) {
  return updaterManualConfiguration(configuration) === null && canRotateNodeRuntimeToken(permissions);
}
