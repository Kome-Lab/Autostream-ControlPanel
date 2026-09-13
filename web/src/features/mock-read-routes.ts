import type { CurrentUser } from "@/types/domain";
import { stripQuery, mockLoginOAuthProviders, mockDeleteCollectionPath } from "./mock-route-values";
import { mockUpdaterSettings, mockUpdaterHostBootstrapJobs, mockCurrentUser, mockAuditLogs, mockStreamArtifacts, mockStreams, mockArchiveShares, mockWorkers, mockMFAStatus, mockPasskeys, mockOAuthLinks, mockSetupStatus, mockAppSettings, mockAppVersion, mockSystemUpdateUpdaters, mockSystemUpdateHosts, mockSystemUpdateTargets, mockSystemUpdateJobs, mockResourceData } from "./mock-state";
import { loadMockArchiveShares, archiveShareKey, publicMockArchiveShareAdmin, publicMockArchiveShare } from "./mock-archive-routes";
import { isMockPullHostAgent, mockEndpointlessPullHostAgent, mockUpdaterConfigurationMetadata, mockConfigureCommand } from "./mock-node-routes";
import { mockWorkerMetrics } from "./mock-metrics";


export function mockGet(path: string): unknown {
  const normalizedPath = stripQuery(path);
  const updaterBootstrapJobs = normalizedPath.match(/^\/system-updates\/updaters\/([^/]+)\/bootstrap-jobs$/);
  if (updaterBootstrapJobs) {
    const updaterID = decodeURIComponent(updaterBootstrapJobs[1]);
    if (updaterID !== mockUpdaterSettings.updater_id) throw new Error("updater_not_found");
    return { jobs: structuredClone(mockUpdaterHostBootstrapJobs.filter((job) => job.updater_id === updaterID)) };
  }
  const updaterSettings = normalizedPath.match(/^\/system-updates\/updaters\/([^/]+)\/settings$/);
  if (updaterSettings) {
    const updaterID = decodeURIComponent(updaterSettings[1]);
    if (updaterID !== mockUpdaterSettings.updater_id) throw new Error("updater_not_found");
    return structuredClone(mockUpdaterSettings);
  }
  if (normalizedPath === "/auth/me") {
    return {
      user: { ...mockCurrentUser.user, roles: [...(mockCurrentUser.user.roles || [])] },
      permissions: [...mockCurrentUser.permissions],
    } satisfies CurrentUser;
  }
  if (normalizedPath === "/audit-logs") {
    const query = path.includes("?") ? path.slice(path.indexOf("?") + 1) : "";
    const params = new URLSearchParams(query);
    const search = String(params.get("q") || "").trim().toLowerCase();
    const result = String(params.get("result") || "").trim().toLowerCase();
    const actionGroup = String(params.get("action_group") || "").trim();
    const excludedActionGroup = String(params.get("exclude_action_group") || "").trim();
    const nodeActivityActions = ["services.register", "services.runtime_config.read", "services.heartbeat", "observability.signals.ingest", "archive.artifacts.reported"];
    return mockAuditLogs.filter((event) => {
      if (actionGroup === "service_runtime_reads" && !["services.register", "services.runtime_config.read"].includes(event.action)) return false;
      if (excludedActionGroup === "service_runtime_reads" && ["services.register", "services.runtime_config.read"].includes(event.action)) return false;
      if (actionGroup === "node_activity" && !nodeActivityActions.includes(event.action)) return false;
      if (excludedActionGroup === "node_activity" && nodeActivityActions.includes(event.action)) return false;
      if (result && String(event.result || "").toLowerCase() !== result) return false;
      if (!search) return true;
      return [event.id, event.action, event.actor_username, event.actor_ip, event.user_agent, event.result, event.resource_type, event.resource_id]
        .some((value) => String(value || "").toLowerCase().includes(search));
    });
  }
  if (normalizedPath === "/stream-logs") return [];
  const streamArtifacts = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts$/);
  if (streamArtifacts) {
    return mockStreamArtifacts[decodeURIComponent(streamArtifacts[1])] || [];
  }
  if (normalizedPath === "/archive/streams") {
    return mockStreams.filter((stream) => (mockStreamArtifacts[stream.id] || []).length > 0);
  }
  if (normalizedPath === "/archive/processing-streams") {
    return mockStreams.filter((stream) =>
      ["stopping", "completed"].includes(String(stream.status).toLowerCase())
      && Boolean(stream.archive_profile_id)
      && !stream.deleted_at
      && (mockStreamArtifacts[stream.id] || []).length === 0,
    );
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
    if (isMockPullHostAgent(node)) {
      return {
        node: mockEndpointlessPullHostAgent(node),
        ...mockUpdaterConfigurationMetadata(),
      };
    }
    const host = node.host || "worker-main.example.jp";
    const port = node.port || 8443;
    const sslEnabled = node.ssl_enabled ?? true;
    const nodeApiUrl = `${sslEnabled ? "https" : "http"}://${host}:${port}`;
    if (node.service_type === "update_agent") {
      return {
        node,
        node_api_url: nodeApiUrl,
        ...mockUpdaterConfigurationMetadata(),
      };
    }
    return {
      node,
      node_api_url: nodeApiUrl,
      configure_command: mockConfigureCommand(node.service_type, node.service_id || node.id, "<regenerate-configure-token>"),
      configuration_yaml: `panel:\n  url: "https://control.example.jp"\n\nnode:\n  id: "${node.service_id || node.id}"\n  name: "${node.service_name}"\n  type: "${node.service_type}"\n\napi:\n  host: "${host}"\n  port: ${port}\n  ssl_enabled: ${sslEnabled}\n\nauth:\n  token_id: "<runtime-token-id>"\n  token: "<regenerate-runtime-token>"\n`,
    };
  }
  const dataByPath: Record<string, unknown> = {
    "/auth/mfa/status": mockMFAStatus,
    "/auth/passkeys": mockPasskeys,
    "/auth/oauth-links": mockOAuthLinks,
    "/auth/oauth/providers": mockLoginOAuthProviders(),
    "/setup/status": mockSetupStatus,
    "/settings/app": mockAppSettings,
    "/settings/app/manage": mockAppSettings,
    "/version": mockAppVersion,
    "/system-updates": { updaters: mockSystemUpdateUpdaters, hosts: mockSystemUpdateHosts, targets: mockSystemUpdateTargets, jobs: mockSystemUpdateJobs },
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

export function mockPathExists(path: string) {
  const normalizedPath = stripQuery(path);
  if (/^\/system-updates\/updaters\/[^/]+\/bootstrap-jobs$/.test(normalizedPath)) return true;
  if (/^\/system-updates\/updaters\/[^/]+\/settings$/.test(normalizedPath)) return true;
  if (/^\/system-updates\/[^/]+\/cancel$/.test(normalizedPath)) return true;
  if (/^\/streams\/[^/]+\/artifacts(?:\/[^/]+)?(?:\/download)?$/.test(normalizedPath)) return true;
  if (/^\/streams\/[^/]+\/artifacts\/[^/]+\/shares(?:\/[^/]+)?$/.test(normalizedPath)) return true;
  if (/^\/streams\/[^/]+$/.test(normalizedPath)) return true;
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
    "/auth/avatar",
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
    "/system-updates",
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
