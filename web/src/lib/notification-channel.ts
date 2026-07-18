export type NotificationChannelPayloadInput = {
  editing: boolean;
  name: string;
  type: string;
  webhookURL: string;
  emailRecipients: string[];
  severityFilter: string[];
  eventTypeFilter: string[];
  enabled: boolean;
};

export type NotificationChannelTestFeedback = {
  ok: boolean;
  message: string;
};

export function notificationChannelTypeLabel(value: string) {
  switch (value.trim().toLowerCase()) {
    case "discord":
      return "Discord";
    case "slack":
      return "Slack";
    case "email":
      return "Email";
    case "generic":
      return "Generic Webhook";
    default:
      return value;
  }
}

export function notificationDeliveryEventKey(delivery: unknown) {
  if (!isRecord(delivery)) return "";
  const eventType = stringValue(delivery.event_type);
  if (eventType !== "admin.audit") return eventType;
  const metadata = delivery.metadata;
  if (!isRecord(metadata)) return eventType;
  const action = [stringValue(metadata.action), stringValue(metadata.rule)].find((value) => value && value !== "<redacted>");
  return action || eventType;
}

export function notificationDeliveryPresentation(delivery: unknown) {
  const eventKey = notificationDeliveryEventKey(delivery);
  if (!isRecord(delivery)) return { eventKey, detail: "", sentAt: "" };
  const metadata = isRecord(delivery.metadata) ? delivery.metadata : {};
  const detail = stringValue(metadata.summary);
  return {
    eventKey,
    detail: detail === "<redacted>" ? "" : detail,
    sentAt: stringValue(delivery.created_at) || stringValue(delivery.sent_at),
  };
}

export function buildNotificationChannelPayload(input: NotificationChannelPayloadInput): Record<string, unknown> {
  const payload: Record<string, unknown> = {
    name: input.name.trim(),
    type: input.type,
    severity_filter: input.severityFilter,
    event_type_filter: normalizeNotificationChannelEventTypeFilter(input.eventTypeFilter),
    enabled: input.enabled,
  };
  if (input.type === "email") {
    if (!input.editing) payload.uses_global_smtp = true;
    if (input.emailRecipients.length > 0) payload.email_recipients = input.emailRecipients;
    return payload;
  }

  if (input.webhookURL.trim()) payload.webhook_url = input.webhookURL.trim();
  return payload;
}

export function normalizeNotificationChannelEventTypeFilter(values: string[]) {
  const seen = new Set<string>();
  return values
    .map((value) => value.trim())
    .filter((value) => value !== "" && !seen.has(value) && Boolean(seen.add(value)));
}

export function notificationChannelTestFeedback(response: unknown): NotificationChannelTestFeedback {
  if (!Array.isArray(response) || response.length === 0) {
    return { ok: false, message: "テスト送信結果を確認できませんでした。" };
  }

  const results = response.filter(isRecord);
  if (results.length === 0) {
    return { ok: false, message: "テスト送信結果を確認できませんでした。" };
  }

  const details = results.slice(0, 4).map((result) => {
    const status = stringValue(result.status).toLowerCase();
    const successful = status === "success";
    const target = maskedTarget(result.target);
    const error = sanitizedError(result.error);
    const parts = [successful ? "成功" : "失敗"];
    if (target) parts.push(`送信先 ${target}`);
    if (!successful && error) parts.push(error);
    return parts.join(" / ");
  });
  const ok = results.every((result) => stringValue(result.status).toLowerCase() === "success");
  return {
    ok,
    message: `${ok ? "テスト送信に成功しました。" : "テスト送信に失敗しました。"} ${details.join(" | ")}`,
  };
}

function maskedTarget(value: unknown) {
  const target = stringValue(value);
  if (!target) return "";
  if (
    target.includes("<WEBHOOK_PATH>") ||
    target.includes("<WEBHOOK_URL>") ||
    target.includes("<WEBHOOK_HOST>") ||
    target.includes("<EMAIL") ||
    target.includes("***") ||
    target === "<redacted>"
  ) {
    return target.slice(0, 512);
  }
  return "";
}

function sanitizedError(value: unknown) {
  const error = stringValue(value);
  if (!error) return "";
  const code = error.toLowerCase();
  const safeMessages: Record<string, string> = {
    smtp_not_configured: "設定 > メールサーバーで有効なSMTP設定を保存してください。",
    smtp_requires_tls: "メールサーバーのTLS設定を有効にしてください。",
    smtp_connect_failed: "メールサーバーへ接続できませんでした。設定と稼働状態を確認してください。",
    smtp_auth_failed: "メールサーバーの認証に失敗しました。認証設定を確認してください。",
    smtp_send_failed: "テストメールを送信できませんでした。メールサーバー設定とログを確認してください。",
    send_failed: "テスト通知を送信できませんでした。通知先設定とログを確認してください。",
    rate_limited: "通知が集中しています。少し待ってから再実行してください。",
  };
  if (safeMessages[code]) return safeMessages[code];
  if (code === "rate_limited" || code.endsWith("_rate_limited")) {
    return "通知が集中しています。少し待ってから再実行してください。";
  }
  if (/^smtp_[a-z0-9_]*_failed$/.test(code)) {
    return "メール送信に失敗しました。メールサーバー設定とログを確認してください。";
  }
  if (
    /https?:\/\//i.test(error) ||
    /discord\.com\/api\/webhooks/i.test(error) ||
    /hooks\.slack\.com\/services/i.test(error) ||
    /\b(?:bearer|authorization)\s+/i.test(error) ||
    /(?:token|secret|password|api[_-]?key)\s*[=:]/i.test(error) ||
    /\b(?:token|secret|password|api[_-]?key)[-_][\w.-]+/i.test(error)
  ) {
    return "詳細は安全のため表示されません。";
  }
  return error.slice(0, 512);
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
