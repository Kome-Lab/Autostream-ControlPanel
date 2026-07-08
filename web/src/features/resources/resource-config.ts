import type { TranslationKey } from "@/lib/i18n";

export type ResourceDefinition = {
  title: string;
  path: string;
  description: string;
  form?: ResourceFormKind;
  createTemplate?: Record<string, unknown>;
  deletable?: boolean;
};

export type ResourceFormKind =
  | "encoder-profile"
  | "discord-config"
  | "youtube-output"
  | "caption-profile"
  | "overlay-profile"
  | "archive-profile"
  | "drive-destination"
  | "oauth-provider"
  | "oauth-account-connect"
  | "user"
  | "role"
  | "notification-channel"
  | "security-settings";

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
        title: "エンコーダープロファイル",
        path: "/profiles/encoder",
        description: "Worker / Encoder が配信開始時に参照する映像変換プロファイルです。",
        form: "encoder-profile",
        deletable: true,
        createTemplate: { name: "1080p60", config: { width: 1920, height: 1080, fps: 60, video_bitrate_kbps: 8000 } },
      },
    ],
  },
  discord: {
    titleKey: "discord",
    description: "Discord BOT Nodeの登録情報とBOTトークンを管理します。Guild、VC、Chat Channelは配信枠で指定します。",
    resources: [
      {
        title: "Discord BOT設定",
        path: "/discord/configs",
        description: "Discord BOT Node、BOTトークン、音声転送と再接続ポリシーの設定です。",
        form: "discord-config",
        deletable: true,
        createTemplate: { name: "main-discord-bot", audio_forward_enabled: true },
      },
    ],
  },
  youtube: {
    titleKey: "youtube",
    description: "YouTube Liveへの出力先、公開範囲、Live API利用設定を管理します。",
    resources: [
      {
        title: "YouTube出力",
        path: "/youtube/outputs",
        description: "配信開始時に使うRTMPまたはYouTube Live API出力です。",
        form: "youtube-output",
        deletable: true,
        createTemplate: { name: "public-live", mode: "live_api_dry_run", privacy_status: "public", rtmp_url: "rtmps://example.youtube.com/live2" },
      },
    ],
  },
  caption: {
    titleKey: "caption",
    description: "字幕生成や手動字幕のプロファイルを管理します。",
    resources: [
      {
        title: "字幕プロファイル",
        path: "/profiles/caption",
        description: "字幕言語、プロバイダ、遅延補正などの設定です。",
        form: "caption-profile",
        deletable: true,
        createTemplate: { name: "日本語ライブ字幕", config: { language: "ja-JP", provider: "deepgram", delay_ms: 800 } },
      },
    ],
  },
  overlay: {
    titleKey: "overlay",
    description: "映像に合成するウォーターマーク画像と表示ルールを管理します。字幕やチャットは映像生成側の設定として扱います。",
    resources: [
      {
        title: "ウォーターマーク設定",
        path: "/profiles/overlay",
        description: "配信映像へ載せるロゴ画像、位置、不透明度の設定です。",
        form: "overlay-profile",
        deletable: true,
        createTemplate: { name: "station-logo", config: { watermark_enabled: true, watermark_image_url: "", watermark_position: "bottom_right", watermark_opacity: 0.7, watermark_width_percent: 14 } },
      },
    ],
  },
  archive: {
    titleKey: "archive",
    description: "録画プロファイルと保存先をまとめて管理します。",
    resources: [
      {
        title: "録画プロファイル",
        path: "/profiles/archive",
        description: "録画形式、保存期間、アップロード有無の設定です。",
        form: "archive-profile",
        deletable: true,
        createTemplate: { name: "shared-drive", config: { format: "mp4", retention_days: 180, upload_enabled: true } },
      },
      {
        title: "Drive保存先",
        path: "/archive/destinations",
        description: "Google Driveなどのアーカイブ保存先です。",
        form: "drive-destination",
        deletable: true,
        createTemplate: { name: "archive-drive", oauth_account_id: "acct-drive", folder_id: "google-drive-folder-id" },
      },
    ],
  },
  integrations: {
    titleKey: "integrations",
    description: "OAuthプロバイダ、接続済みアカウント、外部連携を管理します。",
    resources: [
      {
        title: "OAuthログインプロバイダ",
        path: "/integrations/oauth-providers",
        description: "管理画面ログインに使うOAuthプロバイダです。スコープはログイン用途に固定します。",
        form: "oauth-provider",
        deletable: true,
        createTemplate: { provider_type: "google", name: "Google Workspace", enabled: true, client_id: "client-id", client_secret: "secret", redirect_uri: "https://control.example.jp/auth/oauth/callback" },
      },
      {
        title: "OAuth接続アカウント",
        path: "/integrations/oauth-accounts",
        description: "YouTubeやDrive操作に使う接続済みアカウントです。",
        form: "oauth-account-connect",
        deletable: true,
      },
    ],
  },
  logs: {
    titleKey: "logs",
    description: "配信ログと最近の操作ログを確認します。ストリーム別ログは各配信詳細のAPIと連動します。",
    resources: [
      { title: "配信", path: "/streams", description: "ログ確認対象となる配信です。" },
      { title: "監査ログ", path: "/audit-logs", description: "管理画面で実行された操作の履歴です。" },
    ],
  },
  users: {
    titleKey: "users",
    description: "管理画面ユーザー、状態、割り当てロールを管理します。",
    resources: [
      {
        title: "ユーザー",
        path: "/users",
        description: "運用担当者のログインアカウントです。",
        form: "user",
        deletable: true,
        createTemplate: { username: "operator", temporary_password: "change-me-with-12-chars", role_ids: ["role-operator"] },
      },
    ],
  },
  roles: {
    titleKey: "roles",
    description: "権限セットを役割として管理し、操作ミスを防ぎます。",
    resources: [
      {
        title: "ロール",
        path: "/roles",
        description: "ユーザーに付与する権限セットです。",
        form: "role",
        deletable: true,
        createTemplate: { name: "operator", permissions: ["streams.read", "streams.start", "streams.stop"] },
      },
      { title: "権限一覧", path: "/permissions", description: "利用できる権限一覧です。" },
    ],
  },
  security: {
    titleKey: "security",
    description: "ログインポリシー、MFA、シークレット状態を管理します。",
    resources: [
      { title: "セキュリティ設定", path: "/security/settings", description: "パスワード、ロックアウト、セッション、MFAの設定です。", form: "security-settings" },
      { title: "シークレット状態", path: "/secrets/status", description: "配信に使う秘密情報の登録状況です。" },
    ],
  },
  "service-health": {
    titleKey: "serviceHealth",
    description: "登録済みNodeと各サービスのヘルス、ハートビート、割り当て状態を確認します。",
    resources: [{ title: "サービス状態", path: "/service-health", description: "Control Panelに接続しているNodeの状態です。" }],
  },
  monitoring: {
    titleKey: "monitoring",
    description: "Nodeの生死、インシデント、診断結果を確認します。メトリクスの時系列グラフはメトリクスページで扱います。",
    resources: [
      { title: "インシデント", path: "/observability/incidents", description: "現在検知されている問題です。" },
      { title: "診断結果", path: "/observability/diagnostics", description: "音声、エンコーダー、外部接続の診断結果です。" },
    ],
  },
  incidents: {
    titleKey: "incidents",
    description: "配信停止や品質低下など、対応が必要なイベントを管理します。",
    resources: [{ title: "インシデント", path: "/observability/incidents", description: "検知、確認、解決の状態を追跡します。" }],
  },
  diagnostics: {
    titleKey: "diagnostics",
    description: "配信前チェックやサービス疎通確認の結果を確認します。",
    resources: [{ title: "診断結果", path: "/observability/diagnostics", description: "音声、エンコーダー、外部接続の診断結果です。" }],
  },
  remediation: {
    titleKey: "remediation",
    description: "承認制の復旧アクションを確認し、必要に応じて実行します。",
    resources: [{ title: "復旧アクション", path: "/observability/remediation-actions", description: "再起動、切替、再通知などの復旧候補です。" }],
  },
  notifications: {
    titleKey: "notifications",
    description: "インシデント通知の送信履歴と通知先を管理します。",
    resources: [
      { title: "通知履歴", path: "/observability/notification-deliveries", description: "通知送信の成功、失敗、再試行履歴です。" },
      {
        title: "通知先",
        path: "/observability/notification-channels",
        description: "Discord、Slack、メールなどの通知先です。",
        form: "notification-channel",
        deletable: true,
        createTemplate: { name: "ops-slack", type: "slack", webhook_url: "https://hooks.slack.com/services/...", enabled: true },
      },
    ],
  },
  metrics: {
    titleKey: "metrics",
    description: "配信基盤のメトリクスを時系列で確認します。",
    resources: [{ title: "メトリクス", path: "/observability/metrics", description: "監視システムから取得したメトリクスです。" }],
  },
} satisfies Record<string, ResourcePageConfig>;

export type ResourcePageId = keyof typeof resourcePages;
