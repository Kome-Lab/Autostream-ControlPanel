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
  host?: string;
  port?: number;
  ssl_enabled?: boolean;
  public_url?: string;
  version?: string;
  reported_version?: string;
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
  smtp_enabled?: boolean;
  smtp_host?: string;
  smtp_port?: number;
  smtp_starttls?: boolean;
  smtp_from?: string;
  smtp_username?: string;
  smtp_password_configured?: boolean;
  turnstile_enabled?: boolean;
  turnstile_site_key?: string;
  turnstile_configured?: boolean;
  updated_at?: string;
};

export type AppVersion = {
  service: string;
  version: string;
  commit: string;
  build_date: string;
  latest_version?: string;
  update_available: boolean;
  update_check_source: string;
  update_check_error?: string;
};

export type NodeRegistrationResponse = {
  id: string;
  service_type: string;
  node_type: string;
  scopes: string[];
  token: string;
  configure_token?: string;
  configure_token_expires_at?: string;
  runtime_token_id?: string;
  runtime_token?: string;
  created_at: string;
  configure_command: string;
  configuration_yaml?: string;
  systemd_unit?: string;
  node?: WorkerNode;
};
