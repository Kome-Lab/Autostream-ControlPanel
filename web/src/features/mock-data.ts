import type { AppSettings, AuditLog, CurrentUser, MFAStatus, MetricSnapshot, NodeRegistrationResponse, OAuthLoginProvider, OAuthUserLink, PasskeyCredential, SetupStatus, Stream, WorkerNode } from "@/types/domain";

const baseTime = "2026-07-02T09:00:00+09:00";

export const mockCurrentUser: CurrentUser = {
  user: {
    id: "user-demo-admin",
    username: "demo-admin",
    email: "demo-admin@example.jp",
    status: "active",
    roles: ["super_admin"],
  },
  permissions: [
    "streams.read",
    "streams.create",
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

const mockMFAStatus: MFAStatus = {
  available: true,
  enabled: false,
  pending_enrollment: false,
  recovery_code_count: 0,
  policy_mode: "totp",
  required: false,
};

const mockPasskeys: PasskeyCredential[] = [
  {
    id: "passkey-demo-main",
    user_id: "user-demo-admin",
    name: "業務PC",
    sign_count: 8,
    transports: ["internal"],
    backup_eligible: true,
    backed_up: true,
    created_at: "2026-07-02T08:00:00+09:00",
    updated_at: "2026-07-02T08:00:00+09:00",
    last_used_at: "2026-07-02T08:55:00+09:00",
  },
];

const mockOAuthLinks: OAuthUserLink[] = [
  {
    id: "oauth-link-google-demo",
    user_id: "user-demo-admin",
    provider_id: "oauth-google-login",
    provider_type: "google",
    subject: "google-demo-user",
    email: "demo-admin@example.jp",
    created_at: "2026-07-02T08:00:00+09:00",
    updated_at: "2026-07-02T08:00:00+09:00",
  },
];

export const mockStreams: Stream[] = [
  {
    id: "stream-cable-morning",
    name: "朝の地域ニュース",
    status: "live",
    input_source: "Studio 1 / SDI",
    output_target: "YouTube Live / CATV Web",
    assigned_worker_id: "worker-main",
    assigned_encoder_id: "encoder-main",
    started_at: "2026-07-02T08:55:10+09:00",
    updated_at: baseTime,
    discord_config_id: "discord-main",
    discord_guild_id: "guild-regional",
    discord_voice_channel_id: "voice-morning",
    discord_text_channel_id: "chat-morning",
    auto_start_trigger: "discord_voice_join",
    youtube_output_id: "yt-regional-news",
    archive_profile_id: "archive-shared-drive",
    archive_drive_destination_id: "drive-city",
    archive_oauth_account_id: "acct-drive",
    archive_folder_id_configured: true,
    archive_masked_folder_id: "fol...ews",
    archive_shared_drive: true,
    archive_shared_drive_id: "shared-drive-city",
    archive_file_name: "朝の地域ニュース-20260702.mp4",
    overlay_profile_id: "overlay-lower-third",
  },
  {
    id: "stream-city-council",
    name: "市議会定例会 中継",
    status: "ready",
    input_source: "Council Hall / SRT",
    output_target: "Public Portal",
    assigned_worker_id: "worker-standby",
    assigned_encoder_id: "encoder-standby",
    discord_config_id: "discord-main",
    discord_guild_id: "guild-main",
    discord_voice_channel_id: "voice-council",
    discord_text_channel_id: "chat-council",
    auto_start_trigger: "discord_voice_join",
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
    discord_config_id: "discord-main",
    discord_guild_id: "guild-main",
    discord_voice_channel_id: "voice-seminar",
    discord_text_channel_id: "chat-seminar",
    auto_start_trigger: "discord_voice_join",
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
    discord_config_id: "discord-main",
    discord_guild_id: "guild-main",
    discord_voice_channel_id: "voice-fm-special",
    discord_text_channel_id: "chat-fm-special",
    auto_start_trigger: "discord_voice_join",
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
    reported_commit: "8f71c21a4b2d",
    reported_build_date: "2026-07-02T07:40:00+09:00",
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
    reported_commit: "41df9e2b701a",
    reported_build_date: "2026-07-02T07:42:00+09:00",
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
    reported_commit: "ce2038a91d0b",
    reported_build_date: "2026-06-28T11:20:00+09:00",
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
    reported_commit: "ab96c1d0027e",
    reported_build_date: "2026-06-25T15:10:00+09:00",
    reported_os: "linux",
    reported_arch: "amd64",
    last_heartbeat_at: "2026-07-02T08:57:40+09:00",
    heartbeat_age_sec: 140,
    capabilities: { rtmps_output: true, archive_upload: true },
    reported_capabilities: { rtmps_output: true, archive_upload: true },
    metrics: { cpu_percent: 76, memory_percent: 68, output_bitrate_kbps: 0 },
  },
  {
    id: "discord-main",
    service_id: "discord-main",
    service_type: "discord_bot",
    service_name: "制作Discord Bot",
    status: "online",
    health_status: "healthy",
    assignment_role: "primary",
    current_stream_id: "stream-cable-morning",
    host: "discord-main.example.jp",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://discord-main.example.jp",
    version: "1.2.0",
    reported_version: "1.2.0",
    reported_commit: "529b7e14f08c",
    reported_build_date: "2026-07-02T07:45:00+09:00",
    reported_os: "linux",
    reported_arch: "amd64",
    last_heartbeat_at: "2026-07-02T08:59:59+09:00",
    heartbeat_age_sec: 5,
    capabilities: { audio_capture: true, audio_stream_forward: true },
    reported_capabilities: { audio_capture: true, audio_stream_forward: true },
    metrics: { audio_forward_active: 1 },
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
  {
    id: "audit-004",
    timestamp: "2026-07-02T08:35:00+09:00",
    action: "streams.create",
    actor_username: "nakamura",
    actor_ip: "198.51.100.8",
    user_agent: "Chrome / macOS",
    result: "success",
    resource_type: "stream",
    resource_id: "stream-city-council",
  },
];

export function mockWorkerMetrics(): MetricSnapshot[] {
  const offsets = [-35, -30, -25, -20, -15, -10, -5, 0];
  const nodes = [
    { id: "worker-main", type: "worker", status: "online", cpu: 34, memory: 42, disk: 58, rx: 4200, tx: 2100, heap: 156 * 1024 * 1024, uptime: 212400, workload: { name: "worker.active_jobs", value: 2 } },
    { id: "encoder-main", type: "encoder_recorder", status: "online", cpu: 48, memory: 55, disk: 64, rx: 6200, tx: 9100, heap: 248 * 1024 * 1024, uptime: 188000, workload: { name: "encoder.output_bitrate_kbps", value: 7800 } },
    { id: "worker-standby", type: "worker", status: "online", cpu: 18, memory: 31, disk: 44, rx: 900, tx: 420, heap: 118 * 1024 * 1024, uptime: 167200, workload: { name: "worker.active_jobs", value: 0 } },
    { id: "encoder-field", type: "encoder_recorder", status: "degraded", cpu: 72, memory: 68, disk: 79, rx: 3100, tx: 120, heap: 302 * 1024 * 1024, uptime: 94500, workload: { name: "encoder.output_bitrate_kbps", value: 0 } },
    { id: "discord-main", type: "discord_bot", status: "online", cpu: 14, memory: 26, disk: 37, rx: 1600, tx: 1400, heap: 86 * 1024 * 1024, uptime: 198400, workload: { name: "discord.audio_forward_active", value: 1 } },
  ];
  return nodes.flatMap((node, nodeIndex) =>
    offsets.flatMap((minutesAgo, pointIndex) => {
      const updatedAt = new Date(Date.now() + minutesAgo * 60 * 1000).toISOString();
      const phase = pointIndex * 0.75 + nodeIndex;
      const cpu = clampMetric(node.cpu + Math.sin(phase) * 5, 0, 100);
      const memory = clampMetric(node.memory + Math.cos(phase) * 3, 0, 100);
      const disk = clampMetric(node.disk + Math.sin(pointIndex / 3) * 0.6, 0, 100);
      const rx = Math.max(0, Math.round(node.rx + Math.sin(phase) * 460));
      const tx = Math.max(0, Math.round(node.tx + Math.cos(phase) * 520));
      const heap = Math.max(0, Math.round(node.heap + Math.sin(phase) * 6 * 1024 * 1024));
      return [
        metricAt("node.cpu.used_percent", node.id, node.type, node.status, cpu, updatedAt),
        metricAt("node.load1", node.id, node.type, node.status, Number((cpu / 38).toFixed(2)), updatedAt),
        metricAt("node.memory.used_percent", node.id, node.type, node.status, memory, updatedAt),
        metricAt("node.memory.used_bytes", node.id, node.type, node.status, Math.round((memory / 100) * 16 * 1024 * 1024 * 1024), updatedAt),
        metricAt("node.filesystem.root.used_percent", node.id, node.type, node.status, disk, updatedAt),
        metricAt("node.network.rx_kbps", node.id, node.type, node.status, rx, updatedAt),
        metricAt("node.network.tx_kbps", node.id, node.type, node.status, tx, updatedAt),
        metricAt("process.goroutines", node.id, node.type, node.status, Math.round(24 + nodeIndex * 7 + Math.sin(phase) * 3), updatedAt),
        metricAt("process.heap_alloc_bytes", node.id, node.type, node.status, heap, updatedAt),
        metricAt("process.uptime_seconds", node.id, node.type, node.status, node.uptime + (35 + minutesAgo) * 60, updatedAt),
        metricAt(node.workload.name, node.id, node.type, node.status, node.workload.value, updatedAt),
      ];
    }),
  );
}

function metricAt(name: string, serviceID: string, serviceType: string, status: string, value: number, updatedAt: string): MetricSnapshot {
  return { name, service_id: serviceID, service_type: serviceType, status, value, updated_at: updatedAt };
}

function clampMetric(value: number, min: number, max: number) {
  return Number(Math.min(max, Math.max(min, value)).toFixed(1));
}

export const mockSetupStatus: SetupStatus = {
  setup_enabled: true,
  setup_required: false,
};

export let mockAppSettings: AppSettings = {
  app_name: "AutoStream",
  timezone: "Asia/Tokyo",
  smtp_enabled: false,
  smtp_port: 587,
  smtp_starttls: true,
  smtp_password_configured: false,
  turnstile_enabled: false,
  turnstile_configured: false,
  updated_at: baseTime,
};

export const mockAppVersion = {
  service: "control-panel",
  version: "v1.2.3",
  commit: "mock",
  build_date: baseTime,
  latest_version: "v1.2.4",
  update_available: true,
  update_check_source: "mock",
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
    { id: "overlay-lower-third", name: "自治体ロゴ", watermark_enabled: true, watermark_image_name: "city-logo.png", watermark_canvas_width: 1920, watermark_canvas_height: 1080, watermark_fit_mode: "scale_to_output", updated_at: baseTime },
    { id: "overlay-event", name: "イベントロゴ", watermark_enabled: true, watermark_image_name: "event-logo.webp", watermark_canvas_width: 1920, watermark_canvas_height: 1080, watermark_fit_mode: "scale_to_output", updated_at: "2026-07-01T17:00:00+09:00" },
  ],
  "/profiles/archive": [
    { id: "archive-shared-drive", name: "共有Drive保存", format: "mp4", retention_days: 180, upload_enabled: true, updated_at: baseTime },
    { id: "archive-local", name: "ローカル一時保存", format: "mkv", retention_days: 30, upload_enabled: false, updated_at: "2026-07-01T10:00:00+09:00" },
  ],
  "/discord/configs": [
    { id: "discord-main", name: "制作連絡チャンネル", service_id: "discord-01", guild_id: "guild-main", audio_forward_enabled: true, reconnect_enabled: true, updated_at: baseTime },
    { id: "discord-city", name: "自治体通知", service_id: "discord-city", guild_id: "guild-city", audio_forward_enabled: true, reconnect_enabled: true, updated_at: "2026-07-01T15:40:00+09:00" },
  ],
  "/youtube/outputs": [
    { id: "yt-regional-news", name: "地域ニュース配信", mode: "live_api_dry_run", privacy_status: "public", rtmp_url: "rtmps://example.youtube.com/live2", updated_at: baseTime },
    { id: "yt-private-event", name: "限定公開イベント", mode: "stream_key", privacy_status: "unlisted", rtmp_url: "rtmps://example.youtube.com/live2", updated_at: "2026-07-01T13:00:00+09:00" },
  ],
  "/archive/destinations": [
    { id: "drive-city", name: "自治体広報 Drive", auth_mode: "oauth2", folder_id_configured: true, updated_at: baseTime },
    { id: "drive-bpo", name: "BPO案件別 Drive", auth_mode: "oauth2", folder_id_configured: true, shared_drive: true, updated_at: "2026-07-01T12:00:00+09:00" },
  ],
  "/integrations/oauth-providers": [
    { id: "google-main", provider_type: "google", name: "Google Workspace", enabled: true, client_id: "google-client-id", client_secret_configured: true, allowed_domains: ["example.jp"], auto_provision: false, default_role_ids: [], redirect_uri: "https://control.example.jp/auth/oauth/callback" },
    { id: "github-login", provider_type: "github", name: "GitHub Login", enabled: false, client_id: "github-client-id", client_secret_configured: true, allowed_domains: [], auto_provision: false, default_role_ids: [], redirect_uri: "https://control.example.jp/auth/oauth/callback" },
  ],
  "/integrations/oauth-accounts": [
    { id: "acct-drive", provider_type: "google", account_label: "広報 Drive", display_name: "広報 Drive", email: "archive@example.jp", status: "connected", refresh_token_configured: true, updated_at: baseTime },
    { id: "acct-youtube", provider_type: "google", account_label: "YouTube 管理", display_name: "YouTube 管理", email: "live@example.jp", status: "connected", refresh_token_configured: true, updated_at: "2026-07-01T16:10:00+09:00" },
  ],
  "/users": [
    { id: "user-admin", username: "admin", email: "admin@example.jp", status: "active", roles: ["super_admin"], last_login_at: baseTime },
    { id: "user-operator", username: "operator", email: "operator@example.jp", status: "active", roles: ["operator"], last_login_at: "2026-07-02T08:20:00+09:00" },
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
    { id: "ntf-3", event_type: "admin.audit", status: "success", channel: "slack", incident_id: "", sent_at: "2026-07-02T09:05:00+09:00" },
  ],
  "/observability/notification-channels": [
    { id: "chn-1", name: "制作Discord", type: "discord", enabled: true, masked_webhook_url: "https://example.jp/<WEBHOOK_PATH>" },
    { id: "chn-2", name: "運用Slack", type: "slack", enabled: true, masked_webhook_url: "https://hooks.slack.com/<WEBHOOK_PATH>", severity_filter: ["critical", "error", "warning", "info"], event_type_filter: ["incident.opened", "admin.audit"] },
    { id: "chn-3", name: "運用メール", type: "email", enabled: true, masked_email_target: "o***s@example.jp", smtp_password_configured: true, severity_filter: ["critical", "error"], event_type_filter: ["incident.opened", "incident.resolved"] },
  ],
};

const mockStreamArtifacts: Record<string, Array<Record<string, unknown>>> = {
  "stream-cable-morning": [
    { id: "artifact-morning-final", stream_id: "stream-cable-morning", kind: "archive", name: "final.mp4", relative_path: "final/stream-cable-morning/final.mp4", size_bytes: 734003200, created_at: baseTime },
    { id: "artifact-morning-metadata", stream_id: "stream-cable-morning", kind: "metadata", name: "metadata.json", relative_path: "final/stream-cable-morning/metadata.json", size_bytes: 4096, created_at: baseTime },
  ],
  "stream-council": [
    { id: "artifact-council-final", stream_id: "stream-council", kind: "archive", name: "council-20260702.mp4", relative_path: "final/stream-council/council-20260702.mp4", size_bytes: 2147483648, created_at: "2026-07-02T16:35:00+09:00" },
  ],
};

const mockArchiveShares: Record<string, Array<Record<string, unknown>>> = {};
const mockArchiveSharesStorageKey = "autostream.mock_archive_shares";
let mockArchiveSharesLoaded = false;

export function mockGet(path: string): unknown {
  const normalizedPath = stripQuery(path);
  if (normalizedPath === "/audit-logs") {
    const query = path.includes("?") ? path.slice(path.indexOf("?") + 1) : "";
    const params = new URLSearchParams(query);
    const search = String(params.get("q") || "").trim().toLowerCase();
    const result = String(params.get("result") || "").trim().toLowerCase();
    return mockAuditLogs.filter((event) => {
      if (result && String(event.result || "").toLowerCase() !== result) return false;
      if (!search) return true;
      return [event.id, event.action, event.actor_username, event.actor_ip, event.user_agent, event.result, event.resource_type, event.resource_id]
        .some((value) => String(value || "").toLowerCase().includes(search));
    });
  }
  const streamArtifacts = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts$/);
  if (streamArtifacts) {
    return mockStreamArtifacts[decodeURIComponent(streamArtifacts[1])] || [];
  }
  const artifactShares = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)\/shares$/);
  if (artifactShares) {
    loadMockArchiveShares();
    const streamID = decodeURIComponent(artifactShares[1]);
    const artifactID = decodeURIComponent(artifactShares[2]);
    return (mockArchiveShares[archiveShareKey(streamID, artifactID)] || []).map(publicMockArchiveShareAdmin);
  }
  const archiveShare = normalizedPath.match(/^\/archive-shares\/([^/]+)$/);
  if (archiveShare) {
    return publicMockArchiveShare(decodeURIComponent(archiveShare[1]));
  }
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
    "/auth/mfa/status": mockMFAStatus,
    "/auth/passkeys": mockPasskeys,
    "/auth/oauth-links": mockOAuthLinks,
    "/auth/oauth/providers": mockLoginOAuthProviders(),
    "/setup/status": mockSetupStatus,
    "/settings/app": mockAppSettings,
    "/version": mockAppVersion,
    "/streams": mockStreams,
    "/workers": mockWorkers,
    "/nodes": mockWorkers,
    "/service-health": mockWorkers,
    "/audit-logs": mockAuditLogs,
    "/observability/metrics": mockWorkerMetrics(),
    ...mockResourceData,
  };
  return dataByPath[normalizedPath] ?? [];
}

export function mockPost(path: string, body?: unknown): unknown {
  const normalizedPath = stripQuery(path);
  const artifactShareCreate = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)\/shares$/);
  if (artifactShareCreate) {
    loadMockArchiveShares();
    const streamID = decodeURIComponent(artifactShareCreate[1]);
    const artifactID = decodeURIComponent(artifactShareCreate[2]);
    const artifact = mockArtifactByID(streamID, artifactID);
    if (!artifact) throw new Error("archive_not_found");
    const request = body as Partial<{ expires_in_hours: number; allow_download: boolean }>;
    const expiresInHours = Math.min(24 * 30, Math.max(1, Number(request.expires_in_hours || 24)));
    const token = `mock-share-${streamID}-${artifactID}-${Date.now()}`;
    const origin = typeof window === "undefined" ? "" : window.location.origin;
    const share: Record<string, unknown> = {
      id: `share-${Date.now()}`,
      token,
      stream_id: streamID,
      artifact_id: artifactID,
      allow_download: request.allow_download !== false,
      expires_at: new Date(Date.now() + expiresInHours * 60 * 60 * 1000).toISOString(),
      created_at: new Date().toISOString(),
    };
    const key = archiveShareKey(streamID, artifactID);
    mockArchiveShares[key] = [share, ...(mockArchiveShares[key] || [])];
    saveMockArchiveShares();
    return {
      ...publicMockArchiveShareAdmin(share),
      token,
      url: `${origin}/archive/share/?token=${encodeURIComponent(token)}`,
      api_url: `/archive-shares/${encodeURIComponent(token)}`,
    };
  }
  if (stripQuery(path) === "/auth/login") {
    return { csrf_token: "mock-csrf-token", user: mockCurrentUser.user };
  }
  if (/^\/auth\/oauth\/[^/]+\/start$/.test(stripQuery(path))) {
    const providerID = decodeURIComponent(stripQuery(path).replace(/^\/auth\/oauth\//, "").replace(/\/start$/, ""));
    const provider = mockLoginOAuthProviders().find((item) => item.id === providerID) || mockLoginOAuthProviders()[0];
    return {
      provider,
      authorization_url: "/admin/",
      state: "mock-oauth-login-state",
      nonce: "mock-oauth-login-nonce",
      expires_at: baseTime,
    };
  }
  if (stripQuery(path) === "/auth/change-password") {
    return { status: "password_changed" };
  }
  if (stripQuery(path) === "/auth/mfa/enroll") {
    mockMFAStatus.pending_enrollment = true;
    return {
      method: "totp",
      secret: "JBSWY3DPEHPK3PXP",
      provisioning_uri: "otpauth://totp/AutoStream:demo-admin?secret=JBSWY3DPEHPK3PXP&issuer=AutoStream",
      recovery_codes: ["AS-1111-2222", "AS-3333-4444", "AS-5555-6666"],
      message: "Verify a TOTP code to enable MFA.",
    };
  }
  if (stripQuery(path) === "/auth/mfa/verify") {
    mockMFAStatus.enabled = true;
    mockMFAStatus.pending_enrollment = false;
    mockMFAStatus.method = "totp";
    mockMFAStatus.recovery_code_count = 3;
    return { status: "mfa_enabled", method: "totp" };
  }
  if (stripQuery(path) === "/auth/email/confirm") {
    const request = body as { token?: string };
    if (!String(request.token || "").trim()) throw new Error("invalid_email_change_token");
    return { status: "email_changed", target: maskMockEmail(mockCurrentUser.user.email || "operator@example.jp") };
  }
  if (stripQuery(path) === "/auth/mfa/disable") {
    mockMFAStatus.enabled = false;
    mockMFAStatus.method = "";
    mockMFAStatus.recovery_code_count = 0;
    return { status: "mfa_disabled" };
  }
  if (stripQuery(path) === "/auth/recovery-codes/regenerate") {
    mockMFAStatus.recovery_code_count = 3;
    return { recovery_codes: ["AS-7777-8888", "AS-9999-0000", "AS-1212-3434"] };
  }
  if (/^\/auth\/oauth-links\/[^/]+\/start$/.test(stripQuery(path))) {
    const providerID = decodeURIComponent(stripQuery(path).replace(/^\/auth\/oauth-links\//, "").replace(/\/start$/, ""));
    const request = body as Partial<{ redirect_after: string }>;
    const provider = mockLoginOAuthProviders().find((item) => item.id === providerID) || mockLoginOAuthProviders()[0];
    return {
      provider,
      authorization_url: request.redirect_after || "/admin/account/",
      state: "mock-oauth-link-state",
      nonce: "mock-oauth-link-nonce",
      expires_at: baseTime,
    };
  }
  if (stripQuery(path) === "/auth/passkeys/register/start") {
    return {
      registration_token: "ast_pk_demo_registration",
      expires_at: baseTime,
      public_key: {
        challenge: "ZGVtby1jaGFsbGVuZ2U",
        rp: { id: "localhost", name: "AutoStream Demo" },
        user: { id: "dXNlci1kZW1vLWFkbWlu", name: "demo-admin", displayName: "demo-admin" },
        pubKeyCredParams: [{ type: "public-key", alg: -7 }],
        timeout: 60000,
        attestation: "none",
      },
    };
  }
  if (stripQuery(path) === "/auth/passkeys/register/finish") {
    const request = body as Partial<{ name: string }>;
    const passkey: PasskeyCredential = {
      id: `passkey-demo-${mockPasskeys.length + 1}`,
      user_id: "user-demo-admin",
      name: request.name || "Passkey",
      sign_count: 0,
      transports: ["internal"],
      backup_eligible: true,
      backed_up: false,
      created_at: baseTime,
      updated_at: baseTime,
    };
    mockPasskeys.unshift(passkey);
    return passkey;
  }
  if (stripQuery(path) === "/auth/passkeys/login/start") {
    return {
      challenge_token: "ast_pk_demo_login",
      expires_at: baseTime,
      public_key: {
        challenge: "ZGVtby1sb2dpbi1jaGFsbGVuZ2U",
        timeout: 60000,
        userVerification: "required",
      },
    };
  }
  if (stripQuery(path) === "/auth/passkeys/login/finish") {
    return { csrf_token: "mock-csrf-token", user: mockCurrentUser.user };
  }
  if (stripQuery(path) === "/users") {
    const request = body as Partial<{ username: string; email: string; role_ids: string[] }>;
    if (!String(request.email || "").trim()) throw new Error("email_required");
    const user = {
      id: `user-demo-${request.username || mockResourceData["/users"].length + 1}`,
      username: request.username || "operator",
      email: request.email || "",
      status: "pending_password_change",
      roles: request.role_ids || [],
      created_at: baseTime,
      updated_at: baseTime,
    };
    (mockResourceData["/users"] as Record<string, unknown>[]).unshift(user);
    return user;
  }
  if (stripQuery(path) === "/streams") {
    const request = body as Partial<Stream>;
    const id = `stream-demo-${mockStreams.length + 1}`;
    const stream: Stream = {
      id,
      name: request.name || "新規配信枠",
      status: "created",
      discord_config_id: request.discord_config_id,
      discord_guild_id: request.discord_guild_id,
      discord_voice_channel_id: request.discord_voice_channel_id,
      discord_text_channel_id: request.discord_text_channel_id,
      auto_start_trigger: request.auto_start_trigger,
      encoder_profile_id: request.encoder_profile_id,
      caption_profile_id: request.caption_profile_id,
      overlay_profile_id: request.overlay_profile_id,
      archive_profile_id: request.archive_profile_id || (request.archive_oauth_account_id || request.archive_retention_days ? `archive-${id}` : undefined),
      archive_drive_destination_id: request.archive_oauth_account_id ? `drive-${id}` : undefined,
      archive_oauth_account_id: request.archive_oauth_account_id,
      archive_folder_id_configured: Boolean((request as Partial<Stream> & { archive_folder_id?: string }).archive_folder_id),
      archive_masked_folder_id: (request as Partial<Stream> & { archive_folder_id?: string }).archive_folder_id ? "fol...ock" : undefined,
      archive_shared_drive: request.archive_shared_drive,
      archive_shared_drive_id: request.archive_shared_drive_id,
      archive_file_name: request.archive_file_name || (request.archive_oauth_account_id ? `${request.name || "新規配信枠"}-20260702.mp4` : undefined),
      archive_retention_days: request.archive_retention_days,
      youtube_output_id: request.youtube_output_id,
      encoder_input_url: request.encoder_input_url,
      created_at: baseTime,
      updated_at: baseTime,
    };
    mockStreams.unshift(stream);
    return stream;
  }
  if (stripQuery(path) === "/settings/app/test-email") {
    const request = body as { to?: string };
    const to = String(request.to || "").trim();
    if (!to || !to.includes("@") || /[\r\n\t]/.test(to)) {
      throw new Error("invalid_email_recipient");
    }
    return { status: "sent", target: maskMockEmail(to) };
  }
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
  const configureTokenRotate = stripQuery(path).match(/^\/nodes\/([^/]+)\/configure-token$/);
  if (configureTokenRotate) {
    const nodeID = decodeURIComponent(configureTokenRotate[1]);
    const node = mockWorkers.find((item) => (item.service_id || item.id) === nodeID) || mockWorkers[0];
    const configureToken = "ast_cfg_demo_rotated_7c8f1a2d";
    node.configure_token_expires_at = baseTime;
    node.configure_token_used_at = undefined;
    return {
      node,
      configure_token: configureToken,
      configure_token_expires_at: baseTime,
      configure_command: mockConfigureCommand(node.service_type, node.service_id || node.id, configureToken),
    };
  }
  const runtimeTokenRotate = stripQuery(path).match(/^\/nodes\/([^/]+)\/rotate-token$/);
  if (runtimeTokenRotate) {
    const nodeID = decodeURIComponent(runtimeTokenRotate[1]);
    const node = mockWorkers.find((item) => (item.service_id || item.id) === nodeID) || mockWorkers[0];
    const runtimeToken = "ast_svc_demo_rotated_2f6d0b8e";
    const runtimeTokenID = "runtime-token-demo-rotated";
    const host = node.host || "worker-main.example.jp";
    const port = node.port || 8443;
    const sslEnabled = node.ssl_enabled ?? true;
    node.node_token_rotated_at = baseTime;
    return {
      node,
      runtime_token_id: runtimeTokenID,
      runtime_token: runtimeToken,
      configuration_yaml: `panel:\n  url: "https://control.example.jp"\n\nnode:\n  id: "${node.service_id || node.id}"\n  name: "${node.service_name}"\n  type: "${node.service_type}"\n\napi:\n  host: "${host}"\n  port: ${port}\n  ssl_enabled: ${sslEnabled}\n\nauth:\n  token_id: "${runtimeTokenID}"\n  token: "${runtimeToken}"\n`,
    };
  }
  return { ok: true };
}

function mockConfigureCommand(serviceType: string, nodeID: string, configureToken: string) {
  const configureBinary = mockConfigureBinary(serviceType);
  return `sudo ${configureBinary} configure --panel-url "https://control.example.jp" --token "${configureToken}" --node "${nodeID}" --config "${mockConfigPath(serviceType)}"`;
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

function mockConfigPath(serviceType: string) {
  switch (serviceType) {
    case "encoder_recorder":
      return "/etc/autostream-encoder-recorder/config.yml";
    case "discord_bot":
      return "/etc/autostream-discord-bot/config.yml";
    case "observability":
      return "/etc/autostream-observability/config.yml";
    default:
      return "/etc/autostream-worker/config.yml";
  }
}

export function mockPut(path: string, body?: unknown): unknown {
  const normalizedPath = stripQuery(path);
  const artifactUpdate = stripQuery(path).match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)$/);
  if (artifactUpdate) {
    const streamID = decodeURIComponent(artifactUpdate[1]);
    const artifactID = decodeURIComponent(artifactUpdate[2]);
    const request = body as Partial<{ name: string }>;
    const name = String(request.name || "").trim();
    if (!/^[A-Za-z0-9._-]+\.(mp4|mkv|json|jsonl|vtt)$/.test(name) || name.includes("..")) {
      throw new Error("invalid_stream_artifact");
    }
    const artifacts = mockStreamArtifacts[streamID] || [];
    const artifact = artifacts.find((item) => item.id === artifactID);
    if (!artifact) throw new Error("not_found");
    artifact.name = name;
    artifact.relative_path = `final/${streamID}/${name}`;
    return artifact;
  }
  if (stripQuery(path) === "/auth/email") {
    const request = body as { email?: string };
    const email = String(request.email || "").trim();
    if (!email) {
      throw new Error("email_required");
    }
    if (!email.includes("@") || /[\r\n\t]/.test(email)) {
      throw new Error("invalid_email");
    }
    return { status: "confirmation_sent", target: maskMockEmail(email) };
  }
  if (stripQuery(path) === "/settings/app") {
    const request = body as Partial<AppSettings> & { smtp_password?: string; turnstile_secret?: string };
    const smtpEnabled = Boolean(request.smtp_enabled);
    const turnstileEnabled = Boolean(request.turnstile_enabled);
    mockAppSettings = {
      app_name: request.app_name || mockAppSettings.app_name,
      timezone: request.timezone || mockAppSettings.timezone,
      smtp_enabled: smtpEnabled,
      smtp_host: smtpEnabled ? request.smtp_host || "" : undefined,
      smtp_port: smtpEnabled ? request.smtp_port || 587 : 587,
      smtp_starttls: smtpEnabled ? request.smtp_starttls ?? true : true,
      smtp_from: smtpEnabled ? request.smtp_from || "" : undefined,
      smtp_username: smtpEnabled ? request.smtp_username || "" : undefined,
      smtp_password_configured: smtpEnabled ? Boolean(request.smtp_password || mockAppSettings.smtp_password_configured) : false,
      turnstile_enabled: turnstileEnabled,
      turnstile_site_key: turnstileEnabled ? request.turnstile_site_key || "" : undefined,
      turnstile_configured: turnstileEnabled ? Boolean(request.turnstile_secret || mockAppSettings.turnstile_configured) : false,
      updated_at: baseTime,
    };
    return mockAppSettings;
  }
  const nodeUpdate = stripQuery(path).match(/^\/nodes\/([^/]+)$/);
  if (nodeUpdate) {
    const nodeID = decodeURIComponent(nodeUpdate[1]);
    const request = body as Partial<{ service_name: string; name: string; description: string; host: string; port: number; ssl_enabled: boolean }>;
    const index = mockWorkers.findIndex((node) => (node.service_id || node.id) === nodeID);
    if (index < 0) throw new Error("not_found");
    const existing = mockWorkers[index];
    const host = request.host || existing.host || "worker-main.example.jp";
    const port = request.port || existing.port || 8443;
    const sslEnabled = request.ssl_enabled ?? existing.ssl_enabled ?? true;
    const next: WorkerNode = {
      ...existing,
      service_name: request.service_name || request.name || existing.service_name,
      description: request.description ?? existing.description,
      host,
      port,
      ssl_enabled: sslEnabled,
      public_url: `${sslEnabled ? "https" : "http"}://${host}:${port}`,
    };
    mockWorkers[index] = next;
    return next;
  }
  const collectionPath = mockDeleteCollectionPath(normalizedPath);
  if (collectionPath) {
    const id = decodeURIComponent(normalizedPath.slice(collectionPath.length + 1));
    const rows = mockResourceData[collectionPath];
    if (!Array.isArray(rows)) return { ok: true };
    const index = (rows as Record<string, unknown>[]).findIndex((row) => ["id", "service_id", "name"].some((key) => row[key] === id));
    if (index < 0) throw new Error("not_found");
    const request = (body || {}) as Record<string, unknown>;
    const existing = (rows as Record<string, unknown>[])[index];
    const next: Record<string, unknown> = { ...existing, ...request, id, updated_at: baseTime };
    if (collectionPath === "/integrations/oauth-providers") {
      next.client_secret_configured = Boolean(request.client_secret || existing.client_secret_configured);
      delete next.client_secret;
    }
    (rows as Record<string, unknown>[])[index] = next;
    return next;
  }
  return { ok: true };
}

export function mockDelete(path: string): unknown {
  const normalizedPath = stripQuery(path);
  const artifactShareDelete = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)\/shares\/([^/]+)$/);
  if (artifactShareDelete) {
    loadMockArchiveShares();
    const streamID = decodeURIComponent(artifactShareDelete[1]);
    const artifactID = decodeURIComponent(artifactShareDelete[2]);
    const shareID = decodeURIComponent(artifactShareDelete[3]);
    const shares = mockArchiveShares[archiveShareKey(streamID, artifactID)] || [];
    const share = shares.find((item) => item.id === shareID);
    if (!share) throw new Error("not_found");
    share.revoked_at = new Date().toISOString();
    saveMockArchiveShares();
    return { status: "revoked" };
  }
  const artifactDelete = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)$/);
  if (artifactDelete) {
    const streamID = decodeURIComponent(artifactDelete[1]);
    const artifactID = decodeURIComponent(artifactDelete[2]);
    const artifacts = mockStreamArtifacts[streamID] || [];
    mockStreamArtifacts[streamID] = artifacts.filter((item) => item.id !== artifactID);
    return { status: "deleted" };
  }
  if (/^\/auth\/passkeys\/[^/]+$/.test(normalizedPath)) {
    deleteFromArray(mockPasskeys as unknown as Record<string, unknown>[], decodeURIComponent(normalizedPath.replace(/^\/auth\/passkeys\//, "")));
    return undefined;
  }
  if (/^\/auth\/oauth-links\/[^/]+$/.test(normalizedPath)) {
    deleteFromArray(mockOAuthLinks as unknown as Record<string, unknown>[], decodeURIComponent(normalizedPath.replace(/^\/auth\/oauth-links\//, "")));
    return { status: "deleted" };
  }
  if (/^\/services\/[^/]+$/.test(normalizedPath)) {
    const id = decodeURIComponent(normalizedPath.replace(/^\/services\//, ""));
    deleteFromArray(mockWorkers, id);
    return { status: "deleted" };
  }
  const collectionPath = mockDeleteCollectionPath(normalizedPath);
  if (!collectionPath) return { status: "deleted" };
  const id = decodeURIComponent(normalizedPath.slice(collectionPath.length + 1));
  const rows = mockResourceData[collectionPath];
  if (Array.isArray(rows)) deleteFromArray(rows as Record<string, unknown>[], id);
  return { status: "deleted" };
}

export function mockPathExists(path: string) {
  const normalizedPath = stripQuery(path);
  if (/^\/streams\/[^/]+\/artifacts(?:\/[^/]+)?(?:\/download)?$/.test(normalizedPath)) return true;
  if (/^\/streams\/[^/]+\/artifacts\/[^/]+\/shares(?:\/[^/]+)?$/.test(normalizedPath)) return true;
  if (/^\/archive-shares\/[^/]+(?:\/download)?$/.test(normalizedPath)) return true;
  if (/^\/nodes\/[^/]+\/configuration$/.test(normalizedPath)) return true;
  if (/^\/nodes\/[^/]+$/.test(normalizedPath)) return true;
  if (/^\/nodes\/[^/]+\/configure-token$/.test(normalizedPath)) return true;
  if (/^\/nodes\/[^/]+\/rotate-token$/.test(normalizedPath)) return true;
  if (/^\/services\/[^/]+$/.test(normalizedPath)) return true;
  if (/^\/auth\/passkeys\/[^/]+$/.test(normalizedPath)) return true;
  if (/^\/auth\/oauth\/[^/]+\/start$/.test(normalizedPath)) return true;
  if (/^\/auth\/oauth-links\/[^/]+\/start$/.test(normalizedPath)) return true;
  if (/^\/auth\/oauth-links\/[^/]+$/.test(normalizedPath)) return true;
  if (mockDeleteCollectionPath(normalizedPath)) return true;
  return new Set([
    "/auth/me",
    "/auth/login",
    "/auth/email",
    "/auth/email/confirm",
    "/auth/change-password",
    "/auth/mfa/status",
    "/auth/mfa/enroll",
    "/auth/mfa/verify",
    "/auth/mfa/disable",
    "/auth/recovery-codes/regenerate",
    "/auth/passkeys",
    "/auth/passkeys/register/start",
    "/auth/passkeys/register/finish",
    "/auth/passkeys/login/start",
    "/auth/passkeys/login/finish",
    "/auth/oauth-links",
    "/auth/oauth/providers",
    "/setup/status",
    "/settings/app",
    "/settings/app/test-email",
    "/version",
    "/streams",
    "/workers",
    "/nodes",
    "/service-health",
    "/audit-logs",
    "/observability/metrics",
    "/nodes/registration-tokens",
    "/integrations/oauth-accounts/start",
    ...Object.keys(mockResourceData),
  ]).has(normalizedPath);
}

function mockLoginOAuthProviders(): OAuthLoginProvider[] {
  const providers = mockResourceData["/integrations/oauth-providers"] as OAuthLoginProvider[];
  return providers.filter((provider) => provider.enabled);
}

function mockDeleteCollectionPath(path: string) {
  return Object.keys(mockResourceData)
    .filter((collectionPath) => path.startsWith(`${collectionPath}/`))
    .sort((a, b) => b.length - a.length)[0];
}

function deleteFromArray(rows: Record<string, unknown>[], id: string) {
  const index = rows.findIndex((row) => {
    for (const key of ["id", "service_id", "name"]) {
      const value = row[key];
      if (typeof value === "string" && value === id) return true;
    }
    return false;
  });
  if (index >= 0) rows.splice(index, 1);
}

function stripQuery(path: string) {
  return String(path || "").split("?")[0];
}

function archiveShareKey(streamID: string, artifactID: string) {
  return `${streamID}/${artifactID}`;
}

function loadMockArchiveShares() {
  if (mockArchiveSharesLoaded || typeof window === "undefined") return;
  mockArchiveSharesLoaded = true;
  try {
    const raw = window.sessionStorage.getItem(mockArchiveSharesStorageKey);
    if (!raw) return;
    const parsed = JSON.parse(raw) as Record<string, Array<Record<string, unknown>>>;
    for (const [key, shares] of Object.entries(parsed)) {
      mockArchiveShares[key] = Array.isArray(shares) ? shares : [];
    }
  } catch {
    window.sessionStorage.removeItem(mockArchiveSharesStorageKey);
  }
}

function saveMockArchiveShares() {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(mockArchiveSharesStorageKey, JSON.stringify(mockArchiveShares));
}

function publicMockArchiveShareAdmin(share: Record<string, unknown>) {
  const safeShare = { ...share };
  delete safeShare.token;
  return { ...safeShare, status: mockArchiveShareStatus(share) };
}

function publicMockArchiveShare(token: string) {
  loadMockArchiveShares();
  for (const [key, shares] of Object.entries(mockArchiveShares)) {
    const share = shares.find((item) => item.token === token);
    if (!share) continue;
    const status = mockArchiveShareStatus(share);
    if (status !== "active") throw new Error(status === "revoked" ? "archive_share_revoked" : "archive_share_expired");
    const [streamID, artifactID] = key.split("/");
    const artifact = mockArtifactByID(streamID, artifactID);
    const stream = mockStreams.find((item) => item.id === streamID);
    if (!artifact) throw new Error("archive_not_found");
    const allowDownload = share.allow_download !== false;
    return {
      stream_name: stream?.name || streamID,
      artifact_name: String(artifact.name || artifactID),
      artifact_kind: String(artifact.kind || "archive"),
      size_bytes: Number(artifact.size_bytes || 0),
      created_at: String(artifact.created_at || baseTime),
      allow_download: allowDownload,
      expires_at: String(share.expires_at || baseTime),
      playback_url: `/archive-shares/${encodeURIComponent(token)}/download`,
      download_url: allowDownload ? `/archive-shares/${encodeURIComponent(token)}/download?download=1` : undefined,
    };
  }
  throw new Error("archive_not_found");
}

function mockArchiveShareStatus(share: Record<string, unknown>) {
  if (share.revoked_at) return "revoked";
  const expiresAt = Date.parse(String(share.expires_at || ""));
  if (Number.isFinite(expiresAt) && expiresAt <= Date.now()) return "expired";
  return "active";
}

function mockArtifactByID(streamID: string, artifactID: string) {
  return (mockStreamArtifacts[streamID] || []).find((item) => item.id === artifactID);
}

function maskMockEmail(value: string) {
  const [local, domain] = value.split("@");
  if (!local || !domain) return "masked";
  return `${local.slice(0, 1)}***@${domain}`;
}
