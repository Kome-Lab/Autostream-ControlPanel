export type Locale = "ja" | "en";

export type StreamStatus =
  | "draft"
  | "scheduled"
  | "ready"
  | "starting"
  | "live"
  | "stopping"
  | "stopped"
  | "completed"
  | "failed"
  | "error";

export type Stream = {
  id: string;
  name: string;
  status: StreamStatus | string;
  input_source?: string;
  output_target?: string;
  assigned_worker_id?: string;
  assigned_encoder_id?: string;
  scheduled_start_at?: string;
  scheduled_end_at?: string;
  started_at?: string;
  ended_at?: string;
  updated_at?: string;
  created_at?: string;
  discord_config_id?: string;
  discord_guild_id?: string;
  discord_voice_channel_id?: string;
  discord_text_channel_id?: string;
  auto_start_trigger?: string;
  encoder_profile_id?: string;
  caption_profile_id?: string;
  overlay_profile_id?: string;
  encoder_input_url?: string;
  youtube_output_id?: string;
  archive_profile_id?: string;
  archive_drive_destination_id?: string;
  archive_oauth_account_id?: string;
  archive_folder_id_configured?: boolean;
  archive_masked_folder_id?: string;
  archive_shared_drive?: boolean;
  archive_shared_drive_id?: string;
  archive_file_name?: string;
  archive_retention_days?: number;
};

export type WorkerNode = {
  id: string;
  service_id?: string;
  service_type: string;
  service_name: string;
  description?: string;
  status: string;
  health_status?: string;
  assignment_role?: string;
  current_stream_id?: string;
  transport_mode?: "ssh_v1" | "pull_v2";
  execution_host_id?: string;
  ownership_epoch?: number;
  host?: string;
  port?: number;
  ssl_enabled?: boolean;
  public_url?: string;
  desired_endpoint?: NodeServiceEndpoint;
  applied_endpoint?: NodeServiceEndpoint;
  reported_endpoint?: NodeServiceEndpoint;
  endpoint_revision?: number;
  endpoint_status?: string;
  version?: string;
  reported_version?: string;
  reported_commit?: string;
  reported_build_date?: string;
  reported_hostname?: string;
  reported_os?: string;
  reported_arch?: string;
  last_reported_at?: string;
  last_heartbeat_at?: string;
  heartbeat_age_sec?: number;
  capabilities?: Record<string, unknown>;
  reported_capabilities?: Record<string, unknown>;
  metrics?: Record<string, number | string>;
  configure_token_expires_at?: string;
  configure_token_used_at?: string;
  node_token_rotated_at?: string;
};

export type NodeServiceEndpoint = {
  host: string;
  port: number;
  ssl_enabled: boolean;
  public_url: string;
};

export type AuditLog = {
  id: string;
  timestamp: string;
  action: string;
  actor_username?: string;
  actor_ip?: string;
  user_agent?: string;
  result: string;
  resource_type?: string;
  resource_id?: string;
};

export type MetricSnapshot = {
  name: string;
  service_id: string;
  service_type: string;
  stream_id?: string;
  status?: string;
  value?: number;
  attributes?: Record<string, unknown>;
  updated_at: string;
};

export type MFAStatus = {
  available: boolean;
  enabled: boolean;
  method?: string;
  pending_enrollment: boolean;
  recovery_code_count?: number;
  policy_mode?: string;
  required?: boolean;
  updated_at?: string;
};

export type MFAEnrollResponse = {
  method: "totp" | string;
  secret: string;
  provisioning_uri: string;
  recovery_codes: string[];
  message?: string;
};

export type PasskeyCredential = {
  id: string;
  user_id: string;
  name: string;
  credential_id_hash?: string;
  sign_count: number;
  transports?: string[];
  aaguid?: string;
  backup_eligible: boolean;
  backed_up: boolean;
  created_at: string;
  updated_at: string;
  last_used_at?: string;
};

export type PasskeyRegistrationStart = {
  registration_token: string;
  expires_at: string;
  public_key: Record<string, unknown>;
};

export type PasskeyLoginStart = {
  challenge_token: string;
  expires_at: string;
  public_key: Record<string, unknown>;
};

export type OAuthUserLink = {
  id: string;
  user_id: string;
  provider_id: string;
  provider_type: string;
  subject: string;
  email?: string;
  created_at: string;
  updated_at: string;
};

export type OAuthLoginProvider = {
  id: string;
  provider_type: string;
  name: string;
  enabled: boolean;
  redirect_uri?: string;
};

export type OAuthLinkStartResponse = {
  provider: OAuthLoginProvider;
  authorization_url: string;
  state: string;
  nonce?: string;
  expires_at: string;
};

export type CurrentUser = {
  user: {
    id: string;
    username: string;
    email?: string;
    status?: string;
    roles?: string[];
    avatar_url?: string;
    avatar_updated_at?: string;
  };
  permissions: string[];
};

export type SetupStatus = {
  setup_enabled: boolean;
  setup_required: boolean;
};

export type AppSettings = {
  app_name: string;
  timezone: string;
  google_analytics_enabled?: boolean;
  google_analytics_measurement_id?: string;
  turnstile_enabled?: boolean;
  turnstile_site_key?: string;
  turnstile_configured?: boolean;
  updated_at?: string;
};

export type ManagedAppSettings = AppSettings & {
  smtp_enabled?: boolean;
  smtp_host?: string;
  smtp_port?: number;
  smtp_starttls?: boolean;
  smtp_from?: string;
  smtp_username?: string;
  smtp_password_configured?: boolean;
};

export type ServiceUpdateInfo = {
  latest_version?: string;
  update_check_source: string;
  update_check_error?: string;
};

export type AppVersion = ServiceUpdateInfo & {
  service: string;
  version: string;
  commit: string;
  build_date: string;
  update_available: boolean;
  service_updates: Record<string, ServiceUpdateInfo>;
};

export type SystemUpdateStrategy = "when_idle" | "maintenance";

export type SystemUpdateOperation = "software_update" | "port_reconfigure";

export type SystemUpdatePortReconfigurationResult =
  | "applied"
  | "rolled_back"
  | "unchanged"
  | "rollback_failed";

export type SystemUpdatePortReconfiguration = {
  network_namespace?: string;
  protocol?: "tcp" | "udp";
  old_port?: number;
  new_port?: number;
  expected_endpoint_revision?: number;
  target_endpoint_revision?: number;
  expected_config_revision?: number;
  target_config_revision?: number;
  expected_config_sha256?: string;
  target_config_sha256?: string;
  expected_source_policy_revision?: number;
  expected_updater_policy_revision?: number;
  expected_executor_policy_revision?: number;
  expected_executor_policy_sha256?: string;
  port_plan_sha256?: string;
  docker?: SystemUpdateDockerPortReconfiguration;
  result?: SystemUpdatePortReconfigurationResult;
};

export type SystemUpdateDockerPortReconfiguration = {
  published_host_ip: string;
  old_published_port: number;
  new_published_port: number;
  old_container_port: number;
  new_container_port: number;
  old_health_port: number;
  new_health_port: number;
  approved_compose_config_sha256: string;
  approved_compose_revision: number;
  expected_version_env_sha256: string;
  expected_container_id: string;
  expected_image_id: string;
  expected_repository_digest: string;
};

export type SystemUpdateSoftwareCreateRequest = {
  operation?: "software_update";
  target_id: string;
  strategy: SystemUpdateStrategy;
  idempotency_key: string;
};

export type SystemUpdateSystemdPortReconfigureCreateRequest = {
  operation: "port_reconfigure";
  target_id: string;
  new_port: number;
  expected_endpoint_revision: number;
  idempotency_key: string;
};

export type SystemUpdateDockerPortReconfigureCreateRequest = {
  operation: "port_reconfigure";
  target_id: string;
  new_advertised_port: number;
  new_published_port: number;
  new_container_port: number;
  expected_endpoint_revision: number;
  idempotency_key: string;
};

export type SystemUpdatePortReconfigureCreateRequest =
  | SystemUpdateSystemdPortReconfigureCreateRequest
  | SystemUpdateDockerPortReconfigureCreateRequest;

export type SystemUpdateCreateRequest =
  | SystemUpdateSoftwareCreateRequest
  | SystemUpdatePortReconfigureCreateRequest;

export type SystemUpdateReachability = "reachable" | "unreachable" | "unknown";

export type SystemUpdateAgentStatus = {
  updater_id: string;
  name: string;
  status: string;
  online: boolean;
  version: string;
  transport_mode?: "ssh_v1" | "pull_v2";
  execution_host_id?: string;
  ownership_epoch?: number;
  last_heartbeat_at?: string;
  desired_revision?: number;
  applied_revision?: number;
  policy_status?: string;
  policy_error_code?: string;
  policy_error?: string;
  ssh_client_public_keys?: Record<string, string>;
  ssh_client_key_fingerprints?: Record<string, string>;
  bootstrap_encryption_public_key?: string;
  bootstrap_encryption_key_fingerprint?: string;
};

export type SystemUpdateHostStatus = {
  host_id: string;
  name: string;
  updater_id: string;
  reachability: SystemUpdateReachability;
  reachability_checked_at?: string;
  reachability_code?: string;
};

export type SystemUpdateTarget = {
  target_id: string;
  target_type: string;
  name: string;
  host_id: string;
  current_version?: string;
  latest_version?: string;
  update_available: boolean;
  deployment_mode?: string;
  updater_id?: string;
  updater_online: boolean;
  busy?: boolean;
  current_stream_id?: string;
  eligible: boolean;
  blocked_reason?: string;
  eligible_operations?: SystemUpdateOperation[];
  operation_blocked_reasons?: Partial<Record<SystemUpdateOperation, string>>;
  port_mapping?: SystemUpdatePortMapping;
  update_check_source?: string;
  update_check_error?: string;
};

export type SystemUpdatePortMapping = {
  mode: "docker";
  advertised_port?: number;
  published_host_ip?: string;
  published_port?: number;
  container_port?: number;
  health_port?: number;
  config_revision?: number;
  state: "applied" | "drifted" | "unavailable";
  reported_at?: string;
};

export type SystemUpdateJob = {
  id: string;
  idempotency_key?: string;
  target_id: string;
  target_type: string;
  host_id?: string;
  transport_mode?: "ssh_v1" | "pull_v2";
  ownership_epoch?: number;
  policy_revision?: number;
  updater_id?: string;
  operation?: SystemUpdateOperation;
  port_reconfigure?: SystemUpdatePortReconfiguration;
  current_version?: string;
  target_version?: string;
  deployment_mode?: string;
  strategy?: SystemUpdateStrategy;
  status: string;
  progress?: number;
  code?: string;
  message?: string;
  requested_by?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  sequence?: number;
  report_sequence?: number;
  lease_generation?: number;
  recovery_required?: boolean;
  last_status?: string;
};

export type SystemUpdatesResponse = {
  updaters: SystemUpdateAgentStatus[];
  hosts: SystemUpdateHostStatus[];
  targets: SystemUpdateTarget[];
  jobs: SystemUpdateJob[];
};

export type UpdaterSettingsAPI = {
  bind_host: string;
  host: string;
  port: number;
  ssl_enabled: boolean;
  tls_cert_file?: string;
  tls_key_file?: string;
};

export type UpdaterSettingsHost = {
  host_id: string;
  name: string;
  address: string;
  port: number;
  user: string;
  arch: string;
  host_public_key: string;
  host_key_fingerprint?: string;
  host_public_key_fingerprint?: string;
  ssh_client_public_key?: string;
  ssh_client_key_fingerprint?: string;
};

export type UpdaterSettingsTarget = {
  target_id: string;
  service_id: string;
  host_id: string;
  service_type: string;
  deployment_mode: string;
};

export type UpdaterSettings = {
  updater_id: string;
  revision: number;
  projection_revision?: number;
  local_executor_policy_revision?: number;
  transport_mode: "ssh_v1" | "pull_v2";
  execution_host_id?: string;
  execution_host_ownership?: {
    transport_mode: "ssh_v1" | "pull_v2";
    agent_service_id?: string;
    legacy_agent_service_id?: string;
    ownership_epoch: number;
    policy_revision: number;
  };
  pull_activation?: {
    ready: boolean;
    blocked_reason?: string;
    status: string;
    last_heartbeat_at?: string;
    observe_only: boolean;
    update_executor: boolean;
    mutation_enabled: boolean;
    recovery_pending: boolean;
    reported_ownership_epoch: number;
    reported_projection_revision: number;
  };
  local_executor_policy_sha256?: string;
  api: UpdaterSettingsAPI;
  poll_interval_seconds: number;
  heartbeat_interval_seconds: number;
  hosts: UpdaterSettingsHost[];
  targets: UpdaterSettingsTarget[];
  github_token_configured: boolean;
  github_token_fingerprint?: string;
  updated_at?: string;
};

export type UpdaterSettingsUpdate = {
  expected_revision: number;
  api?: UpdaterSettingsAPI;
  poll_interval_seconds: number;
  heartbeat_interval_seconds: number;
  hosts?: UpdaterSettingsHost[];
  targets: UpdaterSettingsTarget[];
  local_executor_policy_sha256?: string;
  github_token?: string;
};

export type PullUpdaterOwnershipActivationRequest = {
  expected_execution_host_id: string;
  expected_ownership_epoch: number;
  expected_source_policy_revision: number;
  expected_projection_revision: number;
  expected_local_executor_policy_revision: number;
  expected_local_executor_policy_sha256: string;
};

export type PullUpdaterOwnershipActivationResponse = {
  updater_id: string;
  execution_host_id: string;
  transport_mode: "pull_v2";
  agent_service_id: string;
  ownership_epoch: number;
  source_policy_revision: number;
  projection_revision: number;
  local_executor_policy_revision: number;
  local_executor_policy_sha256: string;
};

export type PullUpdaterOwnershipDeactivationRequest = {
  expected_execution_host_id: string;
  expected_ownership_epoch: number;
  expected_source_policy_revision: number;
  expected_projection_revision: number;
  expected_local_executor_policy_revision: number;
  expected_local_executor_policy_sha256: string;
};

export type PullUpdaterOwnershipDeactivationResponse = {
  updater_id: string;
  execution_host_id: string;
  transport_mode: "ssh_v1";
  agent_service_id: string;
  ownership_epoch: number;
  agent_ownership_epoch: 0;
  source_policy_revision: number;
  projection_revision: number;
  local_executor_policy_revision: number;
  local_executor_policy_sha256: string;
};

export type UpdaterHostBootstrapEnvelope = {
  version: 1;
  ephemeral_public_key: string;
  nonce: string;
  ciphertext: string;
};

export type UpdaterHostBootstrapStatus =
  | "awaiting_credentials"
  | "queued"
  | "claimed"
  | "connecting"
  | "uploading"
  | "verifying"
  | "installing"
  | "probing"
  | "succeeded"
  | "failed"
  | "credential_expired";

export type UpdaterHostBootstrapHostResult = {
  host_id: string;
  status: string;
  progress?: number;
  code?: string;
  message?: string;
  updated_at?: string;
  completed_at?: string;
};

export type UpdaterHostBootstrapJob = {
  id: string;
  idempotency_key?: string;
  updater_id: string;
  expected_revision: number;
  status: string;
  host_ids: string[];
  hosts: UpdaterHostBootstrapHostResult[];
  created_at: string;
  updated_at?: string;
  completed_at?: string;
};

export type UpdaterHostBootstrapJobsResponse = {
  jobs: UpdaterHostBootstrapJob[];
};

export type UpdaterHostBootstrapRequest = {
  job_id: string;
  idempotency_key: string;
  expected_revision: number;
  host_ids: string[];
  recipient_key_fingerprint: string;
  envelope: UpdaterHostBootstrapEnvelope;
};

export type NodeRegistrationResponse = {
  id: string;
  service_type: string;
  node_type: string;
  scopes: string[];
  token?: string;
  configure_token?: string;
  configure_token_expires_at?: string;
  runtime_token_id?: string;
  runtime_token?: string;
  created_at: string;
  configure_command: string;
  configuration_yaml?: string;
  configuration_path?: string;
  configuration_example?: string;
  manual_configuration_required?: boolean;
  systemd_unit?: string;
  node?: WorkerNode;
};
