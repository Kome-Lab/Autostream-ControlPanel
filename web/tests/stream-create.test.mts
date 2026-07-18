import assert from "node:assert/strict";
import test from "node:test";

import { buildStreamCreatePayload, type StreamCreateValues } from "../src/lib/stream-create.ts";

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
