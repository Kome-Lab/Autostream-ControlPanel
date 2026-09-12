"use client";

import { useState } from "react";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { buildNotificationChannelPayload, normalizeNotificationChannelEventTypeFilter } from "@/lib/notification-channel";
import { type SubmitResource, type ResourceRow } from "./resource-form-types";
import { rowString, stringListSetting, rowValue, splitList } from "./resource-values";
import { TextField, SelectField, Field, CheckboxList, SwitchField, FormActions } from "./resource-input-fields";

export function NotificationChannelForm({ disabled, submit, initial, submitLabel }: { disabled: boolean; submit: SubmitResource; initial?: ResourceRow; submitLabel?: string }) {
  const row = initial || {};
  const editing = Boolean(initial);
  const [name, setName] = useState(() => rowString(row, ["name"]) || "ops-discord");
  const [type, setType] = useState(() => rowString(row, ["type"]) || "discord");
  const [webhookURL, setWebhookURL] = useState("");
  const [emailRecipients, setEmailRecipients] = useState(() => editing ? "" : "ops@example.jp");
  const [severityFilter, setSeverityFilter] = useState<string[]>(() => editing ? stringListSetting(rowValue(row, ["severity_filter"])) : ["critical", "error", "warning"]);
  const [eventTypeFilter, setEventTypeFilter] = useState<string[]>(() => {
    const configured = editing ? stringListSetting(rowValue(row, ["event_type_filter"])) : ["incident.opened"];
    return normalizeNotificationChannelEventTypeFilter(configured);
  });
  const [enabled, setEnabled] = useState(() => rowValue(row, ["enabled"]) !== false);
  const [migrateToGlobalSMTP, setMigrateToGlobalSMTP] = useState(false);
  const emailRecipientList = splitList(emailRecipients);
  const webhookRequired = type !== "email";
  const emailRequired = type === "email";
  const legacyEmailSMTP = editing && emailRequired && rowValue(row, ["uses_global_smtp"]) === false;
  const maskedWebhookURL = rowString(row, ["masked_webhook_url"]);
  const maskedEmailTarget = rowString(row, ["masked_email_target"]);

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(
          buildNotificationChannelPayload({
            editing,
            name,
            type,
            webhookURL,
            emailRecipients: emailRecipientList,
            migrateToGlobalSMTP: legacyEmailSMTP && migrateToGlobalSMTP,
            severityFilter,
            eventTypeFilter,
            enabled,
          }),
          webhookURL ? { onSensitiveDispatched: () => setWebhookURL("") } : undefined,
        );
      }}
    >
      <div className="grid gap-3 md:grid-cols-2">
        <TextField label="通知先名" value={name} onChange={setName} required />
        <SelectField
          label="通知方式"
          value={type}
          onChange={setType}
          disabled={editing}
          options={[
            { value: "discord", label: "Discord Webhook" },
            { value: "slack", label: "Slack Webhook" },
            { value: "generic", label: "Generic Webhook" },
            { value: "email", label: "メール" },
          ]}
        />
        {webhookRequired ? (
          <TextField
            label={editing ? "Webhook URL（変更する場合のみ入力）" : "Webhook URL"}
            value={webhookURL}
            onChange={setWebhookURL}
            type="password"
            description={editing
              ? `空欄のまま更新すると現在のURLを保持します。保存済みURLの実値は表示しません。${maskedWebhookURL ? ` 現在: ${maskedWebhookURL}` : ""}`
              : type === "slack" ? "Slack は hooks.slack.com のIncoming Webhook URLを指定します。" : "保存後はURLの実値を表示しません。"}
            required={!editing}
          />
        ) : null}
      </div>
      {emailRequired ? (
        <div className="space-y-3">
          <Field
            label={editing ? "送信先メール（変更する場合のみ入力）" : "送信先メール"}
            description={editing ? "空欄のまま更新すると現在の送信先を保持します。複数指定は改行またはカンマで区切ります。" : "複数指定する場合は改行またはカンマで区切ります。"}
          >
            <Textarea value={emailRecipients} onChange={(event) => setEmailRecipients(event.target.value)} className="min-h-20" required={!editing} />
          </Field>
          {legacyEmailSMTP ? (
            <div className="space-y-3 rounded-md border border-amber-300 bg-amber-50 px-3 py-3 text-sm text-amber-950 dark:border-amber-700 dark:bg-amber-950/30 dark:text-amber-100">
              <p>この通知先は旧形式の個別SMTP設定を使用しています。</p>
              <label className="flex items-center justify-between gap-3 rounded-md border border-amber-300 bg-background px-3 py-2 dark:border-amber-700">
                <span>
                  <span className="block font-medium">共通SMTP設定へ移行</span>
                  <span className="mt-1 block text-xs text-muted-foreground">有効にして保存すると、設定画面の「メールサーバー」を使用し、旧個別SMTP認証情報を削除します。この操作は元に戻せません。</span>
                </span>
                <Switch
                  aria-label="共通SMTP設定へ移行"
                  checked={migrateToGlobalSMTP}
                  disabled={disabled}
                  onCheckedChange={(value) => setMigrateToGlobalSMTP(Boolean(value))}
                />
              </label>
              {maskedEmailTarget ? <p>現在の送信先: {maskedEmailTarget}</p> : null}
            </div>
          ) : (
            <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">
              設定画面の「メールサーバー」で構成した共通SMTP設定を使用します。この通知先ではSMTP認証情報の入力は不要です。
              {editing && maskedEmailTarget ? ` 現在の送信先: ${maskedEmailTarget}` : ""}
            </div>
          )}
        </div>
      ) : null}
      <CheckboxList
        label="通知する重要度"
        values={severityFilter}
        onChange={setSeverityFilter}
        items={[
          { value: "critical", label: "重大" },
          { value: "error", label: "エラー" },
          { value: "warning", label: "警告" },
          { value: "info", label: "情報" },
        ]}
      />
      <CheckboxList
        label="通知するイベント"
        values={eventTypeFilter}
        onChange={setEventTypeFilter}
        items={[
          { value: "incident.opened", label: "インシデント発生" },
          { value: "incident.updated", label: "インシデント更新" },
          { value: "incident.resolved", label: "インシデント解決" },
          { value: "diagnostic.created", label: "診断作成" },
          { value: "remediation.pending_approval", label: "復旧承認待ち" },
          { value: "remediation.executed", label: "復旧実行" },
        ]}
      />
      <p className="rounded-md border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">
        監査ログへ保存された認証済みユーザー操作とsystem操作は、有効な通知先へ常に送信されます。上の選択項目はインシデント、診断、復旧イベントだけを絞り込みます。
      </p>
      <SwitchField label="有効化" checked={enabled} onCheckedChange={setEnabled} />
      <FormActions label={submitLabel} disabled={disabled || (webhookRequired && webhookURL.trim() === "" && !editing) || (emailRequired && !editing && emailRecipientList.length === 0)} />
    </form>
  );
}
