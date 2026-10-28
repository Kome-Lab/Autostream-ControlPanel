export type StreamCreateValues = {
  name: string;
  discordConfigID: string;
  autoStartFromDiscord: boolean;
  youtubeOutputID: string;
  archiveProfileID: string;
  encoderProfileID: string;
  captionProfileID: string;
  watermarkEnabled: boolean;
  overlayProfileID: string;
  encoderAudioGainDB: number;
  // Creation requires both roles. On edits, undefined preserves ownership
  // and an explicit empty string unassigns the selected role.
  encoderServiceID?: string;
  workerServiceID?: string;
  scheduledStartAt?: string;
  scheduledEndAt?: string;
  encoderInputURL?: string;
  expectedSettingsRevision?: string;
};

export type StreamServiceAssignmentOption = {
  value: string;
  label: string;
  description?: string;
  disabled?: boolean;
};

export function streamServiceAssignmentOption(
  service: { value: string; label: string; currentStreamID?: string },
  editingStreamID?: string,
): StreamServiceAssignmentOption {
  const owner = service.currentStreamID?.trim() || "";
  const busy = owner !== "" && owner !== (editingStreamID?.trim() || "");
  return {
    value: service.value,
    label: busy ? `${service.label}（別の配信枠で使用中）` : service.label,
    description: busy ? "現在の配信枠を停止し、割り当てを解除してから選択してください。" : undefined,
    disabled: busy,
  };
}

export function streamAssignmentConflictMessage(code?: string): string | undefined {
  const messages: Record<string, string> = {
    service_assignment_conflict: "Nodeの割り当てが同時に更新されました。一覧を更新し、現在の割り当てを確認してから再試行してください。",
    service_assignment_protected_stream: "選択したNodeは開始中・配信中・録画処理中の別の配信枠で使用されています。その処理が完了してから再試行してください。",
    service_unassign_protected_stream: "このNodeは開始中・配信中・録画処理中の配信枠で必要なため、割り当てを解除できません。処理完了後に再試行してください。",
  };
  return messages[code || ""];
}

export function buildStreamCreatePayload(values: StreamCreateValues): Record<string, unknown> {
  return compactRecord({
    name: values.name,
    discord_config_id: values.discordConfigID,
    auto_start_trigger: values.autoStartFromDiscord ? "discord_voice_join" : "",
    youtube_output_id: values.youtubeOutputID,
    archive_profile_id: values.archiveProfileID,
    encoder_profile_id: values.encoderProfileID,
    caption_profile_id: values.captionProfileID,
    overlay_profile_id: values.watermarkEnabled ? values.overlayProfileID : "",
    encoder_audio_gain_db: values.encoderAudioGainDB,
    encoder_service_id: values.encoderServiceID,
    worker_service_id: values.workerServiceID,
    scheduled_start_at: values.scheduledStartAt,
  });
}

// PUT /streams/:id/settings replaces the stored settings. Unlike the create
// request, edits must send intentional empty values so that a user can remove
// an optional profile without leaving an invisible old value behind.
export function buildStreamSettingsPayload(values: StreamCreateValues): Record<string, unknown> {
  return {
    name: values.name.trim(),
    discord_config_id: values.discordConfigID,
    auto_start_trigger: values.autoStartFromDiscord ? "discord_voice_join" : "",
    youtube_output_id: values.youtubeOutputID,
    archive_profile_id: values.archiveProfileID,
    encoder_profile_id: values.encoderProfileID,
    caption_profile_id: values.captionProfileID,
    overlay_profile_id: values.watermarkEnabled ? values.overlayProfileID : "",
    encoder_audio_gain_db: values.encoderAudioGainDB,
    ...(values.encoderServiceID === undefined ? {} : { encoder_service_id: values.encoderServiceID }),
    ...(values.workerServiceID === undefined ? {} : { worker_service_id: values.workerServiceID }),
    scheduled_start_at: values.scheduledStartAt || "",
    scheduled_end_at: values.scheduledEndAt || "",
    encoder_input_url: values.encoderInputURL || "",
    ...(values.expectedSettingsRevision === undefined ? {} : { expected_settings_revision: values.expectedSettingsRevision }),
  };
}

export type StreamCreateValidationIssue = Readonly<{ field: "name" | "schedule"; code: "name_required" | "name_too_long" | "name_invalid" | "schedule_time_invalid" }>;

export function validateStreamCreateValues(values: Pick<StreamCreateValues, "name" | "scheduledStartAt">): readonly StreamCreateValidationIssue[] {
  const issues: StreamCreateValidationIssue[] = [];
  if (!values.name.trim()) issues.push({ field: "name", code: "name_required" });
  else if ([...values.name.trim()].length > 255) issues.push({ field: "name", code: "name_too_long" });
  else if (/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/u.test(values.name)) issues.push({ field: "name", code: "name_invalid" });
  if (values.scheduledStartAt && !Number.isFinite(Date.parse(values.scheduledStartAt))) issues.push({ field: "schedule", code: "schedule_time_invalid" });
  return issues;
}

function compactRecord(record: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(record).filter(([, value]) => value !== "" && value !== undefined));
}

// A datetime-local control represents the browser's local wall-clock time,
// while the API contract uses RFC3339. Keep the conversion at the UI boundary
// so an empty control remains an intentional immediate-start request.
export function streamScheduleInputValue(value?: string): string {
  const raw = value?.trim() || "";
  if (!raw) return "";
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return "";
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

export function streamScheduleRFC3339(value?: string): string {
  const raw = value?.trim() || "";
  if (!raw) return "";
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? raw : date.toISOString();
}
