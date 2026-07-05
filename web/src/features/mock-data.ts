import type { AppSettings, AuditLog, CurrentUser, MetricPoint, NodeRegistrationResponse, SetupStatus, Stream, WorkerNode } from "@/types/domain";

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
    host: "worker-main.example.jp",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://worker-main.example.jp",
    version: "1.2.0",
    reported_version: "1.2.0",
    reported_os: "linux",
    reported_arch: "amd64",
    last_heartbeat_at: "2026-07-02T09:00:00+09:00",
    heartbeat_age_sec: 4,
    capabilities: { overlay_events: true, caption_events: true, participant_state: true },
    reported_capabilities: { overlay_events: true, caption_events: true, participant_state: true },
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
    host: "encoder-main.example.jp",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://encoder-main.example.jp",
    version: "1.2.0",
    reported_version: "1.2.0",
    reported_os: "linux",
    reported_arch: "amd64",
    last_heartbeat_at: "2026-07-02T08:59:58+09:00",
    heartbeat_age_sec: 6,
    capabilities: { rtmps_output: true, archive_upload: true, discord_audio_ingest: true },
    reported_capabilities: { rtmps_output: true, archive_upload: true, discord_audio_ingest: true },
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
    host: "worker-city.example.jp",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://worker-city.example.jp",
    version: "1.1.8",
    reported_version: "1.1.8",
    reported_os: "linux",
    reported_arch: "arm64",
    last_heartbeat_at: "2026-07-02T08:59:54+09:00",
    heartbeat_age_sec: 10,
    capabilities: { overlay_events: true, caption_events: true },
    reported_capabilities: { overlay_events: true, caption_events: true },
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
    host: "encoder-field.example.jp",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://encoder-field.example.jp",
    version: "1.1.4",
    reported_version: "1.1.4",
    reported_os: "linux",
    reported_arch: "amd64",
    last_heartbeat_at: "2026-07-02T08:57:40+09:00",
    heartbeat_age_sec: 140,
    capabilities: { rtmps_output: true, archive_upload: true },
    reported_capabilities: { rtmps_output: true, archive_upload: true },
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

export const mockSetupStatus: SetupStatus = {
  setup_enabled: true,
  setup_required: false,
};

export let mockAppSettings: AppSettings = {
  app_name: "AutoStream",
  updated_at: baseTime,
};

const mockResourceData: Record<string, unknown[]> = {
  "/profiles/encoder": [
    { id: "enc-profile-1080p", name: "1080p60 標準", width: 1920, height: 1080, fps: 60, bitrate_kbps: 7800, updated_at: baseTime },
    { id: "enc-profile-mobile", name: "現場回線向け 720p", width: 1280, height: 720, fps: 30, bitrate_kbps: 3500, updated_at: "2026-07-02T08:35:00+09:00" },
  ],
  "/profiles/caption": [
    { id: "caption-live-ja", name: "日本語ライブ字幕", language: "ja-JP", provider: "Deepgram", delay_ms: 800, updated_at: baseTime },
    { id: "caption-manual", name: "手動字幕", language: "ja-JP", provider: "operator", delay_ms: 0, updated_at: "2026-07-01T18:20:00+09:00" },
  ],
  "/profiles/overlay": [
    { id: "overlay-lower-third", name: "自治体テロップ", safe_area: "16:9 lower", theme: "public", updated_at: baseTime },
    { id: "overlay-event", name: "イベント進行", safe_area: "full", theme: "event", updated_at: "2026-07-01T17:00:00+09:00" },
  ],
  "/profiles/archive": [
    { id: "archive-shared-drive", name: "共有Drive保存", format: "mp4", retention_days: 180, upload_enabled: true, updated_at: baseTime },
    { id: "archive-local", name: "ローカル一時保存", format: "mkv", retention_days: 30, upload_enabled: false, updated_at: "2026-07-01T10:00:00+09:00" },
  ],
  "/discord/configs": [
    { id: "discord-main", name: "制作連絡チャンネル", service_id: "discord-01", guild_id: "guild-main", audio_forward_enabled: true, updated_at: baseTime },
    { id: "discord-city", name: "自治体通知", service_id: "discord-city", guild_id: "guild-city", audio_forward_enabled: false, updated_at: "2026-07-01T15:40:00+09:00" },
  ],
  "/youtube/outputs": [
    { id: "yt-regional-news", name: "地域ニュース配信", mode: "live_api_dry_run", privacy_status: "public", rtmp_url: "rtmps://example.youtube.com/live2", updated_at: baseTime },
    { id: "yt-private-event", name: "限定公開イベント", mode: "stream_key", privacy_status: "unlisted", rtmp_url: "rtmps://example.youtube.com/live2", updated_at: "2026-07-01T13:00:00+09:00" },
  ],
  "/archive/destinations": [
    { id: "drive-city", name: "自治体広報 Drive", auth_mode: "oauth2", folder_id_configured: true, updated_at: baseTime },
    { id: "drive-bpo", name: "BPO案件別 Drive", auth_mode: "service_account", folder_id_configured: true, updated_at: "2026-07-01T12:00:00+09:00" },
  ],
  "/integrations/oauth-providers": [
    { id: "google-main", provider_type: "google", name: "Google Workspace", enabled: true, redirect_uri: "https://control.example.jp/integrations/oauth-accounts/callback" },
    { id: "github-login", provider_type: "github", name: "GitHub Login", enabled: false, redirect_uri: "https://control.example.jp/auth/oauth/callback" },
  ],
  "/integrations/oauth-accounts": [
    { id: "acct-drive", provider_type: "google", account_label: "広報 Drive", email: "archive@example.jp", status: "connected", updated_at: baseTime },
    { id: "acct-youtube", provider_type: "google", account_label: "YouTube 管理", email: "live@example.jp", status: "connected", updated_at: "2026-07-01T16:10:00+09:00" },
  ],
  "/users": [
    { id: "user-admin", username: "admin", status: "active", roles: ["super_admin"], last_login_at: baseTime },
    { id: "user-operator", username: "operator", status: "active", roles: ["operator"], last_login_at: "2026-07-02T08:20:00+09:00" },
  ],
  "/roles": [
    { id: "role-super-admin", name: "super_admin", permissions: ["*"], updated_at: baseTime },
    { id: "role-operator", name: "operator", permissions: ["streams.read", "streams.start", "streams.stop"], updated_at: "2026-07-01T09:00:00+09:00" },
  ],
  "/permissions": [
    { id: "streams.read", name: "streams.read", group: "streams" },
    { id: "system_settings.update", name: "system_settings.update", group: "settings" },
  ],
  "/security/settings": [
    { id: "password_min_length", name: "Password minimum length", value: 12 },
    { id: "mfa_mode", name: "MFA mode", value: "disabled" },
    { id: "session_idle_timeout_min", name: "Session idle timeout", value: 30 },
  ],
  "/secrets/status": [
    { name: "discord_bot_token", configured: true, fingerprint: "sha256:8f7c..." },
    { name: "youtube_stream_key", configured: true, fingerprint: "sha256:1ab2..." },
    { name: "observability_token", configured: false },
  ],
  "/observability/incidents": [
    { id: "inc-1", severity: "warning", status: "acknowledged", title: "現場Encoderのハートビート遅延", service_id: "encoder-field", updated_at: baseTime },
    { id: "inc-2", severity: "info", status: "resolved", title: "YouTube API dry-run retry", service_id: "worker-main", updated_at: "2026-07-02T07:50:00+09:00" },
  ],
  "/observability/diagnostics": [
    { id: "diag-1", check: "audio_status", status: "pass", target: "stream-cable-morning", updated_at: baseTime },
    { id: "diag-2", check: "encoder_preflight", status: "warning", target: "encoder-field", updated_at: "2026-07-02T08:58:00+09:00" },
  ],
  "/observability/remediation-actions": [
    { id: "rem-1", status: "pending_approval", action: "restart_encoder", target: "encoder-field", created_at: baseTime },
    { id: "rem-2", status: "executed", action: "switch_worker", target: "worker-standby", created_at: "2026-07-02T08:30:00+09:00" },
  ],
  "/observability/notification-deliveries": [
    { id: "ntf-1", status: "success", channel: "discord", incident_id: "inc-1", sent_at: baseTime },
    { id: "ntf-2", status: "retrying", channel: "email", incident_id: "inc-2", sent_at: "2026-07-02T08:40:00+09:00" },
  ],
  "/observability/notification-channels": [
    { id: "chn-1", name: "制作Discord", type: "discord", enabled: true, masked_webhook_url: "https://example.jp/<WEBHOOK_PATH>" },
    { id: "chn-2", name: "運用メール", type: "email", enabled: true, masked_email_target: "o***s@example.jp" },
  ],
};

export function mockGet(path: string): unknown {
  const normalizedPath = stripQuery(path);
  const nodeConfiguration = normalizedPath.match(/^\/nodes\/([^/]+)\/configuration$/);
  if (nodeConfiguration) {
    const nodeID = decodeURIComponent(nodeConfiguration[1]);
    const node = mockWorkers.find((item) => (item.service_id || item.id) === nodeID) || mockWorkers[0];
    const host = node.host || "worker-main.example.jp";
    const port = node.port || 8443;
    const sslEnabled = node.ssl_enabled ?? true;
    const nodeApiUrl = `${sslEnabled ? "https" : "http"}://${host}:${port}`;
    return {
      node,
      node_api_url: nodeApiUrl,
      configure_command: mockConfigureCommand(node.service_type, node.service_id || node.id, "<regenerate-configure-token>"),
      configuration_yaml: `panel:\n  url: "https://control.example.jp"\n\nnode:\n  id: "${node.service_id || node.id}"\n  name: "${node.service_name}"\n  type: "${node.service_type}"\n\napi:\n  host: "${host}"\n  port: ${port}\n  ssl_enabled: ${sslEnabled}\n\nauth:\n  token_id: "<runtime-token-id>"\n  token: "<regenerate-runtime-token>"\n`,
    };
  }
  const dataByPath: Record<string, unknown> = {
    "/auth/me": mockCurrentUser,
    "/setup/status": mockSetupStatus,
    "/settings/app": mockAppSettings,
    "/streams": mockStreams,
    "/workers": mockWorkers,
    "/service-health": mockWorkers,
    "/audit-logs": mockAuditLogs,
    "/observability/metrics": mockWorkerMetrics,
    ...mockResourceData,
  };
  return dataByPath[normalizedPath] ?? [];
}

export function mockPost(path: string, body?: unknown): unknown {
  if (stripQuery(path) === "/integrations/oauth-accounts/start") {
    const request = body as Partial<{ provider_id: string; account_label: string; redirect_after: string }>;
    const providers = mockResourceData["/integrations/oauth-providers"] as Array<{ id: string; provider_type: string; name: string; enabled: boolean; redirect_uri: string }>;
    return {
      provider: providers.find((provider) => provider.id === request.provider_id) || providers[0],
      authorization_url: request.redirect_after || "/admin/integrations/",
      state: "mock-oauth-state",
      nonce: "mock-oauth-nonce",
      expires_at: baseTime,
      account_label: request.account_label || "Googleアカウント",
    };
  }
  if (stripQuery(path) === "/nodes/registration-tokens") {
    const request = body as Partial<{
      node_type: string;
      node_id: string;
      name: string;
      description: string;
      host: string;
      port: number;
      ssl_enabled: boolean;
    }>;
    const nodeID = request.node_id || `${request.node_type || "worker"}-new`;
    const configureToken = "ast_cfg_demo_9d2b4b5fd4e3c0a7";
    const runtimeToken = "ast_svc_demo_8e1f2c6a4b0d9f7e";
    const host = request.host || "worker-new.example.jp";
    const port = request.port || 8081;
    const sslEnabled = request.ssl_enabled ?? true;
    const scheme = sslEnabled ? "https" : "http";
    const response: NodeRegistrationResponse = {
      id: "token-demo-node-registration",
      service_type: request.node_type || "worker",
      node_type: request.node_type || "worker",
      scopes: ["service.register", "service.heartbeat", "service.config.read", "service.status.write"],
      token: configureToken,
      configure_token: configureToken,
      configure_token_expires_at: baseTime,
      runtime_token_id: "runtime-token-demo",
      runtime_token: runtimeToken,
      created_at: baseTime,
      configure_command: mockConfigureCommand(request.node_type || "worker", nodeID, configureToken),
      configuration_yaml: `panel:\n  url: "https://control.example.jp"\n\nnode:\n  id: "${nodeID}"\n  name: "${request.name || "新規Node"}"\n  type: "${request.node_type || "worker"}"\n\napi:\n  host: "${host}"\n  port: ${port}\n  ssl_enabled: ${sslEnabled}\n\nauth:\n  token_id: "runtime-token-demo"\n  token: "${runtimeToken}"\n`,
      node: {
        id: nodeID,
        service_id: nodeID,
        service_type: request.node_type || "worker",
        service_name: request.name || "新規Node",
        status: "pending",
        health_status: "pending",
        description: request.description || "",
        host,
        port,
        ssl_enabled: sslEnabled,
        public_url: `${scheme}://${host}:${port}`,
        reported_version: "",
        reported_capabilities: {},
      },
    };
    const existingIndex = mockWorkers.findIndex((node) => (node.service_id || node.id) === nodeID);
    if (response.node) {
      if (existingIndex >= 0) {
        mockWorkers[existingIndex] = response.node;
      } else {
        mockWorkers.unshift(response.node);
      }
    }
    return response;
  }
  return { ok: true };
}

function mockConfigureCommand(serviceType: string, nodeID: string, configureToken: string) {
  return `sudo ${mockConfigureBinary(serviceType)} configure --panel-url "https://control.example.jp" --token "${configureToken}" --node "${nodeID}" --config "/etc/autostream-node/config.yml"`;
}

function mockConfigureBinary(serviceType: string) {
  switch (serviceType) {
    case "encoder_recorder":
      return "autostream-encoder-recorder";
    case "discord_bot":
      return "autostream-discord-bot";
    case "observability":
      return "autostream-observability";
    default:
      return "autostream-worker";
  }
}

export function mockPut(path: string, body?: unknown): unknown {
  if (stripQuery(path) === "/settings/app") {
    const request = body as Partial<AppSettings>;
    mockAppSettings = { app_name: request.app_name || mockAppSettings.app_name, updated_at: baseTime };
    return mockAppSettings;
  }
  return { ok: true };
}

export function mockPathExists(path: string) {
  const normalizedPath = stripQuery(path);
  if (/^\/nodes\/[^/]+\/configuration$/.test(normalizedPath)) return true;
  return new Set([
    "/auth/me",
    "/setup/status",
    "/settings/app",
    "/streams",
    "/workers",
    "/service-health",
    "/audit-logs",
    "/observability/metrics",
    "/nodes/registration-tokens",
    "/integrations/oauth-accounts/start",
    ...Object.keys(mockResourceData),
  ]).has(normalizedPath);
}

function stripQuery(path: string) {
  return String(path || "").split("?")[0];
}
