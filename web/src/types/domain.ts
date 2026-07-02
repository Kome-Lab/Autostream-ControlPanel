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
  youtube_output_id?: string;
  archive_profile_id?: string;
};

export type WorkerNode = {
  id: string;
  service_id?: string;
  service_type: string;
  service_name: string;
  status: string;
  health_status?: string;
  assignment_role?: string;
  current_stream_id?: string;
  public_url?: string;
  version?: string;
  last_heartbeat_at?: string;
  heartbeat_age_sec?: number;
  capabilities?: Record<string, unknown>;
  metrics?: Record<string, number | string>;
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

export type MetricPoint = {
  timestamp: string;
  cpu_percent: number;
  memory_percent: number;
  network_mbps: number;
};

export type CurrentUser = {
  user: {
    id: string;
    username: string;
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
  updated_at?: string;
};

export type NodeRegistrationResponse = {
  id: string;
  service_type: string;
  node_type: string;
  scopes: string[];
  token: string;
  created_at: string;
  configure_command: string;
  node?: WorkerNode;
};
