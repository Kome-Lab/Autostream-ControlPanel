import assert from "node:assert/strict";
import test from "node:test";

import {
  buildNotificationChannelPayload,
  normalizeNotificationChannelEventTypeFilter,
  notificationChannelTestFeedback,
  notificationChannelTypeLabel,
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
