import assert from "node:assert/strict";
import test from "node:test";

import { buildStreamCreatePayload, buildStreamSettingsPayload, streamAssignmentConflictMessage, streamCreateCompatibilityMessage, streamScheduleInputValue, streamScheduleRFC3339, streamServiceAssignmentOption, type StreamCreateValues } from "../src/lib/stream-create.ts";

const baseValues: StreamCreateValues = {
  name: "定例配信",
  discordConfigID: "discord-main",
  discordGuildID: "guild-main",
  discordVoiceChannelID: "voice-main",
  discordTextChannelID: "text-main",
  autoStartFromDiscord: true,
  youtubeOutputID: "youtube-main",
  archiveProfileID: "archive-shared-drive",
  encoderProfileID: "encoder-hd",
  captionProfileID: "caption-ja",
  watermarkEnabled: true,
  overlayProfileID: "overlay-logo",
  encoderAudioGainDB: 3.5,
  encoderServiceID: "encoder-node-01",
  workerServiceID: "worker-node-01",
};

test("stream creation sends the selected recording profile", () => {
  const payload = buildStreamCreatePayload(baseValues);

  assert.equal(payload.archive_profile_id, "archive-shared-drive");
  assert.equal(payload.auto_start_trigger, "discord_voice_join");
  assert.equal(payload.encoder_audio_gain_db, 3.5);
});

test("stream creation never changes runtime Node assignments", () => {
  const payload = buildStreamCreatePayload(baseValues);

  assert.equal("encoder_service_id" in payload, false);
  assert.equal("worker_service_id" in payload, false);
});

test("stream creation no longer sends direct archive settings or an external input URL", () => {
  const payload = buildStreamCreatePayload(baseValues);

  for (const key of [
    "archive_oauth_account_id",
    "archive_folder_id",
    "archive_shared_drive",
    "archive_shared_drive_id",
    "archive_file_name",
    "archive_retention_days",
    "encoder_input_url",
  ]) {
    assert.equal(key in payload, false, `${key} must not be part of the standard create payload`);
  }
});

test("choosing not to record omits archive_profile_id", () => {
  const payload = buildStreamCreatePayload({ ...baseValues, archiveProfileID: "" });

  assert.equal("archive_profile_id" in payload, false);
});

test("stream scheduling is opt-in on create and can be deliberately cleared on edit", () => {
  const future = "2030-07-10T01:00:00Z";

  const inputValue = streamScheduleInputValue(future);
  assert.notEqual(inputValue, "", "an existing RFC3339 schedule must remain visible in the date control");
  assert.equal(Date.parse(streamScheduleRFC3339(inputValue)), Date.parse(future), "saving an unchanged visible schedule must retain its instant");

  const scheduledCreate = buildStreamCreatePayload({ ...baseValues, scheduledStartAt: future });
  assert.equal(scheduledCreate.scheduled_start_at, future);

  const immediateCreate = buildStreamCreatePayload({ ...baseValues, scheduledStartAt: "" });
  assert.equal("scheduled_start_at" in immediateCreate, false, "an empty schedule must not turn an immediate stream into a scheduled one");

  const retainedSchedule = buildStreamSettingsPayload({ ...baseValues, scheduledStartAt: future });
  assert.equal(retainedSchedule.scheduled_start_at, future);

  const clearedSchedule = buildStreamSettingsPayload({ ...baseValues, scheduledStartAt: "" });
  assert.equal(clearedSchedule.scheduled_start_at, "", "an edit must send an explicit clear for a previously scheduled stream");
});

test("stream editing sends standard settings and deliberate clears without legacy archive fields", () => {
  const payload = buildStreamSettingsPayload({
    ...baseValues,
    name: "名称を変更",
    archiveProfileID: "",
    youtubeOutputID: "",
    watermarkEnabled: false,
    overlayProfileID: "overlay-logo",
  });

  assert.equal(payload.name, "名称を変更");
  assert.equal(payload.archive_profile_id, "");
  assert.equal(payload.youtube_output_id, "");
  assert.equal(payload.overlay_profile_id, "");
  for (const key of [
    "archive_oauth_account_id",
    "archive_folder_id",
    "archive_shared_drive",
    "archive_shared_drive_id",
    "archive_file_name",
    "archive_retention_days",
  ]) {
    assert.equal(key in payload, false, `${key} must be omitted by the standard edit form`);
  }
});

test("stream editing distinguishes an explicit Node clear from an omitted assignment", () => {
  const payload = buildStreamSettingsPayload({
    ...baseValues,
    encoderServiceID: "",
    workerServiceID: undefined,
  });

  assert.equal("encoder_service_id" in payload, true);
  assert.equal(payload.encoder_service_id, "");
  assert.equal("worker_service_id" in payload, false);
});

test("stream editing marks a Node owned by another stream busy and keeps the current owner selectable", () => {
  const busy = streamServiceAssignmentOption({ value: "encoder-01", label: "Encoder 01", currentStreamID: "stream-a" }, "stream-b");
  assert.equal(busy.disabled, true);
  assert.match(busy.label, /別の配信枠で使用中/);
  assert.match(busy.description || "", /割り当てを解除/);

  const current = streamServiceAssignmentOption({ value: "encoder-01", label: "Encoder 01", currentStreamID: "stream-a" }, "stream-a");
  assert.equal(current.disabled, false);
  assert.equal(current.label, "Encoder 01");

  const idle = streamServiceAssignmentOption({ value: "encoder-02", label: "Encoder 02" }, "stream-b");
  assert.equal(idle.disabled, false);
});

test("assignment conflicts have actionable messages without exposing identifiers", () => {
  for (const code of ["service_assignment_conflict", "service_assignment_protected_stream", "service_unassign_protected_stream"]) {
    const message = streamAssignmentConflictMessage(code);
    assert.ok(message, `${code} must have an actionable message`);
    assert.doesNotMatch(message || "", /stream-[a-z0-9]|encoder-[a-z0-9]|worker-[a-z0-9]/i);
  }
});

test("legacy create assignment fields have an actionable reload and edit message", () => {
  const message = streamCreateCompatibilityMessage("stream_create_assignment_fields_unsupported");
  assert.match(message || "", /最新画面へ再読み込み/);
  assert.match(message || "", /作成した後に編集画面でNodeを割り当て/);
  assert.equal(streamCreateCompatibilityMessage("service_assignment_conflict"), undefined);
});
