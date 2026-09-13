import type { ManagedAppSettings, UpdaterSettingsUpdate, WorkerNode } from "@/types/domain";
import { stripQuery, maskMockEmail, mockDeleteCollectionPath, mockRoleNames } from "./mock-route-values";
import { mockUpdaterSettings, replaceMockUpdaterSettings, mockSystemUpdateUpdaters, mockStreamArtifacts, replaceMockAppSettings, mockAppSettings, baseTime, mockWorkers, mockResourceData, mockCurrentUser } from "./mock-state";
import { isMockPullHostAgent, mockEndpointlessPullHostAgent } from "./mock-node-routes";


export function mockPut(path: string, body?: unknown): unknown {
  const normalizedPath = stripQuery(path);
  const updaterSettings = normalizedPath.match(/^\/system-updates\/updaters\/([^/]+)\/settings$/);
  if (updaterSettings) {
    const updaterID = decodeURIComponent(updaterSettings[1]);
    if (updaterID !== mockUpdaterSettings.updater_id) throw new Error("updater_not_found");
    const request = (body || {}) as UpdaterSettingsUpdate;
    if (Number(request.expected_revision) !== mockUpdaterSettings.revision) throw new Error("conflict");
    const nextRevision = mockUpdaterSettings.revision + 1;
    const tokenProvided = Object.prototype.hasOwnProperty.call(request, "github_token");
    const token = tokenProvided ? String(request.github_token || "") : "";
    replaceMockUpdaterSettings({
      updater_id: updaterID,
      revision: nextRevision,
      projection_revision: nextRevision,
      local_executor_policy_revision: nextRevision,
      transport_mode: mockUpdaterSettings.transport_mode,
      execution_host_id: mockUpdaterSettings.execution_host_id,
      execution_host_ownership: mockUpdaterSettings.execution_host_ownership
        ? { ...mockUpdaterSettings.execution_host_ownership, policy_revision: nextRevision }
        : undefined,
      pull_activation: mockUpdaterSettings.pull_activation
        ? { ...mockUpdaterSettings.pull_activation, reported_projection_revision: nextRevision }
        : undefined,
      local_executor_policy_sha256: request.local_executor_policy_sha256 ?? mockUpdaterSettings.local_executor_policy_sha256,
      api: request.api ? { ...request.api } : { ...mockUpdaterSettings.api },
      poll_interval_seconds: request.poll_interval_seconds,
      heartbeat_interval_seconds: request.heartbeat_interval_seconds,
      hosts: request.hosts?.map((host) => ({ ...host })) ?? [],
      targets: request.targets.map((target) => ({ ...target })),
      github_token_configured: tokenProvided ? Boolean(token) : mockUpdaterSettings.github_token_configured,
      github_token_fingerprint: tokenProvided
        ? (token ? "sha256:mock-updated-token" : "")
        : mockUpdaterSettings.github_token_fingerprint,
      updated_at: new Date().toISOString(),
    });
    const updater = mockSystemUpdateUpdaters.find((item) => item.updater_id === updaterID);
    if (updater) {
      updater.desired_revision = nextRevision;
      updater.applied_revision = nextRevision;
      updater.policy_status = "applied";
      updater.policy_error_code = "";
      updater.policy_error = "";
    }
    return structuredClone(mockUpdaterSettings);
  }
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
    const request = body as Partial<ManagedAppSettings> & { smtp_password?: string; turnstile_secret?: string };
    const smtpEnabled = Boolean(request.smtp_enabled);
    const turnstileEnabled = Boolean(request.turnstile_enabled);
    replaceMockAppSettings({
      app_name: request.app_name || mockAppSettings.app_name,
      timezone: request.timezone || mockAppSettings.timezone,
      google_analytics_enabled: Boolean(request.google_analytics_enabled),
      google_analytics_measurement_id: request.google_analytics_enabled ? request.google_analytics_measurement_id?.trim().toUpperCase() : undefined,
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
    });
    return mockAppSettings;
  }
  const nodeUpdate = stripQuery(path).match(/^\/nodes\/([^/]+)$/);
  if (nodeUpdate) {
    const nodeID = decodeURIComponent(nodeUpdate[1]);
    const request = body as Partial<{ service_name: string; name: string; description: string; host: string; port: number; ssl_enabled: boolean }>;
    const index = mockWorkers.findIndex((node) => (node.service_id || node.id) === nodeID);
    if (index < 0) throw new Error("not_found");
    const existing = mockWorkers[index];
    const common = {
      ...existing,
      service_name: request.service_name || request.name || existing.service_name,
      description: request.description ?? existing.description,
    };
    const next: WorkerNode = isMockPullHostAgent(existing)
      ? mockEndpointlessPullHostAgent(common)
      : {
          ...common,
          host: request.host || existing.host || "worker-main.example.jp",
          port: request.port || existing.port || 8443,
          ssl_enabled: request.ssl_enabled ?? existing.ssl_enabled ?? true,
        };
    if (!isMockPullHostAgent(next)) {
      next.public_url = `${next.ssl_enabled ? "https" : "http"}://${next.host}:${next.port}`;
    }
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
    if (collectionPath === "/users" && Array.isArray(request.role_ids)) {
      next.roles = mockRoleNames(request.role_ids.map(String));
    }
    if (collectionPath === "/observability/notification-channels") {
      if (typeof request.webhook_url === "string" && request.webhook_url.trim()) {
        next.masked_webhook_url = request.type === "slack" ? "https://hooks.slack.com/<WEBHOOK_PATH>" : "https://discord.com/<WEBHOOK_PATH>";
      }
      if (Array.isArray(request.email_recipients) && typeof request.email_recipients[0] === "string") {
        next.masked_email_target = maskMockEmail(request.email_recipients[0]);
      }
      for (const key of ["webhook_url", "email_recipients", "smtp_host", "smtp_port", "smtp_tls", "smtp_from", "smtp_username", "smtp_password"]) {
        delete next[key];
      }
    }
    (rows as Record<string, unknown>[])[index] = next;
    return next;
  }
  return { ok: true };
}

export async function mockPutBinary(path: string, body: Blob): Promise<unknown> {
  const normalizedPath = stripQuery(path);
  if (normalizedPath !== "/auth/avatar") return { ok: true };
  const bytes = new Uint8Array(await body.arrayBuffer());
  let binary = "";
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, Math.min(offset + chunkSize, bytes.length)));
  }
  const updatedAt = new Date().toISOString();
  mockCurrentUser.user.avatar_url = `data:${body.type || "image/png"};base64,${window.btoa(binary)}`;
  mockCurrentUser.user.avatar_updated_at = updatedAt;
  return { avatar_url: mockCurrentUser.user.avatar_url, content_type: body.type, size_bytes: body.size, updated_at: updatedAt };
}
