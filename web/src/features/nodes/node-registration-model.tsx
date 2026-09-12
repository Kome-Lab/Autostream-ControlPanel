"use client";

import { APIError } from "@/lib/api/client";
import type { WorkerNode } from "@/types/domain";

export const nodeTypes = [
  { value: "worker", label: "Worker Node Agent", defaultPort: 8084, runtimeSecretsRequired: false, description: "番組配信と録画を担当するWorker Node Agent" },
  { value: "encoder_recorder", label: "Encoder / Recorder Node Agent", defaultPort: 8081, runtimeSecretsRequired: true, description: "映像のエンコードと録画を担当するNode Agent" },
  { value: "discord_bot", label: "Discord Bot Node Agent", defaultPort: 8083, runtimeSecretsRequired: false, description: "Discordの音声取得と配信操作を担当するNode Agent" },
  { value: "observability", label: "Observability Node Agent", defaultPort: 8082, runtimeSecretsRequired: false, description: "メトリクス、インシデント、通知を担当するNode Agent" },
  { value: "update_agent", label: "AutoStream Updater / Host Agent", defaultPort: 8090, runtimeSecretsRequired: false, description: "ホスト単位の更新状態をControl Panelへ外向き接続で報告するHost Agent" },
];

export type NodeConfigurationResponse = {
  node?: WorkerNode;
  node_api_url?: string;
  token?: string;
  configure_token?: string;
  configure_token_expires_at?: string;
  runtime_token_id?: string;
  runtime_token?: string;
  configure_command?: string;
  configuration_yaml?: string;
  configuration_path?: string;
  configuration_example?: string;
  manual_configuration_required?: boolean;
  systemd_unit?: string;
  scopes?: string[];
};

export type NodeEditForm = {
  service_name: string;
  description: string;
  host: string;
  port: string;
  ssl_enabled: boolean;
};

export type NodeRegistrationViewMode = "registration" | "registered" | "all";

export function nodeIdentity(node: WorkerNode) {
  return node.service_id || node.id;
}

export function nodeDisplayName(node: WorkerNode) {
  return node.service_name || "未設定のNode名";
}

export function isPullNode(node: WorkerNode) {
  return node.service_type === "update_agent" && node.transport_mode === "pull_v2";
}

export function nodeEditDefaults(node: WorkerNode): NodeEditForm {
  const parsed = parseNodePublicURL(node.public_url);
  return {
    service_name: node.service_name || "",
    description: node.description || "",
    host: node.host || parsed.host,
    port: node.port ? String(node.port) : parsed.port,
    ssl_enabled: node.ssl_enabled ?? parsed.ssl_enabled ?? true,
  };
}

function parseNodePublicURL(publicURL?: string) {
  if (!publicURL) return { host: "", port: "", ssl_enabled: true };
  try {
    const url = new URL(publicURL);
    const sslEnabled = url.protocol === "https:";
    return {
      host: url.hostname,
      port: url.port || (sslEnabled ? "443" : "80"),
      ssl_enabled: sslEnabled,
    };
  } catch {
    return { host: "", port: "", ssl_enabled: true };
  }
}

export function editNodeApiURL(form: NodeEditForm) {
  const host = form.host.trim();
  const port = Number.parseInt(form.port, 10);
  if (!host || !Number.isFinite(port) || port <= 0) return "";
  return `${form.ssl_enabled ? "https" : "http"}://${host}:${port}`;
}

export function nodeRegistrationErrorMessage(error: unknown) {
  if (!error) return "";
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      csrf_failed: "ログイン状態またはCSRF tokenが古くなっています。ページを再読み込みして、もう一度実行してください。",
      invalid_node_scope: "選択したNode権限の組み合わせが無効です。Runtime SecretsやRemediationのチェックを見直してください。",
      permission_escalation: "現在の権限では、このNodeに必要なscopeを発行できません。管理者権限または必要な個別権限を付与してください。",
      node_already_exists: "同じNode IDが既に存在します。別のNode IDにするか、既存NodeのConfigurationから再発行してください。",
      invalid_node_endpoint: "HostまたはPortが無効です。HostはURL全体ではなくFQDNまたはIPだけを入力してください。",
      node_endpoint_blocked: "Node Agent API URLがControl Panelのoutbound allowlistに入っていません。Control Panel envの AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS にこのHost、または *.example.jp のようなwildcardを追加して再起動してください。",
      invalid_node_registration: "Node ID、名前、Host、Portのいずれかが無効です。HostはURL全体ではなくFQDNまたはIPだけを入力し、Control Panelのoutbound allowlistも確認してください。",
      node_type_mismatch: "既存Nodeと異なるNode typeでは発行できません。Node typeとNode IDの組み合わせを確認してください。",
      not_found: "対象のNodeが見つかりません。一覧を更新してください。",
      service_not_found: "対象のNodeが見つかりません。一覧を更新してください。",
      permission_denied: "この操作に必要な権限がありません。Runtime Tokenを再生成できる管理者へ依頼してください。",
      store_node_runtime_token_failed: "Control Panelのenvに AUTOSTREAM_SECRET_ENCRYPTION_KEY が設定されていない、または暗号化設定が不正です。設定後にControl Panelを再起動してください。",
      stream_ingest_signing_key_required: "Control Panelのenvに AUTOSTREAM_STREAM_INGEST_SIGNING_KEY を設定して再起動してから、Worker / Encoder Nodeを作成してください。",
      stream_ingest_signing_key_invalid: "AUTOSTREAM_STREAM_INGEST_SIGNING_KEY は32バイト以上のランダム値にしてください。CHANGE_ME等のプレースホルダーは使用できません。",
      manual_configuration_required: "Control PanelまたはUpdaterが古く、自動設定に対応していません。両方を同じ新しいReleaseへ更新してください。",
      create_node_configure_token_failed: "Configure Tokenの保存に失敗しました。database接続とControl Panelのログを確認してください。",
      create_node_registration_token_failed: "Node Runtime Tokenの作成に失敗しました。Control Panelのログを確認してください。",
      rotate_node_runtime_token_failed: "Node Runtime Tokenの再生成に失敗しました。Control Panelのログを確認してください。",
      runtime_token_not_found: "現在のRuntime Tokenが見つかりません。Nodeを再作成するか、Control Panelのログを確認してください。",
      update_node_failed: "Nodeの更新に失敗しました。Control Panelのログを確認してください。",
      delete_service_failed: "Nodeの削除に失敗しました。割り当て状態とControl Panelのログを確認してください。",
      precreate_node_failed: "Nodeの作成に失敗しました。database接続とControl Panelのログを確認してください。",
    };
    return messages[error.code || ""] || "Node操作に失敗しました。最新状態とControl Panelのログを確認して再試行してください。";
  }
  if (error instanceof Error) return "Node操作に失敗しました。通信状態を確認して再試行してください。";
  return "不明なエラーが発生しました。Control Panelのログを確認してください。";
}
