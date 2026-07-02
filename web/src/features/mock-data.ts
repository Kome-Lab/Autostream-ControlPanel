import type { AuditLog, CurrentUser, MetricPoint, NodeRegistrationResponse, Stream, WorkerNode } from "@/types/domain";

const baseTime = "2026-07-02T09:00:00+09:00";

export const mockCurrentUser: CurrentUser = {
  user: {
    id: "user-demo-admin",
    username: "demo-admin",
    status: "active",
    roles: ["super_admin"],
  },
  permissions: [
    "streams.read",
    "streams.start",
    "streams.stop",
    "streams.update",
    "workers.read",
    "workers.restart",
    "workers.assign",
    "audit_logs.read",
    "audit_logs.export",
    "api_tokens.create",
    "api_tokens.read",
    "secrets.update",
    "remediation.execute",
  ],
};

export const mockStreams: Stream[] = [
  {
    id: "stream-cable-morning",
    name: "朝の地域ニュース",
    status: "live",
    input_source: "Studio 1 / SDI",
    output_target: "YouTube Live / CATV Web",
    assigned_worker_id: "worker-main",
    assigned_encoder_id: "encoder-main",
    scheduled_start_at: "2026-07-02T08:55:00+09:00",
    scheduled_end_at: "2026-07-02T10:00:00+09:00",
    started_at: "2026-07-02T08:55:10+09:00",
    updated_at: baseTime,
    youtube_output_id: "yt-regional-news",
    archive_profile_id: "archive-shared-drive",
  },
  {
    id: "stream-city-council",
    name: "市議会定例会 中継",
    status: "scheduled",
    input_source: "Council Hall / SRT",
    output_target: "Public Portal",
    assigned_worker_id: "worker-standby",
    assigned_encoder_id: "encoder-standby",
    scheduled_start_at: "2026-07-02T13:00:00+09:00",
    scheduled_end_at: "2026-07-02T16:30:00+09:00",
    updated_at: "2026-07-02T08:30:00+09:00",
  },
  {
    id: "stream-event-rehearsal",
    name: "企業セミナー リハーサル",
    status: "ready",
    input_source: "OBS / RTMP",
    output_target: "Private Live",
    assigned_worker_id: "worker-main",
    assigned_encoder_id: "encoder-main",
    scheduled_start_at: "2026-07-02T18:00:00+09:00",
    scheduled_end_at: "2026-07-02T19:15:00+09:00",
    updated_at: "2026-07-02T08:45:00+09:00",
  },
  {
    id: "stream-fm-special",
    name: "コミュニティFM 特別番組",
    status: "failed",
    input_source: "Studio 2 / Audio PC",
    output_target: "Radio Archive",
    assigned_worker_id: "worker-field",
    assigned_encoder_id: "encoder-field",
    scheduled_start_at: "2026-07-02T11:00:00+09:00",
    scheduled_end_at: "2026-07-02T12:00:00+09:00",
    updated_at: "2026-07-02T08:58:00+09:00",
  },
];

export const mockWorkers: WorkerNode[] = [
  {
    id: "worker-main",
    service_id: "worker-main",
    service_type: "worker",
    service_name: "本社メインWorker",
    status: "online",
    health_status: "healthy",
    assignment_role: "primary",
    current_stream_id: "stream-cable-morning",
    public_url: "https://worker-main.example.jp",
    version: "1.2.0",
    last_heartbeat_at: "2026-07-02T09:00:00+09:00",
    heartbeat_age_sec: 4,
    capabilities: { overlay_events: true, caption_events: true, participant_state: true },
    metrics: { cpu_percent: 32, memory_percent: 41, active_jobs: 2 },
  },
  {
    id: "encoder-main",
    service_id: "encoder-main",
    service_type: "encoder_recorder",
    service_name: "本社エンコーダー",
    status: "online",
    health_status: "healthy",
    assignment_role: "primary",
    current_stream_id: "stream-cable-morning",
    public_url: "https://encoder-main.example.jp",
    version: "1.2.0",
    last_heartbeat_at: "2026-07-02T08:59:58+09:00",
    heartbeat_age_sec: 6,
    capabilities: { rtmps_output: true, archive_upload: true, discord_audio_ingest: true },
    metrics: { cpu_percent: 48, memory_percent: 53, output_bitrate_kbps: 7800 },
  },
  {
    id: "worker-standby",
    service_id: "worker-standby",
    service_type: "worker",
    service_name: "庁舎スタンバイWorker",
    status: "online",
    health_status: "healthy",
    assignment_role: "standby",
    current_stream_id: "",
    public_url: "https://worker-city.example.jp",
    version: "1.1.8",
    last_heartbeat_at: "2026-07-02T08:59:54+09:00",
    heartbeat_age_sec: 10,
    capabilities: { overlay_events: true, caption_events: true },
    metrics: { cpu_percent: 18, memory_percent: 29, active_jobs: 0 },
  },
  {
    id: "encoder-field",
    service_id: "encoder-field",
    service_type: "encoder_recorder",
    service_name: "現場持出エンコーダー",
    status: "degraded",
    health_status: "warning",
    assignment_role: "primary",
    current_stream_id: "stream-fm-special",
    public_url: "https://encoder-field.example.jp",
    version: "1.1.4",
    last_heartbeat_at: "2026-07-02T08:57:40+09:00",
    heartbeat_age_sec: 140,
    capabilities: { rtmps_output: true, archive_upload: true },
    metrics: { cpu_percent: 76, memory_percent: 68, output_bitrate_kbps: 0 },
  },
];

export const mockAuditLogs: AuditLog[] = [
  {
    id: "audit-001",
    timestamp: "2026-07-02T08:55:10+09:00",
    action: "streams.start",
    actor_username: "sato",
    actor_ip: "192.0.2.10",
    user_agent: "Chrome / Windows",
    result: "success",
    resource_type: "stream",
    resource_id: "stream-cable-morning",
  },
  {
    id: "audit-002",
    timestamp: "2026-07-02T08:58:25+09:00",
    action: "workers.restart",
    actor_username: "ops-admin",
    actor_ip: "192.0.2.20",
    user_agent: "Edge / Windows",
    result: "success",
    resource_type: "worker",
    resource_id: "encoder-field",
  },
  {
    id: "audit-003",
    timestamp: "2026-07-02T08:59:03+09:00",
    action: "nodes.registration_token.create",
    actor_username: "nakamura",
    actor_ip: "198.51.100.8",
    user_agent: "Chrome / macOS",
    result: "success",
    resource_type: "node",
    resource_id: "worker-standby",
  },
];

export const mockWorkerMetrics: MetricPoint[] = [
  { timestamp: "08:20", cpu_percent: 21, memory_percent: 34, network_mbps: 3.4 },
  { timestamp: "08:30", cpu_percent: 26, memory_percent: 35, network_mbps: 4.1 },
  { timestamp: "08:40", cpu_percent: 31, memory_percent: 38, network_mbps: 4.7 },
  { timestamp: "08:50", cpu_percent: 44, memory_percent: 42, network_mbps: 6.2 },
  { timestamp: "09:00", cpu_percent: 32, memory_percent: 41, network_mbps: 5.1 },
];

export function mockGet(path: string): unknown {
  const normalizedPath = stripQuery(path);
  const dataByPath: Record<string, unknown> = {
    "/auth/me": mockCurrentUser,
    "/streams": mockStreams,
    "/workers": mockWorkers,
    "/service-health": mockWorkers,
    "/audit-logs": mockAuditLogs,
    "/observability/metrics": mockWorkerMetrics,
  };
  return dataByPath[normalizedPath] ?? [];
}

export function mockPost(path: string, body?: unknown): unknown {
  if (stripQuery(path) === "/nodes/registration-tokens") {
    const request = body as Partial<{
      node_type: string;
      node_id: string;
      name: string;
      public_url: string;
      version: string;
    }>;
    const nodeID = request.node_id || `${request.node_type || "worker"}-new`;
    const token = "ast_node_demo_9d2b4b5fd4e3c0a7";
    const response: NodeRegistrationResponse = {
      id: "token-demo-node-registration",
      service_type: request.node_type || "worker",
      node_type: request.node_type || "worker",
      scopes: ["service.register", "service.heartbeat", "service.config.read", "service.status.write"],
      token,
      created_at: baseTime,
      configure_command: `autostream-node configure --panel-url "https://control.example.jp" --token "${token}" --node "${nodeID}"`,
      node: {
        id: nodeID,
        service_id: nodeID,
        service_type: request.node_type || "worker",
        service_name: request.name || "新規Node",
        status: "pending",
        health_status: "pending",
        public_url: request.public_url || "",
        version: request.version || "",
      },
    };
    return response;
  }
  return { ok: true };
}

function stripQuery(path: string) {
  return String(path || "").split("?")[0];
}
