import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { readMovedSource } from "./helpers/moved-source.mts";
import type { WorkerNode } from "../src/types/domain.ts";
import { mockGet, mockPost, mockPut } from "./system-updates-fixture.mts";
import { buildNodeRegistrationRequest, isExecutionHostID, isServicePort, nodeEndpointState, nodeEndpointStatusPresentation, nodeRegistrationDraftValid, nodeServiceEndpointURL } from "../src/lib/node-registration.ts";


export function registerNodeAuthorityCases() {


test("node endpoint presentation distinguishes desired, applied, reported, legacy, and pull ownership", () => {
  const baseNode: WorkerNode = {
    id: "worker-endpoint",
    service_type: "worker",
    service_name: "Endpoint Worker",
    status: "online",
    health_status: "healthy",
  };
  const structured = nodeEndpointState({
    ...baseNode,
    host: "legacy-must-not-be-current.example.com",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://legacy-must-not-be-current.example.com:8443",
    desired_endpoint: {
      host: "desired.example.com",
      port: 18084,
      ssl_enabled: true,
      public_url: "https://desired.example.com:18084",
    },
    applied_endpoint: {
      host: "applied.example.com",
      port: 8084,
      ssl_enabled: false,
      public_url: "http://applied.example.com:8084",
    },
    reported_endpoint: {
      host: "reported.example.com",
      port: 28084,
      ssl_enabled: true,
      public_url: "https://reported.example.com:28084",
    },
    endpoint_revision: 4,
    endpoint_status: "pending",
  });
  assert.equal(structured.kind, "endpoint");
  assert.deepEqual(structured.desired, { url: "https://desired.example.com:18084", source: "structured" });
  assert.deepEqual(structured.applied, { url: "http://applied.example.com:8084", source: "structured" });
  assert.deepEqual(structured.reported, { url: "https://reported.example.com:28084", source: "structured" });
  assert.equal(structured.revision, 4);
  assert.equal(structured.status.label, "反映待ち");
  assert.equal(structured.status.tone, "secondary");
  assert.notEqual(structured.desired.url, structured.applied.url, "desired must never be presented as the applied endpoint");

  const legacy = nodeEndpointState({
    ...baseNode,
    host: "legacy.example.com",
    port: 8443,
    ssl_enabled: true,
    public_url: "https://legacy.example.com:8443",
  });
  assert.deepEqual(legacy.desired, { url: "", source: "missing" });
  assert.deepEqual(legacy.applied, { url: "https://legacy.example.com:8443", source: "legacy" });
  assert.deepEqual(legacy.reported, { url: "", source: "missing" });
  assert.equal(legacy.status.label, "反映済み");

  const partialStructured = nodeEndpointState({
    ...baseNode,
    host: "legacy.example.com",
    port: 8443,
    ssl_enabled: true,
    desired_endpoint: {
      host: "desired.example.com",
      port: 18084,
      ssl_enabled: true,
      public_url: "",
    },
  });
  assert.equal(partialStructured.desired.url, "https://desired.example.com:18084");
  assert.deepEqual(partialStructured.applied, { url: "", source: "missing" });
  assert.equal(partialStructured.status.label, "未報告");

  const pull = nodeEndpointState({
    ...baseNode,
    id: "host-agent-a",
    service_type: "update_agent",
    transport_mode: "pull_v2",
    execution_host_id: "host-a",
    ownership_epoch: 7,
    host: "must-be-ignored.example.com",
    port: 8090,
    public_url: "https://must-be-ignored.example.com:8090",
  });
  assert.equal(pull.kind, "pull_v2");
  assert.equal(pull.transportMode, "pull_v2");
  assert.equal(pull.executionHostID, "host-a");
  assert.equal(pull.ownershipEpoch, 7);
  assert.deepEqual(pull.applied, { url: "", source: "missing" });

  assert.equal(nodeServiceEndpointURL({
    host: "fallback.example.com",
    port: 18081,
    ssl_enabled: false,
    public_url: "",
  }), "http://fallback.example.com:18081");
  assert.deepEqual(
    ["applied", "pending", "drift", "rollback", "blocked", "rollback_failed", ""].map((status) => {
      const presentation = nodeEndpointStatusPresentation(status);
      return [presentation.label, presentation.tone];
    }),
    [
      ["反映済み", "default"],
      ["反映待ち", "secondary"],
      ["差分あり", "destructive"],
      ["ロールバック中", "secondary"],
      ["変更ブロック", "destructive"],
      ["ロールバック失敗", "destructive"],
      ["未報告", "outline"],
    ],
  );
  assert.equal(nodeEndpointStatusPresentation("future_state").label, "状態不明 (future_state)");
});

test("pull host agent registration is endpointless and ordinary node ports use the unprivileged range", () => {
  const base = {
    nodeType: "update_agent",
    nodeID: "host-agent-a",
    name: "Host Agent A",
    description: "Host A",
    host: "must-not-leak.example.com",
    port: "8090",
    sslEnabled: true,
    allowRuntimeSecrets: false,
    allowRemediation: false,
    transportMode: "pull_v2" as const,
    executionHostID: "host-a",
  };
  const pull = buildNodeRegistrationRequest(base);
  assert.deepEqual(pull, {
    node_type: "update_agent",
    node_id: "host-agent-a",
    name: "Host Agent A",
    description: "Host A",
    allow_runtime_secrets: false,
    allow_remediation: false,
    transport_mode: "pull_v2",
    execution_host_id: "host-a",
  });
  assert.equal(nodeRegistrationDraftValid(base), true);
  assert.equal(isExecutionHostID("host-a"), true);
  assert.equal(isExecutionHostID(" bad host "), false);

  const worker = {
    ...base,
    nodeType: "worker",
    nodeID: "worker-a",
    name: "Worker A",
    host: "worker.example.com",
    port: "18084",
    transportMode: "pull_v2" as const,
    executionHostID: "",
  };
  assert.deepEqual(buildNodeRegistrationRequest(worker), {
    node_type: "worker",
    node_id: "worker-a",
    name: "Worker A",
    description: "Host A",
    host: "worker.example.com",
    port: 18084,
    ssl_enabled: true,
    allow_runtime_secrets: false,
    allow_remediation: false,
  });
  assert.equal(nodeRegistrationDraftValid(worker), true);
  assert.equal(isServicePort("1023"), false);
  assert.equal(isServicePort("1024"), true);
  assert.equal(isServicePort("65535"), true);
  assert.equal(isServicePort("65536"), false);

  const assertEndpointlessNode = (value: unknown) => {
    const node = value as Record<string, unknown>;
    for (const field of [
      "host",
      "port",
      "ssl_enabled",
      "public_url",
      "desired_endpoint",
      "applied_endpoint",
      "reported_endpoint",
      "endpoint_revision",
      "endpoint_status",
    ]) {
      assert.equal(field in node, false, `pull_v2 node must not expose ${field}`);
    }
  };
  const assertEndpointlessResponse = (value: unknown) => {
    const response = value as { node?: Record<string, unknown>; node_api_url?: unknown };
    assert.equal("node_api_url" in response, false);
    assert.ok(response.node);
    assertEndpointlessNode(response.node);
    assert.doesNotMatch(JSON.stringify(response), /must-not-leak\.example\.com|8090/);
  };
  const mockPull = mockPost("/nodes/registration-tokens", {
    ...pull,
    host: "must-not-leak.example.com",
    port: 8090,
    ssl_enabled: true,
  });
  assertEndpointlessResponse(mockPull);
  assertEndpointlessResponse(mockGet("/nodes/host-agent-a/configuration"));
  assertEndpointlessResponse(mockPost("/nodes/host-agent-a/configure-token"));
  assertEndpointlessResponse(mockPost("/nodes/host-agent-a/rotate-token"));
  const updatedMockPull = mockPut("/nodes/host-agent-a", {
    service_name: "Host Agent A renamed",
    description: "Updated host agent",
    host: "must-not-return.example.com",
    port: 8090,
    ssl_enabled: false,
  }) as Record<string, unknown>;
  assert.equal(updatedMockPull.service_name, "Host Agent A renamed");
  assertEndpointlessNode(updatedMockPull);
  assert.doesNotMatch(JSON.stringify(updatedMockPull), /must-not-return\.example\.com|8090/);
});

test("updater configure failure guidance requires a fresh token before restart", () => {
  const source = readMovedSource(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url));

  assert.match(source, /設定処理が失敗または結果不確定の場合は、対象サービスを再起動しないでください。/);
  assert.match(source, /新しいConfigure Tokenを発行し、同じtoken-free commandを新しいTokenで再実行/);
  assert.doesNotMatch(source, /失敗または結果不確定の場合も旧Runtime Tokenは維持/);
  assert.doesNotMatch(source, /同じコマンドで再開|再生成を求められた場合だけ/);
});

test("Host Agent configure delegates managed policy to the system update screen", () => {
  const source = readMovedSource(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url));

  assert.match(source, /このHost Agentを稼働させる対象ホストで1回実行/);
  assert.match(source, /Host Agentの実行対象とpolicyは「アプリケーション情報」で設定/);
  assert.match(source, /受信API endpointや専用portは作成しません/);
  assert.doesNotMatch(source, /中央Updater|updater\.json|known_hosts|--init-from|JSON手動設定/);
});

test("updater node description identifies its portless per-host responsibility", () => {
  const source = readMovedSource(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url));

  assert.match(source, /value: "update_agent"[^{}\r\n]*description: "ホスト単位の更新状態をControl Panelへ外向き接続で報告するHost Agent"/);
  assert.match(source, /受信listenerは作成しません/);
  assert.match(source, /Host Pull Agent/);
  assert.match(source, /if \(isPullHostAgent\) return ""/);
  assert.match(source, /configuration\.node\?\.service_type !== "update_agent" && configuration\.node_api_url/);
  assert.match(source, /受信ポートなし（Outbound HTTPS）/);
  assert.match(source, /Host Agentの初期設定/);
  assert.match(source, /このHost Agentを稼働させる対象ホストで1回実行/);
  assert.match(source, /受信API endpointや専用portは作成しません/);
  assert.match(source, /transport_mode: \{state\.transportMode\}/);
  assert.match(source, /execution_host_id: \{state\.executionHostID \|\| "未報告"\}/);
  assert.match(source, /ownership_epoch: \{state\.ownershipEpoch \?\? "未報告"\}/);
  assert.match(source, /label="希望値"/);
  assert.match(source, /反映済み \(legacy\)/);
  assert.match(source, /label="Node報告"/);
  assert.match(source, /Revision \{state\.revision \?\? "未報告"\}/);
  assert.doesNotMatch(source, /port_reconfigure/);
  assert.doesNotMatch(source, /表示されたコマンドを中央Updaterホストで/);
});

test("registered node lists use responsive cards instead of forced-width tables", () => {
  const source = readMovedSource(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url));
  const tableSource = readFileSync(new URL("../src/components/tables/data-table.tsx", import.meta.url), "utf8");

  assert.match(source, /<DataTable[\s\S]*?responsive\s*\/>/);
  assert.doesNotMatch(source, /minTableWidthClass="min-w-\[960px\]"/);
  assert.match(tableSource, /responsive\?: boolean/);
  // UI renewal §6.3: desktop and mobile reuse a single record; secondary
  // endpoint information remains reachable through the disclosure control.
  const recordSource = readFileSync(new URL("../src/components/tables/table-record.tsx", import.meta.url), "utf8");
  assert.match(tableSource, /<TableRecord key=\{row.id\} row=\{row\}/);
  assert.match(recordSource, /data-priority=\{priority > 1 \? "secondary" : "primary"\}/);
  assert.match(recordSource, /aria-expanded=\{expanded\}/);
  assert.equal((recordSource.match(/flexRender\(cell.column.columnDef.cell/g) || []).length, 1);
});
}
