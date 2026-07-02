import type { TranslationKey } from "@/lib/i18n";

export type ResourceDefinition = {
  title: string;
  path: string;
  description: string;
  createTemplate?: Record<string, unknown>;
};

export type ResourcePageConfig = {
  titleKey: TranslationKey;
  description: string;
  resources: ResourceDefinition[];
};

export const resourcePages = {
  encoder: {
    titleKey: "encoder",
    description: "配信品質ごとのエンコード設定を管理します。解像度、フレームレート、ビットレートを案件ごとに再利用できます。",
    resources: [
      {
        title: "Encoder profiles",
        path: "/profiles/encoder",
        description: "Worker / Encoder が配信開始時に参照する映像変換プロファイルです。",
        createTemplate: { name: "1080p60", config: { width: 1920, height: 1080, fps: 60, video_bitrate_kbps: 8000 } },
      },
    ],
  },
  discord: {
    titleKey: "discord",
    description: "Discord Bot連携、音声転送、通知先を案件単位で管理します。",
    resources: [
      {
        title: "Discord configs",
        path: "/discord/configs",
        description: "Discordサービス、ギルド、ボイスチャンネル、再接続ポリシーの設定です。",
        createTemplate: { name: "main-guild", service_id: "discord-01", guild_id: "guild-01", voice_channel_id: "voice-01", audio_forward_enabled: true },
      },
    ],
  },
  youtube: {
    titleKey: "youtube",
    description: "YouTube Liveへの出力先、公開範囲、Live API利用設定を管理します。",
    resources: [
      {
        title: "YouTube outputs",
        path: "/youtube/outputs",
        description: "配信開始時に使うRTMPまたはYouTube Live API出力です。",
        createTemplate: { name: "public-live", mode: "live_api_dry_run", privacy_status: "public", rtmp_url: "rtmps://example.youtube.com/live2" },
      },
    ],
  },
  caption: {
    titleKey: "caption",
    description: "字幕生成や手動字幕のプロファイルを管理します。",
    resources: [
      {
        title: "Caption profiles",
        path: "/profiles/caption",
        description: "字幕言語、プロバイダ、遅延補正などの設定です。",
        createTemplate: { name: "日本語ライブ字幕", config: { language: "ja-JP", provider: "deepgram", delay_ms: 800 } },
      },
    ],
  },
  overlay: {
    titleKey: "overlay",
    description: "テロップ、進行表示、下部表示などのオーバーレイ設定を管理します。",
    resources: [
      {
        title: "Overlay profiles",
        path: "/profiles/overlay",
        description: "画面上に出す案内や番組情報のテンプレートです。",
        createTemplate: { name: "lower-third", config: { safe_area: "16:9 lower", theme: "public" } },
      },
    ],
  },
  archive: {
    titleKey: "archive",
    description: "録画プロファイルと保存先をまとめて管理します。",
    resources: [
      {
        title: "Archive profiles",
        path: "/profiles/archive",
        description: "録画形式、保存期間、アップロード有無の設定です。",
        createTemplate: { name: "shared-drive", config: { format: "mp4", retention_days: 180, upload_enabled: true } },
      },
      {
        title: "Drive destinations",
        path: "/archive/destinations",
        description: "Google Driveなどのアーカイブ保存先です。",
        createTemplate: { name: "archive-drive", auth_mode: "oauth2", oauth_account_id: "acct-drive", folder_id_secret_name: "google_drive_folder_id_main" },
      },
    ],
  },
  integrations: {
    titleKey: "integrations",
    description: "OAuthプロバイダ、接続済みアカウント、外部連携を管理します。",
    resources: [
      {
        title: "OAuth providers",
        path: "/integrations/oauth-providers",
        description: "ログインや接続アカウントに使うOAuthプロバイダです。",
        createTemplate: { provider_type: "google", name: "Google Workspace", enabled: true, client_id: "client-id", client_secret: "secret", redirect_uri: "https://control.example.jp/integrations/oauth-accounts/callback" },
      },
      {
        title: "OAuth accounts",
        path: "/integrations/oauth-accounts",
        description: "YouTubeやDrive操作に使う接続済みアカウントです。",
      },
    ],
  },
  logs: {
    titleKey: "logs",
    description: "配信ログと最近の操作ログを確認します。ストリーム別ログは各配信詳細のAPIと連動します。",
    resources: [
      { title: "Streams", path: "/streams", description: "ログ確認対象となる配信です。" },
      { title: "Audit logs", path: "/audit-logs", description: "管理画面で実行された操作の履歴です。" },
    ],
  },
  users: {
    titleKey: "users",
    description: "管理画面ユーザー、状態、割り当てロールを管理します。",
    resources: [
      {
        title: "Users",
        path: "/users",
        description: "運用担当者のログインアカウントです。",
        createTemplate: { username: "operator", password: "change-me-with-12-chars", role_ids: ["role-operator"] },
      },
    ],
  },
  roles: {
    titleKey: "roles",
    description: "権限セットを役割として管理し、操作ミスを防ぎます。",
    resources: [
      {
        title: "Roles",
        path: "/roles",
        description: "ユーザーに付与する権限セットです。",
        createTemplate: { name: "operator", permissions: ["streams.read", "streams.start", "streams.stop"] },
      },
      { title: "Permissions", path: "/permissions", description: "利用できる権限一覧です。" },
    ],
  },
  security: {
    titleKey: "security",
    description: "ログインポリシー、MFA、シークレット状態を確認します。",
    resources: [
      { title: "Security settings", path: "/security/settings", description: "パスワード、ロックアウト、セッション、MFAの設定です。" },
      { title: "Secret status", path: "/secrets/status", description: "配信に使う秘密情報の登録状況です。" },
    ],
  },
  "service-health": {
    titleKey: "serviceHealth",
    description: "登録済みNodeと各サービスのヘルス、ハートビート、割り当て状態を確認します。",
    resources: [{ title: "Service health", path: "/service-health", description: "Control Panelに接続しているNodeの状態です。" }],
  },
  monitoring: {
    titleKey: "monitoring",
    description: "監視系の主要指標とサービス状態を横断して確認します。",
    resources: [
      { title: "Metrics", path: "/observability/metrics", description: "CPU、メモリ、ネットワークなどの監視値です。" },
      { title: "Incidents", path: "/observability/incidents", description: "現在検知されている問題です。" },
    ],
  },
  incidents: {
    titleKey: "incidents",
    description: "配信停止や品質低下など、対応が必要なイベントを管理します。",
    resources: [{ title: "Incidents", path: "/observability/incidents", description: "検知、確認、解決の状態を追跡します。" }],
  },
  diagnostics: {
    titleKey: "diagnostics",
    description: "配信前チェックやサービス疎通確認の結果を確認します。",
    resources: [{ title: "Diagnostics", path: "/observability/diagnostics", description: "音声、エンコーダー、外部接続の診断結果です。" }],
  },
  remediation: {
    titleKey: "remediation",
    description: "承認制の復旧アクションを確認し、必要に応じて実行します。",
    resources: [{ title: "Remediation actions", path: "/observability/remediation-actions", description: "再起動、切替、再通知などの復旧候補です。" }],
  },
  notifications: {
    titleKey: "notifications",
    description: "インシデント通知の送信履歴と通知先を管理します。",
    resources: [
      { title: "Notification deliveries", path: "/observability/notification-deliveries", description: "通知送信の成功、失敗、再試行履歴です。" },
      {
        title: "Notification channels",
        path: "/observability/notification-channels",
        description: "Discordやメールなどの通知先です。",
        createTemplate: { name: "ops-discord", type: "discord", webhook_url: "https://discord.com/api/webhooks/...", enabled: true },
      },
    ],
  },
  metrics: {
    titleKey: "metrics",
    description: "配信基盤のメトリクスを時系列で確認します。",
    resources: [{ title: "Metrics", path: "/observability/metrics", description: "監視システムから取得したメトリクスです。" }],
  },
} satisfies Record<string, ResourcePageConfig>;

export type ResourcePageId = keyof typeof resourcePages;
