import assert from "node:assert/strict";
import test from "node:test";

import { auditActionLabel } from "../src/lib/audit-action.ts";
import {
  buildNotificationChannelPayload,
  normalizeNotificationChannelEventTypeFilter,
  notificationChannelTestFeedback,
  notificationChannelTypeLabel,
  notificationDeliveryEventKey,
  notificationDeliveryPresentation,
} from "../src/lib/notification-channel.ts";

const basePayload = {
  editing: true,
  name: "ops email",
  type: "email",
  webhookURL: "",
  emailRecipients: [],
  severityFilter: ["critical"],
  eventTypeFilter: ["incident.opened"],
  enabled: true,
};

test("email edit with a blank recipient preserves the target and existing SMTP mode", () => {
  const payload = buildNotificationChannelPayload(basePayload);

  assert.deepEqual(payload, {
    name: "ops email",
    type: "email",
    severity_filter: ["critical"],
    event_type_filter: ["incident.opened"],
    enabled: true,
  });
});

test("legacy email SMTP migrates only when explicitly requested", () => {
  const payload = buildNotificationChannelPayload({
    ...basePayload,
    migrateToGlobalSMTP: true,
  });

  assert.deepEqual(payload, {
    name: "ops email",
    type: "email",
    migrate_to_global_smtp: true,
    severity_filter: ["critical"],
    event_type_filter: ["incident.opened"],
    enabled: true,
  });
  assert.equal("uses_global_smtp" in payload, false);
  assert.equal(Object.keys(payload).some((key) => key.startsWith("smtp_")), false);
});

test("email edit replaces only recipients and never sends SMTP credentials", () => {
  const payload = buildNotificationChannelPayload({
    ...basePayload,
    emailRecipients: ["new@example.jp"],
  });

  assert.deepEqual(payload, {
    name: "ops email",
    type: "email",
    email_recipients: ["new@example.jp"],
    severity_filter: ["critical"],
    event_type_filter: ["incident.opened"],
    enabled: true,
  });
  assert.equal(Object.keys(payload).some((key) => key.startsWith("smtp_")), false);
});

test("an admin-audit-only edit filter remains admin-audit-only", () => {
  const eventTypeFilter = normalizeNotificationChannelEventTypeFilter(["admin.audit"]);
  const payload = buildNotificationChannelPayload({
    ...basePayload,
    eventTypeFilter,
  });

  assert.deepEqual(eventTypeFilter, ["admin.audit"]);
  assert.deepEqual(payload.event_type_filter, ["admin.audit"]);
  assert.notDeepEqual(payload.event_type_filter, []);
});

test("email creation uses the shared SMTP relay", () => {
  const payload = buildNotificationChannelPayload({
    ...basePayload,
    editing: false,
    emailRecipients: ["ops@example.jp"],
  });

  assert.equal(payload.uses_global_smtp, true);
  assert.deepEqual(payload.email_recipients, ["ops@example.jp"]);
  assert.equal(Object.keys(payload).some((key) => key.startsWith("smtp_")), false);
});

test("blank webhook URL preserves the stored secret during edit", () => {
  const payload = buildNotificationChannelPayload({
    ...basePayload,
    type: "discord",
    webhookURL: "",
  });

  assert.equal("webhook_url" in payload, false);
});

test("a new webhook URL is sent only when explicitly entered", () => {
  const payload = buildNotificationChannelPayload({
    ...basePayload,
    type: "discord",
    webhookURL: "https://discord.com/api/webhooks/id/new-token",
  });

  assert.equal(payload.webhook_url, "https://discord.com/api/webhooks/id/new-token");
});

test("notification channel API types have consistent display labels", () => {
  assert.equal(notificationChannelTypeLabel("discord"), "Discord");
  assert.equal(notificationChannelTypeLabel("slack"), "Slack");
  assert.equal(notificationChannelTypeLabel("email"), "Email");
  assert.equal(notificationChannelTypeLabel("generic"), "Generic Webhook");
});

test("admin audit delivery history uses the concrete operation name", () => {
  const eventKey = notificationDeliveryEventKey({
    event_type: "admin.audit",
    metadata: { rule: "secrets.update" },
  });

  assert.equal(eventKey, "secrets.update");
  assert.equal(auditActionLabel(eventKey), "シークレットを更新");
});

test("delivery history keeps lifecycle events and readable unknown actions", () => {
  assert.equal(notificationDeliveryEventKey({ event_type: "incident.opened" }), "incident.opened");
  assert.equal(notificationDeliveryEventKey({ event_type: "admin.audit", metadata: { action: "custom.operation" } }), "custom.operation");
  assert.equal(auditActionLabel("custom.operation"), "Custom Operation");
  assert.equal(notificationDeliveryEventKey({ event_type: "admin.audit", metadata: { action: "<redacted>" } }), "admin.audit");
  assert.equal(notificationDeliveryEventKey({ event_type: "admin.audit" }), "admin.audit");
});

test("delivery history projects current and legacy timestamps with safe details", () => {
  assert.deepEqual(
    notificationDeliveryPresentation({
      event_type: "admin.audit",
      metadata: { action: "streams.retry_upload", summary: "録画ファイルのアップロードを再試行\n実行者: ops" },
      created_at: "2026-07-18T01:32:00Z",
    }),
    {
      eventKey: "streams.retry_upload",
      detail: "録画ファイルのアップロードを再試行\n実行者: ops",
      sentAt: "2026-07-18T01:32:00Z",
    },
  );
  assert.deepEqual(
    notificationDeliveryPresentation({ event_type: "incident.opened", sent_at: "2026-07-17T23:00:00Z" }),
    { eventKey: "incident.opened", detail: "", sentAt: "2026-07-17T23:00:00Z" },
  );
  assert.equal(notificationDeliveryPresentation({ event_type: "admin.audit", metadata: { summary: "<redacted>" } }).detail, "");
});

test("known control-panel audit operations have explicit Japanese labels", () => {
  const actions = [
    "api_tokens.create",
    "api_tokens.revoke",
    "api_tokens.rotate",
    "app.settings.test_email",
    "app.settings.update",
    "archive.artifact.delete",
    "archive.artifact.download",
    "archive.artifact.rename",
    "archive.artifact.share.create",
    "archive.artifact.share.revoke",
    "auth.change_password",
    "auth.avatar.update",
    "auth.avatar.delete",
    "auth.email.change_request",
    "auth.email.confirm",
    "auth.login",
    "auth.logout",
    "auth.oauth.login",
    "auth.oauth.provision_user",
    "auth.oauth.start",
    "auth.passkey.login.start",
    "auth.passkey.login.finish",
    "auth.oauth_link.create",
    "auth.oauth_link.delete",
    "discord_configs.create",
    "discord_configs.delete",
    "discord_configs.update",
    "integrations.drive_destination.create",
    "integrations.drive_destination.delete",
    "integrations.drive_destination.update",
    "integrations.oauth_account.connect",
    "integrations.oauth_account.create",
    "integrations.oauth_account.delete",
    "integrations.oauth_account.update",
    "integrations.oauth_provider.create",
    "integrations.oauth_provider.delete",
    "integrations.oauth_provider.update",
    "mfa.disable",
    "mfa.enroll",
    "mfa.recovery_codes.regenerate",
    "mfa.verify",
    "nodes.configure_token.rotate",
    "nodes.registration_token.create",
    "nodes.runtime_token.rotate",
    "nodes.update",
    "passkeys.delete",
    "passkeys.registration.start",
    "passkeys.registration.finish",
    "remediation.execute",
    "roles.create",
    "roles.delete",
    "roles.update",
    "secrets.update",
    "security.settings.update",
    "services.assign",
    "services.delete",
    "services.runtime_config.preview",
    "services.unassign",
    "setup.first_admin",
    "streams.create",
    "streams.discord_youtube_notify",
    "streams.preview_link.create",
    "streams.retry_upload",
    "streams.start",
    "streams.stop",
    "streams.update_settings",
    "streams.worker_event_test",
    "system_updates.create",
    "system_updates.request",
    "system_updates.cancel",
    "system_updates.report",
    "system_updates.claim",
    "system_updates.authorize",
    "system_updates.succeeded",
    "system_updates.rolled_back",
    "system_updates.failed",
    "users.create",
    "users.delete",
    "users.email_welcome",
    "users.oauth_link.create",
    "users.oauth_link.delete",
    "users.reset_password",
    "users.update",
    "workers.assign",
    "workers.restart",
    "workers.unassign",
    "youtube.complete",
    "youtube_outputs.create",
    "youtube_outputs.delete",
    "youtube_outputs.update",
  ];
  for (const action of actions) {
    const generic = action.replace(/[_\-.]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
    assert.notEqual(auditActionLabel(action), generic, action);
  }
});

test("HTTP-accepted delivery failures are summarized as failures", () => {
  const feedback = notificationChannelTestFeedback([
    {
      status: "failure",
      target: "https://<WEBHOOK_HOST>/<WEBHOOK_PATH>",
      error: "webhook delivery returned status 403",
    },
  ]);

  assert.equal(feedback.ok, false);
  assert.match(feedback.message, /失敗/);
  assert.match(feedback.message, /<WEBHOOK_PATH>/);
  assert.match(feedback.message, /status 403/);
});

test("SMTP delivery error codes become safe user guidance", () => {
  const cases = [
    { code: "smtp_not_configured", expected: /設定 > メールサーバー/ },
    { code: "smtp_requires_tls", expected: /TLS設定/ },
    { code: "smtp_connection_failed", expected: /メール送信に失敗/ },
    { code: "send_failed", expected: /テスト通知を送信できません/ },
  ];

  for (const item of cases) {
    const feedback = notificationChannelTestFeedback([
      { status: "failure", target: "o***s@example.jp", error: item.code },
    ]);

    assert.equal(feedback.ok, false);
    assert.match(feedback.message, item.expected);
    assert.doesNotMatch(feedback.message, new RegExp(item.code));
  }
});

test("email relay configuration errors become safe actionable guidance", () => {
  const cases = [
    { code: "missing_service_scope", expected: /Runtime Tokenを再生成/ },
    { code: "invalid_service_token", expected: /config\.ymlを再発行/ },
    { code: "app_settings_failed", expected: /共通SMTP設定を読み込めません/ },
    { code: "secret_encryption_key_required", expected: /暗号化キーが未設定/ },
  ];

  for (const item of cases) {
    const feedback = notificationChannelTestFeedback([
      { status: "failure", target: "o***s@example.jp", error: item.code },
    ]);

    assert.equal(feedback.ok, false);
    assert.match(feedback.message, item.expected);
    assert.doesNotMatch(feedback.message, new RegExp(item.code));
  }
});

test("rate limited delivery errors become safe retry guidance", () => {
  const feedback = notificationChannelTestFeedback([
    { status: "failure", target: "o***s@example.jp", error: "rate_limited" },
  ]);

  assert.equal(feedback.ok, false);
  assert.match(feedback.message, /少し待ってから再実行/);
  assert.doesNotMatch(feedback.message, /rate_limited/);
});

test("successful test delivery preserves only an explicitly masked target", () => {
  const feedback = notificationChannelTestFeedback([
    { status: "success", target: "https://hooks.slack.com/<WEBHOOK_PATH>" },
  ]);

  assert.equal(feedback.ok, true);
  assert.match(feedback.message, /成功/);
  assert.match(feedback.message, /hooks\.slack\.com/);
});

test("raw targets and unsafe error details are not displayed", () => {
  const feedback = notificationChannelTestFeedback([
    {
      status: "failure",
      target: "https://discord.com/api/webhooks/id/raw-token",
      error: "request to https://discord.com/api/webhooks/id/raw-token failed",
    },
  ]);

  assert.equal(feedback.ok, false);
  assert.doesNotMatch(feedback.message, /raw-token/);
  assert.doesNotMatch(feedback.message, /discord\.com/);
  assert.match(feedback.message, /安全のため表示されません/);
});

test("unknown raw delivery errors are hidden even when they contain no obvious secret", () => {
  const feedback = notificationChannelTestFeedback([
    {
      status: "failure",
      target: "o***s@example.jp",
      error: "dial tcp 10.0.0.25:25: i/o timeout",
    },
  ]);

  assert.equal(feedback.ok, false);
  assert.doesNotMatch(feedback.message, /10\.0\.0\.25|dial tcp|timeout/);
  assert.match(feedback.message, /安全のため表示されません/);
});
