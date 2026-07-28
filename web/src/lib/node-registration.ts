import type { NodeServiceEndpoint, WorkerNode } from "@/types/domain";

export type UpdaterTransportMode = "ssh_v1" | "pull_v2";

export type NodeRegistrationDraft = {
  nodeType: string;
  nodeID: string;
  name: string;
  description: string;
  host: string;
  port: string;
  sslEnabled: boolean;
  allowRuntimeSecrets: boolean;
  allowRemediation: boolean;
  transportMode: UpdaterTransportMode;
  executionHostID: string;
};

const nodeIDPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const executionHostIDPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,190}$/;
const decimalPortPattern = /^[0-9]+$/;

export function isServicePort(value: string | number) {
  const raw = String(value).trim();
  if (!decimalPortPattern.test(raw)) return false;
  const port = Number(raw);
  return Number.isSafeInteger(port) && port >= 1024 && port <= 65535;
}

export function isExecutionHostID(value: string) {
  return executionHostIDPattern.test(value);
}

export function nodeRegistrationDraftValid(draft: NodeRegistrationDraft) {
  if (!nodeIDPattern.test(draft.nodeID.trim()) || draft.name.trim() === "") {
    return false;
  }
  if (draft.nodeType === "update_agent" && draft.transportMode === "pull_v2") {
    return isExecutionHostID(draft.executionHostID);
  }
  return draft.host.trim() !== "" && isServicePort(draft.port);
}

export function buildNodeRegistrationRequest(draft: NodeRegistrationDraft) {
  const common = {
    node_type: draft.nodeType,
    node_id: draft.nodeID.trim(),
    name: draft.name.trim(),
    description: draft.description.trim(),
    allow_runtime_secrets: draft.allowRuntimeSecrets,
    allow_remediation: draft.allowRemediation,
  };
  if (draft.nodeType === "update_agent" && draft.transportMode === "pull_v2") {
    return {
      ...common,
      transport_mode: "pull_v2" as const,
      execution_host_id: draft.executionHostID,
    };
  }
  return {
    ...common,
    host: draft.host.trim(),
    port: Number(draft.port.trim()),
    ssl_enabled: draft.sslEnabled,
    ...(draft.nodeType === "update_agent"
      ? { transport_mode: "ssh_v1" as const }
      : {}),
  };
}

export type NodeEndpointBadgeTone = "default" | "secondary" | "destructive" | "outline";

export type NodeEndpointSnapshot = {
  url: string;
  source: "structured" | "legacy" | "missing";
};

export type NodeEndpointState = {
  kind: "endpoint" | "pull_v2";
  transportMode: string;
  executionHostID: string;
  ownershipEpoch?: number;
  desired: NodeEndpointSnapshot;
  applied: NodeEndpointSnapshot;
  reported: NodeEndpointSnapshot;
  revision?: number;
  status: {
    raw: string;
    label: string;
    detail: string;
    tone: NodeEndpointBadgeTone;
  };
};

export function nodeServiceEndpointURL(endpoint?: NodeServiceEndpoint) {
  if (!endpoint) return "";
  const publicURL = endpoint.public_url?.trim();
  if (publicURL) return publicURL;
  const host = endpoint.host?.trim();
  const port = Number(endpoint.port);
  if (!host || !Number.isSafeInteger(port) || port < 1 || port > 65535) return "";
  return `${endpoint.ssl_enabled ? "https" : "http"}://${host}:${port}`;
}

export function nodeEndpointState(node: WorkerNode): NodeEndpointState {
  if (node.service_type === "update_agent" && node.transport_mode === "pull_v2") {
    return {
      kind: "pull_v2",
      transportMode: "pull_v2",
      executionHostID: node.execution_host_id?.trim() || "",
      ownershipEpoch: positiveInteger(node.ownership_epoch),
      desired: missingEndpoint(),
      applied: missingEndpoint(),
      reported: missingEndpoint(),
      revision: positiveInteger(node.endpoint_revision),
      status: nodeEndpointStatusPresentation(node.endpoint_status),
    };
  }

  const hasStructuredEndpoint = Boolean(
    node.desired_endpoint
    || node.applied_endpoint
    || node.reported_endpoint,
  );
  const legacyApplied = hasStructuredEndpoint ? undefined : legacyNodeEndpoint(node);
  return {
    kind: "endpoint",
    transportMode: node.transport_mode?.trim() || "legacy",
    executionHostID: node.execution_host_id?.trim() || "",
    ownershipEpoch: positiveInteger(node.ownership_epoch),
    desired: endpointSnapshot(node.desired_endpoint, "structured"),
    applied: node.applied_endpoint
      ? endpointSnapshot(node.applied_endpoint, "structured")
      : endpointSnapshot(legacyApplied, legacyApplied ? "legacy" : "missing"),
    reported: endpointSnapshot(node.reported_endpoint, "structured"),
    revision: positiveInteger(node.endpoint_revision),
    status: nodeEndpointStatusPresentation(node.endpoint_status || (legacyApplied ? "applied" : "")),
  };
}

export function nodeEndpointStatusPresentation(status?: string): NodeEndpointState["status"] {
  const raw = String(status || "").trim().toLowerCase();
  const states: Record<string, Omit<NodeEndpointState["status"], "raw">> = {
    applied: {
      label: "反映済み",
      detail: "endpointの反映処理は完了しています。",
      tone: "default",
    },
    pending: {
      label: "反映待ち",
      detail: "希望値はまだ反映済みendpointではありません。",
      tone: "secondary",
    },
    applying: {
      label: "反映中",
      detail: "endpointの変更を反映しています。",
      tone: "secondary",
    },
    drift: {
      label: "差分あり",
      detail: "反映済みendpointとNode報告値に差分があります。",
      tone: "destructive",
    },
    rollback: {
      label: "ロールバック中",
      detail: "以前のendpointへ戻しています。",
      tone: "secondary",
    },
    rolling_back: {
      label: "ロールバック中",
      detail: "以前のendpointへ戻しています。",
      tone: "secondary",
    },
    rollback_pending: {
      label: "ロールバック待ち",
      detail: "以前のendpointへ戻す処理を待っています。",
      tone: "secondary",
    },
    rolled_back: {
      label: "ロールバック済み",
      detail: "以前のendpointへ戻しました。",
      tone: "outline",
    },
    rollback_failed: {
      label: "ロールバック失敗",
      detail: "以前のendpointへ戻せませんでした。Nodeを確認してください。",
      tone: "destructive",
    },
    blocked: {
      label: "変更ブロック",
      detail: "安全条件を満たさないためendpoint変更を停止しています。",
      tone: "destructive",
    },
    failed: {
      label: "反映失敗",
      detail: "endpointを反映できませんでした。Nodeを確認してください。",
      tone: "destructive",
    },
  };
  if (!raw) {
    return {
      raw: "",
      label: "未報告",
      detail: "endpoint状態は未報告です。",
      tone: "outline",
    };
  }
  const known = states[raw];
  if (known) return { raw, ...known };
  return {
    raw,
    label: `状態不明 (${raw})`,
    detail: "Control Panelが認識していないendpoint状態です。",
    tone: "outline",
  };
}

function endpointSnapshot(
  endpoint: NodeServiceEndpoint | undefined,
  source: NodeEndpointSnapshot["source"],
): NodeEndpointSnapshot {
  const url = nodeServiceEndpointURL(endpoint);
  return url ? { url, source } : missingEndpoint();
}

function missingEndpoint(): NodeEndpointSnapshot {
  return { url: "", source: "missing" };
}

function legacyNodeEndpoint(node: WorkerNode): NodeServiceEndpoint | undefined {
  const publicURL = node.public_url?.trim() || "";
  const host = node.host?.trim() || "";
  const port = Number(node.port);
  if (!publicURL && (!host || !Number.isSafeInteger(port) || port < 1 || port > 65535)) {
    return undefined;
  }
  return {
    host,
    port: Number.isSafeInteger(port) ? port : 0,
    ssl_enabled: Boolean(node.ssl_enabled),
    public_url: publicURL,
  };
}

function positiveInteger(value?: number) {
  return Number.isSafeInteger(value) && Number(value) > 0 ? Number(value) : undefined;
}
