export const UPDATER_CONFIGURATION_PATH = "/etc/autostream/updater.json";
export const UPDATER_CONFIGURATION_EXAMPLE = "release/autostream-updater.json.example";

export const UPDATER_MANUAL_CONFIGURATION_STEPS = [
  "panel_url、node_id、この画面のNode Runtime Tokenを設定します。",
  "非公開Releaseを使う場合はfine-grained GitHub読取Tokenを設定します。",
  "targetsを実際のサービスに合わせ、Control Panel／Observabilityにはroot所有のbackup_argvを指定します。",
  "Docker対象ごとに sudo /usr/local/bin/autostream-updater bootstrap-docker-target --config /etc/autostream/updater.json --target <ID> を実行してrollback baselineとversion envを初期化し、出力digestで全ゼロsentinelのcompose_config_sha256を置換します。全targetのsentinel置換後、起動前に sudo /usr/local/bin/autostream-updater validate-config --config /etc/autostream/updater.json を実行し、必要に応じてcompose-config-digestでも最終値を照合します。",
  "設定をroot:autostream-updater・0640で保護してからautostream-updaterを再起動します。",
] as const;

export function updaterManualConfiguration(configuration?: {
  manual_configuration_required?: boolean;
  configuration_path?: string;
  configuration_example?: string;
} | null) {
  if (!configuration?.manual_configuration_required) return null;
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
