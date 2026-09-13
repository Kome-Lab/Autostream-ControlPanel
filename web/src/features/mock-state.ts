import type { AuditLog, CurrentUser, ManagedAppSettings, MFAStatus, OAuthUserLink, PasskeyCredential, SetupStatus, Stream, SystemUpdateAgentStatus, SystemUpdateHostStatus, SystemUpdateJob, SystemUpdateTarget, UpdaterHostBootstrapJob, UpdaterSettings, WorkerNode } from "@/types/domain";
import { createMockWorkerSeeds } from "./mock-worker-seeds";


export const baseTime = "2026-07-02T09:00:00+09:00";

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
    "service_health.read",
    "audit_logs.read",
    "audit_logs.export",
    "api_tokens.create",
    "api_tokens.read",
    "secrets.update",
    "remediation.execute",
    "system_updates.read",
    "system_updates.execute",
  ],
};

export const mockMFAStatus: MFAStatus = {
  available: true,
  enabled: false,
  pending_enrollment: false,
  recovery_code_count: 0,
  policy_mode: "totp",
  required: false,
};

export const mockPasskeys: PasskeyCredential[] = [
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

export const mockOAuthLinks: OAuthUserLink[] = [
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
    auto_start_trigger: "discord_voice_join",
    updated_at: "2026-07-02T08:58:00+09:00",
  },
];

export const mockWorkers: WorkerNode[] = createMockWorkerSeeds(baseTime);

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
  {
    id: "audit-005",
    timestamp: "2026-07-02T08:59:40+09:00",
    action: "services.runtime_config.read",
    actor_username: "encoder-field",
    actor_ip: "192.0.2.30",
    user_agent: "AutoStream Encoder Recorder",
    result: "success",
    resource_type: "service",
    resource_id: "encoder-field",
  },
  {
    id: "audit-006",
    timestamp: "2026-07-02T08:59:45+09:00",
    action: "services.register",
    actor_username: "host-agent-control",
    actor_ip: "192.0.2.31",
    user_agent: "AutoStream Host Agent",
    result: "success",
    resource_type: "service",
    resource_id: "host-agent-control",
  },
];

export const mockSetupStatus: SetupStatus = {
  setup_enabled: true,
  setup_required: false,
};

export let mockAppSettings: ManagedAppSettings = {
  app_name: "AutoStream",
  timezone: "Asia/Tokyo",
  google_analytics_enabled: false,
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
  service_updates: {
    worker: { latest_version: "v1.2.1", update_check_source: "mock" },
    encoder_recorder: { latest_version: "v1.2.0", update_check_source: "mock" },
    discord_bot: { latest_version: "v1.1.8", update_check_source: "mock" },
    observability: { latest_version: "v1.1.5", update_check_source: "mock" },
  },
};

export const mockSystemUpdateUpdaters: SystemUpdateAgentStatus[] = [
  {
    updater_id: "host-agent-control",
    name: "Control Panel Host Agent",
    status: "online",
    online: true,
    version: "v2.0.0",
    transport_mode: "pull_v2",
    execution_host_id: "host-control",
    ownership_epoch: 3,
    last_heartbeat_at: baseTime,
    desired_revision: 1,
    applied_revision: 1,
    policy_status: "applied",
    ssh_client_public_keys: {
      "host-control": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockControl autostream-updater@host-agent-control",
      "host-main": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockMain autostream-updater@host-agent-control",
    },
    ssh_client_key_fingerprints: {
      "host-control": "SHA256:mock-control-client-key",
      "host-main": "SHA256:mock-main-client-key",
    },
    bootstrap_encryption_public_key: "BJyOxKf1A_sVcFK8CDwtgHrwjGdJdiTQiOf3kidPHjnIZpzvhyczkLsUDFqEPWVnhkWGe5YbpjlafiIdMO7s_iU",
    bootstrap_encryption_key_fingerprint: "SHA256:zAub8CwOeAN1WI8elABGIcIi2gdIyoxvFPxQ7HcqRlo",
  },
  {
    updater_id: "host-agent-observability",
    name: "監視ホスト Host Agent",
    status: "online",
    online: true,
    version: "v2.0.0",
    transport_mode: "pull_v2",
    execution_host_id: "host-observability",
    ownership_epoch: 2,
    last_heartbeat_at: baseTime,
    desired_revision: 4,
    applied_revision: 4,
    policy_status: "applied",
  },
];

export let mockUpdaterSettings: UpdaterSettings = {
  updater_id: "host-agent-control",
  revision: 1,
  projection_revision: 1,
  local_executor_policy_revision: 1,
  transport_mode: "pull_v2",
  execution_host_id: "host-control",
  execution_host_ownership: {
    transport_mode: "pull_v2",
    agent_service_id: "host-agent-control",
    ownership_epoch: 3,
    policy_revision: 1,
  },
  pull_activation: {
    ready: true,
    status: "ready",
    last_heartbeat_at: baseTime,
    observe_only: false,
    update_executor: true,
    mutation_enabled: true,
    recovery_pending: false,
    reported_ownership_epoch: 3,
    reported_projection_revision: 1,
  },
  local_executor_policy_sha256: `sha256:${"a".repeat(64)}`,
  api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false, tls_cert_file: "", tls_key_file: "" },
  poll_interval_seconds: 15,
  heartbeat_interval_seconds: 30,
  hosts: [
    {
      host_id: "host-control",
      name: "Control Panelホスト",
      address: "127.0.0.1",
      port: 55850,
      user: "autostream-update-host",
      arch: "amd64",
      host_public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockHostControl root@control",
      host_key_fingerprint: "SHA256:mock-control-host-key",
    },
    {
      host_id: "host-main",
      name: "本社メインホスト",
      address: "192.0.2.10",
      port: 55850,
      user: "autostream-update-host",
      arch: "amd64",
      host_public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockHostMain root@main",
      host_key_fingerprint: "SHA256:mock-main-host-key",
    },
  ],
  targets: [
    { target_id: "control-panel", service_id: "control-panel", host_id: "host-control", service_type: "control_panel", deployment_mode: "systemd" },
  ],
  github_token_configured: true,
  github_token_fingerprint: "sha256:mock-token",
  updated_at: baseTime,
};

export const mockSystemUpdateHosts: SystemUpdateHostStatus[] = [
  { host_id: "host-control", name: "Control Panelホスト", updater_id: "host-agent-control", reachability: "reachable", reachability_checked_at: baseTime },
  { host_id: "host-main", name: "本社メインホスト", updater_id: "host-agent-control", reachability: "reachable", reachability_checked_at: baseTime },
  { host_id: "host-city", name: "庁舎スタンバイホスト", updater_id: "host-agent-control", reachability: "reachable", reachability_checked_at: baseTime },
  { host_id: "host-field", name: "現場持出ホスト", updater_id: "host-agent-control", reachability: "unreachable", reachability_checked_at: baseTime, reachability_code: "host_agent_offline" },
  { host_id: "host-observability", name: "監視ホスト", updater_id: "host-agent-observability", reachability: "reachable", reachability_checked_at: baseTime },
];

export const mockSystemUpdateTargets: SystemUpdateTarget[] = [
  { target_id: "control-panel", target_type: "control_panel", name: "Control Panel", host_id: "host-control", current_version: "v1.2.3", latest_version: "v1.2.4", update_available: true, deployment_mode: "docker_compose", updater_id: "host-agent-control", updater_online: true, eligible: true },
  { target_id: "worker-main", target_type: "worker", name: "本社メインWorker", host_id: "host-main", current_version: "v1.2.0", latest_version: "v1.2.1", update_available: true, deployment_mode: "systemd", updater_id: "host-agent-control", updater_online: true, busy: true, current_stream_id: "stream-cable-morning", eligible: true },
  { target_id: "worker-standby", target_type: "worker", name: "庁舎スタンバイWorker", host_id: "host-city", current_version: "v1.1.8", latest_version: "v1.2.1", update_available: true, deployment_mode: "systemd", updater_id: "host-agent-control", updater_online: true, eligible: true },
  { target_id: "encoder-main", target_type: "encoder_recorder", name: "本社エンコーダー", host_id: "host-main", current_version: "v1.2.0", latest_version: "v1.2.0", update_available: false, deployment_mode: "systemd", updater_id: "host-agent-control", updater_online: true, current_stream_id: "stream-cable-morning", eligible: false, blocked_reason: "update_not_available" },
  { target_id: "encoder-field", target_type: "encoder_recorder", name: "現場持出エンコーダー", host_id: "host-field", current_version: "v1.1.4", latest_version: "v1.2.0", update_available: true, deployment_mode: "systemd", updater_id: "host-agent-control", updater_online: true, current_stream_id: "stream-fm-special", eligible: false, blocked_reason: "target_unreachable" },
  {
    target_id: "observability-main",
    target_type: "observability",
    name: "Observability",
    host_id: "host-observability",
    current_version: "v1.1.3",
    latest_version: "v1.1.5",
    update_available: true,
    deployment_mode: "docker",
    updater_id: "host-agent-observability",
    updater_online: true,
    eligible: true,
    eligible_operations: ["software_update", "port_reconfigure"],
    port_contract_version: 2, port_policy_snapshot_id: `ps1:${"a".repeat(64)}`, local_listen_port: 18090,
    endpoint_revision: 7, applied_endpoint_revision: 7, applied_config_revision: 5, ownership_epoch: 3,
    port_modes: ["local_only", "local_and_advertised"],
    port_mapping: {
      mode: "docker",
      advertised_port: 443,
      published_host_ip: "127.0.0.1",
      published_port: 18090,
      container_port: 8080,
      health_port: 18090,
      config_revision: 5,
      state: "applied",
      reported_at: baseTime,
    },
  },
];

export const mockSystemUpdateJobs: SystemUpdateJob[] = [
  { id: "update-demo-1", target_id: "worker-standby", target_type: "worker", current_version: "v1.1.7", target_version: "v1.1.8", deployment_mode: "systemd", status: "succeeded", progress: 100, message: "更新とヘルスチェックが完了しました。", requested_by: "demo-admin", created_at: "2026-07-02T08:10:00+09:00", updated_at: "2026-07-02T08:12:30+09:00", completed_at: "2026-07-02T08:12:30+09:00" },
];

export const mockUpdaterHostBootstrapJobs: UpdaterHostBootstrapJob[] = [];

export const mockResourceData: Record<string, unknown[]> = {
  "/profiles/encoder": [
    { id: "enc-profile-1080p", name: "1080p60 標準", width: 1920, height: 1080, fps: 60, bitrate_kbps: 7800, updated_at: baseTime },
    { id: "enc-profile-mobile", name: "現場回線向け 720p", width: 1280, height: 720, fps: 30, bitrate_kbps: 3500, updated_at: "2026-07-02T08:35:00+09:00" },
  ],
  "/profiles/caption": [
    { id: "caption-live-ja", name: "日本語ライブ字幕", language: "ja", provider: "deepgram", model: "nova-3", endpointing_ms: 300, interim_results: true, smart_format: true, delay_ms: 800, updated_at: baseTime },
    { id: "caption-live-en", name: "英語ライブ字幕", language: "en", provider: "deepgram", model: "nova-3", endpointing_ms: 300, interim_results: true, smart_format: true, delay_ms: 800, updated_at: "2026-07-01T18:20:00+09:00" },
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
    { id: "acct-drive", provider_id: "google-main", provider_type: "google", provider_name: "Google Workspace", account_label: "広報 Drive", display_name: "広報 Drive", email: "archive@example.jp", account_purpose: "drive", scopes: ["openid", "email", "https://www.googleapis.com/auth/drive.file"], status: "connected", refresh_token_configured: true, refresh_token_updated_at: baseTime, access_token_refreshed_at: baseTime, updated_at: baseTime },
    { id: "acct-youtube", provider_id: "google-main", provider_type: "google", provider_name: "Google Workspace", account_label: "YouTube 管理", display_name: "YouTube 管理", email: "live@example.jp", account_purpose: "youtube", scopes: ["openid", "email", "https://www.googleapis.com/auth/youtube.force-ssl"], status: "connected", refresh_token_configured: true, refresh_token_updated_at: "2026-07-01T16:10:00+09:00", access_token_refreshed_at: "2026-07-01T16:40:00+09:00", updated_at: "2026-07-01T16:10:00+09:00" },
    { id: "acct-both", provider_id: "google-main", provider_type: "google", provider_name: "Google Workspace", account_label: "配信運用 共通", display_name: "配信運用 共通", email: "operations@example.jp", account_purpose: "drive_youtube", scopes: ["openid", "email", "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube.force-ssl"], status: "connected", refresh_token_configured: true, refresh_token_updated_at: "2026-07-02T09:30:00+09:00", access_token_refreshed_at: "2026-07-02T10:00:00+09:00", updated_at: "2026-07-02T09:30:00+09:00" },
  ],
  "/users": [
    { id: "user-admin", username: "admin", email: "admin@example.jp", status: "active", roles: ["super_admin"], role_ids: ["role-super-admin"], last_login_at: baseTime },
    { id: "user-operator", username: "operator", email: "operator@example.jp", status: "active", roles: ["operator"], role_ids: ["role-operator"], last_login_at: "2026-07-02T08:20:00+09:00" },
  ],
  "/roles": [
    { id: "role-super-admin", name: "super_admin", permissions: ["*"], updated_at: baseTime },
    { id: "role-operator", name: "operator", permissions: ["streams.read", "streams.start", "streams.stop"], updated_at: "2026-07-01T09:00:00+09:00" },
  ],
  "/permissions": [
    { id: "streams.read", name: "streams.read", group: "streams" },
    { id: "system_settings.update", name: "system_settings.update", group: "settings" },
    { id: "system_updates.read", name: "system_updates.read", group: "system_updates" },
    { id: "system_updates.execute", name: "system_updates.execute", group: "system_updates" },
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
    { incident_id: "inc-1", rule: "encoder_process_exited", severity: "warning", status: "acknowledged", service_id: "encoder-field", stream_id: "stream-cable-morning", diagnostic_report: { summary: "Encoderのハートビートを再確認してください。", safe_auto_candidates: ["refresh_service_status", "rerun_diagnostics"] }, updated_at: baseTime },
    { incident_id: "inc-2", rule: "youtube_live_api_retry", severity: "info", status: "resolved", service_id: "worker-main", diagnostic_report: { summary: "YouTube APIの再試行は完了しています。" }, updated_at: "2026-07-02T08:58:00+09:00" },
  ],
  "/observability/remediation-actions": [
    { id: "rem-1", incident_id: "inc-1", mode: "manual_approval", status: "pending_approval", action: "restart_encoder", safe_auto: false, requires_approval: true, result: "承認待ち", created_at: baseTime, updated_at: baseTime },
    { id: "rem-2", incident_id: "inc-2", mode: "safe_auto", status: "executed", action: "switch_worker", safe_auto: true, requires_approval: false, result: "recorded_noop", created_at: "2026-07-02T08:30:00+09:00", updated_at: "2026-07-02T08:31:00+09:00" },
    { id: "rem-3", incident_id: "inc-1", mode: "suggest_only", status: "blocked", action: "rerun_diagnostics", safe_auto: true, requires_approval: false, result: "remediation mode is suggest_only", created_at: baseTime, updated_at: baseTime },
  ],
  "/observability/notification-deliveries": [
    { id: "ntf-1", event_type: "incident.opened", status: "success", channel: "discord", incident_id: "inc-1", created_at: baseTime },
    { id: "ntf-2", event_type: "incident.updated", status: "retrying", channel: "email", incident_id: "inc-2", created_at: "2026-07-02T08:40:00+09:00" },
    { id: "ntf-3", event_type: "admin.audit", status: "success", channel: "slack", incident_id: "", metadata: { rule: "integrations.oauth_account.update", summary: "OAuth接続アカウントを更新\n対象: OAuth接続アカウント (acct-01)\n実行者: ops" }, created_at: "2026-07-02T09:05:00+09:00" },
  ],
  "/observability/notification-channels": [
    { id: "chn-1", name: "制作Discord", type: "discord", enabled: true, masked_webhook_url: "https://example.jp/<WEBHOOK_PATH>" },
    { id: "chn-2", name: "運用Slack", type: "slack", enabled: true, masked_webhook_url: "https://hooks.slack.com/<WEBHOOK_PATH>", severity_filter: ["critical", "error", "warning", "info"], event_type_filter: ["incident.opened", "admin.audit"] },
    { id: "chn-3", name: "運用メール", type: "email", enabled: true, masked_email_target: "o***s@example.jp", uses_global_smtp: true, severity_filter: ["critical", "error"], event_type_filter: ["incident.opened", "incident.resolved"] },
  ],
};

export const mockStreamArtifacts: Record<string, Array<Record<string, unknown>>> = {
  "stream-cable-morning": [
    { id: "artifact-morning-final", stream_id: "stream-cable-morning", kind: "archive", name: "final.mp4", relative_path: "final/stream-cable-morning/final.mp4", size_bytes: 734003200, created_at: baseTime },
    { id: "artifact-morning-metadata", stream_id: "stream-cable-morning", kind: "metadata", name: "metadata.json", relative_path: "final/stream-cable-morning/metadata.json", size_bytes: 4096, created_at: baseTime },
  ],
  "stream-council": [
    { id: "artifact-council-final", stream_id: "stream-council", kind: "archive", name: "council-20260702.mp4", relative_path: "final/stream-council/council-20260702.mp4", size_bytes: 2147483648, created_at: "2026-07-02T16:35:00+09:00" },
  ],
};

export const mockArchiveShares: Record<string, Array<Record<string, unknown>>> = {};
export const mockArchiveSharesStorageKey = "autostream.mock_archive_shares";
export let mockArchiveSharesLoaded = false;


export function replaceMockUpdaterSettings(value: UpdaterSettings) {
  mockUpdaterSettings = value;
  return value;
}


export function replaceMockAppSettings(value: ManagedAppSettings) {
  mockAppSettings = value;
  return value;
}


export function setMockArchiveSharesLoaded(value: boolean) {
  mockArchiveSharesLoaded = value;
  return value;
}
