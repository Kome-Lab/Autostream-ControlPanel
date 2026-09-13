import type { NodeRegistrationResponse, WorkerNode } from "@/types/domain";
import { baseTime, mockWorkers } from "./mock-state";


export function mockConfigureCommand(serviceType: string, nodeID: string, configureToken: string) {
  const configureBinary = mockConfigureBinary(serviceType);
  if (serviceType === "update_agent") {
    return `sudo ${configureBinary} configure --panel-url "https://control.example.jp" --node "${nodeID}" --config "${mockConfigPath(serviceType)}"`;
  }
  return `sudo ${configureBinary} configure --panel-url "https://control.example.jp" --token "${configureToken}" --node "${nodeID}" --config "${mockConfigPath(serviceType)}"`;
}

function mockConfigureBinary(serviceType: string) {
  switch (serviceType) {
    case "update_agent":
      return "/usr/local/bin/autostream-host-agent";
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
    case "update_agent":
      return "/etc/autostream/updater/agent.yaml";
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

export function mockUpdaterConfigurationMetadata() {
  return {
    configuration_path: "/etc/autostream/updater/agent.yaml",
  };
}

export function isMockPullHostAgent(node: WorkerNode) {
  return node.service_type === "update_agent" && node.transport_mode === "pull_v2";
}

export function mockEndpointlessPullHostAgent(node: WorkerNode) {
  if (!isMockPullHostAgent(node)) return node;
  const endpointless = { ...node };
  delete endpointless.host;
  delete endpointless.port;
  delete endpointless.ssl_enabled;
  delete endpointless.public_url;
  delete endpointless.desired_endpoint;
  delete endpointless.applied_endpoint;
  delete endpointless.reported_endpoint;
  delete endpointless.endpoint_revision;
  delete endpointless.endpoint_status;
  return endpointless;
}

function mockStreamIngestConfigYAML(serviceType: string) {
  if (serviceType !== "worker" && serviceType !== "encoder_recorder") return "";
  return `\n\nstream_ingest:\n  signing_key: "<stream-ingest-signing-key>"`;
}


export function postMockNodeRegistration(body?: unknown): unknown {
    const request = body as Partial<{
      node_type: string;
      node_id: string;
      name: string;
      description: string;
      host: string;
      port: number;
      ssl_enabled: boolean;
      transport_mode: "pull_v2";
      execution_host_id: string;
    }>;
    const nodeID = request.node_id || `${request.node_type || "worker"}-new`;
    const configureToken = "ast_cfg_demo_9d2b4b5fd4e3c0a7";
    const runtimeToken = "ast_svc_demo_8e1f2c6a4b0d9f7e";
    const host = request.host || "worker-new.example.jp";
    const port = request.port || 8081;
    const sslEnabled = request.ssl_enabled ?? true;
    const scheme = sslEnabled ? "https" : "http";
    const pullHostAgent = request.node_type === "update_agent";
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
      configuration_yaml: pullHostAgent
        ? `{"panel_url":"https://control.example.jp","node_id":"${nodeID}","runtime_token":"${runtimeToken}","service_name":"${request.name || "Host Agent"}"}`
        : `panel:\n  url: "https://control.example.jp"\n\nnode:\n  id: "${nodeID}"\n  name: "${request.name || "新規Node"}"\n  type: "${request.node_type || "worker"}"\n\napi:\n  host: "${host}"\n  port: ${port}\n  ssl_enabled: ${sslEnabled}\n\nauth:\n  token_id: "runtime-token-demo"\n  token: "${runtimeToken}"${mockStreamIngestConfigYAML(request.node_type || "worker")}\n`,
      node: {
        id: nodeID,
        service_id: nodeID,
        service_type: request.node_type || "worker",
        service_name: request.name || "新規Node",
        status: "pending",
        health_status: "pending",
        description: request.description || "",
        ...(pullHostAgent
          ? {
              transport_mode: "pull_v2" as const,
              execution_host_id: request.execution_host_id,
              ownership_epoch: 1,
            }
          : {
              host,
              port,
              ssl_enabled: sslEnabled,
              public_url: `${scheme}://${host}:${port}`,
            }),
        reported_version: "",
        reported_capabilities: {},
      },
    };
    if (response.service_type === "update_agent") {
      response.scopes = ["service.register", "service.heartbeat", "service.config.read", "service.status.write", "updates.claim", "updates.report", "updates.authorize"];
      delete response.configuration_yaml;
      Object.assign(response, mockUpdaterConfigurationMetadata());
    }
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


export function postMockConfigureToken(configureTokenRotate: RegExpMatchArray): unknown {
    const nodeID = decodeURIComponent(configureTokenRotate[1]);
    const node = mockWorkers.find((item) => (item.service_id || item.id) === nodeID) || mockWorkers[0];
    const configureToken = "ast_cfg_demo_rotated_7c8f1a2d";
    node.configure_token_expires_at = baseTime;
    node.configure_token_used_at = undefined;
    return {
      node: mockEndpointlessPullHostAgent(node),
      configure_token: configureToken,
      configure_token_expires_at: baseTime,
      configure_command: mockConfigureCommand(node.service_type, node.service_id || node.id, configureToken),
      ...(node.service_type === "update_agent" ? mockUpdaterConfigurationMetadata() : {}),
    };
  }


export function postMockRuntimeToken(runtimeTokenRotate: RegExpMatchArray): unknown {
    const nodeID = decodeURIComponent(runtimeTokenRotate[1]);
    const node = mockWorkers.find((item) => (item.service_id || item.id) === nodeID) || mockWorkers[0];
    const runtimeToken = "ast_svc_demo_rotated_2f6d0b8e";
    const runtimeTokenID = "runtime-token-demo-rotated";
    const host = node.host || "worker-main.example.jp";
    const port = node.port || 8443;
    const sslEnabled = node.ssl_enabled ?? true;
    node.node_token_rotated_at = baseTime;
    if (node.service_type === "update_agent") {
      return {
        node: mockEndpointlessPullHostAgent(node),
        runtime_token_id: runtimeTokenID,
        runtime_token: runtimeToken,
        ...mockUpdaterConfigurationMetadata(),
      };
    }
    return {
      node,
      runtime_token_id: runtimeTokenID,
      runtime_token: runtimeToken,
      configuration_yaml: `panel:\n  url: "https://control.example.jp"\n\nnode:\n  id: "${node.service_id || node.id}"\n  name: "${node.service_name}"\n  type: "${node.service_type}"\n\napi:\n  host: "${host}"\n  port: ${port}\n  ssl_enabled: ${sslEnabled}\n\nauth:\n  token_id: "${runtimeTokenID}"\n  token: "${runtimeToken}"${mockStreamIngestConfigYAML(node.service_type)}\n`,
    };
  }
