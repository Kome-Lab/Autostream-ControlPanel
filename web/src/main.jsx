import React, { createContext, useContext, useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  AlertTriangle,
  Bell,
  CheckCircle2,
  ClipboardList,
  Database,
  FileText,
  Gauge,
  KeyRound,
  LayoutDashboard,
  LifeBuoy,
  ListRestart,
  Lock,
  MonitorDot,
  Play,
  Radio,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Square,
  UploadCloud,
  Users,
  Wrench,
} from 'lucide-react';
import './style.css';
import { demoAPIData, demoAPIEnabled } from './demoData.js';

const supportedLocales = ['ja', 'en'];
const localeLabels = { ja: 'JP', en: 'EN' };
const localeStorageKey = 'autostream.controlPanel.locale';

const textByLocale = {
  ja: {
    Primary: 'メイン',
    Language: '言語',
    Dashboard: 'ダッシュボード',
    Streams: '配信',
    'Encoder Profiles': 'エンコーダープロファイル',
    'Discord Settings': 'Discord 設定',
    'YouTube Outputs': 'YouTube 出力',
    'Caption/STT Settings': '字幕/STT 設定',
    'Overlay Settings': 'オーバーレイ設定',
    'Archive Settings': 'アーカイブ設定',
    Integrations: '連携',
    'Worker Management': 'Worker 管理',
    Logs: 'ログ',
    Users: 'ユーザー',
    Roles: 'ロール',
    'Audit Logs': '監査ログ',
    'Security Settings': 'セキュリティ設定',
    'API Tokens': 'API トークン',
    'Monitoring Dashboard': '監視ダッシュボード',
    Incidents: 'インシデント',
    Diagnostics: '診断',
    'Remediation Actions': '修復アクション',
    'Notification Channels': '通知チャンネル',
    Metrics: 'メトリクス',
    'Service Health': 'サービスヘルス',
    'Live operations, service health, and recent incidents.': '稼働状況、サービスヘルス、直近インシデントを確認します。',
    'Data is proxied from autostream-observability.': 'データは autostream-observability からプロキシされています。',
    'Administrative configuration and stream operations.': '管理設定と配信オペレーションを操作します。',
    'Recent Audit Logs': '直近の監査ログ',
    'Recent Audit Events': '直近の監査イベント',
    'Metric Snapshots': 'メトリクススナップショット',
    'OAuth Providers': 'OAuth プロバイダー',
    'OAuth Connected Accounts': 'OAuth 接続アカウント',
    'Google Drive Destinations': 'Google Drive 保存先',
    'Discord Bot Configs': 'Discord Bot 設定',
    'Passkey credentials': 'パスキー認証情報',
    Secrets: 'シークレット',
    Workers: 'Worker',
    'Linked OAuth Identities': '連携済み OAuth ID',
    Name: '名前',
    Status: 'ステータス',
    Created: '作成日時',
    Updated: '更新日時',
    Select: '選択',
    Edit: '編集',
    Actions: '操作',
    Action: '操作',
    Type: '種別',
    Kind: '種類',
    Config: '設定',
    Service: 'サービス',
    Stream: '配信',
    'Service Type': 'サービス種別',
    'Bot Service': 'Bot サービス',
    Guild: 'Guild',
    'Voice Channel': 'ボイスチャンネル',
    'Bot Token': 'Bot トークン',
    'Audio Forward': '音声転送',
    'Rejoin Attempts': '再参加試行',
    Mode: 'モード',
    'RTMPS URL': 'RTMPS URL',
    'Stream Key': 'ストリームキー',
    'OAuth Account': 'OAuth アカウント',
    URL: 'URL',
    Role: 'ロール',
    Capabilities: '機能',
    'Heartbeat Metrics': 'ハートビートメトリクス',
    Heartbeat: 'ハートビート',
    Username: 'ユーザー名',
    'Last Login': '最終ログイン',
    'Last IP': '最終 IP',
    Permissions: '権限',
    Timestamp: '時刻',
    Actor: '実行者',
    Resource: 'リソース',
    ID: 'ID',
    Result: '結果',
    Metadata: 'メタデータ',
    Scopes: 'スコープ',
    Revoked: '失効日時',
    Provider: 'プロバイダー',
    Subject: 'サブジェクト',
    Email: 'メール',
    Enabled: '有効',
    Secret: 'シークレット',
    'Auto Provision': '自動プロビジョニング',
    'Allowed Domains': '許可ドメイン',
    Label: 'ラベル',
    'Refresh Token': 'リフレッシュトークン',
    'Auth Mode': '認証方式',
    'Folder ID': 'フォルダー ID',
    'Shared Drive': '共有ドライブ',
    'Base Path': 'ベースパス',
    Event: 'イベント',
    Channel: 'チャンネル',
    Target: '対象',
    Incident: 'インシデント',
    Value: '値',
    Configured: '設定状態',
    Fingerprint: 'フィンガープリント',
    'Credential Hash': '認証情報ハッシュ',
    'Sign Count': '署名回数',
    Transports: 'トランスポート',
    'Last Used': '最終使用',
    Severity: '重要度',
    Rule: 'ルール',
    Summary: '概要',
    Checks: 'チェック',
    Command: 'コマンド',
    Safety: '安全性',
    'Approval required': '承認が必要',
    'Safe candidate': '安全候補',
    Suggested: '提案',
    'Service assignment': 'サービス割り当て',
    'Start preflight': '開始前チェック',
    'Encoder host preflight': 'エンコーダーホスト事前チェック',
    'Discord audio': 'Discord 音声',
    'Encoder audio bridge': 'エンコーダー音声ブリッジ',
    'Worker events': 'Worker イベント',
    'Archive / upload': 'アーカイブ / アップロード',
    'Last dispatch': '直近のディスパッチ',
    'Encoder input URL': 'エンコーダー入力 URL',
    'Stream operation overview': '配信オペレーション概要',
    'Start readiness': '開始準備',
    'Encoder Process': 'エンコーダープロセス',
    'Output FPS': '出力 FPS',
    'Output Bitrate': '出力ビットレート',
    'Dropped Frames': 'ドロップフレーム',
    'Recorder Write': 'レコーダー書き込み',
    'Archive Disk Free': 'アーカイブ空き容量',
    'Package Status': 'パッケージ状態',
    'Final MKV': '最終 MKV',
    'Final MP4': '最終 MP4',
    'Remux Duration': 'Remux 時間',
    'Google Drive Upload': 'Google Drive アップロード',
    'Upload Retries': 'アップロード再試行',
    'Upload Duration': 'アップロード時間',
    'Uploaded Files': 'アップロード済みファイル',
    'Folder Proof': 'フォルダー証跡',
    'Final MP4 Proof': '最終 MP4 証跡',
    'Metadata Proof': 'メタデータ証跡',
    'Discord Audio': 'Discord 音声',
    'Discord Packets': 'Discord パケット',
    'Input Timeout': '入力タイムアウト',
    'Audio Level': '音声レベル',
    'Audio Silence': '無音時間',
    'Audio Clipping': '音割れ',
    'Scene Updates': 'シーン更新',
    'Overlay Events': 'オーバーレイイベント',
    'Caption Events': '字幕イベント',
    'Event Send Failures': 'イベント送信失敗',
    Phase: 'フェーズ',
    Error: 'エラー',
    'Upload attempts': 'アップロード試行',
    Files: 'ファイル',
    Remux: 'Remux',
    'Dry run': 'ドライラン',
    'Upload dry run': 'アップロードドライラン',
    Forwarded: '転送済み',
    'Forward errors': '転送エラー',
    'Forward age': '転送経過',
    'Packet age': 'パケット経過',
    'Encoder / Recorder Metrics': 'エンコーダー / レコーダーメトリクス',
    'Archive / Google Drive Metrics': 'アーカイブ / Google Drive メトリクス',
    'Audio / Input Health': '音声 / 入力ヘルス',
    'Worker Event Metrics': 'Worker イベントメトリクス',
    Upload: 'アップロード',
    'Dry-run': 'ドライラン',
    'Drive Destination': 'Drive 保存先',
    'Severity Filter': '重要度フィルター',
    active: '有効',
    available: '利用可能',
    blocked: 'ブロック',
    completed: '完了',
    configured: '設定済み',
    critical: '重大',
    disabled: '無効',
    enabled: '有効',
    error: 'エラー',
    failed: '失敗',
    healthy: '正常',
    ignored: '無視',
    info: '情報',
    live: '配信中',
    missing: '未設定',
    offline: 'オフライン',
    ok: 'OK',
    online: 'オンライン',
    pending_approval: '承認待ち',
    primary: 'プライマリ',
    ready: '準備完了',
    registered: '登録済み',
    resolved: '解決済み',
    review: '要確認',
    stale: '古い',
    standby: 'スタンバイ',
    starting: '開始中',
    stopping: '停止中',
    stopped: '停止',
    suggested: '提案',
    success: '成功',
    unknown: '不明',
    unavailable: '利用不可',
    warning: '警告',
    waiting: '待機中',
    yes: 'はい',
    no: 'いいえ',
    true: 'はい',
    false: 'いいえ',
    on: 'オン',
    off: 'オフ',
    all: 'すべて',
    accepted: '受付済み',
    alive: '稼働中',
    attention: '要注意',
    'encoder missing': 'エンコーダー未割り当て',
    errors: 'エラーあり',
    exists: 'あり',
    'external input': '外部入力',
    forwarding: '転送中',
    inactive: '非アクティブ',
    loading: '読み込み中',
    neutral: '通常',
    none: 'なし',
    'no metrics': 'メトリクスなし',
    persisted: '保存済み',
    receiving: '受信中',
    retrying: '再試行中',
    'not completed': '未完了',
    'not receiving': '未受信',
    'No active stream': '稼働中の配信なし',
    Services: 'サービス',
    'Current User': '現在のユーザー',
    'Open Incidents': '未解決インシデント',
    'Pending Remediation': '保留中の修復',
    'Notification Deliveries': '通知配信',
    'Loading': '読み込み中',
    'No records': 'レコードがありません',
    'Control Panel': 'コントロールパネル',
    Password: 'パスワード',
    'MFA code': 'MFA コード',
    'Signing in...': 'サインイン中...',
    'Verify MFA': 'MFA を確認',
    'Sign in': 'サインイン',
    'Sign in with Passkey': 'パスキーでサインイン',
    'Back to password': 'パスワードに戻る',
    'OAuth login': 'OAuth ログイン',
    Logout: 'ログアウト',
    Unknown: '不明',
  },
};

const I18nContext = createContext({
  locale: 'en',
  setLocale: () => {},
  t: (value) => value,
});

function normalizeLocale(value) {
  return supportedLocales.includes(value) ? value : '';
}

function initialLocale() {
  try {
    const stored = normalizeLocale(localStorage.getItem(localeStorageKey));
    if (stored) return stored;
  } catch {
    // Keep the UI usable if storage is blocked.
  }
  return String(navigator.language || '').toLowerCase().startsWith('ja') ? 'ja' : 'en';
}

function translateText(locale, value) {
  if (typeof value !== 'string') return value;
  const dictionary = textByLocale[locale] || {};
  if (dictionary[value]) return dictionary[value];
  const configured = value.match(/^configured(\s+.+)$/);
  if (locale === 'ja' && configured) return `設定済み${configured[1]}`;
  return value;
}

function I18nProvider({ children }) {
  const [locale, setLocaleState] = useState(initialLocale);
  const setLocale = (value) => {
    const normalized = normalizeLocale(value) || 'en';
    setLocaleState(normalized);
    try {
      localStorage.setItem(localeStorageKey, normalized);
    } catch {
      // Locale still changes for the current session.
    }
  };
  useEffect(() => {
    document.documentElement.lang = locale === 'ja' ? 'ja' : 'en';
  }, [locale]);
  const value = useMemo(() => ({
    locale,
    setLocale,
    t: (text) => translateText(locale, text),
  }), [locale]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

function useI18n() {
  return useContext(I18nContext);
}

function localizeRendered(value, t) {
  return typeof value === 'string' ? t(value) : value;
}

function LanguageSwitcher() {
  const { locale, setLocale, t } = useI18n();
  return (
    <div className="language-switcher" aria-label={t('Language')}>
      {supportedLocales.map((item) => (
        <button
          className={item === locale ? 'active' : ''}
          key={item}
          type="button"
          aria-pressed={item === locale}
          onClick={() => setLocale(item)}
        >
          {localeLabels[item]}
        </button>
      ))}
    </div>
  );
}

const pages = [
  { id: 'dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { id: 'streams', label: 'Streams', icon: Radio },
  { id: 'encoder', label: 'Encoder Profiles', icon: SlidersHorizontal },
  { id: 'discord', label: 'Discord Settings', icon: Server },
  { id: 'youtube', label: 'YouTube Outputs', icon: UploadCloud },
  { id: 'caption', label: 'Caption/STT Settings', icon: FileText },
  { id: 'overlay', label: 'Overlay Settings', icon: MonitorDot },
  { id: 'archive', label: 'Archive Settings', icon: Database },
  { id: 'integrations', label: 'Integrations', icon: KeyRound },
  { id: 'workers', label: 'Worker Management', icon: Server },
  { id: 'logs', label: 'Logs', icon: ClipboardList },
  { id: 'users', label: 'Users', icon: Users },
  { id: 'roles', label: 'Roles', icon: ShieldCheck },
  { id: 'audit', label: 'Audit Logs', icon: Lock },
  { id: 'security', label: 'Security Settings', icon: KeyRound },
  { id: 'tokens', label: 'API Tokens', icon: KeyRound },
  { id: 'monitoring', label: 'Monitoring Dashboard', icon: Activity },
  { id: 'incidents', label: 'Incidents', icon: AlertTriangle },
  { id: 'diagnostics', label: 'Diagnostics', icon: LifeBuoy },
  { id: 'remediation', label: 'Remediation Actions', icon: Wrench },
  { id: 'notifications', label: 'Notification Channels', icon: Bell },
  { id: 'metrics', label: 'Metrics', icon: Gauge },
  { id: 'health', label: 'Service Health', icon: CheckCircle2 },
];

const observabilityPages = new Set(['monitoring', 'incidents', 'diagnostics', 'remediation', 'notifications', 'metrics']);

function App() {
  const { t } = useI18n();
  const [activePage, setActivePage] = useState('dashboard');
  const [auditSeed, setAuditSeed] = useState({ actionGroup: 'service_assignment', result: 'all', query: '', nonce: 0 });
  const [serviceHealthFocus, setServiceHealthFocus] = useState({ streamID: '', serviceID: '', nonce: 0 });
  const [streamFocus, setStreamFocus] = useState({ streamID: '', nonce: 0 });
  const [me] = useAPI('/auth/me', true, 'object');
  const apiEnabled = demoAPIEnabled || Boolean(me.data?.user);
  const [streams] = useAPI('/streams', apiEnabled && (activePage === 'dashboard' || activePage === 'streams' || activePage === 'workers' || activePage === 'health'));
  const [encoderProfiles] = useAPI('/profiles/encoder', apiEnabled && (activePage === 'encoder' || activePage === 'streams'));
  const [discordConfigs] = useAPI('/discord/configs', apiEnabled && (activePage === 'discord' || activePage === 'streams'));
  const [youtubeOutputs] = useAPI('/youtube/outputs', apiEnabled && (activePage === 'youtube' || activePage === 'streams'));
  const [captionProfiles] = useAPI('/profiles/caption', apiEnabled && (activePage === 'caption' || activePage === 'streams'));
  const [overlayProfiles] = useAPI('/profiles/overlay', apiEnabled && (activePage === 'overlay' || activePage === 'streams'));
  const [archiveProfiles] = useAPI('/profiles/archive', apiEnabled && (activePage === 'archive' || activePage === 'streams'));
  const [integrationProviders] = useAPI('/integrations/oauth-providers', apiEnabled && activePage === 'integrations');
  const [integrationAccounts] = useAPI('/integrations/oauth-accounts', apiEnabled && (activePage === 'integrations' || activePage === 'youtube'));
  const [driveDestinations] = useAPI('/archive/destinations', apiEnabled && (activePage === 'integrations' || activePage === 'archive'));
  const [workers] = useAPI('/workers', apiEnabled && (activePage === 'dashboard' || activePage === 'workers'));
  const [serviceHealth] = useAPI('/service-health', apiEnabled && (activePage === 'dashboard' || activePage === 'streams' || activePage === 'health'));
  const [users] = useAPI('/users', apiEnabled && activePage === 'users');
  const [roles] = useAPI('/roles', apiEnabled && (activePage === 'roles' || activePage === 'users' || activePage === 'integrations'));
  const [permissions] = useAPI('/permissions', apiEnabled && activePage === 'roles');
  const [auditLogs] = useAPI('/audit-logs', apiEnabled && (activePage === 'dashboard' || activePage === 'audit' || activePage === 'logs'));
  const [securitySettings] = useAPI('/security/settings', apiEnabled && activePage === 'security', 'object');
  const [secretStatus] = useAPI('/secrets/status', apiEnabled && activePage === 'security', 'secrets');
  const [tokens] = useAPI('/api-tokens', apiEnabled && activePage === 'tokens');
  const needsObservabilitySummary = activePage === 'dashboard' || activePage === 'monitoring' || activePage === 'streams';
  const [incidents, setIncidents] = useAPI('/observability/incidents', apiEnabled && (needsObservabilitySummary || activePage === 'incidents'));
  const [diagnostics] = useAPI('/observability/diagnostics', apiEnabled && activePage === 'diagnostics');
  const [metrics] = useAPI('/observability/metrics', apiEnabled && (needsObservabilitySummary || activePage === 'metrics'));
  const [remediation, setRemediation] = useAPI('/observability/remediation-actions', apiEnabled && (needsObservabilitySummary || activePage === 'remediation'));
  const [deliveries, setDeliveries] = useAPI('/observability/notification-deliveries', apiEnabled && (activePage === 'notifications' || activePage === 'monitoring'));
  const [notificationChannels] = useAPI('/observability/notification-channels', apiEnabled && activePage === 'notifications');

  const isLoggedIn = Boolean(me.data?.user);
  if (!demoAPIEnabled && !me.loading && !isLoggedIn) {
    return <LoginView me={me} />;
  }

  const active = pages.find((page) => page.id === activePage) || pages[0];
  const ActiveIcon = active.icon;
  const openAudit = ({ actionGroup = 'all', result = 'all', query = '' }) => {
    setAuditSeed({ actionGroup, result, query, nonce: Date.now() });
    setActivePage('audit');
  };
  const openServiceHealth = ({ streamID = '', serviceID = '' } = {}) => {
    setServiceHealthFocus({ streamID, serviceID, nonce: Date.now() });
    setActivePage('health');
  };
  const openStreamOperations = ({ streamID = '' } = {}) => {
    setStreamFocus({ streamID, nonce: Date.now() });
    setActivePage('streams');
  };

  return (
    <main>
      <aside>
        <h1>AutoStream</h1>
        <nav aria-label={t('Primary')}>
          {pages.map((page) => {
            const Icon = page.icon;
            return (
              <button className={page.id === activePage ? 'active' : ''} key={page.id} onClick={() => setActivePage(page.id)}>
                <Icon size={18} />
                <span>{t(page.label)}</span>
              </button>
            );
          })}
        </nav>
      </aside>
      <section>
        <header>
          <ActiveIcon />
          <div>
            <h2>{t(active.label)}</h2>
            <p>{subtitleFor(activePage, t)}</p>
          </div>
          <div className="header-actions">
            <LanguageSwitcher />
            <UserMenu me={me} />
          </div>
        </header>
        {activePage === 'dashboard' && <Dashboard streams={streams} services={serviceHealth} workers={workers} incidents={incidents} remediation={remediation} auditLogs={auditLogs} me={me} metrics={metrics} />}
        {activePage === 'streams' && <StreamsView streams={streams} reload={streams.reload} services={serviceHealth} metrics={metrics} incidents={incidents} remediation={remediation} reloadIncidents={setIncidents} reloadRemediation={setRemediation} profiles={{ encoderProfiles, discordConfigs, captionProfiles, overlayProfiles, archiveProfiles, youtubeOutputs }} onOpenAudit={openAudit} onOpenObservability={setActivePage} onOpenServiceHealth={openServiceHealth} initialFocus={streamFocus} />}
        {activePage === 'encoder' && <ProfileManager title="Encoder Profiles" endpoint="/profiles/encoder" data={encoderProfiles} example={profileExamples.encoder} />}
        {activePage === 'discord' && <DiscordConfigManager data={discordConfigs} />}
        {activePage === 'youtube' && <YouTubeOutputManager data={youtubeOutputs} accounts={integrationAccounts} />}
        {activePage === 'caption' && <ProfileManager title="Caption/STT Settings" endpoint="/profiles/caption" data={captionProfiles} example={profileExamples.caption} />}
        {activePage === 'overlay' && <ProfileManager title="Overlay Settings" endpoint="/profiles/overlay" data={overlayProfiles} example={profileExamples.overlay} />}
        {activePage === 'archive' && <ArchiveProfileManager data={archiveProfiles} destinations={driveDestinations} />}
        {activePage === 'integrations' && <IntegrationRegistryView providers={integrationProviders} accounts={integrationAccounts} destinations={driveDestinations} roles={roles} />}
        {activePage === 'workers' && <WorkersView workers={workers} streams={streams} />}
        {activePage === 'health' && <ServiceHealthView services={serviceHealth} streams={streams} onOpenAudit={openAudit} onOpenStreamOperations={openStreamOperations} initialFocus={serviceHealthFocus} />}
        {activePage === 'users' && <UsersView users={users} roles={roles} />}
        {activePage === 'roles' && <RolesView roles={roles} permissions={permissions} />}
        {activePage === 'audit' && <AuditLogsView data={auditLogs} initialFilter={auditSeed} />}
        {activePage === 'security' && <SecurityView settings={securitySettings} secrets={secretStatus} me={me} />}
        {activePage === 'tokens' && <ApiTokensView data={tokens} />}
        {activePage === 'logs' && <DataTable title="Recent Audit Logs" data={auditLogs} columns={auditColumns} />}
        {activePage === 'monitoring' && <Monitoring incidents={incidents} remediation={remediation} deliveries={deliveries} metrics={metrics} />}
        {activePage === 'incidents' && <Incidents data={incidents} reload={setIncidents} actionable />}
        {activePage === 'remediation' && <Remediation data={remediation} reload={setRemediation} />}
        {activePage === 'notifications' && <Notifications deliveries={deliveries} channels={notificationChannels} />}
        {activePage === 'diagnostics' && <Diagnostics data={diagnostics} />}
        {activePage === 'metrics' && <Metrics data={metrics} incidents={incidents} />}
      </section>
    </main>
  );
}

const implementedPages = new Set(['dashboard', 'streams', 'encoder', 'discord', 'youtube', 'caption', 'overlay', 'archive', 'integrations', 'workers', 'logs', 'users', 'roles', 'audit', 'security', 'tokens', 'health']);

const profileExamples = {
  encoder: {
    width: 1920,
    height: 1080,
    fps: 60,
    video_bitrate_kbps: 8000,
    audio_bitrate_kbps: 160,
    audio_sample_rate_hz: 48000,
    keyframe_interval_sec: 2,
  },
  discord: {
    guild_id: '<DISCORD_GUILD_ID>',
    voice_channel_id: '<VOICE_CHANNEL_ID>',
  },
  caption: {
    provider: 'deepgram',
    language: 'ja',
    enabled: true,
  },
  overlay: {
    theme: 'default',
    show_participants: true,
    show_current_time: true,
  },
  archive: {
    gdrive_base_path: 'AutoStream',
    upload_enabled: true,
  },
  youtube: {
    rtmp_url: 'rtmps://example.youtube.com/live2',
    stream_key_secret_name: 'youtube_stream_key',
  },
};

function useAPI(path, enabled, shape = 'array') {
  const [state, setState] = useState({ loading: false, data: initialAPIData(shape), error: '' });
  const load = useMemo(() => async () => {
    if (!enabled) return;
    if (demoAPIEnabled) {
      setState({ loading: false, data: demoAPIData(path, shape), error: '' });
      return;
    }
    setState((current) => ({ ...current, loading: true, error: '' }));
    try {
      const response = await fetch(path, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
      if (!response.ok) {
        setState({ loading: false, data: initialAPIData(shape), error: response.status === 503 ? 'Observability is not configured.' : `Request failed: ${response.status}` });
        return;
      }
      const body = await response.json();
      let data = [];
      if (shape === 'object') data = body || {};
      else if (shape === 'secrets') data = Array.isArray(body?.secrets) ? body.secrets : [];
      else data = Array.isArray(body) ? body : [];
      setState({ loading: false, data, error: '' });
    } catch {
      setState({ loading: false, data: initialAPIData(shape), error: 'Unable to reach the Control Panel API.' });
    }
  }, [enabled, path]);

  useEffect(() => {
    load();
  }, [load]);

  state.reload = load;
  return [state, load];
}

function initialAPIData(shape) {
  if (shape === 'object') return {};
  return [];
}

function passkeySupported() {
  return Boolean(window.PublicKeyCredential && navigator.credentials);
}

function base64URLToBuffer(value) {
  const base64 = String(value || '').replace(/-/g, '+').replace(/_/g, '/');
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=');
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes.buffer;
}

function bufferToBase64URL(buffer) {
  const bytes = new Uint8Array(buffer || []);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

function credentialCreateOptions(publicKey) {
  const options = { ...(publicKey || {}) };
  options.challenge = base64URLToBuffer(options.challenge);
  if (options.user?.id) options.user = { ...options.user, id: base64URLToBuffer(options.user.id) };
  if (Array.isArray(options.excludeCredentials)) {
    options.excludeCredentials = options.excludeCredentials.map((item) => ({ ...item, id: base64URLToBuffer(item.id) }));
  }
  return options;
}

function credentialRequestOptions(publicKey) {
  const options = { ...(publicKey || {}) };
  options.challenge = base64URLToBuffer(options.challenge);
  if (Array.isArray(options.allowCredentials)) {
    options.allowCredentials = options.allowCredentials.map((item) => ({ ...item, id: base64URLToBuffer(item.id) }));
  }
  return options;
}

function publicKeyCredentialToJSON(credential) {
  const response = credential.response || {};
  const json = {
    id: credential.id,
    rawId: bufferToBase64URL(credential.rawId),
    type: credential.type,
    clientExtensionResults: credential.getClientExtensionResults ? credential.getClientExtensionResults() : {},
  };
  if (credential.authenticatorAttachment) json.authenticatorAttachment = credential.authenticatorAttachment;
  if (response.attestationObject) {
    json.response = {
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      attestationObject: bufferToBase64URL(response.attestationObject),
      transports: typeof response.getTransports === 'function' ? response.getTransports() : undefined,
    };
    return json;
  }
  json.response = {
    clientDataJSON: bufferToBase64URL(response.clientDataJSON),
    authenticatorData: bufferToBase64URL(response.authenticatorData),
    signature: bufferToBase64URL(response.signature),
    userHandle: response.userHandle ? bufferToBase64URL(response.userHandle) : null,
  };
  return json;
}

function LoginView({ me }) {
  const { t } = useI18n();
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  const [mfaChallenge, setMfaChallenge] = useState('');
  const [oauthProviders, setOAuthProviders] = useState({ loading: true, data: [], error: '' });
  const [message, setMessage] = useState(me.error && !me.error.includes('401') ? me.error : '');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let active = true;
    const loadProviders = async () => {
      if (demoAPIEnabled) {
        setOAuthProviders({ loading: false, data: [], error: '' });
        return;
      }
      try {
        const response = await fetch('/auth/oauth/providers', { credentials: 'same-origin', headers: { Accept: 'application/json' } });
        if (!response.ok) {
          if (active) setOAuthProviders({ loading: false, data: [], error: '' });
          return;
        }
        const body = await response.json();
        if (active) setOAuthProviders({ loading: false, data: Array.isArray(body) ? body : [], error: '' });
      } catch {
        if (active) setOAuthProviders({ loading: false, data: [], error: '' });
      }
    };
    loadProviders();
    return () => { active = false; };
  }, []);

  const login = async (event) => {
    event.preventDefault();
    setMessage('');
    setLoading(true);
    try {
      const response = await fetch('/auth/login', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ username: username.trim(), password }),
      });
      const body = await response.json().catch(() => null);
      if (!response.ok) {
        setMessage(controlPanelErrorMessage(body, response.status, 'Login failed.'));
        return;
      }
      if (body?.mfa_required && body?.challenge_token) {
        setMfaChallenge(body.challenge_token);
        setMessage('MFA code is required.');
        return;
      }
      setCSRFToken(body?.csrf_token || '');
      await me.reload?.();
    } catch {
      setMessage('Unable to reach the Control Panel API.');
    } finally {
      setLoading(false);
    }
  };
  const verifyMFA = async (event) => {
    event.preventDefault();
    setMessage('');
    setLoading(true);
    try {
      const response = await fetch('/auth/mfa/verify', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ challenge_token: mfaChallenge, code: mfaCode.trim() }),
      });
      const body = await response.json().catch(() => null);
      if (!response.ok) {
        setMessage(controlPanelErrorMessage(body, response.status, 'MFA verification failed.'));
        return;
      }
      setCSRFToken(body?.csrf_token || '');
      setMfaChallenge('');
      setMfaCode('');
      await me.reload?.();
    } catch {
      setMessage('Unable to reach the Control Panel API.');
    } finally {
      setLoading(false);
    }
  };
  const loginWithPasskey = async () => {
    setMessage('');
    if (!passkeySupported()) {
      setMessage('This browser does not support Passkey / WebAuthn.');
      return;
    }
    setLoading(true);
    try {
      const start = await fetch('/auth/passkeys/login/start', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ username: username.trim() }),
      });
      const startBody = await start.json().catch(() => null);
      if (!start.ok || !startBody?.public_key || !startBody?.challenge_token) {
        setMessage(controlPanelErrorMessage(startBody, start.status, 'Passkey login failed to start.'));
        return;
      }
      const credential = await navigator.credentials.get({ publicKey: credentialRequestOptions(startBody.public_key) });
      const finish = await fetch('/auth/passkeys/login/finish', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ challenge_token: startBody.challenge_token, credential: publicKeyCredentialToJSON(credential) }),
      });
      const finishBody = await finish.json().catch(() => null);
      if (!finish.ok) {
        setMessage(controlPanelErrorMessage(finishBody, finish.status, 'Passkey login failed.'));
        return;
      }
      if (finishBody?.mfa_required && finishBody?.challenge_token) {
        setMfaChallenge(finishBody.challenge_token);
        setMessage('MFA code is required.');
        return;
      }
      setCSRFToken(finishBody?.csrf_token || '');
      await me.reload?.();
    } catch (error) {
      setMessage(error?.name === 'NotAllowedError' ? 'Passkey login was cancelled or timed out.' : 'Passkey login failed.');
    } finally {
      setLoading(false);
    }
  };
  const startOAuth = async (providerID) => {
    setMessage('');
    setLoading(true);
    try {
      const response = await fetch(`/auth/oauth/${providerID}/start`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ redirect_after: '/' }),
      });
      const body = await response.json().catch(() => null);
      if (!response.ok || !body?.authorization_url) {
        setMessage(controlPanelErrorMessage(body, response.status, 'OAuth login failed to start.'));
        return;
      }
      window.location.href = body.authorization_url;
    } catch {
      setMessage('Unable to start OAuth login.');
    } finally {
      setLoading(false);
    }
  };
  return (
    <main className="login-shell">
      <section className="login-panel">
        <div className="login-brand">
          <Radio size={28} />
          <div>
            <h1>AutoStream</h1>
            <span>{t('Control Panel')}</span>
          </div>
          <LanguageSwitcher />
        </div>
        <form onSubmit={mfaChallenge ? verifyMFA : login} className="login-form">
          <label>
            <span>{t('Username')}</span>
            <input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} disabled={Boolean(mfaChallenge)} />
          </label>
          {!mfaChallenge && (
            <label>
              <span>{t('Password')}</span>
              <input autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
            </label>
          )}
          {mfaChallenge && (
            <label>
              <span>{t('MFA code')}</span>
              <input autoComplete="one-time-code" inputMode="numeric" value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} />
            </label>
          )}
          {message && <Message text={message} tone="warning" />}
          <button className="command-btn" type="submit" disabled={loading || !username.trim() || (!mfaChallenge && !password) || (mfaChallenge && !mfaCode.trim())}>
            {loading ? t('Signing in...') : mfaChallenge ? t('Verify MFA') : t('Sign in')}
          </button>
          {!mfaChallenge && <button className="secondary-btn" type="button" disabled={loading} onClick={loginWithPasskey}>{t('Sign in with Passkey')}</button>}
          {mfaChallenge && <button className="secondary-btn" type="button" onClick={() => { setMfaChallenge(''); setMfaCode(''); setPassword(''); setMessage(''); }}>{t('Back to password')}</button>}
        </form>
        {!mfaChallenge && oauthProviders.data.length > 0 && (
          <div className="oauth-login">
            <span>{t('OAuth login')}</span>
            <div className="actions">
              {oauthProviders.data.map((provider) => (
                <button className="secondary-btn" type="button" key={provider.id} disabled={loading} onClick={() => startOAuth(provider.id)}>
                  Continue with {provider.name || provider.provider_type}
                </button>
              ))}
            </div>
          </div>
        )}
      </section>
    </main>
  );
}

function UserMenu({ me }) {
  const { t } = useI18n();
  const user = me.data?.user;
  const roles = Array.isArray(user?.roles) ? user.roles.join(', ') : '';
  const logout = async () => {
    await apiRequest('/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' } });
    clearCSRFToken();
    await me.reload?.();
  };
  return (
    <div className="user-menu">
      <span>{user?.username || t('Unknown')}{roles ? ` / ${roles}` : ''}</span>
      <button className="secondary-btn" type="button" onClick={logout}>{t('Logout')}</button>
    </div>
  );
}

const streamColumns = [
  { key: 'name', label: 'Name' },
  { key: 'status', label: 'Status', render: (value) => <Badge tone={statusTone(value)}>{value}</Badge> },
  { key: 'created_at', label: 'Created' },
  { key: 'updated_at', label: 'Updated' },
];

function StreamsView({ streams, reload, services, metrics, incidents, remediation, reloadIncidents, reloadRemediation, profiles, onOpenAudit, onOpenObservability, onOpenServiceHealth, initialFocus }) {
  const [selectedID, setSelectedID] = useState('');
  const [newStreamName, setNewStreamName] = useState('');
  const [form, setForm] = useState({
    discord_config_id: '',
    discord_guild_id: '',
    discord_voice_channel_id: '',
    discord_text_channel_id: '',
    encoder_profile_id: '',
    caption_profile_id: '',
    overlay_profile_id: '',
    archive_profile_id: '',
    youtube_output_id: '',
    encoder_input_url: '',
    encoder_rtmp_url: '',
  });
  const [message, setMessage] = useState('');
  const [lastDispatch, setLastDispatch] = useState(null);
  const [workerEventMessage, setWorkerEventMessage] = useState('');
  const [testCaption, setTestCaption] = useState('Control Panel test caption');
  const [readinessIssues, setReadinessIssues] = useState([]);
  const selectedStream = streams.data.find((stream) => stream.id === selectedID) || streams.data[0];
  const selectedDiscordConfig = profiles.discordConfigs.data.find((item) => item.id === form.discord_config_id);
  const assignment = streamAssignmentStatus(selectedStream?.id, services.data);
  const streamLabel = (id) => {
    if (!id) return '-';
    const stream = streams.data.find((item) => item.id === id);
    return stream ? `${stream.name} (${stream.status})` : id;
  };
  const [encoderPreflight] = useAPI(selectedStream ? `/streams/${selectedStream.id}/encoder-preflight` : '', Boolean(selectedStream), 'object');
  const [audioStatus] = useAPI(selectedStream ? `/streams/${selectedStream.id}/audio-status` : '', Boolean(selectedStream), 'object');
  const [workerEvents] = useAPI(selectedStream ? `/streams/${selectedStream.id}/worker-events` : '', Boolean(selectedStream), 'object');
  const [externalE2EConfig] = useAPI(selectedStream ? `/streams/${selectedStream.id}/external-e2e-config` : '', Boolean(selectedStream), 'object');
  const streamMetrics = useMemo(() => metricsForStream(metrics, selectedStream?.id), [metrics, selectedStream?.id]);
  const setField = (field, value) => setForm((current) => ({ ...current, [field]: value }));
  useEffect(() => {
    if (!selectedStream) return;
    setForm((current) => ({
      ...current,
      discord_config_id: selectedStream.discord_config_id || '',
      discord_guild_id: selectedStream.discord_guild_id || '',
      discord_voice_channel_id: selectedStream.discord_voice_channel_id || '',
      discord_text_channel_id: selectedStream.discord_text_channel_id || '',
      encoder_profile_id: selectedStream.encoder_profile_id || '',
      caption_profile_id: selectedStream.caption_profile_id || '',
      overlay_profile_id: selectedStream.overlay_profile_id || '',
      archive_profile_id: selectedStream.archive_profile_id || '',
      youtube_output_id: selectedStream.youtube_output_id || '',
      encoder_input_url: selectedStream.encoder_input_url || '',
    }));
  }, [selectedStream?.id, selectedStream?.updated_at]);
  useEffect(() => {
    if (!initialFocus?.nonce || !initialFocus.streamID) return;
    setSelectedID(initialFocus.streamID);
    setReadinessIssues([]);
    setMessage('');
  }, [initialFocus?.nonce, initialFocus?.streamID]);
  const run = async (verb) => {
    if (!selectedStream) return;
    setMessage('');
    setLastDispatch(null);
    setReadinessIssues([]);
    const body = verb === 'start' ? startBody(form) : undefined;
    const endpoint = {
      retry: 'retry-upload',
      youtubeComplete: 'youtube/complete',
    }[verb] || verb;
    try {
      const response = await fetch(`/streams/${selectedStream.id}/${endpoint}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
        body: body ? JSON.stringify(body) : undefined,
      });
      if (!response.ok) {
        let detail = null;
        try {
          detail = await response.json();
        } catch {
          detail = null;
        }
        if (detail?.code === 'missing_stream_assignments' && Array.isArray(detail.missing_service_types)) {
          setReadinessIssues(readinessResponseIssues(detail));
          setMessage(controlPanelErrorMessage(detail, response.status, 'Missing stream assignments.'));
          return;
        }
        if (detail?.code === 'stream_start_not_ready' && Array.isArray(detail.issues)) {
          setReadinessIssues(detail.issues);
          setMessage(controlPanelErrorMessage(detail, response.status, 'Start readiness failed. Resolve the checks below before retrying.'));
          return;
        }
        if (detail?.dispatch) {
          setLastDispatch(dispatchSummary(verb, response.status, detail.dispatch));
        }
        setMessage(controlPanelErrorMessage(detail, response.status));
        return;
      }
      let detail = null;
      try {
        detail = await response.json();
      } catch {
        detail = null;
      }
      if (detail?.dispatch) {
        setLastDispatch(dispatchSummary(verb, response.status, detail.dispatch));
      }
      setMessage(verb === 'youtubeComplete' ? 'YouTube complete retry accepted.' : `${verb} accepted`);
      if (reload) await reload();
    } catch {
      setMessage('Unable to reach the Control Panel API.');
    }
  };
  const createStream = async () => {
    const name = newStreamName.trim();
    if (!name) {
      setMessage('Stream name is required before creating a new stream.');
      return;
    }
    setMessage('');
    setLastDispatch(null);
    setReadinessIssues([]);
    const result = await apiRequest('/streams', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ name, ...streamSettingsBody(form) }),
    });
    if (!result.ok) {
      setMessage(result.message);
      return;
    }
    const createdID = result.body?.id || '';
    setMessage('Stream created with the current Control Panel managed settings.');
    setNewStreamName('');
    if (createdID) setSelectedID(createdID);
    if (reload) await reload();
  };
  const checkReadiness = async () => {
    if (!selectedStream) return;
    setMessage('');
    setReadinessIssues([]);
    const result = await apiRequest(`/streams/${selectedStream.id}/start-readiness`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(startBody(form)),
    });
    if (!result.ok) {
      setMessage(result.message);
      setReadinessIssues(readinessResponseIssues(result.body));
      return;
    }
    const issues = readinessResponseIssues(result.body);
    setReadinessIssues(issues);
    setMessage(result.body?.ready ? 'Start readiness checks passed.' : 'Start readiness checks failed: resolve readiness issues before start.');
  };
  const saveSettings = async () => {
    if (!selectedStream) return;
    setMessage('');
    const result = await apiRequest(`/streams/${selectedStream.id}/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(streamSettingsBody(form)),
    });
    if (!result.ok) {
      setMessage(result.message);
      return;
    }
    setMessage('Stream settings saved.');
    if (reload) await reload();
  };
  const assignService = async (serviceID, assignmentRole = 'primary') => {
    if (!selectedStream || !serviceID) return;
    setMessage('');
    setReadinessIssues([]);
    const result = await apiRequest(`/services/${serviceID}/assign`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ stream_id: selectedStream.id, assignment_role: assignmentRole }),
    });
    if (!result.ok) {
      setMessage(result.message);
      return;
    }
    setMessage(`Service assigned as ${assignmentRole}. Run Check Readiness before Start.`);
    await services.reload?.();
    if (reload) await reload();
  };
  const columns = [
    ...streamColumns,
    {
      key: 'id',
      label: 'Select',
      render: (_, row) => (
        <button className="icon-btn" onClick={() => setSelectedID(row.id)} title="Select stream">
          <Radio size={16} />
        </button>
      ),
    },
  ];
  const sendWorkerEvent = async (eventType) => {
    if (!selectedStream) return;
    setWorkerEventMessage('');
    const payload = eventType === 'caption'
      ? { event_type: 'caption', text: testCaption, speaker_user_id: 'control-panel-test' }
      : { event_type: 'current_time' };
    const result = await apiRequest(`/streams/${selectedStream.id}/worker-events/test`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!result.ok) {
      setWorkerEventMessage(result.message);
      return;
    }
    setWorkerEventMessage(`${eventType} event sent to Worker.`);
    setTimeout(() => workerEvents.reload?.(), 300);
  };
  return (
    <div className="stack">
      <div className="panel">
        <h3>Stream Operations</h3>
        <StreamOperationsOverview
          stream={selectedStream}
          assignment={assignment}
          encoderInputURL={form.encoder_input_url}
          encoderPreflight={encoderPreflight}
          audioStatus={audioStatus}
          workerEvents={workerEvents}
          metrics={streamMetrics}
          lastDispatch={lastDispatch}
          readinessIssues={readinessIssues}
        />
        <StreamObservabilityPanel
          stream={selectedStream}
          incidents={incidents}
          remediation={remediation}
          reloadIncidents={reloadIncidents}
          reloadRemediation={reloadRemediation}
          onOpenObservability={onOpenObservability}
        />
        <div className="form-grid">
          <label>
            <span>New Stream Name</span>
            <input value={newStreamName} onChange={(event) => setNewStreamName(event.target.value)} placeholder="Morning Stream" />
          </label>
          <div className="form-action-field">
            <span>Create</span>
            <button className="secondary-btn" type="button" onClick={createStream}>Create Stream With Current Settings</button>
          </div>
          <label>
            <span>Stream</span>
            <select value={selectedStream?.id || ''} onChange={(event) => setSelectedID(event.target.value)}>
              {streams.data.map((stream) => <option key={stream.id} value={stream.id}>{stream.name} ({stream.status})</option>)}
            </select>
          </label>
          <ProfileSelect label="Discord Config" value={form.discord_config_id} items={profiles.discordConfigs.data} onChange={(value) => setField('discord_config_id', value)} />
          <label>
            <span>Discord Guild ID Override</span>
            <input value={form.discord_guild_id} onChange={(event) => setField('discord_guild_id', event.target.value)} placeholder="blank = config default" />
          </label>
          <label>
            <span>Discord Voice Channel ID Override</span>
            <input value={form.discord_voice_channel_id} onChange={(event) => setField('discord_voice_channel_id', event.target.value)} placeholder="blank = config default" />
          </label>
          <label>
            <span>Discord Text Channel ID Override</span>
            <input value={form.discord_text_channel_id} onChange={(event) => setField('discord_text_channel_id', event.target.value)} placeholder="optional / blank = config default" />
          </label>
          <ProfileSelect label="Encoder Profile" value={form.encoder_profile_id} items={profiles.encoderProfiles.data} onChange={(value) => setField('encoder_profile_id', value)} />
          <ProfileSelect label="Caption Profile" value={form.caption_profile_id} items={profiles.captionProfiles.data} onChange={(value) => setField('caption_profile_id', value)} />
          <ProfileSelect label="Overlay Profile" value={form.overlay_profile_id} items={profiles.overlayProfiles.data} onChange={(value) => setField('overlay_profile_id', value)} />
          <ProfileSelect label="Archive Profile" value={form.archive_profile_id} items={profiles.archiveProfiles.data} onChange={(value) => setField('archive_profile_id', value)} />
          <ProfileSelect label="YouTube Output" value={form.youtube_output_id} items={profiles.youtubeOutputs.data} onChange={(value) => setField('youtube_output_id', value)} />
          <label>
            <span>Encoder Input URL</span>
            <input value={form.encoder_input_url} onChange={(event) => setField('encoder_input_url', event.target.value)} placeholder="blank = Discord audio + generated video" />
          </label>
          <label>
            <span>RTMP URL</span>
            <input value={form.encoder_rtmp_url} onChange={(event) => setField('encoder_rtmp_url', event.target.value)} placeholder="rtmps://example.com/live2" />
          </label>
        </div>
        <StreamAssignmentPlanner
          stream={selectedStream}
          assignment={assignment}
          services={services.data.filter((service) => service.service_type !== 'observability')}
          streamLabel={streamLabel}
          onAssign={assignService}
        />
        <SelectedDiscordConfig config={selectedDiscordConfig} assignment={assignment} overrides={form} />
        <div className="actions">
          <button className="secondary-btn" disabled={!selectedStream} onClick={saveSettings}>Save Settings</button>
          <button className="secondary-btn" disabled={!selectedStream} onClick={checkReadiness}>Check Readiness</button>
          <button className="command-btn" disabled={!selectedStream} onClick={() => run('start')}><Play size={16} />Start</button>
          <button className="command-btn" disabled={!selectedStream} onClick={() => run('stop')}><Square size={16} />Stop</button>
          <button className="command-btn" disabled={!selectedStream} onClick={() => run('retry')}><UploadCloud size={16} />Retry Upload</button>
          <button className="secondary-btn" disabled={!selectedStream} onClick={() => run('youtubeComplete')}>Retry YouTube Complete</button>
          <button className="secondary-btn" disabled={!selectedStream} onClick={() => onOpenAudit?.({ actionGroup: 'stream_lifecycle', query: selectedStream.id })}>View Stream Audit</button>
        </div>
        <AssignmentReadiness assignment={assignment} loading={services.loading} serverIssueCount={readinessIssues.length} />
        <StartPreflight assignment={assignment} encoderInputURL={form.encoder_input_url} serverIssues={readinessIssues} />
        <ExternalE2EConfigExport stream={selectedStream} config={externalE2EConfig} />
        <EncoderPreflightStatus status={encoderPreflight} assignment={assignment} />
        <DiscordBotAudioStatus assignment={assignment} />
        <WorkerEventStatus assignment={assignment} />
        <WorkerEventTools
          assignment={assignment}
          caption={testCaption}
          message={workerEventMessage}
          onCaptionChange={setTestCaption}
          onSend={sendWorkerEvent}
          streamSelected={Boolean(selectedStream)}
        />
        <WorkerEventSidecar status={workerEvents} assignment={assignment} />
        <AudioBridgeStatus status={audioStatus} assignment={assignment} />
        <ReadinessIssues
          issues={readinessIssues}
          stream={selectedStream}
          onOpenAudit={onOpenAudit}
          onOpenPage={onOpenObservability}
          onOpenMetrics={() => onOpenObservability?.('metrics')}
          onOpenServiceHealth={onOpenServiceHealth}
        />
        <DispatchResults summary={lastDispatch} />
        {message && <Message text={message} tone={messageTone(message)} />}
      </div>
      <DataTable title="Streams" data={streams} columns={columns} />
    </div>
  );
}

function ExternalE2EConfigExport({ stream, config }) {
  const [copyMessage, setCopyMessage] = useState('');
  if (!stream) {
    return (
      <div className="external-e2e-export neutral">
        <div className="runtime-preview-heading">
          <div>
            <strong>External verification config export</strong>
            <span>Select a stream to inspect its Control Panel confirmation export.</span>
          </div>
        </div>
      </div>
    );
  }
  if (config.loading) {
    return <Message text="Loading external verification config export..." />;
  }
  if (config.error) {
    return <Message text={`External verification config export unavailable: ${config.error}`} tone="warning" />;
  }
  const data = config.data || {};
  const confirmations = data.confirmations && typeof data.confirmations === 'object' ? data.confirmations : {};
  const runtimeConfig = data.runtime_config && typeof data.runtime_config === 'object' ? data.runtime_config : {};
  const serviceAssignments = data.service_assignments && typeof data.service_assignments === 'object' ? data.service_assignments : {};
  const readiness = data.readiness && typeof data.readiness === 'object' ? data.readiness : {};
  const confirmationKeys = ['youtube_output_saved', 'drive_destination_saved', 'discord_config_saved', 'primary_assignments_saved', 'runtime_config_distribution_enabled'];
  const missingConfirmations = Array.isArray(readiness.missing_confirmations) ? readiness.missing_confirmations : confirmationKeys.filter((key) => confirmations[key] !== true);
  const missingRuntimeIDs = Array.isArray(readiness.missing_runtime_ids) ? readiness.missing_runtime_ids : ['youtube_output_id', 'drive_destination_id', 'discord_config_id', 'encoder_profile_id', 'archive_profile_id'].filter((key) => !String(runtimeConfig[key] || '').trim());
  const missingServiceIDs = Array.isArray(readiness.missing_primary_services) ? readiness.missing_primary_services : ['discord_bot_service_id', 'encoder_recorder_primary_service_id', 'worker_primary_service_id'].filter((key) => !String(serviceAssignments[key] || '').trim());
  const missingCapabilities = Array.isArray(readiness.missing_runtime_config_capabilities) ? readiness.missing_runtime_config_capabilities : [];
  const ready = readiness.ready === true || (missingConfirmations.length === 0 && missingRuntimeIDs.length === 0 && missingServiceIDs.length === 0 && missingCapabilities.length === 0);
  const remediationItems = externalE2ERemediationItems({
    missingConfirmations,
    missingRuntimeIDs,
    missingServiceIDs,
    missingCapabilities,
  });
  const tone = ready ? 'ok' : 'warning';
  const payload = safeExternalE2EConfigPayload(data, stream.id);
  const copy = async () => {
    setCopyMessage('');
    const text = safeJSON(payload);
    try {
      await navigator.clipboard.writeText(text);
      setCopyMessage('External E2E config JSON copied.');
    } catch {
      setCopyMessage('Clipboard is unavailable. Select the JSON preview and copy it manually.');
    }
  };
  return (
    <div className={`external-e2e-export ${tone}`}>
      <div className="runtime-preview-heading">
        <div>
          <strong>External E2E config export</strong>
          <span>Secret-safe confirmation JSON for {stream.name || stream.id}. Raw provider values, stream keys, tokens, and session cookies are not returned.</span>
        </div>
        <div className="inline-actions">
          <button className="secondary-btn" type="button" onClick={() => config.reload?.()}>Refresh Export</button>
          <button className="secondary-btn" type="button" onClick={copy}><ClipboardList size={16} />Copy JSON</button>
        </div>
      </div>
      <div className="runtime-preview-grid">
        <article>
          <span>Confirmations</span>
          <strong>{confirmationKeys.length - missingConfirmations.length}/{confirmationKeys.length}</strong>
          <small>{missingConfirmations.length ? `missing ${missingConfirmations.join(', ')}` : 'ready'}</small>
        </article>
        <article>
          <span>Runtime IDs</span>
          <strong>{5 - missingRuntimeIDs.length}/5</strong>
          <small>{missingRuntimeIDs.length ? `missing ${missingRuntimeIDs.join(', ')}` : 'ready'}</small>
        </article>
        <article>
          <span>Primary services</span>
          <strong>{3 - missingServiceIDs.length}/3</strong>
          <small>{missingServiceIDs.length ? `missing ${missingServiceIDs.join(', ')}` : 'ready'}</small>
        </article>
        <article>
          <span>Runtime config capability</span>
          <strong>{missingCapabilities.length ? 'blocked' : 'ready'}</strong>
          <small>{missingCapabilities.length ? `missing ${missingCapabilities.join(', ')}` : 'all primary services expose runtime_config'}</small>
        </article>
      </div>
      {!ready && remediationItems.length > 0 && (
        <div className="external-e2e-remediation">
          <strong>Control Panel setup still required</strong>
          <ul>
            {remediationItems.map((item) => (
              <li key={`${item.group}-${item.key}`}>
                <code>{item.key}</code>
                <span>{item.message}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      <pre>{safeJSON(payload)}</pre>
      {copyMessage && <small>{copyMessage}</small>}
    </div>
  );
}

function externalE2ERemediationItems({ missingConfirmations = [], missingRuntimeIDs = [], missingServiceIDs = [], missingCapabilities = [] }) {
  const items = [];
  for (const key of missingConfirmations) {
    items.push({
      group: 'confirmation',
      key,
      message: externalE2EConfirmationHint(key),
    });
  }
  for (const key of missingRuntimeIDs) {
    items.push({
      group: 'runtime-id',
      key,
      message: externalE2ERuntimeIDHint(key),
    });
  }
  for (const key of missingServiceIDs) {
    items.push({
      group: 'primary-service',
      key,
      message: externalE2EPrimaryServiceHint(key),
    });
  }
  for (const key of missingCapabilities) {
    items.push({
      group: 'runtime-config-capability',
      key,
      message: `Re-register or restart the ${key} primary service with runtime_config capability enabled, then refresh service health.`,
    });
  }
  return items;
}

function externalE2EConfirmationHint(key) {
  switch (key) {
    case 'youtube_output_saved':
      return 'Open YouTube Outputs, save a stream_key or Live API output, and select it on this stream.';
    case 'drive_destination_saved':
      return 'Open Integrations, save a Drive destination with a write-only folder ID, then link it from the archive profile.';
    case 'discord_config_saved':
      return 'Open Discord Settings, save a bot config, and assign it to this stream.';
    case 'primary_assignments_saved':
      return 'Open Service Health and assign primary Discord Bot, Encoder/Recorder, and Worker services to this stream.';
    case 'runtime_config_distribution_enabled':
      return 'Refresh service registrations until each primary service reports runtime_config capability.';
    default:
      return 'Resolve this Control Panel confirmation before exporting pass evidence.';
  }
}

function externalE2ERuntimeIDHint(key) {
  switch (key) {
    case 'youtube_output_id':
      return 'Select the saved YouTube output on this stream.';
    case 'drive_destination_id':
      return 'Select an archive profile that references a saved Drive destination.';
    case 'discord_config_id':
      return 'Select the saved Discord config on this stream.';
    case 'encoder_profile_id':
      return 'Select the Encoder profile that will receive the real input and output relay settings.';
    case 'archive_profile_id':
      return 'Select the archive profile that performs final.mkv to final.mp4 and Drive upload.';
    default:
      return 'Fill this Control Panel runtime ID with a saved internal record, not a raw provider value.';
  }
}

function externalE2EPrimaryServiceHint(key) {
  switch (key) {
    case 'discord_bot':
      return 'Assign the Discord Bot instance that owns the selected Discord config as primary.';
    case 'encoder_recorder':
      return 'Assign the Encoder/Recorder instance that can capture audio, write archives, and upload Drive artifacts as primary.';
    case 'worker':
      return 'Assign the Worker instance that publishes the production event stream as primary.';
    default:
      return 'Assign the required service as primary for this stream.';
  }
}

function safeExternalE2EConfigPayload(data, streamID) {
  const runtimeConfig = data?.runtime_config && typeof data.runtime_config === 'object' ? data.runtime_config : {};
  const serviceAssignments = data?.service_assignments && typeof data.service_assignments === 'object' ? data.service_assignments : {};
  const confirmations = data?.confirmations && typeof data.confirmations === 'object' ? data.confirmations : {};
  const readiness = data?.readiness && typeof data.readiness === 'object' ? data.readiness : {};
  return {
    schema_version: data?.schema_version || 1,
    stream_id: data?.stream_id || streamID || '',
    runtime_config: {
      youtube_output_id: runtimeConfig.youtube_output_id || '',
      drive_destination_id: runtimeConfig.drive_destination_id || '',
      discord_config_id: runtimeConfig.discord_config_id || '',
      encoder_profile_id: runtimeConfig.encoder_profile_id || '',
      archive_profile_id: runtimeConfig.archive_profile_id || '',
    },
    service_assignments: {
      discord_bot_service_id: serviceAssignments.discord_bot_service_id || '',
      encoder_recorder_primary_service_id: serviceAssignments.encoder_recorder_primary_service_id || '',
      worker_primary_service_id: serviceAssignments.worker_primary_service_id || '',
      encoder_recorder_standby_service_id: serviceAssignments.encoder_recorder_standby_service_id || '',
      worker_standby_service_id: serviceAssignments.worker_standby_service_id || '',
    },
    confirmations: {
      youtube_output_saved: confirmations.youtube_output_saved === true,
      drive_destination_saved: confirmations.drive_destination_saved === true,
      discord_config_saved: confirmations.discord_config_saved === true,
      primary_assignments_saved: confirmations.primary_assignments_saved === true,
      runtime_config_distribution_enabled: confirmations.runtime_config_distribution_enabled === true,
    },
    readiness: {
      ready: readiness.ready === true,
      missing_confirmations: Array.isArray(readiness.missing_confirmations) ? readiness.missing_confirmations : [],
      missing_runtime_ids: Array.isArray(readiness.missing_runtime_ids) ? readiness.missing_runtime_ids : [],
      missing_primary_services: Array.isArray(readiness.missing_primary_services) ? readiness.missing_primary_services : [],
      missing_runtime_config_capabilities: Array.isArray(readiness.missing_runtime_config_capabilities) ? readiness.missing_runtime_config_capabilities : [],
    },
  };
}

function StreamOperationsOverview({ stream, assignment, encoderInputURL, encoderPreflight, audioStatus, workerEvents, metrics, lastDispatch, readinessIssues }) {
  const { t } = useI18n();
  const rows = [
    streamOperationRow(stream),
    assignmentOperationRow(assignment),
    preflightOperationRow(assignment, encoderInputURL, readinessIssues),
    encoderPreflightOperationRow(encoderPreflight, assignment),
    discordBotOperationRow(assignment),
    audioBridgeOperationRow(audioStatus, assignment),
    workerEventOperationRow(workerEvents, assignment),
    archiveOperationRow(metrics),
    dispatchOperationRow(lastDispatch),
  ];
  return (
    <div className="stream-ops-overview" aria-label={t('Stream operation overview')}>
      {rows.map((row) => (
        <div className={`stream-ops-row ${row.tone}`} key={row.id}>
          <div>
            <strong>{t(row.label)}</strong>
            <span>{row.detail}</span>
          </div>
          <Badge tone={row.tone === 'critical' ? 'critical' : row.tone === 'warning' ? 'warning' : row.tone === 'ok' ? 'ok' : 'neutral'}>{row.status}</Badge>
        </div>
      ))}
    </div>
  );
}

function StreamObservabilityPanel({ stream, incidents, remediation, reloadIncidents, reloadRemediation, onOpenObservability }) {
  if (!stream) return null;
  if (incidents?.loading || remediation?.loading) return <Message text="Loading stream incidents and remediation actions..." />;
  if (incidents?.error) return <Message text={incidents.error} tone="warning" />;
  if (remediation?.error) return <Message text={remediation.error} tone="warning" />;
  const scopedIncidents = incidentsForStream(incidents?.data, stream.id);
  const activeIncidents = scopedIncidents.filter((incident) => !incidentClosed(incident));
  const incidentIDs = new Set(scopedIncidents.map((incident) => incident.id).filter(Boolean));
  const scopedActions = remediationActionsForIncidents(remediation?.data, incidentIDs);
  const pendingActions = scopedActions.filter((action) => action.status !== 'executed' && action.status !== 'blocked');
  const headlineTone = activeIncidents.some((incident) => incident.severity === 'critical' || incident.severity === 'error')
    ? 'critical'
    : activeIncidents.length > 0 || pendingActions.length > 0
      ? 'warning'
      : 'ok';
  return (
    <div className={`stream-observability-panel ${headlineTone}`}>
      <div className="stream-observability-heading">
        <div>
          <strong>Stream incident / remediation</strong>
          <span>{stream.name || stream.id}: {activeIncidents.length} active incident(s), {pendingActions.length} pending remediation action(s)</span>
        </div>
        <div className="actions">
          <button className="secondary-btn" type="button" onClick={() => onOpenObservability?.('incidents')}>Open Incidents</button>
          <button className="secondary-btn" type="button" onClick={() => onOpenObservability?.('remediation')}>Open Remediation</button>
        </div>
      </div>
      {activeIncidents.length === 0 && pendingActions.length === 0 ? (
        <span className="muted">No active incident or pending remediation is linked to this stream.</span>
      ) : (
        <div className="stream-observability-grid">
          <div>
            <h4>Active incidents</h4>
            {activeIncidents.length === 0 ? (
              <span className="muted">No active incident.</span>
            ) : activeIncidents.slice(0, 4).map((incident) => (
              <article className="stream-observability-item" key={incident.id}>
                <div>
                  <strong>{incident.rule}</strong>
                  <Badge tone={severityTone(incident.severity)}>{incident.severity}</Badge>
                </div>
                <span>{incident.summary_ja || 'No summary.'}</span>
                <IncidentDiagnosticPreview incident={incident} />
                <small>{incident.service_id || '-'} / updated {formatDateTime(incident.updated_at)}</small>
                <IncidentActions incident={incident} reload={reloadIncidents} />
              </article>
            ))}
          </div>
          <div>
            <h4>Remediation actions</h4>
            {scopedActions.length === 0 ? (
              <span className="muted">No remediation action for linked incidents.</span>
            ) : scopedActions.slice(0, 4).map((action) => (
              <article className="stream-observability-item" key={action.id}>
                <div>
                  <strong>{action.action}</strong>
                  <Badge tone={action.status === 'blocked' ? 'critical' : action.status === 'pending_approval' ? 'warning' : 'ok'}>{action.status}</Badge>
                </div>
                <span>{remediationActionHelp[action.action] || 'Review diagnostic evidence before executing.'}</span>
                <small>{action.requires_approval ? 'Approval required' : action.safe_auto ? 'Safe candidate' : 'Suggested'} / {action.mode}</small>
                <RemediationResult action={action} />
                <ActionButtons action={action} reload={reloadRemediation} />
              </article>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function IncidentDiagnosticPreview({ incident }) {
  const report = incident?.diagnostic_report || incident?.report || {};
  const actions = Array.isArray(report.recommended_actions) ? report.recommended_actions : [];
  if (!report.likely_cause && !report.impact && actions.length === 0) return null;
  return (
    <div className="diagnostic-preview">
      {report.likely_cause && (
        <div>
          <strong>Likely cause</strong>
          <span>{report.likely_cause}</span>
        </div>
      )}
      {report.impact && (
        <div>
          <strong>Impact</strong>
          <span>{report.impact}</span>
        </div>
      )}
      {actions.length > 0 && (
        <div>
          <strong>Next checks</strong>
          <span>{actions.slice(0, 2).join(' / ')}</span>
        </div>
      )}
    </div>
  );
}

function incidentsForStream(rows, streamID) {
  if (!streamID) return [];
  return (Array.isArray(rows) ? rows : []).filter((incident) => incident?.stream_id === streamID);
}

function remediationActionsForIncidents(rows, incidentIDs) {
  if (!incidentIDs || incidentIDs.size === 0) return [];
  return (Array.isArray(rows) ? rows : []).filter((action) => incidentIDs.has(action?.incident_id));
}

function incidentClosed(incident) {
  return incident?.status === 'resolved' || incident?.status === 'ignored';
}

function streamOperationRow(stream) {
  if (!stream) {
    return { id: 'stream', label: 'Stream', status: 'none', tone: 'neutral', detail: 'Select or create a stream job.' };
  }
  return {
    id: 'stream',
    label: 'Stream',
    status: stream.status || 'unknown',
    tone: statusTone(stream.status),
    detail: `${stream.name || stream.id} / updated ${formatDateTime(stream.updated_at)}`,
  };
}

function assignmentOperationRow(assignment) {
  const stale = assignment.assigned.filter((service) => serviceHealthState(service).stale);
  if (assignment.missing.length > 0) {
    return {
      id: 'assignment',
      label: 'Service assignment',
      status: 'missing',
      tone: 'critical',
      detail: `Missing: ${assignment.missing.join(', ')}`,
    };
  }
  if (stale.length > 0) {
    return {
      id: 'assignment',
      label: 'Service assignment',
      status: 'attention',
      tone: 'warning',
      detail: `${stale.length} assigned service heartbeat needs attention.`,
    };
  }
  return {
    id: 'assignment',
    label: 'Service assignment',
    status: 'ready',
    tone: 'ok',
    detail: 'Discord Bot, Worker, and Encoder/Recorder are assigned.',
  };
}

function preflightOperationRow(assignment, encoderInputURL, readinessIssues) {
  if (Array.isArray(readinessIssues) && readinessIssues.length > 0) {
    return {
      id: 'preflight',
      label: 'Start preflight',
      status: 'blocked',
      tone: 'critical',
      detail: `${readinessIssues.length} server-side readiness issue(s) returned by Control Panel.`,
    };
  }
  const checks = startPreflightChecks(assignment, encoderInputURL);
  const critical = checks.filter((check) => check.tone === 'critical').length;
  const warning = checks.filter((check) => check.tone === 'warning').length;
  if (critical > 0) {
    return { id: 'preflight', label: 'Start preflight', status: 'blocked', tone: 'critical', detail: `${critical} blocking check(s) before start dispatch.` };
  }
  if (warning > 0) {
    return { id: 'preflight', label: 'Start preflight', status: 'review', tone: 'warning', detail: `${warning} warning check(s); start may still fail in the service.` };
  }
  return { id: 'preflight', label: 'Start preflight', status: 'ready', tone: 'ok', detail: 'Control Panel checks are ready for start dispatch.' };
}

function encoderPreflightOperationRow(status, assignment) {
  const encoderAssigned = assignment.assigned.some((service) => service.service_type === 'encoder_recorder');
  if (!encoderAssigned) return { id: 'encoder-preflight', label: 'Encoder host preflight', status: 'missing', tone: 'critical', detail: 'Encoder/Recorder is not assigned.' };
  if (status.loading) return { id: 'encoder-preflight', label: 'Encoder host preflight', status: 'loading', tone: 'neutral', detail: 'Loading Encoder/Recorder preflight checks.' };
  if (status.error) return { id: 'encoder-preflight', label: 'Encoder host preflight', status: 'unavailable', tone: 'warning', detail: status.error };
  const data = status.data || {};
  const checks = Array.isArray(data.checks) ? data.checks : [];
  const failed = checks.filter((check) => preflightCheckFailed(check));
  const critical = failed.filter((check) => check.severity === 'critical').length;
  const warning = failed.filter((check) => check.severity !== 'critical').length;
  if (critical > 0) return { id: 'encoder-preflight', label: 'Encoder host preflight', status: 'blocked', tone: 'critical', detail: `${critical} critical Encoder/Recorder check(s) failed.` };
  if (warning > 0 || data.ready === false) return { id: 'encoder-preflight', label: 'Encoder host preflight', status: 'review', tone: 'warning', detail: `${warning || failed.length || 1} Encoder/Recorder warning check(s) need review.` };
  if (data.ready === true) return { id: 'encoder-preflight', label: 'Encoder host preflight', status: 'ready', tone: 'ok', detail: 'FFmpeg, archive path, RTMPS, and uploader prerequisites are ready.' };
  return { id: 'encoder-preflight', label: 'Encoder host preflight', status: 'unknown', tone: 'neutral', detail: 'Preflight has not returned readiness data yet.' };
}

function discordBotOperationRow(assignment) {
  const bot = assignment.assigned.find((service) => service.service_type === 'discord_bot');
  if (!bot) return { id: 'discord-audio', label: 'Discord audio', status: 'missing', tone: 'critical', detail: 'Discord Bot is not assigned.' };
  const health = serviceHealthState(bot);
  const metrics = bot.metrics || {};
  const receiving = Number(metrics['discord.audio_receiving'] || 0);
  const forwardActive = Number(metrics['discord.audio_forward_active'] || 0);
  const forwarded = Number(metrics['discord.audio_forwarded_total'] || 0);
  const errors = Number(metrics['discord.audio_forward_errors_total'] || 0);
  if (health.stale) return { id: 'discord-audio', label: 'Discord audio', status: 'stale', tone: 'warning', detail: `Discord Bot heartbeat is ${health.label}.` };
  if (errors > 0) return { id: 'discord-audio', label: 'Discord audio', status: 'errors', tone: 'warning', detail: `${formatNumber(errors, 0)} forward error(s) reported.` };
  if (receiving < 1 || forwardActive < 1) return { id: 'discord-audio', label: 'Discord audio', status: 'waiting', tone: 'warning', detail: 'VC audio receiving or forward is not active yet.' };
  return { id: 'discord-audio', label: 'Discord audio', status: 'forwarding', tone: 'ok', detail: `${formatNumber(forwarded, 0)} packet(s) forwarded.` };
}

function audioBridgeOperationRow(status, assignment) {
  const encoderAssigned = assignment.assigned.some((service) => service.service_type === 'encoder_recorder');
  if (!encoderAssigned) return { id: 'audio-bridge', label: 'Encoder audio bridge', status: 'missing', tone: 'critical', detail: 'Encoder/Recorder is not assigned.' };
  if (status.loading) return { id: 'audio-bridge', label: 'Encoder audio bridge', status: 'loading', tone: 'neutral', detail: 'Loading bridge status from Encoder/Recorder.' };
  if (status.error) return { id: 'audio-bridge', label: 'Encoder audio bridge', status: 'unavailable', tone: 'warning', detail: status.error };
  const bridge = status.data?.audio_bridge_status || {};
  const packets = Number(bridge.packets_total || 0);
  const age = Number(bridge.last_packet_age_sec || 0);
  if (!bridge.bridge_active) return { id: 'audio-bridge', label: 'Encoder audio bridge', status: 'inactive', tone: 'neutral', detail: 'Bridge is not active for the selected stream.' };
  if (packets <= 0) return { id: 'audio-bridge', label: 'Encoder audio bridge', status: 'waiting', tone: 'warning', detail: 'Bridge is active, but no Discord packet has arrived.' };
  if (age >= 5) return { id: 'audio-bridge', label: 'Encoder audio bridge', status: 'stale', tone: 'warning', detail: `Last packet age is ${formatDurationSeconds(age)}.` };
  return { id: 'audio-bridge', label: 'Encoder audio bridge', status: 'receiving', tone: 'ok', detail: `${formatNumber(packets, 0)} packet(s) received by Encoder/Recorder.` };
}

function workerEventOperationRow(status, assignment) {
  const workerAssigned = assignment.assigned.some((service) => service.service_type === 'worker');
  const encoderAssigned = assignment.assigned.some((service) => service.service_type === 'encoder_recorder');
  if (!workerAssigned) return { id: 'worker-events', label: 'Worker events', status: 'missing', tone: 'critical', detail: 'Worker is not assigned.' };
  if (!encoderAssigned) return { id: 'worker-events', label: 'Worker events', status: 'encoder missing', tone: 'critical', detail: 'Encoder/Recorder must be assigned to inspect sidecar persistence.' };
  if (status.loading) return { id: 'worker-events', label: 'Worker events', status: 'loading', tone: 'neutral', detail: 'Loading persisted Worker event sidecar.' };
  if (status.error) return { id: 'worker-events', label: 'Worker events', status: 'unavailable', tone: 'warning', detail: status.error };
  const events = Array.isArray(status.data?.events) ? status.data.events : [];
  if (events.length <= 0) return { id: 'worker-events', label: 'Worker events', status: 'waiting', tone: 'neutral', detail: 'No persisted Worker event has been observed yet.' };
  return { id: 'worker-events', label: 'Worker events', status: 'persisted', tone: 'ok', detail: `${events.length} event(s) persisted in archive sidecar.` };
}

function archiveOperationRow(metrics) {
  if (!metrics) return { id: 'archive', label: 'Archive / upload', status: 'unknown', tone: 'neutral', detail: 'Metrics are not loaded yet.' };
  if (metrics.loading) return { id: 'archive', label: 'Archive / upload', status: 'loading', tone: 'neutral', detail: 'Loading archive and upload metrics.' };
  if (metrics.error) return { id: 'archive', label: 'Archive / upload', status: 'unavailable', tone: 'warning', detail: metrics.error };
  const latest = latestMetrics(metrics.data);
  const packageStatus = metricValue(latest, 'archive.package_status');
  const uploadStatus = metricValue(latest, 'gdrive.upload_status');
  const retries = metricValue(latest, 'gdrive.upload_retry_count');
  const finalMP4 = metricValue(latest, 'archive.final_mp4_exists');
  if (packageStatus === null && uploadStatus === null && retries === null && finalMP4 === null) {
    return { id: 'archive', label: 'Archive / upload', status: 'no metrics', tone: 'neutral', detail: 'No archive metrics have been reported for this stream yet.' };
  }
  if (packageStatus === 0 || uploadStatus === 0) return { id: 'archive', label: 'Archive / upload', status: 'failed', tone: 'critical', detail: `package=${formatOptionalState(packageStatus)} / upload=${formatOptionalState(uploadStatus)}` };
  if ((retries || 0) >= 3) return { id: 'archive', label: 'Archive / upload', status: 'retrying', tone: 'warning', detail: `${formatNumber(retries, 0)} upload retries reported.` };
  return {
    id: 'archive',
    label: 'Archive / upload',
    status: finalMP4 >= 1 || uploadStatus >= 1 ? 'ok' : 'waiting',
    tone: finalMP4 >= 1 || uploadStatus >= 1 ? 'ok' : 'neutral',
    detail: `final.mp4=${finalMP4 === null ? 'unknown' : formatExists(finalMP4)} / upload=${uploadStatus >= 1 ? 'ok' : 'not completed'}`,
  };
}

function dispatchOperationRow(summary) {
  if (!summary) return { id: 'dispatch', label: 'Last dispatch', status: 'none', tone: 'neutral', detail: 'No start / stop / retry dispatch has run in this page session.' };
  if (summary.failedCount > 0) return { id: 'dispatch', label: 'Last dispatch', status: 'failed', tone: 'warning', detail: `${summary.verb}: ${summary.successCount} succeeded, ${summary.failedCount} failed.` };
  return { id: 'dispatch', label: 'Last dispatch', status: 'accepted', tone: 'ok', detail: `${summary.verb}: ${summary.successCount} service request(s) succeeded.` };
}

function metricsForStream(metrics, streamID) {
  const base = metrics || { loading: false, error: '', data: [] };
  const rows = Array.isArray(base.data) ? base.data : [];
  if (!streamID) return { ...base, data: rows };
  const scoped = rows.filter((row) => row?.stream_id === streamID);
  return { ...base, data: scoped.length > 0 ? scoped : rows };
}

function metricValue(latest, name) {
  const value = latest.get(name)?.value;
  return typeof value === 'number' ? value : null;
}

function formatOptionalState(value) {
  return value === null ? 'unknown' : formatState(value);
}

function DiscordBotAudioStatus({ assignment }) {
  const bot = assignment.assigned.find((service) => service.service_type === 'discord_bot');
  if (!bot) {
    return (
      <div className="audio-bridge-status warning">
        <strong>Discord Bot audio forward</strong>
        <span>Assign a Discord Bot to inspect VC audio and forward status.</span>
      </div>
    );
  }
  const health = serviceHealthState(bot);
  const metrics = bot.metrics || {};
  const voiceConnected = Number(metrics['discord.voice_connected'] || 0);
  const receiving = Number(metrics['discord.audio_receiving'] || 0);
  const forwardEnabled = Number(metrics['discord.audio_forward_enabled'] || 0);
  const forwardActive = Number(metrics['discord.audio_forward_active'] || 0);
  const forwarded = Number(metrics['discord.audio_forwarded_total'] || 0);
  const forwardErrors = Number(metrics['discord.audio_forward_errors_total'] || 0);
  const lastPacketAge = Number(metrics['discord.audio_last_packet_age_sec'] || 0);
  const lastForwardAge = Number(metrics['discord.audio_last_forward_age_sec'] || 0);
  const hasMetrics = Object.keys(metrics).length > 0;
  const tone = health.stale || !hasMetrics || forwardErrors > 0 || voiceConnected < 1 || receiving < 1 || forwardEnabled < 1 || forwardActive < 1 ? 'warning' : 'ok';
  const headline = !hasMetrics
    ? 'Waiting for heartbeat metrics from Discord Bot.'
    : health.stale
      ? `Heartbeat is ${health.label}.`
      : forwardErrors > 0
        ? 'Forward errors have been reported.'
        : voiceConnected < 1
          ? 'Bot is not connected to Discord voice.'
          : receiving < 1
            ? 'Bot is not receiving Discord audio packets.'
            : forwardActive < 1
              ? 'Audio forwarding to Encoder/Recorder is not active.'
              : 'Discord Bot is receiving and forwarding audio.';
  return (
    <div className={`audio-bridge-status ${tone}`}>
      <div>
        <strong>Discord Bot audio forward</strong>
        <span>{headline}</span>
      </div>
      <div className="audio-bridge-grid">
        <MetricChip label="Voice" value={voiceConnected >= 1 ? 'connected' : 'not connected'} />
        <MetricChip label="Receiving" value={receiving >= 1 ? 'yes' : 'no'} />
        <MetricChip label="Forward active" value={forwardActive >= 1 ? 'yes' : 'no'} />
        <MetricChip label="Forwarded" value={formatNumber(forwarded, 0)} />
        <MetricChip label="Forward errors" value={formatNumber(forwardErrors, 0)} />
        <MetricChip label="Last audio age" value={lastPacketAge > 0 ? formatDurationSeconds(lastPacketAge) : '-'} />
        <MetricChip label="Last forward age" value={lastForwardAge > 0 ? formatDurationSeconds(lastForwardAge) : '-'} />
      </div>
    </div>
  );
}

function EncoderPreflightStatus({ status, assignment }) {
  const encoder = assignment.assigned.find((service) => service.service_type === 'encoder_recorder');
  if (!encoder) {
    return (
      <div className="audio-bridge-status warning">
        <strong>Encoder/Recorder preflight</strong>
        <span>Assign an Encoder/Recorder to inspect FFmpeg, archive, RTMPS, and Google Drive readiness.</span>
      </div>
    );
  }
  if (status.loading) {
    return <div className="audio-bridge-status"><strong>Encoder/Recorder preflight</strong><span>Loading Encoder/Recorder host checks...</span></div>;
  }
  if (status.error) {
    return (
      <div className="audio-bridge-status warning">
        <div>
          <strong>Encoder/Recorder preflight</strong>
          <span>{status.error}</span>
        </div>
        <button className="secondary-btn" type="button" onClick={() => status.reload?.()}>Refresh Encoder Preflight</button>
      </div>
    );
  }
  const data = status.data || {};
  const checks = Array.isArray(data.checks) ? data.checks : [];
  const failed = checks.filter((check) => preflightCheckFailed(check));
  const tone = failed.some((check) => check.severity === 'critical') ? 'critical' : failed.length > 0 || data.ready === false ? 'warning' : data.ready === true ? 'ok' : 'neutral';
  const summary = data.ready === true
    ? 'Encoder/Recorder host prerequisites are ready.'
    : failed.length > 0
      ? `${failed.length} preflight check(s) need attention before live start.`
      : 'Preflight data is available, but readiness is not confirmed.';
  return (
    <div className={`audio-bridge-status ${tone}`}>
      <div>
        <strong>Encoder/Recorder preflight</strong>
        <span>{summary}</span>
        {data.checked_at && <small className="muted">Checked at {formatDateTime(data.checked_at)}</small>}
      </div>
      <div className="audio-bridge-grid">
        {checks.length === 0 ? (
          <MetricChip label="Checks" value="none reported" />
        ) : checks.map((check) => (
          <div className="metric-chip" key={check.id || check.message}>
            <span>{check.id || 'check'}</span>
            <strong>{check.status || '-'}</strong>
            <small>{check.message || '-'}</small>
          </div>
        ))}
        {data.summary?.ffmpeg_bin && <MetricChip label="FFmpeg" value={String(data.summary.ffmpeg_bin)} />}
        {data.summary?.archive_root && <MetricChip label="Archive root" value={String(data.summary.archive_root)} />}
      </div>
      <div className="actions">
        <button className="secondary-btn" type="button" onClick={() => status.reload?.()}>Refresh Encoder Preflight</button>
      </div>
    </div>
  );
}

function preflightCheckFailed(check) {
  const value = String(check?.status || '').toLowerCase();
  return value !== '' && value !== 'ok' && value !== 'ready' && value !== 'configured';
}

function WorkerEventStatus({ assignment }) {
  const worker = assignment.assigned.find((service) => service.service_type === 'worker');
  if (!worker) {
    return (
      <div className="audio-bridge-status warning">
        <strong>Worker event path</strong>
        <span>Assign a Worker to inspect overlay and caption event status.</span>
      </div>
    );
  }
  const health = serviceHealthState(worker);
  const metrics = worker.metrics || {};
  const overlayEvents = Number(metrics['worker.overlay_events_total'] || 0);
  const captionEvents = Number(metrics['worker.caption_events_total'] || 0);
  const sceneUpdates = Number(metrics['worker.scene_updates_total'] || 0);
  const sendFailures = Number(metrics['worker.event_send_failures_total'] || 0);
  const hasMetrics = Object.keys(metrics).length > 0;
  const tone = health.stale || !hasMetrics || sendFailures > 0 ? 'warning' : 'ok';
  const headline = !hasMetrics
    ? 'Waiting for heartbeat metrics from Worker.'
    : health.stale
      ? `Heartbeat is ${health.label}.`
      : sendFailures > 0
        ? 'Worker event delivery failures have been reported.'
        : sceneUpdates > 0
          ? 'Worker is generating and sending stream events.'
          : 'Worker is assigned; no stream event has been generated yet.';
  return (
    <div className={`audio-bridge-status ${tone}`}>
      <div>
        <strong>Worker event path</strong>
        <span>{headline}</span>
      </div>
      <div className="audio-bridge-grid">
        <MetricChip label="Overlay" value={formatNumber(overlayEvents, 0)} />
        <MetricChip label="Captions" value={formatNumber(captionEvents, 0)} />
        <MetricChip label="Scene updates" value={formatNumber(sceneUpdates, 0)} />
        <MetricChip label="Send failures" value={formatNumber(sendFailures, 0)} />
      </div>
    </div>
  );
}

function WorkerEventTools({ assignment, caption, message, onCaptionChange, onSend, streamSelected }) {
  const workerAssigned = assignment.assigned.some((service) => service.service_type === 'worker');
  const disabled = !streamSelected || !workerAssigned;
  return (
    <div className={`audio-bridge-status ${workerAssigned ? 'neutral' : 'warning'}`}>
      <div>
        <strong>Worker event test</strong>
        <span>{workerAssigned ? 'Send a lightweight test event through the assigned Worker.' : 'Assign a Worker before sending test events.'}</span>
      </div>
      <div className="event-test-row">
        <label>
          <span>Caption text</span>
          <input value={caption} onChange={(event) => onCaptionChange(event.target.value)} placeholder="Test caption" />
        </label>
        <button className="secondary-btn" disabled={disabled} onClick={() => onSend('current_time')} type="button">Send current time</button>
        <button className="secondary-btn" disabled={disabled || !caption.trim()} onClick={() => onSend('caption')} type="button">Send caption</button>
      </div>
      {message && <Message text={message} tone={message.includes('failed') || message.includes('Request') || message.includes('Unable') ? 'warning' : 'ok'} />}
    </div>
  );
}

function WorkerEventSidecar({ status, assignment }) {
  const encoderAssigned = assignment.assigned.some((service) => service.service_type === 'encoder_recorder');
  if (!encoderAssigned) {
    return (
      <div className="audio-bridge-status warning">
        <strong>Worker event sidecar</strong>
        <span>Assign an Encoder/Recorder to inspect persisted worker events.</span>
      </div>
    );
  }
  if (status.loading) return <Message text="Loading Worker event sidecar..." />;
  if (status.error) {
    return (
      <div className="audio-bridge-status warning">
        <strong>Worker event sidecar</strong>
        <span>{status.error}</span>
      </div>
    );
  }
  const events = Array.isArray(status.data?.events) ? status.data.events : [];
  const recent = events.slice(-5).reverse();
  const tone = events.length > 0 ? 'ok' : 'neutral';
  return (
    <div className={`audio-bridge-status ${tone}`}>
      <div>
        <strong>Worker event sidecar</strong>
        <span>{events.length > 0 ? `${events.length} events persisted by Encoder/Recorder.` : 'No persisted worker event has been reported yet.'}</span>
      </div>
      {recent.length > 0 && (
        <div className="event-list">
          {recent.map((event, index) => (
            <article key={event.id || `${event.type || event.event_type || 'event'}-${event.timestamp || event.created_at || index}`}>
              <div>
                <strong>{event.type || event.event_type || 'event'}</strong>
                <span>{event.timestamp || event.created_at ? formatDateTime(event.timestamp || event.created_at) : '-'}</span>
              </div>
              <code>{eventPreview(event)}</code>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}

function eventPreview(event) {
  const payload = event?.payload || {};
  if (typeof payload.text === 'string' && payload.text) return payload.text;
  if (Array.isArray(payload.participants)) return `${payload.participants.length} participants`;
  if (typeof payload.display_name === 'string' && payload.display_name) return payload.display_name;
  return safeJSON(payload);
}

function AudioBridgeStatus({ status, assignment }) {
  const encoderAssigned = assignment.assigned.some((service) => service.service_type === 'encoder_recorder');
  if (!encoderAssigned) {
    return (
      <div className="audio-bridge-status warning">
        <strong>Discord audio bridge</strong>
        <span>Assign an Encoder/Recorder to inspect audio ingest status.</span>
      </div>
    );
  }
  if (status.loading) {
    return <Message text="Loading Discord audio bridge status..." />;
  }
  if (status.error) {
    return (
      <div className="audio-bridge-status warning">
        <strong>Discord audio bridge</strong>
        <span>{status.error}</span>
      </div>
    );
  }
  const bridge = status.data?.audio_bridge_status || {};
  const packets = Number(bridge.packets_total || 0);
  const forwarded = Number(bridge.rtp_forwarded || 0);
  const age = Number(bridge.last_packet_age_sec || 0);
  const tone = !bridge.bridge_active ? 'neutral' : packets <= 0 ? 'warning' : age >= 5 ? 'warning' : 'ok';
  const headline = !bridge.bridge_active
    ? 'Bridge is not active.'
    : packets <= 0
      ? 'Bridge is active, but no Discord packet has arrived yet.'
      : age >= 5
        ? 'Discord packet age is stale.'
        : 'Discord audio packets are reaching Encoder/Recorder.';
  return (
    <div className={`audio-bridge-status ${tone}`}>
      <div>
        <strong>Discord audio bridge</strong>
        <span>{headline}</span>
      </div>
      <div className="audio-bridge-grid">
        <MetricChip label="Bridge" value={bridge.bridge_active ? 'active' : 'inactive'} />
        <MetricChip label="Packets" value={formatNumber(packets, 0)} />
        <MetricChip label="RTP forwarded" value={formatNumber(forwarded, 0)} />
        <MetricChip label="Last packet age" value={formatDurationSeconds(age)} />
      </div>
      {bridge.last_packet_at && <small>Last packet: {formatDateTime(bridge.last_packet_at)}</small>}
    </div>
  );
}

function MetricChip({ label, value }) {
  return (
    <span className="metric-chip">
      <small>{label}</small>
      <strong>{value}</strong>
    </span>
  );
}

const requiredStreamServiceTypes = ['discord_bot', 'worker', 'encoder_recorder'];
const heartbeatStaleAfterSec = 90;

function streamAssignmentStatus(streamID, services = []) {
  if (!streamID) return { assigned: [], standby: [], allAssigned: [], missing: requiredStreamServiceTypes, ready: false };
  const allAssigned = services.filter((service) => (
    requiredStreamServiceTypes.includes(service.service_type)
    && service.current_stream_id === streamID
  ));
  const assigned = requiredStreamServiceTypes
    .map((type) => allAssigned.find((service) => service.service_type === type && (!service.assignment_role || service.assignment_role === 'primary')))
    .filter(Boolean);
  const standby = allAssigned.filter((service) => service.assignment_role === 'standby');
  const assignedTypes = new Set(assigned.map((service) => service.service_type));
  const missing = requiredStreamServiceTypes.filter((type) => !assignedTypes.has(type));
  return { assigned, standby, allAssigned, missing, ready: missing.length === 0 };
}

function AssignmentReadiness({ assignment, loading, serverIssueCount = 0 }) {
  if (loading) return <Message text="Loading service assignments..." />;
  const serverBlocked = serverIssueCount > 0;
  return (
    <div className={`assignment-readiness ${assignment.ready ? 'ok' : 'warning'}`}>
      <div>
        <strong>Service assignment</strong>
        <span>{assignment.ready ? 'Required service types are assigned. Run Check Readiness before Start.' : 'Assign the missing services before starting the stream.'}</span>
        {assignment.ready && serverBlocked && (
          <small className="readiness-note critical">Assignment is complete, but Start readiness still has {serverIssueCount} server-side issue(s).</small>
        )}
      </div>
      <div className="assignment-pills">
        {requiredStreamServiceTypes.map((type) => {
          const service = assignment.assigned.find((item) => item.service_type === type);
          return (
                <span className={`assignment-pill ${service ? 'ok' : 'missing'}`} key={type}>
                  {type}: {service ? `${service.service_name || service.service_id} (${serviceHealthState(service).label})` : 'missing'}
                </span>
          );
        })}
      </div>
    </div>
  );
}

function StartPreflight({ assignment, encoderInputURL, serverIssues = [] }) {
  const { t } = useI18n();
  const serverIssueCount = Array.isArray(serverIssues) ? serverIssues.length : 0;
  if (serverIssueCount > 0) {
    return (
      <div className="start-preflight critical">
        <div className="preflight-heading">
          <strong>{t('Start readiness')}</strong>
          <span>{serverIssueCount} server-side readiness issue(s) returned by Control Panel. See the issue panel below before pressing Start.</span>
        </div>
      </div>
    );
  }
  const checks = startPreflightChecks(assignment, encoderInputURL);
  const blocking = checks.filter((check) => check.tone === 'critical').length;
  const warnings = checks.filter((check) => check.tone === 'warning').length;
  const tone = blocking > 0 ? 'critical' : warnings > 0 ? 'warning' : 'ok';
  const headline = blocking > 0
    ? `${blocking} blocking start checks.`
    : warnings > 0
      ? `${warnings} start checks need attention.`
      : 'Start preflight checks look ready.';
  return (
    <div className={`start-preflight ${tone}`}>
      <div className="preflight-heading">
        <strong>{t('Start readiness')}</strong>
        <span>{headline}</span>
      </div>
      <div className="preflight-grid">
        {checks.map((check) => (
          <article className={check.tone} key={check.id}>
            <div>
              <strong>{t(check.label)}</strong>
              <Badge tone={check.tone === 'critical' ? 'critical' : check.tone === 'warning' ? 'warning' : 'ok'}>{check.status}</Badge>
            </div>
            <span>{check.detail}</span>
          </article>
        ))}
      </div>
    </div>
  );
}

function startPreflightChecks(assignment, encoderInputURL) {
  const checks = [];
  const discord = assignment.assigned.find((service) => service.service_type === 'discord_bot');
  const worker = assignment.assigned.find((service) => service.service_type === 'worker');
  const encoder = assignment.assigned.find((service) => service.service_type === 'encoder_recorder');
  for (const type of requiredStreamServiceTypes) {
    const service = assignment.assigned.find((item) => item.service_type === type);
    if (!service) {
      checks.push({ id: `assignment-${type}`, label: `${type} assignment`, status: 'missing', tone: 'critical', detail: 'Assign this service before starting.' });
      continue;
    }
    const health = serviceHealthState(service);
    checks.push({
      id: `assignment-${type}`,
      label: `${type} assignment`,
      status: health.stale ? 'attention' : 'ready',
      tone: health.stale ? 'warning' : 'ok',
      detail: `${service.service_name || service.service_id} / ${health.label}`,
    });
  }
  checks.push(serviceURLCheck('encoder-public-url', 'Encoder public URL', encoder, 'Discord Bot and Worker receive this URL during start dispatch.'));
  checks.push(serviceURLCheck('discord-public-url', 'Discord Bot public URL', discord, 'Control Panel dispatches job start/stop to this URL.'));
  checks.push(serviceURLCheck('worker-public-url', 'Worker public URL', worker, 'Control Panel dispatches job start/stop and test events to this URL.'));
  if (!encoderInputURL?.trim()) {
    checks.push(capabilityCheck('discord-audio-capture', 'Discord audio capture', discord, 'audio_capture', 'Discord audio bridge mode needs the bot to capture VC audio.'));
    checks.push(capabilityCheck('discord-audio-forward', 'Discord audio forward', discord, 'audio_stream_forward', 'Discord Bot must forward Opus packets to Encoder/Recorder.'));
  } else {
    checks.push({ id: 'external-input-url', label: 'Encoder input URL', status: 'external input', tone: 'ok', detail: 'External media input is configured; Discord audio bridge is not required for FFmpeg input.' });
  }
  return checks;
}

function serviceURLCheck(id, label, service, detail) {
  if (!service) return { id, label, status: 'missing', tone: 'critical', detail: 'Service is not assigned.' };
  const parsed = parseAbsoluteHTTPURL(service.public_url);
  if (!parsed.ok) return { id, label, status: 'invalid', tone: 'critical', detail: parsed.message };
  return { id, label, status: 'valid', tone: 'ok', detail: `${service.public_url} - ${detail}` };
}

function capabilityCheck(id, label, service, capability, detail) {
  if (!service) return { id, label, status: 'missing', tone: 'critical', detail: 'Discord Bot is not assigned.' };
  const value = capabilityBool(service.capabilities, capability);
  if (value === false) return { id, label, status: 'disabled', tone: 'critical', detail };
  if (value === null) return { id, label, status: 'unknown', tone: 'warning', detail: `${capability} is not reported. ${detail}` };
  return { id, label, status: 'enabled', tone: 'ok', detail };
}

function parseAbsoluteHTTPURL(value) {
  if (!value) return { ok: false, message: 'public_url is missing.' };
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return { ok: false, message: 'public_url must use http or https.' };
    }
    return { ok: true, message: '' };
  } catch {
    return { ok: false, message: 'public_url must be an absolute URL.' };
  }
}

function capabilityBool(capabilities, name) {
  if (!capabilities || capabilities[name] === undefined || capabilities[name] === null) return null;
  const value = capabilities[name];
  if (typeof value === 'boolean') return value;
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase();
    if (['true', '1', 'yes'].includes(normalized)) return true;
    if (['false', '0', 'no'].includes(normalized)) return false;
  }
  return null;
}

function ProfileSelect({ label, value, items, onChange }) {
  return (
    <label>
      <span>{label}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        <option value="">None</option>
        {items.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select>
    </label>
  );
}

function SelectedDiscordConfig({ config, assignment, overrides = {} }) {
  if (!config) {
    if (overrides.discord_guild_id && overrides.discord_voice_channel_id) {
      return <Message text="Discord Config is required before stream-specific guild/channel overrides can be used." tone="critical" />;
    }
    return <Message text="No Discord Config selected. Select a Control Panel managed Discord Config before starting the stream." tone="warning" />;
  }
  const primaryBot = assignment.assigned.find((service) => service.service_type === 'discord_bot' && service.assignment_role === 'primary');
  const botMatches = !config.service_id || !primaryBot || config.service_id === primaryBot.service_id;
  const tone = botMatches ? 'ok' : 'critical';
  const guildID = overrides.discord_guild_id || config.guild_id || '-';
  const voiceID = overrides.discord_voice_channel_id || config.voice_channel_id || '-';
  const textID = overrides.discord_text_channel_id || config.text_channel_id || '-';
  const detail = [
    `Guild: ${guildID}`,
    `Voice: ${voiceID}`,
    `Text: ${textID}`,
    `Bot service: ${config.service_id || 'any assigned primary Discord Bot'}`,
  ].join(' / ');
  return (
    <div className={`assignment-planner ${tone}`}>
      <div className="assignment-planner-heading">
        <div>
          <strong>Stream Discord routing</strong>
          <span>{detail}</span>
          <span>Blank stream fields use the selected Discord Config defaults; non-empty fields are stream-specific overrides.</span>
        </div>
        <Badge tone={tone}>{botMatches ? 'ready' : 'bot mismatch'}</Badge>
      </div>
      {!botMatches && (
        <span>Assign {config.service_id} as the primary Discord Bot, or choose a config for {primaryBot?.service_id || 'the current primary bot'}.</span>
      )}
    </div>
  );
}

function startBody(form) {
  return {
    discord_config_id: form.discord_config_id,
    discord_guild_id: form.discord_guild_id,
    discord_voice_channel_id: form.discord_voice_channel_id,
    discord_text_channel_id: form.discord_text_channel_id,
    encoder_input_url: form.encoder_input_url,
    encoder_rtmp_url: form.encoder_rtmp_url,
    encoder_profile_id: form.encoder_profile_id,
    caption_profile_id: form.caption_profile_id,
    overlay_profile_id: form.overlay_profile_id,
    archive_profile_id: form.archive_profile_id,
    youtube_output_id: form.youtube_output_id,
  };
}

function streamSettingsBody(form) {
  return {
    discord_config_id: form.discord_config_id,
    discord_guild_id: form.discord_guild_id,
    discord_voice_channel_id: form.discord_voice_channel_id,
    discord_text_channel_id: form.discord_text_channel_id,
    encoder_profile_id: form.encoder_profile_id,
    caption_profile_id: form.caption_profile_id,
    overlay_profile_id: form.overlay_profile_id,
    archive_profile_id: form.archive_profile_id,
    youtube_output_id: form.youtube_output_id,
    encoder_input_url: form.encoder_input_url,
  };
}

function ReadinessIssues({ issues = [], stream, onOpenAudit, onOpenMetrics, onOpenServiceHealth, onOpenPage }) {
  if (!Array.isArray(issues) || issues.length === 0) return null;
  const actions = readinessIssueActions(issues);
  return (
    <div className="readiness-issues">
      <div className="readiness-heading">
        <div>
          <strong>Operation readiness checks</strong>
          <span>{issues.length} issue(s) must be resolved before start / stop / retry dispatch.</span>
        </div>
        <div className="actions">
          {actions.serviceHealth && (
            <button className="secondary-btn" type="button" onClick={() => onOpenServiceHealth?.({ streamID: stream?.id || '', serviceID: actions.serviceID })}>
              Open Service Health
            </button>
          )}
          {actions.discord && <button className="secondary-btn" type="button" onClick={() => onOpenPage?.('discord')}>Open Discord Settings</button>}
          {actions.youtube && <button className="secondary-btn" type="button" onClick={() => onOpenPage?.('youtube')}>Open YouTube Outputs</button>}
          {actions.archive && <button className="secondary-btn" type="button" onClick={() => onOpenPage?.('archive')}>Open Archive Settings</button>}
          {actions.integrations && <button className="secondary-btn" type="button" onClick={() => onOpenPage?.('integrations')}>Open Integrations</button>}
          {actions.metrics && <button className="secondary-btn" type="button" onClick={onOpenMetrics}>Open Metrics</button>}
          {stream?.id && <button className="secondary-btn" type="button" onClick={() => onOpenAudit?.({ actionGroup: 'stream_lifecycle', query: stream.id })}>View Stream Audit</button>}
        </div>
      </div>
      <ul>
        {issues.map((issue, index) => {
          const target = issue.service_name || issue.service_id || issue.service_type || 'control_panel';
          return (
            <li key={`${issue.code || 'issue'}-${index}`}>
              <div className="readiness-issue-title">
                <span>{target}</span>
                <code>{issue.code || 'readiness_issue'}</code>
              </div>
              <p>{issue.message || 'This check must pass before service dispatch.'}</p>
              <small>{readinessIssueHint(issue)}</small>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function readinessIssueActions(issues = []) {
  const codes = new Set(issues.map((issue) => issue.code));
  const serviceIssue = issues.find((issue) => issue.service_id || issue.service_type);
  const youtubeCodes = ['youtube_output_not_found', 'youtube_output_invalid_config', 'youtube_stream_key_unavailable', 'youtube_live_api_unavailable', 'youtube_oauth_account_unavailable'];
  const archiveCodes = ['archive_profile_not_found', 'archive_profile_invalid_config', 'drive_destination_not_found', 'drive_destination_unavailable', 'drive_oauth_account_unavailable'];
  const discordCodes = ['discord_config_required', 'discord_config_not_found', 'discord_config_invalid', 'discord_config_service_mismatch', 'discord_audio_forward_unavailable', 'discord_audio_capture_unavailable'];
  return {
    serviceHealth: issues.some((issue) => issue.service_id || issue.service_type || issue.code === 'missing_stream_assignment'),
    serviceID: serviceIssue?.service_id || '',
    discord: [...codes].some((code) => discordCodes.includes(code)),
    youtube: [...codes].some((code) => youtubeCodes.includes(code)),
    archive: [...codes].some((code) => archiveCodes.includes(code)),
    integrations: [...codes].some((code) => code.includes('oauth') || code.includes('drive_destination')),
    metrics: [...codes].some((code) => code.includes('heartbeat') || code.includes('audio') || code.includes('offline')),
  };
}

function readinessIssueHint(issue = {}) {
  switch (issue.code) {
    case 'missing_stream_assignment':
      return 'Open Service Health and assign the required service to this stream.';
    case 'service_call_token_missing':
      return 'Set SERVICE_CALL_TOKEN on the Control Panel and match it with SERVICE_CONTROL_TOKEN_SHA256 on the service.';
    case 'service_public_url_invalid':
    case 'encoder_public_url_invalid':
    case 'encoder_public_url_missing':
      return 'Fix SERVICE_PUBLIC_URL so the Control Panel can reach the service over an allowed HTTP(S) URL.';
    case 'service_offline':
      return 'Start the target service host and confirm that heartbeat is running.';
    case 'service_heartbeat_stale':
      return 'Check Service Health and Metrics for heartbeat age, host status, and network reachability.';
    case 'discord_audio_forward_unavailable':
    case 'discord_audio_capture_unavailable':
      return 'Check Discord Bot audio capability, Encoder/Recorder public URL, and audio token settings.';
    case 'discord_config_required':
      return 'Select a Discord Bot Config on this stream before starting.';
    case 'discord_config_not_found':
      return 'Open Discord Settings and choose an existing Discord Bot Config for this stream.';
    case 'discord_config_invalid':
      return 'Open Discord Settings and verify guild ID and voice channel ID. Stream-level overrides can replace them per stream.';
    case 'discord_config_service_mismatch':
      return 'Assign the Discord Bot service that owns this config as primary, or select a config owned by the current primary Bot.';
    case 'youtube_output_not_found':
      return 'Open YouTube Outputs and select an existing output for this stream.';
    case 'youtube_output_invalid_config':
      return 'Open YouTube Outputs and verify mode, RTMPS URL, stream key secret, or Live API settings.';
    case 'youtube_stream_key_unavailable':
      return 'Open YouTube Outputs and set the write-only stream key. Readiness checks only configured status, not the raw key.';
    case 'youtube_live_api_unavailable':
      return 'Configure the Control Panel YouTube Live API client or use live_api_dry_run / stream_key mode for validation.';
    case 'youtube_oauth_account_unavailable':
      return 'Open Integrations and connect a Google account with YouTube scope, then select it in YouTube Outputs.';
    case 'archive_profile_not_found':
      return 'Open Archive Settings and select an existing archive profile for this stream.';
    case 'archive_profile_invalid_config':
      return 'Open Archive Settings and verify the archive profile and linked Drive destination.';
    case 'drive_destination_not_found':
      return 'Open Integrations and create or select the Drive destination referenced by the archive profile.';
    case 'drive_destination_unavailable':
      return 'Open Integrations and set the write-only Drive folder ID. Readiness checks configured status without reading the raw ID.';
    case 'drive_oauth_account_unavailable':
      return 'Open Integrations and connect a Google account with Drive scope, refresh token, and provider client secret configured.';
    default:
      return 'Review Service Health, Metrics, and service logs, then run Check Readiness again.';
  }
}

function readinessResponseIssues(body) {
  const missing = Array.isArray(body?.missing_service_types) ? body.missing_service_types : [];
  const missingIssues = missing.map((type) => ({
    service_type: type,
    code: 'missing_stream_assignment',
    message: `Assign a ${type} service before start dispatch.`,
  }));
  const issues = Array.isArray(body?.issues) ? body.issues : [];
  return [...missingIssues, ...issues];
}

function controlPanelErrorMessage(body, status, fallback = '') {
  if (!body || typeof body !== 'object') return fallback || `Request failed: ${status}`;
  if (body.code === 'profile_secret_reference_required' || body.code === 'profile_secret_reference_not_allowed') {
    const allowed = Array.isArray(body.allowed_secret_references) ? body.allowed_secret_references.filter(Boolean).join(', ') : '';
    const invalid = Array.isArray(body.invalid_secret_references) ? body.invalid_secret_references.filter(Boolean).join(', ') : '';
    const detail = [invalid ? `Invalid: ${invalid}.` : '', allowed ? `Allowed: ${allowed}.` : ''].filter(Boolean).join(' ');
    return `${body.message || body.code}${detail ? ` ${detail}` : ''}`;
  }
  if (body.code === 'missing_stream_assignments') {
    const missing = Array.isArray(body.missing_service_types) ? body.missing_service_types.join(', ') : 'required service';
    return `Missing stream assignment: ${missing}. Open Service Health and assign the required service before retrying.`;
  }
  if (body.code === 'stream_start_not_ready') {
    const count = Array.isArray(body.issues) ? body.issues.length : 0;
    return `Start readiness failed: ${count} issue(s) must be resolved before start dispatch.`;
  }
  if (body.code === 'service_dispatch_failed') {
    const summary = dispatchSummary('dispatch', status, body.dispatch);
    return `Service dispatch failed: ${summary.successCount} succeeded, ${summary.failedCount} failed. Review the dispatch panel and target service health.`;
  }
  return body.error || body.code ? `${body.error || body.code} (${status})` : fallback || `Request failed: ${status}`;
}

function messageTone(message) {
  if (!message) return 'neutral';
  const lower = message.toLowerCase();
  if (lower.includes('failed') || lower.includes('missing') || lower.includes('unable') || lower.includes('required')) return 'warning';
  if (lower.includes('passed') || lower.includes('accepted')) return 'ok';
  return 'neutral';
}

function dispatchSummary(verb, statusCode, dispatch) {
  const rows = Array.isArray(dispatch) ? dispatch : [dispatch].filter(Boolean);
  const failed = rows.filter((row) => !row.success);
  return {
    verb,
    statusCode,
    rows,
    failedCount: failed.length,
    successCount: rows.length - failed.length,
    checkedAt: new Date().toISOString(),
  };
}

function DispatchResults({ summary }) {
  if (!summary || !Array.isArray(summary.rows) || summary.rows.length === 0) return null;
  const tone = summary.failedCount > 0 ? 'warning' : 'ok';
  return (
    <div className={`dispatch-results ${tone}`}>
      <div className="dispatch-heading">
        <div>
          <strong>Last service dispatch</strong>
          <span>{summary.verb} / HTTP {summary.statusCode} / {summary.successCount} succeeded, {summary.failedCount} failed</span>
        </div>
        <small>{formatDateTime(summary.checkedAt)}</small>
      </div>
      <div className="dispatch-grid">
        {summary.rows.map((row) => (
          <article className={row.success ? 'ok' : 'warning'} key={`${row.service_id || row.service_type}-${row.endpoint}`}>
            <div>
              <strong>{row.service_type || '-'}</strong>
              <Badge tone={row.success ? 'ok' : 'critical'}>{row.success ? 'success' : 'failed'}</Badge>
            </div>
            <span>{row.service_id || '-'}</span>
            <code>{row.endpoint || '-'}</code>
            <small>{dispatchDetail(row)}</small>
          </article>
        ))}
      </div>
    </div>
  );
}

function dispatchDetail(row) {
  const parts = [];
  if (row.status_code) parts.push(`HTTP ${row.status_code}`);
  if (row.code) parts.push(row.code);
  if (row.failure_phase) parts.push(`phase=${row.failure_phase}`);
  if (row.error_class) parts.push(row.error_class);
  if (parts.length > 0) return parts.join(' / ');
  return row.error || 'no status code';
}

function ProfileManager({ title, endpoint, data, example }) {
  const [selectedID, setSelectedID] = useState('');
  const [name, setName] = useState('');
  const [configText, setConfigText] = useState(formatConfig(example));
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });

  const selected = Array.isArray(data.data) ? data.data.find((item) => item.id === selectedID) : null;

  useEffect(() => {
    if (!selected) return;
    setName(selected.name || '');
    setConfigText(formatConfig(selected.config || {}));
    setMessage({ text: '', tone: 'neutral' });
  }, [selected]);

  const reset = () => {
    setSelectedID('');
    setName('');
    setConfigText(formatConfig(example));
    setMessage({ text: '', tone: 'neutral' });
  };

  const save = async () => {
    const trimmedName = name.trim();
    if (!trimmedName) {
      setMessage({ text: 'Name is required.', tone: 'warning' });
      return;
    }

    let config;
    try {
      config = JSON.parse(configText);
    } catch {
      setMessage({ text: 'Config must be valid JSON.', tone: 'warning' });
      return;
    }
    if (!config || Array.isArray(config) || typeof config !== 'object') {
      setMessage({ text: 'Config must be a JSON object.', tone: 'warning' });
      return;
    }

    const method = selectedID ? 'PUT' : 'POST';
    const path = selectedID ? `${endpoint}/${selectedID}` : endpoint;
    const result = await apiRequest(path, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ name: trimmedName, config }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: selectedID ? 'Updated.' : 'Created.', tone: 'ok' });
    await data.reload?.();
    if (!selectedID && result.body?.id) setSelectedID(result.body.id);
  };

  const remove = async () => {
    if (!selectedID) {
      setMessage({ text: 'Select a record to delete.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`${endpoint}/${selectedID}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await data.reload?.();
    reset();
    setMessage({ text: 'Deleted.', tone: 'ok' });
  };

  const columns = [
    ...profileColumns,
    {
      key: 'id',
      label: 'Edit',
      render: (_, row) => <button className="link-btn" type="button" onClick={() => setSelectedID(row.id)}>Edit</button>,
    },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{selectedID ? `Edit ${title}` : `Create ${title}`}</h3>
            <span>Raw secrets must be referenced by secret name only. They are never displayed here.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={reset}>New</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Name</span>
            <input value={name} onChange={(event) => setName(event.target.value)} placeholder="default" />
          </label>
          <label>
            <span>Existing record</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Create new</option>
              {data.data.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
          </label>
          <label className="wide">
            <span>Config JSON</span>
            <textarea value={configText} onChange={(event) => setConfigText(event.target.value)} spellCheck="false" />
          </label>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={save}>{selectedID ? 'Update' : 'Create'}</button>
          <button className="danger-btn" type="button" disabled={!selectedID} onClick={remove}>Delete</button>
        </div>
      </section>
      <DataTable title={title} data={data} columns={columns} />
    </div>
  );
}

function ArchiveProfileManager({ data, destinations }) {
  const blank = {
    name: '',
    drive_destination_id: '',
    service_account_credentials_secret_name: '',
    gdrive_base_path: 'AutoStream',
    upload_enabled: true,
    upload_dry_run: true,
    upload_retry_max: '5',
    retention_days: '30',
    extra_json: '{}',
  };
  const [selectedID, setSelectedID] = useState('');
  const [form, setForm] = useState(blank);
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });
  const profiles = Array.isArray(data.data) ? data.data : [];
  const driveDestinations = Array.isArray(destinations.data) ? destinations.data : [];
  const selected = profiles.find((item) => item.id === selectedID);
  const selectedDestination = driveDestinations.find((item) => item.id === form.drive_destination_id);

  useEffect(() => {
    if (!selected) return;
    const config = selected.config || {};
    const knownKeys = new Set(['drive_destination_id', 'service_account_credentials_secret_name', 'service_account_json_secret_name', 'gdrive_base_path', 'upload_enabled', 'upload_dry_run', 'upload_retry_max', 'retention_days']);
    const extra = Object.fromEntries(Object.entries(config).filter(([key]) => !knownKeys.has(key)));
    setForm({
      name: selected.name || '',
      drive_destination_id: config.drive_destination_id || '',
      service_account_credentials_secret_name: config.service_account_credentials_secret_name || config.service_account_json_secret_name || '',
      gdrive_base_path: config.gdrive_base_path || 'AutoStream',
      upload_enabled: config.upload_enabled ?? true,
      upload_dry_run: config.upload_dry_run ?? true,
      upload_retry_max: config.upload_retry_max != null ? String(config.upload_retry_max) : '5',
      retention_days: config.retention_days != null ? String(config.retention_days) : '30',
      extra_json: formatConfig(extra),
    });
    setMessage({ text: '', tone: 'neutral' });
  }, [selected]);

  const reset = () => {
    setSelectedID('');
    setForm(blank);
    setMessage({ text: '', tone: 'neutral' });
  };
  const update = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const numberOrUndefined = (value) => {
    const trimmed = String(value ?? '').trim();
    if (!trimmed) return undefined;
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : NaN;
  };

  const save = async () => {
    const name = form.name.trim();
    if (!name) {
      setMessage({ text: 'Name is required.', tone: 'warning' });
      return;
    }
    const uploadRetryMax = numberOrUndefined(form.upload_retry_max);
    const retentionDays = numberOrUndefined(form.retention_days);
    if (Number.isNaN(uploadRetryMax) || Number.isNaN(retentionDays)) {
      setMessage({ text: 'Retry max and retention days must be positive numbers.', tone: 'warning' });
      return;
    }
    if (selectedDestination?.auth_mode === 'service_account' && !form.service_account_credentials_secret_name.trim()) {
      setMessage({ text: 'Service Account destinations require a credentials secret name such as google_drive_credentials.', tone: 'warning' });
      return;
    }
    let extra = {};
    try {
      extra = JSON.parse(form.extra_json || '{}');
    } catch {
      setMessage({ text: 'Advanced JSON must be valid JSON.', tone: 'warning' });
      return;
    }
    if (!extra || Array.isArray(extra) || typeof extra !== 'object') {
      setMessage({ text: 'Advanced JSON must be a JSON object.', tone: 'warning' });
      return;
    }
    const config = {
      ...extra,
      drive_destination_id: form.drive_destination_id.trim(),
      gdrive_base_path: form.gdrive_base_path.trim() || 'AutoStream',
      upload_enabled: Boolean(form.upload_enabled),
      upload_dry_run: Boolean(form.upload_dry_run),
    };
    if (form.service_account_credentials_secret_name.trim()) {
      config.service_account_credentials_secret_name = form.service_account_credentials_secret_name.trim();
    }
    if (uploadRetryMax !== undefined) config.upload_retry_max = uploadRetryMax;
    if (retentionDays !== undefined) config.retention_days = retentionDays;

    const result = await apiRequest(selectedID ? `/profiles/archive/${selectedID}` : '/profiles/archive', {
      method: selectedID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ name, config }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await data.reload?.();
    if (!selectedID && result.body?.id) setSelectedID(result.body.id);
    setMessage({ text: selectedID ? 'Updated.' : 'Created.', tone: 'ok' });
  };

  const remove = async () => {
    if (!selectedID) {
      setMessage({ text: 'Select an archive profile to delete.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/profiles/archive/${selectedID}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await data.reload?.();
    reset();
    setMessage({ text: 'Deleted.', tone: 'ok' });
  };

  const columns = [
    { key: 'name', label: 'Name' },
    { key: 'archive_drive_destination', label: 'Drive Destination', render: (_, row) => row.config?.drive_destination_id || '-' },
    { key: 'archive_upload_enabled', label: 'Upload', render: (_, row) => <Badge tone={row.config?.upload_enabled === false ? 'warning' : 'ok'}>{row.config?.upload_enabled === false ? 'disabled' : 'enabled'}</Badge> },
    { key: 'archive_upload_dry_run', label: 'Dry-run', render: (_, row) => <Badge tone={row.config?.upload_dry_run === false ? 'warning' : 'ok'}>{row.config?.upload_dry_run === false ? 'off' : 'on'}</Badge> },
    { key: 'updated_at', label: 'Updated' },
    { key: 'id', label: 'Edit', render: (_, row) => <button className="link-btn" type="button" onClick={() => setSelectedID(row.id)}>Edit</button> },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{selectedID ? 'Edit Archive Settings' : 'Create Archive Settings'}</h3>
            <span>Archive profiles reference Control Panel Drive destinations. Folder IDs and OAuth tokens are never displayed here.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={reset}>New</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing record</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Create new</option>
              {profiles.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input value={form.name} onChange={(event) => update('name', event.target.value)} placeholder="Main Archive" />
          </label>
          <label>
            <span>Drive destination</span>
            <select value={form.drive_destination_id} onChange={(event) => update('drive_destination_id', event.target.value)}>
              <option value="">None / local archive only</option>
              {driveDestinations.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name} ({item.auth_mode}{item.shared_drive ? ', shared drive' : ''})
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>Base path</span>
            <input value={form.gdrive_base_path} onChange={(event) => update('gdrive_base_path', event.target.value)} placeholder="AutoStream" />
          </label>
          <label>
            <span>Service Account credential secret</span>
            <input value={form.service_account_credentials_secret_name} onChange={(event) => update('service_account_credentials_secret_name', event.target.value)} placeholder="google_drive_credentials" />
          </label>
          <label>
            <span>Upload retry max</span>
            <input value={form.upload_retry_max} onChange={(event) => update('upload_retry_max', event.target.value)} inputMode="numeric" />
          </label>
          <label>
            <span>Retention days</span>
            <input value={form.retention_days} onChange={(event) => update('retention_days', event.target.value)} inputMode="numeric" />
          </label>
          <label className="checkline">
            <input type="checkbox" checked={Boolean(form.upload_enabled)} onChange={(event) => update('upload_enabled', event.target.checked)} />
            <span>Upload final archive</span>
          </label>
          <label className="checkline">
            <input type="checkbox" checked={Boolean(form.upload_dry_run)} onChange={(event) => update('upload_dry_run', event.target.checked)} />
            <span>Dry-run upload until external verification is approved</span>
          </label>
          <label className="wide">
            <span>Advanced JSON</span>
            <textarea value={form.extra_json} onChange={(event) => update('extra_json', event.target.value)} spellCheck="false" />
          </label>
        </div>
        {driveDestinations.length === 0 && <Message text="Create a Drive destination in Integrations before enabling Google Drive upload." tone="warning" />}
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={save}>{selectedID ? 'Update' : 'Create'}</button>
          <button className="danger-btn" type="button" disabled={!selectedID} onClick={remove}>Delete</button>
        </div>
      </section>
      <DataTable title="Archive Settings" data={data} columns={columns} />
    </div>
  );
}

function DiscordConfigManager({ data }) {
  const blank = {
    name: '',
    service_id: '',
    guild_id: '',
    voice_channel_id: '',
    text_channel_id: '',
    bot_token: '',
    caption_enabled: false,
    stt_profile_id: '',
    reconnect_enabled: true,
    reconnect_max_attempts: 5,
    reconnect_base_delay: '2s',
    reconnect_max_delay: '30s',
    audio_forward_enabled: true,
  };
  const [selectedID, setSelectedID] = useState('');
  const [form, setForm] = useState(blank);
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });
  const configs = Array.isArray(data.data) ? data.data : [];
  const selected = configs.find((item) => item.id === selectedID);

  useEffect(() => {
    if (!selected) return;
    setForm({
      name: selected.name || '',
      service_id: selected.service_id || '',
      guild_id: selected.guild_id || '',
      voice_channel_id: selected.voice_channel_id || '',
      text_channel_id: selected.text_channel_id || '',
      bot_token: '',
      caption_enabled: Boolean(selected.caption_enabled),
      stt_profile_id: selected.stt_profile_id || '',
      reconnect_enabled: selected.reconnect_enabled ?? true,
      reconnect_max_attempts: selected.reconnect_max_attempts || 5,
      reconnect_base_delay: selected.reconnect_base_delay || '2s',
      reconnect_max_delay: selected.reconnect_max_delay || '30s',
      audio_forward_enabled: selected.audio_forward_enabled ?? true,
    });
    setMessage({ text: '', tone: 'neutral' });
  }, [selected]);

  const update = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const reset = () => {
    setSelectedID('');
    setForm(blank);
    setMessage({ text: '', tone: 'neutral' });
  };
  const save = async () => {
    if (!form.name.trim()) {
      setMessage({ text: 'Name is required.', tone: 'warning' });
      return;
    }
    const reconnectMaxAttempts = Number(form.reconnect_max_attempts);
    if (!Number.isInteger(reconnectMaxAttempts) || reconnectMaxAttempts < 1) {
      setMessage({ text: 'Reconnect attempts must be a positive integer.', tone: 'warning' });
      return;
    }
    const body = {
      ...form,
      name: form.name.trim(),
      service_id: form.service_id.trim(),
      guild_id: form.guild_id.trim(),
      voice_channel_id: form.voice_channel_id.trim(),
      text_channel_id: form.text_channel_id.trim(),
      bot_token: form.bot_token.trim(),
      stt_profile_id: form.stt_profile_id.trim(),
      reconnect_max_attempts: reconnectMaxAttempts,
      reconnect_base_delay: String(form.reconnect_base_delay || '').trim(),
      reconnect_max_delay: String(form.reconnect_max_delay || '').trim(),
    };
    const result = await apiRequest(selectedID ? `/discord/configs/${selectedID}` : '/discord/configs', {
      method: selectedID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(body),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await data.reload?.();
    setForm((current) => ({ ...current, bot_token: '' }));
    if (!selectedID && result.body?.id) setSelectedID(result.body.id);
    setMessage({ text: selectedID ? 'Updated.' : 'Created.', tone: 'ok' });
  };
  const remove = async () => {
    if (!selectedID) {
      setMessage({ text: 'Select a Discord config to delete.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/discord/configs/${selectedID}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await data.reload?.();
    reset();
    setMessage({ text: 'Deleted.', tone: 'ok' });
  };

  const columns = [
    { key: 'name', label: 'Name' },
    { key: 'service_id', label: 'Bot Service', render: (value) => value || '-' },
    { key: 'guild_id', label: 'Guild', render: (value) => value || '-' },
    { key: 'voice_channel_id', label: 'Voice Channel', render: (value) => value || '-' },
    { key: 'bot_token_configured', label: 'Bot Token', render: (value, row) => <Badge tone={value ? 'ok' : 'warning'}>{value ? `configured ${row.bot_token_fingerprint || ''}` : 'missing'}</Badge> },
    { key: 'audio_forward_enabled', label: 'Audio Forward', render: (value) => <Badge tone={value ? 'ok' : 'warning'}>{value ? 'enabled' : 'disabled'}</Badge> },
    { key: 'reconnect_max_attempts', label: 'Rejoin Attempts', render: (value) => value || '-' },
    { key: 'updated_at', label: 'Updated' },
    { key: 'id', label: 'Edit', render: (_, row) => <button className="link-btn" type="button" onClick={() => setSelectedID(row.id)}>Edit</button> },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{selectedID ? 'Edit Discord Bot Config' : 'Create Discord Bot Config'}</h3>
            <span>Bot tokens are write-only. Assign each config to the Discord Bot service that is allowed to read its runtime config.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={reset}>New</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing config</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Create new</option>
              {configs.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input value={form.name} onChange={(event) => update('name', event.target.value)} placeholder="Main Discord Bot" />
          </label>
          <label>
            <span>Bot service ID</span>
            <input value={form.service_id} onChange={(event) => update('service_id', event.target.value)} placeholder="discord-bot-01" />
          </label>
          <label>
            <span>Guild ID</span>
            <input value={form.guild_id} onChange={(event) => update('guild_id', event.target.value)} placeholder="<DISCORD_GUILD_ID>" />
          </label>
          <label>
            <span>Voice channel ID</span>
            <input value={form.voice_channel_id} onChange={(event) => update('voice_channel_id', event.target.value)} placeholder="<VOICE_CHANNEL_ID>" />
          </label>
          <label>
            <span>Text channel ID</span>
            <input value={form.text_channel_id} onChange={(event) => update('text_channel_id', event.target.value)} placeholder="optional" />
          </label>
          <label>
            <span>Bot token</span>
            <input type="password" value={form.bot_token} onChange={(event) => update('bot_token', event.target.value)} placeholder={selectedID ? 'leave blank to keep existing token' : '<DISCORD_BOT_TOKEN>'} />
          </label>
          <label>
            <span>STT profile ID</span>
            <input value={form.stt_profile_id} onChange={(event) => update('stt_profile_id', event.target.value)} placeholder="optional" />
          </label>
          <label className="check-row">
            <input type="checkbox" checked={form.audio_forward_enabled} onChange={(event) => update('audio_forward_enabled', event.target.checked)} />
            <span>Enable audio forward</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={form.reconnect_enabled} onChange={(event) => update('reconnect_enabled', event.target.checked)} />
            <span>Reconnect voice automatically</span>
          </label>
          <label>
            <span>Reconnect attempts</span>
            <input type="number" min="1" step="1" value={form.reconnect_max_attempts} onChange={(event) => update('reconnect_max_attempts', event.target.value)} />
          </label>
          <label>
            <span>Reconnect base delay</span>
            <input value={form.reconnect_base_delay} onChange={(event) => update('reconnect_base_delay', event.target.value)} placeholder="2s" />
          </label>
          <label>
            <span>Reconnect max delay</span>
            <input value={form.reconnect_max_delay} onChange={(event) => update('reconnect_max_delay', event.target.value)} placeholder="30s" />
          </label>
          <label className="check-row">
            <input type="checkbox" checked={form.caption_enabled} onChange={(event) => update('caption_enabled', event.target.checked)} />
            <span>Enable captions/STT forwarding</span>
          </label>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={save}>{selectedID ? 'Update' : 'Create'}</button>
          <button className="danger-btn" type="button" disabled={!selectedID} onClick={remove}>Delete</button>
        </div>
      </section>
      <DataTable title="Discord Bot Configs" data={data} columns={columns} />
    </div>
  );
}

function YouTubeOutputManager({ data, accounts }) {
  const blank = {
    name: '',
    mode: 'stream_key',
    rtmp_url: 'rtmps://a.rtmps.youtube.com/live2',
    stream_key: '',
    oauth_account_id: '',
    broadcast_title_template: '',
    broadcast_description: '',
    privacy_status: 'private',
    latency_preference: 'normal',
    enable_auto_start: true,
    enable_auto_stop: true,
    complete_on_stop: true,
  };
  const [selectedID, setSelectedID] = useState('');
  const [form, setForm] = useState(blank);
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });
  const outputs = Array.isArray(data.data) ? data.data : [];
  const oauthAccounts = Array.isArray(accounts?.data) ? accounts.data : [];
  const selected = outputs.find((item) => item.id === selectedID);
  const accountLabel = (id) => {
    const account = oauthAccounts.find((item) => item.id === id);
    if (!account) return id || '-';
    return `${account.account_label || account.id} / ${account.email || account.subject || account.provider_type || account.id}`;
  };

  useEffect(() => {
    if (!selected) return;
    setForm({
      name: selected.name || '',
      mode: selected.mode || 'stream_key',
      rtmp_url: selected.rtmp_url || 'rtmps://a.rtmps.youtube.com/live2',
      stream_key: '',
      oauth_account_id: selected.oauth_account_id || '',
      broadcast_title_template: selected.broadcast_title_template || '',
      broadcast_description: selected.broadcast_description || '',
      privacy_status: selected.privacy_status || 'private',
      latency_preference: selected.latency_preference || 'normal',
      enable_auto_start: selected.enable_auto_start ?? true,
      enable_auto_stop: selected.enable_auto_stop ?? true,
      complete_on_stop: selected.complete_on_stop ?? true,
    });
    setMessage({ text: '', tone: 'neutral' });
  }, [selected]);

  const update = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const reset = () => {
    setSelectedID('');
    setForm(blank);
    setMessage({ text: '', tone: 'neutral' });
  };
  const save = async () => {
    if (!form.name.trim()) {
      setMessage({ text: 'Name is required.', tone: 'warning' });
      return;
    }
    if (form.mode !== 'stream_key' && !form.oauth_account_id.trim()) {
      setMessage({ text: 'Select an OAuth connected account for YouTube Live API modes.', tone: 'warning' });
      return;
    }
    const body = {
      ...form,
      name: form.name.trim(),
      rtmp_url: form.rtmp_url.trim(),
      stream_key: form.stream_key.trim(),
      oauth_account_id: form.oauth_account_id.trim(),
      broadcast_title_template: form.broadcast_title_template.trim(),
      broadcast_description: form.broadcast_description.trim(),
    };
    if (body.mode !== 'stream_key') body.stream_key = '';
    const result = await apiRequest(selectedID ? `/youtube/outputs/${selectedID}` : '/youtube/outputs', {
      method: selectedID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(body),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await data.reload?.();
    setForm((current) => ({ ...current, stream_key: '' }));
    if (!selectedID && result.body?.id) setSelectedID(result.body.id);
    setMessage({ text: selectedID ? 'Updated.' : 'Created.', tone: 'ok' });
  };
  const remove = async () => {
    if (!selectedID) {
      setMessage({ text: 'Select an output to delete.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/youtube/outputs/${selectedID}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await data.reload?.();
    reset();
    setMessage({ text: 'Deleted.', tone: 'ok' });
  };

  const columns = [
    { key: 'name', label: 'Name' },
    { key: 'mode', label: 'Mode', render: (value) => <Badge tone={value === 'live_api' ? 'ok' : 'neutral'}>{value}</Badge> },
    { key: 'rtmp_url', label: 'RTMPS URL', render: (value) => value || '-' },
    { key: 'stream_key_configured', label: 'Stream Key', render: (value, row) => <Badge tone={value ? 'ok' : 'warning'}>{value ? `configured ${row.stream_key_fingerprint || ''}` : 'missing'}</Badge> },
    { key: 'oauth_account_id', label: 'OAuth Account', render: accountLabel },
    { key: 'updated_at', label: 'Updated' },
    { key: 'id', label: 'Edit', render: (_, row) => <button className="link-btn" type="button" onClick={() => setSelectedID(row.id)}>Edit</button> },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{selectedID ? 'Edit YouTube Output' : 'Create YouTube Output'}</h3>
            <span>Stream keys and OAuth tokens are write-only. Select a Control Panel connected account for Live API modes.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={reset}>New</button>
        </div>
        {accounts?.loading && <Message text="Loading OAuth connected accounts..." />}
        {accounts?.error && <Message text={`OAuth connected accounts unavailable: ${accounts.error}`} tone="warning" />}
        <div className="form-grid">
          <label>
            <span>Existing output</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Create new</option>
              {outputs.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input value={form.name} onChange={(event) => update('name', event.target.value)} placeholder="Main YouTube Output" />
          </label>
          <label>
            <span>Mode</span>
            <select value={form.mode} onChange={(event) => update('mode', event.target.value)}>
              <option value="stream_key">Existing stream key</option>
              <option value="live_api_dry_run">Live API dry-run</option>
              <option value="live_api">Live API</option>
            </select>
          </label>
          <label>
            <span>RTMPS URL</span>
            <input value={form.rtmp_url} onChange={(event) => update('rtmp_url', event.target.value)} placeholder="rtmps://a.rtmps.youtube.com/live2" />
          </label>
          {form.mode === 'stream_key' && (
            <label>
              <span>Stream key</span>
              <input type="password" value={form.stream_key} onChange={(event) => update('stream_key', event.target.value)} placeholder={selectedID ? 'leave blank to keep existing key' : '<YOUTUBE_STREAM_KEY>'} />
            </label>
          )}
          {form.mode !== 'stream_key' && (
            <label>
              <span>OAuth connected account</span>
              <select value={form.oauth_account_id} onChange={(event) => update('oauth_account_id', event.target.value)}>
                <option value="">Select connected account</option>
                {oauthAccounts.map((account) => (
                  <option key={account.id} value={account.id}>{accountLabel(account.id)}</option>
                ))}
              </select>
              {oauthAccounts.length === 0 && <small className="form-note">Create a Google OAuth connected account in Integrations before using Live API mode.</small>}
            </label>
          )}
          <label>
            <span>Privacy</span>
            <select value={form.privacy_status} onChange={(event) => update('privacy_status', event.target.value)}>
              <option value="private">private</option>
              <option value="unlisted">unlisted</option>
              <option value="public">public</option>
            </select>
          </label>
          <label>
            <span>Latency</span>
            <select value={form.latency_preference} onChange={(event) => update('latency_preference', event.target.value)}>
              <option value="normal">normal</option>
              <option value="low">low</option>
              <option value="ultra_low">ultra_low</option>
            </select>
          </label>
          <label className="wide">
            <span>Broadcast title template</span>
            <input value={form.broadcast_title_template} onChange={(event) => update('broadcast_title_template', event.target.value)} placeholder="{{stream_name}}" />
          </label>
          <label className="wide">
            <span>Broadcast description</span>
            <textarea value={form.broadcast_description} onChange={(event) => update('broadcast_description', event.target.value)} />
          </label>
          <label className="check-row">
            <input type="checkbox" checked={form.enable_auto_start} onChange={(event) => update('enable_auto_start', event.target.checked)} />
            <span>Enable auto start</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={form.enable_auto_stop} onChange={(event) => update('enable_auto_stop', event.target.checked)} />
            <span>Enable auto stop</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={form.complete_on_stop} onChange={(event) => update('complete_on_stop', event.target.checked)} />
            <span>Complete broadcast on stream stop</span>
          </label>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={save}>{selectedID ? 'Update' : 'Create'}</button>
          <button className="danger-btn" type="button" disabled={!selectedID} onClick={remove}>Delete</button>
        </div>
      </section>
      <DataTable title="YouTube Outputs" data={data} columns={columns} />
    </div>
  );
}

const profileColumns = [
  { key: 'name', label: 'Name' },
  { key: 'kind', label: 'Kind' },
  { key: 'config', label: 'Config', render: (value) => <code>{safeJSON(value)}</code> },
  { key: 'updated_at', label: 'Updated' },
];

const serviceColumns = [
  { key: 'service_name', label: 'Name' },
  { key: 'service_type', label: 'Type' },
  { key: 'status', label: 'Status', render: (_, row) => {
    const health = serviceHealthState(row);
    return <Badge tone={health.tone}>{health.label}</Badge>;
  } },
  { key: 'public_url', label: 'URL' },
  { key: 'assignment_role', label: 'Role', render: (value) => value ? <Badge tone={value === 'primary' ? 'ok' : 'warning'}>{value}</Badge> : '-' },
  { key: 'current_stream_id', label: 'Stream', render: (value) => value || '-' },
  { key: 'capabilities', label: 'Capabilities', render: (value) => <CapabilityList value={value} /> },
  { key: 'metrics', label: 'Heartbeat Metrics', render: (value) => <ServiceMetricList value={value} /> },
  { key: 'last_heartbeat_at', label: 'Heartbeat', render: (value) => heartbeatLabel(value) },
];

const userColumns = [
  { key: 'username', label: 'Username' },
  { key: 'status', label: 'Status', render: (value) => <Badge tone={value === 'active' ? 'ok' : 'warning'}>{value}</Badge> },
  { key: 'roles', label: 'Roles', render: (value) => Array.isArray(value) ? value.join(', ') : '-' },
  { key: 'last_login_at', label: 'Last Login' },
  { key: 'last_login_ip', label: 'Last IP' },
];

const roleColumns = [
  { key: 'name', label: 'Name' },
  { key: 'permissions', label: 'Permissions', render: (value) => Array.isArray(value) ? value.join(', ') : '-' },
];

const auditColumns = [
  { key: 'timestamp', label: 'Timestamp', render: formatDateTime },
  { key: 'actor_username', label: 'Actor' },
  { key: 'action', label: 'Action' },
  { key: 'resource_type', label: 'Resource' },
  { key: 'resource_id', label: 'ID' },
  { key: 'result', label: 'Result', render: (value) => <Badge tone={value === 'success' ? 'ok' : 'critical'}>{value}</Badge> },
  { key: 'metadata', label: 'Metadata', render: (value) => <code>{safeJSON(value || {})}</code> },
];

const tokenColumns = [
  { key: 'service_type', label: 'Service Type' },
  { key: 'scopes', label: 'Scopes', render: (value) => Array.isArray(value) ? value.join(', ') : '-' },
  { key: 'created_at', label: 'Created' },
  { key: 'revoked_at', label: 'Revoked' },
];

function IntegrationRegistryView({ providers, accounts, destinations, roles }) {
  const [providerID, setProviderID] = useState('');
  const [providerForm, setProviderForm] = useState({
    provider_type: 'google',
    name: '',
    enabled: true,
    client_id: '',
    client_secret: '',
    scopes: 'openid,email',
    allowed_domains: '',
    auto_provision: false,
    default_role_ids: [],
    redirect_uri: '',
  });
  const [accountID, setAccountID] = useState('');
  const [accountForm, setAccountForm] = useState({
    provider_id: '',
    account_label: '',
    subject: '',
    email: '',
    scopes: 'https://www.googleapis.com/auth/drive.file',
    refresh_token: '',
  });
  const [destinationID, setDestinationID] = useState('');
  const [destinationForm, setDestinationForm] = useState({
    name: '',
    auth_mode: 'oauth2',
    oauth_account_id: '',
    folder_id: '',
    shared_drive: false,
    base_path: 'AutoStream',
  });
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });

  const selectedProvider = providers.data.find((item) => item.id === providerID);
  const selectedAccount = accounts.data.find((item) => item.id === accountID);
  const selectedDestination = destinations.data.find((item) => item.id === destinationID);
  const roleOptions = roles.error ? [] : (roles.data || []);

  useEffect(() => {
    if (!selectedProvider) return;
    setProviderForm({
      provider_type: selectedProvider.provider_type || 'google',
      name: selectedProvider.name || '',
      enabled: Boolean(selectedProvider.enabled),
      client_id: selectedProvider.client_id || '',
      client_secret: '',
      scopes: (selectedProvider.scopes || []).join(','),
      allowed_domains: (selectedProvider.allowed_domains || []).join(','),
      auto_provision: Boolean(selectedProvider.auto_provision),
      default_role_ids: selectedProvider.default_role_ids || [],
      redirect_uri: selectedProvider.redirect_uri || '',
    });
  }, [selectedProvider]);

  useEffect(() => {
    if (!selectedAccount) return;
    setAccountForm({
      provider_id: selectedAccount.provider_id || '',
      account_label: selectedAccount.account_label || '',
      subject: selectedAccount.subject || '',
      email: selectedAccount.email || '',
      scopes: (selectedAccount.scopes || []).join(','),
      refresh_token: '',
    });
  }, [selectedAccount]);

  useEffect(() => {
    if (!selectedDestination) return;
    setDestinationForm({
      name: selectedDestination.name || '',
      auth_mode: selectedDestination.auth_mode || 'oauth2',
      oauth_account_id: selectedDestination.oauth_account_id || '',
      folder_id: '',
      shared_drive: Boolean(selectedDestination.shared_drive),
      base_path: selectedDestination.base_path || 'AutoStream',
    });
  }, [selectedDestination]);

  const providerLabel = (id) => {
    const provider = providers.data.find((item) => item.id === id);
    return provider ? `${provider.name} / ${provider.provider_type}` : id || '-';
  };
  const accountLabel = (id) => {
    const account = accounts.data.find((item) => item.id === id);
    return account ? `${account.account_label} / ${account.email || account.subject || account.id}` : id || '-';
  };

  const parseList = (value) => value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean);
  const updateProvider = (key, value) => setProviderForm((current) => ({ ...current, [key]: value }));
  const toggleProviderDefaultRole = (roleID) => setProviderForm((current) => ({
    ...current,
    default_role_ids: current.default_role_ids.includes(roleID)
      ? current.default_role_ids.filter((id) => id !== roleID)
      : [...current.default_role_ids, roleID],
  }));
  const updateAccount = (key, value) => setAccountForm((current) => ({ ...current, [key]: value }));
  const updateDestination = (key, value) => setDestinationForm((current) => ({ ...current, [key]: value }));

  const resetProvider = () => {
    setProviderID('');
    setProviderForm({ provider_type: 'google', name: '', enabled: true, client_id: '', client_secret: '', scopes: 'openid,email', allowed_domains: '', auto_provision: false, default_role_ids: [], redirect_uri: '' });
  };
  const resetAccount = () => {
    setAccountID('');
    setAccountForm({ provider_id: '', account_label: '', subject: '', email: '', scopes: 'https://www.googleapis.com/auth/drive.file', refresh_token: '' });
  };
  const resetDestination = () => {
    setDestinationID('');
    setDestinationForm({ name: '', auth_mode: 'oauth2', oauth_account_id: '', folder_id: '', shared_drive: false, base_path: 'AutoStream' });
  };

  const saveProvider = async () => {
    const result = await apiRequest(providerID ? `/integrations/oauth-providers/${providerID}` : '/integrations/oauth-providers', {
      method: providerID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({
        provider_type: providerForm.provider_type,
        name: providerForm.name.trim(),
        enabled: providerForm.enabled,
        client_id: providerForm.client_id.trim(),
        client_secret: providerForm.client_secret,
        scopes: parseList(providerForm.scopes),
        allowed_domains: parseList(providerForm.allowed_domains),
        auto_provision: Boolean(providerForm.auto_provision),
        default_role_ids: providerForm.auto_provision ? providerForm.default_role_ids : [],
        redirect_uri: providerForm.redirect_uri.trim(),
      }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: providerID ? 'OAuth provider updated.' : 'OAuth provider created.', tone: 'ok' });
    setProviderForm((current) => ({ ...current, client_secret: '' }));
    await providers.reload?.();
    if (!providerID && result.body?.id) setProviderID(result.body.id);
  };

  const saveAccount = async () => {
    if (!accountID) {
      setMessage({ text: 'Use Connect with OAuth to create connected accounts. Manual refresh token entry is disabled.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/integrations/oauth-accounts/${accountID}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({
        account_label: accountForm.account_label.trim(),
      }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'OAuth connected account label updated.', tone: 'ok' });
    await accounts.reload?.();
  };

  const startOAuthAccountConnection = async () => {
    if (!accountForm.provider_id) {
      setMessage({ text: 'Select an OAuth provider first.', tone: 'warning' });
      return;
    }
    const result = await apiRequest('/integrations/oauth-accounts/start', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({
        provider_id: accountForm.provider_id,
        account_label: accountForm.account_label.trim(),
        redirect_after: '/',
      }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    const url = result.body?.authorization_url;
    if (!url) {
      setMessage({ text: 'OAuth authorization URL was not returned.', tone: 'warning' });
      return;
    }
    window.location.href = url;
  };

  const saveDestination = async () => {
    const result = await apiRequest(destinationID ? `/archive/destinations/${destinationID}` : '/archive/destinations', {
      method: destinationID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({
        name: destinationForm.name.trim(),
        auth_mode: destinationForm.auth_mode,
        oauth_account_id: destinationForm.auth_mode === 'oauth2' ? destinationForm.oauth_account_id : '',
        folder_id: destinationForm.folder_id,
        shared_drive: destinationForm.shared_drive,
        base_path: destinationForm.base_path.trim(),
      }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: destinationID ? 'Drive destination updated.' : 'Drive destination created.', tone: 'ok' });
    setDestinationForm((current) => ({ ...current, folder_id: '' }));
    await destinations.reload?.();
    if (!destinationID && result.body?.id) setDestinationID(result.body.id);
  };

  const remove = async (kind, id) => {
    const endpoints = {
      provider: '/integrations/oauth-providers',
      account: '/integrations/oauth-accounts',
      destination: '/archive/destinations',
    };
    const result = await apiRequest(`${endpoints[kind]}/${id}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: `${kind} deleted.`, tone: 'ok' });
    if (kind === 'provider') {
      resetProvider();
      await providers.reload?.();
    } else if (kind === 'account') {
      resetAccount();
      await accounts.reload?.();
    } else {
      resetDestination();
      await destinations.reload?.();
    }
  };

  const providerColumns = [
    { key: 'name', label: 'Name' },
    { key: 'provider_type', label: 'Type' },
    { key: 'enabled', label: 'Enabled', render: (value) => <Badge tone={value ? 'ok' : 'warning'}>{value ? 'enabled' : 'disabled'}</Badge> },
    { key: 'client_secret_configured', label: 'Secret', render: (value, row) => <Badge tone={value ? 'ok' : 'warning'}>{value ? `configured ${row.client_secret_fingerprint || ''}` : 'missing'}</Badge> },
    { key: 'auto_provision', label: 'Auto Provision', render: (value, row) => value ? <Badge tone="ok">{(row.default_role_ids || []).length} role(s)</Badge> : <Badge tone="neutral">disabled</Badge> },
    { key: 'allowed_domains', label: 'Allowed Domains', render: (value) => Array.isArray(value) && value.length ? value.join(', ') : '-' },
    { key: 'id', label: 'Actions', render: (value) => <button className="link-btn" type="button" onClick={() => setProviderID(value)}>Edit</button> },
  ];
  const accountColumns = [
    { key: 'account_label', label: 'Label' },
    { key: 'provider_id', label: 'Provider', render: providerLabel },
    { key: 'email', label: 'Email' },
    { key: 'refresh_token_configured', label: 'Refresh Token', render: (value, row) => <Badge tone={value ? 'ok' : 'warning'}>{value ? `configured ${row.token_fingerprint || ''}` : 'missing'}</Badge> },
    { key: 'id', label: 'Actions', render: (value) => <button className="link-btn" type="button" onClick={() => setAccountID(value)}>Rename</button> },
  ];
  const destinationColumns = [
    { key: 'name', label: 'Name' },
    { key: 'auth_mode', label: 'Auth Mode' },
    { key: 'oauth_account_id', label: 'OAuth Account', render: accountLabel },
    { key: 'masked_folder_id', label: 'Folder ID', render: (value, row) => value || (row.folder_id_configured ? 'configured' : 'missing') },
    { key: 'shared_drive', label: 'Shared Drive', render: (value) => value ? 'yes' : 'no' },
    { key: 'base_path', label: 'Base Path' },
    { key: 'id', label: 'Actions', render: (value) => <button className="link-btn" type="button" onClick={() => setDestinationID(value)}>Edit</button> },
  ];

  if (providers.loading || accounts.loading || destinations.loading) return <Message text="Loading integrations..." />;
  if (providers.error) return <Message text={providers.error} tone="warning" />;
  if (accounts.error) return <Message text={accounts.error} tone="warning" />;
  if (destinations.error) return <Message text={destinations.error} tone="warning" />;

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Integration Registry</h3>
            <span>Operational OAuth, Drive, YouTube, and notification settings should be managed here instead of service env files. Raw secrets are write-only.</span>
          </div>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="integration-summary">
          <span className="health-card ok">OAuth providers: {providers.data.length}</span>
          <span className="health-card ok">Connected accounts: {accounts.data.length}</span>
          <span className="health-card ok">Drive destinations: {destinations.data.length}</span>
        </div>
      </section>

      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{providerID ? 'Edit OAuth Provider' : 'Create OAuth Provider'}</h3>
            <span>Use Google / GitHub / Discord for login providers, and Google for Drive or YouTube connected accounts.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={resetProvider}>New Provider</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing provider</span>
            <select value={providerID} onChange={(event) => setProviderID(event.target.value)}>
              <option value="">Create new</option>
              {providers.data.map((provider) => <option key={provider.id} value={provider.id}>{provider.name} / {provider.provider_type}</option>)}
            </select>
          </label>
          <label>
            <span>Provider type</span>
            <select value={providerForm.provider_type} onChange={(event) => updateProvider('provider_type', event.target.value)}>
              <option value="google">Google</option>
              <option value="github">GitHub</option>
              <option value="discord">Discord</option>
            </select>
          </label>
          <label>
            <span>Name</span>
            <input value={providerForm.name} onChange={(event) => updateProvider('name', event.target.value)} placeholder="Google Login" />
          </label>
          <label>
            <span>Client ID</span>
            <input value={providerForm.client_id} onChange={(event) => updateProvider('client_id', event.target.value)} />
          </label>
          <label>
            <span>Client secret</span>
            <input type="password" value={providerForm.client_secret} onChange={(event) => updateProvider('client_secret', event.target.value)} placeholder={providerID ? 'leave blank to keep existing secret' : ''} />
          </label>
          <label>
            <span>Redirect URI</span>
            <input value={providerForm.redirect_uri} onChange={(event) => updateProvider('redirect_uri', event.target.value)} placeholder="https://control.example.com/auth/oauth/callback" />
          </label>
          <label>
            <span>Scopes</span>
            <input value={providerForm.scopes} onChange={(event) => updateProvider('scopes', event.target.value)} placeholder="openid,email" />
          </label>
          <label>
            <span>Allowed domains</span>
            <input value={providerForm.allowed_domains} onChange={(event) => updateProvider('allowed_domains', event.target.value)} placeholder="example.com" />
          </label>
          <label className="check-row">
            <input type="checkbox" checked={providerForm.enabled} onChange={(event) => updateProvider('enabled', event.target.checked)} />
            <span>Enabled</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={providerForm.auto_provision} onChange={(event) => updateProvider('auto_provision', event.target.checked)} />
            <span>Auto-provision first login</span>
          </label>
          <div className="wide-field">
            <span className="field-label">Default roles for auto-provisioned users</span>
            {roles.loading && <small className="form-note">Loading roles...</small>}
            {roles.error && <Message text={`Role list unavailable: ${roles.error}`} tone="warning" />}
            <div className="checkbox-grid">
              {roleOptions.map((role) => (
                <label key={role.id} className="check-row">
                  <input type="checkbox" checked={providerForm.default_role_ids.includes(role.id)} disabled={!providerForm.auto_provision} onChange={() => toggleProviderDefaultRole(role.id)} />
                  <span>{role.name}</span>
                </label>
              ))}
            </div>
            <small className="form-note">Auto-provision requires at least one default role and server-side roles.assign permission.</small>
          </div>
        </div>
        <div className="actions">
          <button className="command-btn" type="button" onClick={saveProvider}>{providerID ? 'Update Provider' : 'Create Provider'}</button>
          <button className="danger-btn" type="button" disabled={!providerID} onClick={() => remove('provider', providerID)}>Delete Provider</button>
        </div>
      </section>
      <DataTable title="OAuth Providers" data={providers} columns={providerColumns} />

      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{accountID ? 'Rename OAuth Connected Account' : 'Connect OAuth Connected Account'}</h3>
            <span>Connected accounts are created only by OAuth callback. Refresh tokens are encrypted and returned only as configured state and fingerprint.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={resetAccount}>New Account</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing account</span>
            <select value={accountID} onChange={(event) => setAccountID(event.target.value)}>
              <option value="">Connect new</option>
              {accounts.data.map((account) => <option key={account.id} value={account.id}>{account.account_label} / {account.email || account.subject}</option>)}
            </select>
          </label>
          <label>
            <span>Provider</span>
            <select value={accountForm.provider_id} disabled={Boolean(accountID)} onChange={(event) => updateAccount('provider_id', event.target.value)}>
              <option value="">Select provider</option>
              {providers.data.map((provider) => <option key={provider.id} value={provider.id}>{provider.name} / {provider.provider_type}</option>)}
            </select>
          </label>
          <label>
            <span>Label</span>
            <input value={accountForm.account_label} onChange={(event) => updateAccount('account_label', event.target.value)} placeholder="Main YouTube / Drive Account" />
          </label>
          <div className="wide-field">
            <span className="field-label">Connection ceremony</span>
            <small className="form-note">Subject, email, scopes, and refresh token are accepted only from the verified OAuth callback. Manual refresh token entry is disabled.</small>
          </div>
        </div>
        <div className="actions">
          <button className="command-btn" type="button" disabled={!accountID} onClick={saveAccount}>Update Label</button>
          <button className="secondary-btn" type="button" onClick={startOAuthAccountConnection}>Connect with OAuth</button>
          <button className="danger-btn" type="button" disabled={!accountID} onClick={() => remove('account', accountID)}>Delete Account</button>
        </div>
      </section>
      <DataTable title="OAuth Connected Accounts" data={accounts} columns={accountColumns} />

      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{destinationID ? 'Edit Google Drive Destination' : 'Create Google Drive Destination'}</h3>
            <span>Folder IDs, including shared drive folder IDs, are encrypted and sent to Encoder/Recorder only at dispatch time.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={resetDestination}>New Destination</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing destination</span>
            <select value={destinationID} onChange={(event) => setDestinationID(event.target.value)}>
              <option value="">Create new</option>
              {destinations.data.map((destination) => <option key={destination.id} value={destination.id}>{destination.name} / {destination.auth_mode}</option>)}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input value={destinationForm.name} onChange={(event) => updateDestination('name', event.target.value)} placeholder="Main Shared Drive Archive" />
          </label>
          <label>
            <span>Auth mode</span>
            <select value={destinationForm.auth_mode} onChange={(event) => updateDestination('auth_mode', event.target.value)}>
              <option value="oauth2">OAuth connected account</option>
              <option value="service_account">Service Account</option>
            </select>
          </label>
          <label>
            <span>OAuth account</span>
            <select value={destinationForm.oauth_account_id} disabled={destinationForm.auth_mode !== 'oauth2'} onChange={(event) => updateDestination('oauth_account_id', event.target.value)}>
              <option value="">Select account</option>
              {accounts.data.map((account) => <option key={account.id} value={account.id}>{account.account_label} / {account.email || account.subject}</option>)}
            </select>
          </label>
          <label>
            <span>Folder ID</span>
            <input type="password" value={destinationForm.folder_id} onChange={(event) => updateDestination('folder_id', event.target.value)} placeholder={destinationID ? 'leave blank to keep existing folder ID' : '<GOOGLE_DRIVE_FOLDER_ID>'} />
          </label>
          <label>
            <span>Base path</span>
            <input value={destinationForm.base_path} onChange={(event) => updateDestination('base_path', event.target.value)} placeholder="AutoStream" />
          </label>
          <label className="check-row">
            <input type="checkbox" checked={destinationForm.shared_drive} onChange={(event) => updateDestination('shared_drive', event.target.checked)} />
            <span>Shared drive folder</span>
          </label>
        </div>
        <div className="actions">
          <button className="command-btn" type="button" onClick={saveDestination}>{destinationID ? 'Update Destination' : 'Create Destination'}</button>
          <button className="danger-btn" type="button" disabled={!destinationID} onClick={() => remove('destination', destinationID)}>Delete Destination</button>
        </div>
      </section>
      <DataTable title="Google Drive Destinations" data={destinations} columns={destinationColumns} />
    </div>
  );
}

function UsersView({ users, roles }) {
  const [selectedID, setSelectedID] = useState('');
  const [username, setUsername] = useState('');
  const [temporaryPassword, setTemporaryPassword] = useState('');
  const [roleIDs, setRoleIDs] = useState([]);
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });
  const [oauthLinks] = useAPI(selectedID ? `/users/${selectedID}/oauth-links` : '', Boolean(selectedID));
  const selected = users.data.find((user) => user.id === selectedID);

  useEffect(() => {
    if (!selected) return;
    setUsername(selected.username || '');
    const ids = roles.data.filter((role) => selected.roles?.includes(role.name)).map((role) => role.id);
    setRoleIDs(ids);
    setTemporaryPassword('');
    setMessage({ text: '', tone: 'neutral' });
  }, [selected, roles.data]);

  const reset = () => {
    setSelectedID('');
    setUsername('');
    setTemporaryPassword('');
    setRoleIDs([]);
    setMessage({ text: '', tone: 'neutral' });
  };

  const toggleRole = (id) => {
    setRoleIDs((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
  };

  const save = async () => {
    if (!username.trim()) {
      setMessage({ text: 'Username is required.', tone: 'warning' });
      return;
    }
    if (!selectedID && !temporaryPassword) {
      setMessage({ text: 'Temporary password is required for new users.', tone: 'warning' });
      return;
    }
    const path = selectedID ? `/users/${selectedID}` : '/users';
    const body = selectedID
      ? { username: username.trim(), role_ids: roleIDs }
      : { username: username.trim(), temporary_password: temporaryPassword, role_ids: roleIDs };
    const result = await apiRequest(path, {
      method: selectedID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(body),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: selectedID ? 'User updated.' : 'User created.', tone: 'ok' });
    setTemporaryPassword('');
    await users.reload?.();
    if (!selectedID && result.body?.id) setSelectedID(result.body.id);
  };

  const setStatus = async (id, action) => {
    const result = await apiRequest(`/users/${id}/${action}`, {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: `User ${action} completed.`, tone: 'ok' });
    await users.reload?.();
  };

  const resetPassword = async () => {
    if (!selectedID) {
      setMessage({ text: 'Select a user first.', tone: 'warning' });
      return;
    }
    if (!temporaryPassword) {
      setMessage({ text: 'Temporary password is required.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/users/${selectedID}/reset-password`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ temporary_password: temporaryPassword }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setTemporaryPassword('');
    setMessage({ text: 'Temporary password set and password change forced.', tone: 'ok' });
    await users.reload?.();
  };

  const deleteOAuthLink = async (linkID) => {
    const result = await apiRequest(`/users/${selectedID}/oauth-links/${linkID}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'OAuth login link deleted.', tone: 'ok' });
    await oauthLinks.reload?.();
  };

  const columns = [
    ...userColumns,
    {
      key: 'id',
      label: 'Actions',
      render: (_, row) => (
        <div className="actions">
          <button className="link-btn" type="button" onClick={() => setSelectedID(row.id)}>Edit</button>
          <button className="secondary-btn" type="button" onClick={() => setStatus(row.id, 'unlock')}>Unlock</button>
          <button className="secondary-btn" type="button" onClick={() => setStatus(row.id, 'lock')}>Lock</button>
          <button className="danger-btn" type="button" onClick={() => setStatus(row.id, 'disable')}>Disable</button>
        </div>
      ),
    },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{selectedID ? 'Edit User' : 'Create User'}</h3>
            <span>Password hashes are never returned. Reset uses a temporary password.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={reset}>New</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing user</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Create new</option>
              {users.data.map((user) => <option key={user.id} value={user.id}>{user.username}</option>)}
            </select>
          </label>
          <label>
            <span>Username</span>
            <input value={username} onChange={(event) => setUsername(event.target.value)} />
          </label>
          <label>
            <span>{selectedID ? 'Temporary password for reset' : 'Temporary password'}</span>
            <input type="password" value={temporaryPassword} onChange={(event) => setTemporaryPassword(event.target.value)} />
          </label>
          <div className="checkbox-grid wide">
            {roles.data.map((role) => (
              <label className="check-row" key={role.id}>
                <input type="checkbox" checked={roleIDs.includes(role.id)} onChange={() => toggleRole(role.id)} />
                <span>{role.name}</span>
              </label>
            ))}
          </div>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={save}>{selectedID ? 'Update User' : 'Create User'}</button>
          <button className="secondary-btn" type="button" disabled={!selectedID} onClick={() => setStatus(selectedID, 'force-password-change')}>Force Password Change</button>
          <button className="secondary-btn" type="button" disabled={!selectedID} onClick={resetPassword}>Reset Password</button>
        </div>
      </section>
      {selectedID && (
        <section className="panel">
          <div className="panel-heading">
            <div>
              <h3>OAuth Login Links</h3>
              <span>Links are created only through the OAuth callback ceremony. Manual subject entry is disabled.</span>
            </div>
          </div>
          <Message tone="neutral" text="Use the configured Google, GitHub, or Discord OAuth login flow to link accounts. The Control Panel does not accept manually entered provider subjects." />
          <DataTable title="Linked OAuth Identities" data={oauthLinks} columns={[
            { key: 'provider_type', label: 'Provider' },
            { key: 'subject', label: 'Subject' },
            { key: 'email', label: 'Email' },
            { key: 'created_at', label: 'Created' },
            { key: 'id', label: 'Actions', render: (value) => <button className="danger-btn" type="button" onClick={() => deleteOAuthLink(value)}>Delete</button> },
          ]} />
        </section>
      )}
      <DataTable title="Users" data={users} columns={columns} />
    </div>
  );
}

function RolesView({ roles, permissions }) {
  const [selectedID, setSelectedID] = useState('');
  const [name, setName] = useState('');
  const [selectedPermissions, setSelectedPermissions] = useState([]);
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });
  const selected = roles.data.find((role) => role.id === selectedID);

  useEffect(() => {
    if (!selected) return;
    setName(selected.name || '');
    setSelectedPermissions(selected.permissions || []);
    setMessage({ text: '', tone: 'neutral' });
  }, [selected]);

  const reset = () => {
    setSelectedID('');
    setName('');
    setSelectedPermissions([]);
    setMessage({ text: '', tone: 'neutral' });
  };

  const togglePermission = (permission) => {
    setSelectedPermissions((current) => current.includes(permission) ? current.filter((item) => item !== permission) : [...current, permission]);
  };

  const save = async () => {
    if (!name.trim()) {
      setMessage({ text: 'Role name is required.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(selectedID ? `/roles/${selectedID}` : '/roles', {
      method: selectedID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ name: name.trim(), permissions: selectedPermissions }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: selectedID ? 'Role updated.' : 'Role created.', tone: 'ok' });
    await roles.reload?.();
    if (!selectedID && result.body?.id) setSelectedID(result.body.id);
  };

  const remove = async () => {
    if (!selectedID) {
      setMessage({ text: 'Select a role first.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/roles/${selectedID}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await roles.reload?.();
    reset();
    setMessage({ text: 'Role deleted.', tone: 'ok' });
  };

  const columns = [
    ...roleColumns,
    { key: 'id', label: 'Edit', render: (_, row) => <button className="link-btn" type="button" onClick={() => setSelectedID(row.id)}>Edit</button> },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{selectedID ? 'Edit Role' : 'Create Role'}</h3>
            <span>Permissions are enforced server-side and fail closed.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={reset}>New</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing role</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Create new</option>
              {roles.data.map((role) => <option key={role.id} value={role.id}>{role.name}</option>)}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <div className="checkbox-grid wide">
            {permissions.data.map((permission) => (
              <label className="check-row" key={permission}>
                <input type="checkbox" checked={selectedPermissions.includes(permission)} onChange={() => togglePermission(permission)} />
                <span>{permission}</span>
              </label>
            ))}
          </div>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={save}>{selectedID ? 'Update Role' : 'Create Role'}</button>
          <button className="danger-btn" type="button" disabled={!selectedID} onClick={remove}>Delete Role</button>
        </div>
      </section>
      <DataTable title="Roles" data={roles} columns={columns} />
    </div>
  );
}

function WorkersView({ workers, streams }) {
  const [workerID, setWorkerID] = useState('');
  const [streamID, setStreamID] = useState('');
  const [assignmentRole, setAssignmentRole] = useState('primary');
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });

  const assign = async (id = workerID, role = assignmentRole) => {
    if (!id || !streamID) {
      setMessage({ text: 'Select a worker and stream.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/workers/${id}/assign`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ stream_id: streamID, assignment_role: role }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: `Worker assigned as ${role}.`, tone: 'ok' });
    setAssignmentRole(role);
    await workers.reload?.();
  };

  const unassign = async (id = workerID) => {
    if (!id) {
      setMessage({ text: 'Select a worker first.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/workers/${id}/assignment`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Worker unassigned.', tone: 'ok' });
    await workers.reload?.();
  };

  const restart = async (id) => {
    const result = await apiRequest(`/workers/${id}/restart`, {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Worker restart requested.', tone: 'ok' });
    await workers.reload?.();
  };

  const columns = [
    ...serviceColumns,
    {
      key: 'service_id',
      label: 'Actions',
      render: (value) => (
        <div className="actions">
          <button className="link-btn" type="button" onClick={() => { setWorkerID(value); assign(value, assignmentRole); }}>Assign Selected Stream</button>
          <button className="secondary-btn" type="button" onClick={() => { setWorkerID(value); unassign(value); }}>Unassign</button>
          <button className="secondary-btn" type="button" onClick={() => restart(value)}>Restart</button>
        </div>
      ),
    },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Worker Assignment</h3>
            <span>Assign a primary Worker for dispatch, or standby Workers as failover candidates.</span>
          </div>
        </div>
        <div className="form-grid">
          <label>
            <span>Worker</span>
            <select value={workerID} onChange={(event) => setWorkerID(event.target.value)}>
              <option value="">Select worker</option>
              {workers.data.map((worker) => <option key={worker.service_id} value={worker.service_id}>{worker.service_name || worker.service_id}</option>)}
            </select>
          </label>
          <label>
            <span>Stream</span>
            <select value={streamID} onChange={(event) => setStreamID(event.target.value)}>
              <option value="">Select stream</option>
              {streams.data.map((stream) => <option key={stream.id} value={stream.id}>{stream.name} ({stream.status})</option>)}
            </select>
          </label>
          <label>
            <span>Assignment role</span>
            <select value={assignmentRole} onChange={(event) => setAssignmentRole(event.target.value)}>
              <option value="primary">primary - dispatch target</option>
              <option value="standby">standby - failover candidate</option>
            </select>
          </label>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={() => assign()}>Assign Worker as {assignmentRole}</button>
          <button className="secondary-btn" type="button" disabled={!workerID} onClick={() => unassign()}>Unassign Worker</button>
          <button className="secondary-btn" type="button" disabled={!workerID} onClick={() => restart(workerID)}>Restart Worker</button>
        </div>
      </section>
      <DataTable title="Workers" data={workers} columns={columns} />
    </div>
  );
}

function ServiceHealthView({ services, streams, onOpenAudit, onOpenStreamOperations, initialFocus }) {
  const [serviceID, setServiceID] = useState('');
  const [streamID, setStreamID] = useState('');
  const [assignmentRole, setAssignmentRole] = useState('primary');
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });
  const [runtimePreview] = useAPI(serviceID ? `/service-health/${encodeURIComponent(serviceID)}/runtime-config` : '', Boolean(serviceID), 'object');
  const assignableServices = services.data.filter((service) => service.service_type !== 'observability');
  const selectedService = assignableServices.find((service) => service.service_id === serviceID);
  const selectedServiceHealth = serviceHealthState(selectedService);
  const selectedStream = streams.data.find((stream) => stream.id === streamID);
  const selectedStreamAssignment = streamAssignmentStatus(streamID, services.data);
  const healthSummary = services.data.reduce((summary, service) => {
    const health = serviceHealthState(service);
    if (health.label.startsWith('offline')) summary.offline += 1;
    else if (health.stale) summary.stale += 1;
    else summary.healthy += 1;
    return summary;
  }, { healthy: 0, stale: 0, offline: 0 });
  const replacingService = selectedService && streamID && assignmentRole === 'primary'
    ? services.data.find((service) => (
      service.current_stream_id === streamID
      && service.service_type === selectedService.service_type
      && (!service.assignment_role || service.assignment_role === 'primary')
      && service.service_id !== selectedService.service_id
    ))
    : null;
  const movingFromStream = selectedService?.current_stream_id && selectedService.current_stream_id !== streamID
    ? streams.data.find((stream) => stream.id === selectedService.current_stream_id)
    : null;
  const streamLabel = (id) => {
    if (!id) return '-';
    const stream = streams.data.find((item) => item.id === id);
    return stream ? `${stream.name} (${stream.status})` : id;
  };

  useEffect(() => {
    if (!initialFocus?.nonce) return;
    if (initialFocus.streamID) setStreamID(initialFocus.streamID);
    if (initialFocus.serviceID) setServiceID(initialFocus.serviceID);
  }, [initialFocus?.nonce, initialFocus?.serviceID, initialFocus?.streamID]);

  const assign = async (id = serviceID, role = assignmentRole) => {
    if (!id || !streamID) {
      setMessage({ text: 'Select a service and stream.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/services/${id}/assign`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ stream_id: streamID, assignment_role: role }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: `Service assigned as ${role}. Open Stream Operations and run Check Readiness again.`, tone: 'ok' });
    await services.reload?.();
  };

  const unassign = async (id = serviceID) => {
    if (!id) {
      setMessage({ text: 'Select a service first.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/services/${id}/assignment`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Service unassigned. Open Stream Operations and run Check Readiness again if this stream will be started.', tone: 'ok' });
    await services.reload?.();
  };

  const deleteService = async (id = serviceID) => {
    if (!id) {
      setMessage({ text: 'Select a service first.', tone: 'warning' });
      return;
    }
    const service = services.data.find((item) => item.service_id === id);
    const label = service?.service_name || id;
    const confirmed = window.confirm(`Delete service registry entry for "${label}"?\n\nThis removes assignments, deletes service stream events, and revokes the linked service token. Use this for dry-run cleanup or retired services only.`);
    if (!confirmed) return;
    const result = await apiRequest(`/services/${id}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Service registry entry deleted and linked token revoked.', tone: 'ok' });
    setServiceID('');
    await services.reload?.();
  };

  const columns = [
    ...serviceColumns.map((column) => column.key === 'current_stream_id'
      ? { ...column, render: (value) => streamLabel(value) }
      : column),
    {
      key: 'service_id',
      label: 'Actions',
      render: (value, row) => (
        <div className="actions">
          {row.service_type !== 'observability' && (
            <>
              <button className="link-btn" type="button" onClick={() => { setServiceID(value); assign(value, assignmentRole); }}>Assign Selected Stream</button>
              <button className="secondary-btn" type="button" disabled={!row.current_stream_id} onClick={() => { setServiceID(value); unassign(value); }}>Unassign</button>
            </>
          )}
          <button className="secondary-btn" type="button" onClick={() => onOpenAudit?.({ actionGroup: 'service_assignment', query: value })}>Audit</button>
          <button className="danger-btn" type="button" onClick={() => { setServiceID(value); deleteService(value); }}>Delete Registry</button>
        </div>
      ),
    },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Service Assignment</h3>
            <span>Assignments are unique per service type. Assigning a service can move it from another stream or replace the current service of the same type.</span>
          </div>
        </div>
        <div className="form-grid">
          <label>
            <span>Service</span>
            <select value={serviceID} onChange={(event) => setServiceID(event.target.value)}>
              <option value="">Select service</option>
              {assignableServices.map((service) => (
                <option key={service.service_id} value={service.service_id}>
                  {service.service_name || service.service_id} ({service.service_type})
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>Stream</span>
            <select value={streamID} onChange={(event) => setStreamID(event.target.value)}>
              <option value="">Select stream</option>
              {streams.data.map((stream) => <option key={stream.id} value={stream.id}>{stream.name} ({stream.status})</option>)}
            </select>
          </label>
          <label>
            <span>Assignment role</span>
            <select value={assignmentRole} onChange={(event) => setAssignmentRole(event.target.value)}>
              <option value="primary">primary - dispatch target</option>
              <option value="standby">standby - failover candidate</option>
            </select>
          </label>
        </div>
        <div className="health-summary">
          <span className="health-card ok">Healthy: {healthSummary.healthy}</span>
          <span className="health-card warning">Stale: {healthSummary.stale}</span>
          <span className="health-card critical">Offline: {healthSummary.offline}</span>
        </div>
        <StreamAssignmentPlanner
          stream={selectedStream}
          assignment={selectedStreamAssignment}
          services={assignableServices}
          streamLabel={streamLabel}
          onAssign={(id, role = 'primary') => {
            setServiceID(id);
            setAssignmentRole(role);
            assign(id, role);
          }}
        />
        {selectedService && selectedServiceHealth.stale && (
          <Message text={`Selected service health is ${selectedServiceHealth.label}. Confirm the host before assignment or dispatch.`} tone={selectedServiceHealth.tone === 'critical' ? 'critical' : 'warning'} />
        )}
        {streamID && (
          <div className="assignment-preview">
            <div>
              <strong>Selected stream assignments</strong>
              <span>{selectedStream ? selectedStream.name : streamID}</span>
            </div>
            <div className="assignment-pills">
              {requiredStreamServiceTypes.map((type) => {
                const service = selectedStreamAssignment.assigned.find((item) => item.service_type === type);
                return (
                  <span className={`assignment-pill ${service ? (serviceHealthState(service).stale ? 'stale' : 'ok') : 'missing'}`} key={type}>
                    primary {type}: {service?.service_name || service?.service_id || 'missing'}
                  </span>
                );
              })}
            </div>
            {selectedStreamAssignment.standby.length > 0 && (
              <div className="assignment-pills">
                {selectedStreamAssignment.standby.map((service) => (
                  <span className={`assignment-pill ${serviceHealthState(service).stale ? 'stale' : 'ok'}`} key={service.service_id}>
                    standby {service.service_type}: {service.service_name || service.service_id}
                  </span>
                ))}
              </div>
            )}
          </div>
        )}
        <RuntimeConfigPreview service={selectedService} preview={runtimePreview} />
        {(replacingService || movingFromStream) && (
          <div className="assignment-impact">
            <strong>Assignment impact</strong>
            {replacingService && <span>{replacingService.service_name || replacingService.service_id} will be unassigned from this stream.</span>}
            {movingFromStream && <span>{selectedService.service_name || selectedService.service_id} will move from {movingFromStream.name}.</span>}
          </div>
        )}
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={() => assign()}>Assign as {assignmentRole}</button>
          <button className="secondary-btn" type="button" disabled={!serviceID} onClick={() => unassign()}>Unassign Service</button>
          <button className="secondary-btn" type="button" disabled={!streamID} onClick={() => onOpenStreamOperations?.({ streamID })}>Open Stream Operations</button>
          <button className="secondary-btn" type="button" disabled={!streamID} onClick={() => onOpenAudit?.({ actionGroup: 'service_assignment', query: streamID })}>View Stream Assignment Audit</button>
          <button className="secondary-btn" type="button" disabled={!serviceID} onClick={() => onOpenAudit?.({ actionGroup: 'service_assignment', query: serviceID })}>View Service Audit</button>
          <button className="danger-btn" type="button" disabled={!serviceID} onClick={() => deleteService()}>Delete Service Registry</button>
        </div>
      </section>
      <DataTable title="Service Health" data={services} columns={columns} />
    </div>
  );
}

function RuntimeConfigPreview({ service, preview }) {
  if (!service) {
    return (
      <div className="runtime-config-preview neutral">
        <div className="runtime-preview-heading">
          <div>
            <strong>Runtime config preview</strong>
            <span>Select a service to inspect its effective Control Panel-distributed config.</span>
          </div>
        </div>
      </div>
    );
  }
  if (preview.loading) {
    return <Message text="Loading runtime config preview..." />;
  }
  if (preview.error) {
    return <Message text={`Runtime config preview unavailable: ${preview.error}`} tone="warning" />;
  }
  const data = preview.data || {};
  const assignments = Array.isArray(data.assignments) ? data.assignments : [];
  const profiles = data.profiles && typeof data.profiles === 'object' ? data.profiles : {};
  const profileCounts = Object.entries(profiles)
    .map(([kind, items]) => `${kind}: ${Array.isArray(items) ? items.length : 0}`)
    .join(', ') || 'none';
  const discordConfigs = Array.isArray(data.stream_discord_configs) ? data.stream_discord_configs : [];
  const archiveConfigs = Array.isArray(data.stream_archive_configs) ? data.stream_archive_configs : [];
  const youtubeConfigs = Array.isArray(data.stream_youtube_configs) ? data.stream_youtube_configs : [];
  const streamConfigCount = discordConfigs.length + archiveConfigs.length + youtubeConfigs.length;
  const tone = assignments.length > 0 ? 'ok' : 'warning';
  const previewSummary = {
    assignments,
    profile_counts: profileCounts,
    stream_discord_configs: discordConfigs,
    stream_archive_configs: archiveConfigs,
    stream_youtube_configs: youtubeConfigs,
  };
  return (
    <div className={`runtime-config-preview ${tone}`}>
      <div className="runtime-preview-heading">
        <div>
          <strong>Runtime config preview</strong>
          <span>Effective no-store config for {service.service_name || service.service_id}. Secret values remain represented by configured status, fingerprints, or secret reference names.</span>
        </div>
        <button className="secondary-btn" type="button" onClick={() => preview.reload?.()}>Refresh Preview</button>
      </div>
      <div className="runtime-preview-grid">
        <article>
          <span>Assignments</span>
          <strong>{assignments.length}</strong>
          <small>{assignments.map((item) => `${item.stream_id}:${item.assignment_role || 'primary'}`).join(', ') || 'none'}</small>
        </article>
        <article>
          <span>Profiles</span>
          <strong>{Object.keys(profiles).length}</strong>
          <small>{profileCounts}</small>
        </article>
        <article>
          <span>Stream configs</span>
          <strong>{streamConfigCount}</strong>
          <small>Discord {discordConfigs.length}, Archive {archiveConfigs.length}, YouTube {youtubeConfigs.length}</small>
        </article>
      </div>
      <pre>{safeJSON(previewSummary)}</pre>
    </div>
  );
}

function StreamAssignmentPlanner({ stream, assignment, services, streamLabel, onAssign }) {
  if (!stream) {
    return (
      <div className="assignment-planner neutral">
        <strong>Stream assignment planner</strong>
        <span>Select a stream to inspect required service assignments.</span>
      </div>
    );
  }
  const missing = assignment.missing || [];
  const stale = assignment.assigned.filter((service) => serviceHealthState(service).stale);
  const tone = missing.length > 0 ? 'critical' : stale.length > 0 ? 'warning' : 'ok';
  return (
    <div className={`assignment-planner ${tone}`}>
      <div className="assignment-planner-heading">
        <div>
          <strong>Stream assignment planner</strong>
          <span>{stream.name}: {missing.length === 0 ? 'all required service types are assigned' : `missing ${missing.join(', ')}`}</span>
        </div>
        <Badge tone={tone === 'critical' ? 'critical' : tone === 'warning' ? 'warning' : 'ok'}>{tone === 'critical' ? 'not ready' : tone === 'warning' ? 'attention' : 'ready'}</Badge>
      </div>
      <div className="assignment-type-grid">
        {requiredStreamServiceTypes.map((type) => {
          const current = assignment.assigned.find((service) => service.service_type === type);
          const standby = assignment.standby.filter((service) => service.service_type === type);
          const candidates = services.filter((service) => service.service_type === type);
          const currentHealth = serviceHealthState(current);
          return (
            <div className={`assignment-type-row ${current ? (currentHealth.stale ? 'warning' : 'ok') : 'critical'}`} key={type}>
              <div>
                <strong>{type}</strong>
                <span>{current ? `${current.service_name || current.service_id} / ${currentHealth.label}` : 'missing'}</span>
                {standby.length > 0 && (
                  <small>standby: {standby.map((service) => service.service_name || service.service_id).join(', ')}</small>
                )}
              </div>
              <div className="assignment-candidates">
                {candidates.length === 0 ? (
                  <span className="muted">No registered candidate.</span>
                ) : candidates.map((service) => {
                  const health = serviceHealthState(service);
                  const selected = current?.service_id === service.service_id;
                  const standbySelected = standby.some((item) => item.service_id === service.service_id);
                  const disabled = !stream?.id;
                  const moveLabel = service.current_stream_id && service.current_stream_id !== stream.id ? `from ${streamLabel(service.current_stream_id)}` : 'assign';
                  return (
                    <span className="assignment-candidate-actions" key={service.service_id}>
                      <button
                        className={selected ? 'secondary-btn' : 'link-btn'}
                        disabled={disabled || selected}
                        onClick={() => onAssign(service.service_id, 'primary')}
                        title={`${service.service_name || service.service_id} / ${health.label}`}
                        type="button"
                      >
                        {service.service_name || service.service_id} ({health.label}) {selected ? 'primary' : moveLabel}
                      </button>
                      <button
                        className={standbySelected ? 'secondary-btn' : 'link-btn'}
                        disabled={disabled || standbySelected || selected}
                        onClick={() => onAssign(service.service_id, 'standby')}
                        title={`${service.service_name || service.service_id} / ${health.label}`}
                        type="button"
                      >
                        {standbySelected ? 'standby' : 'as standby'}
                      </button>
                    </span>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

const auditActionGroups = {
  all: [],
  service_assignment: ['services.assign', 'services.unassign', 'services.delete', 'workers.assign', 'workers.unassign'],
  service_runtime: ['services.register', 'services.heartbeat', 'archive.artifacts.reported'],
  stream_lifecycle: ['streams.create', 'streams.start', 'streams.stop', 'streams.mark_failed', 'streams.retry_upload'],
  security: ['auth.login', 'auth.logout', 'auth.change_password', 'users.create', 'users.update', 'users.disable', 'users.lock', 'users.unlock', 'users.reset_password', 'users.force_password_change', 'roles.create', 'roles.update', 'roles.delete'],
  secrets: ['secrets.update', 'security.settings.update', 'api_tokens.create', 'api_tokens.revoke'],
  notifications: ['notification_channels.create', 'notification_channels.update', 'notification_channels.delete', 'notification_channels.test'],
};

function AuditLogsView({ data, initialFilter }) {
  const [message, setMessage] = useState('');
  const [actionGroup, setActionGroup] = useState('service_assignment');
  const [resultFilter, setResultFilter] = useState('all');
  const [query, setQuery] = useState('');
  useEffect(() => {
    if (!initialFilter) return;
    setActionGroup(initialFilter.actionGroup || 'all');
    setResultFilter(initialFilter.result || 'all');
    setQuery(initialFilter.query || '');
  }, [initialFilter?.nonce]);
  const auditParams = useMemo(() => {
    const params = new URLSearchParams({ limit: '100' });
    if (actionGroup !== 'all') params.set('action_group', actionGroup);
    if (resultFilter !== 'all') params.set('result', resultFilter);
    if (query.trim()) params.set('q', query.trim());
    return params.toString();
  }, [actionGroup, query, resultFilter]);
  const [filteredAudit, setFilteredAudit] = useState({ loading: true, error: '', data: [] });
  useEffect(() => {
    let cancelled = false;
    setFilteredAudit((current) => ({ ...current, loading: true, error: '' }));
    apiRequest(`/audit-logs?${auditParams}`).then((result) => {
      if (cancelled) return;
      if (!result.ok) {
        setFilteredAudit({ loading: false, error: result.message, data: [] });
        return;
      }
      setFilteredAudit({ loading: false, error: '', data: Array.isArray(result.body) ? result.body : [] });
    });
    return () => { cancelled = true; };
  }, [auditParams]);
  const exportCSV = async () => {
    try {
      const exportParams = new URLSearchParams(auditParams);
      exportParams.set('limit', '500');
      const response = await fetch(`/audit-logs/export?${exportParams.toString()}`, { credentials: 'same-origin', headers: { Accept: 'text/csv' } });
      if (!response.ok) {
        setMessage(`Export failed: ${response.status}`);
        return;
      }
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `autostream-audit-logs-${new Date().toISOString().slice(0, 10)}.csv`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      setMessage('Audit log export started.');
    } catch {
      setMessage('Audit log export failed.');
    }
  };
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Audit Export</h3>
            <span>CSV export excludes secret values and password hashes.</span>
          </div>
          <button className="command-btn" type="button" onClick={exportCSV}>Export CSV</button>
        </div>
        {message && <Message text={message} tone="ok" />}
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Audit Filters</h3>
            <span>Service assignment actions are selected by default so assignment changes are easy to inspect.</span>
          </div>
        </div>
        <div className="form-grid">
          <label>
            <span>Action group</span>
            <select value={actionGroup} onChange={(event) => setActionGroup(event.target.value)}>
              <option value="service_assignment">Service assignment</option>
              <option value="service_runtime">Service runtime</option>
              <option value="stream_lifecycle">Stream lifecycle</option>
              <option value="security">Security / users / roles</option>
              <option value="secrets">Secrets / tokens / settings</option>
              <option value="notifications">Notification channels</option>
              <option value="all">All actions</option>
            </select>
          </label>
          <label>
            <span>Result</span>
            <select value={resultFilter} onChange={(event) => setResultFilter(event.target.value)}>
              <option value="all">All results</option>
              <option value="success">success</option>
              <option value="failure">failure</option>
            </select>
          </label>
          <label>
            <span>Search</span>
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="service id, stream id, action, actor" />
          </label>
        </div>
        <div className="filter-summary">
          Showing {filteredAudit.data.length} filtered audit events from the server. Recent loaded events: {data.data.length}.
        </div>
      </section>
      <DataTable title="Audit Logs" data={filteredAudit} columns={auditColumns} />
    </div>
  );
}

const serviceTypes = ['discord_bot', 'encoder_recorder', 'worker', 'observability'];

const serviceScopes = [
  'service.register',
  'service.heartbeat',
  'service.logs.write',
  'service.status.write',
  'service.config.read',
  'service.secret.resolve',
  'worker.events.write',
  'encoder.status.write',
  'discord.status.write',
  'observability.ingest',
];

const defaultScopesByServiceType = {
  discord_bot: ['service.register', 'service.heartbeat', 'service.config.read', 'service.secret.resolve', 'service.logs.write', 'service.status.write', 'discord.status.write'],
  encoder_recorder: ['service.register', 'service.heartbeat', 'service.config.read', 'service.secret.resolve', 'service.logs.write', 'service.status.write', 'encoder.status.write'],
  worker: ['service.register', 'service.heartbeat', 'service.config.read', 'service.secret.resolve', 'service.logs.write', 'service.status.write', 'worker.events.write'],
  observability: ['service.register', 'service.heartbeat', 'service.config.read', 'service.secret.resolve', 'service.logs.write', 'service.status.write', 'observability.ingest'],
};

const defaultCapabilitiesByServiceType = {
  discord_bot: 'discord_voice,discord_audio_forward,runtime_config',
  encoder_recorder: 'rtmps_output,archive_package,gdrive_upload,discord_audio_ingest,runtime_config',
  worker: 'overlay_events,caption_events,participant_state,runtime_config',
  observability: 'signal_ingest,diagnostics,remediation,notifications',
};

function ApiTokensView({ data }) {
  const [serviceType, setServiceType] = useState('worker');
  const [scopes, setScopes] = useState(defaultScopesByServiceType.worker);
  const [precreate, setPrecreate] = useState({
    service_id: '',
    service_name: '',
    public_url: '',
    version: '0.1.0',
    capabilities: defaultCapabilitiesByServiceType.worker,
  });
  const [createdToken, setCreatedToken] = useState('');
  const [createdBootstrap, setCreatedBootstrap] = useState('');
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });

  const changeServiceType = (value) => {
    setServiceType(value);
    setScopes(defaultScopesByServiceType[value] || ['service.register', 'service.heartbeat']);
    setPrecreate((current) => ({
      ...current,
      service_id: current.service_id || `${value.replaceAll('_', '-')}-01`,
      service_name: current.service_name || serviceTypeLabel(value),
      capabilities: defaultCapabilitiesByServiceType[value] || '',
    }));
    setCreatedToken('');
    setCreatedBootstrap('');
    setMessage({ text: '', tone: 'neutral' });
  };

  const toggleScope = (scope) => {
    setScopes((current) => current.includes(scope) ? current.filter((item) => item !== scope) : [...current, scope]);
  };

  const updatePrecreate = (key, value) => {
    setPrecreate((current) => ({ ...current, [key]: value }));
  };

  const createToken = async () => {
    setCreatedToken('');
    setCreatedBootstrap('');
    if (!scopes.length) {
      setMessage({ text: 'At least one scope is required.', tone: 'warning' });
      return;
    }
    const precreateRequested = Boolean(precreate.service_id.trim());
    if (precreateRequested && !scopes.includes('service.register')) {
      setMessage({ text: 'Pre-created services require service.register scope.', tone: 'warning' });
      return;
    }
    const body = { service_type: serviceType, scopes };
    if (precreateRequested) {
      body.service_id = precreate.service_id.trim();
      body.service_name = precreate.service_name.trim();
      body.public_url = precreate.public_url.trim();
      body.version = precreate.version.trim();
      body.capabilities = parseCapabilityFlags(precreate.capabilities);
    }
    const result = await apiRequest('/api-tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(body),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setCreatedToken(result.body?.token || '');
    if (precreateRequested && result.body?.token) {
      setCreatedBootstrap(serviceBootstrapEnv({
        serviceID: body.service_id,
        serviceName: body.service_name || serviceTypeLabel(serviceType),
        publicURL: body.public_url,
        token: result.body.token,
      }));
    }
    setMessage({ text: precreateRequested ? 'Created token and pending service. Copy the token now; it will not be shown again.' : 'Created. Copy the token now; it will not be shown again.', tone: 'ok' });
    await data.reload?.();
  };

  const revokeToken = async (id) => {
    const result = await apiRequest(`/api-tokens/${id}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Token revoked.', tone: 'ok' });
    await data.reload?.();
  };

  const rotateToken = async (id) => {
    setCreatedToken('');
    setCreatedBootstrap('');
    const result = await apiRequest(`/api-tokens/${id}/rotate`, {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setCreatedToken(result.body?.token || '');
    setMessage({ text: 'Rotated token. Copy the new token now; it will not be shown again.', tone: 'ok' });
    await data.reload?.();
  };

  const columns = [
    ...tokenColumns,
    {
      key: 'id',
      label: 'Action',
      render: (_, row) => (
        <div className="row-actions">
          <button className="command-btn" type="button" disabled={Boolean(row.revoked_at)} onClick={() => rotateToken(row.id)}>
            Rotate
          </button>
          <button className="danger-btn" type="button" disabled={Boolean(row.revoked_at)} onClick={() => revokeToken(row.id)}>
            Revoke
          </button>
        </div>
      ),
    },
  ];

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Create Service Token</h3>
            <span>Pre-create a service registry entry, then copy the one-time token into that service host bootstrap env.</span>
          </div>
        </div>
        <div className="form-grid">
          <label>
            <span>Service type</span>
            <select value={serviceType} onChange={(event) => changeServiceType(event.target.value)}>
              {serviceTypes.map((item) => <option key={item} value={item}>{item}</option>)}
            </select>
          </label>
          <div className="checkbox-grid wide">
            {serviceScopes.map((scope) => (
              <label className="check-row" key={scope}>
                <input type="checkbox" checked={scopes.includes(scope)} onChange={() => toggleScope(scope)} />
                <span>{scope}</span>
              </label>
            ))}
          </div>
          <label>
            <span>Pre-create service ID</span>
            <input value={precreate.service_id} onChange={(event) => updatePrecreate('service_id', event.target.value)} placeholder="encoder-recorder-01" />
          </label>
          <label>
            <span>Service name</span>
            <input value={precreate.service_name} onChange={(event) => updatePrecreate('service_name', event.target.value)} placeholder="Encoder Recorder 01" />
          </label>
          <label>
            <span>Public URL</span>
            <input value={precreate.public_url} onChange={(event) => updatePrecreate('public_url', event.target.value)} placeholder="https://encoder.example.com" />
          </label>
          <label>
            <span>Version</span>
            <input value={precreate.version} onChange={(event) => updatePrecreate('version', event.target.value)} placeholder="0.1.0" />
          </label>
          <label className="wide">
            <span>Capabilities</span>
            <input value={precreate.capabilities} onChange={(event) => updatePrecreate('capabilities', event.target.value)} placeholder="rtmps_output,archive_package,gdrive_upload" />
          </label>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        {createdToken && (
          <div className="token-once">
            <span>One-time token</span>
            <code>{createdToken}</code>
            {createdBootstrap && (
              <>
                <span>Bootstrap env for the service host</span>
                <pre>{createdBootstrap}</pre>
              </>
            )}
          </div>
        )}
        <div className="actions">
          <button className="command-btn" type="button" onClick={createToken}>Create Token</button>
        </div>
      </section>
      <DataTable title="API Tokens" data={data} columns={columns} />
    </div>
  );
}

function serviceTypeLabel(serviceType) {
  return serviceType.split('_').map((part) => part.charAt(0).toUpperCase() + part.slice(1)).join(' ');
}

function serviceBootstrapEnv({ serviceID, serviceName, publicURL, token }) {
  return [
    `SERVICE_ID=${serviceID}`,
    `SERVICE_NAME=${serviceName || serviceID}`,
    `SERVICE_PUBLIC_URL=${publicURL || 'https://service.example.com'}`,
    'CONTROL_PANEL_URL=https://control.example.com',
    `CONTROL_PANEL_TOKEN=${token}`,
    'TZ=Asia/Tokyo',
  ].join('\n');
}

function parseCapabilityFlags(value) {
  return value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean).reduce((acc, key) => {
    acc[key] = true;
    return acc;
  }, {});
}

function Dashboard({ streams, services, workers, incidents, remediation, auditLogs, me, metrics }) {
  const activeStream = streams.data.find((stream) => stream.status === 'live' || stream.status === 'starting');
  const onlineServices = services.data.filter((service) => service.status === 'online').length;
  const openIncidents = incidents.data.filter((incident) => incident.status !== 'resolved' && incident.status !== 'ignored');
  const pendingActions = remediation.data.filter((action) => action.status === 'pending_approval' || action.status === 'suggested');
  const username = me.data?.user?.username || 'Unknown';
  return (
    <>
      <div className="grid">
        <Status icon={<Radio />} label="Active Stream" value={activeStream ? `${activeStream.name} (${activeStream.status})` : 'No active stream'} tone={activeStream ? 'ok' : 'neutral'} />
        <Status icon={<Server />} label="Services" value={`${onlineServices}/${services.data.length} online`} tone={onlineServices ? 'ok' : 'warning'} />
        <Status icon={<MonitorDot />} label="Workers" value={`${workers.data.length} registered`} />
        <Status icon={<ShieldCheck />} label="Current User" value={username} />
        <Status icon={<AlertTriangle />} label="Open Incidents" value={openIncidents.length.toString()} tone={openIncidents.length ? 'critical' : 'ok'} />
        <Status icon={<Wrench />} label="Pending Remediation" value={pendingActions.length.toString()} tone={pendingActions.length ? 'warning' : 'ok'} />
      </div>
      <EncoderMetricSummary metrics={metrics} compact />
      <AudioMetricSummary metrics={metrics} compact />
      <WorkerMetricSummary metrics={metrics} compact />
      <ArchiveMetricSummary metrics={metrics} compact />
      <Incidents data={incidents} compact />
      <DataTable title="Recent Audit Events" data={{ ...auditLogs, data: auditLogs.data.slice(0, 5) }} columns={auditColumns} compact />
    </>
  );
}

function Monitoring({ incidents, remediation, deliveries, metrics }) {
  return (
    <div className="stack">
      <div className="grid">
        <Status icon={<AlertTriangle />} label="Incidents" value={String(incidents.data.length)} />
        <Status icon={<Wrench />} label="Remediation Actions" value={String(remediation.data.length)} />
        <Status icon={<Bell />} label="Notification Deliveries" value={String(deliveries.data.length)} />
        <Status icon={<Gauge />} label="Metric Snapshots" value={String(metrics.data.length)} />
      </div>
      <EncoderMetricSummary metrics={metrics} />
      <AudioMetricSummary metrics={metrics} />
      <WorkerMetricSummary metrics={metrics} />
      <ArchiveMetricSummary metrics={metrics} />
      <Incidents data={incidents} compact />
      <Remediation data={remediation} />
    </div>
  );
}

function EncoderMetricSummary({ metrics, compact = false }) {
  const { t } = useI18n();
  if (metrics.loading) return <Message text="Loading encoder metrics..." />;
  if (metrics.error) return <Message text={metrics.error} tone="warning" />;
  const latest = latestMetrics(metrics.data);
  const cards = [
    metricCard(latest, 'encoder.process_alive', 'Encoder Process', (value) => value >= 1 ? 'alive' : 'stopped', (value) => value >= 1 ? 'ok' : 'critical'),
    metricCard(latest, 'encoder.output_fps', 'Output FPS', (value) => formatNumber(value, 1), (value) => value >= 50 ? 'ok' : value > 0 ? 'warning' : 'neutral'),
    metricCard(latest, 'encoder.output_bitrate_kbps', 'Output Bitrate', (value) => `${formatNumber(value, 0)} kbps`, (value) => value >= 6000 ? 'ok' : value >= 3000 ? 'warning' : 'critical'),
    metricCard(latest, 'encoder.dropped_frames_total', 'Dropped Frames', (value) => formatNumber(value, 0), (value) => value >= 30 ? 'warning' : 'ok'),
    metricCard(latest, 'recorder.write_bitrate_kbps', 'Recorder Write', (value) => `${formatNumber(value, 0)} kbps`, (value) => value > 0 ? 'ok' : 'critical'),
    metricCard(latest, 'recorder.disk_free_bytes', 'Archive Disk Free', formatBytes, (value) => value > 50 * 1024 ** 3 ? 'ok' : value > 10 * 1024 ** 3 ? 'warning' : 'critical'),
  ];
  const visible = compact ? cards.slice(0, 4) : cards;
  return (
    <div className="metric-panel">
      <div className="panel-heading">
        <h3>{t('Encoder / Recorder Metrics')}</h3>
        <span>{metrics.data.length} snapshots</span>
      </div>
      <div className="metric-grid">
        {visible.map((card) => (
          <article className={`metric-card ${card.tone}`} key={card.name}>
            <span>{t(card.label)}</span>
            <strong>{localizeRendered(card.display, t)}</strong>
            <small>{card.updatedAt ? `Updated ${formatDateTime(card.updatedAt)}` : 'No data'}</small>
          </article>
        ))}
      </div>
    </div>
  );
}

function ArchiveMetricSummary({ metrics, compact = false }) {
  const { t } = useI18n();
  if (metrics.loading) return <Message text="Loading archive metrics..." />;
  if (metrics.error) return <Message text={metrics.error} tone="warning" />;
  const latest = latestMetrics(metrics.data);
  const cards = [
    metricCard(latest, 'archive.package_status', 'Package Status', formatState, metricStatusTone),
    metricCard(latest, 'archive.final_mkv_exists', 'Final MKV', formatExists, existsTone),
    metricCard(latest, 'archive.final_mp4_exists', 'Final MP4', formatExists, existsTone),
    metricCard(latest, 'recorder.remux_duration_ms', 'Remux Duration', formatDurationMillis, (value) => value > 0 ? 'ok' : 'neutral'),
    metricCard(latest, 'gdrive.upload_status', 'Google Drive Upload', formatState, metricStatusTone),
    metricCard(latest, 'gdrive.upload_retry_count', 'Upload Retries', (value) => formatNumber(value, 0), (value) => value >= 3 ? 'warning' : 'ok'),
    metricCard(latest, 'gdrive.upload_duration_sec', 'Upload Duration', formatDurationSeconds, (value) => value > 0 ? 'ok' : 'neutral'),
    metricCard(latest, 'gdrive.upload_file_count', 'Uploaded Files', (value) => formatNumber(value, 0), (value) => value >= 2 ? 'ok' : 'warning'),
    metricCard(latest, 'gdrive.upload_folder_fingerprint_present', 'Folder Proof', formatExists, existsTone),
    metricCard(latest, 'gdrive.upload_final_mp4_fingerprint_present', 'Final MP4 Proof', formatExists, existsTone),
    metricCard(latest, 'gdrive.upload_metadata_fingerprint_present', 'Metadata Proof', formatExists, existsTone),
  ];
  const visible = compact ? cards.slice(0, 4) : cards;
  return (
    <div className="metric-panel">
      <div className="panel-heading">
        <h3>{t('Archive / Google Drive Metrics')}</h3>
        <span>{metrics.data.length} snapshots</span>
      </div>
      <div className="metric-grid">
        {visible.map((card) => (
          <article className={`metric-card ${card.tone}`} key={card.name}>
            <span>{t(card.label)}</span>
            <strong>{localizeRendered(card.display, t)}</strong>
            <small>{card.updatedAt ? `Updated ${formatDateTime(card.updatedAt)}` : 'No data'}</small>
          </article>
        ))}
      </div>
    </div>
  );
}

function AudioMetricSummary({ metrics, compact = false }) {
  const { t } = useI18n();
  if (metrics.loading) return <Message text="Loading audio metrics..." />;
  if (metrics.error) return <Message text={metrics.error} tone="warning" />;
  const latest = latestMetrics(metrics.data);
  const cards = [
    metricCard(latest, 'discord.audio_receiving', 'Discord Audio', (value) => value >= 1 ? 'receiving' : 'not receiving', (value) => value >= 1 ? 'ok' : 'critical'),
    metricCard(latest, 'discord.audio_packets_total', 'Discord Packets', (value) => formatNumber(value, 0), (value) => value > 0 ? 'ok' : 'warning'),
    metricCard(latest, 'media.input_timeout_sec', 'Input Timeout', formatDurationSeconds, (value) => value >= 5 ? 'critical' : value > 0 ? 'warning' : 'ok'),
    metricCard(latest, 'encoder.audio_level_db', 'Audio Level', formatDB, (value) => value <= -50 ? 'warning' : value >= -1 ? 'critical' : 'ok'),
    metricCard(latest, 'encoder.audio_silence_sec', 'Audio Silence', formatDurationSeconds, (value) => value >= 5 ? 'warning' : 'ok'),
    metricCard(latest, 'encoder.audio_clipping_total', 'Audio Clipping', (value) => formatNumber(value, 0), (value) => value >= 10 ? 'warning' : 'ok'),
  ];
  const visible = compact ? cards.slice(0, 4) : cards;
  return (
    <div className="metric-panel">
      <div className="panel-heading">
        <h3>{t('Audio / Input Health')}</h3>
        <span>{metrics.data.length} snapshots</span>
      </div>
      <div className="metric-grid">
        {visible.map((card) => (
          <article className={`metric-card ${card.tone}`} key={card.name}>
            <span>{t(card.label)}</span>
            <strong>{localizeRendered(card.display, t)}</strong>
            <small>{card.updatedAt ? `Updated ${formatDateTime(card.updatedAt)}` : 'No data'}</small>
          </article>
        ))}
      </div>
    </div>
  );
}

function WorkerMetricSummary({ metrics, compact = false }) {
  const { t } = useI18n();
  if (metrics.loading) return <Message text="Loading worker metrics..." />;
  if (metrics.error) return <Message text={metrics.error} tone="warning" />;
  const latest = latestMetrics(metrics.data);
  const cards = [
    metricCard(latest, 'worker.scene_updates_total', 'Scene Updates', (value) => formatNumber(value, 0), (value) => value > 0 ? 'ok' : 'neutral'),
    metricCard(latest, 'worker.overlay_events_total', 'Overlay Events', (value) => formatNumber(value, 0), (value) => value > 0 ? 'ok' : 'neutral'),
    metricCard(latest, 'worker.caption_events_total', 'Caption Events', (value) => formatNumber(value, 0), (value) => value > 0 ? 'ok' : 'neutral'),
    metricCard(latest, 'worker.event_send_failures_total', 'Event Send Failures', (value) => formatNumber(value, 0), (value) => value > 0 ? 'warning' : 'ok'),
  ];
  const visible = compact ? cards.slice(0, 3) : cards;
  return (
    <div className="metric-panel">
      <div className="panel-heading">
        <h3>{t('Worker Event Metrics')}</h3>
        <span>{metrics.data.length} snapshots</span>
      </div>
      <div className="metric-grid">
        {visible.map((card) => (
          <article className={`metric-card ${card.tone}`} key={card.name}>
            <span>{t(card.label)}</span>
            <strong>{localizeRendered(card.display, t)}</strong>
            <small>{card.updatedAt ? `Updated ${formatDateTime(card.updatedAt)}` : 'No data'}</small>
          </article>
        ))}
      </div>
    </div>
  );
}

function latestMetrics(rows) {
  const out = new Map();
  for (const row of rows || []) {
    if (!row?.name) continue;
    const current = out.get(row.name);
    if (!current || new Date(row.updated_at || 0) > new Date(current.updated_at || 0)) {
      out.set(row.name, row);
    }
  }
  return out;
}

function metricCard(latest, name, label, formatter, toneFor) {
  const row = latest.get(name);
  const value = typeof row?.value === 'number' ? row.value : 0;
  const hasValue = row?.value !== undefined && row?.value !== null;
  return {
    name,
    label,
    display: hasValue ? formatter(value) : '-',
    tone: hasValue ? toneFor(value) : 'neutral',
    updatedAt: row?.updated_at,
  };
}

function Incidents({ data, compact = false, reload, actionable = false }) {
  const { t } = useI18n();
  if (data.loading) return <Message text="Loading incidents..." />;
  if (data.error) return <Message text={data.error} tone="warning" />;
  const rows = compact ? data.data.slice(0, 5) : data.data;
  if (rows.length === 0) return <Message text="No incidents." />;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>{t('Severity')}</th>
            <th>{t('Status')}</th>
            <th>{t('Rule')}</th>
            <th>{t('Service')}</th>
            <th>{t('Stream')}</th>
            <th>{t('Summary')}</th>
            {!compact && <th>{t('Checks')}</th>}
            {actionable && <th>{t('Actions')}</th>}
          </tr>
        </thead>
        <tbody>
          {rows.map((incident) => {
            const hint = incidentRuleHint(incident.rule);
            const report = incident.diagnostic_report || incident.report || {};
            const evidence = report.evidence || [];
            const highlights = evidenceHighlights(evidence);
            return (
              <tr key={incident.id}>
                <td><Badge tone={severityTone(incident.severity)}>{incident.severity}</Badge></td>
                <td>{t(incident.status)}</td>
                <td>{incident.rule}</td>
                <td>{incident.service_id}</td>
                <td>{incident.stream_id || '-'}</td>
                <td>
                  <div className="incident-summary">
                    <span>{incident.summary_ja}</span>
                    {!compact && report.likely_cause && <small>Likely cause: {report.likely_cause}</small>}
                    {!compact && report.impact && <small>Impact: {report.impact}</small>}
                    <EvidenceHighlights highlights={highlights} compact={compact} />
                    {compact && hint.metrics.length > 0 && <small>{hint.metrics.slice(0, 2).join(', ')}</small>}
                  </div>
                </td>
                {!compact && (
                  <td>
                    <RuleHint hint={hint} />
                  </td>
                )}
                {actionable && <td><IncidentActions incident={incident} reload={reload} /></td>}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function IncidentActions({ incident, reload }) {
  const [busy, setBusy] = useState('');
  const run = async (verb) => {
    setBusy(verb);
    try {
      const response = await fetch(`/observability/incidents/${incident.id}/${verb}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      });
      if (response.ok && reload) await reload();
    } finally {
      setBusy('');
    }
  };
  const closed = incident.status === 'resolved' || incident.status === 'ignored';
  return (
    <div className="actions">
      <button className="icon-btn" disabled={busy !== '' || closed || incident.status === 'acknowledged'} onClick={() => run('acknowledge')} title="Acknowledge">
        <CheckCircle2 size={16} />
      </button>
      <button className="icon-btn" disabled={busy !== '' || closed} onClick={() => run('resolve')} title="Resolve">
        <ListRestart size={16} />
      </button>
    </div>
  );
}

function Remediation({ data, reload }) {
  const { t } = useI18n();
  if (data.loading) return <Message text="Loading remediation actions..." />;
  if (data.error) return <Message text={data.error} tone="warning" />;
  if (data.data.length === 0) return <Message text="No remediation actions." />;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>{t('Action')}</th>
            <th>{t('Status')}</th>
            <th>{t('Mode')}</th>
            <th>{t('Incident')}</th>
            <th>{t('Safety')}</th>
            <th>{t('Result')}</th>
            <th>{t('Command')}</th>
          </tr>
        </thead>
        <tbody>
          {data.data.map((action) => (
            <tr key={action.id}>
              <td><ActionSummary actionName={action.action} /></td>
              <td><Badge tone={action.status === 'blocked' ? 'critical' : action.status === 'pending_approval' ? 'warning' : 'ok'}>{action.status}</Badge></td>
              <td>{action.mode}</td>
              <td>{action.incident_id}</td>
              <td>{t(action.requires_approval ? 'Approval required' : action.safe_auto ? 'Safe candidate' : 'Suggested')}</td>
              <td><RemediationResult action={action} /></td>
              <td><ActionButtons action={action} reload={reload} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RemediationResult({ action }) {
  const result = action.result || '';
  if (!result) {
    return <span className="muted">Not executed yet</span>;
  }
  const tone = remediationResultTone(action.status, result);
  return (
    <div className={`remediation-result ${tone}`}>
      <strong>{formatRemediationResult(result)}</strong>
      {action.executed_at && <span>{formatDateTime(action.executed_at)}</span>}
    </div>
  );
}

function remediationResultTone(status, result) {
  if (status === 'blocked' || result.includes('failed') || result.includes('required') || result.includes('not configured')) return 'critical';
  if (result === 'control_panel_dispatch_executed') return 'ok';
  if (result === 'recorded_noop') return 'neutral';
  return 'warning';
}

function formatRemediationResult(result) {
  const labels = {
    control_panel_dispatch_executed: 'Control Panel dispatch executed',
    recorded_noop: 'Recorded only',
    'control panel dispatch failed': 'Control Panel dispatch failed',
    'control panel dispatch is not configured': 'Control Panel dispatch not configured',
    'stream_id is required for control panel dispatch': 'Stream ID required',
    'incident context is required for control panel dispatch': 'Incident context required',
    'manual approval is required': 'Manual approval required',
    'dangerous action is never auto-executed': 'Dangerous action blocked',
    'action is not marked safe': 'Action is not marked safe',
  };
  return labels[result] || result;
}

const remediationActionHelp = {
  retry_gdrive_upload: 'Retry archive upload through the assigned Encoder/Recorder.',
  retry_package_remux: 'Re-run package/remux only when source archive files are intact.',
  refresh_service_status: 'Refresh service state and heartbeat-derived health.',
  rerun_diagnostics: 'Generate diagnostics again after collecting newer evidence.',
  clear_stale_warning: 'Clear a recovered warning after health signals return.',
  restart_discord_bot: 'Manual approval: restart the Discord Bot service.',
  restart_encoder_recorder: 'Manual approval: restart the Encoder/Recorder service.',
  restart_worker: 'Manual approval: restart the Worker service.',
  reconnect_discord_voice: 'Manual approval: reconnect Discord voice.',
  restart_youtube_rtmps_output: 'Manual approval: restart YouTube RTMPS output.',
};

function ActionSummary({ actionName }) {
  return (
    <div className="action-summary">
      <strong>{actionName}</strong>
      <span>{remediationActionHelp[actionName] || 'Review diagnostic evidence before executing.'}</span>
    </div>
  );
}

function ActionButtons({ action, reload }) {
  const [busy, setBusy] = useState('');
  const [message, setMessage] = useState('');
  const approvalPending = action.requires_approval && action.status === 'pending_approval';
  const canApprove = action.status === 'pending_approval';
  const canExecute = action.status !== 'executed' && action.status !== 'blocked' && !approvalPending;
  const run = async (verb) => {
    setBusy(verb);
    setMessage('');
    try {
      const result = await apiRequest(`/observability/remediation-actions/${action.id}/${verb}`, {
        method: 'POST',
        headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      });
      if (!result.ok) {
        setMessage(result.message);
        return;
      }
      if (reload) await reload();
    } finally {
      setBusy('');
    }
  };
  return (
    <div className="action-command">
      <div className="actions">
        <button className="icon-btn" disabled={busy !== '' || !canApprove} onClick={() => run('approve')} title="Approve">
          <CheckCircle2 size={16} />
        </button>
        <button className="icon-btn" disabled={busy !== '' || !canExecute} onClick={() => run('execute')} title="Execute">
          <ListRestart size={16} />
        </button>
      </div>
      {message && <span className="inline-error">{message}</span>}
    </div>
  );
}

const notificationChannelTypes = ['discord', 'slack', 'generic', 'email'];
const notificationSeverityFilters = ['info', 'warning', 'error', 'critical'];
const notificationEventFilters = [
  'stream.started',
  'stream.live',
  'stream.completed',
  'stream.failed',
  'stream.warning',
  'stream.error',
  'incident.opened',
  'incident.updated',
  'incident.resolved',
  'diagnostic.created',
  'remediation.pending_approval',
  'remediation.executed',
  'archive.upload.completed',
  'archive.upload.failed',
  'service.offline',
  'service.recovered',
];

function Notifications({ deliveries, channels }) {
  const [selectedID, setSelectedID] = useState('');
  const [name, setName] = useState('');
  const [type, setType] = useState('discord');
  const [enabled, setEnabled] = useState(true);
  const [webhookURL, setWebhookURL] = useState('');
  const [emailRecipients, setEmailRecipients] = useState('');
  const [smtpHost, setSMTPHost] = useState('');
  const [smtpPort, setSMTPPort] = useState('587');
  const [smtpTLS, setSMTPTLS] = useState(true);
  const [smtpFrom, setSMTPFrom] = useState('');
  const [smtpUsername, setSMTPUsername] = useState('');
  const [smtpPassword, setSMTPPassword] = useState('');
  const [severityFilter, setSeverityFilter] = useState([]);
  const [eventTypeFilter, setEventTypeFilter] = useState([]);
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });
  const selected = channels.data.find((channel) => channel.id === selectedID);

  useEffect(() => {
    if (!selected) return;
    setName(selected.name || '');
    setType(selected.type || 'discord');
    setEnabled(Boolean(selected.enabled));
    setWebhookURL('');
    setEmailRecipients(Array.isArray(selected.email_recipients) ? selected.email_recipients.join('\n') : '');
    setSMTPHost(selected.smtp_host || '');
    setSMTPPort(selected.smtp_port ? String(selected.smtp_port) : '587');
    setSMTPTLS(selected.smtp_tls !== false);
    setSMTPFrom(selected.smtp_from || '');
    setSMTPUsername(selected.smtp_username || '');
    setSMTPPassword('');
    setSeverityFilter(selected.severity_filter || []);
    setEventTypeFilter(selected.event_type_filter || []);
    setMessage({ text: '', tone: 'neutral' });
  }, [selected]);

  const reset = () => {
    setSelectedID('');
    setName('');
    setType('discord');
    setEnabled(true);
    setWebhookURL('');
    setEmailRecipients('');
    setSMTPHost('');
    setSMTPPort('587');
    setSMTPTLS(true);
    setSMTPFrom('');
    setSMTPUsername('');
    setSMTPPassword('');
    setSeverityFilter([]);
    setEventTypeFilter([]);
    setMessage({ text: '', tone: 'neutral' });
  };

  const toggleItem = (value, setter) => {
    setter((current) => current.includes(value) ? current.filter((item) => item !== value) : [...current, value]);
  };

  const parsedEmailRecipients = () => emailRecipients
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);

  const save = async () => {
    if (!name.trim()) {
      setMessage({ text: 'Channel name is required.', tone: 'warning' });
      return;
    }
    if (type === 'email') {
      if (parsedEmailRecipients().length === 0 || !smtpHost.trim() || !smtpFrom.trim()) {
        setMessage({ text: 'Email recipients, SMTP host, and From address are required.', tone: 'warning' });
        return;
      }
    } else {
      if (!selectedID && !webhookURL.trim()) {
        setMessage({ text: 'Webhook URL is required for new channels.', tone: 'warning' });
        return;
      }
    }
    const body = {
      name: name.trim(),
      type,
      enabled,
      severity_filter: severityFilter,
      event_type_filter: eventTypeFilter,
    };
    if (type === 'email') {
      body.email_recipients = parsedEmailRecipients();
      body.smtp_host = smtpHost.trim();
      body.smtp_port = Number.parseInt(smtpPort, 10) || 587;
      body.smtp_tls = smtpTLS;
      body.smtp_from = smtpFrom.trim();
      body.smtp_username = smtpUsername.trim();
      if (smtpPassword.trim()) body.smtp_password = smtpPassword.trim();
    } else if (webhookURL.trim()) {
      body.webhook_url = webhookURL.trim();
    }
    const result = await apiRequest(selectedID ? `/observability/notification-channels/${selectedID}` : '/observability/notification-channels', {
      method: selectedID ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(body),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setWebhookURL('');
    setSMTPPassword('');
    setMessage({ text: selectedID ? 'Notification channel updated.' : 'Notification channel created.', tone: 'ok' });
    await channels.reload?.();
    if (!selectedID && result.body?.id) setSelectedID(result.body.id);
  };

  const remove = async () => {
    if (!selectedID) {
      setMessage({ text: 'Select a channel first.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/observability/notification-channels/${selectedID}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    await channels.reload?.();
    reset();
    setMessage({ text: 'Notification channel deleted.', tone: 'ok' });
  };

  const test = async (id = selectedID) => {
    if (!id) {
      setMessage({ text: 'Select a channel first.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/observability/notification-channels/${id}/test`, {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Test notification sent.', tone: 'ok' });
    await deliveries.reload?.();
  };

  if (deliveries.loading || channels.loading) return <Message text="Loading notification data..." />;
  if (deliveries.error) return <Message text={deliveries.error} tone="warning" />;
  if (channels.error) return <Message text={channels.error} tone="warning" />;
  const channelColumns = [
    { key: 'name', label: 'Name' },
    { key: 'type', label: 'Type' },
    { key: 'enabled', label: 'Enabled', render: (value) => <Badge tone={value ? 'ok' : 'warning'}>{value ? 'enabled' : 'disabled'}</Badge> },
    { key: 'target', label: 'Target', render: (_, row) => row.type === 'email' ? (row.masked_email_target || '-') : (row.masked_webhook_url || '-') },
    { key: 'severity_filter', label: 'Severity Filter', render: (value) => Array.isArray(value) && value.length ? value.join(', ') : 'all' },
    {
      key: 'id',
      label: 'Actions',
      render: (_, row) => (
        <div className="actions">
          <button className="link-btn" type="button" onClick={() => setSelectedID(row.id)}>Edit</button>
          <button className="secondary-btn" type="button" onClick={() => test(row.id)}>Test</button>
        </div>
      ),
    },
  ];
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>{selectedID ? 'Edit Notification Channel' : 'Create Notification Channel'}</h3>
            <span>Webhook URLs and SMTP passwords are write-only. The table shows only masked targets.</span>
          </div>
          <button className="secondary-btn" type="button" onClick={reset}>New</button>
        </div>
        <div className="form-grid">
          <label>
            <span>Existing channel</span>
            <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
              <option value="">Create new</option>
              {channels.data.map((channel) => <option key={channel.id} value={channel.id}>{channel.name}</option>)}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <label>
            <span>Type</span>
            <select value={type} onChange={(event) => setType(event.target.value)}>
              {notificationChannelTypes.map((item) => <option key={item} value={item}>{item}</option>)}
            </select>
          </label>
          {type !== 'email' && (
            <label>
              <span>Webhook URL</span>
              <input type="password" value={webhookURL} onChange={(event) => setWebhookURL(event.target.value)} placeholder={selectedID ? 'leave blank to keep existing URL' : 'https://example.com/webhook/<TOKEN>'} />
            </label>
          )}
          {type === 'email' && (
            <>
              <label className="wide">
                <span>Recipients</span>
                <textarea value={emailRecipients} onChange={(event) => setEmailRecipients(event.target.value)} placeholder="ops@example.com&#10;alerts@example.com" rows={3} />
              </label>
              <label>
                <span>SMTP Host</span>
                <input value={smtpHost} onChange={(event) => setSMTPHost(event.target.value)} placeholder="smtp.example.com" />
              </label>
              <label>
                <span>SMTP Port</span>
                <input type="number" min="1" max="65535" value={smtpPort} onChange={(event) => setSMTPPort(event.target.value)} />
              </label>
              <label>
                <span>From</span>
                <input value={smtpFrom} onChange={(event) => setSMTPFrom(event.target.value)} placeholder="autostream@example.com" />
              </label>
              <label>
                <span>SMTP Username</span>
                <input value={smtpUsername} onChange={(event) => setSMTPUsername(event.target.value)} />
              </label>
              <label>
                <span>SMTP Password</span>
                <input type="password" value={smtpPassword} onChange={(event) => setSMTPPassword(event.target.value)} placeholder={selectedID ? 'leave blank to keep existing password' : 'optional'} />
              </label>
              <label className="check-row">
                <input type="checkbox" checked={smtpTLS} onChange={(event) => setSMTPTLS(event.target.checked)} />
                <span>Use TLS</span>
              </label>
            </>
          )}
          <label className="check-row">
            <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
            <span>Enabled</span>
          </label>
          <div className="checkbox-grid wide">
            {notificationSeverityFilters.map((severity) => (
              <label className="check-row" key={severity}>
                <input type="checkbox" checked={severityFilter.includes(severity)} onChange={() => toggleItem(severity, setSeverityFilter)} />
                <span>{severity}</span>
              </label>
            ))}
          </div>
          <div className="checkbox-grid wide">
            {notificationEventFilters.map((eventType) => (
              <label className="check-row" key={eventType}>
                <input type="checkbox" checked={eventTypeFilter.includes(eventType)} onChange={() => toggleItem(eventType, setEventTypeFilter)} />
                <span>{eventType}</span>
              </label>
            ))}
          </div>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={save}>{selectedID ? 'Update Channel' : 'Create Channel'}</button>
          <button className="secondary-btn" type="button" disabled={!selectedID} onClick={() => test()}>Test Channel</button>
          <button className="danger-btn" type="button" disabled={!selectedID} onClick={remove}>Delete Channel</button>
        </div>
      </section>
      <DataTable title="Notification Channels" data={channels} columns={channelColumns} />
      <DataTable title="Notification Deliveries" data={deliveries} columns={[
        { key: 'event_type', label: 'Event' },
        { key: 'channel', label: 'Channel' },
        { key: 'status', label: 'Status', render: (value) => <Badge tone={value === 'success' ? 'ok' : 'critical'}>{value}</Badge> },
        { key: 'target', label: 'Target' },
        { key: 'incident_id', label: 'Incident' },
      ]} />
    </div>
  );
}

function Diagnostics({ data }) {
  const [selectedID, setSelectedID] = useState('');
  useEffect(() => {
    if (!selectedID && Array.isArray(data.data) && data.data.length > 0) {
      setSelectedID(data.data[0].id || data.data[0].incident_id || '');
    }
  }, [data.data, selectedID]);
  if (data.loading) return <Message text="Loading diagnostics..." />;
  if (data.error) return <Message text={data.error} tone="warning" />;
  const reports = Array.isArray(data.data) ? data.data : [];
  const selected = reports.find((item) => (item.id || item.incident_id) === selectedID) || reports[0];
  if (!selected) return <Message text="No diagnostic reports." />;
  const report = selected.diagnostic_report || selected.report || {};
  const hint = incidentRuleHint(selected.rule);
  const highlights = evidenceHighlights(report.evidence);
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Diagnostic Reports</h3>
            <span>Select an incident report and review evidence, impact, and next checks.</span>
          </div>
        </div>
        <label>
          <span>Report</span>
          <select value={selectedID} onChange={(event) => setSelectedID(event.target.value)}>
            {reports.map((item) => {
              const id = item.id || item.incident_id;
              return <option key={id} value={id}>{item.rule || 'diagnostic'} / {item.incident_id || item.id || '-'}</option>;
            })}
          </select>
        </label>
      </section>
      <div className="report">
        <h3>{report.summary || selected.rule}</h3>
        <EvidenceHighlights highlights={highlights} />
        <dl>
          <dt>Incident</dt>
          <dd>{selected.incident_id || selected.id || '-'}</dd>
          <dt>Rule</dt>
          <dd>{selected.rule || '-'}</dd>
          <dt>Likely cause</dt>
          <dd>{report.likely_cause || '-'}</dd>
          <dt>Impact</dt>
          <dd>{report.impact || '-'}</dd>
          <dt>Confidence</dt>
          <dd>{typeof report.confidence === 'number' ? `${Math.round(report.confidence * 100)}%` : '-'}</dd>
        </dl>
        <RuleHint hint={hint} />
        <List title="Evidence" items={report.evidence} />
        <List title="Recommended actions" items={report.recommended_actions} />
        <List title="Safe auto candidates" items={report.safe_auto_remediation_candidates} />
        <List title="Actions requiring approval" items={report.actions_requiring_approval} />
      </div>
    </div>
  );
}

function Metrics({ data, incidents }) {
  if (data.loading) return <Message text="Loading metric snapshots..." />;
  if (data.error) return <Message text={data.error} tone="warning" />;
  return (
    <div className="stack">
      <EncoderMetricSummary metrics={data} />
      <AudioMetricSummary metrics={data} />
      <WorkerMetricSummary metrics={data} />
      <ArchiveMetricSummary metrics={data} />
      <DataTable title="Metric Snapshots" data={data} columns={[
        { key: 'name', label: 'Name' },
        { key: 'service_id', label: 'Service' },
        { key: 'service_type', label: 'Type' },
        { key: 'stream_id', label: 'Stream' },
        { key: 'value', label: 'Value', render: (value) => value === undefined || value === null ? '-' : String(value) },
        { key: 'status', label: 'Status' },
        { key: 'updated_at', label: 'Updated' },
      ]} />
      <Incidents data={incidents} compact />
    </div>
  );
}

function DataTable({ title, data, columns, compact = false }) {
  const { t } = useI18n();
  if (data.loading) return <Message text={`${t('Loading')} ${t(title)}...`} />;
  if (data.error) return <Message text={data.error} tone="warning" />;
  const rows = compact ? data.data.slice(0, 5) : data.data;
  if (!Array.isArray(rows) || rows.length === 0) return <Message text={`${t('No records')}: ${t(title)}`} />;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {columns.map((column) => <th key={column.key}>{t(column.label)}</th>)}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={row.id || row.service_id || `${title}-${index}`}>
              {columns.map((column) => (
                <td key={column.key}>{localizeRendered(column.render ? column.render(row[column.key], row) : displayValue(row[column.key]), t)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SecurityView({ settings, secrets, me }) {
  const [passkeys] = useAPI('/auth/passkeys');
  const [form, setForm] = useState({
    password_min_length: 12,
    login_lockout_threshold: 5,
    session_idle_timeout_min: 30,
    session_absolute_lifetime_h: 12,
    mfa_mode: 'disabled',
    mfa_required_roles: '',
  });
  const [secretName, setSecretName] = useState('');
  const [secretValue, setSecretValue] = useState('');
  const [mfaEnrollCode, setMfaEnrollCode] = useState('');
  const [mfaActionCode, setMfaActionCode] = useState('');
  const [mfaEnrollment, setMfaEnrollment] = useState(null);
  const [mfaRecoveryCodes, setMfaRecoveryCodes] = useState([]);
  const [passkeyRegistration, setPasskeyRegistration] = useState(null);
  const [message, setMessage] = useState({ text: '', tone: 'neutral' });

  useEffect(() => {
    if (!settings.data || Array.isArray(settings.data)) return;
    setForm({
      password_min_length: settings.data.password_min_length || 12,
      login_lockout_threshold: settings.data.login_lockout_threshold || 5,
      session_idle_timeout_min: settings.data.session_idle_timeout_min || 30,
      session_absolute_lifetime_h: settings.data.session_absolute_lifetime_h || 12,
      mfa_mode: settings.data.mfa_mode || 'disabled',
      mfa_required_roles: Array.isArray(settings.data.mfa_required_roles) ? settings.data.mfa_required_roles.join(', ') : '',
    });
  }, [settings.data]);

  if (settings.loading || secrets.loading || passkeys.loading) return <Message text="Loading security settings..." />;
  if (settings.error) return <Message text={settings.error} tone="warning" />;
  if (secrets.error) return <Message text={secrets.error} tone="warning" />;
  if (passkeys.error) return <Message text={passkeys.error} tone="warning" />;

  const updateSetting = (key, value) => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const saveSettings = async () => {
    const body = {
      password_min_length: Number(form.password_min_length),
      password_hash: 'argon2id',
      login_lockout_threshold: Number(form.login_lockout_threshold),
      session_idle_timeout_min: Number(form.session_idle_timeout_min),
      session_absolute_lifetime_h: Number(form.session_absolute_lifetime_h),
      remember_me_enabled: false,
      mfa_mode: form.mfa_mode || 'disabled',
      mfa_required_roles: String(form.mfa_required_roles || '').split(',').map((item) => item.trim()).filter(Boolean),
    };
    const result = await apiRequest('/security/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify(body),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Security settings updated.', tone: 'ok' });
    await settings.reload?.();
  };

  const saveSecret = async (clear = false) => {
    if (!secretName) {
      setMessage({ text: 'Select a secret name.', tone: 'warning' });
      return;
    }
    const result = await apiRequest(`/secrets/${encodeURIComponent(secretName)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ value: clear ? '' : secretValue }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setSecretValue('');
    setMessage({ text: clear ? 'Secret cleared.' : 'Secret updated. Raw value was not returned.', tone: 'ok' });
    await secrets.reload?.();
  };

  const beginTOTPEnrollment = async () => {
    const result = await apiRequest('/auth/mfa/enroll', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ code: mfaActionCode.trim() }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMfaEnrollment(result.body || {});
    setMfaRecoveryCodes(result.body?.recovery_codes || []);
    setMfaEnrollCode('');
    setMessage({ text: 'TOTP enrollment started. Verify a current code to enable MFA.', tone: 'ok' });
  };

  const verifyTOTPEnrollment = async () => {
    if (!mfaEnrollCode.trim()) {
      setMessage({ text: 'Enter the TOTP code from your authenticator app.', tone: 'warning' });
      return;
    }
    const result = await apiRequest('/auth/mfa/verify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ code: mfaEnrollCode.trim() }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMfaEnrollment(null);
    setMfaEnrollCode('');
    setMfaActionCode('');
    setMessage({ text: 'TOTP MFA enabled.', tone: 'ok' });
  };

  const regenerateRecoveryCodes = async () => {
    if (!mfaActionCode.trim()) {
      setMessage({ text: 'Enter a current TOTP or recovery code.', tone: 'warning' });
      return;
    }
    const result = await apiRequest('/auth/recovery-codes/regenerate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ code: mfaActionCode.trim() }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMfaRecoveryCodes(result.body?.recovery_codes || []);
    setMfaActionCode('');
    setMessage({ text: 'Recovery codes regenerated. They are shown only once.', tone: 'ok' });
  };

  const disableTOTP = async () => {
    if (!mfaActionCode.trim()) {
      setMessage({ text: 'Enter a current TOTP or recovery code.', tone: 'warning' });
      return;
    }
    const result = await apiRequest('/auth/mfa/disable', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ code: mfaActionCode.trim() }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMfaEnrollment(null);
    setMfaRecoveryCodes([]);
    setMfaActionCode('');
    setMessage({ text: 'TOTP MFA disabled for the current user.', tone: 'ok' });
  };

  const deletePasskey = async (id) => {
    const label = passkeys.data.find((item) => item.id === id)?.name || id;
    if (!window.confirm(`Delete passkey "${label}"?`)) return;
    const result = await apiRequest(`/auth/passkeys/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setMessage({ text: 'Passkey credential deleted.', tone: 'ok' });
    await passkeys.reload?.();
  };

  const startPasskeyRegistration = async () => {
    if (!passkeySupported()) {
      setMessage({ text: 'This browser does not support Passkey / WebAuthn.', tone: 'warning' });
      return;
    }
    const result = await apiRequest('/auth/passkeys/register/start', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
      body: JSON.stringify({ display_name: me.data?.user?.username || 'AutoStream User' }),
    });
    if (!result.ok) {
      setMessage({ text: result.message, tone: 'warning' });
      return;
    }
    setPasskeyRegistration(result.body || {});
    try {
      const credential = await navigator.credentials.create({ publicKey: credentialCreateOptions(result.body?.public_key) });
      const finish = await apiRequest('/auth/passkeys/register/finish', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken(), Accept: 'application/json' },
        body: JSON.stringify({
          registration_token: result.body?.registration_token,
          name: `Passkey for ${me.data?.user?.username || 'AutoStream User'}`,
          credential: publicKeyCredentialToJSON(credential),
        }),
      });
      if (!finish.ok) {
        setMessage({ text: finish.message, tone: 'warning' });
        return;
      }
      setPasskeyRegistration(null);
      setMessage({ text: 'Passkey registered.', tone: 'ok' });
      await passkeys.reload?.();
    } catch (error) {
      setMessage({ text: error?.name === 'NotAllowedError' ? 'Passkey registration was cancelled or timed out.' : 'Passkey registration failed.', tone: 'warning' });
    }
  };

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Security Settings</h3>
            <span>Fail-closed defaults are enforced server-side.</span>
          </div>
          <span>Updated {formatDateTime(settings.data?.updated_at)}</span>
        </div>
        <div className="form-grid">
          <label>
            <span>Password min length</span>
            <input type="number" min="12" value={form.password_min_length} onChange={(event) => updateSetting('password_min_length', event.target.value)} />
          </label>
          <label>
            <span>Login lockout threshold</span>
            <input type="number" min="3" value={form.login_lockout_threshold} onChange={(event) => updateSetting('login_lockout_threshold', event.target.value)} />
          </label>
          <label>
            <span>Session idle timeout minutes</span>
            <input type="number" min="5" value={form.session_idle_timeout_min} onChange={(event) => updateSetting('session_idle_timeout_min', event.target.value)} />
          </label>
          <label>
            <span>Session absolute lifetime hours</span>
            <input type="number" min="1" value={form.session_absolute_lifetime_h} onChange={(event) => updateSetting('session_absolute_lifetime_h', event.target.value)} />
          </label>
          <label>
            <span>MFA mode</span>
            <select value={form.mfa_mode} onChange={(event) => updateSetting('mfa_mode', event.target.value)}>
              <option value="disabled">disabled</option>
              <option value="totp">totp</option>
              <option value="passkey">passkey</option>
            </select>
          </label>
          <label>
            <span>MFA methods</span>
            <input value={(settings.data?.mfa_supported_methods || ['totp']).join(', ')} disabled />
          </label>
          <label>
            <span>MFA required roles</span>
            <input value={form.mfa_required_roles} onChange={(event) => updateSetting('mfa_required_roles', event.target.value)} placeholder="blank = all users, e.g. super_admin, admin" />
          </label>
          <label>
            <span>Passkey / WebAuthn</span>
            <input value={settings.data?.passkey_status || 'available'} disabled />
          </label>
          <label>
            <span>Password hash</span>
            <input value="argon2id" disabled />
          </label>
        </div>
        <p className="form-note">TOTP mode requires TOTP after password or OAuth login. Passkey mode requires targeted users to sign in with a registered WebAuthn credential; password and OAuth login do not issue sessions for those users.</p>
        <div className="actions">
          <button className="command-btn" type="button" onClick={saveSettings}>Save Settings</button>
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Current User Passkeys</h3>
            <span>Register, use, and remove WebAuthn credentials for the current user.</span>
          </div>
          <Badge tone="ok">{settings.data?.passkey_status || 'available'}</Badge>
        </div>
        <div className="actions">
          <button className="command-btn" type="button" onClick={startPasskeyRegistration}>Register Passkey</button>
        </div>
        {passkeyRegistration && (
          <div className="inline-note">
            <strong>Challenge ready:</strong> RP {passkeyRegistration.public_key?.rp?.id || '-'} / expires {formatDateTime(passkeyRegistration.expires_at)}. The one-time registration token is held only in this browser response.
          </div>
        )}
        <DataTable title="Passkey credentials" data={passkeys} columns={[
          { key: 'name', label: 'Name' },
          { key: 'credential_id_hash', label: 'Credential Hash', render: (value) => <code>{value ? `${String(value).slice(0, 12)}...` : '-'}</code> },
          { key: 'sign_count', label: 'Sign Count' },
          { key: 'transports', label: 'Transports', render: (value) => Array.isArray(value) && value.length > 0 ? value.join(', ') : '-' },
          { key: 'last_used_at', label: 'Last Used', render: formatDateTime },
          { key: 'created_at', label: 'Created', render: formatDateTime },
          { key: 'id', label: 'Actions', render: (value) => <button className="danger-btn" type="button" onClick={() => deletePasskey(value)}>Delete</button> },
        ]} />
        <p className="form-note">This table never includes raw credential IDs or public key CBOR. Registration/login ceremony data is stored server-side and discarded after use.</p>
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Current User MFA</h3>
            <span>Manage TOTP enrollment for {me.data?.user?.username || 'the current user'}. One-time secrets are not returned again.</span>
          </div>
        </div>
        <div className="form-grid">
          <label>
            <span>Current TOTP or recovery code</span>
            <input value={mfaActionCode} onChange={(event) => setMfaActionCode(event.target.value)} inputMode="numeric" autoComplete="one-time-code" placeholder="required for re-enroll, disable, or recovery regeneration" />
          </label>
          <label>
            <span>Enrollment verification code</span>
            <input value={mfaEnrollCode} onChange={(event) => setMfaEnrollCode(event.target.value)} inputMode="numeric" autoComplete="one-time-code" placeholder="6 digit code after scanning" />
          </label>
        </div>
        {mfaEnrollment && (
          <div className="token-once">
            <span>TOTP secret shown once</span>
            <code>{mfaEnrollment.secret}</code>
            <span>Provisioning URI</span>
            <code>{mfaEnrollment.provisioning_uri}</code>
          </div>
        )}
        {mfaRecoveryCodes.length > 0 && (
          <div className="token-once">
            <span>Recovery codes shown once</span>
            <code>{mfaRecoveryCodes.join('  ')}</code>
          </div>
        )}
        <p className="form-note">TOTP mode must be enabled in Security Settings before enrollment. Recovery codes are hashed server-side and cannot be viewed again.</p>
        <div className="actions">
          <button className="command-btn" type="button" onClick={beginTOTPEnrollment}>Start TOTP Enrollment</button>
          <button className="secondary-btn" type="button" disabled={!mfaEnrollment} onClick={verifyTOTPEnrollment}>Verify Enrollment</button>
          <button className="secondary-btn" type="button" onClick={regenerateRecoveryCodes}>Regenerate Recovery Codes</button>
          <button className="danger-btn" type="button" onClick={disableTOTP}>Disable TOTP</button>
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h3>Update Secret</h3>
            <span>Raw secret values are write-only and are never returned by the API.</span>
          </div>
        </div>
        <div className="form-grid">
          <label>
            <span>Secret name</span>
            <select value={secretName} onChange={(event) => setSecretName(event.target.value)}>
              <option value="">Select secret</option>
              {secrets.data.map((secret) => <option key={secret.name} value={secret.name}>{secret.name}</option>)}
            </select>
          </label>
          <label>
            <span>New value</span>
            <input type="password" value={secretValue} onChange={(event) => setSecretValue(event.target.value)} placeholder="write-only secret value" />
          </label>
        </div>
        {message.text && <Message text={message.text} tone={message.tone} />}
        <div className="actions">
          <button className="command-btn" type="button" onClick={() => saveSecret(false)}>Update Secret</button>
          <button className="danger-btn" type="button" disabled={!secretName} onClick={() => saveSecret(true)}>Clear Secret</button>
        </div>
      </section>
      <DataTable title="Secrets" data={secrets} columns={[
        { key: 'name', label: 'Name' },
        { key: 'configured', label: 'Configured', render: (value) => <Badge tone={value ? 'ok' : 'warning'}>{value ? 'configured' : 'missing'}</Badge> },
        { key: 'fingerprint', label: 'Fingerprint' },
        { key: 'updated_at', label: 'Updated' },
      ]} />
    </div>
  );
}

function Status({ icon, label, value, tone = 'neutral' }) {
  const { t } = useI18n();
  return (
    <article className={`status ${tone}`}>
      {icon}
      <strong>{t(label)}</strong>
      <span>{localizeRendered(value, t)}</span>
    </article>
  );
}

function Badge({ children, tone }) {
  const { t } = useI18n();
  return <span className={`badge ${tone}`}>{localizeRendered(children || '-', t)}</span>;
}

function CapabilityList({ value }) {
  const entries = Object.entries(value || {})
    .filter(([, enabled]) => typeof enabled === 'boolean')
    .sort(([left], [right]) => left.localeCompare(right));
  if (entries.length === 0) return <span>-</span>;
  return (
    <div className="capability-list">
      {entries.map(([name, enabled]) => (
        <span className={`capability-pill ${enabled ? 'enabled' : 'disabled'}`} key={name}>
          {name}: {enabled ? 'on' : 'off'}
        </span>
      ))}
    </div>
  );
}

function ServiceMetricList({ value }) {
  const metrics = value || {};
  const important = [
    ['discord.audio_receiving', 'audio'],
    ['discord.audio_packets_total', 'rx'],
    ['discord.audio_forwarded_total', 'tx'],
    ['discord.audio_forward_errors_total', 'tx errors'],
    ['discord.voice_connected', 'voice'],
    ['discord.participant_count', 'participants'],
    ['worker.overlay_events_total', 'overlay'],
    ['worker.caption_events_total', 'captions'],
    ['worker.scene_updates_total', 'scene'],
    ['worker.event_send_failures_total', 'worker errors'],
  ].filter(([name]) => metrics[name] !== undefined && metrics[name] !== null);
  if (important.length === 0) return <span>-</span>;
  return (
    <div className="capability-list">
      {important.map(([name, label]) => {
        const value = Number(metrics[name]);
        const healthy = name.endsWith('errors_total') ? value <= 0 : value > 0;
        return (
          <span className={`capability-pill ${healthy ? 'enabled' : 'disabled'}`} key={name}>
            {label}: {formatMetricValue(name, value)}
          </span>
        );
      })}
    </div>
  );
}

const ruleHints = {
  archive_remux_slow: {
    metrics: ['recorder.remux_duration_ms', 'archive.final_mkv_exists', 'archive.final_mp4_exists', 'recorder.disk_free_bytes'],
    checks: ['remux log', 'archive disk I/O', 'final.mkv size'],
  },
  archive_package_failed: {
    metrics: ['archive.package_status', 'archive.final_mkv_exists', 'archive.final_mp4_exists', 'recorder.disk_free_bytes'],
    checks: ['final.mkv exists', 'remux log', 'archive permissions'],
  },
  gdrive_upload_failed: {
    metrics: ['gdrive.upload_status', 'gdrive.upload_retry_count', 'gdrive.upload_duration_sec', 'gdrive.upload_file_count', 'gdrive.upload_folder_fingerprint_present', 'gdrive.upload_final_mp4_fingerprint_present', 'gdrive.upload_metadata_fingerprint_present'],
    checks: ['Drive folder share', 'service account credential', 'uploaded file proof', 'retry-upload'],
  },
  recorder_not_writing: {
    metrics: ['recorder.write_bitrate_kbps', 'recorder.file_size_bytes', 'recorder.disk_free_bytes'],
    checks: ['FFmpeg process', 'final.mkv size', 'archive disk free'],
  },
  media_input_timeout: {
    metrics: ['media.input_timeout_sec', 'discord.audio_receiving', 'discord.audio_forward_active', 'encoder.process_alive'],
    checks: ['input stream', 'Discord audio ingest', 'Encoder audio-status', 'FFmpeg progress'],
  },
  discord_audio_not_receiving: {
    metrics: ['discord.audio_receiving', 'discord.audio_packets_total', 'discord.audio_last_packet_age_sec', 'media.input_timeout_sec'],
    checks: ['Discord voice connection', 'Discord Connect/Speak permissions', 'Encoder audio-status'],
  },
  discord_audio_forward_failed: {
    metrics: ['discord.audio_forward_errors_total', 'discord.audio_forwarded_total', 'discord.audio_last_forward_age_sec'],
    checks: ['Encoder public URL', 'ENCODER_AUDIO_TOKEN', 'service assignment', 'Encoder audio-status'],
  },
  discord_audio_forward_recovered: {
    metrics: ['discord.audio_forward_errors_total', 'discord.audio_forwarded_total', 'discord.audio_last_forward_age_sec'],
    checks: ['forwarded total is increasing', 'last forward age is low', 'network/encoder load trend'],
  },
  discord_audio_forward_stale: {
    metrics: ['discord.audio_last_forward_age_sec', 'discord.audio_forwarded_total', 'discord.audio_forward_errors_total'],
    checks: ['Encoder audio-status', 'Bot to Encoder reachability', 'service token match'],
  },
  discord_audio_forward_inactive: {
    metrics: ['discord.audio_forward_enabled', 'discord.audio_forward_active', 'discord.voice_connected'],
    checks: ['ENCODER_AUDIO_TOKEN', 'Encoder public URL', 'stream assignment', 'audio_stream_forward capability'],
  },
  discord_reconnect_loop: {
    metrics: ['discord.reconnect_count', 'discord.gateway_connected', 'discord.voice_connected'],
    checks: ['Discord gateway log', 'Bot host network/DNS', 'service heartbeat freshness'],
  },
  discord_voice_disconnected: {
    metrics: ['discord.voice_disconnect_count', 'discord.voice_connected', 'discord.audio_forward_active', 'discord.audio_last_packet_age_sec'],
    checks: ['Discord VC membership', 'Connect/Speak permissions', 'Encoder audio-status', 'reconnect_discord_voice remediation'],
  },
  audio_silence: {
    metrics: ['encoder.audio_level_db', 'encoder.audio_silence_sec', 'discord.audio_receiving'],
    checks: ['Discord mute/routing', 'audio mapping', 'input level'],
  },
  audio_clipping: {
    metrics: ['encoder.audio_level_db', 'encoder.audio_clipping_total'],
    checks: ['input gain', 'mix headroom', 'audio filter chain'],
  },
  encoder_process_exited: {
    metrics: ['encoder.process_alive', 'encoder.output_fps', 'encoder.output_bitrate_kbps'],
    checks: ['FFmpeg logs', 'input URL', 'RTMPS output'],
  },
  worker_event_send_failed: {
    metrics: ['worker.event_send_failures_total', 'worker.scene_updates_total', 'worker.overlay_events_total', 'worker.caption_events_total'],
    checks: ['Worker event path', 'Worker event sidecar', 'ENCODER_RECORDER_URL', 'service assignment'],
  },
  stream_stop_timeout: {
    metrics: ['stream.stop_duration_ms', 'archive.package_status', 'recorder.remux_duration_ms', 'gdrive.upload_status'],
    checks: ['FFmpeg stop', 'remux status', 'upload status'],
  },
};

function incidentRuleHint(rule) {
  return ruleHints[rule] || { metrics: [], checks: ['related logs', 'service health', 'recent metrics'] };
}

function RuleHint({ hint }) {
  if (!hint || (hint.metrics.length === 0 && hint.checks.length === 0)) return <span>-</span>;
  return (
    <div className="rule-hint">
      {hint.metrics.length > 0 && (
        <div>
          <strong>Metrics</strong>
          <span>{hint.metrics.join(', ')}</span>
        </div>
      )}
      {hint.checks.length > 0 && (
        <div>
          <strong>Checks</strong>
          <span>{hint.checks.join(', ')}</span>
        </div>
      )}
    </div>
  );
}

const evidenceHighlightLabels = {
  failure_phase: 'Phase',
  error_class: 'Error',
  upload_attempts: 'Upload attempts',
  file_count: 'Files',
  remux_duration_ms: 'Remux',
  dry_run: 'Dry run',
  upload_dry_run: 'Upload dry run',
  'discord.audio_forwarded_total': 'Forwarded',
  'discord.audio_forward_errors_total': 'Forward errors',
  'discord.audio_last_forward_age_sec': 'Forward age',
  'discord.audio_last_packet_age_sec': 'Packet age',
};

const evidenceHighlightPriority = [
  'failure_phase',
  'error_class',
  'discord.audio_forward_errors_total',
  'discord.audio_forwarded_total',
  'discord.audio_last_forward_age_sec',
  'discord.audio_last_packet_age_sec',
  'upload_attempts',
  'remux_duration_ms',
  'file_count',
  'dry_run',
  'upload_dry_run',
];

function evidenceHighlights(evidence = []) {
  if (!Array.isArray(evidence)) return [];
  const values = new Map();
  for (const item of evidence) {
    if (typeof item !== 'string') continue;
    const index = item.indexOf('=');
    if (index <= 0) continue;
    const key = item.slice(0, index).trim();
    const value = item.slice(index + 1).trim();
    if (evidenceHighlightLabels[key] && value && !values.has(key)) {
      values.set(key, value);
    }
  }
  return evidenceHighlightPriority
    .filter((key) => values.has(key))
    .map((key) => ({ key, label: evidenceHighlightLabels[key], value: formatEvidenceValue(key, values.get(key)), tone: evidenceTone(key, values.get(key)) }));
}

function EvidenceHighlights({ highlights, compact = false }) {
  const { t } = useI18n();
  if (!Array.isArray(highlights) || highlights.length === 0) return null;
  const visible = compact ? highlights.slice(0, 2) : highlights;
  return (
    <div className={`evidence-highlights ${compact ? 'compact' : ''}`}>
      {visible.map((item) => (
        <span className={`evidence-chip ${item.tone}`} key={item.key}>
          <strong>{t(item.label)}</strong>
          <span>{item.value}</span>
        </span>
      ))}
    </div>
  );
}

function formatEvidenceValue(key, value) {
  if (key === 'remux_duration_ms') {
    const number = Number(value);
    return Number.isFinite(number) ? formatDurationMillis(number) : value;
  }
  if (key === 'discord.audio_last_forward_age_sec' || key === 'discord.audio_last_packet_age_sec') {
    const number = Number(value);
    return Number.isFinite(number) ? formatDurationSeconds(number) : value;
  }
  if (key === 'discord.audio_forwarded_total' || key === 'discord.audio_forward_errors_total') {
    const number = Number(value);
    return Number.isFinite(number) ? formatNumber(number, 0) : value;
  }
  return value;
}

function evidenceTone(key, value) {
  if (key === 'failure_phase') {
    if (value === 'upload') return 'warning';
    if (value === 'remux' || value === 'package' || value === 'input') return 'critical';
  }
  if (key === 'error_class') return 'critical';
  if (key === 'discord.audio_forward_errors_total') {
    const number = Number(value);
    return Number.isFinite(number) && number > 0 ? 'warning' : 'neutral';
  }
  if (key === 'discord.audio_last_forward_age_sec') {
    const number = Number(value);
    return Number.isFinite(number) && number >= 5 ? 'critical' : 'neutral';
  }
  if (key === 'discord.audio_forwarded_total') {
    const number = Number(value);
    return Number.isFinite(number) && number > 0 ? 'ok' : 'warning';
  }
  return 'neutral';
}

function formatMetricValue(name, value) {
  if (!Number.isFinite(value)) return '-';
  if (name === 'discord.audio_receiving' || name === 'discord.voice_connected') return value >= 1 ? 'yes' : 'no';
  return formatNumber(value, 0);
}

function Message({ text, tone = 'neutral' }) {
  const { t } = useI18n();
  return <div className={`message ${tone}`}>{localizeRendered(text, t)}</div>;
}

function List({ title, items = [] }) {
  const { t } = useI18n();
  if (!Array.isArray(items) || items.length === 0) return null;
  return (
    <>
      <h4>{t(title)}</h4>
      <ul>
        {items.map((item) => <li key={item}>{item}</li>)}
      </ul>
    </>
  );
}

function severityTone(severity) {
  if (severity === 'critical' || severity === 'error') return 'critical';
  if (severity === 'warning') return 'warning';
  if (severity === 'info') return 'neutral';
  return 'ok';
}

function statusTone(status) {
  if (status === 'live' || status === 'completed') return 'ok';
  if (status === 'failed') return 'critical';
  if (status === 'starting' || status === 'stopping') return 'warning';
  return 'neutral';
}

function serviceHealthState(service) {
  if (!service) return { label: 'unknown', tone: 'warning', stale: true };
  if (service.health_status) {
    if (service.health_status === 'offline') return { label: 'offline', tone: 'critical', stale: true };
    if (service.health_status === 'no_heartbeat') return { label: `${service.status || 'registered'} / no heartbeat`, tone: 'warning', stale: true };
    if (service.health_status === 'stale') return { label: `${service.status || 'unknown'} / stale ${formatDuration(service.heartbeat_age_sec || 0)}`, tone: 'warning', stale: true };
    if (service.health_status === 'healthy') return { label: service.status || 'healthy', tone: service.status === 'online' ? 'ok' : 'warning', stale: false };
  }
  if (service.status === 'offline') return { label: 'offline', tone: 'critical', stale: true };
  if (!service.last_heartbeat_at) return { label: `${service.status || 'registered'} / no heartbeat`, tone: 'warning', stale: true };
  const ageSec = heartbeatAgeSec(service.last_heartbeat_at);
  if (!Number.isFinite(ageSec)) return { label: service.status || 'unknown', tone: 'warning', stale: true };
  if (ageSec > heartbeatStaleAfterSec) return { label: `${service.status || 'unknown'} / stale ${formatDuration(ageSec)}`, tone: 'warning', stale: true };
  return { label: service.status || 'unknown', tone: service.status === 'online' ? 'ok' : 'warning', stale: false };
}

function heartbeatAgeSec(value) {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) return Number.NaN;
  return Math.max(0, Math.floor((Date.now() - time) / 1000));
}

function heartbeatLabel(value) {
  if (!value) return 'never';
  const ageSec = heartbeatAgeSec(value);
  if (!Number.isFinite(ageSec)) return formatDateTime(value);
  return `${formatDateTime(value)} (${formatDuration(ageSec)} ago)`;
}

function formatDuration(totalSeconds) {
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const minutes = Math.floor(totalSeconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

function displayValue(value) {
  if (value === undefined || value === null || value === '') return '-';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (Array.isArray(value)) return value.join(', ');
  if (typeof value === 'object') return safeJSON(value);
  return String(value);
}

async function apiRequest(path, options) {
  if (demoAPIEnabled) {
    return { ok: true, status: 200, body: demoMutationResponse(path, options) };
  }
  try {
    const response = await fetch(path, { credentials: 'same-origin', ...options });
    let body = null;
    const contentType = response.headers.get('content-type') || '';
    if (contentType.includes('application/json')) {
      body = await response.json();
    }
    if (!response.ok) {
      return { ok: false, status: response.status, body, message: controlPanelErrorMessage(body, response.status) };
    }
    return { ok: true, status: response.status, body };
  } catch {
    return { ok: false, status: 0, body: null, message: 'Unable to reach the Control Panel API.' };
  }
}

function demoMutationResponse(path) {
  if (path === '/api-tokens' || path.includes('/api-tokens/') && path.endsWith('/rotate')) {
    return {
      id: 'service-token-demo',
      service_type: 'worker',
      scopes: defaultScopesByServiceType.worker,
      token: 'ast_svc_demo_one_time_token',
      created_at: new Date().toISOString(),
    };
  }
  if (path.endsWith('/start-readiness')) {
    return {
      ready: false,
      issues: [
        {
          code: 'discord_audio_forward_unavailable',
          service_type: 'discord_bot',
          service_id: 'discord-bot-demo',
          message: 'Demo: Discord Bot audio forwarding is currently inactive. Review Discord Bot audio forward and Encoder audio-status.',
        },
      ],
    };
  }
  if (path.includes('/worker-events/test')) {
    return { status: 'accepted' };
  }
  return { status: 'demo_accepted' };
}

function formatConfig(value) {
  return JSON.stringify(value || {}, null, 2);
}

function formatNumber(value, digits) {
  return new Intl.NumberFormat('en-US', { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(value);
}

function formatDB(value) {
  if (!Number.isFinite(value)) return '-';
  return `${formatNumber(value, 1)} dB`;
}

function formatBytes(value) {
  if (!Number.isFinite(value) || value <= 0) return '-';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let index = 0;
  let amount = value;
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024;
    index += 1;
  }
  return `${formatNumber(amount, amount >= 10 ? 1 : 2)} ${units[index]}`;
}

function formatDurationSeconds(value) {
  if (!Number.isFinite(value) || value <= 0) return '-';
  if (value < 60) return `${formatNumber(value, 1)} sec`;
  const minutes = Math.floor(value / 60);
  const seconds = Math.round(value % 60);
  return `${minutes} min ${seconds} sec`;
}

function formatDurationMillis(value) {
  if (!Number.isFinite(value) || value < 0) return '-';
  if (value < 1000) return `${formatNumber(value, value >= 10 ? 0 : 1)} ms`;
  return formatDurationSeconds(value / 1000);
}

function formatState(value) {
  return value >= 1 ? 'ok' : 'failed';
}

function metricStatusTone(value) {
  return value >= 1 ? 'ok' : 'critical';
}

function formatExists(value) {
  return value >= 1 ? 'exists' : 'missing';
}

function existsTone(value) {
  return value >= 1 ? 'ok' : 'warning';
}

function formatDateTime(value) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

function safeJSON(value) {
  if (value === undefined || value === null) return '-';
  try {
    return JSON.stringify(value);
  } catch {
    return '[unavailable]';
  }
}

function csrfToken() {
  return window.__AUTOSTREAM_CSRF_TOKEN__ || sessionStorage.getItem('autostream.csrf_token') || document.querySelector('meta[name="csrf-token"]')?.content || '';
}

function setCSRFToken(value) {
  if (!value) return;
  window.__AUTOSTREAM_CSRF_TOKEN__ = value;
  sessionStorage.setItem('autostream.csrf_token', value);
}

function clearCSRFToken() {
  window.__AUTOSTREAM_CSRF_TOKEN__ = '';
  sessionStorage.removeItem('autostream.csrf_token');
}

function subtitleFor(page, t = (value) => value) {
  if (page === 'dashboard') return t('Live operations, service health, and recent incidents.');
  if (observabilityPages.has(page)) return t('Data is proxied from autostream-observability.');
  return t('Administrative configuration and stream operations.');
}

createRoot(document.getElementById('root')).render(
  <I18nProvider>
    <App />
  </I18nProvider>,
);
