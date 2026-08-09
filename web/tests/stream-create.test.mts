import assert from "node:assert/strict";
import test from "node:test";

import { buildStreamCreatePayload, buildStreamSettingsPayload, type StreamCreateValues } from "../src/lib/stream-create.ts";

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
  encoderServiceID: "encoder-node-01",
  workerServiceID: "worker-node-01",
};

test("stream creation sends the selected recording profile", () => {
  const payload = buildStreamCreatePayload(baseValues);

  assert.equal(payload.archive_profile_id, "archive-shared-drive");
  assert.equal(payload.auto_start_trigger, "discord_voice_join");
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
