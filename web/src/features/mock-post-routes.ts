import type { PasskeyCredential } from "@/types/domain";
import { stripQuery, mockLoginOAuthProviders, maskMockEmail, mockRoleNames } from "./mock-route-values";
import { mockResourceData, mockSystemUpdateJobs, mockCurrentUser, baseTime, mockMFAStatus, mockPasskeys } from "./mock-state";
import { postMockBootstrapJob, postMockSystemUpdate } from "./mock-system-update-routes";
import { postMockArchiveShare } from "./mock-archive-routes";
import { postMockStream } from "./mock-stream-routes";
import { postMockNodeRegistration, postMockConfigureToken, postMockRuntimeToken } from "./mock-node-routes";


export function mockPost(path: string, body?: unknown): unknown {
  const normalizedPath = stripQuery(path);
  const diagnosticRerun = normalizedPath.match(/^\/observability\/incidents\/([^/]+)\/diagnostics\/rerun$/);
  if (diagnosticRerun) {
    const incidentID = decodeURIComponent(diagnosticRerun[1]);
    const incidents = mockResourceData["/observability/incidents"] as Array<Record<string, unknown>>;
    const incident = incidents.find((row) => row.id === incidentID);
    if (!incident) throw new Error("not_found");
    const now = new Date().toISOString();
    incident.updated_at = now;
    const diagnostics = mockResourceData["/observability/diagnostics"] as Array<Record<string, unknown>>;
    for (const diagnostic of diagnostics) {
      if (diagnostic.incident_id === incidentID) diagnostic.updated_at = now;
    }
    return { incident: structuredClone(incident), outcome: "evaluated" };
  }
  const incidentAction = normalizedPath.match(/^\/observability\/incidents\/([^/]+)\/(acknowledge|resolve)$/);
  if (incidentAction) {
    const incidentID = decodeURIComponent(incidentAction[1]);
    const status = incidentAction[2] === "acknowledge" ? "acknowledged" : "resolved";
    const incidents = mockResourceData["/observability/incidents"] as Array<Record<string, unknown>>;
    const incident = incidents.find((row) => row.id === incidentID);
    if (!incident) throw new Error("not_found");
    incident.status = status;
    incident.updated_at = new Date().toISOString();
    const diagnostics = mockResourceData["/observability/diagnostics"] as Array<Record<string, unknown>>;
    for (const diagnostic of diagnostics) {
      if (diagnostic.incident_id === incidentID) {
        diagnostic.status = status;
        diagnostic.updated_at = incident.updated_at;
      }
    }
    return structuredClone(incident);
  }
  const remediationAction = normalizedPath.match(/^\/observability\/remediation-actions\/([^/]+)\/(approve|execute)$/);
  if (remediationAction) {
    const actionID = decodeURIComponent(remediationAction[1]);
    const status = remediationAction[2] === "approve" ? "approved" : "executed";
    const actions = mockResourceData["/observability/remediation-actions"] as Array<Record<string, unknown>>;
    const action = actions.find((row) => row.id === actionID);
    if (!action) throw new Error("not_found");
    action.status = status;
    action.updated_at = new Date().toISOString();
    return structuredClone(action);
  }
  const updaterBootstrapJobs = normalizedPath.match(/^\/system-updates\/updaters\/([^/]+)\/bootstrap-jobs$/);
  if (updaterBootstrapJobs) {
    return postMockBootstrapJob(updaterBootstrapJobs, body);
  }
  if (normalizedPath === "/system-updates") {
    return postMockSystemUpdate(body);
  }
  const systemUpdateCancel = normalizedPath.match(/^\/system-updates\/([^/]+)\/cancel$/);
  if (systemUpdateCancel) {
    const id = decodeURIComponent(systemUpdateCancel[1]);
    const job = mockSystemUpdateJobs.find((item) => item.id === id);
    if (!job) throw new Error("job_not_found");
    job.status = "cancelled";
    job.message = "オペレーターが更新をキャンセルしました。";
    job.updated_at = new Date().toISOString();
    job.completed_at = job.updated_at;
    return job;
  }
  const notificationTest = normalizedPath.match(/^\/observability\/notification-channels\/([^/]+)\/test$/);
  if (notificationTest) {
    const id = decodeURIComponent(notificationTest[1]);
    const rows = mockResourceData["/observability/notification-channels"] as Array<Record<string, unknown>>;
    const channel = rows.find((row) => row.id === id);
    if (!channel) throw new Error("not_found");
    return [{
      status: "success",
      channel: channel.type,
      target: channel.masked_webhook_url || channel.masked_email_target || "<WEBHOOK_URL>",
    }];
  }
  const artifactShareCreate = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)\/shares$/);
  if (artifactShareCreate) {
    return postMockArchiveShare(artifactShareCreate, body);
  }
  if (stripQuery(path) === "/auth/login") {
    return { csrf_token: "mock-csrf-token", user: mockCurrentUser.user };
  }
  if (/^\/auth\/oauth\/[^/]+\/start$/.test(stripQuery(path))) {
    const providerID = decodeURIComponent(stripQuery(path).replace(/^\/auth\/oauth\//, "").replace(/\/start$/, ""));
    const request = body as Partial<{ redirect_after: string }>;
    const provider = mockLoginOAuthProviders().find((item) => item.id === providerID) || mockLoginOAuthProviders()[0];
    return {
      provider,
      authorization_url: request.redirect_after || "/admin/",
      state: "mock-oauth-login-state",
      nonce: "mock-oauth-login-nonce",
      expires_at: baseTime,
    };
  }
  const previewLink = stripQuery(path).match(/^\/streams\/([^/]+)\/preview-links$/);
  if (previewLink) {
    const streamID = decodeURIComponent(previewLink[1]);
    return {
      stream_id: streamID,
      url: `https://control.example.jp/stream-previews/mock-preview-token-${encodeURIComponent(streamID)}/index.m3u8`,
      playback_url: `https://control.example.jp/stream-previews/mock-preview-token-${encodeURIComponent(streamID)}/index.m3u8`,
      player_url: `https://control.example.jp/stream-preview/?token=mock-preview-token-${encodeURIComponent(streamID)}`,
      expires_at: new Date(Date.now() + 12 * 60 * 60 * 1000).toISOString(),
    };
  }
  if (stripQuery(path) === "/auth/change-password") {
    return { status: "password_changed" };
  }
  if (stripQuery(path) === "/auth/mfa/enroll") {
    mockMFAStatus.pending_enrollment = true;
    return {
      method: "totp",
      secret: "JBSWY3DPEHPK3PXP",
      provisioning_uri: "otpauth://totp/AutoStream:demo-admin?secret=JBSWY3DPEHPK3PXP&issuer=AutoStream",
      recovery_codes: ["AS-1111-2222", "AS-3333-4444", "AS-5555-6666"],
      message: "Verify a TOTP code to enable MFA.",
    };
  }
  if (stripQuery(path) === "/auth/mfa/verify") {
    mockMFAStatus.enabled = true;
    mockMFAStatus.pending_enrollment = false;
    mockMFAStatus.method = "totp";
    mockMFAStatus.recovery_code_count = 3;
    return { status: "mfa_enabled", method: "totp" };
  }
  if (stripQuery(path) === "/auth/email/confirm") {
    const request = body as { token?: string };
    if (!String(request.token || "").trim()) throw new Error("invalid_email_change_token");
    return { status: "email_changed", target: maskMockEmail(mockCurrentUser.user.email || "operator@example.jp") };
  }
  if (stripQuery(path) === "/auth/mfa/disable") {
    mockMFAStatus.enabled = false;
    mockMFAStatus.method = "";
    mockMFAStatus.recovery_code_count = 0;
    return { status: "mfa_disabled" };
  }
  if (stripQuery(path) === "/auth/recovery-codes/regenerate") {
    mockMFAStatus.recovery_code_count = 3;
    return { recovery_codes: ["AS-7777-8888", "AS-9999-0000", "AS-1212-3434"] };
  }
  if (/^\/auth\/oauth-links\/[^/]+\/start$/.test(stripQuery(path))) {
    const providerID = decodeURIComponent(stripQuery(path).replace(/^\/auth\/oauth-links\//, "").replace(/\/start$/, ""));
    const request = body as Partial<{ redirect_after: string }>;
    const provider = mockLoginOAuthProviders().find((item) => item.id === providerID) || mockLoginOAuthProviders()[0];
    return {
      provider,
      authorization_url: request.redirect_after || "/admin/account/",
      state: "mock-oauth-link-state",
      nonce: "mock-oauth-link-nonce",
      expires_at: baseTime,
    };
  }
  if (stripQuery(path) === "/auth/passkeys/register/start") {
    return {
      registration_token: "ast_pk_demo_registration",
      expires_at: baseTime,
      public_key: {
        challenge: "ZGVtby1jaGFsbGVuZ2U",
        rp: { id: "localhost", name: "AutoStream Demo" },
        user: { id: "dXNlci1kZW1vLWFkbWlu", name: "demo-admin", displayName: "demo-admin" },
        pubKeyCredParams: [{ type: "public-key", alg: -7 }],
        timeout: 60000,
        attestation: "none",
      },
    };
  }
  if (stripQuery(path) === "/auth/passkeys/register/finish") {
    const request = body as Partial<{ name: string }>;
    const passkey: PasskeyCredential = {
      id: `passkey-demo-${mockPasskeys.length + 1}`,
      user_id: "user-demo-admin",
      name: request.name || "Passkey",
      sign_count: 0,
      transports: ["internal"],
      backup_eligible: true,
      backed_up: false,
      created_at: baseTime,
      updated_at: baseTime,
    };
    mockPasskeys.unshift(passkey);
    return passkey;
  }
  if (stripQuery(path) === "/auth/passkeys/login/start") {
    return {
      challenge_token: "ast_pk_demo_login",
      expires_at: baseTime,
      public_key: {
        challenge: "ZGVtby1sb2dpbi1jaGFsbGVuZ2U",
        timeout: 60000,
        userVerification: "required",
      },
    };
  }
  if (stripQuery(path) === "/auth/passkeys/login/finish") {
    return { csrf_token: "mock-csrf-token", user: mockCurrentUser.user };
  }
  if (stripQuery(path) === "/users") {
    const request = body as Partial<{ username: string; email: string; role_ids: string[] }>;
    if (!String(request.email || "").trim()) throw new Error("email_required");
    const roleIDs = request.role_ids || [];
    const user = {
      id: `user-demo-${request.username || mockResourceData["/users"].length + 1}`,
      username: request.username || "operator",
      email: request.email || "",
      status: "pending_password_change",
      roles: mockRoleNames(roleIDs),
      role_ids: roleIDs,
      created_at: baseTime,
      updated_at: baseTime,
    };
    (mockResourceData["/users"] as Record<string, unknown>[]).unshift(user);
    return user;
  }
  if (stripQuery(path) === "/streams") {
    return postMockStream(body);
  }
  if (stripQuery(path) === "/settings/app/test-email") {
    const request = body as { to?: string };
    const to = String(request.to || "").trim();
    if (!to || !to.includes("@") || /[\r\n\t]/.test(to)) {
      throw new Error("invalid_email_recipient");
    }
    return { status: "sent", target: maskMockEmail(to) };
  }
  if (stripQuery(path) === "/integrations/oauth-accounts/start") {
    const request = body as Partial<{ provider_id: string; oauth_account_id: string; account_label: string; account_purpose: string; redirect_after: string }>;
    const providers = mockResourceData["/integrations/oauth-providers"] as Array<{ id: string; provider_type: string; name: string; enabled: boolean; redirect_uri: string }>;
    return {
      provider: providers.find((provider) => provider.id === request.provider_id) || providers[0],
      authorization_url: request.redirect_after || "/admin/integrations/",
      state: "mock-oauth-state",
      nonce: "mock-oauth-nonce",
      expires_at: baseTime,
      account_label: request.account_label || "Googleアカウント",
      account_purpose: request.account_purpose || "drive_youtube",
      relink: Boolean(request.oauth_account_id),
    };
  }
  if (stripQuery(path) === "/nodes/registration-tokens") {
    return postMockNodeRegistration(body);
  }
  const configureTokenRotate = stripQuery(path).match(/^\/nodes\/([^/]+)\/configure-token$/);
  if (configureTokenRotate) {
    return postMockConfigureToken(configureTokenRotate);
  }
  const runtimeTokenRotate = stripQuery(path).match(/^\/nodes\/([^/]+)\/rotate-token$/);
  if (runtimeTokenRotate) {
    return postMockRuntimeToken(runtimeTokenRotate);
  }
  return { ok: true };
}
